package sensorca

// 픽스처는 전부 런타임 생성한다 — 실제 키·인증서 커밋 금지 (보안 하드 규칙 4).

import (
	"bytes"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"net"
	"testing"
	"time"
)

func newTestCA(t *testing.T) *CA {
	t.Helper()
	ca, err := New("AutoCert Sensor CA (test)")
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	return ca
}

func testCSR(t *testing.T, cn string) ([]byte, *ecdsa.PrivateKey) {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatalf("generate key: %v", err)
	}
	der, err := x509.CreateCertificateRequest(rand.Reader, &x509.CertificateRequest{
		Subject: pkix.Name{CommonName: cn},
	}, key)
	if err != nil {
		t.Fatalf("create csr: %v", err)
	}
	return pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE REQUEST", Bytes: der}), key
}

func parseCertPEM(t *testing.T, certPEM []byte) *x509.Certificate {
	t.Helper()
	block, _ := pem.Decode(certPEM)
	if block == nil || block.Type != "CERTIFICATE" {
		t.Fatal("not a certificate PEM block")
	}
	cert, err := x509.ParseCertificate(block.Bytes)
	if err != nil {
		t.Fatalf("parse certificate: %v", err)
	}
	return cert
}

func TestNewCA(t *testing.T) {
	ca := newTestCA(t)
	cert := parseCertPEM(t, ca.CertPEM())

	if !cert.IsCA {
		t.Error("CA cert must have IsCA")
	}
	if !cert.MaxPathLenZero {
		t.Error("CA cert must have MaxPathLenZero (no sub-CA)")
	}
	if cert.PublicKeyAlgorithm != x509.ECDSA {
		t.Errorf("public key algorithm = %v, want ECDSA", cert.PublicKeyAlgorithm)
	}
	if got := cert.PublicKey.(*ecdsa.PublicKey).Curve; got != elliptic.P256() {
		t.Errorf("curve = %v, want P-256", got.Params().Name)
	}
	wantExpiry := time.Now().Add(caValidity)
	if diff := cert.NotAfter.Sub(wantExpiry); diff < -time.Hour || diff > time.Hour {
		t.Errorf("NotAfter = %v, want ~10y from now", cert.NotAfter)
	}
	if err := cert.CheckSignatureFrom(cert); err != nil {
		t.Errorf("self-signature invalid: %v", err)
	}

	if _, err := New(""); err == nil {
		t.Error("empty common name: expected error, got nil")
	}
}

func TestLoadRoundTrip(t *testing.T) {
	ca := newTestCA(t)
	keyDER, err := ca.KeyDER()
	if err != nil {
		t.Fatalf("KeyDER: %v", err)
	}

	loaded, err := Load(ca.CertPEM(), keyDER)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	// 복원된 CA로 실제 서명이 되는지 확인한다.
	csrPEM, _ := testCSR(t, "sensor-roundtrip")
	if _, err := loaded.SignClientCSR(csrPEM); err != nil {
		t.Errorf("loaded CA cannot sign: %v", err)
	}

	// 인증서와 키가 짝이 아니면 거부한다.
	other := newTestCA(t)
	otherKey, err := other.KeyDER()
	if err != nil {
		t.Fatalf("KeyDER: %v", err)
	}
	if _, err := Load(ca.CertPEM(), otherKey); err == nil {
		t.Error("mismatched key: expected error, got nil")
	}
	if _, err := Load([]byte("junk"), keyDER); err == nil {
		t.Error("junk cert: expected error, got nil")
	}
	if _, err := Load(ca.CertPEM(), []byte("junk")); err == nil {
		t.Error("junk key: expected error, got nil")
	}
}

func TestSignClientCSR(t *testing.T) {
	ca := newTestCA(t)
	csrPEM, key := testCSR(t, "sensor-01")

	certPEM, err := ca.SignClientCSR(csrPEM)
	if err != nil {
		t.Fatalf("SignClientCSR: %v", err)
	}
	cert := parseCertPEM(t, certPEM)

	if cert.Subject.CommonName != "sensor-01" {
		t.Errorf("CN = %q, want sensor-01", cert.Subject.CommonName)
	}
	if cert.IsCA {
		t.Error("client cert must not be a CA")
	}
	if !cert.PublicKey.(*ecdsa.PublicKey).Equal(&key.PublicKey) {
		t.Error("client cert public key does not match CSR key")
	}
	if len(cert.ExtKeyUsage) != 1 || cert.ExtKeyUsage[0] != x509.ExtKeyUsageClientAuth {
		t.Errorf("ExtKeyUsage = %v, want [ClientAuth]", cert.ExtKeyUsage)
	}
	wantExpiry := time.Now().Add(clientCertValidity)
	if diff := cert.NotAfter.Sub(wantExpiry); diff < -time.Hour || diff > time.Hour {
		t.Errorf("NotAfter = %v, want ~90d from now", cert.NotAfter)
	}

	// CA 풀 기준으로 클라이언트 인증서 체인이 검증돼야 한다.
	pool := x509.NewCertPool()
	pool.AddCert(parseCertPEM(t, ca.CertPEM()))
	if _, err := cert.Verify(x509.VerifyOptions{
		Roots:     pool,
		KeyUsages: []x509.ExtKeyUsage{x509.ExtKeyUsageClientAuth},
	}); err != nil {
		t.Errorf("client cert does not verify against CA: %v", err)
	}
}

func TestSignClientCSRRejectsInvalid(t *testing.T) {
	ca := newTestCA(t)

	if _, err := ca.SignClientCSR([]byte("not pem")); err == nil {
		t.Error("non-PEM csr: expected error, got nil")
	}

	// 서명이 깨진 CSR은 거부한다 (본문 바이트 변조).
	csrPEM, _ := testCSR(t, "tampered")
	block, _ := pem.Decode(csrPEM)
	block.Bytes[len(block.Bytes)-1] ^= 0xff
	tampered := pem.EncodeToMemory(block)
	if _, err := ca.SignClientCSR(tampered); err == nil {
		t.Error("tampered csr: expected error, got nil")
	}
}

func TestIssueServerCert(t *testing.T) {
	ca := newTestCA(t)
	ip := net.ParseIP("10.1.2.3")

	certPEM, keyDER, err := ca.IssueServerCert("autocert-server", []string{"autocert.internal"}, []net.IP{ip})
	if err != nil {
		t.Fatalf("IssueServerCert: %v", err)
	}
	cert := parseCertPEM(t, certPEM)

	if len(cert.DNSNames) != 1 || cert.DNSNames[0] != "autocert.internal" {
		t.Errorf("DNSNames = %v, want [autocert.internal]", cert.DNSNames)
	}
	if len(cert.IPAddresses) != 1 || !cert.IPAddresses[0].Equal(ip) {
		t.Errorf("IPAddresses = %v, want [10.1.2.3]", cert.IPAddresses)
	}
	if len(cert.ExtKeyUsage) != 1 || cert.ExtKeyUsage[0] != x509.ExtKeyUsageServerAuth {
		t.Errorf("ExtKeyUsage = %v, want [ServerAuth]", cert.ExtKeyUsage)
	}

	// 반환된 키가 인증서와 짝인지 확인한다.
	parsedKey, err := x509.ParsePKCS8PrivateKey(keyDER)
	if err != nil {
		t.Fatalf("parse server key: %v", err)
	}
	if !parsedKey.(*ecdsa.PrivateKey).PublicKey.Equal(cert.PublicKey) {
		t.Error("server key does not match server cert")
	}

	pool := x509.NewCertPool()
	pool.AddCert(parseCertPEM(t, ca.CertPEM()))
	if _, err := cert.Verify(x509.VerifyOptions{
		Roots:     pool,
		DNSName:   "autocert.internal",
		KeyUsages: []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
	}); err != nil {
		t.Errorf("server cert does not verify against CA: %v", err)
	}

	if _, _, err := ca.IssueServerCert("cn", nil, nil); err == nil {
		t.Error("no SANs: expected error, got nil")
	}
	if _, _, err := ca.IssueServerCert("", []string{"h"}, nil); err == nil {
		t.Error("empty common name: expected error, got nil")
	}
}

func TestSealOpen(t *testing.T) {
	key := make([]byte, KeySize)
	if _, err := rand.Read(key); err != nil {
		t.Fatalf("generate key: %v", err)
	}
	plaintext := []byte("runtime-generated secret material")

	ciphertext, nonce, err := Seal(key, plaintext)
	if err != nil {
		t.Fatalf("Seal: %v", err)
	}
	if bytes.Contains(ciphertext, plaintext) {
		t.Error("ciphertext contains plaintext")
	}

	got, err := Open(key, ciphertext, nonce)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	if !bytes.Equal(got, plaintext) {
		t.Error("round trip mismatch")
	}

	// 잘못된 키·변조·nonce 길이는 전부 실패해야 한다.
	wrongKey := make([]byte, KeySize)
	if _, err := rand.Read(wrongKey); err != nil {
		t.Fatalf("generate key: %v", err)
	}
	if _, err := Open(wrongKey, ciphertext, nonce); err == nil {
		t.Error("wrong key: expected error, got nil")
	}
	tampered := bytes.Clone(ciphertext)
	tampered[0] ^= 0xff
	if _, err := Open(key, tampered, nonce); err == nil {
		t.Error("tampered ciphertext: expected error, got nil")
	}
	if _, err := Open(key, ciphertext, nonce[:len(nonce)-1]); err == nil {
		t.Error("short nonce: expected error, got nil")
	}
	if _, _, err := Seal(key[:16], plaintext); err == nil {
		t.Error("16-byte key: expected error, got nil (AES-256 requires 32)")
	}
	if _, err := Open(key[:16], ciphertext, nonce); err == nil {
		t.Error("16-byte key open: expected error, got nil")
	}
}

// TestCAKeyRoundTripThroughSeal은 저장 경로 전체를 모사한다:
// KeyDER → Seal(암호문 저장) → Open → Load 복원.
func TestCAKeyRoundTripThroughSeal(t *testing.T) {
	ca := newTestCA(t)
	keyDER, err := ca.KeyDER()
	if err != nil {
		t.Fatalf("KeyDER: %v", err)
	}
	sealKey := make([]byte, KeySize)
	if _, err := rand.Read(sealKey); err != nil {
		t.Fatalf("generate key: %v", err)
	}

	ciphertext, nonce, err := Seal(sealKey, keyDER)
	if err != nil {
		t.Fatalf("Seal: %v", err)
	}
	opened, err := Open(sealKey, ciphertext, nonce)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	if _, err := Load(ca.CertPEM(), opened); err != nil {
		t.Errorf("Load after seal round trip: %v", err)
	}
}
