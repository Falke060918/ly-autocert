package config

import (
	"net"
	"testing"
)

// setRequired는 Load가 요구하는 필수 환경변수를 채운다.
func setRequired(t *testing.T) {
	t.Helper()
	t.Setenv("DATABASE_URL", "postgres://u:p@localhost:5432/db?sslmode=disable")
}

func TestLoadSensorListenAddrDefault(t *testing.T) {
	setRequired(t)
	t.Setenv("SENSOR_LISTEN_ADDR", "")
	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.SensorListenAddr != ":8443" {
		t.Fatalf("SensorListenAddr = %q, want %q", cfg.SensorListenAddr, ":8443")
	}
}

func TestLoadSensorListenAddrOverride(t *testing.T) {
	setRequired(t)
	t.Setenv("SENSOR_LISTEN_ADDR", ":9443")
	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.SensorListenAddr != ":9443" {
		t.Fatalf("SensorListenAddr = %q, want %q", cfg.SensorListenAddr, ":9443")
	}
}

func TestLoadSensorAdvertisedHosts(t *testing.T) {
	setRequired(t)
	// 콤마 구분, 공백 허용, DNS와 IP 혼합 — IP는 SAN IP로, 나머지는 DNS로 분류.
	t.Setenv("SENSOR_ADVERTISED_HOSTS", "autocert.internal, 10.0.0.5 ,gw.example.com,")
	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	wantDNS := []string{"autocert.internal", "gw.example.com"}
	if len(cfg.SensorAdvertisedDNS) != len(wantDNS) {
		t.Fatalf("SensorAdvertisedDNS = %v, want %v", cfg.SensorAdvertisedDNS, wantDNS)
	}
	for i, d := range wantDNS {
		if cfg.SensorAdvertisedDNS[i] != d {
			t.Fatalf("SensorAdvertisedDNS = %v, want %v", cfg.SensorAdvertisedDNS, wantDNS)
		}
	}
	if len(cfg.SensorAdvertisedIPs) != 1 || !cfg.SensorAdvertisedIPs[0].Equal(net.ParseIP("10.0.0.5")) {
		t.Fatalf("SensorAdvertisedIPs = %v, want [10.0.0.5]", cfg.SensorAdvertisedIPs)
	}
}

func TestLoadSensorAdvertisedHostsEmpty(t *testing.T) {
	setRequired(t)
	t.Setenv("SENSOR_ADVERTISED_HOSTS", "")
	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if len(cfg.SensorAdvertisedDNS) != 0 || len(cfg.SensorAdvertisedIPs) != 0 {
		t.Fatalf("advertised hosts should be empty, got dns=%v ips=%v",
			cfg.SensorAdvertisedDNS, cfg.SensorAdvertisedIPs)
	}
}
