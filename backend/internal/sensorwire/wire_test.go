package sensorwire

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/base64"
	"encoding/pem"
	"strings"
	"testing"
	"time"
)

// testCSRPEM은 테스트 전용 CSR을 런타임에 생성한다 (민감값 하드코딩 금지 — 보안 하드 규칙 4).
func testCSRPEM(t *testing.T) string {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatalf("generate key: %v", err)
	}
	der, err := x509.CreateCertificateRequest(rand.Reader, &x509.CertificateRequest{
		Subject: pkix.Name{CommonName: "test-sensor"},
	}, key)
	if err != nil {
		t.Fatalf("create csr: %v", err)
	}
	return string(pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE REQUEST", Bytes: der}))
}

func TestEnrollRequestValidate(t *testing.T) {
	csr := testCSRPEM(t)

	valid := EnrollRequest{Token: "opaque-one-time-token", CSRPEM: csr}
	if err := valid.Validate(); err != nil {
		t.Errorf("valid request rejected: %v", err)
	}

	cases := []struct {
		name string
		req  EnrollRequest
	}{
		{"empty token", EnrollRequest{Token: "", CSRPEM: csr}},
		{"empty csr", EnrollRequest{Token: "tok", CSRPEM: ""}},
		{"not pem", EnrollRequest{Token: "tok", CSRPEM: "not a pem"}},
		{"wrong block type", EnrollRequest{Token: "tok", CSRPEM: strings.ReplaceAll(csr, "CERTIFICATE REQUEST", "CERTIFICATE")}},
		{"trailing data", EnrollRequest{Token: "tok", CSRPEM: csr + csr}},
	}
	for _, tc := range cases {
		if err := tc.req.Validate(); err == nil {
			t.Errorf("%s: expected error, got nil", tc.name)
		}
	}
}

func TestRenewRequestValidate(t *testing.T) {
	if err := (RenewRequest{CSRPEM: testCSRPEM(t)}).Validate(); err != nil {
		t.Errorf("valid request rejected: %v", err)
	}
	if err := (RenewRequest{}).Validate(); err == nil {
		t.Error("empty csr: expected error, got nil")
	}
}

func TestJobStartAndHeartbeatValidate(t *testing.T) {
	if err := (JobStartRequest{TotalEndpoints: 10}).Validate(); err != nil {
		t.Errorf("valid job start rejected: %v", err)
	}
	if err := (JobStartRequest{TotalEndpoints: -1}).Validate(); err == nil {
		t.Error("negative total_endpoints: expected error, got nil")
	}
	if err := (HeartbeatRequest{ScannedEndpoints: 3, ReachableEndpoints: 2, CertificatesFound: 1}).Validate(); err != nil {
		t.Errorf("valid heartbeat rejected: %v", err)
	}
	if err := (HeartbeatRequest{ScannedEndpoints: -1}).Validate(); err == nil {
		t.Error("negative counter: expected error, got nil")
	}
}

func TestObservationValidate(t *testing.T) {
	now := time.Now()
	chain := []string{base64.StdEncoding.EncodeToString([]byte{0x30, 0x03, 0x02, 0x01, 0x01})}

	valid := Observation{IP: "10.0.0.1", Port: 443, Reachable: true, TLSVersion: "TLS 1.3", ChainDER: chain, ObservedAt: now}
	if err := valid.Validate(); err != nil {
		t.Errorf("valid observation rejected: %v", err)
	}
	validV6 := Observation{IP: "fd00::1", Port: 8443, Reachable: true, ChainDER: chain, ObservedAt: now}
	if err := validV6.Validate(); err != nil {
		t.Errorf("valid ipv6 observation rejected: %v", err)
	}
	unreachable := Observation{IP: "10.0.0.2", Port: 443, Reachable: false, ObservedAt: now}
	if err := unreachable.Validate(); err != nil {
		t.Errorf("valid unreachable observation rejected: %v", err)
	}

	cases := []struct {
		name string
		obs  Observation
	}{
		{"bad ip", Observation{IP: "not-an-ip", Port: 443, Reachable: true, ChainDER: chain, ObservedAt: now}},
		{"cidr not allowed", Observation{IP: "10.0.0.0/24", Port: 443, Reachable: true, ChainDER: chain, ObservedAt: now}},
		{"port zero", Observation{IP: "10.0.0.1", Port: 0, Reachable: true, ChainDER: chain, ObservedAt: now}},
		{"port too high", Observation{IP: "10.0.0.1", Port: 70000, Reachable: true, ChainDER: chain, ObservedAt: now}},
		{"unreachable with chain", Observation{IP: "10.0.0.1", Port: 443, Reachable: false, ChainDER: chain, ObservedAt: now}},
		{"zero observed_at", Observation{IP: "10.0.0.1", Port: 443, Reachable: true, ChainDER: chain}},
		{"empty chain entry", Observation{IP: "10.0.0.1", Port: 443, Reachable: true, ChainDER: []string{""}, ObservedAt: now}},
		{"bad base64", Observation{IP: "10.0.0.1", Port: 443, Reachable: true, ChainDER: []string{"!!not base64!!"}, ObservedAt: now}},
	}
	for _, tc := range cases {
		if err := tc.obs.Validate(); err == nil {
			t.Errorf("%s: expected error, got nil", tc.name)
		}
	}
}

func TestObservationBatchValidate(t *testing.T) {
	now := time.Now()
	ok := Observation{IP: "10.0.0.1", Port: 443, Reachable: false, ObservedAt: now}

	if err := (ObservationBatch{Observations: []Observation{ok}}).Validate(); err != nil {
		t.Errorf("valid batch rejected: %v", err)
	}
	if err := (ObservationBatch{}).Validate(); err == nil {
		t.Error("empty batch: expected error, got nil")
	}

	big := ObservationBatch{Observations: make([]Observation, MaxObservationBatch+1)}
	for i := range big.Observations {
		big.Observations[i] = ok
	}
	if err := big.Validate(); err == nil {
		t.Error("oversized batch: expected error, got nil")
	}

	bad := ObservationBatch{Observations: []Observation{ok, {IP: "bad", Port: 443, ObservedAt: now}}}
	if err := bad.Validate(); err == nil {
		t.Error("batch with invalid item: expected error, got nil")
	}
}

func TestCompleteRequestValidate(t *testing.T) {
	if err := (CompleteRequest{Status: CompleteCompleted, ScannedEndpoints: 5}).Validate(); err != nil {
		t.Errorf("valid completed rejected: %v", err)
	}
	if err := (CompleteRequest{Status: CompleteFailed, Error: "lease expired"}).Validate(); err != nil {
		t.Errorf("valid failed rejected: %v", err)
	}

	cases := []struct {
		name string
		req  CompleteRequest
	}{
		{"unknown status", CompleteRequest{Status: "running"}},
		{"empty status", CompleteRequest{}},
		{"completed with error", CompleteRequest{Status: CompleteCompleted, Error: "boom"}},
		{"negative counter", CompleteRequest{Status: CompleteCompleted, CertificatesFound: -1}},
	}
	for _, tc := range cases {
		if err := tc.req.Validate(); err == nil {
			t.Errorf("%s: expected error, got nil", tc.name)
		}
	}
}
