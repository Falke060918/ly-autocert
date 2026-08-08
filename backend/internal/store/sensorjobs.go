// sensorjobs.go — 센서 위임 스캔 Job의 pull 리스 상태 머신 (Phase 3a, docs/00-spec.md 7.8).
// pending → leased(만료 시각) → running(하트비트 연장) → completed/failed.
// 리스 만료는 서버 reaper가 failed로 회수한다. Observation 배치는
// scan_job_endpoints PK(job_id, ip, port)를 아비터로 멱등 반영한다 (0010).
// 인증서·개인키 본문은 저장·로그 어디에도 넣지 않는다 (보안 하드 규칙 3).
package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"
)

// sensorLeaseTTL은 리스 유효 시간이다. leased→running 전이와 running 하트비트가
// 이 시간만큼 만료 시각을 연장한다. 초과 시 reaper가 failed로 회수한다.
const sensorLeaseTTL = 90 * time.Second

// sensorScopePrefix는 센서 스캔의 targets.executor_scope 접두사다.
// 전체 값은 'sensor:<sensor_id>' — 세그먼트 간 사설 IP 겹침을 분리한다 (00-spec 12장).
const sensorScopePrefix = "sensor:"

// leaseExpiredError는 reaper가 회수한 Job에 기록하는 오류 문자열이다.
const leaseExpiredError = "lease expired"

// ErrNoPendingSensorJob은 리스할 pending Job이 없을 때 반환한다(핸들러가 204 매핑).
var ErrNoPendingSensorJob = errors.New("store: no pending sensor job")

// ErrSensorJobState는 상태 머신이 허용하지 않는 전이 요청에 반환한다.
// 터미널 상태(completed/failed)의 Job에 대한 모든 전이 요청이 여기 해당한다.
var ErrSensorJobState = errors.New("store: sensor job state conflict")

// SensorLease는 리스로 배정된 Job 1건이다(sensorwire.LeaseResponse의 원천).
type SensorLease struct {
	JobID          string
	JobType        string
	Targets        []string
	Ports          []int
	TimeoutMs      int
	Concurrency    int
	LeaseExpiresAt time.Time
}

// LeaseNextSensorJob은 해당 센서의 가장 오래된 pending Job 1건을 leased로 전이한다.
// FOR UPDATE SKIP LOCKED — 같은 센서의 동시 폴링이 겹쳐도 한 Job은 한 번만 리스된다.
// pending Job이 없으면 ErrNoPendingSensorJob.
func (s *Store) LeaseNextSensorJob(ctx context.Context, sensorID string) (*SensorLease, error) {
	var (
		l          SensorLease
		targetsStr string
		portsStr   string
	)
	err := s.db.QueryRowContext(ctx, `
		UPDATE scan_jobs
		   SET status = 'leased', leased_at = now(),
		       lease_expires_at = now() + make_interval(secs => $2)
		 WHERE id = (
			SELECT id FROM scan_jobs
			 WHERE executor_type = 'sensor' AND sensor_id = $1::uuid AND status = 'pending'
			 ORDER BY created_at
			 LIMIT 1
			 FOR UPDATE SKIP LOCKED)
		RETURNING id, job_type,
		          array_to_string(targets, chr(31)), array_to_string(ports, ','),
		          timeout_ms, concurrency, lease_expires_at`,
		sensorID, sensorLeaseTTL.Seconds(),
	).Scan(&l.JobID, &l.JobType, &targetsStr, &portsStr, &l.TimeoutMs, &l.Concurrency, &l.LeaseExpiresAt)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNoPendingSensorJob
	}
	if err != nil {
		return nil, fmt.Errorf("store: lease sensor job: %w", err)
	}
	l.Targets = splitNonEmpty(targetsStr, sansSep)
	l.Ports = parseInts(splitNonEmpty(portsStr, portSep))
	return &l, nil
}

// MarkSensorJobRunning은 leased → running 전이다(센서의 JobStart 보고).
// 총 엔드포인트 수를 기록하고 리스를 연장한다. leased가 아니면 오류.
func (s *Store) MarkSensorJobRunning(ctx context.Context, jobID, sensorID string, total int) error {
	res, err := s.db.ExecContext(ctx, `
		UPDATE scan_jobs
		   SET status = 'running', started_at = now(), total_endpoints = $3,
		       lease_expires_at = now() + make_interval(secs => $4)
		 WHERE id = $1::uuid AND sensor_id = $2::uuid AND status = 'leased'`,
		jobID, sensorID, total, sensorLeaseTTL.Seconds())
	if err != nil {
		return fmt.Errorf("store: mark sensor job running: %w", err)
	}
	return s.checkSensorJobAffected(ctx, res, jobID, sensorID, "running")
}

// SensorProgress는 센서가 하트비트로 보고하는 누적 진행률이다.
// 센서 로컬 집계라 서버가 반영한 관측 수보다 앞선다(수집 후 배치 전송).
type SensorProgress struct {
	ScannedEndpoints   int
	ReachableEndpoints int
	CertificatesFound  int
}

// ExtendSensorJobLease는 running 중 하트비트로 리스를 연장하고 보고된 진행률을
// 같은 UPDATE로 반영한다. 관측 배치가 재계산하는 절대값과 섞이지 않도록
// GREATEST로 앞선 값만 취한다(뒤늦은 하트비트가 진행률을 되돌리지 않는다).
// running이 아니면 오류(리스 만료로 이미 failed 회수된 경우 포함).
func (s *Store) ExtendSensorJobLease(ctx context.Context, jobID, sensorID string, p SensorProgress) error {
	res, err := s.db.ExecContext(ctx, `
		UPDATE scan_jobs
		   SET lease_expires_at    = now() + make_interval(secs => $3),
		       scanned_endpoints   = GREATEST(scanned_endpoints, $4),
		       reachable_endpoints = GREATEST(reachable_endpoints, $5),
		       certificates_found  = GREATEST(certificates_found, $6)
		 WHERE id = $1::uuid AND sensor_id = $2::uuid AND status = 'running'`,
		jobID, sensorID, sensorLeaseTTL.Seconds(),
		p.ScannedEndpoints, p.ReachableEndpoints, p.CertificatesFound)
	if err != nil {
		return fmt.Errorf("store: extend sensor job lease: %w", err)
	}
	return s.checkSensorJobAffected(ctx, res, jobID, sensorID, "running(heartbeat)")
}

// MarkSensorJobFinished는 종결 전이다(센서의 Complete 보고).
// completed는 running에서만, failed는 leased/running에서 허용한다
// (센서가 대상 전개 단계에서 실패하면 running 전이 없이 failed 보고가 온다).
// 종료 감사 이벤트는 서버 경로(MarkScanFinished)와 동일 형식으로 남긴다.
func (s *Store) MarkSensorJobFinished(ctx context.Context, jobID, sensorID, status string, scanErr *string) error {
	if status != ScanStatusCompleted && status != ScanStatusFailed {
		return fmt.Errorf("%w: invalid finish status %q", ErrSensorJobState, status)
	}

	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("store: begin: %w", err)
	}
	defer tx.Rollback() //nolint:errcheck

	var scanned, reachable, found, newCerts, total int
	err = tx.QueryRowContext(ctx, `
		UPDATE scan_jobs
		   SET status = $3, finished_at = now(), error = $4
		 WHERE id = $1::uuid AND sensor_id = $2::uuid
		   AND (($3 = 'completed' AND status = 'running')
		     OR ($3 = 'failed' AND status IN ('leased', 'running')))
		RETURNING scanned_endpoints, reachable_endpoints, certificates_found, certificates_new, total_endpoints`,
		jobID, sensorID, status, scanErr,
	).Scan(&scanned, &reachable, &found, &newCerts, &total)
	if errors.Is(err, sql.ErrNoRows) {
		return s.sensorJobStateError(ctx, jobID, sensorID, status)
	}
	if err != nil {
		return fmt.Errorf("store: mark sensor job finished: %w", err)
	}

	// 감사 삽입은 트랜잭션의 마지막 문장이어야 한다 (docs/GOTCHAS.md).
	if err := auditScanFinished(ctx, tx, jobID, status, total, scanned, reachable, found, newCerts); err != nil {
		return err
	}

	if err := tx.Commit(); err != nil {
		return fmt.Errorf("store: commit: %w", err)
	}
	return nil
}

// ReapExpiredSensorLeases는 리스가 만료된 leased/running Job을 failed로 회수하고
// 건당 종료 감사 이벤트를 남긴다. 회수한 Job 수를 반환한다.
func (s *Store) ReapExpiredSensorLeases(ctx context.Context) (int, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return 0, fmt.Errorf("store: begin: %w", err)
	}
	defer tx.Rollback() //nolint:errcheck

	rows, err := tx.QueryContext(ctx, `
		UPDATE scan_jobs
		   SET status = 'failed', finished_at = now(), error = $1
		 WHERE executor_type = 'sensor' AND status IN ('leased', 'running')
		   AND lease_expires_at < now()
		RETURNING id, scanned_endpoints, reachable_endpoints, certificates_found, certificates_new, total_endpoints`,
		leaseExpiredError)
	if err != nil {
		return 0, fmt.Errorf("store: reap expired leases: %w", err)
	}
	type reaped struct {
		id                                        string
		scanned, reachable, found, newCerts, total int
	}
	var jobs []reaped
	for rows.Next() {
		var r reaped
		if err := rows.Scan(&r.id, &r.scanned, &r.reachable, &r.found, &r.newCerts, &r.total); err != nil {
			rows.Close()
			return 0, fmt.Errorf("store: scan reaped job: %w", err)
		}
		jobs = append(jobs, r)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return 0, fmt.Errorf("store: reap expired leases: %w", err)
	}

	// 감사 삽입은 트랜잭션의 마지막 문장(들)이어야 한다 (docs/GOTCHAS.md).
	for _, r := range jobs {
		if err := auditScanFinished(ctx, tx, r.id, ScanStatusFailed, r.total, r.scanned, r.reachable, r.found, r.newCerts); err != nil {
			return 0, err
		}
	}

	if err := tx.Commit(); err != nil {
		return 0, fmt.Errorf("store: commit: %w", err)
	}
	return len(jobs), nil
}

// RecordObservationBatch는 센서의 Observation 배치를 멱등 반영한다.
// scan_job_endpoints PK(job_id, ip, port) 아비터의 ON CONFLICT DO NOTHING으로
// 이미 처리된 관측은 건너뛰고, 새로 삽입된 관측만 카운터·인벤토리에 반영한다 —
// 같은 배치를 다시 POST해도 결과가 중복되지 않는다 (00-spec 7.8 결과 수집).
// 체인은 호출자(API 계층)가 x509 파싱을 마친 상태로 넘긴다.
// 새로 반영된 관측 수를 반환한다.
func (s *Store) RecordObservationBatch(ctx context.Context, jobID, sensorID string, batch []EndpointObservation) (int, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return 0, fmt.Errorf("store: begin: %w", err)
	}
	defer tx.Rollback() //nolint:errcheck

	// 소유·상태 확인: 해당 센서의 Job이며 아직 종결되지 않았어야 한다.
	var status string
	err = tx.QueryRowContext(ctx,
		`SELECT status FROM scan_jobs WHERE id = $1::uuid AND sensor_id = $2::uuid`,
		jobID, sensorID).Scan(&status)
	if errors.Is(err, sql.ErrNoRows) {
		return 0, ErrScanJobNotFound
	}
	if err != nil {
		return 0, fmt.Errorf("store: check sensor job: %w", err)
	}
	if status == ScanStatusCompleted || status == ScanStatusFailed {
		return 0, fmt.Errorf("%w: job is %s, cannot accept observations", ErrSensorJobState, status)
	}

	scope := sensorScopePrefix + sensorID
	applied := 0
	newSum := 0
	for _, obs := range batch {
		res, err := tx.ExecContext(ctx, `
			INSERT INTO scan_job_endpoints (job_id, ip, port)
			VALUES ($1::uuid, $2::inet, $3)
			ON CONFLICT DO NOTHING`,
			jobID, obs.IP, obs.Port)
		if err != nil {
			return 0, fmt.Errorf("store: insert scan job endpoint: %w", err)
		}
		n, err := res.RowsAffected()
		if err != nil {
			return 0, fmt.Errorf("store: insert scan job endpoint: %w", err)
		}
		if n == 0 {
			continue // 이미 처리된 관측 — 멱등 재전송
		}
		_, certsFound, certsNew, err := applyEndpointObservation(ctx, tx, scope, obs)
		if err != nil {
			return 0, err
		}
		// 엔드포인트별 결과를 남긴다 — 진행률 카운터를 절대값으로 재계산하는 근거다(0011).
		if _, err := tx.ExecContext(ctx, `
			UPDATE scan_job_endpoints SET reachable = $4, certificates_found = $5
			 WHERE job_id = $1::uuid AND ip = $2::inet AND port = $3`,
			jobID, obs.IP, obs.Port, obs.Reachable, certsFound); err != nil {
			return 0, fmt.Errorf("store: update scan job endpoint result: %w", err)
		}
		applied++
		newSum += certsNew
	}

	if applied > 0 {
		if err := syncSensorScanCounters(ctx, tx, jobID, newSum); err != nil {
			return 0, err
		}
	}

	if err := tx.Commit(); err != nil {
		return 0, fmt.Errorf("store: commit: %w", err)
	}
	return applied, nil
}

// syncSensorScanCounters는 센서 Job의 진행률 카운터를 scan_job_endpoints에서
// 절대값으로 재계산한다. 증분(+=)이 아니라 재계산이므로 하트비트가 앞서 반영한
// 진행률과 이중 계상되지 않는다(GREATEST로 큰 쪽을 남긴다).
// certificates_new는 서버만 아는 값이라 하트비트가 건드리지 않으므로 증분한다.
func syncSensorScanCounters(ctx context.Context, tx *sql.Tx, jobID string, certsNew int) error {
	if _, err := tx.ExecContext(ctx, `
		UPDATE scan_jobs sj SET
			scanned_endpoints   = GREATEST(sj.scanned_endpoints, agg.scanned),
			reachable_endpoints = GREATEST(sj.reachable_endpoints, agg.reachable),
			certificates_found  = GREATEST(sj.certificates_found, agg.found),
			certificates_new    = sj.certificates_new + $2
		  FROM (
			SELECT count(*)                                  AS scanned,
			       count(*) FILTER (WHERE reachable)         AS reachable,
			       coalesce(sum(certificates_found), 0)      AS found
			  FROM scan_job_endpoints WHERE job_id = $1::uuid) agg
		 WHERE sj.id = $1::uuid`,
		jobID, certsNew); err != nil {
		return fmt.Errorf("store: sync sensor scan counters: %w", err)
	}
	return nil
}

// checkSensorJobAffected는 가드된 UPDATE가 0행이면 원인을 구분해 오류를 만든다.
func (s *Store) checkSensorJobAffected(ctx context.Context, res sql.Result, jobID, sensorID, want string) error {
	n, err := res.RowsAffected()
	if err != nil {
		return fmt.Errorf("store: rows affected: %w", err)
	}
	if n == 1 {
		return nil
	}
	return s.sensorJobStateError(ctx, jobID, sensorID, want)
}

// sensorJobStateError는 Job 부재와 상태 충돌을 구분한 오류를 반환한다.
// 현재 상태만 담고 대상·인증서 정보는 담지 않는다 (보안 하드 규칙 3).
func (s *Store) sensorJobStateError(ctx context.Context, jobID, sensorID, want string) error {
	var cur string
	err := s.db.QueryRowContext(ctx,
		`SELECT status FROM scan_jobs WHERE id = $1::uuid AND sensor_id = $2::uuid`,
		jobID, sensorID).Scan(&cur)
	if errors.Is(err, sql.ErrNoRows) {
		return ErrScanJobNotFound
	}
	if err != nil {
		return fmt.Errorf("store: check sensor job: %w", err)
	}
	return fmt.Errorf("%w: job is %s, cannot transition to %s", ErrSensorJobState, cur, want)
}
