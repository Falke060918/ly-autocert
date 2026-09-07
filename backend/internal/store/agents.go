// agents.go — 에이전트(원격 수집기, Phase 3a)의 영속화 (docs/00-spec.md 7.8·12장, 마이그레이션 0009).
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

// 에이전트 감사 이벤트 타입 — 등록·비활성화·토큰 발급은 전건 기록한다 (00-spec 7.8).
const (
	EventAgentTokenIssue = "agent.token.issue"
	EventAgentEnroll     = "agent.enroll"
	EventAgentRenew      = "agent.renew"
	EventAgentDisable    = "agent.disable"
)

// agentEnrollTokenTTL은 1회용 등록 토큰의 유효기간이다.
const agentEnrollTokenTTL = 24 * time.Hour

// ErrAgentNotFound는 조건에 맞는 에이전트가 없을 때 반환한다.
var ErrAgentNotFound = errors.New("store: agent not found")

// ErrAgentEnrollTokenInvalid는 등록 토큰이 없거나·이미 소진됐거나·만료된 경우다.
// 세 경우를 구분하지 않는다 — 응답 구분은 토큰 추측 단서가 된다.
var ErrAgentEnrollTokenInvalid = errors.New("store: enrollment token invalid or expired")

// agentColumns는 에이전트 조회 공통 컬럼이다. enrollment_token_hash는
// 대조 전용이므로 어떤 조회에도 포함하지 않는다.
const agentColumns = `id, name, status, token_expires_at, client_cert_fingerprint_sha256,
	registered_at, last_seen_at, disabled_at, created_at, updated_at`

func scanAgent(row rowScanner) (*domain.Agent, error) {
	var sn domain.Agent
	var status string
	if err := row.Scan(&sn.ID, &sn.Name, &status, &sn.TokenExpiresAt,
		&sn.ClientCertFingerprintSHA256, &sn.RegisteredAt, &sn.LastSeenAt,
		&sn.DisabledAt, &sn.CreatedAt, &sn.UpdatedAt); err != nil {
		return nil, err
	}
	sn.Status = domain.AgentStatus(status)
	return &sn, nil
}

// newAgentEnrollmentToken은 1회용 등록 토큰(crypto/rand 32바이트 hex)과
// 저장용 SHA-256 해시(hex)를 만든다. 토큰 평문은 반환값 외 어디에도 남기지 않는다.
func newAgentEnrollmentToken() (token, tokenHash string, err error) {
	buf := make([]byte, 32)
	if _, err := rand.Read(buf); err != nil {
		return "", "", fmt.Errorf("store: generate enrollment token: %w", err)
	}
	token = hex.EncodeToString(buf)
	sum := sha256.Sum256([]byte(token))
	return token, hex.EncodeToString(sum[:]), nil
}

// CreateAgentInput은 에이전트 생성(=등록 토큰 발급) 요청이다.
type CreateAgentInput struct {
	Name      string
	ActorID   *string
	RequestID *string
	SourceIP  *string
}

// CreateAgent는 pending 에이전트를 만들고 1회용 등록 토큰을 발급한다.
// DB에는 토큰의 SHA-256 해시만 저장하며(유효기간 24h), 평문 토큰은 반환값으로만
// 1회 전달된다 — 이후 서버는 평문을 복원할 수 없다. 감사(agent.token.issue) 동일 트랜잭션.
func (s *Store) CreateAgent(ctx context.Context, in CreateAgentInput) (*domain.Agent, string, error) {
	name := strings.TrimSpace(in.Name)
	if name == "" {
		return nil, "", errors.New("store: agent name required")
	}
	token, tokenHash, err := newAgentEnrollmentToken()
	if err != nil {
		return nil, "", err
	}

	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, "", fmt.Errorf("store: begin: %w", err)
	}
	defer tx.Rollback() //nolint:errcheck

	sn, err := scanAgent(tx.QueryRowContext(ctx, `
		INSERT INTO agents (name, enrollment_token_hash, token_expires_at)
		VALUES ($1, $2, $3)
		RETURNING `+agentColumns,
		name, tokenHash, time.Now().Add(agentEnrollTokenTTL)))
	if err != nil {
		return nil, "", fmt.Errorf("store: insert agent: %w", err)
	}

	// 감사에는 식별자·만료 시각만 담는다 — 토큰 평문·해시는 담지 않는다.
	if err := audit.Insert(ctx, tx, audit.Event{
		Type:       EventAgentTokenIssue,
		ActorID:    in.ActorID,
		TargetType: "agent",
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

// EnrollAgentInput은 에이전트 등록(토큰 소진 + 클라이언트 인증서 fingerprint 기록) 요청이다.
type EnrollAgentInput struct {
	Token                       string // 1회용 등록 토큰 평문 (해시는 store가 계산)
	ClientCertFingerprintSHA256 string // 발급한 클라이언트 인증서 fingerprint (hex 64자)
	RequestID                   *string
	SourceIP                    *string
}

// EnrollAgent는 등록 토큰을 원자적으로 소진한다: 단일 UPDATE가
// pending + 해시 일치 + 미만료를 동시에 검사하므로 같은 토큰의 동시 등록은
// 정확히 1건만 성공한다. 성공 시 active 전이 + fingerprint·registered_at 기록.
// 실패(무효·소진·만료)도 감사(agent.enroll, failure)를 남기고 ErrAgentEnrollTokenInvalid를 반환한다.
func (s *Store) EnrollAgent(ctx context.Context, in EnrollAgentInput) (*domain.Agent, error) {
	sum := sha256.Sum256([]byte(in.Token))
	tokenHash := hex.EncodeToString(sum[:])
	fp := strings.ToLower(strings.TrimSpace(in.ClientCertFingerprintSHA256))

	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, fmt.Errorf("store: begin: %w", err)
	}
	defer tx.Rollback() //nolint:errcheck

	sn, err := scanAgent(tx.QueryRowContext(ctx, `
		UPDATE agents
		   SET status = 'active',
		       client_cert_fingerprint_sha256 = $2,
		       registered_at = now(),
		       updated_at = now()
		 WHERE status = 'pending'
		   AND enrollment_token_hash = $1
		   AND token_expires_at > now()
		RETURNING `+agentColumns,
		tokenHash, fp))
	if errors.Is(err, sql.ErrNoRows) {
		// 실패도 전건 감사 — 사유 코드만 담고 토큰 관련 값은 담지 않는다.
		if err := audit.Insert(ctx, tx, audit.Event{
			Type:       EventAgentEnroll,
			TargetType: "agent",
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
		return nil, ErrAgentEnrollTokenInvalid
	}
	if err != nil {
		return nil, fmt.Errorf("store: enroll agent: %w", err)
	}

	if err := audit.Insert(ctx, tx, audit.Event{
		Type:       EventAgentEnroll,
		TargetType: "agent",
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

// GetActiveAgentByFingerprint는 mTLS 클라이언트 인증서 fingerprint로
// active 에이전트를 찾는다 (mTLS 미들웨어의 아이덴티티 대조). 없으면 ErrAgentNotFound.
func (s *Store) GetActiveAgentByFingerprint(ctx context.Context, fingerprint string) (*domain.Agent, error) {
	fp := strings.ToLower(strings.TrimSpace(fingerprint))
	sn, err := scanAgent(s.db.QueryRowContext(ctx, `
		SELECT `+agentColumns+`
		  FROM agents
		 WHERE client_cert_fingerprint_sha256 = $1 AND status = 'active'`, fp))
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrAgentNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("store: get agent by fingerprint: %w", err)
	}
	return sn, nil
}

// TouchAgentLastSeen은 last_seen_at을 갱신한다. 하트비트·요청마다 불리는
// best-effort 텔레메트리라 호출부는 실패를 무시해도 된다 (updated_at은 건드리지 않는다).
func (s *Store) TouchAgentLastSeen(ctx context.Context, agentID string) error {
	if _, err := s.db.ExecContext(ctx,
		`UPDATE agents SET last_seen_at = now() WHERE id = $1::uuid`, agentID); err != nil {
		return fmt.Errorf("store: touch agent last seen: %w", err)
	}
	return nil
}

// RenewAgentInput은 클라이언트 인증서 갱신(fingerprint 교체) 요청이다.
type RenewAgentInput struct {
	OldFingerprint string // 현재 mTLS 인증서 fingerprint (아이덴티티 증명)
	NewFingerprint string // 새로 서명한 인증서 fingerprint
	RequestID      *string
	SourceIP       *string
}

// RenewAgentFingerprint는 active 에이전트의 fingerprint를 원자적으로 교체한다:
// 단일 UPDATE가 기존 fingerprint 일치를 조건으로 걸어, 동시 갱신·이미 교체된
// 인증서로의 재시도는 ErrAgentNotFound로 실패한다. 감사(agent.renew) 동일 트랜잭션.
func (s *Store) RenewAgentFingerprint(ctx context.Context, in RenewAgentInput) (*domain.Agent, error) {
	oldFP := strings.ToLower(strings.TrimSpace(in.OldFingerprint))
	newFP := strings.ToLower(strings.TrimSpace(in.NewFingerprint))

	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, fmt.Errorf("store: begin: %w", err)
	}
	defer tx.Rollback() //nolint:errcheck

	sn, err := scanAgent(tx.QueryRowContext(ctx, `
		UPDATE agents
		   SET client_cert_fingerprint_sha256 = $2, updated_at = now()
		 WHERE client_cert_fingerprint_sha256 = $1 AND status = 'active'
		RETURNING `+agentColumns,
		oldFP, newFP))
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrAgentNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("store: renew agent fingerprint: %w", err)
	}

	if err := audit.Insert(ctx, tx, audit.Event{
		Type:       EventAgentRenew,
		TargetType: "agent",
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

// DisableAgentInput은 관리자 비활성화 요청이다.
type DisableAgentInput struct {
	AgentID   string
	ActorID   *string
	RequestID *string
	SourceIP  *string
}

// DisableAgent는 에이전트를 disabled로 전이한다(pending·active 모두 허용 —
// 미등록 에이전트의 토큰 회수 용도 포함). 이후 mTLS 대조·등록에서 제외된다.
// 없거나 이미 disabled면 ErrAgentNotFound. 감사(agent.disable) 동일 트랜잭션.
func (s *Store) DisableAgent(ctx context.Context, in DisableAgentInput) (*domain.Agent, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, fmt.Errorf("store: begin: %w", err)
	}
	defer tx.Rollback() //nolint:errcheck

	sn, err := scanAgent(tx.QueryRowContext(ctx, `
		UPDATE agents
		   SET status = 'disabled', disabled_at = now(), updated_at = now()
		 WHERE id = $1::uuid AND status <> 'disabled'
		RETURNING `+agentColumns,
		in.AgentID))
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrAgentNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("store: disable agent: %w", err)
	}

	if err := audit.Insert(ctx, tx, audit.Event{
		Type:       EventAgentDisable,
		ActorID:    in.ActorID,
		TargetType: "agent",
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

// ListAgents는 에이전트 전체를 최신 생성 순으로 반환한다.
func (s *Store) ListAgents(ctx context.Context) ([]domain.Agent, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT `+agentColumns+` FROM agents ORDER BY created_at DESC, id`)
	if err != nil {
		return nil, fmt.Errorf("store: list agents: %w", err)
	}
	defer rows.Close()

	out := []domain.Agent{}
	for rows.Next() {
		sn, err := scanAgent(rows)
		if err != nil {
			return nil, fmt.Errorf("store: scan agent: %w", err)
		}
		out = append(out, *sn)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("store: list agents: %w", err)
	}
	return out, nil
}

// GetAgent는 에이전트 1건을 조회한다. 없으면 ErrAgentNotFound.
func (s *Store) GetAgent(ctx context.Context, agentID string) (*domain.Agent, error) {
	sn, err := scanAgent(s.db.QueryRowContext(ctx, `
		SELECT `+agentColumns+` FROM agents WHERE id = $1::uuid`, agentID))
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrAgentNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("store: get agent: %w", err)
	}
	return sn, nil
}
