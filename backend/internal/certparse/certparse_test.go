package certparse

// 픽스처는 전부 테스트 실행 시점에 생성한다 — 실제 키·인증서를 커밋하지 않는다
// (CLAUDE.md 보안 하드 규칙 4).

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/sha256"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/hex"
	"encoding/pem"
	"errors"
	"math/big"
	"net"
	"regexp"
	"testing"
	"time"

	"github.com/Falke060918/INHERIT_AutoCert/backend/internal/domain"
)

type fixture struct {
	cert   *x509.Certificate
	der    []byte
	keyDER []byte // PKCS#8
}

func newKey(t *testing.T) *ecdsa.PrivateKey {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatalf("generate key: %v", err)
	}
	return key
}

func serverTemplate() *x509.Certificate {
	return &x509.Certificate{
		SerialNumber: big.NewInt(4269),
		Subject:      pkix.Name{CommonName: "test-fixture.example.test"},
		NotBefore:    time.Now().Add(-time.Hour),
		NotAfter:     time.Now().Add(90 * 24 * time.Hour),
		KeyUsage:     x509.KeyUsageDigitalSignature,
		ExtKeyUsage:  []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
		DNSNames:     []string{"test-fixture.example.test", "alt.example.test"},
		IPAddresses:  []net.IP{net.ParseIP("192.0.2.10")},
	}
}

func caTemplate() *x509.Certificate {
	return &x509.Certificate{
		SerialNumber:          big.NewInt(1),
		Subject:               pkix.Name{CommonName: "test-fixture-ca"},
		NotBefore:             time.Now().Add(-time.Hour),
		NotAfter:              time.Now().Add(365 * 24 * time.Hour),
		KeyUsage:              x509.KeyUsageCertSign,
		IsCA:                  true,
		BasicConstraintsValid: true,
	}
}

func newSelfSigned(t *testing.T, tmpl *x509.Certificate) fixture {
	t.Helper()
	key := newKey(t)
	der, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &key.PublicKey, key)
	if err != nil {
		t.Fatalf("create certificate: %v", err)
	}
	cert, err := x509.ParseCertificate(der)
	if err != nil {
		t.Fatalf("parse created certificate: %v", err)
	}
	keyDER, err := x509.MarshalPKCS8PrivateKey(key)
	if err != nil {
		t.Fatalf("marshal key: %v", err)
	}
	return fixture{cert: cert, der: der, keyDER: keyDER}
}

// newChain은 CA + CA가 서명한 리프의 PEM 체인을 만든다 (리프가 먼저).
func newChain(t *testing.T) (leafDER, caDER []byte) {
	t.Helper()
	caKey := newKey(t)
	caTmpl := caTemplate()
	caDER, err := x509.CreateCertificate(rand.Reader, caTmpl, caTmpl, &caKey.PublicKey, caKey)
	if err != nil {
		t.Fatalf("create ca: %v", err)
	}
	caCert, err := x509.ParseCertificate(caDER)
	if err != nil {
		t.Fatalf("parse ca: %v", err)
	}
	leafKey := newKey(t)
	leafDER, err = x509.CreateCertificate(rand.Reader, serverTemplate(), caCert, &leafKey.PublicKey, caKey)
	if err != nil {
		t.Fatalf("create leaf: %v", err)
	}
	return leafDER, caDER
}

func pemCert(der []byte) []byte {
	return pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der})
}

func TestParsePEMSingle(t *testing.T) {
	fx := newSelfSigned(t, serverTemplate())
	certs, err := Parse(pemCert(fx.der))
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if len(certs) != 1 {
		t.Fatalf("got %d certs, want 1", len(certs))
	}
	if certs[0].Subject.CommonName != "test-fixture.example.test" {
		t.Errorf("unexpected CN %q", certs[0].Subject.CommonName)
	}
}

func TestParsePEMChainKeepsOrder(t *testing.T) {
	leafDER, caDER := newChain(t)
	data := append(pemCert(leafDER), pemCert(caDER)...)
	certs, err := Parse(data)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if len(certs) != 2 {
		t.Fatalf("got %d certs, want 2", len(certs))
	}
	if certs[0].IsCA || !certs[1].IsCA {
		t.Errorf("chain order not preserved: [0].IsCA=%v [1].IsCA=%v", certs[0].IsCA, certs[1].IsCA)
	}
}

func TestParseDER(t *testing.T) {
	fx := newSelfSigned(t, serverTemplate())
	certs, err := Parse(fx.der)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if len(certs) != 1 {
		t.Fatalf("got %d certs, want 1", len(certs))
	}
}

func TestRejectPrivateKeyPEM(t *testing.T) {
	fx := newSelfSigned(t, serverTemplate())
	keyPEM := pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: fx.keyDER})
	// 인증서 + 개인키가 함께 온 업로드도 통째로 거부해야 한다.
	data := append(pemCert(fx.der), keyPEM...)
	if _, err := Parse(data); !errors.Is(err, ErrPrivateKeyFound) {
		t.Fatalf("got %v, want ErrPrivateKeyFound", err)
	}
}

func TestRejectPrivateKeyDER(t *testing.T) {
	fx := newSelfSigned(t, serverTemplate())
	if _, err := Parse(fx.keyDER); !errors.Is(err, ErrPrivateKeyFound) {
		t.Fatalf("got %v, want ErrPrivateKeyFound", err)
	}
}

func TestRejectGarbage(t *testing.T) {
	if _, err := Parse([]byte("this is not a certificate")); err == nil {
		t.Fatal("garbage input parsed without error")
	}
	if _, err := Parse([]byte("-----BEGIN GREETING-----\naGVsbG8=\n-----END GREETING-----\n")); !errors.Is(err, ErrNoCertificate) {
		t.Fatal("PEM without certificate blocks must return ErrNoCertificate")
	}
}

func TestFingerprintFormat(t *testing.T) {
	fx := newSelfSigned(t, serverTemplate())
	fp := FingerprintSHA256(fx.cert)
	// DB CHECK(certificates_fingerprint_sha256_hex)와 동일한 형식이어야 한다.
	if !regexp.MustCompile(`^[0-9a-f]{64}$`).MatchString(fp) {
		t.Fatalf("fingerprint %q is not lowercase hex 64", fp)
	}
	want := sha256.Sum256(fx.der)
	if fp != hex.EncodeToString(want[:]) {
		t.Fatalf("fingerprint mismatch")
	}
}

func TestDescribeServerCert(t *testing.T) {
	fx := newSelfSigned(t, serverTemplate())
	d := Describe(fx.cert, time.Now())
	if d.CertificateType != domain.CertTypeServer {
		t.Errorf("type = %q, want server", d.CertificateType)
	}
	if d.Status != domain.CertStatusActive {
		t.Errorf("status = %q, want active", d.Status)
	}
	if d.CommonName != "test-fixture.example.test" {
		t.Errorf("common name = %q", d.CommonName)
	}
	if d.PublicKeyAlgorithm != "ECDSA" || d.PublicKeySize != 256 {
		t.Errorf("public key = %s/%d, want ECDSA/256", d.PublicKeyAlgorithm, d.PublicKeySize)
	}
	wantSANs := map[string]bool{"test-fixture.example.test": true, "alt.example.test": true, "192.0.2.10": true}
	if len(d.SANs) != len(wantSANs) {
		t.Fatalf("SANs = %v", d.SANs)
	}
	for _, san := range d.SANs {
		if !wantSANs[san] {
			t.Errorf("unexpected SAN %q", san)
		}
	}
}

func TestDescribeCAAndExpired(t *testing.T) {
	ca := newSelfSigned(t, caTemplate())
	if d := Describe(ca.cert, time.Now()); d.CertificateType != domain.CertTypeCA {
		t.Errorf("type = %q, want ca", d.CertificateType)
	}
	expiredTmpl := serverTemplate()
	expiredTmpl.NotBefore = time.Now().Add(-2 * time.Hour)
	expiredTmpl.NotAfter = time.Now().Add(-time.Hour)
	fx := newSelfSigned(t, expiredTmpl)
	if d := Describe(fx.cert, time.Now()); d.Status != domain.CertStatusExpired {
		t.Errorf("status = %q, want expired", d.Status)
	}
}
