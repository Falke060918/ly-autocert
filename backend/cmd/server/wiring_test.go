// wiring_test.go — api.NotificationChannels 어댑터 회귀 테스트.
//
// 핵심 규약 둘:
//  1. 비활성 사유의 우선순위는 NOTIFY_ENABLED → config 설정 오류 → 채널 자신의
//     Enabled()다 (디스패처가 딜리버리를 건너뛸 때 보는 순서와 같아야 한다).
//  2. 상태 조회와 테스트 발송 결과에는 어떤 설정값도 실리지 않는다 — 이 문자열은
//     API 응답과 감사 로그로 그대로 나간다.
package main

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"net"
	"strings"
	"testing"
	"time"

	"github.com/Falke060918/INHERIT_AutoCert/backend/internal/config"
	"github.com/Falke060918/INHERIT_AutoCert/backend/internal/notify"
)

// 테스트 전용 가짜 설정값. 실제 값이 아니며, 상태·에러 문자열에 새어 나오지 않는지
// 확인하는 데만 쓴다 (보안 하드 규칙 4 — 실제 키·자격증명을 커밋하지 않는다).
const (
	testSMTPHost      = "smtp.internal.example"
	testSMTPUser      = "svc-notify-must-never-appear"
	testSMTPPassword  = "pw-must-never-appear-1a2b"
	testSMTPFrom      = "autocert@corp.example"
	testWebhookHost   = "hooks.internal.example"
	testWebhookPath   = "/services/T0KEN-must-never-appear"
	testWebhookSecret = "whsec-must-never-appear-3c4d"
	testWebhookToken  = "bearer-must-never-appear-5e6f"
	testSyslogAddr    = "syslog.internal.example:5514"
)

func testLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, nil))
}

// stubInboxStore는 알림함 삽입 여부만 세는 저장 대역이다.
type stubInboxStore struct{ inserts int }

func (s *stubInboxStore) InsertInboxItem(context.Context, notify.InboxEntry) (bool, error) {
	s.inserts++
	return true, nil
}

// statusReasons는 채널 → (활성 여부, 사유)로 상태를 펼친다.
func statusReasons(c *notifyChannels) map[string]struct {
	enabled bool
	reason  string
} {
	out := map[string]struct {
		enabled bool
		reason  string
	}{}
	for _, st := range c.Status() {
		out[st.Channel] = struct {
			enabled bool
			reason  string
		}{st.Enabled, st.Reason}
	}
	return out
}

// TestNotifyChannelStatusReasonPriority는 사유 우선순위 표를 고정한다.
// 세 사유가 동시에 성립하도록 SMTP를 미설정(SMTP_HOST 없음)으로 두고
// NotifyChannelErrors에도 항목을 넣은 뒤, NOTIFY_ENABLED만 바꿔 가며 본다.
func TestNotifyChannelStatusReasonPriority(t *testing.T) {
	const configErr = "SMTP_PORT must be a number between 1 and 65535"
	cfg := config.Config{
		Notify: config.NotifyConfig{
			Enabled: true,
			// SMTP_HOST가 비어 채널 스스로도 비활성이다.
			Email: notify.EmailConfig{Password: testSMTPPassword},
		},
		NotifyChannelErrors: map[string]string{notify.ChannelEmail: configErr},
	}

	t.Run("NOTIFY_ENABLED=false wins", func(t *testing.T) {
		off := cfg
		off.Notify.Enabled = false
		// startNotify가 비활성일 때 하는 것과 같다: notifiers 없이 어댑터만 만든다.
		got := statusReasons(newNotifyChannels(off, nil))
		for _, ch := range notifyChannelOrder {
			if got[ch].enabled || got[ch].reason != notifyDisabledReason {
				t.Errorf("%s = (%v, %q), want (false, %q)", ch, got[ch].enabled, got[ch].reason, notifyDisabledReason)
			}
		}
	})

	t.Run("config error wins over the channel's own reason", func(t *testing.T) {
		notifiers := newNotifiers(cfg.Notify, testLogger(), &stubInboxStore{})
		got := statusReasons(newNotifyChannels(cfg, notifiers))
		if got[notify.ChannelEmail].enabled || got[notify.ChannelEmail].reason != configErr {
			t.Errorf("email = (%v, %q), want (false, %q)",
				got[notify.ChannelEmail].enabled, got[notify.ChannelEmail].reason, configErr)
		}
	})

	t.Run("the channel's own reason is last", func(t *testing.T) {
		noConfigErr := cfg
		noConfigErr.NotifyChannelErrors = nil
		notifiers := newNotifiers(noConfigErr.Notify, testLogger(), &stubInboxStore{})
		got := statusReasons(newNotifyChannels(noConfigErr, notifiers))
		if got[notify.ChannelEmail].enabled || !strings.Contains(got[notify.ChannelEmail].reason, "SMTP_HOST") {
			t.Errorf("email = (%v, %q), want a disabled channel naming SMTP_HOST",
				got[notify.ChannelEmail].enabled, got[notify.ChannelEmail].reason)
		}
		// 설정이 다 갖춰진 채널은 활성이고 사유가 없다.
		if !got[notify.ChannelInbox].enabled || got[notify.ChannelInbox].reason != "" {
			t.Errorf("inbox = (%v, %q), want (true, \"\")",
				got[notify.ChannelInbox].enabled, got[notify.ChannelInbox].reason)
		}
	})

	t.Run("an unwired channel is reported, not hidden", func(t *testing.T) {
		only := []notify.Notifier{notify.NewInbox(notify.InboxConfig{Store: &stubInboxStore{}})}
		bare := cfg
		bare.NotifyChannelErrors = nil
		got := statusReasons(newNotifyChannels(bare, only))
		if got[notify.ChannelSyslog].enabled || got[notify.ChannelSyslog].reason != notifyChannelMissingReason {
			t.Errorf("syslog = (%v, %q), want (false, %q)",
				got[notify.ChannelSyslog].enabled, got[notify.ChannelSyslog].reason, notifyChannelMissingReason)
		}
	})
}

// configuredCfg는 모든 채널에 특징적인 설정값이 들어간 설정이다.
func configuredCfg() config.Config {
	return config.Config{
		Notify: config.NotifyConfig{
			Enabled: true,
			Email: notify.EmailConfig{
				Host: testSMTPHost, Port: 2525,
				Username: testSMTPUser, Password: testSMTPPassword,
				From: testSMTPFrom,
				// 잘못된 값이라 채널이 비활성이 되고 사유 문자열이 만들어진다.
				TLSMode: "ssl",
			},
			Webhook: notify.WebhookConfig{
				URL:        "https://" + testWebhookHost + testWebhookPath,
				Secret:     testWebhookSecret,
				AuthHeader: "Authorization: Bearer " + testWebhookToken,
			},
			Syslog: notify.SyslogConfig{Network: "sctp", Addr: testSyslogAddr},
		},
	}
}

// forbiddenValues는 밖으로 나가면 안 되는 설정값 전부다.
func forbiddenValues() []string {
	return []string{
		testSMTPHost, testSMTPUser, testSMTPPassword, testSMTPFrom, "2525",
		testWebhookHost, testWebhookPath, testWebhookSecret, testWebhookToken,
		testSyslogAddr, "5514",
	}
}

func assertNoConfigValues(t *testing.T, what, s string, values []string) {
	t.Helper()
	for _, v := range values {
		if v != "" && strings.Contains(s, v) {
			t.Errorf("%s leaked a configuration value %q: %s", what, v, s)
		}
	}
}

// TestNotifyChannelStatusCarriesNoConfigValues는 상태 응답 전체를 문자열로 펼쳐
// SMTP 호스트·계정·비밀번호, 웹훅 URL·시크릿·인증 헤더, syslog 주소가 없음을 본다.
func TestNotifyChannelStatusCarriesNoConfigValues(t *testing.T) {
	cfg := configuredCfg()
	notifiers := newNotifiers(cfg.Notify, testLogger(), &stubInboxStore{})
	channels := newNotifyChannels(cfg, notifiers)

	rendered := fmt.Sprintf("%+v", channels.Status())
	assertNoConfigValues(t, "channel status", rendered, forbiddenValues())

	// 통과가 공허하지 않도록: 사유는 실제로 채워져 있고 변수 이름을 지목한다.
	got := statusReasons(channels)
	if got[notify.ChannelEmail].enabled || !strings.Contains(got[notify.ChannelEmail].reason, "SMTP_TLS_MODE") {
		t.Errorf("email reason = %q, want it to name SMTP_TLS_MODE", got[notify.ChannelEmail].reason)
	}
	if got[notify.ChannelSyslog].enabled || !strings.Contains(got[notify.ChannelSyslog].reason, "SYSLOG_NETWORK") {
		t.Errorf("syslog reason = %q, want it to name SYSLOG_NETWORK", got[notify.ChannelSyslog].reason)
	}
	if !got[notify.ChannelWebhook].enabled {
		t.Errorf("webhook = (%v, %q), want an enabled channel", got[notify.ChannelWebhook].enabled, got[notify.ChannelWebhook].reason)
	}
}

// TestNotifyInboxTestSendSucceeds는 알림함 테스트 발송이 실패로 보이지 않는지 본다.
// 알림함은 채울 설정이 없는 채널이라 확인할 것이 없다 — 예전에는 수신자 사용자 id가
// 없다는 이유로 항상 failed로 끝나 고장난 것처럼 보였다.
func TestNotifyInboxTestSendSucceeds(t *testing.T) {
	cfg := configuredCfg()
	store := &stubInboxStore{}
	channels := newNotifyChannels(cfg, newNotifiers(cfg.Notify, testLogger(), store))

	if err := channels.SendTest(context.Background(), notify.ChannelInbox, ""); err != nil {
		t.Fatalf("SendTest(inbox) = %v, want nil", err)
	}
	if store.inserts != 0 {
		t.Errorf("inbox test send inserted %d rows, want 0 (there is no real notification event)", store.inserts)
	}
}

// TestNotifySendTestErrorCarriesNoConfigValues는 실패한 테스트 발송의 에러에도
// 설정값이 없음을 본다. 이 문자열은 API 응답과 감사 로그에 그대로 실린다.
func TestNotifySendTestErrorCarriesNoConfigValues(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	addr := ln.Addr().String()
	_ = ln.Close() // 아무도 듣지 않는 주소를 만든다

	cfg := configuredCfg()
	cfg.Notify.Webhook = notify.WebhookConfig{
		URL:        "http://" + addr + testWebhookPath,
		Secret:     testWebhookSecret,
		AuthHeader: "Authorization: Bearer " + testWebhookToken,
		Timeout:    2 * time.Second,
	}
	channels := newNotifyChannels(cfg, newNotifiers(cfg.Notify, testLogger(), &stubInboxStore{}))

	sendErr := channels.SendTest(context.Background(), notify.ChannelWebhook, "")
	if sendErr == nil {
		t.Fatal("SendTest(webhook) = nil, want an error for an unreachable endpoint")
	}
	assertNoConfigValues(t, "webhook test send error", sendErr.Error(),
		append(forbiddenValues(), addr, cfg.Notify.Webhook.URL))
	if !strings.Contains(sendErr.Error(), "WEBHOOK_URL") {
		t.Errorf("error must name the variable to check: %q", sendErr)
	}
}
