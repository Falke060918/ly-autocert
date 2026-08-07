package auth

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"golang.org/x/crypto/bcrypt"

	"github.com/Falke060918/INHERIT_AutoCert/backend/internal/domain"
)

var (
	// ErrInvalidCredentials는 사용자 없음·비밀번호 불일치·비활성 계정을 구분하지 않는다
	// (계정 존재 여부 노출 방지).
	ErrInvalidCredentials = errors.New("auth: invalid credentials")
	// ErrUnauthenticated는 세션 토큰이 없거나 만료·폐기됐음을 뜻한다.
	ErrUnauthenticated = errors.New("auth: unauthenticated")
	// ErrUsernameTaken은 username UNIQUE 위반이다.
	ErrUsernameTaken = errors.New("auth: username already taken")
)

// Service는 로컬 계정 인증과 세션 관리를 담당한다.
type Service struct {
	pool       *sql.DB
	sessionTTL time.Duration
	bcryptCost int
}

// NewService는 운영 기본값(bcrypt cost 12)으로 Service를 만든다.
func NewService(pool *sql.DB, sessionTTL time.Duration) *Service {
	return &Service{pool: pool, sessionTTL: sessionTTL, bcryptCost: bcryptCost}
}

// NewServiceForTest는 테스트용으로 bcrypt 비용을 최소로 낮춘다.
func NewServiceForTest(pool *sql.DB, sessionTTL time.Duration) *Service {
	return &Service{pool: pool, sessionTTL: sessionTTL, bcryptCost: bcrypt.MinCost}
}

const userColumns = `id, username, display_name, password_hash, role, auth_provider,
	is_active, department_id, created_at, updated_at`

func scanUser(row interface{ Scan(...any) error }) (*domain.User, error) {
	var u domain.User
	err := row.Scan(&u.ID, &u.Username, &u.DisplayName, &u.PasswordHash, &u.Role,
		&u.AuthProvider, &u.IsActive, &u.DepartmentID, &u.CreatedAt, &u.UpdatedAt)
	if err != nil {
		return nil, err
	}
	return &u, nil
}

// Login은 자격 증명을 검증하고 새 세션을 만든다.
// 반환된 token은 클라이언트에만 전달한다. 실패 사유는 구분 없이 ErrInvalidCredentials.
func (s *Service) Login(ctx context.Context, username, password string) (user *domain.User, token string, expiresAt time.Time, err error) {
	row := s.pool.QueryRowContext(ctx,
		`SELECT `+userColumns+` FROM users WHERE username = $1`, username)
	u, err := scanUser(row)
	if errors.Is(err, sql.ErrNoRows) {
		// 사용자 없음에도 해시 비교 1회 수행해 타이밍 차이를 줄인다.
		_ = bcrypt.CompareHashAndPassword([]byte("$2a$10$invalidinvalidinvalidinvalidinvalidinvalidinvalidinval"), []byte(password))
		return nil, "", time.Time{}, ErrInvalidCredentials
	}
	if err != nil {
		return nil, "", time.Time{}, fmt.Errorf("auth: login query: %w", err)
	}
	if !VerifyPassword(u.PasswordHash, password) || !u.IsActive {
		return nil, "", time.Time{}, ErrInvalidCredentials
	}

	token, tokenHash, err := NewSessionToken()
	if err != nil {
		return nil, "", time.Time{}, err
	}
	expiresAt = time.Now().UTC().Add(s.sessionTTL)
	_, err = s.pool.ExecContext(ctx,
		`INSERT INTO sessions (user_id, token_hash, expires_at) VALUES ($1, $2, $3)`,
		u.ID, tokenHash, expiresAt)
	if err != nil {
		return nil, "", time.Time{}, fmt.Errorf("auth: create session: %w", err)
	}
	return u, token, expiresAt, nil
}

// Authenticate는 토큰으로 활성 세션의 사용자를 찾는다.
// 만료·폐기·비활성 계정은 모두 ErrUnauthenticated.
func (s *Service) Authenticate(ctx context.Context, token string) (*domain.User, error) {
	if token == "" {
		return nil, ErrUnauthenticated
	}
	row := s.pool.QueryRowContext(ctx,
		`SELECT `+userColumns+` FROM users u
		 WHERE u.is_active
		   AND EXISTS (
		     SELECT 1 FROM sessions s
		     WHERE s.user_id = u.id AND s.token_hash = $1
		       AND s.revoked_at IS NULL AND s.expires_at > now())`,
		HashToken(token))
	u, err := scanUser(row)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrUnauthenticated
	}
	if err != nil {
		return nil, fmt.Errorf("auth: authenticate query: %w", err)
	}
	return u, nil
}

// Logout은 토큰의 세션을 폐기한다. 이미 없거나 폐기된 세션이어도 에러가 아니다(멱등).
func (s *Service) Logout(ctx context.Context, token string) error {
	_, err := s.pool.ExecContext(ctx,
		`UPDATE sessions SET revoked_at = now()
		 WHERE token_hash = $1 AND revoked_at IS NULL`, HashToken(token))
	if err != nil {
		return fmt.Errorf("auth: logout: %w", err)
	}
	return nil
}

// CreateUser는 정책 검사·해시 후 사용자를 만든다. 평문 비밀번호는 저장·로그하지 않는다.
func (s *Service) CreateUser(ctx context.Context, username, password string, displayName *string, role domain.Role) (*domain.User, error) {
	if err := ValidatePassword(password, username); err != nil {
		return nil, err
	}
	if !role.Valid() {
		return nil, fmt.Errorf("auth: invalid role %q", role)
	}
	hash, err := HashPassword(password, s.bcryptCost)
	if err != nil {
		return nil, fmt.Errorf("auth: hash password: %w", err)
	}
	row := s.pool.QueryRowContext(ctx,
		`INSERT INTO users (username, display_name, password_hash, role)
		 VALUES ($1, $2, $3, $4)
		 RETURNING `+userColumns, username, displayName, hash, role)
	u, err := scanUser(row)
	if err != nil {
		if isUniqueViolation(err) {
			return nil, ErrUsernameTaken
		}
		return nil, fmt.Errorf("auth: create user: %w", err)
	}
	return u, nil
}

// ListUsers는 전체 사용자를 username 순으로 반환한다 (Phase 1: 페이지네이션 없음).
func (s *Service) ListUsers(ctx context.Context) ([]*domain.User, error) {
	rows, err := s.pool.QueryContext(ctx,
		`SELECT `+userColumns+` FROM users ORDER BY username`)
	if err != nil {
		return nil, fmt.Errorf("auth: list users: %w", err)
	}
	defer rows.Close()
	users := []*domain.User{}
	for rows.Next() {
		u, err := scanUser(rows)
		if err != nil {
			return nil, fmt.Errorf("auth: scan user: %w", err)
		}
		users = append(users, u)
	}
	return users, rows.Err()
}

// BootstrapAdmin은 users가 비어 있을 때 초기 System Administrator를 만든다.
// password가 비어 있으면 아무것도 하지 않고 경고만 남긴다 (secret 하드코딩 금지 —
// 초기 비밀번호는 환경변수로만 주입한다).
func (s *Service) BootstrapAdmin(ctx context.Context, username, password string) error {
	var n int
	if err := s.pool.QueryRowContext(ctx, `SELECT count(*) FROM users`).Scan(&n); err != nil {
		return fmt.Errorf("auth: count users: %w", err)
	}
	if n > 0 {
		return nil
	}
	if password == "" {
		slog.Warn("no users exist and BOOTSTRAP_ADMIN_PASSWORD is not set; login is impossible until it is provided")
		return nil
	}
	u, err := s.CreateUser(ctx, username, password, nil, domain.RoleSystemAdministrator)
	if err != nil {
		return fmt.Errorf("auth: bootstrap admin: %w", err)
	}
	slog.Info("bootstrap admin created", "username", u.Username, "user_id", u.ID)
	return nil
}

// isUniqueViolation은 PostgreSQL unique_violation(23505)인지 확인한다.
func isUniqueViolation(err error) bool {
	type sqlState interface{ SQLState() string }
	var st sqlState
	return errors.As(err, &st) && st.SQLState() == "23505"
}
