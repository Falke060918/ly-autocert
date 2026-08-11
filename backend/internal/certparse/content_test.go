package certparse

// 0018 원문 파생 속성(Describe의 Content)의 검증.
// 픽스처는 전부 테스트 실행 시점에 생성한다 (보안 하드 규칙 4).

import (
	"bytes"
	"crypto/sha256"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/hex"
	"math/big"
	"regexp"
	"testing"
	"time"
)

func TestDescribeContentLeaf(t *testing.T) {
	tmpl := serverTemplate()
	tmpl.ExtKeyUsage = []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth, x509.ExtKeyUsageClientAuth}
	tmpl.OCSPServer = []string{"http://ocsp.example.test"}
	tmpl.CRLDistributionPoints = []string{"http://crl.example.test/a.crl", "http://crl.example.test/b.crl"}
	tmpl.IssuingCertificateURL = []string{"http://aia.example.test/ca.cer"}
	fx := newSelfSigned(t, tmpl)

	c := Describe(fx.cert, time.Now()).Content

	if !bytes.Equal(c.RawDER, fx.cert.Raw) {
		t.Error("raw_der가 인증서 DER 원문과 다르다")
	}
	if c.SPKISHA256 == nil {
		t.Fatal("spki_sha256 = nil")
	}
	sum := sha256.Sum256(fx.cert.RawSubjectPublicKeyInfo)
	if *c.SPKISHA256 != hex.EncodeToString(sum[:]) {
		t.Error("spki_sha256가 SubjectPublicKeyInfo 다이제스트와 다르다")
	}
	// DB CHECK(certificates_spki_sha256_hex)와 같은 형식이어야 한다.
	if !regexp.MustCompile(`^[0-9a-f]{64}$`).MatchString(*c.SPKISHA256) {
		t.Errorf("spki_sha256 형식 불일치: 길이 %d", len(*c.SPKISHA256))
	}
	if c.KeyUsage == nil || *c.KeyUsage != int(x509.KeyUsageDigitalSignature) {
		t.Errorf("key_usage = %v, want %d", c.KeyUsage, int(x509.KeyUsageDigitalSignature))
	}
	// classify()는 첫 매칭 1개만 쓰지만 EKU는 전부 남아야 한다.
	if len(c.ExtKeyUsage) != 2 || c.ExtKeyUsage[0] != "server_auth" || c.ExtKeyUsage[1] != "client_auth" {
		t.Errorf("ext_key_usage = %v, want [server_auth client_auth]", c.ExtKeyUsage)
	}
	if c.IsCA == nil || *c.IsCA {
		t.Errorf("is_ca = %v, want false", c.IsCA)
	}
	// 리프에는 pathLenConstraint가 비적용이다.
	if c.PathLenConstraint != nil {
		t.Errorf("path_len_constraint = %v, want nil", *c.PathLenConstraint)
	}
	if len(c.OCSPURLs) != 1 || len(c.CRLURLs) != 2 || len(c.AIACAIssuerURLs) != 1 {
		t.Errorf("URL 목록 = ocsp %v / crl %v / aia %v", c.OCSPURLs, c.CRLURLs, c.AIACAIssuerURLs)
	}
}

// 확장이 하나도 없으면 nil이 아니라 빈 슬라이스여야 한다 —
// 저장 계층이 "미상(NULL)"과 "값 없음('{}')"을 이것으로 가른다.
func TestDescribeContentEmptyIsNotNil(t *testing.T) {
	tmpl := &x509.Certificate{
		SerialNumber: big.NewInt(11),
		Subject:      pkix.Name{CommonName: "bare-fixture.example.test"},
		NotBefore:    time.Now().Add(-time.Hour),
		NotAfter:     time.Now().Add(24 * time.Hour),
	}
	fx := newSelfSigned(t, tmpl)
	c := Describe(fx.cert, time.Now()).Content

	if c.ExtKeyUsage == nil || len(c.ExtKeyUsage) != 0 {
		t.Errorf("ext_key_usage = %v, want 빈 슬라이스", c.ExtKeyUsage)
	}
	if c.OCSPURLs == nil || c.CRLURLs == nil || c.AIACAIssuerURLs == nil {
		t.Error("URL 목록은 값이 없어도 nil이 아니어야 한다")
	}
	if c.KeyUsage == nil || *c.KeyUsage != 0 {
		t.Errorf("key_usage = %v, want 0 (확장 없음)", c.KeyUsage)
	}
}

func TestDescribeContentCAPathLen(t *testing.T) {
	// 제약 없는 CA → -1
	fx := newSelfSigned(t, caTemplate())
	c := Describe(fx.cert, time.Now()).Content
	if c.IsCA == nil || !*c.IsCA {
		t.Errorf("is_ca = %v, want true", c.IsCA)
	}
	if c.PathLenConstraint == nil || *c.PathLenConstraint != -1 {
		t.Errorf("path_len_constraint = %v, want -1 (제약 없음)", c.PathLenConstraint)
	}

	// pathLen=0 (하위 CA를 만들 수 없는 CA) → 0. -1과 반드시 구분돼야 한다.
	zero := caTemplate()
	zero.MaxPathLen = 0
	zero.MaxPathLenZero = true
	fxZero := newSelfSigned(t, zero)
	cz := Describe(fxZero.cert, time.Now()).Content
	if cz.PathLenConstraint == nil || *cz.PathLenConstraint != 0 {
		t.Errorf("path_len_constraint = %v, want 0", cz.PathLenConstraint)
	}

	// pathLen=2
	two := caTemplate()
	two.MaxPathLen = 2
	fxTwo := newSelfSigned(t, two)
	ct := Describe(fxTwo.cert, time.Now()).Content
	if ct.PathLenConstraint == nil || *ct.PathLenConstraint != 2 {
		t.Errorf("path_len_constraint = %v, want 2", ct.PathLenConstraint)
	}
}

// classify()의 동작은 0018로 바뀌지 않는다 — certificate_type이 달라지면
// 기존 필터·위험 판정이 통째로 흔들린다. is_ca·ext_key_usage는 추가 정보일 뿐이다.
func TestClassifyUnchangedBy0018(t *testing.T) {
	cases := []struct {
		name string
		tmpl func() *x509.Certificate
		want string
	}{
		{"server", func() *x509.Certificate { return serverTemplate() }, "server"},
		{"ca", func() *x509.Certificate { return caTemplate() }, "ca"},
		{"client-first", func() *x509.Certificate {
			tm := serverTemplate()
			tm.ExtKeyUsage = []x509.ExtKeyUsage{x509.ExtKeyUsageClientAuth, x509.ExtKeyUsageServerAuth}
			return tm
		}, "client"},
		{"other", func() *x509.Certificate {
			tm := serverTemplate()
			tm.ExtKeyUsage = nil
			return tm
		}, "other"},
		// CA 비트가 EKU보다 우선한다는 규칙도 그대로다.
		{"ca-beats-eku", func() *x509.Certificate {
			tm := caTemplate()
			tm.ExtKeyUsage = []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth}
			return tm
		}, "ca"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			fx := newSelfSigned(t, tc.tmpl())
			if got := string(Describe(fx.cert, time.Now()).CertificateType); got != tc.want {
				t.Errorf("certificate_type = %q, want %q", got, tc.want)
			}
		})
	}
}
