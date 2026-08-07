// Package audit는 감사 이벤트(AuditEvent)의 기록·조회·무결성 검증을 담당한다
// (docs/00-spec.md 7.12장, 이슈 #7).
//
// audit_events는 append-only다 — UPDATE/DELETE/TRUNCATE는 DB 트리거가 차단한다
// (마이그레이션 0007). 무결성 검증값(prev_hash + integrity_hash SHA-256 해시 체인)은
// BEFORE INSERT 트리거가 계산하므로 어떤 삽입 경로든 자동으로 체인에 연결된다.
// 동시 삽입은 트리거의 pg_advisory_xact_lock으로 직렬화된다.
//
// before/after에 인증서 본문·개인키·비밀번호 등 민감값을 넣지 않는다 (보안 하드 규칙 3).
package audit

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"

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

// Execer는 *sql.DB와 *sql.Tx를 모두 받기 위한 최소 인터페이스다.
type Execer interface {
	ExecContext(ctx context.Context, query string, args ...any) (sql.Result, error)
}

// Insert는 이벤트 1건을 저장한다. 호출자의 트랜잭션(tx)을 넘기면 본 변경과
// 원자적으로 커밋된다 — 변경 API는 반드시 같은 트랜잭션으로 기록해 감사 실패 시
// 변경도 롤백되게 한다 (보안 하드 규칙 5).
// prev_hash·integrity_hash는 DB 트리거가 채우므로 여기서 넘기지 않는다.
func Insert(ctx context.Context, ex Execer, e Event) error {
	before, err := marshalValue(e.Before)
	if err != nil {
		return fmt.Errorf("audit: marshal before: %w", err)
	}
	after, err := marshalValue(e.After)
	if err != nil {
		return fmt.Errorf("audit: marshal after: %w", err)
	}
	_, err = ex.ExecContext(ctx,
		`INSERT INTO audit_events
		   (event_type, actor_id, target_type, target_id, request_id,
		    before_value, after_value, result, source_ip)
		 VALUES ($1, $2::uuid, $3, $4, $5, $6::jsonb, $7::jsonb, $8, $9::inet)`,
		e.Type, e.ActorID, e.TargetType, e.TargetID, e.RequestID,
		before, after, string(e.Result), e.SourceIP)
	if err != nil {
		return fmt.Errorf("audit: insert event: %w", err)
	}
	return nil
}

// Recorder는 audit_events 테이블에 대한 기록·조회·검증 진입점이다.
type Recorder struct {
	pool *sql.DB
}

func NewRecorder(pool *sql.DB) *Recorder {
	return &Recorder{pool: pool}
}

// Record는 트랜잭션 없이 이벤트를 저장한다. 상태 변경이 없는 이벤트
// (로그인 실패, authz.denied 등)에만 쓴다. 변경을 동반하면 RecordTx를 쓴다.
func (r *Recorder) Record(ctx context.Context, e Event) error {
	return Insert(ctx, r.pool, e)
}

// RecordTx는 호출자의 트랜잭션 안에서 이벤트를 저장한다 — 변경+감사 원자성 보장.
func (r *Recorder) RecordTx(ctx context.Context, tx *sql.Tx, e Event) error {
	return Insert(ctx, tx, e)
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
