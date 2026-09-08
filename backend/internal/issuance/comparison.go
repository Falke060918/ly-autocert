package issuance

import (
	"bytes"
	"crypto/sha256"
	"crypto/x509"
	"encoding/hex"
	"encoding/pem"
	"errors"
	"math"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/Falke060918/INHERIT_AutoCert/backend/internal/certparse"
	"github.com/Falke060918/INHERIT_AutoCert/backend/internal/issuance/upstream"
	"github.com/Falke060918/INHERIT_AutoCert/backend/internal/store"
)

func digest(data []byte) string { sum := sha256.Sum256(data); return hex.EncodeToString(sum[:]) }
func certificateInfo(c *x509.Certificate) store.IssuedCertificateInfo {
	d := certparse.Describe(c, time.Now())
	return store.IssuedCertificateInfo{Subject: c.Subject.String(), Issuer: c.Issuer.String(), CommonName: c.Subject.CommonName, DNSNames: append([]string{}, c.DNSNames...), SKI: hex.EncodeToString(c.SubjectKeyId), AKI: hex.EncodeToString(c.AuthorityKeyId), Fingerprint: d.FingerprintSHA256, PublicKeySHA256: digest(c.RawSubjectPublicKeyInfo), Algorithm: d.PublicKeyAlgorithm, KeySize: d.PublicKeySize, NotBefore: c.NotBefore, NotAfter: c.NotAfter, IsCA: c.IsCA}
}

// InspectIssued records request/response differences without correcting the CA
// response. Only unreadable certificates or private-key material remain errors.
func InspectIssued(s upstream.Snapshot, csrPEM string, spec upstream.Spec) (*x509.Certificate, string, string, *store.IssuanceComparison, error) {
	fail := func(message string) (*x509.Certificate, string, string, *store.IssuanceComparison, error) {
		return nil, "", "", nil, errors.New(message)
	}
	data, err := certificateData(s.Certificate.Base64)
	if err != nil {
		return fail("CA 발급 인증서의 인코딩을 읽을 수 없습니다")
	}
	certs, err := certparse.Parse(data)
	if errors.Is(err, certparse.ErrPrivateKeyFound) || bytes.Contains(data, []byte("PRIVATE KEY")) {
		return fail("CA 응답에 개인키가 포함되어 등록할 수 없습니다")
	}
	if err != nil || len(certs) != 1 {
		return fail("CA 응답을 단일 발급 인증서로 읽을 수 없습니다")
	}
	c := certs[0]
	now := time.Now()
	out := &store.IssuanceComparison{Requested: spec, Actual: certificateInfo(c), CheckedAt: now, Chain: []store.IssuedCertificateInfo{}, Warnings: []store.IssuanceWarning{}, ChainSourceSHA256: digest([]byte(s.Certificate.Chain))}
	warn := func(code, message string) {
		out.Warnings = append(out.Warnings, store.IssuanceWarning{Code: code, Message: message})
	}
	if block, _ := pem.Decode([]byte(csrPEM)); block != nil {
		if csr, e := x509.ParseCertificateRequest(block.Bytes); e == nil {
			out.CSRSignatureValid = csr.CheckSignature() == nil
			if pub, e := x509.MarshalPKIXPublicKey(csr.PublicKey); e == nil {
				out.CSRPublicKeySHA256 = digest(pub)
				match := bytes.Equal(pub, c.RawSubjectPublicKeyInfo)
				out.CSRKeyMatches = &match
			}
		}
	}
	if out.CSRKeyMatches == nil {
		warn("csr_key_unknown", "요청 CSR과 발급 인증서의 공개키 일치를 확인할 수 없습니다. 요청 개인키를 연결하지 않았습니다.")
	} else if !*out.CSRKeyMatches {
		warn("csr_key_mismatch", "요청 CSR과 발급 인증서의 공개키가 다릅니다. 요청 개인키를 연결하지 않았습니다.")
	}
	if !out.CSRSignatureValid {
		warn("csr_signature_invalid", "요청 CSR 서명을 확인할 수 없습니다.")
	}
	wanted, actual := append([]string{}, spec.DNSNames...), append([]string{}, c.DNSNames...)
	slices.Sort(wanted)
	slices.Sort(actual)
	out.DNSMatches = slices.Equal(wanted, actual)
	if !out.DNSMatches {
		warn("dns_mismatch", "요청 도메인과 발급 인증서의 DNS SAN이 다릅니다. 비교 내용을 확인하세요.")
	}
	out.CurrentlyValid = now.Before(c.NotAfter) && !c.NotBefore.After(now.Add(5*time.Minute))
	if !out.CurrentlyValid {
		warn("certificate_time_invalid", "발급 인증서가 현재 유효기간 밖에 있습니다. 실제 시작일과 만료일을 확인하세요.")
	}
	out.ValidityDays = c.NotAfter.Sub(c.NotBefore).Hours() / 24
	out.ValidityMatches = spec.ValidityDays == 0 || math.Abs(out.ValidityDays-float64(spec.ValidityDays)) <= 5.0/1440
	if !out.ValidityMatches {
		warn("validity_mismatch", "요청 기간과 발급 인증서의 실제 유효기간이 다릅니다. 비교 내용을 확인하세요.")
	}
	actualAlg := strings.ToLower(out.Actual.Algorithm)
	out.RequestedKeyMatches = spec.Algorithm == "" || (strings.HasPrefix(spec.Algorithm, "rsa") && actualAlg == "rsa" && spec.Algorithm == "rsa"+strconv.Itoa(out.Actual.KeySize)) || (spec.Algorithm == "ecdsa_p256" && actualAlg == "ecdsa" && out.Actual.KeySize == 256) || (spec.Algorithm == "ecdsa_p384" && actualAlg == "ecdsa" && out.Actual.KeySize == 384)
	if !out.RequestedKeyMatches {
		warn("key_algorithm_mismatch", "요청한 키 종류·크기와 발급 인증서의 키가 다릅니다.")
	}
	if c.IsCA {
		warn("certificate_is_ca", "발급된 인증서에 CA 속성이 있습니다.")
	}
	chainErr := validateCAChain(c, s.Certificate.Chain)
	if chainErr != nil {
		var chainWarning *ChainWarning
		if !errors.As(chainErr, &chainWarning) {
			return fail(chainErr.Error())
		}
		warn(chainWarning.Code, chainWarning.Error())
	}
	if chainData, e := certificateData(s.Certificate.Chain); e == nil {
		if chainCerts, e := certparse.Parse(chainData); e == nil {
			var warning *ChainWarning
			out.ChainParseable = chainErr == nil || (errors.As(chainErr, &warning) && warning.Code == "ca_chain_signature_failed")
			prev := c
			for _, ca := range chainCerts {
				info := certificateInfo(ca)
				valid := bytes.Equal(prev.RawIssuer, ca.RawSubject) && prev.CheckSignatureFrom(ca) == nil
				info.SignsPrevious = &valid
				out.Chain = append(out.Chain, info)
				prev = ca
			}
		}
	}
	leaf := string(data)
	if !bytes.Contains(data, []byte("-----BEGIN")) {
		leaf = string(pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: c.Raw}))
	}
	return c, leaf, s.Certificate.Chain, out, nil
}
