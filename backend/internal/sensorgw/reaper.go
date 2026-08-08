// reaper.go — 리스 만료 회수 goroutine (docs/00-spec.md 7.8 작업 분배:
// "리스가 만료되면 작업을 실패로 처리한다"). 회수·감사 자체는
// store.ReapExpiredSensorLeases가 단일 트랜잭션으로 수행한다.
package sensorgw

import (
	"context"
	"log/slog"
	"time"
)

// reaperInterval은 리스 만료 검사 주기다 (리스 TTL 90s의 1/3).
const reaperInterval = 30 * time.Second

// StartReaper는 리스 만료 회수 goroutine을 시작한다. 반환된 stop은
// goroutine을 멈추고 종료를 기다린다 (ctx 취소로도 멈춘다).
func (g *Gateway) StartReaper(ctx context.Context) (stop func()) {
	return g.startReaper(ctx, reaperInterval)
}

// startReaper는 주기를 인자로 받는 내부 구현이다 (테스트가 짧은 주기로 돌린다).
func (g *Gateway) startReaper(ctx context.Context, interval time.Duration) (stop func()) {
	ctx, cancel := context.WithCancel(ctx)
	done := make(chan struct{})
	go func() {
		defer close(done)
		ticker := time.NewTicker(interval)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				n, err := g.st.ReapExpiredSensorLeases(ctx)
				if err != nil {
					if ctx.Err() == nil {
						slog.Error("sensorgw: reap expired leases failed", "error", err.Error())
					}
					continue
				}
				if n > 0 {
					slog.Info("sensorgw: reaped expired sensor leases", "count", n)
				}
			}
		}
	}()
	return func() {
		cancel()
		<-done
	}
}
