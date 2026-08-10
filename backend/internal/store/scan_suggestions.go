package store

// scan_suggestions.go — 스캔 대상 입력 제안 (GET /discovery/scan-suggestions).
// 수기 CIDR 입력을 줄이기 위한 읽기 전용 파생 조회 2종이다. 감사 이벤트 없음.
// env 유래 제안(SCAN_SUGGESTED_NETWORKS)은 config→api 경로로 붙는다 —
// store는 환경변수를 읽지 않는다.

import (
	"context"
	"fmt"
)

// DerivedNetwork는 targets 테이블에서 파생한 /24 대역 1개다.
type DerivedNetwork struct {
	CIDR        string `json:"cidr"`
	TargetCount int    `json:"target_count"` // 이 대역에서 이미 발견된 대상 수
}

// ScanSuggestions는 스캔 대상 제안 조회 결과다.
type ScanSuggestions struct {
	// RecentTargets는 수동 스캔(origin=manual)에 최근 쓰인 대상 원문이다
	// (IP/CIDR, 최근 사용 순·중복 제거).
	RecentTargets []string `json:"recent_targets"`
	// DerivedNetworks는 발견된 네트워크 대상의 IPv4 주소를 /24로 묶은 대역이다
	// (대상 수 내림차순). 센서 스코프 구분 없이 합산한다 — 제안일 뿐 판정이 아니다.
	DerivedNetworks []DerivedNetwork `json:"derived_networks"`
}

// ScanSuggestions는 최근 사용 대역과 targets 파생 /24 대역을 조회한다.
func (s *Store) ScanSuggestions(ctx context.Context, recentLimit, networkLimit int) (*ScanSuggestions, error) {
	res := &ScanSuggestions{
		RecentTargets:   []string{},
		DerivedNetworks: []DerivedNetwork{},
	}

	// ① 최근 수동 스캔의 대상 원문 — 스윕 청크·재스캔 발주는 사람이 입력한 값이
	// 아니므로 제외한다(재스캔은 IP 1개짜리 Job이 대량이라 제안을 도배한다).
	rows, err := s.db.QueryContext(ctx, `
		SELECT tgt
		  FROM scan_jobs sj
		  CROSS JOIN LATERAL unnest(sj.targets) AS tgt
		 WHERE sj.origin = 'manual'
		 GROUP BY tgt
		 ORDER BY max(sj.created_at) DESC, tgt ASC
		 LIMIT $1`, recentLimit)
	if err != nil {
		return nil, fmt.Errorf("store: recent scan targets: %w", err)
	}
	defer rows.Close() //nolint:errcheck
	for rows.Next() {
		var t string
		if err := rows.Scan(&t); err != nil {
			return nil, fmt.Errorf("store: scan recent target: %w", err)
		}
		res.RecentTargets = append(res.RecentTargets, t)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("store: iterate recent targets: %w", err)
	}

	// ② 발견된 대상의 /24 파생 대역 (IPv4만 — IPv6는 /24 묶음이 의미가 없다).
	nrows, err := s.db.QueryContext(ctx, `
		SELECT network(set_masklen(t.ip_address, 24))::text AS cidr, count(*)
		  FROM targets t
		 WHERE t.target_type = 'network' AND t.ip_address IS NOT NULL
		   AND family(t.ip_address) = 4
		 GROUP BY 1
		 ORDER BY count(*) DESC, 1 ASC
		 LIMIT $1`, networkLimit)
	if err != nil {
		return nil, fmt.Errorf("store: derived networks: %w", err)
	}
	defer nrows.Close() //nolint:errcheck
	for nrows.Next() {
		var dn DerivedNetwork
		if err := nrows.Scan(&dn.CIDR, &dn.TargetCount); err != nil {
			return nil, fmt.Errorf("store: scan derived network: %w", err)
		}
		res.DerivedNetworks = append(res.DerivedNetworks, dn)
	}
	if err := nrows.Err(); err != nil {
		return nil, fmt.Errorf("store: iterate derived networks: %w", err)
	}
	return res, nil
}
