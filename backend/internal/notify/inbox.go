// inbox.go — 제품 UI 알림함 채널 어댑터 (docs/design/05-notifications.md 5.4).
// 외부 의존이 0이라 사실상 실패하지 않는다. 설치 직후 SMTP·웹훅이 하나도 설정되지
// 않은 상태에서도 알림이 도착해야 하므로 시드 정책이 이 채널만 쓴다 (zero-config).
package notify

import (
	"context"
	"errors"
	"log/slog"
	"strings"
)

// InboxEntry는 알림함 행 1건의 저장 요청이다.
// 저장 계층의 같은 이름 구조체와 필드가 겹치지만 그 타입을 쓰지 않는다 —
// notify가 store를 import하면 의존 방향이 뒤집힌다(설계 의존 방향: config → notify,
// store는 notify를 모른다). 호출부가 얇은 어댑터로 두 타입을 잇는다.
type InboxEntry struct {
	// UserID는 알림을 받을 사용자 id다 (Message.Recipient에 실려 온다).
	UserID string
	// EventID는 notification_events.id(UUID)다. 이벤트 종류 문자열이 아니다.
	EventID string
	Title   string
	Body    string
	// Severity는 SeverityInfo|Warning|Error|Critical 중 하나다.
	Severity string
	// CertificateID는 화면 딥링크용이며 없을 수 있다.
	CertificateID *string
}

// InboxStore는 알림함 채널이 쓰는 유일한 저장 연산이다.
// 구현은 UNIQUE (user_id, event_id) 아비터로 멱등해야 한다 — 발송 재시도로 같은
// 사건이 같은 사용자에게 두 번 꽂히지 않는다 (설계 5.4·13).
// created=false는 이미 있던 행이라는 뜻이며 오류가 아니다.
//
// 호출부(cmd/server)가 저장 계층을 감싸 주입한다:
//
//	InsertInboxItem: func(ctx context.Context, in notify.InboxEntry) (bool, error) {
//	    return st.InsertInboxItem(ctx, store.InboxInsert{ ... })
//	}
type InboxStore interface {
	InsertInboxItem(ctx context.Context, in InboxEntry) (created bool, err error)
}

// DataKeyTestSend는 테스트 발송 표식이다 (Message.Data[DataKeyTestSend] == true).
// 운영자가 채널 설정을 확인하려고 손으로 보낸 1건이라는 뜻이며, 실제 알림 사건이 아니다.
// 테스트 메시지를 만드는 쪽(cmd/server의 notifyTestMessage)이 채운다.
const DataKeyTestSend = "test"

// InboxConfig는 알림함 채널 설정이다. 환경변수로 조절할 것이 없다.
type InboxConfig struct {
	Store  InboxStore
	Logger *slog.Logger
}

// InboxNotifier는 알림함 행을 만드는 채널이다.
type InboxNotifier struct {
	channelState
	store InboxStore
}

// NewInbox는 알림함 채널을 만든다. 다른 채널과 달리 비활성 사유가 존재하지 않는다.
func NewInbox(cfg InboxConfig) *InboxNotifier {
	return &InboxNotifier{
		channelState: channelState{log: cfg.Logger},
		store:        cfg.Store,
	}
}

// Channel은 채널 식별자를 반환한다.
func (n *InboxNotifier) Channel() string { return ChannelInbox }

// Enabled는 항상 true다. 알림함은 외부 시스템에 접속하지 않으므로 채울 설정이 없고,
// 아무것도 설정하지 않은 설치 직후에도 알림이 도착해야 한다 (설계 2 zero-config).
// 배선 누락은 설정 문제가 아니라 프로그래밍 오류이므로 Send에서 영구 실패로 드러낸다.
func (n *InboxNotifier) Enabled() (bool, string) { return true, "" }

// Send는 알림함 행 1건을 멱등 삽입한다.
// 재시도 대상은 DB 오류뿐이다 — 값이 모자란 메시지는 다시 보내도 같으므로 영구 실패다.
func (n *InboxNotifier) Send(ctx context.Context, m Message) error {
	if n.store == nil {
		return Permanent(errors.New("notify/inbox: store is not wired"))
	}
	// 테스트 발송은 행을 만들지 않고 성공으로 끝낸다.
	// 알림함은 채울 설정이 없어 항상 활성인 채널이라 테스트 발송으로 확인할 외부
	// 설정 자체가 없고, 실제 삽입에는 notification_events 행(FK)이 필요해 저장 계층이
	// 테스트 전용 이벤트를 만들어 줘야 한다 — 가짜 알림 행이 남는 부작용이 진단
	// 가치보다 크다. 예전에는 이 경로가 "message has no recipient user id"로 끝나
	// 운영자에게 알림함이 고장난 것처럼 보였다.
	if isTestSend(m) {
		n.logger().Debug("notify: inbox test send needs no configuration — nothing was inserted",
			"channel", ChannelInbox)
		return nil
	}
	userID := strings.TrimSpace(m.Recipient)
	if userID == "" {
		return Permanent(errors.New("notify/inbox: message has no recipient user id"))
	}
	eventID := dataString(m.Data, DataKeyEventID)
	if eventID == "" {
		return Permanentf("notify/inbox: message has no %s", DataKeyEventID)
	}
	severity := m.Severity
	if severity == "" {
		severity = SeverityInfo
	}
	entry := InboxEntry{
		UserID:   userID,
		EventID:  eventID,
		Title:    m.Title,
		Body:     m.TextBody,
		Severity: severity,
	}
	if certID := dataString(m.Data, DataKeyCertificateID); certID != "" {
		entry.CertificateID = &certID
	}

	created, err := n.store.InsertInboxItem(ctx, entry)
	if err != nil {
		// 락 경합·일시 단절은 다시 시도하면 성공한다.
		return Temporaryf("notify/inbox: insert failed: %v", err)
	}
	if !created {
		// 이미 꽂혀 있던 사건이다. 발송은 성공으로 본다 (at-least-once 흡수, 설계 13).
		// 사용자 id는 로그에 남기지 않는다 (설계 11.4).
		n.logger().Debug("notify: inbox item already present",
			"channel", ChannelInbox, "event_id", eventID)
	}
	return nil
}

// isTestSend는 테스트 발송 메시지인지 판정한다.
func isTestSend(m Message) bool {
	v, ok := m.Data[DataKeyTestSend].(bool)
	return ok && v
}
