package sensor

// 테스트 픽스처는 전부 런타임 생성이다 — 실제 키·인증서·토큰을 커밋하지 않는다 (보안 하드 규칙 4).
// 서버 게이트웨이는 미구현이므로 httptest 모의 서버가 sensorwire 계약을 대신 이행한다.

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/hex"
	"encoding/pem"
	"io"
	"log/slog"
	"math/big"
	"net"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

// discardLogger는 테스트 출력 오염을 막는다.
func discardLogger() *slog.Logger {
	return slog.New(slog.NewJSONHandler(io.Discard, nil))
}

// testCA는 테스트 전용 런타임 생성 CA다.
type testCA struct {
	cert    *x509.Certificate
	key     *ecdsa.PrivateKey
	certPEM []byte
}

func newTestCA(t *testing.T) *testCA {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatalf("generate ca key: %v", err)
	}
	tmpl := &x509.Certificate{
		SerialNumber:          big.NewInt(1),
		Subject:               pkix.Name{CommonName: "test-sensor-ca"},
		NotBefore:             time.Now().Add(-time.Hour),
		NotAfter:              time.Now().Add(24 * time.Hour),
		KeyUsage:              x509.KeyUsageCertSign | x509.KeyUsageDigitalSignature,
		BasicConstraintsValid: true,
		IsCA:                  true,
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &key.PublicKey, key)
	if err != nil {
		t.Fatalf("create ca cert: %v", err)
	}
	cert, err := x509.ParseCertificate(der)
	if err != nil {
		t.Fatalf("parse ca cert: %v", err)
	}
	return &testCA{
		cert:    cert,
		key:     key,
		certPEM: pem.EncodeToMemory(&pem.Block{Type: pemTypeCertificate, Bytes: der}),
	}
}

// issueClientCert는 주어진 공개키로 mTLS 클라이언트 인증서 PEM을 발급한다.
// 핸들러 고루틴에서도 쓰므로 t.Fatalf 대신 error를 반환한다.
func (ca *testCA) issueClientCert(pub *ecdsa.PublicKey, cn string, notAfter time.Time) ([]byte, error) {
	serial, err := rand.Int(rand.Reader, new(big.Int).Lsh(big.NewInt(1), 128))
	if err != nil {
		return nil, err
	}
	tmpl := &x509.Certificate{
		SerialNumber: serial,
		Subject:      pkix.Name{CommonName: cn},
		NotBefore:    time.Now().Add(-time.Hour),
		NotAfter:     notAfter,
		KeyUsage:     x509.KeyUsageDigitalSignature,
		ExtKeyUsage:  []x509.ExtKeyUsage{x509.ExtKeyUsageClientAuth},
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, ca.cert, pub, ca.key)
	if err != nil {
		return nil, err
	}
	return pem.EncodeToMemory(&pem.Block{Type: pemTypeCertificate, Bytes: der}), nil
}

// issueClientCertPEM은 issueClientCert의 테스트 본선용 래퍼다.
func (ca *testCA) issueClientCertPEM(t *testing.T, pub *ecdsa.PublicKey, cn string, notAfter time.Time) []byte {
	t.Helper()
	certPEM, err := ca.issueClientCert(pub, cn, notAfter)
	if err != nil {
		t.Fatalf("issue client cert: %v", err)
	}
	return certPEM
}

// issueServerCert는 SAN 127.0.0.1/::1/localhost의 서버 인증서(DER)와 키를 발급한다.
func (ca *testCA) issueServerCert(t *testing.T) ([]byte, *ecdsa.PrivateKey) {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatalf("generate server key: %v", err)
	}
	tmpl := &x509.Certificate{
		SerialNumber: newSerial(t),
		Subject:      pkix.Name{CommonName: "test-gateway"},
		NotBefore:    time.Now().Add(-time.Hour),
		NotAfter:     time.Now().Add(24 * time.Hour),
		KeyUsage:     x509.KeyUsageDigitalSignature,
		ExtKeyUsage:  []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
		IPAddresses:  []net.IP{net.ParseIP("127.0.0.1"), net.ParseIP("::1")},
		DNSNames:     []string{"localhost"},
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, ca.cert, &key.PublicKey, ca.key)
	if err != nil {
		t.Fatalf("issue server cert: %v", err)
	}
	return der, key
}

func newSerial(t *testing.T) *big.Int {
	t.Helper()
	serial, err := rand.Int(rand.Reader, new(big.Int).Lsh(big.NewInt(1), 128))
	if err != nil {
		t.Fatalf("generate serial: %v", err)
	}
	return serial
}

// newEnrolledIdentity는 등록 완료 상태(키·클라이언트 인증서·CA·ID 저장)를 만들어 복원한다.
func newEnrolledIdentity(t *testing.T, ca *testCA, dir string, notAfter time.Time) *Identity {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatalf("generate sensor key: %v", err)
	}
	certPEM := ca.issueClientCertPEM(t, &key.PublicKey, "test-sensor", notAfter)
	if err := SaveEnrollment(dir, "sensor-test-1", key, certPEM, ca.certPEM); err != nil {
		t.Fatalf("save enrollment: %v", err)
	}
	id, err := LoadIdentity(dir)
	if err != nil {
		t.Fatalf("load identity: %v", err)
	}
	return id
}

// newMTLSGateway는 클라이언트 인증서를 요구(RequireAndVerifyClientCert)하는 모의
// 게이트웨이를 띄운다. 센서의 mTLS 1경로 규약을 테스트에서 강제한다.
func newMTLSGateway(t *testing.T, ca *testCA, handler http.Handler) *httptest.Server {
	t.Helper()
	serverDER, serverKey := ca.issueServerCert(t)
	pool := x509.NewCertPool()
	pool.AddCert(ca.cert)
	ts := httptest.NewUnstartedServer(handler)
	ts.TLS = &tls.Config{
		MinVersion:   tls.VersionTLS12,
		Certificates: []tls.Certificate{{Certificate: [][]byte{serverDER}, PrivateKey: serverKey}},
		ClientCAs:    pool,
		ClientAuth:   tls.RequireAndVerifyClientCert,
	}
	ts.StartTLS()
	t.Cleanup(ts.Close)
	return ts
}

// newTLSTarget은 스캔 대상용 자가서명 TLS 리스너를 띄우고 (포트, 리프 DER)를 반환한다.
func newTLSTarget(t *testing.T) (int, []byte) {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatalf("generate target key: %v", err)
	}
	tmpl := &x509.Certificate{
		SerialNumber: newSerial(t),
		Subject:      pkix.Name{CommonName: "scan-target.test"},
		NotBefore:    time.Now().Add(-time.Hour),
		NotAfter:     time.Now().Add(24 * time.Hour),
		KeyUsage:     x509.KeyUsageDigitalSignature,
		ExtKeyUsage:  []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
		IPAddresses:  []net.IP{net.ParseIP("127.0.0.1")},
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &key.PublicKey, key)
	if err != nil {
		t.Fatalf("create target cert: %v", err)
	}
	ln, err := tls.Listen("tcp", "127.0.0.1:0", &tls.Config{
		MinVersion:   tls.VersionTLS12,
		Certificates: []tls.Certificate{{Certificate: [][]byte{der}, PrivateKey: key}},
	})
	if err != nil {
		t.Fatalf("listen target: %v", err)
	}
	t.Cleanup(func() { _ = ln.Close() })
	go func() {
		for {
			conn, err := ln.Accept()
			if err != nil {
				return
			}
			go func(c net.Conn) {
				_ = c.(*tls.Conn).Handshake()
				_ = c.Close()
			}(conn)
		}
	}()
	return ln.Addr().(*net.TCPAddr).Port, der
}

// newStallTarget은 accept만 하고 응답하지 않는 리스너다 — 타임아웃 경로용.
func newStallTarget(t *testing.T) int {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen stall target: %v", err)
	}
	t.Cleanup(func() { _ = ln.Close() })
	go func() {
		var conns []net.Conn
		defer func() {
			for _, c := range conns {
				_ = c.Close()
			}
		}()
		for {
			conn, err := ln.Accept()
			if err != nil {
				return
			}
			conns = append(conns, conn)
		}
	}()
	return ln.Addr().(*net.TCPAddr).Port
}

// randomToken은 테스트용 1회용 등록 토큰을 런타임 생성한다.
func randomToken(t *testing.T) string {
	t.Helper()
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		t.Fatalf("generate token: %v", err)
	}
	return "test-" + hex.EncodeToString(b[:])
}
