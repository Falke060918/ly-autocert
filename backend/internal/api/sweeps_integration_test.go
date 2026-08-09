package api

import (
	"encoding/json"
	"net/http"
	"regexp"
	"strconv"
	"testing"

	"github.com/Falke060918/INHERIT_AutoCert/backend/internal/auth"
	"github.com/Falke060918/INHERIT_AutoCert/backend/internal/domain"
)

// 전 포트 백그라운드 스윕 API 검증 (04-port-autoscan 9.2):
// 생성(202)·ETA 초과 거부(422, 실제 일수 포함)·조회·상태 전이·프리셋·RBAC.

func (e *testEnv) cleanSweeps(t *testing.T) {
	t.Helper()
	for _, stmt := range []string{
		`DELETE FROM scan_jobs WHERE sweep_id IS NOT NULL`,
		`DELETE FROM scan_sweeps`,
	} {
		if _, err := e.pool.Exec(stmt); err != nil {
			t.Fatalf("cleanup %q: %v", stmt, err)
		}
	}
}

type sweepJSON struct {
	ID       string `json:"id"`
	Status   string `json:"status"`
	PortSpec string `json:"port_spec"`
	Progress struct {
		ChunksTotal int `json:"chunks_total"`
	} `json:"progress"`
}

func TestDiscoverySweepAPI(t *testing.T) {
	env := newTestEnv(t)
	env.cleanSweeps(t)
	ctx := t.Context()

	operatorPW, viewerPW := randomPassword(t), randomPassword(t)
	if _, err := env.svc.CreateUser(ctx, "sw-operator", operatorPW, nil, domain.RoleOperator, nil, auth.AuditMeta{}); err != nil {
		t.Fatalf("create operator: %v", err)
	}
	if _, err := env.svc.CreateUser(ctx, "sw-viewer", viewerPW, nil, domain.RoleViewer, nil, auth.AuditMeta{}); err != nil {
		t.Fatalf("create viewer: %v", err)
	}
	operatorToken, status := env.login(t, "sw-operator", operatorPW)
	if status != http.StatusOK {
		t.Fatalf("operator login = %d, want 200", status)
	}
	viewerToken, status := env.login(t, "sw-viewer", viewerPW)
	if status != http.StatusOK {
		t.Fatalf("viewer login = %d, want 200", status)
	}

	t.Run("role gate", func(t *testing.T) {
		body := map[string]any{"targets": []string{"10.10.0.0/30"}, "port_spec": "web"}
		if resp := env.do(t, http.MethodPost, "/api/v1/discovery/sweeps", viewerToken, body); resp.StatusCode != http.StatusForbidden {
			t.Errorf("viewer create sweep = %d, want 403", resp.StatusCode)
		}
		if resp := env.do(t, http.MethodPost, "/api/v1/discovery/sweeps", "", body); resp.StatusCode != http.StatusUnauthorized {
			t.Errorf("unauthenticated create sweep = %d, want 401", resp.StatusCode)
		}
		if resp := env.do(t, http.MethodGet, "/api/v1/discovery/sweeps", viewerToken, nil); resp.StatusCode != http.StatusOK {
			t.Errorf("viewer list sweeps = %d, want 200 (read-only is all roles)", resp.StatusCode)
		}
	})

	t.Run("bad input is 400", func(t *testing.T) {
		cases := []map[string]any{
			{"targets": []string{"not-an-ip"}, "port_spec": "web"},
			{"targets": []string{"10.10.0.1"}, "port_spec": "8443-"},
			{"targets": []string{}, "port_spec": "web"},
		}
		for _, body := range cases {
			resp := env.do(t, http.MethodPost, "/api/v1/discovery/sweeps", operatorToken, body)
			if resp.StatusCode != http.StatusBadRequest {
				t.Errorf("create sweep %v = %d, want 400", body, resp.StatusCode)
			}
		}
	})

	// 5.4: ETA가 상한을 넘으면 422이고, 메시지에 **실제 소요 일수**가 들어간다.
	t.Run("eta over the limit is 422 with the day count", func(t *testing.T) {
		resp := env.do(t, http.MethodPost, "/api/v1/discovery/sweeps", operatorToken,
			map[string]any{"targets": []string{"10.20.0.0/20"}, "port_spec": "all", "rate_pps": 10})
		if resp.StatusCode != http.StatusUnprocessableEntity {
			t.Fatalf("oversized sweep = %d, want 422", resp.StatusCode)
		}
		var out struct {
			Error struct {
				Code    string `json:"code"`
				Message string `json:"message"`
			} `json:"error"`
		}
		if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
			t.Fatalf("decode error: %v", err)
		}
		if out.Error.Code != "sweep_too_long" {
			t.Errorf("error code = %q, want sweep_too_long", out.Error.Code)
		}
		m := regexp.MustCompile(`약 ([0-9.]+)일`).FindStringSubmatch(out.Error.Message)
		if m == nil {
			t.Fatalf("message %q has no day count; 사용자가 얼마나 줄여야 할지 알 수 없다", out.Error.Message)
		}
		days, err := strconv.ParseFloat(m[1], 64)
		if err != nil || days <= 30 {
			t.Errorf("day count = %q (parse err %v), want a number above the 30-day limit", m[1], err)
		}
	})

	// 생성: 4 IP × web 프리셋 6포트 = 24 엔드포인트.
	resp := env.do(t, http.MethodPost, "/api/v1/discovery/sweeps", operatorToken,
		map[string]any{"name": "야간 스윕", "targets": []string{"10.30.0.0/30"}, "port_spec": "web"})
	if resp.StatusCode != http.StatusAccepted {
		t.Fatalf("create sweep = %d, want 202", resp.StatusCode)
	}
	var created struct {
		SweepID           string  `json:"sweep_id"`
		Status            string  `json:"status"`
		TotalEndpoints    int64   `json:"total_endpoints"`
		TotalChunks       int     `json:"total_chunks"`
		EstimatedFinishAt *string `json:"estimated_finish_at"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&created); err != nil {
		t.Fatalf("decode create response: %v", err)
	}
	if created.SweepID == "" || created.Status != "queued" {
		t.Fatalf("created = %+v, want a queued sweep id", created)
	}
	if created.TotalEndpoints != 24 {
		t.Errorf("total_endpoints = %d, want 24 (4 IPs × 6 web ports)", created.TotalEndpoints)
	}
	if created.TotalChunks < 1 {
		t.Errorf("total_chunks = %d, want >= 1", created.TotalChunks)
	}
	if created.EstimatedFinishAt == nil {
		t.Error("estimated_finish_at is null; ETA는 생성 시점에 파생된다")
	}
	if n := env.auditCount(t, "discovery.sweep", domain.AuditSuccess); n < 1 {
		t.Errorf("discovery.sweep audit events = %d, want >= 1 (created)", n)
	}

	t.Run("get and list", func(t *testing.T) {
		resp := env.do(t, http.MethodGet, "/api/v1/discovery/sweeps/"+created.SweepID, viewerToken, nil)
		if resp.StatusCode != http.StatusOK {
			t.Fatalf("get sweep = %d, want 200", resp.StatusCode)
		}
		var sw sweepJSON
		if err := json.NewDecoder(resp.Body).Decode(&sw); err != nil {
			t.Fatalf("decode sweep: %v", err)
		}
		if sw.ID != created.SweepID || sw.PortSpec != "web" {
			t.Errorf("sweep = %+v, want id %s with port_spec web", sw, created.SweepID)
		}
		if sw.Progress.ChunksTotal != created.TotalChunks {
			t.Errorf("progress.chunks_total = %d, want %d", sw.Progress.ChunksTotal, created.TotalChunks)
		}

		resp = env.do(t, http.MethodGet, "/api/v1/discovery/sweeps?status=queued", viewerToken, nil)
		if resp.StatusCode != http.StatusOK {
			t.Fatalf("list sweeps = %d, want 200", resp.StatusCode)
		}
		var list struct {
			Items []sweepJSON `json:"items"`
			Total int         `json:"total"`
		}
		if err := json.NewDecoder(resp.Body).Decode(&list); err != nil {
			t.Fatalf("decode list: %v", err)
		}
		if list.Total != 1 || len(list.Items) != 1 {
			t.Errorf("queued sweeps = %d (items %d), want 1", list.Total, len(list.Items))
		}
		if resp := env.do(t, http.MethodGet, "/api/v1/discovery/sweeps?status=bogus", viewerToken, nil); resp.StatusCode != http.StatusBadRequest {
			t.Errorf("bad status filter = %d, want 400", resp.StatusCode)
		}
	})

	t.Run("unknown sweep is 404", func(t *testing.T) {
		for _, id := range []string{"00000000-0000-0000-0000-000000000000", "not-a-uuid"} {
			if resp := env.do(t, http.MethodGet, "/api/v1/discovery/sweeps/"+id, viewerToken, nil); resp.StatusCode != http.StatusNotFound {
				t.Errorf("get sweep %s = %d, want 404", id, resp.StatusCode)
			}
			if resp := env.do(t, http.MethodPost, "/api/v1/discovery/sweeps/"+id+"/pause", operatorToken, nil); resp.StatusCode != http.StatusNotFound {
				t.Errorf("pause sweep %s = %d, want 404", id, resp.StatusCode)
			}
		}
	})

	t.Run("pause resume cancel", func(t *testing.T) {
		transition := func(action string, token string) (int, sweepJSON) {
			t.Helper()
			resp := env.do(t, http.MethodPost, "/api/v1/discovery/sweeps/"+created.SweepID+"/"+action, token, nil)
			var sw sweepJSON
			if resp.StatusCode == http.StatusOK {
				if err := json.NewDecoder(resp.Body).Decode(&sw); err != nil {
					t.Fatalf("decode %s response: %v", action, err)
				}
			}
			return resp.StatusCode, sw
		}

		if code, _ := transition("pause", viewerToken); code != http.StatusForbidden {
			t.Errorf("viewer pause = %d, want 403", code)
		}
		if code, sw := transition("pause", operatorToken); code != http.StatusOK || sw.Status != "paused" {
			t.Fatalf("pause = %d/%s, want 200/paused", code, sw.Status)
		}
		if code, _ := transition("pause", operatorToken); code != http.StatusConflict {
			t.Errorf("second pause = %d, want 409", code)
		}
		if code, sw := transition("resume", operatorToken); code != http.StatusOK || sw.Status != "queued" {
			t.Fatalf("resume = %d/%s, want 200/queued (발주 전이라 started_at이 없다)", code, sw.Status)
		}
		if code, sw := transition("cancel", operatorToken); code != http.StatusOK || sw.Status != "canceled" {
			t.Fatalf("cancel = %d/%s, want 200/canceled", code, sw.Status)
		}
		if code, _ := transition("cancel", operatorToken); code != http.StatusConflict {
			t.Errorf("cancel after cancel = %d, want 409", code)
		}
		// 전이 3건 + 생성 1건이 모두 감사된다.
		if n := env.auditCount(t, "discovery.sweep", domain.AuditSuccess); n < 4 {
			t.Errorf("discovery.sweep audit events = %d, want >= 4 (created + pause/resume/cancel)", n)
		}
	})
}

func TestDiscoveryPortPresetsAndScanPortSpec(t *testing.T) {
	env := newTestEnv(t)
	ctx := t.Context()

	pw := randomPassword(t)
	if _, err := env.svc.CreateUser(ctx, "pp-operator", pw, nil, domain.RoleOperator, nil, auth.AuditMeta{}); err != nil {
		t.Fatalf("create operator: %v", err)
	}
	token, status := env.login(t, "pp-operator", pw)
	if status != http.StatusOK {
		t.Fatalf("login = %d, want 200", status)
	}

	t.Run("port presets are served from the single source of truth", func(t *testing.T) {
		resp := env.do(t, http.MethodGet, "/api/v1/discovery/port-presets", token, nil)
		if resp.StatusCode != http.StatusOK {
			t.Fatalf("port presets = %d, want 200", resp.StatusCode)
		}
		var out struct {
			Default string `json:"default"`
			Items   []struct {
				Name  string `json:"name"`
				Count int    `json:"count"`
				Ports []int  `json:"ports"`
			} `json:"items"`
		}
		if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
			t.Fatalf("decode presets: %v", err)
		}
		if out.Default != "common" {
			t.Errorf("default = %q, want common", out.Default)
		}
		if len(out.Items) != 3 {
			t.Fatalf("presets = %d, want 3 (web/common/all)", len(out.Items))
		}
		for _, p := range out.Items {
			if p.Name == "all" {
				if p.Count != 65535 || len(p.Ports) != 0 {
					t.Errorf("all preset = count %d ports %d, want count 65535 with no port list", p.Count, len(p.Ports))
				}
			}
		}
	})

	t.Run("port_spec drives the scan and all is rejected", func(t *testing.T) {
		// 192.0.2.0/24는 문서화 전용 대역(TEST-NET-1)이라 실제로 응답하지 않는다.
		resp := env.do(t, http.MethodPost, "/api/v1/discovery/scans", token,
			map[string]any{"targets": []string{"192.0.2.10"}, "port_spec": "8443,9443", "timeout_ms": 100})
		if resp.StatusCode != http.StatusAccepted {
			t.Fatalf("scan with port_spec = %d, want 202", resp.StatusCode)
		}
		var created struct {
			TotalEndpoints int `json:"total_endpoints"`
		}
		if err := json.NewDecoder(resp.Body).Decode(&created); err != nil {
			t.Fatalf("decode scan: %v", err)
		}
		if created.TotalEndpoints != 2 {
			t.Errorf("total_endpoints = %d, want 2", created.TotalEndpoints)
		}

		for _, spec := range []string{"all", "web,all", "8443-"} {
			resp := env.do(t, http.MethodPost, "/api/v1/discovery/scans", token,
				map[string]any{"targets": []string{"192.0.2.10"}, "port_spec": spec})
			if resp.StatusCode != http.StatusBadRequest {
				t.Errorf("scan with port_spec %q = %d, want 400", spec, resp.StatusCode)
			}
		}
	})
}
