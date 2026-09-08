package store

// 위험 판정 SQL 술어 + 집계.
//
// 판정 자체의 정본은 순수 패키지 internal/certrisk다 — 응답에 실리는 판정 결과는
// 전부 그쪽이 계산한다. 이 파일은 **같은 판정을 SQL로 다시 표현한 것**이며 오직
// 목록 필터·집계 카운트에만 쓴다. 두 구현이 갈라지면 "화면엔 danger인데 필터로는
// 안 잡히는" 상태가 되므로 certificates_risk_test.go의 교차 검증이 픽스처 전건에서
// 두 결과가 같은지 고정한다 — 규칙을 추가할 때는 반드시 같은 커밋에 양쪽을 넣는다.
//
// SQL로 옮긴 이유는 페이지네이션이다. Go에서 걸러 내면 total·page가 필터를 반영하지
// 못하고, 인벤토리 전체를 메모리로 끌어와야 한다.

import (
	"context"
	"fmt"
	"strings"

	"github.com/Falke060918/INHERIT_AutoCert/backend/internal/certrisk"
)

// riskWildcardSQL은 SAN 와일드카드 판정의 공통 골격이다.
// certrisk.classifyWildcard와 같은 순서로 판정한다:
// 소문자화 → 별표 개수 → 맨 앞 레이블 여부 → 뒤 레이블 수.
const riskWildcardSQL = `EXISTS (
		SELECT 1 FROM unnest(certificates.sans) AS s(n)
		 CROSS JOIN LATERAL (SELECT btrim(lower(s.n)) AS ln) AS w
		 CROSS JOIN LATERAL (
			SELECT string_to_array(w.ln, '.') AS lb,
			       length(w.ln) - length(replace(w.ln, '*', '')) AS stars
		 ) AS v
		 WHERE position('*' in w.ln) > 0 AND (%s))`

// riskWildcardSimpleShape는 "맨 앞 레이블 1개에만 별표"인 정규 와일드카드 형태다.
const riskWildcardSimpleShape = `v.stars = 1 AND position('*' in v.lb[1]) > 0`

// riskWildcardBroadBase는 공개 접미사 근접 판정이다 (레이블 수 휴리스틱).
// 뒤 레이블이 1개 이하이거나, 2개이면서 TLD가 2글자 ccTLD이고 앞이 준-레지스트리 레이블일 때다.
const riskWildcardBroadBase = `(
			coalesce(array_length(v.lb, 1), 0) - 1 <= 1
			OR (coalesce(array_length(v.lb, 1), 0) - 1 = 2
			    AND length(v.lb[3]) = 2
			    AND v.lb[2] IN ('co','com','ne','net','or','org','ac','edu','go','gov','mil','sch'))
		)`

// riskSelfSignedShape는 "진짜 자체 서명"의 조건이다 (subject=issuer 비교는 뺀 부분).
// AKI가 없거나 자기 SKI와 같고 CA 인증서여야 한다.
const riskSelfSignedShape = `(certificates.certificate_type = 'ca'
		AND (certificates.authority_key_id IS NULL
		     OR certificates.authority_key_id = ''
		     OR certificates.authority_key_id IS NOT DISTINCT FROM certificates.subject_key_id))`

// riskLegacyTLSSQL은 위치 축 판정이다. removed 위치의 옛 관측은 세지 않는다.
const riskLegacyTLSSQL = `EXISTS (
		SELECT 1 FROM certificate_locations l
		 WHERE l.certificate_id = certificates.id
		   AND l.deployment_status <> 'removed'
		   AND l.tls_version IN ('TLS 1.0', 'TLS 1.1'))`

// riskIncompleteChainSQL은 중간 CA 누락 판정이다 (0018, 위치 축).
// `= false`이므로 NULL(미상 — 0018 이전 관측·파일 업로드)은 잡히지 않는다.
// certrisk.incompleteChainRule이 nil을 판정에서 빼는 것과 같은 기준이다.
const riskIncompleteChainSQL = `EXISTS (
		SELECT 1 FROM certificate_locations l
		 WHERE l.certificate_id = certificates.id
		   AND l.deployment_status <> 'removed'
		   AND l.chain_complete = false)`

// riskPredicates는 판정 코드별 SQL 술어다 (certrisk.Rules()의 코드와 1:1이어야 한다).
var riskPredicates = map[certrisk.Code]string{
	certrisk.CodeWeakKey: `((upper(certificates.public_key_algorithm) = 'RSA'
			AND certificates.public_key_size > 0 AND certificates.public_key_size < 2048)
		 OR (upper(certificates.public_key_algorithm) = 'ECDSA'
			AND certificates.public_key_size > 0 AND certificates.public_key_size < 256))`,

	certrisk.CodeKeyDeprecated2030: `((upper(certificates.public_key_algorithm) = 'RSA'
			AND certificates.public_key_size >= 2048 AND certificates.public_key_size < 3072)
		 OR (upper(certificates.public_key_algorithm) = 'ECDSA'
			AND certificates.public_key_size >= 256 AND certificates.public_key_size < 384))`,

	certrisk.CodeKeyStrong: `((upper(certificates.public_key_algorithm) = 'RSA'
			AND certificates.public_key_size >= 3072)
		 OR (upper(certificates.public_key_algorithm) = 'ECDSA'
			AND certificates.public_key_size >= 384))`,

	certrisk.CodeEd25519Key: `upper(certificates.public_key_algorithm) = 'ED25519'`,

	certrisk.CodeWeakSignature: `upper(replace(replace(replace(
			certificates.signature_algorithm, '-', ''), ' ', ''), '_', '')) ~ '(MD2|MD4|MD5|SHA1)'`,

	certrisk.CodeNoSAN: `coalesce(array_length(certificates.sans, 1), 0) = 0`,

	certrisk.CodeLongValidity: `certificates.not_after - certificates.not_before > interval '825 days'`,

	certrisk.CodeExpiredCA: `(certificates.certificate_type = 'ca' AND certificates.not_after < now())`,

	certrisk.CodeSelfSigned: `(certificates.subject_dn = certificates.issuer_dn
		AND ` + riskSelfSignedShape + `)`,

	certrisk.CodeChainAnomaly: `(certificates.subject_dn = certificates.issuer_dn
		AND NOT ` + riskSelfSignedShape + `)`,

	certrisk.CodeLegacyTLS: riskLegacyTLSSQL,

	certrisk.CodeIncompleteChain: riskIncompleteChainSQL,
}

func init() {
	// 와일드카드 3종은 공통 골격에서 파생한다.
	riskPredicates[certrisk.CodeWildcardMultiLabel] = fmt.Sprintf(riskWildcardSQL,
		`v.stars > 1 OR position('*' in v.lb[1]) = 0`)
	riskPredicates[certrisk.CodeWildcardBroad] = fmt.Sprintf(riskWildcardSQL,
		riskWildcardSimpleShape+" AND "+riskWildcardBroadBase)
	riskPredicates[certrisk.CodeWildcard] = fmt.Sprintf(riskWildcardSQL,
		riskWildcardSimpleShape+" AND NOT "+riskWildcardBroadBase)

	// 카탈로그에 있는데 술어가 없으면(또는 반대면) 필터가 조용히 아무것도 안 거른다.
	// 기동 시점에 터뜨려 배포 전에 잡는다.
	for _, r := range certrisk.Rules() {
		if _, ok := riskPredicates[r.Code]; !ok {
			panic("store: missing risk SQL predicate for code " + string(r.Code))
		}
	}
	if len(riskPredicates) != len(certrisk.Rules()) {
		panic("store: risk SQL predicates do not match the certrisk catalog")
	}
}

// riskCodePredicate는 판정 코드 1개의 SQL 술어를 반환한다.
func riskCodePredicate(code certrisk.Code) (string, bool) {
	p, ok := riskPredicates[code]
	return p, ok
}

// riskSeverityUnion은 해당 심각도 규칙들의 OR 술어다. 규칙이 없으면 ("", false).
func riskSeverityUnion(sev certrisk.Severity) (string, bool) {
	var parts []string
	for _, r := range certrisk.Rules() {
		if r.Severity != sev {
			continue
		}
		parts = append(parts, riskPredicates[r.Code])
	}
	if len(parts) == 0 {
		return "", false
	}
	return "(" + strings.Join(parts, "\n\t\tOR ") + ")", true
}

// riskSeverityPredicate는 "이 인증서의 **최고** 심각도가 sev인가"를 판정한다.
// 최고 심각도 기준이라 danger/warn/info/ok가 인벤토리를 겹침 없이 분할한다 —
// 대시보드 KPI 숫자의 합이 전체와 맞아야 클릭 이동이 배신하지 않는다.
// 심각도 ok는 "위험 항목이 하나도 없음"이다(ok 등급 판정 항목의 유무와 무관).
func riskSeverityPredicate(sev certrisk.Severity) string {
	var above []string // sev보다 심각한 등급들의 union
	for _, s := range certrisk.Severities() {
		if s == certrisk.SeverityOK {
			break // ok 등급 판정 항목은 최고 심각도를 올리지 않는다
		}
		u, ok := riskSeverityUnion(s)
		if !ok {
			continue
		}
		if s == sev {
			pred := u
			for _, a := range above {
				pred += " AND NOT " + a
			}
			return "(" + pred + ")"
		}
		above = append(above, u)
	}
	// 여기까지 왔으면 sev == ok다 — 위험 항목이 하나도 없는 경우.
	if len(above) == 0 {
		return "TRUE"
	}
	pred := "NOT " + above[0]
	for _, a := range above[1:] {
		pred += " AND NOT " + a
	}
	return "(" + pred + ")"
}

// buildRiskConds는 목록 조회의 위험 필터 조건을 만든다.
// 심각도 필터와 항목 코드 필터는 서로 독립이며 둘 다 지정하면 AND로 겹친다
// (같은 계열 안에서는 OR — 다른 필터들과 같은 규약).
func buildRiskConds(in ListCertificatesInput) []string {
	var conds []string
	if len(in.RiskSeverities) > 0 {
		parts := make([]string, 0, len(in.RiskSeverities))
		for _, s := range in.RiskSeverities {
			parts = append(parts, riskSeverityPredicate(s))
		}
		conds = append(conds, "("+strings.Join(parts, " OR ")+")")
	}
	if len(in.RiskCodes) > 0 {
		var parts []string
		for _, c := range in.RiskCodes {
			if p, ok := riskCodePredicate(c); ok {
				parts = append(parts, p)
			}
		}
		if len(parts) > 0 {
			conds = append(conds, "("+strings.Join(parts, " OR ")+")")
		}
	}
	return conds
}

// RiskSeverityCount는 최고 심각도별 인증서 수다.
type RiskSeverityCount struct {
	Severity certrisk.Severity `json:"severity"`
	Count    int               `json:"count"`
}

// RiskFindingCount는 판정 항목별 인증서 수다 (카탈로그 메타데이터 포함).
type RiskFindingCount struct {
	certrisk.Rule
	Count int `json:"count"`
}

// CertificateRiskSummary는 위험 판정 집계 결과다.
type CertificateRiskSummary struct {
	Total int `json:"total"`
	// Severities는 최고 심각도 기준 분할이라 Count의 합이 Total과 같다.
	Severities []RiskSeverityCount `json:"severities"`
	// Findings는 항목별 건수다. 인증서 1건이 여러 항목에 걸리므로 합은 Total과 다르다.
	// 건수 0인 항목도 카탈로그 순서 그대로 내려보낸다(화면 목록이 데이터에 따라 흔들리지 않게).
	Findings []RiskFindingCount `json:"findings"`
}

// CertificateRisks는 인벤토리 전체의 위험 판정을 단일 쿼리로 집계한다.
// status=replaced는 제외한다 — 교체된 인증서는 만료 알림·대시보드 요약에서 이미
// 빠져 있어 여기만 남기면 화면들이 서로를 부정한다(대시보드 요약과 같은 규약).
// 따라서 이 집계에서 목록으로 이동할 때는 exclude_status=replaced를 함께 보낸다.
// 조회 전용이라 감사 이벤트를 남기지 않는다 (00-spec 7.12 기록 대상에 조회 없음).
func (s *Store) CertificateRisks(ctx context.Context, scopes ...CertificateScope) (*CertificateRiskSummary, error) {
	severities := certrisk.Severities()
	rules := certrisk.Rules()

	exprs := []string{"count(*)"}
	for _, sev := range severities {
		exprs = append(exprs, "count(*) FILTER (WHERE "+riskSeverityPredicate(sev)+")")
	}
	for _, r := range rules {
		exprs = append(exprs, "count(*) FILTER (WHERE "+riskPredicates[r.Code]+")")
	}

	out := &CertificateRiskSummary{
		Severities: make([]RiskSeverityCount, len(severities)),
		Findings:   make([]RiskFindingCount, len(rules)),
	}
	dest := make([]any, 0, len(exprs))
	dest = append(dest, &out.Total)
	for i := range severities {
		out.Severities[i].Severity = severities[i]
		dest = append(dest, &out.Severities[i].Count)
	}
	for i := range rules {
		out.Findings[i].Rule = rules[i]
		dest = append(dest, &out.Findings[i].Count)
	}

	query := "SELECT " + strings.Join(exprs, ",\n\t") +
		"\n FROM certificates WHERE " + certificateAggregateScopeSQL(selectedCertificateScope(scopes), "certificates")
	if err := s.db.QueryRowContext(ctx, query).Scan(dest...); err != nil {
		return nil, fmt.Errorf("store: certificate risks: %w", err)
	}
	return out, nil
}
