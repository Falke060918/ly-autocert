package api

import (
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/Falke060918/INHERIT_AutoCert/backend/internal/discovery"
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

	t.Run("all preset is rejected for single scans", func(t *testing.T) {
		for _, spec := range []string{"all", "web,all", " ALL "} {
			if _, err := resolveScanPorts(spec, nil); !errors.Is(err, discovery.ErrInvalidScanInput) {
				t.Errorf("resolveScanPorts(%q) error = %v, want ErrInvalidScanInput", spec, err)
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
