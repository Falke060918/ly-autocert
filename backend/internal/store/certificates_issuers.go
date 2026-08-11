package store

// certificates_issuers.go — 발급 기관 분포·CA 트리 집계 (GET /certificates/issuers).
// 전부 파생 집계이며 저장 컬럼을 새로 만들지 않는다. 조회 전용이라 감사 이벤트 없음.
//
// replaced 제외 규약: 대시보드 요약(certificates_summary.go)과 같은 이유로
// 리프 카운트·분포에서 status='replaced'를 제외한다 — 교체 감지가 죽었다고
// 판정한 인증서가 분포에 남으면 목록 화면(프론트 기본 exclude_status=replaced)과
// 숫자가 어긋난다. CA 노드 자체는 상태를 실어 전부 보여준다(교체된 CA도 이력이다).

import (
	"context"
	"fmt"
	"sort"
	"time"
)

// IssuerOrgCount는 발급 기관 1개의 인증서 수다. Organization이 nil이면
// issuer_dn에 O가 없는(또는 아직 백필되지 않은) 인증서 묶음이다.
type IssuerOrgCount struct {
	Organization *string `json:"organization"`
	Count        int     `json:"count"`
}

// CATreeNode는 CA 트리 1노드다. 루트(자체 서명 CA) 아래에 그 계보의 중간 CA가
// 평탄화되어 붙는 2계층 구조다 — 중간의 중간도 루트 바로 아래로 붙는다.
// 인벤토리에 없는 상위(중간 CA만 보유한 경우의 루트)는 Stored=false·ID nil로,
// 하위가 아는 issuer_dn만 담는다 (certificates_chain.go ChainItem과 같은 규약).
type CATreeNode struct {
	ID         *string    `json:"id"`
	Stored     bool       `json:"stored"`
	SubjectDN  string     `json:"subject_dn"`
	CommonName *string    `json:"common_name"`
	NotAfter   *time.Time `json:"not_after"`
	Status     *string    `json:"status"`
	SelfSigned bool       `json:"self_signed"`
	// LeafCount는 이 CA가 직접 발급한 비-CA 인증서 수다(replaced 제외).
	// 매칭은 findIssuer의 역방향: SKI=AKI 우선, 키 식별자 부재 시 DN 폴백.
	LeafCount int `json:"leaf_count"`
	// SubtreeLeafCount는 이 노드와 하위(Children) CA의 LeafCount 합산이다.
	// 루트의 "하위 포함" 표기용 — 중간 CA는 손자가 없어(2계층 평탄화) LeafCount와 같다.
	SubtreeLeafCount int `json:"subtree_leaf_count"`
	// Children은 이 루트 계보의 중간 CA들이다(루트 노드에서만 비어 있지 않다).
	Children []CATreeNode `json:"children"`
}

// CertificateIssuers는 발급 기관 집계 결과다.
type CertificateIssuers struct {
	Organizations []IssuerOrgCount `json:"organizations"`
	CATree        []CATreeNode     `json:"ca_tree"`
	// UnmatchedLeafCount는 인벤토리의 어떤 CA와도 발급 관계가 잡히지 않는
	// 비-CA 인증서 수다(체인 없이 단독 업로드된 리프 등, replaced 제외).
	UnmatchedLeafCount int `json:"unmatched_leaf_count"`
}

// issuedByMatchSQL은 "ca가 child를 발급했다" 술어다 — certificates_chain.go
// findIssuer의 역방향과 같은 규칙(SKI=AKI 정확 일치 우선, 키 식별자가 없으면
// DN 폴백). ListCertificates의 issued_by 필터와 같은 관계를 봐야 트리에서
// 노드를 클릭해 이동한 목록의 건수가 노드의 leaf_count와 일치한다.
const issuedByMatchSQL = `(
	    (ca.subject_key_id IS NOT NULL AND child.authority_key_id = ca.subject_key_id)
	 OR (child.issuer_dn = ca.subject_dn
	     AND (child.authority_key_id IS NULL OR ca.subject_key_id IS NULL))
	)`

// caIssuerRow는 트리 구성에 쓰는 CA 인증서 1행이다(chainRow + 리프 수).
type caIssuerRow struct {
	chainRow
	leafCount int
}

// CertificateIssuers는 발급 기관 분포와 CA 트리를 집계한다.
func (s *Store) CertificateIssuers(ctx context.Context) (*CertificateIssuers, error) {
	res := &CertificateIssuers{
		Organizations: []IssuerOrgCount{},
		CATree:        []CATreeNode{},
	}

	// ① 발급 기관 분포 — 비-CA 인증서만 센다. 체인으로 수집된 CA·루트 행까지
	// 세면 "내 인증서가 어느 기관에서 왔나"가 아니라 보유 CA 수가 섞인다.
	rows, err := s.db.QueryContext(ctx, `
		SELECT issuer_organization, count(*)
		  FROM certificates
		 WHERE certificate_type <> 'ca' AND status <> 'replaced'
		 GROUP BY issuer_organization
		 ORDER BY count(*) DESC, issuer_organization ASC NULLS LAST`)
	if err != nil {
		return nil, fmt.Errorf("store: issuer organizations: %w", err)
	}
	defer rows.Close() //nolint:errcheck
	for rows.Next() {
		var oc IssuerOrgCount
		if err := rows.Scan(&oc.Organization, &oc.Count); err != nil {
			return nil, fmt.Errorf("store: scan issuer organization: %w", err)
		}
		res.Organizations = append(res.Organizations, oc)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("store: iterate issuer organizations: %w", err)
	}

	// ② CA 인증서 전체 + 각자의 직접 발급 리프 수 (단일 쿼리).
	cas, err := s.listCAIssuers(ctx)
	if err != nil {
		return nil, err
	}
	res.CATree = buildCATree(cas)

	// ③ 어떤 보유 CA와도 매칭되지 않는 리프 수.
	err = s.db.QueryRowContext(ctx, `
		SELECT count(*)
		  FROM certificates child
		 WHERE child.certificate_type <> 'ca' AND child.status <> 'replaced'
		   AND NOT EXISTS (
			SELECT 1 FROM certificates ca
			 WHERE ca.certificate_type = 'ca' AND ca.id <> child.id
			   AND `+issuedByMatchSQL+`)`).Scan(&res.UnmatchedLeafCount)
	if err != nil {
		return nil, fmt.Errorf("store: unmatched leaf count: %w", err)
	}
	return res, nil
}

// listCAIssuers는 CA 인증서 전체와 직접 발급 리프 수를 조회한다.
func (s *Store) listCAIssuers(ctx context.Context) ([]caIssuerRow, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT ca.id, ca.subject_dn, ca.issuer_dn, ca.common_name, ca.not_after,
		       ca.certificate_type, ca.status, ca.subject_key_id, ca.authority_key_id,
		       count(child.id)
		  FROM certificates ca
		  LEFT JOIN certificates child
		    ON child.certificate_type <> 'ca' AND child.status <> 'replaced'
		   AND child.id <> ca.id AND `+issuedByMatchSQL+`
		 WHERE ca.certificate_type = 'ca'
		 GROUP BY ca.id
		 ORDER BY ca.subject_dn ASC, ca.not_after DESC, ca.id ASC`)
	if err != nil {
		return nil, fmt.Errorf("store: list ca issuers: %w", err)
	}
	defer rows.Close() //nolint:errcheck

	var cas []caIssuerRow
	for rows.Next() {
		var c caIssuerRow
		if err := rows.Scan(&c.id, &c.subjectDN, &c.issuerDN, &c.cn, &c.notAfter,
			&c.ctype, &c.status, &c.ski, &c.aki, &c.leafCount); err != nil {
			return nil, fmt.Errorf("store: scan ca issuer: %w", err)
		}
		cas = append(cas, c)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("store: iterate ca issuers: %w", err)
	}
	return cas, nil
}

// buildCATree는 CA 목록을 루트→중간 2계층 트리로 만든다. 상위 탐색은 메모리에서
// findIssuer와 같은 우선순위(SKI 정확 일치 → 유효(미만료) → not_after 최신)로
// 고르고, maxChainDepth·방문 집합으로 순환을 끊는다 (certificates_chain.go 규약).
func buildCATree(cas []caIssuerRow) []CATreeNode {
	roots := []CATreeNode{}
	rootIdx := map[string]int{}   // 보유 루트: CA id → roots 인덱스
	orphanIdx := map[string]int{} // 미보유 루트 placeholder: issuer_dn → roots 인덱스
	addStoredRoot := func(c caIssuerRow) int {
		if i, ok := rootIdx[c.id]; ok {
			return i
		}
		roots = append(roots, toCATreeNode(c))
		rootIdx[c.id] = len(roots) - 1
		return len(roots) - 1
	}

	// 루트를 먼저 세운다 — 자식 없는 루트도 노드로 나와야 한다.
	for _, c := range cas {
		if c.selfSigned() {
			addStoredRoot(c)
		}
	}
	// 중간 CA는 계보의 루트를 찾아 그 아래에 평탄화해 붙인다.
	for _, c := range cas {
		if c.selfSigned() {
			continue
		}
		top, found := climbToRoot(c, cas)
		var i int
		if found {
			i = addStoredRoot(top)
		} else {
			// 보유하지 않은 루트 — 마지막으로 아는 상위의 issuer_dn placeholder.
			dn := top.issuerDN
			var ok bool
			if i, ok = orphanIdx[dn]; !ok {
				roots = append(roots, CATreeNode{SubjectDN: dn, Children: []CATreeNode{}})
				i = len(roots) - 1
				orphanIdx[dn] = i
			}
		}
		roots[i].Children = append(roots[i].Children, toCATreeNode(c))
	}

	// 하위 포함 합산 확정 + 정렬: 계보 리프 합 내림차순 → subject_dn.
	// 자식은 리프 수 내림차순 → subject_dn.
	for i := range roots {
		roots[i].SubtreeLeafCount = treeLeafTotal(roots[i])
		sort.SliceStable(roots[i].Children, func(a, b int) bool {
			ca, cb := roots[i].Children[a], roots[i].Children[b]
			if ca.LeafCount != cb.LeafCount {
				return ca.LeafCount > cb.LeafCount
			}
			return ca.SubjectDN < cb.SubjectDN
		})
	}
	sort.SliceStable(roots, func(a, b int) bool {
		ta, tb := roots[a].SubtreeLeafCount, roots[b].SubtreeLeafCount
		if ta != tb {
			return ta > tb
		}
		return roots[a].SubjectDN < roots[b].SubjectDN
	})
	return roots
}

func treeLeafTotal(n CATreeNode) int {
	total := n.LeafCount
	for _, c := range n.Children {
		total += c.LeafCount
	}
	return total
}

// climbToRoot는 중간 CA에서 루트 방향으로 올라간다. 보유 루트에 닿으면
// (그 루트, true), 상위가 끊기면 (마지막으로 아는 CA, false)를 반환한다.
func climbToRoot(c caIssuerRow, cas []caIssuerRow) (caIssuerRow, bool) {
	cur := c
	visited := map[string]bool{cur.id: true}
	for depth := 0; depth < maxChainDepth; depth++ {
		parent, ok := findIssuerInMemory(cur, cas, visited)
		if !ok {
			return cur, false
		}
		if parent.selfSigned() {
			return parent, true
		}
		visited[parent.id] = true
		cur = parent
	}
	return cur, false
}

// findIssuerInMemory는 findIssuer(SQL)와 같은 술어·우선순위의 메모리 구현이다.
func findIssuerInMemory(child caIssuerRow, cas []caIssuerRow, visited map[string]bool) (caIssuerRow, bool) {
	var best caIssuerRow
	bestRank := -1
	now := time.Now()
	for _, cand := range cas {
		if visited[cand.id] {
			continue
		}
		skiMatch := child.aki != nil && cand.ski != nil && *cand.ski == *child.aki
		dnMatch := cand.subjectDN == child.issuerDN && (child.aki == nil || cand.ski == nil)
		if !skiMatch && !dnMatch {
			continue
		}
		// 우선순위: SKI 정확 일치(4) > 유효(2) > 없음(0). 동률이면 not_after 최신.
		rank := 0
		if skiMatch {
			rank += 4
		}
		if cand.notAfter.After(now) {
			rank += 2
		}
		if rank > bestRank || (rank == bestRank && cand.notAfter.After(best.notAfter)) {
			best, bestRank = cand, rank
		}
	}
	return best, bestRank >= 0
}

func toCATreeNode(c caIssuerRow) CATreeNode {
	item := toChainItem(c.chainRow)
	return CATreeNode{
		ID:         item.ID,
		Stored:     true,
		SubjectDN:  item.SubjectDN,
		CommonName: item.CommonName,
		NotAfter:   item.NotAfter,
		Status:     item.Status,
		SelfSigned: item.SelfSigned,
		LeafCount:  c.leafCount,
		// 하위 합산은 buildCATree가 자식을 붙인 뒤 확정한다(중간 CA는 이 값 그대로).
		SubtreeLeafCount: c.leafCount,
		Children:         []CATreeNode{},
	}
}
