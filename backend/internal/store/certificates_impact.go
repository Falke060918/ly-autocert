package store

// certificates_impact.go — "이 CA가 만료되면 무엇이 영향받는가" 집계
// (GET /certificates/{id}/impact).
//
// 부모-자식 관계는 저장하지 않는다 — certificates_issuers.go의 issuedByMatchSQL
// (SKI=AKI 우선, 키 식별자가 없으면 DN 폴백)을 **재귀 CTE로 전이 확장**해 조회
// 시점에 계산한다. 새 컬럼·마이그레이션이 없다(위험 판정·서버 축과 같은 규약).
//
// 목록 API의 issued_by= 필터는 **직계 1단계**만 본다. 여기는 전이적 자손 전체이며
// GET /servers의 issued_by= 필터도 같은 계열 술어를 쓴다 — 그래서 이 응답의
// server_count는 GET /servers?issued_by=<id>의 total과 정확히 같다(같은 CTE를
// 공유해 계산한다). 두 수가 갈리면 화면이 서로를 부정한다.
//
// 집합 규약(하나로 통일한다):
//   - 영향 집합 = 대상 인증서 자신 + 전이적 자손. 리프 수·도메인·서버·만료 분포는
//     전부 이 집합에서 status='replaced'를 뺀 것 위에서 센다(대시보드 요약·발급
//     기관 분포와 같은 규약). 대상이 CA가 아니면 자손이 0건이고 자기 배포분만 남는다.
//   - descendant_ca_count만 replaced를 포함한다 — CA 트리가 "교체된 CA도 이력"으로
//     전부 보여주는 것과 같다.
//
// 조회 전용이라 감사 이벤트를 남기지 않는다(00-spec 7.12 기록 대상에 조회 없음).
// 응답에 인증서 원문(DER/PEM)·개인키는 없고 로그에도 남기지 않는다.

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"
)

// impactListLimit은 도메인·서버 목록에 실어 보내는 최대 행 수다.
// 넘치면 잘라 보내고 *_truncated를 세운다 — 목록 API처럼 페이지네이션하지 않는
// 이유는 이 응답이 "한 화면짜리 영향 요약"이기 때문이다. 전체를 훑어야 하면
// 도메인은 GET /certificates?issued_by=, 서버는 GET /servers?issued_by=로 간다
// (후자는 여기와 같은 계열 술어라 total이 이 응답의 server_count와 같다).
const impactListLimit = 200

// certFamilySQL은 "대상 인증서와 그 전이적 자손"을 만드는 재귀 CTE 본문이다.
// $1::uuid(대상 인증서 id)를 받으며 호출자가 `WITH RECURSIVE fam AS ( … )`
// 자리에 그대로 끼운다 (certFamilyCTEs).
//
// 순환 차단은 2중이다: 경로 배열 방문 검사(교차 서명된 CA 쌍)와 깊이 상한
// maxChainDepth(certificates_chain.go와 같은 값). 자체 서명 루트는 AKI=자기 SKI라
// 스스로를 발급한 것으로 잡히므로 child.id <> ca.id로 제외한다(목록 issued_by
// 필터와 같은 규약). 같은 키로 재발급된 동일 DN CA가 자손으로 잡히는 것은
// 의도된 동작이다 — 그 키가 만료되면 실제로 함께 영향받는다.
//
// 성능: 재귀 단계마다 child.authority_key_id·child.issuer_dn으로 탐색하므로
// 두 컬럼의 인덱스가 이 쿼리의 비용을 지배한다.
var certFamilySQL = fmt.Sprintf(`
		SELECT c.id, 0 AS depth, ARRAY[c.id] AS path, false AS descendant
		  FROM certificates c
		 WHERE c.id = $1::uuid
		 UNION ALL
		SELECT child.id, f.depth + 1, f.path || child.id, true
		  FROM fam f
		  JOIN certificates ca ON ca.id = f.id AND ca.certificate_type = 'ca'
		  JOIN certificates child
		    ON child.id <> ca.id
		   AND NOT (child.id = ANY(f.path))
		   AND %s
		 WHERE f.depth < %d`, issuedByMatchSQL, maxChainDepth)

// certFamilyCTEs는 WITH 절에 끼울 계열 CTE 2개다 — fam(자신+전이적 자손)과
// fam_live(그중 replaced를 뺀 id 집합). 영향 집계와 GET /servers의 issued_by=
// 필터가 **같은 문자열**을 공유한다.
var certFamilyCTEs = fmt.Sprintf(`fam AS (%s
	), fam_live AS (
		SELECT DISTINCT f.id
		  FROM fam f
		  JOIN certificates c ON c.id = f.id AND c.status <> 'replaced'
	)`, certFamilySQL)

// certImpactCTEs는 영향 집계 전용 CTE 묶음이다(계열 + 인증서 속성 + 서버 축
// 엔드포인트). endpoint CTE는 서버 목록(servers.go)이 쓰는 것과 같은 정의라
// 여기의 서버·엔드포인트 수가 서버 화면과 같은 판정 위에서 세어진다.
var certImpactCTEs = "WITH RECURSIVE " + certFamilyCTEs + `, famc AS (
		SELECT id, bool_or(descendant) AS descendant, max(depth) AS depth
		  FROM fam GROUP BY id
	), impact AS (
		SELECT fc.id, fc.descendant, fc.depth, c.certificate_type, c.status,
		       c.not_after, c.common_name, c.sans
		  FROM famc fc JOIN certificates c ON c.id = fc.id
	), ` + strings.TrimPrefix(serverEndpointCTE(""), "WITH ")

// ImpactSubject는 영향을 물은 인증서 자신의 요약이다(원문 없음).
type ImpactSubject struct {
	ID              string    `json:"id"`
	CommonName      string    `json:"common_name"`
	SubjectDN       string    `json:"subject_dn"`
	NotAfter        time.Time `json:"not_after"`
	Status          string    `json:"status"`
	CertificateType string    `json:"certificate_type"`
	SelfSigned      bool      `json:"self_signed"`
	IsCA            bool      `json:"is_ca"`
}

// ImpactExpiry는 영향 집합의 만료 분포다. N일 구간은 **누적**이고(30d는 7d 포함)
// 이미 만료된 것은 구간에서 빠진다 — 대시보드 요약과 같은 규약.
type ImpactExpiry struct {
	Expired   int `json:"expired"`
	Within7d  int `json:"within_7d"`
	Within30d int `json:"within_30d"`
	Within90d int `json:"within_90d"`
}

// ImpactDomain은 영향받는 도메인 1건이다. 이름은 **비-CA 인증서의** CN과 SAN을
// 합쳐 소문자로 정규화한 값이다 — CA의 CN("Corp Root CA")은 도메인이 아니라
// 집계에서 뺀다. SAN에 실린 IP·메일 주소는 파서가 넣은 대로 들어온다.
type ImpactDomain struct {
	Name             string `json:"name"`
	CertificateCount int    `json:"certificate_count"`
	// ServerCount는 그 이름을 가진 인증서가 지금 붙어 있는 서버 수다(0일 수 있다 —
	// 파일 업로드만 되고 어디에도 배포되지 않은 인증서).
	ServerCount int `json:"server_count"`
}

// CertificateImpact는 "이 인증서가 만료되면 영향받는 것" 집계 결과다.
type CertificateImpact struct {
	Certificate ImpactSubject `json:"certificate"`
	// DescendantCACount는 전이적 자손 CA 수다(자신 제외, replaced 포함).
	DescendantCACount int `json:"descendant_ca_count"`
	// DescendantLeafCount는 전이적 자손 비-CA 인증서 수다(replaced 제외).
	DescendantLeafCount int `json:"descendant_leaf_count"`
	// CertificateCount는 영향 집합 전체 크기다(자신 포함, replaced 제외).
	CertificateCount int `json:"certificate_count"`
	// MaxDepth는 자손까지의 최대 발급 단계다(자신만이면 0).
	MaxDepth int `json:"max_depth"`
	// EndpointCount는 영향 집합이 지금 붙어 있는 (호스트, 포트) 수다.
	EndpointCount int `json:"endpoint_count"`
	// ServerCount는 GET /servers?issued_by=<id>의 total과 같다.
	ServerCount      int              `json:"server_count"`
	DomainCount      int              `json:"domain_count"`
	Expiry           ImpactExpiry     `json:"expiry"`
	Domains          []ImpactDomain   `json:"domains"`
	DomainsTruncated bool             `json:"domains_truncated"`
	Servers          []ServerListItem `json:"servers"`
	ServersTruncated bool             `json:"servers_truncated"`
	// ListLimit은 domains·servers에 실은 최대 행 수다(*_truncated의 기준).
	ListLimit int `json:"list_limit"`
}

// CertificateImpact는 인증서 1건의 영향 범위를 집계한다.
// 잘못된 형식의 id는 존재하지 않는 리소스로 취급한다(uuid 캐스트 오류 방지).
func (s *Store) CertificateImpact(ctx context.Context, id string) (*CertificateImpact, error) {
	id = strings.ToLower(strings.TrimSpace(id))
	if !uuidRe.MatchString(id) {
		return nil, ErrNotFound
	}

	res := &CertificateImpact{
		Domains:   []ImpactDomain{},
		Servers:   []ServerListItem{},
		ListLimit: impactListLimit,
	}
	subject, err := s.impactSubject(ctx, id)
	if err != nil {
		return nil, err
	}
	res.Certificate = subject

	if err := s.impactCounts(ctx, id, res); err != nil {
		return nil, err
	}
	domains, total, err := s.impactDomains(ctx, id)
	if err != nil {
		return nil, err
	}
	res.Domains, res.DomainCount = domains, total
	res.DomainsTruncated = total > len(domains)

	// 서버 목록은 서버 축 조회를 그대로 재사용한다 — 같은 필터·같은 정렬이라
	// "영향받는 서버 12곳"을 눌러 GET /servers?issued_by=로 넘어가도 첫 페이지가
	// 같은 순서로 이어진다.
	servers, err := s.ListServers(ctx, ListServersInput{
		IssuedBy: id, Page: 1, PageSize: impactListLimit,
	})
	if err != nil {
		return nil, err
	}
	res.Servers, res.ServerCount = servers.Items, servers.Total
	res.ServersTruncated = servers.Total > len(servers.Items)
	return res, nil
}

// impactSubject는 대상 인증서 요약을 읽는다(존재 검사 겸용).
func (s *Store) impactSubject(ctx context.Context, id string) (ImpactSubject, error) {
	var it ImpactSubject
	err := s.db.QueryRowContext(ctx, `
		SELECT id::text, common_name, subject_dn, not_after, status, certificate_type,
		       (subject_dn = issuer_dn
		        AND (authority_key_id IS NULL OR subject_key_id IS NULL
		             OR authority_key_id = subject_key_id)),
		       certificate_type = 'ca'
		  FROM certificates
		 WHERE id = $1::uuid`, id,
	).Scan(&it.ID, &it.CommonName, &it.SubjectDN, &it.NotAfter, &it.Status,
		&it.CertificateType, &it.SelfSigned, &it.IsCA)
	if errors.Is(err, sql.ErrNoRows) {
		return it, ErrNotFound
	}
	if err != nil {
		return it, fmt.Errorf("store: impact subject: %w", err)
	}
	return it, nil
}

// impactCounts는 계열 규모·엔드포인트 수·만료 분포를 단일 쿼리로 센다.
func (s *Store) impactCounts(ctx context.Context, id string, out *CertificateImpact) error {
	err := s.db.QueryRowContext(ctx, certImpactCTEs+`
		SELECT
			(SELECT count(*) FROM impact WHERE descendant AND certificate_type = 'ca'),
			(SELECT count(*) FROM impact
			  WHERE descendant AND certificate_type <> 'ca' AND status <> 'replaced'),
			(SELECT count(*) FROM impact WHERE status <> 'replaced'),
			(SELECT coalesce(max(depth), 0) FROM impact),
			(SELECT count(*) FROM endpoint e
			  WHERE e.certificate_id IN (SELECT id FROM fam_live)),
			(SELECT count(*) FROM impact
			  WHERE status <> 'replaced' AND not_after < now()),
			(SELECT count(*) FROM impact
			  WHERE status <> 'replaced' AND not_after >= now()
			    AND not_after < now() + interval '7 days'),
			(SELECT count(*) FROM impact
			  WHERE status <> 'replaced' AND not_after >= now()
			    AND not_after < now() + interval '30 days'),
			(SELECT count(*) FROM impact
			  WHERE status <> 'replaced' AND not_after >= now()
			    AND not_after < now() + interval '90 days')`, id,
	).Scan(&out.DescendantCACount, &out.DescendantLeafCount, &out.CertificateCount,
		&out.MaxDepth, &out.EndpointCount,
		&out.Expiry.Expired, &out.Expiry.Within7d, &out.Expiry.Within30d, &out.Expiry.Within90d)
	if err != nil {
		return fmt.Errorf("store: impact counts: %w", err)
	}
	return nil
}

// impactDomains는 영향받는 도메인 목록(상위 limit건)과 전체 개수를 조회한다.
// 전체 개수는 LIMIT 앞에서 계산되는 윈도우 함수로 같은 쿼리에서 얻는다 —
// 별도 count 쿼리를 두면 두 수가 다른 스냅샷 위에서 나온다.
func (s *Store) impactDomains(ctx context.Context, id string) ([]ImpactDomain, int, error) {
	rows, err := s.db.QueryContext(ctx, certImpactCTEs+`, dom AS (
			SELECT d.name,
			       count(DISTINCT i.id) AS certificate_count,
			       count(DISTINCT e.target_id) AS server_count
			  FROM impact i
			  CROSS JOIN LATERAL (
			       SELECT lower(btrim(i.common_name)) AS name
			        UNION
			       SELECT lower(btrim(s)) FROM unnest(i.sans) AS s
			  ) d
			  LEFT JOIN endpoint e ON e.certificate_id = i.id
			 WHERE i.status <> 'replaced' AND i.certificate_type <> 'ca' AND d.name <> ''
			 GROUP BY d.name
		)
		SELECT name, certificate_count, server_count, count(*) OVER () AS total
		  FROM dom
		 ORDER BY certificate_count DESC, name ASC
		 LIMIT $2`, id, impactListLimit)
	if err != nil {
		return nil, 0, fmt.Errorf("store: impact domains: %w", err)
	}
	defer rows.Close() //nolint:errcheck

	domains := []ImpactDomain{} // 빈 결과도 JSON []로 나가도록
	total := 0
	for rows.Next() {
		var d ImpactDomain
		if err := rows.Scan(&d.Name, &d.CertificateCount, &d.ServerCount, &total); err != nil {
			return nil, 0, fmt.Errorf("store: scan impact domain: %w", err)
		}
		domains = append(domains, d)
	}
	if err := rows.Err(); err != nil {
		return nil, 0, fmt.Errorf("store: iterate impact domains: %w", err)
	}
	return domains, total, nil
}
