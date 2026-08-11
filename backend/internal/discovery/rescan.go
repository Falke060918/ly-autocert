// rescan.go — 주기 재스캔 스케줄러 (docs/design/06-rescan-and-replacement.md 3~8장).
//
// tick 1회가 하는 일은 셋이다.
//  1. 은퇴 — 오래 안 보인 엔드포인트를 발주 대상에서 뺀다 (설계 3.4).
//  2. 발주 — due 엔드포인트를 claim하고 (스코프, 포트)별로 묶어 scan_jobs 1행씩 낸다.
//  3. 전이 — 교체가 확정된 인증서를 replaced로 넘긴다 (설계 8.5).
//
// 3은 관측 트랜잭션 안에서 하지 않는다. 관측은 초당 수십 건이 들어오는 경로이고
// 전이는 감사 해시 체인의 advisory lock을 잡으므로, 섞으면 스캔 처리량이 감사
// 직렬화에 묶인다 — 그래서 별도 배치인 이 tick이 부른다.
//
// **새 job 유형을 만들지 않는다.** 재스캔은 같은 스캔의 다른 발주자이며 상태
// enum·센서 프로토콜은 한 글자도 바뀌지 않는다 (설계 4.2·5.1).
//
// 배선 예 (cmd/server):
//
//	stop := discovery.NewManager(st).
//		UseRescanOptions(discovery.RescanOptions{ /* config.Rescan 값 */ }).
//		StartRescanScheduler(ctx)
//	defer stop()
package discovery

import (
	"context"
	"errors"
	"log/slog"
	"strings"
	"sync"
	"time"

	"github.com/Falke060918/INHERIT_AutoCert/backend/internal/store"
)

// sensorScopePrefix는 targets.executor_scope의 센서 접두사다 (0010 규약).
// 전체 값은 'sensor:<sensor_id>'이고, 그 뒤가 곧 Job에 배정할 센서 id다.
const sensorScopePrefix = "sensor:"

const (
	// rescanTickInterval은 발주 시도 주기다. 가장 짧은 유효 간격(가속 1h)보다
	// 충분히 촘촘하면 되고, 실제 프로브 속도는 due 판정과 레이트 제한이 정한다 —
	// tick을 더 자주 돌려도 due가 아니면 아무 일도 일어나지 않는다.
	rescanTickInterval = 5 * time.Minute

	// 만료 임박 가속 2티어의 경계값이다 (설계 3.1).
	//
	// 근거: 갱신은 만료 직전에 일어난다. 운영자가 D-3에 새 인증서를 깔았는데
	// 재확인이 기본 주기(12h)면 그 사이에 옛 인증서로 D-1 알림이 나간다 —
	// 오탐을 없애려는 기능이 오탐 창을 만드는 셈이다. 반대로 잔여 300일짜리를
	// 6시간마다 볼 이유는 없으므로 가속은 임박 구간에만 건다.
	//
	// 이미 만료된 인증서도 정의상 "잔여 7일 이하"라 가속 구간에 남는다.
	// 의도된 동작이다: 만료된 채 서비스 중인 엔드포인트야말로 교체를 가장 빨리
	// 확인해야 할 대상이고, 총량은 레이트 제한과 배치 상한이 이미 묶고 있다.
	rescanNearExpiry   = 30 * 24 * time.Hour
	rescanNearInterval = 6 * time.Hour

	rescanUrgentExpiry   = 7 * 24 * time.Hour
	rescanUrgentInterval = 1 * time.Hour

	// rescanTimeout은 엔드포인트당 핸드셰이크 타임아웃이다(단발 스캔과 같은 값).
	rescanTimeout = defaultTimeout
)

// 재스캔 기본값 (설계 6장·12.1). 단발 스캔(50pps/동시성 20)과 스윕(10pps/8)
// 사이에 둔다 — 배경 작업이지만 스윕처럼 며칠짜리는 아니고 신선도에 시간 예산이
// 걸려 있다.
const (
	// DefaultRescanInterval은 RESCAN_INTERVAL 기본값이다.
	DefaultRescanInterval = 12 * time.Hour
	// RescanRatePPS는 재스캔 Job의 기본 dial 레이트다.
	RescanRatePPS = 20
	// RescanConcurrency는 재스캔 Job의 워커 동시성이다.
	RescanConcurrency = 8
	// DefaultRescanBatchIPs는 한 tick이 claim하는 엔드포인트 상한이다.
	// 초과분은 다음 tick으로 넘어간다(due 순서는 오래된 순이라 굶지 않는다).
	DefaultRescanBatchIPs = 1024
	// DefaultRescanRetireMisses는 은퇴 판정 연속 미관측 횟수다.
	DefaultRescanRetireMisses = 30
)

// RescanOptions는 재스캔 스케줄러 설정이다 (RESCAN_*).
// config.RescanConfig와 1:1이지만 discovery가 config를 import하지 않도록 별도 타입이다.
type RescanOptions struct {
	// Enabled가 false면 스케줄러를 아예 시작하지 않는다.
	Enabled bool
	// Interval은 가속 구간에 들지 않는 엔드포인트의 재확인 간격이다.
	Interval time.Duration
	// RatePPS는 재스캔 Job의 dial 레이트다.
	RatePPS int
	// Concurrency는 재스캔 Job의 워커 동시성이다.
	Concurrency int
	// BatchIPs는 한 tick이 claim하는 엔드포인트 상한이다.
	BatchIPs int
	// ReplaceConfirm은 교체 확정에 필요한 연속 관측 횟수다.
	ReplaceConfirm int
	// RetireMisses는 은퇴 판정 연속 미관측 횟수다.
	RetireMisses int
}

// DefaultRescanOptions는 설계 12.1의 기본값이다.
func DefaultRescanOptions() RescanOptions {
	return RescanOptions{
		Enabled:        true,
		Interval:       DefaultRescanInterval,
		RatePPS:        RescanRatePPS,
		Concurrency:    RescanConcurrency,
		BatchIPs:       DefaultRescanBatchIPs,
		ReplaceConfirm: store.DefaultReplacementConfirmThreshold,
		RetireMisses:   DefaultRescanRetireMisses,
	}
}

// normalized는 0·음수를 기본값으로 되돌린다. 값 검증·경고는 config가 이미 했으므로
// 여기서는 배선 누락(제로값 구조체)만 방어한다.
func (o RescanOptions) normalized() RescanOptions {
	d := DefaultRescanOptions()
	if o.Interval <= 0 {
		o.Interval = d.Interval
	}
	if o.RatePPS < 1 {
		o.RatePPS = d.RatePPS
	}
	if o.Concurrency < 1 {
		o.Concurrency = d.Concurrency
	}
	if o.BatchIPs < 1 {
		o.BatchIPs = d.BatchIPs
	}
	if o.ReplaceConfirm < 1 {
		o.ReplaceConfirm = d.ReplaceConfirm
	}
	if o.RetireMisses < 1 {
		o.RetireMisses = d.RetireMisses
	}
	return o
}

// intervals는 store가 due 판정에 쓰는 티어 규칙으로 옮겨 담는다.
func (o RescanOptions) intervals() store.RescanIntervals {
	return store.RescanIntervals{
		Default:      o.Interval,
		NearExpiry:   rescanNearExpiry,
		Near:         rescanNearInterval,
		UrgentExpiry: rescanUrgentExpiry,
		Urgent:       rescanUrgentInterval,
	}
}

// UseRescanOptions는 재스캔 설정을 주입한 Manager를 돌려준다(자기 자신).
// cmd/server가 config.RescanConfig 값을 넘기는 자리다.
func (m *Manager) UseRescanOptions(opts RescanOptions) *Manager {
	m.rescan = opts.normalized()
	return m
}

// RescanSummary는 tick 1회의 처리 결과다(로그·API 응답용).
type RescanSummary struct {
	// Skipped는 다른 인스턴스가 tick을 도는 중이라 아무것도 하지 않았다는 뜻이다.
	Skipped bool
	// Disabled는 'registered' 정기 스캔 설정 행이 꺼져 있어 발주를 건너뛰었다는 뜻이다.
	// 은퇴·교체 전이는 그대로 돈다 — 둘은 이미 모인 관측을 정리하는 단계다.
	Disabled bool
	// Retired는 이번 tick에 은퇴시킨 모니터 수다.
	Retired int
	// Claimed는 claim한 엔드포인트 수다(= 이번에 다시 보기로 한 대상).
	Claimed int
	// ServerJobs·SensorJobs는 발주한 scan_jobs 행 수다.
	ServerJobs, SensorJobs int
	// Replaced·LocationsOnly는 교체 전이 배치의 결과다.
	Replaced, LocationsOnly int
}

// rescanScheduler는 발주 루프의 상태다(진행 중인 인프로세스 실행 추적).
type rescanScheduler struct {
	m    *Manager
	opts RescanOptions
	wg   sync.WaitGroup
}

// StartRescanScheduler는 재스캔 tick goroutine을 시작한다. 반환된 stop은 goroutine과
// 진행 중인 Job 실행을 멈추고 종료를 기다린다 (ctx 취소로도 멈춘다) —
// StartSweepScheduler와 같은 정지 규약이다.
//
// Enabled=false면 아무것도 띄우지 않고 사유 로그 1줄만 남긴다(stop은 no-op).
func (m *Manager) StartRescanScheduler(ctx context.Context) (stop func()) {
	return m.startRescanScheduler(ctx, rescanTickInterval)
}

// startRescanScheduler는 tick 주기를 인자로 받는 내부 구현이다 (테스트가 짧은 주기로 돌린다).
func (m *Manager) startRescanScheduler(ctx context.Context, tick time.Duration) (stop func()) {
	opts := m.rescan.normalized()
	if !opts.Enabled {
		slog.Info("discovery: rescan scheduler disabled (RESCAN_ENABLED=false)")
		return func() {}
	}
	if tick <= 0 {
		tick = rescanTickInterval
	}

	s := &rescanScheduler{m: m, opts: opts}
	// 수동 1배치 발주(DispatchRescanOnce)가 같은 인스턴스를 쓰게 남겨둔다 —
	// 그때 시작된 인프로세스 실행도 아래 stop의 wg.Wait에 함께 잡힌다.
	m.rescanSched = s
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
		s.wg.Wait() // 진행 중이던 Job 실행까지 정리한 뒤 돌아온다.
	}
}

// RunRescanOnce는 tick 1회를 즉시 실행한다(수동 발주·테스트용).
// 스케줄러를 띄우지 않아도 쓸 수 있도록 Manager의 설정만으로 동작한다.
func (m *Manager) RunRescanOnce(ctx context.Context) (RescanSummary, error) {
	s := &rescanScheduler{m: m, opts: m.rescan.normalized()}
	sum, err := s.runOnce(ctx)
	s.wg.Wait()
	return sum, err
}

// DispatchRescanOnce는 tick 1회를 **발주까지만** 하고 즉시 돌아온다 —
// 서버 스코프 배치의 인프로세스 스캔은 배경에서 계속 돈다.
//
// RunRescanOnce와 갈리는 지점이 이것 하나다. 수동 발주 API는 202로 즉시 답해야
// 하는데(설계 11.2), 1배치는 기본값에서 1024 엔드포인트 × 20pps ≈ 51초라
// 완주를 기다리면 요청이 그동안 붙잡힌다. 그래서 발주 결과(Job 수·claim 수)만
// 돌려주고 실행은 기다리지 않는다.
//
// ctx는 **요청 컨텍스트가 아니라 서버 수명 컨텍스트**를 넘겨야 한다 — 응답과
// 함께 취소되면 방금 발주한 Job이 종료 상태를 못 찍고 다음 기동의 고아 회수로 넘어간다.
//
// 스케줄러가 떠 있으면 그 인스턴스를 재사용하므로 여기서 시작된 실행도
// 스케줄러 stop의 대기 대상이 된다.
func (m *Manager) DispatchRescanOnce(ctx context.Context) (RescanSummary, error) {
	s := m.rescanSched
	if s == nil {
		s = &rescanScheduler{m: m, opts: m.rescan.normalized()}
	}
	return s.runOnce(ctx)
}

// runTick은 runOnce를 돌리고 실패를 로그로만 남긴다 (루프는 계속된다).
func (s *rescanScheduler) runTick(ctx context.Context) {
	sum, err := s.runOnce(ctx)
	if err != nil && ctx.Err() == nil {
		slog.Error("discovery: rescan tick failed", "error", err.Error())
	}
	if sum.Claimed > 0 || sum.Retired > 0 || sum.Replaced > 0 {
		slog.Info("discovery: rescan tick",
			"claimed", sum.Claimed, "server_jobs", sum.ServerJobs, "sensor_jobs", sum.SensorJobs,
			"retired", sum.Retired, "replaced", sum.Replaced, "locations_only", sum.LocationsOnly)
	}
}

// runOnce는 은퇴 → 발주 → 전이 3단계를 순서대로 실행한다.
// 한 단계의 실패가 뒤 단계를 막지 않는다 — 셋은 서로 독립이고, 발주가 막혀도
// 이미 관측된 교체는 확정돼야 한다.
func (s *rescanScheduler) runOnce(ctx context.Context) (RescanSummary, error) {
	var sum RescanSummary
	var errs []error

	// ⓪ 설정 행 오버라이드. 'registered' 정기 스캔(0019)이 환경변수보다 우선한다.
	opts, enabled, err := s.settings(ctx)
	if err != nil {
		errs = append(errs, err)
	}

	// ① 은퇴. claim보다 앞이라 이번 tick부터 발주 대상에서 빠진다.
	// 멱등(retired_at IS NULL 가드)이므로 tick 락 밖이어도 안전하다.
	retired, err := s.m.store.RetireMissingEndpointMonitors(ctx, opts.RetireMisses)
	if err != nil {
		errs = append(errs, err)
	}
	sum.Retired = retired

	// ② 발주. claim이 tick 락을 겸한다 — 잡지 못하면 다른 인스턴스가 도는 중이다.
	// 설정 행이 꺼져 있으면 이 단계만 건너뛴다(③ 전이는 그대로 돈다).
	if !enabled {
		sum.Disabled = true
		res, err := s.reconcile(ctx, opts)
		if err != nil {
			errs = append(errs, err)
		}
		sum.Replaced, sum.LocationsOnly = res.Replaced, res.LocationsOnly
		return sum, errors.Join(errs...)
	}
	due, locked, err := s.m.store.ClaimDueEndpointMonitors(ctx, opts.intervals(), opts.BatchIPs)
	if err != nil {
		errs = append(errs, err)
	}
	if !locked {
		// 전이도 같은 인스턴스가 이어서 한다. 여기서 겹쳐 돌 이유가 없다.
		sum.Skipped = true
		return sum, errors.Join(errs...)
	}
	sum.Claimed = len(due)
	for _, b := range groupRescanBatches(due) {
		sensor, err := s.dispatch(ctx, b, opts)
		if err != nil {
			if ctx.Err() == nil {
				// 대상 원문은 감사에 이미 있다 — 로그에는 사유와 규모만 남긴다.
				slog.Error("discovery: rescan dispatch failed",
					"error", err.Error(), "port", b.port, "targets", len(b.ips))
			}
			errs = append(errs, err)
			continue
		}
		if sensor {
			sum.SensorJobs++
			continue
		}
		sum.ServerJobs++
	}

	// ③ 교체 전이. 관측 트랜잭션 밖의 별도 배치다 (설계 8.5·10.2).
	res, err := s.reconcile(ctx, opts)
	if err != nil {
		errs = append(errs, err)
	}
	sum.Replaced = res.Replaced
	sum.LocationsOnly = res.LocationsOnly
	return sum, errors.Join(errs...)
}

// reconcile은 교체 확정 배치를 돌린다 (③ 단계 — 발주를 건너뛴 tick도 이건 한다).
func (s *rescanScheduler) reconcile(ctx context.Context, opts RescanOptions) (store.ReplacementSummary, error) {
	res, err := s.m.store.ReconcileReplacedCertificates(ctx, store.ReplacementOptions{
		ConfirmThreshold: opts.ReplaceConfirm,
	})
	if err != nil {
		return store.ReplacementSummary{}, err
	}
	return res, nil
}

// settings는 이번 tick에 적용할 설정과 발주 활성 여부를 정한다.
//
// 'registered' 정기 스캔 행(0019)이 정본이고 환경변수(RESCAN_*)는 그 행이 비워 둔
// 값의 기본치다 — 화면에서 끄거나 주기를 바꾼 것이 재기동 없이 먹어야 한다.
//
// **만료 임박 가속 티어는 오버라이드하지 않는다**(설계 3.1의 상수 그대로다).
// 설정 행이 바꾸는 것은 전역 기본 주기·발주 레이트뿐이고, 임박 구간은 오탐 창을
// 막기 위한 안전장치라 운영자가 실수로 늘릴 수 있는 표면에 두지 않는다.
//
// 행이 없거나 조회에 실패하면 환경변수 설정 그대로 돈다 — 정기 스캔 설정을 읽지
// 못했다고 재스캔이 멈추면 인벤토리 신선도가 조용히 죽는다.
func (s *rescanScheduler) settings(ctx context.Context) (RescanOptions, bool, error) {
	opts := s.opts
	sc, err := s.m.store.GetScanScheduleByKind(ctx, store.ScheduleKindRegistered)
	if errors.Is(err, store.ErrScanScheduleNotFound) {
		return opts, true, nil
	}
	if err != nil {
		return opts, true, err
	}
	if sc.IntervalSeconds != nil && *sc.IntervalSeconds > 0 {
		opts.Interval = time.Duration(*sc.IntervalSeconds) * time.Second
	}
	if sc.RatePPS != nil && *sc.RatePPS > 0 {
		opts.RatePPS = *sc.RatePPS
	}
	return opts, sc.Enabled, nil
}

// rescanBatch는 발주 단위 1건이다 — 같은 실행 스코프·같은 포트의 IP 묶음.
type rescanBatch struct {
	scope string
	port  int
	ips   []string
}

// groupRescanBatches는 claim한 엔드포인트를 (executor_scope, 포트)로 묶는다 (설계 4.1·5.1).
//
// 포트를 1개로 고정하는 것이 이 함수의 존재 이유다. scan_jobs는 targets × ports
// 데카르트 곱이라(0006) 엔드포인트 쌍을 그대로 넣으면 요청하지 않은 조합까지
// 스캔된다 — 낭비가 아니라 범위 위반이고, 감사에 남는 스캔 범위가 실제와 달라진다.
//
// 입력은 store가 (scope, port, ip) 순으로 정렬해 돌려주므로 한 번 훑는 것으로
// 충분하고 결과 순서도 결정론적이다.
func groupRescanBatches(due []store.DueEndpointMonitor) []rescanBatch {
	var out []rescanBatch
	for _, d := range due {
		last := len(out) - 1
		if last < 0 || out[last].scope != d.Scope || out[last].port != d.Port {
			out = append(out, rescanBatch{scope: d.Scope, port: d.Port, ips: []string{d.IP}})
			continue
		}
		if out[last].ips[len(out[last].ips)-1] == d.IP {
			continue // 같은 (스코프, 포트, IP)는 모니터가 1행뿐이라 정상 경로엔 없다.
		}
		out[last].ips = append(out[last].ips, d.IP)
	}
	return out
}

// dispatch는 배치 1건을 발주하고, 서버 스코프면 인프로세스 실행까지 건다.
// 센서 스코프면 true를 반환한다(발주만 하고 끝 — 센서가 pull 리스로 가져간다).
// opts는 이번 tick의 유효 설정이다(설정 행 오버라이드가 반영된 값).
func (s *rescanScheduler) dispatch(ctx context.Context, b rescanBatch, opts RescanOptions) (sensor bool, err error) {
	endpoints, err := Expand(b.ips, []int{b.port})
	if err != nil {
		return false, err
	}

	var sensorID *string
	if id, found := strings.CutPrefix(b.scope, sensorScopePrefix); found {
		sensorID = &id
	}

	jobID, err := s.m.store.CreateRescanScanJob(ctx, store.RescanJobInput{
		IPs:         b.ips,
		Port:        b.port,
		SensorID:    sensorID,
		TimeoutMs:   int(rescanTimeout / time.Millisecond),
		Concurrency: opts.Concurrency,
		RatePPS:     opts.RatePPS,
	})
	if err != nil {
		return false, err
	}

	if sensorID != nil {
		// 센서 입장에서 재스캔 Job은 다른 스캔 Job과 구별되지 않는다 — 그게 목적이다.
		return true, nil
	}

	// 실행은 기존 run 경로를 그대로 탄다 — InterleaveByHost와 RateLimiter.Wait이
	// 자동으로 적용된다(설계 6장). 배치가 "같은 포트, 여러 IP"라 연속 프로브가
	// 같은 호스트로 가지 않는 것도 여기서 한 번 더 보장된다.
	s.wg.Add(1)
	go func() {
		defer s.wg.Done()
		s.m.run(ctx, jobID, endpoints, opts.Concurrency, rescanTimeout, NewRateLimiter(opts.RatePPS))
	}()
	return false, nil
}
