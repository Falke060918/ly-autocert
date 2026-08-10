// replacement.go — 인증서 교체 감지의 조정(reconcile) 배치.
//
// 재스캔 모니터(rescan.go)가 쌓은 관측 근거를 읽어, 네트워크에서 사라진 옛
// 인증서를 replaced로 전이한다. 관측 트랜잭션 안에서 하지 않는다 — 관측은 초당
// 수십 건이 들어오는 경로이고 전이는 감사 체인(advisory lock)을 잡으므로,
// 섞으면 스캔 처리량이 감사 직렬화에 묶인다. 주기 tick이 호출하는 별도 배치다.
//
// 저장·기록하는 것은 식별자와 지문뿐이다. 인증서 원문·개인키는 넣지 않는다
// (보안 하드 규칙 3).
package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	"github.com/Falke060918/INHERIT_AutoCert/backend/internal/audit"
	"github.com/Falke060918/INHERIT_AutoCert/backend/internal/domain"
)

// EventCertificateReplaced는 교체 확정 감사 이벤트 타입이다.
const EventCertificateReplaced = "certificate.replaced"

// 교체 확정으로 종결되는 발송 대기 알림에 남기는 사유.
// 이미 없어진 인증서로 만료 메일이 나가는 것을 막는다.
const replacedDeliverySkipReason = "certificate replaced"

const (
	// DefaultReplacementConfirmThreshold는 교체 확정에 필요한 연속 관측 횟수다.
	// 1회로 전이하면 LB 뒤에서 인증서 A·B가 번갈아 보일 때 서로를 무한히
	// replaced로 만든다.
	DefaultReplacementConfirmThreshold = 2
	// DefaultReplacementLimit은 tick당 전이 상한이다. 첫 가동 시 누적 스톡이
	// 한꺼번에 전이되면 감사 체인 직렬화가 로그인·업로드를 지연시킨다.
	DefaultReplacementLimit = 200
)

// ReplacementOptions는 교체 조정 배치의 실행 파라미터다.
// 0 이하 값은 기본값으로 대체된다.
type ReplacementOptions struct {
	ConfirmThreshold int
	Limit            int
}

func (o ReplacementOptions) withDefaults() ReplacementOptions {
	if o.ConfirmThreshold <= 0 {
		o.ConfirmThreshold = DefaultReplacementConfirmThreshold
	}
	if o.Limit <= 0 {
		o.Limit = DefaultReplacementLimit
	}
	return o
}

// ReplacementSummary는 배치 1회의 처리 결과다.
type ReplacementSummary struct {
	// Candidates는 전이 조건을 검토한 (모니터, 옛 인증서) 쌍의 수다.
	Candidates int
	// Replaced는 status를 replaced로 전이한 인증서 수다.
	Replaced int
	// LocationsOnly는 위치만 removed로 정리하고 상태는 유지한 건수다
	// (수동 업로드 보호 · 다른 엔드포인트에 잔존).
	LocationsOnly int
	// ReplacedCertificateIDs는 전이된 인증서 id다(호출자 로깅용).
	ReplacedCertificateIDs []string
}

// replacementCandidate는 전이 후보 1건이다(모니터 1행 = 엔드포인트 1개).
type replacementCandidate struct {
	monitorID      string
	targetID       string
	port           int
	confirmations  int
	observedAt     *time.Time
	oldID          string
	oldStatus      string
	oldFingerprint string
	newID          string
}

// ReconcileReplacedCertificates는 교체가 확정된 인증서를 replaced로 전이한다.
//
// 전이 조건(전부 AND):
//  1. 새 지문이 confirmThreshold회 이상 연속 관측됐다(플래핑 억제).
//  2. 옛 인증서의 모든 network_endpoint 위치가 removed다.
//  3. 옛 인증서에 file/keystore/windows_store 위치가 하나도 없다.
//  4. 현재 status가 active 또는 unknown이다(이미 revoked/replaced면 no-op — 멱등).
//
// 조건 3이 핵심이다: 수동 업로드는 관측이 아니라 선언이므로 "네트워크에서 안
// 보인다"가 "폐기됐다"를 뜻하지 않는다(아직 배포 전일 수 있다). 그런 인증서는
// 위치만 removed로 표시하고 상태는 그대로 둔다.
//
// 후보는 1건씩 독립 트랜잭션으로 처리한다 — 한 건의 경합·실패가 나머지를
// 되돌리지 않고, 감사 체인의 advisory lock 보유 구간도 건당으로 짧게 유지된다.
func (s *Store) ReconcileReplacedCertificates(ctx context.Context, opt ReplacementOptions) (ReplacementSummary, error) {
	opt = opt.withDefaults()

	candidates, err := s.replacementCandidates(ctx, opt)
	if err != nil {
		return ReplacementSummary{}, err
	}

	sum := ReplacementSummary{Candidates: len(candidates)}
	for _, c := range candidates {
		replaced, err := s.applyReplacement(ctx, c)
		if err != nil {
			return sum, err
		}
		if replaced {
			sum.Replaced++
			sum.ReplacedCertificateIDs = append(sum.ReplacedCertificateIDs, c.oldID)
		} else {
			sum.LocationsOnly++
		}
	}
	return sum, nil
}

// replacementCandidates는 전이 후보를 고른다.
//
// 후보 조건에 "이 엔드포인트에 아직 removed가 아닌 옛 인증서 위치가 있다"를
// 넣어 배치가 자기 자신을 소진하게 만든다 — 위치를 정리하고 나면 같은 모니터는
// 다음 교체가 관측될 때까지 후보에서 빠지므로, 전이하지 못한 건(수동 업로드
// 보호 등)이 매 tick 재검토되지 않는다.
func (s *Store) replacementCandidates(ctx context.Context, opt ReplacementOptions) ([]replacementCandidate, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT m.id::text, m.target_id::text, m.port, m.current_streak, m.last_success_at,
		       oldc.id::text, oldc.status, oldc.fingerprint_sha256, newc.id::text
		  FROM endpoint_monitors m
		  JOIN certificates oldc ON oldc.fingerprint_sha256 = m.previous_fingerprint
		  JOIN certificates newc ON newc.fingerprint_sha256 = m.current_fingerprint
		 WHERE m.enabled AND m.retired_at IS NULL
		   AND m.current_fingerprint <> m.previous_fingerprint
		   AND m.current_streak >= $1
		   AND oldc.status IN ('active', 'unknown')
		   AND EXISTS (
		       SELECT 1 FROM certificate_locations cl
		        WHERE cl.certificate_id = oldc.id
		          AND cl.target_id = m.target_id
		          AND cl.port = m.port
		          AND cl.location_type = 'network_endpoint'
		          AND cl.deployment_status <> 'removed')
		 ORDER BY m.last_success_at ASC NULLS FIRST
		 LIMIT $2`,
		opt.ConfirmThreshold, opt.Limit)
	if err != nil {
		return nil, fmt.Errorf("store: list replacement candidates: %w", err)
	}
	defer rows.Close()

	var out []replacementCandidate
	for rows.Next() {
		var c replacementCandidate
		if err := rows.Scan(&c.monitorID, &c.targetID, &c.port, &c.confirmations, &c.observedAt,
			&c.oldID, &c.oldStatus, &c.oldFingerprint, &c.newID); err != nil {
			return nil, fmt.Errorf("store: scan replacement candidate: %w", err)
		}
		out = append(out, c)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("store: list replacement candidates: %w", err)
	}
	return out, nil
}

// applyReplacement는 후보 1건을 단일 트랜잭션으로 처리한다.
// 상태를 전이했으면 true, 위치만 정리했으면 false를 반환한다.
func (s *Store) applyReplacement(ctx context.Context, c replacementCandidate) (bool, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return false, fmt.Errorf("store: begin: %w", err)
	}
	defer tx.Rollback() //nolint:errcheck

	// 후보 선정과 전이 사이에 상태가 바뀌었을 수 있다 — 행을 잠그고 다시 본다.
	var status string
	err = tx.QueryRowContext(ctx,
		`SELECT status FROM certificates WHERE id = $1::uuid FOR UPDATE`, c.oldID).Scan(&status)
	if errors.Is(err, sql.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return false, fmt.Errorf("store: lock replaced certificate: %w", err)
	}
	if status != string(domain.CertStatusActive) && status != string(domain.CertStatusUnknown) {
		return false, nil // 이미 revoked/replaced — 멱등 no-op
	}

	// 관측된 엔드포인트의 위치를 removed로 전이한다. 행은 지우지 않는다 —
	// "여기 있었다"는 이력이 감사·조사에 필요하다. last_verified_at도 갱신하지
	// 않는다: 마지막 실관측 시각을 보존해야 언제부터 사라졌는지 알 수 있다.
	if _, err := tx.ExecContext(ctx, `
		UPDATE certificate_locations
		   SET deployment_status = 'removed'
		 WHERE certificate_id = $1::uuid AND target_id = $2::uuid AND port = $3
		   AND location_type = 'network_endpoint' AND deployment_status <> 'removed'`,
		c.oldID, c.targetID, c.port); err != nil {
		return false, fmt.Errorf("store: mark location removed: %w", err)
	}

	var liveEndpoints, managedLocations int
	if err := tx.QueryRowContext(ctx, `
		SELECT count(*) FILTER (WHERE location_type = 'network_endpoint' AND deployment_status <> 'removed'),
		       count(*) FILTER (WHERE location_type IN ('file', 'keystore', 'windows_store'))
		  FROM certificate_locations WHERE certificate_id = $1::uuid`,
		c.oldID).Scan(&liveEndpoints, &managedLocations); err != nil {
		return false, fmt.Errorf("store: count remaining locations: %w", err)
	}
	if liveEndpoints > 0 || managedLocations > 0 {
		// 아직 다른 곳에 배포돼 있거나(2), 업로드로 선언된 인증서다(3).
		if err := tx.Commit(); err != nil {
			return false, fmt.Errorf("store: commit: %w", err)
		}
		return false, nil
	}

	if _, err := tx.ExecContext(ctx, `
		UPDATE certificates
		   SET status = 'replaced', replaced_by_certificate_id = $2::uuid,
		       replaced_at = now(), updated_at = now()
		 WHERE id = $1::uuid`,
		c.oldID, c.newID); err != nil {
		return false, fmt.Errorf("store: mark certificate replaced: %w", err)
	}

	// 이미 발주된 만료 알림을 종결한다 — 교체된 인증서로 메일이 나가면 오탐이다.
	// 리스를 잡고 있는 'sending'은 건드리지 않는다(디스패처 소관).
	if _, err := tx.ExecContext(ctx, `
		UPDATE notification_deliveries d
		   SET status = 'skipped', last_error = $2,
		       leased_at = NULL, lease_expires_at = NULL, updated_at = now()
		  FROM notification_events e
		 WHERE d.event_id = e.id AND e.certificate_id = $1::uuid AND d.status = 'pending'`,
		c.oldID, replacedDeliverySkipReason); err != nil {
		return false, fmt.Errorf("store: skip pending deliveries: %w", err)
	}

	// 감사 삽입은 트랜잭션의 마지막 문장이어야 한다 (docs/GOTCHAS.md).
	// actor는 NULL — 사람이 아니라 관측 근거에 의한 시스템 전이다.
	before := map[string]any{
		"status":      status,
		"fingerprint": c.oldFingerprint,
	}
	after := map[string]any{
		"status":      string(domain.CertStatusReplaced),
		"replaced_by": c.newID,
		"evidence": map[string]any{
			"target_id":     c.targetID,
			"port":          c.port,
			"observed_at":   c.observedAt,
			"confirmations": c.confirmations,
		},
	}
	if err := audit.Insert(ctx, tx, audit.Event{
		Type:       EventCertificateReplaced,
		TargetType: "certificate",
		TargetID:   &c.oldID,
		Before:     before,
		After:      after,
		Result:     domain.AuditSuccess,
	}); err != nil {
		return false, err
	}

	if err := tx.Commit(); err != nil {
		return false, fmt.Errorf("store: commit: %w", err)
	}
	return true, nil
}
