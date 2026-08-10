package store

// 대시보드 요약 집계 (이슈 #8).
// 만료 구간은 저장된 status가 아니라 not_after에서 파생 계산한다 (목록 필터와 동일 기준).
// N일 구간은 누적값이다 — expiring_30d는 7일 이내를 포함한다. 이미 만료된 것은 제외.
//
// 유일한 status 조건은 replaced 제외다(06-rescan-and-replacement 11.4). 교체 감지가
// 죽었다고 판정한 인증서는 만료 알림에서 이미 빠져 있으므로(store/notifications.go의
// `status NOT IN ('revoked','replaced')`) 대시보드에 남겨 두면 두 화면이 서로를
// 부정한다 — 알림은 안 오는데 "만료 임박 N건"만 계속 뜬다.

import (
	"context"
	"fmt"
)

// Summary는 대시보드 요약 집계 결과다.
type Summary struct {
	Total       int `json:"total"`
	Expiring7d  int `json:"expiring_7d"`
	Expiring30d int `json:"expiring_30d"`
	Expiring60d int `json:"expiring_60d"`
	Expiring90d int `json:"expiring_90d"`
	Expired     int `json:"expired"`
	SelfSigned  int `json:"self_signed"`
	Unowned     int `json:"unowned"`
}

// CertificateSummary는 인벤토리 전체를 단일 쿼리로 집계한다.
// 조회 전용이라 감사 이벤트를 남기지 않는다 (00-spec 7.12 기록 대상에 조회 없음).
func (s *Store) CertificateSummary(ctx context.Context) (*Summary, error) {
	var sum Summary
	err := s.db.QueryRowContext(ctx, `
		SELECT count(*),
			count(*) FILTER (WHERE not_after >= now() AND not_after < now() + interval '7 days'),
			count(*) FILTER (WHERE not_after >= now() AND not_after < now() + interval '30 days'),
			count(*) FILTER (WHERE not_after >= now() AND not_after < now() + interval '60 days'),
			count(*) FILTER (WHERE not_after >= now() AND not_after < now() + interval '90 days'),
			count(*) FILTER (WHERE not_after < now()),
			count(*) FILTER (WHERE subject_dn = issuer_dn),
			count(*) FILTER (WHERE owner_id IS NULL)
		FROM certificates
		WHERE status <> 'replaced'`).Scan(
		&sum.Total, &sum.Expiring7d, &sum.Expiring30d, &sum.Expiring60d, &sum.Expiring90d,
		&sum.Expired, &sum.SelfSigned, &sum.Unowned,
	)
	if err != nil {
		return nil, fmt.Errorf("store: certificate summary: %w", err)
	}
	return &sum, nil
}
