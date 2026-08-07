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
type ScanInput struct {
	Targets     []string // IP/CIDR 입력 원문 (재현·감사용)
	Ports       []int
	TimeoutMs   int
	Concurrency int
	ActorID     *string
	RequestID   *string
	SourceIP    *string
}

// ScanJob은 스캔 Job 1건의 상태·진행률·결과 요약이다.
type ScanJob struct {
	ID                 string     `json:"id"`
	Status             string     `json:"status"`
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
	ServerName string
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

	var id string
	err = tx.QueryRowContext(ctx, `
		INSERT INTO scan_jobs (targets, ports, timeout_ms, concurrency, status)
		VALUES (
			coalesce(string_to_array(nullif($1, ''), chr(31)), '{}'),
			coalesce(string_to_array(nullif($2, ''), ','), '{}')::int[],
			$3, $4, 'pending')
		RETURNING id`,
		strings.Join(in.Targets, sansSep), joinInts(in.Ports),
		in.TimeoutMs, in.Concurrency,
	).Scan(&id)
	if err != nil {
		return "", fmt.Errorf("store: insert scan job: %w", err)
	}

	after := map[string]any{
		"phase":       "created",
		"targets":     in.Targets,
		"ports":       in.Ports,
		"timeout_ms":  in.TimeoutMs,
		"concurrency": in.Concurrency,
	}
	if err := insertAuditEvent(ctx, tx, auditEvent{
		EventType:  EventDiscoveryScan,
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
func (s *Store) RecordEndpointResult(ctx context.Context, jobID string, obs EndpointObservation) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("store: begin: %w", err)
	}
	defer tx.Rollback() //nolint:errcheck

	reachable := 0
	certsFound := 0
	certsNew := 0
	if obs.Reachable {
		reachable = 1
		if len(obs.Chain) > 0 {
			n, err := recordDiscoveredChain(ctx, tx, obs)
			if err != nil {
				return err
			}
			certsFound = len(obs.Chain)
			certsNew = n
		}
	}

	if _, err := tx.ExecContext(ctx, `
		UPDATE scan_jobs SET
			scanned_endpoints   = scanned_endpoints + 1,
			reachable_endpoints = reachable_endpoints + $2,
			certificates_found  = certificates_found + $3,
			certificates_new    = certificates_new + $4
		 WHERE id = $1::uuid`,
		jobID, reachable, certsFound, certsNew); err != nil {
		return fmt.Errorf("store: update scan counters: %w", err)
	}

	if err := tx.Commit(); err != nil {
		return fmt.Errorf("store: commit: %w", err)
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
	if err := insertAuditEvent(ctx, tx, auditEvent{
		EventType:  EventDiscoveryScan,
		TargetType: "scan_job",
		TargetID:   &jobID,
		After:      after,
		Result:     result,
	}); err != nil {
		return err
	}

	if err := tx.Commit(); err != nil {
		return fmt.Errorf("store: commit: %w", err)
	}
	return nil
}

// GetScanJob은 Job 1건을 조회한다. 없으면 ErrScanJobNotFound.
func (s *Store) GetScanJob(ctx context.Context, jobID string) (*ScanJob, error) {
	var (
		j          ScanJob
		targetsStr string
		portsStr   string
	)
	err := s.db.QueryRowContext(ctx, `
		SELECT id, status,
		       array_to_string(targets, chr(31)),
		       array_to_string(ports, ','),
		       timeout_ms, concurrency, total_endpoints, scanned_endpoints,
		       reachable_endpoints, certificates_found, certificates_new,
		       error, created_at, started_at, finished_at
		  FROM scan_jobs WHERE id = $1::uuid`,
		jobID,
	).Scan(&j.ID, &j.Status, &targetsStr, &portsStr, &j.TimeoutMs, &j.Concurrency,
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
// 새로 만들어진 인증서 수를 반환한다.
func recordDiscoveredChain(ctx context.Context, tx *sql.Tx, obs EndpointObservation) (int, error) {
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

	targetID, err := ensureNetworkTarget(ctx, tx, obs.IP, obs.ServerName)
	if err != nil {
		return 0, err
	}
	if err := upsertNetworkLocation(ctx, tx, leaf.id, targetID, obs.Port); err != nil {
		return 0, err
	}
	return newCount, nil
}

// ensureNetworkTarget은 IP로 대상 시스템을 찾거나 만들고 reachable로 표시한다.
func ensureNetworkTarget(ctx context.Context, tx *sql.Tx, ip, hostname string) (string, error) {
	var id string
	err := tx.QueryRowContext(ctx,
		`SELECT id FROM targets WHERE ip_address = $1::inet AND target_type = $2 LIMIT 1`,
		ip, networkTargetType).Scan(&id)
	if errors.Is(err, sql.ErrNoRows) {
		var hn *string
		if hostname != "" {
			hn = &hostname
		}
		err = tx.QueryRowContext(ctx, `
			INSERT INTO targets (name, target_type, hostname, ip_address, connection_status)
			VALUES ($1, $2, $3, $4::inet, 'reachable') RETURNING id`,
			ip, networkTargetType, hn, ip).Scan(&id)
		if err != nil {
			return "", fmt.Errorf("store: insert network target: %w", err)
		}
		return id, nil
	}
	if err != nil {
		return "", fmt.Errorf("store: find network target: %w", err)
	}
	if _, err := tx.ExecContext(ctx,
		`UPDATE targets SET connection_status = 'reachable' WHERE id = $1`, id); err != nil {
		return "", fmt.Errorf("store: update target status: %w", err)
	}
	return id, nil
}

// upsertNetworkLocation은 (인증서, 대상, 포트) 위치를 만들거나 최근 확인 시각을 갱신한다.
// 재스캔 시 위치가 중복 생성되지 않게 한다(중복 실행 안전).
func upsertNetworkLocation(ctx context.Context, tx *sql.Tx, certID, targetID string, port int) error {
	var id string
	err := tx.QueryRowContext(ctx, `
		SELECT id FROM certificate_locations
		 WHERE certificate_id = $1 AND target_id = $2 AND location_type = $3 AND port = $4
		 LIMIT 1`,
		certID, targetID, string(domain.LocationNetworkEndpoint), port).Scan(&id)
	if errors.Is(err, sql.ErrNoRows) {
		_, err = tx.ExecContext(ctx, `
			INSERT INTO certificate_locations
				(certificate_id, target_id, location_type, port, last_verified_at, deployment_status)
			VALUES ($1, $2, $3, $4, now(), $5)`,
			certID, targetID, string(domain.LocationNetworkEndpoint), port, string(domain.DeployDeployed))
		if err != nil {
			return fmt.Errorf("store: insert network location: %w", err)
		}
		return nil
	}
	if err != nil {
		return fmt.Errorf("store: find network location: %w", err)
	}
	if _, err := tx.ExecContext(ctx, `
		UPDATE certificate_locations
		   SET last_verified_at = now(), deployment_status = $2
		 WHERE id = $1`,
		id, string(domain.DeployDeployed)); err != nil {
		return fmt.Errorf("store: update network location: %w", err)
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
