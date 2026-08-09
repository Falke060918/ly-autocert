package domain

import "time"

// Role은 기본 역할 6종이다 (docs/00-spec.md 7.11장).
type Role string

const (
	RoleSystemAdministrator   Role = "system_administrator"
	RolePKIAdministrator      Role = "pki_administrator"
	RoleSecurityAdministrator Role = "security_administrator"
	RoleOperator              Role = "operator"
	RoleAuditor               Role = "auditor"
	RoleViewer                Role = "viewer"
)

// AllRoles는 유효한 역할 전체다. 인증된 사용자 누구나 접근 가능한 라우트에 쓴다.
var AllRoles = []Role{
	RoleSystemAdministrator,
	RolePKIAdministrator,
	RoleSecurityAdministrator,
	RoleOperator,
	RoleAuditor,
	RoleViewer,
}

// Valid는 role이 정의된 6종 중 하나인지 확인한다.
func (r Role) Valid() bool {
	for _, v := range AllRoles {
		if r == v {
			return true
		}
	}
	return false
}

// User는 로컬 계정이다. PasswordHash는 bcrypt 결과만 담으며
// JSON 직렬화에서 항상 제외한다 (보안 하드 규칙 1·3).
type User struct {
	ID           string    `json:"id"`
	Username     string    `json:"username"`
	DisplayName  *string   `json:"display_name"`
	PasswordHash string    `json:"-"`
	Role         Role      `json:"role"`
	AuthProvider string    `json:"auth_provider"` // MVP는 'local'만. LDAP/AD/OIDC/SAML 확장점.
	IsActive     bool      `json:"is_active"`
	DepartmentID *string   `json:"department_id"`
	CreatedAt    time.Time `json:"created_at"`
	UpdatedAt    time.Time `json:"updated_at"`
	// Email은 알림 메일 수신 주소다 (마이그레이션 0012). nil이면 이 계정은
	// owner/role/user 수신자로 지정돼도 메일 채널에서 해석되지 않는다.
	Email *string `json:"email"`
}
