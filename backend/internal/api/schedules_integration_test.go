package api

// 정기 스캔 스케줄 API 통합 테스트 (0019).
// TEST_DATABASE_URL 미설정 시 스킵 (전용 테스트 DB — 메인 DB에 돌리지 않는다).
//
// 검증 축 5개: ① 스케줄러 미배선에서도 조회·설정 변경이 살아 있고 실행만 503인가
// ② 권한 게이트가 스캔 발주와 같은 3역할인가 ③ **포트 문법 검증자가 실제로
// 주입되었는가**(미주입이면 "비었는지"까지만 보므로 깨진 사양이 통과한다)
// ④ 설정 변경이 discovery.schedule 감사 이벤트를 남기는가
// ⑤ 생성·삭제가 **preset 전용**인가(full·registered는 409, preset은 N행 등록 가능).

import (
	"context"
	"encoding/json"
	"net/http"
	"sync"
	"testing"

	"github.com/Falke060918/INHERIT_AutoCert/backend/internal/auth"
	"github.com/Falke060918/INHERIT_AutoCert/backend/internal/domain"
	"github.com/Falke060918/INHERIT_AutoCert/backend/internal/store"
)

// fakeSchedules는 배선된 스케줄러를 흉내낸다. 실제 발주는 하지 않는다.
type fakeSchedules struct {
	mu     sync.Mutex
	run    *store.ScanScheduleRun
	err    error
	actors []store.ScanScheduleActor
}

func (f *fakeSchedules) RunNow(_ context.Context, _ string, act store.ScanScheduleActor) (*store.ScanScheduleRun, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.actors = append(f.actors, act)
	return f.run, f.err
}

func (f *fakeSchedules) CancelRun(_ context.Context, _ string, act store.ScanScheduleActor) (*store.ScanScheduleRun, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.actors = append(f.actors, act)
	return f.run, f.err
}

// scheduleListBody는 목록 응답의 검증용 형태다(전체 스키마를 다시 적지 않는다).
type scheduleListBody struct {
	RequestID string `json:"request_id"`
	Wired     bool   `json:"wired"`
	Items     []struct {
		ID        string                 `json:"id"`
		Kind      string                 `json:"kind"`
		Enabled   bool                   `json:"enabled"`
		PortSpec  *string                `json:"port_spec"`
		Targets   []string               `json:"targets"`
		ActiveRun *store.ScanScheduleRun `json:"active_run"`
		LastRun   *store.ScanScheduleRun `json:"last_run"`
	} `json:"items"`
}

// newScheduleOperator는 스캔 발주 권한을 가진 계정을 만들고 토큰을 반환한다.
func newScheduleOperator(t *testing.T, env *testEnv, username string) string {
	t.Helper()
	pw := randomPassword(t)
	if _, err := env.svc.CreateUser(context.Background(), username, pw, nil,
		domain.RoleOperator, nil, auth.AuditMeta{}); err != nil {
		t.Fatalf("create operator: %v", err)
	}
	token, status := env.login(t, username, pw)
	if status != http.StatusOK {
		t.Fatalf("login = %d, want 200", status)
	}
	return token
}

// listSchedules는 목록을 읽는다.
func listSchedules(t *testing.T, env *testEnv, token string) scheduleListBody {
	t.Helper()
	resp := env.do(t, http.MethodGet, "/api/v1/discovery/schedules", token, nil)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("list schedules = %d, want 200", resp.StatusCode)
	}
	var out scheduleListBody
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		t.Fatalf("decode: %v", err)
	}
	return out
}

func TestScanScheduleAPIWithoutWiring(t *testing.T) {
	env := newTestEnv(t)
	ctx := context.Background()

	pw := randomPassword(t)
	if _, err := env.svc.CreateUser(ctx, "schedule-viewer", pw, nil, domain.RoleViewer, nil, auth.AuditMeta{}); err != nil {
		t.Fatalf("create viewer: %v", err)
	}
	viewerToken, status := env.login(t, "schedule-viewer", pw)
	if status != http.StatusOK {
		t.Fatalf("login = %d, want 200", status)
	}
	operatorToken := newScheduleOperator(t, env, "schedule-operator-unwired")

	// 설정 원복: 스케줄 행은 시드된 공유 자원이라 테스트가 남긴 변경을 되돌린다.
	var seedSpec *string
	if err := env.pool.QueryRow(
		`SELECT port_spec FROM scan_schedules WHERE kind = 'full'`).Scan(&seedSpec); err != nil {
		t.Fatalf("read full schedule: %v", err)
	}
	t.Cleanup(func() {
		_, _ = env.pool.Exec(
			`UPDATE scan_schedules SET port_spec = $1, enabled = false, targets = '{}' WHERE kind = 'full'`,
			seedSpec)
	})

	t.Run("unauthenticated list returns 401", func(t *testing.T) {
		if resp := env.do(t, http.MethodGet, "/api/v1/discovery/schedules", "", nil); resp.StatusCode != http.StatusUnauthorized {
			t.Errorf("list = %d, want 401", resp.StatusCode)
		}
	})

	t.Run("viewer reads seeded schedules and sees not-wired", func(t *testing.T) {
		out := listSchedules(t, env, viewerToken)
		if out.Wired {
			t.Error("wired = true, want false (컨트롤러 미주입)")
		}
		if out.RequestID == "" {
			t.Error("request_id 비어 있음")
		}
		kinds := map[string]bool{}
		for _, it := range out.Items {
			kinds[it.Kind] = true
			if it.Kind == store.ScheduleKindFull && it.Enabled {
				t.Error("full 시드가 켜져 있다 — 신규 설치가 저절로 전 대역을 훑으면 안 된다")
			}
			if it.Kind == store.ScheduleKindRegistered && (len(it.Targets) != 0 || it.PortSpec != nil) {
				t.Errorf("registered 시드에 대상·포트가 있다: %v/%v", it.Targets, it.PortSpec)
			}
		}
		if !kinds[store.ScheduleKindFull] || !kinds[store.ScheduleKindRegistered] {
			t.Errorf("시드 2종이 보이지 않는다: %v", kinds)
		}
	})

	t.Run("viewer cannot change settings or run", func(t *testing.T) {
		resp := env.do(t, http.MethodPatch, "/api/v1/discovery/schedules", viewerToken,
			map[string]any{"kind": "full", "enabled": false})
		if resp.StatusCode != http.StatusForbidden {
			t.Errorf("viewer patch = %d, want 403 (스캔 발주와 같은 3역할)", resp.StatusCode)
		}
		out := listSchedules(t, env, viewerToken)
		resp = env.do(t, http.MethodPost,
			"/api/v1/discovery/schedules/"+out.Items[0].ID+"/run", viewerToken, nil)
		if resp.StatusCode != http.StatusForbidden {
			t.Errorf("viewer run = %d, want 403", resp.StatusCode)
		}
	})

	t.Run("broken port spec is rejected — validator is injected", func(t *testing.T) {
		// 빈 토큰이 섞인 사양이다. 검증자를 주입하지 않으면 "비어 있지 않다"만 보고
		// 통과해, 요청한 것보다 좁게 스캔되는 사양이 그대로 저장된다.
		resp := env.do(t, http.MethodPatch, "/api/v1/discovery/schedules", operatorToken,
			map[string]any{"kind": "full", "port_spec": "443,,8443"})
		if resp.StatusCode != http.StatusBadRequest {
			t.Fatalf("patch = %d, want 400 (포트 사양 파서가 걸러야 한다)", resp.StatusCode)
		}
		if code := errorCode(t, resp); code != "invalid_schedule" {
			t.Errorf("code = %q, want invalid_schedule", code)
		}
	})

	t.Run("enabling without targets is rejected", func(t *testing.T) {
		resp := env.do(t, http.MethodPatch, "/api/v1/discovery/schedules", operatorToken,
			map[string]any{"kind": "full", "enabled": true})
		if resp.StatusCode != http.StatusBadRequest {
			t.Errorf("patch = %d, want 400 (대상 없이 켤 수 없다)", resp.StatusCode)
		}
	})

	t.Run("registered rejects targets", func(t *testing.T) {
		resp := env.do(t, http.MethodPatch, "/api/v1/discovery/schedules", operatorToken,
			map[string]any{"kind": "registered", "targets": []string{"198.51.100.0/24"}})
		if resp.StatusCode != http.StatusBadRequest {
			t.Errorf("patch = %d, want 400 (등록된 인증서 스캔은 대상을 갖지 않는다)", resp.StatusCode)
		}
	})

	t.Run("unknown fields and ambiguous targets are rejected", func(t *testing.T) {
		resp := env.do(t, http.MethodPatch, "/api/v1/discovery/schedules", operatorToken,
			map[string]any{"kind": "full", "portspec": "web"})
		if resp.StatusCode != http.StatusBadRequest {
			t.Errorf("unknown field patch = %d, want 400", resp.StatusCode)
		}
		resp = env.do(t, http.MethodPatch, "/api/v1/discovery/schedules", operatorToken,
			map[string]any{"enabled": false})
		if resp.StatusCode != http.StatusBadRequest {
			t.Errorf("target-less patch = %d, want 400 (id·kind 중 하나는 있어야 한다)", resp.StatusCode)
		}
	})

	t.Run("valid patch persists and is audited", func(t *testing.T) {
		before := env.auditCount(t, store.EventDiscoverySchedule, domain.AuditSuccess)
		resp := env.do(t, http.MethodPatch, "/api/v1/discovery/schedules", operatorToken,
			map[string]any{"kind": "full", "port_spec": "web", "targets": []string{"198.51.100.0/24"}})
		if resp.StatusCode != http.StatusOK {
			t.Fatalf("patch = %d, want 200", resp.StatusCode)
		}
		var updated store.ScanSchedule
		if err := json.NewDecoder(resp.Body).Decode(&updated); err != nil {
			t.Fatalf("decode: %v", err)
		}
		if updated.PortSpec == nil || *updated.PortSpec != "web" {
			t.Errorf("port_spec = %v, want web", updated.PortSpec)
		}
		if after := env.auditCount(t, store.EventDiscoverySchedule, domain.AuditSuccess); after != before+1 {
			t.Errorf("감사 이벤트 %d → %d, want +1", before, after)
		}
	})

	t.Run("run without wiring returns 503", func(t *testing.T) {
		out := listSchedules(t, env, operatorToken)
		resp := env.do(t, http.MethodPost,
			"/api/v1/discovery/schedules/"+out.Items[0].ID+"/run", operatorToken, nil)
		if resp.StatusCode != http.StatusServiceUnavailable {
			t.Fatalf("run = %d, want 503", resp.StatusCode)
		}
		if code := errorCode(t, resp); code != "schedule_unavailable" {
			t.Errorf("code = %q, want schedule_unavailable", code)
		}
	})
}

// TestScanSchedulePresetLifecycle은 생성·삭제 라우트를 고정한다.
// **preset 전용**이라는 규칙이 API 표면에서 깨지면 시드 1행 정본이 무너진다 —
// full을 하나 더 만들거나 registered를 지우면 스케줄러가 어느 행을 볼지 알 수 없다.
func TestScanSchedulePresetLifecycle(t *testing.T) {
	env := newTestEnv(t)
	ctx := context.Background()

	operatorToken := newScheduleOperator(t, env, "schedule-operator-preset")
	pw := randomPassword(t)
	if _, err := env.svc.CreateUser(ctx, "schedule-viewer-preset", pw, nil,
		domain.RoleViewer, nil, auth.AuditMeta{}); err != nil {
		t.Fatalf("create viewer: %v", err)
	}
	viewerToken, status := env.login(t, "schedule-viewer-preset", pw)
	if status != http.StatusOK {
		t.Fatalf("login = %d, want 200", status)
	}

	// 테스트가 만든 preset은 시드 행과 달리 남으면 다른 테스트의 목록에 섞인다.
	t.Cleanup(func() {
		_, _ = env.pool.Exec(
			`DELETE FROM scan_schedules WHERE kind = 'preset' AND name LIKE 'preset-test-%'`)
	})

	body := func(name string, extra map[string]any) map[string]any {
		req := map[string]any{
			"name": name, "enabled": true,
			"targets": []string{"198.51.100.0/24"}, "port_spec": "web",
		}
		for k, v := range extra {
			req[k] = v
		}
		return req
	}

	t.Run("viewer cannot create", func(t *testing.T) {
		resp := env.do(t, http.MethodPost, "/api/v1/discovery/schedules", viewerToken,
			body("preset-test-forbidden", nil))
		if resp.StatusCode != http.StatusForbidden {
			t.Errorf("viewer create = %d, want 403 (스캔 발주와 같은 3역할)", resp.StatusCode)
		}
	})

	t.Run("only preset can be created", func(t *testing.T) {
		resp := env.do(t, http.MethodPost, "/api/v1/discovery/schedules", operatorToken,
			body("preset-test-kind", map[string]any{"kind": "full"}))
		if resp.StatusCode != http.StatusBadRequest {
			t.Fatalf("create(kind=full) = %d, want 400", resp.StatusCode)
		}
		if code := errorCode(t, resp); code != "invalid_request" {
			t.Errorf("code = %q, want invalid_request", code)
		}
	})

	t.Run("broken port spec is rejected — validator is injected", func(t *testing.T) {
		resp := env.do(t, http.MethodPost, "/api/v1/discovery/schedules", operatorToken,
			body("preset-test-badspec", map[string]any{"port_spec": "443,,8443"}))
		if resp.StatusCode != http.StatusBadRequest {
			t.Fatalf("create = %d, want 400 (포트 사양 파서가 걸러야 한다)", resp.StatusCode)
		}
		if code := errorCode(t, resp); code != "invalid_schedule" {
			t.Errorf("code = %q, want invalid_schedule", code)
		}
	})

	t.Run("unknown field is rejected", func(t *testing.T) {
		resp := env.do(t, http.MethodPost, "/api/v1/discovery/schedules", operatorToken,
			body("preset-test-unknown", map[string]any{"portspec": "web"}))
		if resp.StatusCode != http.StatusBadRequest {
			t.Errorf("create = %d, want 400", resp.StatusCode)
		}
	})

	t.Run("enabling without targets is rejected", func(t *testing.T) {
		resp := env.do(t, http.MethodPost, "/api/v1/discovery/schedules", operatorToken,
			body("preset-test-notargets", map[string]any{"targets": []string{}}))
		if resp.StatusCode != http.StatusBadRequest {
			t.Errorf("create = %d, want 400 (대상 없이 켤 수 없다)", resp.StatusCode)
		}
	})

	t.Run("interval below one hour is rejected", func(t *testing.T) {
		resp := env.do(t, http.MethodPost, "/api/v1/discovery/schedules", operatorToken,
			body("preset-test-fast", map[string]any{"interval_seconds": 60}))
		if resp.StatusCode != http.StatusBadRequest {
			t.Errorf("create = %d, want 400 (주기 하한 1시간)", resp.StatusCode)
		}
	})

	var created store.ScanSchedule
	t.Run("create persists as preset and is audited", func(t *testing.T) {
		before := env.auditCount(t, store.EventDiscoverySchedule, domain.AuditSuccess)
		resp := env.do(t, http.MethodPost, "/api/v1/discovery/schedules", operatorToken,
			body("preset-test-a", map[string]any{"interval_seconds": 7200}))
		if resp.StatusCode != http.StatusCreated {
			t.Fatalf("create = %d, want 201", resp.StatusCode)
		}
		if err := json.NewDecoder(resp.Body).Decode(&created); err != nil {
			t.Fatalf("decode: %v", err)
		}
		if created.Kind != store.ScheduleKindPreset {
			t.Errorf("kind = %q, want preset", created.Kind)
		}
		// 켜서 만들어도 즉시 돌지 않는다 — 예정만 잡힌다.
		if created.NextRunAt == nil {
			t.Error("next_run_at이 비었다 — 켜서 만들면 다음 실행이 잡혀야 한다")
		}
		if after := env.auditCount(t, store.EventDiscoverySchedule, domain.AuditSuccess); after != before+1 {
			t.Errorf("감사 이벤트 %d → %d, want +1", before, after)
		}
	})

	t.Run("preset can be registered more than once", func(t *testing.T) {
		resp := env.do(t, http.MethodPost, "/api/v1/discovery/schedules", operatorToken,
			body("preset-test-b", nil))
		if resp.StatusCode != http.StatusCreated {
			t.Fatalf("second create = %d, want 201 (preset은 N행)", resp.StatusCode)
		}
		presets := 0
		for _, it := range listSchedules(t, env, operatorToken).Items {
			if it.Kind == store.ScheduleKindPreset {
				presets++
			}
		}
		if presets < 2 {
			t.Errorf("preset %d건, want 2건 이상", presets)
		}
	})

	t.Run("seeded kinds cannot be deleted", func(t *testing.T) {
		var fullID string
		if err := env.pool.QueryRow(
			`SELECT id::text FROM scan_schedules WHERE kind = 'full'`).Scan(&fullID); err != nil {
			t.Fatalf("read full schedule: %v", err)
		}
		resp := env.do(t, http.MethodDelete, "/api/v1/discovery/schedules/"+fullID, operatorToken, nil)
		if resp.StatusCode != http.StatusConflict {
			t.Fatalf("delete(full) = %d, want 409", resp.StatusCode)
		}
		if code := errorCode(t, resp); code != "schedule_kind" {
			t.Errorf("code = %q, want schedule_kind", code)
		}
	})

	t.Run("unknown ids are 404", func(t *testing.T) {
		resp := env.do(t, http.MethodDelete,
			"/api/v1/discovery/schedules/3f2504e0-4f89-11d3-9a0c-0305e82c3399", operatorToken, nil)
		if resp.StatusCode != http.StatusNotFound {
			t.Errorf("delete(unknown) = %d, want 404", resp.StatusCode)
		}
		resp = env.do(t, http.MethodDelete, "/api/v1/discovery/schedules/not-a-uuid", operatorToken, nil)
		if resp.StatusCode != http.StatusNotFound {
			t.Errorf("delete(bad id) = %d, want 404", resp.StatusCode)
		}
	})

	t.Run("viewer cannot delete", func(t *testing.T) {
		resp := env.do(t, http.MethodDelete, "/api/v1/discovery/schedules/"+created.ID, viewerToken, nil)
		if resp.StatusCode != http.StatusForbidden {
			t.Errorf("viewer delete = %d, want 403", resp.StatusCode)
		}
	})

	t.Run("delete removes the row and is audited", func(t *testing.T) {
		before := env.auditCount(t, store.EventDiscoverySchedule, domain.AuditSuccess)
		resp := env.do(t, http.MethodDelete, "/api/v1/discovery/schedules/"+created.ID, operatorToken, nil)
		if resp.StatusCode != http.StatusNoContent {
			t.Fatalf("delete = %d, want 204", resp.StatusCode)
		}
		if after := env.auditCount(t, store.EventDiscoverySchedule, domain.AuditSuccess); after != before+1 {
			t.Errorf("감사 이벤트 %d → %d, want +1", before, after)
		}
		for _, it := range listSchedules(t, env, operatorToken).Items {
			if it.ID == created.ID {
				t.Fatal("삭제된 스케줄이 목록에 남아 있다")
			}
		}
	})
}

func TestScanScheduleRunWithWiring(t *testing.T) {
	fake := &fakeSchedules{run: &store.ScanScheduleRun{
		ID:         "3f2504e0-4f89-11d3-9a0c-0305e82c3301",
		ScheduleID: "3f2504e0-4f89-11d3-9a0c-0305e82c3302",
		Trigger:    store.ScheduleTriggerManual,
		Status:     store.ScheduleRunRunning,
	}}
	env := newTestEnv(t, WithScheduleController(fake))
	token := newScheduleOperator(t, env, "schedule-operator-wired")

	t.Run("list reports wired", func(t *testing.T) {
		if out := listSchedules(t, env, token); !out.Wired {
			t.Error("wired = false, want true")
		}
	})

	t.Run("run returns 202 and carries the actor", func(t *testing.T) {
		out := listSchedules(t, env, token)
		resp := env.do(t, http.MethodPost,
			"/api/v1/discovery/schedules/"+out.Items[0].ID+"/run", token, nil)
		if resp.StatusCode != http.StatusAccepted {
			t.Fatalf("run = %d, want 202", resp.StatusCode)
		}
		var run store.ScanScheduleRun
		if err := json.NewDecoder(resp.Body).Decode(&run); err != nil {
			t.Fatalf("decode: %v", err)
		}
		if run.Trigger != store.ScheduleTriggerManual {
			t.Errorf("trigger = %q, want manual", run.Trigger)
		}
		fake.mu.Lock()
		defer fake.mu.Unlock()
		if len(fake.actors) != 1 {
			t.Fatalf("RunNow 호출 %d회, want 1", len(fake.actors))
		}
		// 감사는 store가 남기지만 "누가 눌렀는지"는 API가 넘겨야 한다.
		if fake.actors[0].ActorID == nil || *fake.actors[0].ActorID == "" {
			t.Error("actor_id가 스케줄러로 전달되지 않았다")
		}
		if fake.actors[0].RequestID == nil {
			t.Error("request_id가 스케줄러로 전달되지 않았다")
		}
	})

	t.Run("already running returns 409 with the active run id", func(t *testing.T) {
		fake.mu.Lock()
		fake.err = &store.ScanScheduleRunActiveError{
			RunID: "3f2504e0-4f89-11d3-9a0c-0305e82c3303", Trigger: store.ScheduleTriggerSchedule}
		fake.mu.Unlock()
		defer func() {
			fake.mu.Lock()
			fake.err = nil
			fake.mu.Unlock()
		}()

		out := listSchedules(t, env, token)
		resp := env.do(t, http.MethodPost,
			"/api/v1/discovery/schedules/"+out.Items[0].ID+"/run", token, nil)
		if resp.StatusCode != http.StatusConflict {
			t.Fatalf("run = %d, want 409", resp.StatusCode)
		}
		var body struct {
			Error struct {
				Code string `json:"code"`
			} `json:"error"`
			RunID   string `json:"run_id"`
			Trigger string `json:"trigger"`
		}
		if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
			t.Fatalf("decode: %v", err)
		}
		if body.Error.Code != "schedule_run_active" || body.RunID == "" {
			t.Errorf("409 응답 = %+v, want schedule_run_active + run_id", body)
		}
	})

	t.Run("cancel returns 200 and rejects a bad id", func(t *testing.T) {
		resp := env.do(t, http.MethodPost,
			"/api/v1/discovery/schedules/runs/"+fake.run.ID+"/cancel", token, nil)
		if resp.StatusCode != http.StatusOK {
			t.Fatalf("cancel = %d, want 200", resp.StatusCode)
		}
		resp = env.do(t, http.MethodPost,
			"/api/v1/discovery/schedules/runs/not-a-uuid/cancel", token, nil)
		if resp.StatusCode != http.StatusNotFound {
			t.Errorf("cancel(bad id) = %d, want 404", resp.StatusCode)
		}
	})

	t.Run("cancel of a finished run returns 409", func(t *testing.T) {
		fake.mu.Lock()
		fake.err = store.ErrScanScheduleRunState
		fake.mu.Unlock()
		defer func() {
			fake.mu.Lock()
			fake.err = nil
			fake.mu.Unlock()
		}()
		resp := env.do(t, http.MethodPost,
			"/api/v1/discovery/schedules/runs/"+fake.run.ID+"/cancel", token, nil)
		if resp.StatusCode != http.StatusConflict {
			t.Fatalf("cancel = %d, want 409", resp.StatusCode)
		}
		if code := errorCode(t, resp); code != "schedule_run_state" {
			t.Errorf("code = %q, want schedule_run_state", code)
		}
	})
}
