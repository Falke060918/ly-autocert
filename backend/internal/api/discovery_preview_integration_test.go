package api

import (
	"encoding/json"
	"net/http"
	"testing"

	"github.com/Falke060918/INHERIT_AutoCert/backend/internal/auth"
	"github.com/Falke060918/INHERIT_AutoCert/backend/internal/domain"
)

// 자동 승격 + 미리보기 검증 (STATE 다음 할 일 2):
// 단발↔스윕 승격은 총 엔드포인트 수로만 갈리고, 미리보기는 아무것도 만들지 않으면서
// 발주와 같은 숫자를 돌려준다. 레이트·청크·ETA 가드는 스윕 경로 그대로다.
// TEST_DATABASE_URL 미설정 시 스킵한다.

type scanPreviewJSON struct {
	Mode               string `json:"mode"`
	Promoted           bool   `json:"promoted"`
	PromotionThreshold int    `json:"promotion_threshold"`
	IPCount            int    `json:"ip_count"`
	PortSpec           string `json:"port_spec"`
	PortCount          int    `json:"port_count"`
	TotalEndpoints     int64  `json:"total_endpoints"`
	RatePPS            int    `json:"rate_pps"`
	EstimatedSeconds   int64  `json:"estimated_seconds"`
	TotalChunks        int    `json:"total_chunks"`
	MaxDays            int    `json:"max_days"`
	TooLong            bool   `json:"too_long"`
	Message            string `json:"message"`
}

type scanAcceptedJSON struct {
	Mode           string `json:"mode"`
	Promoted       bool   `json:"promoted"`
	JobID          string `json:"job_id"`
	SweepID        string `json:"sweep_id"`
	Status         string `json:"status"`
	TotalEndpoints int64  `json:"total_endpoints"`
	TotalChunks    int    `json:"total_chunks"`
}

func TestDiscoveryScanPreviewAndPromotion(t *testing.T) {
	env := newTestEnv(t)
	env.cleanSweeps(t)
	ctx := t.Context()

	operatorPW, viewerPW := randomPassword(t), randomPassword(t)
	if _, err := env.svc.CreateUser(ctx, "pv-operator", operatorPW, nil, domain.RoleOperator, nil, auth.AuditMeta{}); err != nil {
		t.Fatalf("create operator: %v", err)
	}
	if _, err := env.svc.CreateUser(ctx, "pv-viewer", viewerPW, nil, domain.RoleViewer, nil, auth.AuditMeta{}); err != nil {
		t.Fatalf("create viewer: %v", err)
	}
	operatorToken, status := env.login(t, "pv-operator", operatorPW)
	if status != http.StatusOK {
		t.Fatalf("operator login = %d, want 200", status)
	}
	viewerToken, status := env.login(t, "pv-viewer", viewerPW)
	if status != http.StatusOK {
		t.Fatalf("viewer login = %d, want 200", status)
	}

	preview := func(t *testing.T, token string, body map[string]any) (*http.Response, scanPreviewJSON) {
		t.Helper()
		resp := env.do(t, http.MethodPost, "/api/v1/discovery/scans/preview", token, body)
		var out scanPreviewJSON
		if resp.StatusCode == http.StatusOK {
			if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
				t.Fatalf("decode preview: %v", err)
			}
		}
		return resp, out
	}

	// 미리보기는 조회이지만 발주 폼의 사전 계산이라 발주와 같은 3역할이다.
	t.Run("role gate", func(t *testing.T) {
		body := map[string]any{"targets": []string{"10.11.0.0/30"}, "port_spec": "web"}
		if resp, _ := preview(t, viewerToken, body); resp.StatusCode != http.StatusForbidden {
			t.Errorf("viewer preview = %d, want 403", resp.StatusCode)
		}
		if resp, _ := preview(t, "", body); resp.StatusCode != http.StatusUnauthorized {
			t.Errorf("unauthenticated preview = %d, want 401", resp.StatusCode)
		}
	})

	t.Run("bad input is 400 with the parser reason", func(t *testing.T) {
		resp, _ := preview(t, operatorToken, map[string]any{
			"targets": []string{"10.11.0.1"}, "port_spec": "443,,8443"})
		if resp.StatusCode != http.StatusBadRequest {
			t.Fatalf("malformed port_spec preview = %d, want 400", resp.StatusCode)
		}
	})

	t.Run("small request stays a single scan", func(t *testing.T) {
		resp, out := preview(t, operatorToken, map[string]any{
			"targets": []string{"10.11.0.0/30"}, "ports": []int{443, 8443}})
		if resp.StatusCode != http.StatusOK {
			t.Fatalf("preview = %d, want 200", resp.StatusCode)
		}
		if out.Mode != "scan" || out.Promoted {
			t.Errorf("mode/promoted = %q/%v, want scan/false", out.Mode, out.Promoted)
		}
		if out.IPCount != 4 || out.PortCount != 2 || out.TotalEndpoints != 8 {
			t.Errorf("preview = {ips:%d, ports:%d, endpoints:%d}, want {4, 2, 8}",
				out.IPCount, out.PortCount, out.TotalEndpoints)
		}
		// 정수 목록 입력도 같은 문법의 사양으로 되돌아온다.
		if out.PortSpec != "443,8443" {
			t.Errorf("port_spec = %q, want \"443,8443\"", out.PortSpec)
		}
		if out.TotalChunks != 0 {
			t.Errorf("total_chunks = %d, want 0 for a single scan", out.TotalChunks)
		}
		if out.EstimatedSeconds < 1 {
			t.Errorf("estimated_seconds = %d, want >= 1", out.EstimatedSeconds)
		}
	})

	t.Run("all preset over the threshold previews as a sweep", func(t *testing.T) {
		resp, out := preview(t, operatorToken, map[string]any{
			"targets": []string{"10.11.0.1"}, "port_spec": "all"})
		if resp.StatusCode != http.StatusOK {
			t.Fatalf("preview = %d, want 200 (all is no longer rejected)", resp.StatusCode)
		}
		if out.Mode != "sweep" || !out.Promoted {
			t.Errorf("mode/promoted = %q/%v, want sweep/true", out.Mode, out.Promoted)
		}
		if out.TotalEndpoints != 65535 {
			t.Errorf("total_endpoints = %d, want 65535", out.TotalEndpoints)
		}
		if out.TotalChunks < 1 || out.PromotionThreshold < 1 {
			t.Errorf("chunks/threshold = %d/%d, want both >= 1", out.TotalChunks, out.PromotionThreshold)
		}
		if out.TooLong {
			t.Errorf("too_long = true for %d endpoints, want false", out.TotalEndpoints)
		}
	})

	// ETA 상한은 스윕 경로 그대로다: 미리보기는 거부하지 않고 알려 주고,
	// 실제 발주는 422로 거부한다.
	t.Run("oversized request warns in preview and is refused on create", func(t *testing.T) {
		body := map[string]any{"targets": []string{"10.12.0.0/16"}, "port_spec": "all"}
		resp, out := preview(t, operatorToken, body)
		if resp.StatusCode != http.StatusOK {
			t.Fatalf("preview = %d, want 200 (preview never refuses)", resp.StatusCode)
		}
		if !out.TooLong || out.Message == "" {
			t.Errorf("preview = {too_long:%v, message:%q}, want a warning", out.TooLong, out.Message)
		}
		create := env.do(t, http.MethodPost, "/api/v1/discovery/scans", operatorToken, body)
		if create.StatusCode != http.StatusUnprocessableEntity {
			t.Errorf("oversized create = %d, want 422 (sweep_too_long)", create.StatusCode)
		}
	})

	t.Run("create promotes to a sweep and records it in the audit", func(t *testing.T) {
		body := map[string]any{"targets": []string{"10.13.0.1"}, "port_spec": "all"}
		resp := env.do(t, http.MethodPost, "/api/v1/discovery/scans", operatorToken, body)
		if resp.StatusCode != http.StatusAccepted {
			t.Fatalf("promoted create = %d, want 202", resp.StatusCode)
		}
		var out scanAcceptedJSON
		if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
			t.Fatalf("decode create: %v", err)
		}
		if out.Mode != "sweep" || !out.Promoted {
			t.Fatalf("mode/promoted = %q/%v, want sweep/true", out.Mode, out.Promoted)
		}
		if out.SweepID == "" || out.JobID != "" {
			t.Fatalf("ids = {sweep:%q, job:%q}, want a sweep id only", out.SweepID, out.JobID)
		}
		if out.Status != "queued" || out.TotalEndpoints != 65535 || out.TotalChunks < 1 {
			t.Errorf("create = {status:%q, endpoints:%d, chunks:%d}, want queued/65535/>=1",
				out.Status, out.TotalEndpoints, out.TotalChunks)
		}

		// 스윕 행이 실제로 생겼고 포트 사양이 그대로 보존됐다(청크 재생의 근거).
		r := env.do(t, http.MethodGet, "/api/v1/discovery/sweeps/"+out.SweepID, operatorToken, nil)
		if r.StatusCode != http.StatusOK {
			t.Fatalf("get promoted sweep = %d, want 200", r.StatusCode)
		}
		var sw sweepJSON
		if err := json.NewDecoder(r.Body).Decode(&sw); err != nil {
			t.Fatalf("decode sweep: %v", err)
		}
		if sw.PortSpec != "all" || sw.Status != "queued" {
			t.Errorf("sweep = {port_spec:%q, status:%q}, want all/queued", sw.PortSpec, sw.Status)
		}

		// 승격 사실이 감사에 남는다 — 사용자가 누른 것(단발)과 만들어진 것(스윕)이 다르다.
		var promoted bool
		if err := env.pool.QueryRow(
			`SELECT coalesce((after_value ->> 'promoted')::boolean, false)
			   FROM audit_events WHERE event_type = 'discovery.sweep' AND target_id = $1`,
			out.SweepID).Scan(&promoted); err != nil {
			t.Fatalf("read sweep audit: %v", err)
		}
		if !promoted {
			t.Error("discovery.sweep audit has no promoted marker")
		}
	})

	// 승격 임계 이하의 요청은 예전 그대로 단발 Job이다(회귀 방지).
	t.Run("small create stays a single scan job", func(t *testing.T) {
		resp := env.do(t, http.MethodPost, "/api/v1/discovery/scans", operatorToken,
			map[string]any{"targets": []string{"10.14.0.1"}, "ports": []int{9},
				"timeout_ms": 100})
		if resp.StatusCode != http.StatusAccepted {
			t.Fatalf("create = %d, want 202", resp.StatusCode)
		}
		var out scanAcceptedJSON
		if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
			t.Fatalf("decode create: %v", err)
		}
		if out.Mode != "scan" || out.Promoted || out.JobID == "" || out.SweepID != "" {
			t.Errorf("create = %+v, want a plain scan job", out)
		}
	})
}
