package api

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"testing"

	"github.com/Falke060918/INHERIT_AutoCert/backend/internal/auth"
	"github.com/Falke060918/INHERIT_AutoCert/backend/internal/domain"
)

// 사용자 관리 API 검증: 수정·활성 토글·비밀번호 재설정의 성공 경로,
// 비관리자 거부(403), 락아웃 방지 거부, 감사 이벤트 기록, 세션 즉시 폐기.
// TEST_DATABASE_URL 미설정 시 스킵한다 (전용 테스트 DB).
// 비밀번호는 난수만 쓴다 — 시크릿 하드코딩 금지 (보안 하드 규칙 4).

// errorCode는 오류 응답 본문의 error.code를 뽑는다.
func errorCode(t *testing.T, resp *http.Response) string {
	t.Helper()
	var body errorResponse
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		t.Fatalf("decode error body: %v", err)
	}
	return body.Error.Code
}

// decodeUser는 사용자 응답 본문을 디코드한다.
func decodeUser(t *testing.T, resp *http.Response) domain.User {
	t.Helper()
	var u domain.User
	if err := json.NewDecoder(resp.Body).Decode(&u); err != nil {
		t.Fatalf("decode user: %v", err)
	}
	return u
}

// auditCountFor는 (event_type, target_id, actor_id) 조합의 감사 이벤트 수를 센다.
func (e *testEnv) auditCountFor(t *testing.T, eventType, targetID, actorID string) int {
	t.Helper()
	var n int
	err := e.pool.QueryRow(
		`SELECT count(*) FROM audit_events
		  WHERE event_type = $1 AND target_id = $2 AND actor_id = $3::uuid AND result = 'success'`,
		eventType, targetID, actorID).Scan(&n)
	if err != nil {
		t.Fatalf("audit count for %s: %v", eventType, err)
	}
	return n
}

func TestUserManagementAPI(t *testing.T) {
	env := newTestEnv(t)
	ctx := context.Background()

	adminPW, viewerPW, opPW := randomPassword(t), randomPassword(t), randomPassword(t)
	admin, err := env.svc.CreateUser(ctx, "um-admin", adminPW, nil, domain.RoleSystemAdministrator, nil, auth.AuditMeta{})
	if err != nil {
		t.Fatalf("create admin: %v", err)
	}
	viewer, err := env.svc.CreateUser(ctx, "um-viewer", viewerPW, nil, domain.RoleViewer, nil, auth.AuditMeta{})
	if err != nil {
		t.Fatalf("create viewer: %v", err)
	}
	operator, err := env.svc.CreateUser(ctx, "um-operator", opPW, nil, domain.RoleOperator, nil, auth.AuditMeta{})
	if err != nil {
		t.Fatalf("create operator: %v", err)
	}

	adminToken, status := env.login(t, "um-admin", adminPW)
	if status != http.StatusOK {
		t.Fatalf("admin login = %d, want 200", status)
	}
	viewerToken, status := env.login(t, "um-viewer", viewerPW)
	if status != http.StatusOK {
		t.Fatalf("viewer login = %d, want 200", status)
	}

	t.Run("non-admin is denied on every mutation", func(t *testing.T) {
		cases := []struct {
			method, path string
			body         any
		}{
			{http.MethodPatch, "/api/v1/users/" + operator.ID, map[string]string{"role": "viewer"}},
			{http.MethodPost, "/api/v1/users/" + operator.ID + "/disable", nil},
			{http.MethodPost, "/api/v1/users/" + operator.ID + "/enable", nil},
			{http.MethodPost, "/api/v1/users/" + operator.ID + "/password",
				map[string]string{"password": randomPassword(t)}},
		}
		for _, c := range cases {
			resp := env.do(t, c.method, c.path, viewerToken, c.body)
			if resp.StatusCode != http.StatusForbidden {
				t.Errorf("viewer %s %s = %d, want 403", c.method, c.path, resp.StatusCode)
			}
		}
		// 대상 사용자는 그대로여야 한다 (거부는 변경을 남기지 않는다).
		if u, err := env.svc.ListUsers(ctx); err != nil {
			t.Fatalf("list users: %v", err)
		} else {
			for _, x := range u {
				if x.ID == operator.ID && (x.Role != domain.RoleOperator || !x.IsActive) {
					t.Errorf("operator changed after denied calls: role=%s active=%v", x.Role, x.IsActive)
				}
			}
		}
	})

	t.Run("patch updates role and display name with audit", func(t *testing.T) {
		resp := env.do(t, http.MethodPatch, "/api/v1/users/"+operator.ID, adminToken,
			map[string]any{"role": "pki_administrator", "display_name": "PKI 담당"})
		if resp.StatusCode != http.StatusOK {
			t.Fatalf("patch user = %d, want 200", resp.StatusCode)
		}
		got := decodeUser(t, resp)
		if got.Role != domain.RolePKIAdministrator {
			t.Errorf("role = %s, want pki_administrator", got.Role)
		}
		if got.DisplayName == nil || *got.DisplayName != "PKI 담당" {
			t.Errorf("display_name = %v, want %q", got.DisplayName, "PKI 담당")
		}
		if n := env.auditCountFor(t, "user.update", operator.ID, admin.ID); n != 1 {
			t.Errorf("user.update audit events = %d, want 1", n)
		}
	})

	t.Run("patch with only display_name keeps role; null clears it", func(t *testing.T) {
		resp := env.do(t, http.MethodPatch, "/api/v1/users/"+operator.ID, adminToken,
			map[string]any{"display_name": nil})
		if resp.StatusCode != http.StatusOK {
			t.Fatalf("patch display_name null = %d, want 200", resp.StatusCode)
		}
		got := decodeUser(t, resp)
		if got.DisplayName != nil {
			t.Errorf("display_name = %v, want nil", *got.DisplayName)
		}
		if got.Role != domain.RolePKIAdministrator {
			t.Errorf("role = %s, want pki_administrator (unchanged)", got.Role)
		}
	})

	t.Run("patch rejects empty body, invalid role, unknown user", func(t *testing.T) {
		resp := env.do(t, http.MethodPatch, "/api/v1/users/"+operator.ID, adminToken, map[string]any{})
		if resp.StatusCode != http.StatusBadRequest {
			t.Errorf("empty patch = %d, want 400", resp.StatusCode)
		} else if code := errorCode(t, resp); code != "no_fields" {
			t.Errorf("empty patch code = %q, want no_fields", code)
		}
		resp = env.do(t, http.MethodPatch, "/api/v1/users/"+operator.ID, adminToken,
			map[string]any{"role": "superuser"})
		if resp.StatusCode != http.StatusBadRequest {
			t.Errorf("invalid role patch = %d, want 400", resp.StatusCode)
		}
		// 존재하지 않는 UUID와 UUID가 아닌 경로값 모두 404다.
		resp = env.do(t, http.MethodPatch, "/api/v1/users/00000000-0000-4000-8000-000000000000",
			adminToken, map[string]any{"role": "viewer"})
		if resp.StatusCode != http.StatusNotFound {
			t.Errorf("unknown user patch = %d, want 404", resp.StatusCode)
		}
		resp = env.do(t, http.MethodPatch, "/api/v1/users/not-a-uuid", adminToken,
			map[string]any{"role": "viewer"})
		if resp.StatusCode != http.StatusNotFound {
			t.Errorf("non-uuid patch = %d, want 404", resp.StatusCode)
		}
	})

	t.Run("lockout guard: self role change and self disable rejected", func(t *testing.T) {
		resp := env.do(t, http.MethodPatch, "/api/v1/users/"+admin.ID, adminToken,
			map[string]any{"role": "viewer"})
		if resp.StatusCode != http.StatusConflict {
			t.Fatalf("self demotion = %d, want 409", resp.StatusCode)
		}
		if code := errorCode(t, resp); code != "self_role_change_forbidden" {
			t.Errorf("self demotion code = %q, want self_role_change_forbidden", code)
		}
		resp = env.do(t, http.MethodPost, "/api/v1/users/"+admin.ID+"/disable", adminToken, nil)
		if resp.StatusCode != http.StatusConflict {
			t.Fatalf("self disable = %d, want 409", resp.StatusCode)
		}
		if code := errorCode(t, resp); code != "self_disable_forbidden" {
			t.Errorf("self disable code = %q, want self_disable_forbidden", code)
		}
		// 관리자 계정은 그대로 살아 있어야 한다.
		if resp := env.do(t, http.MethodGet, "/api/v1/auth/me", adminToken, nil); resp.StatusCode != http.StatusOK {
			t.Errorf("admin me after rejected lockout attempts = %d, want 200", resp.StatusCode)
		}
	})

	// 마지막 활성 관리자 보호는 HTTP 경로에서는 자기 자신 가드에 먼저 걸리므로
	// (호출자가 곧 유일한 활성 관리자) 서비스 계층에서 직접 검증한다.
	t.Run("lockout guard: last active administrator cannot be disabled or demoted", func(t *testing.T) {
		if _, err := env.svc.SetUserActive(ctx, admin.ID, false, viewer.ID, auth.AuditMeta{}); !errors.Is(err, auth.ErrLastAdmin) {
			t.Errorf("disable last admin err = %v, want ErrLastAdmin", err)
		}
		role := domain.RoleViewer
		if _, err := env.svc.UpdateUser(ctx, admin.ID, auth.UserUpdate{Role: &role}, viewer.ID, auth.AuditMeta{}); !errors.Is(err, auth.ErrLastAdmin) {
			t.Errorf("demote last admin err = %v, want ErrLastAdmin", err)
		}
		if resp := env.do(t, http.MethodGet, "/api/v1/auth/me", adminToken, nil); resp.StatusCode != http.StatusOK {
			t.Errorf("admin still usable = %d, want 200", resp.StatusCode)
		}
	})

	newViewerPW := randomPassword(t)
	t.Run("password reset revokes sessions and never leaks the password", func(t *testing.T) {
		resp := env.do(t, http.MethodPost, "/api/v1/users/"+viewer.ID+"/password", adminToken,
			map[string]string{"password": "short"})
		if resp.StatusCode != http.StatusBadRequest {
			t.Errorf("weak password reset = %d, want 400", resp.StatusCode)
		} else if code := errorCode(t, resp); code != "weak_password" {
			t.Errorf("weak password code = %q, want weak_password", code)
		}

		resp = env.do(t, http.MethodPost, "/api/v1/users/"+viewer.ID+"/password", adminToken,
			map[string]string{"password": newViewerPW})
		if resp.StatusCode != http.StatusNoContent {
			t.Fatalf("password reset = %d, want 204", resp.StatusCode)
		}
		// 기존 세션은 즉시 폐기된다.
		if resp := env.do(t, http.MethodGet, "/api/v1/auth/me", viewerToken, nil); resp.StatusCode != http.StatusUnauthorized {
			t.Errorf("viewer me after password reset = %d, want 401", resp.StatusCode)
		}
		if _, status := env.login(t, "um-viewer", viewerPW); status != http.StatusUnauthorized {
			t.Errorf("login with old password = %d, want 401", status)
		}
		if _, status := env.login(t, "um-viewer", newViewerPW); status != http.StatusOK {
			t.Errorf("login with new password = %d, want 200", status)
		}
		if n := env.auditCountFor(t, "user.password.reset", viewer.ID, admin.ID); n != 1 {
			t.Errorf("user.password.reset audit events = %d, want 1", n)
		}
		// 감사 payload에 비밀번호·해시가 없어야 한다 (보안 하드 규칙 3).
		var payload string
		if err := env.pool.QueryRow(
			`SELECT coalesce(before_value::text, '') || coalesce(after_value::text, '')
			   FROM audit_events WHERE event_type = 'user.password.reset' AND target_id = $1`,
			viewer.ID).Scan(&payload); err != nil {
			t.Fatalf("read audit payload: %v", err)
		}
		if strings.Contains(payload, newViewerPW) || strings.Contains(payload, "$2a$") {
			t.Error("audit payload contains the password or a bcrypt hash")
		}
	})

	t.Run("disable blocks login and revokes sessions; enable restores", func(t *testing.T) {
		token, status := env.login(t, "um-viewer", newViewerPW)
		if status != http.StatusOK {
			t.Fatalf("viewer re-login = %d, want 200", status)
		}
		resp := env.do(t, http.MethodPost, "/api/v1/users/"+viewer.ID+"/disable", adminToken, nil)
		if resp.StatusCode != http.StatusOK {
			t.Fatalf("disable = %d, want 200", resp.StatusCode)
		}
		if got := decodeUser(t, resp); got.IsActive {
			t.Error("disabled user is_active = true, want false")
		}
		if resp := env.do(t, http.MethodGet, "/api/v1/auth/me", token, nil); resp.StatusCode != http.StatusUnauthorized {
			t.Errorf("me after disable = %d, want 401", resp.StatusCode)
		}
		if _, status := env.login(t, "um-viewer", newViewerPW); status != http.StatusUnauthorized {
			t.Errorf("login while disabled = %d, want 401", status)
		}
		if n := env.auditCountFor(t, "user.disable", viewer.ID, admin.ID); n != 1 {
			t.Errorf("user.disable audit events = %d, want 1", n)
		}

		resp = env.do(t, http.MethodPost, "/api/v1/users/"+viewer.ID+"/enable", adminToken, nil)
		if resp.StatusCode != http.StatusOK {
			t.Fatalf("enable = %d, want 200", resp.StatusCode)
		}
		if got := decodeUser(t, resp); !got.IsActive {
			t.Error("enabled user is_active = false, want true")
		}
		if _, status := env.login(t, "um-viewer", newViewerPW); status != http.StatusOK {
			t.Errorf("login after enable = %d, want 200", status)
		}
		if n := env.auditCountFor(t, "user.enable", viewer.ID, admin.ID); n != 1 {
			t.Errorf("user.enable audit events = %d, want 1", n)
		}
	})

	t.Run("list users exposes role and is_active", func(t *testing.T) {
		resp := env.do(t, http.MethodGet, "/api/v1/users", adminToken, nil)
		if resp.StatusCode != http.StatusOK {
			t.Fatalf("list users = %d, want 200", resp.StatusCode)
		}
		var out struct {
			Items []map[string]any `json:"items"`
			Total int              `json:"total"`
		}
		if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
			t.Fatalf("decode list: %v", err)
		}
		if out.Total != 3 {
			t.Errorf("total = %d, want 3", out.Total)
		}
		for _, item := range out.Items {
			if _, ok := item["role"]; !ok {
				t.Error("list item missing role")
			}
			if _, ok := item["is_active"]; !ok {
				t.Error("list item missing is_active")
			}
			if _, ok := item["password_hash"]; ok {
				t.Error("list item leaks password_hash")
			}
		}
	})
}
