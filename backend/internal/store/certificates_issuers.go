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

// maxCATreeDepth는 트리 중첩 상한이다. 실제 계층은 있는 만큼 다 내려가며
// (평탄화하지 않는다), 이 값은 교차 서명·손상 데이터로 만들어진 병적으로 긴
// 사슬에서 재귀가 폭주하지 않게 막는 안전 상한일 뿐이다. 순환 자체는 상한이
// 아니라 부모 사슬 검사로 끊는다(buildCATree). 체인 조회(maxChainDepth=8)의
// 두 배를 둔 것은, 체인은 "루트까지 몇 단계"고 여기는 "인벤토리 전체 계보"라
// 같은 값을 쓰면 정상 데이터가 잘릴 여지가 더 크기 때문이다.
const maxCATreeDepth = 2 * maxChainDepth

// caLeafPreviewLimit는 노드 1개에 싣는 리프 미리보기 최대 건수다.
// 전체 리프는 목록 API(GET /certificates?issued_by=<노드 id>)가 페이지네이션과
// 함께 내려준다 — 트리 응답에 인벤토리 전체를 실으면 노드 수 × 리프 수가 된다.
const caLeafPreviewLimit = 5

// CALeafPreview는 CA 노드가 직접 발급한 리프 1건의 미리보기다.
// 트리를 펼쳤을 때 "무엇이 달려 있나"를 즉시 보여주기 위한 최소 필드만 담는다.
type CALeafPreview struct {
	ID         string    `json:"id"`
	CommonName string    `json:"common_name"`
	SubjectDN  string    `json:"subject_dn"`
	NotAfter   time.Time `json:"not_after"`
	Status     string    `json:"status"`
}

// CATreeNode는 CA 트리 1노드다. 루트(자체 서명 CA) 아래로 **실제 계층 그대로**
// 중간 CA가 중첩된다 — 중간의 중간도 제 부모 밑에 붙는다(깊이 제한 없음,
// maxCATreeDepth는 병적 데이터용 안전 상한).
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
	// Depth는 이 노드가 놓인 계층이다(루트 0). 중첩 구조에서 파생 가능하지만,
	// 응답만 보고 계층이 실제로 살아 있는지 확인할 수 있게 명시한다.
	Depth int `json:"depth"`
	// LeafCount는 이 CA가 직접 발급한 비-CA 인증서 수다(replaced 제외).
	// 매칭은 findIssuer의 역방향: SKI=AKI 우선, 키 식별자 부재 시 DN 폴백.
	LeafCount int `json:"leaf_count"`
	// SubtreeLeafCount는 이 노드와 **모든** 하위 CA의 LeafCount 합산이다
	// (손자·증손자 포함). 화면의 "하위 포함 N건" 표기용.
	SubtreeLeafCount int `json:"subtree_leaf_count"`
	// LeafPreview는 이 노드가 직접 발급한 리프 중 만료가 가장 임박한
	// 최대 caLeafPreviewLimit건이다. LeafCount가 이 길이보다 크면 나머지는
	// GET /certificates?issued_by=<id>로 이어서 조회한다.
	LeafPreview []CALeafPreview `json:"leaf_preview"`
	// Children은 이 노드가 직접 발급한 하위 CA들이다.
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
func (s *Store) CertificateIssuers(ctx context.Context, scopes ...CertificateScope) (*CertificateIssuers, error) {
	scope := selectedCertificateScope(scopes)
	res := &CertificateIssuers{
		Organizations: []IssuerOrgCount{},
		CATree:        []CATreeNode{},
	}

	// ① 발급 기관 분포 — 비-CA 인증서만 센다. 체인으로 수집된 CA·루트 행까지
	// 세면 "내 인증서가 어느 기관에서 왔나"가 아니라 보유 CA 수가 섞인다.
	rows, err := s.db.QueryContext(ctx, `
		SELECT issuer_organization, count(*)
		  FROM certificates
 WHERE certificate_type <> 'ca' AND `+certificateAggregateScopeSQL(scope, "certificates")+`
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

	// ② CA 인증서 전체 + 각자의 직접 발급 리프 수 (단일 쿼리) + 노드별 리프 미리보기.
	cas, err := s.listCAIssuers(ctx, scope)
	if err != nil {
		return nil, err
	}
	previews, err := s.listCALeafPreviews(ctx, scope)
	if err != nil {
		return nil, err
	}
	res.CATree = buildCATree(cas, previews)

	// ③ 어떤 보유 CA와도 매칭되지 않는 리프 수.
	err = s.db.QueryRowContext(ctx, `
		SELECT count(*)
		  FROM certificates child
		 WHERE child.certificate_type <> 'ca' AND `+certificateAggregateScopeSQL(scope, "child")+`
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
func (s *Store) listCAIssuers(ctx context.Context, scope CertificateScope) ([]caIssuerRow, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT ca.id, ca.subject_dn, ca.issuer_dn, ca.common_name, ca.not_after,
		       ca.certificate_type, ca.status, ca.subject_key_id, ca.authority_key_id,
		       count(child.id)
		  FROM certificates ca
		  LEFT JOIN certificates child
		    ON child.certificate_type <> 'ca' AND `+certificateAggregateScopeSQL(scope, "child")+`
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

// listCALeafPreviews는 CA 노드별 리프 미리보기(노드당 최대 caLeafPreviewLimit건)를
// 한 쿼리로 모은다. 노드마다 따로 조회하면 CA 수만큼 왕복이 되고, 상한 없이 전부
// 실으면 응답이 인벤토리 전체가 된다. 순서는 **만료 임박순**이다 — 미리보기의
// 용도가 "이 CA 밑에서 먼저 손봐야 할 게 뭔가"이기 때문이다.
func (s *Store) listCALeafPreviews(ctx context.Context, scope CertificateScope) (map[string][]CALeafPreview, error) {
	rows, err := s.db.QueryContext(ctx, `
		WITH ranked AS (
			SELECT ca.id AS ca_id, child.id AS leaf_id, child.common_name,
			       child.subject_dn, child.not_after, child.status,
			       row_number() OVER (PARTITION BY ca.id
			                          ORDER BY child.not_after ASC, child.id ASC) AS rn
			  FROM certificates ca
			  JOIN certificates child
			    ON child.certificate_type <> 'ca' AND `+certificateAggregateScopeSQL(scope, "child")+`
			   AND child.id <> ca.id AND `+issuedByMatchSQL+`
			 WHERE ca.certificate_type = 'ca'
		)
		SELECT ca_id, leaf_id, common_name, subject_dn, not_after, status
		  FROM ranked
		 WHERE rn <= $1::int
		 ORDER BY ca_id, rn`, caLeafPreviewLimit)
	if err != nil {
		return nil, fmt.Errorf("store: list ca leaf previews: %w", err)
	}
	defer rows.Close() //nolint:errcheck

	previews := map[string][]CALeafPreview{}
	for rows.Next() {
		var caID string
		var p CALeafPreview
		if err := rows.Scan(&caID, &p.ID, &p.CommonName, &p.SubjectDN, &p.NotAfter, &p.Status); err != nil {
			return nil, fmt.Errorf("store: scan ca leaf preview: %w", err)
		}
		previews[caID] = append(previews[caID], p)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("store: iterate ca leaf previews: %w", err)
	}
	return previews, nil
}

// buildCATree는 CA 목록을 **실제 계층 그대로의 N계층 트리**로 만든다.
// 상위 탐색은 메모리에서 findIssuer와 같은 우선순위(SKI 정확 일치 → 유효(미만료)
// → not_after 최신)로 한 단계씩만 고른다 — 예전처럼 루트까지 올라가 평탄화하지
// 않는다. 순환은 부모 사슬을 훑어 닫는 엣지를 끊고(breakParentCycles),
// maxCATreeDepth를 넘는 사슬은 상한 지점에서 잘라 그 노드를 최상위로 올린다
// (버리지 않는다 — 노드 유실이 평탄화보다 나쁘다).
func buildCATree(cas []caIssuerRow, previews map[string][]CALeafPreview) []CATreeNode {
	// ① 각 CA의 직속 부모를 한 단계만 찾는다.
	parentOf := make(map[string]string, len(cas))
	for _, c := range cas {
		if c.selfSigned() {
			continue
		}
		if p, ok := findIssuerInMemory(c, cas, map[string]bool{c.id: true}); ok {
			parentOf[c.id] = p.id
		}
	}
	breakParentCycles(cas, parentOf)
	capParentDepth(cas, parentOf)

	// ② 부모별 자식 목록. cas의 정렬 순서를 그대로 이어받아 결정론적이다.
	childrenOf := map[string][]caIssuerRow{}
	for _, c := range cas {
		if p, ok := parentOf[c.id]; ok {
			childrenOf[p] = append(childrenOf[p], c)
		}
	}

	// ③ 최상위: 자체 서명 루트 + 상위를 보유하지 않은 CA(미보유 루트 placeholder 아래).
	roots := []CATreeNode{}
	orphanIdx := map[string]int{} // issuer_dn → roots 인덱스
	for _, c := range cas {
		if _, hasParent := parentOf[c.id]; hasParent {
			continue
		}
		node := buildCASubtree(c, childrenOf, previews, 0, map[string]bool{})
		if c.selfSigned() {
			roots = append(roots, node)
			continue
		}
		// 보유하지 않은 상위 — 하위가 아는 issuer_dn 자리 노드 아래로 묶는다.
		i, ok := orphanIdx[c.issuerDN]
		if !ok {
			roots = append(roots, CATreeNode{
				SubjectDN: c.issuerDN, LeafPreview: []CALeafPreview{}, Children: []CATreeNode{}})
			i = len(roots) - 1
			orphanIdx[c.issuerDN] = i
		}
		reparent(&node, 1)
		roots[i].Children = append(roots[i].Children, node)
	}

	// ④ placeholder 루트의 하위 합산 확정 + 전체 정렬.
	for i := range roots {
		if !roots[i].Stored {
			roots[i].SubtreeLeafCount = subtreeLeafTotal(roots[i])
		}
		sortCAChildren(&roots[i])
	}
	sortCANodes(roots)
	return roots
}

// buildCASubtree는 c를 뿌리로 하위 CA를 재귀적으로 붙인다. visited는 순환을
// 이미 끊은 뒤의 방어선이고, depth는 maxCATreeDepth 안전 상한이다.
func buildCASubtree(c caIssuerRow, childrenOf map[string][]caIssuerRow,
	previews map[string][]CALeafPreview, depth int, visited map[string]bool) CATreeNode {
	node := toCATreeNode(c)
	node.Depth = depth
	node.LeafPreview = previews[c.id]
	if node.LeafPreview == nil {
		node.LeafPreview = []CALeafPreview{}
	}
	if visited[c.id] || depth >= maxCATreeDepth {
		return node
	}
	visited[c.id] = true
	for _, child := range childrenOf[c.id] {
		sub := buildCASubtree(child, childrenOf, previews, depth+1, visited)
		node.SubtreeLeafCount += sub.SubtreeLeafCount
		node.Children = append(node.Children, sub)
	}
	delete(visited, c.id)
	return node
}

// reparent는 placeholder 아래로 옮긴 서브트리의 Depth를 다시 매긴다.
func reparent(n *CATreeNode, depth int) {
	n.Depth = depth
	for i := range n.Children {
		reparent(&n.Children[i], depth+1)
	}
}

func subtreeLeafTotal(n CATreeNode) int {
	total := n.LeafCount
	for _, c := range n.Children {
		total += subtreeLeafTotal(c)
	}
	return total
}

// sortCAChildren은 하위 정렬을 재귀 적용한다: 하위 포함 리프 수 내림차순 →
// subject_dn 오름차순. 최상위와 같은 기준이라 계층이 깊어져도 규칙이 하나다.
func sortCAChildren(n *CATreeNode) {
	sortCANodes(n.Children)
	for i := range n.Children {
		sortCAChildren(&n.Children[i])
	}
}

func sortCANodes(nodes []CATreeNode) {
	sort.SliceStable(nodes, func(a, b int) bool {
		if nodes[a].SubtreeLeafCount != nodes[b].SubtreeLeafCount {
			return nodes[a].SubtreeLeafCount > nodes[b].SubtreeLeafCount
		}
		return nodes[a].SubjectDN < nodes[b].SubjectDN
	})
}

// breakParentCycles는 부모 사슬을 따라가다 이미 지나온 노드로 돌아오면 그
// 엣지를 버린다(교차 서명 데이터에서 A→B→A가 나올 수 있다). 엣지를 잃은
// 노드는 최상위로 올라오므로 노드 자체는 유실되지 않는다.
func breakParentCycles(cas []caIssuerRow, parentOf map[string]string) {
	for _, c := range cas {
		seen := map[string]bool{c.id: true}
		cur := c.id
		for {
			p, ok := parentOf[cur]
			if !ok {
				break
			}
			if seen[p] {
				delete(parentOf, cur)
				break
			}
			seen[p] = true
			cur = p
		}
	}
}

// capParentDepth는 maxCATreeDepth를 넘는 사슬의 엣지를 끊어 그 아래를 최상위로
// 올린다. 정상 데이터(체인 2~4단)는 걸리지 않으며, 손상 데이터에서 재귀가
// 폭주하는 것만 막는다.
func capParentDepth(cas []caIssuerRow, parentOf map[string]string) {
	for _, c := range cas {
		depth := 0
		cur := c.id
		for {
			p, ok := parentOf[cur]
			if !ok {
				break
			}
			depth++
			if depth > maxCATreeDepth {
				delete(parentOf, cur)
				break
			}
			cur = p
		}
	}
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
		// 하위 합산은 buildCASubtree가 자식을 붙이면서 더한다(잎 노드는 이 값 그대로).
		SubtreeLeafCount: c.leafCount,
		LeafPreview:      []CALeafPreview{},
		Children:         []CATreeNode{},
	}
}
