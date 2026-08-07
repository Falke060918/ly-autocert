package auth

// 이슈 #7(이월된 reviewer 지적) 검증: 사용자 생성·로그인 세션·로그아웃의
// 변경+감사 기록이 단일 트랜잭션이어야 한다 — 감사 실패 시 변경도 롤백된다
// (보안 하드 규칙 5의 데이터 레벨 충족).
// TEST_DATABASE_URL 미설정 시 스킵한다 (전용 테스트 DB).
// 비밀번호는 난수 생성만 쓴다 — 시크릿 하드코딩 금지 (보안 하드 규칙 4).

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/base64"
	"os"
	"testing"
	"time"

	"github.com/Falke060918/INHERIT_AutoCert/backend/internal/audit"
	"github.com/Falke060918/INHERIT_AutoCert/backend/internal/db"
	"github.com/Falke060918/INHERIT_AutoCert/backend/internal/domain"
)

func testPool(t *testing.T) *sql.DB {
	t.Helper()
	dsn := os.Getenv("TEST_DATABASE_URL")
	if dsn == "" {
		t.Skip("TEST_DATABASE_URL not set; skipping auth atomicity test")
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

func randomPassword(t *testing.T) string {
	t.Helper()
	b := make([]byte, 18)
	if _, err := rand.Read(b); err != nil {
		t.Fatalf("random password: %v", err)
	}
	return base64.RawURLEncoding.EncodeToString(b)
}

func countRow(t *testing.T, pool *sql.DB, query string, args ...any) int {
	t.Helper()
	var n int
	if err := pool.QueryRow(query, args...).Scan(&n); err != nil {
		t.Fatalf("count %q: %v", query, err)
	}
	return n
}

// TestChangeAndAuditAreAtomic은 감사 기록이 실패하면(테이블 이름 변경으로 유도)
// 사용자 생성·세션 생성도 롤백되는지 검증한다.
func TestChangeAndAuditAreAtomic(t *testing.T) {
	pool := testPool(t)
	ctx := context.Background()
	t.Cleanup(func() {
		// 테스트 사용자·세션 정리 (감사 이벤트는 append-only라 남긴다).
		_, _ = pool.Exec(`DELETE FROM sessions WHERE user_id IN (SELECT id FROM users WHERE username LIKE 'atom-%')`)
		_, _ = pool.Exec(`DELETE FROM users WHERE username LIKE 'atom-%'`)
	})

	rec := audit.NewRecorder(pool)
	svc := NewServiceForTest(pool, time.Hour, rec)

	adminPW := randomPassword(t)
	admin, err := svc.CreateUser(ctx, "atom-admin", adminPW, nil,
		domain.RoleSystemAdministrator, nil, AuditMeta{})
	if err != nil {
		t.Fatalf("create admin: %v", err)
	}
	if n := countRow(t, pool,
		`SELECT count(*) FROM audit_events WHERE event_type = 'user.created' AND target_id = $1`,
		admin.ID); n != 1 {
		t.Fatalf("user.created audit events = %d, want 1", n)
	}

	// 감사 기록을 강제로 실패시킨다: 감사 테이블을 잠시 숨긴다 (테스트 전용 DB).
	if _, err := pool.Exec(`ALTER TABLE audit_events RENAME TO audit_events_atomtest`); err != nil {
		t.Fatalf("hide audit table: %v", err)
	}
	hidden := true
	restore := func() {
		if hidden {
			if _, err := pool.Exec(`ALTER TABLE audit_events_atomtest RENAME TO audit_events`); err != nil {
				t.Fatalf("restore audit table: %v", err)
			}
			hidden = false
		}
	}
	t.Cleanup(restore)

	t.Run("audit failure rolls back user creation", func(t *testing.T) {
		if _, err := svc.CreateUser(ctx, "atom-user2", randomPassword(t), nil,
			domain.RoleViewer, &admin.ID, AuditMeta{}); err == nil {
			t.Fatal("create user succeeded without audit; want error")
		}
		if n := countRow(t, pool,
			`SELECT count(*) FROM users WHERE username = 'atom-user2'`); n != 0 {
			t.Errorf("user rows after failed audit = %d, want 0 (rollback)", n)
		}
	})

	t.Run("audit failure rolls back login session", func(t *testing.T) {
		if _, _, _, err := svc.Login(ctx, "atom-admin", adminPW, AuditMeta{}); err == nil {
			t.Fatal("login succeeded without audit; want error")
		}
		if n := countRow(t, pool,
			`SELECT count(*) FROM sessions WHERE user_id = $1`, admin.ID); n != 0 {
			t.Errorf("sessions after failed audit = %d, want 0 (rollback)", n)
		}
	})

	restore()

	t.Run("with audit restored, change and audit both persist", func(t *testing.T) {
		_, token, _, err := svc.Login(ctx, "atom-admin", adminPW, AuditMeta{})
		if err != nil {
			t.Fatalf("login: %v", err)
		}
		if n := countRow(t, pool,
			`SELECT count(*) FROM sessions WHERE user_id = $1`, admin.ID); n != 1 {
			t.Errorf("sessions = %d, want 1", n)
		}
		if n := countRow(t, pool,
			`SELECT count(*) FROM audit_events WHERE event_type = 'user.login' AND result = 'success' AND actor_id = $1::uuid`,
			admin.ID); n != 1 {
			t.Errorf("user.login audit events = %d, want 1", n)
		}
		if err := svc.Logout(ctx, token, admin.ID, AuditMeta{}); err != nil {
			t.Fatalf("logout: %v", err)
		}
		if n := countRow(t, pool,
			`SELECT count(*) FROM audit_events WHERE event_type = 'user.logout' AND actor_id = $1::uuid`,
			admin.ID); n != 1 {
			t.Errorf("user.logout audit events = %d, want 1", n)
		}
	})
}
