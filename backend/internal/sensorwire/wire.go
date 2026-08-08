// Package sensorwire는 서버-센서 mTLS API의 wire DTO를 정의한다 (docs/00-spec.md 7.8).
// 서버와 센서 바이너리가 같은 타입을 공유하기 위한 패키지이므로
// 표준 라이브러리 외에는 아무것도 import하지 않는다 (내부 패키지 의존 0).
// 등록 토큰·CSR·인증서는 wire로만 다루고 이 패키지는 아무것도 저장하지 않는다.
package sensorwire

import (
	"encoding/base64"
	"encoding/pem"
	"errors"
	"fmt"
	"net/netip"
	"time"
)

// MaxObservationBatch는 배치 1건의 최대 관측 수다. 멱등 재전송 단위를 제한한다.
const MaxObservationBatch = 1000

// pemTypeCSR은 CSR PEM 블록의 타입이다.
const pemTypeCSR = "CERTIFICATE REQUEST"

// EnrollRequest는 센서 최초 등록 요청이다. 1회용 등록 토큰(원문)과
// 센서 로컬 키쌍으로 만든 CSR을 담는다. 서버는 토큰을 SHA-256 해시로만 대조한다.
type EnrollRequest struct {
	Token  string `json:"token"`
	CSRPEM string `json:"csr_pem"`
}

// Validate는 형식 검사만 한다. 토큰 해시 대조·CSR 서명 검증은 서버 몫이다.
// 에러 메시지에 토큰·CSR 내용은 넣지 않는다 (보안 하드 규칙 3).
func (r EnrollRequest) Validate() error {
	if r.Token == "" {
		return errors.New("sensorwire: enroll: token is required")
	}
	return validateCSRPEM(r.CSRPEM)
}

// EnrollResponse는 등록 성공 응답이다. 발급된 클라이언트 인증서와
// 서버 검증용 CA 인증서를 담는다. 개인키는 센서 호스트를 떠나지 않으므로 없다.
type EnrollResponse struct {
	SensorID      string `json:"sensor_id"`
	ClientCertPEM string `json:"client_cert_pem"`
	CACertPEM     string `json:"ca_cert_pem"`
}

// RenewRequest는 클라이언트 인증서 갱신 요청이다(만료 전 재발급). mTLS 위에서 호출된다.
type RenewRequest struct {
	CSRPEM string `json:"csr_pem"`
}

// Validate는 형식 검사만 한다.
func (r RenewRequest) Validate() error { return validateCSRPEM(r.CSRPEM) }

// RenewResponse는 갱신된 클라이언트 인증서를 담는다.
type RenewResponse struct {
	ClientCertPEM string `json:"client_cert_pem"`
}

// LeaseResponse는 pull 리스로 배정된 작업 1건이다. 작업이 없으면 서버는 204를 반환한다.
// JobType은 폐쇄 enum이며 센서는 모르는 유형을 거부해야 한다 (결정론적 자동화 규칙).
type LeaseResponse struct {
	JobID          string    `json:"job_id"`
	JobType        string    `json:"job_type"` // 'scan'만 정의됨
	Targets        []string  `json:"targets"`  // IP/CIDR 입력 원문
	Ports          []int     `json:"ports"`
	TimeoutMs      int       `json:"timeout_ms"`
	Concurrency    int       `json:"concurrency"`
	LeaseExpiresAt time.Time `json:"lease_expires_at"`
}

// JobStartRequest는 leased → running 전이 보고다. 대상 전개 후 총 엔드포인트 수를 알린다.
type JobStartRequest struct {
	TotalEndpoints int `json:"total_endpoints"`
}

// Validate는 카운터 형식을 검사한다.
func (r JobStartRequest) Validate() error {
	if r.TotalEndpoints < 0 {
		return errors.New("sensorwire: job start: total_endpoints must be >= 0")
	}
	return nil
}

// HeartbeatRequest는 running 중 진행률 보고다. 리스 연장을 겸한다.
type HeartbeatRequest struct {
	ScannedEndpoints   int `json:"scanned_endpoints"`
	ReachableEndpoints int `json:"reachable_endpoints"`
	CertificatesFound  int `json:"certificates_found"`
}

// Validate는 카운터 형식을 검사한다. 하한(>= 0)만 본다 —
// 상한은 Job의 total_endpoints가 정하는 상태 의존 값인데 이 패키지는 Job 상태를
// 모르므로(내부 패키지 의존 0) 서버가 반영 시점에 클램프한다
// (store.ExtendSensorJobLease의 LEAST). 여기서 400으로 거절하지 않는 이유는
// 센서의 진행률 카운터가 원자적으로 함께 읽히지 않아 일시적 역전이 정상이기 때문이다.
func (r HeartbeatRequest) Validate() error {
	if r.ScannedEndpoints < 0 || r.ReachableEndpoints < 0 || r.CertificatesFound < 0 {
		return errors.New("sensorwire: heartbeat: counters must be >= 0")
	}
	return nil
}

// Observation은 엔드포인트 1개의 스캔 관측이다.
// ChainDER는 서버 제시 순서(리프 우선)의 인증서 DER를 base64 표준 인코딩한 배열이다.
type Observation struct {
	IP         string    `json:"ip"`
	Port       int       `json:"port"`
	Reachable  bool      `json:"reachable"`
	TLSVersion string    `json:"tls_version,omitempty"` // 협상된 프로토콜 버전. 도달 실패 시 빈 값
	Hostname   string    `json:"hostname,omitempty"`    // 역방향 DNS 또는 리프 CN 폴백. 없으면 빈 값
	ChainDER   []string  `json:"chain_der,omitempty"`   // base64(DER) 배열. 도달 실패 시 비어 있음
	ObservedAt time.Time `json:"observed_at"`
}

// Validate는 형식 검사만 한다. DER 파싱·체인 검증은 서버 몫이다.
// 에러 메시지에 인증서 바이트를 넣지 않는다 (보안 하드 규칙 3).
func (o Observation) Validate() error {
	if _, err := netip.ParseAddr(o.IP); err != nil {
		return errors.New("sensorwire: observation: ip must be a valid IP address")
	}
	if o.Port < 1 || o.Port > 65535 {
		return fmt.Errorf("sensorwire: observation: port %d out of range 1-65535", o.Port)
	}
	if !o.Reachable && len(o.ChainDER) > 0 {
		return errors.New("sensorwire: observation: unreachable endpoint must not carry a chain")
	}
	if o.ObservedAt.IsZero() {
		return errors.New("sensorwire: observation: observed_at is required")
	}
	for i, der := range o.ChainDER {
		if der == "" {
			return fmt.Errorf("sensorwire: observation: chain_der[%d] is empty", i)
		}
		if _, err := base64.StdEncoding.DecodeString(der); err != nil {
			return fmt.Errorf("sensorwire: observation: chain_der[%d] is not valid base64", i)
		}
	}
	return nil
}

// ObservationBatch는 스캔 결과 배치 POST 본문이다.
// 서버는 (job_id, ip, port) 기준으로 멱등 처리한다 — 같은 배치 재전송은 중복되지 않는다.
type ObservationBatch struct {
	Observations []Observation `json:"observations"`
}

// Validate는 배치 크기와 각 관측의 형식을 검사한다.
func (b ObservationBatch) Validate() error {
	if len(b.Observations) == 0 {
		return errors.New("sensorwire: observation batch: at least one observation is required")
	}
	if len(b.Observations) > MaxObservationBatch {
		return fmt.Errorf("sensorwire: observation batch: exceeds max %d observations", MaxObservationBatch)
	}
	for i, o := range b.Observations {
		if err := o.Validate(); err != nil {
			return fmt.Errorf("sensorwire: observation batch: item %d: %w", i, err)
		}
	}
	return nil
}

// CompleteStatus는 완료 보고의 종결 상태다. 폐쇄 enum.
type CompleteStatus string

const (
	CompleteCompleted CompleteStatus = "completed"
	CompleteFailed    CompleteStatus = "failed"
)

// CompleteRequest는 running → completed/failed 전이 보고다.
type CompleteRequest struct {
	Status             CompleteStatus `json:"status"`
	Error              string         `json:"error,omitempty"` // failed일 때 사유. 인증서·키 내용 금지
	ScannedEndpoints   int            `json:"scanned_endpoints"`
	ReachableEndpoints int            `json:"reachable_endpoints"`
	CertificatesFound  int            `json:"certificates_found"`
}

// Validate는 종결 상태 enum과 카운터 형식을 검사한다.
func (r CompleteRequest) Validate() error {
	switch r.Status {
	case CompleteCompleted, CompleteFailed:
	default:
		return errors.New("sensorwire: complete: status must be 'completed' or 'failed'")
	}
	if r.Status == CompleteCompleted && r.Error != "" {
		return errors.New("sensorwire: complete: error must be empty when status is 'completed'")
	}
	if r.ScannedEndpoints < 0 || r.ReachableEndpoints < 0 || r.CertificatesFound < 0 {
		return errors.New("sensorwire: complete: counters must be >= 0")
	}
	return nil
}

// validateCSRPEM은 문자열이 CSR PEM 블록 정확히 1개인지 확인한다.
// 서명·내용 검증은 하지 않는다(서버의 sensorca 몫).
func validateCSRPEM(s string) error {
	if s == "" {
		return errors.New("sensorwire: csr_pem is required")
	}
	block, rest := pem.Decode([]byte(s))
	if block == nil {
		return errors.New("sensorwire: csr_pem is not valid PEM")
	}
	if block.Type != pemTypeCSR {
		return fmt.Errorf("sensorwire: csr_pem must be a %q block", pemTypeCSR)
	}
	if len(rest) > 0 {
		return errors.New("sensorwire: csr_pem must contain exactly one PEM block")
	}
	return nil
}
