// rescan.go — 인증서 자동 재스캔의 엔드포인트 모니터(0015 endpoint_monitors) 영속화.
//
// 모니터는 "지금 인증서가 붙어 있는 (대상, 포트)"를 추적한다. 관측 반영은
// applyEndpointObservation 한 곳에서만 일어난다 — 서버 직접 스캔(discovery.go)과
// 센서 관측 배치(sensorjobs.go)가 그 함수를 공유하므로 훅도 하나면 된다.
//
// 저장하는 것은 지문(SHA-256 다이제스트)뿐이다. 인증서 원문·개인키는 넣지 않는다
// (보안 하드 규칙 1·3).
package store

import (
	"context"
	"crypto/x509"
	"database/sql"
	"errors"
	"fmt"
	"time"

	"github.com/Falke060918/INHERIT_AutoCert/backend/internal/certparse"
	"github.com/Falke060918/INHERIT_AutoCert/backend/internal/domain"
)

// ErrEndpointMonitorNotFound는 존재하지 않는 모니터 조회 시 반환한다.
var ErrEndpointMonitorNotFound = errors.New("store: endpoint monitor not found")

// EndpointMonitor는 재스캔 대상 엔드포인트 1건의 상태다.
type EndpointMonitor struct {
	ID                  string     `json:"id"`
	TargetID            string     `json:"target_id"`
	Port                int        `json:"port"`
	FirstSeenAt         time.Time  `json:"first_seen_at"`
	LastProbeAt         *time.Time `json:"last_probe_at"`
	LastSuccessAt       *time.Time `json:"last_success_at"`
	CurrentFingerprint  *string    `json:"current_fingerprint"`
	CurrentStreak       int        `json:"current_streak"`
	PreviousFingerprint *string    `json:"previous_fingerprint"`
	ConsecutiveMisses   int        `json:"consecutive_misses"`
	Enabled             bool       `json:"enabled"`
	IntervalSeconds     *int       `json:"interval_seconds"`
	RetiredAt           *time.Time `json:"retired_at"`
}

// DueEndpointMonitor는 재스캔 주기가 지난 엔드포인트 1건이다.
// 스캔 실행에 필요한 최소 정보(어디를·어느 실행 주체로)만 담는다.
type DueEndpointMonitor struct {
	ID          string
	TargetID    string
	IP          string
	Hostname    string
	Port        int
	Scope       string // targets.executor_scope — 'server' 또는 'sensor:<uuid>'
	LastProbeAt *time.Time
}

// ListDueEndpointMonitors는 재스캔 주기가 지난 모니터를 오래된 순으로 반환한다.
// interval_seconds가 있으면 그 값이, 없으면 defaultInterval이 주기다.
// 비활성·은퇴 모니터와 IP가 없는 대상은 제외한다(스캐너 입력이 IP뿐이다).
func (s *Store) ListDueEndpointMonitors(ctx context.Context, defaultInterval time.Duration, limit int) ([]DueEndpointMonitor, error) {
	if limit <= 0 {
		return nil, nil
	}
	seconds := int(defaultInterval / time.Second)
	if seconds <= 0 {
		seconds = 1
	}
	rows, err := s.db.QueryContext(ctx, `
		SELECT m.id::text, m.target_id::text, host(t.ip_address), coalesce(t.hostname, ''),
		       m.port, t.executor_scope, m.last_probe_at
		  FROM endpoint_monitors m
		  JOIN targets t ON t.id = m.target_id
		 WHERE m.enabled AND m.retired_at IS NULL
		   AND t.ip_address IS NOT NULL
		   AND (m.last_probe_at IS NULL
		        OR m.last_probe_at <= now() - make_interval(secs => coalesce(m.interval_seconds, $1)))
		 ORDER BY m.last_probe_at ASC NULLS FIRST
		 LIMIT $2`,
		seconds, limit)
	if err != nil {
		return nil, fmt.Errorf("store: list due endpoint monitors: %w", err)
	}
	defer rows.Close()

	var out []DueEndpointMonitor
	for rows.Next() {
		var d DueEndpointMonitor
		if err := rows.Scan(&d.ID, &d.TargetID, &d.IP, &d.Hostname, &d.Port, &d.Scope, &d.LastProbeAt); err != nil {
			return nil, fmt.Errorf("store: scan due endpoint monitor: %w", err)
		}
		out = append(out, d)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("store: list due endpoint monitors: %w", err)
	}
	return out, nil
}

// GetEndpointMonitor는 (대상, 포트) 모니터 1건을 조회한다.
// 없으면 ErrEndpointMonitorNotFound.
func (s *Store) GetEndpointMonitor(ctx context.Context, targetID string, port int) (*EndpointMonitor, error) {
	var m EndpointMonitor
	err := s.db.QueryRowContext(ctx, `
		SELECT id::text, target_id::text, port, first_seen_at, last_probe_at, last_success_at,
		       current_fingerprint, current_streak, previous_fingerprint,
		       consecutive_misses, enabled, interval_seconds, retired_at
		  FROM endpoint_monitors
		 WHERE target_id = $1::uuid AND port = $2`,
		targetID, port,
	).Scan(&m.ID, &m.TargetID, &m.Port, &m.FirstSeenAt, &m.LastProbeAt, &m.LastSuccessAt,
		&m.CurrentFingerprint, &m.CurrentStreak, &m.PreviousFingerprint,
		&m.ConsecutiveMisses, &m.Enabled, &m.IntervalSeconds, &m.RetiredAt)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrEndpointMonitorNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("store: get endpoint monitor: %w", err)
	}
	return &m, nil
}

// recordEndpointMonitorProbe는 관측 1건을 모니터에 반영한다(applyEndpointObservation 훅).
//
// 규약:
//   - 도달 성공: last_probe_at·last_success_at 갱신, consecutive_misses 0으로 리셋.
//     지문이 이전과 같으면 current_streak++, 다르면 previous ← current, current ← 새 지문,
//     streak = 1. 지문을 못 얻은 성공(체인 없음)은 지문·streak을 건드리지 않는다.
//   - 도달 실패: last_probe_at 갱신 + consecutive_misses++만. 지문·streak·성공 시각은
//     그대로다 — 방화벽·일시 장애로 지금까지의 관측 근거를 잃으면 안 된다.
//
// 모니터 행은 도달 성공에서만 생긴다. 도달 실패는 기존 행이 있을 때만 반영하고
// 대상 행조차 없으면 조용히 건너뛴다 — 전 포트 스윕은 도달 실패가 수만 건이라
// 여기서 행을 만들면 인벤토리가 오염된다.
func recordEndpointMonitorProbe(ctx context.Context, tx *sql.Tx, scope string, obs EndpointObservation) error {
	fingerprint := observedLeafFingerprint(obs)

	targetID, err := findNetworkTarget(ctx, tx, scope, obs.IP)
	if err != nil {
		return err
	}
	if targetID == "" {
		if !obs.Reachable || fingerprint == nil {
			return nil
		}
		// 첫 관측이라 대상 행이 아직 없다. 아래 인벤토리 반영이 어차피 만드는
		// 행이므로 같은 upsert를 먼저 부른다(멱등).
		targetID, err = ensureNetworkTarget(ctx, tx, scope, obs.IP, obs.Hostname)
		if err != nil {
			return err
		}
	}

	if !obs.Reachable {
		if _, err := tx.ExecContext(ctx, `
			UPDATE endpoint_monitors
			   SET last_probe_at = now(),
			       consecutive_misses = consecutive_misses + 1
			 WHERE target_id = $1::uuid AND port = $2`,
			targetID, obs.Port); err != nil {
			return fmt.Errorf("store: record endpoint monitor miss: %w", err)
		}
		return nil
	}

	if _, err := tx.ExecContext(ctx, `
		INSERT INTO endpoint_monitors (
			target_id, port, first_seen_at, last_probe_at, last_success_at,
			current_fingerprint, current_streak, consecutive_misses
		) VALUES (
			$1::uuid, $2, now(), now(), now(),
			$3, CASE WHEN $3::text IS NULL THEN 0 ELSE 1 END, 0
		)
		ON CONFLICT ON CONSTRAINT endpoint_monitors_target_port_key DO UPDATE SET
			last_probe_at      = now(),
			last_success_at    = now(),
			consecutive_misses = 0,
			previous_fingerprint = CASE
				WHEN $3::text IS NULL THEN endpoint_monitors.previous_fingerprint
				WHEN endpoint_monitors.current_fingerprint IS DISTINCT FROM $3::text
					THEN endpoint_monitors.current_fingerprint
				ELSE endpoint_monitors.previous_fingerprint END,
			current_fingerprint = coalesce($3::text, endpoint_monitors.current_fingerprint),
			current_streak = CASE
				WHEN $3::text IS NULL THEN endpoint_monitors.current_streak
				WHEN endpoint_monitors.current_fingerprint IS NOT DISTINCT FROM $3::text
					THEN endpoint_monitors.current_streak + 1
				ELSE 1 END`,
		targetID, obs.Port, fingerprint); err != nil {
		return fmt.Errorf("store: record endpoint monitor probe: %w", err)
	}
	return nil
}

// findNetworkTarget은 (스코프, IP)의 네트워크 대상 id를 찾는다. 없으면 빈 문자열.
// ensureNetworkTarget과 달리 행을 만들지 않는다.
func findNetworkTarget(ctx context.Context, tx *sql.Tx, scope, ip string) (string, error) {
	var id string
	err := tx.QueryRowContext(ctx, `
		SELECT id::text FROM targets
		 WHERE executor_scope = $1 AND ip_address = $2::inet AND target_type = 'network'`,
		scope, ip).Scan(&id)
	if errors.Is(err, sql.ErrNoRows) {
		return "", nil
	}
	if err != nil {
		return "", fmt.Errorf("store: find network target: %w", err)
	}
	return id, nil
}

// observedLeafFingerprint는 관측 체인의 리프 지문을 반환한다.
// 리프 = 첫 비-CA 인증서(없으면 [0]) — recordDiscoveredChain·import와 같은 규약이다.
// 체인이 없으면 nil(지문 미상)이다.
func observedLeafFingerprint(obs EndpointObservation) *string {
	if len(obs.Chain) == 0 {
		return nil
	}
	leaf := obs.Chain[0]
	for _, xc := range obs.Chain {
		if !isCACertificate(xc) {
			leaf = xc
			break
		}
	}
	fp := certparse.FingerprintSHA256(leaf)
	return &fp
}

// isCACertificate는 recordDiscoveredChain의 리프 판정(certparse.Describe 결과의
// CertTypeCA)과 같은 기준을 쓴다.
func isCACertificate(cert *x509.Certificate) bool {
	return certparse.Describe(cert, time.Now()).CertificateType == domain.CertTypeCA
}
