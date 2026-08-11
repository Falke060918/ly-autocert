// Package certrisk는 이미 저장된 인증서 메타데이터만으로 위험 항목을 판정한다.
//
// 판정은 **저장하지 않고 조회 시 계산한다** — 판정 기준선이 2027·2030에 움직이므로
// (NIST IR 8547, CA/B Forum 수명 단축 일정) 저장하면 낡는다. 저장된 status가 아니라
// not_after로 만료를 파생 계산하는 기존 규약과 같다.
//
// 이 패키지는 DB·네트워크에 의존하지 않는다. 입력은 domain 구조체와 시각뿐이며
// 판정 함수는 전부 순수 함수다(같은 입력 → 같은 결과). 인증서 원문·개인키는
// 애초에 저장되지 않으므로 메시지에 실릴 수 없다 (보안 하드 규칙 3).
//
// 폐쇄망 사설 PKI가 주 고객이므로 **공인(publicly trusted) 인증서에만 적용되는
// 규칙은 이번 단위에 넣지 않는다** — 사설 인증서 전건이 danger가 되면 판정 자체가
// 무시된다. 규칙마다 PublicTrustOnly 구분 필드를 두어 공인 전용 규칙이 들어올
// 자리만 남긴다(이번 단위의 규칙은 전부 false).
package certrisk

import (
	"time"

	"github.com/Falke060918/INHERIT_AutoCert/backend/internal/domain"
)

// Severity는 판정 심각도다. 제품의 상태 3색 규약(danger/warn/ok)에 정보성 info를 더한 4단이다.
// 장식용 색이 아니라 사용자가 취할 행동이 갈리는 단계다.
type Severity string

const (
	// SeverityOK는 "이 항목은 문제없음"이다. 최고 심각도로 쓰이면 위험 항목이 하나도 없다는 뜻이다.
	SeverityOK Severity = "ok"
	// SeverityInfo는 지금 당장 문제는 아니지만 알고 있어야 하는 사실이다(예: 2030 사용 중단 예정).
	SeverityInfo Severity = "info"
	// SeverityWarn은 점검이 필요한 항목이다.
	SeverityWarn Severity = "warn"
	// SeverityDanger는 즉시 조치 대상이다.
	SeverityDanger Severity = "danger"
)

// severityRank는 최고 심각도 계산 순서다. 값이 클수록 심각하다.
var severityRank = map[Severity]int{
	SeverityOK: 0, SeverityInfo: 1, SeverityWarn: 2, SeverityDanger: 3,
}

// Severities는 심각도를 심각한 순으로 반환한다(집계·검증용).
func Severities() []Severity {
	return []Severity{SeverityDanger, SeverityWarn, SeverityInfo, SeverityOK}
}

// IsSeverity는 값이 정의된 심각도인지 반환한다 (API 파라미터 검증용).
func IsSeverity(v string) bool {
	_, ok := severityRank[Severity(v)]
	return ok
}

// worse는 둘 중 더 심각한 쪽을 반환한다.
func worse(a, b Severity) Severity {
	if severityRank[b] > severityRank[a] {
		return b
	}
	return a
}

// Scope는 판정 대상 축이다. 인증서 자체의 속성과 그것이 설치된 위치(엔드포인트)의
// 속성은 조치 주체가 다르므로 반드시 구분한다 — 낡은 TLS 버전은 인증서를 바꿔도
// 해결되지 않고 서버 설정을 고쳐야 한다.
type Scope string

const (
	// ScopeCertificate는 인증서 자체의 속성으로 판정한 항목이다.
	ScopeCertificate Scope = "certificate"
	// ScopeLocation은 인증서가 설치된 위치(certificate_locations)의 속성으로 판정한 항목이다.
	ScopeLocation Scope = "location"
)

// Code는 판정 항목 코드다. 화면 배지·필터의 안정적인 키이므로 값을 바꾸지 않는다.
type Code string

const (
	// CodeWeakKey는 현대 기준에 미달하는 키 길이다 (RSA < 2048 / ECDSA < 256).
	CodeWeakKey Code = "weak_key"
	// CodeKeyDeprecated2030은 2030년 사용 중단 예정 구간이다 (RSA 2048~3071 / ECDSA 256~383).
	CodeKeyDeprecated2030 Code = "key_deprecated_2030"
	// CodeKeyStrong은 2030 이후에도 유효한 키 강도다 (RSA ≥ 3072 / ECDSA ≥ 384).
	CodeKeyStrong Code = "key_strong"
	// CodeEd25519Key는 Ed25519 키다. 사설 PKI에서는 정상이나 공인 CA 발급 사례가 없다.
	CodeEd25519Key Code = "ed25519_key"
	// CodeWeakSignature는 SHA-256 미만 서명 알고리즘이다 (SHA-1/MD5/MD2/MD4).
	CodeWeakSignature Code = "weak_signature"
	// CodeNoSAN은 SAN이 하나도 없는 인증서다.
	CodeNoSAN Code = "no_san"
	// CodeWildcard는 단순 와일드카드 SAN이다 (*.example.com).
	CodeWildcard Code = "wildcard"
	// CodeWildcardBroad는 공개 접미사에 근접한 와일드카드다 (*.com, *.co.kr).
	CodeWildcardBroad Code = "wildcard_broad"
	// CodeWildcardMultiLabel은 다중 레벨·비정규 위치 와일드카드다 (*.*.x, a.*.x).
	CodeWildcardMultiLabel Code = "wildcard_multi_label"
	// CodeLongValidity는 825일을 초과하는 유효기간이다.
	CodeLongValidity Code = "long_validity"
	// CodeExpiredCA는 이미 만료된 CA 인증서다.
	CodeExpiredCA Code = "expired_ca"
	// CodeSelfSigned는 자체 서명 CA다.
	CodeSelfSigned Code = "self_signed"
	// CodeChainAnomaly는 subject=issuer인데 자체 서명 조건을 만족하지 않는 인증서다.
	CodeChainAnomaly Code = "chain_anomaly"
	// CodeLegacyTLS는 위치가 TLS 1.0/1.1로 협상하는 경우다 (위치 축).
	CodeLegacyTLS Code = "legacy_tls"
)

// Rule은 판정 항목 1개의 고정 메타데이터다.
// 코드 1개의 심각도는 **고정**이다 — 같은 코드가 상황에 따라 다른 심각도를 내면
// 화면 필터와 집계가 서로를 부정한다. 강도가 갈리는 판정은 코드를 나눈다
// (weak_key / key_deprecated_2030 / key_strong).
type Rule struct {
	Code     Code     `json:"code"`
	Severity Severity `json:"severity"`
	Scope    Scope    `json:"scope"`
	Title    string   `json:"title"`
	// PublicTrustOnly는 공인(publicly trusted) 인증서에만 적용되는 규칙인지다.
	// 사설 CA가 발급한 인증서에 공인 전용 규칙을 danger로 띄우면 전건 빨간불이
	// 되므로 화면이 이 필드로 갈라 낼 수 있어야 한다. 이번 단위의 규칙은 전부 false다.
	PublicTrustOnly bool `json:"public_trust_only"`
}

// rules는 판정 항목 카탈로그다. 이 순서가 판정 결과·집계 응답의 순서다.
var rules = []Rule{
	{CodeWeakKey, SeverityDanger, ScopeCertificate, "약한 키 길이", false},
	{CodeKeyDeprecated2030, SeverityInfo, ScopeCertificate, "2030년 사용 중단 예정 키", false},
	{CodeKeyStrong, SeverityOK, ScopeCertificate, "충분한 키 강도", false},
	{CodeEd25519Key, SeverityInfo, ScopeCertificate, "Ed25519 키", false},
	{CodeWeakSignature, SeverityDanger, ScopeCertificate, "약한 서명 알고리즘", false},
	{CodeNoSAN, SeverityDanger, ScopeCertificate, "SAN 없음", false},
	{CodeWildcardBroad, SeverityDanger, ScopeCertificate, "과도하게 넓은 와일드카드", false},
	{CodeWildcard, SeverityWarn, ScopeCertificate, "와일드카드 SAN", false},
	{CodeWildcardMultiLabel, SeverityWarn, ScopeCertificate, "다중 레벨 와일드카드", false},
	{CodeLongValidity, SeverityWarn, ScopeCertificate, "과다 유효기간", false},
	{CodeExpiredCA, SeverityDanger, ScopeCertificate, "만료된 CA", false},
	{CodeSelfSigned, SeverityWarn, ScopeCertificate, "자체 서명", false},
	{CodeChainAnomaly, SeverityDanger, ScopeCertificate, "체인 이상", false},
	{CodeLegacyTLS, SeverityDanger, ScopeLocation, "낡은 TLS 버전", false},
}

// ruleByCode는 코드 → 규칙 조회용 색인이다.
var ruleByCode = func() map[Code]Rule {
	m := make(map[Code]Rule, len(rules))
	for _, r := range rules {
		m[r.Code] = r
	}
	return m
}()

// Rules는 판정 항목 카탈로그를 고정 순서로 반환한다.
// 집계 응답이 건수 0인 항목까지 내려 화면 목록이 데이터에 따라 흔들리지 않게 한다.
func Rules() []Rule {
	out := make([]Rule, len(rules))
	copy(out, rules)
	return out
}

// LookupRule은 코드에 해당하는 규칙을 반환한다 (API 파라미터 검증용).
func LookupRule(code string) (Rule, bool) {
	r, ok := ruleByCode[Code(code)]
	return r, ok
}

// Finding은 판정 결과 1건이다.
type Finding struct {
	Code            Code     `json:"code"`
	Severity        Severity `json:"severity"`
	Scope           Scope    `json:"scope"`
	Title           string   `json:"title"`
	PublicTrustOnly bool     `json:"public_trust_only"`
	// Message는 사람이 읽는 판정 사유다. 인증서 원문·개인키는 담기지 않는다
	// (저장 자체를 하지 않으므로 담길 수 없다).
	Message string `json:"message"`
}

// Assessment는 인증서 1건의 판정 결과다.
type Assessment struct {
	// Severity는 Findings의 최고 심각도다. 판정 항목이 하나도 없으면 ok다.
	Severity Severity  `json:"severity"`
	Findings []Finding `json:"findings"`
}

// LocationInput은 위치(엔드포인트) 축 판정 입력이다.
// 인증서가 아니라 설치 위치의 속성이므로 Certificate와 분리해서 받는다.
type LocationInput struct {
	// TLSVersion은 스캔이 관측한 협상 버전 문자열이다 (예 "TLS 1.2"). 관측 이력이 없으면 빈 값.
	TLSVersion string
	// Removed는 이 위치가 더 이상 인증서를 제공하지 않는다고 판정된 상태인지다
	// (deployment_status='removed'). 사라진 위치의 옛 관측으로 danger를 띄우지 않는다.
	Removed bool
}

// Input은 판정 1회의 입력이다. Locations가 비어 있으면 위치 축 판정은 생략된다
// (목록 화면처럼 위치를 싣지 않는 경로에서 위치 판정이 거짓 음성으로 빠지는 것이
// 아니라 애초에 대상이 아니라는 뜻이다).
type Input struct {
	Certificate domain.Certificate
	Locations   []LocationInput
	// Now는 만료 판정 기준 시각이다. 제로값이면 time.Now()를 쓴다.
	Now time.Time
}

// Evaluate는 입력 1건을 판정한다. 순수 함수이며 DB·네트워크를 건드리지 않는다.
// 결과의 Findings 순서는 카탈로그(Rules) 순서로 고정된다.
func Evaluate(in Input) Assessment {
	now := in.Now
	if now.IsZero() {
		now = time.Now()
	}
	c := in.Certificate

	findings := make([]Finding, 0, 4)
	add := func(code Code, message string) {
		findings = append(findings, newFinding(code, message))
	}

	// 카탈로그 순서와 같은 순서로 판정한다.
	if code, msg, ok := keyStrengthRule(c); ok {
		add(code, msg)
	}
	if msg, ok := weakSignatureRule(c); ok {
		add(CodeWeakSignature, msg)
	}
	if msg, ok := noSANRule(c); ok {
		add(CodeNoSAN, msg)
	}
	findings = append(findings, wildcardRules(c)...)
	if msg, ok := longValidityRule(c); ok {
		add(CodeLongValidity, msg)
	}
	if msg, ok := expiredCARule(c, now); ok {
		add(CodeExpiredCA, msg)
	}
	if code, msg, ok := selfSignedRule(c); ok {
		add(code, msg)
	}
	if msg, ok := legacyTLSRule(in.Locations); ok {
		add(CodeLegacyTLS, msg)
	}

	sev := SeverityOK
	for _, f := range findings {
		sev = worse(sev, f.Severity)
	}
	return Assessment{Severity: sev, Findings: findings}
}

// newFinding은 카탈로그 메타데이터를 붙인 판정 1건을 만든다.
func newFinding(code Code, message string) Finding {
	r := ruleByCode[code]
	return Finding{
		Code:            r.Code,
		Severity:        r.Severity,
		Scope:           r.Scope,
		Title:           r.Title,
		PublicTrustOnly: r.PublicTrustOnly,
		Message:         message,
	}
}
