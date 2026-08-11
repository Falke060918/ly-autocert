package api

// 기본값 노출과 통합 목록 파라미터 검증 — DB가 필요 없는 순수 경로다.

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/Falke060918/INHERIT_AutoCert/backend/internal/discovery"
	"github.com/Falke060918/INHERIT_AutoCert/backend/internal/store"
)

// 주입된 SCAN_SWEEP_* 설정이 그대로 응답에 실려야 한다 — 이 값이 화면
// placeholder가 되므로 여기서 어긋나면 화면이 거짓말한다.
func TestDiscoveryDefaultsReflectsInjectedConfig(t *testing.T) {
	srv := newServer(nil, nil, nil, nil, WithSweepDefaults(SweepDefaults{
		RatePPS: 40, ChunkEndpoints: 2048, MaxDays: 7,
	}))

	rec := httptest.NewRecorder()
	srv.handleDiscoveryDefaults(rec, httptest.NewRequest(http.MethodGet, "/api/v1/discovery/defaults", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	var body discoveryDefaultsResponse
	if err := json.NewDecoder(rec.Body).Decode(&body); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if body.Sweep.RatePPS != 40 || body.Sweep.ChunkEndpoints != 2048 || body.Sweep.MaxDays != 7 {
		t.Errorf("sweep defaults = %+v, want the injected values", body.Sweep)
	}
	if body.Scan.PortSpec != discovery.DefaultPortSpec {
		t.Errorf("scan.port_spec = %q, want %q", body.Scan.PortSpec, discovery.DefaultPortSpec)
	}
	if body.Limits.MaxRatePPS != discovery.MaxRatePPS || body.Limits.MaxEndpoints != discovery.MaxEndpoints {
		t.Errorf("limits = %+v, want the scanner constants", body.Limits)
	}
}

// 배선이 없으면 store 기본값으로 폴백한다(스윕 생성 경로와 같은 규칙).
func TestDiscoveryDefaultsFallsBackToStoreDefaults(t *testing.T) {
	srv := newServer(nil, nil, nil, nil)

	rec := httptest.NewRecorder()
	srv.handleDiscoveryDefaults(rec, httptest.NewRequest(http.MethodGet, "/api/v1/discovery/defaults", nil))
	var body discoveryDefaultsResponse
	if err := json.NewDecoder(rec.Body).Decode(&body); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if body.Sweep.RatePPS != store.DefaultSweepRatePPS ||
		body.Sweep.ChunkEndpoints != store.DefaultSweepChunkEndpoints ||
		body.Sweep.MaxDays != store.DefaultSweepMaxDays {
		t.Errorf("sweep defaults = %+v, want the store fallbacks", body.Sweep)
	}
}

func TestParseActivityParams(t *testing.T) {
	t.Run("kind accepts repeated and comma separated values", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodGet,
			"/api/v1/discovery/activity?kind=scan,sweep&kind=schedule_run&active=true", nil)
		in, perr := parseActivityParams(req)
		if perr != nil {
			t.Fatalf("parse: %+v", perr)
		}
		if len(in.Kinds) != 3 {
			t.Errorf("kinds = %v, want 3 values", in.Kinds)
		}
		if in.Active == nil || !*in.Active {
			t.Errorf("active = %v, want true", in.Active)
		}
	})

	t.Run("unknown kind is a 400 parameter error", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodGet, "/api/v1/discovery/activity?kind=rescan", nil)
		if _, perr := parseActivityParams(req); perr == nil || perr.code != "invalid_kind" {
			t.Errorf("perr = %+v, want invalid_kind", perr)
		}
	})

	t.Run("non-boolean active is rejected", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodGet, "/api/v1/discovery/activity?active=yes", nil)
		if _, perr := parseActivityParams(req); perr == nil || perr.code != "invalid_active" {
			t.Errorf("perr = %+v, want invalid_active", perr)
		}
	})
}
