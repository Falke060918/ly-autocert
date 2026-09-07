package domain

import "time"

// AgentStatus는 에이전트(원격 수집기)의 라이프사이클 상태다 (docs/00-spec.md 7.8·12장).
type AgentStatus string

const (
	AgentPending  AgentStatus = "pending"  // 등록 토큰 발급됨, 아직 미등록
	AgentActive   AgentStatus = "active"   // 등록 완료, mTLS 통신 가능
	AgentDisabled AgentStatus = "disabled" // 관리자 비활성화
)

// AllAgentStatuses는 유효한 에이전트 상태 전체다.
var AllAgentStatuses = []AgentStatus{AgentPending, AgentActive, AgentDisabled}

// Valid는 status가 정의된 3종 중 하나인지 확인한다.
func (s AgentStatus) Valid() bool {
	for _, v := range AllAgentStatuses {
		if s == v {
			return true
		}
	}
	return false
}

// Agent는 네트워크 세그먼트당 배치하는 원격 수집기다 (Phase 3a).
// EnrollmentTokenHash는 1회용 등록 토큰의 SHA-256 해시만 담으며(원문 미저장)
// JSON 직렬화에서 항상 제외한다 (보안 하드 규칙 1 — sessions token_hash와 동일 규약).
type Agent struct {
	ID                          string      `json:"id"`
	Name                        string      `json:"name"`
	Status                      AgentStatus `json:"status"`
	EnrollmentTokenHash         string      `json:"-"`
	TokenExpiresAt              time.Time   `json:"token_expires_at"`
	ClientCertFingerprintSHA256 *string     `json:"client_cert_fingerprint_sha256"` // 소문자 hex 64자. 등록 전에는 nil
	RegisteredAt                *time.Time  `json:"registered_at"`
	LastSeenAt                  *time.Time  `json:"last_seen_at"`
	DisabledAt                  *time.Time  `json:"disabled_at"`
	CreatedAt                   time.Time   `json:"created_at"`
	UpdatedAt                   time.Time   `json:"updated_at"`
}
