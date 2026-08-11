package store

// 정기 스캔 스케줄(0019) 저장 계층 검증.
// 검증 축은 "겹치지 않는가"와 "주기를 언제 미는가" 둘이다.

import (
	"context"
	"database/sql"
	"errors"
	"testing"
	"time"
)

// sqlStateOf는 PostgreSQL 오류 코드를 꺼낸다(없으면 "").
func sqlStateOf(err error) string {
	type sqlState interface{ SQLState() string }
	var st sqlState
	if errors.As(err, &st) {
		return st.SQLState()
	}
	return ""
}

// createTestPresetSchedule은 preset 스케줄 1건을 만들고 정리를 등록한다.
func createTestPresetSchedule(t *testing.T, st *Store, pool *sql.DB, reqID string, in ScanScheduleInput) *ScanSchedule {
	t.Helper()
	if in.Name == "" {
		in.Name = "테스트 프리셋"
	}
	if in.PortSpec == "" {
		in.PortSpec = "443,8443"
	}
	if len(in.Targets) == 0 {
		in.Targets = []string{"192.0.2.0/24"}
	}
	in.Actor.RequestID = &reqID
	sc, err := st.CreateScanSchedule(context.Background(), in)
	if err != nil {
		t.Fatalf("create preset schedule: %v", err)
	}
	t.Cleanup(func() {
		// 실행 이력은 FK CASCADE로 함께 사라진다.
		_, _ = pool.Exec(`DELETE FROM scan_schedules WHERE id = $1::uuid`, sc.ID)
	})
	return sc
}

// setNextRunAt은 예정 시각을 과거로 밀어 due 상태를 만든다(테스트 전용 직접 UPDATE).
func setNextRunAt(t *testing.T, pool *sql.DB, id string, d time.Duration) {
	t.Helper()
	if _, err := pool.Exec(
		`UPDATE scan_schedules SET next_run_at = now() + make_interval(secs => $2::int) WHERE id = $1::uuid`,
		id, int(d/time.Second)); err != nil {
		t.Fatalf("set next_run_at: %v", err)
	}
}

// TestScanScheduleSeedAndSingletonKinds는 시드 2행과 부분 유니크 제약을 고정한다.
// preset만 N행이고 full·registered는 각각 1행이다.
func TestScanScheduleSeedAndSingletonKinds(t *testing.T) {
	pool := testPool(t)
	st := New(pool)
	ctx := context.Background()
	reqID := "test-req-schedule-seed"
	t.Cleanup(func() { cleanupAuditByRequestID(pool, reqID) })

	full, err := st.GetScanScheduleByKind(ctx, ScheduleKindFull)
	if err != nil {
		t.Fatalf("get full schedule: %v", err)
	}
	// 신규 설치가 저절로 전 대역을 훑지 않아야 한다.
	if full.Enabled || len(full.Targets) != 0 {
		t.Errorf("full seed enabled=%v targets=%v, want disabled with no targets", full.Enabled, full.Targets)
	}
	if full.IntervalSeconds == nil || *full.IntervalSeconds != 30*24*3600 {
		t.Errorf("full seed interval = %v, want 30 days", full.IntervalSeconds)
	}
	reg, err := st.GetScanScheduleByKind(ctx, ScheduleKindRegistered)
	if err != nil {
		t.Fatalf("get registered schedule: %v", err)
	}
	if !reg.Enabled {
		t.Errorf("registered seed enabled = false, want true")
	}
	if len(reg.Targets) != 0 || reg.PortSpec != nil {
		t.Errorf("registered seed targets=%v port_spec=%v, want empty", reg.Targets, reg.PortSpec)
	}

	// preset은 여러 개 등록할 수 있다 (대역마다 다른 주기·포트).
	p1 := createTestPresetSchedule(t, st, pool, reqID, ScanScheduleInput{Name: "DMZ", Targets: []string{"192.0.2.0/24"}})
	p2 := createTestPresetSchedule(t, st, pool, reqID, ScanScheduleInput{Name: "사내망", Targets: []string{"198.51.100.0/24"}, PortSpec: "common"})
	if p1.ID == p2.ID {
		t.Fatalf("two presets share an id")
	}
	list, err := st.ListScanSchedules(ctx)
	if err != nil {
		t.Fatalf("list schedules: %v", err)
	}
	seen := map[string]bool{}
	for _, sc := range list {
		seen[sc.ID] = true
	}
	if !seen[p1.ID] || !seen[p2.ID] || !seen[full.ID] || !seen[reg.ID] {
		t.Errorf("list missing schedules: %v", seen)
	}

	// full·registered는 2행이 될 수 없다(부분 유니크 uq_scan_schedules_singleton_kind).
	for _, kind := range []string{ScheduleKindFull, ScheduleKindRegistered} {
		_, err := pool.Exec(
			`INSERT INTO scan_schedules (kind, name, enabled) VALUES ($1, '중복', false)`, kind)
		if !isUniqueViolation(err) {
			t.Errorf("second %s row err = %v, want unique violation", kind, err)
		}
	}
}

// TestScanScheduleRegisteredRejectsTargets는 "등록된 인증서 스캔은 대상·포트를
// 쓰지 않는다"가 store 검증과 DB CHECK 양쪽에 박혀 있는지 본다.
func TestScanScheduleRegisteredRejectsTargets(t *testing.T) {
	pool := testPool(t)
	st := New(pool)
	ctx := context.Background()
	reqID := "test-req-schedule-registered"
	t.Cleanup(func() { cleanupAuditByRequestID(pool, reqID) })

	reg, err := st.GetScanScheduleByKind(ctx, ScheduleKindRegistered)
	if err != nil {
		t.Fatalf("get registered schedule: %v", err)
	}

	targets := []string{"192.0.2.0/24"}
	if _, err := st.UpdateScanSchedule(ctx, reg.ID, ScanScheduleUpdate{
		Targets: &targets,
		Actor:   ScanScheduleActor{RequestID: &reqID},
	}); !errors.Is(err, ErrScanScheduleInput) {
		t.Errorf("registered update with targets err = %v, want ErrScanScheduleInput", err)
	}
	spec := "443"
	if _, err := st.UpdateScanSchedule(ctx, reg.ID, ScanScheduleUpdate{
		PortSpec: &spec,
		Actor:    ScanScheduleActor{RequestID: &reqID},
	}); !errors.Is(err, ErrScanScheduleInput) {
		t.Errorf("registered update with port_spec err = %v, want ErrScanScheduleInput", err)
	}

	// DB CHECK도 같은 규칙을 강제한다(거부되므로 시드 행은 그대로 남는다).
	_, err = pool.Exec(
		`UPDATE scan_schedules SET targets = ARRAY['192.0.2.0/24'] WHERE id = $1::uuid`, reg.ID)
	if state := sqlStateOf(err); state != "23514" {
		t.Errorf("direct registered targets update err = %v (sqlstate %q), want check_violation", err, state)
	}

	after, err := st.GetScanScheduleByKind(ctx, ScheduleKindRegistered)
	if err != nil {
		t.Fatalf("re-get registered schedule: %v", err)
	}
	if len(after.Targets) != 0 || after.PortSpec != nil {
		t.Errorf("registered row mutated: targets=%v port_spec=%v", after.Targets, after.PortSpec)
	}
}

// TestScanScheduleEnabledRequiresTargets는 "켜져 있는데 대상이 없는" 스케줄을 막는다.
func TestScanScheduleEnabledRequiresTargets(t *testing.T) {
	pool := testPool(t)
	st := New(pool)
	ctx := context.Background()
	reqID := "test-req-schedule-enabled-targets"
	t.Cleanup(func() { cleanupAuditByRequestID(pool, reqID) })

	if _, err := st.CreateScanSchedule(ctx, ScanScheduleInput{
		Name: "빈 대상", Enabled: true, PortSpec: "443",
		Actor: ScanScheduleActor{RequestID: &reqID},
	}); !errors.Is(err, ErrScanScheduleInput) {
		t.Errorf("enabled preset without targets err = %v, want ErrScanScheduleInput", err)
	}

	sc := createTestPresetSchedule(t, st, pool, reqID, ScanScheduleInput{Name: "대상 비우기"})
	empty := []string{}
	enabled := true
	if _, err := st.UpdateScanSchedule(ctx, sc.ID, ScanScheduleUpdate{
		Enabled: &enabled, Targets: &empty,
		Actor: ScanScheduleActor{RequestID: &reqID},
	}); !errors.Is(err, ErrScanScheduleInput) {
		t.Errorf("enable with empty targets err = %v, want ErrScanScheduleInput", err)
	}
}

// TestScanScheduleIntervalFloor는 full·preset 주기 하한(1시간)을 고정한다.
func TestScanScheduleIntervalFloor(t *testing.T) {
	pool := testPool(t)
	st := New(pool)
	ctx := context.Background()
	reqID := "test-req-schedule-interval"
	t.Cleanup(func() { cleanupAuditByRequestID(pool, reqID) })

	tooShort := MinScheduleIntervalSeconds - 1
	if _, err := st.CreateScanSchedule(ctx, ScanScheduleInput{
		Name: "짧은 주기", Targets: []string{"192.0.2.0/24"}, PortSpec: "443",
		IntervalSeconds: &tooShort,
		Actor:           ScanScheduleActor{RequestID: &reqID},
	}); !errors.Is(err, ErrScanScheduleInput) {
		t.Errorf("interval below floor err = %v, want ErrScanScheduleInput", err)
	}

	sc := createTestPresetSchedule(t, st, pool, reqID, ScanScheduleInput{Name: "주기 수정"})
	if _, err := st.UpdateScanSchedule(ctx, sc.ID, ScanScheduleUpdate{
		IntervalSeconds: &tooShort,
		Actor:           ScanScheduleActor{RequestID: &reqID},
	}); !errors.Is(err, ErrScanScheduleInput) {
		t.Errorf("update interval below floor err = %v, want ErrScanScheduleInput", err)
	}
	// 하한 자체는 통과해야 한다.
	atFloor := MinScheduleIntervalSeconds
	if _, err := st.UpdateScanSchedule(ctx, sc.ID, ScanScheduleUpdate{
		IntervalSeconds: &atFloor,
		Actor:           ScanScheduleActor{RequestID: &reqID},
	}); err != nil {
		t.Errorf("update interval at floor: %v", err)
	}
}

// TestScanSchedulePresetDeleteOnly는 preset만 지울 수 있음을 고정한다.
func TestScanSchedulePresetDeleteOnly(t *testing.T) {
	pool := testPool(t)
	st := New(pool)
	ctx := context.Background()
	reqID := "test-req-schedule-delete"
	t.Cleanup(func() { cleanupAuditByRequestID(pool, reqID) })

	full, err := st.GetScanScheduleByKind(ctx, ScheduleKindFull)
	if err != nil {
		t.Fatalf("get full schedule: %v", err)
	}
	if err := st.DeleteScanSchedule(ctx, full.ID, ScanScheduleActor{RequestID: &reqID}); !errors.Is(err, ErrScanScheduleKind) {
		t.Errorf("delete full schedule err = %v, want ErrScanScheduleKind", err)
	}

	sc := createTestPresetSchedule(t, st, pool, reqID, ScanScheduleInput{Name: "삭제 대상"})
	if err := st.DeleteScanSchedule(ctx, sc.ID, ScanScheduleActor{RequestID: &reqID}); err != nil {
		t.Fatalf("delete preset schedule: %v", err)
	}
	if _, err := st.GetScanSchedule(ctx, sc.ID); !errors.Is(err, ErrScanScheduleNotFound) {
		t.Errorf("get deleted schedule err = %v, want ErrScanScheduleNotFound", err)
	}
}

// TestScanScheduleRunActiveIsUnique는 겹침 방지의 두 겹(store 판정 + 부분 유니크)을 본다.
func TestScanScheduleRunActiveIsUnique(t *testing.T) {
	pool := testPool(t)
	st := New(pool)
	ctx := context.Background()
	reqID := "test-req-schedule-run-active"
	t.Cleanup(func() { cleanupAuditByRequestID(pool, reqID) })

	sc := createTestPresetSchedule(t, st, pool, reqID, ScanScheduleInput{Name: "중복 실행"})

	run, err := st.StartManualScanScheduleRun(ctx, sc.ID, ScanScheduleActor{RequestID: &reqID})
	if err != nil {
		t.Fatalf("start manual run: %v", err)
	}
	if run.Status != ScheduleRunRunning || run.Trigger != ScheduleTriggerManual {
		t.Errorf("run status=%q trigger=%q, want running/manual", run.Status, run.Trigger)
	}

	_, err = st.StartManualScanScheduleRun(ctx, sc.ID, ScanScheduleActor{RequestID: &reqID})
	if !errors.Is(err, ErrScanScheduleRunActive) {
		t.Fatalf("second manual run err = %v, want ErrScanScheduleRunActive", err)
	}
	var activeErr *ScanScheduleRunActiveError
	if !errors.As(err, &activeErr) || activeErr.RunID != run.ID {
		t.Errorf("second manual run err = %#v, want *ScanScheduleRunActiveError{RunID: %s}", err, run.ID)
	}

	// 부분 유니크 uq_scan_schedule_runs_active가 최종 방어다.
	_, err = pool.Exec(
		`INSERT INTO scan_schedule_runs (schedule_id, trigger) VALUES ($1::uuid, 'schedule')`, sc.ID)
	if !isUniqueViolation(err) {
		t.Errorf("second running row err = %v, want unique violation", err)
	}

	if _, err := st.FinishScanScheduleRun(ctx, run.ID, ScanScheduleRunResult{
		Status: ScheduleRunCompleted, DispatchedJobs: 2, DispatchedEndpoints: 512,
	}); err != nil {
		t.Fatalf("finish run: %v", err)
	}
	// 종료된 실행을 다시 종료하지 않는다(카운터 덧씌움 방지).
	if _, err := st.FinishScanScheduleRun(ctx, run.ID, ScanScheduleRunResult{
		Status: ScheduleRunFailed,
	}); !errors.Is(err, ErrScanScheduleRunNotFound) {
		t.Errorf("re-finish run err = %v, want ErrScanScheduleRunNotFound", err)
	}

	// 종료했으니 다음 실행은 열린다.
	next, err := st.StartManualScanScheduleRun(ctx, sc.ID, ScanScheduleActor{RequestID: &reqID})
	if err != nil {
		t.Fatalf("manual run after finish: %v", err)
	}
	if next.ID == run.ID {
		t.Errorf("second run reused id %s", run.ID)
	}

	runs, err := st.ListScanScheduleRuns(ctx, ListScanScheduleRunsInput{ScheduleID: sc.ID, PageSize: 50})
	if err != nil {
		t.Fatalf("list runs: %v", err)
	}
	if runs.Total != 2 {
		t.Errorf("run history total = %d, want 2", runs.Total)
	}
}

// TestClaimDueScanSchedulesComputesNextRun은 클레임의 두 규약을 고정한다.
// ① next_run_at = **실행이 시작한 시각** + 주기 ② 실행 행이 같은 문장에서 열린다.
func TestClaimDueScanSchedulesComputesNextRun(t *testing.T) {
	pool := testPool(t)
	st := New(pool)
	ctx := context.Background()
	reqID := "test-req-schedule-claim"
	t.Cleanup(func() { cleanupAuditByRequestID(pool, reqID) })

	interval := MinScheduleIntervalSeconds
	sc := createTestPresetSchedule(t, st, pool, reqID, ScanScheduleInput{
		Name: "클레임 대상", Enabled: true, IntervalSeconds: &interval,
	})
	setNextRunAt(t, pool, sc.ID, -time.Minute)

	claimed, locked, err := st.ClaimDueScanSchedules(ctx, time.Hour, 20)
	if err != nil {
		t.Fatalf("claim due schedules: %v", err)
	}
	if !locked {
		t.Fatalf("claim reported lock contention in a single-process test")
	}
	var got *ClaimedScanSchedule
	for i := range claimed {
		if claimed[i].ScheduleID == sc.ID {
			got = &claimed[i]
		}
	}
	if got == nil {
		t.Fatalf("due schedule %s was not claimed (claimed=%d)", sc.ID, len(claimed))
	}
	if got.Kind != ScheduleKindPreset || got.PortSpec != "443,8443" || len(got.Targets) != 1 {
		t.Errorf("claimed = %+v, want preset with its targets/port_spec", *got)
	}
	if got.IntervalSeconds != interval {
		t.Errorf("claimed interval = %d, want %d", got.IntervalSeconds, interval)
	}

	after, err := st.GetScanSchedule(ctx, sc.ID)
	if err != nil {
		t.Fatalf("get schedule after claim: %v", err)
	}
	if after.NextRunAt == nil || after.LastRunStartedAt == nil || after.LastRunID == nil {
		t.Fatalf("after claim next=%v last_started=%v last_run=%v, want all set",
			after.NextRunAt, after.LastRunStartedAt, after.LastRunID)
	}
	if *after.LastRunID != got.RunID {
		t.Errorf("last_run_id = %s, want claimed run %s", *after.LastRunID, got.RunID)
	}
	want := got.StartedAt.Add(time.Duration(interval) * time.Second)
	if diff := after.NextRunAt.Sub(want); diff > time.Second || diff < -time.Second {
		t.Errorf("next_run_at = %v, want start+interval %v", after.NextRunAt, want)
	}

	run, err := st.ListScanScheduleRuns(ctx, ListScanScheduleRunsInput{ScheduleID: sc.ID, PageSize: 10})
	if err != nil {
		t.Fatalf("list runs: %v", err)
	}
	if run.Total != 1 || run.Items[0].Trigger != ScheduleTriggerSchedule || run.Items[0].Status != ScheduleRunRunning {
		t.Errorf("claim run history = %+v, want one running/schedule row", run.Items)
	}
}

// TestClaimDueScanSchedulesSkipsCycleWhileRunning은 "주기를 넘기는 실행"의 규약이다.
// 활성 실행이 있으면 그 주기는 건너뛰고 next_run_at도 밀지 않는다 —
// 끝나는 즉시 다음 tick이 집어간다(겹침 0).
func TestClaimDueScanSchedulesSkipsCycleWhileRunning(t *testing.T) {
	pool := testPool(t)
	st := New(pool)
	ctx := context.Background()
	reqID := "test-req-schedule-skip"
	t.Cleanup(func() { cleanupAuditByRequestID(pool, reqID) })

	interval := MinScheduleIntervalSeconds
	sc := createTestPresetSchedule(t, st, pool, reqID, ScanScheduleInput{
		Name: "겹침 방지", Enabled: true, IntervalSeconds: &interval,
	})
	setNextRunAt(t, pool, sc.ID, -time.Minute)

	claimed, _, err := st.ClaimDueScanSchedules(ctx, time.Hour, 20)
	if err != nil {
		t.Fatalf("first claim: %v", err)
	}
	runID := ""
	for _, c := range claimed {
		if c.ScheduleID == sc.ID {
			runID = c.RunID
		}
	}
	if runID == "" {
		t.Fatalf("schedule was not claimed")
	}

	// 앞 실행이 아직 도는 상태에서 다음 주기가 도래했다.
	setNextRunAt(t, pool, sc.ID, -time.Minute)
	before, err := st.GetScanSchedule(ctx, sc.ID)
	if err != nil {
		t.Fatalf("get schedule: %v", err)
	}
	again, _, err := st.ClaimDueScanSchedules(ctx, time.Hour, 20)
	if err != nil {
		t.Fatalf("second claim: %v", err)
	}
	for _, c := range again {
		if c.ScheduleID == sc.ID {
			t.Fatalf("schedule claimed twice while a run is active (run %s)", c.RunID)
		}
	}
	after, err := st.GetScanSchedule(ctx, sc.ID)
	if err != nil {
		t.Fatalf("get schedule after skipped claim: %v", err)
	}
	if !after.NextRunAt.Equal(*before.NextRunAt) {
		t.Errorf("next_run_at moved on a skipped cycle: %v → %v", before.NextRunAt, after.NextRunAt)
	}

	// 실행이 끝나면 즉시 다음 실행이 잡힌다.
	if _, err := st.FinishScanScheduleRun(ctx, runID, ScanScheduleRunResult{Status: ScheduleRunCompleted}); err != nil {
		t.Fatalf("finish run: %v", err)
	}
	third, _, err := st.ClaimDueScanSchedules(ctx, time.Hour, 20)
	if err != nil {
		t.Fatalf("third claim: %v", err)
	}
	found := false
	for _, c := range third {
		if c.ScheduleID == sc.ID {
			found = true
		}
	}
	if !found {
		t.Errorf("schedule was not claimed after its run finished")
	}
}

// TestStartManualScanScheduleRunKeepsNextRunAt은 사용자 확정 규약이다 —
// '지금 실행'은 예정된 다음 실행 시각을 리셋하지 않는다.
func TestStartManualScanScheduleRunKeepsNextRunAt(t *testing.T) {
	pool := testPool(t)
	st := New(pool)
	ctx := context.Background()
	reqID := "test-req-schedule-manual"
	t.Cleanup(func() { cleanupAuditByRequestID(pool, reqID) })

	interval := MinScheduleIntervalSeconds
	sc := createTestPresetSchedule(t, st, pool, reqID, ScanScheduleInput{
		Name: "수동 실행", Enabled: true, IntervalSeconds: &interval,
	})
	before, err := st.GetScanSchedule(ctx, sc.ID)
	if err != nil {
		t.Fatalf("get schedule: %v", err)
	}
	if before.NextRunAt == nil {
		t.Fatalf("enabled schedule has no next_run_at")
	}

	run, err := st.StartManualScanScheduleRun(ctx, sc.ID, ScanScheduleActor{RequestID: &reqID})
	if err != nil {
		t.Fatalf("start manual run: %v", err)
	}
	after, err := st.GetScanSchedule(ctx, sc.ID)
	if err != nil {
		t.Fatalf("get schedule after manual run: %v", err)
	}
	if !after.NextRunAt.Equal(*before.NextRunAt) {
		t.Errorf("manual run moved next_run_at: %v → %v", before.NextRunAt, after.NextRunAt)
	}
	if after.LastRunID == nil || *after.LastRunID != run.ID {
		t.Errorf("last_run_id = %v, want manual run %s", after.LastRunID, run.ID)
	}

	// 수동 실행이 도는 동안 주기 클레임도 걸리지 않는다.
	setNextRunAt(t, pool, sc.ID, -time.Minute)
	claimed, _, err := st.ClaimDueScanSchedules(ctx, time.Hour, 20)
	if err != nil {
		t.Fatalf("claim during manual run: %v", err)
	}
	for _, c := range claimed {
		if c.ScheduleID == sc.ID {
			t.Errorf("schedule claimed while a manual run is active")
		}
	}
}

// TestClaimDueScanSchedulesIgnoresRegistered는 registered가 클레임 대상이 아님을
// 고정한다 — 그 종류는 기존 재스캔 엔진(endpoint_monitors)이 돌린다.
func TestClaimDueScanSchedulesIgnoresRegistered(t *testing.T) {
	pool := testPool(t)
	st := New(pool)
	ctx := context.Background()

	reg, err := st.GetScanScheduleByKind(ctx, ScheduleKindRegistered)
	if err != nil {
		t.Fatalf("get registered schedule: %v", err)
	}
	setNextRunAt(t, pool, reg.ID, -time.Minute)
	t.Cleanup(func() {
		_, _ = pool.Exec(`UPDATE scan_schedules SET next_run_at = NULL WHERE id = $1::uuid`, reg.ID)
	})

	claimed, _, err := st.ClaimDueScanSchedules(ctx, time.Hour, 20)
	if err != nil {
		t.Fatalf("claim due schedules: %v", err)
	}
	for _, c := range claimed {
		if c.ScheduleID == reg.ID {
			t.Fatalf("registered schedule was claimed by the sweep tick")
		}
	}
}

// TestUpdateScanScheduleReschedulesOnEnable은 켜기/끄기의 next_run_at 규약이다.
func TestUpdateScanScheduleReschedulesOnEnable(t *testing.T) {
	pool := testPool(t)
	st := New(pool)
	ctx := context.Background()
	reqID := "test-req-schedule-toggle"
	t.Cleanup(func() { cleanupAuditByRequestID(pool, reqID) })

	interval := MinScheduleIntervalSeconds
	sc := createTestPresetSchedule(t, st, pool, reqID, ScanScheduleInput{
		Name: "켜고 끄기", IntervalSeconds: &interval,
	})
	if sc.NextRunAt != nil {
		t.Errorf("disabled schedule has next_run_at %v, want NULL", sc.NextRunAt)
	}

	on := true
	enabled, err := st.UpdateScanSchedule(ctx, sc.ID, ScanScheduleUpdate{
		Enabled: &on, Actor: ScanScheduleActor{RequestID: &reqID},
	})
	if err != nil {
		t.Fatalf("enable schedule: %v", err)
	}
	if enabled.NextRunAt == nil {
		t.Fatalf("enabled schedule has no next_run_at")
	}
	// 이름만 바꾸는 수정은 예정 시각을 밀지 않는다.
	name := "이름만 변경"
	renamed, err := st.UpdateScanSchedule(ctx, sc.ID, ScanScheduleUpdate{
		Name: &name, Actor: ScanScheduleActor{RequestID: &reqID},
	})
	if err != nil {
		t.Fatalf("rename schedule: %v", err)
	}
	if !renamed.NextRunAt.Equal(*enabled.NextRunAt) {
		t.Errorf("rename moved next_run_at: %v → %v", enabled.NextRunAt, renamed.NextRunAt)
	}

	off := false
	disabled, err := st.UpdateScanSchedule(ctx, sc.ID, ScanScheduleUpdate{
		Enabled: &off, Actor: ScanScheduleActor{RequestID: &reqID},
	})
	if err != nil {
		t.Fatalf("disable schedule: %v", err)
	}
	if disabled.NextRunAt != nil {
		t.Errorf("disabled schedule kept next_run_at %v", disabled.NextRunAt)
	}
}
