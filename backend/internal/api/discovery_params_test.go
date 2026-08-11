package api

import (
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/Falke060918/INHERIT_AutoCert/backend/internal/discovery"
	"github.com/Falke060918/INHERIT_AutoCert/backend/internal/store"
)

// 단발 스캔의 포트 결정 규칙 (04-port-autoscan 9.2): port_spec > ports > common.
// DB가 필요 없는 순수 함수 테스트다.

func TestResolveScanPortsPriority(t *testing.T) {
	common, err := discovery.ParsePortSpec(discovery.DefaultPortSpec)
	if err != nil {
		t.Fatalf("parse default spec: %v", err)
	}

	t.Run("no input falls back to common", func(t *testing.T) {
		ports, err := resolveScanPorts("", nil)
		if err != nil {
			t.Fatalf("resolve: %v", err)
		}
		if len(ports) != len(common) {
			t.Fatalf("default ports = %d, want %d (common preset)", len(ports), len(common))
		}
		// 기본값이 [443]에서 common으로 바뀌었다는 사실을 고정한다.
		if len(ports) == 1 {
			t.Error("default port set is still a single port; expected the common preset")
		}
	})

	t.Run("ports is used when port_spec is absent", func(t *testing.T) {
		ports, err := resolveScanPorts("", []int{443, 8443})
		if err != nil {
			t.Fatalf("resolve: %v", err)
		}
		if len(ports) != 2 || ports[0] != 443 || ports[1] != 8443 {
			t.Errorf("ports = %v, want [443 8443]", ports)
		}
	})

	t.Run("port_spec wins over ports", func(t *testing.T) {
		ports, err := resolveScanPorts("8000-8002", []int{443})
		if err != nil {
			t.Fatalf("resolve: %v", err)
		}
		if len(ports) != 3 || ports[0] != 8000 || ports[2] != 8002 {
			t.Errorf("ports = %v, want [8000 8001 8002]", ports)
		}
	})

	// all 프리셋은 더 이상 거부하지 않는다 — 크기 판정은 planScan의 자동 승격이
	// 담당하므로 파서는 요청을 그대로 펼친다.
	t.Run("all preset is accepted", func(t *testing.T) {
		for _, spec := range []string{"all", "web,all", " ALL "} {
			ports, err := resolveScanPorts(spec, nil)
			if err != nil {
				t.Errorf("resolveScanPorts(%q) error = %v, want nil", spec, err)
				continue
			}
			if len(ports) != 65535 {
				t.Errorf("resolveScanPorts(%q) = %d ports, want 65535", spec, len(ports))
			}
		}
	})

	t.Run("malformed spec is an input error", func(t *testing.T) {
		if _, err := resolveScanPorts("not-a-port", nil); !errors.Is(err, discovery.ErrInvalidScanInput) {
			t.Errorf("error = %v, want ErrInvalidScanInput", err)
		}
	})
}

// 400 본문에는 파서가 만든 한국어 사유만 실린다. 내부 센티널 접두사
// ("discovery: invalid scan input: ")는 프론트가 서버 메시지를 그대로 띄우므로
// 사용자 눈에 그대로 노출된다 — 로그에만 남기고 응답에서는 뺀다.
func TestWriteScanInputErrorHidesInternalPrefix(t *testing.T) {
	_, err := resolveScanPorts("abc", nil)
	if err == nil {
		t.Fatal("resolveScanPorts(\"abc\") = nil error, want error")
	}

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/api/v1/discovery/scans", nil)
	writeScanInputError(rec, req, err)

	if rec.Code != http.StatusBadRequest {
		t.Errorf("status = %d, want 400", rec.Code)
	}
	var body struct {
		Error struct {
			Code    string `json:"code"`
			Message string `json:"message"`
		} `json:"error"`
	}
	if derr := json.NewDecoder(rec.Body).Decode(&body); derr != nil {
		t.Fatalf("decode body: %v", derr)
	}
	if body.Error.Code != "invalid_scan_request" {
		t.Errorf("code = %q, want invalid_scan_request", body.Error.Code)
	}
	for _, leak := range []string{"discovery:", "invalid scan input"} {
		if strings.Contains(body.Error.Message, leak) {
			t.Errorf("message = %q, 내부 접두사 %q가 노출됩니다", body.Error.Message, leak)
		}
	}
	if !strings.Contains(body.Error.Message, "포트") {
		t.Errorf("message = %q, 파서의 한국어 사유가 남아 있지 않습니다", body.Error.Message)
	}
}

// 감싸지 않은 오류가 400 경로로 들어와도 원문을 그대로 흘리지 않는다.
func TestScanInputMessageFallsBackForUnwrappedError(t *testing.T) {
	got := scanInputMessage(errors.New("store: pq: relation \"scan_jobs\" does not exist"))
	if strings.Contains(got, "store:") || strings.Contains(got, "scan_jobs") {
		t.Errorf("message = %q, 내부 오류 원문이 노출됩니다", got)
	}
	if got == "" {
		t.Error("message = \"\", 사용자에게 보일 문구가 필요합니다")
	}
}

// 자동 승격 판정: 총 엔드포인트 수(IP × 포트)가 임계를 **넘을 때만** 스윕이다.
// 임계는 SCAN_SWEEP_CHUNK_ENDPOINTS(청크 1개 분량)이므로 딱 1청크로 끝나는
// 요청은 단발로 남는다. DB가 필요 없다.
func TestPlanScanPromotion(t *testing.T) {
	// 임계 8: /30(4 IP) × 포트 수로 경계를 정확히 짚기 위한 값이다.
	srv := newServer(nil, nil, nil, nil, WithSweepDefaults(SweepDefaults{
		RatePPS: 10, ChunkEndpoints: 8, MaxDays: 30,
	}))

	t.Run("at the threshold it stays a single scan", func(t *testing.T) {
		plan, err := srv.planScan(createScanRequest{
			Targets: []string{"10.9.0.0/30"}, Ports: []int{443, 8443},
		})
		if err != nil {
			t.Fatalf("plan: %v", err)
		}
		if plan.TotalEndpoints != 8 {
			t.Fatalf("total_endpoints = %d, want 8", plan.TotalEndpoints)
		}
		if plan.Promoted {
			t.Error("promoted = true at the threshold, want false (> only)")
		}
		if plan.RatePPS != discovery.DefaultScanRatePPS {
			t.Errorf("rate_pps = %d, want %d (single scan)", plan.RatePPS, discovery.DefaultScanRatePPS)
		}
		if plan.TotalChunks != 0 {
			t.Errorf("total_chunks = %d, want 0 for a single scan", plan.TotalChunks)
		}
	})

	t.Run("over the threshold it promotes to a sweep", func(t *testing.T) {
		plan, err := srv.planScan(createScanRequest{
			Targets: []string{"10.9.0.0/30"}, Ports: []int{443, 8443, 9443},
		})
		if err != nil {
			t.Fatalf("plan: %v", err)
		}
		if !plan.Promoted {
			t.Fatalf("promoted = false for %d endpoints (threshold %d), want true",
				plan.TotalEndpoints, plan.Threshold)
		}
		if plan.RatePPS != 10 {
			t.Errorf("rate_pps = %d, want the sweep rate 10", plan.RatePPS)
		}
		if plan.TotalChunks < 1 {
			t.Errorf("total_chunks = %d, want >= 1 on the sweep path", plan.TotalChunks)
		}
		if plan.ETASeconds != float64(plan.TotalEndpoints)/10 {
			t.Errorf("eta = %v, want endpoints/rate", plan.ETASeconds)
		}
	})

	// all 프리셋은 거부가 아니라 승격이다 (예전에는 400이었다).
	t.Run("all preset promotes instead of failing", func(t *testing.T) {
		plan, err := srv.planScan(createScanRequest{
			Targets: []string{"10.9.0.1"}, PortSpec: "all",
		})
		if err != nil {
			t.Fatalf("plan: %v", err)
		}
		if !plan.Promoted || plan.TotalEndpoints != 65535 {
			t.Errorf("plan = {promoted:%v, endpoints:%d}, want {true, 65535}",
				plan.Promoted, plan.TotalEndpoints)
		}
	})

	t.Run("defaults fall back to the store constants", func(t *testing.T) {
		bare := newServer(nil, nil, nil, nil)
		plan, err := bare.planScan(createScanRequest{Targets: []string{"10.9.0.1"}})
		if err != nil {
			t.Fatalf("plan: %v", err)
		}
		if plan.Threshold != store.DefaultSweepChunkEndpoints {
			t.Errorf("threshold = %d, want %d", plan.Threshold, store.DefaultSweepChunkEndpoints)
		}
		if plan.MaxDays != store.DefaultSweepMaxDays {
			t.Errorf("max_days = %d, want %d", plan.MaxDays, store.DefaultSweepMaxDays)
		}
	})

	t.Run("invalid input is a scan input error", func(t *testing.T) {
		if _, err := srv.planScan(createScanRequest{Targets: []string{"not-an-ip"}}); !errors.Is(err, discovery.ErrInvalidScanInput) {
			t.Errorf("error = %v, want ErrInvalidScanInput", err)
		}
		if _, err := srv.planScan(createScanRequest{Targets: []string{"10.9.0.1"}, PortSpec: "8000-"}); !errors.Is(err, discovery.ErrInvalidScanInput) {
			t.Errorf("error = %v, want ErrInvalidScanInput", err)
		}
	})
}

// 스윕 행·미리보기에 실을 포트 사양 원문. 정수 목록 입력도 같은 문법으로 되돌아와야
// 청크 재생(port_spec 재파싱)이 가능하다.
func TestEffectivePortSpec(t *testing.T) {
	cases := []struct {
		spec  string
		ports []int
		want  string
	}{
		{" web ", nil, "web"},
		{"", []int{443, 8443}, "443,8443"},
		{"", nil, discovery.DefaultPortSpec},
		{"all", []int{443}, "all"},
	}
	for _, c := range cases {
		if got := effectivePortSpec(c.spec, c.ports); got != c.want {
			t.Errorf("effectivePortSpec(%q, %v) = %q, want %q", c.spec, c.ports, got, c.want)
		}
	}
}

func TestExpandSweepIPs(t *testing.T) {
	ips, err := expandSweepIPs([]string{"10.0.0.0/30", "10.0.0.0/30"})
	if err != nil {
		t.Fatalf("expand: %v", err)
	}
	if len(ips) != 4 {
		t.Fatalf("ips = %v, want 4 addresses (deduplicated)", ips)
	}
	if _, err := expandSweepIPs([]string{"not-an-ip"}); !errors.Is(err, discovery.ErrInvalidScanInput) {
		t.Errorf("error = %v, want ErrInvalidScanInput", err)
	}
}
