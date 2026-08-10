// adminip.go — 관리 콘솔 접근 IP 허용 목록의 영속화 (마이그레이션 0014).
//
// 잠금 방지가 이 파일의 핵심이다. 변경 3종(추가·활성 토글·삭제)은 전부
// ① 배타 advisory lock으로 직렬화하고, ② 변경을 적용한 뒤 같은 트랜잭션 안에서
// "요청자 IP가 여전히 통과하는가"를 다시 계산해, ③ 통과하지 못하면
// ErrAdminIPSelfLockout으로 롤백한다.
// 핸들러가 변경 전에 미리 계산하는 방식은 동시 요청 두 건이 서로를 잠글 수 있어 쓰지 않는다.
//
// 감사 INSERT는 각 트랜잭션의 마지막 문장이다 (감사 해시 체인의 advisory lock 구간 최소화).
package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"

	"github.com/Falke060918/INHERIT_AutoCert/backend/internal/audit"
	"github.com/Falke060918/INHERIT_AutoCert/backend/internal/domain"
)

// 접근 IP 규칙 감사 이벤트 타입 — 변경 3종은 예외 없이 기록한다 (보안 하드 규칙 5).
const (
	EventAdminIPRuleCreate = "admin_ip_rule.create"
	EventAdminIPRuleUpdate = "admin_ip_rule.update"
	EventAdminIPRuleDelete = "admin_ip_rule.delete"
)

// adminIPLockKey는 규칙 변경을 직렬화하는 advisory lock 키다 ('adminip' 6바이트).
// 동시에 들어온 삭제 두 건이 각자 "나는 아직 통과한다"고 판정한 뒤 함께 커밋되면
// 둘 다 잠기는 창이 생긴다 — 변경 경로 전체를 한 줄로 세운다.
const adminIPLockKey = `SELECT pg_advisory_xact_lock(x'61646d696970'::bigint)`

// adminIPRuleColumns는 규칙 조회 공통 컬럼이다. cidr은 text로 캐스팅해서 읽는다
// (database/sql은 cidr 타입을 직접 스캔하지 못한다 — 패키지 주석의 inet 규약과 동일).
const adminIPRuleColumns = `id, cidr::text, description, is_active, created_by::text, created_at, updated_at`

// ErrAdminIPRuleNotFound는 대상 규칙이 없을 때다.
var ErrAdminIPRuleNotFound = errors.New("store: admin ip rule not found")

// ErrAdminIPRuleExists는 같은 대역이 이미 등록돼 있을 때다.
var ErrAdminIPRuleExists = errors.New("store: admin ip rule already exists")

// ErrAdminIPSelfLockout은 그 변경을 적용하면 요청자 자신이 차단되는 경우다.
// 규칙을 되돌릴 유일한 화면이 그 규칙 뒤에 있으므로, 이 변경은 거부하는 것이 맞다.
var ErrAdminIPSelfLockout = errors.New("store: change would lock out the requesting client ip")

// AdminIPRuleMutation은 규칙 변경 요청의 공통 메타다.
//
// ActorIP는 잠금 방지 판정의 기준이 되는 요청자 출발지 IP다. nil이면 판정을
// 건너뛴다 — 비상 우회 대역(ADMIN_IP_ALLOW_BOOTSTRAP)으로 들어온 복구 요청 전용이며,
// API 계층이 그 경우에만 nil을 넘긴다.
type AdminIPRuleMutation struct {
	ActorID   *string
	ActorIP   *string
	RequestID *string
	SourceIP  *string
}

// CreateAdminIPRuleInput은 규칙 추가 요청이다. CIDR은 API 계층이 정규화한 값이다
// (단일 IP는 /32·/128, 호스트 비트가 켜진 값은 거부된 뒤).
type CreateAdminIPRuleInput struct {
	CIDR        string
	Description string
	IsActive    bool
	AdminIPRuleMutation
}

func scanAdminIPRule(row rowScanner) (*domain.AdminIPRule, error) {
	var r domain.AdminIPRule
	if err := row.Scan(&r.ID, &r.CIDR, &r.Description, &r.IsActive,
		&r.CreatedBy, &r.CreatedAt, &r.UpdatedAt); err != nil {
		return nil, err
	}
	return &r, nil
}

// ListAdminIPRules는 규칙 전체를 대역 순으로 반환한다 (비활성 포함 — 화면이 토글한다).
func (s *Store) ListAdminIPRules(ctx context.Context) ([]domain.AdminIPRule, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT `+adminIPRuleColumns+` FROM admin_ip_rules ORDER BY cidr`)
	if err != nil {
		return nil, fmt.Errorf("store: list admin ip rules: %w", err)
	}
	defer rows.Close()

	rules := []domain.AdminIPRule{}
	for rows.Next() {
		r, err := scanAdminIPRule(rows)
		if err != nil {
			return nil, fmt.Errorf("store: scan admin ip rule: %w", err)
		}
		rules = append(rules, *r)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("store: iterate admin ip rules: %w", err)
	}
	return rules, nil
}

// ActiveAdminIPCIDRs는 활성 규칙의 대역 문자열만 반환한다 — 미들웨어 캐시 적재용이다.
// 빈 슬라이스는 "제한 없음"을 뜻한다 (오류가 아니다).
func (s *Store) ActiveAdminIPCIDRs(ctx context.Context) ([]string, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT cidr::text FROM admin_ip_rules WHERE is_active ORDER BY cidr`)
	if err != nil {
		return nil, fmt.Errorf("store: load active admin ip rules: %w", err)
	}
	defer rows.Close()

	cidrs := []string{}
	for rows.Next() {
		var c string
		if err := rows.Scan(&c); err != nil {
			return nil, fmt.Errorf("store: scan active admin ip rule: %w", err)
		}
		cidrs = append(cidrs, c)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("store: iterate active admin ip rules: %w", err)
	}
	return cidrs, nil
}

// CreateAdminIPRule은 허용 규칙을 추가한다. 추가 결과로 요청자 자신이 차단되면
// ErrAdminIPSelfLockout으로 롤백한다 — 목록이 비어 있던 서버에 자기 대역이 아닌
// 규칙을 처음 넣는 것이 대표적인 잠금 경로다.
func (s *Store) CreateAdminIPRule(ctx context.Context, in CreateAdminIPRuleInput) (*domain.AdminIPRule, error) {
	cidr := strings.TrimSpace(in.CIDR)
	if cidr == "" {
		return nil, errors.New("store: admin ip rule cidr required")
	}

	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, fmt.Errorf("store: begin: %w", err)
	}
	defer tx.Rollback() //nolint:errcheck
	if _, err := tx.ExecContext(ctx, adminIPLockKey); err != nil {
		return nil, fmt.Errorf("store: lock admin ip rules: %w", err)
	}

	rule, err := scanAdminIPRule(tx.QueryRowContext(ctx, `
		INSERT INTO admin_ip_rules (cidr, description, is_active, created_by)
		VALUES ($1::cidr, $2, $3, $4::uuid)
		RETURNING `+adminIPRuleColumns,
		cidr, strings.TrimSpace(in.Description), in.IsActive, in.ActorID))
	switch {
	case isUniqueViolation(err):
		return nil, ErrAdminIPRuleExists
	case err != nil:
		return nil, fmt.Errorf("store: insert admin ip rule: %w", err)
	}

	if err := checkAdminIPLockout(ctx, tx, in.ActorIP); err != nil {
		return nil, err
	}

	if err := audit.Insert(ctx, tx, audit.Event{
		Type:       EventAdminIPRuleCreate,
		ActorID:    in.ActorID,
		TargetType: "admin_ip_rule",
		TargetID:   &rule.ID,
		RequestID:  in.RequestID,
		After:      rule,
		Result:     domain.AuditSuccess,
		SourceIP:   in.SourceIP,
	}); err != nil {
		return nil, err
	}
	if err := tx.Commit(); err != nil {
		return nil, fmt.Errorf("store: commit: %w", err)
	}
	return rule, nil
}

// SetAdminIPRuleActive는 규칙을 활성/비활성으로 토글한다.
// 비활성화가 요청자를 차단하면 ErrAdminIPSelfLockout이다. 마지막 활성 규칙을 끄는 것은
// "제한 없음"이 되므로 항상 허용된다 — 잠금이 아니라 해제이기 때문이다.
func (s *Store) SetAdminIPRuleActive(ctx context.Context, id string, active bool, m AdminIPRuleMutation) (*domain.AdminIPRule, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, fmt.Errorf("store: begin: %w", err)
	}
	defer tx.Rollback() //nolint:errcheck
	if _, err := tx.ExecContext(ctx, adminIPLockKey); err != nil {
		return nil, fmt.Errorf("store: lock admin ip rules: %w", err)
	}

	before, err := scanAdminIPRule(tx.QueryRowContext(ctx,
		`SELECT `+adminIPRuleColumns+` FROM admin_ip_rules WHERE id = $1::uuid FOR UPDATE`, id))
	switch {
	case errors.Is(err, sql.ErrNoRows):
		return nil, ErrAdminIPRuleNotFound
	case err != nil:
		return nil, fmt.Errorf("store: load admin ip rule: %w", err)
	}

	after, err := scanAdminIPRule(tx.QueryRowContext(ctx, `
		UPDATE admin_ip_rules SET is_active = $2, updated_at = now()
		WHERE id = $1::uuid
		RETURNING `+adminIPRuleColumns, id, active))
	if err != nil {
		return nil, fmt.Errorf("store: update admin ip rule: %w", err)
	}

	if err := checkAdminIPLockout(ctx, tx, m.ActorIP); err != nil {
		return nil, err
	}

	if err := audit.Insert(ctx, tx, audit.Event{
		Type:       EventAdminIPRuleUpdate,
		ActorID:    m.ActorID,
		TargetType: "admin_ip_rule",
		TargetID:   &after.ID,
		RequestID:  m.RequestID,
		Before:     before,
		After:      after,
		Result:     domain.AuditSuccess,
		SourceIP:   m.SourceIP,
	}); err != nil {
		return nil, err
	}
	if err := tx.Commit(); err != nil {
		return nil, fmt.Errorf("store: commit: %w", err)
	}
	return after, nil
}

// DeleteAdminIPRule은 규칙을 지운다. 삭제로 요청자가 차단되면 ErrAdminIPSelfLockout이다.
func (s *Store) DeleteAdminIPRule(ctx context.Context, id string, m AdminIPRuleMutation) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("store: begin: %w", err)
	}
	defer tx.Rollback() //nolint:errcheck
	if _, err := tx.ExecContext(ctx, adminIPLockKey); err != nil {
		return fmt.Errorf("store: lock admin ip rules: %w", err)
	}

	before, err := scanAdminIPRule(tx.QueryRowContext(ctx,
		`DELETE FROM admin_ip_rules WHERE id = $1::uuid RETURNING `+adminIPRuleColumns, id))
	switch {
	case errors.Is(err, sql.ErrNoRows):
		return ErrAdminIPRuleNotFound
	case err != nil:
		return fmt.Errorf("store: delete admin ip rule: %w", err)
	}

	if err := checkAdminIPLockout(ctx, tx, m.ActorIP); err != nil {
		return err
	}

	if err := audit.Insert(ctx, tx, audit.Event{
		Type:       EventAdminIPRuleDelete,
		ActorID:    m.ActorID,
		TargetType: "admin_ip_rule",
		TargetID:   &before.ID,
		RequestID:  m.RequestID,
		Before:     before,
		Result:     domain.AuditSuccess,
		SourceIP:   m.SourceIP,
	}); err != nil {
		return err
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("store: commit: %w", err)
	}
	return nil
}

// checkAdminIPLockout은 변경이 적용된 트랜잭션 상태에서 요청자 IP가 여전히
// 통과하는지 확인한다. 판정 규칙은 미들웨어와 동일하다:
//   - 활성 규칙 0건 = 제한 없음 → 통과
//   - 활성 규칙이 있으면 그중 하나라도 IP를 포함해야 통과 (<<= 는 "포함된다")
//
// actorIP가 nil이면 판정을 건너뛴다 (비상 우회 대역으로 들어온 복구 요청).
func checkAdminIPLockout(ctx context.Context, tx *sql.Tx, actorIP *string) error {
	if actorIP == nil {
		return nil
	}
	var total, matched int
	err := tx.QueryRowContext(ctx, `
		SELECT count(*), count(*) FILTER (WHERE $1::inet <<= cidr)
		FROM admin_ip_rules WHERE is_active`, *actorIP).Scan(&total, &matched)
	if err != nil {
		return fmt.Errorf("store: evaluate admin ip lockout: %w", err)
	}
	if total > 0 && matched == 0 {
		return ErrAdminIPSelfLockout
	}
	return nil
}
