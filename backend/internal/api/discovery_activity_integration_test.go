package api

// 통합 작업 목록·기본값 API의 배선 검증 (TEST_DATABASE_URL 미설정 시 스킵).
// 판정 규칙 자체는 store 계층 테스트가 고정한다 — 여기서는 라우팅·권한·응답
// 모양만 본다.

import (
	"encoding/json"
	"net/http"
	"testing"

	"github.com/Falke060918/INHERIT_AutoCert/backend/internal/auth"
	"github.com/Falke060918/INHERIT_AutoCert/backend/internal/domain"
)

type activityItemJSON struct {
	Kind   string `json:"kind"`
	ID     string `json:"id"`
	Status string `json:"status"`
	Active bool   `json:"active"`
	Cancel bool   `json:"cancel"`
}

type activityListJSON struct {
	Items    []activityItemJSON `json:"items"`
	Total    int                `json:"total"`
	Page     int                `json:"page"`
	PageSize int                `json:"page_size"`
}

func TestDiscoveryActivityAndDefaultsEndpoints(t *testing.T) {
	env := newTestEnv(t)
	env.cleanInventory(t)
	ctx := t.Context()

	adminPW := randomPassword(t)
	viewerPW := randomPassword(t)
	if _, err := env.svc.CreateUser(ctx, "act-admin", adminPW, nil, domain.RoleSystemAdministrator, nil, auth.AuditMeta{}); err != nil {
		t.Fatalf("create admin: %v", err)
	}
	if _, err := env.svc.CreateUser(ctx, "act-viewer", viewerPW, nil, domain.RoleViewer, nil, auth.AuditMeta{}); err != nil {
		t.Fatalf("create viewer: %v", err)
	}
	adminToken, status := env.login(t, "act-admin", adminPW)
	if status != http.StatusOK {
		t.Fatalf("admin login = %d, want 200", status)
	}
	viewerToken, status := env.login(t, "act-viewer", viewerPW)
	if status != http.StatusOK {
		t.Fatalf("viewer login = %d, want 200", status)
	}

	// 도달 불가 대상 1개 — 곧 실패로 끝나지만 목록에는 실려야 한다.
	resp := env.do(t, http.MethodPost, "/api/v1/discovery/scans", adminToken, map[string]any{
		"targets":    []string{"192.0.2.7"},
		"ports":      []int{9},
		"timeout_ms": 200,
	})
	if resp.StatusCode != http.StatusAccepted {
		t.Fatalf("create scan = %d, want 202", resp.StatusCode)
	}
	var created struct {
		JobID string `json:"job_id"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&created); err != nil {
		t.Fatalf("decode create: %v", err)
	}

	t.Run("unauthenticated activity is 401", func(t *testing.T) {
		r := env.do(t, http.MethodGet, "/api/v1/discovery/activity", "", nil)
		if r.StatusCode != http.StatusUnauthorized {
			t.Errorf("activity without token = %d, want 401", r.StatusCode)
		}
	})

	t.Run("viewer can read the activity list", func(t *testing.T) {
		r := env.do(t, http.MethodGet, "/api/v1/discovery/activity", viewerToken, nil)
		if r.StatusCode != http.StatusOK {
			t.Fatalf("activity = %d, want 200", r.StatusCode)
		}
		var body activityListJSON
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Fatalf("decode activity: %v", err)
		}
		found := false
		for _, it := range body.Items {
			if it.ID == created.JobID {
				found = true
				if it.Kind != "scan" {
					t.Errorf("kind = %q, want scan", it.Kind)
				}
			}
		}
		if !found {
			t.Errorf("manual scan job %s is missing from the activity list (total=%d)",
				created.JobID, body.Total)
		}
	})

	t.Run("unknown kind is 400", func(t *testing.T) {
		r := env.do(t, http.MethodGet, "/api/v1/discovery/activity?kind=rescan", viewerToken, nil)
		if r.StatusCode != http.StatusBadRequest {
			t.Errorf("kind=rescan = %d, want 400", r.StatusCode)
		}
	})

	t.Run("viewer can read discovery defaults", func(t *testing.T) {
		r := env.do(t, http.MethodGet, "/api/v1/discovery/defaults", viewerToken, nil)
		if r.StatusCode != http.StatusOK {
			t.Fatalf("defaults = %d, want 200", r.StatusCode)
		}
		var body discoveryDefaultsResponse
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Fatalf("decode defaults: %v", err)
		}
		if body.Sweep.RatePPS < 1 || body.Sweep.ChunkEndpoints < 1 || body.Sweep.MaxDays < 1 {
			t.Errorf("sweep defaults = %+v, want positive effective values", body.Sweep)
		}
		if body.Limits.MaxEndpoints < 1 {
			t.Errorf("limits = %+v, want positive values", body.Limits)
		}
	})
}
