package discovery

import (
	"context"
	"log/slog"
	"sync"
	"time"

	"github.com/Falke060918/INHERIT_AutoCert/backend/internal/store"
)

// 타임아웃·동시성 기본값·경계값. 사용자 입력을 이 범위로 클램프한다.
const (
	defaultTimeout     = 5 * time.Second
	minTimeout         = 100 * time.Millisecond
	maxTimeout         = 30 * time.Second
	defaultConcurrency = 20
	maxConcurrency     = 256
)

// scanEndpoint는 실제 스캔 함수다. 테스트에서 대체한다(실행 경로를 그대로 타면서
// dial만 걷어내기 위함 — scanner.go의 lookupAddr와 같은 방식이다).
var scanEndpoint = ScanEndpoint

// Manager는 스캔 Job의 생성·백그라운드 실행을 조율한다.
// 실제 인증서 작업(발견)은 AI가 아닌 명시적 상태 머신으로 실행한다 (CLAUDE.md 결정론적 자동화).
type Manager struct {
	store *store.Store
	// sweep은 대역 스윕 스케줄러 설정이다 (sweep.go). 기본값은 NewManager가 채운다.
	sweep SweepOptions
	// rescan은 주기 재스캔 스케줄러 설정이다 (rescan.go). 기본값은 NewManager가 채운다.
	rescan RescanOptions
	// rescanSched는 StartRescanScheduler가 띄운 루프의 상태다 (rescan.go).
	// 수동 1배치 발주(API)가 같은 인스턴스를 쓰게 해서, 그때 시작된 인프로세스
	// 실행도 스케줄러의 stop 대기에 함께 잡히게 한다. 대입은 기동 시 1회뿐이고
	// HTTP 서버는 그 뒤에 뜬다(cmd/server/main.go 순서) — 이후로는 읽기 전용이다.
	rescanSched *rescanScheduler
	// schedule은 정기 스캔 스케줄러 설정이다 (schedules.go). 기본값은 NewManager가 채운다.
	schedule ScheduleOptions

	// cancelMu/cancels는 수동 단발 스캔(Start의 서버 실행 경로)의 Job별 취소
	// 레지스트리다. 취소 API가 CancelScan으로 실행 중 goroutine의 ctx를 끊는다.
	// 스윕 청크·재스캔 발주 Job은 등록하지 않는다 — 스윕은 스윕 API로 제어하고,
	// 재스캔 running은 짧아(1배치 상한) 상태 전이(canceled)만으로 충분하다.
	cancelMu sync.Mutex
	cancels  map[string]context.CancelFunc
}

// NewManager는 Manager를 만든다.
func NewManager(st *store.Store) *Manager {
	return &Manager{store: st, sweep: DefaultSweepOptions(), rescan: DefaultRescanOptions(),
		schedule: DefaultScheduleOptions()}
}

// UseSweepOptions는 스윕 스케줄러 설정을 주입한 Manager를 돌려준다(자기 자신).
// cmd/server가 config.SweepConfig 값을 넘기는 자리다 — 값 오류는 config가 이미
// 경고 + 기본값 폴백으로 처리하므로 여기서는 0/음수만 기본값으로 되돌린다.
func (m *Manager) UseSweepOptions(opts SweepOptions) *Manager {
	m.sweep = opts.normalized()
	return m
}

// StartResult는 스캔 생성 결과다(즉시 반환값).
type StartResult struct {
	JobID          string
	TotalEndpoints int
}

// Start는 입력을 검증·펼쳐 Job을 만들고 백그라운드 스캔을 띄운 뒤 즉시 반환한다.
// 입력 오류는 discovery.ErrInvalidScanInput으로 감싸 반환한다(핸들러가 400 매핑).
// SensorID가 지정되면 executor_type='sensor'인 pending Job만 만들고 즉시 반환한다 —
// 실행은 센서가 pull 리스로 가져간다(서버 goroutine 없음, 00-spec 7.8 작업 분배).
func (m *Manager) Start(ctx context.Context, in store.ScanInput) (*StartResult, error) {
	endpoints, err := Expand(in.Targets, in.Ports)
	if err != nil {
		return nil, err
	}

	timeout := clampTimeout(in.TimeoutMs)
	concurrency := clampConcurrency(in.Concurrency)
	// 생성 응답의 total_endpoints와 저장된 Job 행이 어긋나지 않게 생성 시점에 기록한다.
	in.TotalEndpoints = len(endpoints)

	if in.SensorID != nil {
		// 센서는 리스 응답의 timeout_ms/concurrency를 그대로 쓰므로
		// 서버 실행 경로와 같은 클램프를 저장값에 반영해 둔다.
		in.TimeoutMs = int(timeout / time.Millisecond)
		in.Concurrency = concurrency
		jobID, err := m.store.CreateScanJob(ctx, in)
		if err != nil {
			return nil, err
		}
		return &StartResult{JobID: jobID, TotalEndpoints: len(endpoints)}, nil
	}

	jobID, err := m.store.CreateScanJob(ctx, in)
	if err != nil {
		return nil, err
	}

	// 백그라운드 실행은 요청 컨텍스트와 무관하게 살아야 하므로 Background를 쓴다.
	// 취소 API가 끊을 수 있게 Job별 cancel을 레지스트리에 등록한다.
	// 단발 스캔은 기존 기본값(50pps/동시성 20)으로 돈다 — 스윕은 이보다 더 느리다(설계 7.3).
	runCtx, cancel := context.WithCancel(context.Background())
	m.registerCancel(jobID, cancel)
	go func() {
		defer m.releaseCancel(jobID)
		m.run(runCtx, jobID, endpoints, concurrency, timeout, NewRateLimiter(DefaultScanRatePPS))
	}()

	return &StartResult{JobID: jobID, TotalEndpoints: len(endpoints)}, nil
}

// registerCancel은 Job의 취소 함수를 등록한다.
func (m *Manager) registerCancel(jobID string, cancel context.CancelFunc) {
	m.cancelMu.Lock()
	defer m.cancelMu.Unlock()
	if m.cancels == nil {
		m.cancels = map[string]context.CancelFunc{}
	}
	m.cancels[jobID] = cancel
}

// releaseCancel은 등록을 해제하고 cancel을 호출해 ctx 리소스를 회수한다(멱등).
func (m *Manager) releaseCancel(jobID string) {
	m.cancelMu.Lock()
	cancel := m.cancels[jobID]
	delete(m.cancels, jobID)
	m.cancelMu.Unlock()
	if cancel != nil {
		cancel()
	}
}

// CancelScan은 실행 중인 서버 스캔 goroutine의 ctx를 끊는다. 등록이 없으면
// false다(이미 종료됐거나 이 프로세스가 실행 주체가 아닌 Job) — 상태 전이는
// store.CancelScanJob이 이미 끝냈으므로 false여도 오류가 아니다.
// ctx가 끊긴 goroutine은 종료 상태를 찍지 않고 물러난다(run의 ctx.Err() 분기) —
// 행은 이미 canceled이고, MarkScanFinished의 미종결 가드가 경합 덮어쓰기를 막는다.
func (m *Manager) CancelScan(jobID string) bool {
	m.cancelMu.Lock()
	cancel, ok := m.cancels[jobID]
	m.cancelMu.Unlock()
	if ok {
		cancel()
	}
	return ok
}

// run은 Job의 상태 머신을 진행한다: running → 엔드포인트 스캔 → completed/failed.
// lim은 dial 속도 제한이다(nil이면 무제한). 스윕 자식 Job은 자기 행의 rate_pps로,
// 단발 스캔은 DefaultScanRatePPS로 돈다.
func (m *Manager) run(ctx context.Context, jobID string, endpoints []Endpoint, concurrency int, timeout time.Duration, lim *RateLimiter) {
	if err := m.store.MarkScanRunning(ctx, jobID, len(endpoints)); err != nil {
		slog.Error("discovery: mark running failed", "error", err.Error(), "job_id", jobID)
		reason := "failed to start scan"
		if ferr := m.store.MarkScanFinished(ctx, jobID, store.ScanStatusFailed, &reason); ferr != nil {
			slog.Error("discovery: mark failed", "error", ferr.Error(), "job_id", jobID)
		}
		return
	}

	scanAll(ctx, endpoints, concurrency, timeout, lim, func(obs Observation) {
		// 개별 DB 오류는 로그만 남기고 계속한다 — 한 엔드포인트가 전체 Job을 멈추지 않는다.
		// 로그에는 job_id와 오류 문자열만 남긴다(인증서 본문 금지, 보안 하드 규칙 3).
		if err := m.store.RecordEndpointResult(ctx, jobID, toStoreObs(obs)); err != nil {
			slog.Error("discovery: record endpoint result failed", "error", err.Error(), "job_id", jobID)
		}
	})

	if err := ctx.Err(); err != nil {
		// 중단(서버 종료 등)에는 종료 상태를 찍지 않는다 — 스캔이 끝나지 않았기 때문이다.
		// 남은 행은 다음 기동의 ReapOrphanedServerScanJobs가 failed로 회수하고,
		// 스윕 자식 Job이면 그 seq가 재발주된다(store의 부분 유니크가 failed를 비켜간다).
		slog.Warn("discovery: scan interrupted", "job_id", jobID, "reason", err.Error())
		return
	}

	if err := m.store.MarkScanFinished(ctx, jobID, store.ScanStatusCompleted, nil); err != nil {
		slog.Error("discovery: mark completed failed", "error", err.Error(), "job_id", jobID)
	}
}

// scanAll은 엔드포인트 목록을 워커풀로 훑고 결과를 sink에 넘긴다.
// 순서는 호스트 라운드로빈(InterleaveByHost)이고, 속도는 dial 직전 lim.Wait으로 맞춘다 —
// IPS가 보는 것은 SYN 비율이므로 핸드셰이크 뒤가 아니라 앞에서 눌러야 한다 (설계 7.1·7.2).
// sink는 여러 goroutine에서 동시에 호출된다.
func scanAll(ctx context.Context, endpoints []Endpoint, concurrency int, timeout time.Duration, lim *RateLimiter, sink func(Observation)) {
	sem := make(chan struct{}, concurrency)
	var wg sync.WaitGroup
	for _, ep := range InterleaveByHost(endpoints) {
		sem <- struct{}{}
		wg.Add(1)
		go func(ep Endpoint) {
			defer wg.Done()
			defer func() { <-sem }()

			if err := lim.Wait(ctx); err != nil {
				return // ctx 취소 — 남은 프로브를 보내지 않는다.
			}
			sink(scanEndpoint(ctx, ep, timeout))
		}(ep)
	}
	wg.Wait()
}

// toStoreObs는 스캔 결과를 store 계층 타입으로 변환한다(순환 import 방지).
func toStoreObs(o Observation) store.EndpointObservation {
	return store.EndpointObservation{
		IP:         o.IP,
		Port:       o.Port,
		Reachable:  o.Reachable,
		TLSVersion: o.TLSVersion,
		Hostname:   o.Hostname,
		Chain:      o.Chain,
	}
}

func clampTimeout(ms int) time.Duration {
	if ms <= 0 {
		return defaultTimeout
	}
	d := time.Duration(ms) * time.Millisecond
	if d < minTimeout {
		return minTimeout
	}
	if d > maxTimeout {
		return maxTimeout
	}
	return d
}

func clampConcurrency(n int) int {
	if n <= 0 {
		return defaultConcurrency
	}
	if n > maxConcurrency {
		return maxConcurrency
	}
	return n
}
