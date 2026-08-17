package api

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"testing"

	"github.com/Falke060918/INHERIT_AutoCert/backend/internal/auth"
	"github.com/Falke060918/INHERIT_AutoCert/backend/internal/domain"
	"github.com/Falke060918/INHERIT_AutoCert/backend/internal/store"
)

// 사용자 관리 API 검증: 수정·활성 토글·비밀번호 재설정의 성공 경로,
// 비관리자 거부(403), 락아웃 방지 거부, 감사 이벤트 기록, 세션 즉시 폐기.
// TEST_DATABASE_URL 미설정 시 스킵한다 (전용 테스트 DB).
// 비밀번호는 난수만 쓴다 — 시크릿 하드코딩 금지 (보안 하드 규칙 4).

// errorCode는 오류 응답 본문의 error.code를 뽑는다.
func errorCode(t *testing.T, resp *http.Response) string {
	t.Helper()
	code, _ := errorCodeMessage(t, resp)
	return code
}

// errorCodeMessage는 오류 응답 본문의 error.code와 error.message를 함께 뽑는다.
func errorCodeMessage(t *testing.T, resp *http.Response) (string, string) {
	t.Helper()
	var body errorResponse
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		t.Fatalf("decode error body: %v", err)
	}
	return body.Error.Code, body.Error.Message
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
		} else if code, msg := errorCodeMessage(t, resp); code != "weak_password" {
			t.Errorf("weak password code = %q, want weak_password", code)
		} else if want := fmt.Sprintf("비밀번호는 %d자 이상이어야 합니다", auth.MinPasswordLength); msg != want {
			// 사용자 메시지는 한글 정책 문구다 — auth 원문(영문)이 새면 회귀다.
			t.Errorf("weak password message = %q, want %q", msg, want)
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

// TestLastAdminGuardCountsOnlyActiveAdministrators는 마지막 관리자 가드가
// 비활성 관리자를 "남은 관리자"로 세지 않는지 고정한다.
// 가드의 is_active 술어가 빠지면 비활성 관리자 계정 하나가 남았다는 이유로
// 마지막 활성 관리자의 비활성화·강등이 통과해 제품 전체가 잠긴다.
//
// ErrLastAdmin은 HTTP 경로에서는 자기 자신 가드에 먼저 걸려 도달하지 않는다
// (auth/users.go의 ensureAnotherActiveAdmin 주석 참조). 도달 가능한 경로인
// 서비스 계층 직접 호출로 검증한다 — actor는 관리자가 아닌 사용자다.
// newTestEnv가 users/sessions/audit_events를 비우므로 다른 테스트와 격리된다.
func TestLastAdminGuardCountsOnlyActiveAdministrators(t *testing.T) {
	env := newTestEnv(t)
	ctx := context.Background()

	admin, err := env.svc.CreateUser(ctx, "la-admin", randomPassword(t), nil,
		domain.RoleSystemAdministrator, nil, auth.AuditMeta{})
	if err != nil {
		t.Fatalf("create admin: %v", err)
	}
	spare, err := env.svc.CreateUser(ctx, "la-spare-admin", randomPassword(t), nil,
		domain.RoleSystemAdministrator, nil, auth.AuditMeta{})
	if err != nil {
		t.Fatalf("create spare admin: %v", err)
	}
	viewer, err := env.svc.CreateUser(ctx, "la-viewer", randomPassword(t), nil,
		domain.RoleViewer, nil, auth.AuditMeta{})
	if err != nil {
		t.Fatalf("create viewer: %v", err)
	}

	// 대조군: 활성 관리자가 2명이면 한 명은 비활성화할 수 있다.
	if _, err := env.svc.SetUserActive(ctx, spare.ID, false, admin.ID, auth.AuditMeta{}); err != nil {
		t.Fatalf("disable spare admin: %v", err)
	}

	// 이제 관리자 계정은 2개지만 활성은 admin 하나뿐이다 — 비활성 관리자는 세면 안 된다.
	if _, err := env.svc.SetUserActive(ctx, admin.ID, false, viewer.ID, auth.AuditMeta{}); !errors.Is(err, auth.ErrLastAdmin) {
		t.Errorf("disable last active admin err = %v, want ErrLastAdmin", err)
	}
	role := domain.RoleViewer
	if _, err := env.svc.UpdateUser(ctx, admin.ID, auth.UserUpdate{Role: &role}, viewer.ID, auth.AuditMeta{}); !errors.Is(err, auth.ErrLastAdmin) {
		t.Errorf("demote last active admin err = %v, want ErrLastAdmin", err)
	}

	// 거부는 상태를 남기지 않는다.
	users, err := env.svc.ListUsers(ctx)
	if err != nil {
		t.Fatalf("list users: %v", err)
	}
	for _, u := range users {
		if u.ID == admin.ID && (!u.IsActive || u.Role != domain.RoleSystemAdministrator) {
			t.Errorf("admin changed after rejected calls: role=%s active=%v", u.Role, u.IsActive)
		}
	}

	// 예비 관리자를 되살리면 가드가 풀린다 — 가드가 무조건 막는 게 아님을 확인한다.
	if _, err := env.svc.SetUserActive(ctx, spare.ID, true, admin.ID, auth.AuditMeta{}); err != nil {
		t.Fatalf("re-enable spare admin: %v", err)
	}
	if _, err := env.svc.SetUserActive(ctx, admin.ID, false, viewer.ID, auth.AuditMeta{}); err != nil {
		t.Errorf("disable admin with another active admin present: %v", err)
	}
}

// TestUserEmailAPI는 계정의 알림 메일 주소를 제품 경로(생성·부분 수정)로 넣고
// 지울 수 있는지 고정한다. 이 값이 없으면 owner/role/user 수신자가 메일 채널에서
// 0명으로 해석돼 알림이 조용히 사라진다 (마이그레이션 0012 users.email).
// 모르는 필드를 400으로 거부하는 것도 함께 고정한다 — 조용히 버리면
// "201을 받았는데 값이 안 들어갔다"는 오진을 부른다.
func TestUserEmailAPI(t *testing.T) {
	env := newTestEnv(t)
	ctx := context.Background()

	adminPW := randomPassword(t)
	admin, err := env.svc.CreateUser(ctx, "em-admin", adminPW, nil,
		domain.RoleSystemAdministrator, nil, auth.AuditMeta{})
	if err != nil {
		t.Fatalf("create admin: %v", err)
	}
	adminToken, status := env.login(t, "em-admin", adminPW)
	if status != http.StatusOK {
		t.Fatalf("admin login = %d, want 200", status)
	}

	// dbEmail은 DB에 실제로 들어간 값을 읽는다 (응답만 보면 유실을 못 잡는다).
	dbEmail := func(t *testing.T, id string) *string {
		t.Helper()
		var v *string
		if err := env.pool.QueryRow(`SELECT email FROM users WHERE id = $1::uuid`, id).Scan(&v); err != nil {
			t.Fatalf("read email: %v", err)
		}
		return v
	}

	var target domain.User
	t.Run("create stores the email", func(t *testing.T) {
		resp := env.do(t, http.MethodPost, "/api/v1/users", adminToken, map[string]any{
			"username": "em-operator", "password": randomPassword(t),
			"role": "operator", "email": "  em-operator@example.test  ",
		})
		if resp.StatusCode != http.StatusCreated {
			t.Fatalf("create user = %d, want 201", resp.StatusCode)
		}
		target = decodeUser(t, resp)
		if target.Email == nil || *target.Email != "em-operator@example.test" {
			t.Fatalf("response email = %v, want em-operator@example.test (trimmed)", target.Email)
		}
		if got := dbEmail(t, target.ID); got == nil || *got != "em-operator@example.test" {
			t.Errorf("stored email = %v, want em-operator@example.test", got)
		}
	})

	t.Run("create rejects a malformed email and unknown fields", func(t *testing.T) {
		cases := []struct {
			name, email, wantCode, wantMsg string
		}{
			{"no at sign", "em-nobody.example.test", "invalid_email", "메일 주소는 local@domain 형식이어야 합니다"},
			{"empty local part", "@example.test", "invalid_email", "메일 주소는 local@domain 형식이어야 합니다"},
			{"empty domain part", "em-nobody@", "invalid_email", "메일 주소는 local@domain 형식이어야 합니다"},
			{"two at signs", "a@b@example.test", "invalid_email", "메일 주소는 local@domain 형식이어야 합니다"},
			{"inner space", "em nobody@example.test", "invalid_email", "메일 주소에는 공백을 넣을 수 없습니다"},
		}
		for _, c := range cases {
			resp := env.do(t, http.MethodPost, "/api/v1/users", adminToken, map[string]any{
				"username": "em-bad-" + c.name, "password": randomPassword(t),
				"role": "viewer", "email": c.email,
			})
			if resp.StatusCode != http.StatusBadRequest {
				t.Errorf("%s: create = %d, want 400", c.name, resp.StatusCode)
				continue
			}
			if code, msg := errorCodeMessage(t, resp); code != c.wantCode {
				t.Errorf("%s: code = %q, want %q", c.name, code, c.wantCode)
			} else if msg != c.wantMsg {
				// 사용자 메시지는 한글이다 — 영문 원문이 새면 회귀다.
				t.Errorf("%s: message = %q, want %q", c.name, msg, c.wantMsg)
			}
		}
		// 오타 난 필드는 조용히 버리지 않는다.
		resp := env.do(t, http.MethodPost, "/api/v1/users", adminToken, map[string]any{
			"username": "em-typo", "password": randomPassword(t),
			"role": "viewer", "e_mail": "em-typo@example.test",
		})
		if resp.StatusCode != http.StatusBadRequest {
			t.Errorf("unknown field create = %d, want 400", resp.StatusCode)
		} else if code := errorCode(t, resp); code != "invalid_body" {
			t.Errorf("unknown field code = %q, want invalid_body", code)
		}
		// 거부된 요청은 계정을 남기지 않는다.
		var n int
		if err := env.pool.QueryRow(
			`SELECT count(*) FROM users WHERE username LIKE 'em-bad-%' OR username = 'em-typo'`).Scan(&n); err != nil {
			t.Fatalf("count rejected users: %v", err)
		}
		if n != 0 {
			t.Errorf("rejected create left %d user rows, want 0", n)
		}
	})

	t.Run("create without email leaves it null", func(t *testing.T) {
		resp := env.do(t, http.MethodPost, "/api/v1/users", adminToken, map[string]any{
			"username": "em-noaddr", "password": randomPassword(t), "role": "viewer",
		})
		if resp.StatusCode != http.StatusCreated {
			t.Fatalf("create user = %d, want 201", resp.StatusCode)
		}
		created := decodeUser(t, resp)
		if created.Email != nil {
			t.Errorf("email = %v, want null", *created.Email)
		}
		// 빈 문자열도 NULL로 정규화한다 (0012 users_email_shape CHECK가 빈 값을 거부한다).
		resp = env.do(t, http.MethodPost, "/api/v1/users", adminToken, map[string]any{
			"username": "em-blank", "password": randomPassword(t), "role": "viewer", "email": "   ",
		})
		if resp.StatusCode != http.StatusCreated {
			t.Fatalf("create with blank email = %d, want 201", resp.StatusCode)
		}
		if got := decodeUser(t, resp); got.Email != nil {
			t.Errorf("blank email stored as %q, want null", *got.Email)
		}
	})

	t.Run("patch sets, replaces and clears the email with one audit event", func(t *testing.T) {
		resp := env.do(t, http.MethodPatch, "/api/v1/users/"+target.ID, adminToken,
			map[string]any{"email": "em-ops@example.test"})
		if resp.StatusCode != http.StatusOK {
			t.Fatalf("patch email = %d, want 200", resp.StatusCode)
		}
		if got := decodeUser(t, resp); got.Email == nil || *got.Email != "em-ops@example.test" {
			t.Fatalf("patched email = %v, want em-ops@example.test", got.Email)
		}
		if got := dbEmail(t, target.ID); got == nil || *got != "em-ops@example.test" {
			t.Errorf("stored email = %v, want em-ops@example.test", got)
		}
		// email만 보낸 요청은 no_fields가 아니다.
		if n := env.auditCountFor(t, "user.update", target.ID, admin.ID); n != 1 {
			t.Errorf("user.update audit events = %d, want 1", n)
		}
		// 감사 before/after가 메일 주소 변경을 담는다.
		var payload string
		if err := env.pool.QueryRow(
			`SELECT coalesce(before_value::text, '') || coalesce(after_value::text, '')
			   FROM audit_events WHERE event_type = 'user.update' AND target_id = $1`,
			target.ID).Scan(&payload); err != nil {
			t.Fatalf("read audit payload: %v", err)
		}
		if !strings.Contains(payload, "em-ops@example.test") {
			t.Error("user.update audit payload does not record the new email")
		}

		// null은 지움이다 — 이후 이 계정은 메일 수신자로 해석되지 않는다.
		resp = env.do(t, http.MethodPatch, "/api/v1/users/"+target.ID, adminToken,
			map[string]any{"email": nil})
		if resp.StatusCode != http.StatusOK {
			t.Fatalf("patch email null = %d, want 200", resp.StatusCode)
		}
		if got := decodeUser(t, resp); got.Email != nil {
			t.Errorf("cleared email = %v, want null", *got.Email)
		}
		if got := dbEmail(t, target.ID); got != nil {
			t.Errorf("stored email after clear = %v, want null", *got)
		}
		// 빈 문자열도 지움이다.
		if resp := env.do(t, http.MethodPatch, "/api/v1/users/"+target.ID, adminToken,
			map[string]any{"email": "em-back@example.test"}); resp.StatusCode != http.StatusOK {
			t.Fatalf("restore email = %d, want 200", resp.StatusCode)
		}
		resp = env.do(t, http.MethodPatch, "/api/v1/users/"+target.ID, adminToken,
			map[string]any{"email": ""})
		if resp.StatusCode != http.StatusOK {
			t.Fatalf("patch empty email = %d, want 200", resp.StatusCode)
		}
		if got := dbEmail(t, target.ID); got != nil {
			t.Errorf("stored email after empty string = %v, want null", *got)
		}
	})

	t.Run("patch rejects malformed email and unknown fields without changing anything", func(t *testing.T) {
		if resp := env.do(t, http.MethodPatch, "/api/v1/users/"+target.ID, adminToken,
			map[string]any{"email": "em-ops.example.test"}); resp.StatusCode != http.StatusBadRequest {
			t.Errorf("malformed patch = %d, want 400", resp.StatusCode)
		} else if code := errorCode(t, resp); code != "invalid_email" {
			t.Errorf("malformed patch code = %q, want invalid_email", code)
		}
		if resp := env.do(t, http.MethodPatch, "/api/v1/users/"+target.ID, adminToken,
			map[string]any{"emial": "em-ops@example.test"}); resp.StatusCode != http.StatusBadRequest {
			t.Errorf("unknown field patch = %d, want 400", resp.StatusCode)
		} else if code := errorCode(t, resp); code != "invalid_body" {
			t.Errorf("unknown field patch code = %q, want invalid_body", code)
		}
		if got := dbEmail(t, target.ID); got != nil {
			t.Errorf("email changed by a rejected patch: %v", *got)
		}
	})

	t.Run("list exposes email so the operator can see who cannot receive mail", func(t *testing.T) {
		resp := env.do(t, http.MethodGet, "/api/v1/users", adminToken, nil)
		if resp.StatusCode != http.StatusOK {
			t.Fatalf("list users = %d, want 200", resp.StatusCode)
		}
		var out struct {
			Items []map[string]any `json:"items"`
		}
		if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
			t.Fatalf("decode list: %v", err)
		}
		for _, item := range out.Items {
			if _, ok := item["email"]; !ok {
				t.Fatalf("list item %v missing email key", item["username"])
			}
		}
	})
}

// TestUserEmailResolvesRoleRecipient는 결함의 본체를 고정한다: 제품 경로로 넣은
// 메일 주소가 kind=role 수신자 해석에 실제로 쓰이는지다.
// 주소가 없으면 그 계정은 수신자로 잡히지 않고 딜리버리가 skipped로 끝난다.
func TestUserEmailResolvesRoleRecipient(t *testing.T) {
	env := newTestEnv(t)
	ctx := context.Background()

	adminPW := randomPassword(t)
	if _, err := env.svc.CreateUser(ctx, "rr-admin", adminPW, nil,
		domain.RoleSystemAdministrator, nil, auth.AuditMeta{}); err != nil {
		t.Fatalf("create admin: %v", err)
	}
	adminToken, status := env.login(t, "rr-admin", adminPW)
	if status != http.StatusOK {
		t.Fatalf("admin login = %d, want 200", status)
	}

	// 같은 역할의 두 계정 — 한 명만 메일 주소가 있다.
	create := func(username string, body map[string]any) {
		t.Helper()
		body["username"] = username
		body["password"] = randomPassword(t)
		body["role"] = "security_administrator"
		resp := env.do(t, http.MethodPost, "/api/v1/users", adminToken, body)
		if resp.StatusCode != http.StatusCreated {
			t.Fatalf("create %s = %d, want 201", username, resp.StatusCode)
		}
	}
	create("rr-with-mail", map[string]any{"email": "rr-with-mail@example.test"})
	create("rr-without-mail", map[string]any{})

	// 만료 임박 인증서 1건 (픽스처는 테스트 전용으로 만든다 — 실제 키·인증서 금지).
	sum := sha256.Sum256([]byte("role-recipient-fixture"))
	fingerprint := hex.EncodeToString(sum[:])
	const policyName = "rr-role-recipients"
	// 재실행 격리: newTestEnv는 users/sessions/audit_events만 비우므로 이 테스트의
	// 픽스처(인증서·정책·그 이벤트)는 직접 지운다 — 테스트 전용 DB에서만 하는 정리다.
	for _, c := range []struct {
		query string
		arg   any
	}{
		{`DELETE FROM notification_events
		   WHERE certificate_id IN (SELECT id FROM certificates WHERE fingerprint_sha256 = $1)`, fingerprint},
		{`DELETE FROM certificates WHERE fingerprint_sha256 = $1`, fingerprint},
		{`DELETE FROM notification_policies WHERE name = $1`, policyName},
	} {
		if _, err := env.pool.Exec(c.query, c.arg); err != nil {
			t.Fatalf("cleanup fixture: %v", err)
		}
	}
	var certID string
	if err := env.pool.QueryRow(`
		INSERT INTO certificates (
			fingerprint_sha256, serial_number, subject_dn, issuer_dn, common_name, sans,
			not_before, not_after, public_key_algorithm, public_key_size,
			signature_algorithm, certificate_type, status
		) VALUES ($1, 'rr-1', 'CN=rr.example.test', 'CN=fixture-ca', 'rr.example.test', '{}',
			now() - interval '90 days', now() + interval '10 days', 'ECDSA', 256,
			'ECDSA-SHA256', 'server', 'active')
		RETURNING id`, fingerprint).Scan(&certID); err != nil {
		t.Fatalf("insert certificate fixture: %v", err)
	}

	st := store.New(env.pool)
	if _, err := st.CreateNotificationPolicy(ctx, store.NotificationPolicyInput{
		Name:       policyName,
		Enabled:    true,
		EventTypes: []string{store.NotifyEventCertExpiring},
		Thresholds: []int{30},
		Channels:   []string{store.NotifyChannelEmail},
		Recipients: []store.NotificationRecipient{
			{Kind: store.RecipientRole, Role: string(domain.RoleSecurityAdministrator)},
		},
	}); err != nil {
		t.Fatalf("create policy: %v", err)
	}

	events, err := st.DetectExpiryEvents(ctx, store.DetectModeSend)
	if err != nil {
		t.Fatalf("detect events: %v", err)
	}
	ids := []string{}
	for _, e := range events {
		if e.CertificateID != nil && *e.CertificateID == certID {
			ids = append(ids, e.ID)
		}
	}
	if len(ids) == 0 {
		t.Fatal("no expiry event detected for the fixture certificate")
	}
	if _, err := st.FanOutDeliveries(ctx, ids); err != nil {
		t.Fatalf("fan out deliveries: %v", err)
	}

	rows, err := env.pool.Query(`
		SELECT recipient, status FROM notification_deliveries
		 WHERE event_id = ANY($1::uuid[]) AND channel = 'email'`, pqArray(ids))
	if err != nil {
		t.Fatalf("read deliveries: %v", err)
	}
	defer rows.Close()
	got := map[string]string{}
	for rows.Next() {
		var recipient, deliveryStatus string
		if err := rows.Scan(&recipient, &deliveryStatus); err != nil {
			t.Fatalf("scan delivery: %v", err)
		}
		got[recipient] = deliveryStatus
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("read deliveries: %v", err)
	}

	if s, ok := got["rr-with-mail@example.test"]; !ok {
		t.Errorf("no delivery for the account with an email address (got %v)", got)
	} else if s != store.DeliveryStatusPending {
		t.Errorf("delivery status = %q, want pending", s)
	}
	// 주소가 없는 계정은 수신자가 되지 못한다 — 그래서 화면이 이를 드러내야 한다.
	if _, ok := got[""]; ok && len(got) == 1 {
		t.Errorf("only the skipped placeholder delivery exists: %v", got)
	}
}

// pqArray는 uuid[] 바인딩용 리터럴을 만든다 (pgx stdlib은 []string을 배열로 넘기지 않는다).
func pqArray(ids []string) string {
	return "{" + strings.Join(ids, ",") + "}"
}
