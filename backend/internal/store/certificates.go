// Package store는 도메인 엔티티의 PostgreSQL 영속화를 담당한다.
//
// 타입 변환 규약 (database/sql + pgx stdlib):
//   - text[] 컬럼(sans 등)은 []string 파라미터·스캔을 직접 지원하지 않으므로
//     SQL 쪽 string_to_array / array_to_string(…, chr(31))로 변환한다.
//   - inet 컬럼(source_ip, ip_address)은 Go에서 *string으로 다루고,
//     삽입은 `$n::inet` 캐스트, 조회는 `host(col)`로 문자열 변환한다.
package store

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/Falke060918/INHERIT_AutoCert/backend/internal/domain"
)

// sansSep는 text[] 변환용 구분자(US, 0x1F)다. SAN 값에는 나타나지 않는다.
const sansSep = "\x1f"

// EventCertificateImport는 수동 업로드 감사 이벤트 타입이다.
const EventCertificateImport = "certificate.import"

// 수동 업로드의 설치 위치를 붙일 고정 Target.
// targets.name에 UNIQUE 제약이 없어 동시 업로드 시 중복 생성될 수 있으나,
// MVP는 단일 인스턴스 전제라 허용한다 (docs/01-plan.md).
const (
	manualTargetName = "manual-upload"
	manualTargetType = "manual"
)

// Store는 *sql.DB 위에서 저장 연산을 제공한다.
type Store struct {
	db *sql.DB
}

// New는 Store를 만든다.
func New(db *sql.DB) *Store {
	return &Store{db: db}
}

// ImportInput은 업로드 1건의 저장 요청이다. Certificates는 파일 내 순서를
// 유지한다. 설치 위치는 첫 비-CA 인증서(리프)에 붙는다 — 순서 가정 없음.
type ImportInput struct {
	Certificates []domain.Certificate
	FileName     *string // 업로드 원본 파일명 (certificate_locations.file_path)
	RequestID    *string
	SourceIP     *string
	ActorID      *string // 이슈 #6(로그인+RBAC) 전까지 nil
}

// ImportedCertificate는 인증서 1개의 저장 결과다.
type ImportedCertificate struct {
	Certificate domain.Certificate
	Created     bool // false면 fingerprint 중복 — 기존 레코드 재사용
}

// ImportResult는 업로드 1건의 저장 결과다.
type ImportResult struct {
	Certificates []ImportedCertificate
	LocationID   string
}

const upsertCertificateSQL = `
	INSERT INTO certificates (
		fingerprint_sha256, serial_number, subject_dn, issuer_dn, common_name, sans,
		not_before, not_after, public_key_algorithm, public_key_size,
		signature_algorithm, certificate_type, status
	) VALUES (
		$1, $2, $3, $4, $5,
		coalesce(string_to_array(nullif($6, ''), chr(31)), '{}'),
		$7, $8, $9, $10, $11, $12, $13
	)
	ON CONFLICT (fingerprint_sha256) DO UPDATE SET updated_at = now()
	RETURNING id, status, created_at, updated_at, (xmax = 0) AS created`

// ImportCertificates는 업로드 1건을 단일 트랜잭션으로 저장한다.
// fingerprint 중복이면 새 Certificate를 만들지 않고(status 등 기존 값 유지)
// 설치 위치와 감사 이벤트만 추가한다 (이슈 #3 완료 조건).
func (s *Store) ImportCertificates(ctx context.Context, in ImportInput) (*ImportResult, error) {
	if len(in.Certificates) == 0 {
		return nil, errors.New("store: no certificates to import")
	}

	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, fmt.Errorf("store: begin: %w", err)
	}
	defer tx.Rollback() //nolint:errcheck

	res := &ImportResult{}
	for _, c := range in.Certificates {
		out := c
		var created bool
		var status string
		err := tx.QueryRowContext(ctx, upsertCertificateSQL,
			c.FingerprintSHA256, c.SerialNumber, c.SubjectDN, c.IssuerDN, c.CommonName,
			strings.Join(c.SANs, sansSep),
			c.NotBefore, c.NotAfter, c.PublicKeyAlgorithm, c.PublicKeySize,
			c.SignatureAlgorithm, string(c.CertificateType), string(c.Status),
		).Scan(&out.ID, &status, &out.CreatedAt, &out.UpdatedAt, &created)
		if err != nil {
			return nil, fmt.Errorf("store: upsert certificate: %w", err)
		}
		out.Status = domain.CertificateStatus(status)
		res.Certificates = append(res.Certificates, ImportedCertificate{Certificate: out, Created: created})
	}

	targetID, err := ensureManualTarget(ctx, tx)
	if err != nil {
		return nil, err
	}

	// 리프에만 설치 위치를 붙인다. 체인의 CA는 인벤토리 레코드로만 남는다.
	// 리프 = 첫 비-CA 인증서 (certparse.Describe가 x509 IsCA를 CertTypeCA로 매핑).
	// 파일 내 순서가 CA 먼저여도 안전하다. 전부 CA면 [0] 폴백.
	leaf := res.Certificates[0].Certificate
	for _, ic := range res.Certificates {
		if ic.Certificate.CertificateType != domain.CertTypeCA {
			leaf = ic.Certificate
			break
		}
	}
	err = tx.QueryRowContext(ctx, `
		INSERT INTO certificate_locations (
			certificate_id, target_id, location_type, file_path, deployment_status
		) VALUES ($1, $2, $3, $4, $5)
		RETURNING id`,
		leaf.ID, targetID, string(domain.LocationFile), in.FileName, string(domain.DeployUnknown),
	).Scan(&res.LocationID)
	if err != nil {
		return nil, fmt.Errorf("store: insert location: %w", err)
	}

	// 변경 API는 예외 없이 감사 이벤트를 남긴다 (보안 하드 규칙 5).
	// after_value에는 식별자·다이제스트만 담고 인증서 본문은 넣지 않는다 (규칙 3).
	after := struct {
		CertificateIDs []string `json:"certificate_ids"`
		Fingerprints   []string `json:"fingerprints"`
		DuplicateCount int      `json:"duplicate_count"`
		LocationID     string   `json:"location_id"`
	}{LocationID: res.LocationID}
	for _, ic := range res.Certificates {
		after.CertificateIDs = append(after.CertificateIDs, ic.Certificate.ID)
		after.Fingerprints = append(after.Fingerprints, ic.Certificate.FingerprintSHA256)
		if !ic.Created {
			after.DuplicateCount++
		}
	}
	if err := insertAuditEvent(ctx, tx, auditEvent{
		EventType:  EventCertificateImport,
		ActorID:    in.ActorID,
		TargetType: "certificate",
		TargetID:   &leaf.ID,
		RequestID:  in.RequestID,
		After:      after,
		Result:     domain.AuditSuccess,
		SourceIP:   in.SourceIP,
	}); err != nil {
		return nil, err
	}

	if err := tx.Commit(); err != nil {
		return nil, fmt.Errorf("store: commit: %w", err)
	}
	return res, nil
}

// RecordImportFailure는 거부된 업로드(개인키 포함·파싱 불가)의 감사 이벤트를 남긴다.
// reason에는 사유 코드만 담는다 — 업로드 내용은 절대 담지 않는다 (보안 하드 규칙 3·5).
func (s *Store) RecordImportFailure(ctx context.Context, reason string, requestID, sourceIP *string) error {
	return insertAuditEvent(ctx, s.db, auditEvent{
		EventType:  EventCertificateImport,
		TargetType: "certificate",
		RequestID:  requestID,
		After:      map[string]string{"reason": reason},
		Result:     domain.AuditFailure,
		SourceIP:   sourceIP,
	})
}

func ensureManualTarget(ctx context.Context, tx *sql.Tx) (string, error) {
	var id string
	err := tx.QueryRowContext(ctx,
		`SELECT id FROM targets WHERE name = $1 AND target_type = $2 LIMIT 1`,
		manualTargetName, manualTargetType).Scan(&id)
	if errors.Is(err, sql.ErrNoRows) {
		err = tx.QueryRowContext(ctx,
			`INSERT INTO targets (name, target_type) VALUES ($1, $2) RETURNING id`,
			manualTargetName, manualTargetType).Scan(&id)
	}
	if err != nil {
		return "", fmt.Errorf("store: ensure manual target: %w", err)
	}
	return id, nil
}

// auditEvent는 감사 이벤트 삽입 입력이다. After는 JSON 직렬화 가능해야 하며
// 민감값(인증서 본문·개인키)을 담지 않는다.
type auditEvent struct {
	EventType  string
	ActorID    *string
	TargetType string
	TargetID   *string
	RequestID  *string
	After      any // nil이면 NULL
	Result     domain.AuditResult
	SourceIP   *string
}

// execer는 *sql.Tx와 *sql.DB를 모두 받기 위한 최소 인터페이스다.
type execer interface {
	ExecContext(ctx context.Context, query string, args ...any) (sql.Result, error)
}

func insertAuditEvent(ctx context.Context, ex execer, ev auditEvent) error {
	var after []byte
	if ev.After != nil {
		b, err := json.Marshal(ev.After)
		if err != nil {
			return fmt.Errorf("store: marshal audit after_value: %w", err)
		}
		after = b
	}
	_, err := ex.ExecContext(ctx, `
		INSERT INTO audit_events (
			event_type, actor_id, target_type, target_id, request_id,
			before_value, after_value, result, source_ip, integrity_hash
		) VALUES ($1, $2::uuid, $3, $4, $5, NULL, $6::jsonb, $7, $8::inet, $9)`,
		ev.EventType, ev.ActorID, ev.TargetType, ev.TargetID, ev.RequestID,
		after, string(ev.Result), ev.SourceIP, integrityHash(ev, after),
	)
	if err != nil {
		return fmt.Errorf("store: insert audit event: %w", err)
	}
	return nil
}

// integrityHash는 이벤트 필드의 SHA-256 단독 해시다(표준 라이브러리 사용).
// 해시 체인 연결·검증 API는 이슈 #7(감사 로그)에서 구현한다 — 임시 자리값.
func integrityHash(ev auditEvent, after []byte) string {
	deref := func(p *string) string {
		if p == nil {
			return ""
		}
		return *p
	}
	canonical := strings.Join([]string{
		ev.EventType, deref(ev.ActorID), ev.TargetType, deref(ev.TargetID),
		deref(ev.RequestID), string(after), string(ev.Result), deref(ev.SourceIP),
	}, "\x00")
	sum := sha256.Sum256([]byte(canonical))
	return hex.EncodeToString(sum[:])
}
