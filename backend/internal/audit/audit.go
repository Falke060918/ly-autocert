// Package audit는 감사 이벤트(AuditEvent) 기록을 담당한다 (docs/00-spec.md 7.12장).
// audit_events는 append-only다 — 이 패키지는 INSERT만 한다.
// before/after에 인증서 본문·개인키·비밀번호 등 민감값을 넣지 않는다 (보안 하드 규칙 3).
package audit

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"time"

	"github.com/Falke060918/INHERIT_AutoCert/backend/internal/domain"
)

// Event는 기록할 감사 이벤트 한 건이다.
type Event struct {
	Type       string // 예: user.login, user.logout, user.created, authz.denied
	ActorID    *string
	TargetType string
	TargetID   *string
	RequestID  *string
	Before     any // nil이면 NULL. 민감값 금지.
	After      any
	Result     domain.AuditResult
	SourceIP   *string
}

// Recorder는 audit_events 테이블에 이벤트를 기록한다.
type Recorder struct {
	pool *sql.DB
}

func NewRecorder(pool *sql.DB) *Recorder {
	return &Recorder{pool: pool}
}

// Record는 이벤트를 저장한다. 변경 API는 이 호출이 실패하면 요청 자체를 실패시킨다
// (보안 하드 규칙 5 — 감사 없는 변경 금지).
func (r *Recorder) Record(ctx context.Context, e Event) error {
	before, err := marshalValue(e.Before)
	if err != nil {
		return fmt.Errorf("audit: marshal before: %w", err)
	}
	after, err := marshalValue(e.After)
	if err != nil {
		return fmt.Errorf("audit: marshal after: %w", err)
	}
	createdAt := time.Now().UTC()
	hash := integrityHash(e, before, after, createdAt)
	_, err = r.pool.ExecContext(ctx,
		`INSERT INTO audit_events
		   (event_type, actor_id, target_type, target_id, request_id,
		    before_value, after_value, result, source_ip, created_at, integrity_hash)
		 VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11)`,
		e.Type, e.ActorID, e.TargetType, e.TargetID, e.RequestID,
		before, after, e.Result, e.SourceIP, createdAt, hash)
	if err != nil {
		return fmt.Errorf("audit: insert event: %w", err)
	}
	return nil
}

func marshalValue(v any) (*string, error) {
	if v == nil {
		return nil, nil
	}
	b, err := json.Marshal(v)
	if err != nil {
		return nil, err
	}
	s := string(b)
	return &s, nil
}

// integrityHash는 이벤트 필드의 SHA-256 무결성 검증값이다.
// 이슈 #7에서 이전 이벤트 해시와 연결(체인)해 변조 탐지를 완성한다.
func integrityHash(e Event, before, after *string, createdAt time.Time) string {
	h := sha256.New()
	write := func(s *string) {
		if s != nil {
			h.Write([]byte(*s))
		}
		h.Write([]byte{0}) // 필드 경계
	}
	write(&e.Type)
	write(e.ActorID)
	write(&e.TargetType)
	write(e.TargetID)
	write(e.RequestID)
	write(before)
	write(after)
	result := string(e.Result)
	write(&result)
	write(e.SourceIP)
	ts := createdAt.Format(time.RFC3339Nano)
	write(&ts)
	return hex.EncodeToString(h.Sum(nil))
}
