package audit

// 감사 로그 조회 (이슈 #7 완료 조건: 사용자·이벤트 유형·기간으로 조회).
// 읽기 전용이므로 감사 이벤트를 남기지 않는다 (00-spec 7.12 기록 대상에 조회 없음).

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/Falke060918/INHERIT_AutoCert/backend/internal/domain"
)

// ListInput은 감사 로그 조회 조건이다. nil 필드는 필터하지 않는다.
type ListInput struct {
	ActorID   *string // uuid — 검증은 API 계층에서
	EventType *string
	From      *time.Time // created_at >= From
	To        *time.Time // created_at <= To
	Page      int
	PageSize  int
}

// ListResult는 감사 로그 조회 결과다 (최신 이벤트 우선).
type ListResult struct {
	Items    []domain.AuditEvent
	Total    int
	Page     int
	PageSize int
}

// List는 조건에 맞는 감사 이벤트를 id 내림차순(최신 우선)으로 반환한다.
func (r *Recorder) List(ctx context.Context, in ListInput) (*ListResult, error) {
	if in.Page < 1 {
		in.Page = 1
	}
	if in.PageSize < 1 {
		in.PageSize = 50
	}

	var conds []string
	var args []any
	add := func(cond string, v any) {
		args = append(args, v)
		conds = append(conds, fmt.Sprintf(cond, len(args)))
	}
	if in.ActorID != nil {
		add("actor_id = $%d::uuid", *in.ActorID)
	}
	if in.EventType != nil {
		add("event_type = $%d", *in.EventType)
	}
	if in.From != nil {
		add("created_at >= $%d", *in.From)
	}
	if in.To != nil {
		add("created_at <= $%d", *in.To)
	}
	where := ""
	if len(conds) > 0 {
		where = " WHERE " + strings.Join(conds, " AND ")
	}

	res := &ListResult{Page: in.Page, PageSize: in.PageSize, Items: []domain.AuditEvent{}}
	if err := r.pool.QueryRowContext(ctx,
		`SELECT count(*) FROM audit_events`+where, args...).Scan(&res.Total); err != nil {
		return nil, fmt.Errorf("audit: count events: %w", err)
	}

	limitArgs := append(args, in.PageSize, (in.Page-1)*in.PageSize)
	rows, err := r.pool.QueryContext(ctx, fmt.Sprintf(
		`SELECT id, event_type, actor_id::text, target_type, target_id, request_id,
		        before_value::text, after_value::text, result, host(source_ip),
		        created_at, integrity_hash, prev_hash
		   FROM audit_events%s
		  ORDER BY id DESC
		  LIMIT $%d OFFSET $%d`, where, len(args)+1, len(args)+2), limitArgs...)
	if err != nil {
		return nil, fmt.Errorf("audit: list events: %w", err)
	}
	defer rows.Close()

	for rows.Next() {
		var ev domain.AuditEvent
		var before, after *string
		var result string
		if err := rows.Scan(&ev.ID, &ev.EventType, &ev.ActorID, &ev.TargetType,
			&ev.TargetID, &ev.RequestID, &before, &after, &result, &ev.SourceIP,
			&ev.CreatedAt, &ev.IntegrityHash, &ev.PrevHash); err != nil {
			return nil, fmt.Errorf("audit: scan event: %w", err)
		}
		if before != nil {
			ev.BeforeValue = []byte(*before)
		}
		if after != nil {
			ev.AfterValue = []byte(*after)
		}
		ev.Result = domain.AuditResult(result)
		res.Items = append(res.Items, ev)
	}
	return res, rows.Err()
}
