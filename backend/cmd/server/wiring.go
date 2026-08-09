// wiring.go — 백그라운드 루프(알림 스케줄러·디스패처, 대역 스윕) 배선.
//
// notify는 store를 import하지 않는 설계이므로(의존 방향: config → notify, store는
// notify를 모른다) 두 계층을 잇는 얇은 어댑터가 여기 있다. discovery.SweepOptions도
// config.SweepConfig와 1:1이지만 별도 타입이라 여기서 옮겨 담는다.
//
// 실패 격리 원칙은 센서 게이트웨이와 같다: 알림·스윕 설정 오류는 그 기능만
// 비활성화할 뿐 코어 API(8080)를 죽이지 않는다. 그래서 이 파일의 함수는
// 에러를 반환하지 않고 사유를 로그로만 남긴다.
//
// 보안: 로그에 남기는 것은 환경변수 이름과 사유 문자열뿐이다. config·notify가
// 만드는 사유 문자열은 secret을 담지 않는다는 계약이며 여기서 값을 더하지 않는다.
package main

import (
	"context"
	"log/slog"
	"sort"

	"github.com/Falke060918/INHERIT_AutoCert/backend/internal/config"
	"github.com/Falke060918/INHERIT_AutoCert/backend/internal/discovery"
	"github.com/Falke060918/INHERIT_AutoCert/backend/internal/notify"
	"github.com/Falke060918/INHERIT_AutoCert/backend/internal/store"
)

// notifyStore는 저장 계층을 notify의 세 인터페이스로 잇는 어댑터다.
// 셋 다 같은 *store.Store를 쓰므로 타입 하나로 겸한다.
type notifyStore struct{ st *store.Store }

var (
	_ notify.SchedulerStore  = notifyStore{}
	_ notify.DispatcherStore = notifyStore{}
	_ notify.InboxStore      = notifyStore{}
)

// RunNotificationTick은 탐지+팬아웃을 단일 트랜잭션으로 실행한다.
func (a notifyStore) RunNotificationTick(ctx context.Context, seed bool) (notify.TickResult, bool, error) {
	mode := store.DetectModeSend
	if seed {
		mode = store.DetectModeSeed
	}
	res, locked, err := a.st.RunNotificationTick(ctx, mode)
	if err != nil || res == nil {
		return notify.TickResult{}, locked, err
	}
	return notify.TickResult{Events: len(res.Events), Deliveries: res.Deliveries}, locked, nil
}

// HasNotificationEvents는 이벤트가 한 번이라도 적재된 적 있는지 반환한다.
func (a notifyStore) HasNotificationEvents(ctx context.Context) (bool, error) {
	return a.st.HasNotificationEvents(ctx)
}

// PurgeNotifications는 보존 기간을 넘긴 종료 상태 행을 지운다.
func (a notifyStore) PurgeNotifications(ctx context.Context, retentionDays int) (notify.PurgeResult, error) {
	res, err := a.st.PurgeNotifications(ctx, retentionDays)
	if err != nil || res == nil {
		return notify.PurgeResult{}, err
	}
	return notify.PurgeResult{
		Deliveries: res.Deliveries,
		Events:     res.Events,
		InboxItems: res.InboxItems,
	}, nil
}

// ReapExpiredDeliveryLeases는 리스가 만료된 sending 행을 pending으로 되돌린다.
func (a notifyStore) ReapExpiredDeliveryLeases(ctx context.Context) (int, error) {
	return a.st.ReapExpiredDeliveryLeases(ctx)
}

// LeaseDeliveries는 발송 차례가 된 딜리버리를 잡아 notify 측 표현으로 옮긴다.
// 인증서 원문 바이트가 실릴 수 없도록 payload의 고정 필드만 옮긴다 (하드 규칙 3).
func (a notifyStore) LeaseDeliveries(ctx context.Context, limit int) ([]notify.Delivery, error) {
	rows, err := a.st.LeaseDeliveries(ctx, limit)
	if err != nil {
		return nil, err
	}
	out := make([]notify.Delivery, 0, len(rows))
	for _, d := range rows {
		certID := d.Payload.CertificateID
		if certID == "" && d.CertificateID != nil {
			certID = *d.CertificateID
		}
		out = append(out, notify.Delivery{
			ID:           d.ID,
			EventID:      d.EventID,
			Channel:      d.Channel,
			Recipient:    d.Recipient,
			AttemptCount: d.AttemptCount,
			EventType:    d.EventType,
			Severity:     d.Severity,
			Facts: notify.CertExpiryFacts{
				CertificateID: certID,
				CommonName:    d.Payload.CommonName,
				NotAfter:      d.Payload.NotAfter,
				DaysRemaining: d.Payload.DaysRemaining,
				Environment:   d.Payload.Environment,
				OwnerDisplay:  d.Payload.OwnerDisplay,
				LocationCount: d.Payload.LocationCount,
			},
		})
	}
	return out, nil
}

// MarkDeliverySent는 발송 성공을 기록한다.
func (a notifyStore) MarkDeliverySent(ctx context.Context, deliveryID string) error {
	return a.st.MarkDeliverySent(ctx, deliveryID)
}

// MarkDeliveryFailed는 실패를 기록한다. 재시도 계산 결과(DeliveryAttempt)는
// 저장 계층이 이미 행에 반영했으므로 디스패처는 쓰지 않는다.
func (a notifyStore) MarkDeliveryFailed(ctx context.Context, deliveryID, sendErr string, permanent bool) error {
	_, err := a.st.MarkDeliveryFailed(ctx, deliveryID, sendErr, permanent)
	return err
}

// MarkDeliverySkipped는 시도하지 않고 사유와 함께 종결한다.
func (a notifyStore) MarkDeliverySkipped(ctx context.Context, deliveryID, reason string) error {
	return a.st.MarkDeliverySkipped(ctx, deliveryID, reason)
}

// InsertInboxItem은 알림함 행 1건을 멱등 삽입한다.
func (a notifyStore) InsertInboxItem(ctx context.Context, in notify.InboxEntry) (bool, error) {
	return a.st.InsertInboxItem(ctx, store.InboxInsert{
		UserID:        in.UserID,
		EventID:       in.EventID,
		Title:         in.Title,
		Body:          in.Body,
		Severity:      in.Severity,
		CertificateID: in.CertificateID,
	})
}

// logStartupWarnings는 설정 값 오류·위험한 설정과 채널별 비활성 사유를 기동 시 남긴다.
// 여기 실리는 문자열은 config가 만든 것이며 변수 이름과 사유만 담는다 —
// 비밀번호·토큰 값은 config 단계에서 이미 배제되어 있다.
func logStartupWarnings(logger *slog.Logger, cfg config.Config) {
	for _, w := range cfg.Warnings {
		logger.Warn("config: " + w)
	}
	for _, channel := range sortedKeys(cfg.NotifyChannelErrors) {
		// 이 채널의 딜리버리는 시도 없이 skipped로 종결된다 (무한 pending 방지).
		logger.Warn("notify: channel disabled by configuration error",
			"channel", channel, "reason", cfg.NotifyChannelErrors[channel])
	}
}

func sortedKeys(m map[string]string) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

// startNotify는 알림 스케줄러와 디스패처를 띄운다. 반환된 stop은 둘 다 멈추고
// 종료를 기다린다 — sensorgw.StartReaper와 같은 정지 규약이다.
//
// NOTIFY_ENABLED=false면 아무것도 띄우지 않는다(스케줄러가 그 사실을 로그 1줄로 남긴다).
// 이 함수는 어떤 설정에서도 에러를 내지 않는다: 채널 미설정·오설정은 채널 단위
// 비활성으로만 드러나고 코어 API는 그대로 뜬다.
func startNotify(ctx context.Context, logger *slog.Logger, cfg config.Config, st *store.Store) (stop func()) {
	adapter := notifyStore{st: st}
	scheduler := notify.NewScheduler(notify.SchedulerConfig{
		Store:         adapter,
		Enabled:       cfg.Notify.Enabled,
		Interval:      cfg.Notify.ScanInterval,
		FirstRunSeed:  cfg.Notify.FirstRun == config.FirstRunSeed,
		RetentionDays: cfg.Notify.RetentionDays,
		Logger:        logger,
	})
	// 비활성이면 StartScheduler가 no-op + 사유 로그 1줄을 남긴다. 디스패처도 띄우지
	// 않는다 — 새로 생기는 딜리버리가 없으므로 큐를 도는 의미가 없다.
	stopScheduler := scheduler.StartScheduler(ctx)
	if !cfg.Notify.Enabled {
		return stopScheduler
	}

	notifiers := newNotifiers(cfg.Notify, logger, adapter)
	dispatcher := notify.NewDispatcher(notify.DispatcherConfig{
		Store:         adapter,
		Notifiers:     notifiers,
		ChannelErrors: cfg.NotifyChannelErrors,
		// 비면 본문에서 딥링크를 생략한다 — 오류가 아니다.
		BaseURL: cfg.Notify.BaseURL,
		Logger:  logger,
	})
	stopDispatcher := dispatcher.StartDispatcher(ctx)

	enabled, disabled := channelStatus(notifiers, cfg.NotifyChannelErrors)
	logger.Info("notify: scheduler and dispatcher started",
		"scan_interval", cfg.Notify.ScanInterval.String(),
		"retention_days", cfg.Notify.RetentionDays,
		"first_run", cfg.Notify.FirstRun,
		"deep_links", cfg.Notify.BaseURL != "",
		"enabled_channels", enabled,
		"disabled_channels", disabled)

	return func() {
		stopScheduler()
		stopDispatcher()
	}
}

// newNotifiers는 채널 어댑터 4종을 만든다. 생성자는 에러를 내지 않으며
// 미설정·오설정은 Enabled() == false + 사유로만 드러난다.
// inbox는 외부 의존이 없어 항상 활성이다 — 아무것도 설정하지 않은 설치 직후에도
// 알림이 도착하게 하는 zero-config 경로다.
func newNotifiers(cfg config.NotifyConfig, logger *slog.Logger, inbox notify.InboxStore) []notify.Notifier {
	email := cfg.Email
	email.Logger = logger
	webhook := cfg.Webhook
	webhook.Logger = logger
	syslog := cfg.Syslog
	syslog.Logger = logger
	return []notify.Notifier{
		notify.NewEmail(email),
		notify.NewWebhook(webhook),
		notify.NewSyslog(syslog),
		notify.NewInbox(notify.InboxConfig{Store: inbox, Logger: logger}),
	}
}

// channelStatus는 기동 로그용 채널 상태를 만든다. 비활성 항목은 "채널: 사유"다 —
// 사유는 config·notify가 만든 secret 없는 문자열이다.
func channelStatus(notifiers []notify.Notifier, channelErrors map[string]string) (enabled, disabled []string) {
	for _, n := range notifiers {
		if reason := channelErrors[n.Channel()]; reason != "" {
			disabled = append(disabled, n.Channel()+": "+reason)
			continue
		}
		if ok, why := n.Enabled(); !ok {
			disabled = append(disabled, n.Channel()+": "+why)
			continue
		}
		enabled = append(enabled, n.Channel())
	}
	return enabled, disabled
}

// sweepOptions는 config.SweepConfig를 discovery.SweepOptions로 옮겨 담는다
// (discovery가 config를 import하지 않으므로 별도 타입이다). 값 검증·경고는
// config가 이미 했으므로 여기서는 매핑만 한다.
func sweepOptions(c config.SweepConfig) discovery.SweepOptions {
	return discovery.SweepOptions{
		Enabled:        c.Enabled,
		RatePPS:        c.RatePPS,
		Concurrency:    c.Concurrency,
		ChunkEndpoints: c.ChunkEndpoints,
		ChunkGap:       c.ChunkGap,
		StallAfter:     c.StallAfter,
	}
}

// startSweepScheduler는 대역 스윕 발주 루프를 띄운다.
// API 핸들러가 쓰는 Manager와 상태를 공유하지 않으므로 전용 인스턴스를 만든다.
// SCAN_SWEEP_ENABLED=false면 아무것도 띄우지 않고 사유 로그 1줄만 남긴다(stop은 no-op).
func startSweepScheduler(ctx context.Context, logger *slog.Logger, cfg config.Config, st *store.Store) (stop func()) {
	stop = discovery.NewManager(st).
		UseSweepOptions(sweepOptions(cfg.Sweep)).
		StartSweepScheduler(ctx)
	if cfg.Sweep.Enabled {
		logger.Info("discovery: sweep scheduler started",
			"rate_pps", cfg.Sweep.RatePPS,
			"concurrency", cfg.Sweep.Concurrency,
			"chunk_endpoints", cfg.Sweep.ChunkEndpoints,
			"chunk_gap", cfg.Sweep.ChunkGap.String(),
			"stall_after", cfg.Sweep.StallAfter.String())
	}
	return stop
}
