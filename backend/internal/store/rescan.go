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
	"sort"
	"strings"
	"time"

	"github.com/Falke060918/INHERIT_AutoCert/backend/internal/audit"
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

// RescanIntervals는 유효 재확인 간격 규칙이다 (설계 3.1 — 전역 기본 + 만료 임박 가속 2티어).
// 모니터에 interval_seconds override가 있으면 이 규칙보다 그 값이 우선한다.
type RescanIntervals struct {
	// Default는 가속 구간에 들지 않는 엔드포인트의 간격이다.
	Default time.Duration
	// NearExpiry 이하로 남은 인증서가 붙어 있으면 Near 간격을 쓴다.
	NearExpiry, Near time.Duration
	// UrgentExpiry 이하면 Urgent 간격을 쓴다(NearExpiry보다 우선).
	UrgentExpiry, Urgent time.Duration
}

// seconds는 SQL make_interval에 넘길 초 단위 값이다. 0·음수는 1초로 올린다
// (0을 그대로 넘기면 모든 모니터가 매 tick due가 된다).
func positiveSeconds(d time.Duration) int {
	if s := int(d / time.Second); s > 0 {
		return s
	}
	return 1
}

// ClaimDueEndpointMonitors는 주기가 지난 모니터를 골라 **발주 시점에** last_probe_at을
// 찍고 그 목록을 반환한다 (설계 7.2 claim).
//
// locked=false는 다른 인스턴스가 이미 재스캔 tick을 도는 중이라는 뜻이며 오류가
// 아니다 — 알림 스케줄러(RunNotificationTick)와 같은 규약이고 키만 다르다.
//
// 미리 찍는 대가를 명시한다: 발주한 Job이 실패해도 그 엔드포인트는 다음 주기까지
// 다시 보지 않는다. 반대 설계(완료 시점에 찍기)는 Job이 도는 동안 같은 엔드포인트가
// 계속 due로 보여 중복 발주를 낳고, 그걸 막으려면 in-flight 상태 컬럼이 하나 더 필요하다.
//
// 비활성 센서 스코프는 아예 고르지 않는다 — 센서가 죽어 있으면 그 Job은 pending으로
// 쌓이기만 하기 때문이다. monitor에 신규 상태 컬럼을 만들지 않는 것이 규약이라
// (설계 5.2) 판정은 sensors.status로 그때그때 한다.
func (s *Store) ClaimDueEndpointMonitors(ctx context.Context, iv RescanIntervals, limit int) ([]DueEndpointMonitor, bool, error) {
	if limit <= 0 {
		return nil, true, nil
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, false, fmt.Errorf("store: begin: %w", err)
	}
	defer tx.Rollback() //nolint:errcheck

	var locked bool
	// 키는 'rescan'의 ASCII 바이트다 (알림 tick의 x'6e6f74696679'와 같은 규약, 다른 키).
	if err := tx.QueryRowContext(ctx,
		`SELECT pg_try_advisory_xact_lock(x'72657363616e'::bigint)`).Scan(&locked); err != nil {
		return nil, false, fmt.Errorf("store: rescan scheduler lock: %w", err)
	}
	if !locked {
		return nil, false, nil
	}

	// 티어 판정은 저장된 status가 아니라 not_after로 한다 — status를 만료로 갱신하는
	// 주체가 아직 없어 stale이다 (설계 3.2). 한 엔드포인트에 위치가 여러 개면 가장
	// 임박한 것을 기준으로 한다: 빨리 보는 쪽이 안전한 실패다.
	rows, err := tx.QueryContext(ctx, `
		WITH due AS (
			SELECT m.id, m.target_id, m.port, m.last_probe_at AS prev
			  FROM endpoint_monitors m
			  JOIN targets t ON t.id = m.target_id
			  LEFT JOIN LATERAL (
			      SELECT min(c.not_after) AS not_after
			        FROM certificate_locations cl
			        JOIN certificates c ON c.id = cl.certificate_id
			       WHERE cl.target_id = m.target_id AND cl.port = m.port
			         AND cl.location_type = 'network_endpoint'
			         AND cl.deployment_status <> 'removed'
			         AND c.status NOT IN ('replaced', 'revoked')
			  ) exp ON true
			 WHERE m.enabled AND m.retired_at IS NULL
			   AND t.ip_address IS NOT NULL
			   AND (t.executor_scope = 'server'
			        OR EXISTS (SELECT 1 FROM sensors sn
			                    WHERE sn.status = 'active'
			                      AND t.executor_scope = 'sensor:' || sn.id::text))
			   AND (m.last_probe_at IS NULL
			        OR m.last_probe_at <= now() - make_interval(secs => coalesce(
			             m.interval_seconds,
			             CASE
			               WHEN exp.not_after IS NULL THEN $1::int
			               WHEN exp.not_after <= now() + make_interval(secs => $2::int) THEN $3::int
			               WHEN exp.not_after <= now() + make_interval(secs => $4::int) THEN $5::int
			               ELSE $1::int END)))
			 ORDER BY m.last_probe_at ASC NULLS FIRST
			 LIMIT $6)
		UPDATE endpoint_monitors m
		   SET last_probe_at = now()
		  FROM due
		  JOIN targets t ON t.id = due.target_id
		 WHERE m.id = due.id
		RETURNING due.id::text, due.target_id::text, host(t.ip_address),
		          coalesce(t.hostname, ''), due.port, t.executor_scope, due.prev`,
		positiveSeconds(iv.Default),
		positiveSeconds(iv.UrgentExpiry), positiveSeconds(iv.Urgent),
		positiveSeconds(iv.NearExpiry), positiveSeconds(iv.Near),
		limit)
	if err != nil {
		return nil, true, fmt.Errorf("store: claim due endpoint monitors: %w", err)
	}

	var out []DueEndpointMonitor
	for rows.Next() {
		var d DueEndpointMonitor
		if err := rows.Scan(&d.ID, &d.TargetID, &d.IP, &d.Hostname, &d.Port, &d.Scope, &d.LastProbeAt); err != nil {
			rows.Close()
			return nil, true, fmt.Errorf("store: scan claimed endpoint monitor: %w", err)
		}
		out = append(out, d)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return nil, true, fmt.Errorf("store: claim due endpoint monitors: %w", err)
	}

	// UPDATE ... RETURNING의 행 순서는 보장되지 않는다. 발주 그룹과 Job의 targets
	// 순서가 실행마다 달라지면 감사 기록을 비교할 수 없으므로 여기서 고정한다.
	sort.Slice(out, func(i, j int) bool {
		if out[i].Scope != out[j].Scope {
			return out[i].Scope < out[j].Scope
		}
		if out[i].Port != out[j].Port {
			return out[i].Port < out[j].Port
		}
		return out[i].IP < out[j].IP
	})

	if err := tx.Commit(); err != nil {
		return nil, true, fmt.Errorf("store: commit: %w", err)
	}
	return out, true, nil
}

// RetireMissingEndpointMonitors는 연속 misses회 이상 관측되지 않은 모니터를
// 은퇴시키고 그 수를 반환한다 (설계 3.4).
//
// 행을 지우지 않는다 — first_seen_at이 "이 엔드포인트를 언제부터 알았나"의 유일한
// 기록이기 때문이다. 은퇴는 조회 가능한 상태이지 삭제가 아니다.
func (s *Store) RetireMissingEndpointMonitors(ctx context.Context, misses int) (int, error) {
	if misses <= 0 {
		return 0, nil
	}
	res, err := s.db.ExecContext(ctx, `
		UPDATE endpoint_monitors
		   SET retired_at = now()
		 WHERE retired_at IS NULL AND consecutive_misses >= $1`,
		misses)
	if err != nil {
		return 0, fmt.Errorf("store: retire endpoint monitors: %w", err)
	}
	n, err := res.RowsAffected()
	if err != nil {
		return 0, fmt.Errorf("store: retire endpoint monitors: %w", err)
	}
	return int(n), nil
}

// RescanJobInput은 재스캔 배치 1건의 발주 입력이다.
// 포트가 **단수**인 것이 핵심이다: scan_jobs는 targets × ports 데카르트 곱으로
// 전개되므로(0006) 포트를 여러 개 실으면 요청하지 않은 엔드포인트까지 스캔된다.
type RescanJobInput struct {
	// IPs는 이 배치가 다시 볼 대상 IP다(전부 같은 포트·같은 실행 스코프).
	IPs []string
	// Port는 이 배치의 유일한 포트다.
	Port int
	// SensorID가 nil이 아니면 executor_type='sensor'인 pending Job이 되고
	// 센서가 기존 리스 경로로 pull해 간다(센서 프로토콜 변경 없음).
	SensorID    *string
	TimeoutMs   int
	Concurrency int
	RatePPS     int
}

// CreateRescanScanJob은 origin='rescan'인 스캔 Job을 만들고 생성 감사를 남긴다.
//
// 새 Job 유형이 아니라 **같은 스캔의 다른 발주자**다 — job_type·상태 enum·기존
// 유니크 인덱스·ON CONFLICT 아비터를 건드리지 않는다 (설계 4.2, 0010 42P10 전례).
// 감사는 기존 discovery.scan을 재사용하고 origin으로만 구분한다 (설계 10.4).
func (s *Store) CreateRescanScanJob(ctx context.Context, in RescanJobInput) (string, error) {
	if len(in.IPs) == 0 {
		return "", fmt.Errorf("store: rescan job needs at least one target")
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return "", fmt.Errorf("store: begin: %w", err)
	}
	defer tx.Rollback() //nolint:errcheck

	executorType := domain.ExecutorServer
	if in.SensorID != nil {
		executorType = domain.ExecutorSensor
	}

	var id string
	err = tx.QueryRowContext(ctx, `
		INSERT INTO scan_jobs (
			targets, ports, timeout_ms, concurrency, status, executor_type, sensor_id,
			total_endpoints, rate_pps, origin)
		VALUES (
			coalesce(string_to_array(nullif($1, ''), chr(31)), '{}'),
			ARRAY[$2::int],
			$3, $4, 'pending', $5, $6::uuid,
			$7, $8, 'rescan')
		RETURNING id`,
		strings.Join(in.IPs, sansSep), in.Port, in.TimeoutMs, in.Concurrency,
		string(executorType), in.SensorID, len(in.IPs), in.RatePPS,
	).Scan(&id)
	if err != nil {
		return "", fmt.Errorf("store: insert rescan job: %w", err)
	}

	after := map[string]any{
		"phase":           "created",
		"origin":          "rescan",
		"targets":         in.IPs,
		"ports":           []int{in.Port},
		"timeout_ms":      in.TimeoutMs,
		"concurrency":     in.Concurrency,
		"rate_pps":        in.RatePPS,
		"executor_type":   string(executorType),
		"total_endpoints": len(in.IPs),
	}
	if in.SensorID != nil {
		after["sensor_id"] = *in.SensorID
	}
	// 감사 삽입은 트랜잭션의 마지막 문장이어야 한다 (docs/GOTCHAS.md).
	// actor는 NULL — 사람이 아니라 주기 스케줄러가 낸 발주다.
	if err := audit.Insert(ctx, tx, audit.Event{
		Type:       EventDiscoveryScan,
		TargetType: "scan_job",
		TargetID:   &id,
		After:      after,
		Result:     domain.AuditSuccess,
	}); err != nil {
		return "", err
	}

	if err := tx.Commit(); err != nil {
		return "", fmt.Errorf("store: commit: %w", err)
	}
	return id, nil
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
			-- 은퇴한 엔드포인트가 다시 보이면 되살린다 (설계 3.4). 은퇴는 삭제가
			-- 아니라 상태이고, 되돌리는 근거는 관측 자신이다.
			retired_at         = NULL,
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
