package notify

import (
	"context"
	"path/filepath"
	"strings"
	"testing"
)

// 테스트 전용 가짜 secret. 실제 값이 아니며, Enabled 사유·에러에 새어 나오지 않는지 확인하는 데만 쓴다.
const (
	fakePassword = "pw-must-never-be-logged-8f2a"
	fakeSecret   = "hmac-must-never-be-logged-4b71"
	fakeToken    = "token-must-never-be-logged-9c3d"
)

func assertNoSecrets(t *testing.T, what, s string) {
	t.Helper()
	for _, secret := range []string{fakePassword, fakeSecret, fakeToken} {
		if strings.Contains(s, secret) {
			t.Errorf("%s leaked a secret: %q", what, s)
		}
	}
}

func TestEmailDisabledWhenUnconfigured(t *testing.T) {
	cases := []struct {
		name       string
		cfg        EmailConfig
		wantReason string
	}{
		{"no host", EmailConfig{Password: fakePassword, From: "autocert@example.test"}, "SMTP_HOST"},
		{"no from", EmailConfig{Host: "smtp.example.test", Password: fakePassword}, "SMTP_FROM"},
		{"bad tls mode", EmailConfig{Host: "smtp.example.test", From: "a@example.test", Password: fakePassword, TLSMode: "ssl"}, "SMTP_TLS_MODE"},
		{"missing ca file", EmailConfig{Host: "smtp.example.test", From: "a@example.test", Password: fakePassword, CAFile: filepath.Join(t.TempDir(), "absent.pem")}, "not readable"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			n := NewEmail(tc.cfg)
			ok, reason := n.Enabled()
			if ok {
				t.Fatal("channel must be disabled")
			}
			if !strings.Contains(reason, tc.wantReason) {
				t.Errorf("reason = %q, want it to mention %q", reason, tc.wantReason)
			}
			assertNoSecrets(t, "email Enabled reason", reason)

			// 비활성 채널로의 발송은 재시도해도 같으므로 영구 실패다.
			err := n.Send(context.Background(), Message{Recipient: "ops@example.test"})
			if !IsPermanent(err) {
				t.Errorf("Send on a disabled channel must be permanent, got %v", err)
			}
			assertNoSecrets(t, "email Send error", err.Error())
		})
	}
}

func TestEmailEnabledWithMinimalConfig(t *testing.T) {
	n := NewEmail(EmailConfig{Host: "smtp.example.test", From: "autocert@example.test", Password: fakePassword})
	if ok, reason := n.Enabled(); !ok {
		t.Fatalf("channel must be enabled, reason: %s", reason)
	}
	if n.tlsMode != TLSModeStartTLS {
		t.Errorf("default tls mode = %q, want %q", n.tlsMode, TLSModeStartTLS)
	}
	if n.port != 25 {
		t.Errorf("default starttls port = %d, want 25", n.port)
	}
	if p := NewEmail(EmailConfig{Host: "h", From: "a@b.test", TLSMode: TLSModeTLS}).port; p != 465 {
		t.Errorf("default implicit tls port = %d, want 465", p)
	}
}

func TestEmailRejectsRecipientlessMessage(t *testing.T) {
	n := NewEmail(EmailConfig{Host: "smtp.example.test", From: "autocert@example.test"})
	if err := n.Send(context.Background(), Message{Title: "t"}); !IsPermanent(err) {
		t.Errorf("missing recipient must be permanent, got %v", err)
	}
}

func TestWebhookDisabledWhenUnconfigured(t *testing.T) {
	cases := []struct {
		name       string
		cfg        WebhookConfig
		wantReason string
	}{
		{"no url", WebhookConfig{Secret: fakeSecret, AuthHeader: "Authorization: Bearer " + fakeToken}, "WEBHOOK_URL"},
		{"bad scheme", WebhookConfig{URL: "ftp://example.test/hook", Secret: fakeSecret}, "http://"},
		{"malformed auth header", WebhookConfig{URL: "https://example.test/hook", AuthHeader: "Bearer " + fakeToken}, "WEBHOOK_AUTH_HEADER"},
		{"missing ca file", WebhookConfig{URL: "https://example.test/hook", Secret: fakeSecret, CAFile: filepath.Join(t.TempDir(), "absent.pem")}, "not readable"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			n := NewWebhook(tc.cfg)
			ok, reason := n.Enabled()
			if ok {
				t.Fatal("channel must be disabled")
			}
			if !strings.Contains(reason, tc.wantReason) {
				t.Errorf("reason = %q, want it to mention %q", reason, tc.wantReason)
			}
			assertNoSecrets(t, "webhook Enabled reason", reason)

			err := n.Send(context.Background(), Message{Title: "t"})
			if !IsPermanent(err) {
				t.Errorf("Send on a disabled channel must be permanent, got %v", err)
			}
			assertNoSecrets(t, "webhook Send error", err.Error())
		})
	}
}

func TestSyslogDisabledWhenUnconfigured(t *testing.T) {
	cases := []struct {
		name       string
		cfg        SyslogConfig
		wantReason string
	}{
		{"no addr", SyslogConfig{}, "SYSLOG_ADDR"},
		{"bad network", SyslogConfig{Addr: "127.0.0.1:514", Network: "sctp"}, "SYSLOG_NETWORK"},
		{"bad facility name", SyslogConfig{Addr: "127.0.0.1:514", Facility: "local9"}, "SYSLOG_FACILITY"},
		{"facility out of range", SyslogConfig{Addr: "127.0.0.1:514", Facility: "42"}, "SYSLOG_FACILITY"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			n := NewSyslog(tc.cfg)
			ok, reason := n.Enabled()
			if ok {
				t.Fatal("channel must be disabled")
			}
			if !strings.Contains(reason, tc.wantReason) {
				t.Errorf("reason = %q, want it to mention %q", reason, tc.wantReason)
			}
			if err := n.Send(context.Background(), Message{Title: "t"}); !IsPermanent(err) {
				t.Errorf("Send on a disabled channel must be permanent, got %v", err)
			}
		})
	}
}

func TestSyslogDefaults(t *testing.T) {
	n := NewSyslog(SyslogConfig{Addr: "127.0.0.1:514"})
	if ok, reason := n.Enabled(); !ok {
		t.Fatalf("channel must be enabled, reason: %s", reason)
	}
	if n.network != "udp" {
		t.Errorf("default network = %q, want udp", n.network)
	}
	if n.appName != "autocert" {
		t.Errorf("default app name = %q, want autocert", n.appName)
	}
	if n.facility != 16 {
		t.Errorf("default facility = %d, want 16 (local0)", n.facility)
	}
}

func TestParseFacility(t *testing.T) {
	cases := []struct {
		in      string
		want    int
		wantErr bool
	}{
		{"", 16, false},
		{"local0", 16, false},
		{"LOCAL7", 23, false},
		{"daemon", 3, false},
		{"0", 0, false},
		{"23", 23, false},
		{"24", 0, true},
		{"-1", 0, true},
		{"nope", 0, true},
	}
	for _, tc := range cases {
		got, err := parseFacility(tc.in)
		if tc.wantErr {
			if err == nil {
				t.Errorf("parseFacility(%q) expected an error", tc.in)
			}
			continue
		}
		if err != nil || got != tc.want {
			t.Errorf("parseFacility(%q) = %d, %v; want %d, nil", tc.in, got, err, tc.want)
		}
	}
}

func TestSanitizeHeaderValueDropsCRLF(t *testing.T) {
	if got := sanitizeHeaderValue("subject\r\nBcc: attacker@example.test"); strings.ContainsAny(got, "\r\n") {
		t.Errorf("header value must not keep CR/LF: %q", got)
	}
}
