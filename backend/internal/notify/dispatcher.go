// dispatcher.go — 딜리버리 큐 소비 루프 (docs/design/05-notifications.md 6).
// 리스로 잡은 딜리버리를 채널 어댑터로 보내고 결과를 저장 계층에 기록한다.
//
// 의존 방향: notify는 store를 import하지 않는다. 필요한 저장 연산을 이 패키지가
// 인터페이스로 선언하고 호출부(cmd/server)가 얇은 어댑터로 주입한다 —
// config → notify 방향이 이미 있어 반대 방향 import는 순환이 될 여지를 만든다.
//
// 보안: 로그에 남기는 것은 delivery_id·channel·event_id·오류뿐이다.
// 수신자 메일 주소는 어디에도 남기지 않는다 (설계 11.4).
package notify

import (
	"context"
	"errors"
	"log/slog"
	"net/url"
	"strings"
	"time"
)

// 디스패처 기본값. 이 조합이 그대로 발송 레이트 제한이다 (설계 6.2):
//
//	20건 / 15초 = 분당 약 80건
//
// 그래서 별도의 rate-limit 환경변수를 두지 않는다. 조절이 필요하면 주기와 배치
// 크기를 바꾼다 — 설정 표면을 늘리지 않는 것이 목적이다.
const (
	defaultDispatchInterval = 15 * time.Second
	defaultDispatchBatch    = 20
	// defaultSendTimeout은 딜리버리 1건(발송 + 결과 기록)의 상한이다.
	// 채널 어댑터가 각자 더 짧은 타임아웃을 갖고 있으므로 이건 마지막 안전장치다.
	defaultSendTimeout = 60 * time.Second
)

// Message.Data 키. 채널 어댑터와 웹훅 수신 측이 함께 쓰는 계약이므로 문자열을 고정한다.
const (
	// DataKeyEventID는 저장 이벤트 UUID(notification_events.id)다.
	// 알림함이 UNIQUE (user_id, event_id) 멱등 삽입에 쓰고, 웹훅 수신 측은 이 값으로
	// 중복 수신을 제거한다 (설계 13). Message.EventID는 종류 문자열이라 쓸 수 없다.
	DataKeyEventID = "notification_event_id"
	// DataKeyCertificateID는 대상 인증서 id다 (BuildCertExpiryMessage가 채운다).
	DataKeyCertificateID = "certificate_id"
)

// recipientMark는 오류 문자열에서 수신자 값을 가린 자리다.
const recipientMark = "[recipient]"

// Delivery는 디스패처가 처리하는 발송 1건이다 (저장 계층 리스 결과의 notify 측 표현).
// 채널 어댑터가 필요한 사실을 전부 실어 보내므로 어댑터가 DB를 다시 읽지 않는다.
type Delivery struct {
	// ID는 notification_deliveries.id다.
	ID string
	// EventID는 notification_events.id다.
	EventID string
	// Channel은 ChannelEmail|ChannelWebhook|ChannelInbox|ChannelSyslog 중 하나다.
	Channel string
	// Recipient는 메일 주소 또는 사용자 id다. 로그에 넣지 않는다.
	Recipient string
	// AttemptCount는 지금까지의 시도 횟수다 (진단용).
	AttemptCount int
	// EventType은 저장된 이벤트 종류다. 비면 메시지 빌더의 판정을 그대로 쓴다.
	EventType string
	// Severity는 저장된 심각도다. 비면 메시지 빌더의 판정을 그대로 쓴다.
	Severity string
	// Facts는 알림 본문에 필요한 사실이다. 인증서 원문 바이트는 들어올 수 없다
	// (고정 필드 구조체 — 보안 하드 규칙 3).
	Facts CertExpiryFacts
}

// DispatcherStore는 디스패처가 쓰는 저장 연산이다.
// 호출부(cmd/server)가 저장 계층을 감싸 주입한다 — 리스는 FOR UPDATE SKIP LOCKED,
// 결과 기록은 백오프·시도 횟수까지 저장 계층이 책임진다.
//
// MarkDeliverySkipped는 "보낼 수 없는 채널"을 종결 상태로 끝내는 경로다.
// 이것이 없으면 설정이 고쳐질 때까지 pending이 무한히 쌓인다.
type DispatcherStore interface {
	// LeaseDeliveries는 발송 차례가 된 딜리버리를 최대 limit건 잡아 sending으로 전이한다.
	LeaseDeliveries(ctx context.Context, limit int) ([]Delivery, error)
	// MarkDeliverySent는 발송 성공을 기록한다.
	MarkDeliverySent(ctx context.Context, deliveryID string) error
	// MarkDeliveryFailed는 실패를 기록한다. permanent면 즉시 종결하고,
	// 아니면 백오프를 걸어 재시도 대기로 되돌린다.
	MarkDeliveryFailed(ctx context.Context, deliveryID, sendErr string, permanent bool) error
	// MarkDeliverySkipped는 시도하지 않고 사유와 함께 종결한다.
	MarkDeliverySkipped(ctx context.Context, deliveryID, reason string) error
}

// DispatcherConfig는 디스패처 설정이다.
type DispatcherConfig struct {
	Store DispatcherStore
	// Notifiers는 채널 어댑터다. 채널당 하나이며, 뒤에 온 같은 채널이 앞을 덮는다.
	Notifiers []Notifier
	// ChannelErrors는 config.NotifyChannelErrors다 (키: 채널 식별자, 값: 사유).
	// 여기 실린 채널은 아예 시도하지 않고 skipped로 끝낸다.
	// 값 문자열은 저장·로그에 그대로 나가므로 secret을 담지 않는다는 전제다.
	ChannelErrors map[string]string
	// BaseURL은 알림 본문 딥링크의 기준 URL이다 (NOTIFY_BASE_URL). 비면 링크를 생략한다.
	BaseURL string
	// Interval은 큐 소비 주기다 (기본 15s).
	Interval time.Duration
	// BatchSize는 한 번에 리스할 최대 건수다 (기본 20).
	BatchSize int
	// SendTimeout은 딜리버리 1건의 상한이다 (기본 60s).
	SendTimeout time.Duration
	Logger      *slog.Logger
}

// Dispatcher는 딜리버리 큐를 소비하는 루프다.
type Dispatcher struct {
	store         DispatcherStore
	notifiers     map[string]Notifier
	channelErrors map[string]string
	baseURL       string
	interval      time.Duration
	batchSize     int
	sendTimeout   time.Duration
	log           *slog.Logger
}

// NewDispatcher는 디스패처를 만든다. 생성자는 에러를 반환하지 않는다 —
// 알림 배선 오류가 코어 API를 죽이면 안 되므로 채널 단위로만 드러난다.
func NewDispatcher(cfg DispatcherConfig) *Dispatcher {
	d := &Dispatcher{
		store:         cfg.Store,
		notifiers:     map[string]Notifier{},
		channelErrors: cfg.ChannelErrors,
		baseURL:       strings.TrimRight(strings.TrimSpace(cfg.BaseURL), "/"),
		interval:      orDefault(cfg.Interval, defaultDispatchInterval),
		sendTimeout:   orDefault(cfg.SendTimeout, defaultSendTimeout),
		batchSize:     cfg.BatchSize,
		log:           cfg.Logger,
	}
	for _, n := range cfg.Notifiers {
		if n == nil {
			continue
		}
		d.notifiers[n.Channel()] = n
	}
	if d.batchSize < 1 {
		d.batchSize = defaultDispatchBatch
	}
	return d
}

func (d *Dispatcher) logger() *slog.Logger {
	if d.log != nil {
		return d.log
	}
	return slog.Default()
}

// StartDispatcher는 큐 소비 goroutine을 시작한다. 반환된 stop은 goroutine을 멈추고
// 종료를 기다린다 (ctx 취소로도 멈춘다) — sensorgw.StartReaper와 같은 정지 규약이다.
func (d *Dispatcher) StartDispatcher(ctx context.Context) (stop func()) {
	ctx, cancel := context.WithCancel(ctx)
	done := make(chan struct{})
	go func() {
		defer close(done)
		ticker := time.NewTicker(d.interval)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				if _, err := d.RunOnce(ctx); err != nil && ctx.Err() == nil {
					d.logger().Error("notify: dispatch batch failed", "error", err.Error())
				}
			}
		}
	}()
	return func() {
		cancel()
		<-done
	}
}

// RunOnce는 한 배치를 리스해 처리하고 처리 건수를 반환한다 (테스트·수동 실행용).
// ctx가 취소되면 진행 중이던 한 건을 마친 뒤 남은 리스를 두고 빠져나온다 —
// 남은 행은 리스 만료 회수로 pending으로 돌아온다 (설계 7.5).
func (d *Dispatcher) RunOnce(ctx context.Context) (int, error) {
	if d.store == nil {
		return 0, errors.New("notify: dispatcher store is not wired")
	}
	batch, err := d.store.LeaseDeliveries(ctx, d.batchSize)
	if err != nil {
		return 0, err
	}
	done := 0
	for _, del := range batch {
		// 딜리버리는 채널×수신자당 1행이므로 한 건씩 독립 처리한다 —
		// 한 채널(예: 죽은 SMTP)의 실패가 다른 채널의 발송을 막지 않는다.
		d.deliver(ctx, del)
		done++
		if ctx.Err() != nil {
			break
		}
	}
	return done, nil
}

// deliver는 딜리버리 1건을 처리한다. 이 함수는 에러를 반환하지 않는다 —
// 한 건의 실패는 저장 계층에 기록될 뿐 배치를 중단시키지 않는다.
func (d *Dispatcher) deliver(ctx context.Context, del Delivery) {
	log := d.logger().With(
		"delivery_id", del.ID,
		"channel", del.Channel,
		"event_id", del.EventID,
	)
	// 발송·결과 기록은 취소되지 않는 컨텍스트로 한다. 정지 신호가 와도 진행 중
	// 한 건은 마치고, "보냈는데 기록을 못 한" 구멍을 만들지 않기 위해서다.
	sendCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), d.sendTimeout)
	defer cancel()

	if reason, unavailable := d.unavailable(del.Channel); unavailable {
		if err := d.store.MarkDeliverySkipped(sendCtx, del.ID, reason); err != nil {
			log.Error("notify: mark delivery skipped failed", "error", err.Error())
			return
		}
		log.Warn("notify: delivery skipped", "reason", reason)
		return
	}

	err := d.notifiers[del.Channel].Send(sendCtx, d.message(del))
	if err == nil {
		if err := d.store.MarkDeliverySent(sendCtx, del.ID); err != nil {
			// 발송은 됐는데 기록이 안 됐다. 리스 만료 후 다시 시도되어 중복 발송이
			// 될 수 있다 — at-least-once로 수용한 성질이다 (설계 13).
			log.Error("notify: mark delivery sent failed", "error", err.Error())
			return
		}
		log.Info("notify: delivery sent", "attempt", del.AttemptCount+1)
		return
	}

	// 분류되지 않은 에러는 영구 실패다 (errors.go — 원인 모를 실패로 큐를 막지 않는다).
	permanent := !IsTemporary(err)
	// 채널 어댑터가 이미 주소를 지우지만, 새 어댑터가 규칙을 어겨도 로그·저장 경로에
	// 수신자가 흘러가지 않도록 여기서 한 번 더 막는다 (설계 11.4).
	reason := scrubRecipient(err.Error(), del.Recipient)
	if err := d.store.MarkDeliveryFailed(sendCtx, del.ID, reason, permanent); err != nil {
		log.Error("notify: mark delivery failed", "error", err.Error())
		return
	}
	if permanent {
		log.Error("notify: delivery failed permanently", "error", reason)
		return
	}
	log.Warn("notify: delivery failed, will retry", "attempt", del.AttemptCount+1, "error", reason)
}

// unavailable은 채널이 지금 보낼 수 없는 상태인지와 그 사유를 반환한다.
// 셋 다 "설정이 바뀌기 전에는 몇 번을 시도해도 같다"이므로 재시도가 아니라
// skipped로 종결한다 — 무한 pending 방지.
func (d *Dispatcher) unavailable(channel string) (string, bool) {
	if reason := d.channelErrors[channel]; reason != "" {
		return "channel configuration error: " + reason, true
	}
	n, ok := d.notifiers[channel]
	if !ok {
		return "no adapter is wired for channel " + channel, true
	}
	if enabled, why := n.Enabled(); !enabled {
		return why, true
	}
	return "", false
}

// message는 딜리버리에서 채널에 넘길 메시지를 만든다.
func (d *Dispatcher) message(del Delivery) Message {
	facts := del.Facts
	if d.baseURL != "" && facts.CertificateID != "" {
		facts.DeepLink = d.baseURL + "/certificates/" + url.PathEscape(facts.CertificateID)
	}
	m := BuildCertExpiryMessage(facts)
	// 저장된 종류·심각도가 정본이다. 탐지 시점과 발송 시점의 남은 일수가 달라도
	// 알림함·웹훅이 이벤트와 같은 값을 갖게 한다.
	if del.EventType != "" {
		m.EventID = del.EventType
	}
	if del.Severity != "" {
		m.Severity = del.Severity
	}
	m.Recipient = del.Recipient
	if m.Data == nil {
		m.Data = map[string]any{}
	}
	m.Data[DataKeyEventID] = del.EventID
	return m
}

// scrubRecipient는 문자열에서 수신자 값을 가린다.
func scrubRecipient(s, recipient string) string {
	r := strings.TrimSpace(recipient)
	if r == "" || !strings.Contains(s, r) {
		return s
	}
	return strings.ReplaceAll(s, r, recipientMark)
}

// dataString은 Message.Data에서 문자열 값을 꺼낸다. 없거나 문자열이 아니면 빈 문자열이다.
func dataString(data map[string]any, key string) string {
	v, ok := data[key].(string)
	if !ok {
		return ""
	}
	return strings.TrimSpace(v)
}
