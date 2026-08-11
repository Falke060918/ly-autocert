package certrisk

// 판정 엔진 단위 테스트. 각 규칙의 경계값을 전부 덮는다.
// 픽스처는 domain 구조체를 직접 만든다 — 실제 키·인증서를 하드코딩하지 않는다
// (보안 하드 규칙 4). DB·네트워크가 필요 없어 항상 실행된다.

import (
	"strings"
	"testing"
	"time"

	"github.com/Falke060918/INHERIT_AutoCert/backend/internal/domain"
)

var testNow = time.Date(2026, 8, 11, 12, 0, 0, 0, time.UTC)

// baseCert는 아무 판정에도 걸리지 않는(키 강도만 ok인) 기준 인증서다.
// 각 테스트는 필요한 필드만 바꿔 그 규칙 하나만 발화시킨다.
func baseCert() domain.Certificate {
	return domain.Certificate{
		SubjectDN:          "CN=leaf.example.test,O=Example",
		IssuerDN:           "CN=fixture-ca,O=Example",
		CommonName:         "leaf.example.test",
		SANs:               []string{"leaf.example.test"},
		NotBefore:          testNow.Add(-30 * 24 * time.Hour),
		NotAfter:           testNow.Add(300 * 24 * time.Hour),
		PublicKeyAlgorithm: "ECDSA",
		PublicKeySize:      384,
		SignatureAlgorithm: "ECDSA-SHA384",
		CertificateType:    domain.CertTypeServer,
		Status:             domain.CertStatusActive,
	}
}

func assess(c domain.Certificate, locs ...LocationInput) Assessment {
	return Evaluate(Input{Certificate: c, Locations: locs, Now: testNow})
}

// codes는 판정 결과의 코드 목록이다.
func codes(a Assessment) []Code {
	out := make([]Code, len(a.Findings))
	for i, f := range a.Findings {
		out[i] = f.Code
	}
	return out
}

func hasCode(a Assessment, want Code) bool {
	for _, c := range codes(a) {
		if c == want {
			return true
		}
	}
	return false
}

func findingFor(t *testing.T, a Assessment, want Code) Finding {
	t.Helper()
	for _, f := range a.Findings {
		if f.Code == want {
			return f
		}
	}
	t.Fatalf("finding %q not found; got %v", want, codes(a))
	return Finding{}
}

func TestBaselineCertificateIsOK(t *testing.T) {
	a := assess(baseCert())
	if a.Severity != SeverityOK {
		t.Fatalf("severity = %q, want ok (findings %v)", a.Severity, codes(a))
	}
	if !hasCode(a, CodeKeyStrong) {
		t.Errorf("baseline should report key_strong; got %v", codes(a))
	}
}

func TestKeyStrengthBoundaries(t *testing.T) {
	cases := []struct {
		alg  string
		size int
		want Code
	}{
		{"RSA", 1024, CodeWeakKey},
		{"RSA", 2047, CodeWeakKey},
		{"RSA", 2048, CodeKeyDeprecated2030},
		{"RSA", 3071, CodeKeyDeprecated2030},
		{"RSA", 3072, CodeKeyStrong},
		{"RSA", 4096, CodeKeyStrong},
		{"ECDSA", 224, CodeWeakKey},
		{"ECDSA", 255, CodeWeakKey},
		{"ECDSA", 256, CodeKeyDeprecated2030},
		{"ECDSA", 383, CodeKeyDeprecated2030},
		{"ECDSA", 384, CodeKeyStrong},
		{"ECDSA", 521, CodeKeyStrong},
		{"Ed25519", 256, CodeEd25519Key},
	}
	for _, tc := range cases {
		c := baseCert()
		c.PublicKeyAlgorithm = tc.alg
		c.PublicKeySize = tc.size
		a := assess(c)
		if !hasCode(a, tc.want) {
			t.Errorf("%s/%d: codes = %v, want %q", tc.alg, tc.size, codes(a), tc.want)
		}
	}
}

func TestKeyStrengthSeverityMapping(t *testing.T) {
	weak := baseCert()
	weak.PublicKeyAlgorithm = "RSA"
	weak.PublicKeySize = 1024
	if got := assess(weak).Severity; got != SeverityDanger {
		t.Errorf("RSA-1024 severity = %q, want danger", got)
	}

	dep := baseCert()
	dep.PublicKeyAlgorithm = "RSA"
	dep.PublicKeySize = 2048
	if got := assess(dep).Severity; got != SeverityInfo {
		t.Errorf("RSA-2048 severity = %q, want info", got)
	}

	ed := baseCert()
	ed.PublicKeyAlgorithm = "Ed25519"
	ed.SignatureAlgorithm = "Ed25519"
	if got := assess(ed).Severity; got != SeverityInfo {
		t.Errorf("Ed25519 severity = %q, want info", got)
	}
}

// 알 수 없는 알고리즘·길이 0은 판정하지 않는다 — 모르는 것을 danger로 띄우면 오탐이다.
func TestUnknownKeyAlgorithmIsNotJudged(t *testing.T) {
	for _, tc := range []struct {
		alg  string
		size int
	}{{"DSA", 1024}, {"Unknown", 0}, {"RSA", 0}, {"ECDSA", 0}} {
		c := baseCert()
		c.PublicKeyAlgorithm = tc.alg
		c.PublicKeySize = tc.size
		a := assess(c)
		for _, code := range []Code{CodeWeakKey, CodeKeyDeprecated2030, CodeKeyStrong, CodeEd25519Key} {
			if hasCode(a, code) {
				t.Errorf("%s/%d: unexpected key finding %q", tc.alg, tc.size, code)
			}
		}
	}
}

func TestSignatureAlgorithmBoundaries(t *testing.T) {
	cases := []struct {
		alg  string
		weak bool
	}{
		{"MD2-RSA", true},
		{"MD5-RSA", true},
		{"SHA1-RSA", true},
		{"ECDSA-SHA1", true},
		{"DSA-SHA1", true},
		{"SHA-1 with RSA", true}, // 표기가 달라도 같은 토큰으로 잡는다
		{"SHA256-RSA", false},
		{"ECDSA-SHA256", false},
		{"SHA384-RSA", false},
		{"SHA512-RSA", false},
		{"SHA256-RSAPSS", false},
		{"Ed25519", false},
		{"UnknownSignatureAlgorithm", false}, // 모르는 것은 약하다고 단정하지 않는다
	}
	for _, tc := range cases {
		c := baseCert()
		c.SignatureAlgorithm = tc.alg
		a := assess(c)
		if got := hasCode(a, CodeWeakSignature); got != tc.weak {
			t.Errorf("%s: weak_signature = %v, want %v", tc.alg, got, tc.weak)
		}
		if tc.weak && a.Severity != SeverityDanger {
			t.Errorf("%s: severity = %q, want danger", tc.alg, a.Severity)
		}
	}
}

func TestSANCountBoundary(t *testing.T) {
	none := baseCert()
	none.SANs = nil
	a := assess(none)
	if !hasCode(a, CodeNoSAN) || a.Severity != SeverityDanger {
		t.Errorf("0 SAN: codes = %v severity = %q, want no_san/danger", codes(a), a.Severity)
	}

	empty := baseCert()
	empty.SANs = []string{}
	if !hasCode(assess(empty), CodeNoSAN) {
		t.Error("empty SAN slice should report no_san")
	}

	one := baseCert()
	one.SANs = []string{"leaf.example.test"}
	if hasCode(assess(one), CodeNoSAN) {
		t.Error("1 SAN should not report no_san")
	}
}

func TestWildcardClassification(t *testing.T) {
	cases := []struct {
		san  string
		want Code // "" 이면 와일드카드 판정 없음
	}{
		{"leaf.example.test", ""},
		{"10.0.0.5", ""},
		{"*.example.com", CodeWildcard},
		{"*.example.co.kr", CodeWildcard},
		{"*.internal.corp.example.com", CodeWildcard},
		{"*.com", CodeWildcardBroad},
		{"*.kr", CodeWildcardBroad},
		{"*.co.kr", CodeWildcardBroad},
		{"*.ne.jp", CodeWildcardBroad},
		{"*.corp", CodeWildcardBroad},
		{"*", CodeWildcardBroad},
		{"*.*.example.com", CodeWildcardMultiLabel},
		{"api.*.example.com", CodeWildcardMultiLabel},
	}
	wildcardCodes := []Code{CodeWildcard, CodeWildcardBroad, CodeWildcardMultiLabel}
	for _, tc := range cases {
		c := baseCert()
		c.SANs = []string{tc.san}
		a := assess(c)
		for _, code := range wildcardCodes {
			want := code == tc.want
			if got := hasCode(a, code); got != want {
				t.Errorf("%q: %s = %v, want %v (codes %v)", tc.san, code, got, want, codes(a))
			}
		}
	}
}

func TestWildcardSeverityAndGrouping(t *testing.T) {
	c := baseCert()
	c.SANs = []string{"*.example.com", "*.co.kr", "*.*.example.com", "leaf.example.test"}
	a := assess(c)
	for _, code := range []Code{CodeWildcard, CodeWildcardBroad, CodeWildcardMultiLabel} {
		if !hasCode(a, code) {
			t.Errorf("missing %q; got %v", code, codes(a))
		}
	}
	if a.Severity != SeverityDanger {
		t.Errorf("severity = %q, want danger (broad wildcard)", a.Severity)
	}
	// 유형당 판정은 1건이며 대상 이름이 메시지에 실린다.
	f := findingFor(t, a, CodeWildcardBroad)
	if !strings.Contains(f.Message, "*.co.kr") {
		t.Errorf("broad message should name the SAN, got %q", f.Message)
	}
}

func TestValidityPeriodBoundaries(t *testing.T) {
	cases := []struct {
		days int
		want bool
	}{{824, false}, {825, false}, {826, true}, {3650, true}}
	for _, tc := range cases {
		c := baseCert()
		c.NotBefore = testNow.Add(-time.Duration(tc.days) * 24 * time.Hour)
		c.NotAfter = testNow
		a := assess(c)
		if got := hasCode(a, CodeLongValidity); got != tc.want {
			t.Errorf("%d days: long_validity = %v, want %v", tc.days, got, tc.want)
		}
	}

	c := baseCert()
	c.NotBefore = testNow.Add(-1000 * 24 * time.Hour)
	c.NotAfter = testNow.Add(24 * time.Hour)
	if got := assess(c).Severity; got != SeverityWarn {
		t.Errorf("long validity severity = %q, want warn", got)
	}
}

func TestExpiredCARule(t *testing.T) {
	// 만료된 CA(자체 서명) → expired_ca danger.
	ca := baseCert()
	ca.CertificateType = domain.CertTypeCA
	ca.NotAfter = testNow.Add(-24 * time.Hour)
	a := assess(ca)
	if !hasCode(a, CodeExpiredCA) || a.Severity != SeverityDanger {
		t.Errorf("expired CA: codes = %v severity = %q", codes(a), a.Severity)
	}

	// 아직 유효한 CA는 판정 없음.
	live := baseCert()
	live.CertificateType = domain.CertTypeCA
	live.NotAfter = testNow.Add(24 * time.Hour)
	if hasCode(assess(live), CodeExpiredCA) {
		t.Error("live CA should not report expired_ca")
	}

	// 만료된 리프는 expired_ca가 아니다(만료 판정은 기존 경로 소관).
	leaf := baseCert()
	leaf.NotAfter = testNow.Add(-24 * time.Hour)
	if hasCode(assess(leaf), CodeExpiredCA) {
		t.Error("expired leaf should not report expired_ca")
	}
}

func TestSelfSignedThreeBranches(t *testing.T) {
	ski := "aabbcc"
	other := "ddeeff"

	// ① subject = issuer + AKI 없음 + CA → 자체 서명 warn
	noAKI := baseCert()
	noAKI.SubjectDN = "CN=fixture-root-ca"
	noAKI.IssuerDN = "CN=fixture-root-ca"
	noAKI.CertificateType = domain.CertTypeCA
	noAKI.SubjectKeyID = &ski
	a := assess(noAKI)
	if !hasCode(a, CodeSelfSigned) || hasCode(a, CodeChainAnomaly) {
		t.Errorf("no-AKI self-signed CA: codes = %v", codes(a))
	}
	if a.Severity != SeverityWarn {
		t.Errorf("self-signed severity = %q, want warn", a.Severity)
	}

	// ② subject = issuer + AKI = SKI + CA → 자체 서명 warn
	matchAKI := noAKI
	matchAKI.AuthorityKeyID = &ski
	if a := assess(matchAKI); !hasCode(a, CodeSelfSigned) || hasCode(a, CodeChainAnomaly) {
		t.Errorf("AKI=SKI self-signed CA: codes = %v", codes(a))
	}

	// ③-a AKI가 SKI와 다름 → 체인 이상 danger
	mismatch := noAKI
	mismatch.AuthorityKeyID = &other
	a = assess(mismatch)
	if !hasCode(a, CodeChainAnomaly) || hasCode(a, CodeSelfSigned) {
		t.Errorf("AKI mismatch: codes = %v", codes(a))
	}
	if a.Severity != SeverityDanger {
		t.Errorf("chain anomaly severity = %q, want danger", a.Severity)
	}

	// ③-b CA가 아님 → 체인 이상 danger
	notCA := baseCert()
	notCA.SubjectDN = "CN=selfsigned.example.test"
	notCA.IssuerDN = "CN=selfsigned.example.test"
	notCA.SubjectKeyID = &ski
	if a := assess(notCA); !hasCode(a, CodeChainAnomaly) || hasCode(a, CodeSelfSigned) {
		t.Errorf("self-issued non-CA: codes = %v", codes(a))
	}

	// ③-c AKI는 있는데 SKI가 없음 → 비교 불가이므로 체인 이상
	noSKI := baseCert()
	noSKI.SubjectDN = "CN=fixture-root-ca"
	noSKI.IssuerDN = "CN=fixture-root-ca"
	noSKI.CertificateType = domain.CertTypeCA
	noSKI.AuthorityKeyID = &ski
	if a := assess(noSKI); !hasCode(a, CodeChainAnomaly) {
		t.Errorf("AKI without SKI: codes = %v", codes(a))
	}

	// subject ≠ issuer 는 어느 쪽도 아니다.
	normal := baseCert()
	if a := assess(normal); hasCode(a, CodeSelfSigned) || hasCode(a, CodeChainAnomaly) {
		t.Errorf("normal issued cert: codes = %v", codes(a))
	}
}

func TestLegacyTLSIsLocationScoped(t *testing.T) {
	c := baseCert()

	// 위치를 주지 않으면 위치 축 판정 자체가 없다.
	if hasCode(assess(c), CodeLegacyTLS) {
		t.Error("no locations should not produce legacy_tls")
	}

	for _, v := range []string{"TLS 1.0", "TLS 1.1"} {
		a := assess(c, LocationInput{TLSVersion: v})
		f := findingFor(t, a, CodeLegacyTLS)
		if f.Scope != ScopeLocation {
			t.Errorf("%s: scope = %q, want location", v, f.Scope)
		}
		if f.Severity != SeverityDanger || a.Severity != SeverityDanger {
			t.Errorf("%s: severity = %q/%q, want danger", v, f.Severity, a.Severity)
		}
	}

	for _, v := range []string{"TLS 1.2", "TLS 1.3", ""} {
		if hasCode(assess(c, LocationInput{TLSVersion: v}), CodeLegacyTLS) {
			t.Errorf("%q should not produce legacy_tls", v)
		}
	}

	// removed 위치의 옛 관측으로는 판정하지 않는다.
	if hasCode(assess(c, LocationInput{TLSVersion: "TLS 1.0", Removed: true}), CodeLegacyTLS) {
		t.Error("removed location should not produce legacy_tls")
	}

	// 여러 위치는 1건으로 묶이고 버전이 함께 나열된다.
	a := assess(c,
		LocationInput{TLSVersion: "TLS 1.1"},
		LocationInput{TLSVersion: "TLS 1.0"},
		LocationInput{TLSVersion: "TLS 1.3"},
	)
	f := findingFor(t, a, CodeLegacyTLS)
	if !strings.Contains(f.Message, "TLS 1.0") || !strings.Contains(f.Message, "TLS 1.1") {
		t.Errorf("message should list both versions, got %q", f.Message)
	}
	count := 0
	for _, x := range a.Findings {
		if x.Code == CodeLegacyTLS {
			count++
		}
	}
	if count != 1 {
		t.Errorf("legacy_tls findings = %d, want 1", count)
	}
}

func TestIncompleteChainIsLocationScoped(t *testing.T) {
	c := baseCert()
	yes, no := true, false

	// 위치가 없거나 미상(nil)이면 판정하지 않는다 — 모르는 것을 danger로 띄우지 않는다.
	if hasCode(assess(c), CodeIncompleteChain) {
		t.Error("no locations should not produce incomplete_chain")
	}
	if hasCode(assess(c, LocationInput{}), CodeIncompleteChain) {
		t.Error("chain_complete=nil(0018 이전 관측)이 판정에 걸렸다")
	}
	// 완전한 체인도 걸리지 않는다.
	if hasCode(assess(c, LocationInput{ChainComplete: &yes}), CodeIncompleteChain) {
		t.Error("complete chain should not produce incomplete_chain")
	}

	a := assess(c, LocationInput{ChainComplete: &no})
	f := findingFor(t, a, CodeIncompleteChain)
	if f.Scope != ScopeLocation {
		t.Errorf("scope = %q, want location (재발급이 아니라 배포를 고쳐야 한다)", f.Scope)
	}
	if f.Severity != SeverityDanger || a.Severity != SeverityDanger {
		t.Errorf("severity = %q/%q, want danger", f.Severity, a.Severity)
	}

	// removed 위치의 옛 관측으로는 판정하지 않는다.
	if hasCode(assess(c, LocationInput{ChainComplete: &no, Removed: true}), CodeIncompleteChain) {
		t.Error("removed location should not produce incomplete_chain")
	}

	// 여러 위치가 걸려도 판정은 1건으로 묶이고 곳 수가 실린다.
	multi := assess(c,
		LocationInput{ChainComplete: &no},
		LocationInput{ChainComplete: &no},
		LocationInput{ChainComplete: &yes},
		LocationInput{},
	)
	count := 0
	for _, x := range multi.Findings {
		if x.Code == CodeIncompleteChain {
			count++
		}
	}
	if count != 1 {
		t.Errorf("incomplete_chain findings = %d, want 1", count)
	}
	if msg := findingFor(t, multi, CodeIncompleteChain).Message; !strings.Contains(msg, "2곳") {
		t.Errorf("message should count 2 locations, got %q", msg)
	}
}

// 위치 축 규칙 둘은 서로 독립이다 — 하나가 걸려도 다른 하나를 가리지 않는다.
func TestLocationScopedRulesAreIndependent(t *testing.T) {
	c := baseCert()
	no := false
	a := assess(c, LocationInput{TLSVersion: "TLS 1.0", ChainComplete: &no})
	if !hasCode(a, CodeLegacyTLS) || !hasCode(a, CodeIncompleteChain) {
		t.Errorf("codes = %v, want both legacy_tls and incomplete_chain", codes(a))
	}
}

// 최고 심각도는 판정 항목 중 가장 심각한 값이다 (ok < info < warn < danger).
func TestAssessmentSeverityIsMaximum(t *testing.T) {
	c := baseCert()
	c.PublicKeyAlgorithm = "RSA"
	c.PublicKeySize = 2048             // info
	c.SANs = []string{"*.example.com"} // warn
	if got := assess(c).Severity; got != SeverityWarn {
		t.Errorf("severity = %q, want warn", got)
	}
	c.SignatureAlgorithm = "SHA1-RSA" // danger
	if got := assess(c).Severity; got != SeverityDanger {
		t.Errorf("severity = %q, want danger", got)
	}
}

// 판정 결과 순서는 카탈로그 순서로 고정된다 — 화면이 매 요청마다 다른 순서를 받지 않는다.
func TestFindingOrderFollowsCatalog(t *testing.T) {
	c := baseCert()
	c.PublicKeyAlgorithm = "RSA"
	c.PublicKeySize = 1024
	c.SignatureAlgorithm = "SHA1-RSA"
	c.SANs = []string{"*.co.kr", "*.example.com"}
	c.NotBefore = testNow.Add(-1000 * 24 * time.Hour)
	c.SubjectDN = "CN=weird"
	c.IssuerDN = "CN=weird"

	got := codes(assess(c, LocationInput{TLSVersion: "TLS 1.0"}))
	want := []Code{
		CodeWeakKey, CodeWeakSignature, CodeWildcardBroad, CodeWildcard,
		CodeLongValidity, CodeChainAnomaly, CodeLegacyTLS,
	}
	if len(got) != len(want) {
		t.Fatalf("codes = %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("codes = %v, want %v", got, want)
		}
	}
}

// 카탈로그는 코드마다 심각도가 고정이다 — 같은 코드가 상황에 따라 색이 바뀌면
// 목록 필터와 집계가 서로를 부정한다.
func TestRuleCatalogIsConsistent(t *testing.T) {
	seen := map[Code]bool{}
	for _, r := range Rules() {
		if seen[r.Code] {
			t.Errorf("duplicate rule code %q", r.Code)
		}
		seen[r.Code] = true
		if !IsSeverity(string(r.Severity)) {
			t.Errorf("rule %q has unknown severity %q", r.Code, r.Severity)
		}
		if r.Scope != ScopeCertificate && r.Scope != ScopeLocation {
			t.Errorf("rule %q has unknown scope %q", r.Code, r.Scope)
		}
		if r.Title == "" {
			t.Errorf("rule %q has no title", r.Code)
		}
		if r.PublicTrustOnly {
			t.Errorf("rule %q is public-trust-only; this unit ships private-safe rules only", r.Code)
		}
		got, ok := LookupRule(string(r.Code))
		if !ok || got != r {
			t.Errorf("LookupRule(%q) = %v/%v", r.Code, got, ok)
		}
	}
	if _, ok := LookupRule("no_such_code"); ok {
		t.Error("LookupRule accepted an unknown code")
	}
	if IsSeverity("critical") {
		t.Error("IsSeverity accepted an undefined severity")
	}
}

// Now 제로값이면 현재 시각으로 판정한다(호출자가 시각을 안 넘겨도 동작해야 한다).
func TestZeroNowFallsBackToCurrentTime(t *testing.T) {
	ca := baseCert()
	ca.CertificateType = domain.CertTypeCA
	ca.NotAfter = time.Now().Add(-time.Hour)
	a := Evaluate(Input{Certificate: ca})
	if !hasCode(a, CodeExpiredCA) {
		t.Errorf("codes = %v, want expired_ca", codes(a))
	}
}
