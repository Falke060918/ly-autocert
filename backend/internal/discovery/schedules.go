// schedules.go — 정기 스캔 스케줄러 (0019 scan_schedules).
//
// 정기 스캔은 **새 실행 엔진이 아니라 발주 트리거**다. tick 1회가 하는 일은 둘이다.
//  1. 마감 — 발주한 스윕이 끝난 실행(run)을 종료 상태로 닫는다.
//  2. 발주 — 주기가 도래한 full·preset을 claim하고 스윕 1건씩 낸다.
//
// registered는 여기서 돌지 않는다. 그 종류의 행은 기존 재스캔 엔진
// (endpoint_monitors)의 **설정 행**이며, 오버라이드는 rescan.go가 읽는다.
//
// 마감이 발주보다 앞인 이유: 실행 중인 run이 있으면 store의 클레임이 그 주기를
// 건너뛴다(uq_scan_schedule_runs_active). 끝난 run을 먼저 닫아야 같은 tick에서
// 다음 주기를 집을 수 있다.
//
// 배선 예 (cmd/server):
//
//	stop := discovery.NewManager(st).
//		UseSweepOptions(...).UseScheduleOptions(discovery.ScheduleOptions{ ... }).
//		StartScanScheduleRunner(ctx)
//	defer stop()
package discovery

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"github.com/Falke060918/INHERIT_AutoCert/backend/internal/store"
)

const (
	// scheduleTickInterval은 발주 시도 주기다. 실제 빈도는 next_run_at이 정하므로
	// tick이 촘촘해도 주기가 아니면 아무 일도 일어나지 않는다(재스캔 tick과 같은 규약).
	scheduleTickInterval = 1 * time.Minute

	// scheduleRunListLimit은 한 tick에서 마감 판정할 활성 실행 수 상한이다.
	// 스케줄당 활성 실행은 1건이라 실무상 스케줄 수를 넘지 않는다.
	scheduleRunListLimit = 200

	// scheduleOrphanRunAfter는 스윕이 붙지 않은 실행을 실패로 닫는 기준이다.
	// 발주와 attach 사이에서 프로세스가 죽으면 그 실행이 running으로 남아 스케줄이
	// 영영 다음 주기를 집지 못한다 — 활성 유니크 인덱스가 곧 영구 잠금이 된다.
	scheduleOrphanRunAfter = 15 * time.Minute
)

// DefaultScheduleInterval은 interval_seconds가 비어 있는 스케줄의 주기다.
// 시드된 full은 30일을 명시하고 있으므로 이 값은 사용자가 주기를 지정하지 않은
// preset에만 적용된다.
const DefaultScheduleInterval = 24 * time.Hour

// DefaultSchedulesPerTick은 한 tick이 claim하는 스케줄 수 상한이다.
// 스윕은 며칠짜리 배경 작업이라 한 번에 여러 건이 시작될 이유가 없다.
const DefaultSchedulesPerTick = 4

// ScheduleOptions는 정기 스캔 스케줄러 설정이다.
// 전용 환경변수를 새로 만들지 않는다 — 발주 대상이 스윕이므로 스윕 설정을 따른다.
type ScheduleOptions struct {
	// Enabled가 false면 tick 루프를 아예 시작하지 않는다.
	// 스윕 발주자가 없으면 발주해 봐야 청크를 돌 주체가 없으므로
	// cmd/server는 SCAN_SWEEP_ENABLED를 그대로 넘긴다.
	Enabled bool
	// DefaultInterval은 interval_seconds가 NULL인 스케줄의 주기다.
	DefaultInterval time.Duration
	// MaxPerTick은 한 tick이 claim하는 스케줄 수 상한이다.
	MaxPerTick int
	// MaxDays는 발주할 스윕의 ETA 상한이다(SCAN_SWEEP_MAX_DAYS).
	// 넘으면 스윕을 만들지 않고 그 실행을 failed로 닫는다 — 대역을 좁히라는 신호다.
	MaxDays int
}

// DefaultScheduleOptions는 배선이 없을 때의 기본값이다.
func DefaultScheduleOptions() ScheduleOptions {
	return ScheduleOptions{
		Enabled:         true,
		DefaultInterval: DefaultScheduleInterval,
		MaxPerTick:      DefaultSchedulesPerTick,
		MaxDays:         store.DefaultSweepMaxDays,
	}
}

// normalized는 0·음수를 기본값으로 되돌린다(배선 누락 방어 — 스윕·재스캔과 같은 규약).
func (o ScheduleOptions) normalized() ScheduleOptions {
	d := DefaultScheduleOptions()
	if o.DefaultInterval <= 0 {
		o.DefaultInterval = d.DefaultInterval
	}
	if o.MaxPerTick < 1 {
		o.MaxPerTick = d.MaxPerTick
	}
	if o.MaxDays < 1 {
		o.MaxDays = d.MaxDays
	}
	return o
}

// UseScheduleOptions는 정기 스캔 설정을 주입한 Manager를 돌려준다(자기 자신).
func (m *Manager) UseScheduleOptions(opts ScheduleOptions) *Manager {
	m.schedule = opts.normalized()
	return m
}

// ScheduleSummary는 tick 1회의 처리 결과다(로그·테스트용).
type ScheduleSummary struct {
	// Skipped는 다른 인스턴스가 tick을 도는 중이라 발주 단계를 건너뛰었다는 뜻이다.
	Skipped bool
	// Closed는 이번 tick에 마감한 실행 수다.
	Closed int
	// Started는 이번 tick에 발주한 실행 수다.
	Started int
	// Failed는 발주에 실패해 failed로 닫은 실행 수다.
	Failed int
}

// scheduleRunner는 tick 루프의 상태다.
type scheduleRunner struct {
	m    *Manager
	opts ScheduleOptions
}

// StartScanScheduleRunner는 정기 스캔 tick goroutine을 시작한다.
// 반환된 stop은 goroutine을 멈추고 종료를 기다린다(ctx 취소로도 멈춘다) —
// StartSweepScheduler와 같은 정지 규약이다.
//
// Enabled=false면 아무것도 띄우지 않고 사유 로그 1줄만 남긴다(stop은 no-op).
func (m *Manager) StartScanScheduleRunner(ctx context.Context) (stop func()) {
	return m.startScanScheduleRunner(ctx, scheduleTickInterval)
}

// startScanScheduleRunner는 tick 주기를 인자로 받는 내부 구현이다(테스트가 짧은 주기로 돌린다).
func (m *Manager) startScanScheduleRunner(ctx context.Context, tick time.Duration) (stop func()) {
	opts := m.schedule.normalized()
	if !opts.Enabled {
		slog.Info("discovery: scan schedule runner disabled (SCAN_SWEEP_ENABLED=false)")
		return func() {}
	}
	if tick <= 0 {
		tick = scheduleTickInterval
	}

	s := &scheduleRunner{m: m, opts: opts}
	ctx, cancel := context.WithCancel(ctx)
	done := make(chan struct{})
	go func() {
		defer close(done)
		ticker := time.NewTicker(tick)
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

// RunScanScheduleTickOnce는 tick 1회를 즉시 실행한다(테스트·수동 점검용).
// 스케줄러를 띄우지 않아도 Manager 설정만으로 동작한다.
func (m *Manager) RunScanScheduleTickOnce(ctx context.Context) (ScheduleSummary, error) {
	s := &scheduleRunner{m: m, opts: m.schedule.normalized()}
	return s.runOnce(ctx)
}

// runTick은 runOnce를 돌리고 실패를 로그로만 남긴다(루프는 계속된다).
func (s *scheduleRunner) runTick(ctx context.Context) {
	sum, err := s.runOnce(ctx)
	if err != nil && ctx.Err() == nil {
		slog.Error("discovery: scan schedule tick failed", "error", err.Error())
	}
	if sum.Started > 0 || sum.Closed > 0 || sum.Failed > 0 {
		slog.Info("discovery: scan schedule tick",
			"started", sum.Started, "closed", sum.Closed, "failed", sum.Failed)
	}
}

// runOnce는 마감 → 발주 두 단계를 순서대로 실행한다.
// 앞 단계의 실패가 뒤 단계를 막지 않는다(재스캔 tick과 같은 규약).
func (s *scheduleRunner) runOnce(ctx context.Context) (ScheduleSummary, error) {
	var (
		sum  ScheduleSummary
		errs []error
	)

	closed, err := s.closeFinishedRuns(ctx)
	if err != nil {
		errs = append(errs, err)
	}
	sum.Closed = closed

	due, locked, err := s.m.store.ClaimDueScanSchedules(ctx, s.opts.DefaultInterval, s.opts.MaxPerTick)
	if err != nil {
		errs = append(errs, err)
	}
	if !locked {
		sum.Skipped = true
		return sum, errors.Join(errs...)
	}
	for _, c := range due {
		if err := s.dispatchClaimed(ctx, c); err != nil {
			if ctx.Err() == nil {
				// 대상 원문은 감사·스케줄 행에 이미 있다 — 로그에는 사유와 id만 남긴다.
				slog.Error("discovery: scan schedule dispatch failed",
					"error", err.Error(), "schedule_id", c.ScheduleID, "run_id", c.RunID)
			}
			errs = append(errs, err)
			sum.Failed++
			continue
		}
		sum.Started++
	}
	return sum, errors.Join(errs...)
}

// closeFinishedRuns는 발주한 스윕이 끝난 실행을 같은 상태로 닫는다.
//
// 실행이 running으로 남아 있는 동안 그 스케줄의 주기는 건너뛰어진다(설계 규약 ②).
// 즉 이 마감이 곧 "다음 주기를 열어 주는" 단계다.
func (s *scheduleRunner) closeFinishedRuns(ctx context.Context) (int, error) {
	list, err := s.m.store.ListScanScheduleRuns(ctx, store.ListScanScheduleRunsInput{
		Statuses: []string{store.ScheduleRunRunning},
		Page:     1,
		PageSize: scheduleRunListLimit,
	})
	if err != nil {
		return 0, err
	}

	closed := 0
	var errs []error
	for _, run := range list.Items {
		res, ok, err := s.runOutcome(ctx, run)
		if err != nil {
			errs = append(errs, err)
			continue
		}
		if !ok {
			continue
		}
		if _, err := s.m.store.FinishScanScheduleRun(ctx, run.ID, res); err != nil {
			// 다른 인스턴스가 먼저 닫았으면 오류가 아니다.
			if errors.Is(err, store.ErrScanScheduleRunNotFound) {
				continue
			}
			errs = append(errs, err)
			continue
		}
		closed++
	}
	return closed, errors.Join(errs...)
}

// runOutcome은 실행 1건이 끝났는지와 그 종료 상태를 판정한다.
// ok=false면 아직 도는 중이라는 뜻이다.
func (s *scheduleRunner) runOutcome(ctx context.Context, run store.ScanScheduleRun) (store.ScanScheduleRunResult, bool, error) {
	if run.SweepID == nil {
		// 스윕이 붙지 않은 실행: registered 수동 실행은 발주 직후 스스로 닫히므로
		// 여기 남아 있다면 발주와 attach 사이에서 프로세스가 죽은 것이다.
		if time.Since(run.StartedAt) < scheduleOrphanRunAfter {
			return store.ScanScheduleRunResult{}, false, nil
		}
		reason := "스윕이 발주되지 않은 채 남은 실행을 회수했습니다."
		return store.ScanScheduleRunResult{
			Status: store.ScheduleRunFailed,
			Error:  &reason,
		}, true, nil
	}

	sw, err := s.m.store.GetSweep(ctx, *run.SweepID)
	if errors.Is(err, store.ErrSweepNotFound) {
		reason := "발주한 스윕을 찾을 수 없습니다."
		return store.ScanScheduleRunResult{
			Status: store.ScheduleRunFailed,
			Error:  &reason,
		}, true, nil
	}
	if err != nil {
		return store.ScanScheduleRunResult{}, false, err
	}

	var status string
	switch sw.Status {
	case store.SweepStatusCompleted:
		status = store.ScheduleRunCompleted
	case store.SweepStatusFailed:
		status = store.ScheduleRunFailed
	case store.SweepStatusCanceled:
		status = store.ScheduleRunCanceled
	default:
		return store.ScanScheduleRunResult{}, false, nil // queued/running/paused — 아직 도는 중이다.
	}
	return store.ScanScheduleRunResult{
		Status:              status,
		SweepID:             run.SweepID,
		DispatchedJobs:      sw.Progress.ChunksCompleted,
		DispatchedEndpoints: sw.TotalEndpoints,
		Error:               sw.LastError,
	}, true, nil
}

// scheduleDispatch는 스윕 1건을 발주하는 데 필요한 스케줄 값이다.
// claim 결과(ClaimedScanSchedule)와 설정 행(ScanSchedule) 둘 다 이 형태로 옮겨 담아
// 주기 실행과 '지금 실행'이 **같은 발주 경로**를 타게 한다.
type scheduleDispatch struct {
	scheduleID    string
	runID         string
	name          string
	targets       []string
	portSpec      string
	executorScope string
	ratePPS       int
}

// dispatchClaimed는 claim된 스케줄 1건을 발주한다. 실패하면 그 실행을 failed로 닫는다
// (닫지 않으면 활성 유니크 인덱스 때문에 그 스케줄이 영영 다음 주기를 집지 못한다).
func (s *scheduleRunner) dispatchClaimed(ctx context.Context, c store.ClaimedScanSchedule) error {
	return s.dispatch(ctx, scheduleDispatch{
		scheduleID:    c.ScheduleID,
		runID:         c.RunID,
		name:          c.Name,
		targets:       c.Targets,
		portSpec:      c.PortSpec,
		executorScope: c.ExecutorScope,
		ratePPS:       c.RatePPS,
	})
}

// dispatch는 스윕을 만들고 실행 행에 연결한다.
func (s *scheduleRunner) dispatch(ctx context.Context, d scheduleDispatch) error {
	sweepID, err := s.createSweep(ctx, d)
	if err != nil {
		s.failRun(ctx, d.runID, err)
		return err
	}
	if err := s.m.store.AttachScanScheduleRunSweep(ctx, d.runID, sweepID); err != nil {
		// 스윕은 이미 만들어졌다 — 취소해 두지 않으면 아무도 모르는 채로 돈다.
		if _, cerr := s.m.store.CancelSweep(ctx, sweepID, store.SweepActor{}); cerr != nil && ctx.Err() == nil {
			slog.Error("discovery: cancel orphan schedule sweep failed",
				"error", cerr.Error(), "sweep_id", sweepID)
		}
		s.failRun(ctx, d.runID, err)
		return err
	}
	slog.Info("discovery: scan schedule sweep dispatched",
		"schedule_id", d.scheduleID, "run_id", d.runID, "sweep_id", sweepID)
	return nil
}

// createSweep은 스케줄 정의로 스윕 1건을 만든다.
// 대상·포트 전개는 스윕 생성 API와 **같은 구현**을 쓴다(파서는 한 곳이다).
func (s *scheduleRunner) createSweep(ctx context.Context, d scheduleDispatch) (string, error) {
	ports, err := ParsePortSpec(d.portSpec)
	if err != nil {
		return "", err
	}
	ips, err := expandSweepIPs(d.targets)
	if err != nil {
		return "", err
	}

	name := strings.TrimSpace(d.name)
	rate := d.ratePPS
	if rate < 1 {
		rate = s.m.sweep.normalized().RatePPS
	}
	// 발주자가 스케줄러라 actor는 NULL이다 — 사람이 낸 발주와는 schedule_id로 갈린다
	// (재스캔 발주가 origin으로 갈리는 것과 같은 규약).
	sweep, err := s.m.store.CreateSweep(ctx, store.SweepInput{
		Name:           &name,
		Targets:        d.targets,
		IPCount:        len(ips),
		PortSpec:       d.portSpec,
		PortCount:      len(ports),
		ExecutorScope:  d.executorScope,
		RatePPS:        rate,
		ChunkEndpoints: s.m.sweep.normalized().ChunkEndpoints,
		MaxDays:        s.opts.MaxDays,
		ScheduleID:     &d.scheduleID,
	})
	if err != nil {
		return "", err
	}
	return sweep.ID, nil
}

// failRun은 발주 실패를 실행 행에 남긴다. 사유 문자열은 파서·store가 만든 것이라
// 인증서·키가 실릴 경로가 없다.
func (s *scheduleRunner) failRun(ctx context.Context, runID string, cause error) {
	reason := cause.Error()
	if _, err := s.m.store.FinishScanScheduleRun(ctx, runID, store.ScanScheduleRunResult{
		Status: store.ScheduleRunFailed,
		Error:  &reason,
	}); err != nil && ctx.Err() == nil {
		slog.Error("discovery: close failed scan schedule run",
			"error", err.Error(), "run_id", runID)
	}
}

// ---------------------------------------------------------------------------
// '지금 실행' · 취소
// ---------------------------------------------------------------------------

// RunScanScheduleNow는 스케줄 1건을 즉시 1회 실행한다(API의 '지금 실행').
//
// **주기를 리셋하지 않는다** — store가 next_run_at을 그대로 두고 실행 행만 연다
// (사용자 확정 규약). 활성 실행이 있으면 *store.ScanScheduleRunActiveError이고
// 핸들러가 409로 옮긴다.
//
// registered는 스윕이 아니라 기존 재스캔 엔진을 1배치 발주하고 곧바로 닫는다 —
// 그 종류에는 발주 결과를 기다릴 스윕 행이 없다.
//
// ctx는 **요청 컨텍스트가 아니라 서버 수명 컨텍스트**를 넘겨야 한다(재스캔 수동
// 발주와 같은 이유 — 응답과 함께 취소되면 발주한 Job이 종료 상태를 못 찍는다).
func (m *Manager) RunScanScheduleNow(ctx context.Context, scheduleID string, act store.ScanScheduleActor) (*store.ScanScheduleRun, error) {
	sc, err := m.store.GetScanSchedule(ctx, scheduleID)
	if err != nil {
		return nil, err
	}
	run, err := m.store.StartManualScanScheduleRun(ctx, scheduleID, act)
	if err != nil {
		return nil, err
	}

	s := &scheduleRunner{m: m, opts: m.schedule.normalized()}
	if sc.Kind == store.ScheduleKindRegistered {
		if err := s.runRegisteredNow(ctx, run.ID); err != nil {
			return nil, err
		}
		return m.store.GetScanScheduleRun(ctx, run.ID)
	}

	portSpec := ""
	if sc.PortSpec != nil {
		portSpec = *sc.PortSpec
	}
	rate := 0
	if sc.RatePPS != nil {
		rate = *sc.RatePPS
	}
	if err := s.dispatch(ctx, scheduleDispatch{
		scheduleID:    sc.ID,
		runID:         run.ID,
		name:          sc.Name,
		targets:       sc.Targets,
		portSpec:      portSpec,
		executorScope: sc.ExecutorScope,
		ratePPS:       rate,
	}); err != nil {
		return nil, fmt.Errorf("%w: %s", store.ErrScanScheduleInput, err.Error())
	}
	return m.store.GetScanScheduleRun(ctx, run.ID)
}

// runRegisteredNow는 등록된 인증서 정기 스캔의 '지금 실행'이다.
// 기존 재스캔 1배치를 발주하고(due 판정은 그대로) 실행 행을 즉시 닫는다.
func (s *scheduleRunner) runRegisteredNow(ctx context.Context, runID string) error {
	sum, err := s.m.DispatchRescanOnce(ctx)
	if err != nil {
		s.failRun(ctx, runID, err)
		return err
	}
	if _, err := s.m.store.FinishScanScheduleRun(ctx, runID, store.ScanScheduleRunResult{
		Status:              store.ScheduleRunCompleted,
		DispatchedJobs:      sum.ServerJobs + sum.SensorJobs,
		DispatchedEndpoints: int64(sum.Claimed),
	}); err != nil {
		return err
	}
	return nil
}

// CancelScanScheduleRun은 실행 1건을 취소한다 — 발주한 스윕을 먼저 멈추고
// 실행 이력을 canceled로 닫는다.
//
// 순서가 규약이다: 스윕을 세우지 않은 채 실행만 닫으면 다음 주기가 열리면서
// 두 스윕이 동시에 돈다(겹치지 않는다는 전제가 깨진다).
func (m *Manager) CancelScanScheduleRun(ctx context.Context, runID string, act store.ScanScheduleActor) (*store.ScanScheduleRun, error) {
	run, err := m.store.GetScanScheduleRun(ctx, runID)
	if err != nil {
		return nil, err
	}
	if run.Status != store.ScheduleRunRunning {
		return nil, store.ErrScanScheduleRunState
	}
	if run.SweepID != nil {
		_, err := m.store.CancelSweep(ctx, *run.SweepID, store.SweepActor{
			ActorID:   act.ActorID,
			RequestID: act.RequestID,
			SourceIP:  act.SourceIP,
		})
		// 이미 종결된 스윕(ErrSweepState)은 취소할 것이 없다 — 실행만 닫으면 된다.
		if err != nil && !errors.Is(err, store.ErrSweepState) && !errors.Is(err, store.ErrSweepNotFound) {
			return nil, err
		}
	}
	return m.store.CancelScanScheduleRun(ctx, runID, act)
}
