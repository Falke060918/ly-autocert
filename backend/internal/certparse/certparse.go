// Package certparse는 업로드된 인증서 바이트를 파싱해 도메인 메타데이터로 변환한다.
// 파싱은 표준 라이브러리 crypto/x509만 사용한다 (자체 암호 구현 금지, 보안 하드 규칙 2).
// 오류 메시지·로그에 입력 내용(인증서·키 바이트)을 포함하지 않는다 (보안 하드 규칙 3).
package certparse

import (
	"bytes"
	"crypto/ecdsa"
	"crypto/ed25519"
	"crypto/rsa"
	"crypto/sha256"
	"crypto/x509"
	"encoding/hex"
	"encoding/pem"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/Falke060918/INHERIT_AutoCert/backend/internal/domain"
)

var (
	// ErrPrivateKeyFound는 입력에 개인키가 섞여 있을 때 반환한다.
	// 개인키는 저장·로그 없이 업로드 전체를 거부한다 (보안 하드 규칙 1·3).
	ErrPrivateKeyFound = errors.New("certparse: input contains private key material")
	// ErrNoCertificate는 입력에서 인증서를 하나도 찾지 못했을 때 반환한다.
	ErrNoCertificate = errors.New("certparse: no certificate found in input")
)

// decoder는 포맷 1개의 판정·파싱 쌍이다.
// CRT/CER은 내용이 PEM 또는 DER이므로 아래 둘로 커버된다.
// P7B(PKCS#7)·PFX(PKCS#12) 지원 시 이 목록에 디코더를 추가한다
// (docs/01-plan.md MVP 3 — 확장 가능한 구조로 설계).
type decoder struct {
	name   string
	detect func([]byte) bool
	parse  func([]byte) ([]*x509.Certificate, error)
}

var decoders = []decoder{
	{name: "pem", detect: isPEM, parse: parsePEM},
	{name: "der", detect: func([]byte) bool { return true }, parse: parseDER},
}

// Parse는 PEM(체인 포함) 또는 DER 바이트에서 인증서 목록을 파일 내 순서대로 추출한다.
func Parse(data []byte) ([]*x509.Certificate, error) {
	for _, d := range decoders {
		if d.detect(data) {
			return d.parse(data)
		}
	}
	return nil, ErrNoCertificate
}

func isPEM(data []byte) bool {
	return bytes.Contains(data, []byte("-----BEGIN "))
}

// parsePEM은 모든 CERTIFICATE 블록을 파싱한다. PRIVATE KEY 계열 블록이 하나라도
// 있으면 전체를 거부한다. 그 외 블록 타입(CSR 등)은 무시한다.
func parsePEM(data []byte) ([]*x509.Certificate, error) {
	var certs []*x509.Certificate
	rest := data
	for {
		var block *pem.Block
		block, rest = pem.Decode(rest)
		if block == nil {
			break
		}
		if strings.Contains(block.Type, "PRIVATE KEY") {
			return nil, ErrPrivateKeyFound
		}
		if block.Type != "CERTIFICATE" {
			continue
		}
		cert, err := x509.ParseCertificate(block.Bytes)
		if err != nil {
			// x509 오류 문자열은 입력 바이트를 포함하지 않으므로 래핑해도 안전하다.
			return nil, fmt.Errorf("certparse: invalid certificate in PEM block %d: %w", len(certs)+1, err)
		}
		certs = append(certs, cert)
	}
	if len(certs) == 0 {
		return nil, ErrNoCertificate
	}
	return certs, nil
}

// parseDER은 DER 인증서(연접 포함)를 파싱한다. DER로 인코딩된 개인키는
// 인증서 파싱 전에 판별해 거부한다.
func parseDER(data []byte) ([]*x509.Certificate, error) {
	if isDERPrivateKey(data) {
		return nil, ErrPrivateKeyFound
	}
	certs, err := x509.ParseCertificates(data)
	if err != nil {
		return nil, fmt.Errorf("certparse: invalid DER: %w", err)
	}
	if len(certs) == 0 {
		return nil, ErrNoCertificate
	}
	return certs, nil
}

func isDERPrivateKey(data []byte) bool {
	if _, err := x509.ParsePKCS8PrivateKey(data); err == nil {
		return true
	}
	if _, err := x509.ParsePKCS1PrivateKey(data); err == nil {
		return true
	}
	if _, err := x509.ParseECPrivateKey(data); err == nil {
		return true
	}
	return false
}

// FingerprintSHA256은 DER 바이트의 SHA-256 다이제스트를 소문자 hex 64자로 반환한다.
// DB의 certificates_fingerprint_sha256_hex CHECK와 형식이 일치한다 (중복 판정 키).
func FingerprintSHA256(cert *x509.Certificate) string {
	sum := sha256.Sum256(cert.Raw)
	return hex.EncodeToString(sum[:])
}

// Describe는 파싱된 인증서를 도메인 메타데이터로 변환한다.
// ID·CreatedAt·UpdatedAt은 저장 시점에 DB가 채운다. 인증서 본문은 담지 않는다.
func Describe(cert *x509.Certificate, now time.Time) domain.Certificate {
	return domain.Certificate{
		FingerprintSHA256:  FingerprintSHA256(cert),
		SerialNumber:       strings.ToLower(cert.SerialNumber.Text(16)),
		SubjectDN:          cert.Subject.String(),
		IssuerDN:           cert.Issuer.String(),
		CommonName:         cert.Subject.CommonName,
		SANs:               collectSANs(cert),
		NotBefore:          cert.NotBefore,
		NotAfter:           cert.NotAfter,
		PublicKeyAlgorithm: cert.PublicKeyAlgorithm.String(),
		PublicKeySize:      publicKeySize(cert),
		SignatureAlgorithm: cert.SignatureAlgorithm.String(),
		SubjectKeyID:       keyIDHex(cert.SubjectKeyId),
		AuthorityKeyID:     keyIDHex(cert.AuthorityKeyId),
		CertificateType:    classify(cert),
		Status:             lifecycleStatus(cert, now),
	}
}

// keyIDHex는 SKI/AKI 확장 바이트를 소문자 hex로 변환한다. 확장이 없으면 nil.
// 체인 관계 매칭(리프의 AKI = 상위의 SKI)에 쓴다 — DB CHECK(0008)와 형식이 일치한다.
func keyIDHex(b []byte) *string {
	if len(b) == 0 {
		return nil
	}
	s := hex.EncodeToString(b)
	return &s
}

func collectSANs(cert *x509.Certificate) []string {
	sans := make([]string, 0, len(cert.DNSNames)+len(cert.IPAddresses)+len(cert.EmailAddresses)+len(cert.URIs))
	sans = append(sans, cert.DNSNames...)
	for _, ip := range cert.IPAddresses {
		sans = append(sans, ip.String())
	}
	sans = append(sans, cert.EmailAddresses...)
	for _, u := range cert.URIs {
		sans = append(sans, u.String())
	}
	return sans
}

// publicKeySize는 공개키 길이(bit)를 반환한다. 알 수 없는 알고리즘이면 0.
func publicKeySize(cert *x509.Certificate) int {
	switch key := cert.PublicKey.(type) {
	case *rsa.PublicKey:
		return key.N.BitLen()
	case *ecdsa.PublicKey:
		return key.Curve.Params().BitSize
	case ed25519.PublicKey:
		return 8 * ed25519.PublicKeySize
	default:
		return 0
	}
}

// classify는 저장용 인증서 용도 분류를 정한다. CA 여부가 최우선이다.
func classify(cert *x509.Certificate) domain.CertificateType {
	if cert.IsCA {
		return domain.CertTypeCA
	}
	for _, eku := range cert.ExtKeyUsage {
		switch eku {
		case x509.ExtKeyUsageServerAuth:
			return domain.CertTypeServer
		case x509.ExtKeyUsageClientAuth:
			return domain.CertTypeClient
		case x509.ExtKeyUsageCodeSigning:
			return domain.CertTypeCodeSigning
		case x509.ExtKeyUsageEmailProtection:
			return domain.CertTypeEmail
		}
	}
	return domain.CertTypeOther
}

// lifecycleStatus는 저장하는 라이프사이클 상태만 정한다.
// '만료 임박' 같은 파생 상태는 조회 시 NotAfter에서 계산한다 (domain 참조).
func lifecycleStatus(cert *x509.Certificate, now time.Time) domain.CertificateStatus {
	if now.After(cert.NotAfter) {
		return domain.CertStatusExpired
	}
	return domain.CertStatusActive
}
