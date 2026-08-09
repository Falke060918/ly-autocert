package notify

// 알림함 채널 어댑터 검증 (docs/design/05-notifications.md 5.4).
// - Enabled는 언제나 true다 (zero-config — 설치 직후에도 알림이 도착해야 한다).
// - 재시도 대상은 DB 오류뿐이고, 값이 모자란 메시지는 영구 실패다.
// - 중복 삽입(created=false)은 성공으로 흡수한다 (at-least-once, 설계 13).

import (
	"context"
	"errors"
	"strings"
	"testing"
)

// 알림함도 Notifier를 만족한다는 사실을 컴파일 타임에 고정한다.
var _ Notifier = (*InboxNotifier)(nil)

// fakeInboxStore는 알림함 저장을 흉내 낸다.
type fakeInboxStore struct {
	entries []InboxEntry
	created bool
	err     error
}

func (f *fakeInboxStore) InsertInboxItem(_ context.Context, in InboxEntry) (bool, error) {
	f.entries = append(f.entries, in)
	if f.err != nil {
		return false, f.err
	}
	return f.created, nil
}

// inboxMessage는 정상 경로의 메시지 1건이다.
func inboxMessage() Message {
	return Message{
		Title:     "[인증서 만료 D-3] app.example.test",
		TextBody:  "본문",
		Severity:  SeverityError,
		EventID:   EventCertExpiring,
		Recipient: "11111111-1111-1111-1111-111111111111",
		Data: map[string]any{
			DataKeyEventID:       "22222222-2222-2222-2222-222222222222",
			DataKeyCertificateID: "33333333-3333-3333-3333-333333333333",
		},
	}
}

func TestInboxChannelAndAlwaysEnabled(t *testing.T) {
	n := NewInbox(InboxConfig{})
	if got := n.Channel(); got != ChannelInbox {
		t.Errorf("Channel() = %q, want %q", got, ChannelInbox)
	}
	// store가 없어도 Enabled는 true다 — 설정할 것이 없는 채널이기 때문이다.
	if ok, reason := n.Enabled(); !ok || reason != "" {
		t.Errorf("Enabled() = (%v, %q), want (true, \"\")", ok, reason)
	}
}

func TestInboxSendInsertsRow(t *testing.T) {
	st := &fakeInboxStore{created: true}
	n := NewInbox(InboxConfig{Store: st})

	if err := n.Send(context.Background(), inboxMessage()); err != nil {
		t.Fatalf("Send() = %v, want nil", err)
	}
	if len(st.entries) != 1 {
		t.Fatalf("insert count = %d, want 1", len(st.entries))
	}
	got := st.entries[0]
	if got.UserID != "11111111-1111-1111-1111-111111111111" {
		t.Errorf("UserID = %q", got.UserID)
	}
	// event_id는 종류 문자열이 아니라 저장 이벤트 UUID여야 한다.
	if got.EventID != "22222222-2222-2222-2222-222222222222" {
		t.Errorf("EventID = %q, want the stored event uuid", got.EventID)
	}
	if got.CertificateID == nil || *got.CertificateID != "33333333-3333-3333-3333-333333333333" {
		t.Errorf("CertificateID = %v", got.CertificateID)
	}
	if got.Title == "" || got.Body == "" {
		t.Errorf("title/body not carried: %+v", got)
	}
	if got.Severity != SeverityError {
		t.Errorf("Severity = %q, want %q", got.Severity, SeverityError)
	}
}

func TestInboxSendDefaultsSeverityAndOmitsCertificate(t *testing.T) {
	st := &fakeInboxStore{created: true}
	n := NewInbox(InboxConfig{Store: st})

	m := inboxMessage()
	m.Severity = ""
	delete(m.Data, DataKeyCertificateID)
	if err := n.Send(context.Background(), m); err != nil {
		t.Fatalf("Send() = %v, want nil", err)
	}
	if got := st.entries[0].Severity; got != SeverityInfo {
		t.Errorf("Severity = %q, want %q", got, SeverityInfo)
	}
	if st.entries[0].CertificateID != nil {
		t.Errorf("CertificateID = %v, want nil", st.entries[0].CertificateID)
	}
}

func TestInboxDuplicateInsertIsSuccess(t *testing.T) {
	// ON CONFLICT DO NOTHING으로 흡수된 중복은 실패가 아니다 — 재시도를 유발하면
	// 같은 사건으로 큐가 무한히 돈다.
	st := &fakeInboxStore{created: false}
	n := NewInbox(InboxConfig{Store: st})
	if err := n.Send(context.Background(), inboxMessage()); err != nil {
		t.Fatalf("Send() = %v, want nil", err)
	}
}

func TestInboxDatabaseErrorIsTemporary(t *testing.T) {
	st := &fakeInboxStore{err: errors.New("connection reset")}
	n := NewInbox(InboxConfig{Store: st})
	err := n.Send(context.Background(), inboxMessage())
	if err == nil || !IsTemporary(err) {
		t.Fatalf("Send() = %v, want a temporary error", err)
	}
}

// 테스트 발송에는 수신자 사용자 id도, 저장된 이벤트 UUID도 없다. 예전에는 그래서
// 항상 "no recipient user id" 실패로 끝나 운영자에게 알림함이 고장난 것처럼 보였다.
// 지금은 삽입 없이 성공으로 끝낸다 — 알림함은 확인할 설정 자체가 없는 채널이다.
func TestInboxTestSendSucceedsWithoutInserting(t *testing.T) {
	st := &fakeInboxStore{created: true}
	n := NewInbox(InboxConfig{Store: st})

	m := Message{
		Title:    "[AutoCert] 알림 채널 테스트",
		TextBody: "본문",
		Severity: SeverityInfo,
		EventID:  "notification.test",
		Data:     map[string]any{"event": "notification.test", DataKeyTestSend: true},
	}
	if err := n.Send(context.Background(), m); err != nil {
		t.Fatalf("Send() = %v, want nil — a test send must not look like a failure", err)
	}
	if len(st.entries) != 0 {
		t.Errorf("test send must not insert a row, got %d", len(st.entries))
	}
}

// 표식이 없는 평소 메시지는 예전 그대로 검증을 거친다.
func TestInboxTestSendMarkerDoesNotWeakenNormalMessages(t *testing.T) {
	st := &fakeInboxStore{created: true}
	n := NewInbox(InboxConfig{Store: st})

	m := inboxMessage()
	m.Data[DataKeyTestSend] = false
	if err := n.Send(context.Background(), m); err != nil {
		t.Fatalf("Send() = %v, want nil", err)
	}
	if len(st.entries) != 1 {
		t.Fatalf("insert count = %d, want 1", len(st.entries))
	}
}

func TestInboxInvalidMessageIsPermanent(t *testing.T) {
	cases := []struct {
		name  string
		store InboxStore
		mut   func(m *Message)
		want  string
	}{
		{
			name:  "store not wired",
			store: nil,
			mut:   func(*Message) {},
			want:  "store is not wired",
		},
		{
			name:  "no recipient",
			store: &fakeInboxStore{created: true},
			mut:   func(m *Message) { m.Recipient = "  " },
			want:  "recipient",
		},
		{
			name:  "no stored event id",
			store: &fakeInboxStore{created: true},
			mut:   func(m *Message) { delete(m.Data, DataKeyEventID) },
			want:  DataKeyEventID,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			n := NewInbox(InboxConfig{Store: tc.store})
			m := inboxMessage()
			tc.mut(&m)
			err := n.Send(context.Background(), m)
			if err == nil || !IsPermanent(err) {
				t.Fatalf("Send() = %v, want a permanent error", err)
			}
			if !strings.Contains(err.Error(), tc.want) {
				t.Errorf("error %q does not mention %q", err.Error(), tc.want)
			}
		})
	}
}
