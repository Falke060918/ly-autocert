// Package domain은 핵심 데이터 모델의 Go 타입을 정의한다 (docs/00-spec.md 12장).
// 완전 구현 4개 엔티티만 담는다. CA / RenewalJob / DeploymentJob / Policy는
// placeholder 테이블만 있고 타입은 해당 Phase에서 추가한다.
package domain

import (
	"encoding/json"
	"time"
)

// CertificateStatus는 저장되는 라이프사이클 상태다.
// '만료 임박' 같은 파생 상태는 NotAfter에서 계산하며 저장하지 않는다.
type CertificateStatus string

const (
	CertStatusActive   CertificateStatus = "active"
	CertStatusExpired  CertificateStatus = "expired"
	CertStatusRevoked  CertificateStatus = "revoked"
	CertStatusReplaced CertificateStatus = "replaced"
	CertStatusUnknown  CertificateStatus = "unknown"
)

// CertificateType은 인증서 용도 분류다.
type CertificateType string

const (
	CertTypeServer      CertificateType = "server"
	CertTypeClient      CertificateType = "client"
	CertTypeCA          CertificateType = "ca"
	CertTypeCodeSigning CertificateType = "code_signing"
	CertTypeEmail       CertificateType = "email"
	CertTypeOther       CertificateType = "other"
)

// Certificate는 인벤토리의 중심 엔티티다. 개인키는 포함하지 않는다.
type Certificate struct {
	ID                 string            `json:"id"`
	FingerprintSHA256  string            `json:"fingerprint_sha256"` // 소문자 hex 64자, 중복 판정 키
	SerialNumber       string            `json:"serial_number"`
	SubjectDN          string            `json:"subject_dn"`
	IssuerDN           string            `json:"issuer_dn"`
	CommonName         string            `json:"common_name"`
	SANs               []string          `json:"sans"`
	NotBefore          time.Time         `json:"not_before"`
	NotAfter           time.Time         `json:"not_after"`
	PublicKeyAlgorithm string            `json:"public_key_algorithm"`
	PublicKeySize      int               `json:"public_key_size"`
	SignatureAlgorithm string            `json:"signature_algorithm"`
	CertificateType    CertificateType   `json:"certificate_type"`
	Status             CertificateStatus `json:"status"`
	CAID               *string           `json:"ca_id"`
	OwnerID            *string           `json:"owner_id"`
	DepartmentID       *string           `json:"department_id"`
	Environment        *string           `json:"environment"`
	AutoRenew          bool              `json:"auto_renew"`
	RenewalPolicyID    *string           `json:"renewal_policy_id"`
	CreatedAt          time.Time         `json:"created_at"`
	UpdatedAt          time.Time         `json:"updated_at"`
}

// LocationType은 인증서가 설치된 위치의 종류다. Phase 1은 network_endpoint가 주.
type LocationType string

const (
	LocationNetworkEndpoint LocationType = "network_endpoint"
	LocationFile            LocationType = "file"
	LocationWindowsStore    LocationType = "windows_store"
	LocationKeystore        LocationType = "keystore"
	LocationOther           LocationType = "other"
)

// DeploymentStatus는 위치별 배포 상태다.
type DeploymentStatus string

const (
	DeployUnknown  DeploymentStatus = "unknown"
	DeployDeployed DeploymentStatus = "deployed"
	DeployPending  DeploymentStatus = "pending"
	DeployFailed   DeploymentStatus = "failed"
	DeployRemoved  DeploymentStatus = "removed"
)

// CertificateLocation은 인증서가 실제로 설치된 위치다 (Certificate N:M Target).
type CertificateLocation struct {
	ID               string           `json:"id"`
	CertificateID    string           `json:"certificate_id"`
	TargetID         string           `json:"target_id"`
	LocationType     LocationType     `json:"location_type"`
	FilePath         *string          `json:"file_path"`
	StoreName        *string          `json:"store_name"`
	ServiceName      *string          `json:"service_name"`
	Port             *int             `json:"port"`
	LastVerifiedAt   *time.Time       `json:"last_verified_at"`
	DeploymentStatus DeploymentStatus `json:"deployment_status"`
}

// ConnectionStatus는 대상 시스템 연결 상태다.
type ConnectionStatus string

const (
	ConnUnknown     ConnectionStatus = "unknown"
	ConnReachable   ConnectionStatus = "reachable"
	ConnUnreachable ConnectionStatus = "unreachable"
)

// Target은 인증서가 설치·발견되는 대상 시스템이다.
type Target struct {
	ID               string           `json:"id"`
	Name             string           `json:"name"`
	TargetType       string           `json:"target_type"`
	Hostname         *string          `json:"hostname"`
	IPAddress        *string          `json:"ip_address"`
	OperatingSystem  *string          `json:"operating_system"`
	Environment      *string          `json:"environment"`
	AgentID          *string          `json:"agent_id"` // 에이전트는 Phase 3, 자리만
	ConnectionStatus ConnectionStatus `json:"connection_status"`
	Tags             []string         `json:"tags"`
}

// AuditResult는 감사 이벤트의 결과다.
type AuditResult string

const (
	AuditSuccess AuditResult = "success"
	AuditFailure AuditResult = "failure"
	AuditDenied  AuditResult = "denied"
)

// AuditEvent는 감사 로그 한 건이다. append-only이며 수정·삭제하지 않는다.
// Before/After에 인증서 본문·개인키 등 민감값을 넣지 않는다 (보안 하드 규칙 3).
type AuditEvent struct {
	ID            int64           `json:"id"`
	EventType     string          `json:"event_type"`
	ActorID       *string         `json:"actor_id"`
	TargetType    string          `json:"target_type"`
	TargetID      *string         `json:"target_id"`
	RequestID     *string         `json:"request_id"`
	BeforeValue   json.RawMessage `json:"before_value"`
	AfterValue    json.RawMessage `json:"after_value"`
	Result        AuditResult     `json:"result"`
	SourceIP      *string         `json:"source_ip"`
	CreatedAt     time.Time       `json:"created_at"`
	IntegrityHash string          `json:"integrity_hash"` // SHA-256 해시 체인 무결성 검증값 (이슈 #7)
	PrevHash      string          `json:"prev_hash"`      // 직전 이벤트의 integrity_hash (첫 이벤트는 '0' 64자)
}
