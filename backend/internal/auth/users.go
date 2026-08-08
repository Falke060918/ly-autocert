// users.go — 사용자 관리(속성 수정·활성 토글·관리자 비밀번호 재설정) 서비스.
//
// 모든 변경은 변경 + 감사 기록을 단일 트랜잭션으로 묶는다 — 감사 실패 시 변경도
// 롤백된다 (보안 하드 규칙 5). 감사 INSERT는 트랜잭션의 **마지막 문장**이다:
// 감사 해시 체인 트리거가 잡는 advisory lock이 커밋까지 유지되기 때문이다.
//
// 비밀번호 평문·해시는 감사 payload·에러 메시지·로그 어디에도 넣지 않는다
// (보안 하드 규칙 3·4). 해시는 기존 bcrypt 경로(HashPassword)를 그대로 쓴다.
package auth

import (
	"context"
	"database/sql"
	"errors"
	"fmt"

	"github.com/Falke060918/INHERIT_AutoCert/backend/internal/audit"
	"github.com/Falke060918/INHERIT_AutoCert/backend/internal/domain"
)

var (
	// ErrUserNotFound는 대상 사용자가 없음을 뜻한다.
	ErrUserNotFound = errors.New("auth: user not found")
	// ErrNoUserChanges는 부분 수정 요청에 바꿀 필드가 하나도 없음을 뜻한다.
	ErrNoUserChanges = errors.New("auth: no fields to update")
	// ErrSelfDisable은 자기 계정 비활성화 시도다 (락아웃 방지).
	ErrSelfDisable = errors.New("auth: cannot disable your own account")
	// ErrSelfRoleChange는 자기 역할 변경 시도다 (락아웃 방지 — 스스로 강등할 수 없다).
	ErrSelfRoleChange = errors.New("auth: cannot change your own role")
	// ErrLastAdmin은 마지막 남은 활성 System Administrator를 비활성화·강등하려는
	// 시도다. 허용하면 제품 전체가 잠긴다.
	ErrLastAdmin = errors.New("auth: the last active system administrator must remain")
)

// UserUpdate는 PATCH 부분 수정 입력이다. 제공되지 않은 필드는 그대로 둔다.
type UserUpdate struct {
	// Role이 nil이면 역할을 바꾸지 않는다.
	Role *domain.Role
	// DisplayNameSet이 true일 때만 display_name을 쓴다.
	// 이때 DisplayName이 nil이면 NULL로 지운다.
	DisplayNameSet bool
	DisplayName    *string
}

// UpdateUser는 역할·표시 이름을 부분 수정하고 user.update 감사 이벤트를 남긴다.
// 자기 역할 변경과 마지막 활성 관리자 강등은 거부한다 (락아웃 방지).
func (s *Service) UpdateUser(ctx context.Context, targetID string, upd UserUpdate, actorID string, meta AuditMeta) (*domain.User, error) {
	if upd.Role == nil && !upd.DisplayNameSet {
		return nil, ErrNoUserChanges
	}
	if upd.Role != nil && !upd.Role.Valid() {
		return nil, fmt.Errorf("auth: invalid role %q", *upd.Role)
	}

	tx, err := s.pool.BeginTx(ctx, nil)
	if err != nil {
		return nil, fmt.Errorf("auth: begin: %w", err)
	}
	defer tx.Rollback() //nolint:errcheck

	before, err := lockUser(ctx, tx, targetID)
	if err != nil {
		return nil, err
	}
	if upd.Role != nil && *upd.Role != before.Role {
		if targetID == actorID {
			return nil, ErrSelfRoleChange
		}
		if before.Role == domain.RoleSystemAdministrator && before.IsActive {
			if err := ensureAnotherActiveAdmin(ctx, tx, targetID); err != nil {
				return nil, err
			}
		}
	}

	role := before.Role
	if upd.Role != nil {
		role = *upd.Role
	}
	displayName := before.DisplayName
	if upd.DisplayNameSet {
		displayName = upd.DisplayName
	}

	row := tx.QueryRowContext(ctx,
		`UPDATE users SET role = $2, display_name = $3, updated_at = now()
		 WHERE id = $1::uuid RETURNING `+userColumns, targetID, role, displayName)
	after, err := scanUser(row)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrUserNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("auth: update user: %w", err)
	}

	// 감사 INSERT는 트랜잭션의 마지막 문장이다 (해시 체인 락 구간 최소화).
	if err := s.rec.RecordTx(ctx, tx, audit.Event{
		Type:       "user.update",
		ActorID:    &actorID,
		TargetType: "user",
		TargetID:   &after.ID,
		RequestID:  meta.RequestID,
		Before:     userAuditValue(before),
		After:      userAuditValue(after),
		Result:     domain.AuditSuccess,
		SourceIP:   meta.SourceIP,
	}); err != nil {
		return nil, err
	}
	if err := tx.Commit(); err != nil {
		return nil, fmt.Errorf("auth: commit update user: %w", err)
	}
	return after, nil
}

// SetUserActive는 계정을 활성/비활성으로 바꾸고 user.enable/user.disable 감사
// 이벤트를 남긴다. 비활성화는 해당 사용자의 활성 세션을 같은 트랜잭션에서 전부
// 폐기한다 — 그렇지 않으면 기존 세션이 TTL 동안 살아남아 차단이 무의미해진다.
// 자기 계정 비활성화와 마지막 활성 관리자 비활성화는 거부한다 (락아웃 방지).
func (s *Service) SetUserActive(ctx context.Context, targetID string, active bool, actorID string, meta AuditMeta) (*domain.User, error) {
	tx, err := s.pool.BeginTx(ctx, nil)
	if err != nil {
		return nil, fmt.Errorf("auth: begin: %w", err)
	}
	defer tx.Rollback() //nolint:errcheck

	before, err := lockUser(ctx, tx, targetID)
	if err != nil {
		return nil, err
	}
	if !active {
		if targetID == actorID {
			return nil, ErrSelfDisable
		}
		if before.IsActive && before.Role == domain.RoleSystemAdministrator {
			if err := ensureAnotherActiveAdmin(ctx, tx, targetID); err != nil {
				return nil, err
			}
		}
	}

	row := tx.QueryRowContext(ctx,
		`UPDATE users SET is_active = $2, updated_at = now()
		 WHERE id = $1::uuid RETURNING `+userColumns, targetID, active)
	after, err := scanUser(row)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrUserNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("auth: set user active: %w", err)
	}
	if !active {
		if err := revokeUserSessions(ctx, tx, targetID); err != nil {
			return nil, err
		}
	}

	eventType := "user.enable"
	if !active {
		eventType = "user.disable"
	}
	if err := s.rec.RecordTx(ctx, tx, audit.Event{
		Type:       eventType,
		ActorID:    &actorID,
		TargetType: "user",
		TargetID:   &after.ID,
		RequestID:  meta.RequestID,
		Before:     userAuditValue(before),
		After:      userAuditValue(after),
		Result:     domain.AuditSuccess,
		SourceIP:   meta.SourceIP,
	}); err != nil {
		return nil, err
	}
	if err := tx.Commit(); err != nil {
		return nil, fmt.Errorf("auth: commit set user active: %w", err)
	}
	return after, nil
}

// ResetPassword는 관리자가 대상 사용자의 비밀번호를 재설정하고
// user.password.reset 감사 이벤트를 남긴다. 재설정은 해당 사용자의 활성 세션을
// 전부 폐기한다 — 탈취된 세션이 새 비밀번호와 무관하게 살아남지 않게 한다.
// 평문·해시는 반환값·감사 payload·에러 어디에도 나타나지 않는다.
func (s *Service) ResetPassword(ctx context.Context, targetID, newPassword, actorID string, meta AuditMeta) error {
	// 정책 검사와 bcrypt 해시는 트랜잭션 밖에서 한다 — 감사 체인 advisory lock을
	// 잡은 채로 수백 ms짜리 해시 계산을 하지 않기 위해서다.
	row := s.pool.QueryRowContext(ctx,
		`SELECT `+userColumns+` FROM users WHERE id = $1::uuid`, targetID)
	target, err := scanUser(row)
	if errors.Is(err, sql.ErrNoRows) {
		return ErrUserNotFound
	}
	if err != nil {
		return fmt.Errorf("auth: load user: %w", err)
	}
	if err := ValidatePassword(newPassword, target.Username); err != nil {
		return err
	}
	hash, err := HashPassword(newPassword, s.bcryptCost)
	if err != nil {
		return fmt.Errorf("auth: hash password: %w", err)
	}

	tx, err := s.pool.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("auth: begin: %w", err)
	}
	defer tx.Rollback() //nolint:errcheck

	res, err := tx.ExecContext(ctx,
		`UPDATE users SET password_hash = $2, updated_at = now() WHERE id = $1::uuid`,
		targetID, hash)
	if err != nil {
		return fmt.Errorf("auth: reset password: %w", err)
	}
	n, err := res.RowsAffected()
	if err != nil {
		return fmt.Errorf("auth: reset password rows: %w", err)
	}
	if n == 0 {
		return ErrUserNotFound
	}
	if err := revokeUserSessions(ctx, tx, targetID); err != nil {
		return err
	}

	// 비밀번호·해시는 감사 값에 넣지 않는다 (보안 하드 규칙 3·4).
	if err := s.rec.RecordTx(ctx, tx, audit.Event{
		Type:       "user.password.reset",
		ActorID:    &actorID,
		TargetType: "user",
		TargetID:   &target.ID,
		RequestID:  meta.RequestID,
		After:      map[string]any{"username": target.Username, "sessions_revoked": true},
		Result:     domain.AuditSuccess,
		SourceIP:   meta.SourceIP,
	}); err != nil {
		return err
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("auth: commit reset password: %w", err)
	}
	return nil
}

// lockUser는 대상 사용자를 FOR UPDATE로 잠그고 읽는다 — 동시 수정 경합에서
// 락아웃 방지 검사와 실제 UPDATE 사이가 벌어지지 않게 한다.
func lockUser(ctx context.Context, tx *sql.Tx, id string) (*domain.User, error) {
	row := tx.QueryRowContext(ctx,
		`SELECT `+userColumns+` FROM users WHERE id = $1::uuid FOR UPDATE`, id)
	u, err := scanUser(row)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrUserNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("auth: load user: %w", err)
	}
	return u, nil
}

// ensureAnotherActiveAdmin은 excludeID 외에 활성 System Administrator가
// 최소 1명 남는지 확인한다. 없으면 ErrLastAdmin.
//
// 도달성 주의 — 현재 HTTP 경로에서는 ErrLastAdmin이 발생하지 않는다.
// 두 호출자(UpdateUser·SetUserActive)는 모두 system_administrator 전용 라우트이고
// (server.go의 protected(..., RoleSystemAdministrator)), Authenticate가 `u.is_active`를
// 요구하므로 actor는 항상 "활성 system_administrator"다. 게다가 두 호출자 모두
// 자기 자신 가드(ErrSelfRoleChange·ErrSelfDisable)를 먼저 통과시키므로
// target != actor가 보장된다 → actor가 곧 excludeID 밖의 활성 관리자라 count >= 1이다.
// 즉 마지막 관리자의 자가 강등·자가 비활성화는 ErrLastAdmin이 아니라
// self_role_change_forbidden·self_disable_forbidden으로 먼저 걸린다.
//
// 그럼에도 가드를 남기는 이유는 심층 방어다: 위 전제(역할 게이트·활성 세션 요구·
// 자기 자신 가드) 중 하나라도 깨지거나 호출 경로가 늘면(내부 배치·향후 API·
// 위임 관리자 역할) 즉시 도달 가능해지고, 그때 실패하면 제품 전체가 잠긴다.
// 서비스 계층 직접 호출은 지금도 도달 가능한 경로이며
// api/users_integration_test.go의 "lockout guard" 케이스들이 이를 고정한다.
func ensureAnotherActiveAdmin(ctx context.Context, tx *sql.Tx, excludeID string) error {
	var n int
	err := tx.QueryRowContext(ctx,
		`SELECT count(*) FROM users
		  WHERE role = $1 AND is_active AND id <> $2::uuid`,
		domain.RoleSystemAdministrator, excludeID).Scan(&n)
	if err != nil {
		return fmt.Errorf("auth: count active administrators: %w", err)
	}
	if n == 0 {
		return ErrLastAdmin
	}
	return nil
}

// revokeUserSessions는 대상 사용자의 미폐기 세션을 전부 폐기한다.
func revokeUserSessions(ctx context.Context, tx *sql.Tx, userID string) error {
	_, err := tx.ExecContext(ctx,
		`UPDATE sessions SET revoked_at = now()
		 WHERE user_id = $1::uuid AND revoked_at IS NULL`, userID)
	if err != nil {
		return fmt.Errorf("auth: revoke sessions: %w", err)
	}
	return nil
}

// userAuditValue는 감사 before/after에 넣을 최소 필드다 — 해시는 제외한다.
func userAuditValue(u *domain.User) map[string]any {
	return map[string]any{
		"username":     u.Username,
		"role":         u.Role,
		"display_name": u.DisplayName,
		"is_active":    u.IsActive,
	}
}
