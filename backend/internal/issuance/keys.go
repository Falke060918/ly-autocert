package issuance

import (
	"crypto"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"errors"
	"github.com/Falke060918/INHERIT_AutoCert/backend/internal/issuance/upstream"
	"github.com/Falke060918/INHERIT_AutoCert/backend/internal/sensorca"
)

type SealedKey struct {
	Ciphertext, Nonce []byte
	CSR               string
}

func GenerateKey(s upstream.Spec, sealKey []byte) (SealedKey, error) {
	var out SealedKey
	if e := s.Validate(); e != nil {
		return out, e
	}
	if len(sealKey) != 32 {
		return out, errors.New("ISSUANCE_ENC_KEY 설정이 필요합니다")
	}
	var key crypto.Signer
	var e error
	switch s.Algorithm {
	case "rsa2048":
		key, e = rsa.GenerateKey(rand.Reader, 2048)
	case "rsa3072":
		key, e = rsa.GenerateKey(rand.Reader, 3072)
	case "rsa4096":
		key, e = rsa.GenerateKey(rand.Reader, 4096)
	case "ecdsa_p256":
		key, e = ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	case "ecdsa_p384":
		key, e = ecdsa.GenerateKey(elliptic.P384(), rand.Reader)
	}
	if e != nil {
		return out, errors.New("개인키 생성 실패")
	}
	csr, e := x509.CreateCertificateRequest(rand.Reader, &x509.CertificateRequest{Subject: pkix.Name{CommonName: s.DNSNames[0]}, DNSNames: s.DNSNames}, key)
	if e != nil {
		return out, errors.New("CSR 생성 실패")
	}
	der, e := x509.MarshalPKCS8PrivateKey(key)
	if e != nil {
		return out, e
	}
	defer clear(der)
	out.Ciphertext, out.Nonce, e = sensorca.Seal(sealKey, der)
	out.CSR = string(pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE REQUEST", Bytes: csr}))
	return out, e
}
