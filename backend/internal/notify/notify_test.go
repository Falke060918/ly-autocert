package notify

import "testing"

// 각 어댑터가 Notifier를 만족하는지 컴파일 타임에 고정한다.
var (
	_ Notifier = (*EmailNotifier)(nil)
	_ Notifier = (*WebhookNotifier)(nil)
	_ Notifier = (*SyslogNotifier)(nil)
)

func TestChannelNames(t *testing.T) {
	cases := []struct {
		notifier Notifier
		want     string
	}{
		{NewEmail(EmailConfig{}), ChannelEmail},
		{NewWebhook(WebhookConfig{}), ChannelWebhook},
		{NewSyslog(SyslogConfig{}), ChannelSyslog},
	}
	for _, tc := range cases {
		if got := tc.notifier.Channel(); got != tc.want {
			t.Errorf("Channel() = %q, want %q", got, tc.want)
		}
	}
}
