// Package sensorca는 센서 mTLS용 내부 CA의 순수 crypto 연산을 제공한다 (docs/00-spec.md 7.8).
// 저장에 의존하지 않는다 — 영속화(sensor_ca 테이블)는 store 계층 몫이며,
// 개인키는 반드시 Seal로 봉인해 암호문만 저장한다 (보안 하드 규칙 1).
// 표준 라이브러리(crypto/x509·ecdsa·rand·aes+GCM)만 사용한다 — 자체 암호 구현 금지 (규칙 2).
// 키·인증서 바이트를 로그·에러 메시지에 넣지 않는다 (규칙 3).
package sensorca

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"errors"
	"fmt"
	"math/big"
	"net"
	"time"
)

const (
	// caValidity는 CA 인증서 유효기간이다. 온프레미스 내부 전용 CA이므로 길게 둔다.
	caValidity = 10 * 365 * 24 * time.Hour
	// clientCertValidity는 센서 클라이언트 인증서 유효기간이다. 만료 전 Renew로 갱신한다.
	clientCertValidity = 90 * 24 * time.Hour
	// serverCertValidity는 서버(mTLS 리스너) 인증서 유효기간이다.
	serverCertValidity = 2 * 365 * 24 * time.Hour
	// notBeforeSkew는 시계 오차 허용 여유다.
	notBeforeSkew = 5 * time.Minute
	// KeySize는 Seal/Open이 요구하는 AES-256 키 길이(바이트)다.
	KeySize = 32

	pemTypeCert = "CERTIFICATE"
	pemTypeCSR  = "CERTIFICATE REQUEST"
)

// 기동 실패 원인을 호출자가 문자열 대조 없이 구분할 수 있게 센티넬로 노출한다.
// cmd/server가 errors.Is로 갈라 어떤 환경변수를 고쳐야 하는지 지목한다 —
// 원인과 무관한 변수를 지목하면 운영자가 엉뚱한 곳을 보게 된다.
// 두 메시지 모두 키·인증서 바이트를 담지 않는다 (보안 하드 규칙 3).
var (
	// ErrNoSAN은 서버 인증서에 넣을 SAN이 하나도 없을 때다 (SENSOR_ADVERTISED_HOSTS 미설정).
	ErrNoSAN = errors.New("sensorca: at least one SAN (dns name or ip) is required")
	// ErrOpen은 봉인된 값을 풀지 못했을 때다 (봉인 키 불일치 또는 암호문 변조).
	ErrOpen = errors.New("sensorca: open failed (wrong key or tampered ciphertext)")
)

// CA는 메모리에 로드된 센서 CA다. 개인키를 담으므로 로그·직렬화에 노출하지 않는다.
type CA struct {
	cert *x509.Certificate
	key  *ecdsa.PrivateKey
}

// New는 자체 서명 CA를 새로 만든다. ECDSA P-256, 유효기간 10년.
func New(commonName string) (*CA, error) {
	if commonName == "" {
		return nil, errors.New("sensorca: common name is required")
	}
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return nil, fmt.Errorf("sensorca: generate ca key: %w", err)
	}
	serial, err := newSerial()
	if err != nil {
		return nil, err
	}
	now := time.Now()
	tmpl := &x509.Certificate{
		SerialNumber:          serial,
		Subject:               pkix.Name{CommonName: commonName},
		NotBefore:             now.Add(-notBeforeSkew),
		NotAfter:              now.Add(caValidity),
		IsCA:                  true,
		BasicConstraintsValid: true,
		MaxPathLenZero:        true, // 하위 CA 발급 금지 — 리프 전용
		KeyUsage:              x509.KeyUsageCertSign | x509.KeyUsageCRLSign,
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &key.PublicKey, key)
	if err != nil {
		return nil, fmt.Errorf("sensorca: create ca certificate: %w", err)
	}
	cert, err := x509.ParseCertificate(der)
	if err != nil {
		return nil, fmt.Errorf("sensorca: parse ca certificate: %w", err)
	}
	return &CA{cert: cert, key: key}, nil
}

// Load는 저장돼 있던 CA 인증서 PEM과 개인키 DER(PKCS#8, Open으로 개봉된 평문)로 CA를 복원한다.
func Load(certPEM, keyDER []byte) (*CA, error) {
	block, _ := pem.Decode(certPEM)
	if block == nil || block.Type != pemTypeCert {
		return nil, errors.New("sensorca: ca cert is not a certificate PEM block")
	}
	cert, err := x509.ParseCertificate(block.Bytes)
	if err != nil {
		return nil, errors.New("sensorca: parse ca certificate failed")
	}
	if !cert.IsCA {
		return nil, errors.New("sensorca: certificate is not a CA")
	}
	parsed, err := x509.ParsePKCS8PrivateKey(keyDER)
	if err != nil {
		return nil, errors.New("sensorca: parse ca key failed")
	}
	key, ok := parsed.(*ecdsa.PrivateKey)
	if !ok {
		return nil, errors.New("sensorca: ca key is not an ECDSA key")
	}
	if !key.PublicKey.Equal(cert.PublicKey) {
		return nil, errors.New("sensorca: ca key does not match ca certificate")
	}
	return &CA{cert: cert, key: key}, nil
}

// CertPEM은 CA 인증서를 PEM으로 반환한다. 공개 정보이므로 그대로 저장·배포 가능하다.
func (c *CA) CertPEM() []byte {
	return pem.EncodeToMemory(&pem.Block{Type: pemTypeCert, Bytes: c.cert.Raw})
}

// KeyDER는 CA 개인키를 PKCS#8 DER 평문으로 반환한다.
// 저장 전 반드시 Seal로 봉인해야 한다 — 평문 저장 금지 (보안 하드 규칙 1).
func (c *CA) KeyDER() ([]byte, error) {
	der, err := x509.MarshalPKCS8PrivateKey(c.key)
	if err != nil {
		return nil, errors.New("sensorca: marshal ca key failed")
	}
	return der, nil
}

// SignClientCSR은 센서가 제출한 CSR로 클라이언트 인증서를 발급한다. 유효기간 90일.
// Subject는 CSR의 것을 쓰되 SAN 등 확장은 받지 않는다(클라이언트 인증 전용).
func (c *CA) SignClientCSR(csrPEM []byte) ([]byte, error) {
	block, _ := pem.Decode(csrPEM)
	if block == nil || block.Type != pemTypeCSR {
		return nil, errors.New("sensorca: csr is not a certificate request PEM block")
	}
	csr, err := x509.ParseCertificateRequest(block.Bytes)
	if err != nil {
		return nil, errors.New("sensorca: parse csr failed")
	}
	if err := csr.CheckSignature(); err != nil {
		return nil, errors.New("sensorca: csr signature check failed")
	}
	serial, err := newSerial()
	if err != nil {
		return nil, err
	}
	now := time.Now()
	tmpl := &x509.Certificate{
		SerialNumber:          serial,
		Subject:               csr.Subject,
		NotBefore:             now.Add(-notBeforeSkew),
		NotAfter:              now.Add(clientCertValidity),
		BasicConstraintsValid: true,
		KeyUsage:              x509.KeyUsageDigitalSignature,
		ExtKeyUsage:           []x509.ExtKeyUsage{x509.ExtKeyUsageClientAuth},
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, c.cert, csr.PublicKey, c.key)
	if err != nil {
		return nil, errors.New("sensorca: create client certificate failed")
	}
	return pem.EncodeToMemory(&pem.Block{Type: pemTypeCert, Bytes: der}), nil
}

// IssueServerCert는 서버(mTLS 리스너)용 인증서와 새 개인키를 발급한다.
// SAN(DNS 이름·IP)은 인자로 받는다. 반환 키는 PKCS#8 DER 평문 —
// 호출자가 즉시 사용하거나 Seal로 봉인해 보관한다.
func (c *CA) IssueServerCert(commonName string, dnsNames []string, ips []net.IP) (certPEM, keyDER []byte, err error) {
	if commonName == "" {
		return nil, nil, errors.New("sensorca: common name is required")
	}
	if len(dnsNames) == 0 && len(ips) == 0 {
		return nil, nil, ErrNoSAN
	}
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return nil, nil, fmt.Errorf("sensorca: generate server key: %w", err)
	}
	serial, err := newSerial()
	if err != nil {
		return nil, nil, err
	}
	now := time.Now()
	tmpl := &x509.Certificate{
		SerialNumber:          serial,
		Subject:               pkix.Name{CommonName: commonName},
		NotBefore:             now.Add(-notBeforeSkew),
		NotAfter:              now.Add(serverCertValidity),
		BasicConstraintsValid: true,
		KeyUsage:              x509.KeyUsageDigitalSignature,
		ExtKeyUsage:           []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
		DNSNames:              dnsNames,
		IPAddresses:           ips,
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, c.cert, &key.PublicKey, c.key)
	if err != nil {
		return nil, nil, errors.New("sensorca: create server certificate failed")
	}
	keyDER, err = x509.MarshalPKCS8PrivateKey(key)
	if err != nil {
		return nil, nil, errors.New("sensorca: marshal server key failed")
	}
	return pem.EncodeToMemory(&pem.Block{Type: pemTypeCert, Bytes: der}), keyDER, nil
}

// Seal은 평문을 AES-256-GCM으로 봉인한다. key는 정확히 32바이트.
// 반환값은 (암호문, nonce) — sensor_ca.ca_key_ciphertext / ca_key_nonce에 대응한다.
func Seal(key, plaintext []byte) (ciphertext, nonce []byte, err error) {
	gcm, err := newGCM(key)
	if err != nil {
		return nil, nil, err
	}
	nonce = make([]byte, gcm.NonceSize())
	if _, err := rand.Read(nonce); err != nil {
		return nil, nil, fmt.Errorf("sensorca: generate nonce: %w", err)
	}
	return gcm.Seal(nil, nonce, plaintext, nil), nonce, nil
}

// Open은 Seal의 역연산이다. 키가 다르거나 암호문이 변조됐으면 실패한다.
func Open(key, ciphertext, nonce []byte) ([]byte, error) {
	gcm, err := newGCM(key)
	if err != nil {
		return nil, err
	}
	if len(nonce) != gcm.NonceSize() {
		return nil, errors.New("sensorca: invalid nonce size")
	}
	plaintext, err := gcm.Open(nil, nonce, ciphertext, nil)
	if err != nil {
		return nil, ErrOpen
	}
	return plaintext, nil
}

func newGCM(key []byte) (cipher.AEAD, error) {
	if len(key) != KeySize {
		return nil, fmt.Errorf("sensorca: key must be exactly %d bytes", KeySize)
	}
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, errors.New("sensorca: init cipher failed")
	}
	return cipher.NewGCM(block)
}

// newSerial은 128비트 난수 시리얼을 만든다 (crypto/rand — 자체 난수 구현 금지).
func newSerial() (*big.Int, error) {
	limit := new(big.Int).Lsh(big.NewInt(1), 128)
	serial, err := rand.Int(rand.Reader, limit)
	if err != nil {
		return nil, fmt.Errorf("sensorca: generate serial: %w", err)
	}
	return serial, nil
}
