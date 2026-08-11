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
	// CipherSuite는 협상된 암호 스위트 이름이다(예 "TLS_AES_128_GCM_SHA256").
	// 이미 하는 핸드셰이크의 ConnectionState에서 그대로 읽으므로 **추가 프로브가 없다** —
	// 레이트·ETA에 영향을 주지 않아 항상 수집한다.
	CipherSuite string
	// SupportedTLSVersions는 서버가 지원한다고 확인된 프로토콜 버전 목록이다(오름차순).
	// **엔드포인트당 추가 핸드셰이크가 필요**해 옵트인이며(ScanOptions.ProbeVersions),
	// 끄면 nil이다. nil은 "지원 안 함"이 아니라 "탐지하지 않음(미상)"이다.
	SupportedTLSVersions []string
	Hostname             string
	Chain                []*x509.Certificate
}

// ScanOptions는 엔드포인트 1개 스캔의 선택 동작이다.
//
// === 레이트 리미터·스윕 ETA와의 충돌 (선행 검토 결론) ===
// 지원 버전 목록 탐지는 엔드포인트 1개에 핸드셰이크를 여러 번 건다. 그대로 두면
// 제품의 두 불변식이 조용히 깨진다.
//
//  1. RateLimiter는 "엔드포인트 1개 = dial 1회"를 전제로 dial 직전에 한 번만
//     Wait을 받는다(manager.scanAll). 즉 rate_pps는 곧 초당 dial 수다. 추가
//     핸드셰이크가 리미터를 안 거치면 회선에 나가는 실제 속도가 설정값의 최대
//     4배가 되고, 더 나쁘게도 그 4회가 **같은 (IP, 포트)에 연속으로** 꽂혀
//     InterleaveByHost가 억제하려던 포트스캔 탐지 패턴을 그대로 만든다.
//  2. ETA는 total_endpoints / rate_pps다(store.CreateSweep·api.planScan).
//     엔드포인트당 비용이 4배가 되면 ETA는 4배 낙관이 되고, 그 위에 세운
//     SCAN_SWEEP_MAX_DAYS(기본 30일) 상한이 실제 120일짜리 스윕을 통과시킨다.
//
// 해소: ① 추가 핸드셰이크는 **같은 리미터**를 거친다(Gate) — 비용이 추가 패킷이
// 아니라 엔드포인트 처리량 감소로 나타나므로 회선 속도는 설정값 그대로다.
// ② ETA·상한은 ProbesPerEndpoint 배수를 곱해 계산한다(api·store 양쪽).
// 그래도 켜면 스윕 소요가 최대 4배가 되어 기존에 통과하던 요청이 422로 바뀐다 —
// 그래서 **기본 off의 옵트인**이다(SCAN_TLS_PROBE_VERSIONS).
type ScanOptions struct {
	// ProbeVersions가 true면 협상 버전보다 낮은 버전을 하나씩 다시 시도해
	// 서버가 지원하는 버전 목록을 만든다. 기본은 false다.
	ProbeVersions bool
	// Gate는 **추가** 프로브를 보내기 직전에 호출하는 속도 게이트다
	// (RateLimiter.Wait를 넘긴다). 첫 프로브는 호출자가 이미 게이트를 거친 뒤
	// 이 함수를 부르므로 여기서 다시 걸지 않는다. nil이면 무제한이다.
	Gate func(context.Context) error
}

// probeVersionOrder는 지원 버전 탐지가 훑는 순서다(낮은 것부터). 값 자체는
// 관측 대상의 범위이지 우리 연결의 보안 등급이 아니다(아래 MinVersion 주석과 같은 이유).
var probeVersionOrder = []uint16{
	tls.VersionTLS10, tls.VersionTLS11, tls.VersionTLS12, tls.VersionTLS13,
}

// ProbesPerEndpoint는 엔드포인트 1개가 회선에 내보내는 dial 수의 **상한**이다.
// 레이트·ETA 계산의 배수이며, 지원 버전 탐지를 켜면 len(probeVersionOrder)다
// (첫 핸드셰이크가 최고 버전을 확정하므로 나머지는 그보다 낮은 버전 수 = 최대 3회.
// 도달 불가 엔드포인트는 추가 프로브가 0회다 — 상한이지 평균이 아니다).
func ProbesPerEndpoint(probeVersions bool) int {
	if !probeVersions {
		return 1
	}
	return len(probeVersionOrder)
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
	return ScanEndpointWithOptions(ctx, ep, timeout, ScanOptions{})
}

// ScanEndpointWithOptions는 ScanEndpoint에 선택 동작(ScanOptions)을 더한 형태다.
// 옵션이 제로값이면 ScanEndpoint와 완전히 같게 동작한다(프로브 1회).
func ScanEndpointWithOptions(ctx context.Context, ep Endpoint, timeout time.Duration, opts ScanOptions) Observation {
	obs := Observation{Endpoint: ep}

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
	//
	// MaxVersion은 지정하지 않는다 — Go 클라이언트 기본값(TLS 1.3)까지 제시하므로
	// 서버는 자기가 지원하는 **최고** 버전을 고른다. 지원 버전 탐지가 협상 버전보다
	// 위를 다시 물어보지 않아도 되는 근거가 이것이다.
	state, ok := handshake(ctx, ep, timeout, &tls.Config{ //nolint:gosec // 수집 전용, 신뢰 판단 아님 — 대상 범위이지 우리 연결의 보안 등급이 아님
		InsecureSkipVerify: true,
		MinVersion:         tls.VersionTLS10,
	})
	if !ok {
		return obs // 도달 불가 / TLS 아님 / 핸드셰이크 실패 — 오류 내용은 남기지 않는다.
	}

	obs.Reachable = true
	obs.TLSVersion = tlsVersionName(state.Version)
	// tls.CipherSuiteName은 표준 라이브러리 구현이다(자체 암호·자체 표기 금지, 규칙 2).
	// 미등록 ID는 "0x____" 형태 문자열을 돌려주므로 빈 값이 되지 않는다.
	obs.CipherSuite = tls.CipherSuiteName(state.CipherSuite)
	obs.Chain = state.PeerCertificates

	if opts.ProbeVersions {
		obs.SupportedTLSVersions = probeSupportedVersions(ctx, ep, timeout, state.Version, opts.Gate)
	}

	// 호스트 이름은 스펙 7.1의 수집 항목이다. ConnectionState().ServerName은
	// 클라이언트가 보낸 SNI(우리는 안 보냄)라 항상 비므로 쓰지 않는다.
	obs.Hostname = resolveHostname(ctx, ep.IP, obs.Chain)
	return obs
}

// handshake는 dial + TLS 핸드셰이크 1회다. 실패는 ok=false이며 오류 내용을
// 반환하지 않는다 — 핸드셰이크 오류 문자열은 인증서 정보를 담을 수 있다 (보안 하드 규칙 3).
// 연결은 언제나 이 함수 안에서 닫힌다.
func handshake(ctx context.Context, ep Endpoint, timeout time.Duration, conf *tls.Config) (tls.ConnectionState, bool) {
	addr := net.JoinHostPort(ep.IP, strconv.Itoa(ep.Port))
	dialer := net.Dialer{Timeout: timeout}
	raw, err := dialer.DialContext(ctx, "tcp", addr)
	if err != nil {
		return tls.ConnectionState{}, false // TCP 도달 불가 — 전체 Job은 계속.
	}
	tconn := tls.Client(raw, conf)
	_ = raw.SetDeadline(time.Now().Add(timeout))
	if err := tconn.HandshakeContext(ctx); err != nil {
		_ = raw.Close()
		return tls.ConnectionState{}, false
	}
	state := tconn.ConnectionState()
	_ = tconn.Close()
	return state, true
}

// probeSupportedVersions는 서버가 지원하는 TLS 버전 목록을 만든다(오름차순).
//
// 첫 핸드셰이크가 이미 1.0~1.3을 제시했으므로 negotiated는 서버가 지원하는
// **최고** 버전이다 — 그보다 위는 물어볼 필요가 없다. 남은 것은 아래 버전들뿐이라
// 추가 핸드셰이크는 최대 3회다(negotiated가 TLS 1.0이면 0회).
//
// 각 추가 프로브는 gate(= RateLimiter.Wait)를 먼저 통과한다. 통과하지 못하면
// (ctx 취소) **부분 목록 대신 nil**을 돌려준다 — "TLS 1.2가 목록에 없다"와
// "거기까지 못 물어봤다"를 섞으면 목록이 거짓말을 한다.
func probeSupportedVersions(ctx context.Context, ep Endpoint, timeout time.Duration,
	negotiated uint16, gate func(context.Context) error) []string {
	idx := -1
	for i, v := range probeVersionOrder {
		if v == negotiated {
			idx = i
			break
		}
	}
	if idx < 0 {
		return []string{tlsVersionName(negotiated)} // 알 수 없는 버전 — 관측한 사실만 남긴다.
	}

	supported := make([]string, 0, idx+1)
	for _, v := range probeVersionOrder[:idx] {
		if gate != nil {
			if err := gate(ctx); err != nil {
				return nil
			}
		}
		if _, ok := handshake(ctx, ep, timeout, &tls.Config{ //nolint:gosec // 수집 전용 — 대상이 이 버전을 받아 주는지만 확인한다
			InsecureSkipVerify: true,
			MinVersion:         v,
			MaxVersion:         v,
		}); ok {
			supported = append(supported, tlsVersionName(v))
		}
		if ctx.Err() != nil {
			return nil
		}
	}
	return append(supported, tlsVersionName(negotiated))
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

// TLSVersionNames는 스캐너가 기록하는 프로토콜 버전 표기의 **정본 목록**이다(오름차순).
// certificate_locations.tls_version·supported_tls_versions에 들어가는 값이 이것이며,
// 목록 필터(tls_version=)의 검증도 여기서 나온다 — 표기가 두 군데로 갈라지면
// 화면의 값과 필터가 조용히 어긋난다.
func TLSVersionNames() []string {
	out := make([]string, 0, len(probeVersionOrder))
	for _, v := range probeVersionOrder {
		out = append(out, tlsVersionName(v))
	}
	return out
}

// IsTLSVersionName은 name이 TLSVersionNames의 값 중 하나인지 반환한다.
func IsTLSVersionName(name string) bool {
	for _, v := range probeVersionOrder {
		if tlsVersionName(v) == name {
			return true
		}
	}
	return false
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
