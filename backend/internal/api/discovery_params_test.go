package api

import (
	"errors"
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
