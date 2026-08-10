package auth

import (
	"context"
	"database/sql"
	"errors"
	"fmt"

	"github.com/Falke060918/INHERIT_AutoCert/backend/internal/audit"
	"github.com/Falke060918/INHERIT_AutoCert/backend/internal/domain"
)

// ErrSetupCompleted는 초기 설정이 이미 끝났음을 뜻한다 (= 사용자가 1명 이상 존재).
// 초기 관리자 생성 경로는 이 시점 이후 영구히 닫힌다.
var ErrSetupCompleted = errors.New("auth: initial setup already completed")

// setupLockKey는 초기 관리자 생성을 직렬화하는 advisory lock 키다 ("setup"의 ASCII hex).
// 알림 스케줄러(x'6e6f74696679')와 같은 관례이며 서로 다른 키라 경합하지 않는다.
const setupLockKey = `x'7365747570'::bigint`

// SetupRequired는 초기 설정 화면을 열어야 하는지(= 사용자가 0명인지) 알려준다.
// 인증 없이 호출되는 경로이므로 계정 수 자체는 노출하지 않고 불리언만 돌려준다.
func (s *Service) SetupRequired(ctx context.Context) (bool, error) {
	var exists bool
	if err := s.pool.QueryRowContext(ctx,
		`SELECT EXISTS (SELECT 1 FROM users)`).Scan(&exists); err != nil {
		return false, fmt.Errorf("auth: setup status: %w", err)
	}
	return !exists, nil
}

// CreateInitialAdmin은 사용자가 0명일 때만 최초 System Administrator를 만든다.
// 인증 없이 호출되는 유일한 변경 경로이므로 "0명일 때만"을 DB가 보장한다 — 두 겹이다.
//  1. 트랜잭션 시작 직후 고정 키 advisory lock으로 동시 요청을 **직렬화**한다.
//     Read Committed에서 뒤에 온 요청은 앞선 요청이 커밋된 뒤에야 락을 얻고,
//     그때는 users가 비어 있지 않다.
//  2. INSERT 자체가 `WHERE NOT EXISTS (SELECT 1 FROM users)`라 한 문장 안에서 판정한다 —
//     행이 만들어지지 않으면(sql.ErrNoRows) ErrSetupCompleted다.
//
// 비밀번호 정책은 일반 생성 경로와 같은 ValidatePassword를 그대로 쓴다.
// 평문 비밀번호는 저장·로그·감사 어디에도 남기지 않는다.
func (s *Service) CreateInitialAdmin(ctx context.Context, username, password string, meta AuditMeta) (*domain.User, error) {
	if err := ValidatePassword(password, username); err != nil {
		return nil, err
	}
	// bcrypt 해시는 트랜잭션 밖에서 계산한다 — advisory lock 보유 구간을 줄인다.
	hash, err := HashPassword(password, s.bcryptCost)
	if err != nil {
		return nil, fmt.Errorf("auth: hash password: %w", err)
	}
	tx, err := s.pool.BeginTx(ctx, nil)
	if err != nil {
		return nil, fmt.Errorf("auth: begin: %w", err)
	}
	defer tx.Rollback() //nolint:errcheck
	if _, err := tx.ExecContext(ctx, `SELECT pg_advisory_xact_lock(`+setupLockKey+`)`); err != nil {
		return nil, fmt.Errorf("auth: setup lock: %w", err)
	}
	row := tx.QueryRowContext(ctx,
		`INSERT INTO users (username, password_hash, role)
		 SELECT $1, $2, $3 WHERE NOT EXISTS (SELECT 1 FROM users)
		 RETURNING `+userColumns, username, hash, domain.RoleSystemAdministrator)
	u, err := scanUser(row)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrSetupCompleted
	}
	if err != nil {
		if isUniqueViolation(err) {
			// users가 비어 있어야만 여기 오므로 사실상 도달하지 않는다.
			return nil, ErrUsernameTaken
		}
		return nil, fmt.Errorf("auth: create initial admin: %w", err)
	}
	// 미인증 경로로 만들어진 계정임을 감사에 남긴다 (보안 하드 규칙 5).
	// actor는 없다 — 아직 로그인할 수 있는 계정이 존재하지 않는 시점이다.
	if err := s.rec.RecordTx(ctx, tx, audit.Event{
		Type:       "user.created",
		TargetType: "user",
		TargetID:   &u.ID,
		RequestID:  meta.RequestID,
		After: map[string]any{"username": u.Username, "role": u.Role,
			"is_active": u.IsActive, "source": "initial_setup"},
		Result:   domain.AuditSuccess,
		SourceIP: meta.SourceIP,
	}); err != nil {
		return nil, err
	}
	if err := tx.Commit(); err != nil {
		return nil, fmt.Errorf("auth: commit initial admin: %w", err)
	}
	return u, nil
}
