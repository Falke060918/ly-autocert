package api

import (
	"bytes"
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"
	"time"

	"github.com/Falke060918/INHERIT_AutoCert/backend/internal/audit"
	"github.com/Falke060918/INHERIT_AutoCert/backend/internal/auth"
	"github.com/Falke060918/INHERIT_AutoCert/backend/internal/db"
	"github.com/Falke060918/INHERIT_AutoCert/backend/internal/domain"
)

// 이슈 #6 완료 조건 검증: 권한 없는 역할이 보호된 API를 호출하면 403.
// TEST_DATABASE_URL 미설정 시 스킵한다 (전용 테스트 DB — 메인 DB에 돌리지 않는다).

// randomPassword는 테스트 전용 난수 비밀번호를 만든다 — 시크릿 하드코딩 금지.
func randomPassword(t *testing.T) string {
	t.Helper()
	b := make([]byte, 18)
	if _, err := rand.Read(b); err != nil {
		t.Fatalf("random password: %v", err)
	}
	return base64.RawURLEncoding.EncodeToString(b)
}

type testEnv struct {
	pool   *sql.DB
	svc    *auth.Service
	server *httptest.Server
}

func newTestEnv(t *testing.T) *testEnv {
	t.Helper()
	dsn := os.Getenv("TEST_DATABASE_URL")
	if dsn == "" {
		t.Skip("TEST_DATABASE_URL not set; skipping auth integration test")
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
	// 테스트 격리: 이전 실행 잔류 제거 (테스트 전용 DB에서만 허용되는 정리다).
	// audit_events는 append-only 트리거(0007)가 DELETE를 차단하므로, 테이블 소유자
	// 권한으로 트리거를 잠시 내리고 지운다 — 운영 DB에서는 절대 하지 않는 조작이다.
	for _, stmt := range []string{
		`DELETE FROM sessions`, `DELETE FROM users`,
		`ALTER TABLE audit_events DISABLE TRIGGER USER`,
		`DELETE FROM audit_events`,
		`ALTER TABLE audit_events ENABLE TRIGGER USER`,
	} {
		if _, err := pool.Exec(stmt); err != nil {
			t.Fatalf("cleanup %q: %v", stmt, err)
		}
	}
	rec := audit.NewRecorder(pool)
	svc := auth.NewServiceForTest(pool, time.Hour, rec)
	server := httptest.NewServer(NewServer(pool, svc, rec))
	t.Cleanup(server.Close)
	return &testEnv{pool: pool, svc: svc, server: server}
}

// do는 JSON 요청을 보낸다. token이 비어 있지 않으면 Bearer로 붙인다.
func (e *testEnv) do(t *testing.T, method, path, token string, body any) *http.Response {
	t.Helper()
	var buf bytes.Buffer
	if body != nil {
		if err := json.NewEncoder(&buf).Encode(body); err != nil {
			t.Fatalf("encode body: %v", err)
		}
	}
	req, err := http.NewRequest(method, e.server.URL+path, &buf)
	if err != nil {
		t.Fatalf("new request: %v", err)
	}
	req.Header.Set("Content-Type", "application/json")
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("%s %s: %v", method, path, err)
	}
	t.Cleanup(func() { resp.Body.Close() })
	return resp
}

func (e *testEnv) login(t *testing.T, username, password string) (token string, status int) {
	t.Helper()
	resp := e.do(t, http.MethodPost, "/api/v1/auth/login", "",
		map[string]string{"username": username, "password": password})
	if resp.StatusCode != http.StatusOK {
		return "", resp.StatusCode
	}
	var out struct {
		Token string `json:"token"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		t.Fatalf("decode login response: %v", err)
	}
	return out.Token, resp.StatusCode
}

// auditCount는 (event_type, result) 조합의 감사 이벤트 수를 센다.
func (e *testEnv) auditCount(t *testing.T, eventType string, result domain.AuditResult) int {
	t.Helper()
	var n int
	err := e.pool.QueryRow(
		`SELECT count(*) FROM audit_events WHERE event_type = $1 AND result = $2`,
		eventType, result).Scan(&n)
	if err != nil {
		t.Fatalf("audit count: %v", err)
	}
	return n
}

func TestLoginRBACAndAudit(t *testing.T) {
	env := newTestEnv(t)
	ctx := context.Background()

	adminPW := randomPassword(t)
	viewerPW := randomPassword(t)
	if _, err := env.svc.CreateUser(ctx, "it-admin", adminPW, nil, domain.RoleSystemAdministrator, nil, auth.AuditMeta{}); err != nil {
		t.Fatalf("create admin: %v", err)
	}
	if _, err := env.svc.CreateUser(ctx, "it-viewer", viewerPW, nil, domain.RoleViewer, nil, auth.AuditMeta{}); err != nil {
		t.Fatalf("create viewer: %v", err)
	}

	t.Run("login failure returns 401 and audits", func(t *testing.T) {
		if _, status := env.login(t, "it-admin", "wrong-"+randomPassword(t)); status != http.StatusUnauthorized {
			t.Errorf("wrong password login = %d, want 401", status)
		}
		if n := env.auditCount(t, "user.login", domain.AuditFailure); n != 1 {
			t.Errorf("user.login failure audit events = %d, want 1", n)
		}
	})

	t.Run("unknown user returns same 401", func(t *testing.T) {
		if _, status := env.login(t, "no-such-user", randomPassword(t)); status != http.StatusUnauthorized {
			t.Errorf("unknown user login = %d, want 401", status)
		}
	})

	adminToken, status := env.login(t, "it-admin", adminPW)
	if status != http.StatusOK || adminToken == "" {
		t.Fatalf("admin login = %d, token empty=%v; want 200 with token", status, adminToken == "")
	}
	viewerToken, status := env.login(t, "it-viewer", viewerPW)
	if status != http.StatusOK {
		t.Fatalf("viewer login = %d, want 200", status)
	}
	if n := env.auditCount(t, "user.login", domain.AuditSuccess); n != 2 {
		t.Errorf("user.login success audit events = %d, want 2", n)
	}

	t.Run("no token returns 401", func(t *testing.T) {
		if resp := env.do(t, http.MethodGet, "/api/v1/auth/me", "", nil); resp.StatusCode != http.StatusUnauthorized {
			t.Errorf("me without token = %d, want 401", resp.StatusCode)
		}
	})

	t.Run("me returns current user", func(t *testing.T) {
		resp := env.do(t, http.MethodGet, "/api/v1/auth/me", viewerToken, nil)
		if resp.StatusCode != http.StatusOK {
			t.Fatalf("me = %d, want 200", resp.StatusCode)
		}
		var u domain.User
		if err := json.NewDecoder(resp.Body).Decode(&u); err != nil {
			t.Fatalf("decode me: %v", err)
		}
		if u.Username != "it-viewer" || u.Role != domain.RoleViewer {
			t.Errorf("me = %s/%s, want it-viewer/viewer", u.Username, u.Role)
		}
	})

	t.Run("viewer is denied user management with 403", func(t *testing.T) {
		resp := env.do(t, http.MethodPost, "/api/v1/users", viewerToken,
			map[string]string{"username": "x", "password": randomPassword(t), "role": "viewer"})
		if resp.StatusCode != http.StatusForbidden {
			t.Errorf("viewer create user = %d, want 403", resp.StatusCode)
		}
		if resp := env.do(t, http.MethodGet, "/api/v1/users", viewerToken, nil); resp.StatusCode != http.StatusForbidden {
			t.Errorf("viewer list users = %d, want 403", resp.StatusCode)
		}
		if n := env.auditCount(t, "authz.denied", domain.AuditDenied); n != 2 {
			t.Errorf("authz.denied audit events = %d, want 2", n)
		}
	})

	t.Run("admin creates user; weak password rejected", func(t *testing.T) {
		resp := env.do(t, http.MethodPost, "/api/v1/users", adminToken,
			map[string]string{"username": "it-operator", "password": "short", "role": "operator"})
		if resp.StatusCode != http.StatusBadRequest {
			t.Errorf("weak password create = %d, want 400", resp.StatusCode)
		}
		resp = env.do(t, http.MethodPost, "/api/v1/users", adminToken,
			map[string]string{"username": "it-operator", "password": randomPassword(t), "role": "operator"})
		if resp.StatusCode != http.StatusCreated {
			t.Fatalf("admin create user = %d, want 201", resp.StatusCode)
		}
		// 3 = 셋업의 svc.CreateUser 2건 + API 생성 1건 — CreateUser가 서비스
		// 레벨에서 감사하므로 어떤 경로든 기록된다 (이슈 #7).
		if n := env.auditCount(t, "user.created", domain.AuditSuccess); n != 3 {
			t.Errorf("user.created audit events = %d, want 3", n)
		}
	})

	t.Run("invalid role rejected", func(t *testing.T) {
		resp := env.do(t, http.MethodPost, "/api/v1/users", adminToken,
			map[string]string{"username": "it-x", "password": randomPassword(t), "role": "superuser"})
		if resp.StatusCode != http.StatusBadRequest {
			t.Errorf("invalid role create = %d, want 400", resp.StatusCode)
		}
	})

	t.Run("logout revokes session and audits", func(t *testing.T) {
		if resp := env.do(t, http.MethodPost, "/api/v1/auth/logout", viewerToken, nil); resp.StatusCode != http.StatusNoContent {
			t.Fatalf("logout = %d, want 204", resp.StatusCode)
		}
		if resp := env.do(t, http.MethodGet, "/api/v1/auth/me", viewerToken, nil); resp.StatusCode != http.StatusUnauthorized {
			t.Errorf("me after logout = %d, want 401", resp.StatusCode)
		}
		if n := env.auditCount(t, "user.logout", domain.AuditSuccess); n != 1 {
			t.Errorf("user.logout audit events = %d, want 1", n)
		}
	})

	t.Run("responses carry request id", func(t *testing.T) {
		resp := env.do(t, http.MethodGet, "/healthz", "", nil)
		if resp.Header.Get("X-Request-ID") == "" {
			t.Error("X-Request-ID header missing")
		}
	})
}
