package certrisk

// 판정 규칙 구현. 각 규칙은 이미 저장된 컬럼만 본다 — 새 마이그레이션 없이 도는 것이
// 이번 단위의 전제다. 규칙을 늘릴 때는 certrisk.go의 카탈로그(rules)에 항목을 먼저
// 추가하고, store/certificates_risk.go에 같은 의미의 SQL 술어를 같은 커밋에 넣는다
// (교차 검증 테스트가 두 구현의 어긋남을 잡는다).

import (
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/Falke060918/INHERIT_AutoCert/backend/internal/domain"
)

// 키 강도 경계값. NIST IR 8547(2030년 RSA-2048·P-256 사용 중단)이 근거다.
const (
	rsaWeakBits         = 2048 // 미만이면 danger
	rsaDeprecatedBits   = 3072 // 미만이면 2030 사용 중단 예정
	ecdsaWeakBits       = 256  // 미만이면 danger
	ecdsaDeprecatedBits = 384  // 미만이면 2030 사용 중단 예정
)

// maxValidityDays는 사설 인증서 유효기간 상한이다.
// 825일은 공인 CA에 강제된 값이지만 사설 PKI에서도 "키 교체 없이 굴러가는 기간"의
// 실무 상한으로 통용된다 — 그래서 danger가 아니라 warn이다.
const maxValidityDays = 825

// keyStrengthRule은 공개키 알고리즘·길이로 키 강도를 판정한다.
// 알고리즘을 모르거나 길이가 0(파서가 뽑지 못한 알고리즘)이면 판정하지 않는다 —
// 모르는 것을 danger로 띄우면 오탐이 된다.
func keyStrengthRule(c domain.Certificate) (Code, string, bool) {
	alg := strings.ToUpper(strings.TrimSpace(c.PublicKeyAlgorithm))
	size := c.PublicKeySize
	switch alg {
	case "RSA":
		if size <= 0 {
			return "", "", false
		}
		switch {
		case size < rsaWeakBits:
			return CodeWeakKey, fmt.Sprintf(
				"RSA %d비트 키는 최소 기준 %d비트에 미달합니다. 키를 재생성해 재발급하십시오.",
				size, rsaWeakBits), true
		case size < rsaDeprecatedBits:
			return CodeKeyDeprecated2030, fmt.Sprintf(
				"RSA %d비트 키는 2030년까지만 유효한 강도입니다(NIST IR 8547). 다음 갱신 때 %d비트 이상으로 올리십시오.",
				size, rsaDeprecatedBits), true
		default:
			return CodeKeyStrong, fmt.Sprintf(
				"RSA %d비트 키로 2030년 이후 기준을 충족합니다.", size), true
		}
	case "ECDSA":
		if size <= 0 {
			return "", "", false
		}
		switch {
		case size < ecdsaWeakBits:
			return CodeWeakKey, fmt.Sprintf(
				"ECDSA %d비트 키는 최소 기준 P-256(%d비트)에 미달합니다. 키를 재생성해 재발급하십시오.",
				size, ecdsaWeakBits), true
		case size < ecdsaDeprecatedBits:
			return CodeKeyDeprecated2030, fmt.Sprintf(
				"ECDSA P-%d 키는 2030년까지만 유효한 강도입니다(NIST IR 8547). 다음 갱신 때 P-384 이상으로 올리십시오.",
				size), true
		default:
			return CodeKeyStrong, fmt.Sprintf(
				"ECDSA P-%d 키로 2030년 이후 기준을 충족합니다.", size), true
		}
	case "ED25519":
		return CodeEd25519Key, "Ed25519 키입니다. 사설 PKI에서는 정상이지만 공인 CA가 발급한 사례가 없어 외부 신뢰가 필요한 곳에는 쓸 수 없습니다.", true
	}
	return "", "", false
}

// weakDigests는 SHA-256 미만으로 판정하는 다이제스트 토큰이다.
// 서명 알고리즘 문자열은 crypto/x509가 만든 값이다(예 "SHA1-RSA", "ECDSA-SHA1").
var weakDigests = []string{"MD2", "MD4", "MD5", "SHA1"}

// normalizeSigAlg는 서명 알고리즘 문자열을 대문자·구분자 제거 형태로 정규화한다.
// "SHA1-RSA"·"SHA-1 with RSA" 어느 표기든 같은 토큰 검사에 걸리게 하기 위함이다.
func normalizeSigAlg(s string) string {
	r := strings.NewReplacer("-", "", " ", "", "_", "")
	return r.Replace(strings.ToUpper(s))
}

// weakSignatureRule은 SHA-256 미만 서명 알고리즘을 판정한다.
// 알 수 없는 알고리즘은 판정하지 않는다 — "모른다"는 "약하다"가 아니다.
func weakSignatureRule(c domain.Certificate) (string, bool) {
	norm := normalizeSigAlg(c.SignatureAlgorithm)
	for _, d := range weakDigests {
		if strings.Contains(norm, d) {
			return fmt.Sprintf(
				"서명 알고리즘 %s는 SHA-256 미만입니다. 충돌 공격이 실증된 다이제스트이므로 재발급이 필요합니다.",
				c.SignatureAlgorithm), true
		}
	}
	return "", false
}

// noSANRule은 SAN이 하나도 없는 인증서를 판정한다.
// RFC 6125가 CN 폴백을 폐기했고 Chrome 58(2017)부터 CN을 아예 무시하므로,
// SAN이 없으면 현대 클라이언트에서 이름 불일치로 연결이 실패한다.
func noSANRule(c domain.Certificate) (string, bool) {
	if len(c.SANs) > 0 {
		return "", false
	}
	return "SAN(Subject Alternative Name)이 없습니다. 현대 브라우저·클라이언트는 CN 폴백을 무시하므로(RFC 6125, Chrome 58 이후) 이름 불일치로 연결이 실패합니다.", true
}

// wildcardKind는 와일드카드 SAN의 분류다.
type wildcardKind int

const (
	wcNone wildcardKind = iota
	wcSimple
	wcBroad
	wcMultiLabel
)

// broadSecondLevels는 2글자 ccTLD 아래의 준-레지스트리 레이블이다.
// 공개 접미사 목록(Public Suffix List)을 반입하지 않고 레이블 수 + 이 짧은 목록으로만
// 판정한다 — 목록을 크게 키우면 폐쇄망에서 갱신할 방법이 없어 오히려 낡는다.
var broadSecondLevels = map[string]bool{
	"co": true, "com": true, "ne": true, "net": true, "or": true, "org": true,
	"ac": true, "edu": true, "go": true, "gov": true, "mil": true, "sch": true,
}

// classifyWildcard는 SAN 1건의 와일드카드 유형을 판정한다.
//
// 공개 접미사 판정은 외부 목록 없이 레이블 수 휴리스틱으로 한다:
//   - 와일드카드 뒤 레이블이 1개 이하(*.com, *.kr, *) → 레지스트리 통째 → broad
//   - 2개이고 TLD가 2글자 ccTLD이며 앞이 준-레지스트리 레이블(*.co.kr) → broad
//   - 그 외(*.example.com) → simple
func classifyWildcard(name string) wildcardKind {
	n := strings.ToLower(strings.TrimSpace(name))
	if !strings.Contains(n, "*") {
		return wcNone
	}
	labels := strings.Split(n, ".")
	// 별표가 2개 이상이거나 맨 앞 레이블이 아니면 RFC 6125가 허용하지 않는 형태다.
	if strings.Count(n, "*") > 1 || !strings.Contains(labels[0], "*") {
		return wcMultiLabel
	}
	base := labels[1:]
	if len(base) <= 1 {
		return wcBroad
	}
	if len(base) == 2 && len(base[1]) == 2 && broadSecondLevels[base[0]] {
		return wcBroad
	}
	return wcSimple
}

// wildcardRules는 SAN 목록을 유형별로 묶어 판정 결과를 만든다.
// 유형당 최대 1건이며 대상 이름은 메시지에 함께 싣는다(SAN은 이미 응답에 실리는 공개 정보다).
func wildcardRules(c domain.Certificate) []Finding {
	byKind := map[wildcardKind][]string{}
	for _, san := range c.SANs {
		k := classifyWildcard(san)
		if k == wcNone {
			continue
		}
		byKind[k] = append(byKind[k], strings.ToLower(strings.TrimSpace(san)))
	}
	// 카탈로그 순서(broad → simple → multi_label)를 그대로 따른다.
	order := []struct {
		kind wildcardKind
		code Code
		tmpl string
	}{
		{wcBroad, CodeWildcardBroad,
			"와일드카드 %s가 공개 접미사에 근접합니다. 사실상 해당 네임스페이스 전체에 유효한 인증서이므로 범위를 좁혀 재발급하십시오."},
		{wcSimple, CodeWildcard,
			"와일드카드 SAN %s가 있습니다. 키 하나가 유출되면 하위 도메인 전체가 영향을 받습니다."},
		{wcMultiLabel, CodeWildcardMultiLabel,
			"와일드카드 %s는 맨 앞 레이블 1개 형식이 아닙니다. RFC 6125를 따르는 클라이언트는 이 이름을 매칭하지 않습니다."},
	}
	var out []Finding
	for _, o := range order {
		names := byKind[o.kind]
		if len(names) == 0 {
			continue
		}
		sort.Strings(names)
		out = append(out, newFinding(o.code, fmt.Sprintf(o.tmpl, joinNames(names))))
	}
	return out
}

// joinNames는 이름 목록을 최대 3건까지 나열하고 나머지는 건수로 접는다.
func joinNames(names []string) string {
	if len(names) <= 3 {
		return strings.Join(names, ", ")
	}
	return fmt.Sprintf("%s 외 %d건", strings.Join(names[:3], ", "), len(names)-3)
}

// longValidityRule은 825일을 초과하는 유효기간을 판정한다.
func longValidityRule(c domain.Certificate) (string, bool) {
	if c.NotAfter.IsZero() || c.NotBefore.IsZero() {
		return "", false
	}
	d := c.NotAfter.Sub(c.NotBefore)
	if d <= time.Duration(maxValidityDays)*24*time.Hour {
		return "", false
	}
	days := int(d.Hours() / 24)
	return fmt.Sprintf(
		"유효기간이 %d일로 권장 상한 %d일을 초과합니다. 수명이 길수록 키 유출 노출 기간도 길어집니다.",
		days, maxValidityDays), true
}

// expiredCARule은 이미 만료된 CA 인증서를 판정한다.
// CA가 만료되면 그 CA가 발급한 인증서가 전부 검증에 실패하므로 리프 만료와 파급이 다르다.
func expiredCARule(c domain.Certificate, now time.Time) (string, bool) {
	if c.CertificateType != domain.CertTypeCA || !c.NotAfter.Before(now) {
		return "", false
	}
	return "CA 인증서가 이미 만료됐습니다. 이 CA가 발급한 인증서는 전부 체인 검증에 실패합니다.", true
}

// selfSignedRule은 subject=issuer인 인증서를 자체 서명과 체인 이상으로 가른다.
//
// 문자열 비교(subject_dn = issuer_dn)만으로는 둘을 구분할 수 없다. 진짜 자체 서명은
// AKI가 없거나 자기 SKI와 같고 CA 인증서여야 한다. 그 조건이 어긋난 채 DN만 같은 것은
// 체인 검증 실패·위조 의심 신호이므로 danger로 따로 띄운다.
func selfSignedRule(c domain.Certificate) (Code, string, bool) {
	if c.SubjectDN != c.IssuerDN {
		return "", "", false
	}
	akiOK := c.AuthorityKeyID == nil || *c.AuthorityKeyID == "" ||
		(c.SubjectKeyID != nil && *c.AuthorityKeyID == *c.SubjectKeyID)
	isCA := c.CertificateType == domain.CertTypeCA
	if akiOK && isCA {
		return CodeSelfSigned, "자체 서명 CA입니다. 신뢰 저장소에 직접 배포된 루트인지 확인하십시오.", true
	}
	var reasons []string
	if !akiOK {
		reasons = append(reasons, "AKI(Authority Key Identifier)가 자신의 SKI와 일치하지 않습니다")
	}
	if !isCA {
		reasons = append(reasons, "CA 인증서가 아닙니다")
	}
	return CodeChainAnomaly, fmt.Sprintf(
		"Subject와 Issuer가 같은데 %s. 자체 서명이 아니라 체인 검증 실패 또는 위조 의심 신호입니다.",
		strings.Join(reasons, "며 ")), true
}

// legacyTLSVersions는 폐기된 TLS 버전이다 (RFC 8996). 값은 스캐너가 기록하는 표기와 같다.
var legacyTLSVersions = map[string]bool{"TLS 1.0": true, "TLS 1.1": true}

// legacyTLSRule은 위치(엔드포인트) 축 판정이다 — 인증서가 아니라 서버 설정 문제이므로
// Scope가 location이다. 인증서를 재발급해도 해결되지 않는다.
// removed로 표시된 위치는 더 이상 인증서를 제공하지 않으므로 판정에서 뺀다.
func legacyTLSRule(locs []LocationInput) (string, bool) {
	seen := map[string]bool{}
	for _, l := range locs {
		if l.Removed || !legacyTLSVersions[l.TLSVersion] {
			continue
		}
		seen[l.TLSVersion] = true
	}
	if len(seen) == 0 {
		return "", false
	}
	versions := make([]string, 0, len(seen))
	for v := range seen {
		versions = append(versions, v)
	}
	sort.Strings(versions)
	return fmt.Sprintf(
		"이 인증서를 제공하는 위치가 %s로 협상합니다. 해당 버전은 폐기됐으므로(RFC 8996) 서버 설정에서 TLS 1.2 이상만 허용하십시오.",
		strings.Join(versions, " / ")), true
}

// incompleteChainRule은 중간 CA 누락(풀체인 미배포) 판정이다 — 위치 축이다.
// 인증서를 재발급해도 해결되지 않고 서버에 중간 CA를 함께 배포해야 한다.
//
// 판정 근거는 **그 엔드포인트가 제시한 체인만**이다 (0018 chain_complete).
// 인벤토리의 다른 인증서로 보완하면 판정이 무의미해진다 — 다른 서버가 제시한
// 중간 CA가 인벤토리에 있다는 이유로 체인이 완성돼 보이던 것이 0018 이전 상태다.
// 브라우저는 AIA로 자동 보완하지만 curl·Java·Python·모바일 클라이언트는 즉시 실패한다.
//
// ChainComplete가 nil인 위치(0018 이전 관측·파일 업로드)는 **판정하지 않는다** —
// 모르는 것을 danger로 띄우지 않는 것이 이 엔진의 원칙이다.
// removed 위치를 빼는 것은 다른 위치 축 규칙과 같다.
func incompleteChainRule(locs []LocationInput) (string, bool) {
	n := 0
	for _, l := range locs {
		if l.Removed || l.ChainComplete == nil || *l.ChainComplete {
			continue
		}
		n++
	}
	if n == 0 {
		return "", false
	}
	return fmt.Sprintf(
		"이 인증서를 제공하는 위치 %d곳이 중간 CA를 함께 제시하지 않습니다. 브라우저는 자동 보완하지만 curl·Java·Python·모바일 클라이언트는 검증에 실패하므로, 서버에 중간 CA를 포함한 전체 체인을 배포하십시오.",
		n), true
}
