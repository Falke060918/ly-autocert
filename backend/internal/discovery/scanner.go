// Package discovery는 에이전트리스 네트워크 TLS 스캔을 담당한다 (docs/00-spec.md 7.1).
// 대상 서버에 아무것도 설치하지 않고 crypto/tls 핸드셰이크만으로 서버가 제시하는
// 인증서 체인을 수집해 인벤토리에 반영한다 (docs/STATE.md: Phase 1 스캔 전략).
//
// 보안: 인증서·개인키 바이트를 로그·에러 메시지에 남기지 않는다 (보안 하드 규칙 3).
// 스캔은 표준 라이브러리 crypto/tls·crypto/x509만 사용한다 (자체 암호 구현 금지, 규칙 2).
package discovery

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"errors"
	"fmt"
	"net"
	"net/netip"
	"strconv"
	"strings"
	"time"
)

// ErrInvalidScanInput은 잘못된 스캔 입력(형식 오류·범위 초과·대상 없음)을 감싼다.
// 핸들러는 이 오류를 400으로 매핑한다.
var ErrInvalidScanInput = errors.New("discovery: invalid scan input")

// MaxEndpoints는 한 스캔이 다룰 수 있는 (IP, 포트) 조합 상한이다.
// 실수로 거대한 CIDR을 넣어 무한정 스캔하는 것을 막는다.
const MaxEndpoints = 65536

// maxCIDRHostBits는 허용하는 CIDR 호스트 비트 상한이다(2^20 ≈ 100만 주소).
// 그 이상은 Expand 단계에서 즉시 거부해 주소 열거 자체를 막는다.
const maxCIDRHostBits = 20

// Endpoint는 스캔 대상 1개(IP + 포트)다.
type Endpoint struct {
	IP   string
	Port int
}

// Observation은 엔드포인트 1개의 스캔 결과다.
// Reachable은 "TLS 핸드셰이크에 성공해 인증서를 얻었는가"를 뜻한다 —
// TCP는 열렸지만 TLS가 아니거나 핸드셰이크가 실패하면 false다.
type Observation struct {
	Endpoint
	Reachable  bool
	TLSVersion string
	Hostname   string
	Chain      []*x509.Certificate
}

// Expand는 IP/CIDR 목록과 포트 목록을 (IP, 포트) 엔드포인트 목록으로 펼친다.
// 잘못된 입력은 ErrInvalidScanInput으로 감싸 반환한다. 중복 IP는 제거한다.
func Expand(targets []string, ports []int) ([]Endpoint, error) {
	if len(ports) == 0 {
		return nil, fmt.Errorf("%w: 포트를 하나 이상 지정하십시오", ErrInvalidScanInput)
	}
	for _, p := range ports {
		if p < 1 || p > 65535 {
			return nil, fmt.Errorf("%w: 포트 범위 초과 (%d)", ErrInvalidScanInput, p)
		}
	}

	var ips []string
	seen := make(map[string]bool)
	add := func(s string) {
		if !seen[s] {
			seen[s] = true
			ips = append(ips, s)
		}
	}

	for _, raw := range targets {
		t := strings.TrimSpace(raw)
		if t == "" {
			continue
		}
		if strings.Contains(t, "/") {
			prefix, err := netip.ParsePrefix(t)
			if err != nil {
				return nil, fmt.Errorf("%w: CIDR 형식 오류 (%q)", ErrInvalidScanInput, t)
			}
			hostBits := prefix.Addr().BitLen() - prefix.Bits()
			if hostBits > maxCIDRHostBits {
				return nil, fmt.Errorf("%w: CIDR 범위가 너무 큽니다 (%q)", ErrInvalidScanInput, t)
			}
			prefix = prefix.Masked()
			for addr := prefix.Addr(); prefix.Contains(addr); addr = addr.Next() {
				add(addr.String())
			}
		} else {
			addr, err := netip.ParseAddr(t)
			if err != nil {
				return nil, fmt.Errorf("%w: IP 형식 오류 (%q)", ErrInvalidScanInput, t)
			}
			add(addr.String())
		}
	}

	if len(ips) == 0 {
		return nil, fmt.Errorf("%w: 스캔할 대상이 없습니다", ErrInvalidScanInput)
	}

	endpoints := make([]Endpoint, 0, len(ips)*len(ports))
	for _, ip := range ips {
		for _, p := range ports {
			endpoints = append(endpoints, Endpoint{IP: ip, Port: p})
		}
	}
	if len(endpoints) > MaxEndpoints {
		return nil, fmt.Errorf("%w: 엔드포인트 수 초과 (%d > %d)", ErrInvalidScanInput, len(endpoints), MaxEndpoints)
	}
	return endpoints, nil
}

// ScanEndpoint는 엔드포인트 1개에 TLS 핸드셰이크를 시도해 서버 인증서 체인을 수집한다.
// 도달 불가·타임아웃·비-TLS 포트는 Reachable=false로 조용히 처리한다 —
// 개별 실패는 전체 Job을 멈추지 않는다. 핸드셰이크 오류는 인증서 정보를 담을 수 있어
// 로그·반환값에 넣지 않는다 (보안 하드 규칙 3).
func ScanEndpoint(ctx context.Context, ep Endpoint, timeout time.Duration) Observation {
	obs := Observation{Endpoint: ep}
	addr := net.JoinHostPort(ep.IP, strconv.Itoa(ep.Port))

	dialer := net.Dialer{Timeout: timeout}
	raw, err := dialer.DialContext(ctx, "tcp", addr)
	if err != nil {
		return obs // TCP 도달 불가 — 실패 기록, 전체 Job은 계속.
	}

	// InsecureSkipVerify: 인벤토리 수집 목적이므로 자기서명·만료·이름 불일치 인증서도
	// 모두 수집한다. 검증(신뢰 판단)을 하지 않으며 이 결과를 신뢰 결정에 쓰지 않는다.
	// 수집 전용임 — 스펙 7.1은 "발견된 모든 인증서"를 인벤토리 대상으로 본다.
	//
	// MinVersion=TLS 1.0: 이것은 **우리 제품의 보안 설정이 아니라 관측 대상의 범위**다.
	// Go 클라이언트 기본값(TLS 1.2)을 그대로 두면 TLS 1.0/1.1만 지원하는 레거시 장비가
	// 핸드셰이크 실패로 Reachable=false가 되어 인증서가 조용히 누락된다 — 폐쇄망·레거시
	// 환경이 이 제품의 대상이고, 그런 서버의 인증서야말로 관리 사각지대다.
	// 스캐너는 인증서를 읽기만 하며 이 연결로 비밀을 주고받지 않는다. 이 tls.Config는
	// 스캔 전용이고 제품의 다른 TLS 경로(웹훅·SMTP·센서 mTLS)와 공유하지 않는다 —
	// 그쪽은 각자 MinVersion=TLS 1.2를 유지한다.
	conf := &tls.Config{ //nolint:gosec // 수집 전용, 신뢰 판단 아님 — 대상 범위이지 우리 연결의 보안 등급이 아님
		InsecureSkipVerify: true,
		MinVersion:         tls.VersionTLS10,
	}
	tconn := tls.Client(raw, conf)
	_ = raw.SetDeadline(time.Now().Add(timeout))
	if err := tconn.HandshakeContext(ctx); err != nil {
		_ = raw.Close()
		return obs // TLS 아님/핸드셰이크 실패 — 수집 없음. 오류 내용은 남기지 않는다.
	}

	state := tconn.ConnectionState()
	obs.Reachable = true
	obs.TLSVersion = tlsVersionName(state.Version)
	obs.Chain = state.PeerCertificates
	_ = tconn.Close()

	// 호스트 이름은 스펙 7.1의 수집 항목이다. ConnectionState().ServerName은
	// 클라이언트가 보낸 SNI(우리는 안 보냄)라 항상 비므로 쓰지 않는다.
	obs.Hostname = resolveHostname(ctx, ep.IP, obs.Chain)
	return obs
}

// hostnameLookupTimeout은 역방향 DNS 조회 상한이다. best-effort —
// 폐쇄망에는 역방향 존이 없는 경우가 많아 짧게 자르고 폴백한다.
const hostnameLookupTimeout = 1 * time.Second

// lookupAddr는 역방향 DNS 조회 함수다. 테스트에서 대체한다.
var lookupAddr = func(ctx context.Context, ip string) ([]string, error) {
	return net.DefaultResolver.LookupAddr(ctx, ip)
}

// resolveHostname은 역방향 DNS(짧은 타임아웃) → 실패 시 리프 인증서 CN 폴백으로
// 호스트 이름을 구한다. 둘 다 없으면 빈 문자열(대상 hostname은 NULL 허용).
// PeerCertificates[0]은 TLS 규약상 리프다.
func resolveHostname(ctx context.Context, ip string, chain []*x509.Certificate) string {
	lctx, cancel := context.WithTimeout(ctx, hostnameLookupTimeout)
	defer cancel()
	if names, err := lookupAddr(lctx, ip); err == nil && len(names) > 0 && names[0] != "" {
		return strings.TrimSuffix(names[0], ".")
	}
	if len(chain) > 0 {
		return chain[0].Subject.CommonName
	}
	return ""
}

// tlsVersionName은 협상된 TLS 버전을 사람이 읽는 이름으로 바꾼다.
func tlsVersionName(v uint16) string {
	switch v {
	case tls.VersionTLS10:
		return "TLS 1.0"
	case tls.VersionTLS11:
		return "TLS 1.1"
	case tls.VersionTLS12:
		return "TLS 1.2"
	case tls.VersionTLS13:
		return "TLS 1.3"
	default:
		return fmt.Sprintf("unknown (0x%04x)", v)
	}
}
