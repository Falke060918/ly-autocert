// notifications.go — 만료 알림 파이프라인의 영속화 (docs/design/05-notifications.md, 마이그레이션 0012).
// 정책(policy) → 이벤트 탐지(dedup_key 멱등) → 딜리버리 팬아웃(채널×수신자)
// → 리스 발송(FOR UPDATE SKIP LOCKED) → 재시도·고아 회수 → 알림함·보존.
//
// 보안:
//   - payload에는 공개 필드 스냅샷만 담는다. 인증서 원문(PEM/DER)·개인키는 넣지 않는다 (하드 규칙 3).
//   - 수신자 주소는 저장·조회 경로에만 존재한다. 로그·에러 메시지에 넣지 않는다 (설계 11.4).
//   - 정책 변경은 감사 이벤트를 같은 트랜잭션으로 남긴다 (하드 규칙 5).
package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"sort"
	"strings"
	"time"

	"github.com/Falke060918/INHERIT_AutoCert/backend/internal/audit"
	"github.com/Falke060918/INHERIT_AutoCert/backend/internal/domain"
)

// 알림 정책 변경 감사 이벤트 타입 (설계 9.2).
const (
	EventNotificationPolicyCreate = "notification.policy.create"
	EventNotificationPolicyUpdate = "notification.policy.update"
	EventNotificationPolicyDelete = "notification.policy.delete"
)

// 채널 식별자. notify 패키지와 같은 문자열이지만 그 패키지를 import하지 않는다 —
// inbox 어댑터(notify)가 store를 쓰게 되면 순환 import가 되기 때문이다.
const (
	NotifyChannelEmail   = "email"
	NotifyChannelWebhook = "webhook"
	NotifyChannelInbox   = "inbox"
	NotifyChannelSyslog  = "syslog"
)

// 이벤트 종류 (설계 1.2 — 나머지 종류는 확장점으로만 남긴다).
const (
	NotifyEventCertExpiring = "certificate.expiring"
	NotifyEventCertExpired  = "certificate.expired"
)

// 이벤트 상태. 발송 진행 상태는 여기가 아니라 딜리버리가 갖는다.
const (
	NotifyEventPending    = "pending"
	NotifyEventSuppressed = "suppressed"
)

// 딜리버리 상태(명시적 상태 머신).
// pending → sending → sent | failed, 수신자 해석 실패는 skipped로 종결한다.
const (
	DeliveryStatusPending = "pending"
	DeliveryStatusSending = "sending"
	DeliveryStatusSent    = "sent"
	DeliveryStatusFailed  = "failed"
	DeliveryStatusSkipped = "skipped"
)

// 수신자 종류 (설계 14.2).
const (
	RecipientOwner  = "owner"
	RecipientRole   = "role"
	RecipientUser   = "user"
	RecipientStatic = "static"
)

// 딜리버리 리스·재시도 파라미터 (설계 6.1).
// 백오프는 1m·2m·4m·8m이며 maxDeliveryAttempts회 실패하면 failed로 종결한다.
const (
	deliveryLeaseTTL      = 2 * time.Minute
	deliveryRetryBase     = time.Minute
	deliveryRetryMax      = time.Hour
	maxDeliveryAttempts   = 5
	maxDeliveryErrorRunes = 500
	maxLeaseBatch         = 200
)

// 수신자를 해석하지 못했을 때 딜리버리에 남기는 사유다.
// 조용히 사라지면 "왜 안 왔는지"를 답할 수 없다 (설계 14.3).
// 주소·사용자 식별자를 담지 않는다.
const (
	skipReasonNoRecipient    = "no recipient resolved for this channel"
	skipReasonOwnerNoFallack = "owner unresolved and policy has no fallback recipient"
)

// ErrNotificationPolicyNameTaken은 정책 이름 UNIQUE 충돌이다(핸들러가 409 매핑).
var ErrNotificationPolicyNameTaken = errors.New("store: notification policy name already exists")

// ErrDeliveryState는 상태 머신이 허용하지 않는 딜리버리 전이 요청에 반환한다.
var ErrDeliveryState = errors.New("store: notification delivery state conflict")

// querier는 *sql.DB와 *sql.Tx를 함께 받기 위한 최소 인터페이스다 —
// 스케줄러 tick은 탐지와 팬아웃을 단일 트랜잭션 안에서 연달아 실행한다 (설계 7.2).
type querier interface {
	ExecContext(ctx context.Context, query string, args ...any) (sql.Result, error)
	QueryContext(ctx context.Context, query string, args ...any) (*sql.Rows, error)
	QueryRowContext(ctx context.Context, query string, args ...any) *sql.Row
}

// ===== 정책 =====

// NotificationRecipient는 정책의 수신자 1건이다 (recipients jsonb 원소).
type NotificationRecipient struct {
	Kind   string `json:"kind"`              // owner | role | user | static
	Role   string `json:"role,omitempty"`    // kind=role
	UserID string `json:"user_id,omitempty"` // kind=user
	Email  string `json:"email,omitempty"`   // kind=static
}

// NotificationPolicy는 정책 1건이다.
// 필터가 비어 있으면(nil) 전체 인증서에 매칭된다.
type NotificationPolicy struct {
	ID         string   `json:"id"`
	Name       string   `json:"name"`
	Enabled    bool     `json:"enabled"`
	EventTypes []string `json:"event_types"`
	Thresholds []int    `json:"thresholds"`
	// CAThresholds는 발급 기관(CA) 인증서에만 쓰는 임계값이다 (마이그레이션 0020).
	// nil = 미설정(CA도 Thresholds를 쓴다) / 빈 배열 = CA는 알리지 않는다.
	// 두 상태가 구분되어야 하므로 nil을 빈 배열로 정규화하지 않는다.
	CAThresholds        []int                   `json:"ca_thresholds"`
	Channels            []string                `json:"channels"`
	Recipients          []NotificationRecipient `json:"recipients"`
	FilterEnvironments  []string                `json:"filter_environments"`
	FilterDepartmentIDs []string                `json:"filter_department_ids"`
	CreatedBy           *string                 `json:"created_by"`
	CreatedAt           time.Time               `json:"created_at"`
	UpdatedAt           time.Time               `json:"updated_at"`
}

// notificationPolicyColumns는 scanNotificationPolicy와 순서가 일치해야 한다.
// 배열 컬럼은 패키지 규약대로 구분자 연결 문자열로 내린다 (text[]=0x1F, 숫자·uuid[]=쉼표).
const notificationPolicyColumns = `
	id, name, enabled,
	array_to_string(event_types, chr(31)),
	array_to_string(thresholds, ','),
	array_to_string(ca_thresholds, ','),
	array_to_string(channels, chr(31)),
	recipients::text,
	array_to_string(filter_environments, chr(31)),
	array_to_string(filter_department_ids, ','),
	created_by, created_at, updated_at`

func scanNotificationPolicy(row rowScanner) (*NotificationPolicy, error) {
	var (
		p            NotificationPolicy
		eventTypes   string
		thresholds   string
		caThresholds sql.NullString
		channels     string
		recipients   string
		envs         sql.NullString
		depts        sql.NullString
	)
	if err := row.Scan(&p.ID, &p.Name, &p.Enabled, &eventTypes, &thresholds, &caThresholds,
		&channels, &recipients, &envs, &depts, &p.CreatedBy, &p.CreatedAt, &p.UpdatedAt); err != nil {
		return nil, err
	}
	p.EventTypes = splitNonEmpty(eventTypes, sansSep)
	p.Thresholds = parseInts(splitNonEmpty(thresholds, portSep))
	// NULL(미설정)과 '{}'(CA 알림 끔)은 다른 뜻이다 — array_to_string은 빈 배열에도
	// 빈 문자열을 주므로 NULL 여부(Valid)로만 갈라야 한다.
	// parseInts는 빈 입력에도 non-nil 빈 슬라이스를 준다 — '{}'가 nil로 접히지 않는다.
	if caThresholds.Valid {
		p.CAThresholds = parseInts(splitNonEmpty(caThresholds.String, portSep))
	}
	p.Channels = splitNonEmpty(channels, sansSep)
	p.Recipients = []NotificationRecipient{}
	if recipients != "" {
		if err := json.Unmarshal([]byte(recipients), &p.Recipients); err != nil {
			return nil, fmt.Errorf("store: decode policy recipients: %w", err)
		}
	}
	if envs.Valid {
		p.FilterEnvironments = splitNonEmpty(envs.String, sansSep)
	}
	if depts.Valid {
		p.FilterDepartmentIDs = splitNonEmpty(depts.String, portSep)
	}
	return &p, nil
}

// NotificationPolicyInput은 정책 생성 요청이다.
// 빈 필터는 NULL로 정규화한다 — NULL과 빈 배열 모두 "전체 매칭"이라 구분할 의미가 없다.
type NotificationPolicyInput struct {
	Name       string
	Enabled    bool
	EventTypes []string
	Thresholds []int
	// CAThresholds는 nil이면 NULL(미설정)로, 빈 배열이면 '{}'(CA 알림 끔)로 저장된다.
	CAThresholds        []int
	Channels            []string
	Recipients          []NotificationRecipient
	FilterEnvironments  []string
	FilterDepartmentIDs []string
	ActorID             *string
	RequestID           *string
	SourceIP            *string
}

// NotificationPolicyPatch는 부분 수정 요청이다. nil 필드는 건드리지 않는다.
type NotificationPolicyPatch struct {
	Name       *string
	Enabled    *bool
	EventTypes *[]string
	Thresholds *[]int
	// CAThresholds는 3상태다: nil = 안 건드림 / 가리키는 값이 nil = NULL로 되돌림
	// (CA도 Thresholds를 쓴다) / 가리키는 값이 슬라이스 = 그 값으로 설정.
	CAThresholds        *[]int
	Channels            *[]string
	Recipients          *[]NotificationRecipient
	FilterEnvironments  *[]string
	FilterDepartmentIDs *[]string
	ActorID             *string
	RequestID           *string
	SourceIP            *string
}

// nullableJoin은 빈 슬라이스를 NULL(=필터 없음)로 바꾼다.
func nullableJoin(vs []string, sep string) *string {
	if len(vs) == 0 {
		return nil
	}
	s := strings.Join(vs, sep)
	return &s
}

// nullableInts는 nil 슬라이스만 NULL로 바꾼다 — 빈 배열은 빈 문자열로 남겨
// SQL 쪽 CASE가 '{}'로 저장하게 한다. ca_thresholds의 NULL('미설정')과
// '{}'('CA는 알리지 않음')는 뜻이 다르므로 여기서 접으면 안 된다.
func nullableInts(vs []int) *string {
	if vs == nil {
		return nil
	}
	s := joinInts(vs)
	return &s
}

func validateChannels(channels []string) error {
	if len(channels) == 0 {
		return errors.New("store: notification policy needs at least one channel")
	}
	for _, ch := range channels {
		switch ch {
		case NotifyChannelEmail, NotifyChannelWebhook, NotifyChannelInbox, NotifyChannelSyslog:
		default:
			return fmt.Errorf("store: unknown notification channel %q", ch)
		}
	}
	return nil
}

// validateRecipients는 수신자 종류·참조 형식을 검사한다.
// 오류 메시지에 메일 주소를 넣지 않는다 (설계 11.4).
func validateRecipients(rs []NotificationRecipient) error {
	for _, r := range rs {
		switch r.Kind {
		case RecipientOwner:
		case RecipientRole:
			if !domain.Role(r.Role).Valid() {
				return fmt.Errorf("store: unknown recipient role %q", r.Role)
			}
		case RecipientUser:
			if !uuidRe.MatchString(strings.ToLower(r.UserID)) {
				return errors.New("store: recipient user_id must be a uuid")
			}
		case RecipientStatic:
			if i := strings.Index(r.Email, "@"); i < 1 {
				return errors.New("store: static recipient needs an email address")
			}
		default:
			return fmt.Errorf("store: unknown recipient kind %q", r.Kind)
		}
	}
	return nil
}

func validateDepartmentIDs(ids []string) error {
	for _, id := range ids {
		if !uuidRe.MatchString(strings.ToLower(id)) {
			return errors.New("store: filter_department_ids must be uuids")
		}
	}
	return nil
}

func validatePolicyCore(name string, eventTypes, channels []string, thresholds, caThresholds []int,
	recipients []NotificationRecipient, deptIDs []string) error {
	if strings.TrimSpace(name) == "" {
		return errors.New("store: notification policy name required")
	}
	if len(eventTypes) == 0 {
		return errors.New("store: notification policy needs at least one event type")
	}
	for _, d := range thresholds {
		if d <= 0 {
			return fmt.Errorf("store: threshold days must be positive, got %d", d)
		}
	}
	for _, d := range caThresholds {
		if d <= 0 {
			return fmt.Errorf("store: ca threshold days must be positive, got %d", d)
		}
	}
	if err := validateChannels(channels); err != nil {
		return err
	}
	if err := validateRecipients(recipients); err != nil {
		return err
	}
	return validateDepartmentIDs(deptIDs)
}

// auditRecipients는 감사 값에 넣을 수신자 요약이다.
// static 수신자의 메일 주소는 복제하지 않는다 — 주소 원본은 정책 행에만 두고
// 감사에는 "정적 수신자가 몇 명인지"만 남긴다 (PII 확산 억제).
func auditRecipients(rs []NotificationRecipient) []map[string]string {
	out := make([]map[string]string, 0, len(rs))
	for _, r := range rs {
		m := map[string]string{"kind": r.Kind}
		switch r.Kind {
		case RecipientRole:
			m["role"] = r.Role
		case RecipientUser:
			m["user_id"] = r.UserID
		}
		out = append(out, m)
	}
	return out
}

func policyAuditValue(p *NotificationPolicy) map[string]any {
	return map[string]any{
		"name":                  p.Name,
		"enabled":               p.Enabled,
		"event_types":           p.EventTypes,
		"thresholds":            p.Thresholds,
		"ca_thresholds":         p.CAThresholds,
		"channels":              p.Channels,
		"recipients":            auditRecipients(p.Recipients),
		"filter_environments":   p.FilterEnvironments,
		"filter_department_ids": p.FilterDepartmentIDs,
	}
}

// ListNotificationPolicies는 정책 전체를 이름 순으로 반환한다.
func (s *Store) ListNotificationPolicies(ctx context.Context) ([]NotificationPolicy, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT `+notificationPolicyColumns+` FROM notification_policies ORDER BY name, id`)
	if err != nil {
		return nil, fmt.Errorf("store: list notification policies: %w", err)
	}
	defer rows.Close() //nolint:errcheck

	out := []NotificationPolicy{}
	for rows.Next() {
		p, err := scanNotificationPolicy(rows)
		if err != nil {
			return nil, fmt.Errorf("store: scan notification policy: %w", err)
		}
		out = append(out, *p)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("store: list notification policies: %w", err)
	}
	return out, nil
}

// GetNotificationPolicy는 정책 1건을 조회한다. 없으면 ErrNotFound.
func (s *Store) GetNotificationPolicy(ctx context.Context, id string) (*NotificationPolicy, error) {
	if !uuidRe.MatchString(strings.ToLower(strings.TrimSpace(id))) {
		return nil, ErrNotFound
	}
	p, err := scanNotificationPolicy(s.db.QueryRowContext(ctx,
		`SELECT `+notificationPolicyColumns+` FROM notification_policies WHERE id = $1::uuid`, id))
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("store: get notification policy: %w", err)
	}
	return p, nil
}

// CreateNotificationPolicy는 정책을 만들고 감사(notification.policy.create)를 같은
// 트랜잭션으로 남긴다. 이름이 겹치면 ErrNotificationPolicyNameTaken.
func (s *Store) CreateNotificationPolicy(ctx context.Context, in NotificationPolicyInput) (*NotificationPolicy, error) {
	if err := validatePolicyCore(in.Name, in.EventTypes, in.Channels, in.Thresholds,
		in.CAThresholds, in.Recipients, in.FilterDepartmentIDs); err != nil {
		return nil, err
	}
	recipients, err := json.Marshal(recipientsOrEmpty(in.Recipients))
	if err != nil {
		return nil, fmt.Errorf("store: encode recipients: %w", err)
	}

	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, fmt.Errorf("store: begin: %w", err)
	}
	defer tx.Rollback() //nolint:errcheck

	p, err := scanNotificationPolicy(tx.QueryRowContext(ctx, `
		INSERT INTO notification_policies (
			name, enabled, event_types, thresholds, ca_thresholds, channels, recipients,
			filter_environments, filter_department_ids, created_by)
		VALUES (
			$1, $2,
			coalesce(string_to_array(nullif($3, ''), chr(31)), '{}'),
			coalesce(string_to_array(nullif($4, ''), ','), '{}')::int[],
			-- NULL(미설정) / '{}'(CA 알림 끔) / 값 3상태를 그대로 보존한다.
			CASE WHEN $5::text IS NULL THEN NULL
			     ELSE coalesce(string_to_array(nullif($5::text, ''), ','), '{}')::int[] END,
			coalesce(string_to_array(nullif($6, ''), chr(31)), '{}'),
			$7::jsonb,
			string_to_array($8::text, chr(31)),
			string_to_array($9::text, ',')::uuid[],
			$10::uuid)
		RETURNING `+notificationPolicyColumns,
		strings.TrimSpace(in.Name), in.Enabled,
		strings.Join(in.EventTypes, sansSep), joinInts(in.Thresholds), nullableInts(in.CAThresholds),
		strings.Join(in.Channels, sansSep), string(recipients),
		nullableJoin(in.FilterEnvironments, sansSep), nullableJoin(in.FilterDepartmentIDs, portSep),
		in.ActorID))
	if err != nil {
		if isUniqueViolation(err) {
			return nil, ErrNotificationPolicyNameTaken
		}
		return nil, fmt.Errorf("store: insert notification policy: %w", err)
	}

	// 감사 삽입은 트랜잭션의 마지막 문장이어야 한다 (docs/GOTCHAS.md).
	if err := audit.Insert(ctx, tx, audit.Event{
		Type:       EventNotificationPolicyCreate,
		ActorID:    in.ActorID,
		TargetType: "notification_policy",
		TargetID:   &p.ID,
		RequestID:  in.RequestID,
		After:      policyAuditValue(p),
		Result:     domain.AuditSuccess,
		SourceIP:   in.SourceIP,
	}); err != nil {
		return nil, err
	}
	if err := tx.Commit(); err != nil {
		return nil, fmt.Errorf("store: commit: %w", err)
	}
	return p, nil
}

func recipientsOrEmpty(rs []NotificationRecipient) []NotificationRecipient {
	if rs == nil {
		return []NotificationRecipient{}
	}
	return rs
}

// UpdateNotificationPolicy는 지정된 필드만 갱신하고 before/after 감사를 남긴다.
func (s *Store) UpdateNotificationPolicy(ctx context.Context, id string, in NotificationPolicyPatch) (*NotificationPolicy, error) {
	cur, err := s.GetNotificationPolicy(ctx, id)
	if err != nil {
		return nil, err
	}
	next := *cur
	if in.Name != nil {
		next.Name = strings.TrimSpace(*in.Name)
	}
	if in.Enabled != nil {
		next.Enabled = *in.Enabled
	}
	if in.EventTypes != nil {
		next.EventTypes = *in.EventTypes
	}
	if in.Thresholds != nil {
		next.Thresholds = *in.Thresholds
	}
	// 가리키는 값이 nil이면 NULL(미설정)로 되돌린다 — 빈 배열('{}')과 구분된다.
	if in.CAThresholds != nil {
		next.CAThresholds = *in.CAThresholds
	}
	if in.Channels != nil {
		next.Channels = *in.Channels
	}
	if in.Recipients != nil {
		next.Recipients = *in.Recipients
	}
	if in.FilterEnvironments != nil {
		next.FilterEnvironments = *in.FilterEnvironments
	}
	if in.FilterDepartmentIDs != nil {
		next.FilterDepartmentIDs = *in.FilterDepartmentIDs
	}
	if err := validatePolicyCore(next.Name, next.EventTypes, next.Channels, next.Thresholds,
		next.CAThresholds, next.Recipients, next.FilterDepartmentIDs); err != nil {
		return nil, err
	}
	recipients, err := json.Marshal(recipientsOrEmpty(next.Recipients))
	if err != nil {
		return nil, fmt.Errorf("store: encode recipients: %w", err)
	}

	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, fmt.Errorf("store: begin: %w", err)
	}
	defer tx.Rollback() //nolint:errcheck

	// updated_at은 애플리케이션이 갱신한다 (0002 규약 — 트리거 없음).
	p, err := scanNotificationPolicy(tx.QueryRowContext(ctx, `
		UPDATE notification_policies SET
			name                  = $2,
			enabled               = $3,
			event_types           = coalesce(string_to_array(nullif($4, ''), chr(31)), '{}'),
			thresholds            = coalesce(string_to_array(nullif($5, ''), ','), '{}')::int[],
			ca_thresholds         = CASE WHEN $6::text IS NULL THEN NULL
			                             ELSE coalesce(string_to_array(nullif($6::text, ''), ','), '{}')::int[] END,
			channels              = coalesce(string_to_array(nullif($7, ''), chr(31)), '{}'),
			recipients            = $8::jsonb,
			filter_environments   = string_to_array($9::text, chr(31)),
			filter_department_ids = string_to_array($10::text, ',')::uuid[],
			updated_at            = now()
		 WHERE id = $1::uuid
		RETURNING `+notificationPolicyColumns,
		cur.ID, next.Name, next.Enabled,
		strings.Join(next.EventTypes, sansSep), joinInts(next.Thresholds),
		nullableInts(next.CAThresholds),
		strings.Join(next.Channels, sansSep), string(recipients),
		nullableJoin(next.FilterEnvironments, sansSep), nullableJoin(next.FilterDepartmentIDs, portSep)))
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		if isUniqueViolation(err) {
			return nil, ErrNotificationPolicyNameTaken
		}
		return nil, fmt.Errorf("store: update notification policy: %w", err)
	}

	if err := audit.Insert(ctx, tx, audit.Event{
		Type:       EventNotificationPolicyUpdate,
		ActorID:    in.ActorID,
		TargetType: "notification_policy",
		TargetID:   &p.ID,
		RequestID:  in.RequestID,
		Before:     policyAuditValue(cur),
		After:      policyAuditValue(p),
		Result:     domain.AuditSuccess,
		SourceIP:   in.SourceIP,
	}); err != nil {
		return nil, err
	}
	if err := tx.Commit(); err != nil {
		return nil, fmt.Errorf("store: commit: %w", err)
	}
	return p, nil
}

// DeleteNotificationPolicy는 정책을 지우고 감사를 남긴다.
// 이미 만들어진 이벤트는 policy_id가 NULL로 남아 이력이 보존된다 (0012 ON DELETE SET NULL).
func (s *Store) DeleteNotificationPolicy(ctx context.Context, id string, actorID, requestID, sourceIP *string) error {
	cur, err := s.GetNotificationPolicy(ctx, id)
	if err != nil {
		return err
	}

	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("store: begin: %w", err)
	}
	defer tx.Rollback() //nolint:errcheck

	res, err := tx.ExecContext(ctx, `DELETE FROM notification_policies WHERE id = $1::uuid`, cur.ID)
	if err != nil {
		return fmt.Errorf("store: delete notification policy: %w", err)
	}
	n, err := res.RowsAffected()
	if err != nil {
		return fmt.Errorf("store: delete notification policy: %w", err)
	}
	if n == 0 {
		return ErrNotFound
	}

	if err := audit.Insert(ctx, tx, audit.Event{
		Type:       EventNotificationPolicyDelete,
		ActorID:    actorID,
		TargetType: "notification_policy",
		TargetID:   &cur.ID,
		RequestID:  requestID,
		Before:     policyAuditValue(cur),
		Result:     domain.AuditSuccess,
		SourceIP:   sourceIP,
	}); err != nil {
		return err
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("store: commit: %w", err)
	}
	return nil
}

// isUniqueViolation은 PostgreSQL unique_violation(23505)인지 확인한다.
func isUniqueViolation(err error) bool {
	type sqlState interface{ SQLState() string }
	var st sqlState
	return errors.As(err, &st) && st.SQLState() == "23505"
}

// ===== 이벤트 탐지 =====

// DetectMode는 탐지 결과를 발송 대상으로 볼지(send) 기준선으로만 적재할지(seed) 정한다.
// seed는 설치 첫 tick 전용이다 — 이미 지난 임계값을 suppressed로 적재해 dedup_key를
// 선점하므로, 설치 당일 인벤토리 전체가 한꺼번에 발송되지 않는다 (설계 7.7).
type DetectMode string

const (
	DetectModeSend DetectMode = "send"
	DetectModeSeed DetectMode = "seed"
)

func (m DetectMode) eventStatus() string {
	if m == DetectModeSeed {
		return NotifyEventSuppressed
	}
	return NotifyEventPending
}

// NotificationPayload는 이벤트 payload jsonb의 공개 필드 스냅샷이다.
// 인증서 원문·개인키·소유자 메일 주소는 담지 않는다 (하드 규칙 3, 설계 11.2).
type NotificationPayload struct {
	CertificateID string    `json:"certificate_id"`
	CommonName    string    `json:"common_name"`
	NotAfter      time.Time `json:"not_after"`
	DaysRemaining int       `json:"days_remaining"`
	Environment   string    `json:"environment"`
	OwnerDisplay  string    `json:"owner"`
	LocationCount int       `json:"location_count"`
	// IsCA는 발급 기관 인증서 여부다. 만료의 파급 범위가 다르므로(하위 인증서
	// 전체가 함께 신뢰를 잃는다) 알림 제목·본문이 이 사실을 밝힌다.
	// 0018 이전 행은 is_ca가 NULL이라 판정 기준은 certificate_type='ca'다.
	IsCA bool `json:"is_ca"`
}

// DetectedEvent는 이번 탐지로 새로 생긴 이벤트 1건이다.
// 이미 있던 이벤트는 ON CONFLICT DO NOTHING으로 반환되지 않으므로,
// 여기 담긴 것만 팬아웃하면 중복 발송이 구조적으로 불가능하다 (설계 7.3).
type DetectedEvent struct {
	ID            string              `json:"id"`
	DedupKey      string              `json:"dedup_key"`
	EventType     string              `json:"event_type"`
	CertificateID *string             `json:"certificate_id"`
	ThresholdDays *int                `json:"threshold_days"`
	PolicyID      *string             `json:"policy_id"`
	Severity      string              `json:"severity"`
	Status        string              `json:"status"`
	Payload       NotificationPayload `json:"payload"`
}

// detectExpiryEventsSQL은 만료 예정·만료 이벤트를 세트 기반 단일 문장으로 적재한다.
//
// 판정 기준은 저장된 status가 아니라 not_after다 — status를 만료로 갱신하는 주체가
// 없어 stale이기 때문이다. status는 revoked/replaced 제외 필터로만 쓴다 (설계 7.4).
// CA 인증서도 포함한다(CA 만료는 리프 전체를 무너뜨린다).
//
// CA 임계값(0020): 정책에 ca_thresholds가 있으면 certificate_type='ca'인 인증서만
// 그 목록으로 판정한다. 사건 종류·채널·수신자는 리프와 그대로 공유하므로 기존
// 정책·알림함·웹훅 수신 측이 CA를 놓치지 않는다. ca_thresholds가 NULL이면
// 0020 이전과 같이 thresholds를 쓰고, 빈 배열이면 unnest가 0행이라 CA는 발화하지 않는다.
//
// dedup_key에 정책 id를 넣지 않는다 — 두 정책이 겹쳐도 이벤트는 1건이고 수신자만
// 합집합이 된다 (설계 4.1). policy_id는 "최초로 이 이벤트를 만든 정책" 참고값이라
// DISTINCT ON으로 가장 오래된 정책을 고정 선택한다.
//
// 임계값 선택 규칙(설계 7.3.1) — **인증서·정책당 임계값 1개만 발화한다.**
// 정책 thresholds 중 이미 도달한 것(`th.days >= days_remaining`)들의 **최솟값** 하나만
// 이벤트가 된다. `unnest`를 그대로 조인해 "도달한 임계값 전부"를 매칭하면 다음 두 경로로
// 소급 폭주가 난다(둘 다 실측된 결함이다):
//   - 정책에 임계값을 나중에 추가하면, 이미 지나간 임계값이 그 자리에서 새로 발화한다.
//   - 스캔이 D-3짜리 인증서를 새로 발견하면 90/60/30/7이 동시에 터져 제목이 전부
//     "D-3"인 알림이 채널×수신자만큼 나간다(인증서 200건이면 메일 수백 통).
//
// 인증서 단위 기준선은 dedup_key가 대신한다 — 임계값이 작아질 때마다 키가 바뀌므로
// D-90 → D-60 → D-30 → D-7의 정상 케이던스는 그대로 유지되고, 한 번 지나간(=더 큰)
// 임계값은 애초에 생성되지 않는다.
//
// 비교 기준은 `not_after <= now() + t.days`가 아니라 `days_remaining`이다 —
// 알림 제목·payload가 쓰는 D-day와 같은 값이라야 "D-7 알림인데 제목은 D-6"이 안 난다.
const detectExpiryEventsSQL = `
INSERT INTO notification_events (
	dedup_key, event_type, certificate_id, threshold_days, policy_id, severity, payload, status)
SELECT DISTINCT ON (m.dedup_key)
	m.dedup_key, m.event_type, m.certificate_id, m.threshold_days, m.policy_id,
	m.severity, m.payload, $1
FROM (
	SELECT 'certificate.expiring:' || c.id::text || ':' || t.days::text AS dedup_key,
	       'certificate.expiring'::text                                AS event_type,
	       c.id                                                        AS certificate_id,
	       t.days                                                      AS threshold_days,
	       p.id                                                        AS policy_id,
	       p.created_at                                                AS policy_created_at,
	       CASE WHEN dr.days_remaining <= 0  THEN 'critical'
	            WHEN dr.days_remaining <= 7  THEN 'error'
	            WHEN dr.days_remaining <= 30 THEN 'warning'
	            ELSE 'info' END                                        AS severity,
	       jsonb_build_object(
	           'certificate_id', c.id,
	           'common_name',    c.common_name,
	           'not_after',      to_char(c.not_after AT TIME ZONE 'UTC', 'YYYY-MM-DD"T"HH24:MI:SS"Z"'),
	           'days_remaining', dr.days_remaining,
	           'environment',    coalesce(c.environment, ''),
	           'owner',          coalesce(u.display_name, u.username, ''),
	           'location_count', (SELECT count(*) FROM certificate_locations cl
	                               WHERE cl.certificate_id = c.id),
	           'is_ca',          c.certificate_type = 'ca')             AS payload
	  FROM notification_policies p
	  CROSS JOIN certificates c
	  LEFT JOIN users u ON u.id = c.owner_id
	  CROSS JOIN LATERAL (
	      SELECT floor(extract(epoch FROM (c.not_after - now())) / 86400)::int AS days_remaining) dr
	  CROSS JOIN LATERAL (
	      SELECT min(th.days) AS days
	        FROM unnest(CASE WHEN c.certificate_type = 'ca' AND p.ca_thresholds IS NOT NULL
	                         THEN p.ca_thresholds ELSE p.thresholds END) AS th(days)
	       WHERE th.days > 0 AND th.days >= dr.days_remaining) t
	 WHERE p.enabled
	   AND 'certificate.expiring' = ANY(p.event_types)
	   AND t.days IS NOT NULL
	   AND c.status NOT IN ('revoked', 'replaced')
	   AND c.not_after >= now()
	   AND (p.filter_environments IS NULL OR cardinality(p.filter_environments) = 0
	        OR c.environment = ANY(p.filter_environments))
	   AND (p.filter_department_ids IS NULL OR cardinality(p.filter_department_ids) = 0
	        OR c.department_id = ANY(p.filter_department_ids))
	UNION ALL
	SELECT 'certificate.expired:' || c.id::text,
	       'certificate.expired'::text,
	       c.id,
	       NULL::int,
	       p.id,
	       p.created_at,
	       'critical',
	       jsonb_build_object(
	           'certificate_id', c.id,
	           'common_name',    c.common_name,
	           'not_after',      to_char(c.not_after AT TIME ZONE 'UTC', 'YYYY-MM-DD"T"HH24:MI:SS"Z"'),
	           'days_remaining', dr.days_remaining,
	           'environment',    coalesce(c.environment, ''),
	           'owner',          coalesce(u.display_name, u.username, ''),
	           'location_count', (SELECT count(*) FROM certificate_locations cl
	                               WHERE cl.certificate_id = c.id),
	           'is_ca',          c.certificate_type = 'ca')
	  FROM notification_policies p
	  CROSS JOIN certificates c
	  LEFT JOIN users u ON u.id = c.owner_id
	  CROSS JOIN LATERAL (
	      SELECT floor(extract(epoch FROM (c.not_after - now())) / 86400)::int AS days_remaining) dr
	 WHERE p.enabled
	   AND 'certificate.expired' = ANY(p.event_types)
	   AND c.status NOT IN ('revoked', 'replaced')
	   AND c.not_after < now()
	   AND (p.filter_environments IS NULL OR cardinality(p.filter_environments) = 0
	        OR c.environment = ANY(p.filter_environments))
	   AND (p.filter_department_ids IS NULL OR cardinality(p.filter_department_ids) = 0
	        OR c.department_id = ANY(p.filter_department_ids))
) m
ORDER BY m.dedup_key, m.policy_created_at, m.policy_id
ON CONFLICT (dedup_key) DO NOTHING
RETURNING id, dedup_key, event_type, certificate_id, threshold_days, policy_id,
          severity, status, payload::text`

// DetectExpiryEvents는 자체 연결로 탐지만 수행한다(테스트·수동 실행용).
// 스케줄러는 RunNotificationTick을 쓴다 — 그쪽이 advisory lock과 팬아웃까지 묶는다.
func (s *Store) DetectExpiryEvents(ctx context.Context, mode DetectMode) ([]DetectedEvent, error) {
	return detectExpiryEvents(ctx, s.db, mode)
}

func detectExpiryEvents(ctx context.Context, q querier, mode DetectMode) ([]DetectedEvent, error) {
	rows, err := q.QueryContext(ctx, detectExpiryEventsSQL, mode.eventStatus())
	if err != nil {
		return nil, fmt.Errorf("store: detect expiry events: %w", err)
	}
	defer rows.Close() //nolint:errcheck

	out := []DetectedEvent{}
	for rows.Next() {
		var (
			e       DetectedEvent
			payload string
		)
		if err := rows.Scan(&e.ID, &e.DedupKey, &e.EventType, &e.CertificateID, &e.ThresholdDays,
			&e.PolicyID, &e.Severity, &e.Status, &payload); err != nil {
			return nil, fmt.Errorf("store: scan detected event: %w", err)
		}
		if err := json.Unmarshal([]byte(payload), &e.Payload); err != nil {
			return nil, fmt.Errorf("store: decode event payload: %w", err)
		}
		out = append(out, e)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("store: detect expiry events: %w", err)
	}
	return out, nil
}

// HasNotificationEvents는 이벤트가 하나라도 적재된 적 있는지 반환한다.
// 스케줄러가 NOTIFY_FIRST_RUN=seed 적용 여부(첫 실행인지)를 판단하는 근거다.
func (s *Store) HasNotificationEvents(ctx context.Context) (bool, error) {
	var exists bool
	if err := s.db.QueryRowContext(ctx,
		`SELECT EXISTS (SELECT 1 FROM notification_events)`).Scan(&exists); err != nil {
		return false, fmt.Errorf("store: check notification events: %w", err)
	}
	return exists, nil
}

// ===== 수신자 해석 · 딜리버리 팬아웃 =====

// notifyUser는 수신자 해석에 필요한 사용자 최소 정보다.
type notifyUser struct {
	ID      string
	Role    string
	Email   string
	Display string
}

// loadNotifyUsers는 활성 사용자를 id·역할별 색인으로 읽는다.
// 팬아웃 1회당 한 번만 조회한다(정책·이벤트 수만큼 반복 조회하지 않는다).
func loadNotifyUsers(ctx context.Context, q querier) (map[string]notifyUser, map[string][]notifyUser, error) {
	rows, err := q.QueryContext(ctx, `
		SELECT id, role, coalesce(email, ''), coalesce(display_name, username)
		  FROM users WHERE is_active ORDER BY username`)
	if err != nil {
		return nil, nil, fmt.Errorf("store: load notification users: %w", err)
	}
	defer rows.Close() //nolint:errcheck

	byID := map[string]notifyUser{}
	byRole := map[string][]notifyUser{}
	for rows.Next() {
		var u notifyUser
		if err := rows.Scan(&u.ID, &u.Role, &u.Email, &u.Display); err != nil {
			return nil, nil, fmt.Errorf("store: scan notification user: %w", err)
		}
		byID[u.ID] = u
		byRole[u.Role] = append(byRole[u.Role], u)
	}
	if err := rows.Err(); err != nil {
		return nil, nil, fmt.Errorf("store: load notification users: %w", err)
	}
	return byID, byRole, nil
}

// eventPolicyMatch는 "이 이벤트를 커버하는 정책" 1쌍이다.
type eventPolicyMatch struct {
	EventID    string
	OwnerID    sql.NullString
	Channels   []string
	Recipients []NotificationRecipient
}

// matchEventPoliciesSQL은 이벤트를 커버하는 정책을 다시 계산한다.
// dedup_key에 정책 id가 없으므로(설계 4.1) 이벤트 1건을 여러 정책이 커버할 수 있고,
// 수신자는 그 정책들의 합집합이 된다.
const matchEventPoliciesSQL = `
	SELECT e.id, c.owner_id,
	       array_to_string(p.channels, chr(31)),
	       p.recipients::text
	  FROM notification_events e
	  JOIN certificates c ON c.id = e.certificate_id
	  JOIN notification_policies p
	    ON p.enabled
	   AND e.event_type = ANY(p.event_types)
	   AND (e.threshold_days IS NULL OR e.threshold_days = ANY(p.thresholds))
	   AND (p.filter_environments IS NULL OR cardinality(p.filter_environments) = 0
	        OR c.environment = ANY(p.filter_environments))
	   AND (p.filter_department_ids IS NULL OR cardinality(p.filter_department_ids) = 0
	        OR c.department_id = ANY(p.filter_department_ids))
	 WHERE e.id = ANY(string_to_array($1, chr(31))::uuid[])
	   AND e.status = 'pending'
	 ORDER BY e.id, p.created_at, p.id`

// channelKey는 (이벤트, 채널) 단위 집계 키다.
type channelKey struct {
	eventID string
	channel string
}

// resolveRecipients는 한 정책이 한 채널에 대해 만들어내는 수신자 식별자를 반환한다.
// email 채널은 메일 주소, inbox 채널은 사용자 id, webhook/syslog는 빈 문자열
// (수신자 개념이 없는 채널 브로드캐스트 — 설계 14.1) 하나다.
// owner 해석에 실패하면 정책의 static 수신자로 폴백한다 (설계 14.3).
func resolveRecipients(channel string, ownerID sql.NullString, rs []NotificationRecipient,
	byID map[string]notifyUser, byRole map[string][]notifyUser) (out []string, ownerFailed bool) {
	if channel == NotifyChannelWebhook || channel == NotifyChannelSyslog {
		return []string{""}, false
	}

	add := func(u notifyUser) {
		if channel == NotifyChannelEmail {
			if u.Email != "" {
				out = append(out, u.Email)
			}
			return
		}
		out = append(out, u.ID)
	}
	var statics []string
	ownerRequested, ownerResolved := false, false

	for _, r := range rs {
		switch r.Kind {
		case RecipientOwner:
			ownerRequested = true
			before := len(out)
			if ownerID.Valid {
				if u, ok := byID[ownerID.String]; ok {
					add(u)
				}
			}
			if len(out) > before {
				ownerResolved = true
			}
		case RecipientRole:
			for _, u := range byRole[r.Role] {
				add(u)
			}
		case RecipientUser:
			if u, ok := byID[r.UserID]; ok {
				add(u)
			}
		case RecipientStatic:
			// static은 주소이므로 메일 채널에만 의미가 있다.
			// 알림함은 사용자 행이 필요해 주소로 대체할 수 없다.
			if channel == NotifyChannelEmail && r.Email != "" {
				statics = append(statics, r.Email)
				out = append(out, r.Email)
			}
		}
	}
	// 폴백: owner 해석에 실패해도 정책의 static 수신자가 그 자리를 메운다(이미 out에 있다).
	// 그것도 없을 때만 실패로 표시해 호출부가 사유를 남기게 한다 (설계 14.3).
	ownerFailed = ownerRequested && !ownerResolved && len(statics) == 0
	return out, ownerFailed
}

// FanOutDeliveries는 pending 이벤트에 대해 채널×수신자 딜리버리 행을 만든다.
// 생성된(중복이 아닌) 행 수를 반환한다.
func (s *Store) FanOutDeliveries(ctx context.Context, eventIDs []string) (int, error) {
	return fanOutDeliveries(ctx, s.db, eventIDs)
}

func fanOutDeliveries(ctx context.Context, q querier, eventIDs []string) (int, error) {
	if len(eventIDs) == 0 {
		return 0, nil
	}
	byID, byRole, err := loadNotifyUsers(ctx, q)
	if err != nil {
		return 0, err
	}

	rows, err := q.QueryContext(ctx, matchEventPoliciesSQL, strings.Join(eventIDs, sansSep))
	if err != nil {
		return 0, fmt.Errorf("store: match event policies: %w", err)
	}
	matches := []eventPolicyMatch{}
	for rows.Next() {
		var (
			m          eventPolicyMatch
			channels   string
			recipients string
		)
		if err := rows.Scan(&m.EventID, &m.OwnerID, &channels, &recipients); err != nil {
			rows.Close() //nolint:errcheck
			return 0, fmt.Errorf("store: scan event policy match: %w", err)
		}
		m.Channels = splitNonEmpty(channels, sansSep)
		if recipients != "" {
			if err := json.Unmarshal([]byte(recipients), &m.Recipients); err != nil {
				rows.Close() //nolint:errcheck
				return 0, fmt.Errorf("store: decode policy recipients: %w", err)
			}
		}
		matches = append(matches, m)
	}
	rows.Close() //nolint:errcheck
	if err := rows.Err(); err != nil {
		return 0, fmt.Errorf("store: match event policies: %w", err)
	}

	// (이벤트, 채널)별로 정책들의 수신자를 합집합으로 모은다.
	// 여기서 비어 있는 채널만 skipped 사유를 남긴다 — 다른 정책이 같은 채널에
	// 수신자를 하나라도 제공하면 사유 행을 만들지 않는다.
	seen := map[channelKey]map[string]bool{}
	order := []channelKey{}
	reason := map[channelKey]string{}
	for _, m := range matches {
		for _, ch := range m.Channels {
			key := channelKey{eventID: m.EventID, channel: ch}
			if _, ok := seen[key]; !ok {
				seen[key] = map[string]bool{}
				order = append(order, key)
			}
			recips, ownerFailed := resolveRecipients(ch, m.OwnerID, m.Recipients, byID, byRole)
			for _, r := range recips {
				seen[key][r] = true
			}
			if len(recips) == 0 {
				if ownerFailed {
					reason[key] = skipReasonOwnerNoFallack
				} else if _, ok := reason[key]; !ok {
					reason[key] = skipReasonNoRecipient
				}
			}
		}
	}

	created := 0
	for _, key := range order {
		recips := make([]string, 0, len(seen[key]))
		for r := range seen[key] {
			recips = append(recips, r)
		}
		sort.Strings(recips) // 삽입 순서를 결정론적으로 고정한다.

		if len(recips) == 0 {
			why := reason[key]
			if why == "" {
				why = skipReasonNoRecipient
			}
			n, err := insertDelivery(ctx, q, key.eventID, key.channel, "", DeliveryStatusSkipped, &why)
			if err != nil {
				return created, err
			}
			created += n
			continue
		}
		for _, r := range recips {
			n, err := insertDelivery(ctx, q, key.eventID, key.channel, r, DeliveryStatusPending, nil)
			if err != nil {
				return created, err
			}
			created += n
		}
	}
	return created, nil
}

// insertDelivery는 딜리버리 1행을 멱등 삽입한다.
// UNIQUE (event_id, channel, recipient) 아비터 — 재평가·재시작으로 같은 조합이
// 다시 들어와도 발송 행이 늘지 않는다 (설계 3.7).
func insertDelivery(ctx context.Context, q querier, eventID, channel, recipient, status string, lastErr *string) (int, error) {
	res, err := q.ExecContext(ctx, `
		INSERT INTO notification_deliveries (event_id, channel, recipient, status, last_error)
		VALUES ($1::uuid, $2, $3, $4, $5)
		ON CONFLICT (event_id, channel, recipient) DO NOTHING`,
		eventID, channel, recipient, status, lastErr)
	if err != nil {
		return 0, fmt.Errorf("store: insert notification delivery: %w", err)
	}
	n, err := res.RowsAffected()
	if err != nil {
		return 0, fmt.Errorf("store: insert notification delivery: %w", err)
	}
	return int(n), nil
}

// ===== 스케줄러 tick =====

// NotificationTickResult는 tick 1회의 결과 요약이다.
type NotificationTickResult struct {
	Events     []DetectedEvent
	Deliveries int
}

// RunNotificationTick은 탐지+팬아웃을 단일 트랜잭션으로 실행한다.
// 트랜잭션 시작 직후 고정 키 advisory lock을 시도하고, 잡지 못하면 (nil, false, nil)을
// 반환한다 — 다른 인스턴스가 이미 tick을 도는 중이라는 뜻이며 오류가 아니다 (설계 7.2).
// 락은 트랜잭션 종료와 함께 자동 해제되므로 별도 해제 코드가 없다.
// seed 모드는 발송하지 않으므로 팬아웃을 건너뛴다.
func (s *Store) RunNotificationTick(ctx context.Context, mode DetectMode) (*NotificationTickResult, bool, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, false, fmt.Errorf("store: begin: %w", err)
	}
	defer tx.Rollback() //nolint:errcheck

	var locked bool
	// 키는 'notify'의 ASCII 바이트다 (감사 체인 트리거의 x'6175646974'와 같은 규약).
	if err := tx.QueryRowContext(ctx,
		`SELECT pg_try_advisory_xact_lock(x'6e6f74696679'::bigint)`).Scan(&locked); err != nil {
		return nil, false, fmt.Errorf("store: notification scheduler lock: %w", err)
	}
	if !locked {
		return nil, false, nil
	}

	events, err := detectExpiryEvents(ctx, tx, mode)
	if err != nil {
		return nil, true, err
	}
	res := &NotificationTickResult{Events: events}
	if mode != DetectModeSeed && len(events) > 0 {
		ids := make([]string, 0, len(events))
		for _, e := range events {
			ids = append(ids, e.ID)
		}
		n, err := fanOutDeliveries(ctx, tx, ids)
		if err != nil {
			return nil, true, err
		}
		res.Deliveries = n
	}
	if err := tx.Commit(); err != nil {
		return nil, true, fmt.Errorf("store: commit: %w", err)
	}
	return res, true, nil
}

// ===== 디스패처 리스 · 결과 기록 =====

// LeasedDelivery는 리스로 배정된 발송 1건이다. 채널 어댑터가 필요한 모든 사실을
// 이벤트에서 함께 실어 보낸다(어댑터가 DB를 다시 읽지 않는다).
type LeasedDelivery struct {
	ID             string
	EventID        string
	Channel        string
	Recipient      string
	AttemptCount   int
	EventType      string
	Severity       string
	CertificateID  *string
	ThresholdDays  *int
	Payload        NotificationPayload
	LeaseExpiresAt time.Time
}

// LeaseDeliveries는 발송 차례가 된 pending 딜리버리를 최대 limit건 잡아
// sending으로 전이하고 리스 만료 시각을 건다.
// FOR UPDATE SKIP LOCKED — 디스패처가 여럿이어도 같은 행을 두 번 집지 않는다.
func (s *Store) LeaseDeliveries(ctx context.Context, limit int) ([]LeasedDelivery, error) {
	if limit < 1 {
		limit = 1
	}
	if limit > maxLeaseBatch {
		limit = maxLeaseBatch
	}
	rows, err := s.db.QueryContext(ctx, `
		UPDATE notification_deliveries d
		   SET status = 'sending', leased_at = now(),
		       lease_expires_at = now() + make_interval(secs => $2), updated_at = now()
		  FROM notification_events e
		 WHERE e.id = d.event_id
		   AND d.id IN (
		       SELECT id FROM notification_deliveries
		        WHERE status = 'pending' AND next_attempt_at <= now()
		        ORDER BY next_attempt_at, created_at
		        LIMIT $1
		        FOR UPDATE SKIP LOCKED)
		RETURNING d.id, d.event_id, d.channel, d.recipient, d.attempt_count, d.lease_expires_at,
		          e.event_type, e.severity, e.certificate_id, e.threshold_days, e.payload::text`,
		limit, deliveryLeaseTTL.Seconds())
	if err != nil {
		return nil, fmt.Errorf("store: lease notification deliveries: %w", err)
	}
	defer rows.Close() //nolint:errcheck

	out := []LeasedDelivery{}
	for rows.Next() {
		var (
			d       LeasedDelivery
			payload string
		)
		if err := rows.Scan(&d.ID, &d.EventID, &d.Channel, &d.Recipient, &d.AttemptCount,
			&d.LeaseExpiresAt, &d.EventType, &d.Severity, &d.CertificateID, &d.ThresholdDays,
			&payload); err != nil {
			return nil, fmt.Errorf("store: scan leased delivery: %w", err)
		}
		if err := json.Unmarshal([]byte(payload), &d.Payload); err != nil {
			return nil, fmt.Errorf("store: decode event payload: %w", err)
		}
		out = append(out, d)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("store: lease notification deliveries: %w", err)
	}
	return out, nil
}

// deliveryBackoff는 attempt회차 실패 뒤 다음 시도까지의 대기 시간이다.
// 1m · 2m · 4m · 8m … 이며 deliveryRetryMax(1시간)로 상한을 건다 (설계 6.1).
// MarkDeliveryFailed의 SQL이 같은 식을 쓰며, 통합 테스트가 둘의 일치를 고정한다.
func deliveryBackoff(attempt int) time.Duration {
	if attempt < 1 {
		attempt = 1
	}
	d := float64(deliveryRetryBase) * math.Pow(2, float64(attempt-1))
	if d > float64(deliveryRetryMax) {
		return deliveryRetryMax
	}
	return time.Duration(d)
}

// truncateError는 저장할 오류 문자열을 maxDeliveryErrorRunes로 자른다.
// 룬 단위로 자른다 — 바이트로 자르면 한글이 깨진 채 저장된다.
func truncateError(s string) string {
	r := []rune(s)
	if len(r) <= maxDeliveryErrorRunes {
		return s
	}
	return string(r[:maxDeliveryErrorRunes])
}

// MarkDeliverySent는 발송 성공을 기록한다.
// pending도 허용한다 — 리스가 만료돼 리퍼가 되돌린 직후 발송이 성공한 경우
// (at-least-once, 설계 13) 그 사실을 잃지 않기 위해서다.
//
// attempt_count는 성공에서도 올린다 — 이 컬럼은 "실패 횟수"가 아니라 "시도 횟수"이며
// MarkDeliveryFailed와 규약이 같아야 한다. 올리지 않으면 발송 이력 화면의 sent 행이
// 전부 "시도 0"으로 보이는 반면 디스패처 로그는 같은 건을 attempt: 1로 남긴다.
func (s *Store) MarkDeliverySent(ctx context.Context, deliveryID string) error {
	res, err := s.db.ExecContext(ctx, `
		UPDATE notification_deliveries
		   SET status = 'sent', sent_at = now(), last_error = NULL,
		       attempt_count = attempt_count + 1,
		       leased_at = NULL, lease_expires_at = NULL, updated_at = now()
		 WHERE id = $1::uuid AND status IN ('sending', 'pending')`, deliveryID)
	if err != nil {
		return fmt.Errorf("store: mark delivery sent: %w", err)
	}
	n, err := res.RowsAffected()
	if err != nil {
		return fmt.Errorf("store: mark delivery sent: %w", err)
	}
	if n == 0 {
		return ErrDeliveryState
	}
	return nil
}

// MarkDeliverySkipped는 시도 없이 딜리버리를 skipped로 종결한다.
// 디스패처가 config에서 비활성된 채널을 만났을 때 쓰는 경로다 — 이것이 없으면
// 설정이 고쳐질 때까지 pending이 무한히 쌓인다 (notify.DispatcherStore).
//
// 이미 종료 상태(sent/failed/skipped)인 행은 사유조차 덮어쓰지 않고 조용히 성공을
// 반환한다(멱등) — 디스패처 tick이 겹쳐도 종결된 기록을 뒤집지 않기 위해서다.
// reason은 500자로 절단해 저장하며, 수신자 주소를 담지 않는 것은 호출부 책임이다.
func (s *Store) MarkDeliverySkipped(ctx context.Context, deliveryID, reason string) error {
	_, err := s.db.ExecContext(ctx, `
		UPDATE notification_deliveries
		   SET status = 'skipped', last_error = $2,
		       leased_at = NULL, lease_expires_at = NULL, updated_at = now()
		 WHERE id = $1::uuid AND status IN ('sending', 'pending')`,
		deliveryID, truncateError(reason))
	if err != nil {
		return fmt.Errorf("store: mark delivery skipped: %w", err)
	}
	return nil
}

// DeliveryAttempt는 실패 기록 후의 딜리버리 상태다.
type DeliveryAttempt struct {
	Status        string
	AttemptCount  int
	NextAttemptAt time.Time
}

// MarkDeliveryFailed는 발송 실패를 기록한다.
// permanent면 즉시 failed로 종결하고, 임시 실패면 attempt_count를 올린 뒤
// 백오프를 걸어 pending으로 되돌린다. maxDeliveryAttempts회째 실패는 failed다.
// sendErr는 500자로 절단해 저장하며, 수신자 주소를 담지 않는 것은 호출부 책임이다.
func (s *Store) MarkDeliveryFailed(ctx context.Context, deliveryID, sendErr string, permanent bool) (*DeliveryAttempt, error) {
	var a DeliveryAttempt
	err := s.db.QueryRowContext(ctx, `
		UPDATE notification_deliveries
		   SET attempt_count    = attempt_count + 1,
		       status           = CASE WHEN $3 OR attempt_count + 1 >= $4 THEN 'failed' ELSE 'pending' END,
		       next_attempt_at  = now() + make_interval(
		                              secs => LEAST($5::double precision * power(2, attempt_count), $6::double precision)),
		       last_error       = $2,
		       leased_at        = NULL,
		       lease_expires_at = NULL,
		       updated_at       = now()
		 WHERE id = $1::uuid AND status IN ('sending', 'pending')
		RETURNING status, attempt_count, next_attempt_at`,
		deliveryID, truncateError(sendErr), permanent, maxDeliveryAttempts,
		deliveryRetryBase.Seconds(), deliveryRetryMax.Seconds(),
	).Scan(&a.Status, &a.AttemptCount, &a.NextAttemptAt)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrDeliveryState
	}
	if err != nil {
		return nil, fmt.Errorf("store: mark delivery failed: %w", err)
	}
	return &a, nil
}

// ReapExpiredDeliveryLeases는 리스가 만료된 sending 행을 pending으로 되돌린다.
// 스캔 Job과 달리 failed가 아니라 재시도 대기로 회수한다 — 발송은 재실행 비용이
// 낮고, 프로세스가 발송 도중 죽었을 뿐이기 때문이다 (설계 8·7.5).
// attempt_count는 올리지 않는다(시도 자체가 성립하지 않았다).
func (s *Store) ReapExpiredDeliveryLeases(ctx context.Context) (int, error) {
	res, err := s.db.ExecContext(ctx, `
		UPDATE notification_deliveries
		   SET status = 'pending', leased_at = NULL, lease_expires_at = NULL, updated_at = now()
		 WHERE status = 'sending' AND lease_expires_at < now()`)
	if err != nil {
		return 0, fmt.Errorf("store: reap expired delivery leases: %w", err)
	}
	n, err := res.RowsAffected()
	if err != nil {
		return 0, fmt.Errorf("store: reap expired delivery leases: %w", err)
	}
	return int(n), nil
}

// ===== 알림함 =====

// 알림함 목록 필터. archived는 보관함 뷰이고, all은 "보관하지 않은 전체"다.
const (
	InboxFilterUnread   = "unread"
	InboxFilterAll      = "all"
	InboxFilterArchived = "archived"
)

// InboxItem은 알림함 행 1건이다. 사건 시점의 표시 내용을 굳혀 두므로
// 인증서가 지워져도 읽을 수 있다.
type InboxItem struct {
	ID            string     `json:"id"`
	EventID       string     `json:"event_id"`
	Title         string     `json:"title"`
	Body          string     `json:"body"`
	Severity      string     `json:"severity"`
	CertificateID *string    `json:"certificate_id"`
	ReadAt        *time.Time `json:"read_at"`
	ArchivedAt    *time.Time `json:"archived_at"`
	CreatedAt     time.Time  `json:"created_at"`
}

// InboxList는 알림함 목록 조회 결과다.
type InboxList struct {
	Items    []InboxItem `json:"items"`
	Total    int         `json:"total"`
	Unread   int         `json:"unread"`
	Page     int         `json:"page"`
	PageSize int         `json:"page_size"`
}

// InboxInsert는 알림함 행 1건의 저장 요청이다.
type InboxInsert struct {
	UserID        string
	EventID       string
	Title         string
	Body          string
	Severity      string
	CertificateID *string
}

const inboxColumns = `id, event_id, title, body, severity, certificate_id,
	read_at, archived_at, created_at`

func scanInboxItem(row rowScanner) (InboxItem, error) {
	var it InboxItem
	err := row.Scan(&it.ID, &it.EventID, &it.Title, &it.Body, &it.Severity,
		&it.CertificateID, &it.ReadAt, &it.ArchivedAt, &it.CreatedAt)
	return it, err
}

// InsertInboxItem은 알림함 행을 멱등 삽입한다.
// UNIQUE (user_id, event_id) 아비터 — 발송 재시도로 같은 사건이 같은 사용자에게
// 두 번 꽂히지 않는다 (설계 5.4·13). 새로 만들어졌으면 true.
func (s *Store) InsertInboxItem(ctx context.Context, in InboxInsert) (bool, error) {
	res, err := s.db.ExecContext(ctx, `
		INSERT INTO notification_inbox (user_id, event_id, title, body, severity, certificate_id)
		VALUES ($1::uuid, $2::uuid, $3, $4, $5, $6::uuid)
		ON CONFLICT (user_id, event_id) DO NOTHING`,
		in.UserID, in.EventID, in.Title, in.Body, in.Severity, in.CertificateID)
	if err != nil {
		return false, fmt.Errorf("store: insert inbox item: %w", err)
	}
	n, err := res.RowsAffected()
	if err != nil {
		return false, fmt.Errorf("store: insert inbox item: %w", err)
	}
	return n == 1, nil
}

// inboxFilterCond는 필터별 WHERE 조건 조각이다.
func inboxFilterCond(filter string) string {
	switch filter {
	case InboxFilterArchived:
		return "archived_at IS NOT NULL"
	case InboxFilterAll:
		return "archived_at IS NULL"
	default: // unread
		return "read_at IS NULL AND archived_at IS NULL"
	}
}

// ListInbox는 본인 알림함을 최신순으로 조회한다.
// user_id는 항상 바인드 조건으로 들어간다 — 남의 알림은 어떤 필터로도 나오지 않는다.
func (s *Store) ListInbox(ctx context.Context, userID, filter string, page, pageSize int) (*InboxList, error) {
	if page < 1 {
		page = 1
	}
	if pageSize < 1 {
		pageSize = defaultPageSize
	}
	if pageSize > maxPageSize {
		pageSize = maxPageSize
	}
	cond := inboxFilterCond(filter)

	out := &InboxList{Items: []InboxItem{}, Page: page, PageSize: pageSize}
	if err := s.db.QueryRowContext(ctx,
		`SELECT count(*) FROM notification_inbox WHERE user_id = $1::uuid AND `+cond,
		userID).Scan(&out.Total); err != nil {
		return nil, fmt.Errorf("store: count inbox: %w", err)
	}
	unread, err := s.CountUnreadInbox(ctx, userID)
	if err != nil {
		return nil, err
	}
	out.Unread = unread

	rows, err := s.db.QueryContext(ctx,
		`SELECT `+inboxColumns+` FROM notification_inbox
		  WHERE user_id = $1::uuid AND `+cond+`
		  ORDER BY created_at DESC, id DESC LIMIT $2 OFFSET $3`,
		userID, pageSize, (page-1)*pageSize)
	if err != nil {
		return nil, fmt.Errorf("store: list inbox: %w", err)
	}
	defer rows.Close() //nolint:errcheck
	for rows.Next() {
		it, err := scanInboxItem(rows)
		if err != nil {
			return nil, fmt.Errorf("store: scan inbox item: %w", err)
		}
		out.Items = append(out.Items, it)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("store: list inbox: %w", err)
	}
	return out, nil
}

// CountUnreadInbox는 배지용 안 읽음 개수다(보관한 것은 제외).
func (s *Store) CountUnreadInbox(ctx context.Context, userID string) (int, error) {
	var n int
	if err := s.db.QueryRowContext(ctx, `
		SELECT count(*) FROM notification_inbox
		 WHERE user_id = $1::uuid AND read_at IS NULL AND archived_at IS NULL`,
		userID).Scan(&n); err != nil {
		return 0, fmt.Errorf("store: count unread inbox: %w", err)
	}
	return n, nil
}

// MarkInboxRead는 본인 알림 1건을 읽음 처리한다. 이미 읽었으면 시각을 유지한다.
// 남의 행이면 ErrNotFound — 소유 여부를 응답으로 흘리지 않기 위해 403이 아니다.
// 알림함 읽음·보관은 감사 대상이 아니다 (설계 12 ①).
func (s *Store) MarkInboxRead(ctx context.Context, userID, itemID string) error {
	return s.updateInboxItem(ctx, userID, itemID,
		`UPDATE notification_inbox SET read_at = coalesce(read_at, now())
		  WHERE id = $1::uuid AND user_id = $2::uuid`)
}

// ArchiveInboxItem은 본인 알림 1건을 보관한다(읽음도 함께 처리한다).
func (s *Store) ArchiveInboxItem(ctx context.Context, userID, itemID string) error {
	return s.updateInboxItem(ctx, userID, itemID,
		`UPDATE notification_inbox
		    SET archived_at = coalesce(archived_at, now()), read_at = coalesce(read_at, now())
		  WHERE id = $1::uuid AND user_id = $2::uuid`)
}

func (s *Store) updateInboxItem(ctx context.Context, userID, itemID, query string) error {
	if !uuidRe.MatchString(strings.ToLower(strings.TrimSpace(itemID))) {
		return ErrNotFound
	}
	res, err := s.db.ExecContext(ctx, query, itemID, userID)
	if err != nil {
		return fmt.Errorf("store: update inbox item: %w", err)
	}
	n, err := res.RowsAffected()
	if err != nil {
		return fmt.Errorf("store: update inbox item: %w", err)
	}
	if n == 0 {
		return ErrNotFound
	}
	return nil
}

// MarkAllInboxRead는 본인의 안 읽은 알림을 모두 읽음 처리하고 그 수를 반환한다.
func (s *Store) MarkAllInboxRead(ctx context.Context, userID string) (int, error) {
	res, err := s.db.ExecContext(ctx, `
		UPDATE notification_inbox SET read_at = now()
		 WHERE user_id = $1::uuid AND read_at IS NULL AND archived_at IS NULL`, userID)
	if err != nil {
		return 0, fmt.Errorf("store: mark all inbox read: %w", err)
	}
	n, err := res.RowsAffected()
	if err != nil {
		return 0, fmt.Errorf("store: mark all inbox read: %w", err)
	}
	return int(n), nil
}

// ===== 딜리버리 이력 조회 · 보존 =====

// DeliveryRecord는 발송 이력 1건이다 (관리자·auditor 조회용).
// Recipient를 포함하는 이유는 "왜 안 왔는지"를 답하기 위해서다 (설계 14.3).
// 이 값을 로그로 옮기지 않는다 (설계 11.4).
type DeliveryRecord struct {
	ID            string     `json:"id"`
	EventID       string     `json:"event_id"`
	Channel       string     `json:"channel"`
	Recipient     string     `json:"recipient"`
	Status        string     `json:"status"`
	AttemptCount  int        `json:"attempt_count"`
	NextAttemptAt time.Time  `json:"next_attempt_at"`
	LastError     *string    `json:"last_error"`
	SentAt        *time.Time `json:"sent_at"`
	CreatedAt     time.Time  `json:"created_at"`
	EventType     string     `json:"event_type"`
	Severity      string     `json:"severity"`
	CertificateID *string    `json:"certificate_id"`
	ThresholdDays *int       `json:"threshold_days"`
}

// ListDeliveriesInput은 발송 이력 조회 조건이다. 빈 값은 필터하지 않는다.
type ListDeliveriesInput struct {
	Status   string
	Channel  string
	EventID  string
	Page     int
	PageSize int
}

// DeliveryList는 발송 이력 조회 결과다.
type DeliveryList struct {
	Items    []DeliveryRecord `json:"items"`
	Total    int              `json:"total"`
	Page     int              `json:"page"`
	PageSize int              `json:"page_size"`
}

// ListNotificationDeliveries는 발송 이력을 최신순으로 조회한다.
func (s *Store) ListNotificationDeliveries(ctx context.Context, in ListDeliveriesInput) (*DeliveryList, error) {
	page, size := in.Page, in.PageSize
	if page < 1 {
		page = 1
	}
	if size < 1 {
		size = defaultPageSize
	}
	if size > maxPageSize {
		size = maxPageSize
	}

	var (
		conds []string
		args  []any
	)
	arg := func(v any) string {
		args = append(args, v)
		return fmt.Sprintf("$%d", len(args))
	}
	if in.Status != "" {
		conds = append(conds, "d.status = "+arg(in.Status))
	}
	if in.Channel != "" {
		conds = append(conds, "d.channel = "+arg(in.Channel))
	}
	if in.EventID != "" {
		if !uuidRe.MatchString(strings.ToLower(strings.TrimSpace(in.EventID))) {
			return nil, ErrNotFound
		}
		conds = append(conds, "d.event_id = "+arg(in.EventID)+"::uuid")
	}
	where := ""
	if len(conds) > 0 {
		where = " WHERE " + strings.Join(conds, " AND ")
	}

	out := &DeliveryList{Items: []DeliveryRecord{}, Page: page, PageSize: size}
	if err := s.db.QueryRowContext(ctx,
		"SELECT count(*) FROM notification_deliveries d"+where, args...).Scan(&out.Total); err != nil {
		return nil, fmt.Errorf("store: count notification deliveries: %w", err)
	}

	query := fmt.Sprintf(`
		SELECT d.id, d.event_id, d.channel, d.recipient, d.status, d.attempt_count,
		       d.next_attempt_at, d.last_error, d.sent_at, d.created_at,
		       e.event_type, e.severity, e.certificate_id, e.threshold_days
		  FROM notification_deliveries d
		  JOIN notification_events e ON e.id = d.event_id%s
		 ORDER BY d.created_at DESC, d.id DESC LIMIT $%d OFFSET $%d`,
		where, len(args)+1, len(args)+2)
	rows, err := s.db.QueryContext(ctx, query, append(args, size, (page-1)*size)...)
	if err != nil {
		return nil, fmt.Errorf("store: list notification deliveries: %w", err)
	}
	defer rows.Close() //nolint:errcheck
	for rows.Next() {
		var r DeliveryRecord
		if err := rows.Scan(&r.ID, &r.EventID, &r.Channel, &r.Recipient, &r.Status,
			&r.AttemptCount, &r.NextAttemptAt, &r.LastError, &r.SentAt, &r.CreatedAt,
			&r.EventType, &r.Severity, &r.CertificateID, &r.ThresholdDays); err != nil {
			return nil, fmt.Errorf("store: scan notification delivery: %w", err)
		}
		out.Items = append(out.Items, r)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("store: list notification deliveries: %w", err)
	}
	return out, nil
}

// PurgeResult는 보존 정리로 삭제된 행 수다.
type PurgeResult struct {
	Deliveries int
	Events     int
	InboxItems int
}

// PurgeNotifications는 NOTIFY_RETENTION_DAYS를 넘긴 종료 상태 행을 지운다.
// 진행 중(pending/sending) 딜리버리와 아직 보관하지 않은 알림함 행은 나이와
// 무관하게 남긴다 (설계 7.6).
//
// 순서가 load-bearing이다: 이벤트를 먼저 지우면 FK CASCADE가 아직 읽지 않은
// 알림함 행까지 함께 지운다. 그래서 종료 딜리버리·보관된 알림함을 먼저 지우고,
// 남은 참조가 없는 이벤트만 마지막에 지운다.
//
// 예외 하나가 더 있다: **아직 재탐지되는 이벤트는 나이와 무관하게 남긴다.**
// `certificate.expired`의 탐지 조건 `c.not_after < now()`에는 상한이 없어서, 인벤토리에
// 남아 있는 만료 인증서는 영원히 탐지 대상이다. 이 이벤트를 지우면 dedup_key가 풀려
// 다음 tick에 같은 인증서가 다시 발송된다 — 보존 주기(기본 180일)마다 만료 알림이
// 되살아난다. suppressed(seed) 이벤트는 딜리버리·알림함 참조가 아예 없어 위 두 NOT
// EXISTS를 그냥 통과하므로 특히 위험하다.
// `certificate.expiring`은 이 예외가 필요 없다 — 임계값을 넘긴 인증서는 보존 기간이
// 지나기 전에 만료되어 `not_after >= now()` 조건에서 빠지므로 재탐지되지 않는다.
func (s *Store) PurgeNotifications(ctx context.Context, retentionDays int) (*PurgeResult, error) {
	if retentionDays < 1 {
		return nil, fmt.Errorf("store: retention days must be positive, got %d", retentionDays)
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, fmt.Errorf("store: begin: %w", err)
	}
	defer tx.Rollback() //nolint:errcheck

	res := &PurgeResult{}
	n, err := execCount(ctx, tx, `
		DELETE FROM notification_deliveries
		 WHERE status IN ('sent', 'failed', 'skipped')
		   AND created_at < now() - make_interval(days => $1)`, retentionDays)
	if err != nil {
		return nil, fmt.Errorf("store: purge notification deliveries: %w", err)
	}
	res.Deliveries = n

	n, err = execCount(ctx, tx, `
		DELETE FROM notification_inbox
		 WHERE archived_at IS NOT NULL
		   AND created_at < now() - make_interval(days => $1)`, retentionDays)
	if err != nil {
		return nil, fmt.Errorf("store: purge notification inbox: %w", err)
	}
	res.InboxItems = n

	n, err = execCount(ctx, tx, `
		DELETE FROM notification_events e
		 WHERE e.created_at < now() - make_interval(days => $1)
		   AND NOT EXISTS (SELECT 1 FROM notification_deliveries d WHERE d.event_id = e.id)
		   AND NOT EXISTS (SELECT 1 FROM notification_inbox i WHERE i.event_id = e.id)
		   AND NOT (e.event_type = 'certificate.expired'
		            AND EXISTS (SELECT 1 FROM certificates c
		                         WHERE c.id = e.certificate_id
		                           AND c.status NOT IN ('revoked', 'replaced')
		                           AND c.not_after < now()))`, retentionDays)
	if err != nil {
		return nil, fmt.Errorf("store: purge notification events: %w", err)
	}
	res.Events = n

	if err := tx.Commit(); err != nil {
		return nil, fmt.Errorf("store: commit: %w", err)
	}
	return res, nil
}

func execCount(ctx context.Context, q querier, query string, args ...any) (int, error) {
	res, err := q.ExecContext(ctx, query, args...)
	if err != nil {
		return 0, err
	}
	n, err := res.RowsAffected()
	if err != nil {
		return 0, err
	}
	return int(n), nil
}
