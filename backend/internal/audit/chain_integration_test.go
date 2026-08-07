package audit

// 이슈 #7 완료 조건 검증: 해시 체인 연결, append-only DB 차단, 변조 지점 특정,
// 동시 삽입 직렬화. TEST_DATABASE_URL 미설정 시 스킵한다 (전용 테스트 DB —
// 메인 DB에 돌리지 않는다). 픽스처는 합성 값만 쓴다 (보안 하드 규칙 4).

import (
	"context"
	"database/sql"
	"fmt"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/Falke060918/INHERIT_AutoCert/backend/internal/db"
	"github.com/Falke060918/INHERIT_AutoCert/backend/internal/domain"
)

const genesisHash = "0000000000000000000000000000000000000000000000000000000000000000"

func testPool(t *testing.T) *sql.DB {
	t.Helper()
	dsn := os.Getenv("TEST_DATABASE_URL")
	if dsn == "" {
		t.Skip("TEST_DATABASE_URL not set; skipping audit chain test")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	pool, err := db.Connect(ctx, dsn)
	if err != nil {
		t.Fatalf("connect: %v", err)
	}
	t.Cleanup(func() { pool.Close() })
	if err := db.Migrate(pool); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	return pool
}

// cleanAuditTable은 체인을 처음부터 다시 시작하도록 테이블을 비운다.
// append-only 트리거를 테이블 소유자 권한으로 잠시 내린다 — 테스트 전용 DB에서만
// 허용되는 조작이며, 운영에서는 절대 하지 않는다.
func cleanAuditTable(t *testing.T, pool *sql.DB) {
	t.Helper()
	for _, stmt := range []string{
		`ALTER TABLE audit_events DISABLE TRIGGER USER`,
		`DELETE FROM audit_events`,
		`ALTER TABLE audit_events ENABLE TRIGGER USER`,
	} {
		if _, err := pool.Exec(stmt); err != nil {
			t.Fatalf("clean audit table %q: %v", stmt, err)
		}
	}
}

// withTriggersDisabled는 슈퍼유저(트리거 우회) 변조를 흉내 낸다.
func withTriggersDisabled(t *testing.T, pool *sql.DB, fn func()) {
	t.Helper()
	if _, err := pool.Exec(`ALTER TABLE audit_events DISABLE TRIGGER USER`); err != nil {
		t.Fatalf("disable triggers: %v", err)
	}
	defer func() {
		if _, err := pool.Exec(`ALTER TABLE audit_events ENABLE TRIGGER USER`); err != nil {
			t.Fatalf("enable triggers: %v", err)
		}
	}()
	fn()
}

func recordN(t *testing.T, rec *Recorder, n int) {
	t.Helper()
	for i := 0; i < n; i++ {
		reqID := fmt.Sprintf("test-chain-%d", i)
		if err := rec.Record(context.Background(), Event{
			Type:       "test.chain",
			TargetType: "test",
			RequestID:  &reqID,
			After:      map[string]int{"seq": i},
			Result:     domain.AuditSuccess,
		}); err != nil {
			t.Fatalf("record %d: %v", i, err)
		}
	}
}

// TestHashChainLinksEvents는 각 이벤트의 prev_hash가 직전 이벤트의
// integrity_hash와 연결되고, 첫 이벤트는 genesis인지 검증한다.
func TestHashChainLinksEvents(t *testing.T) {
	pool := testPool(t)
	cleanAuditTable(t, pool)
	t.Cleanup(func() { cleanAuditTable(t, pool) })
	rec := NewRecorder(pool)

	recordN(t, rec, 3)

	rows, err := pool.Query(
		`SELECT integrity_hash, prev_hash FROM audit_events ORDER BY id`)
	if err != nil {
		t.Fatalf("read chain: %v", err)
	}
	defer rows.Close()
	prev := genesisHash
	i := 0
	for rows.Next() {
		var hash, prevHash string
		if err := rows.Scan(&hash, &prevHash); err != nil {
			t.Fatalf("scan: %v", err)
		}
		if prevHash != prev {
			t.Errorf("event %d: prev_hash = %q, want %q", i, prevHash, prev)
		}
		if len(hash) != 64 || strings.ToLower(hash) != hash {
			t.Errorf("event %d: integrity_hash %q is not lowercase hex 64", i, hash)
		}
		prev = hash
		i++
	}
	if i != 3 {
		t.Fatalf("chain length = %d, want 3", i)
	}

	res, err := rec.Verify(context.Background())
	if err != nil {
		t.Fatalf("verify: %v", err)
	}
	if !res.Valid || res.TotalEvents != 3 || len(res.Invalid) != 0 {
		t.Errorf("verify = valid=%v total=%d invalid=%d, want valid 3 0",
			res.Valid, res.TotalEvents, len(res.Invalid))
	}
	if res.HeadHash == nil || *res.HeadHash != prev {
		t.Errorf("head_hash mismatch: got %v, want %q", res.HeadHash, prev)
	}
}

// TestAppendOnlyBlockedAtDBLevel은 UPDATE/DELETE/TRUNCATE가 DB 트리거로
// 차단되는지 검증한다 (이월된 reviewer 지적 — append-only DB 레벨 강제).
func TestAppendOnlyBlockedAtDBLevel(t *testing.T) {
	pool := testPool(t)
	cleanAuditTable(t, pool)
	t.Cleanup(func() { cleanAuditTable(t, pool) })
	rec := NewRecorder(pool)
	recordN(t, rec, 1)

	for name, stmt := range map[string]string{
		"update":   `UPDATE audit_events SET event_type = 'tampered'`,
		"delete":   `DELETE FROM audit_events`,
		"truncate": `TRUNCATE audit_events`,
	} {
		_, err := pool.Exec(stmt)
		if err == nil {
			t.Errorf("%s succeeded; want append-only rejection", name)
		} else if !strings.Contains(err.Error(), "append-only") {
			t.Errorf("%s error = %v, want append-only message", name, err)
		}
	}
}

// TestVerifyPinpointsTampering은 트리거를 우회한(슈퍼유저) 수동 변조 후
// 검증이 변조 지점(id)을 특정하는지 검증한다.
func TestVerifyPinpointsTampering(t *testing.T) {
	pool := testPool(t)
	cleanAuditTable(t, pool)
	t.Cleanup(func() { cleanAuditTable(t, pool) })
	rec := NewRecorder(pool)
	ctx := context.Background()

	recordN(t, rec, 5)
	var ids []int64
	rows, err := pool.Query(`SELECT id FROM audit_events ORDER BY id`)
	if err != nil {
		t.Fatalf("read ids: %v", err)
	}
	for rows.Next() {
		var id int64
		if err := rows.Scan(&id); err != nil {
			t.Fatalf("scan id: %v", err)
		}
		ids = append(ids, id)
	}
	rows.Close()
	if len(ids) != 5 {
		t.Fatalf("events = %d, want 5", len(ids))
	}

	// 1) 내용 변조: 3번째 이벤트의 after_value 수정 → 해당 id의 hash_mismatch.
	withTriggersDisabled(t, pool, func() {
		if _, err := pool.Exec(
			`UPDATE audit_events SET after_value = '{"seq":999}'::jsonb WHERE id = $1`,
			ids[2]); err != nil {
			t.Fatalf("tamper update: %v", err)
		}
	})
	res, err := rec.Verify(ctx)
	if err != nil {
		t.Fatalf("verify after tamper: %v", err)
	}
	if res.Valid {
		t.Fatal("verify.Valid = true after tampering, want false")
	}
	if len(res.Invalid) != 1 || res.Invalid[0].ID != ids[2] || !res.Invalid[0].HashMismatch {
		t.Fatalf("invalid = %+v, want exactly [{id:%d hash_mismatch}]", res.Invalid, ids[2])
	}

	// 2) 중간 삭제: 2번째 이벤트 삭제 → 다음 이벤트(3번째)의 chain_broken.
	withTriggersDisabled(t, pool, func() {
		if _, err := pool.Exec(`DELETE FROM audit_events WHERE id = $1`, ids[1]); err != nil {
			t.Fatalf("tamper delete: %v", err)
		}
	})
	res, err = rec.Verify(ctx)
	if err != nil {
		t.Fatalf("verify after delete: %v", err)
	}
	if res.Valid {
		t.Fatal("verify.Valid = true after mid-chain delete, want false")
	}
	found := false
	for _, iv := range res.Invalid {
		if iv.ID == ids[2] && iv.ChainBroken {
			found = true
		}
	}
	if !found {
		t.Errorf("invalid = %+v, want chain_broken at id %d", res.Invalid, ids[2])
	}
}

// TestConcurrentInsertsSerializeChain은 동시 삽입 경합에서도 체인이 갈라지지
// 않는지 검증한다 (advisory lock 직렬화 + prev_hash UNIQUE).
func TestConcurrentInsertsSerializeChain(t *testing.T) {
	pool := testPool(t)
	cleanAuditTable(t, pool)
	t.Cleanup(func() { cleanAuditTable(t, pool) })
	rec := NewRecorder(pool)

	const n = 20
	var wg sync.WaitGroup
	errCh := make(chan error, n)
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			reqID := fmt.Sprintf("test-concurrent-%d", i)
			errCh <- rec.Record(context.Background(), Event{
				Type:       "test.concurrent",
				TargetType: "test",
				RequestID:  &reqID,
				Result:     domain.AuditSuccess,
			})
		}(i)
	}
	wg.Wait()
	close(errCh)
	for err := range errCh {
		if err != nil {
			t.Fatalf("concurrent record: %v", err)
		}
	}

	var total, distinctPrev int
	if err := pool.QueryRow(
		`SELECT count(*), count(DISTINCT prev_hash) FROM audit_events`).
		Scan(&total, &distinctPrev); err != nil {
		t.Fatalf("count: %v", err)
	}
	if total != n || distinctPrev != n {
		t.Errorf("events=%d distinct prev_hash=%d, want %d %d (체인 분기 없음)", total, distinctPrev, n, n)
	}
	res, err := rec.Verify(context.Background())
	if err != nil {
		t.Fatalf("verify: %v", err)
	}
	if !res.Valid {
		t.Errorf("verify.Valid = false after concurrent inserts: %+v", res.Invalid)
	}
}
