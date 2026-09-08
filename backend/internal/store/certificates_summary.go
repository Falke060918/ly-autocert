package store

// 대시보드 요약 집계 (이슈 #8).
// 만료 구간은 저장된 status가 아니라 not_after에서 파생 계산한다 (목록 필터와 동일 기준).
// N일 구간은 누적값이다 — expiring_30d는 7일 이내를 포함한다. 이미 만료된 것은 제외.
//
// inventory는 기존 replaced 제외를 유지한다. server는 실제 현재 연결을 우선하며
// 전역 상태만으로 현재 적용 인증서를 숨기지 않는다.

import (
	"context"
	"fmt"
	"strings"
)

// Summary는 대시보드 요약 집계 결과다.
type Summary struct {
	ExpiryWeeks [13]int `json:"expiry_weeks"`
	Total       int     `json:"total"`
	Expiring7d  int     `json:"expiring_7d"`
	Expiring30d int     `json:"expiring_30d"`
	Expiring60d int     `json:"expiring_60d"`
	Expiring90d int     `json:"expiring_90d"`
	Expired     int     `json:"expired"`
	SelfSigned  int     `json:"self_signed"`
	Unowned     int     `json:"unowned"`
}

// CertificateSummary는 인벤토리 전체를 단일 쿼리로 집계한다.
// 조회 전용이라 감사 이벤트를 남기지 않는다 (00-spec 7.12 기록 대상에 조회 없음).
func (s *Store) CertificateSummary(ctx context.Context, scopes ...CertificateScope) (*Summary, error) {
	var sum Summary
	scope := selectedCertificateScope(scopes)
	buckets := make([]string, 13)
	dest := []any{&sum.Total, &sum.Expiring7d, &sum.Expiring30d, &sum.Expiring60d, &sum.Expiring90d, &sum.Expired, &sum.SelfSigned, &sum.Unowned}
	for i := range buckets {
		end := (i + 1) * 7
		if end > 90 {
			end = 90
		}
		buckets[i] = fmt.Sprintf("count(*) FILTER (WHERE not_after >= now() + interval '%d days' AND not_after < now() + interval '%d days')", i*7, end)
		dest = append(dest, &sum.ExpiryWeeks[i])
	}
	err := s.db.QueryRowContext(ctx, `
		SELECT count(*),
			count(*) FILTER (WHERE not_after >= now() AND not_after < now() + interval '7 days'),
			count(*) FILTER (WHERE not_after >= now() AND not_after < now() + interval '30 days'),
			count(*) FILTER (WHERE not_after >= now() AND not_after < now() + interval '60 days'),
			count(*) FILTER (WHERE not_after >= now() AND not_after < now() + interval '90 days'),
			count(*) FILTER (WHERE not_after < now()),
			count(*) FILTER (WHERE subject_dn = issuer_dn),
			count(*) FILTER (WHERE owner_id IS NULL), `+strings.Join(buckets, ",")+`
 FROM certificates WHERE `+certificateAggregateScopeSQL(scope, "certificates")).Scan(dest...)
	if err != nil {
		return nil, fmt.Errorf("store: certificate summary: %w", err)
	}
	return &sum, nil
}
