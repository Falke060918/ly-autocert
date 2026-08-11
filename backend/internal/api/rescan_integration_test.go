package api

// 주기 재스캔 API 통합 테스트 (06-rescan-and-replacement 11.2).
// TEST_DATABASE_URL 미설정 시 스킵 (전용 테스트 DB — 메인 DB에 돌리지 않는다).
//
// 검증 축 3개: ① 스케줄러 미배선에서도 코어가 정상이고 진단이 명확한가
// ② 권한 게이트가 스캔 발주와 같은 3역할인가 ③ exclude_status가 기본 동작을
// 바꾸지 않는가(내보내기 공유 입력).

import (
	"context"
	"encoding/json"
	"net/http"
	"sync"
	"testing"
	"time"

	"github.com/Falke060918/INHERIT_AutoCert/backend/internal/auth"
	"github.com/Falke060918/INHERIT_AutoCert/backend/internal/domain"
)

// fakeRescan은 배선된 스케줄러를 흉내낸다. 실제 발주는 하지 않는다.
type fakeRescan struct {
	mu       sync.Mutex
	enabled  bool
	interval time.Duration
	result   RescanRunResult
	err      error
	calls    []RescanRunActor
}

func (f *fakeRescan) Settings() RescanSettings {
	f.mu.Lock()
	defer f.mu.Unlock()
	return RescanSettings{Enabled: f.enabled, Interval: f.interval}
}

func (f *fakeRescan) RunOnce(_ context.Context, actor RescanRunActor) (RescanRunResult, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls = append(f.calls, actor)
	return f.result, f.err
}

type rescanStatusBody struct {
	RequestID       string  `json:"request_id"`
	Enabled         bool    `json:"enabled"`
	Wired           bool    `json:"wired"`
	IntervalSeconds int     `json:"interval_seconds"`
	Reason          string  `json:"reason"`
	Monitored       int     `json:"monitored_endpoints"`
	Retired         int     `json:"retired_endpoints"`
	Due             int     `json:"due_endpoints"`
	LastRunAt       *string `json:"last_run_at"`
}

func TestRescanStatusWithoutWiring(t *testing.T) {
	env := newTestEnv(t)
	ctx := context.Background()

	pw := randomPassword(t)
	if _, err := env.svc.CreateUser(ctx, "rescan-viewer", pw, nil, domain.RoleViewer, nil, auth.AuditMeta{}); err != nil {
		t.Fatalf("create viewer: %v", err)
	}
	token, status := env.login(t, "rescan-viewer", pw)
	if status != http.StatusOK {
		t.Fatalf("login = %d, want 200", status)
	}

	t.Run("status without token returns 401", func(t *testing.T) {
		if resp := env.do(t, http.MethodGet, "/api/v1/discovery/rescan/status", "", nil); resp.StatusCode != http.StatusUnauthorized {
			t.Errorf("status = %d, want 401", resp.StatusCode)
		}
	})

	t.Run("viewer reads status and sees not-wired", func(t *testing.T) {
		resp := env.do(t, http.MethodGet, "/api/v1/discovery/rescan/status", token, nil)
		if resp.StatusCode != http.StatusOK {
			t.Fatalf("status = %d, want 200 (배선이 없어도 코어는 답한다)", resp.StatusCode)
		}
		var out rescanStatusBody
		if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
			t.Fatalf("decode: %v", err)
		}
		if out.Wired || out.Enabled {
			t.Errorf("wired/enabled = %v/%v, want false/false", out.Wired, out.Enabled)
		}
		if out.Reason == "" {
			t.Error("reason 비어 있음 — 배선 없음의 진단 경로가 사라졌다")
		}
		if out.IntervalSeconds != int(defaultRescanInterval/time.Second) {
			t.Errorf("interval_seconds = %d, want %d", out.IntervalSeconds,
				int(defaultRescanInterval/time.Second))
		}
		if out.RequestID == "" {
			t.Error("request_id 비어 있음")
		}
	})

	t.Run("run without wiring returns 503", func(t *testing.T) {
		admin, adminToken := newRescanAdmin(t, env, "rescan-admin-unwired")
		_ = admin
		resp := env.do(t, http.MethodPost, "/api/v1/discovery/rescan/run", adminToken, nil)
		if resp.StatusCode != http.StatusServiceUnavailable {
			t.Fatalf("run = %d, want 503", resp.StatusCode)
		}
		var out struct {
			Error struct {
				Code string `json:"code"`
			} `json:"error"`
		}
		if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
			t.Fatalf("decode: %v", err)
		}
		if out.Error.Code != "rescan_unavailable" {
			t.Errorf("code = %q, want rescan_unavailable", out.Error.Code)
		}
	})

	t.Run("viewer cannot run a batch", func(t *testing.T) {
		resp := env.do(t, http.MethodPost, "/api/v1/discovery/rescan/run", token, nil)
		if resp.StatusCode != http.StatusForbidden {
			t.Errorf("viewer run = %d, want 403 (스캔 발주와 같은 3역할)", resp.StatusCode)
		}
	})
}

// newRescanAdmin은 스캔 발주 권한을 가진 계정을 만들고 토큰을 반환한다.
func newRescanAdmin(t *testing.T, env *testEnv, username string) (string, string) {
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
	return username, token
}

func TestRescanRunWithWiring(t *testing.T) {
	fake := &fakeRescan{enabled: false, interval: 6 * time.Hour,
		result: RescanRunResult{Jobs: 2, Endpoints: 37}}
	env := newTestEnv(t, WithRescanController(fake))

	_, token := newRescanAdmin(t, env, "rescan-operator")

	t.Run("disabled config returns 409 and never calls the scheduler", func(t *testing.T) {
		resp := env.do(t, http.MethodPost, "/api/v1/discovery/rescan/run", token, nil)
		if resp.StatusCode != http.StatusConflict {
			t.Fatalf("run = %d, want 409", resp.StatusCode)
		}
		fake.mu.Lock()
		calls := len(fake.calls)
		fake.mu.Unlock()
		if calls != 0 {
			t.Errorf("RunOnce 호출 %d회, want 0 (비활성 상태에서 발주하면 안 된다)", calls)
		}
	})

	t.Run("status reports wired but disabled", func(t *testing.T) {
		resp := env.do(t, http.MethodGet, "/api/v1/discovery/rescan/status", token, nil)
		var out rescanStatusBody
		if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
			t.Fatalf("decode: %v", err)
		}
		if !out.Wired || out.Enabled {
			t.Errorf("wired/enabled = %v/%v, want true/false", out.Wired, out.Enabled)
		}
		if out.IntervalSeconds != int((6*time.Hour)/time.Second) {
			t.Errorf("interval_seconds = %d, want 21600 (배선 값이 기본값을 이긴다)",
				out.IntervalSeconds)
		}
	})

	fake.mu.Lock()
	fake.enabled = true
	fake.mu.Unlock()

	t.Run("enabled run dispatches and carries the actor", func(t *testing.T) {
		resp := env.do(t, http.MethodPost, "/api/v1/discovery/rescan/run", token, nil)
		if resp.StatusCode != http.StatusAccepted {
			t.Fatalf("run = %d, want 202", resp.StatusCode)
		}
		var out struct {
			RequestID           string `json:"request_id"`
			DispatchedJobs      int    `json:"dispatched_jobs"`
			DispatchedEndpoints int    `json:"dispatched_endpoints"`
		}
		if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
			t.Fatalf("decode: %v", err)
		}
		if out.DispatchedJobs != 2 || out.DispatchedEndpoints != 37 {
			t.Errorf("dispatched = %d/%d, want 2/37", out.DispatchedJobs, out.DispatchedEndpoints)
		}
		fake.mu.Lock()
		defer fake.mu.Unlock()
		if len(fake.calls) != 1 {
			t.Fatalf("RunOnce 호출 %d회, want 1", len(fake.calls))
		}
		// 감사 이벤트는 스케줄러 구현이 남긴다(설계 10.4) — API는 행위자를 넘기는
		// 책임까지다. actor가 nil이면 감사에 "누가 눌렀는지"가 사라진다.
		if fake.calls[0].ActorID == nil || *fake.calls[0].ActorID == "" {
			t.Error("actor_id가 스케줄러로 전달되지 않았다")
		}
		if fake.calls[0].RequestID == nil {
			t.Error("request_id가 스케줄러로 전달되지 않았다")
		}
	})
}
