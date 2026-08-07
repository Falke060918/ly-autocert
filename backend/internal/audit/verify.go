package audit

// 해시 체인 무결성 검증 (이슈 #7).
// 각 행의 해시를 삽입 때와 같은 SQL 함수(audit_event_hash, 마이그레이션 0007)로
// 재계산해 비교하고, prev_hash가 직전 행의 integrity_hash와 연결되는지 확인한다.
// 같은 함수를 쓰므로 표현(타임스탬프·jsonb 정규화) 불일치로 인한 오탐이 없다.
//
// 변조 지점 특정:
//   - hash_mismatch: 해당 행의 내용이 변조됨 (재계산 해시 ≠ 저장 해시)
//   - chain_broken: 직전 행과의 연결 단절 — 중간 행 삭제 또는 앞 행의 재해시 흔적
// 한계: 꼬리(최신) 이벤트 삭제는 체인만으로 탐지 불가 — HeadID/HeadHash를 외부에
// 주기적으로 기록(anchoring)해 보완한다.

import (
	"context"
	"fmt"
)

// maxInvalidReported는 검증 응답에 담는 변조 의심 행 수 상한이다.
const maxInvalidReported = 100

// InvalidEvent는 검증에 실패한 감사 이벤트 1건이다.
type InvalidEvent struct {
	ID           int64 `json:"id"`
	HashMismatch bool  `json:"hash_mismatch"`
	ChainBroken  bool  `json:"chain_broken"`
}

// VerifyResult는 체인 전체 검증 결과다.
type VerifyResult struct {
	Valid         bool           `json:"valid"`
	TotalEvents   int64          `json:"total_events"`
	HeadID        *int64         `json:"head_id"`
	HeadHash      *string        `json:"head_hash"` // 외부 기록(anchoring)용
	Invalid       []InvalidEvent `json:"invalid"`
	InvalidCapped bool           `json:"invalid_capped"`
}

// Verify는 audit_events 전체 체인을 검증한다.
func (r *Recorder) Verify(ctx context.Context) (*VerifyResult, error) {
	res := &VerifyResult{Invalid: []InvalidEvent{}}

	if err := r.pool.QueryRowContext(ctx,
		`SELECT count(*),
		        max(id),
		        (SELECT integrity_hash FROM audit_events ORDER BY id DESC LIMIT 1)
		   FROM audit_events`).Scan(&res.TotalEvents, &res.HeadID, &res.HeadHash); err != nil {
		return nil, fmt.Errorf("audit: verify head: %w", err)
	}

	rows, err := r.pool.QueryContext(ctx,
		`SELECT id, hash_mismatch, chain_broken FROM (
		    SELECT a.id,
		           a.integrity_hash <> audit_event_hash(a) AS hash_mismatch,
		           a.prev_hash <> coalesce(lag(a.integrity_hash) OVER (ORDER BY a.id),
		                                   repeat('0', 64)) AS chain_broken
		      FROM audit_events a
		 ) checked
		 WHERE hash_mismatch OR chain_broken
		 ORDER BY id
		 LIMIT $1`, maxInvalidReported+1)
	if err != nil {
		return nil, fmt.Errorf("audit: verify chain: %w", err)
	}
	defer rows.Close()

	for rows.Next() {
		var iv InvalidEvent
		if err := rows.Scan(&iv.ID, &iv.HashMismatch, &iv.ChainBroken); err != nil {
			return nil, fmt.Errorf("audit: scan invalid event: %w", err)
		}
		res.Invalid = append(res.Invalid, iv)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	if len(res.Invalid) > maxInvalidReported {
		res.Invalid = res.Invalid[:maxInvalidReported]
		res.InvalidCapped = true
	}
	res.Valid = len(res.Invalid) == 0
	return res, nil
}
