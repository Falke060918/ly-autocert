// sensors.go — 센서(원격 수집기, Phase 3a)의 영속화 (docs/00-spec.md 7.8·12장, 마이그레이션 0009).
// 1회용 등록 토큰은 SHA-256 해시만 저장하고 평문은 발급 응답으로만 반환한다.
// 토큰 평문·개인키·인증서 본문은 저장·감사·로그·에러 어디에도 넣지 않는다 (보안 하드 규칙 1·3).
package store

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/Falke060918/INHERIT_AutoCert/backend/internal/audit"
	"github.com/Falke060918/INHERIT_AutoCert/backend/internal/domain"
)

// 센서 감사 이벤트 타입 — 등록·비활성화·토큰 발급은 전건 기록한다 (00-spec 7.8).
const (
	EventSensorTokenIssue = "sensor.token.issue"
	EventSensorEnroll     = "sensor.enroll"
	EventSensorRenew      = "sensor.renew"
	EventSensorDisable    = "sensor.disable"
)

// enrollTokenTTL은 1회용 등록 토큰의 유효기간이다.
const enrollTokenTTL = 24 * time.Hour

// ErrSensorNotFound는 조건에 맞는 센서가 없을 때 반환한다.
var ErrSensorNotFound = errors.New("store: sensor not found")

// ErrEnrollTokenInvalid는 등록 토큰이 없거나·이미 소진됐거나·만료된 경우다.
// 세 경우를 구분하지 않는다 — 응답 구분은 토큰 추측 단서가 된다.
var ErrEnrollTokenInvalid = errors.New("store: enrollment token invalid or expired")

// ErrSensorCANotFound는 센서 CA가 아직 생성되지 않았을 때 반환한다.
var ErrSensorCANotFound = errors.New("store: sensor ca not found")

// ErrSensorCAExists는 센서 CA 단일 행이 이미 존재할 때 반환한다.
// 호출부는 LoadSensorCA로 다시 읽는다 (동시 부트스트랩 경합 안전).
var ErrSensorCAExists = errors.New("store: sensor ca already exists")

// sensorColumns는 센서 조회 공통 컬럼이다. enrollment_token_hash는
// 대조 전용이므로 어떤 조회에도 포함하지 않는다.
const sensorColumns = `id, name, status, token_expires_at, client_cert_fingerprint_sha256,
	registered_at, last_seen_at, disabled_at, created_at, updated_at`

// rowScanner는 *sql.Row / *sql.Rows 공통 스캔 인터페이스다.
type rowScanner interface{ Scan(dest ...any) error }

func scanSensor(row rowScanner) (*domain.Sensor, error) {
	var sn domain.Sensor
	var status string
	if err := row.Scan(&sn.ID, &sn.Name, &status, &sn.TokenExpiresAt,
		&sn.ClientCertFingerprintSHA256, &sn.RegisteredAt, &sn.LastSeenAt,
		&sn.DisabledAt, &sn.CreatedAt, &sn.UpdatedAt); err != nil {
		return nil, err
	}
	sn.Status = domain.SensorStatus(status)
	return &sn, nil
}

// newEnrollmentToken은 1회용 등록 토큰(crypto/rand 32바이트 hex)과
// 저장용 SHA-256 해시(hex)를 만든다. 토큰 평문은 반환값 외 어디에도 남기지 않는다.
func newEnrollmentToken() (token, tokenHash string, err error) {
	buf := make([]byte, 32)
	if _, err := rand.Read(buf); err != nil {
		return "", "", fmt.Errorf("store: generate enrollment token: %w", err)
	}
	token = hex.EncodeToString(buf)
	sum := sha256.Sum256([]byte(token))
	return token, hex.EncodeToString(sum[:]), nil
}

// CreateSensorInput은 센서 생성(=등록 토큰 발급) 요청이다.
type CreateSensorInput struct {
	Name      string
	ActorID   *string
	RequestID *string
	SourceIP  *string
}

// CreateSensor는 pending 센서를 만들고 1회용 등록 토큰을 발급한다.
// DB에는 토큰의 SHA-256 해시만 저장하며(유효기간 24h), 평문 토큰은 반환값으로만
// 1회 전달된다 — 이후 서버는 평문을 복원할 수 없다. 감사(sensor.token.issue) 동일 트랜잭션.
func (s *Store) CreateSensor(ctx context.Context, in CreateSensorInput) (*domain.Sensor, string, error) {
	name := strings.TrimSpace(in.Name)
	if name == "" {
		return nil, "", errors.New("store: sensor name required")
	}
	token, tokenHash, err := newEnrollmentToken()
	if err != nil {
		return nil, "", err
	}

	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, "", fmt.Errorf("store: begin: %w", err)
	}
	defer tx.Rollback() //nolint:errcheck

	sn, err := scanSensor(tx.QueryRowContext(ctx, `
		INSERT INTO sensors (name, enrollment_token_hash, token_expires_at)
		VALUES ($1, $2, $3)
		RETURNING `+sensorColumns,
		name, tokenHash, time.Now().Add(enrollTokenTTL)))
	if err != nil {
		return nil, "", fmt.Errorf("store: insert sensor: %w", err)
	}

	// 감사에는 식별자·만료 시각만 담는다 — 토큰 평문·해시는 담지 않는다.
	if err := audit.Insert(ctx, tx, audit.Event{
		Type:       EventSensorTokenIssue,
		ActorID:    in.ActorID,
		TargetType: "sensor",
		TargetID:   &sn.ID,
		RequestID:  in.RequestID,
		After: map[string]any{
			"name":             sn.Name,
			"token_expires_at": sn.TokenExpiresAt,
		},
		Result:   domain.AuditSuccess,
		SourceIP: in.SourceIP,
	}); err != nil {
		return nil, "", err
	}

	if err := tx.Commit(); err != nil {
		return nil, "", fmt.Errorf("store: commit: %w", err)
	}
	return sn, token, nil
}

// EnrollSensorInput은 센서 등록(토큰 소진 + 클라이언트 인증서 fingerprint 기록) 요청이다.
type EnrollSensorInput struct {
	Token                       string // 1회용 등록 토큰 평문 (해시는 store가 계산)
	ClientCertFingerprintSHA256 string // 발급한 클라이언트 인증서 fingerprint (hex 64자)
	RequestID                   *string
	SourceIP                    *string
}

// EnrollSensor는 등록 토큰을 원자적으로 소진한다: 단일 UPDATE가
// pending + 해시 일치 + 미만료를 동시에 검사하므로 같은 토큰의 동시 등록은
// 정확히 1건만 성공한다. 성공 시 active 전이 + fingerprint·registered_at 기록.
// 실패(무효·소진·만료)도 감사(sensor.enroll, failure)를 남기고 ErrEnrollTokenInvalid를 반환한다.
func (s *Store) EnrollSensor(ctx context.Context, in EnrollSensorInput) (*domain.Sensor, error) {
	sum := sha256.Sum256([]byte(in.Token))
	tokenHash := hex.EncodeToString(sum[:])
	fp := strings.ToLower(strings.TrimSpace(in.ClientCertFingerprintSHA256))

	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, fmt.Errorf("store: begin: %w", err)
	}
	defer tx.Rollback() //nolint:errcheck

	sn, err := scanSensor(tx.QueryRowContext(ctx, `
		UPDATE sensors
		   SET status = 'active',
		       client_cert_fingerprint_sha256 = $2,
		       registered_at = now(),
		       updated_at = now()
		 WHERE status = 'pending'
		   AND enrollment_token_hash = $1
		   AND token_expires_at > now()
		RETURNING `+sensorColumns,
		tokenHash, fp))
	if errors.Is(err, sql.ErrNoRows) {
		// 실패도 전건 감사 — 사유 코드만 담고 토큰 관련 값은 담지 않는다.
		if err := audit.Insert(ctx, tx, audit.Event{
			Type:       EventSensorEnroll,
			TargetType: "sensor",
			RequestID:  in.RequestID,
			After:      map[string]string{"reason": "invalid_or_expired_token"},
			Result:     domain.AuditFailure,
			SourceIP:   in.SourceIP,
		}); err != nil {
			return nil, err
		}
		if err := tx.Commit(); err != nil {
			return nil, fmt.Errorf("store: commit: %w", err)
		}
		return nil, ErrEnrollTokenInvalid
	}
	if err != nil {
		return nil, fmt.Errorf("store: enroll sensor: %w", err)
	}

	if err := audit.Insert(ctx, tx, audit.Event{
		Type:       EventSensorEnroll,
		TargetType: "sensor",
		TargetID:   &sn.ID,
		RequestID:  in.RequestID,
		After: map[string]any{
			"name":                           sn.Name,
			"client_cert_fingerprint_sha256": fp,
		},
		Result:   domain.AuditSuccess,
		SourceIP: in.SourceIP,
	}); err != nil {
		return nil, err
	}

	if err := tx.Commit(); err != nil {
		return nil, fmt.Errorf("store: commit: %w", err)
	}
	return sn, nil
}

// GetActiveSensorByFingerprint는 mTLS 클라이언트 인증서 fingerprint로
// active 센서를 찾는다 (mTLS 미들웨어의 아이덴티티 대조). 없으면 ErrSensorNotFound.
func (s *Store) GetActiveSensorByFingerprint(ctx context.Context, fingerprint string) (*domain.Sensor, error) {
	fp := strings.ToLower(strings.TrimSpace(fingerprint))
	sn, err := scanSensor(s.db.QueryRowContext(ctx, `
		SELECT `+sensorColumns+`
		  FROM sensors
		 WHERE client_cert_fingerprint_sha256 = $1 AND status = 'active'`, fp))
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrSensorNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("store: get sensor by fingerprint: %w", err)
	}
	return sn, nil
}

// TouchSensorLastSeen은 last_seen_at을 갱신한다. 하트비트·요청마다 불리는
// best-effort 텔레메트리라 호출부는 실패를 무시해도 된다 (updated_at은 건드리지 않는다).
func (s *Store) TouchSensorLastSeen(ctx context.Context, sensorID string) error {
	if _, err := s.db.ExecContext(ctx,
		`UPDATE sensors SET last_seen_at = now() WHERE id = $1::uuid`, sensorID); err != nil {
		return fmt.Errorf("store: touch sensor last seen: %w", err)
	}
	return nil
}

// RenewSensorInput은 클라이언트 인증서 갱신(fingerprint 교체) 요청이다.
type RenewSensorInput struct {
	OldFingerprint string // 현재 mTLS 인증서 fingerprint (아이덴티티 증명)
	NewFingerprint string // 새로 서명한 인증서 fingerprint
	RequestID      *string
	SourceIP       *string
}

// RenewSensorFingerprint는 active 센서의 fingerprint를 원자적으로 교체한다:
// 단일 UPDATE가 기존 fingerprint 일치를 조건으로 걸어, 동시 갱신·이미 교체된
// 인증서로의 재시도는 ErrSensorNotFound로 실패한다. 감사(sensor.renew) 동일 트랜잭션.
func (s *Store) RenewSensorFingerprint(ctx context.Context, in RenewSensorInput) (*domain.Sensor, error) {
	oldFP := strings.ToLower(strings.TrimSpace(in.OldFingerprint))
	newFP := strings.ToLower(strings.TrimSpace(in.NewFingerprint))

	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, fmt.Errorf("store: begin: %w", err)
	}
	defer tx.Rollback() //nolint:errcheck

	sn, err := scanSensor(tx.QueryRowContext(ctx, `
		UPDATE sensors
		   SET client_cert_fingerprint_sha256 = $2, updated_at = now()
		 WHERE client_cert_fingerprint_sha256 = $1 AND status = 'active'
		RETURNING `+sensorColumns,
		oldFP, newFP))
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrSensorNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("store: renew sensor fingerprint: %w", err)
	}

	if err := audit.Insert(ctx, tx, audit.Event{
		Type:       EventSensorRenew,
		TargetType: "sensor",
		TargetID:   &sn.ID,
		RequestID:  in.RequestID,
		After: map[string]any{
			"old_fingerprint": oldFP,
			"new_fingerprint": newFP,
		},
		Result:   domain.AuditSuccess,
		SourceIP: in.SourceIP,
	}); err != nil {
		return nil, err
	}

	if err := tx.Commit(); err != nil {
		return nil, fmt.Errorf("store: commit: %w", err)
	}
	return sn, nil
}

// DisableSensorInput은 관리자 비활성화 요청이다.
type DisableSensorInput struct {
	SensorID  string
	ActorID   *string
	RequestID *string
	SourceIP  *string
}

// DisableSensor는 센서를 disabled로 전이한다(pending·active 모두 허용 —
// 미등록 센서의 토큰 회수 용도 포함). 이후 mTLS 대조·등록에서 제외된다.
// 없거나 이미 disabled면 ErrSensorNotFound. 감사(sensor.disable) 동일 트랜잭션.
func (s *Store) DisableSensor(ctx context.Context, in DisableSensorInput) (*domain.Sensor, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, fmt.Errorf("store: begin: %w", err)
	}
	defer tx.Rollback() //nolint:errcheck

	sn, err := scanSensor(tx.QueryRowContext(ctx, `
		UPDATE sensors
		   SET status = 'disabled', disabled_at = now(), updated_at = now()
		 WHERE id = $1::uuid AND status <> 'disabled'
		RETURNING `+sensorColumns,
		in.SensorID))
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrSensorNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("store: disable sensor: %w", err)
	}

	if err := audit.Insert(ctx, tx, audit.Event{
		Type:       EventSensorDisable,
		ActorID:    in.ActorID,
		TargetType: "sensor",
		TargetID:   &sn.ID,
		RequestID:  in.RequestID,
		After:      map[string]any{"name": sn.Name, "status": string(sn.Status)},
		Result:     domain.AuditSuccess,
		SourceIP:   in.SourceIP,
	}); err != nil {
		return nil, err
	}

	if err := tx.Commit(); err != nil {
		return nil, fmt.Errorf("store: commit: %w", err)
	}
	return sn, nil
}

// ListSensors는 센서 전체를 최신 생성 순으로 반환한다.
func (s *Store) ListSensors(ctx context.Context) ([]domain.Sensor, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT `+sensorColumns+` FROM sensors ORDER BY created_at DESC, id`)
	if err != nil {
		return nil, fmt.Errorf("store: list sensors: %w", err)
	}
	defer rows.Close()

	out := []domain.Sensor{}
	for rows.Next() {
		sn, err := scanSensor(rows)
		if err != nil {
			return nil, fmt.Errorf("store: scan sensor: %w", err)
		}
		out = append(out, *sn)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("store: list sensors: %w", err)
	}
	return out, nil
}

// GetSensor는 센서 1건을 조회한다. 없으면 ErrSensorNotFound.
func (s *Store) GetSensor(ctx context.Context, sensorID string) (*domain.Sensor, error) {
	sn, err := scanSensor(s.db.QueryRowContext(ctx, `
		SELECT `+sensorColumns+` FROM sensors WHERE id = $1::uuid`, sensorID))
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrSensorNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("store: get sensor: %w", err)
	}
	return sn, nil
}

// SensorCARecord는 센서 mTLS 내부 CA의 저장 형태다 (sensor_ca 단일 행, 0009).
// 개인키는 sensorca.Seal이 만든 AES-256-GCM 암호문+nonce로만 저장한다 —
// 평문 키는 이 패키지를 거치지 않는다 (보안 하드 규칙 1).
type SensorCARecord struct {
	CertPEM       []byte
	KeyCiphertext []byte
	KeyNonce      []byte
	CreatedAt     time.Time
}

// SaveSensorCA는 센서 CA 단일 행을 생성한다. 이미 있으면 ErrSensorCAExists —
// 동시 부트스트랩 경합 시 진 쪽은 LoadSensorCA로 다시 읽는다. CA 교체(회전)는
// 명시적 운영 절차가 필요하므로 덮어쓰기를 제공하지 않는다.
func (s *Store) SaveSensorCA(ctx context.Context, rec SensorCARecord) error {
	if len(rec.CertPEM) == 0 || len(rec.KeyCiphertext) == 0 || len(rec.KeyNonce) == 0 {
		return errors.New("store: sensor ca record incomplete")
	}
	res, err := s.db.ExecContext(ctx, `
		INSERT INTO sensor_ca (ca_cert_pem, ca_key_ciphertext, ca_key_nonce)
		VALUES ($1, $2, $3)
		ON CONFLICT (id) DO NOTHING`,
		string(rec.CertPEM), rec.KeyCiphertext, rec.KeyNonce)
	if err != nil {
		return fmt.Errorf("store: save sensor ca: %w", err)
	}
	n, err := res.RowsAffected()
	if err != nil {
		return fmt.Errorf("store: save sensor ca: %w", err)
	}
	if n == 0 {
		return ErrSensorCAExists
	}
	return nil
}

// LoadSensorCA는 센서 CA 단일 행을 읽는다. 없으면 ErrSensorCANotFound.
func (s *Store) LoadSensorCA(ctx context.Context) (*SensorCARecord, error) {
	var rec SensorCARecord
	var certPEM string
	err := s.db.QueryRowContext(ctx, `
		SELECT ca_cert_pem, ca_key_ciphertext, ca_key_nonce, created_at FROM sensor_ca`).
		Scan(&certPEM, &rec.KeyCiphertext, &rec.KeyNonce, &rec.CreatedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrSensorCANotFound
	}
	if err != nil {
		return nil, fmt.Errorf("store: load sensor ca: %w", err)
	}
	rec.CertPEM = []byte(certPEM)
	return &rec, nil
}
