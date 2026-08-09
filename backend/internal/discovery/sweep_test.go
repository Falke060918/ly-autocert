package discovery

// 스윕 스케줄러 테스트.
// 실행 경로(scanAll)는 그대로 타되 dial만 테스트 대역으로 걷어낸다 —
// 폐쇄망 CI에서 실제 네트워크에 프로브를 내보내지 않기 위함이다.
// DB가 필요한 테스트는 TEST_DATABASE_URL 미설정 시 스킵한다
// (전용 테스트 DB — 메인 DB에 돌리지 않는다, docs/MAP.md).

import (
	"context"
	"database/sql"
	"os"
	"sync"
	"testing"
	"time"

	"github.com/Falke060918/INHERIT_AutoCert/backend/internal/db"
	"github.com/Falke060918/INHERIT_AutoCert/backend/internal/domain"
	"github.com/Falke060918/INHERIT_AutoCert/backend/internal/store"
)

// ---------------------------------------------------------------------------
// 헬퍼
// ---------------------------------------------------------------------------

// stubScan은 ScanEndpoint를 테스트 대역으로 바꾼다(t.Cleanup에서 복원).
// 병렬 실행 시 서로를 덮어쓰므로 이 파일의 테스트는 t.Parallel을 쓰지 않는다.
func stubScan(t *testing.T, fn func(Endpoint) Observation) {
	t.Helper()
	prev := scanEndpoint
	scanEndpoint = func(_ context.Context, ep Endpoint, _ time.Duration) Observation { return fn(ep) }
	t.Cleanup(func() { scanEndpoint = prev })
}

// testSweepStore는 TEST_DATABASE_URL로 연결한다. 미설정이면 스킵한다.
func testSweepStore(t *testing.T) (*store.Store, *sql.DB) {
	t.Helper()
	dsn := os.Getenv("TEST_DATABASE_URL")
	if dsn == "" {
		t.Skip("TEST_DATABASE_URL not set; skipping sweep scheduler test")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	pool, err := db.Connect(ctx, dsn)
	if err != nil {
		t.Fatalf("connect: %v", err)
	}
	t.Cleanup(func() { pool.Close() })
	if err := db.Migrate(pool); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	return store.New(pool), pool
}

// newSchedSweep은 스윕 1건과 정리 훅을 만든다.
// audit_events는 append-only 트리거(0007)가 DELETE를 막으므로 테스트 전용 DB에서만
// 허용되는 조작으로 트리거를 잠시 내리고 지운다.
func newSchedSweep(t *testing.T, st *store.Store, pool *sql.DB, in store.SweepInput) *store.Sweep {
	t.Helper()
	sw, err := st.CreateSweep(context.Background(), in)
	if err != nil {
		t.Fatalf("create sweep: %v", err)
	}
	t.Cleanup(func() {
		_, _ = pool.Exec(`ALTER TABLE audit_events DISABLE TRIGGER USER`)
		_, _ = pool.Exec(`DELETE FROM audit_events WHERE target_id IN (
			SELECT id::text FROM scan_jobs WHERE sweep_id = $1::uuid)`, sw.ID)
		_, _ = pool.Exec(`DELETE FROM audit_events WHERE target_id = $1`, sw.ID)
		_, _ = pool.Exec(`ALTER TABLE audit_events ENABLE TRIGGER USER`)
		// scan_job_endpoints는 job FK가 ON DELETE CASCADE다. 관측은 전부
		// 도달 불가(테스트 대역)라 targets·certificates 행은 생기지 않는다.
		_, _ = pool.Exec(`DELETE FROM scan_jobs WHERE sweep_id = $1::uuid`, sw.ID)
		_, _ = pool.Exec(`DELETE FROM scan_sweeps WHERE id = $1::uuid`, sw.ID)
	})
	return sw
}

// sweepChunkJob은 자식 청크 Job 1건의 검증용 요약이다.
type sweepChunkJob struct {
	Seq     int
	Ports   string
	Status  string
	Total   int
	Scanned int
}

// chunkJobs는 자식 청크 Job을 **발주 순서**(created_at)로 읽는다.
func chunkJobs(t *testing.T, pool *sql.DB, sweepID string) []sweepChunkJob {
	t.Helper()
	rows, err := pool.Query(`
		SELECT sweep_seq, array_to_string(ports, ','), status, total_endpoints, scanned_endpoints
		  FROM scan_jobs WHERE sweep_id = $1::uuid
		 ORDER BY created_at, sweep_seq`, sweepID)
	if err != nil {
		t.Fatalf("query chunk jobs: %v", err)
	}
	defer rows.Close() //nolint:errcheck
	var out []sweepChunkJob
	for rows.Next() {
		var j sweepChunkJob
		if err := rows.Scan(&j.Seq, &j.Ports, &j.Status, &j.Total, &j.Scanned); err != nil {
			t.Fatalf("scan chunk job: %v", err)
		}
		out = append(out, j)
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("iterate chunk jobs: %v", err)
	}
	return out
}

// waitFor는 조건이 참이 될 때까지 폴링한다. 시한을 넘기면 실패한다.
func waitFor(t *testing.T, timeout time.Duration, what string, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("timeout %v: %s", timeout, what)
}

// sweepStatus는 스윕의 현재 상태를 읽는다.
func sweepStatus(t *testing.T, st *store.Store, sweepID string) string {
	t.Helper()
	sw, err := st.GetSweep(context.Background(), sweepID)
	if err != nil {
		t.Fatalf("get sweep: %v", err)
	}
	return sw.Status
}

// schedOpts는 테스트용 스케줄러 설정이다(빠른 레이트 — 지연은 리미터 테스트에서 본다).
func schedOpts() SweepOptions {
	return SweepOptions{
		Enabled:        true,
		RatePPS:        MaxRatePPS,
		Concurrency:    4,
		ChunkEndpoints: 4,
		ChunkGap:       0,
		StallAfter:     time.Hour,
	}
}

// ---------------------------------------------------------------------------
// 청크 계산 주입 (DB 불필요)
// ---------------------------------------------------------------------------

// TestBuildSweepPlanUsesWaveOrder는 웨이브 우선순위 주입을 본다:
// ① common 프리셋 → ② 1-1023 → ③ 1024-49151 → ④ 49152-65535 (설계 4.3).
// 이 순서가 곧 seq 순서라 중간에 취소해도 값어치 높은 포트는 이미 훑은 상태가 된다.
func TestBuildSweepPlanUsesWaveOrder(t *testing.T) {
	plan, err := buildSweepPlan(store.Sweep{
		Targets:        []string{"198.51.100.0/30"},
		PortSpec:       "50000,22,8443,1024,443",
		ChunkEndpoints: 4,
	})
	if err != nil {
		t.Fatalf("build sweep plan: %v", err)
	}

	want := []int{443, 8443, 22, 1024, 50000} // common 2개 → well-known → registered → ephemeral
	if len(plan.ports) != len(want) {
		t.Fatalf("ports = %v, want %v", plan.ports, want)
	}
	for i, p := range want {
		if plan.ports[i] != p {
			t.Fatalf("ports = %v, want %v", plan.ports, want)
		}
	}
	if len(plan.ips) != 4 {
		t.Errorf("ips = %v, want 4개 (/30 전개)", plan.ips)
	}

	// seq 0은 가장 값어치 높은 포트(443)부터다.
	ips, ports, ok := plan.chunk(0)
	if !ok || len(ips) != 4 || len(ports) != 1 || ports[0] != 443 {
		t.Errorf("chunk(0) = (%v, %v, %v), want 4 IP × [443]", ips, ports, ok)
	}
}

// TestBuildSweepPlanRejectsBadSpec은 전개 불가 입력이 오류로 나오는지 본다
// (스케줄러는 이 경우 해당 스윕만 건너뛴다 — 다른 스윕은 계속 돈다).
func TestBuildSweepPlanRejectsBadSpec(t *testing.T) {
	if _, err := buildSweepPlan(store.Sweep{Targets: []string{"198.51.100.1"}, PortSpec: "nope"}); err == nil {
		t.Error("잘못된 포트 스펙이 오류 없이 통과했다")
	}
	if _, err := buildSweepPlan(store.Sweep{Targets: []string{"not-an-ip"}, PortSpec: "443"}); err == nil {
		t.Error("잘못된 대상이 오류 없이 통과했다")
	}
}

// ---------------------------------------------------------------------------
// 실행 경로: 리미터·호스트 라운드로빈 (DB 불필요)
// ---------------------------------------------------------------------------

// TestScanAllPacesDials는 리미터가 **실제 스캔 경로**에서 dial을 지연시키는지 본다.
// 100pps로 12건이면 버스트 1건을 뺀 11건이 10ms 간격 → 최소 ~110ms.
func TestScanAllPacesDials(t *testing.T) {
	stubScan(t, func(ep Endpoint) Observation { return Observation{Endpoint: ep} })

	eps, err := Expand([]string{"198.51.100.1", "198.51.100.2", "198.51.100.3", "198.51.100.4"}, []int{443, 8443, 9443})
	if err != nil {
		t.Fatalf("Expand: %v", err)
	}
	const pps = 100
	want := time.Duration(len(eps)-rateBurst) * time.Second / pps

	start := time.Now()
	scanAll(context.Background(), eps, 4, time.Second, NewRateLimiter(pps), func(Observation) {})
	elapsed := time.Since(start)

	// 하한만 엄격히 본다 — 스캔은 요청 속도보다 빨라질 수 없다.
	if elapsed < want-10*time.Millisecond {
		t.Errorf("%d개 스캔이 %v만에 끝났다, want 최소 ~%v (리미터가 dial을 누르지 않았다)", len(eps), elapsed, want)
	}
	if elapsed > 3*time.Second {
		t.Errorf("%d개 스캔이 %v 걸렸다, want ~%v", len(eps), elapsed, want)
	}

	// 무제한(nil) 리미터는 같은 경로를 지연 없이 통과한다.
	start = time.Now()
	scanAll(context.Background(), eps, 4, time.Second, nil, func(Observation) {})
	if elapsed := time.Since(start); elapsed > 500*time.Millisecond {
		t.Errorf("무제한 리미터가 %v 지연시켰다, want ~0", elapsed)
	}
}

// TestScanAllInterleavesHosts는 호스트 라운드로빈이 **실제 스캔 경로**에 적용됐는지 본다.
// IPS의 포트스캔 탐지는 호스트당 연속 프로브 수를 임계로 삼으므로 이것이 오탐 억제의 핵심이다.
func TestScanAllInterleavesHosts(t *testing.T) {
	var mu sync.Mutex
	var order []Endpoint
	stubScan(t, func(ep Endpoint) Observation {
		mu.Lock()
		order = append(order, ep)
		mu.Unlock()
		return Observation{Endpoint: ep}
	})

	// Expand는 IP별로 포트를 연달아 펼친다 — 재배열이 없으면 같은 IP가 연속으로 나온다.
	eps, err := Expand([]string{"198.51.100.1", "198.51.100.2", "198.51.100.3"}, []int{443, 8443, 9443})
	if err != nil {
		t.Fatalf("Expand: %v", err)
	}
	// 동시성 1이라 기록 순서가 곧 프로브 순서다.
	scanAll(context.Background(), eps, 1, time.Second, nil, func(Observation) {})

	if len(order) != len(eps) {
		t.Fatalf("스캔 %d건, want %d", len(order), len(eps))
	}
	for i := 1; i < len(order); i++ {
		if order[i].IP == order[i-1].IP {
			t.Errorf("연속 프로브가 같은 호스트를 때렸다 (index %d, %s) — InterleaveByHost 미적용", i, order[i].IP)
		}
	}
}

// TestScanAllStopsOnCanceledContext는 취소된 ctx에서 프로브가 나가지 않는지 본다.
// (서버 종료 시 남은 청크를 회선으로 밀어내지 않는다.)
func TestScanAllStopsOnCanceledContext(t *testing.T) {
	var probes int
	var mu sync.Mutex
	stubScan(t, func(ep Endpoint) Observation {
		mu.Lock()
		probes++
		mu.Unlock()
		return Observation{Endpoint: ep}
	})

	eps, err := Expand([]string{"198.51.100.1", "198.51.100.2"}, []int{443, 8443})
	if err != nil {
		t.Fatalf("Expand: %v", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	scanAll(ctx, eps, 2, time.Second, NewRateLimiter(MaxRatePPS), func(Observation) {})

	if probes != 0 {
		t.Errorf("취소된 ctx에서 프로브 %d건이 나갔다, want 0", probes)
	}
}

// ---------------------------------------------------------------------------
// 스케줄러 기동 (DB 불필요)
// ---------------------------------------------------------------------------

// TestSweepSchedulerDisabledDoesNotStart는 SCAN_SWEEP_ENABLED=false에서
// goroutine을 아예 띄우지 않는지 본다. store가 nil이라 발주·stall 갱신을
// 한 번이라도 시도하면 nil 역참조로 즉시 panic한다.
func TestSweepSchedulerDisabledDoesNotStart(t *testing.T) {
	opts := schedOpts()
	opts.Enabled = false
	m := NewManager(nil).UseSweepOptions(opts)

	stop := m.startSweepScheduler(context.Background(), time.Millisecond, time.Millisecond)
	time.Sleep(100 * time.Millisecond) // 켜져 있었다면 수십 번 돌고도 남을 시간
	stop()                             // no-op이어야 하고 두 번 불러도 안전하다
	stop()
}

// ---------------------------------------------------------------------------
// 발주 루프 (TEST_DATABASE_URL 필요)
// ---------------------------------------------------------------------------

// TestSweepSchedulerDispatchesChunksInOrder는 스케줄러가 청크를 웨이브 순서대로
// 하나씩 발주하고 서버 스코프 청크를 실행해 스윕을 completed까지 끌고 가는지 본다.
func TestSweepSchedulerDispatchesChunksInOrder(t *testing.T) {
	st, pool := testSweepStore(t)

	var mu sync.Mutex
	scanned := map[Endpoint]int{}
	stubScan(t, func(ep Endpoint) Observation {
		mu.Lock()
		scanned[ep]++
		mu.Unlock()
		return Observation{Endpoint: ep} // 도달 불가 — 인증서 픽스처 없이 경로만 본다.
	})

	targets := []string{"198.51.100.0/30"}
	sw := newSchedSweep(t, st, pool, store.SweepInput{
		Targets: targets, IPCount: 4,
		PortSpec: "50000,8443,443", PortCount: 3,
		ExecutorScope: domain.ExecutorScopeServer,
		RatePPS:       MaxRatePPS, ChunkEndpoints: 4,
	})
	if sw.TotalChunks != 3 {
		t.Fatalf("total_chunks = %d, want 3 (4 IP × 1 포트 × 3청크)", sw.TotalChunks)
	}

	m := NewManager(st).UseSweepOptions(schedOpts())
	stop := m.startSweepScheduler(context.Background(), 10*time.Millisecond, time.Hour)
	defer stop()

	waitFor(t, 30*time.Second, "스윕이 completed로 끝나지 않았다", func() bool {
		return sweepStatus(t, st, sw.ID) == store.SweepStatusCompleted
	})
	stop()

	jobs := chunkJobs(t, pool, sw.ID)
	if len(jobs) != 3 {
		t.Fatalf("자식 청크 = %d건, want 3", len(jobs))
	}
	// 발주 순서 = 웨이브 순서(common 443·8443 → ephemeral 50000).
	wantPorts := []string{"443", "8443", "50000"}
	for i, j := range jobs {
		if j.Seq != i {
			t.Errorf("%d번째 발주 seq = %d, want %d (순차 발주 아님)", i, j.Seq, i)
		}
		if j.Ports != wantPorts[i] {
			t.Errorf("seq %d 포트 = %q, want %q (웨이브 순서 아님)", j.Seq, j.Ports, wantPorts[i])
		}
		if j.Status != store.ScanStatusCompleted {
			t.Errorf("seq %d status = %q, want completed", j.Seq, j.Status)
		}
		if j.Total != 4 || j.Scanned != 4 {
			t.Errorf("seq %d 진행률 = %d/%d, want 4/4", j.Seq, j.Scanned, j.Total)
		}
	}

	// 청크는 결정론적으로 재생되므로 엔드포인트가 정확히 한 번씩 나간다.
	mu.Lock()
	defer mu.Unlock()
	if len(scanned) != 12 {
		t.Errorf("스캔한 서로 다른 엔드포인트 = %d, want 12", len(scanned))
	}
	for ep, n := range scanned {
		if n != 1 {
			t.Errorf("엔드포인트 %v를 %d번 스캔했다, want 1", ep, n)
		}
	}

	final, err := st.GetSweep(context.Background(), sw.ID)
	if err != nil {
		t.Fatalf("get sweep: %v", err)
	}
	if final.Progress.ChunksCompleted != 3 || final.Progress.ScannedEndpoints != 12 {
		t.Errorf("진행률 = 청크 %d/%d·엔드포인트 %d, want 3/3·12",
			final.Progress.ChunksCompleted, final.TotalChunks, final.Progress.ScannedEndpoints)
	}
}

// TestSweepSchedulerRunsOneChunkAtATime은 동시 발주가 스윕당 1개로 제한되는지 본다.
// 청크가 도는 동안에는 다음 청크 행이 생기지 않아야 한다 (설계 4.1).
func TestSweepSchedulerRunsOneChunkAtATime(t *testing.T) {
	st, pool := testSweepStore(t)

	release := make(chan struct{})
	var releaseOnce sync.Once
	releaseAll := func() { releaseOnce.Do(func() { close(release) }) }
	started := make(chan struct{}, 64)
	stubScan(t, func(ep Endpoint) Observation {
		select {
		case started <- struct{}{}:
		default:
		}
		<-release
		return Observation{Endpoint: ep}
	})

	sw := newSchedSweep(t, st, pool, store.SweepInput{
		Targets: []string{"198.51.100.0/30"}, IPCount: 4,
		PortSpec: "443,8443,9443", PortCount: 3,
		ExecutorScope: domain.ExecutorScopeServer,
		RatePPS:       MaxRatePPS, ChunkEndpoints: 4,
	})

	m := NewManager(st).UseSweepOptions(schedOpts())
	stop := m.startSweepScheduler(context.Background(), 10*time.Millisecond, time.Hour)
	defer func() {
		releaseAll() // 실패로 빠져나가도 붙잡힌 워커를 풀어 stop이 걸리지 않게 한다.
		stop()
	}()

	select {
	case <-started:
	case <-time.After(20 * time.Second):
		t.Fatal("첫 청크가 실행되지 않았다")
	}
	// 첫 청크가 붙잡혀 있는 동안 여러 tick을 흘려보낸다.
	time.Sleep(300 * time.Millisecond)

	if jobs := chunkJobs(t, pool, sw.ID); len(jobs) != 1 {
		t.Fatalf("진행 중 자식 청크 = %d건, want 1 (동시 발주 최대 1)", len(jobs))
	}
	var active int
	if err := pool.QueryRow(`SELECT count(*) FROM scan_jobs
		 WHERE sweep_id = $1::uuid AND status IN ('pending','leased','running')`, sw.ID).Scan(&active); err != nil {
		t.Fatalf("count active: %v", err)
	}
	if active != 1 {
		t.Fatalf("활성 청크 = %d건, want 1", active)
	}

	releaseAll()
	waitFor(t, 30*time.Second, "남은 청크가 발주되지 않았다", func() bool {
		return sweepStatus(t, st, sw.ID) == store.SweepStatusCompleted
	})
	if jobs := chunkJobs(t, pool, sw.ID); len(jobs) != 3 {
		t.Errorf("총 자식 청크 = %d건, want 3", len(jobs))
	}
}

// TestSweepSchedulerPauseStopsDispatch는 pause의 의미를 본다:
// **신규 발주만** 멈추고 진행 중인 청크는 완주한다 (설계 6.2).
func TestSweepSchedulerPauseStopsDispatch(t *testing.T) {
	st, pool := testSweepStore(t)
	ctx := context.Background()

	release := make(chan struct{})
	var releaseOnce sync.Once
	releaseAll := func() { releaseOnce.Do(func() { close(release) }) }
	started := make(chan struct{}, 64)
	stubScan(t, func(ep Endpoint) Observation {
		select {
		case started <- struct{}{}:
		default:
		}
		<-release
		return Observation{Endpoint: ep}
	})

	sw := newSchedSweep(t, st, pool, store.SweepInput{
		Targets: []string{"198.51.100.0/30"}, IPCount: 4,
		PortSpec: "443,8443,9443", PortCount: 3,
		ExecutorScope: domain.ExecutorScopeServer,
		RatePPS:       MaxRatePPS, ChunkEndpoints: 4,
	})

	m := NewManager(st).UseSweepOptions(schedOpts())
	stop := m.startSweepScheduler(ctx, 10*time.Millisecond, time.Hour)
	defer func() {
		releaseAll()
		stop()
	}()

	select {
	case <-started:
	case <-time.After(20 * time.Second):
		t.Fatal("첫 청크가 실행되지 않았다")
	}

	// 청크 0이 도는 도중에 일시정지한다.
	if _, err := st.PauseSweep(ctx, sw.ID, store.SweepActor{}); err != nil {
		t.Fatalf("pause sweep: %v", err)
	}
	releaseAll()

	// 진행 중이던 청크는 완주한다.
	waitFor(t, 30*time.Second, "진행 중이던 청크가 완주하지 않았다", func() bool {
		jobs := chunkJobs(t, pool, sw.ID)
		return len(jobs) >= 1 && jobs[0].Status == store.ScanStatusCompleted
	})

	// 그 뒤로는 여러 tick이 지나도 신규 발주가 없다.
	time.Sleep(300 * time.Millisecond)
	if jobs := chunkJobs(t, pool, sw.ID); len(jobs) != 1 {
		t.Fatalf("paused 상태의 자식 청크 = %d건, want 1 (신규 발주 중단)", len(jobs))
	}
	if got := sweepStatus(t, st, sw.ID); got != store.SweepStatusPaused {
		t.Fatalf("status = %q, want paused", got)
	}

	// resume하면 next_seq부터 다시 발주한다.
	if _, err := st.ResumeSweep(ctx, sw.ID, store.SweepActor{}); err != nil {
		t.Fatalf("resume sweep: %v", err)
	}
	waitFor(t, 30*time.Second, "resume 후 스윕이 끝나지 않았다", func() bool {
		return sweepStatus(t, st, sw.ID) == store.SweepStatusCompleted
	})
	if jobs := chunkJobs(t, pool, sw.ID); len(jobs) != 3 {
		t.Errorf("총 자식 청크 = %d건, want 3", len(jobs))
	}
}

// TestSweepSchedulerRefreshesStalls는 스케줄러가 "센서 대기" 표시를 주기적으로
// 갱신하는지 본다. 신규 종료 상태를 만들지 않고 running/queued를 유지한다 (설계 6.4).
func TestSweepSchedulerRefreshesStalls(t *testing.T) {
	st, pool := testSweepStore(t)
	ctx := context.Background()
	stubScan(t, func(ep Endpoint) Observation { return Observation{Endpoint: ep} })

	// 센서 스코프 스윕. 자식 청크를 미리 pending으로 넣어 두면 활성 청크가 있으므로
	// 스케줄러는 신규 발주를 하지 않고 stall 갱신만 한다.
	sw := newSchedSweep(t, st, pool, store.SweepInput{
		Targets: []string{"198.51.100.0/30"}, IPCount: 4,
		PortSpec: "443", PortCount: 1,
		ExecutorScope: "sensor:11111111-1111-1111-1111-111111111111",
		RatePPS:       MaxRatePPS, ChunkEndpoints: 4,
	})
	if _, err := pool.Exec(`
		INSERT INTO scan_jobs (targets, ports, timeout_ms, concurrency, status, executor_type,
			total_endpoints, sweep_id, sweep_seq, rate_pps, port_spec, created_at)
		VALUES (ARRAY['198.51.100.0'], ARRAY[443], 5000, 1, 'pending', 'server',
			1, $1::uuid, 0, 10, '443', now() - interval '30 minutes')`, sw.ID); err != nil {
		t.Fatalf("insert pending chunk: %v", err)
	}

	opts := schedOpts()
	opts.StallAfter = time.Minute
	m := NewManager(st).UseSweepOptions(opts)
	stop := m.startSweepScheduler(ctx, time.Hour, 20*time.Millisecond) // 발주 tick은 끄고 stall만 돌린다
	defer stop()

	waitFor(t, 20*time.Second, "stalled_since가 세팅되지 않았다", func() bool {
		cur, err := st.GetSweep(ctx, sw.ID)
		if err != nil {
			t.Fatalf("get sweep: %v", err)
		}
		return cur.StalledSince != nil
	})
	if got := sweepStatus(t, st, sw.ID); got != store.SweepStatusQueued && got != store.SweepStatusRunning {
		t.Errorf("status = %q, want queued/running (대기는 종료 상태가 아니다)", got)
	}
}
