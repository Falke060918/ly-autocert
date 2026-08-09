package notify

// 스케줄러 검증 (docs/design/05-notifications.md 7).
// - NOTIFY_ENABLED=false면 아무것도 시작하지 않는다.
// - 락을 못 잡은 tick은 오류가 아니다 (다른 인스턴스가 도는 중).
// - seed 판정은 프로세스 첫 성공 tick에서만 한다.
// - 보존 삭제는 tick마다 돌고, 탐지가 실패해도 함께 시도한다.
// - 기동 시 고아 딜리버리를 1회 회수하고, stop()은 깨끗이 돌아온다.
//
// 마지막 테스트만 실제 DB를 쓴다 (TEST_DATABASE_URL 미설정 시 스킵).
// 전용 테스트 DB에만 돌린다 — 픽스처는 실행 시점에 만들고 정리한다 (보안 하드 규칙 4).

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"log/slog"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/Falke060918/INHERIT_AutoCert/backend/internal/db"
	"github.com/Falke060918/INHERIT_AutoCert/backend/internal/store"
)

// ===== 테스트 더블 =====

type fakeSchedulerStore struct {
	mu sync.Mutex

	tickSeeds  []bool
	tickResult TickResult
	tickLocked bool
	tickErr    error

	hasEvents    bool
	hasEventsErr error
	hasCalls     int

	purgeDays []int
	purgeErr  error

	reaps      int
	reapResult int

	firstTick chan struct{}
	once      sync.Once
}

func newFakeSchedulerStore() *fakeSchedulerStore {
	return &fakeSchedulerStore{tickLocked: true, firstTick: make(chan struct{})}
}

func (f *fakeSchedulerStore) RunNotificationTick(_ context.Context, seed bool) (TickResult, bool, error) {
	f.mu.Lock()
	f.tickSeeds = append(f.tickSeeds, seed)
	res, locked, err := f.tickResult, f.tickLocked, f.tickErr
	f.mu.Unlock()
	f.once.Do(func() { close(f.firstTick) })
	return res, locked, err
}

func (f *fakeSchedulerStore) HasNotificationEvents(context.Context) (bool, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.hasCalls++
	return f.hasEvents, f.hasEventsErr
}

func (f *fakeSchedulerStore) PurgeNotifications(_ context.Context, days int) (PurgeResult, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.purgeDays = append(f.purgeDays, days)
	return PurgeResult{}, f.purgeErr
}

func (f *fakeSchedulerStore) ReapExpiredDeliveryLeases(context.Context) (int, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.reaps++
	return f.reapResult, nil
}

func (f *fakeSchedulerStore) seeds() []bool {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]bool(nil), f.tickSeeds...)
}

func (f *fakeSchedulerStore) counts() (ticks, purges, reaps, hasCalls int) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.tickSeeds), len(f.purgeDays), f.reaps, f.hasCalls
}

func testScheduler(st SchedulerStore, cfg SchedulerConfig) *Scheduler {
	cfg.Store = st
	if cfg.Logger == nil {
		cfg.Logger = slog.New(slog.DiscardHandler)
	}
	return NewScheduler(cfg)
}

// ===== 기동 · 정지 =====

func TestSchedulerDisabledDoesNotStart(t *testing.T) {
	st := newFakeSchedulerStore()
	log, buf := bufferLogger()
	s := testScheduler(st, SchedulerConfig{Enabled: false, Interval: time.Millisecond, Logger: log})

	stop := s.StartScheduler(context.Background())
	stop() // no-op이어야 하며 블록되지 않는다.

	time.Sleep(20 * time.Millisecond)
	if ticks, purges, reaps, _ := st.counts(); ticks+purges+reaps != 0 {
		t.Errorf("disabled scheduler touched the store: ticks=%d purges=%d reaps=%d", ticks, purges, reaps)
	}
	if !strings.Contains(buf.String(), "NOTIFY_ENABLED=false") {
		t.Errorf("disabled scheduler did not say why:\n%s", buf.String())
	}
}

func TestSchedulerReapsOrphansOnStartAndTicks(t *testing.T) {
	st := newFakeSchedulerStore()
	s := testScheduler(st, SchedulerConfig{Enabled: true, Interval: 5 * time.Millisecond, RetentionDays: 180})

	stop := s.StartScheduler(context.Background())
	select {
	case <-st.firstTick:
	case <-time.After(5 * time.Second):
		stop()
		t.Fatal("scheduler never ticked")
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

	ticks, purges, reaps, _ := st.counts()
	if reaps != 1 {
		t.Errorf("orphan reap ran %d times, want exactly 1 (startup only)", reaps)
	}
	if ticks < 1 || purges < 1 {
		t.Errorf("ticks=%d purges=%d, want at least 1 each", ticks, purges)
	}
	// 정지 후에는 더 돌지 않는다.
	after := ticks
	time.Sleep(30 * time.Millisecond)
	if now, _, _, _ := st.counts(); now != after {
		t.Errorf("scheduler kept ticking after stop: %d -> %d", after, now)
	}
}

// ===== tick 1회 =====

func TestSchedulerRunOnceDetectsAndPurges(t *testing.T) {
	st := newFakeSchedulerStore()
	st.tickResult = TickResult{Events: 3, Deliveries: 5}
	s := testScheduler(st, SchedulerConfig{Enabled: true, RetentionDays: 90})

	if err := s.RunOnce(context.Background()); err != nil {
		t.Fatalf("RunOnce() = %v", err)
	}
	if ticks, purges, _, _ := st.counts(); ticks != 1 || purges != 1 {
		t.Errorf("ticks=%d purges=%d, want 1 each", ticks, purges)
	}
	if st.purgeDays[0] != 90 {
		t.Errorf("retention days = %d, want 90", st.purgeDays[0])
	}
}

func TestSchedulerSkipsPurgeWithoutRetention(t *testing.T) {
	st := newFakeSchedulerStore()
	s := testScheduler(st, SchedulerConfig{Enabled: true, RetentionDays: 0})
	if err := s.RunOnce(context.Background()); err != nil {
		t.Fatalf("RunOnce() = %v", err)
	}
	if _, purges, _, _ := st.counts(); purges != 0 {
		t.Errorf("purges = %d, want 0", purges)
	}
}

func TestSchedulerLockNotAcquiredIsNotAnError(t *testing.T) {
	// 다른 인스턴스가 tick을 도는 중이라는 뜻이므로 조용히 넘어가야 한다 (설계 7.2).
	st := newFakeSchedulerStore()
	st.tickLocked = false
	s := testScheduler(st, SchedulerConfig{Enabled: true, RetentionDays: 180})

	if err := s.RunOnce(context.Background()); err != nil {
		t.Fatalf("RunOnce() = %v, want nil on lock contention", err)
	}
	// 보존 삭제는 락과 무관하게 돈다.
	if _, purges, _, _ := st.counts(); purges != 1 {
		t.Errorf("purges = %d, want 1", purges)
	}
}

func TestSchedulerPurgesEvenWhenDetectFails(t *testing.T) {
	st := newFakeSchedulerStore()
	st.tickErr = errors.New("deadlock detected")
	s := testScheduler(st, SchedulerConfig{Enabled: true, RetentionDays: 180})

	if err := s.RunOnce(context.Background()); err == nil {
		t.Fatal("RunOnce() = nil, want the detect error")
	}
	if _, purges, _, _ := st.counts(); purges != 1 {
		t.Errorf("purges = %d, want 1 (retention must not depend on detection)", purges)
	}
}

func TestSchedulerWithoutStoreFails(t *testing.T) {
	s := NewScheduler(SchedulerConfig{Enabled: true, Logger: slog.New(slog.DiscardHandler)})
	if err := s.RunOnce(context.Background()); err == nil {
		t.Fatal("RunOnce() = nil, want a wiring error")
	}
}

// ===== 첫 실행 seed 판정 =====

func TestSchedulerSeedsOnlyOnTheFirstRun(t *testing.T) {
	st := newFakeSchedulerStore()
	st.hasEvents = false // 이벤트가 하나도 없는 새 설치
	s := testScheduler(st, SchedulerConfig{Enabled: true, FirstRunSeed: true, RetentionDays: 180})

	for i := 0; i < 3; i++ {
		if err := s.RunOnce(context.Background()); err != nil {
			t.Fatalf("RunOnce(%d) = %v", i, err)
		}
	}
	want := []bool{true, false, false}
	got := st.seeds()
	if len(got) != len(want) {
		t.Fatalf("tick count = %d, want %d", len(got), len(want))
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("tick %d seed = %v, want %v", i, got[i], want[i])
		}
	}
	if _, _, _, hasCalls := st.counts(); hasCalls != 1 {
		t.Errorf("HasNotificationEvents called %d times, want 1", hasCalls)
	}
}

func TestSchedulerDoesNotSeedWhenEventsAlreadyExist(t *testing.T) {
	st := newFakeSchedulerStore()
	st.hasEvents = true // 재기동이다. seed로 돌리면 새 임계값을 놓친다.
	s := testScheduler(st, SchedulerConfig{Enabled: true, FirstRunSeed: true, RetentionDays: 180})

	if err := s.RunOnce(context.Background()); err != nil {
		t.Fatalf("RunOnce() = %v", err)
	}
	if seeds := st.seeds(); len(seeds) != 1 || seeds[0] {
		t.Errorf("seeds = %v, want [false]", seeds)
	}
}

func TestSchedulerNeverSeedsWhenFirstRunIsSend(t *testing.T) {
	st := newFakeSchedulerStore()
	st.hasEvents = false
	s := testScheduler(st, SchedulerConfig{Enabled: true, FirstRunSeed: false, RetentionDays: 180})

	if err := s.RunOnce(context.Background()); err != nil {
		t.Fatalf("RunOnce() = %v", err)
	}
	if seeds := st.seeds(); len(seeds) != 1 || seeds[0] {
		t.Errorf("seeds = %v, want [false]", seeds)
	}
	if _, _, _, hasCalls := st.counts(); hasCalls != 0 {
		t.Errorf("HasNotificationEvents called %d times, want 0", hasCalls)
	}
}

func TestSchedulerSkipsTickWhenSeedDecisionIsUnknown(t *testing.T) {
	// 첫 tick인지 모를 때 추측하면 설치 당일 인벤토리 전체가 발송된다.
	st := newFakeSchedulerStore()
	st.hasEventsErr = errors.New("connection reset")
	s := testScheduler(st, SchedulerConfig{Enabled: true, FirstRunSeed: true, RetentionDays: 180})

	if err := s.RunOnce(context.Background()); err == nil {
		t.Fatal("RunOnce() = nil, want the lookup error")
	}
	if ticks, _, _, _ := st.counts(); ticks != 0 {
		t.Errorf("ticks = %d, want 0 (detection must not run on a guess)", ticks)
	}
}

func TestSchedulerRetriesSeedDecisionAfterLockContention(t *testing.T) {
	// 락을 못 잡은 tick은 "첫 tick을 마쳤다"로 치지 않는다.
	st := newFakeSchedulerStore()
	st.tickLocked = false
	st.hasEvents = false
	s := testScheduler(st, SchedulerConfig{Enabled: true, FirstRunSeed: true, RetentionDays: 180})

	if err := s.RunOnce(context.Background()); err != nil {
		t.Fatalf("RunOnce() = %v", err)
	}
	st.mu.Lock()
	st.tickLocked = true
	st.mu.Unlock()
	if err := s.RunOnce(context.Background()); err != nil {
		t.Fatalf("RunOnce() = %v", err)
	}
	if seeds := st.seeds(); len(seeds) != 2 || !seeds[0] || !seeds[1] {
		t.Errorf("seeds = %v, want [true true]", seeds)
	}
}

// ===== 실제 저장 계층 연동 =====

// storeSchedulerAdapter는 저장 계층을 SchedulerStore로 잇는 얇은 어댑터다.
// cmd/server가 배선할 때 쓰는 것과 같은 모양이며, 이것이 컴파일된다는 사실 자체가
// "인터페이스가 store.Store로 충족 가능하다"를 고정한다.
type storeSchedulerAdapter struct{ st *store.Store }

var _ SchedulerStore = storeSchedulerAdapter{}

func (a storeSchedulerAdapter) RunNotificationTick(ctx context.Context, seed bool) (TickResult, bool, error) {
	mode := store.DetectModeSend
	if seed {
		mode = store.DetectModeSeed
	}
	res, locked, err := a.st.RunNotificationTick(ctx, mode)
	if err != nil || res == nil {
		return TickResult{}, locked, err
	}
	return TickResult{Events: len(res.Events), Deliveries: res.Deliveries}, locked, nil
}

func (a storeSchedulerAdapter) HasNotificationEvents(ctx context.Context) (bool, error) {
	return a.st.HasNotificationEvents(ctx)
}

func (a storeSchedulerAdapter) PurgeNotifications(ctx context.Context, days int) (PurgeResult, error) {
	res, err := a.st.PurgeNotifications(ctx, days)
	if err != nil || res == nil {
		return PurgeResult{}, err
	}
	return PurgeResult{Deliveries: res.Deliveries, Events: res.Events, InboxItems: res.InboxItems}, nil
}

func (a storeSchedulerAdapter) ReapExpiredDeliveryLeases(ctx context.Context) (int, error) {
	return a.st.ReapExpiredDeliveryLeases(ctx)
}

func randSuffix(t *testing.T) string {
	t.Helper()
	buf := make([]byte, 6)
	if _, err := rand.Read(buf); err != nil {
		t.Fatalf("random: %v", err)
	}
	return hex.EncodeToString(buf)
}

func TestSchedulerTickCreatesEventsAndDeliveries(t *testing.T) {
	dsn := os.Getenv("TEST_DATABASE_URL")
	if dsn == "" {
		t.Skip("TEST_DATABASE_URL not set; skipping notify scheduler integration test")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	pool, err := db.Connect(ctx, dsn)
	if err != nil {
		t.Fatalf("connect: %v", err)
	}
	t.Cleanup(func() { pool.Close() })
	if err := db.Migrate(pool); err != nil {
		t.Fatalf("migrate: %v", err)
	}

	suffix := randSuffix(t)
	// 시드 정책('기본 만료 알림')은 role=system_administrator에게 알림함으로 보낸다.
	// password_hash는 검증에 쓰이지 않는 자리 채움값이며 실제 자격증명이 아니다.
	var userID string
	if err := pool.QueryRow(`
		INSERT INTO users (username, display_name, password_hash, role, email)
		VALUES ($1, $1, $2, 'system_administrator', $3) RETURNING id`,
		"notify-sched-"+suffix, "x-not-a-hash-"+suffix,
		"notify-sched-"+suffix+"@example.test").Scan(&userID); err != nil {
		t.Fatalf("insert test user: %v", err)
	}
	t.Cleanup(func() {
		_, _ = pool.Exec(`DELETE FROM users WHERE id = $1::uuid`, userID)
	})

	// 만료까지 5일 — 시드 정책의 기본 임계값 7·30·60·90에 걸린다.
	// 탐지 쿼리는 컬럼만 읽으므로 x509 생성이 필요 없다.
	var certID string
	if err := pool.QueryRow(`
		INSERT INTO certificates (
			fingerprint_sha256, serial_number, subject_dn, issuer_dn, common_name,
			not_before, not_after, public_key_algorithm, public_key_size, signature_algorithm,
			certificate_type, status)
		VALUES ($1, '1', 'CN=' || $2, 'CN=' || $2, $2,
		        now() - interval '365 days', now() + interval '5 days',
		        'ECDSA', 256, 'ECDSA-SHA256', 'server', 'active')
		RETURNING id`,
		randSuffix(t)+randSuffix(t)+randSuffix(t)+randSuffix(t)+randSuffix(t)+"beef",
		"notify-sched-"+suffix+".example.test").Scan(&certID); err != nil {
		t.Fatalf("insert test certificate: %v", err)
	}
	t.Cleanup(func() {
		_, _ = pool.Exec(`DELETE FROM notification_deliveries d
			USING notification_events e WHERE e.id = d.event_id AND e.certificate_id = $1::uuid`, certID)
		_, _ = pool.Exec(`DELETE FROM notification_inbox i
			USING notification_events e WHERE e.id = i.event_id AND e.certificate_id = $1::uuid`, certID)
		_, _ = pool.Exec(`DELETE FROM notification_events WHERE certificate_id = $1::uuid`, certID)
		_, _ = pool.Exec(`DELETE FROM certificates WHERE id = $1::uuid`, certID)
	})

	s := NewScheduler(SchedulerConfig{
		Store:         storeSchedulerAdapter{st: store.New(pool)},
		Enabled:       true,
		FirstRunSeed:  false, // 기준선 적재가 아니라 발송 대상 생성을 확인한다.
		RetentionDays: 180,
		Logger:        slog.New(slog.DiscardHandler),
	})
	if err := s.RunOnce(ctx); err != nil {
		t.Fatalf("RunOnce: %v", err)
	}

	var events int
	if err := pool.QueryRow(`
		SELECT count(*) FROM notification_events
		 WHERE certificate_id = $1::uuid AND event_type = 'certificate.expiring'
		   AND status = 'pending'`, certID).Scan(&events); err != nil {
		t.Fatalf("count events: %v", err)
	}
	if events == 0 {
		t.Fatal("scheduler tick created no expiry events")
	}

	var deliveries int
	if err := pool.QueryRow(`
		SELECT count(*) FROM notification_deliveries d
		  JOIN notification_events e ON e.id = d.event_id
		 WHERE e.certificate_id = $1::uuid AND d.channel = 'inbox' AND d.recipient = $2`,
		certID, userID).Scan(&deliveries); err != nil {
		t.Fatalf("count deliveries: %v", err)
	}
	if deliveries == 0 {
		t.Fatal("fan-out produced no inbox delivery for the administrator recipient")
	}

	// 같은 tick을 다시 돌려도 dedup_key가 이벤트를 1건으로 묶는다.
	if err := s.RunOnce(ctx); err != nil {
		t.Fatalf("second RunOnce: %v", err)
	}
	var again int
	if err := pool.QueryRow(`SELECT count(*) FROM notification_events WHERE certificate_id = $1::uuid`,
		certID).Scan(&again); err != nil {
		t.Fatalf("recount events: %v", err)
	}
	if again != events {
		t.Errorf("event count changed on re-run: %d -> %d", events, again)
	}
}
