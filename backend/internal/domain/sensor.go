package domain

import "time"

// SensorStatus는 센서(원격 수집기)의 라이프사이클 상태다 (docs/00-spec.md 7.8·12장).
type SensorStatus string

const (
	SensorPending  SensorStatus = "pending"  // 등록 토큰 발급됨, 아직 미등록
	SensorActive   SensorStatus = "active"   // 등록 완료, mTLS 통신 가능
	SensorDisabled SensorStatus = "disabled" // 관리자 비활성화
)

// AllSensorStatuses는 유효한 센서 상태 전체다.
var AllSensorStatuses = []SensorStatus{SensorPending, SensorActive, SensorDisabled}

// Valid는 status가 정의된 3종 중 하나인지 확인한다.
func (s SensorStatus) Valid() bool {
	for _, v := range AllSensorStatuses {
		if s == v {
			return true
		}
	}
	return false
}

// Sensor는 네트워크 세그먼트당 배치하는 원격 수집기다 (Phase 3a).
// EnrollmentTokenHash는 1회용 등록 토큰의 SHA-256 해시만 담으며(원문 미저장)
// JSON 직렬화에서 항상 제외한다 (보안 하드 규칙 1 — sessions token_hash와 동일 규약).
type Sensor struct {
	ID                          string       `json:"id"`
	Name                        string       `json:"name"`
	Status                      SensorStatus `json:"status"`
	EnrollmentTokenHash         string       `json:"-"`
	TokenExpiresAt              time.Time    `json:"token_expires_at"`
	ClientCertFingerprintSHA256 *string      `json:"client_cert_fingerprint_sha256"` // 소문자 hex 64자. 등록 전에는 nil
	RegisteredAt                *time.Time   `json:"registered_at"`
	LastSeenAt                  *time.Time   `json:"last_seen_at"`
	DisabledAt                  *time.Time   `json:"disabled_at"`
	CreatedAt                   time.Time    `json:"created_at"`
	UpdatedAt                   time.Time    `json:"updated_at"`
}

// JobType은 센서에 위임 가능한 작업 유형이다. 폐쇄 enum — 'scan' 하나로 시작하며
// 임의 명령 실행 유형을 추가하지 않는다 (결정론적 자동화 규칙).
type JobType string

const JobTypeScan JobType = "scan"

// Valid는 t가 정의된 작업 유형인지 확인한다.
func (t JobType) Valid() bool { return t == JobTypeScan }

// ExecutorType은 스캔 Job의 실행 주체다 (00-spec 12장 ScanJob 확장 필드).
type ExecutorType string

const (
	ExecutorServer ExecutorType = "server" // 서버 직접 실행 (Phase 1 경로)
	ExecutorSensor ExecutorType = "sensor" // 센서 위임 실행 (pull 리스)
)

// Valid는 e가 정의된 실행 주체인지 확인한다.
func (e ExecutorType) Valid() bool { return e == ExecutorServer || e == ExecutorSensor }

// ScanJobStatusLeased는 0010에서 추가된 스캔 Job 상태다: 센서가 작업을 가져갔고
// 리스 만료 시각 안에 running 전이(하트비트)가 없으면 서버가 failed로 회수한다.
// 기존 상태(pending/running/completed/failed) 문자열은 store 패키지가 관리한다.
const ScanJobStatusLeased = "leased"

// ExecutorScopeServer는 targets.executor_scope의 서버 직접 스캔 값이다.
// 센서 스캔은 'sensor:<sensor_id>' 형태로 스코프를 분리한다(사설 IP 대역 겹침 대응).
const ExecutorScopeServer = "server"
