package discovery

// 정기 스캔 스케줄러 테스트 (schedules.go).
// 고정하는 규약은 넷이다: 주기 도래 → 스윕 1건 발주 / 발주한 스윕이 끝나야 실행이
// 닫히고 다음 주기가 열리는 것 / '지금 실행'이 주기를 리셋하지 않는 것 /
// 재스캔이 'registered' 설정 행을 오버라이드로 읽는 것(가속 티어는 그대로).
//
// DB가 필요한 테스트는 TEST_DATABASE_URL 미설정 시 스킵한다.

import (
	"context"
	"database/sql"
	"testing"
	"time"

	"github.com/Falke060918/INHERIT_AutoCert/backend/internal/store"
)

// newTestSchedule은 preset 스케줄 1건과 정리 훅을 만든다.
// 감사 행은 append-only 트리거(0007)를 잠시 내리고 지운다(sweep_test와 같은 방식).
func newTestSchedule(t *testing.T, st *store.Store, pool *sql.DB, in store.ScanScheduleInput) *store.ScanSchedule {
	t.Helper()
	sc, err := st.CreateScanSchedule(context.Background(), in)
	if err != nil {
		t.Fatalf("create scan schedule: %v", err)
	}
	t.Cleanup(func() {
		_, _ = pool.Exec(`ALTER TABLE audit_events DISABLE TRIGGER USER`)
		_, _ = pool.Exec(`DELETE FROM audit_events WHERE target_id IN (
			SELECT id::text FROM scan_sweeps WHERE schedule_id = $1::uuid)`, sc.ID)
		_, _ = pool.Exec(`DELETE FROM audit_events WHERE target_id = $1`, sc.ID)
		_, _ = pool.Exec(`ALTER TABLE audit_events ENABLE TRIGGER USER`)
		_, _ = pool.Exec(`DELETE FROM scan_jobs WHERE sweep_id IN (
			SELECT id FROM scan_sweeps WHERE schedule_id = $1::uuid)`, sc.ID)
		// 실행 이력은 FK CASCADE로 함께 사라지지만 스윕은 schedule_id만 NULL이 된다.
		_, _ = pool.Exec(`DELETE FROM scan_sweeps WHERE schedule_id = $1::uuid`, sc.ID)
		_, _ = pool.Exec(`DELETE FROM scan_schedules WHERE id = $1::uuid`, sc.ID)
	})
	return sc
}

// dueNow는 예정 시각을 과거로 밀어 주기 도래 상태를 만든다(테스트 전용 직접 UPDATE).
func dueNow(t *testing.T, pool *sql.DB, scheduleID string) {
	t.Helper()
	if _, err := pool.Exec(
		`UPDATE scan_schedules SET next_run_at = now() - interval '1 minute' WHERE id = $1::uuid`,
		scheduleID); err != nil {
		t.Fatalf("set next_run_at: %v", err)
	}
}

// testScheduleOptions는 테스트용 스케줄 설정이다.
func testScheduleOptions() ScheduleOptions {
	return ScheduleOptions{
		Enabled:         true,
		DefaultInterval: 6 * time.Hour,
		MaxPerTick:      4,
		MaxDays:         store.DefaultSweepMaxDays,
	}
}

// TestScanScheduleTickDispatchesSweep은 tick 1회가 주기 도래 스케줄에 스윕 1건을
// 발주하고 실행 행에 연결하는 것을 고정한다. 발주된 스윕에는 schedule_id가 실려야
// 한다 — 정기/수동 구분이 그 컬럼 하나에 달려 있다(scan_jobs.origin은 불변).
func TestScanScheduleTickDispatchesSweep(t *testing.T) {
	st, pool := testSweepStore(t)
	ctx := context.Background()
	m := NewManager(st).UseScheduleOptions(testScheduleOptions())

	sc := newTestSchedule(t, st, pool, store.ScanScheduleInput{
		Name:     "정기 스캔 tick 테스트",
		Enabled:  true,
		Targets:  []string{"198.51.100.0/30"},
		PortSpec: "443",
	})
	dueNow(t, pool, sc.ID)

	sum, err := m.RunScanScheduleTickOnce(ctx)
	if err != nil {
		t.Fatalf("schedule tick: %v", err)
	}
	if sum.Started != 1 || sum.Failed != 0 {
		t.Fatalf("tick summary = %+v, want started=1 failed=0", sum)
	}

	runs, err := st.ListScanScheduleRuns(ctx, store.ListScanScheduleRunsInput{ScheduleID: sc.ID})
	if err != nil {
		t.Fatalf("list runs: %v", err)
	}
	if len(runs.Items) != 1 {
		t.Fatalf("runs = %d, want 1", len(runs.Items))
	}
	run := runs.Items[0]
	if run.Status != store.ScheduleRunRunning || run.Trigger != store.ScheduleTriggerSchedule {
		t.Errorf("run = %s/%s, want running/schedule", run.Status, run.Trigger)
	}
	if run.SweepID == nil {
		t.Fatal("run.sweep_id is nil — 발주한 스윕이 실행에 연결되지 않았다")
	}

	sw, err := st.GetSweep(ctx, *run.SweepID)
	if err != nil {
		t.Fatalf("get sweep: %v", err)
	}
	if sw.ScheduleID == nil || *sw.ScheduleID != sc.ID {
		t.Errorf("sweep.schedule_id = %v, want %s", sw.ScheduleID, sc.ID)
	}
	if sw.PortSpec != "443" || sw.TotalEndpoints != 4 {
		t.Errorf("sweep = %s/%d endpoints, want 443/4", sw.PortSpec, sw.TotalEndpoints)
	}

	// 다음 예정은 **실행 시작 시각 + 주기**로 밀려 있어야 한다(예정 시각 기준이 아니다).
	after, err := st.GetScanSchedule(ctx, sc.ID)
	if err != nil {
		t.Fatalf("get schedule: %v", err)
	}
	if after.NextRunAt == nil || !after.NextRunAt.After(time.Now()) {
		t.Errorf("next_run_at = %v, want 미래 시각", after.NextRunAt)
	}
}

// TestScanScheduleTickClosesRunWhenSweepFinishes는 마감 단계를 고정한다.
// 실행이 running으로 남아 있는 동안 그 스케줄의 주기는 건너뛰어지므로, 스윕이 끝난
// 실행을 닫는 것이 곧 "다음 주기를 여는 것"이다.
func TestScanScheduleTickClosesRunWhenSweepFinishes(t *testing.T) {
	st, pool := testSweepStore(t)
	ctx := context.Background()
	m := NewManager(st).UseScheduleOptions(testScheduleOptions())

	sc := newTestSchedule(t, st, pool, store.ScanScheduleInput{
		Name:     "정기 스캔 마감 테스트",
		Enabled:  true,
		Targets:  []string{"198.51.100.0/30"},
		PortSpec: "443",
	})
	dueNow(t, pool, sc.ID)
	if _, err := m.RunScanScheduleTickOnce(ctx); err != nil {
		t.Fatalf("schedule tick: %v", err)
	}

	runs, err := st.ListScanScheduleRuns(ctx, store.ListScanScheduleRunsInput{ScheduleID: sc.ID})
	if err != nil || len(runs.Items) != 1 {
		t.Fatalf("list runs: %v (%d건)", err, len(runs.Items))
	}
	run := runs.Items[0]

	// 스윕이 아직 도는 동안에는 실행이 닫히지 않는다.
	sum, err := m.RunScanScheduleTickOnce(ctx)
	if err != nil {
		t.Fatalf("schedule tick: %v", err)
	}
	if sum.Closed != 0 {
		t.Errorf("closed = %d, want 0 (스윕이 아직 도는 중)", sum.Closed)
	}

	// 스윕 종료를 흉내낸다(스윕 상태 머신 자체는 sweep_test가 고정한다).
	if _, err := pool.Exec(
		`UPDATE scan_sweeps SET status = 'completed', finished_at = now() WHERE id = $1::uuid`,
		*run.SweepID); err != nil {
		t.Fatalf("finish sweep: %v", err)
	}

	sum, err = m.RunScanScheduleTickOnce(ctx)
	if err != nil {
		t.Fatalf("schedule tick: %v", err)
	}
	if sum.Closed != 1 {
		t.Errorf("closed = %d, want 1", sum.Closed)
	}
	closed, err := st.GetScanScheduleRun(ctx, run.ID)
	if err != nil {
		t.Fatalf("get run: %v", err)
	}
	if closed.Status != store.ScheduleRunCompleted || closed.FinishedAt == nil {
		t.Errorf("run = %s finished_at=%v, want completed", closed.Status, closed.FinishedAt)
	}
	if closed.DispatchedEndpoints != 4 {
		t.Errorf("dispatched_endpoints = %d, want 4", closed.DispatchedEndpoints)
	}
}

// TestRunScanScheduleNowKeepsNextRunAt는 '지금 실행'이 주기를 리셋하지 않음을
// 발주 경로까지 포함해 고정한다 — 설정을 자주 눌러도 원래 일정이 밀리면 안 된다
// (사용자 확정 규약). store 단위 테스트와 달리 여기서는 스윕까지 실제로 발주된다.
func TestRunScanScheduleNowKeepsNextRunAt(t *testing.T) {
	st, pool := testSweepStore(t)
	ctx := context.Background()
	m := NewManager(st).UseScheduleOptions(testScheduleOptions())

	sc := newTestSchedule(t, st, pool, store.ScanScheduleInput{
		Name:     "정기 스캔 수동 실행 테스트",
		Enabled:  true,
		Targets:  []string{"198.51.100.0/30"},
		PortSpec: "443",
	})
	before, err := st.GetScanSchedule(ctx, sc.ID)
	if err != nil {
		t.Fatalf("get schedule: %v", err)
	}

	run, err := m.RunScanScheduleNow(ctx, sc.ID, store.ScanScheduleActor{})
	if err != nil {
		t.Fatalf("run now: %v", err)
	}
	if run.Trigger != store.ScheduleTriggerManual || run.SweepID == nil {
		t.Fatalf("run = %s sweep=%v, want manual + 스윕 발주", run.Trigger, run.SweepID)
	}

	after, err := st.GetScanSchedule(ctx, sc.ID)
	if err != nil {
		t.Fatalf("get schedule: %v", err)
	}
	if before.NextRunAt == nil || after.NextRunAt == nil || !after.NextRunAt.Equal(*before.NextRunAt) {
		t.Errorf("next_run_at %v → %v, want 불변", before.NextRunAt, after.NextRunAt)
	}
	if after.LastRunID == nil || *after.LastRunID != run.ID {
		t.Errorf("last_run_id = %v, want %s", after.LastRunID, run.ID)
	}

	// 활성 실행이 있는 동안 두 번째 '지금 실행'은 거부된다(겹침 금지).
	if _, err := m.RunScanScheduleNow(ctx, sc.ID, store.ScanScheduleActor{}); err == nil {
		t.Error("두 번째 '지금 실행'이 통과했다 — 활성 실행은 1건이어야 한다")
	}
}

// TestRescanSettingsFollowRegisteredSchedule은 재스캔이 'registered' 설정 행을
// 오버라이드로 읽는 것을 고정한다. **만료 임박 가속 티어는 오버라이드하지 않는다** —
// 그 구간은 갱신 직후의 오탐 창을 막는 안전장치라 설정 표면에 두지 않는다.
func TestRescanSettingsFollowRegisteredSchedule(t *testing.T) {
	st, pool := testSweepStore(t)
	ctx := context.Background()
	s := &rescanScheduler{m: NewManager(st), opts: testRescanOptions().normalized()}

	reg, err := st.GetScanScheduleByKind(ctx, store.ScheduleKindRegistered)
	if err != nil {
		t.Fatalf("get registered schedule: %v", err)
	}
	// 시드 값(enabled=true, interval NULL)으로 되돌린다.
	t.Cleanup(func() {
		_, _ = pool.Exec(
			`UPDATE scan_schedules SET enabled = $2, interval_seconds = $3::int, rate_pps = $4::int
			  WHERE id = $1::uuid`, reg.ID, reg.Enabled, reg.IntervalSeconds, reg.RatePPS)
	})

	// 시드 상태: 켜져 있고 주기는 환경변수 기본값 그대로다.
	opts, enabled, err := s.settings(ctx)
	if err != nil {
		t.Fatalf("settings: %v", err)
	}
	if !enabled || opts.Interval != s.opts.Interval {
		t.Errorf("seed settings = enabled:%v interval:%v, want true/%v", enabled, opts.Interval, s.opts.Interval)
	}

	if _, err := pool.Exec(
		`UPDATE scan_schedules SET enabled = false, interval_seconds = 3600, rate_pps = 7
		  WHERE id = $1::uuid`, reg.ID); err != nil {
		t.Fatalf("update registered schedule: %v", err)
	}

	opts, enabled, err = s.settings(ctx)
	if err != nil {
		t.Fatalf("settings: %v", err)
	}
	if enabled {
		t.Error("enabled = true, want false (설정 행이 발주를 끈다)")
	}
	if opts.Interval != time.Hour || opts.RatePPS != 7 {
		t.Errorf("override = %v/%dpps, want 1h/7pps", opts.Interval, opts.RatePPS)
	}
	// 가속 티어는 상수 그대로여야 한다.
	iv := opts.intervals()
	if iv.Near != rescanNearInterval || iv.Urgent != rescanUrgentInterval {
		t.Errorf("가속 티어 = %v/%v, want %v/%v", iv.Near, iv.Urgent, rescanNearInterval, rescanUrgentInterval)
	}

	// 꺼진 상태의 tick은 발주하지 않는다(은퇴·교체 전이는 그대로 돈다).
	sum, err := s.runOnce(ctx)
	if err != nil {
		t.Fatalf("rescan tick: %v", err)
	}
	if !sum.Disabled || sum.Claimed != 0 {
		t.Errorf("tick summary = %+v, want disabled=true claimed=0", sum)
	}
}
