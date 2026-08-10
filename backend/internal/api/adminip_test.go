package api

// 접근 IP 제한의 순수 로직 검증 (DB 불필요).
// 판정 규칙 3개를 여기서 고정한다:
//   - 출발지 판정: 신뢰 프록시가 아니면 XFF를 절대 보지 않는다.
//   - 허용 판정: 활성 규칙 0건이면 전체 허용, 비상 우회 대역은 규칙과 무관하게 통과.
//   - CIDR 정규화: 단일 IP는 /32·/128, 호스트 비트가 켜진 값은 조용히 깎지 않고 거부.

import (
	"context"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

// mustCIDRs는 테스트 픽스처용 대역 목록을 만든다.
func mustCIDRs(t *testing.T, specs ...string) []*net.IPNet {
	t.Helper()
	var nets []*net.IPNet
	for _, s := range specs {
		_, n, err := net.ParseCIDR(s)
		if err != nil {
			t.Fatalf("parse fixture cidr %q: %v", s, err)
		}
		nets = append(nets, n)
	}
	return nets
}

func TestResolveClientIPTrustsForwardedForOnlyBehindTrustedProxy(t *testing.T) {
	tests := []struct {
		name       string
		trusted    []string
		remoteAddr string
		xff        []string
		want       string
	}{
		{
			name:       "신뢰 프록시 미설정이면 XFF를 무시한다",
			remoteAddr: "10.9.9.9:5555",
			xff:        []string{"203.0.113.7"},
			want:       "10.9.9.9",
		},
		{
			name:       "신뢰 프록시가 아닌 직접 접속의 XFF도 무시한다",
			trusted:    []string{"172.20.0.0/16"},
			remoteAddr: "10.9.9.9:5555",
			xff:        []string{"203.0.113.7"},
			want:       "10.9.9.9",
		},
		{
			name:       "신뢰 프록시 뒤에서는 XFF의 마지막 비신뢰 값을 쓴다",
			trusted:    []string{"172.20.0.0/16"},
			remoteAddr: "172.20.0.5:5555",
			xff:        []string{"203.0.113.7, 172.20.0.9"},
			want:       "203.0.113.7",
		},
		{
			name:       "헤더가 여러 번 와도 하나로 이어 본다",
			trusted:    []string{"172.20.0.0/16"},
			remoteAddr: "172.20.0.5:5555",
			xff:        []string{"198.51.100.4", "172.20.0.9"},
			want:       "198.51.100.4",
		},
		{
			name:       "XFF가 전부 신뢰 프록시면 직접 접속 IP를 쓴다",
			trusted:    []string{"172.20.0.0/16"},
			remoteAddr: "172.20.0.5:5555",
			xff:        []string{"172.20.0.9"},
			want:       "172.20.0.5",
		},
		{
			name:       "XFF에 손상된 값이 섞이면 판정 불가다",
			trusted:    []string{"172.20.0.0/16"},
			remoteAddr: "172.20.0.5:5555",
			xff:        []string{"not-an-ip, 172.20.0.9"},
			want:       "",
		},
		{
			name:       "XFF가 없으면 직접 접속 IP를 쓴다",
			trusted:    []string{"172.20.0.0/16"},
			remoteAddr: "172.20.0.5:5555",
			want:       "172.20.0.5",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			g := newAdminIPGuard(nil)
			g.access = AdminIPAccess{TrustedProxies: mustCIDRs(t, tt.trusted...)}

			r := httptest.NewRequest(http.MethodGet, "/api/v1/certificates", nil)
			r.RemoteAddr = tt.remoteAddr
			for _, v := range tt.xff {
				r.Header.Add("X-Forwarded-For", v)
			}

			got := ""
			if ip := g.resolveClientIP(r); ip != nil {
				got = ip.String()
			}
			if got != tt.want {
				t.Errorf("resolveClientIP = %q, want %q", got, tt.want)
			}
		})
	}
}

// 캐시가 채워진 상태에서의 허용 판정. store를 건드리지 않도록 스냅샷을 직접 심는다.
func TestAdminIPGuardAllow(t *testing.T) {
	tests := []struct {
		name      string
		rules     []string
		bootstrap []string
		ip        string
		want      bool
	}{
		{name: "규칙이 없으면 전체 허용", ip: "203.0.113.7", want: true},
		{name: "규칙이 없으면 판정 불가 IP도 허용", ip: "", want: true},
		{name: "허용 대역 안", rules: []string{"10.0.0.0/8"}, ip: "10.1.2.3", want: true},
		{name: "허용 대역 밖", rules: []string{"10.0.0.0/8"}, ip: "203.0.113.7", want: false},
		{
			name:  "단일 호스트 규칙은 그 주소만",
			rules: []string{"10.1.2.3/32"}, ip: "10.1.2.4", want: false,
		},
		{
			name:  "제한이 걸린 서버에서 판정 불가 IP는 차단",
			rules: []string{"10.0.0.0/8"}, ip: "", want: false,
		},
		{
			name:      "비상 우회 대역은 규칙과 무관하게 통과",
			rules:     []string{"10.0.0.0/8"},
			bootstrap: []string{"192.0.2.0/24"},
			ip:        "192.0.2.10", want: true,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			g := newAdminIPGuard(nil)
			g.access = AdminIPAccess{Bootstrap: mustCIDRs(t, tt.bootstrap...)}
			// 캐시를 유효한 상태로 심어 DB 조회 경로를 타지 않게 한다.
			g.nets, g.loaded, g.loadedAt = mustCIDRs(t, tt.rules...), true, time.Now()

			got, err := g.allow(context.Background(), net.ParseIP(tt.ip))
			if err != nil {
				t.Fatalf("allow: %v", err)
			}
			if got != tt.want {
				t.Errorf("allow(%q) = %v, want %v", tt.ip, got, tt.want)
			}
		})
	}
}

// invalidate 후에는 캐시를 다시 읽어야 한다 — 재기동 없이 반영되는 지점이다.
func TestAdminIPGuardInvalidateDropsSnapshot(t *testing.T) {
	g := newAdminIPGuard(nil)
	g.nets, g.loaded, g.loadedAt = mustCIDRs(t, "10.0.0.0/8"), true, time.Now()

	g.invalidate()
	if g.loaded {
		t.Fatal("invalidate did not drop the cached snapshot")
	}
}

func TestNormalizeAdminIPCIDR(t *testing.T) {
	tests := []struct {
		in      string
		want    string
		wantErr string // 오류 메시지에 반드시 포함될 조각
	}{
		{in: "10.0.0.5", want: "10.0.0.5/32"},
		{in: "  10.0.0.5  ", want: "10.0.0.5/32"},
		{in: "10.0.0.0/8", want: "10.0.0.0/8"},
		{in: "2001:db8::1", want: "2001:db8::1/128"},
		{in: "2001:db8::/32", want: "2001:db8::/32"},
		{in: "", wantErr: "필요합니다"},
		{in: "example.com", wantErr: "CIDR 대역이 아닙니다"},
		{in: "10.0.0.0/99", wantErr: "CIDR 대역이 아닙니다"},
		// 호스트 비트가 켜진 값은 조용히 깎지 않고, 두 선택지를 모두 알려준다.
		{in: "192.168.1.5/24", wantErr: "192.168.1.0/24"},
	}
	for _, tt := range tests {
		t.Run(tt.in, func(t *testing.T) {
			got, err := normalizeAdminIPCIDR(tt.in)
			if tt.wantErr != "" {
				if err == nil {
					t.Fatalf("normalizeAdminIPCIDR(%q) = %q, want error", tt.in, got)
				}
				if !strings.Contains(err.Error(), tt.wantErr) {
					t.Errorf("error = %q, want it to contain %q", err.Error(), tt.wantErr)
				}
				return
			}
			if err != nil {
				t.Fatalf("normalizeAdminIPCIDR(%q): %v", tt.in, err)
			}
			if got != tt.want {
				t.Errorf("normalizeAdminIPCIDR(%q) = %q, want %q", tt.in, got, tt.want)
			}
		})
	}
}

// 호스트 비트가 켜진 입력의 오류 문구는 단일 호스트 선택지도 함께 제시해야 한다.
func TestNormalizeAdminIPCIDRHostBitsMessageOffersBothChoices(t *testing.T) {
	_, err := normalizeAdminIPCIDR("192.168.1.5/24")
	if err == nil {
		t.Fatal("want error for host bits set")
	}
	for _, want := range []string{"192.168.1.0/24", "192.168.1.5/32"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error = %q, want it to contain %q", err.Error(), want)
		}
	}
}
