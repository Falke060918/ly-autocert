package discovery

import (
	"context"
	"errors"
	"reflect"
	"testing"
	"time"
)

// TestNewRateLimiterBounds는 pps 해석 규칙을 검증한다:
// 0 이하 = 무제한, 정상값 = 그대로, MaxRatePPS 초과 = 클램프(그 사실이 드러나야 함).
func TestNewRateLimiterBounds(t *testing.T) {
	tests := []struct {
		name        string
		pps         int
		wantPPS     int
		wantClamped bool
	}{
		{name: "zero is unlimited", pps: 0, wantPPS: 0},
		{name: "negative is unlimited", pps: -1, wantPPS: 0},
		{name: "default scan rate", pps: DefaultScanRatePPS, wantPPS: DefaultScanRatePPS},
		{name: "sweep rate", pps: SweepRatePPS, wantPPS: SweepRatePPS},
		{name: "at max is not clamped", pps: MaxRatePPS, wantPPS: MaxRatePPS},
		{name: "above max is clamped", pps: MaxRatePPS + 1, wantPPS: MaxRatePPS, wantClamped: true},
		{name: "absurd value is clamped", pps: 1_000_000, wantPPS: MaxRatePPS, wantClamped: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			l := NewRateLimiter(tt.pps)
			if got := l.PPS(); got != tt.wantPPS {
				t.Errorf("PPS() = %d, want %d", got, tt.wantPPS)
			}
			if got := l.Clamped(); got != tt.wantClamped {
				t.Errorf("Clamped() = %v, want %v", got, tt.wantClamped)
			}
		})
	}
}

// TestRateLimiterUnlimitedDoesNotBlock은 무제한 리미터가 실제로 대기하지 않는지 본다.
func TestRateLimiterUnlimitedDoesNotBlock(t *testing.T) {
	l := NewRateLimiter(0)
	start := time.Now()
	for i := 0; i < 200; i++ {
		if err := l.Wait(context.Background()); err != nil {
			t.Fatalf("Wait: unexpected error: %v", err)
		}
	}
	if elapsed := time.Since(start); elapsed > 200*time.Millisecond {
		t.Errorf("unlimited limiter blocked for %v, want ~0", elapsed)
	}
}

// TestRateLimiterPaces는 pps 제한이 실제로 프로브를 지연시키는지 확인한다.
// 100pps로 20건이면 버스트 1개를 뺀 19건이 10ms 간격 → 최소 ~190ms.
// 짧은 시간창으로 검증해 테스트가 느려지지 않게 한다.
func TestRateLimiterPaces(t *testing.T) {
	const (
		pps  = 100
		n    = 20
		want = time.Duration(n-rateBurst) * time.Second / pps // 190ms
	)
	l := NewRateLimiter(pps)

	start := time.Now()
	for i := 0; i < n; i++ {
		if err := l.Wait(context.Background()); err != nil {
			t.Fatalf("Wait(%d): unexpected error: %v", i, err)
		}
	}
	elapsed := time.Since(start)

	// 하한만 엄격히 본다 — 리미터는 요청 속도보다 빨라질 수 없다.
	// 상한은 CI 스케줄링 지터를 감안해 넉넉히 둔다.
	if elapsed < want-10*time.Millisecond {
		t.Errorf("%d waits at %d pps took %v, want at least ~%v", n, pps, elapsed, want)
	}
	if elapsed > 3*time.Second {
		t.Errorf("%d waits at %d pps took %v, far beyond the expected ~%v", n, pps, elapsed, want)
	}
}

// TestRateLimiterWaitCanceled는 ctx 취소 시 Wait이 즉시 오류를 반환하는지 본다.
// 스캔 Job 중단이 대기 중인 프로브에 막혀 지연되면 안 된다.
func TestRateLimiterWaitCanceled(t *testing.T) {
	t.Run("already canceled ctx", func(t *testing.T) {
		ctx, cancel := context.WithCancel(context.Background())
		cancel()
		for _, l := range []*RateLimiter{NewRateLimiter(100), NewRateLimiter(0)} {
			if err := l.Wait(ctx); !errors.Is(err, context.Canceled) {
				t.Errorf("Wait(canceled) = %v, want context.Canceled (pps=%d)", err, l.PPS())
			}
		}
	})

	t.Run("canceled while waiting", func(t *testing.T) {
		l := NewRateLimiter(1) // 버스트 1개 소진 후 다음 토큰은 1초 뒤
		if err := l.Wait(context.Background()); err != nil {
			t.Fatalf("first Wait: unexpected error: %v", err)
		}

		ctx, cancel := context.WithCancel(context.Background())
		go func() {
			time.Sleep(20 * time.Millisecond)
			cancel()
		}()

		start := time.Now()
		err := l.Wait(ctx)
		elapsed := time.Since(start)
		if !errors.Is(err, context.Canceled) {
			t.Errorf("Wait = %v, want context.Canceled", err)
		}
		if elapsed > 500*time.Millisecond {
			t.Errorf("Wait returned after %v, want release right after cancel", elapsed)
		}
	})
}

// TestInterleaveByHostSpreadsHosts는 3호스트×3포트에서 연속 2개가
// 같은 호스트가 아님을 검증한다 — 포트스캔 탐지 임계 회피의 핵심.
func TestInterleaveByHostSpreadsHosts(t *testing.T) {
	eps, err := Expand([]string{"10.0.0.1", "10.0.0.2", "10.0.0.3"}, []int{443, 8443, 9443})
	if err != nil {
		t.Fatalf("Expand: %v", err)
	}
	if len(eps) != 9 {
		t.Fatalf("Expand produced %d endpoints, want 9", len(eps))
	}

	out := InterleaveByHost(eps)
	if len(out) != len(eps) {
		t.Fatalf("length = %d, want %d", len(out), len(eps))
	}
	for i := 1; i < len(out); i++ {
		if out[i].IP == out[i-1].IP {
			t.Errorf("consecutive probes hit the same host at index %d: %s", i, out[i].IP)
		}
	}
}

// TestInterleaveByHostPreservesElements는 호스트별 개수가 불균등해도
// 원소가 사라지거나 중복되지 않는지 본다(1개짜리 호스트 섞임).
func TestInterleaveByHostPreservesElements(t *testing.T) {
	in := []Endpoint{
		{IP: "10.0.0.1", Port: 443},
		{IP: "10.0.0.1", Port: 8443},
		{IP: "10.0.0.1", Port: 9443},
		{IP: "10.0.0.2", Port: 443},
		{IP: "10.0.0.3", Port: 443},
		{IP: "10.0.0.3", Port: 8443},
	}
	orig := append([]Endpoint(nil), in...)

	out := InterleaveByHost(in)
	if len(out) != len(in) {
		t.Fatalf("length = %d, want %d", len(out), len(in))
	}
	if !reflect.DeepEqual(in, orig) {
		t.Error("InterleaveByHost mutated its input slice")
	}

	counts := make(map[Endpoint]int, len(in))
	for _, ep := range out {
		counts[ep]++
	}
	for _, ep := range orig {
		if counts[ep] != 1 {
			t.Errorf("endpoint %v appears %d times, want exactly 1", ep, counts[ep])
		}
		delete(counts, ep)
	}
	for ep := range counts {
		t.Errorf("unexpected endpoint in output: %v", ep)
	}

	// 앞쪽 라운드에서는 호스트가 골고루 섞여야 한다(꼬리는 남은 호스트가 하나뿐이라 예외).
	for i := 1; i < 3; i++ {
		if out[i].IP == out[i-1].IP {
			t.Errorf("consecutive same host in the balanced head at index %d: %s", i, out[i].IP)
		}
	}
}

// TestInterleaveByHostDeterministic은 같은 입력이 항상 같은 출력을 내는지 본다.
// 랜덤 셔플이면 재시도 시 순서가 흔들려 부분 재실행 멱등성이 깨진다.
func TestInterleaveByHostDeterministic(t *testing.T) {
	in, err := Expand([]string{"192.0.2.10", "192.0.2.11", "192.0.2.12", "192.0.2.13"}, []int{443, 8443, 9443, 10443})
	if err != nil {
		t.Fatalf("Expand: %v", err)
	}

	first := InterleaveByHost(in)
	for i := 0; i < 5; i++ {
		again := InterleaveByHost(in)
		if !reflect.DeepEqual(first, again) {
			t.Fatalf("run %d differs from the first run — output is not deterministic", i+1)
		}
	}
}

// TestInterleaveByHostEdgeCases는 빈 입력·단일 원소를 확인한다.
func TestInterleaveByHostEdgeCases(t *testing.T) {
	if got := InterleaveByHost(nil); len(got) != 0 {
		t.Errorf("InterleaveByHost(nil) = %v, want empty", got)
	}
	one := []Endpoint{{IP: "10.0.0.1", Port: 443}}
	if got := InterleaveByHost(one); !reflect.DeepEqual(got, one) {
		t.Errorf("InterleaveByHost(single) = %v, want %v", got, one)
	}
}
