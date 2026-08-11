package store

// 통합 작업 목록(GET /discovery/activity)의 저장 계층 검증.
// 검증 축은 둘이다: ① 무엇이 목록에 실리는가(자동 발주가 목록을 도배하지 않는가)
// ② cancel 판정이 각 축의 상태 머신과 일치하는가.

import (
	"context"
	"database/sql"
	"testing"
)

// insertActivityScanJob은 scan_jobs 1행을 직접 넣는다.
// CreateScanJob은 origin='manual'·status='pending' 고정이라 종료 상태·자동 발주
// 픽스처를 만들 수 없어 여기서는 SQL로 직접 넣는다.
func insertActivityScanJob(t *testing.T, pool *sql.DB, origin, status, executorType string) string {
	t.Helper()
	var id string
	err := pool.QueryRow(`
		INSERT INTO scan_jobs (targets, ports, timeout_ms, concurrency, status, origin,
		                       executor_type, total_endpoints, scanned_endpoints)
		VALUES ('{192.0.2.10}', '{443}', 2000, 4, $1, $2, $3, 1, 0)
		RETURNING id::text`, status, origin, executorType).Scan(&id)
	if err != nil {
		t.Fatalf("insert scan job (%s/%s): %v", origin, status, err)
	}
	t.Cleanup(func() { _, _ = pool.Exec(`DELETE FROM scan_jobs WHERE id = $1::uuid`, id) })
	return id
}

// insertActivitySweep은 scan_sweeps 1행을 직접 넣는다(scheduleID는 nil이면 NULL).
func insertActivitySweep(t *testing.T, pool *sql.DB, status string, scheduleID *string) string {
	t.Helper()
	var id string
	err := pool.QueryRow(`
		INSERT INTO scan_sweeps (name, targets, port_spec, executor_scope, status,
		                         total_endpoints, total_chunks, rate_pps, chunk_endpoints, schedule_id)
		VALUES ('activity-sweep', '{192.0.2.0/24}', 'all', 'server', $1,
		        100, 2, 10, 4096, $2::uuid)
		RETURNING id::text`, status, scheduleID).Scan(&id)
	if err != nil {
		t.Fatalf("insert scan sweep (%s): %v", status, err)
	}
	t.Cleanup(func() { _, _ = pool.Exec(`DELETE FROM scan_sweeps WHERE id = $1::uuid`, id) })
	return id
}

// insertActivitySweepChunk은 스윕 자식 청크 Job 1건을 넣는다(진행률 집계 대상).
func insertActivitySweepChunk(t *testing.T, pool *sql.DB, sweepID string, seq int) string {
	t.Helper()
	var id string
	err := pool.QueryRow(`
		INSERT INTO scan_jobs (targets, ports, timeout_ms, concurrency, status, origin,
		                       executor_type, total_endpoints, scanned_endpoints,
		                       reachable_endpoints, certificates_found, certificates_new,
		                       sweep_id, sweep_seq)
		VALUES ('{192.0.2.0/24}', '{443}', 2000, 4, 'completed', 'sweep', 'server',
		        10, 10, 3, 2, 1, $1::uuid, $2)
		RETURNING id::text`, sweepID, seq).Scan(&id)
	if err != nil {
		t.Fatalf("insert sweep chunk: %v", err)
	}
	t.Cleanup(func() { _, _ = pool.Exec(`DELETE FROM scan_jobs WHERE id = $1::uuid`, id) })
	return id
}

// insertActivitySchedule은 preset 스케줄 1행을 넣는다(감사 이벤트 없이).
func insertActivitySchedule(t *testing.T, pool *sql.DB, name string) string {
	t.Helper()
	var id string
	err := pool.QueryRow(`
		INSERT INTO scan_schedules (kind, name, enabled, targets, port_spec, interval_seconds)
		VALUES ('preset', $1, true, '{192.0.2.0/24}', '443,8443', 86400)
		RETURNING id::text`, name).Scan(&id)
	if err != nil {
		t.Fatalf("insert scan schedule: %v", err)
	}
	t.Cleanup(func() { _, _ = pool.Exec(`DELETE FROM scan_schedules WHERE id = $1::uuid`, id) })
	return id
}

func insertActivityScheduleRun(t *testing.T, pool *sql.DB, scheduleID, trigger, status string, sweepID *string) string {
	t.Helper()
	var id string
	err := pool.QueryRow(`
		INSERT INTO scan_schedule_runs (schedule_id, trigger, status, sweep_id, finished_at)
		VALUES ($1::uuid, $2, $3, $4::uuid,
		        CASE WHEN $3 = 'running' THEN NULL ELSE now() END)
		RETURNING id::text`, scheduleID, trigger, status, sweepID).Scan(&id)
	if err != nil {
		t.Fatalf("insert scan schedule run (%s): %v", status, err)
	}
	t.Cleanup(func() { _, _ = pool.Exec(`DELETE FROM scan_schedule_runs WHERE id = $1::uuid`, id) })
	return id
}

// activityByID는 목록을 id로 색인한다 — 다른 테스트가 남긴 행과 섞여도
// 존재/부재 판정이 흔들리지 않는다.
func activityByID(items []ActivityItem) map[string]ActivityItem {
	m := make(map[string]ActivityItem, len(items))
	for _, it := range items {
		m[it.ID] = it
	}
	return m
}

func TestListDiscoveryActivityCombinesSources(t *testing.T) {
	pool := testPool(t)
	st := New(pool)
	ctx := context.Background()

	manualJob := insertActivityScanJob(t, pool, ScanOriginManual, ScanStatusPending, "server")
	rescanJob := insertActivityScanJob(t, pool, ScanOriginRescan, ScanStatusRunning, "server")
	standalone := insertActivitySweep(t, pool, SweepStatusRunning, nil)
	chunkJob := insertActivitySweepChunk(t, pool, standalone, 0)
	scheduleID := insertActivitySchedule(t, pool, "활동 목록 테스트")
	scheduledSweep := insertActivitySweep(t, pool, SweepStatusRunning, &scheduleID)
	runID := insertActivityScheduleRun(t, pool, scheduleID, ScheduleTriggerSchedule,
		ScheduleRunRunning, &scheduledSweep)

	res, err := st.ListDiscoveryActivity(ctx, ListActivityInput{PageSize: 200})
	if err != nil {
		t.Fatalf("list discovery activity: %v", err)
	}
	items := activityByID(res.Items)

	// ① 자동 발주는 싣지 않는다 — 재스캔 개별 Job과 스윕 자식 청크는 도배의 원인이다.
	if _, ok := items[rescanJob]; ok {
		t.Error("rescan job is listed; automatic rescan jobs must stay out of the activity list")
	}
	if _, ok := items[chunkJob]; ok {
		t.Error("sweep chunk job is listed; the parent sweep represents it")
	}
	// 정기 스캔이 발주한 스윕은 실행 행이 대표한다(같은 작업이 두 줄로 보이면 안 된다).
	if _, ok := items[scheduledSweep]; ok {
		t.Error("schedule-dispatched sweep is listed separately from its schedule run")
	}

	// ② 세 축이 하나의 목록으로 합쳐진다.
	job, ok := items[manualJob]
	if !ok {
		t.Fatal("manual scan job is missing from the activity list")
	}
	if job.Kind != ActivityKindScan {
		t.Errorf("manual job kind = %q, want %q", job.Kind, ActivityKindScan)
	}
	if !job.Active || !job.Cancel {
		t.Errorf("pending manual job: active=%v cancel=%v, want both true", job.Active, job.Cancel)
	}
	if job.PortSpec != "443" {
		t.Errorf("manual job port_spec = %q, want \"443\"", job.PortSpec)
	}

	sweep, ok := items[standalone]
	if !ok {
		t.Fatal("standalone sweep is missing from the activity list")
	}
	if sweep.Kind != ActivityKindSweep {
		t.Errorf("sweep kind = %q, want %q", sweep.Kind, ActivityKindSweep)
	}
	// 진행률은 스윕 상세와 같은 자식 Job 집계에서 온다.
	if sweep.ScannedEndpoints != 10 || sweep.CertificatesNew != 1 {
		t.Errorf("sweep progress = scanned %d / new %d, want 10 / 1",
			sweep.ScannedEndpoints, sweep.CertificatesNew)
	}
	if !sweep.Cancel {
		t.Error("running sweep cancel = false, want true")
	}

	run, ok := items[runID]
	if !ok {
		t.Fatal("schedule run is missing from the activity list")
	}
	if run.Kind != ActivityKindScheduleRun {
		t.Errorf("run kind = %q, want %q", run.Kind, ActivityKindScheduleRun)
	}
	if run.ScheduleID == nil || *run.ScheduleID != scheduleID {
		t.Errorf("run schedule_id = %v, want %s", run.ScheduleID, scheduleID)
	}
	if run.SweepID == nil || *run.SweepID != scheduledSweep {
		t.Errorf("run sweep_id = %v, want %s", run.SweepID, scheduledSweep)
	}
	if run.Name == nil || *run.Name != "활동 목록 테스트" {
		t.Errorf("run name = %v, want the schedule name", run.Name)
	}
	if !run.Active || !run.Cancel {
		t.Errorf("running schedule run: active=%v cancel=%v, want both true", run.Active, run.Cancel)
	}
}

func TestListDiscoveryActivityCancelJudgment(t *testing.T) {
	pool := testPool(t)
	st := New(pool)
	ctx := context.Background()

	done := insertActivityScanJob(t, pool, ScanOriginManual, ScanStatusCompleted, "server")
	sensorRunning := insertActivityScanJob(t, pool, ScanOriginManual, ScanStatusRunning, "sensor")
	canceledSweep := insertActivitySweep(t, pool, SweepStatusCanceled, nil)
	pausedSweep := insertActivitySweep(t, pool, SweepStatusPaused, nil)
	scheduleID := insertActivitySchedule(t, pool, "취소 판정 테스트")
	finishedRun := insertActivityScheduleRun(t, pool, scheduleID, ScheduleTriggerManual,
		ScheduleRunCompleted, nil)

	res, err := st.ListDiscoveryActivity(ctx, ListActivityInput{PageSize: 200})
	if err != nil {
		t.Fatalf("list discovery activity: %v", err)
	}
	items := activityByID(res.Items)

	cases := []struct {
		name   string
		id     string
		active bool
		cancel bool
	}{
		// 종료된 Job은 취소 대상이 아니다.
		{"completed scan", done, false, false},
		// 센서가 집어간 실행은 멈출 수 없다(CancelScanJob과 같은 판정).
		{"sensor running scan", sensorRunning, true, false},
		{"canceled sweep", canceledSweep, false, false},
		// paused는 종료가 아니다 — 여전히 취소할 수 있다.
		{"paused sweep", pausedSweep, true, true},
		{"finished schedule run", finishedRun, false, false},
	}
	for _, c := range cases {
		it, ok := items[c.id]
		if !ok {
			t.Errorf("%s is missing from the activity list", c.name)
			continue
		}
		if it.Active != c.active || it.Cancel != c.cancel {
			t.Errorf("%s: active=%v cancel=%v, want %v / %v",
				c.name, it.Active, it.Cancel, c.active, c.cancel)
		}
	}

	// active 필터는 종료된 항목을 걷어낸다.
	activeOnly := true
	res, err = st.ListDiscoveryActivity(ctx, ListActivityInput{Active: &activeOnly, PageSize: 200})
	if err != nil {
		t.Fatalf("list active discovery activity: %v", err)
	}
	items = activityByID(res.Items)
	if _, ok := items[done]; ok {
		t.Error("active=true returned a completed scan job")
	}
	if _, ok := items[sensorRunning]; !ok {
		t.Error("active=true dropped a running scan job")
	}

	// kind 필터는 축 하나만 남긴다.
	res, err = st.ListDiscoveryActivity(ctx, ListActivityInput{
		Kinds: []string{ActivityKindSweep}, PageSize: 200})
	if err != nil {
		t.Fatalf("list sweep activity: %v", err)
	}
	for _, it := range res.Items {
		if it.Kind != ActivityKindSweep {
			t.Fatalf("kind=sweep filter returned %q", it.Kind)
		}
	}
	if _, ok := activityByID(res.Items)[canceledSweep]; !ok {
		t.Error("kind=sweep filter dropped the canceled sweep")
	}
}
