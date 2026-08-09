// scheduler.go — 만료 탐지 스케줄러 (docs/design/05-notifications.md 7).
// 주기적으로 탐지·팬아웃 tick을 돌리고 보존 기간을 넘긴 알림 행을 지운다.
// 탐지 SQL·advisory lock·팬아웃은 저장 계층이 단일 트랜잭션으로 수행하고,
// 여기서는 "언제 돌릴지"만 정한다 (sensorgw.StartReaper와 같은 구조 — 새 패턴을 만들지 않는다).
//
// 의존 방향: notify는 store를 import하지 않는다. 필요한 저장 연산을 이 패키지가
// 인터페이스로 선언하고 호출부(cmd/server)가 얇은 어댑터로 주입한다 —
// config → notify 방향이 이미 있어 반대 방향 import는 순환이 될 여지를 만든다.
package notify

import (
	"context"
	"errors"
	"log/slog"
	"sync"
	"time"
)

// defaultScanInterval은 NOTIFY_SCAN_INTERVAL 기본값이다 (설계 10).
const defaultScanInterval = time.Hour

// TickResult는 tick 1회로 새로 생긴 이벤트·딜리버리 수다.
type TickResult struct {
	Events     int
	Deliveries int
}

// PurgeResult는 보존 정리로 삭제된 행 수다.
type PurgeResult struct {
	Deliveries int
	Events     int
	InboxItems int
}

// SchedulerStore는 스케줄러가 쓰는 저장 연산이다.
// 호출부(cmd/server)가 저장 계층을 감싸 주입한다:
//
//	RunNotificationTick: func(ctx context.Context, seed bool) (notify.TickResult, bool, error) {
//	    mode := store.DetectModeSend
//	    if seed { mode = store.DetectModeSeed }
//	    res, locked, err := st.RunNotificationTick(ctx, mode)
//	    ...
//	}
type SchedulerStore interface {
	// RunNotificationTick은 탐지+팬아웃을 단일 트랜잭션으로 실행한다.
	// seed면 이미 지난 임계값을 발송하지 않고 기준선으로만 적재한다 (설계 7.7).
	// locked=false는 다른 인스턴스가 이미 tick을 도는 중이라는 뜻이며 오류가 아니다 —
	// 저장 계층의 pg_try_advisory_xact_lock 실패를 그대로 옮긴 것이다 (설계 7.2).
	RunNotificationTick(ctx context.Context, seed bool) (res TickResult, locked bool, err error)
	// HasNotificationEvents는 이벤트가 한 번이라도 적재된 적 있는지 반환한다.
	// seed 적용 여부(설치 첫 tick인지)의 유일한 판단 근거다.
	HasNotificationEvents(ctx context.Context) (bool, error)
	// PurgeNotifications는 보존 기간을 넘긴 종료 상태 행을 지운다 (설계 7.6).
	PurgeNotifications(ctx context.Context, retentionDays int) (PurgeResult, error)
	// ReapExpiredDeliveryLeases는 리스가 만료된 sending 행을 pending으로 되돌린다.
	ReapExpiredDeliveryLeases(ctx context.Context) (reaped int, err error)
}

// SchedulerConfig는 스케줄러 설정이다 (config.NotifyConfig에서 옮겨 담는다).
type SchedulerConfig struct {
	Store SchedulerStore
	// Enabled는 NOTIFY_ENABLED다. false면 스케줄러를 아예 시작하지 않는다.
	Enabled bool
	// Interval은 tick 주기다 (NOTIFY_SCAN_INTERVAL, 기본 1h).
	Interval time.Duration
	// FirstRunSeed는 NOTIFY_FIRST_RUN=seed다. 설치 첫 tick에서 이미 지난 임계값을
	// 발송하지 않고 기준선으로만 적재한다 (설치 당일 폭주 방지).
	FirstRunSeed bool
	// RetentionDays는 NOTIFY_RETENTION_DAYS다. 0 이하면 보존 삭제를 하지 않는다.
	RetentionDays int
	Logger        *slog.Logger
}

// Scheduler는 탐지 tick 루프다.
type Scheduler struct {
	store         SchedulerStore
	enabled       bool
	interval      time.Duration
	firstRunSeed  bool
	retentionDays int
	log           *slog.Logger

	mu sync.Mutex
	// tickDone은 이 프로세스가 tick을 한 번이라도 성공시켰는지다.
	// seed 판정을 첫 tick에서만 하기 위한 상태다.
	tickDone bool
}

// NewScheduler는 스케줄러를 만든다. 생성자는 에러를 반환하지 않는다 —
// 알림 설정 오류가 코어 API를 죽이면 안 된다 (센서 게이트웨이와 같은 원칙).
func NewScheduler(cfg SchedulerConfig) *Scheduler {
	return &Scheduler{
		store:         cfg.Store,
		enabled:       cfg.Enabled,
		interval:      orDefault(cfg.Interval, defaultScanInterval),
		firstRunSeed:  cfg.FirstRunSeed,
		retentionDays: cfg.RetentionDays,
		log:           cfg.Logger,
	}
}

func (s *Scheduler) logger() *slog.Logger {
	if s.log != nil {
		return s.log
	}
	return slog.Default()
}

// StartScheduler는 탐지 tick goroutine을 시작한다. 반환된 stop은 goroutine을 멈추고
// 종료를 기다린다 (ctx 취소로도 멈춘다) — sensorgw.StartReaper와 같은 정지 규약이다.
// NOTIFY_ENABLED=false면 아무것도 시작하지 않고 그 사실만 남긴다.
func (s *Scheduler) StartScheduler(ctx context.Context) (stop func()) {
	if !s.enabled {
		s.logger().Info("notify: scheduler disabled by NOTIFY_ENABLED=false; " +
			"no expiry notifications are detected or delivered")
		return func() {}
	}
	ctx, cancel := context.WithCancel(ctx)
	done := make(chan struct{})
	go func() {
		defer close(done)
		// 기동 시 1회: 발송 도중 죽은 프로세스가 남긴 고아 딜리버리를 되돌린다 (설계 7.5).
		s.reapOrphans(ctx)
		// 첫 tick을 주기만큼 기다리지 않는다 — 기본 주기가 1시간이라 재기동 직후
		// 탐지가 그만큼 늦어지고, seed 기준선은 설치 직후에 잡혀야 한다.
		s.runTick(ctx)

		ticker := time.NewTicker(s.interval)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				s.runTick(ctx)
			}
		}
	}()
	return func() {
		cancel()
		<-done
	}
}

// runTick은 RunOnce를 돌리고 실패를 로그로만 남긴다 (루프는 계속된다).
func (s *Scheduler) runTick(ctx context.Context) {
	if err := s.RunOnce(ctx); err != nil && ctx.Err() == nil {
		s.logger().Error("notify: scheduler tick failed", "error", err.Error())
	}
}

// reapOrphans는 리스가 만료된 sending 딜리버리를 pending으로 되돌린다.
func (s *Scheduler) reapOrphans(ctx context.Context) {
	if s.store == nil {
		return
	}
	n, err := s.store.ReapExpiredDeliveryLeases(ctx)
	if err != nil {
		if ctx.Err() == nil {
			s.logger().Error("notify: reap orphaned deliveries failed", "error", err.Error())
		}
		return
	}
	if n > 0 {
		s.logger().Info("notify: reclaimed orphaned deliveries", "count", n)
	}
}

// RunOnce는 tick 1회다: 탐지·팬아웃 + 보존 삭제 (테스트·수동 실행용).
// 보존 삭제는 tick 성패와 무관하게 시도한다 — 둘은 서로 독립이며,
// 탐지가 막혀도 오래된 행이 무한히 쌓이면 안 된다.
func (s *Scheduler) RunOnce(ctx context.Context) error {
	if s.store == nil {
		return errors.New("notify: scheduler store is not wired")
	}
	return errors.Join(s.detect(ctx), s.purge(ctx))
}

func (s *Scheduler) detect(ctx context.Context) error {
	seed, err := s.seedMode(ctx)
	if err != nil {
		// 첫 tick인지 판단할 근거가 없으면 추측하지 않는다. 다음 tick에서 다시 본다 —
		// 잘못 추측하면 설치 당일 인벤토리 전체가 한꺼번에 발송된다.
		return err
	}
	res, locked, err := s.store.RunNotificationTick(ctx, seed)
	if err != nil {
		return err
	}
	if !locked {
		// 다른 인스턴스가 도는 중이다. 오류가 아니므로 조용히 넘어간다 (설계 7.2).
		s.logger().Debug("notify: scheduler tick skipped; another instance holds the lock")
		return nil
	}
	s.markTickDone()
	if res.Events > 0 || res.Deliveries > 0 {
		s.logger().Info("notify: expiry scan produced events",
			"events", res.Events, "deliveries", res.Deliveries, "seed", seed)
	}
	return nil
}

func (s *Scheduler) purge(ctx context.Context) error {
	if s.retentionDays < 1 {
		return nil
	}
	res, err := s.store.PurgeNotifications(ctx, s.retentionDays)
	if err != nil {
		return err
	}
	if res.Deliveries > 0 || res.Events > 0 || res.InboxItems > 0 {
		s.logger().Info("notify: purged notifications past retention",
			"retention_days", s.retentionDays, "deliveries", res.Deliveries,
			"events", res.Events, "inbox_items", res.InboxItems)
	}
	return nil
}

// seedMode는 이번 tick을 seed(기준선 적재)로 돌릴지 정한다.
// 판정은 프로세스 생애 첫 성공 tick 전까지만 하고, 그 뒤에는 항상 send다.
func (s *Scheduler) seedMode(ctx context.Context) (bool, error) {
	s.mu.Lock()
	skip := !s.firstRunSeed || s.tickDone
	s.mu.Unlock()
	if skip {
		return false, nil
	}
	has, err := s.store.HasNotificationEvents(ctx)
	if err != nil {
		return false, err
	}
	return !has, nil
}

func (s *Scheduler) markTickDone() {
	s.mu.Lock()
	s.tickDone = true
	s.mu.Unlock()
}
