package store

// 주기 재스캔 현황 집계 (06-rescan-and-replacement 11.2 GET /discovery/rescan/status).
//
// 어느 값도 컬럼으로 저장하지 않는다 — due 판정과 마찬가지로 매번 파생한다
// (설계 2.3). 저장된 진행 상태는 갱신 주체를 빠뜨리는 순간 조용히 틀린다.

import (
	"context"
	"fmt"
	"time"
)

// RescanOverview는 재스캔 감시 현황 1회 스냅샷이다.
type RescanOverview struct {
	// Monitored는 발주 대상인 모니터 수다(활성 + 미은퇴).
	Monitored int `json:"monitored_endpoints"`
	// Retired는 연속 미관측으로 은퇴한 모니터 수다. 행은 이력이라 남아 있다(설계 3.4).
	Retired int `json:"retired_endpoints"`
	// Due는 주기가 지났는데 아직 발주되지 않은 모니터 수다 = 적체.
	// 0이 정상이고, 계속 쌓이면 스케줄러가 안 돌거나 배치 상한이 모자란 것이다.
	Due int `json:"due_endpoints"`
	// LastRunAt은 origin='rescan' 스캔 Job이 마지막으로 발주된 시각이다.
	// 한 번도 없으면 nil — 스케줄러 기동 여부의 유일한 관측 지점이다.
	LastRunAt *time.Time `json:"last_run_at"`
}

// GetRescanOverview는 재스캔 현황을 단일 쿼리로 집계한다.
// defaultInterval은 monitor의 interval_seconds가 NULL일 때 쓰는 전역 기본 주기다.
// 조회 전용이라 감사 이벤트를 남기지 않는다.
func (s *Store) GetRescanOverview(ctx context.Context, defaultInterval time.Duration) (*RescanOverview, error) {
	seconds := int(defaultInterval / time.Second)
	if seconds <= 0 {
		seconds = 1
	}
	var o RescanOverview
	// due 조건은 ListDueEndpointMonitors와 **같은 술어**여야 한다 — 화면이 "지연 3"을
	// 보여주는데 발주가 0건이면 진단이 어긋난다. IP 없는 대상 제외까지 그대로 맞춘다.
	err := s.db.QueryRowContext(ctx, `
		SELECT
			count(*) FILTER (WHERE m.enabled AND m.retired_at IS NULL),
			count(*) FILTER (WHERE m.retired_at IS NOT NULL),
			count(*) FILTER (WHERE m.enabled AND m.retired_at IS NULL
				AND t.ip_address IS NOT NULL
				AND (m.last_probe_at IS NULL
				     OR m.last_probe_at <= now() - make_interval(secs => coalesce(m.interval_seconds, $1)))),
			(SELECT max(created_at) FROM scan_jobs WHERE origin = 'rescan')
		  FROM endpoint_monitors m
		  JOIN targets t ON t.id = m.target_id`,
		seconds,
	).Scan(&o.Monitored, &o.Retired, &o.Due, &o.LastRunAt)
	if err != nil {
		return nil, fmt.Errorf("store: rescan overview: %w", err)
	}
	return &o, nil
}
