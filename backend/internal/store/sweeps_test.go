package store

// 전 포트 백그라운드 스윕(docs/design/04-port-autoscan.md)의 영속화 검증.
// 청크 계산은 DB 없이 도는 순수 함수 테스트, 나머지는 TEST_DATABASE_URL 통합 테스트다.
// 픽스처는 전부 테스트 실행 시점에 생성한다 (보안 하드 규칙 4).

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"
)

// testPriorityPorts는 common 프리셋을 흉내 낸 우선 포트다. discovery 패키지의
// 실제 프리셋을 쓰지 않는 이유는 의존 방향(discovery → store)이 단방향이라
// store 테스트가 그것을 import하면 순환이 되기 때문이다.
var testPriorityPorts = []int{443, 8443, 9443}

// ---------------------------------------------------------------------------
// 청크 계산 (DB 불필요)
// ---------------------------------------------------------------------------

// TestWaveOrderPortsPutsPresetFirst는 웨이브 정렬 순서를 본다:
// 프리셋 → 1-1023 → 1024-49151 → 49152-65535, 웨이브 안에서는 오름차순.
func TestWaveOrderPortsPutsPresetFirst(t *testing.T) {
	in := []int{60000, 22, 8443, 1, 50000, 443, 1024, 49151, 9443, 443}
	got := WaveOrderPorts(in, testPriorityPorts)
	want := []int{443, 8443, 9443, 1, 22, 1024, 49151, 50000, 60000}
	if len(got) != len(want) {
		t.Fatalf("wave order = %v, want %v (중복 제거 포함)", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("wave order = %v, want %v", got, want)
		}
	}
}

// TestChunkForSeqIsDeterministic은 재시도 멱등성의 근거를 본다:
// 같은 seq를 두 번 계산하면 항상 같은 엔드포인트 집합이어야 한다 (설계 5.2).
func TestChunkForSeqIsDeterministic(t *testing.T) {
	ips := testIPs(300)
	ports := WaveOrderPorts(testPortRange(1, 200), testPriorityPorts)
	const chunkSize = 4096

	total := SweepChunkCount(len(ips), len(ports), chunkSize)
	if total < 2 {
		t.Fatalf("total chunks = %d, want at least 2", total)
	}
	for seq := 0; seq < total; seq++ {
		a, ap, ok := ChunkForSeq(ips, ports, chunkSize, seq)
		if !ok {
			t.Fatalf("seq %d: ok = false, want true (total=%d)", seq, total)
		}
		b, bp, _ := ChunkForSeq(ips, ports, chunkSize, seq)
		if strings.Join(a, ",") != strings.Join(b, ",") || fmt.Sprint(ap) != fmt.Sprint(bp) {
			t.Fatalf("seq %d: 두 번째 계산이 달라졌다 (재시도 멱등 위반)", seq)
		}
	}
	if _, _, ok := ChunkForSeq(ips, ports, chunkSize, total); ok {
		t.Errorf("seq %d(범위 밖): ok = true, want false", total)
	}
	if _, _, ok := ChunkForSeq(ips, ports, chunkSize, -1); ok {
		t.Errorf("seq -1: ok = true, want false")
	}
}

// TestChunkForSeqCoversEveryEndpointOnce는 청크 곱이 상한 이하이고,
// 전 청크의 합집합이 (IP × 포트) 전체를 정확히 한 번씩 덮는지 본다.
func TestChunkForSeqCoversEveryEndpointOnce(t *testing.T) {
	cases := []struct{ ipCount, portCount, chunkSize int }{
		{1, 1, 4096},     // 최소
		{3, 65535, 4096}, // IP가 적고 포트가 전수 — 포트 분할이 지배
		{5000, 26, 4096}, // IP가 청크 상한보다 많다 — IP 분할이 지배
		{300, 200, 4096}, // 양쪽 분할
		{254, 26, 4096},  // /24 × common — 청크 1개에 들어간다
		{100, 100, 7},    // 상한이 작아 포트당 여러 IP 그룹
		{9, 5, 0},        // chunkSize 0 → 기본값 폴백
	}
	for _, c := range cases {
		ips := testIPs(c.ipCount)
		ports := testPortRange(1, c.portCount)
		total := SweepChunkCount(c.ipCount, c.portCount, c.chunkSize)
		limit := c.chunkSize
		if limit < 1 {
			limit = DefaultSweepChunkEndpoints
		}

		seen := make(map[string]bool, c.ipCount*c.portCount)
		for seq := 0; seq < total; seq++ {
			cips, cports, ok := ChunkForSeq(ips, ports, c.chunkSize, seq)
			if !ok {
				t.Fatalf("%+v seq %d: ok = false", c, seq)
			}
			if n := len(cips) * len(cports); n > limit {
				t.Fatalf("%+v seq %d: 엔드포인트 %d개 > 상한 %d", c, seq, n, limit)
			}
			for _, ip := range cips {
				for _, p := range cports {
					key := ip + ":" + fmt.Sprint(p)
					if seen[key] {
						t.Fatalf("%+v: %s가 두 청크에 중복 배정됐다", c, key)
					}
					seen[key] = true
				}
			}
		}
		if len(seen) != c.ipCount*c.portCount {
			t.Errorf("%+v: 덮인 엔드포인트 = %d, want %d", c, len(seen), c.ipCount*c.portCount)
		}
	}
}

// TestChunkForSeqStartsWithPresetWave는 seq 0이 프리셋(common) 포트부터
// 나오는지 본다 — 나올 확률이 높은 것부터 훑는다는 웨이브 배치의 핵심이다.
func TestChunkForSeqStartsWithPresetWave(t *testing.T) {
	ips := testIPs(1000)
	ports := WaveOrderPorts(testPortRange(1, 65535), testPriorityPorts)
	// 청크당 포트 수가 프리셋 개수와 같아지도록 상한을 잡는다(1000 × 3).
	chunkSize := len(ips) * len(testPriorityPorts)

	_, first, ok := ChunkForSeq(ips, ports, chunkSize, 0)
	if !ok {
		t.Fatal("seq 0: ok = false")
	}
	prio := map[int]bool{}
	for _, p := range testPriorityPorts {
		prio[p] = true
	}
	if len(first) != len(testPriorityPorts) {
		t.Fatalf("seq 0 포트 수 = %d, want %d", len(first), len(testPriorityPorts))
	}
	for _, p := range first {
		if !prio[p] {
			t.Errorf("seq 0에 프리셋 밖 포트 %d가 들어갔다 (웨이브 순서 위반): %v", p, first)
		}
	}

	// 마지막 청크는 실효성이 가장 낮은 ephemeral 대역이다.
	total := SweepChunkCount(len(ips), len(ports), chunkSize)
	_, last, ok := ChunkForSeq(ips, ports, chunkSize, total-1)
	if !ok {
		t.Fatalf("마지막 seq %d: ok = false", total-1)
	}
	for _, p := range last {
		if p <= waveRegisteredMaxPort {
			t.Errorf("마지막 청크에 포트 %d가 남았다 (ephemeral이 마지막이어야 한다): %v", p, last)
		}
	}
}

// TestSweepChunkCountEmpty는 대상·포트가 비면 청크가 0개인지 본다.
func TestSweepChunkCountEmpty(t *testing.T) {
	if n := SweepChunkCount(0, 100, 4096); n != 0 {
		t.Errorf("IP 0개 청크 수 = %d, want 0", n)
	}
	if n := SweepChunkCount(100, 0, 4096); n != 0 {
		t.Errorf("포트 0개 청크 수 = %d, want 0", n)
	}
	if _, _, ok := ChunkForSeq(nil, nil, 4096, 0); ok {
		t.Error("빈 스윕의 seq 0: ok = true, want false")
	}
}

// testIPs는 사설 대역(RFC1918)의 픽스처 IP를 만든다 — 실제 스캔은 하지 않는다.
func testIPs(n int) []string {
	out := make([]string, n)
	for i := 0; i < n; i++ {
		out[i] = fmt.Sprintf("10.99.%d.%d", i/256, i%256)
	}
	return out
}

// testPortRange는 [from, from+count) 포트 목록이다.
func testPortRange(from, count int) []int {
	out := make([]int, count)
	for i := range out {
		out[i] = from + i
	}
	return out
}

// ---------------------------------------------------------------------------
// 영속화 (TEST_DATABASE_URL 필요)
// ---------------------------------------------------------------------------

// newTestSweep은 스윕 1건과 정리 훅을 만든다.
func newTestSweep(t *testing.T, st *Store, pool *sql.DB, in SweepInput) *Sweep {
	t.Helper()
	sw, err := st.CreateSweep(context.Background(), in)
	if err != nil {
		t.Fatalf("create sweep: %v", err)
	}
	t.Cleanup(func() {
		// 자식 청크 Job의 감사(발주·종료)는 Job id가 target이라 먼저 지운다.
		_, _ = pool.Exec(`ALTER TABLE audit_events DISABLE TRIGGER USER`)
		_, _ = pool.Exec(`DELETE FROM audit_events WHERE target_id IN (
			SELECT id::text FROM scan_jobs WHERE sweep_id = $1::uuid)`, sw.ID)
		_, _ = pool.Exec(`ALTER TABLE audit_events ENABLE TRIGGER USER`)
		_, _ = pool.Exec(`DELETE FROM scan_jobs WHERE sweep_id = $1::uuid`, sw.ID)
		_, _ = pool.Exec(`DELETE FROM scan_sweeps WHERE id = $1::uuid`, sw.ID)
		cleanupAuditByTargetID(pool, sw.ID)
	})
	return sw
}

// sweepDispatch는 테스트용 발주 파라미터다. 청크 함수는 순수 함수 경로 그대로다.
func sweepDispatch(ips []string, ports []int, chunkSize int) SweepDispatchInput {
	return SweepDispatchInput{
		TimeoutMs:   1000,
		Concurrency: 1,
		Chunk: func(seq int) ([]string, []int, bool) {
			return ChunkForSeq(ips, ports, chunkSize, seq)
		},
	}
}

// TestCreateSweepRejectsOverLongETA는 ETA 상한 거부를 본다.
// 거부 오류에 **실제 소요 일수**가 들어 있어야 사용자가 얼마나 줄일지 알 수 있다 (설계 5.4).
func TestCreateSweepRejectsOverLongETA(t *testing.T) {
	pool := testPool(t)
	st := New(pool)
	ctx := context.Background()

	// 4094 IP × 65535 포트 = 2.68억 엔드포인트 ÷ 10pps ≈ 310일 (설계 11.1 리스크 표).
	_, err := st.CreateSweep(ctx, SweepInput{
		Targets: []string{"198.51.100.0/20"}, IPCount: 4094,
		PortSpec: "1-65535", PortCount: 65535,
		ExecutorScope: "server", RatePPS: 10,
	})
	if err == nil {
		t.Fatal("create sweep: 오류 없이 통과했다 (ETA 상한 미적용)")
	}
	if !errors.Is(err, ErrSweepTooLong) {
		t.Fatalf("create sweep error = %v, want ErrSweepTooLong", err)
	}
	var tooLong *SweepTooLongError
	if !errors.As(err, &tooLong) {
		t.Fatalf("create sweep error = %v, want *SweepTooLongError (일수 전달 불가)", err)
	}
	if tooLong.EstimatedDays < 300 || tooLong.EstimatedDays > 320 {
		t.Errorf("estimated days = %f, want 약 310 (2.68억 ÷ 10pps)", tooLong.EstimatedDays)
	}
	if tooLong.MaxDays != DefaultSweepMaxDays {
		t.Errorf("max days = %d, want %d", tooLong.MaxDays, DefaultSweepMaxDays)
	}
	if !strings.Contains(tooLong.Error(), formatSweepDays(tooLong.EstimatedDays)) {
		t.Errorf("메시지에 실제 소요 일수가 없다: %q", tooLong.Error())
	}

	// 행이 만들어지지 않았어야 한다.
	var n int
	if err := pool.QueryRow(
		`SELECT count(*) FROM scan_sweeps WHERE targets = ARRAY['198.51.100.0/20'::text]`).Scan(&n); err != nil {
		t.Fatalf("count sweeps: %v", err)
	}
	if n != 0 {
		t.Errorf("거부된 스윕 행 = %d, want 0", n)
	}
}

// TestCreateSweepDerivesTotals는 생성 시 파생값(총 엔드포인트·청크·ETA)을 본다.
func TestCreateSweepDerivesTotals(t *testing.T) {
	pool := testPool(t)
	st := New(pool)

	reqID := "test-req-sweep-create"
	t.Cleanup(func() { cleanupAuditByRequestID(pool, reqID) })

	sw := newTestSweep(t, st, pool, SweepInput{
		Targets: []string{"198.51.100.0/24"}, IPCount: 254,
		PortSpec: "common", PortCount: 26,
		ExecutorScope: "server", RatePPS: 10, RequestID: &reqID,
	})

	if sw.Status != SweepStatusQueued {
		t.Errorf("status = %q, want queued", sw.Status)
	}
	if sw.TotalEndpoints != 254*26 {
		t.Errorf("total_endpoints = %d, want %d", sw.TotalEndpoints, 254*26)
	}
	if want := SweepChunkCount(254, 26, DefaultSweepChunkEndpoints); sw.TotalChunks != want {
		t.Errorf("total_chunks = %d, want %d", sw.TotalChunks, want)
	}
	if sw.ChunkEndpoints != DefaultSweepChunkEndpoints {
		t.Errorf("chunk_endpoints = %d, want %d (기본값 폴백)", sw.ChunkEndpoints, DefaultSweepChunkEndpoints)
	}
	if sw.EstimatedFinishAt == nil || !sw.EstimatedFinishAt.After(sw.CreatedAt) {
		t.Errorf("estimated_finish_at = %v, want created_at 이후", sw.EstimatedFinishAt)
	}

	// 생성 감사가 남는다 (하드 규칙 5).
	var audits int
	if err := pool.QueryRow(
		`SELECT count(*) FROM audit_events WHERE target_id = $1 AND event_type = $2`,
		sw.ID, EventDiscoverySweep).Scan(&audits); err != nil {
		t.Fatalf("count audit events: %v", err)
	}
	if audits != 1 {
		t.Errorf("생성 감사 = %d건, want 1", audits)
	}
}

// TestDispatchNextChunkSerializes는 발주 규율 전반을 본다:
// 동시 발주에도 청크가 하나만 나가고, 진행 중 청크가 있으면 추가 발주가 없고,
// 전 청크 소진 시 completed로 전이한다.
func TestDispatchNextChunkSerializes(t *testing.T) {
	pool := testPool(t)
	st := New(pool)
	ctx := context.Background()

	ips := testIPs(4)
	ports := WaveOrderPorts([]int{443, 8443, 9443, 22}, testPriorityPorts)
	const chunkSize = 4 // IP 4개 × 포트 1개 = 청크 4개
	total := SweepChunkCount(len(ips), len(ports), chunkSize)
	if total != 4 {
		t.Fatalf("총 청크 = %d, want 4 (테스트 전제)", total)
	}

	sw := newTestSweep(t, st, pool, SweepInput{
		Targets: ips, IPCount: len(ips),
		PortSpec: "443,8443,9443,22", PortCount: len(ports),
		ExecutorScope: "server", RatePPS: 100, ChunkEndpoints: chunkSize,
	})
	in := sweepDispatch(ips, ports, chunkSize)

	// 동시 발주 8개 — 정확히 1개만 나가야 한다.
	var (
		wg       sync.WaitGroup
		mu       sync.Mutex
		chunks   []*SweepChunk
		dispErrs []error
	)
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			c, err := st.DispatchNextChunk(ctx, sw.ID, in)
			mu.Lock()
			defer mu.Unlock()
			if err != nil {
				dispErrs = append(dispErrs, err)
				return
			}
			if c != nil {
				chunks = append(chunks, c)
			}
		}()
	}
	wg.Wait()
	for _, err := range dispErrs {
		t.Errorf("dispatch: %v", err)
	}
	if len(chunks) != 1 {
		t.Fatalf("동시 발주 결과 = %d건, want 1 (동시 청크는 최대 1개)", len(chunks))
	}
	if chunks[0].Seq != 0 || chunks[0].Attempt != 1 {
		t.Errorf("첫 청크 = seq %d attempt %d, want seq 0 attempt 1", chunks[0].Seq, chunks[0].Attempt)
	}

	// 발주된 자식 Job은 스윕 컬럼을 채운다.
	var (
		gotSweep    string
		gotSeq      int
		gotRate     int
		gotPortSpec string
	)
	if err := pool.QueryRow(
		`SELECT sweep_id::text, sweep_seq, rate_pps, port_spec FROM scan_jobs WHERE id = $1::uuid`,
		chunks[0].JobID).Scan(&gotSweep, &gotSeq, &gotRate, &gotPortSpec); err != nil {
		t.Fatalf("read chunk job: %v", err)
	}
	if gotSweep != sw.ID || gotSeq != 0 || gotRate != 100 || gotPortSpec != "443,8443,9443,22" {
		t.Errorf("자식 Job 컬럼 = (%s, %d, %d, %q), want (%s, 0, 100, %q)",
			gotSweep, gotSeq, gotRate, gotPortSpec, sw.ID, "443,8443,9443,22")
	}

	// 스윕은 running으로 전진하고 next_seq는 1이다.
	cur, err := st.GetSweep(ctx, sw.ID)
	if err != nil {
		t.Fatalf("get sweep: %v", err)
	}
	if cur.Status != SweepStatusRunning || cur.NextSeq != 1 {
		t.Errorf("스윕 = (%s, next_seq %d), want (running, 1)", cur.Status, cur.NextSeq)
	}

	// 남은 청크를 순차 소진한다.
	for seq := 1; seq < total; seq++ {
		if err := st.MarkScanFinished(ctx, lastChunkJobID(t, pool, sw.ID), ScanStatusCompleted, nil); err != nil {
			t.Fatalf("finish chunk: %v", err)
		}
		c, err := st.DispatchNextChunk(ctx, sw.ID, in)
		if err != nil {
			t.Fatalf("dispatch seq %d: %v", seq, err)
		}
		if c == nil || c.Seq != seq {
			t.Fatalf("dispatch = %+v, want seq %d", c, seq)
		}
	}
	if err := st.MarkScanFinished(ctx, lastChunkJobID(t, pool, sw.ID), ScanStatusCompleted, nil); err != nil {
		t.Fatalf("finish last chunk: %v", err)
	}

	// 더 발주할 것이 없으면 completed다.
	if c, err := st.DispatchNextChunk(ctx, sw.ID, in); err != nil || c != nil {
		t.Fatalf("dispatch after last = (%+v, %v), want (nil, nil)", c, err)
	}
	done, err := st.GetSweep(ctx, sw.ID)
	if err != nil {
		t.Fatalf("get sweep: %v", err)
	}
	if done.Status != SweepStatusCompleted {
		t.Errorf("status = %q, want completed", done.Status)
	}
	if done.FinishedAt == nil {
		t.Error("finished_at이 비었다")
	}
	if done.Progress.ChunksCompleted != total {
		t.Errorf("progress.chunks_completed = %d, want %d", done.Progress.ChunksCompleted, total)
	}
}

// TestDispatchNextChunkRecordsSweepOrigin은 자식 청크 Job의 origin을 고정한다.
//
// 0015가 scan_jobs.origin을 추가하면서 기존 행만 백필했고(`WHERE sweep_id IS NOT NULL`),
// 발주 INSERT는 컬럼을 빼먹어 **앞으로 생기는** 스윕 자식이 DEFAULT 'manual'로
// 기록됐다. origin은 "누가 이 스캔을 냈나"의 유일한 기록이고 재스캔 현황
// (`rescan_status.go`의 `origin='rescan'` 집계)과 같은 축을 쓰므로,
// 여기가 틀리면 발주자별 진단이 통째로 어긋난다.
func TestDispatchNextChunkRecordsSweepOrigin(t *testing.T) {
	pool := testPool(t)
	st := New(pool)
	ctx := context.Background()

	ips := testIPs(2)
	ports := WaveOrderPorts([]int{443}, testPriorityPorts)
	const chunkSize = 2

	sw := newTestSweep(t, st, pool, SweepInput{
		Targets: ips, IPCount: len(ips),
		PortSpec: "443", PortCount: len(ports),
		ExecutorScope: "server", RatePPS: 100, ChunkEndpoints: chunkSize,
	})

	chunk, err := st.DispatchNextChunk(ctx, sw.ID, sweepDispatch(ips, ports, chunkSize))
	if err != nil {
		t.Fatalf("dispatch: %v", err)
	}
	if chunk == nil {
		t.Fatal("첫 청크가 발주되지 않았다")
	}

	var origin string
	if err := pool.QueryRow(
		`SELECT origin FROM scan_jobs WHERE id = $1::uuid`, chunk.JobID).Scan(&origin); err != nil {
		t.Fatalf("read origin: %v", err)
	}
	if origin != "sweep" {
		t.Errorf("자식 Job origin = %q, want %q", origin, "sweep")
	}
}

// TestDispatchRetriesReapedChunk는 기존 리퍼와의 상호작용을 본다:
// ReapOrphanedServerScanJobs가 자식 청크를 failed로 회수해도 스윕은 무사하고,
// 부분 유니크 인덱스가 failed 행을 비켜가 같은 seq가 재발주된다 (설계 5.3).
// 재시도는 seq당 총 2회까지이고, 소진하면 스윕이 failed로 전이한다.
func TestDispatchRetriesReapedChunk(t *testing.T) {
	pool := testPool(t)
	st := New(pool)
	ctx := context.Background()

	ips := testIPs(2)
	ports := []int{443}
	const chunkSize = 1 // 청크 2개 (IP 1개 × 포트 1개)
	sw := newTestSweep(t, st, pool, SweepInput{
		Targets: ips, IPCount: len(ips),
		PortSpec: "443", PortCount: len(ports),
		ExecutorScope: "server", RatePPS: 100, ChunkEndpoints: chunkSize,
	})
	in := sweepDispatch(ips, ports, chunkSize)

	first, err := st.DispatchNextChunk(ctx, sw.ID, in)
	if err != nil || first == nil {
		t.Fatalf("dispatch first = (%+v, %v)", first, err)
	}

	// 서버 재시작 회수 — 자식 청크가 failed가 된다.
	if _, err := st.ReapOrphanedServerScanJobs(ctx); err != nil {
		t.Fatalf("reap: %v", err)
	}
	job, err := st.GetScanJob(ctx, first.JobID)
	if err != nil {
		t.Fatalf("get chunk job: %v", err)
	}
	if job.Status != ScanStatusFailed {
		t.Fatalf("회수된 청크 status = %q, want failed", job.Status)
	}
	if cur, err := st.GetSweep(ctx, sw.ID); err != nil {
		t.Fatalf("get sweep: %v", err)
	} else if cur.Status != SweepStatusRunning {
		t.Errorf("회수 후 스윕 status = %q, want running (스윕은 회수 대상이 아니다)", cur.Status)
	}

	// 같은 seq가 재발주된다 (attempt 2).
	retry, err := st.DispatchNextChunk(ctx, sw.ID, in)
	if err != nil || retry == nil {
		t.Fatalf("retry dispatch = (%+v, %v)", retry, err)
	}
	if retry.Seq != first.Seq {
		t.Errorf("재발주 seq = %d, want %d (실패한 seq 재시도)", retry.Seq, first.Seq)
	}
	if retry.Attempt != 2 {
		t.Errorf("재발주 attempt = %d, want 2", retry.Attempt)
	}
	if retry.JobID == first.JobID {
		t.Error("재발주가 같은 Job 행을 재사용했다 — 새 행이어야 한다")
	}

	// 두 번째도 실패하면 상한 소진 — 스윕이 failed가 된다.
	if _, err := st.ReapOrphanedServerScanJobs(ctx); err != nil {
		t.Fatalf("reap 2: %v", err)
	}
	c, err := st.DispatchNextChunk(ctx, sw.ID, in)
	if err != nil {
		t.Fatalf("dispatch after exhaustion: %v", err)
	}
	if c != nil {
		t.Errorf("상한 소진 후 발주 = %+v, want nil", c)
	}
	failed, err := st.GetSweep(ctx, sw.ID)
	if err != nil {
		t.Fatalf("get sweep: %v", err)
	}
	if failed.Status != SweepStatusFailed {
		t.Errorf("status = %q, want failed (재시도 상한 소진)", failed.Status)
	}
	if failed.LastError == nil || !strings.Contains(*failed.LastError, orphanedServerJobError) {
		t.Errorf("last_error = %v, want 마지막 청크 오류 포함", failed.LastError)
	}
}

// TestSweepProgressIsDerived는 진행률이 자식 Job 집계에서 파생되는지 본다.
// 부모에 카운터를 두지 않으므로 실패 후 재시도로 같은 엔드포인트를 다시 훑어도
// 이중 계상되지 않는다 (0011 사고 재발 방지).
func TestSweepProgressIsDerived(t *testing.T) {
	pool := testPool(t)
	st := New(pool)
	ctx := context.Background()

	ips := testIPs(3)
	ports := []int{443}
	const chunkSize = 1 // 청크 3개
	sw := newTestSweep(t, st, pool, SweepInput{
		Targets: ips, IPCount: len(ips),
		PortSpec: "443", PortCount: len(ports),
		ExecutorScope: "server", RatePPS: 100, ChunkEndpoints: chunkSize,
	})
	in := sweepDispatch(ips, ports, chunkSize)

	// 청크 0: 절반 진행 후 실패 → 재시도해서 완료.
	c0, err := st.DispatchNextChunk(ctx, sw.ID, in)
	if err != nil || c0 == nil {
		t.Fatalf("dispatch 0 = (%+v, %v)", c0, err)
	}
	setChunkCounters(t, pool, c0.JobID, 1, 1, 2, 1)
	if err := st.MarkScanFinished(ctx, c0.JobID, ScanStatusFailed, nil); err != nil {
		t.Fatalf("fail chunk 0: %v", err)
	}
	retry, err := st.DispatchNextChunk(ctx, sw.ID, in)
	if err != nil || retry == nil {
		t.Fatalf("retry = (%+v, %v)", retry, err)
	}
	setChunkCounters(t, pool, retry.JobID, 1, 1, 2, 1)
	if err := st.MarkScanFinished(ctx, retry.JobID, ScanStatusCompleted, nil); err != nil {
		t.Fatalf("finish retry: %v", err)
	}

	// 청크 1: 정상 완료.
	c1, err := st.DispatchNextChunk(ctx, sw.ID, in)
	if err != nil || c1 == nil {
		t.Fatalf("dispatch 1 = (%+v, %v)", c1, err)
	}
	setChunkCounters(t, pool, c1.JobID, 1, 0, 0, 0)
	if err := st.MarkScanFinished(ctx, c1.JobID, ScanStatusCompleted, nil); err != nil {
		t.Fatalf("finish chunk 1: %v", err)
	}

	// 청크 2: 발주만 하고 진행 중.
	c2, err := st.DispatchNextChunk(ctx, sw.ID, in)
	if err != nil || c2 == nil {
		t.Fatalf("dispatch 2 = (%+v, %v)", c2, err)
	}

	cur, err := st.GetSweep(ctx, sw.ID)
	if err != nil {
		t.Fatalf("get sweep: %v", err)
	}
	p := cur.Progress
	if p.ChunksTotal != 3 {
		t.Errorf("chunks_total = %d, want 3", p.ChunksTotal)
	}
	if p.ChunksCompleted != 2 {
		t.Errorf("chunks_completed = %d, want 2", p.ChunksCompleted)
	}
	if p.ChunksActive != 1 {
		t.Errorf("chunks_active = %d, want 1", p.ChunksActive)
	}
	if p.FailedAttempts != 1 {
		t.Errorf("failed_attempts = %d, want 1", p.FailedAttempts)
	}
	// 실패 행(1건)은 합산에서 빠진다 — 재시도분과 이중 계상되면 3이 된다.
	if p.ScannedEndpoints != 2 {
		t.Errorf("scanned_endpoints = %d, want 2 (실패 행 제외 — 이중 계상 금지)", p.ScannedEndpoints)
	}
	if p.ScannedEndpoints > cur.TotalEndpoints {
		t.Errorf("scanned_endpoints %d > total_endpoints %d (오버슛)", p.ScannedEndpoints, cur.TotalEndpoints)
	}
	if p.CertificatesFound != 2 {
		t.Errorf("certificates_found = %d, want 2", p.CertificatesFound)
	}
	if p.CertificatesNew != 1 {
		t.Errorf("certificates_new = %d, want 1", p.CertificatesNew)
	}
}

// TestSweepPauseResumeCancel은 상태 전이가 **발주자만** 제어하는지 본다:
// pause·cancel은 신규 발주만 멈추고 진행 중 청크는 그대로 완주한다 (설계 6.2).
func TestSweepPauseResumeCancel(t *testing.T) {
	pool := testPool(t)
	st := New(pool)
	ctx := context.Background()

	ips := testIPs(3)
	ports := []int{443}
	const chunkSize = 1
	reqID := "test-req-sweep-transition"
	t.Cleanup(func() { cleanupAuditByRequestID(pool, reqID) })

	sw := newTestSweep(t, st, pool, SweepInput{
		Targets: ips, IPCount: len(ips),
		PortSpec: "443", PortCount: len(ports),
		ExecutorScope: "server", RatePPS: 100, ChunkEndpoints: chunkSize,
	})
	in := sweepDispatch(ips, ports, chunkSize)
	act := SweepActor{RequestID: &reqID}

	c0, err := st.DispatchNextChunk(ctx, sw.ID, in)
	if err != nil || c0 == nil {
		t.Fatalf("dispatch 0 = (%+v, %v)", c0, err)
	}

	paused, err := st.PauseSweep(ctx, sw.ID, act)
	if err != nil {
		t.Fatalf("pause: %v", err)
	}
	if paused.Status != SweepStatusPaused {
		t.Errorf("status = %q, want paused", paused.Status)
	}
	// 진행 중 청크는 살아 있다 — 취소를 전파하지 않는다.
	if job, err := st.GetScanJob(ctx, c0.JobID); err != nil {
		t.Fatalf("get chunk job: %v", err)
	} else if job.Status != ScanStatusPending {
		t.Errorf("pause 후 청크 status = %q, want pending (완주시킨다)", job.Status)
	}
	// 청크가 끝나도 paused면 신규 발주가 없다.
	if err := st.MarkScanFinished(ctx, c0.JobID, ScanStatusCompleted, nil); err != nil {
		t.Fatalf("finish chunk 0: %v", err)
	}
	if c, err := st.DispatchNextChunk(ctx, sw.ID, in); err != nil || c != nil {
		t.Fatalf("paused dispatch = (%+v, %v), want (nil, nil)", c, err)
	}
	if _, err := st.PauseSweep(ctx, sw.ID, act); !errors.Is(err, ErrSweepState) {
		t.Errorf("중복 pause error = %v, want ErrSweepState", err)
	}

	resumed, err := st.ResumeSweep(ctx, sw.ID, act)
	if err != nil {
		t.Fatalf("resume: %v", err)
	}
	if resumed.Status != SweepStatusRunning {
		t.Errorf("status = %q, want running", resumed.Status)
	}
	c1, err := st.DispatchNextChunk(ctx, sw.ID, in)
	if err != nil || c1 == nil || c1.Seq != 1 {
		t.Fatalf("resume 후 dispatch = (%+v, %v), want seq 1", c1, err)
	}

	canceled, err := st.CancelSweep(ctx, sw.ID, act)
	if err != nil {
		t.Fatalf("cancel: %v", err)
	}
	if canceled.Status != SweepStatusCanceled {
		t.Errorf("status = %q, want canceled", canceled.Status)
	}
	if job, err := st.GetScanJob(ctx, c1.JobID); err != nil {
		t.Fatalf("get chunk job: %v", err)
	} else if job.Status != ScanStatusPending {
		t.Errorf("cancel 후 청크 status = %q, want pending (완주시킨다)", job.Status)
	}
	if _, err := st.ResumeSweep(ctx, sw.ID, act); !errors.Is(err, ErrSweepState) {
		t.Errorf("canceled resume error = %v, want ErrSweepState", err)
	}

	// 전이 3건 + 생성 1건의 감사가 남는다 (하드 규칙 5).
	var audits int
	if err := pool.QueryRow(
		`SELECT count(*) FROM audit_events WHERE target_id = $1 AND event_type = $2`,
		sw.ID, EventDiscoverySweep).Scan(&audits); err != nil {
		t.Fatalf("count audit events: %v", err)
	}
	if audits != 4 {
		t.Errorf("스윕 감사 = %d건, want 4 (생성 + pause/resume/cancel)", audits)
	}

	if _, err := st.PauseSweep(ctx, "00000000-0000-0000-0000-000000000000", act); !errors.Is(err, ErrSweepNotFound) {
		t.Errorf("없는 스윕 pause error = %v, want ErrSweepNotFound", err)
	}
}

// TestRefreshSweepStalls는 센서 대기 표시의 근거를 본다: 센서 스코프 스윕의
// 청크가 오래 pending이면 stalled_since가 서고, 집히면 해제된다.
// 종료 상태는 만들지 않는다 — 스윕은 계속 running이다.
func TestRefreshSweepStalls(t *testing.T) {
	pool := testPool(t)
	st := New(pool)
	ctx := context.Background()

	sensorID := newTestSensor(t, pool, "sensor-fixture-sweep-stall")
	ips := testIPs(2)
	ports := []int{443}
	const chunkSize = 1
	sw := newTestSweep(t, st, pool, SweepInput{
		Targets: ips, IPCount: len(ips),
		PortSpec: "443", PortCount: len(ports),
		ExecutorScope: sensorScopePrefix + sensorID, RatePPS: 100, ChunkEndpoints: chunkSize,
	})
	in := sweepDispatch(ips, ports, chunkSize)

	c0, err := st.DispatchNextChunk(ctx, sw.ID, in)
	if err != nil || c0 == nil {
		t.Fatalf("dispatch 0 = (%+v, %v)", c0, err)
	}
	// 센서 스코프 스윕의 청크는 센서 위임 Job이어야 리스로 집힌다.
	var executorType string
	if err := pool.QueryRow(`SELECT executor_type FROM scan_jobs WHERE id = $1::uuid`, c0.JobID).
		Scan(&executorType); err != nil {
		t.Fatalf("read executor_type: %v", err)
	}
	if executorType != "sensor" {
		t.Fatalf("executor_type = %q, want sensor", executorType)
	}

	// 방금 만든 청크는 아직 stalled가 아니다.
	if _, err := st.RefreshSweepStalls(ctx, time.Hour); err != nil {
		t.Fatalf("refresh stalls: %v", err)
	}
	if cur, err := st.GetSweep(ctx, sw.ID); err != nil {
		t.Fatalf("get sweep: %v", err)
	} else if cur.StalledSince != nil {
		t.Errorf("stalled_since = %v, want nil (아직 대기 시간 미달)", cur.StalledSince)
	}

	// 청크를 오래된 것으로 만든다.
	if _, err := pool.Exec(
		`UPDATE scan_jobs SET created_at = now() - interval '30 minutes' WHERE id = $1::uuid`,
		c0.JobID); err != nil {
		t.Fatalf("age chunk: %v", err)
	}
	if n, err := st.RefreshSweepStalls(ctx, DefaultSweepStallAfter); err != nil || n < 1 {
		t.Fatalf("refresh stalls = (%d, %v), want >=1", n, err)
	}
	stalled, err := st.GetSweep(ctx, sw.ID)
	if err != nil {
		t.Fatalf("get sweep: %v", err)
	}
	if stalled.StalledSince == nil {
		t.Fatal("stalled_since가 비었다 (센서 대기 미감지)")
	}
	if stalled.Status != SweepStatusRunning {
		t.Errorf("status = %q, want running (신규 종료 상태를 만들지 않는다)", stalled.Status)
	}

	// 센서가 집어가면 해제된다.
	if _, err := st.LeaseNextSensorJob(ctx, sensorID); err != nil {
		t.Fatalf("lease: %v", err)
	}
	if _, err := st.RefreshSweepStalls(ctx, DefaultSweepStallAfter); err != nil {
		t.Fatalf("refresh stalls: %v", err)
	}
	if cleared, err := st.GetSweep(ctx, sw.ID); err != nil {
		t.Fatalf("get sweep: %v", err)
	} else if cleared.StalledSince != nil {
		t.Errorf("stalled_since = %v, want nil (리스 후 해제)", cleared.StalledSince)
	}
}

// TestListSweepsFiltersAndPaginates는 상태 필터·최신순 페이지네이션을 본다.
func TestListSweepsFiltersAndPaginates(t *testing.T) {
	pool := testPool(t)
	st := New(pool)
	ctx := context.Background()

	act := SweepActor{}
	mk := func(name string) *Sweep {
		t.Helper()
		n := name
		return newTestSweep(t, st, pool, SweepInput{
			Name: &n, Targets: testIPs(1), IPCount: 1,
			PortSpec: "443", PortCount: 1,
			ExecutorScope: "server", RatePPS: 100,
		})
	}
	a := mk("sweep-list-a")
	b := mk("sweep-list-b")
	c := mk("sweep-list-c")
	// created_at을 명시적으로 벌려 정렬을 결정론적으로 만든다(같은 초에 생성되면 tie).
	for i, sw := range []*Sweep{a, b, c} {
		if _, err := pool.Exec(
			`UPDATE scan_sweeps SET created_at = now() - make_interval(mins => $2) WHERE id = $1::uuid`,
			sw.ID, 3-i); err != nil {
			t.Fatalf("age sweep: %v", err)
		}
	}
	if _, err := st.CancelSweep(ctx, b.ID, act); err != nil {
		t.Fatalf("cancel: %v", err)
	}

	got, err := st.ListSweeps(ctx, ListSweepsInput{Statuses: []string{SweepStatusCanceled}})
	if err != nil {
		t.Fatalf("list sweeps: %v", err)
	}
	found := false
	for _, sw := range got.Items {
		if sw.Status != SweepStatusCanceled {
			t.Errorf("필터 결과에 status %q가 섞였다", sw.Status)
		}
		if sw.ID == b.ID {
			found = true
		}
	}
	if !found {
		t.Error("canceled 필터에 대상 스윕이 없다")
	}

	// 최신순 — 마지막에 만든 c가 a보다 앞이다.
	all, err := st.ListSweeps(ctx, ListSweepsInput{PageSize: 200})
	if err != nil {
		t.Fatalf("list sweeps: %v", err)
	}
	idxOf := func(id string) int {
		for i, sw := range all.Items {
			if sw.ID == id {
				return i
			}
		}
		return -1
	}
	if ia, ic := idxOf(a.ID), idxOf(c.ID); ia < 0 || ic < 0 || ic > ia {
		t.Errorf("정렬 위치 a=%d c=%d, want c가 a보다 앞(최신순)", ia, ic)
	}

	page, err := st.ListSweeps(ctx, ListSweepsInput{PageSize: 1, Page: 1})
	if err != nil {
		t.Fatalf("list sweeps: %v", err)
	}
	if len(page.Items) != 1 || page.PageSize != 1 || page.Total < 3 {
		t.Errorf("페이지 = %d건 size %d total %d, want 1건 size 1 total>=3",
			len(page.Items), page.PageSize, page.Total)
	}

	if _, err := st.GetSweep(ctx, "not-a-uuid"); !errors.Is(err, ErrSweepNotFound) {
		t.Errorf("잘못된 id 조회 error = %v, want ErrSweepNotFound", err)
	}
}

// lastChunkJobID는 스윕의 가장 최근 자식 청크 Job id를 돌려준다.
func lastChunkJobID(t *testing.T, pool *sql.DB, sweepID string) string {
	t.Helper()
	var id string
	if err := pool.QueryRow(
		`SELECT id::text FROM scan_jobs WHERE sweep_id = $1::uuid ORDER BY created_at DESC, sweep_seq DESC LIMIT 1`,
		sweepID).Scan(&id); err != nil {
		t.Fatalf("read last chunk job: %v", err)
	}
	return id
}

// setChunkCounters는 자식 청크 Job의 진행률 카운터를 직접 세팅한다
// (실제 스캔 없이 집계 파생만 검증하기 위한 픽스처).
func setChunkCounters(t *testing.T, pool *sql.DB, jobID string, scanned, reachable, found, newCerts int) {
	t.Helper()
	if _, err := pool.Exec(`
		UPDATE scan_jobs SET scanned_endpoints = $2, reachable_endpoints = $3,
		       certificates_found = $4, certificates_new = $5
		 WHERE id = $1::uuid`,
		jobID, scanned, reachable, found, newCerts); err != nil {
		t.Fatalf("set chunk counters: %v", err)
	}
}
