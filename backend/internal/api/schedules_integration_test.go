package api

// 정기 스캔 스케줄 API 통합 테스트 (0019).
// TEST_DATABASE_URL 미설정 시 스킵 (전용 테스트 DB — 메인 DB에 돌리지 않는다).
//
// 검증 축 4개: ① 스케줄러 미배선에서도 조회·설정 변경이 살아 있고 실행만 503인가
// ② 권한 게이트가 스캔 발주와 같은 3역할인가 ③ **포트 문법 검증자가 실제로
// 주입되었는가**(미주입이면 "비었는지"까지만 보므로 깨진 사양이 통과한다)
// ④ 설정 변경이 discovery.schedule 감사 이벤트를 남기는가.

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
