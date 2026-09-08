package issuance

import (
	"bytes"
	"crypto/x509"
	"encoding/asn1"
	"encoding/pem"
	"errors"

	"github.com/Falke060918/INHERIT_AutoCert/backend/internal/certparse"
)

// ChainWarning describes the received chain after the leaf has been parsed.
// Security errors are ordinary errors and must never be downgraded to warnings.
type ChainWarning struct {
	Code    string
	message string
}

func (w *ChainWarning) Error() string { return w.message }

func validateCAChain(leaf *x509.Certificate, raw string) error {
	format := &ChainWarning{"ca_chain_parse_failed", "CA가 제공한 인증서 체인의 형식 검증 실패. 원본을 보존했습니다. CA에 확인하세요."}
	if raw == "" {
		return &ChainWarning{"ca_chain_missing", "CA가 인증서 체인을 제공하지 않았습니다. CA에 확인하세요."}
	}
	data, err := certificateData(raw)
	if bytes.Contains([]byte(raw), []byte("PRIVATE KEY")) || bytes.Contains(data, []byte("PRIVATE KEY")) {
		return errors.New("CA 응답에 개인키가 포함되어 등록을 차단했습니다")
	}
	if err != nil {
		return format
	}
	// DER bundles can contain concatenated objects. Check each object before a
	// parse failure is downgraded; a trailing private key must stay fatal too.
	if !bytes.Contains(data, []byte("-----BEGIN")) {
		for rest := data; len(rest) > 0; {
			var object asn1.RawValue
			tail, parseErr := asn1.Unmarshal(rest, &object)
			if parseErr != nil {
				break
			}
			if _, parseErr = certparse.Parse(object.FullBytes); errors.Is(parseErr, certparse.ErrPrivateKeyFound) {
				return errors.New("CA 응답에 개인키가 포함되어 등록을 차단했습니다")
			}
			rest = tail
		}
	}
	certs, err := certparse.Parse(data)
	if errors.Is(err, certparse.ErrPrivateKeyFound) {
		return errors.New("CA 응답에 개인키가 포함되어 등록을 차단했습니다")
	}
	if err != nil {
		return format
	}
	// certparse intentionally tolerates unrelated PEM blocks and trailing text.
	// A CA chain must account for the entire input to claim validation success.
	if bytes.Contains(data, []byte("-----BEGIN")) {
		rest := bytes.TrimSpace(data)
		for len(rest) > 0 {
			if !bytes.HasPrefix(rest, []byte("-----BEGIN CERTIFICATE-----")) {
				return format
			}
			block, tail := pem.Decode(rest)
			if block == nil || block.Type != "CERTIFICATE" {
				return format
			}
			rest = bytes.TrimSpace(tail)
		}
	}
	prev := leaf
	for _, ca := range certs {
		if !ca.IsCA || !bytes.Equal(prev.RawIssuer, ca.RawSubject) ||
			(len(prev.AuthorityKeyId) > 0 && len(ca.SubjectKeyId) > 0 && !bytes.Equal(prev.AuthorityKeyId, ca.SubjectKeyId)) ||
			prev.CheckSignatureFrom(ca) != nil {
			return &ChainWarning{"ca_chain_signature_failed", "CA가 제공한 인증서 체인의 서명 검증 실패. 원본을 보존했습니다. CA에 확인하세요."}
		}
		prev = ca
	}
	return nil
}
