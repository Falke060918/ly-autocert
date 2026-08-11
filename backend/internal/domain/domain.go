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
	ID                string `json:"id"`
	FingerprintSHA256 string `json:"fingerprint_sha256"` // 소문자 hex 64자, 중복 판정 키
	SerialNumber      string `json:"serial_number"`
	SubjectDN         string `json:"subject_dn"`
	IssuerDN          string `json:"issuer_dn"`
	// IssuerOrganization은 발급자 DN의 O(Organization) 값이다 (0016).
	// 신규 행은 certparse.Describe가 x509 pkix.Name에서 직접 뽑고,
	// 0016 이전 행은 issuer_dn 문자열에서 소급 백필됐다. O가 없으면 nil.
	IssuerOrganization *string           `json:"issuer_organization"`
	CommonName         string            `json:"common_name"`
	SANs               []string          `json:"sans"`
	NotBefore          time.Time         `json:"not_before"`
	NotAfter           time.Time         `json:"not_after"`
	PublicKeyAlgorithm string            `json:"public_key_algorithm"`
	PublicKeySize      int               `json:"public_key_size"`
	SignatureAlgorithm string            `json:"signature_algorithm"`
	SubjectKeyID       *string           `json:"subject_key_id"`   // SKI 소문자 hex. 확장 없음·0008 이전 데이터는 nil
	AuthorityKeyID     *string           `json:"authority_key_id"` // AKI 소문자 hex. 확장 없음·0008 이전 데이터는 nil
	CertificateType    CertificateType   `json:"certificate_type"`
	Status             CertificateStatus `json:"status"`
	CAID               *string           `json:"ca_id"`
	OwnerID            *string           `json:"owner_id"`
	DepartmentID       *string           `json:"department_id"`
	Environment        *string           `json:"environment"`
	AutoRenew          bool              `json:"auto_renew"`
	RenewalPolicyID    *string           `json:"renewal_policy_id"`
	// ReplacedByCertificateID·ReplacedAt은 교체 감지(0015)가 채우는 링크다.
	// 같은 엔드포인트에서 이 인증서를 대신한 새 인증서를 가리킨다. Status가
	// replaced가 아니면 둘 다 nil이다.
	ReplacedByCertificateID *string    `json:"replaced_by_certificate_id"`
	ReplacedAt              *time.Time `json:"replaced_at"`
	CreatedAt               time.Time  `json:"created_at"`
	UpdatedAt               time.Time  `json:"updated_at"`
	// Content는 원문에서만 알 수 있는 저장 전용 필드다 (0018). JSON에 실리지 않고
	// 조회 경로도 채우지 않는다 — 아래 타입 주석 참조.
	Content CertificateContent `json:"-"`
}

// CertificateContent는 인증서 DER 원문과 원문에서만 뽑을 수 있는 속성이다 (0018).
//
// **저장 전용(write-only)이다.** 저장 경로(certparse.Describe → upsert)만 채우며
// 조회 경로(certificateColumns)는 읽지 않으므로 조회 결과에서는 항상 제로값이다.
// 원문을 API로 내보내는 경로는 아직 없다 — 내보내기는 별도 판단 대상이다.
// RawDER는 인증서의 공개 부분이며 개인키가 아니다. 값은 로그·에러 메시지에
// 절대 싣지 않는다 (보안 하드 규칙 3).
//
// nil / 빈 슬라이스의 의미가 다르다: 저장 계층은 RawDER가 비어 있으면 "미상"으로
// 보고 전 컬럼에 NULL을 넣고, RawDER가 있으면 빈 슬라이스를 "값이 없음"('{}')으로
// 넣는다. 0018 이전 행이 NULL로 남는 것과 같은 구분이다.
type CertificateContent struct {
	RawDER []byte
	// SPKISHA256은 SubjectPublicKeyInfo의 SHA-256 (소문자 hex 64자)이다.
	SPKISHA256 *string
	// KeyUsage는 x509 KeyUsage 비트마스크다. 0이면 KeyUsage 확장이 없다.
	KeyUsage *int
	// ExtKeyUsage는 EKU 전체 목록이다. certificate_type은 첫 매칭 1개만 반영한다.
	ExtKeyUsage []string
	IsCA        *bool
	// PathLenConstraint는 BasicConstraints의 pathLenConstraint다.
	// -1은 "제약 없음"이고, nil은 비적용(CA가 아니거나 BasicConstraints 없음)이다.
	PathLenConstraint *int
	OCSPURLs          []string
	CRLURLs           []string
	AIACAIssuerURLs   []string
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
