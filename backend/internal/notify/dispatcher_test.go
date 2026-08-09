package notify

// 디스패처 검증 (docs/design/05-notifications.md 6·11.4).
// - 임시 실패는 재시도로, 영구 실패·미분류 실패는 즉시 종결로 간다.
// - 보낼 수 없는 채널(설정 오류·어댑터 없음·비활성)의 딜리버리는 skipped로 끝난다.
// - 한 채널의 실패가 다른 채널의 발송을 막지 않는다.
// - ctx 취소 시 진행 중 한 건을 마치고 깨끗이 멈춘다.
// - 로그·저장된 오류 어디에도 수신자 주소가 남지 않는다.

import (
	"bytes"
	"context"
	"errors"
	"log/slog"
	"strings"
	"sync"
	"testing"
	"time"
)

// ===== 테스트 더블 =====

type failedCall struct {
	id        string
	reason    string
	permanent bool
}

type skippedCall struct {
	id     string
	reason string
}

// fakeDispatchStore는 리스 큐와 결과 기록을 흉내 낸다.
type fakeDispatchStore struct {
	mu       sync.Mutex
	batches  [][]Delivery
	leaseErr error

	leases  int
	sent    []string
	failed  []failedCall
	skipped []skippedCall
}

func (f *fakeDispatchStore) LeaseDeliveries(_ context.Context, _ int) ([]Delivery, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.leases++
	if f.leaseErr != nil {
		return nil, f.leaseErr
	}
	if len(f.batches) == 0 {
		return nil, nil
	}
	batch := f.batches[0]
	f.batches = f.batches[1:]
	return batch, nil
}

func (f *fakeDispatchStore) MarkDeliverySent(_ context.Context, id string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.sent = append(f.sent, id)
	return nil
}

func (f *fakeDispatchStore) MarkDeliveryFailed(_ context.Context, id, sendErr string, permanent bool) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.failed = append(f.failed, failedCall{id: id, reason: sendErr, permanent: permanent})
	return nil
}

func (f *fakeDispatchStore) MarkDeliverySkipped(_ context.Context, id, reason string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.skipped = append(f.skipped, skippedCall{id: id, reason: reason})
	return nil
}

func (f *fakeDispatchStore) snapshot() ([]string, []failedCall, []skippedCall, int) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]string(nil), f.sent...),
		append([]failedCall(nil), f.failed...),
		append([]skippedCall(nil), f.skipped...),
		f.leases
}

// fakeChannel은 채널 어댑터를 흉내 낸다.
type fakeChannel struct {
	channel  string
	disabled string
	err      error
	// block이 nil이 아니면 Send가 닫힐 때까지 기다린다 (취소 시나리오용).
	block chan struct{}

	mu   sync.Mutex
	sent []Message
}

func (c *fakeChannel) Channel() string { return c.channel }

func (c *fakeChannel) Enabled() (bool, string) {
	if c.disabled != "" {
		return false, c.disabled
	}
	return true, ""
}

func (c *fakeChannel) Send(_ context.Context, m Message) error {
	if c.block != nil {
		<-c.block
	}
	c.mu.Lock()
	c.sent = append(c.sent, m)
	c.mu.Unlock()
	return c.err
}

func (c *fakeChannel) messages() []Message {
	c.mu.Lock()
	defer c.mu.Unlock()
	return append([]Message(nil), c.sent...)
}

// testDelivery는 기본 딜리버리 1건이다.
func testDelivery(id, channel, recipient string) Delivery {
	return Delivery{
		ID:        id,
		EventID:   "e-" + id,
		Channel:   channel,
		Recipient: recipient,
		EventType: EventCertExpiring,
		Severity:  SeverityWarning,
		Facts: CertExpiryFacts{
			CertificateID: "cert-" + id,
			CommonName:    "app.example.test",
			NotAfter:      time.Now().Add(72 * time.Hour),
			DaysRemaining: 3,
		},
	}
}

// bufferLogger는 로그 출력을 캡처한다.
func bufferLogger() (*slog.Logger, *bytes.Buffer) {
	buf := &bytes.Buffer{}
	h := slog.NewJSONHandler(buf, &slog.HandlerOptions{Level: slog.LevelDebug})
	return slog.New(h), buf
}

// ===== 결과 기록 =====

func TestDispatcherMarksSentOnSuccess(t *testing.T) {
	st := &fakeDispatchStore{batches: [][]Delivery{{testDelivery("d1", ChannelInbox, "user-1")}}}
	ch := &fakeChannel{channel: ChannelInbox}
	d := NewDispatcher(DispatcherConfig{Store: st, Notifiers: []Notifier{ch}, Logger: slog.New(slog.DiscardHandler)})

	n, err := d.RunOnce(context.Background())
	if err != nil || n != 1 {
		t.Fatalf("RunOnce() = (%d, %v), want (1, nil)", n, err)
	}
	sent, failed, skipped, _ := st.snapshot()
	if len(sent) != 1 || sent[0] != "d1" {
		t.Errorf("sent = %v, want [d1]", sent)
	}
	if len(failed) != 0 || len(skipped) != 0 {
		t.Errorf("failed=%v skipped=%v, want both empty", failed, skipped)
	}
}

func TestDispatcherClassifiesFailures(t *testing.T) {
	cases := []struct {
		name          string
		err           error
		wantPermanent bool
	}{
		{"temporary retries", Temporaryf("smtp connection refused"), false},
		{"permanent fails now", Permanentf("mailbox does not exist"), true},
		// 분류되지 않은 에러를 무한 재시도하면 큐가 막힌다 (errors.go 규약).
		{"unclassified fails now", errors.New("something odd"), true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			st := &fakeDispatchStore{batches: [][]Delivery{{testDelivery("d1", ChannelEmail, "ops@example.test")}}}
			ch := &fakeChannel{channel: ChannelEmail, err: tc.err}
			d := NewDispatcher(DispatcherConfig{Store: st, Notifiers: []Notifier{ch}, Logger: slog.New(slog.DiscardHandler)})

			if _, err := d.RunOnce(context.Background()); err != nil {
				t.Fatalf("RunOnce() = %v", err)
			}
			sent, failed, _, _ := st.snapshot()
			if len(sent) != 0 {
				t.Errorf("sent = %v, want empty", sent)
			}
			if len(failed) != 1 {
				t.Fatalf("failed = %v, want 1 entry", failed)
			}
			if failed[0].permanent != tc.wantPermanent {
				t.Errorf("permanent = %v, want %v", failed[0].permanent, tc.wantPermanent)
			}
		})
	}
}

// ===== 보낼 수 없는 채널은 skipped로 종결 =====

func TestDispatcherSkipsUnavailableChannels(t *testing.T) {
	cases := []struct {
		name       string
		notifiers  []Notifier
		errs       map[string]string
		wantReason string
	}{
		{
			name:       "config error disables the channel",
			notifiers:  []Notifier{&fakeChannel{channel: ChannelEmail}},
			errs:       map[string]string{ChannelEmail: "SMTP_PORT must be a number"},
			wantReason: "SMTP_PORT",
		},
		{
			name:       "no adapter wired",
			notifiers:  nil,
			wantReason: "no adapter is wired",
		},
		{
			name:       "adapter reports disabled",
			notifiers:  []Notifier{&fakeChannel{channel: ChannelEmail, disabled: "SMTP_HOST is not set"}},
			wantReason: "SMTP_HOST is not set",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			st := &fakeDispatchStore{batches: [][]Delivery{{testDelivery("d1", ChannelEmail, "ops@example.test")}}}
			d := NewDispatcher(DispatcherConfig{
				Store: st, Notifiers: tc.notifiers, ChannelErrors: tc.errs,
				Logger: slog.New(slog.DiscardHandler),
			})
			if _, err := d.RunOnce(context.Background()); err != nil {
				t.Fatalf("RunOnce() = %v", err)
			}
			sent, failed, skipped, _ := st.snapshot()
			if len(sent) != 0 || len(failed) != 0 {
				t.Errorf("sent=%v failed=%v, want both empty", sent, failed)
			}
			if len(skipped) != 1 {
				t.Fatalf("skipped = %v, want 1 entry", skipped)
			}
			if !strings.Contains(skipped[0].reason, tc.wantReason) {
				t.Errorf("skip reason %q does not mention %q", skipped[0].reason, tc.wantReason)
			}
			// 시도 자체를 하지 않는다.
			for _, n := range tc.notifiers {
				if fc, ok := n.(*fakeChannel); ok && len(fc.messages()) != 0 {
					t.Errorf("disabled channel was still sent to")
				}
			}
		})
	}
}

func TestDispatcherChannelFailureDoesNotBlockOtherChannels(t *testing.T) {
	// 딜리버리는 채널×수신자당 1행이므로 죽은 SMTP 하나가 알림함·웹훅을 막으면 안 된다.
	st := &fakeDispatchStore{batches: [][]Delivery{{
		testDelivery("d-mail", ChannelEmail, "ops@example.test"),
		testDelivery("d-inbox", ChannelInbox, "user-1"),
		testDelivery("d-hook", ChannelWebhook, ""),
	}}}
	email := &fakeChannel{channel: ChannelEmail, err: Temporaryf("dial tcp: connection refused")}
	inbox := &fakeChannel{channel: ChannelInbox}
	hook := &fakeChannel{channel: ChannelWebhook}
	d := NewDispatcher(DispatcherConfig{
		Store: st, Notifiers: []Notifier{email, inbox, hook},
		ChannelErrors: map[string]string{ChannelSyslog: "SYSLOG_NETWORK must be udp or tcp"},
		Logger:        slog.New(slog.DiscardHandler),
	})

	n, err := d.RunOnce(context.Background())
	if err != nil || n != 3 {
		t.Fatalf("RunOnce() = (%d, %v), want (3, nil)", n, err)
	}
	sent, failed, _, _ := st.snapshot()
	if len(sent) != 2 {
		t.Errorf("sent = %v, want the two healthy channels", sent)
	}
	if len(failed) != 1 || failed[0].id != "d-mail" {
		t.Errorf("failed = %v, want only d-mail", failed)
	}
}

// ===== 수신자 주소는 어디에도 남지 않는다 =====

func TestDispatcherNeverLogsRecipient(t *testing.T) {
	const addr = "secret-person@example.test"
	st := &fakeDispatchStore{batches: [][]Delivery{{
		testDelivery("d1", ChannelEmail, addr),
		testDelivery("d2", ChannelInbox, addr),
	}}}
	// 어댑터가 규칙을 어기고 주소를 에러에 실어 보내는 상황을 가정한다.
	email := &fakeChannel{channel: ChannelEmail, err: Temporaryf("recipient %s rejected", addr)}
	inbox := &fakeChannel{channel: ChannelInbox}
	log, buf := bufferLogger()
	d := NewDispatcher(DispatcherConfig{Store: st, Notifiers: []Notifier{email, inbox}, Logger: log})

	if _, err := d.RunOnce(context.Background()); err != nil {
		t.Fatalf("RunOnce() = %v", err)
	}
	if strings.Contains(buf.String(), addr) {
		t.Errorf("log leaked the recipient address:\n%s", buf.String())
	}
	// 진단은 남아야 한다 — 무엇이 실패했는지는 delivery_id·channel·event_id로 찾는다.
	if !strings.Contains(buf.String(), "d1") || !strings.Contains(buf.String(), ChannelEmail) {
		t.Errorf("log lost the delivery identity:\n%s", buf.String())
	}
	_, failed, _, _ := st.snapshot()
	if len(failed) != 1 {
		t.Fatalf("failed = %v, want 1 entry", failed)
	}
	if strings.Contains(failed[0].reason, addr) {
		t.Errorf("stored last_error leaked the recipient address: %q", failed[0].reason)
	}
	if !strings.Contains(failed[0].reason, recipientMark) {
		t.Errorf("stored last_error = %q, want the address replaced by %q", failed[0].reason, recipientMark)
	}
}

// ===== 메시지 조립 =====

func TestDispatcherMessageCarriesEventIDAndDeepLink(t *testing.T) {
	del := testDelivery("d1", ChannelInbox, "user-1")
	st := &fakeDispatchStore{batches: [][]Delivery{{del}}}
	ch := &fakeChannel{channel: ChannelInbox}
	d := NewDispatcher(DispatcherConfig{
		Store: st, Notifiers: []Notifier{ch},
		BaseURL: "https://autocert.example.test/", Logger: slog.New(slog.DiscardHandler),
	})

	if _, err := d.RunOnce(context.Background()); err != nil {
		t.Fatalf("RunOnce() = %v", err)
	}
	msgs := ch.messages()
	if len(msgs) != 1 {
		t.Fatalf("message count = %d, want 1", len(msgs))
	}
	m := msgs[0]
	if got := dataString(m.Data, DataKeyEventID); got != del.EventID {
		t.Errorf("Data[%s] = %q, want %q", DataKeyEventID, got, del.EventID)
	}
	if m.Recipient != del.Recipient {
		t.Errorf("Recipient = %q, want %q", m.Recipient, del.Recipient)
	}
	if m.EventID != del.EventType || m.Severity != del.Severity {
		t.Errorf("event/severity = (%q, %q), want (%q, %q)",
			m.EventID, m.Severity, del.EventType, del.Severity)
	}
	wantLink := "https://autocert.example.test/certificates/cert-d1"
	if got := dataString(m.Data, "deep_link"); got != wantLink {
		t.Errorf("deep_link = %q, want %q", got, wantLink)
	}
	// 인증서 원문이 본문에 흘러들지 않는다 (보안 하드 규칙 3).
	if strings.Contains(m.TextBody, "BEGIN CERTIFICATE") {
		t.Errorf("message body contains certificate material")
	}
}

func TestDispatcherOmitsDeepLinkWithoutBaseURL(t *testing.T) {
	st := &fakeDispatchStore{batches: [][]Delivery{{testDelivery("d1", ChannelInbox, "user-1")}}}
	ch := &fakeChannel{channel: ChannelInbox}
	d := NewDispatcher(DispatcherConfig{Store: st, Notifiers: []Notifier{ch}, Logger: slog.New(slog.DiscardHandler)})
	if _, err := d.RunOnce(context.Background()); err != nil {
		t.Fatalf("RunOnce() = %v", err)
	}
	if got := dataString(ch.messages()[0].Data, "deep_link"); got != "" {
		t.Errorf("deep_link = %q, want empty", got)
	}
}

// ===== 리스 오류 · 정지 =====

func TestDispatcherReturnsLeaseError(t *testing.T) {
	st := &fakeDispatchStore{leaseErr: errors.New("connection reset")}
	d := NewDispatcher(DispatcherConfig{Store: st, Logger: slog.New(slog.DiscardHandler)})
	if _, err := d.RunOnce(context.Background()); err == nil {
		t.Fatal("RunOnce() = nil, want the lease error")
	}
}

func TestDispatcherFinishesInFlightSendOnCancel(t *testing.T) {
	// 취소가 와도 진행 중 한 건은 마치고, 나머지 리스는 건드리지 않는다.
	st := &fakeDispatchStore{batches: [][]Delivery{{
		testDelivery("d1", ChannelInbox, "user-1"),
		testDelivery("d2", ChannelInbox, "user-2"),
	}}}
	release := make(chan struct{})
	ch := &fakeChannel{channel: ChannelInbox, block: release}
	d := NewDispatcher(DispatcherConfig{Store: st, Notifiers: []Notifier{ch}, Logger: slog.New(slog.DiscardHandler)})

	ctx, cancel := context.WithCancel(context.Background())
	type result struct {
		n   int
		err error
	}
	res := make(chan result, 1)
	go func() {
		n, err := d.RunOnce(ctx)
		res <- result{n, err}
	}()

	// 첫 건이 발송 중일 때 정지 신호를 준다.
	cancel()
	close(release)

	select {
	case r := <-res:
		if r.err != nil {
			t.Fatalf("RunOnce() = %v", r.err)
		}
		if r.n != 1 {
			t.Fatalf("processed = %d, want 1 (in-flight only)", r.n)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("RunOnce did not return after cancel")
	}
	// 취소된 컨텍스트로도 결과가 기록돼야 한다 — 안 그러면 "보냈는데 기록 못 함"이 된다.
	sent, _, _, _ := st.snapshot()
	if len(sent) != 1 || sent[0] != "d1" {
		t.Errorf("sent = %v, want [d1]", sent)
	}
}

func TestDispatcherStopsCleanly(t *testing.T) {
	st := &fakeDispatchStore{}
	d := NewDispatcher(DispatcherConfig{
		Store: st, Interval: 5 * time.Millisecond,
		Notifiers: []Notifier{&fakeChannel{channel: ChannelInbox}},
		Logger:    slog.New(slog.DiscardHandler),
	})
	stop := d.StartDispatcher(context.Background())

	// 최소 한 번은 큐를 봤는지 확인한 뒤 멈춘다.
	deadline := time.Now().Add(3 * time.Second)
	for {
		if _, _, _, leases := st.snapshot(); leases > 0 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("dispatcher never leased")
		}
		time.Sleep(2 * time.Millisecond)
	}

	done := make(chan struct{})
	go func() {
		stop()
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("stop() did not return")
	}
}
