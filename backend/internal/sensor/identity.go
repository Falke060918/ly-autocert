package sensor

import (
	"crypto/ecdsa"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"errors"
	"sync/atomic"
)

const pemTypeCertificate = "CERTIFICATE"
const pemTypeCSR = "CERTIFICATE REQUEST"

// Identity는 등록된 센서의 mTLS 신원이다. 개인키를 담으므로 로그·직렬화에 노출하지 않는다.
// 클라이언트 인증서는 renew로 무중단 교체되므로 atomic 포인터로 든다.
type Identity struct {
	SensorID string

	key     *ecdsa.PrivateKey
	caPool  *x509.CertPool
	tlsCert atomic.Pointer[tls.Certificate]
	leaf    atomic.Pointer[x509.Certificate]
}

// Leaf는 현재 클라이언트 인증서(파싱본)를 반환한다. 만료 검사에 쓴다.
func (id *Identity) Leaf() *x509.Certificate { return id.leaf.Load() }

// TLSConfig는 서버 게이트웨이용 mTLS 클라이언트 설정을 만든다.
// GetClientCertificate 콜백으로 renew 후 새 인증서가 재핸드셰이크에 즉시 반영된다.
func (id *Identity) TLSConfig() *tls.Config {
	return &tls.Config{
		MinVersion: tls.VersionTLS12,
		RootCAs:    id.caPool,
		GetClientCertificate: func(*tls.CertificateRequestInfo) (*tls.Certificate, error) {
			return id.tlsCert.Load(), nil
		},
	}
}

// NewCSRPEM은 기존 개인키로 갱신용 새 CSR을 만든다. Subject는 현재 인증서를 따른다.
func (id *Identity) NewCSRPEM() ([]byte, error) {
	return newCSRPEM(id.key, id.Leaf().Subject.CommonName)
}

// InstallClientCert는 갱신된 인증서 PEM을 검증(키 일치)하고 메모리 신원에 반영한다.
// 파일 교체(ReplaceClientCert)는 호출자가 먼저 한다 — 파일이 진실이고 메모리는 캐시다.
func (id *Identity) InstallClientCert(certPEM []byte) error {
	leaf, err := clientCertForKey(certPEM, id.key)
	if err != nil {
		return err
	}
	id.install(leaf)
	return nil
}

// install은 파싱·검증이 끝난 리프를 tls.Certificate와 함께 스왑한다.
func (id *Identity) install(leaf *x509.Certificate) {
	pair := &tls.Certificate{Certificate: [][]byte{leaf.Raw}, PrivateKey: id.key, Leaf: leaf}
	id.tlsCert.Store(pair)
	id.leaf.Store(leaf)
}

// newCSRPEM은 ECDSA 키로 CSR PEM을 만든다. 확장 없이 Subject CN만 담는다.
func newCSRPEM(key *ecdsa.PrivateKey, commonName string) ([]byte, error) {
	if commonName == "" {
		commonName = "autocert-sensor"
	}
	tmpl := &x509.CertificateRequest{Subject: pkix.Name{CommonName: commonName}}
	der, err := x509.CreateCertificateRequest(rand.Reader, tmpl, key)
	if err != nil {
		return nil, errors.New("sensor: create csr failed")
	}
	return pem.EncodeToMemory(&pem.Block{Type: pemTypeCSR, Bytes: der}), nil
}

// clientCertForKey는 인증서 PEM을 파싱하고 공개키가 로컬 개인키와 일치하는지 확인한다.
// 에러 메시지에 인증서 내용은 넣지 않는다 (보안 하드 규칙 3).
func clientCertForKey(certPEM []byte, key *ecdsa.PrivateKey) (*x509.Certificate, error) {
	block, _ := pem.Decode(certPEM)
	if block == nil || block.Type != pemTypeCertificate {
		return nil, errors.New("sensor: client certificate is not a certificate PEM block")
	}
	leaf, err := x509.ParseCertificate(block.Bytes)
	if err != nil {
		return nil, errors.New("sensor: parse client certificate failed")
	}
	pub, ok := leaf.PublicKey.(*ecdsa.PublicKey)
	if !ok || !pub.Equal(&key.PublicKey) {
		return nil, errors.New("sensor: client certificate does not match the local private key")
	}
	return leaf, nil
}
