package store

// 발급 기관(0016) 저장·필터·집계 검증.
// 픽스처는 전부 실행 시점에 생성한다 (보안 하드 규칙 4).

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"crypto/x509/pkix"
	"math/big"
	"testing"
	"time"

	"github.com/Falke060918/INHERIT_AutoCert/backend/internal/certparse"
	"github.com/Falke060918/INHERIT_AutoCert/backend/internal/domain"
)

// newTestIssuerChain은 루트 CA(O=rootOrg) → 중간 CA(O=issuingOrg) → 리프 2개
// 체인을 만든다. 리프의 issuer_organization은 issuingOrg가 된다.
func newTestIssuerChain(t *testing.T, rootOrg, issuingOrg string) (root, intermediate, leaf1, leaf2 domain.Certificate) {
	t.Helper()
	newKey := func() *ecdsa.PrivateKey {
		key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
		if err != nil {
			t.Fatalf("generate key: %v", err)
		}
		return key
	}
	mkCert := func(tmpl, parent *x509.Certificate, pub *ecdsa.PublicKey, signer *ecdsa.PrivateKey) *x509.Certificate {
		der, err := x509.CreateCertificate(rand.Reader, tmpl, parent, pub, signer)
		if err != nil {
			t.Fatalf("create certificate: %v", err)
		}
		cert, err := x509.ParseCertificate(der)
		if err != nil {
			t.Fatalf("parse certificate: %v", err)
		}
		return cert
	}

	rootKey, intKey, leafKey1, leafKey2 := newKey(), newKey(), newKey(), newKey()
	rootTmpl := &x509.Certificate{
		SerialNumber:          big.NewInt(200),
		Subject:               pkix.Name{CommonName: "issuers-fixture-root", Organization: []string{rootOrg}},
		NotBefore:             time.Now().Add(-time.Hour),
		NotAfter:              time.Now().Add(3650 * 24 * time.Hour),
		KeyUsage:              x509.KeyUsageCertSign,
		IsCA:                  true,
		BasicConstraintsValid: true,
	}
	rootCert := mkCert(rootTmpl, rootTmpl, &rootKey.PublicKey, rootKey)

	intTmpl := &x509.Certificate{
		SerialNumber:          big.NewInt(201),
		Subject:               pkix.Name{CommonName: "issuers-fixture-int", Organization: []string{issuingOrg}},
		NotBefore:             time.Now().Add(-time.Hour),
		NotAfter:              time.Now().Add(1825 * 24 * time.Hour),
		KeyUsage:              x509.KeyUsageCertSign,
		IsCA:                  true,
		BasicConstraintsValid: true,
	}
	intCert := mkCert(intTmpl, rootCert, &intKey.PublicKey, rootKey)

	leafTmpl := func(serial int64, cn string) *x509.Certificate {
		return &x509.Certificate{
			SerialNumber: big.NewInt(serial),
			Subject:      pkix.Name{CommonName: cn},
			NotBefore:    time.Now().Add(-time.Hour),
			NotAfter:     time.Now().Add(90 * 24 * time.Hour),
			KeyUsage:     x509.KeyUsageDigitalSignature,
			ExtKeyUsage:  []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
			DNSNames:     []string{cn},
		}
	}
	leafCert1 := mkCert(leafTmpl(202, "issuers-leaf-1.example.test"), intCert, &leafKey1.PublicKey, intKey)
	leafCert2 := mkCert(leafTmpl(203, "issuers-leaf-2.example.test"), intCert, &leafKey2.PublicKey, intKey)

	now := time.Now()
	return certparse.Describe(rootCert, now), certparse.Describe(intCert, now),
		certparse.Describe(leafCert1, now), certparse.Describe(leafCert2, now)
}

// TestIssuerOrganizationPersistedOnInsert는 upsert INSERT 경로가
// issuer_organization을 실제로 저장하는지 DB 컬럼으로 검증한다 —
// 백필만 통과하고 신규 행이 NULL로 남는 0015 origin 전례 방지.
func TestIssuerOrganizationPersistedOnInsert(t *testing.T) {
	pool := testPool(t)
	st := New(pool)
	ctx := context.Background()

	root, intermediate, leaf1, _ := newTestIssuerChain(t, "Issuers Root Org", "Issuers Signing Org")
	reqID := "test-req-issuer-org-insert"
	fileName := "issuers-fixture.pem"
	t.Cleanup(func() {
		_, _ = pool.Exec(`DELETE FROM certificates WHERE fingerprint_sha256 IN ($1, $2, $3)`,
			root.FingerprintSHA256, intermediate.FingerprintSHA256, leaf1.FingerprintSHA256)
		cleanupAuditByRequestID(pool, reqID)
	})

	if _, err := st.ImportCertificates(ctx, ImportInput{
		Certificates: []domain.Certificate{root, intermediate, leaf1},
		FileName:     &fileName,
		RequestID:    &reqID,
	}); err != nil {
		t.Fatalf("import chain: %v", err)
	}

	var got *string
	if err := pool.QueryRow(`SELECT issuer_organization FROM certificates WHERE fingerprint_sha256 = $1`,
		leaf1.FingerprintSHA256).Scan(&got); err != nil {
		t.Fatalf("read issuer_organization: %v", err)
	}
	if got == nil || *got != "Issuers Signing Org" {
		t.Errorf("leaf issuer_organization = %v, want %q", got, "Issuers Signing Org")
	}
	if err := pool.QueryRow(`SELECT issuer_organization FROM certificates WHERE fingerprint_sha256 = $1`,
		intermediate.FingerprintSHA256).Scan(&got); err != nil {
		t.Fatalf("read intermediate issuer_organization: %v", err)
	}
	if got == nil || *got != "Issuers Root Org" {
		t.Errorf("intermediate issuer_organization = %v, want %q", got, "Issuers Root Org")
	}
}

// TestListCertificatesIssuerFilters는 issuer_org 정확 일치(빈 문자열은
// "조직 미상" IS NULL)와 issued_by(SKI=AKI 매칭) 필터를 검증한다.
func TestListCertificatesIssuerFilters(t *testing.T) {
	pool := testPool(t)
	st := New(pool)
	ctx := context.Background()

	root, intermediate, leaf1, leaf2 := newTestIssuerChain(t, "Filter Root Org", "Filter Signing Org")
	standalone := newTestCert(t) // 발급자 DN에 O가 없다 → issuer_organization NULL
	reqID := "test-req-issuer-filters"
	fileName := "issuers-filter-fixture.pem"
	t.Cleanup(func() {
		_, _ = pool.Exec(`DELETE FROM certificates WHERE fingerprint_sha256 IN ($1, $2, $3, $4, $5)`,
			root.FingerprintSHA256, intermediate.FingerprintSHA256,
			leaf1.FingerprintSHA256, leaf2.FingerprintSHA256, standalone.FingerprintSHA256)
		cleanupAuditByRequestID(pool, reqID)
	})

	res, err := st.ImportCertificates(ctx, ImportInput{
		Certificates: []domain.Certificate{root, intermediate, leaf1, leaf2, standalone},
		FileName:     &fileName,
		RequestID:    &reqID,
	})
	if err != nil {
		t.Fatalf("import chain: %v", err)
	}
	ids := map[string]string{} // fingerprint → id
	for _, ic := range res.Certificates {
		ids[ic.Certificate.FingerprintSHA256] = ic.Certificate.ID
	}

	org := "Filter Signing Org"
	list, err := st.ListCertificates(ctx, ListCertificatesInput{IssuerOrg: &org})
	if err != nil {
		t.Fatalf("list by issuer_org: %v", err)
	}
	if list.Total != 2 {
		t.Errorf("issuer_org filter total = %d, want 2", list.Total)
	}
	for _, item := range list.Items {
		if item.IssuerOrganization == nil || *item.IssuerOrganization != org {
			t.Errorf("issuer_org filter returned certificate with issuer_organization = %v", item.IssuerOrganization)
		}
	}

	// 빈 문자열은 "조직 미상"(issuer_organization IS NULL) 필터다 —
	// nil(필터 없음)과 구분된다. 분포의 "조직 미상" 행 클릭 경로.
	emptyOrg := ""
	list, err = st.ListCertificates(ctx, ListCertificatesInput{IssuerOrg: &emptyOrg, PageSize: 200})
	if err != nil {
		t.Fatalf("list by empty issuer_org: %v", err)
	}
	foundStandalone := false
	for _, item := range list.Items {
		if item.IssuerOrganization != nil {
			t.Errorf("issuer_org=\"\" returned certificate with issuer_organization = %q", *item.IssuerOrganization)
		}
		if item.ID == ids[standalone.FingerprintSHA256] {
			foundStandalone = true
		}
	}
	if !foundStandalone {
		t.Errorf("issuer_org=\"\" filter did not return the NULL-organization fixture")
	}

	intID := ids[intermediate.FingerprintSHA256]
	list, err = st.ListCertificates(ctx, ListCertificatesInput{IssuedBy: &intID})
	if err != nil {
		t.Fatalf("list by issued_by: %v", err)
	}
	if list.Total != 2 {
		t.Fatalf("issued_by(intermediate) total = %d, want 2 leaves", list.Total)
	}
	wantLeaves := map[string]bool{ids[leaf1.FingerprintSHA256]: true, ids[leaf2.FingerprintSHA256]: true}
	for _, item := range list.Items {
		if !wantLeaves[item.ID] {
			t.Errorf("issued_by(intermediate) returned unexpected certificate %s", item.ID)
		}
	}

	// 루트가 발급한 것은 중간 CA 하나뿐이어야 한다(자기 자신은 제외).
	rootID := ids[root.FingerprintSHA256]
	list, err = st.ListCertificates(ctx, ListCertificatesInput{IssuedBy: &rootID})
	if err != nil {
		t.Fatalf("list by issued_by root: %v", err)
	}
	if list.Total != 1 || list.Items[0].ID != intID {
		t.Errorf("issued_by(root) = %d rows, want exactly the intermediate", list.Total)
	}
}

// TestCertificateIssuersAggregation은 분포·CA 트리·미매칭 카운트를 검증한다.
func TestCertificateIssuersAggregation(t *testing.T) {
	pool := testPool(t)
	st := New(pool)
	ctx := context.Background()

	root, intermediate, leaf1, leaf2 := newTestIssuerChain(t, "Agg Root Org", "Agg Signing Org")
	standalone := newTestCert(t) // 자체 서명 리프 — 어떤 CA와도 매칭되지 않는다.
	reqID := "test-req-issuer-agg"
	fileName := "issuers-agg-fixture.pem"
	t.Cleanup(func() {
		_, _ = pool.Exec(`DELETE FROM certificates WHERE fingerprint_sha256 IN ($1, $2, $3, $4, $5)`,
			root.FingerprintSHA256, intermediate.FingerprintSHA256,
			leaf1.FingerprintSHA256, leaf2.FingerprintSHA256, standalone.FingerprintSHA256)
		cleanupAuditByRequestID(pool, reqID)
	})

	res, err := st.ImportCertificates(ctx, ImportInput{
		Certificates: []domain.Certificate{root, intermediate, leaf1, leaf2, standalone},
		FileName:     &fileName,
		RequestID:    &reqID,
	})
	if err != nil {
		t.Fatalf("import fixtures: %v", err)
	}
	ids := map[string]string{}
	for _, ic := range res.Certificates {
		ids[ic.Certificate.FingerprintSHA256] = ic.Certificate.ID
	}

	agg, err := st.CertificateIssuers(ctx)
	if err != nil {
		t.Fatalf("certificate issuers: %v", err)
	}

	// 분포: 발급 org "Agg Signing Org"의 비-CA 인증서는 리프 2개다
	// (CA 행은 분포에서 제외 — 보유 CA 수가 섞이면 안 된다).
	foundOrg := false
	for _, oc := range agg.Organizations {
		if oc.Organization != nil && *oc.Organization == "Agg Signing Org" {
			foundOrg = true
			if oc.Count != 2 {
				t.Errorf("org %q count = %d, want 2", *oc.Organization, oc.Count)
			}
		}
		if oc.Organization != nil && *oc.Organization == "Agg Root Org" {
			t.Errorf("org %q must not appear — it only issued a CA", *oc.Organization)
		}
	}
	if !foundOrg {
		t.Error("organizations missing entry for Agg Signing Org")
	}

	// 트리: 루트 아래에 중간 CA가 붙고, 리프 수는 중간 2 / 루트 0이다.
	var rootNode *CATreeNode
	for i := range agg.CATree {
		if agg.CATree[i].ID != nil && *agg.CATree[i].ID == ids[root.FingerprintSHA256] {
			rootNode = &agg.CATree[i]
		}
	}
	if rootNode == nil {
		t.Fatal("ca_tree missing fixture root node")
	}
	if !rootNode.Stored || !rootNode.SelfSigned {
		t.Errorf("root node stored=%v self_signed=%v, want true/true", rootNode.Stored, rootNode.SelfSigned)
	}
	if rootNode.LeafCount != 0 {
		t.Errorf("root leaf_count = %d, want 0 (중간 CA는 리프가 아니다)", rootNode.LeafCount)
	}
	// 루트의 subtree_leaf_count는 하위 중간 CA 발급분까지 합산해야 한다 —
	// 직접 발급(leaf_count=0)만 응답에 실으면 화면의 "하위 포함 N건"이 거짓이 된다.
	if rootNode.SubtreeLeafCount != 2 {
		t.Errorf("root subtree_leaf_count = %d, want 2 (하위 중간 CA 리프 합산)", rootNode.SubtreeLeafCount)
	}
	if len(rootNode.Children) != 1 {
		t.Fatalf("root children = %d, want 1", len(rootNode.Children))
	}
	child := rootNode.Children[0]
	if child.ID == nil || *child.ID != ids[intermediate.FingerprintSHA256] {
		t.Errorf("root child = %v, want intermediate", child.ID)
	}
	if child.LeafCount != 2 {
		t.Errorf("intermediate leaf_count = %d, want 2", child.LeafCount)
	}
	// 이 픽스처의 중간 CA는 하위 CA가 없으므로 subtree = 직접 발급이다.
	if child.SubtreeLeafCount != 2 {
		t.Errorf("intermediate subtree_leaf_count = %d, want 2", child.SubtreeLeafCount)
	}

	// 단독 자체 서명 리프는 어떤 CA와도 매칭되지 않는다.
	if agg.UnmatchedLeafCount < 1 {
		t.Errorf("unmatched_leaf_count = %d, want >= 1", agg.UnmatchedLeafCount)
	}
}

// newTestDeepIssuerChain은 루트 → 중간1 → 중간2 → 리프 4단 체인을 만든다.
// 2계층 평탄화라면 중간2가 루트 바로 아래로 올라와 계층이 사라진다.
func newTestDeepIssuerChain(t *testing.T, tag string) (root, int1, int2, leaf domain.Certificate) {
	t.Helper()
	newKey := func() *ecdsa.PrivateKey {
		key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
		if err != nil {
			t.Fatalf("generate key: %v", err)
		}
		return key
	}
	mkCert := func(tmpl, parent *x509.Certificate, pub *ecdsa.PublicKey, signer *ecdsa.PrivateKey) *x509.Certificate {
		der, err := x509.CreateCertificate(rand.Reader, tmpl, parent, pub, signer)
		if err != nil {
			t.Fatalf("create certificate: %v", err)
		}
		cert, err := x509.ParseCertificate(der)
		if err != nil {
			t.Fatalf("parse certificate: %v", err)
		}
		return cert
	}
	caTmpl := func(serial int64, cn string) *x509.Certificate {
		return &x509.Certificate{
			SerialNumber:          big.NewInt(serial),
			Subject:               pkix.Name{CommonName: cn, Organization: []string{tag + " Org"}},
			NotBefore:             time.Now().Add(-time.Hour),
			NotAfter:              time.Now().Add(3650 * 24 * time.Hour),
			KeyUsage:              x509.KeyUsageCertSign,
			IsCA:                  true,
			BasicConstraintsValid: true,
		}
	}

	rootKey, k1, k2, leafKey := newKey(), newKey(), newKey(), newKey()
	rootTmpl := caTmpl(300, tag+"-root")
	rootCert := mkCert(rootTmpl, rootTmpl, &rootKey.PublicKey, rootKey)
	int1Cert := mkCert(caTmpl(301, tag+"-int1"), rootCert, &k1.PublicKey, rootKey)
	int2Cert := mkCert(caTmpl(302, tag+"-int2"), int1Cert, &k2.PublicKey, k1)
	leafCert := mkCert(&x509.Certificate{
		SerialNumber: big.NewInt(303),
		Subject:      pkix.Name{CommonName: tag + "-leaf.example.test"},
		NotBefore:    time.Now().Add(-time.Hour),
		NotAfter:     time.Now().Add(90 * 24 * time.Hour),
		KeyUsage:     x509.KeyUsageDigitalSignature,
		ExtKeyUsage:  []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
		DNSNames:     []string{tag + "-leaf.example.test"},
	}, int2Cert, &leafKey.PublicKey, k2)

	now := time.Now()
	return certparse.Describe(rootCert, now), certparse.Describe(int1Cert, now),
		certparse.Describe(int2Cert, now), certparse.Describe(leafCert, now)
}

// TestCertificateIssuersDeepTree는 CA 트리가 깊이 제한 없이(2계층 평탄화 없이)
// 실제 계층 그대로 내려오는지, 각 노드의 하위 합산·깊이·리프 미리보기가
// 맞는지 검증한다.
func TestCertificateIssuersDeepTree(t *testing.T) {
	pool := testPool(t)
	st := New(pool)
	ctx := context.Background()

	root, int1, int2, leaf := newTestDeepIssuerChain(t, "deeptree")
	reqID := "test-req-issuer-deep"
	fileName := "issuers-deep-fixture.pem"
	t.Cleanup(func() {
		_, _ = pool.Exec(`DELETE FROM certificates WHERE fingerprint_sha256 IN ($1, $2, $3, $4)`,
			root.FingerprintSHA256, int1.FingerprintSHA256,
			int2.FingerprintSHA256, leaf.FingerprintSHA256)
		cleanupAuditByRequestID(pool, reqID)
	})

	res, err := st.ImportCertificates(ctx, ImportInput{
		Certificates: []domain.Certificate{root, int1, int2, leaf},
		FileName:     &fileName,
		RequestID:    &reqID,
	})
	if err != nil {
		t.Fatalf("import fixtures: %v", err)
	}
	ids := map[string]string{}
	for _, ic := range res.Certificates {
		ids[ic.Certificate.FingerprintSHA256] = ic.Certificate.ID
	}

	agg, err := st.CertificateIssuers(ctx)
	if err != nil {
		t.Fatalf("certificate issuers: %v", err)
	}

	var rootNode *CATreeNode
	for i := range agg.CATree {
		if agg.CATree[i].ID != nil && *agg.CATree[i].ID == ids[root.FingerprintSHA256] {
			rootNode = &agg.CATree[i]
		}
	}
	if rootNode == nil {
		t.Fatal("ca_tree missing fixture root node")
	}
	if rootNode.Depth != 0 {
		t.Errorf("root depth = %d, want 0", rootNode.Depth)
	}
	if rootNode.SubtreeLeafCount != 1 {
		t.Errorf("root subtree_leaf_count = %d, want 1", rootNode.SubtreeLeafCount)
	}
	if len(rootNode.Children) != 1 {
		t.Fatalf("root children = %d, want 1 (중간1만)", len(rootNode.Children))
	}
	n1 := rootNode.Children[0]
	if n1.ID == nil || *n1.ID != ids[int1.FingerprintSHA256] {
		t.Fatalf("root child = %v, want int1", n1.ID)
	}
	if n1.Depth != 1 {
		t.Errorf("int1 depth = %d, want 1", n1.Depth)
	}
	if n1.LeafCount != 0 || n1.SubtreeLeafCount != 1 {
		t.Errorf("int1 leaf_count/subtree = %d/%d, want 0/1", n1.LeafCount, n1.SubtreeLeafCount)
	}
	if len(n1.Children) != 1 {
		t.Fatalf("int1 children = %d, want 1 (중간2 — 평탄화되면 0)", len(n1.Children))
	}
	n2 := n1.Children[0]
	if n2.ID == nil || *n2.ID != ids[int2.FingerprintSHA256] {
		t.Fatalf("int1 child = %v, want int2", n2.ID)
	}
	if n2.Depth != 2 {
		t.Errorf("int2 depth = %d, want 2", n2.Depth)
	}
	if n2.LeafCount != 1 || n2.SubtreeLeafCount != 1 {
		t.Errorf("int2 leaf_count/subtree = %d/%d, want 1/1", n2.LeafCount, n2.SubtreeLeafCount)
	}
	if len(n2.Children) != 0 {
		t.Errorf("int2 children = %d, want 0", len(n2.Children))
	}

	// 리프 미리보기: 리프를 직접 발급한 노드에만 실린다.
	if len(n2.LeafPreview) != 1 {
		t.Fatalf("int2 leaf_preview = %d, want 1", len(n2.LeafPreview))
	}
	if n2.LeafPreview[0].ID != ids[leaf.FingerprintSHA256] {
		t.Errorf("int2 leaf_preview id = %s, want the fixture leaf", n2.LeafPreview[0].ID)
	}
	if n2.LeafPreview[0].CommonName != "deeptree-leaf.example.test" {
		t.Errorf("int2 leaf_preview common_name = %q", n2.LeafPreview[0].CommonName)
	}
	if len(rootNode.LeafPreview) != 0 || len(n1.LeafPreview) != 0 {
		t.Error("리프를 직접 발급하지 않은 노드에 leaf_preview가 실렸다")
	}
}

// TestCALeafPreviewCapped는 미리보기가 상한(caLeafPreviewLimit)을 넘지 않고
// 만료 임박 순으로 잘리는지 검증한다 — 노드가 많을 때 응답이 인벤토리 전체를
// 싣지 않게 하는 것이 이 상한의 목적이다.
func TestCALeafPreviewCapped(t *testing.T) {
	previews := map[string][]CALeafPreview{}
	cas := []caIssuerRow{{
		chainRow: chainRow{
			id: "ca-1", subjectDN: "CN=cap", issuerDN: "CN=cap",
			notAfter: time.Now().Add(time.Hour), ctype: "ca", status: "active",
		},
		leafCount: caLeafPreviewLimit + 3,
	}}
	for i := 0; i < caLeafPreviewLimit; i++ {
		previews["ca-1"] = append(previews["ca-1"], CALeafPreview{ID: "leaf", CommonName: "x"})
	}
	tree := buildCATree(cas, previews)
	if len(tree) != 1 {
		t.Fatalf("tree roots = %d, want 1", len(tree))
	}
	if len(tree[0].LeafPreview) != caLeafPreviewLimit {
		t.Errorf("leaf_preview = %d, want %d", len(tree[0].LeafPreview), caLeafPreviewLimit)
	}
	if tree[0].LeafCount <= len(tree[0].LeafPreview) {
		t.Error("leaf_count가 미리보기 길이보다 커야 프론트가 '전체 보기'를 띄울 수 있다")
	}
}

// TestBuildCATreeBreaksCycles는 교차 서명 등으로 부모 관계가 순환해도
// 트리 구성이 끝나고 노드가 유실되지 않는지 검증한다.
func TestBuildCATreeBreaksCycles(t *testing.T) {
	mk := func(id, subject, issuer, ski, aki string) caIssuerRow {
		s, a := ski, aki
		return caIssuerRow{chainRow: chainRow{
			id: id, subjectDN: subject, issuerDN: issuer, cn: id,
			notAfter: time.Now().Add(time.Hour), ctype: "ca", status: "active",
			ski: &s, aki: &a,
		}}
	}
	// A는 B가, B는 A가 발급했다고 주장한다 (자체 서명 루트 없음).
	cas := []caIssuerRow{
		mk("a", "CN=A", "CN=B", "ski-a", "ski-b"),
		mk("b", "CN=B", "CN=A", "ski-b", "ski-a"),
	}
	tree := buildCATree(cas, nil)
	seen := map[string]bool{}
	var walk func(nodes []CATreeNode, depth int)
	walk = func(nodes []CATreeNode, depth int) {
		if depth > maxCATreeDepth+1 {
			t.Fatal("트리가 상한보다 깊다 — 순환이 끊기지 않았다")
		}
		for _, n := range nodes {
			if n.ID != nil {
				seen[*n.ID] = true
			}
			walk(n.Children, depth+1)
		}
	}
	walk(tree, 0)
	if !seen["a"] || !seen["b"] {
		t.Errorf("순환 CA 노드가 유실됐다: %v", seen)
	}
}

// TestIssuerOrgBackfillExpression은 0016 백필이 쓰는 RFC2253 O= 추출 표현식의
// 의미를 실제 Postgres에서 고정한다 — 이스케이프된 쉼표(\,)가 값을 자르지 않고,
// 이스케이프된 구분자 뒤의 가짜 O=는 무시된다. 표현식은 마이그레이션 파일
// 0016_issuer_organization.up.sql과 같아야 한다.
func TestIssuerOrgBackfillExpression(t *testing.T) {
	pool := testPool(t)

	cases := []struct {
		dn   string
		want *string
	}{
		{"CN=leaf.example.com,O=Acme Corp,C=US", strPtr("Acme Corp")},
		{`CN=x,O=Acme\, Inc.,C=KR`, strPtr("Acme, Inc.")},
		{"CN=only-cn", nil},
		{"O=Lead Org,CN=x", strPtr("Lead Org")},
		{`CN=trap\,O=fake,O=Real Org,C=KR`, strPtr("Real Org")},
		{"CN=x,OU=Team,C=KR", nil}, // OU는 O가 아니다
		{"CN=x+O=MultiVal,C=KR", strPtr("MultiVal")},
	}
	for _, tc := range cases {
		var got *string
		err := pool.QueryRow(`
			SELECT nullif(btrim(regexp_replace(
			           substring($1::text from '(?:^|(?<!\\)[,+])O=((?:[^,+\\]|\\.)*)'),
			           '\\(.)', '\1', 'g')), '')`, tc.dn).Scan(&got)
		if err != nil {
			t.Fatalf("backfill expression for %q: %v", tc.dn, err)
		}
		switch {
		case tc.want == nil && got != nil:
			t.Errorf("dn %q → %q, want NULL", tc.dn, *got)
		case tc.want != nil && (got == nil || *got != *tc.want):
			t.Errorf("dn %q → %v, want %q", tc.dn, got, *tc.want)
		}
	}
}

func strPtr(s string) *string { return &s }
