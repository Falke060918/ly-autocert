package store

// 센서(Phase 3a U2) store 통합 테스트. 픽스처(토큰·fingerprint·CA 바이트)는
// 전부 실행 시점에 생성한다 — 실제 키·인증서·토큰을 커밋하지 않는다 (보안 하드 규칙 4).
// 토큰 평문은 단언 외 어디에도 출력하지 않는다.

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"errors"
	"fmt"
	"regexp"
	"sync"
	"testing"
	"time"
)

var hex64Re = regexp.MustCompile(`^[0-9a-f]{64}$`)

// newSensorFixtureName은 테스트 격리용 유일 센서 이름을 만든다.
func newSensorFixtureName(t *testing.T) string {
	t.Helper()
	return fmt.Sprintf("u2-sensor-%s-%d", t.Name(), time.Now().UnixNano())
}

// newTestFingerprint는 클라이언트 인증서 fingerprint 자리의 hex 64자 픽스처다
// (실제 인증서를 만들지 않는다 — 형식만 대조 대상이다).
func newTestFingerprint(t *testing.T) string {
	t.Helper()
	buf := make([]byte, 32)
	if _, err := rand.Read(buf); err != nil {
		t.Fatalf("generate fingerprint fixture: %v", err)
	}
	return hex.EncodeToString(buf)
}

// cleanupSensorsByName은 테스트가 만든 센서 행을 정리한다 (best-effort).
func cleanupSensorsByName(pool *sql.DB, name string) {
	_, _ = pool.Exec(`DELETE FROM sensors WHERE name = $1`, name)
}

// countAudit은 request_id + event_type + result로 감사 이벤트 수를 센다.
func countAudit(t *testing.T, pool *sql.DB, reqID, eventType, result string) int {
	t.Helper()
	var n int
	if err := pool.QueryRow(`
		SELECT count(*) FROM audit_events
		 WHERE request_id = $1 AND event_type = $2 AND result = $3`,
		reqID, eventType, result).Scan(&n); err != nil {
		t.Fatalf("count audit events: %v", err)
	}
	return n
}

func TestCreateSensorIssuesOneTimeToken(t *testing.T) {
	pool := testPool(t)
	st := New(pool)
	ctx := context.Background()

	name := newSensorFixtureName(t)
	reqID := "u2-req-create-" + name
	t.Cleanup(func() {
		cleanupSensorsByName(pool, name)
		cleanupAuditByRequestID(pool, reqID)
	})

	before := time.Now()
	sn, token, err := st.CreateSensor(ctx, CreateSensorInput{Name: name, RequestID: &reqID})
	if err != nil {
		t.Fatalf("create sensor: %v", err)
	}

	if !hex64Re.MatchString(token) {
		t.Errorf("token format: want 64-char lowercase hex, got %d chars", len(token))
	}
	if sn.Status != "pending" {
		t.Errorf("status = %q, want pending", sn.Status)
	}
	if sn.ClientCertFingerprintSHA256 != nil || sn.RegisteredAt != nil || sn.DisabledAt != nil {
		t.Error("new sensor: fingerprint/registered_at/disabled_at must be nil")
	}
	// 만료는 24h 뒤여야 한다 (허용 오차 ±5m).
	wantExp := before.Add(24 * time.Hour)
	if d := sn.TokenExpiresAt.Sub(wantExp); d < -5*time.Minute || d > 5*time.Minute {
		t.Errorf("token_expires_at = %v, want ~%v", sn.TokenExpiresAt, wantExp)
	}

	// DB에는 토큰 평문이 아니라 SHA-256 해시(hex)만 저장된다.
	var storedHash string
	if err := pool.QueryRow(
		`SELECT enrollment_token_hash FROM sensors WHERE id = $1::uuid`, sn.ID).
		Scan(&storedHash); err != nil {
		t.Fatalf("read token hash: %v", err)
	}
	sum := sha256.Sum256([]byte(token))
	if storedHash != hex.EncodeToString(sum[:]) {
		t.Error("stored hash != sha256(token)")
	}
	if storedHash == token {
		t.Error("token plaintext stored in DB")
	}

	// 감사 sensor.token.issue 1건 — after_value에 토큰 평문·해시가 없어야 한다.
	if n := countAudit(t, pool, reqID, EventSensorTokenIssue, "success"); n != 1 {
		t.Errorf("sensor.token.issue audit count = %d, want 1", n)
	}
	var leaked int
	if err := pool.QueryRow(`
		SELECT count(*) FROM audit_events
		 WHERE request_id = $1
		   AND (position($2 in after_value::text) > 0 OR position($3 in after_value::text) > 0)`,
		reqID, token, storedHash).Scan(&leaked); err != nil {
		t.Fatalf("check audit leak: %v", err)
	}
	if leaked != 0 {
		t.Error("audit after_value contains token plaintext or hash")
	}
}

func TestEnrollSensorConsumesTokenAtomically(t *testing.T) {
	pool := testPool(t)
	st := New(pool)
	ctx := context.Background()

	name := newSensorFixtureName(t)
	reqID := "u2-req-enroll-" + name
	t.Cleanup(func() {
		cleanupSensorsByName(pool, name)
		cleanupAuditByRequestID(pool, reqID)
	})

	_, token, err := st.CreateSensor(ctx, CreateSensorInput{Name: name, RequestID: &reqID})
	if err != nil {
		t.Fatalf("create sensor: %v", err)
	}

	// 같은 토큰의 동시 등록 — 정확히 1건만 성공해야 한다 (원자 소진).
	// fingerprint 픽스처는 테스트 고루틴에서 미리 만든다 (t.Fatal은 고루틴 밖에서만).
	const attempts = 5
	fps := make([]string, attempts)
	for i := range fps {
		fps[i] = newTestFingerprint(t)
	}
	var wg sync.WaitGroup
	errCh := make(chan error, attempts)
	for i := 0; i < attempts; i++ {
		wg.Add(1)
		go func(fp string) {
			defer wg.Done()
			_, err := st.EnrollSensor(ctx, EnrollSensorInput{
				Token:                       token,
				ClientCertFingerprintSHA256: fp,
				RequestID:                   &reqID,
			})
			errCh <- err
		}(fps[i])
	}
	wg.Wait()
	close(errCh)
	success, invalid := 0, 0
	for err := range errCh {
		switch {
		case err == nil:
			success++
		case errors.Is(err, ErrEnrollTokenInvalid):
			invalid++
		default:
			t.Errorf("enroll: unexpected error: %v", err)
		}
	}
	if success != 1 || invalid != attempts-1 {
		t.Errorf("concurrent enroll: success=%d invalid=%d, want 1/%d", success, invalid, attempts-1)
	}

	// 성공한 등록의 상태 전이 확인.
	var status string
	var fp *string
	var registeredAt *time.Time
	if err := pool.QueryRow(`
		SELECT status, client_cert_fingerprint_sha256, registered_at
		  FROM sensors WHERE name = $1`, name).
		Scan(&status, &fp, &registeredAt); err != nil {
		t.Fatalf("read sensor: %v", err)
	}
	if status != "active" || fp == nil || registeredAt == nil {
		t.Errorf("after enroll: status=%q fp set=%v registered set=%v, want active/true/true",
			status, fp != nil, registeredAt != nil)
	}

	// 실패도 전건 감사 (sensor.enroll, failure).
	if n := countAudit(t, pool, reqID, EventSensorEnroll, "success"); n != 1 {
		t.Errorf("sensor.enroll success audit = %d, want 1", n)
	}
	if n := countAudit(t, pool, reqID, EventSensorEnroll, "failure"); n != attempts-1 {
		t.Errorf("sensor.enroll failure audit = %d, want %d", n, attempts-1)
	}

	// 존재하지 않는 토큰도 동일한 오류다.
	if _, err := st.EnrollSensor(ctx, EnrollSensorInput{
		Token:                       "no-such-token",
		ClientCertFingerprintSHA256: newTestFingerprint(t),
		RequestID:                   &reqID,
	}); !errors.Is(err, ErrEnrollTokenInvalid) {
		t.Errorf("unknown token: err = %v, want ErrEnrollTokenInvalid", err)
	}
}

func TestEnrollSensorRejectsExpiredToken(t *testing.T) {
	pool := testPool(t)
	st := New(pool)
	ctx := context.Background()

	name := newSensorFixtureName(t)
	reqID := "u2-req-expired-" + name
	t.Cleanup(func() {
		cleanupSensorsByName(pool, name)
		cleanupAuditByRequestID(pool, reqID)
	})

	sn, token, err := st.CreateSensor(ctx, CreateSensorInput{Name: name, RequestID: &reqID})
	if err != nil {
		t.Fatalf("create sensor: %v", err)
	}
	if _, err := pool.Exec(
		`UPDATE sensors SET token_expires_at = now() - interval '1 minute' WHERE id = $1::uuid`,
		sn.ID); err != nil {
		t.Fatalf("expire token: %v", err)
	}

	if _, err := st.EnrollSensor(ctx, EnrollSensorInput{
		Token:                       token,
		ClientCertFingerprintSHA256: newTestFingerprint(t),
		RequestID:                   &reqID,
	}); !errors.Is(err, ErrEnrollTokenInvalid) {
		t.Errorf("expired token: err = %v, want ErrEnrollTokenInvalid", err)
	}

	// 만료 토큰은 소진되지 않는다 — pending 그대로.
	var status string
	if err := pool.QueryRow(`SELECT status FROM sensors WHERE id = $1::uuid`, sn.ID).
		Scan(&status); err != nil {
		t.Fatalf("read status: %v", err)
	}
	if status != "pending" {
		t.Errorf("status = %q, want pending", status)
	}
}

// enrollTestSensor는 생성+등록까지 끝낸 active 센서 픽스처를 만든다.
func enrollTestSensor(t *testing.T, st *Store, pool *sql.DB, name, reqID string) (sensorID, fingerprint string) {
	t.Helper()
	ctx := context.Background()
	_, token, err := st.CreateSensor(ctx, CreateSensorInput{Name: name, RequestID: &reqID})
	if err != nil {
		t.Fatalf("create sensor: %v", err)
	}
	fp := newTestFingerprint(t)
	sn, err := st.EnrollSensor(ctx, EnrollSensorInput{
		Token: token, ClientCertFingerprintSHA256: fp, RequestID: &reqID,
	})
	if err != nil {
		t.Fatalf("enroll sensor: %v", err)
	}
	return sn.ID, fp
}

func TestGetActiveSensorByFingerprintAndTouch(t *testing.T) {
	pool := testPool(t)
	st := New(pool)
	ctx := context.Background()

	name := newSensorFixtureName(t)
	reqID := "u2-req-mtls-" + name
	t.Cleanup(func() {
		cleanupSensorsByName(pool, name)
		cleanupAuditByRequestID(pool, reqID)
	})

	id, fp := enrollTestSensor(t, st, pool, name, reqID)

	sn, err := st.GetActiveSensorByFingerprint(ctx, fp)
	if err != nil {
		t.Fatalf("get by fingerprint: %v", err)
	}
	if sn.ID != id {
		t.Errorf("sensor id = %q, want %q", sn.ID, id)
	}
	if sn.LastSeenAt != nil {
		t.Error("last_seen_at set before first touch")
	}

	if err := st.TouchSensorLastSeen(ctx, id); err != nil {
		t.Fatalf("touch last seen: %v", err)
	}
	sn, err = st.GetActiveSensorByFingerprint(ctx, fp)
	if err != nil {
		t.Fatalf("get by fingerprint after touch: %v", err)
	}
	if sn.LastSeenAt == nil {
		t.Error("last_seen_at not updated by touch")
	}

	if _, err := st.GetActiveSensorByFingerprint(ctx, newTestFingerprint(t)); !errors.Is(err, ErrSensorNotFound) {
		t.Errorf("unknown fingerprint: err = %v, want ErrSensorNotFound", err)
	}
}

func TestRenewSensorFingerprint(t *testing.T) {
	pool := testPool(t)
	st := New(pool)
	ctx := context.Background()

	name := newSensorFixtureName(t)
	reqID := "u2-req-renew-" + name
	t.Cleanup(func() {
		cleanupSensorsByName(pool, name)
		cleanupAuditByRequestID(pool, reqID)
	})

	id, oldFP := enrollTestSensor(t, st, pool, name, reqID)
	newFP := newTestFingerprint(t)

	sn, err := st.RenewSensorFingerprint(ctx, RenewSensorInput{
		OldFingerprint: oldFP, NewFingerprint: newFP, RequestID: &reqID,
	})
	if err != nil {
		t.Fatalf("renew fingerprint: %v", err)
	}
	if sn.ID != id {
		t.Errorf("sensor id = %q, want %q", sn.ID, id)
	}

	// 새 fingerprint로만 조회된다.
	if _, err := st.GetActiveSensorByFingerprint(ctx, newFP); err != nil {
		t.Errorf("get by new fingerprint: %v", err)
	}
	if _, err := st.GetActiveSensorByFingerprint(ctx, oldFP); !errors.Is(err, ErrSensorNotFound) {
		t.Errorf("old fingerprint still resolves: err = %v, want ErrSensorNotFound", err)
	}

	// 이미 교체된 fingerprint로의 재시도는 실패한다 (원자 교체).
	if _, err := st.RenewSensorFingerprint(ctx, RenewSensorInput{
		OldFingerprint: oldFP, NewFingerprint: newTestFingerprint(t), RequestID: &reqID,
	}); !errors.Is(err, ErrSensorNotFound) {
		t.Errorf("stale renew: err = %v, want ErrSensorNotFound", err)
	}

	if n := countAudit(t, pool, reqID, EventSensorRenew, "success"); n != 1 {
		t.Errorf("sensor.renew audit = %d, want 1", n)
	}
}

func TestDisableSensor(t *testing.T) {
	pool := testPool(t)
	st := New(pool)
	ctx := context.Background()

	name := newSensorFixtureName(t)
	reqID := "u2-req-disable-" + name
	t.Cleanup(func() {
		cleanupSensorsByName(pool, name)
		cleanupAuditByRequestID(pool, reqID)
	})

	id, fp := enrollTestSensor(t, st, pool, name, reqID)

	sn, err := st.DisableSensor(ctx, DisableSensorInput{SensorID: id, RequestID: &reqID})
	if err != nil {
		t.Fatalf("disable sensor: %v", err)
	}
	if sn.Status != "disabled" || sn.DisabledAt == nil {
		t.Errorf("after disable: status=%q disabled_at set=%v, want disabled/true", sn.Status, sn.DisabledAt != nil)
	}

	// 비활성 센서는 mTLS 대조에서 제외된다.
	if _, err := st.GetActiveSensorByFingerprint(ctx, fp); !errors.Is(err, ErrSensorNotFound) {
		t.Errorf("disabled sensor resolves by fingerprint: err = %v, want ErrSensorNotFound", err)
	}

	// 중복 비활성화는 ErrSensorNotFound (감사 중복 방지).
	if _, err := st.DisableSensor(ctx, DisableSensorInput{SensorID: id, RequestID: &reqID}); !errors.Is(err, ErrSensorNotFound) {
		t.Errorf("double disable: err = %v, want ErrSensorNotFound", err)
	}
	if n := countAudit(t, pool, reqID, EventSensorDisable, "success"); n != 1 {
		t.Errorf("sensor.disable audit = %d, want 1", n)
	}
}

func TestDisablePendingSensorRevokesToken(t *testing.T) {
	pool := testPool(t)
	st := New(pool)
	ctx := context.Background()

	name := newSensorFixtureName(t)
	reqID := "u2-req-revoke-" + name
	t.Cleanup(func() {
		cleanupSensorsByName(pool, name)
		cleanupAuditByRequestID(pool, reqID)
	})

	sn, token, err := st.CreateSensor(ctx, CreateSensorInput{Name: name, RequestID: &reqID})
	if err != nil {
		t.Fatalf("create sensor: %v", err)
	}
	if _, err := st.DisableSensor(ctx, DisableSensorInput{SensorID: sn.ID, RequestID: &reqID}); err != nil {
		t.Fatalf("disable pending sensor: %v", err)
	}

	// 비활성화된 pending 센서의 토큰은 더 이상 등록에 쓸 수 없다.
	if _, err := st.EnrollSensor(ctx, EnrollSensorInput{
		Token:                       token,
		ClientCertFingerprintSHA256: newTestFingerprint(t),
		RequestID:                   &reqID,
	}); !errors.Is(err, ErrEnrollTokenInvalid) {
		t.Errorf("enroll after disable: err = %v, want ErrEnrollTokenInvalid", err)
	}
}

func TestListAndGetSensors(t *testing.T) {
	pool := testPool(t)
	st := New(pool)
	ctx := context.Background()

	nameA := newSensorFixtureName(t) + "-a"
	nameB := newSensorFixtureName(t) + "-b"
	reqID := "u2-req-list-" + nameA
	t.Cleanup(func() {
		cleanupSensorsByName(pool, nameA)
		cleanupSensorsByName(pool, nameB)
		cleanupAuditByRequestID(pool, reqID)
	})

	snA, _, err := st.CreateSensor(ctx, CreateSensorInput{Name: nameA, RequestID: &reqID})
	if err != nil {
		t.Fatalf("create sensor a: %v", err)
	}
	if _, _, err := st.CreateSensor(ctx, CreateSensorInput{Name: nameB, RequestID: &reqID}); err != nil {
		t.Fatalf("create sensor b: %v", err)
	}

	list, err := st.ListSensors(ctx)
	if err != nil {
		t.Fatalf("list sensors: %v", err)
	}
	found := map[string]bool{}
	for _, sn := range list {
		found[sn.Name] = true
	}
	if !found[nameA] || !found[nameB] {
		t.Errorf("list missing fixtures: a=%v b=%v", found[nameA], found[nameB])
	}

	got, err := st.GetSensor(ctx, snA.ID)
	if err != nil {
		t.Fatalf("get sensor: %v", err)
	}
	if got.Name != nameA {
		t.Errorf("get sensor name = %q, want %q", got.Name, nameA)
	}

	if _, err := st.GetSensor(ctx, "00000000-0000-0000-0000-000000000000"); !errors.Is(err, ErrSensorNotFound) {
		t.Errorf("unknown id: err = %v, want ErrSensorNotFound", err)
	}
}

func TestSensorCASaveLoadSingleRow(t *testing.T) {
	pool := testPool(t)
	st := New(pool)
	ctx := context.Background()

	// sensor_ca는 단일 행 테이블 — 테스트 전후로 비운다 (전용 테스트 DB 전제).
	clear := func() { _, _ = pool.Exec(`DELETE FROM sensor_ca`) }
	clear()
	t.Cleanup(clear)

	if _, err := st.LoadSensorCA(ctx); !errors.Is(err, ErrSensorCANotFound) {
		t.Fatalf("empty load: err = %v, want ErrSensorCANotFound", err)
	}

	// 암호문·nonce 자리의 무작위 바이트 픽스처 (실키 아님 — 저장/로드 왕복만 검증).
	rec := SensorCARecord{
		CertPEM:       []byte("-----BEGIN CERTIFICATE-----\nu2-fixture\n-----END CERTIFICATE-----\n"),
		KeyCiphertext: make([]byte, 48),
		KeyNonce:      make([]byte, 12),
	}
	if _, err := rand.Read(rec.KeyCiphertext); err != nil {
		t.Fatalf("fixture ciphertext: %v", err)
	}
	if _, err := rand.Read(rec.KeyNonce); err != nil {
		t.Fatalf("fixture nonce: %v", err)
	}

	if err := st.SaveSensorCA(ctx, rec); err != nil {
		t.Fatalf("save sensor ca: %v", err)
	}
	got, err := st.LoadSensorCA(ctx)
	if err != nil {
		t.Fatalf("load sensor ca: %v", err)
	}
	if !bytes.Equal(got.CertPEM, rec.CertPEM) ||
		!bytes.Equal(got.KeyCiphertext, rec.KeyCiphertext) ||
		!bytes.Equal(got.KeyNonce, rec.KeyNonce) {
		t.Error("loaded sensor ca record != saved record")
	}
	if got.CreatedAt.IsZero() {
		t.Error("created_at not set")
	}

	// 단일 행 강제 — 두 번째 저장은 ErrSensorCAExists.
	if err := st.SaveSensorCA(ctx, rec); !errors.Is(err, ErrSensorCAExists) {
		t.Errorf("second save: err = %v, want ErrSensorCAExists", err)
	}

	// 불완전 레코드는 거부한다.
	if err := st.SaveSensorCA(ctx, SensorCARecord{CertPEM: rec.CertPEM}); err == nil {
		t.Error("incomplete record accepted")
	}
}
