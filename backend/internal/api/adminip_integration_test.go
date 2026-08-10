package api

// 접근 IP 제한 통합 테스트 (TEST_DATABASE_URL 필요 — 미설정 시 스킵).
//
// 출발지 IP를 바꿔가며 시험해야 하는데 httptest는 언제나 127.0.0.1에서 접속한다.
// 그래서 127.0.0.0/8을 신뢰 프록시로 두고 X-Forwarded-For로 출발지를 흉내낸다 —
// "신뢰 프록시 뒤에서만 XFF를 본다"는 실제 배포 경로를 그대로 태우는 셈이다.

import (
	"context"
	"encoding/json"
	"net/http"
	"testing"

	"github.com/Falke060918/INHERIT_AutoCert/backend/internal/auth"
	"github.com/Falke060918/INHERIT_AutoCert/backend/internal/domain"
	"github.com/Falke060918/INHERIT_AutoCert/backend/internal/store"
)

// 테스트에서 쓰는 가상의 출발지들 (RFC 5737·RFC 1918 문서용 대역).
const (
	ipInsider  = "10.1.2.3"   // 규칙으로 허용할 운영자 자리
	ipOutsider = "10.9.9.9"   // 규칙 밖
	ipRescue   = "192.0.2.10" // 비상 우회 대역 안
	cidrRescue = "192.0.2.0/24"
)

func newAdminIPEnv(t *testing.T) *testEnv {
	t.Helper()
	return newTestEnv(t, WithAdminIPAccess(AdminIPAccess{
		TrustedProxies: mustCIDRs(t, "127.0.0.0/8"),
		Bootstrap:      mustCIDRs(t, cidrRescue),
	}))
}

// doFrom은 do와 같지만 X-Forwarded-For로 출발지를 지정한다.
func (e *testEnv) doFrom(t *testing.T, method, path, token, sourceIP string, body any) *http.Response {
	t.Helper()
	req := e.newRequest(t, method, path, token, body)
	req.Header.Set("X-Forwarded-For", sourceIP)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("%s %s: %v", method, path, err)
	}
	t.Cleanup(func() { resp.Body.Close() })
	return resp
}

// loginFrom은 지정한 출발지에서 로그인한다.
func (e *testEnv) loginFrom(t *testing.T, username, password, sourceIP string) string {
	t.Helper()
	resp := e.doFrom(t, http.MethodPost, "/api/v1/auth/login", "", sourceIP,
		map[string]string{"username": username, "password": password})
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("login = %d, want 200", resp.StatusCode)
	}
	var out struct {
		Token string `json:"token"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		t.Fatalf("decode login response: %v", err)
	}
	return out.Token
}

type adminIPRuleJSON struct {
	ID          string `json:"id"`
	CIDR        string `json:"cidr"`
	Description string `json:"description"`
	IsActive    bool   `json:"is_active"`
}

type adminIPListJSON struct {
	Items  []adminIPRuleJSON `json:"items"`
	Total  int               `json:"total"`
	Access struct {
		ClientIP               string `json:"client_ip"`
		Enforced               bool   `json:"enforced"`
		BootstrapConfigured    bool   `json:"bootstrap_configured"`
		ClientViaBootstrap     bool   `json:"client_via_bootstrap"`
		TrustedProxyConfigured bool   `json:"trusted_proxy_configured"`
	} `json:"access"`
}

func (e *testEnv) listAdminIPRules(t *testing.T, token, sourceIP string) adminIPListJSON {
	t.Helper()
	resp := e.doFrom(t, http.MethodGet, "/api/v1/admin-ip-rules", token, sourceIP, nil)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("list admin ip rules = %d, want 200", resp.StatusCode)
	}
	var out adminIPListJSON
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		t.Fatalf("decode admin ip rule list: %v", err)
	}
	return out
}

// errorCode는 오류 응답의 code를 꺼낸다.
func errorCodeOf(t *testing.T, resp *http.Response) string {
	t.Helper()
	var out struct {
		Error struct {
			Code string `json:"code"`
		} `json:"error"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		t.Fatalf("decode error response: %v", err)
	}
	return out.Error.Code
}

func (e *testEnv) newAdminIPAdmin(t *testing.T, username string) string {
	t.Helper()
	pw := randomPassword(t)
	if _, err := e.svc.CreateUser(context.Background(), username, pw, nil,
		domain.RoleSystemAdministrator, nil, auth.AuditMeta{}); err != nil {
		t.Fatalf("create admin: %v", err)
	}
	return e.loginFrom(t, username, pw, ipInsider)
}

// 목록이 비어 있으면 제한이 없다 — 기본 설치가 스스로 잠기지 않는다.
func TestAdminIPEmptyListMeansNoRestriction(t *testing.T) {
	env := newAdminIPEnv(t)
	token := env.newAdminIPAdmin(t, "adminip-empty")

	list := env.listAdminIPRules(t, token, ipInsider)
	if list.Total != 0 || list.Access.Enforced {
		t.Fatalf("fresh install: total=%d enforced=%v, want 0/false", list.Total, list.Access.Enforced)
	}
	// 서버가 본 출발지를 그대로 돌려줘야 운영자가 자기 IP를 착각하지 않는다.
	if list.Access.ClientIP != ipInsider {
		t.Errorf("access.client_ip = %q, want %q", list.Access.ClientIP, ipInsider)
	}
	if !list.Access.TrustedProxyConfigured || !list.Access.BootstrapConfigured {
		t.Errorf("access flags = %+v, want both configured", list.Access)
	}
	// 규칙이 없으니 어떤 출발지에서도 통과한다.
	if resp := env.doFrom(t, http.MethodGet, "/api/v1/certificates/summary", token, ipOutsider, nil); resp.StatusCode != http.StatusOK {
		t.Errorf("unrestricted request from %s = %d, want 200", ipOutsider, resp.StatusCode)
	}
}

// 조회·변경 모두 System Administrator 전용이다 (보안 하드 규칙 6).
func TestAdminIPRulesRequireSystemAdministrator(t *testing.T) {
	env := newAdminIPEnv(t)
	pw := randomPassword(t)
	if _, err := env.svc.CreateUser(context.Background(), "adminip-viewer", pw, nil,
		domain.RoleViewer, nil, auth.AuditMeta{}); err != nil {
		t.Fatalf("create viewer: %v", err)
	}
	token := env.loginFrom(t, "adminip-viewer", pw, ipInsider)

	cases := []struct{ method, path string }{
		{http.MethodGet, "/api/v1/admin-ip-rules"},
		{http.MethodPost, "/api/v1/admin-ip-rules"},
		{http.MethodPatch, "/api/v1/admin-ip-rules/00000000-0000-0000-0000-000000000001"},
		{http.MethodDelete, "/api/v1/admin-ip-rules/00000000-0000-0000-0000-000000000001"},
	}
	for _, c := range cases {
		resp := env.doFrom(t, c.method, c.path, token, ipInsider, map[string]any{"cidr": "10.0.0.0/8"})
		if resp.StatusCode != http.StatusForbidden {
			t.Errorf("%s %s as viewer = %d, want 403", c.method, c.path, resp.StatusCode)
		}
	}
}

// 잠금 방지 핵심: 자기 IP를 덮지 않는 첫 규칙을 넣으면 409로 거부하고 아무것도 남기지 않는다.
func TestAdminIPCreateRejectsSelfLockout(t *testing.T) {
	env := newAdminIPEnv(t)
	token := env.newAdminIPAdmin(t, "adminip-lockout")

	resp := env.doFrom(t, http.MethodPost, "/api/v1/admin-ip-rules", token, ipInsider,
		map[string]any{"cidr": "10.9.9.0/24", "description": "다른 사무실"})
	if resp.StatusCode != http.StatusConflict {
		t.Fatalf("self-locking create = %d, want 409", resp.StatusCode)
	}
	if code := errorCodeOf(t, resp); code != "self_lockout_forbidden" {
		t.Errorf("error code = %q, want self_lockout_forbidden", code)
	}
	// 롤백 확인: 행도 감사도 남지 않는다.
	if list := env.listAdminIPRules(t, token, ipInsider); list.Total != 0 {
		t.Errorf("rules after rejected create = %d, want 0", list.Total)
	}
	if n := env.auditCount(t, store.EventAdminIPRuleCreate, domain.AuditSuccess); n != 0 {
		t.Errorf("audit events after rejected create = %d, want 0", n)
	}
}

// 규칙이 생기면 재기동 없이 즉시 반영된다(캐시 무효화) — 그리고 감사가 남는다.
func TestAdminIPRuleAppliesImmediatelyAndAudits(t *testing.T) {
	env := newAdminIPEnv(t)
	token := env.newAdminIPAdmin(t, "adminip-apply")

	// 캐시를 미리 "규칙 없음"으로 채워둔다 — 무효화가 안 되면 이 스냅샷이 계속 쓰인다.
	env.listAdminIPRules(t, token, ipInsider)

	resp := env.doFrom(t, http.MethodPost, "/api/v1/admin-ip-rules", token, ipInsider,
		map[string]any{"cidr": "10.1.2.0/24", "description": "본사"})
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("create = %d, want 201", resp.StatusCode)
	}
	var created adminIPRuleJSON
	if err := json.NewDecoder(resp.Body).Decode(&created); err != nil {
		t.Fatalf("decode created rule: %v", err)
	}
	if created.CIDR != "10.1.2.0/24" || !created.IsActive {
		t.Errorf("created rule = %+v, want active 10.1.2.0/24", created)
	}
	if n := env.auditCount(t, store.EventAdminIPRuleCreate, domain.AuditSuccess); n != 1 {
		t.Fatalf("admin_ip_rule.create audit events = %d, want 1", n)
	}

	// 허용 대역 밖은 즉시 차단된다 (인증보다 앞이라 로그인 경로도 막힌다).
	blocked := env.doFrom(t, http.MethodGet, "/api/v1/certificates/summary", token, ipOutsider, nil)
	if blocked.StatusCode != http.StatusForbidden {
		t.Fatalf("request from %s = %d, want 403", ipOutsider, blocked.StatusCode)
	}
	if code := errorCodeOf(t, blocked); code != "ip_not_allowed" {
		t.Errorf("blocked error code = %q, want ip_not_allowed", code)
	}
	login := env.doFrom(t, http.MethodPost, "/api/v1/auth/login", "", ipOutsider,
		map[string]string{"username": "adminip-apply", "password": "irrelevant"})
	if login.StatusCode != http.StatusForbidden {
		t.Errorf("login from %s = %d, want 403 (blocked before authentication)", ipOutsider, login.StatusCode)
	}
	// 허용 대역 안은 그대로 동작한다.
	if ok := env.doFrom(t, http.MethodGet, "/api/v1/certificates/summary", token, ipInsider, nil); ok.StatusCode != http.StatusOK {
		t.Errorf("request from %s = %d, want 200", ipInsider, ok.StatusCode)
	}
	// 헬스체크는 제한 대상이 아니다 — 오케스트레이터가 컨테이너를 죽이면 안 된다.
	if hz := env.doFrom(t, http.MethodGet, "/healthz", "", ipOutsider, nil); hz.StatusCode != http.StatusOK {
		t.Errorf("healthz from %s = %d, want 200", ipOutsider, hz.StatusCode)
	}
}

// 비상 우회 대역은 규칙과 무관하게 통과하고, 그 경로에서는 잠금 방지 판정을 건너뛴다.
func TestAdminIPBootstrapBypassRecoversFromLockout(t *testing.T) {
	env := newAdminIPEnv(t)
	token := env.newAdminIPAdmin(t, "adminip-rescue")

	resp := env.doFrom(t, http.MethodPost, "/api/v1/admin-ip-rules", token, ipInsider,
		map[string]any{"cidr": "10.1.2.0/24"})
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("create = %d, want 201", resp.StatusCode)
	}
	var created adminIPRuleJSON
	if err := json.NewDecoder(resp.Body).Decode(&created); err != nil {
		t.Fatalf("decode created rule: %v", err)
	}

	// 우회 대역은 규칙에 없지만 통과한다.
	list := env.listAdminIPRules(t, token, ipRescue)
	if !list.Access.ClientViaBootstrap || !list.Access.Enforced {
		t.Fatalf("access from bootstrap = %+v, want via_bootstrap+enforced", list.Access)
	}
	// 우회 경로에서는 "자기 IP가 남는가" 판정을 건너뛰므로 마지막 규칙도 지울 수 있다.
	del := env.doFrom(t, http.MethodDelete, "/api/v1/admin-ip-rules/"+created.ID, token, ipRescue, nil)
	if del.StatusCode != http.StatusNoContent {
		t.Fatalf("delete from bootstrap = %d, want 204", del.StatusCode)
	}
	if n := env.auditCount(t, store.EventAdminIPRuleDelete, domain.AuditSuccess); n != 1 {
		t.Errorf("admin_ip_rule.delete audit events = %d, want 1", n)
	}
	// 규칙이 사라졌으니 다시 제한 없음이다.
	if ok := env.doFrom(t, http.MethodGet, "/api/v1/certificates/summary", token, ipOutsider, nil); ok.StatusCode != http.StatusOK {
		t.Errorf("request from %s after delete = %d, want 200", ipOutsider, ok.StatusCode)
	}
}

// 비활성 토글도 잠금 방지 대상이다. 단, 마지막 활성 규칙을 끄는 것은 "제한 해제"라 허용된다.
func TestAdminIPToggleLockoutRules(t *testing.T) {
	env := newAdminIPEnv(t)
	token := env.newAdminIPAdmin(t, "adminip-toggle")

	mine := env.createAdminIPRule(t, token, "10.1.2.0/24")
	other := env.createAdminIPRule(t, token, "10.9.9.0/24")

	// 내 대역만 끄면 다른 활성 규칙이 남아 내가 차단된다 → 거부.
	resp := env.doFrom(t, http.MethodPatch, "/api/v1/admin-ip-rules/"+mine, token, ipInsider,
		map[string]any{"is_active": false})
	if resp.StatusCode != http.StatusConflict {
		t.Fatalf("self-locking toggle = %d, want 409", resp.StatusCode)
	}
	if code := errorCodeOf(t, resp); code != "self_lockout_forbidden" {
		t.Errorf("error code = %q, want self_lockout_forbidden", code)
	}

	// 남의 대역을 먼저 끄는 것은 문제없다.
	if r := env.doFrom(t, http.MethodPatch, "/api/v1/admin-ip-rules/"+other, token, ipInsider,
		map[string]any{"is_active": false}); r.StatusCode != http.StatusOK {
		t.Fatalf("toggle other = %d, want 200", r.StatusCode)
	}
	// 이제 내 대역이 마지막 활성 규칙이다 — 끄면 제한 해제이므로 허용된다.
	if r := env.doFrom(t, http.MethodPatch, "/api/v1/admin-ip-rules/"+mine, token, ipInsider,
		map[string]any{"is_active": false}); r.StatusCode != http.StatusOK {
		t.Fatalf("toggle last active rule = %d, want 200", r.StatusCode)
	}
	if list := env.listAdminIPRules(t, token, ipInsider); list.Access.Enforced {
		t.Error("enforced should be false once every rule is inactive")
	}
	if n := env.auditCount(t, store.EventAdminIPRuleUpdate, domain.AuditSuccess); n != 2 {
		t.Errorf("admin_ip_rule.update audit events = %d, want 2", n)
	}
}

// 입력 검증: 모르는 필드·잘못된 CIDR·중복 대역.
func TestAdminIPCreateInputValidation(t *testing.T) {
	env := newAdminIPEnv(t)
	token := env.newAdminIPAdmin(t, "adminip-input")

	tests := []struct {
		name     string
		body     map[string]any
		want     int
		wantCode string
	}{
		{
			name:     "모르는 필드는 400 (조용히 버리지 않는다)",
			body:     map[string]any{"cidr": "10.1.2.0/24", "enabled": true},
			want:     http.StatusBadRequest,
			wantCode: "invalid_body",
		},
		{
			name:     "IP가 아닌 값",
			body:     map[string]any{"cidr": "example.com"},
			want:     http.StatusBadRequest,
			wantCode: "invalid_cidr",
		},
		{
			name:     "호스트 비트가 켜진 CIDR",
			body:     map[string]any{"cidr": "10.1.2.5/24"},
			want:     http.StatusBadRequest,
			wantCode: "invalid_cidr",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			resp := env.doFrom(t, http.MethodPost, "/api/v1/admin-ip-rules", token, ipInsider, tt.body)
			if resp.StatusCode != tt.want {
				t.Fatalf("status = %d, want %d", resp.StatusCode, tt.want)
			}
			if code := errorCodeOf(t, resp); code != tt.wantCode {
				t.Errorf("error code = %q, want %q", code, tt.wantCode)
			}
		})
	}

	// 단일 IP는 /32로 정규화되고, 같은 대역 재등록은 409다.
	env.createAdminIPRule(t, token, ipInsider)
	list := env.listAdminIPRules(t, token, ipInsider)
	if list.Total != 1 || list.Items[0].CIDR != ipInsider+"/32" {
		t.Fatalf("normalized rule = %+v, want %s/32", list.Items, ipInsider)
	}
	dup := env.doFrom(t, http.MethodPost, "/api/v1/admin-ip-rules", token, ipInsider,
		map[string]any{"cidr": ipInsider + "/32"})
	if dup.StatusCode != http.StatusConflict {
		t.Fatalf("duplicate cidr = %d, want 409", dup.StatusCode)
	}
	if code := errorCodeOf(t, dup); code != "cidr_taken" {
		t.Errorf("error code = %q, want cidr_taken", code)
	}
}

// createAdminIPRule은 규칙을 하나 만들고 id를 반환한다 (201이 아니면 실패).
func (e *testEnv) createAdminIPRule(t *testing.T, token, cidr string) string {
	t.Helper()
	resp := e.doFrom(t, http.MethodPost, "/api/v1/admin-ip-rules", token, ipInsider,
		map[string]any{"cidr": cidr})
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("create rule %s = %d, want 201", cidr, resp.StatusCode)
	}
	var out adminIPRuleJSON
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		t.Fatalf("decode created rule: %v", err)
	}
	return out.ID
}
