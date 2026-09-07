// Package agentwire contains only the closed deployment protocol. Channel
// authentication uses mTLS and a closed action enum; no arbitrary commands cross it.
package agentwire

import (
	"encoding/hex"
	"errors"
	"net"
	"path"
	"strings"
	"time"
)

type EnrollRequest struct {
	Token  string `json:"token"`
	CSRPEM string `json:"csr_pem"`
}

func (r EnrollRequest) Validate() error {
	if len(r.Token) != 64 || len(r.CSRPEM) == 0 || len(r.CSRPEM) > 16384 {
		return errors.New("등록 요청 형식 오류")
	}
	return nil
}

type EnrollResponse struct {
	AgentID       string `json:"agent_id"`
	ClientCertPEM string `json:"client_cert_pem"`
	CACertPEM     string `json:"ca_cert_pem"`
}
type Target struct {
	ID                   string  `json:"id,omitempty"`
	AgentID              string  `json:"agent_id"`
	Kind                 string  `json:"kind"`
	ServiceUnit          string  `json:"service_unit"`
	CertPath             string  `json:"cert_path"`
	KeyPath              string  `json:"key_path"`
	ChainPath            string  `json:"chain_path"`
	VerifyHost           string  `json:"verify_host"`
	VerifyPort           int     `json:"verify_port"`
	SNIName              string  `json:"sni_name"`
	CurrentCertificateID *string `json:"current_certificate_id,omitempty"`
	CurrentFingerprint   string  `json:"current_fingerprint,omitempty"`
	Active               bool    `json:"active"`
}

func (t Target) Validate() error {
	if !((t.Kind == "nginx" && t.ServiceUnit == "nginx") || (t.Kind == "apache" && (t.ServiceUnit == "httpd" || t.ServiceUnit == "apache2"))) {
		return errors.New("nginx 또는 Apache 서비스만 배포할 수 있습니다")
	}
	seen := map[string]bool{}
	for _, p := range []string{t.CertPath, t.KeyPath, t.ChainPath} {
		if p == "" {
			continue
		}
		if !path.IsAbs(p) || path.Clean(p) != p || strings.ContainsAny(p, "\x00\r\n") || seen[p] {
			return errors.New("서로 다른 정규 절대 파일 경로가 필요합니다")
		}
		seen[p] = true
	}
	if t.CertPath == "" || t.KeyPath == "" {
		return errors.New("인증서·개인키 경로가 필요합니다")
	}
	ip := net.ParseIP(t.VerifyHost)
	if ip == nil || !ip.IsLoopback() || t.VerifyPort < 1 || t.VerifyPort > 65535 {
		return errors.New("검증 주소는 로컬 루프백 IP와 유효한 포트여야 합니다")
	}
	if len(t.SNIName) > 253 || strings.ContainsAny(t.SNIName, "/\r\n\x00 ") {
		return errors.New("SNI 이름 형식 오류")
	}
	return nil
}

type Lease struct {
	JobID               string    `json:"job_id"`
	Kind                string    `json:"kind"`
	Target              Target    `json:"target"`
	ExpectedFingerprint string    `json:"expected_fingerprint"`
	LeaseToken          string    `json:"lease_token"`
	ExpiresAt           time.Time `json:"lease_expires_at"`
}

// SignedLease carries exact signed bytes. The trusted public key is supplied
// by the administrator separately, never taken from a gateway response.
type SignedLease struct {
	Payload   []byte `json:"payload"`
	Signature []byte `json:"signature"`
}

func Fingerprint(s string) bool {
	b, e := hex.DecodeString(s)
	return e == nil && len(b) == 32 && s == strings.ToLower(s)
}

type Material struct {
	CertPEM  string `json:"cert_pem"`
	ChainPEM string `json:"chain_pem"`
	KeyPEM   string `json:"key_pem"`
}
type Step struct {
	At      time.Time `json:"at"`
	Name    string    `json:"name"`
	OK      bool      `json:"ok"`
	Message string    `json:"message"`
}
type Result struct {
	Status              string `json:"status"`
	Steps               []Step `json:"steps"`
	Error               string `json:"error"`
	ObservedFingerprint string `json:"observed_fingerprint"`
	LeaseToken          string `json:"lease_token"`
}

func (r Result) Validate() error {
	if r.Status != "completed" && r.Status != "failed" && r.Status != "rolled_back" {
		return errors.New("완료 상태 오류")
	}
	if len(r.Steps) > 32 || len(r.Error) > 300 {
		return errors.New("결과 크기 오류")
	}
	for _, s := range r.Steps {
		switch s.Name {
		case "precheck", "backup", "write_temp", "swap", "config_test", "reload", "verify", "cleanup", "rollback":
		default:
			return errors.New("단계 이름 오류")
		}
		if len(s.Message) > 200 {
			return errors.New("단계 결과 크기 오류")
		}
	}
	if r.Status != "failed" && !Fingerprint(r.ObservedFingerprint) {
		return errors.New("검증 지문이 필요합니다")
	}
	return nil
}
