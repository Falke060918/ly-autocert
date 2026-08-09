package discovery

import (
	"context"
	"log/slog"
	"time"

	"golang.org/x/time/rate"
)

// 스캔 속도 기본값. 폐쇄망 고객사 장비(IPS·방화벽·노후 임베디드 스택)를
// 놀라게 하지 않는 것이 목적이므로, 빠른 스캔보다 조용한 스캔을 기본으로 둔다.
const (
	// DefaultScanRatePPS는 단발·프리셋 스캔의 기본 프로브 속도(초당 probe)다.
	DefaultScanRatePPS = 50
	// DefaultScanConcurrency는 단발·프리셋 스캔의 기본 동시성이다.
	// manager.go의 defaultConcurrency와 같은 값을 공유한다(어긋나면 두 경로의 부하가 달라진다).
	DefaultScanConcurrency = defaultConcurrency

	// SweepRatePPS·SweepConcurrency·SweepChunkPause는 넓은 대역을 훑는
	// 스윕(대규모 CIDR) 전용 값이다. 단발 스캔보다 더 낮추고 청크 사이에 공백을 둬
	// "지속적인 대량 프로브"로 보이지 않게 한다.
	SweepRatePPS     = 10
	SweepConcurrency = 8
	SweepChunkPause  = 5 * time.Second

	// MaxRatePPS는 요청 가능한 속도 상한이다. 이보다 큰 값은 이 값으로 잘라낸다 —
	// 사용자가 실수로 큰 수를 넣어도 고객사 장비에 스캔 폭주가 나가지 않게 한다.
	MaxRatePPS = 1000

	// rateBurst는 토큰 버킷의 버스트 크기다. 1로 고정한다 —
	// 버스트를 허용하면 초당 평균은 지켜도 순간 폭주가 IPS 임계를 넘긴다.
	rateBurst = 1
)

// RateLimiter는 프로브 송신 속도를 초당 pps개로 고르게 펴는 토큰 버킷이다.
// 서버 스캔(manager)과 센서 스캔(cmd/sensor)이 같은 타입을 공유한다.
//
// 사용법: dial **직전에** Wait을 호출한다. goroutine을 띄우는 시점이 아니라
// 실제 패킷이 나가기 직전에 걸어야 회선에 나가는 속도가 제한된다.
//
//	if err := lim.Wait(ctx); err != nil {
//	    return // ctx 취소 — Job 중단
//	}
//	obs := ScanEndpoint(ctx, ep, timeout)
//
// 여러 goroutine에서 동시에 Wait을 호출해도 안전하다(rate.Limiter가 스레드 안전).
type RateLimiter struct {
	lim     *rate.Limiter // nil이면 무제한
	pps     int           // 실제 적용된 속도. 0이면 무제한.
	clamped bool          // 요청값이 MaxRatePPS로 잘렸는가
}

// NewRateLimiter는 초당 pps개로 제한하는 리미터를 만든다.
// pps <= 0이면 무제한(내부 limiter가 nil이고 Wait이 즉시 반환)이다.
// pps > MaxRatePPS이면 MaxRatePPS로 잘라내고 경고 로그를 남긴다 —
// 잘렸는지는 Clamped()·PPS()로도 확인할 수 있다.
func NewRateLimiter(pps int) *RateLimiter {
	if pps <= 0 {
		return &RateLimiter{}
	}
	clamped := false
	if pps > MaxRatePPS {
		slog.Warn("discovery: scan rate clamped",
			"requested_pps", pps, "applied_pps", MaxRatePPS)
		pps = MaxRatePPS
		clamped = true
	}
	return &RateLimiter{
		lim:     rate.NewLimiter(rate.Limit(pps), rateBurst),
		pps:     pps,
		clamped: clamped,
	}
}

// Wait은 다음 프로브를 보내도 되는 시점까지 블록한다.
// ctx가 취소되면 즉시 그 오류를 반환한다(대기 중이어도 즉시 풀린다).
// 무제한 리미터는 블록하지 않지만, 이미 취소된 ctx면 그 오류를 그대로 반환한다 —
// 제한/무제한 경로의 취소 동작을 같게 유지하기 위함이다.
func (l *RateLimiter) Wait(ctx context.Context) error {
	if l == nil || l.lim == nil {
		return ctx.Err()
	}
	return l.lim.Wait(ctx)
}

// PPS는 실제 적용 중인 초당 프로브 수를 반환한다. 무제한이면 0이다.
func (l *RateLimiter) PPS() int {
	if l == nil {
		return 0
	}
	return l.pps
}

// Clamped는 요청 속도가 MaxRatePPS로 잘렸는지 알린다.
// 호출자는 이 값으로 사용자에게 "요청보다 느리게 돈다"를 알릴 수 있다.
func (l *RateLimiter) Clamped() bool {
	if l == nil {
		return false
	}
	return l.clamped
}

// InterleaveByHost는 엔드포인트를 호스트(IP)별로 묶은 뒤 각 호스트에서 하나씩
// 번갈아 뽑아 재배열한다. 원소는 하나도 잃거나 늘리지 않는다.
//
// 왜 필요한가: IPS·방화벽의 포트스캔 탐지는 대개 "호스트당 초당 distinct 포트 수"를
// 임계로 삼는다. Expand는 IP별로 포트를 연달아 펼치므로 그대로 스캔하면
// 같은 IP에 여러 포트가 연속으로 꽂혀 임계를 바로 넘긴다.
// 이 재배열 하나가 오탐(포트스캔 경보) 억제에 가장 크게 기여한다 —
// 폐쇄망 고객사 장비를 놀라게 하지 않는 것이 목적이다.
//
// 결정론: 호스트 순서는 입력에서 처음 등장한 순서, 호스트 내부는 입력 순서를 그대로
// 유지한다. 같은 입력이면 항상 같은 출력이다. 랜덤 셔플을 쓰지 않는다 —
// 재시도 시 순서가 달라지면 부분 재실행의 멱등성이 깨진다.
//
// 호스트별 개수가 불균등하면 라운드가 진행되며 짧은 호스트가 먼저 소진돼
// 꼬리 부분에는 가장 포트가 많은 호스트가 연속으로 남는다. 이는 남은 호스트가
// 하나뿐이라 피할 수 없으며, 그 구간의 속도는 RateLimiter가 따로 눌러 준다.
func InterleaveByHost(endpoints []Endpoint) []Endpoint {
	if len(endpoints) < 2 {
		return append([]Endpoint(nil), endpoints...)
	}

	// map 순회 순서는 비결정적이므로 호스트 순서는 별도 슬라이스로 고정한다.
	order := make([]string, 0, len(endpoints))
	groups := make(map[string][]Endpoint, len(endpoints))
	maxLen := 0
	for _, ep := range endpoints {
		if _, ok := groups[ep.IP]; !ok {
			order = append(order, ep.IP)
		}
		groups[ep.IP] = append(groups[ep.IP], ep)
		if n := len(groups[ep.IP]); n > maxLen {
			maxLen = n
		}
	}

	out := make([]Endpoint, 0, len(endpoints))
	for round := 0; round < maxLen; round++ {
		for _, ip := range order {
			if g := groups[ip]; round < len(g) {
				out = append(out, g[round])
			}
		}
	}
	return out
}
