// discovery.go — 네트워크 TLS 스캔(이슈 #5)의 영속화.
// scan_jobs 상태 머신 + 발견된 인증서의 인벤토리 반영(fingerprint 중복 판정).
// 인증서·개인키 본문은 저장·로그 어디에도 넣지 않는다 (보안 하드 규칙 3).
package store

import (
	"context"
	"crypto/sha256"
	"crypto/x509"
	"database/sql"
	"encoding/hex"
	"errors"
	"fmt"
	"slices"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/Falke060918/INHERIT_AutoCert/backend/internal/audit"
	"github.com/Falke060918/INHERIT_AutoCert/backend/internal/certparse"
	"github.com/Falke060918/INHERIT_AutoCert/backend/internal/domain"
)

// EventDiscoveryScan은 스캔 Job 생성·완료 감사 이벤트 타입이다.
const EventDiscoveryScan = "discovery.scan"

// EventDiscoveryScanCancel은 스캔 Job 취소 감사 이벤트 타입이다 (0017).
const EventDiscoveryScanCancel = "discovery.scan.cancel"

// 스캔 Job 상태(명시적 상태 머신). 서버 실행: pending → running → completed/failed.
// 센서 위임: pending → leased → running → …(sensorjobs.go 리스 상태 머신).
// canceled는 취소 API 전용 종료 상태다(0017) — pending 즉시 취소 + 서버 실행
// running의 ctx 취소만 만든다(센서 leased/running 취소는 범위 외).
const (
	ScanStatusPending   = "pending"
	ScanStatusLeased    = "leased"
	ScanStatusRunning   = "running"
	ScanStatusCompleted = "completed"
	ScanStatusFailed    = "failed"
	ScanStatusCanceled  = "canceled"
)

// networkTargetType은 네트워크 스캔으로 발견된 대상 시스템의 target_type이다.
const networkTargetType = "network"

// portSep는 int[] 컬럼 변환용 구분자다(포트는 정수라 쉼표 안전).
const portSep = ","

// ScanInput은 스캔 Job 생성 요청이다.
// SensorID가 지정되면 executor_type='sensor'인 pending Job으로 만들어지고
// 센서가 pull 리스로 가져간다(서버는 직접 실행하지 않는다 — 00-spec 7.8).
type ScanInput struct {
	Targets     []string // IP/CIDR 입력 원문 (재현·감사용)
	Ports       []int
	TimeoutMs   int
	Concurrency int
	// TotalEndpoints는 대상·포트를 전개한 엔드포인트 수다. 생성 응답이 돌려주는
	// 값과 저장된 Job 행이 어긋나지 않도록 생성 시점에 기록한다(센서 위임 포함).
	TotalEndpoints int
	SensorID       *string // 센서 위임 실행일 때만. nil이면 서버 직접 실행
	ActorID        *string
	RequestID      *string
	SourceIP       *string
}

// ScanJob은 스캔 Job 1건의 상태·진행률·결과 요약이다.
type ScanJob struct {
	ID     string `json:"id"`
	Status string `json:"status"`
	// Origin은 발주 출처다 (0015: manual/sweep/rescan). 목록에서 사람이 발주한
	// 스캔과 백그라운드 발주(스윕 청크·재스캔)를 구분하는 기준이다.
	Origin             string     `json:"origin"`
	ExecutorType       string     `json:"executor_type"`
	SensorID           *string    `json:"sensor_id,omitempty"`
	Targets            []string   `json:"targets"`
	Ports              []int      `json:"ports"`
	TimeoutMs          int        `json:"timeout_ms"`
	Concurrency        int        `json:"concurrency"`
	TotalEndpoints     int        `json:"total_endpoints"`
	ScannedEndpoints   int        `json:"scanned_endpoints"`
	ReachableEndpoints int        `json:"reachable_endpoints"`
	CertificatesFound  int        `json:"certificates_found"`
	CertificatesNew    int        `json:"certificates_new"`
	Error              *string    `json:"error"`
	CreatedAt          time.Time  `json:"created_at"`
	StartedAt          *time.Time `json:"started_at"`
	FinishedAt         *time.Time `json:"finished_at"`
}

// EndpointObservation은 엔드포인트 1개의 스캔 결과(store 관점)다.
// discovery 패키지의 결과를 store가 discovery를 import하지 않고 받기 위한 타입이다.
type EndpointObservation struct {
	IP         string
	Port       int
	Reachable  bool
	TLSVersion string
	Hostname   string
	Chain      []*x509.Certificate
}

// ErrScanJobNotFound는 존재하지 않는 Job 조회 시 반환한다.
var ErrScanJobNotFound = errors.New("store: scan job not found")

// ErrScanJobState는 상태 머신이 허용하지 않는 전이 요청에 반환한다.
var ErrScanJobState = errors.New("store: scan job state conflict")

// ScanJobStateError는 ErrScanJobState에 거부 시점의 상태 문맥을 실은 형태다 —
// 핸들러가 "이미 종료된 Job"과 "센서 실행 중"을 다른 안내로 가른다.
// errors.Is(err, ErrScanJobState)는 계속 참이다(기존 호출부·테스트 규약 유지).
type ScanJobStateError struct {
	Status       string
	ExecutorType string
}

func (e *ScanJobStateError) Error() string {
	return "store: scan job state conflict: " + e.Status + " (" + e.ExecutorType + ")"
}

func (e *ScanJobStateError) Is(target error) bool { return target == ErrScanJobState }

// ErrScanJobSweepChunk는 스윕 자식 청크에 대한 개별 취소 요청에 반환한다.
// 청크를 여기서 canceled로 만들면 스윕 집계에서 그 seq가 live로 남아 재발주도
// 안 되고(부분 유니크는 failed만 비켜간다) 스윕은 그 구간을 조용히 건너뛴
// 채 completed가 된다 — 스윕 제어는 스윕 API(pause/cancel)로만 한다.
var ErrScanJobSweepChunk = errors.New("store: sweep chunk jobs are controlled by their sweep")

// DuplicateScanError는 동일 정규화 요청이 이미 활성(pending/leased/running)일 때
// 반환한다. JobID는 기존 활성 Job이다 (핸들러가 409 응답에 싣는다).
type DuplicateScanError struct {
	JobID string
}

func (e *DuplicateScanError) Error() string {
	return "store: duplicate active scan job " + e.JobID
}

// scanDedupHash는 스캔 요청의 정규화 지문이다(실행기·대상·포트 — 0017 dedup_hash).
// 대상은 공백 제거·정렬·중복 제거, 포트는 정렬·중복 제거 후 해시하므로 입력 순서가
// 달라도 같은 요청으로 판정된다. 해시는 중복 판정 키일 뿐 보안 용도가 아니다.
func scanDedupHash(executorType string, sensorID *string, targets []string, ports []int) string {
	executor := executorType
	if sensorID != nil {
		executor = executorType + ":" + *sensorID
	}
	ts := make([]string, 0, len(targets))
	for _, t := range targets {
		if v := strings.TrimSpace(t); v != "" {
			ts = append(ts, v)
		}
	}
	sort.Strings(ts)
	ts = slices.Compact(ts)
	ps := append([]int(nil), ports...)
	sort.Ints(ps)
	ps = slices.Compact(ps)

	h := sha256.New()
	h.Write([]byte(executor))
	for _, t := range ts {
		h.Write([]byte{0x1f})
		h.Write([]byte(t))
	}
	h.Write([]byte{0x1e})
	h.Write([]byte(joinInts(ps)))
	return hex.EncodeToString(h.Sum(nil))
}

// CreateScanJob은 pending 상태의 스캔 Job을 만들고 생성 감사 이벤트를 남긴다.
// 동일 정규화 요청(실행기·대상·포트)이 이미 활성이면 *DuplicateScanError를
// 반환한다 — 버튼 연타·더블클릭이 같은 스캔을 동시에 두 번 돌리지 않게 한다.
// 이 경로는 수동 발주(origin='manual') 전용이라 dedup_hash를 항상 채운다
// (스윕 청크·재스캔 발주는 각자의 중복 방지 장치가 있고 dedup_hash는 NULL이다).
func (s *Store) CreateScanJob(ctx context.Context, in ScanInput) (string, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return "", fmt.Errorf("store: begin: %w", err)
	}
	defer tx.Rollback() //nolint:errcheck

	executorType := domain.ExecutorServer
	if in.SensorID != nil {
		executorType = domain.ExecutorSensor
	}
	dedupHash := scanDedupHash(string(executorType), in.SensorID, in.Targets, in.Ports)

	// 선판정: 같은 지문의 활성 Job이 있으면 INSERT 없이 돌려준다.
	// 동시 요청 경합은 아래 부분 유니크 인덱스(23505)가 마저 막는다.
	if dupID, err := s.findActiveDuplicateScan(ctx, dedupHash); err != nil {
		return "", err
	} else if dupID != "" {
		return "", &DuplicateScanError{JobID: dupID}
	}

	var id string
	err = tx.QueryRowContext(ctx, `
		INSERT INTO scan_jobs (targets, ports, timeout_ms, concurrency, status, executor_type, sensor_id, total_endpoints, dedup_hash)
		VALUES (
			coalesce(string_to_array(nullif($1, ''), chr(31)), '{}'),
			coalesce(string_to_array(nullif($2, ''), ','), '{}')::int[],
			$3, $4, 'pending', $5, $6::uuid, $7, $8)
		RETURNING id`,
		strings.Join(in.Targets, sansSep), joinInts(in.Ports),
		in.TimeoutMs, in.Concurrency, string(executorType), in.SensorID, in.TotalEndpoints,
		dedupHash,
	).Scan(&id)
	if isUniqueViolation(err) {
		// 이 INSERT가 걸릴 수 있는 유니크는 uq_scan_jobs_dedup_active뿐이다
		// (sweep_id/sweep_seq는 NULL — 부분 유니크 비대상). 경합에서 진 쪽이다.
		tx.Rollback() //nolint:errcheck
		if dupID, lerr := s.findActiveDuplicateScan(ctx, dedupHash); lerr == nil && dupID != "" {
			return "", &DuplicateScanError{JobID: dupID}
		}
		// 그 사이 상대 Job이 종료된 극단 경합 — 재시도 안내만 한다.
		return "", &DuplicateScanError{}
	}
	if err != nil {
		return "", fmt.Errorf("store: insert scan job: %w", err)
	}

	after := map[string]any{
		"phase":         "created",
		"targets":       in.Targets,
		"ports":         in.Ports,
		"timeout_ms":    in.TimeoutMs,
		"concurrency":   in.Concurrency,
		"executor_type": string(executorType),
	}
	if in.SensorID != nil {
		after["sensor_id"] = *in.SensorID
	}
	if err := audit.Insert(ctx, tx, audit.Event{
		Type:       EventDiscoveryScan,
		ActorID:    in.ActorID,
		TargetType: "scan_job",
		TargetID:   &id,
		RequestID:  in.RequestID,
		After:      after,
		Result:     domain.AuditSuccess,
		SourceIP:   in.SourceIP,
	}); err != nil {
		return "", err
	}

	if err := tx.Commit(); err != nil {
		return "", fmt.Errorf("store: commit: %w", err)
	}
	return id, nil
}

// findActiveDuplicateScan은 같은 지문의 활성 Job id를 찾는다. 없으면 "".
func (s *Store) findActiveDuplicateScan(ctx context.Context, dedupHash string) (string, error) {
	var id string
	err := s.db.QueryRowContext(ctx, `
		SELECT id FROM scan_jobs
		 WHERE dedup_hash = $1 AND status IN ('pending', 'leased', 'running')
		 ORDER BY created_at DESC LIMIT 1`, dedupHash).Scan(&id)
	if errors.Is(err, sql.ErrNoRows) {
		return "", nil
	}
	if err != nil {
		return "", fmt.Errorf("store: find duplicate scan: %w", err)
	}
	return id, nil
}

// ScanCancelInput은 취소 요청의 감사 메타데이터다.
type ScanCancelInput struct {
	ActorID   *string
	RequestID *string
	SourceIP  *string
}

// CancelScanJob은 스캔 Job을 canceled로 전이하고 discovery.scan.cancel 감사
// 이벤트를 같은 트랜잭션에서 남긴다 (변경 API 감사 하드 규칙 5).
// 허용 전이(MVP): pending(실행기 무관 — 아직 아무도 집지 않았다) 그리고
// running+server(호출자가 Manager의 cancel 레지스트리로 goroutine ctx를 끊는다).
// 센서 leased/running은 범위 외 — 프로토콜 변경 없이는 센서를 멈출 수 없어
// "취소됐는데 계속 도는" 거짓 상태를 만들지 않는다(ErrScanJobState).
// 스윕 자식 청크는 ErrScanJobSweepChunk — 스윕 API로만 제어한다.
func (s *Store) CancelScanJob(ctx context.Context, jobID string, in ScanCancelInput) (*ScanJob, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, fmt.Errorf("store: begin: %w", err)
	}
	defer tx.Rollback() //nolint:errcheck

	var (
		status, executorType string
		sweepID              *string
	)
	err = tx.QueryRowContext(ctx, `
		SELECT status, executor_type, sweep_id::text
		  FROM scan_jobs WHERE id = $1::uuid FOR UPDATE`,
		jobID).Scan(&status, &executorType, &sweepID)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrScanJobNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("store: lock scan job: %w", err)
	}
	if sweepID != nil {
		return nil, ErrScanJobSweepChunk
	}
	cancelable := status == ScanStatusPending ||
		(status == ScanStatusRunning && executorType == string(domain.ExecutorServer))
	if !cancelable {
		return nil, &ScanJobStateError{Status: status, ExecutorType: executorType}
	}

	if _, err := tx.ExecContext(ctx, `
		UPDATE scan_jobs SET status = 'canceled', finished_at = now()
		 WHERE id = $1::uuid`, jobID); err != nil {
		return nil, fmt.Errorf("store: cancel scan job: %w", err)
	}

	// 감사 삽입은 트랜잭션의 마지막 문장이어야 한다 (docs/GOTCHAS.md).
	if err := audit.Insert(ctx, tx, audit.Event{
		Type:       EventDiscoveryScanCancel,
		ActorID:    in.ActorID,
		TargetType: "scan_job",
		TargetID:   &jobID,
		RequestID:  in.RequestID,
		Before:     map[string]any{"status": status},
		After:      map[string]any{"status": ScanStatusCanceled},
		Result:     domain.AuditSuccess,
		SourceIP:   in.SourceIP,
	}); err != nil {
		return nil, err
	}

	if err := tx.Commit(); err != nil {
		return nil, fmt.Errorf("store: commit: %w", err)
	}
	return s.GetScanJob(ctx, jobID)
}

// MarkScanRunning은 Job을 running으로 전이하고 총 엔드포인트 수를 기록한다.
// pending에서만 전이한다(상태 머신 강제 — 중복 시작 방지).
func (s *Store) MarkScanRunning(ctx context.Context, jobID string, total int) error {
	_, err := s.db.ExecContext(ctx, `
		UPDATE scan_jobs
		   SET status = 'running', started_at = now(), total_endpoints = $2
		 WHERE id = $1::uuid AND status = 'pending'`,
		jobID, total)
	if err != nil {
		return fmt.Errorf("store: mark scan running: %w", err)
	}
	return nil
}

// RecordEndpointResult는 엔드포인트 1개의 스캔 결과를 Job 카운터에 반영하고,
// 도달 가능하면 인증서 체인을 인벤토리에 저장한다(fingerprint 중복 판정).
// 단일 트랜잭션 — 카운터와 인벤토리가 원자적으로 갱신된다.
// 서버 직접 스캔 경로이므로 대상 스코프는 'server'다 (0010 executor_scope).
func (s *Store) RecordEndpointResult(ctx context.Context, jobID string, obs EndpointObservation) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("store: begin: %w", err)
	}
	defer tx.Rollback() //nolint:errcheck

	reachable, certsFound, certsNew, err := applyEndpointObservation(ctx, tx, domain.ExecutorScopeServer, obs)
	if err != nil {
		return err
	}
	if err := addScanCounters(ctx, tx, jobID, 1, reachable, certsFound, certsNew); err != nil {
		return err
	}

	if err := tx.Commit(); err != nil {
		return fmt.Errorf("store: commit: %w", err)
	}
	return nil
}

// applyEndpointObservation은 관측 1건을 인벤토리에 반영한다(도달 시 체인 upsert).
// scope는 targets.executor_scope 값 — 서버 'server' / 센서 'sensor:<uuid>'.
// 반영 결과의 카운터 증분(도달, 발견, 신규)을 반환한다.
//
// 이 함수는 서버 직접 스캔과 센서 관측 배치가 공유하는 단일 진입점이다 —
// 재스캔 모니터 갱신(0015)도 여기 한 곳에서만 일어난다.
func applyEndpointObservation(ctx context.Context, tx *sql.Tx, scope string, obs EndpointObservation) (reachable, certsFound, certsNew int, err error) {
	// 도달 실패도 관측이다(consecutive_misses) — 아래 조기 반환보다 앞에 둔다.
	if err := recordEndpointMonitorProbe(ctx, tx, scope, obs); err != nil {
		return 0, 0, 0, err
	}
	if !obs.Reachable {
		return 0, 0, 0, nil
	}
	reachable = 1
	if len(obs.Chain) > 0 {
		n, err := recordDiscoveredChain(ctx, tx, scope, obs)
		if err != nil {
			return 0, 0, 0, err
		}
		certsFound = len(obs.Chain)
		certsNew = n
	}
	return reachable, certsFound, certsNew, nil
}

// addScanCounters는 Job 진행률·결과 카운터를 증분 갱신한다.
func addScanCounters(ctx context.Context, tx *sql.Tx, jobID string, scanned, reachable, certsFound, certsNew int) error {
	if _, err := tx.ExecContext(ctx, `
		UPDATE scan_jobs SET
			scanned_endpoints   = scanned_endpoints + $2,
			reachable_endpoints = reachable_endpoints + $3,
			certificates_found  = certificates_found + $4,
			certificates_new    = certificates_new + $5
		 WHERE id = $1::uuid`,
		jobID, scanned, reachable, certsFound, certsNew); err != nil {
		return fmt.Errorf("store: update scan counters: %w", err)
	}
	return nil
}

// MarkScanFinished는 Job을 종료 상태(completed/failed)로 전이하고 완료 감사 이벤트를 남긴다.
// 미종결(pending/running) 상태에서만 전이한다 — 취소 API와의 경합에서 종료 직전의
// goroutine이 canceled 행을 completed로 덮어쓰고 이중 종료 감사를 남기지 않게 한다.
// 이미 종결된 행이면 no-op(nil)이다.
func (s *Store) MarkScanFinished(ctx context.Context, jobID, status string, scanErr *string) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("store: begin: %w", err)
	}
	defer tx.Rollback() //nolint:errcheck

	var scanned, reachable, found, newCerts, total int
	err = tx.QueryRowContext(ctx, `
		UPDATE scan_jobs
		   SET status = $2, finished_at = now(), error = $3
		 WHERE id = $1::uuid AND status IN ('pending', 'running')
		RETURNING scanned_endpoints, reachable_endpoints, certificates_found, certificates_new, total_endpoints`,
		jobID, status, scanErr,
	).Scan(&scanned, &reachable, &found, &newCerts, &total)
	if errors.Is(err, sql.ErrNoRows) {
		return nil // 이미 종결(취소 포함) — 상태·감사 모두 그대로 둔다.
	}
	if err != nil {
		return fmt.Errorf("store: mark scan finished: %w", err)
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

// orphanedServerJobError는 서버 재시작으로 실행 주체가 사라진 Job에 남기는 오류다.
const orphanedServerJobError = "server restarted"

// ReapOrphanedServerScanJobs는 서버 직접 실행 Job 중 미종결(pending/running) 행을
// failed로 회수하고 건당 종료 감사 이벤트를 남긴다. 회수한 Job 수를 반환한다.
//
// 서버 직접 실행 Job은 생성 시 띄운 인프로세스 goroutine이 유일한 실행 주체다 —
// 프로세스가 죽으면 그 Job을 이어받을 주체가 없고, pending으로 되돌려도 이를
// 집어가는 디스패처가 없어 영구 대기가 된다. 그래서 재큐잉이 아니라 failed 회수다
// (센서 위임 Job의 리스 만료 회수와 같은 처리).
//
// 호출 시점은 서버 기동 직후다 — 단일 인스턴스 배포 전제(docker-compose)에서
// 기동 시점에 살아 있는 서버 실행 Job은 정의상 고아다. 센서 위임 Job
// (executor_type='sensor')은 건드리지 않는다(리스 reaper 소관).
func (s *Store) ReapOrphanedServerScanJobs(ctx context.Context) (int, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return 0, fmt.Errorf("store: begin: %w", err)
	}
	defer tx.Rollback() //nolint:errcheck

	rows, err := tx.QueryContext(ctx, `
		UPDATE scan_jobs
		   SET status = 'failed', finished_at = now(), error = $1
		 WHERE executor_type = 'server' AND status IN ('pending', 'running')
		RETURNING id, scanned_endpoints, reachable_endpoints, certificates_found, certificates_new, total_endpoints`,
		orphanedServerJobError)
	if err != nil {
		return 0, fmt.Errorf("store: reap orphaned server scan jobs: %w", err)
	}
	type reaped struct {
		id                                         string
		scanned, reachable, found, newCerts, total int
	}
	var jobs []reaped
	for rows.Next() {
		var r reaped
		if err := rows.Scan(&r.id, &r.scanned, &r.reachable, &r.found, &r.newCerts, &r.total); err != nil {
			rows.Close()
			return 0, fmt.Errorf("store: scan orphaned job: %w", err)
		}
		jobs = append(jobs, r)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return 0, fmt.Errorf("store: reap orphaned server scan jobs: %w", err)
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

// auditScanFinished는 Job 종료 감사 이벤트를 남긴다(서버·센서 경로 공용).
// 호출자는 이 삽입을 트랜잭션의 마지막 문장으로 둔다 (docs/GOTCHAS.md).
func auditScanFinished(ctx context.Context, tx *sql.Tx, jobID, status string, total, scanned, reachable, found, newCerts int) error {
	result := domain.AuditSuccess
	if status == ScanStatusFailed {
		result = domain.AuditFailure
	}
	after := map[string]any{
		"phase":               "completed",
		"status":              status,
		"total_endpoints":     total,
		"scanned_endpoints":   scanned,
		"reachable_endpoints": reachable,
		"certificates_found":  found,
		"certificates_new":    newCerts,
	}
	return audit.Insert(ctx, tx, audit.Event{
		Type:       EventDiscoveryScan,
		TargetType: "scan_job",
		TargetID:   &jobID,
		After:      after,
		Result:     result,
	})
}

// GetScanJob은 Job 1건을 조회한다. 없으면 ErrScanJobNotFound.
func (s *Store) GetScanJob(ctx context.Context, jobID string) (*ScanJob, error) {
	var (
		j          ScanJob
		targetsStr string
		portsStr   string
	)
	err := s.db.QueryRowContext(ctx, `
		SELECT id, status, origin, executor_type, sensor_id::text,
		       array_to_string(targets, chr(31)),
		       array_to_string(ports, ','),
		       timeout_ms, concurrency, total_endpoints, scanned_endpoints,
		       reachable_endpoints, certificates_found, certificates_new,
		       error, created_at, started_at, finished_at
		  FROM scan_jobs WHERE id = $1::uuid`,
		jobID,
	).Scan(&j.ID, &j.Status, &j.Origin, &j.ExecutorType, &j.SensorID, &targetsStr, &portsStr, &j.TimeoutMs, &j.Concurrency,
		&j.TotalEndpoints, &j.ScannedEndpoints, &j.ReachableEndpoints,
		&j.CertificatesFound, &j.CertificatesNew, &j.Error,
		&j.CreatedAt, &j.StartedAt, &j.FinishedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrScanJobNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("store: get scan job: %w", err)
	}
	j.Targets = splitNonEmpty(targetsStr, sansSep)
	j.Ports = parseInts(splitNonEmpty(portsStr, portSep))
	return &j, nil
}

// 스캔 Job 발주 출처 (0015 scan_jobs.origin CHECK와 같은 값).
const (
	ScanOriginManual = "manual"
	ScanOriginSweep  = "sweep"
	ScanOriginRescan = "rescan"
)

// ListScanJobsInput은 스캔 Job 목록 조회 조건이다.
type ListScanJobsInput struct {
	Statuses []string // 비면 전체 (값 검증은 API 계층)
	Origins  []string // 비면 전체 (manual/sweep/rescan)
	Page     int
	PageSize int
}

// ScanJobList는 스캔 Job 목록 조회 결과다.
type ScanJobList struct {
	Items    []ScanJob `json:"items"`
	Total    int       `json:"total"`
	Page     int       `json:"page"`
	PageSize int       `json:"page_size"`
}

// ListScanJobs는 스캔 Job을 최신 생성 순으로 조회한다(상태·출처 필터 + 페이지네이션).
// 조회 전용이라 감사 이벤트를 남기지 않는다 (00-spec 7.12 기록 대상에 조회 없음).
func (s *Store) ListScanJobs(ctx context.Context, in ListScanJobsInput) (*ScanJobList, error) {
	page := in.Page
	if page < 1 {
		page = 1
	}
	size := in.PageSize
	if size < 1 {
		size = defaultPageSize
	}
	if size > maxPageSize {
		size = maxPageSize
	}

	var conds []string
	var args []any
	if len(in.Statuses) > 0 {
		args = append(args, strings.Join(in.Statuses, sansSep))
		conds = append(conds, fmt.Sprintf("status = ANY(string_to_array($%d, chr(31)))", len(args)))
	}
	if len(in.Origins) > 0 {
		args = append(args, strings.Join(in.Origins, sansSep))
		conds = append(conds, fmt.Sprintf("origin = ANY(string_to_array($%d, chr(31)))", len(args)))
	}
	where := ""
	if len(conds) > 0 {
		where = " WHERE " + strings.Join(conds, " AND ")
	}

	var total int
	if err := s.db.QueryRowContext(ctx,
		"SELECT count(*) FROM scan_jobs"+where, args...).Scan(&total); err != nil {
		return nil, fmt.Errorf("store: count scan jobs: %w", err)
	}

	// id 보조 정렬로 페이지 경계를 안정화한다 (ListCertificates와 같은 규약).
	query := fmt.Sprintf(`
		SELECT id, status, origin, executor_type, sensor_id::text,
		       array_to_string(targets, chr(31)),
		       array_to_string(ports, ','),
		       timeout_ms, concurrency, total_endpoints, scanned_endpoints,
		       reachable_endpoints, certificates_found, certificates_new,
		       error, created_at, started_at, finished_at
		  FROM scan_jobs%s
		 ORDER BY created_at DESC, id ASC LIMIT $%d OFFSET $%d`,
		where, len(args)+1, len(args)+2)
	rows, err := s.db.QueryContext(ctx, query, append(args, size, (page-1)*size)...)
	if err != nil {
		return nil, fmt.Errorf("store: list scan jobs: %w", err)
	}
	defer rows.Close() //nolint:errcheck

	items := []ScanJob{} // 빈 결과도 JSON []로 나가도록 nil이 아닌 슬라이스
	for rows.Next() {
		var (
			j          ScanJob
			targetsStr string
			portsStr   string
		)
		if err := rows.Scan(&j.ID, &j.Status, &j.Origin, &j.ExecutorType, &j.SensorID,
			&targetsStr, &portsStr, &j.TimeoutMs, &j.Concurrency,
			&j.TotalEndpoints, &j.ScannedEndpoints, &j.ReachableEndpoints,
			&j.CertificatesFound, &j.CertificatesNew, &j.Error,
			&j.CreatedAt, &j.StartedAt, &j.FinishedAt); err != nil {
			return nil, fmt.Errorf("store: scan scan job: %w", err)
		}
		j.Targets = splitNonEmpty(targetsStr, sansSep)
		j.Ports = parseInts(splitNonEmpty(portsStr, portSep))
		items = append(items, j)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("store: iterate scan jobs: %w", err)
	}
	return &ScanJobList{Items: items, Total: total, Page: page, PageSize: size}, nil
}

// recordDiscoveredChain은 발견된 체인을 저장하고 리프에 network_endpoint 위치를 붙인다.
// scope는 targets.executor_scope 값이다. 새로 만들어진 인증서 수를 반환한다.
func recordDiscoveredChain(ctx context.Context, tx *sql.Tx, scope string, obs EndpointObservation) (int, error) {
	type stored struct {
		id  string
		typ domain.CertificateType
	}
	certs := make([]stored, 0, len(obs.Chain))
	newCount := 0
	now := time.Now()

	for _, xc := range obs.Chain {
		dc := certparse.Describe(xc, now)
		var (
			id        string
			status    string
			createdAt time.Time
			updatedAt time.Time
			created   bool
		)
		args := append([]any{
			dc.FingerprintSHA256, dc.SerialNumber, dc.SubjectDN, dc.IssuerDN, dc.CommonName,
			strings.Join(dc.SANs, sansSep),
			dc.NotBefore, dc.NotAfter, dc.PublicKeyAlgorithm, dc.PublicKeySize,
			dc.SignatureAlgorithm, string(dc.CertificateType), string(dc.Status),
			dc.SubjectKeyID, dc.AuthorityKeyID, dc.IssuerOrganization,
		}, certificateContentArgs(dc.Content)...)
		err := tx.QueryRowContext(ctx, upsertCertificateSQL, args...).
			Scan(&id, &status, &createdAt, &updatedAt, &created)
		if err != nil {
			return 0, fmt.Errorf("store: upsert discovered certificate: %w", err)
		}
		if created {
			newCount++
		}
		certs = append(certs, stored{id: id, typ: dc.CertificateType})
	}

	// 리프 = 첫 비-CA 인증서. 위치는 리프에만 붙인다(import와 동일 규약).
	leaf := certs[0]
	for _, c := range certs {
		if c.typ != domain.CertTypeCA {
			leaf = c
			break
		}
	}

	targetID, err := ensureNetworkTarget(ctx, tx, scope, obs.IP, obs.Hostname)
	if err != nil {
		return 0, err
	}
	// 이 엔드포인트가 "몇 장을 제시했고 그것만으로 체인이 이어지는가"는 위치에
	// 남긴다 (0018). 인벤토리의 다른 인증서로 보완하지 않는 것이 판정의 요점이다.
	chain := certparse.AnalyzePresentedChain(obs.Chain)
	if err := upsertNetworkLocation(ctx, tx, leaf.id, targetID, obs.Port, obs.TLSVersion, chain); err != nil {
		return 0, err
	}
	return newCount, nil
}

// ensureNetworkTarget은 (스코프, IP)로 대상 시스템을 만들거나 reachable로 갱신한다.
// SELECT-후-INSERT가 아니라 부분 유니크 인덱스(uq_targets_network_scope_ip, 0010)를
// 아비터로 쓰는 단일 upsert다 — 같은 IP의 여러 포트가 동시 트랜잭션으로
// 들어와도 대상이 중복 생성되지 않는다. 스코프 분리로 세그먼트 간
// 사설 IP 대역 겹침에도 대상이 섞이지 않는다 (00-spec 12장 유의점).
func ensureNetworkTarget(ctx context.Context, tx *sql.Tx, scope, ip, hostname string) (string, error) {
	var hn *string
	if hostname != "" {
		hn = &hostname
	}
	var id string
	err := tx.QueryRowContext(ctx, `
		INSERT INTO targets (name, target_type, hostname, ip_address, connection_status, executor_scope)
		VALUES ($1, $2, $3, $4::inet, 'reachable', $5)
		ON CONFLICT (executor_scope, ip_address) WHERE target_type = 'network'
		DO UPDATE SET connection_status = 'reachable',
		              hostname = coalesce(excluded.hostname, targets.hostname)
		RETURNING id`,
		ip, networkTargetType, hn, ip, scope).Scan(&id)
	if err != nil {
		return "", fmt.Errorf("store: upsert network target: %w", err)
	}
	return id, nil
}

// upsertNetworkLocation은 (인증서, 대상, 포트) 위치를 만들거나 최근 확인 시각·
// TLS 버전·제시 체인 판정을 갱신한다. 부분 유니크 인덱스
// (uq_certificate_locations_network_endpoint, 0006)를 아비터로 쓰는 단일 upsert라
// 재스캔·동시 실행에도 위치가 증식하지 않는다.
// 제시 체인(0018)은 관측할 때마다 최신 값으로 덮어쓴다 — 서버 설정이 고쳐지면
// 판정도 따라 바뀌어야 한다. 체인이 비어 있는 관측은 NULL(미상)로 남긴다.
func upsertNetworkLocation(ctx context.Context, tx *sql.Tx, certID, targetID string, port int, tlsVersion string, chain certparse.PresentedChain) error {
	var tv *string
	if tlsVersion != "" {
		tv = &tlsVersion
	}
	var chainLen *int
	var chainComplete *bool
	if chain.Length > 0 {
		chainLen, chainComplete = &chain.Length, &chain.Complete
	}
	_, err := tx.ExecContext(ctx, `
		INSERT INTO certificate_locations
			(certificate_id, target_id, location_type, port, tls_version, last_verified_at, deployment_status,
			 presented_chain_len, chain_complete)
		VALUES ($1, $2, $3, $4, $5, now(), $6, $7, $8)
		ON CONFLICT (certificate_id, target_id, port) WHERE location_type = 'network_endpoint'
		DO UPDATE SET last_verified_at = now(),
		              deployment_status = excluded.deployment_status,
		              tls_version = coalesce(excluded.tls_version, certificate_locations.tls_version),
		              presented_chain_len = coalesce(excluded.presented_chain_len, certificate_locations.presented_chain_len),
		              chain_complete = coalesce(excluded.chain_complete, certificate_locations.chain_complete)`,
		certID, targetID, string(domain.LocationNetworkEndpoint), port, tv, string(domain.DeployDeployed),
		chainLen, chainComplete)
	if err != nil {
		return fmt.Errorf("store: upsert network location: %w", err)
	}
	return nil
}

// joinInts는 int 슬라이스를 쉼표 문자열로 만든다(int[] 삽입용).
func joinInts(vs []int) string {
	if len(vs) == 0 {
		return ""
	}
	parts := make([]string, len(vs))
	for i, v := range vs {
		parts[i] = strconv.Itoa(v)
	}
	return strings.Join(parts, portSep)
}

// parseInts는 문자열 슬라이스를 int 슬라이스로 바꾼다(파싱 실패값은 건너뛴다).
func parseInts(ss []string) []int {
	out := make([]int, 0, len(ss))
	for _, s := range ss {
		if n, err := strconv.Atoi(s); err == nil {
			out = append(out, n)
		}
	}
	return out
}

// splitNonEmpty는 sep로 나누되 빈 문자열은 빈 슬라이스로 반환한다.
// array_to_string은 빈 배열에 빈 문자열을 주므로 strings.Split의 [""] 오류를 막는다.
func splitNonEmpty(s, sep string) []string {
	if s == "" {
		return nil
	}
	return strings.Split(s, sep)
}
