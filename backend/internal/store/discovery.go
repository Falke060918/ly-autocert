// discovery.go — 네트워크 TLS 스캔(이슈 #5)의 영속화.
// scan_jobs 상태 머신 + 발견된 인증서의 인벤토리 반영(fingerprint 중복 판정).
// 인증서·개인키 본문은 저장·로그 어디에도 넣지 않는다 (보안 하드 규칙 3).
package store

import (
	"context"
	"crypto/x509"
	"database/sql"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/Falke060918/INHERIT_AutoCert/backend/internal/audit"
	"github.com/Falke060918/INHERIT_AutoCert/backend/internal/certparse"
	"github.com/Falke060918/INHERIT_AutoCert/backend/internal/domain"
)

// EventDiscoveryScan은 스캔 Job 생성·완료 감사 이벤트 타입이다.
const EventDiscoveryScan = "discovery.scan"

// 스캔 Job 상태(명시적 상태 머신). pending → running → completed/failed.
const (
	ScanStatusPending   = "pending"
	ScanStatusRunning   = "running"
	ScanStatusCompleted = "completed"
	ScanStatusFailed    = "failed"
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
	SensorID    *string // 센서 위임 실행일 때만. nil이면 서버 직접 실행
	ActorID     *string
	RequestID   *string
	SourceIP    *string
}

// ScanJob은 스캔 Job 1건의 상태·진행률·결과 요약이다.
type ScanJob struct {
	ID                 string     `json:"id"`
	Status             string     `json:"status"`
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

// CreateScanJob은 pending 상태의 스캔 Job을 만들고 생성 감사 이벤트를 남긴다.
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

	var id string
	err = tx.QueryRowContext(ctx, `
		INSERT INTO scan_jobs (targets, ports, timeout_ms, concurrency, status, executor_type, sensor_id)
		VALUES (
			coalesce(string_to_array(nullif($1, ''), chr(31)), '{}'),
			coalesce(string_to_array(nullif($2, ''), ','), '{}')::int[],
			$3, $4, 'pending', $5, $6::uuid)
		RETURNING id`,
		strings.Join(in.Targets, sansSep), joinInts(in.Ports),
		in.TimeoutMs, in.Concurrency, string(executorType), in.SensorID,
	).Scan(&id)
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
func applyEndpointObservation(ctx context.Context, tx *sql.Tx, scope string, obs EndpointObservation) (reachable, certsFound, certsNew int, err error) {
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
		 WHERE id = $1::uuid
		RETURNING scanned_endpoints, reachable_endpoints, certificates_found, certificates_new, total_endpoints`,
		jobID, status, scanErr,
	).Scan(&scanned, &reachable, &found, &newCerts, &total)
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
		SELECT id, status, executor_type, sensor_id::text,
		       array_to_string(targets, chr(31)),
		       array_to_string(ports, ','),
		       timeout_ms, concurrency, total_endpoints, scanned_endpoints,
		       reachable_endpoints, certificates_found, certificates_new,
		       error, created_at, started_at, finished_at
		  FROM scan_jobs WHERE id = $1::uuid`,
		jobID,
	).Scan(&j.ID, &j.Status, &j.ExecutorType, &j.SensorID, &targetsStr, &portsStr, &j.TimeoutMs, &j.Concurrency,
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
		err := tx.QueryRowContext(ctx, upsertCertificateSQL,
			dc.FingerprintSHA256, dc.SerialNumber, dc.SubjectDN, dc.IssuerDN, dc.CommonName,
			strings.Join(dc.SANs, sansSep),
			dc.NotBefore, dc.NotAfter, dc.PublicKeyAlgorithm, dc.PublicKeySize,
			dc.SignatureAlgorithm, string(dc.CertificateType), string(dc.Status),
			dc.SubjectKeyID, dc.AuthorityKeyID,
		).Scan(&id, &status, &createdAt, &updatedAt, &created)
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
	if err := upsertNetworkLocation(ctx, tx, leaf.id, targetID, obs.Port, obs.TLSVersion); err != nil {
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
// TLS 버전을 갱신한다. 부분 유니크 인덱스(uq_certificate_locations_network_endpoint,
// 0006)를 아비터로 쓰는 단일 upsert라 재스캔·동시 실행에도 위치가 증식하지 않는다.
func upsertNetworkLocation(ctx context.Context, tx *sql.Tx, certID, targetID string, port int, tlsVersion string) error {
	var tv *string
	if tlsVersion != "" {
		tv = &tlsVersion
	}
	_, err := tx.ExecContext(ctx, `
		INSERT INTO certificate_locations
			(certificate_id, target_id, location_type, port, tls_version, last_verified_at, deployment_status)
		VALUES ($1, $2, $3, $4, $5, now(), $6)
		ON CONFLICT (certificate_id, target_id, port) WHERE location_type = 'network_endpoint'
		DO UPDATE SET last_verified_at = now(),
		              deployment_status = excluded.deployment_status,
		              tls_version = coalesce(excluded.tls_version, certificate_locations.tls_version)`,
		certID, targetID, string(domain.LocationNetworkEndpoint), port, tv, string(domain.DeployDeployed))
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
