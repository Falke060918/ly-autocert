package discovery

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"errors"
	"math/big"
	"net"
	"net/http"
	"net/http/httptest"
	"strconv"
	"testing"
	"time"
)

func TestExpand(t *testing.T) {
	tests := []struct {
		name      string
		targets   []string
		ports     []int
		wantCount int
		wantErr   bool
	}{
		{name: "single ip one port", targets: []string{"127.0.0.1"}, ports: []int{443}, wantCount: 1},
		{name: "cidr /30 two ports", targets: []string{"10.0.0.0/30"}, ports: []int{443, 8443}, wantCount: 8}, // 4 addr × 2
		{name: "dedup overlapping", targets: []string{"127.0.0.1", "127.0.0.1"}, ports: []int{443}, wantCount: 1},
		{name: "no ports", targets: []string{"127.0.0.1"}, ports: nil, wantErr: true},
		{name: "bad port", targets: []string{"127.0.0.1"}, ports: []int{0}, wantErr: true},
		{name: "bad cidr", targets: []string{"10.0.0.0/notanetwork"}, ports: []int{443}, wantErr: true},
		{name: "bad ip", targets: []string{"not-an-ip"}, ports: []int{443}, wantErr: true},
		{name: "cidr too large", targets: []string{"10.0.0.0/8"}, ports: []int{443}, wantErr: true},
		{name: "empty targets", targets: []string{"", "  "}, ports: []int{443}, wantErr: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			eps, err := Expand(tt.targets, tt.ports)
			if tt.wantErr {
				if err == nil {
					t.Fatalf("Expand(%v, %v) = nil error, want error", tt.targets, tt.ports)
				}
				if !errors.Is(err, ErrInvalidScanInput) {
					t.Errorf("error = %v, want wrapped ErrInvalidScanInput", err)
				}
				return
			}
			if err != nil {
				t.Fatalf("Expand: unexpected error: %v", err)
			}
			if len(eps) != tt.wantCount {
				t.Errorf("endpoint count = %d, want %d", len(eps), tt.wantCount)
			}
		})
	}
}

// TestScanEndpointCollectsCertificate는 실제 TLS 서버(자기서명)를 스캔해
// 인증서를 수집하는지 확인한다. InsecureSkipVerify 경로가 자기서명도 수집함을 검증한다.
func TestScanEndpointCollectsCertificate(t *testing.T) {
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}))
	defer srv.Close()

	host, portStr, err := net.SplitHostPort(srv.Listener.Addr().String())
	if err != nil {
		t.Fatalf("split host port: %v", err)
	}
	port, _ := strconv.Atoi(portStr)

	obs := ScanEndpoint(context.Background(), Endpoint{IP: host, Port: port}, 3*time.Second)
	if !obs.Reachable {
		t.Fatal("expected reachable TLS endpoint")
	}
	if len(obs.Chain) == 0 {
		t.Fatal("expected at least one certificate in chain")
	}
	if obs.TLSVersion == "" {
		t.Error("expected TLS version to be set")
	}
}

// newTestServerCert는 테스트 전용 자기서명 인증서를 매 실행마다 새로 만든다.
// 키·인증서를 저장소에 커밋하지 않기 위한 것이다 (보안 하드 규칙 4).
func newTestServerCert(t *testing.T) tls.Certificate {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatalf("generate test key: %v", err)
	}
	tmpl := x509.Certificate{
		SerialNumber: big.NewInt(1),
		Subject:      pkix.Name{CommonName: "legacy-tls.example.test"},
		NotBefore:    time.Now().Add(-time.Hour),
		NotAfter:     time.Now().Add(time.Hour),
		KeyUsage:     x509.KeyUsageDigitalSignature,
		ExtKeyUsage:  []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
		IPAddresses:  []net.IP{net.ParseIP("127.0.0.1")},
	}
	der, err := x509.CreateCertificate(rand.Reader, &tmpl, &tmpl, &key.PublicKey, key)
	if err != nil {
		t.Fatalf("create test certificate: %v", err)
	}
	return tls.Certificate{Certificate: [][]byte{der}, PrivateKey: key}
}

// startPinnedVersionTLSServer는 딱 한 가지 TLS 버전만 받아들이는 테스트 서버를 띄운다.
// 구형 장비(TLS 1.0/1.1 전용)를 흉내내기 위한 것이다.
// 주의: Go 서버는 MinVersion을 명시하지 않으면 TLS 1.2 미만을 거부한다 —
// Min/Max를 모두 같은 값으로 고정해야 1.0/1.1 전용 서버가 된다.
func startPinnedVersionTLSServer(t *testing.T, version uint16) (string, int) {
	t.Helper()
	conf := &tls.Config{
		Certificates: []tls.Certificate{newTestServerCert(t)},
		MinVersion:   version,
		MaxVersion:   version,
	}
	ln, err := tls.Listen("tcp", "127.0.0.1:0", conf)
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	t.Cleanup(func() { _ = ln.Close() })

	go func() {
		for {
			conn, err := ln.Accept()
			if err != nil {
				return // 리스너 종료.
			}
			go func() {
				defer conn.Close()
				if tc, ok := conn.(*tls.Conn); ok {
					_ = tc.Handshake() // 실패는 클라이언트 쪽 검증 대상이다.
				}
			}()
		}
	}()

	host, portStr, err := net.SplitHostPort(ln.Addr().String())
	if err != nil {
		t.Fatalf("split host port: %v", err)
	}
	port, _ := strconv.Atoi(portStr)
	return host, port
}

// TestScanEndpointCollectsLegacyTLSVersions는 TLS 1.0/1.1 전용 서버에서도 인증서가
// 수집되는지 확인한다(회귀 고정). 스캐너 tls.Config에 MinVersion이 없으면 Go 클라이언트
// 기본값이 TLS 1.2라 구형 장비가 조용히 Reachable=false로 빠진다 — 폐쇄망 레거시 장비의
// 인증서가 통째로 누락되던 결함이다. 1.2/1.3도 같은 표에서 회귀 확인한다.
func TestScanEndpointCollectsLegacyTLSVersions(t *testing.T) {
	tests := []struct {
		name    string
		version uint16
		want    string
	}{
		{name: "tls 1.0 only server", version: tls.VersionTLS10, want: "TLS 1.0"},
		{name: "tls 1.1 only server", version: tls.VersionTLS11, want: "TLS 1.1"},
		{name: "tls 1.2 only server", version: tls.VersionTLS12, want: "TLS 1.2"},
		{name: "tls 1.3 only server", version: tls.VersionTLS13, want: "TLS 1.3"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			host, port := startPinnedVersionTLSServer(t, tt.version)

			obs := ScanEndpoint(context.Background(), Endpoint{IP: host, Port: port}, 3*time.Second)
			if !obs.Reachable {
				t.Fatalf("Reachable = false, want true (%s 전용 서버의 인증서가 누락된다)", tt.want)
			}
			if len(obs.Chain) == 0 {
				t.Fatal("expected at least one certificate in chain")
			}
			if obs.TLSVersion != tt.want {
				t.Errorf("TLSVersion = %q, want %q", obs.TLSVersion, tt.want)
			}
		})
	}
}

// TestScanEndpointUnreachable는 닫힌 포트가 Reachable=false로 처리되는지 확인한다.
func TestScanEndpointUnreachable(t *testing.T) {
	// 리스너를 열었다가 즉시 닫아 확실히 닫힌 포트를 얻는다.
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	addr := ln.Addr().String()
	ln.Close()

	_, portStr, _ := net.SplitHostPort(addr)
	port, _ := strconv.Atoi(portStr)

	obs := ScanEndpoint(context.Background(), Endpoint{IP: "127.0.0.1", Port: port}, 500*time.Millisecond)
	if obs.Reachable {
		t.Error("expected unreachable for closed port")
	}
	if len(obs.Chain) != 0 {
		t.Error("expected no certificates from closed port")
	}
}

// TestResolveHostname은 호스트 이름 수집(스펙 7.1)을 검증한다:
// 역방향 DNS 성공 시 그 이름(마침표 제거), 실패 시 리프 인증서 CN 폴백,
// 둘 다 없으면 빈 문자열. lookupAddr 훅으로 실제 DNS 없이 검증한다.
func TestResolveHostname(t *testing.T) {
	orig := lookupAddr
	t.Cleanup(func() { lookupAddr = orig })

	leaf := &x509.Certificate{Subject: pkix.Name{CommonName: "cn-fallback.example.test"}}

	t.Run("reverse dns wins", func(t *testing.T) {
		lookupAddr = func(ctx context.Context, ip string) ([]string, error) {
			return []string{"rdns.example.test."}, nil
		}
		if got := resolveHostname(context.Background(), "203.0.113.10", []*x509.Certificate{leaf}); got != "rdns.example.test" {
			t.Errorf("hostname = %q, want %q", got, "rdns.example.test")
		}
	})

	t.Run("falls back to leaf CN on lookup failure", func(t *testing.T) {
		lookupAddr = func(ctx context.Context, ip string) ([]string, error) {
			return nil, errors.New("no reverse zone")
		}
		if got := resolveHostname(context.Background(), "203.0.113.10", []*x509.Certificate{leaf}); got != "cn-fallback.example.test" {
			t.Errorf("hostname = %q, want CN fallback", got)
		}
	})

	t.Run("empty when no rdns and no chain", func(t *testing.T) {
		lookupAddr = func(ctx context.Context, ip string) ([]string, error) {
			return nil, errors.New("no reverse zone")
		}
		if got := resolveHostname(context.Background(), "203.0.113.10", nil); got != "" {
			t.Errorf("hostname = %q, want empty", got)
		}
	})
}
