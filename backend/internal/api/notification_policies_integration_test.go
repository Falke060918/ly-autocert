package api

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/Falke060918/INHERIT_AutoCert/backend/internal/auth"
	"github.com/Falke060918/INHERIT_AutoCert/backend/internal/domain"
)

// 알림 정책·채널·발송 이력 API 검증 (05-notifications 9.2·9.3).
// 핵심 단언 3개: 역할 게이트(403/401), 채널 응답에 비밀값 부재, 감사 이벤트 4종.

// fakeChannels는 테스트용 채널 배선이다. 내부에 "설정값"을 들고 있지만
// 인터페이스가 {channel, enabled, reason}만 노출하므로 응답으로 샐 자리가 없다 —
// 그 성질을 테스트가 검증한다.
type fakeChannels struct {
	smtpHost     string
	smtpPassword string
	webhookURL   string
	sendErr      error
	sent         []string
}

func (f *fakeChannels) Status() []ChannelStatus {
	return []ChannelStatus{
		{Channel: "email", Enabled: true},
		{Channel: "webhook", Enabled: false, Reason: "WEBHOOK_URL is not set"},
		{Channel: "inbox", Enabled: true},
		{Channel: "syslog", Enabled: false, Reason: "SYSLOG_ADDR is not set"},
	}
}

func (f *fakeChannels) SendTest(_ context.Context, channel, _ string) error {
	if f.sendErr != nil {
		return f.sendErr
	}
	f.sent = append(f.sent, channel)
	return nil
}

// policyBody는 유효한 정책 생성 본문이다.
func policyBody(name string) map[string]any {
	return map[string]any{
		"name":        name,
		"enabled":     true,
		"event_types": []string{"certificate.expiring"},
		"thresholds":  []int{30, 7},
		"channels":    []string{"inbox"},
		"recipients":  []map[string]string{{"kind": "role", "role": "system_administrator"}},
	}
}

func decodePolicy(t *testing.T, resp *http.Response) map[string]any {
	t.Helper()
	var out map[string]any
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		t.Fatalf("decode policy: %v", err)
	}
	return out
}

func TestNotificationPolicyAPI(t *testing.T) {
	env := newTestEnv(t)
	env.cleanNotifications(t)
	ctx := t.Context()

	users := map[domain.Role]string{
		domain.RoleSecurityAdministrator: "np-secadmin",
		domain.RoleSystemAdministrator:   "np-sysadmin",
		domain.RoleOperator:              "np-operator",
		domain.RoleAuditor:               "np-auditor",
	}
	tokens := map[domain.Role]string{}
	for role, username := range users {
		pw := randomPassword(t)
		if _, err := env.svc.CreateUser(ctx, username, pw, nil, role, nil, auth.AuditMeta{}); err != nil {
			t.Fatalf("create %s: %v", username, err)
		}
		token, status := env.login(t, username, pw)
		if status != http.StatusOK {
			t.Fatalf("%s login = %d, want 200", username, status)
		}
		tokens[role] = token
	}

	t.Run("role gate", func(t *testing.T) {
		if resp := env.do(t, http.MethodGet, "/api/v1/notification-policies", "", nil); resp.StatusCode != http.StatusUnauthorized {
			t.Errorf("unauthenticated list = %d, want 401", resp.StatusCode)
		}
		for _, role := range []domain.Role{domain.RoleOperator, domain.RoleAuditor} {
			resp := env.do(t, http.MethodGet, "/api/v1/notification-policies", tokens[role], nil)
			if resp.StatusCode != http.StatusForbidden {
				t.Errorf("%s list policies = %d, want 403", role, resp.StatusCode)
			}
			resp = env.do(t, http.MethodPost, "/api/v1/notification-policies", tokens[role], policyBody("x"))
			if resp.StatusCode != http.StatusForbidden {
				t.Errorf("%s create policy = %d, want 403", role, resp.StatusCode)
			}
			resp = env.do(t, http.MethodGet, "/api/v1/notification-channels", tokens[role], nil)
			if resp.StatusCode != http.StatusForbidden {
				t.Errorf("%s list channels = %d, want 403", role, resp.StatusCode)
			}
		}
		// 발송 이력만 auditor에도 열려 있다 (설계 14.3).
		if resp := env.do(t, http.MethodGet, "/api/v1/notification-deliveries", tokens[domain.RoleAuditor], nil); resp.StatusCode != http.StatusOK {
			t.Errorf("auditor list deliveries = %d, want 200", resp.StatusCode)
		}
		if resp := env.do(t, http.MethodGet, "/api/v1/notification-deliveries", tokens[domain.RoleOperator], nil); resp.StatusCode != http.StatusForbidden {
			t.Errorf("operator list deliveries = %d, want 403", resp.StatusCode)
		}
	})

	t.Run("invalid bodies are 400", func(t *testing.T) {
		cases := map[string]map[string]any{
			"empty name":        {"name": "  ", "event_types": []string{"certificate.expiring"}, "channels": []string{"inbox"}},
			"unknown channel":   {"name": "c1", "event_types": []string{"certificate.expiring"}, "channels": []string{"carrier-pigeon"}},
			"no channel":        {"name": "c2", "event_types": []string{"certificate.expiring"}, "channels": []string{}},
			"unknown event":     {"name": "c3", "event_types": []string{"certificate.exploded"}, "channels": []string{"inbox"}},
			"recipient kind":    {"name": "c4", "event_types": []string{"certificate.expiring"}, "channels": []string{"inbox"}, "recipients": []map[string]string{{"kind": "address", "email": "a@b.c"}}},
			"recipient role":    {"name": "c5", "event_types": []string{"certificate.expiring"}, "channels": []string{"inbox"}, "recipients": []map[string]string{{"kind": "role", "role": "wizard"}}},
			"negative treshold": {"name": "c6", "event_types": []string{"certificate.expiring"}, "channels": []string{"inbox"}, "thresholds": []int{0}},
			"ca threshold 0":    {"name": "c7", "event_types": []string{"certificate.expiring"}, "channels": []string{"inbox"}, "ca_thresholds": []int{0}},
			"ca threshold max":  {"name": "c8", "event_types": []string{"certificate.expiring"}, "channels": []string{"inbox"}, "ca_thresholds": []int{4000}},
		}
		for name, body := range cases {
			resp := env.do(t, http.MethodPost, "/api/v1/notification-policies", tokens[domain.RoleSecurityAdministrator], body)
			if resp.StatusCode != http.StatusBadRequest {
				t.Errorf("%s create = %d, want 400", name, resp.StatusCode)
			}
		}
	})

	// security_administrator가 정책을 만들 수 있어야 한다 (00-spec 7.11 정책 설정).
	resp := env.do(t, http.MethodPost, "/api/v1/notification-policies",
		tokens[domain.RoleSecurityAdministrator], policyBody("만료 임박 알림"))
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("secadmin create policy = %d, want 201", resp.StatusCode)
	}
	created := decodePolicy(t, resp)
	policyID, _ := created["id"].(string)
	if policyID == "" {
		t.Fatal("created policy has no id")
	}
	if n := env.auditCount(t, "notification.policy.create", domain.AuditSuccess); n != 1 {
		t.Errorf("notification.policy.create audit events = %d, want 1", n)
	}

	t.Run("duplicate name is 409", func(t *testing.T) {
		resp := env.do(t, http.MethodPost, "/api/v1/notification-policies",
			tokens[domain.RoleSystemAdministrator], policyBody("만료 임박 알림"))
		if resp.StatusCode != http.StatusConflict {
			t.Errorf("duplicate name = %d, want 409", resp.StatusCode)
		}
	})

	t.Run("list returns the policy", func(t *testing.T) {
		resp := env.do(t, http.MethodGet, "/api/v1/notification-policies", tokens[domain.RoleSystemAdministrator], nil)
		if resp.StatusCode != http.StatusOK {
			t.Fatalf("list = %d, want 200", resp.StatusCode)
		}
		var out struct {
			Items []map[string]any `json:"items"`
			Total int              `json:"total"`
		}
		if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
			t.Fatalf("decode list: %v", err)
		}
		if out.Total != 1 || len(out.Items) != 1 {
			t.Fatalf("policies total = %d (items %d), want 1", out.Total, len(out.Items))
		}
	})

	t.Run("patch updates only the given fields", func(t *testing.T) {
		resp := env.do(t, http.MethodPatch, "/api/v1/notification-policies/"+policyID,
			tokens[domain.RoleSecurityAdministrator], map[string]any{"enabled": false})
		if resp.StatusCode != http.StatusOK {
			t.Fatalf("patch = %d, want 200", resp.StatusCode)
		}
		updated := decodePolicy(t, resp)
		if updated["enabled"] != false {
			t.Errorf("enabled = %v, want false", updated["enabled"])
		}
		if updated["name"] != "만료 임박 알림" {
			t.Errorf("name = %v, want unchanged", updated["name"])
		}
		if n := env.auditCount(t, "notification.policy.update", domain.AuditSuccess); n != 1 {
			t.Errorf("notification.policy.update audit events = %d, want 1", n)
		}

		// 잘못된 채널로의 부분 수정은 400이다.
		resp = env.do(t, http.MethodPatch, "/api/v1/notification-policies/"+policyID,
			tokens[domain.RoleSecurityAdministrator], map[string]any{"channels": []string{"telepathy"}})
		if resp.StatusCode != http.StatusBadRequest {
			t.Errorf("patch with unknown channel = %d, want 400", resp.StatusCode)
		}
	})

	t.Run("unknown policy is 404", func(t *testing.T) {
		for _, path := range []string{
			"/api/v1/notification-policies/00000000-0000-0000-0000-000000000000",
			"/api/v1/notification-policies/not-a-uuid",
		} {
			resp := env.do(t, http.MethodPatch, path, tokens[domain.RoleSystemAdministrator],
				map[string]any{"enabled": true})
			if resp.StatusCode != http.StatusNotFound {
				t.Errorf("patch %s = %d, want 404", path, resp.StatusCode)
			}
			resp = env.do(t, http.MethodDelete, path, tokens[domain.RoleSystemAdministrator], nil)
			if resp.StatusCode != http.StatusNotFound {
				t.Errorf("delete %s = %d, want 404", path, resp.StatusCode)
			}
		}
	})

	// CA 전용 임계값(0022)은 3상태다. PATCH에서 "키 없음"과 "명시적 null"이 같은
	// 값으로 접히면 한 번 설정한 CA 임계값을 API로 되돌릴 수 없게 된다.
	t.Run("ca_thresholds keeps three states through PATCH", func(t *testing.T) {
		patch := func(body map[string]any) map[string]any {
			t.Helper()
			resp := env.do(t, http.MethodPatch, "/api/v1/notification-policies/"+policyID,
				tokens[domain.RoleSecurityAdministrator], body)
			if resp.StatusCode != http.StatusOK {
				t.Fatalf("patch %v = %d, want 200", body, resp.StatusCode)
			}
			return decodePolicy(t, resp)
		}

		got := patch(map[string]any{"ca_thresholds": []int{365, 180}})
		days, _ := got["ca_thresholds"].([]any)
		if len(days) != 2 || days[0] != float64(365) {
			t.Fatalf("ca_thresholds after set = %#v, want [365 180]", got["ca_thresholds"])
		}

		// 빈 배열은 "CA는 알리지 않음"이라 null(미설정)과 구분되어야 한다.
		got = patch(map[string]any{"ca_thresholds": []int{}})
		if days, ok := got["ca_thresholds"].([]any); !ok || len(days) != 0 {
			t.Fatalf("ca_thresholds after empty = %#v, want []", got["ca_thresholds"])
		}

		// 키가 없으면 건드리지 않는다.
		got = patch(map[string]any{"enabled": false})
		if days, ok := got["ca_thresholds"].([]any); !ok || len(days) != 0 {
			t.Fatalf("ca_thresholds after an unrelated patch = %#v, want [] (unchanged)", got["ca_thresholds"])
		}

		// 명시적 null은 해제다.
		got = patch(map[string]any{"ca_thresholds": nil})
		if got["ca_thresholds"] != nil {
			t.Fatalf("ca_thresholds after null = %#v, want null", got["ca_thresholds"])
		}

		for name, body := range map[string]map[string]any{
			"zero":         {"ca_thresholds": []int{0}},
			"over max":     {"ca_thresholds": []int{4000}},
			"not an array": {"ca_thresholds": "365"},
		} {
			resp := env.do(t, http.MethodPatch, "/api/v1/notification-policies/"+policyID,
				tokens[domain.RoleSecurityAdministrator], body)
			if resp.StatusCode != http.StatusBadRequest {
				t.Errorf("patch ca_thresholds (%s) = %d, want 400", name, resp.StatusCode)
			}
		}
	})

	t.Run("channels without wiring report a reason, never a value", func(t *testing.T) {
		resp := env.do(t, http.MethodGet, "/api/v1/notification-channels", tokens[domain.RoleSystemAdministrator], nil)
		if resp.StatusCode != http.StatusOK {
			t.Fatalf("list channels = %d, want 200", resp.StatusCode)
		}
		var out struct {
			Items []ChannelStatus `json:"items"`
		}
		if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
			t.Fatalf("decode channels: %v", err)
		}
		if len(out.Items) != 4 {
			t.Fatalf("channels = %d, want 4", len(out.Items))
		}
		for _, ch := range out.Items {
			if ch.Enabled || ch.Reason == "" {
				t.Errorf("channel %s = %+v, want disabled with a reason", ch.Channel, ch)
			}
		}
	})

	t.Run("test send without wiring is 503 and still audits", func(t *testing.T) {
		resp := env.do(t, http.MethodPost, "/api/v1/notification-policies/"+policyID+"/test",
			tokens[domain.RoleSecurityAdministrator], map[string]any{})
		if resp.StatusCode != http.StatusServiceUnavailable {
			t.Fatalf("test send = %d, want 503", resp.StatusCode)
		}
		if n := env.auditCount(t, "notification.test", domain.AuditFailure); n != 1 {
			t.Errorf("notification.test failure audit events = %d, want 1", n)
		}
	})

	t.Run("delete removes the policy and audits", func(t *testing.T) {
		resp := env.do(t, http.MethodDelete, "/api/v1/notification-policies/"+policyID,
			tokens[domain.RoleSystemAdministrator], nil)
		if resp.StatusCode != http.StatusNoContent {
			t.Fatalf("delete = %d, want 204", resp.StatusCode)
		}
		if n := env.auditCount(t, "notification.policy.delete", domain.AuditSuccess); n != 1 {
			t.Errorf("notification.policy.delete audit events = %d, want 1", n)
		}
		resp = env.do(t, http.MethodDelete, "/api/v1/notification-policies/"+policyID,
			tokens[domain.RoleSystemAdministrator], nil)
		if resp.StatusCode != http.StatusNotFound {
			t.Errorf("second delete = %d, want 404", resp.StatusCode)
		}
	})
}

// 채널 배선이 있는 서버: 테스트 발송과 채널 목록의 비밀값 부재를 검증한다.
func TestNotificationChannelsWithWiring(t *testing.T) {
	fake := &fakeChannels{
		smtpHost:     "smtp.internal.example",
		smtpPassword: "s3cr3t-smtp-password",
		webhookURL:   "https://hooks.internal.example/abcdef",
	}
	env := newTestEnv(t, WithNotificationChannels(fake))
	env.cleanNotifications(t)
	ctx := t.Context()

	pw := randomPassword(t)
	if _, err := env.svc.CreateUser(ctx, "nc-secadmin", pw, nil, domain.RoleSecurityAdministrator, nil, auth.AuditMeta{}); err != nil {
		t.Fatalf("create secadmin: %v", err)
	}
	token, status := env.login(t, "nc-secadmin", pw)
	if status != http.StatusOK {
		t.Fatalf("login = %d, want 200", status)
	}

	t.Run("channel list carries no configuration value", func(t *testing.T) {
		resp := env.do(t, http.MethodGet, "/api/v1/notification-channels", token, nil)
		if resp.StatusCode != http.StatusOK {
			t.Fatalf("list channels = %d, want 200", resp.StatusCode)
		}
		raw, err := io.ReadAll(resp.Body)
		if err != nil {
			t.Fatalf("read body: %v", err)
		}
		body := string(raw)
		for _, secret := range []string{fake.smtpHost, fake.smtpPassword, fake.webhookURL} {
			if strings.Contains(body, secret) {
				t.Errorf("channel response leaks a configured value (%q)", secret)
			}
		}
		// 응답 키는 channel/enabled/reason뿐이어야 한다.
		var out struct {
			Items []map[string]any `json:"items"`
		}
		if err := json.Unmarshal(raw, &out); err != nil {
			t.Fatalf("decode channels: %v", err)
		}
		if len(out.Items) != 4 {
			t.Fatalf("channels = %d, want 4", len(out.Items))
		}
		for _, item := range out.Items {
			for key := range item {
				switch key {
				case "channel", "enabled", "reason":
				default:
					t.Errorf("channel response has unexpected key %q", key)
				}
			}
		}
	})

	// 테스트 발송: 비활성 채널은 skipped + 사유, 활성 채널은 sent.
	body := policyBody("배선 테스트 정책")
	body["channels"] = []string{"inbox", "webhook"}
	resp := env.do(t, http.MethodPost, "/api/v1/notification-policies", token, body)
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("create policy = %d, want 201", resp.StatusCode)
	}
	policyID, _ := decodePolicy(t, resp)["id"].(string)

	t.Run("test send reports per-channel results and audits success", func(t *testing.T) {
		resp := env.do(t, http.MethodPost, "/api/v1/notification-policies/"+policyID+"/test", token, map[string]any{})
		if resp.StatusCode != http.StatusOK {
			t.Fatalf("test send = %d, want 200", resp.StatusCode)
		}
		var out struct {
			Results []testChannelResult `json:"results"`
		}
		if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
			t.Fatalf("decode test result: %v", err)
		}
		got := map[string]testChannelResult{}
		for _, r := range out.Results {
			got[r.Channel] = r
		}
		if got["inbox"].Status != testResultSent {
			t.Errorf("inbox result = %+v, want sent", got["inbox"])
		}
		if got["webhook"].Status != testResultSkipped || got["webhook"].Reason == "" {
			t.Errorf("webhook result = %+v, want skipped with a reason", got["webhook"])
		}
		if n := env.auditCount(t, "notification.test", domain.AuditSuccess); n != 1 {
			t.Errorf("notification.test success audit events = %d, want 1", n)
		}
	})

	t.Run("channel not used by the policy is 400", func(t *testing.T) {
		resp := env.do(t, http.MethodPost, "/api/v1/notification-policies/"+policyID+"/test", token,
			map[string]any{"channel": "email"})
		if resp.StatusCode != http.StatusBadRequest {
			t.Errorf("test with foreign channel = %d, want 400", resp.StatusCode)
		}
	})

	t.Run("send failure is reported and audited as failure", func(t *testing.T) {
		fake.sendErr = errors.New("smtp: connection refused")
		defer func() { fake.sendErr = nil }()
		resp := env.do(t, http.MethodPost, "/api/v1/notification-policies/"+policyID+"/test", token,
			map[string]any{"channel": "inbox"})
		if resp.StatusCode != http.StatusOK {
			t.Fatalf("test send = %d, want 200", resp.StatusCode)
		}
		var out struct {
			Results []testChannelResult `json:"results"`
		}
		if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
			t.Fatalf("decode test result: %v", err)
		}
		if len(out.Results) != 1 || out.Results[0].Status != testResultFailed {
			t.Fatalf("results = %+v, want one failed entry", out.Results)
		}
		if n := env.auditCount(t, "notification.test", domain.AuditFailure); n != 1 {
			t.Errorf("notification.test failure audit events = %d, want 1", n)
		}
	})
}

// 발송 이력 조회: 필터와 "왜 안 왔는지"(skipped 사유) 노출을 확인한다.
func TestNotificationDeliveriesAPI(t *testing.T) {
	env := newTestEnv(t)
	env.cleanNotifications(t)
	ctx := t.Context()

	pw := randomPassword(t)
	if _, err := env.svc.CreateUser(ctx, "nd-auditor", pw, nil, domain.RoleAuditor, nil, auth.AuditMeta{}); err != nil {
		t.Fatalf("create auditor: %v", err)
	}
	token, status := env.login(t, "nd-auditor", pw)
	if status != http.StatusOK {
		t.Fatalf("login = %d, want 200", status)
	}

	eventID := env.insertNotificationEvent(t, "certificate.expiring:delivery:30")
	for _, d := range []struct{ channel, recipient, status, lastErr string }{
		{"inbox", "user-1", "sent", ""},
		{"email", "", "skipped", "no recipient resolved for this channel"},
	} {
		if _, err := env.pool.Exec(`
			INSERT INTO notification_deliveries (event_id, channel, recipient, status, last_error)
			VALUES ($1::uuid, $2, $3, $4, nullif($5, ''))`,
			eventID, d.channel, d.recipient, d.status, d.lastErr); err != nil {
			t.Fatalf("insert delivery: %v", err)
		}
	}

	type deliveryList struct {
		Items []struct {
			Channel   string  `json:"channel"`
			Status    string  `json:"status"`
			LastError *string `json:"last_error"`
		} `json:"items"`
		Total int `json:"total"`
	}
	get := func(query string) deliveryList {
		t.Helper()
		resp := env.do(t, http.MethodGet, "/api/v1/notification-deliveries"+query, token, nil)
		if resp.StatusCode != http.StatusOK {
			t.Fatalf("list deliveries%s = %d, want 200", query, resp.StatusCode)
		}
		var out deliveryList
		if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
			t.Fatalf("decode deliveries: %v", err)
		}
		return out
	}

	if all := get(""); all.Total != 2 {
		t.Errorf("deliveries total = %d, want 2", all.Total)
	}
	skipped := get("?status=skipped")
	if skipped.Total != 1 || len(skipped.Items) != 1 {
		t.Fatalf("skipped total = %d, want 1", skipped.Total)
	}
	if skipped.Items[0].LastError == nil || *skipped.Items[0].LastError == "" {
		t.Error("skipped delivery has no reason; '왜 안 왔는지'를 답할 수 없다")
	}
	if byChannel := get("?channel=inbox"); byChannel.Total != 1 {
		t.Errorf("inbox deliveries = %d, want 1", byChannel.Total)
	}
	if byEvent := get("?event_id=" + eventID); byEvent.Total != 2 {
		t.Errorf("deliveries for event = %d, want 2", byEvent.Total)
	}

	for _, query := range []string{"?status=bogus", "?channel=bogus", "?event_id=not-a-uuid", "?page=0"} {
		resp := env.do(t, http.MethodGet, "/api/v1/notification-deliveries"+query, token, nil)
		if resp.StatusCode != http.StatusBadRequest {
			t.Errorf("list deliveries%s = %d, want 400", query, resp.StatusCode)
		}
	}
}
