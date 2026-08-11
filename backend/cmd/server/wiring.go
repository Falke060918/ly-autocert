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
	"errors"
	"log/slog"
	"sort"
	"strings"

	"github.com/Falke060918/INHERIT_AutoCert/backend/internal/api"
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
// 함께 반환하는 channels는 API(상태 조회·테스트 발송)가 쓰는 어댑터이며 항상
// non-nil이다 — 알림이 꺼져 있어도 "왜 안 오는지"는 화면에서 보여야 한다.
//
// NOTIFY_ENABLED=false면 아무것도 띄우지 않는다(스케줄러가 그 사실을 로그 1줄로 남긴다).
// 이 함수는 어떤 설정에서도 에러를 내지 않는다: 채널 미설정·오설정은 채널 단위
// 비활성으로만 드러나고 코어 API는 그대로 뜬다.
func startNotify(ctx context.Context, logger *slog.Logger, cfg config.Config, st *store.Store) (stop func(), channels *notifyChannels) {
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
		// 보낼 곳이 없으므로 채널 어댑터도 빈 채로 만든다 — 상태 조회는 전 채널을
		// NOTIFY_ENABLED=false 사유로 답하고 테스트 발송은 같은 사유로 거절한다.
		return stopScheduler, newNotifyChannels(cfg, nil)
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
	}, newNotifyChannels(cfg, notifiers)
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

// ===== API 배선: 채널 상태 조회·테스트 발송 =====

// notifyChannelOrder는 상태 응답의 채널 순서를 고정한다 (설계 1.2의 4채널).
// api 쪽 미배선 응답과 같은 순서다 — 배선 유무로 화면 순서가 바뀌면 안 된다.
var notifyChannelOrder = []string{
	notify.ChannelEmail,
	notify.ChannelWebhook,
	notify.ChannelInbox,
	notify.ChannelSyslog,
}

// notifyDisabledReason은 알림 기능 자체가 꺼졌을 때의 전 채널 공통 사유다.
const notifyDisabledReason = "NOTIFY_ENABLED=false (notifications are turned off)"

// notifyChannelMissingReason은 어댑터에 그 채널의 Notifier가 없을 때다.
// newNotifiers가 4종을 모두 만들므로 정상 경로에서는 나오지 않는다 —
// 채널을 늘리며 배선을 빠뜨린 프로그래밍 오류를 화면에 드러내는 안전망이다.
const notifyChannelMissingReason = "channel is not wired on this server"

// notifyChannels는 notify 채널 4종을 api.NotificationChannels로 잇는 어댑터다.
// startNotify가 만든 Notifier 인스턴스를 그대로 재사용한다 — 상태·테스트가 실제
// 발송에 쓰이는 것과 다른 인스턴스를 보면 진단이 거짓말이 된다.
//
// 보안: 밖으로 나가는 것은 채널 이름·활성 여부·사유 문자열뿐이다. 사유는 config와
// notify가 만든 것이며 SMTP 호스트·계정·비밀번호·웹훅 URL을 담지 않는다는 계약이다
// (api.ChannelStatus에는 설정값을 담을 필드 자체가 없다).
type notifyChannels struct {
	// enabled는 NOTIFY_ENABLED다. false면 notifiers가 비어 있다.
	enabled bool
	// notifiers는 채널 식별자 → Notifier다.
	notifiers map[string]notify.Notifier
	// channelErrors는 config 단계에서 채널을 비활성화한 사유다(값 오류·파일 접근 실패).
	// 주의: config는 채널당 **첫 번째** 사유만 기록하므로(disableChannel) 한 채널에
	// 문제가 둘 이상이면 여기 보이는 reason은 그중 하나뿐이다 — 예를 들어
	// SMTP_PORT가 잘못되고 SMTP_PASSWORD_FILE도 못 읽으면 앞의 것만 나온다.
	// 하나를 고친 뒤 다음 사유가 새로 뜰 수 있다.
	channelErrors map[string]string
	// defaultRecipients는 테스트 발송에서 수신자를 비워 보낼 때의 기본값이다.
	// 주소는 여기 담길 뿐 상태·에러 문자열로 나가지 않는다.
	defaultRecipients map[string]string
	// redactions는 발송 에러 문자열에서 지울 설정값이다 (아래 redactConfigValues).
	redactions []redaction
}

// redaction은 에러 문자열에 섞여 나오면 지워야 하는 설정값 하나다.
// marker는 운영자가 어느 변수인지 알 수 있게 변수 이름을 담는다.
type redaction struct {
	value  string
	marker string
}

var _ api.NotificationChannels = (*notifyChannels)(nil)

// newNotifyChannels는 채널 어댑터를 만든다. notifiers가 비면(NOTIFY_ENABLED=false)
// 전 채널이 그 사유로 비활성이라고 답한다.
func newNotifyChannels(cfg config.Config, notifiers []notify.Notifier) *notifyChannels {
	c := &notifyChannels{
		enabled:       cfg.Notify.Enabled,
		notifiers:     make(map[string]notify.Notifier, len(notifiers)),
		channelErrors: cfg.NotifyChannelErrors,
		defaultRecipients: map[string]string{
			// 수신자를 비워 보내면 발신 주소로 보낸다 — 운영자가 자기 릴레이를
			// 확인하는 가장 짧은 경로다. webhook/syslog는 수신자 개념이 없고,
			// inbox의 수신자는 사용자 id라 서버가 고를 기본값이 없다.
			notify.ChannelEmail: cfg.Notify.Email.From,
		},
		redactions: configRedactions(cfg.Notify),
	}
	for _, n := range notifiers {
		c.notifiers[n.Channel()] = n
	}
	return c
}

// configRedactions는 발송 에러에 섞여 나올 수 있는 설정값 목록을 만든다.
// 채널 구현은 "비밀번호·시크릿·수신자 주소를 담지 않는다"는 계약이지만 웹훅은
// 실패한 요청 URL을 그대로 싣는다 — 경로에 토큰이 박힌 웹훅이 흔하므로 응답·감사로
// 나가기 전에 이 경계에서 한 번 더 지운다. 빈 값은 넣지 않는다(전체 치환 사고 방지).
func configRedactions(cfg config.NotifyConfig) []redaction {
	candidates := []redaction{
		{cfg.Webhook.URL, "[WEBHOOK_URL]"},
		{cfg.Webhook.Secret, "[WEBHOOK_HMAC_SECRET]"},
		{cfg.Email.Password, "[SMTP_PASSWORD]"},
	}
	if _, value, ok := strings.Cut(cfg.Webhook.AuthHeader, ":"); ok {
		candidates = append(candidates, redaction{strings.TrimSpace(value), "[WEBHOOK_AUTH_HEADER]"})
	}
	out := make([]redaction, 0, len(candidates))
	for _, r := range candidates {
		if strings.TrimSpace(r.value) != "" {
			out = append(out, redaction{value: r.value, marker: r.marker})
		}
	}
	return out
}

// redactConfigValues는 문자열에서 설정값을 마커로 바꾼다.
func (c *notifyChannels) redactConfigValues(s string) string {
	for _, r := range c.redactions {
		s = strings.ReplaceAll(s, r.value, r.marker)
	}
	return s
}

// Status는 채널별 활성 여부와 사유를 반환한다.
// 배선 실패로 코어 API가 죽는 일이 없어야 하므로 어떤 경로에서도 패닉하지 않는다.
func (c *notifyChannels) Status() []api.ChannelStatus {
	out := make([]api.ChannelStatus, 0, len(notifyChannelOrder))
	for _, ch := range notifyChannelOrder {
		enabled, reason := c.channelState(ch)
		out = append(out, api.ChannelStatus{Channel: ch, Enabled: enabled, Reason: reason})
	}
	return out
}

// channelState는 채널 하나의 활성 여부와 사유를 고른다. 우선순위는
// NOTIFY_ENABLED → config 설정 오류 → 채널 자신의 Enabled()다.
// 디스패처가 딜리버리를 건너뛸 때 보는 순서와 같다.
func (c *notifyChannels) channelState(channel string) (bool, string) {
	if !c.enabled {
		return false, notifyDisabledReason
	}
	if reason := c.channelErrors[channel]; reason != "" {
		return false, reason
	}
	n, ok := c.notifiers[channel]
	if !ok {
		return false, notifyChannelMissingReason
	}
	if ok, why := n.Enabled(); !ok {
		return false, why
	}
	return true, ""
}

// SendTest는 채널 하나로 테스트 알림 1건을 보낸다.
// 비활성 채널은 보내지 않고 사유를 담은 에러를 돌려준다 — 이 문자열은 API 응답과
// 감사에 실리므로 secret이 없는 것(config·notify가 만든 사유)만 싣는다.
func (c *notifyChannels) SendTest(ctx context.Context, channel, recipient string) error {
	if ok, reason := c.channelState(channel); !ok {
		return errors.New("channel is disabled: " + reason)
	}
	recipient = strings.TrimSpace(recipient)
	if recipient == "" {
		recipient = c.defaultRecipients[channel]
	}
	// Notifier.Send를 그대로 쓴다 — 발송 경로를 여기서 다시 구현하지 않는다.
	// 에러 문자열의 1차 위생(수신자 주소 제거)은 채널 구현 책임이고, 여기서는
	// 그래도 새어 나오는 설정값(웹훅 URL 등)을 마지막으로 지운다.
	//
	// inbox는 수신자가 사용자 id이고 본문에 저장 이벤트 UUID가 있어야 하므로
	// 테스트 발송은 사유가 담긴 실패로 끝난다 — 상태 조회로는 정상 진단된다.
	if err := c.notifiers[channel].Send(ctx, notifyTestMessage(recipient)); err != nil {
		return errors.New(c.redactConfigValues(err.Error()))
	}
	return nil
}

// notifyTestMessage는 테스트 발송 1건의 메시지다. 인증서 사실을 담지 않는 고정
// 문구라 원문 인증서·키가 실릴 경로 자체가 없다 (보안 하드 규칙 3).
func notifyTestMessage(recipient string) notify.Message {
	return notify.Message{
		Title: "[AutoCert] 알림 채널 테스트",
		TextBody: "AutoCert 알림 채널 테스트 메시지입니다.\n" +
			"이 메시지가 도착했다면 이 채널의 발송 설정은 정상입니다.\n",
		Data: map[string]any{
			"event": api.EventNotificationTest,
			"test":  true,
		},
		Severity:  notify.SeverityInfo,
		EventID:   api.EventNotificationTest,
		Recipient: recipient,
	}
}

// sweepDefaults는 config에서 API가 쓰는 스윕 기본값만 추린다.
// 주입하지 않으면 스윕 생성이 store 상수로 폴백해 SCAN_SWEEP_* 설정이 무시된다.
// TLSProbeVersions만 SweepConfig 밖(SCAN_TLS_PROBE_VERSIONS)에서 온다 —
// 스윕 전용 설정이 아니라 스캔 전반의 엔드포인트당 프로브 수를 정하는 값이라
// 단발 스캔의 ETA에도 똑같이 곱해진다.
func sweepDefaults(c config.SweepConfig, tlsProbeVersions bool) api.SweepDefaults {
	return api.SweepDefaults{
		RatePPS:          c.RatePPS,
		ChunkEndpoints:   c.ChunkEndpoints,
		MaxDays:          c.MaxDays,
		TLSProbeVersions: tlsProbeVersions,
	}
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
		UseTLSVersionProbe(cfg.ScanTLSProbeVersions).
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

// rescanOptions는 config.RescanConfig를 discovery.RescanOptions로 옮겨 담는다
// (discovery가 config를 import하지 않으므로 별도 타입이다). 값 검증·경고는
// config가 이미 했으므로 여기서는 매핑만 한다. 동시성은 설정 표면을 늘리지 않기
// 위해 상수(discovery.RescanConcurrency)를 그대로 쓴다.
func rescanOptions(c config.RescanConfig) discovery.RescanOptions {
	return discovery.RescanOptions{
		Enabled:        c.Enabled,
		Interval:       c.Interval,
		RatePPS:        c.RatePPS,
		Concurrency:    discovery.RescanConcurrency,
		BatchIPs:       c.BatchIPs,
		ReplaceConfirm: c.ReplaceConfirm,
		RetireMisses:   c.RetireMisses,
	}
}

// startRescanScheduler는 주기 재스캔 tick 루프를 띄운다(발주 + 교체 전이).
// 스윕과 마찬가지로 API Manager와 상태를 공유하지 않는 전용 인스턴스다.
// RESCAN_ENABLED=false면 tick 루프를 띄우지 않고 사유 로그 1줄만 남긴다(stop은 no-op).
//
// 반환하는 controller는 그래도 non-nil이다 — 알림 채널 어댑터와 같은 규약이다.
// 꺼져 있는 것과 배선이 없는 것은 화면에서 구분돼야 하고(`enabled` vs `wired`),
// 감시 엔드포인트 수·적체 수는 스케줄러가 안 돌아도 의미가 있는 값이다.
// 반환하는 mgr은 정기 스캔 스케줄러가 이어서 쓰는 인스턴스다 — '등록된 인증서'
// 정기 스캔의 '지금 실행'이 곧 재스캔 1배치 발주이므로, 그 발주가 스케줄러와 같은
// 설정·같은 진행 중 실행 추적(wg)을 공유해야 한다.
func startRescanScheduler(
	ctx context.Context, logger *slog.Logger, cfg config.Config, st *store.Store,
) (stop func(), controller api.RescanController, mgr *discovery.Manager) {
	mgr = discovery.NewManager(st).UseRescanOptions(rescanOptions(cfg.Rescan)).
		UseTLSVersionProbe(cfg.ScanTLSProbeVersions)
	stop = mgr.StartRescanScheduler(ctx)
	if cfg.Rescan.Enabled {
		logger.Info("discovery: rescan scheduler started",
			"interval", cfg.Rescan.Interval.String(),
			"rate_pps", cfg.Rescan.RatePPS,
			"batch_ips", cfg.Rescan.BatchIPs,
			"replace_confirm", cfg.Rescan.ReplaceConfirm,
			"retire_misses", cfg.Rescan.RetireMisses)
	}
	return stop, &rescanController{mgr: mgr, cfg: cfg.Rescan, serverCtx: ctx}, mgr
}

// scheduleOptions는 정기 스캔 스케줄러 설정을 만든다 (0019).
// 전용 환경변수를 새로 만들지 않는다 — full·preset의 실체가 스윕이므로 스윕 설정을
// 따르고, 주기·대상·레이트는 DB의 설정 행이 정본이다.
func scheduleOptions(cfg config.Config) discovery.ScheduleOptions {
	return discovery.ScheduleOptions{
		// 스윕 발주자가 없으면 청크를 돌 주체가 없다 — 함께 뜨고 함께 꺼진다.
		Enabled:         cfg.Sweep.Enabled,
		DefaultInterval: discovery.DefaultScheduleInterval,
		MaxPerTick:      discovery.DefaultSchedulesPerTick,
		MaxDays:         cfg.Sweep.MaxDays,
	}
}

// startScanScheduleRunner는 정기 스캔 tick 루프를 띄운다(주기 도래 → 스윕 1건 발주).
// mgr은 재스캔 스케줄러가 쓰는 인스턴스를 그대로 받는다(위 주석 참조).
//
// 반환하는 controller는 스윕이 꺼져 있어도 non-nil이다 — 알림 채널·재스캔 어댑터와
// 같은 규약이다(꺼진 것과 배선이 없는 것은 화면에서 구분돼야 한다).
func startScanScheduleRunner(
	ctx context.Context, logger *slog.Logger, cfg config.Config, mgr *discovery.Manager,
) (stop func(), controller api.ScheduleController) {
	mgr = mgr.UseSweepOptions(sweepOptions(cfg.Sweep)).UseScheduleOptions(scheduleOptions(cfg))
	stop = mgr.StartScanScheduleRunner(ctx)
	if cfg.Sweep.Enabled {
		logger.Info("discovery: scan schedule runner started",
			"default_interval", discovery.DefaultScheduleInterval.String(),
			"max_per_tick", discovery.DefaultSchedulesPerTick,
			"max_days", cfg.Sweep.MaxDays)
	}
	return stop, &scheduleController{mgr: mgr, serverCtx: ctx}
}

// scheduleController는 정기 스캔 스케줄러를 api.ScheduleController로 잇는 어댑터다
// (rescanController와 같은 자리 — api는 discovery의 스케줄 타입을 import하지 않는다).
type scheduleController struct {
	mgr *discovery.Manager
	// serverCtx는 서버 수명 컨텍스트다. '지금 실행'이 시작하는 발주·인프로세스 스캔은
	// 응답보다 오래 살아야 하므로 요청 컨텍스트를 쓰지 않는다(재스캔과 같은 이유).
	serverCtx context.Context
}

var _ api.ScheduleController = (*scheduleController)(nil)

// RunNow는 주기를 리셋하지 않고 1회 실행만 연다.
func (c *scheduleController) RunNow(_ context.Context, scheduleID string, act store.ScanScheduleActor) (*store.ScanScheduleRun, error) {
	return c.mgr.RunScanScheduleNow(c.serverCtx, scheduleID, act)
}

// CancelRun은 실행 1건을 취소한다(발주한 스윕을 먼저 멈춘다).
// 취소는 DB 전이뿐이라 요청 컨텍스트를 그대로 쓴다.
func (c *scheduleController) CancelRun(ctx context.Context, runID string, act store.ScanScheduleActor) (*store.ScanScheduleRun, error) {
	return c.mgr.CancelScanScheduleRun(ctx, runID, act)
}

// rescanController는 재스캔 스케줄러를 api.RescanController로 잇는 어댑터다
// (notifyChannels와 같은 자리 — api는 discovery의 재스캔 타입을 import하지 않는다).
type rescanController struct {
	mgr *discovery.Manager
	cfg config.RescanConfig
	// serverCtx는 서버 수명 컨텍스트다. 수동 1배치가 시작하는 인프로세스 스캔은
	// 응답보다 오래 살아야 하므로 요청 컨텍스트를 쓰지 않는다.
	serverCtx context.Context
}

var _ api.RescanController = (*rescanController)(nil)

// Settings는 화면이 띄울 설정 요약이다(원문 값이 아니라 on/off와 유효 간격뿐).
func (c *rescanController) Settings() api.RescanSettings {
	return api.RescanSettings{Enabled: c.cfg.Enabled, Interval: c.cfg.Interval}
}

// RunOnce는 due 판정을 그대로 두고 1배치를 즉시 발주한다.
//
// 요청 컨텍스트(ctx)는 쓰지 않는다 — 발주한 Job의 인프로세스 실행이 202 응답과
// 함께 취소되면 그 Job은 종료 상태를 못 찍고 다음 기동의 고아 회수로 넘어간다.
//
// actor는 아직 감사에 실리지 않는다 — 발주 감사(discovery.scan)는 store가
// actor NULL로 남기며(설계 10.4), 사람이 낸 발주와 스케줄러 발주는 지금은
// after_value.origin으로만 구분된다.
func (c *rescanController) RunOnce(_ context.Context, _ api.RescanRunActor) (api.RescanRunResult, error) {
	sum, err := c.mgr.DispatchRescanOnce(c.serverCtx)
	if err != nil {
		return api.RescanRunResult{}, err
	}
	return api.RescanRunResult{
		Jobs:      sum.ServerJobs + sum.SensorJobs,
		Endpoints: sum.Claimed,
	}, nil
}
