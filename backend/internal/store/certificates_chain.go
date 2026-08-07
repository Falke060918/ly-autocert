package store

// certificates_chain.go — 인증서 체인 관계 조회 (02-goals M3).
// 업로드 시 체인은 개별 Certificate 레코드로 분해되므로, 상세 조회 시점에
// 발급자 관계를 재구성한다. 상위(발급자) 매칭 규칙:
//   1) 리프의 AKI = 상위의 SKI 정확 일치 (우선)
//   2) 키 식별자가 없으면(0008 이전 데이터·확장 없는 인증서) issuer_dn = subject_dn 폴백
// 양쪽 모두 키 식별자를 갖고 있는데 값이 다르면 DN이 같아도 상위로 보지 않는다
// (동일 DN 재발급 CA 오매칭 방지).

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/Falke060918/INHERIT_AutoCert/backend/internal/domain"
)

// maxChainDepth는 체인 탐색 깊이 상한이다. 실제 체인은 2~4단계가 일반적이며,
// 교차 서명 순환 데이터가 있어도 이 상한과 방문 집합이 무한 탐색을 막는다.
const maxChainDepth = 8

// ChainItem은 체인 1단계다. 첫 항목은 조회한 인증서 자신이고 상위로 갈수록 뒤다.
// 인벤토리에 없는 상위는 Stored=false로 남기며 SubjectDN(하위의 issuer_dn)만 안다.
type ChainItem struct {
	ID              *string    `json:"id"` // 미보유면 nil
	Stored          bool       `json:"stored"`
	SubjectDN       string     `json:"subject_dn"`
	CommonName      *string    `json:"common_name"`
	NotAfter        *time.Time `json:"not_after"`
	CertificateType *string    `json:"certificate_type"`
	Status          *string    `json:"status"`
	SelfSigned      bool       `json:"self_signed"` // 루트 판정 (subject=issuer, 키 식별자 모순 없음)
}

// chainRow는 체인 탐색에 필요한 최소 필드다.
type chainRow struct {
	id        string
	subjectDN string
	issuerDN  string
	cn        string
	notAfter  time.Time
	ctype     string
	status    string
	ski       *string
	aki       *string
}

func chainRowFromCertificate(c domain.Certificate) chainRow {
	return chainRow{
		id: c.ID, subjectDN: c.SubjectDN, issuerDN: c.IssuerDN, cn: c.CommonName,
		notAfter: c.NotAfter, ctype: string(c.CertificateType), status: string(c.Status),
		ski: c.SubjectKeyID, aki: c.AuthorityKeyID,
	}
}

// selfSigned는 자체 서명(=루트 후보) 여부다. DN이 같아도 AKI·SKI가 모두 있고
// 값이 다르면 자체 서명이 아니다 (동일 DN으로 교차 서명된 경우).
func (r chainRow) selfSigned() bool {
	if r.subjectDN != r.issuerDN {
		return false
	}
	if r.aki != nil && r.ski != nil && *r.aki != *r.ski {
		return false
	}
	return true
}

func toChainItem(r chainRow) ChainItem {
	cn, notAfter, ctype, status := r.cn, r.notAfter, r.ctype, r.status
	id := r.id
	return ChainItem{
		ID: &id, Stored: true, SubjectDN: r.subjectDN,
		CommonName: &cn, NotAfter: &notAfter, CertificateType: &ctype, Status: &status,
		SelfSigned: r.selfSigned(),
	}
}

// getCertificateChain은 cert에서 루트 방향으로 체인을 만든다 (자신 포함).
// 상위가 인벤토리에 없으면 미보유 항목(Stored=false)으로 끝난다.
func (s *Store) getCertificateChain(ctx context.Context, c domain.Certificate) ([]ChainItem, error) {
	cur := chainRowFromCertificate(c)
	chain := []ChainItem{toChainItem(cur)}
	visited := []string{cur.id}
	for len(chain) < maxChainDepth {
		if cur.selfSigned() {
			return chain, nil // 루트 도달
		}
		parent, err := s.findIssuer(ctx, cur, visited)
		if errors.Is(err, sql.ErrNoRows) {
			// 상위 미보유 — DN만 아는 자리 항목으로 종료.
			chain = append(chain, ChainItem{SubjectDN: cur.issuerDN})
			return chain, nil
		}
		if err != nil {
			return nil, err
		}
		visited = append(visited, parent.id)
		chain = append(chain, toChainItem(parent))
		cur = parent
	}
	return chain, nil
}

// findIssuer는 child의 상위 인증서 1건을 찾는다. 없으면 sql.ErrNoRows.
// 후보가 여럿이면 SKI 정확 일치 → 유효(미만료) → not_after 최신 순으로 고른다.
func (s *Store) findIssuer(ctx context.Context, child chainRow, visited []string) (chainRow, error) {
	var p chainRow
	err := s.db.QueryRowContext(ctx, `
		SELECT id, subject_dn, issuer_dn, common_name, not_after, certificate_type, status,
		       subject_key_id, authority_key_id
		  FROM certificates
		 WHERE certificate_type = 'ca'
		   AND NOT (id = ANY(string_to_array($1, chr(31))::uuid[]))
		   AND (
		        ($2::text IS NOT NULL AND subject_key_id = $2)
		     OR (subject_dn = $3 AND ($2::text IS NULL OR subject_key_id IS NULL))
		   )
		 ORDER BY (subject_key_id IS NOT NULL AND subject_key_id = $2::text) DESC,
		          (not_after >= now()) DESC, not_after DESC, id
		 LIMIT 1`,
		strings.Join(visited, sansSep), child.aki, child.issuerDN,
	).Scan(&p.id, &p.subjectDN, &p.issuerDN, &p.cn, &p.notAfter, &p.ctype, &p.status,
		&p.ski, &p.aki)
	if errors.Is(err, sql.ErrNoRows) {
		return p, err
	}
	if err != nil {
		return p, fmt.Errorf("store: find issuer: %w", err)
	}
	return p, nil
}
