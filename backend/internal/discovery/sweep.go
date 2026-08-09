// sweep.go — 대역 스윕 스케줄러 (docs/design/04-port-autoscan.md 4·5·7장).
//
// 하는 일은 하나다: 활성 스윕을 돌며 store.DispatchNextChunk로 **다음 청크 1건**을
// 자식 scan_jobs 행으로 발주하고, 서버 스코프면 그 Job을 인프로세스로 실행한다.
// 청크 계산(웨이브 순서·seq→엔드포인트)은 순수 함수라 DB에 저장하지 않는다 —
// store는 discovery를 import할 수 없으므로(의존 방향 discovery → store) 여기서
// 프리셋·포트 목록을 주입한다.
//
// 상태 머신은 store가 갖고 있고 이 파일은 발주자일 뿐이다. 특히
// **scan_jobs 상태 enum은 한 글자도 건드리지 않는다** (GOTCHAS "Contract drift").
//
// 배선 예 (cmd/server):
//
//	stop := discovery.NewManager(st).
//		UseSweepOptions(discovery.SweepOptions{ /* config.Sweep 값 */ }).
//		StartSweepScheduler(ctx)
//	defer stop()
package discovery

import (
	"context"
	"log/slog"
	"sync"
	"time"

	"github.com/Falke060918/INHERIT_AutoCert/backend/internal/domain"
	"github.com/Falke060918/INHERIT_AutoCert/backend/internal/store"
)

const (
	// minSweepTick은 발주 시도 주기의 하한이다. ChunkGap=0("공백 없음")도 유효한
	// 설정값이라 그대로 티커에 넣으면 panic이거나 스핀 루프가 된다.
	minSweepTick = 200 * time.Millisecond

	// sweepStallInterval은 "센서 대기"(stalled_since) 표시 갱신 주기다.
	// 이 갱신은 상태를 종결시키지 않는다 — UI 표시용 플래그일 뿐이다 (설계 6.4).
	sweepStallInterval = 1 * time.Minute

	// sweepListPageSize·sweepListMaxPages는 한 tick에서 훑는 활성 스윕 상한이다.
	// 스윕은 며칠짜리 배경 작업이라 동시 활성 수가 이 값을 넘길 일은 실무상 없다.
	sweepListPageSize = 100
	sweepListMaxPages = 10

	// sweepChunkTimeout은 자식 청크 Job의 엔드포인트당 핸드셰이크 타임아웃이다.
	// 단발 스캔 기본값과 같다(스윕은 속도로 조용해지지, 타임아웃으로 조용해지지 않는다).
	sweepChunkTimeout = defaultTimeout

	// sweepIPProbePort는 IP 전개 전용 더미 포트다. 이 포트로 스캔하지 않는다 —
	// Expand를 재사용해 CIDR 가드·중복 제거 규칙을 한 곳에 두기 위한 인자일 뿐이다.
	sweepIPProbePort = 1
)

// SweepOptions는 스윕 스케줄러 설정이다 (SCAN_SWEEP_*).
// config.SweepConfig와 1:1이지만 discovery가 config를 import하지 않도록 별도 타입이다.
type SweepOptions struct {
	// Enabled가 false면 스케줄러를 아예 시작하지 않는다.
	Enabled bool
	// RatePPS는 스윕 행에 rate_pps가 없을 때만 쓰는 폴백 레이트다.
	// 정상 경로에서는 자식 Job의 rate_pps(= 스윕 행 값)를 쓴다.
	RatePPS int
	// Concurrency는 청크 실행 워커 동시성이다.
	Concurrency int
	// ChunkEndpoints는 청크 하나의 최대 엔드포인트 수다(스윕 행 값이 우선).
	ChunkEndpoints int
	// ChunkGap은 청크 발주 간 공백이다. 0도 유효한 값이다("공백 없음").
	ChunkGap time.Duration
	// StallAfter는 자식 Job이 이만큼 pending에 머무르면 "센서 대기"로 표시하는 기준이다.
	StallAfter time.Duration
}

// DefaultSweepOptions는 설계 7.3의 기본값이다.
func DefaultSweepOptions() SweepOptions {
	return SweepOptions{
		Enabled:        true,
		RatePPS:        SweepRatePPS,
		Concurrency:    SweepConcurrency,
		ChunkEndpoints: store.DefaultSweepChunkEndpoints,
		ChunkGap:       SweepChunkPause,
		StallAfter:     store.DefaultSweepStallAfter,
	}
}

// normalized는 0·음수를 기본값으로 되돌린다. ChunkGap만 0을 그대로 둔다(유효한 값).
func (o SweepOptions) normalized() SweepOptions {
	d := DefaultSweepOptions()
	if o.RatePPS < 1 {
		o.RatePPS = d.RatePPS
	}
	if o.Concurrency < 1 {
		o.Concurrency = d.Concurrency
	}
	if o.ChunkEndpoints < 1 {
		o.ChunkEndpoints = d.ChunkEndpoints
	}
	if o.ChunkGap < 0 {
		o.ChunkGap = d.ChunkGap
	}
	if o.StallAfter <= 0 {
		o.StallAfter = d.StallAfter
	}
	return o
}

// StartSweepScheduler는 스윕 발주 goroutine을 시작한다. 반환된 stop은 goroutine과
// 진행 중인 청크 실행을 멈추고 종료를 기다린다 (ctx 취소로도 멈춘다) —
// sensorgw.StartReaper와 같은 규약이다.
//
// Enabled=false면 아무것도 띄우지 않고 로그 1줄만 남긴다(stop은 no-op).
func (m *Manager) StartSweepScheduler(ctx context.Context) (stop func()) {
	return m.startSweepScheduler(ctx, m.sweep.ChunkGap, sweepStallInterval)
}

// startSweepScheduler는 주기를 인자로 받는 내부 구현이다 (테스트가 짧은 주기로 돌린다).
func (m *Manager) startSweepScheduler(ctx context.Context, gap, stallEvery time.Duration) (stop func()) {
	opts := m.sweep.normalized()
	if !opts.Enabled {
		slog.Info("discovery: sweep scheduler disabled (SCAN_SWEEP_ENABLED=false)")
		return func() {}
	}

	tick := gap
	if tick < minSweepTick {
		tick = minSweepTick
	}
	if stallEvery <= 0 {
		stallEvery = sweepStallInterval
	}

	s := &sweepScheduler{
		m:        m,
		opts:     opts,
		gap:      gap,
		plans:    map[string]*sweepPlan{},
		inflight: map[string]bool{},
		nextAt:   map[string]time.Time{},
	}

	ctx, cancel := context.WithCancel(ctx)
	done := make(chan struct{})
	go func() {
		defer close(done)
		ticker := time.NewTicker(tick)
		defer ticker.Stop()
		stallTicker := time.NewTicker(stallEvery)
		defer stallTicker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				s.dispatchDue(ctx)
			case <-stallTicker.C:
				s.refreshStalls(ctx)
			}
		}
	}()
	return func() {
		cancel()
		<-done
		s.wg.Wait() // 진행 중이던 청크 실행까지 정리한 뒤 돌아온다.
	}
}

// sweepPlan은 스윕 1건의 전개 결과 캐시다(IP 목록·웨이브 정렬된 포트 목록).
// 스윕 정의는 생성 시점에 고정되므로(설계 11.1) 매 tick 다시 전개할 이유가 없다.
type sweepPlan struct {
	ips       []string
	ports     []int
	chunkSize int
}

// chunk는 store.SweepChunkFunc다: seq 하나로 그 청크를 결정론적으로 재생한다.
func (p *sweepPlan) chunk(seq int) ([]string, []int, bool) {
	return store.ChunkForSeq(p.ips, p.ports, p.chunkSize, seq)
}

// sweepScheduler는 발주 루프의 상태다(전개 캐시·로컬 실행 표시·청크 간 공백).
type sweepScheduler struct {
	m    *Manager
	opts SweepOptions
	gap  time.Duration

	wg sync.WaitGroup

	mu       sync.Mutex
	plans    map[string]*sweepPlan // nil 값 = 전개 실패(다시 시도하지 않는다)
	inflight map[string]bool       // 이 프로세스가 실행 중인 청크가 있는 스윕
	nextAt   map[string]time.Time  // 청크 간 공백이 끝나는 시각
}

// dispatchDue는 활성 스윕을 한 바퀴 돌며 발주 가능한 것만 발주한다.
func (s *sweepScheduler) dispatchDue(ctx context.Context) {
	active := s.activeSweeps(ctx)
	for _, sw := range active {
		if ctx.Err() != nil {
			return
		}
		if !s.due(sw) {
			continue
		}
		s.dispatchOne(ctx, sw)
	}
	s.prune(active)
}

// due는 지금 이 스윕에 발주를 시도할 가치가 있는지 본다.
// 동시 청크 1개는 store가 보장하지만, 진행 중인 줄 아는 스윕에 매 tick 트랜잭션을
// 여는 것은 낭비다 — 여기서 미리 걸러낸다.
func (s *sweepScheduler) due(sw store.Sweep) bool {
	if sw.Progress.ChunksActive > 0 {
		return false
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.inflight[sw.ID] {
		return false // 방금 발주해 집계에 아직 안 잡힌 청크가 돌고 있다.
	}
	if at, ok := s.nextAt[sw.ID]; ok && time.Now().Before(at) {
		return false // 청크 간 공백(SCAN_SWEEP_CHUNK_GAP) 중이다.
	}
	return true
}

// dispatchOne은 스윕 1건의 다음 청크를 발주하고, 서버 스코프면 실행까지 건다.
func (s *sweepScheduler) dispatchOne(ctx context.Context, sw store.Sweep) {
	plan := s.planFor(sw)
	if plan == nil {
		return // 전개 불가 — 이미 1회 로그했다.
	}

	chunk, err := s.m.store.DispatchNextChunk(ctx, sw.ID, store.SweepDispatchInput{
		TimeoutMs:   int(sweepChunkTimeout / time.Millisecond),
		Concurrency: s.opts.Concurrency,
		Chunk:       plan.chunk,
	})
	if err != nil {
		if ctx.Err() == nil {
			slog.Error("discovery: sweep chunk dispatch failed", "error", err.Error(), "sweep_id", sw.ID)
		}
		return
	}
	if chunk == nil {
		return // 발주할 것이 없다(진행 중·일시정지·취소·종결).
	}

	s.markDispatched(sw.ID)
	slog.Info("discovery: sweep chunk dispatched",
		"sweep_id", sw.ID, "job_id", chunk.JobID, "seq", chunk.Seq,
		"attempt", chunk.Attempt, "endpoints", chunk.Endpoints)

	if sw.ExecutorScope != domain.ExecutorScopeServer {
		return // 센서 스코프 — pending으로 두면 센서가 pull 리스로 가져간다 (00-spec 7.8).
	}
	s.runChunk(ctx, sw, chunk, plan)
}

// runChunk는 서버 스코프 청크를 인프로세스로 실행한다(단발 스캔과 같은 실행 경로).
func (s *sweepScheduler) runChunk(ctx context.Context, sw store.Sweep, chunk *store.SweepChunk, plan *sweepPlan) {
	ips, ports, ok := plan.chunk(chunk.Seq)
	if !ok {
		// 발주된 seq를 재생하지 못한다 — 행은 pending으로 남고 다음 기동의
		// ReapOrphanedServerScanJobs가 failed로 회수해 재발주 경로를 탄다.
		slog.Error("discovery: sweep chunk not reproducible", "sweep_id", sw.ID, "seq", chunk.Seq)
		return
	}
	endpoints, err := Expand(ips, ports)
	if err != nil {
		slog.Error("discovery: sweep chunk expand failed", "error", err.Error(), "sweep_id", sw.ID, "seq", chunk.Seq)
		return
	}

	// 자식 Job은 자기 행의 rate_pps로 돈다 — 그 값은 스윕 행에서 복사된 것이다.
	rate := sw.RatePPS
	if rate < 1 {
		rate = s.opts.RatePPS
	}

	s.setInflight(sw.ID, true)
	s.wg.Add(1)
	go func() {
		defer s.wg.Done()
		defer s.setInflight(sw.ID, false)
		s.m.run(ctx, chunk.JobID, endpoints, s.opts.Concurrency, sweepChunkTimeout, NewRateLimiter(rate))
	}()
}

// refreshStalls는 "센서 대기" 표시를 갱신한다 (설계 6.4).
func (s *sweepScheduler) refreshStalls(ctx context.Context) {
	n, err := s.m.store.RefreshSweepStalls(ctx, s.opts.StallAfter)
	if err != nil {
		if ctx.Err() == nil {
			slog.Error("discovery: refresh sweep stalls failed", "error", err.Error())
		}
		return
	}
	if n > 0 {
		slog.Info("discovery: sweep stall flags refreshed", "count", n)
	}
}

// activeSweeps는 queued/running 스윕을 페이지 단위로 모은다.
func (s *sweepScheduler) activeSweeps(ctx context.Context) []store.Sweep {
	var out []store.Sweep
	for page := 1; page <= sweepListMaxPages; page++ {
		list, err := s.m.store.ListSweeps(ctx, store.ListSweepsInput{
			Statuses: []string{store.SweepStatusQueued, store.SweepStatusRunning},
			Page:     page,
			PageSize: sweepListPageSize,
		})
		if err != nil {
			if ctx.Err() == nil {
				slog.Error("discovery: list active sweeps failed", "error", err.Error())
			}
			return out
		}
		out = append(out, list.Items...)
		if len(list.Items) == 0 || len(out) >= list.Total {
			break
		}
	}
	return out
}

// planFor는 스윕의 전개 캐시를 돌려준다. 전개 실패는 nil을 캐시해 매 tick 같은
// 오류를 반복 기록하지 않는다(스윕 정의는 불변이라 다시 시도해도 결과가 같다).
func (s *sweepScheduler) planFor(sw store.Sweep) *sweepPlan {
	s.mu.Lock()
	if plan, ok := s.plans[sw.ID]; ok {
		s.mu.Unlock()
		return plan
	}
	s.mu.Unlock()

	plan, err := buildSweepPlan(sw)
	if err != nil {
		// 대상·포트 원문은 감사에 이미 있고 로그에는 스윕 id와 사유만 남긴다.
		slog.Error("discovery: sweep plan expand failed", "error", err.Error(), "sweep_id", sw.ID)
		plan = nil
	}
	s.mu.Lock()
	s.plans[sw.ID] = plan
	s.mu.Unlock()
	return plan
}

// markDispatched는 청크 간 공백의 시작점을 기록한다.
func (s *sweepScheduler) markDispatched(sweepID string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.nextAt[sweepID] = time.Now().Add(s.gap)
}

// setInflight는 이 프로세스가 실행 중인 청크 유무를 기록한다.
func (s *sweepScheduler) setInflight(sweepID string, running bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if running {
		s.inflight[sweepID] = true
		return
	}
	delete(s.inflight, sweepID)
}

// prune은 더 이상 활성이 아닌 스윕의 캐시를 버린다(장기 구동 시 맵 증가 방지).
func (s *sweepScheduler) prune(active []store.Sweep) {
	live := make(map[string]bool, len(active))
	for _, sw := range active {
		live[sw.ID] = true
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	for id := range s.plans {
		if !live[id] && !s.inflight[id] {
			delete(s.plans, id)
			delete(s.nextAt, id)
		}
	}
}

// buildSweepPlan은 스윕 행의 원문(targets·port_spec)을 청크 계산 입력으로 전개한다.
//
// 웨이브 순서는 ① common 프리셋 → ② 1-1023 → ③ 1024-49151 → ④ 49152-65535다
// (설계 4.3). 우선순위 목록을 store에 넘기는 것이 이 파일의 핵심 역할이다 —
// 프리셋 정의는 discovery에 있고 store는 discovery를 import할 수 없다.
func buildSweepPlan(sw store.Sweep) (*sweepPlan, error) {
	ports, err := ParsePortSpec(sw.PortSpec)
	if err != nil {
		return nil, err
	}
	ips, err := expandSweepIPs(sw.Targets)
	if err != nil {
		return nil, err
	}
	size := sw.ChunkEndpoints
	if size < 1 {
		size = store.DefaultSweepChunkEndpoints
	}
	return &sweepPlan{
		ips:       ips,
		ports:     store.WaveOrderPorts(ports, presetCommon),
		chunkSize: size,
	}, nil
}

// expandSweepIPs는 IP/CIDR 원문을 개별 IP 목록으로 전개한다.
// Expand를 그대로 재사용해 CIDR 가드(호스트 비트 ≤20)와 중복 제거 규칙을 한 곳에 둔다.
func expandSweepIPs(targets []string) ([]string, error) {
	eps, err := Expand(targets, []int{sweepIPProbePort})
	if err != nil {
		return nil, err
	}
	ips := make([]string, 0, len(eps))
	for _, ep := range eps {
		ips = append(ips, ep.IP)
	}
	return ips, nil
}
