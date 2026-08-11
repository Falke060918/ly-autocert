package store

// 영향 범위 집계(재귀 계열 확장) + GET /servers issued_by= 필터 검증.
// 픽스처는 전부 실행 시점에 생성한다 (보안 하드 규칙 4).

import (
	"context"
	"errors"
	"testing"

	"github.com/Falke060918/INHERIT_AutoCert/backend/internal/domain"
)

// TestCertificateImpactTransitiveFamily는 루트 → 중간 CA → 리프 2개 체인에서
// 재귀 확장이 **직계 1단계를 넘어** 손자 리프까지 집계하는지 본다.
// 목록 필터(issued_by, 직계)와 갈리는 지점이 이 테스트의 핵심이다.
func TestCertificateImpactTransitiveFamily(t *testing.T) {
	pool := testPool(t)
	st := New(pool)
	ctx := context.Background()

	root, intermediate, leaf1, leaf2 := newTestIssuerChain(t, "Impact Root Org", "Impact Signing Org")
	reqID := "test-req-impact"
	fileName := "impact-fixture.pem"
	var targetID string
	t.Cleanup(func() {
		if targetID != "" {
			_, _ = pool.Exec(`DELETE FROM certificate_locations WHERE target_id = $1::uuid`, targetID)
			_, _ = pool.Exec(`DELETE FROM targets WHERE id = $1::uuid`, targetID)
		}
		_, _ = pool.Exec(`DELETE FROM certificates WHERE fingerprint_sha256 IN ($1, $2, $3, $4)`,
			root.FingerprintSHA256, intermediate.FingerprintSHA256,
			leaf1.FingerprintSHA256, leaf2.FingerprintSHA256)
		cleanupAuditByRequestID(pool, reqID)
	})

	res, err := st.ImportCertificates(ctx, ImportInput{
		Certificates: []domain.Certificate{root, intermediate, leaf1, leaf2},
		FileName:     &fileName,
		RequestID:    &reqID,
	})
	if err != nil {
		t.Fatalf("import chain: %v", err)
	}
	ids := map[string]string{}
	for _, ic := range res.Certificates {
		ids[ic.Certificate.FingerprintSHA256] = ic.Certificate.ID
	}
	rootID, intID := ids[root.FingerprintSHA256], ids[intermediate.FingerprintSHA256]
	leaf1ID, leaf2ID := ids[leaf1.FingerprintSHA256], ids[leaf2.FingerprintSHA256]

	// 리프 1개만 실제 엔드포인트에 배포한다 — 서버·엔드포인트 수가 인증서 수와
	// 따로 움직이는지 보기 위해서다.
	if err := pool.QueryRow(`
		INSERT INTO targets (name, target_type, hostname, ip_address, executor_scope)
		VALUES ('impact-host', 'network', 'impact-host.example.test', '198.51.100.77'::inet, 'server')
		RETURNING id::text`).Scan(&targetID); err != nil {
		t.Fatalf("insert target: %v", err)
	}
	if _, err := pool.Exec(`
		INSERT INTO certificate_locations
			(certificate_id, target_id, location_type, port, last_verified_at, deployment_status)
		VALUES ($1::uuid, $2::uuid, 'network_endpoint', 443, now(), 'deployed')`,
		leaf1ID, targetID); err != nil {
		t.Fatalf("insert location: %v", err)
	}

	// ① 루트 — 손자 리프 2건까지 잡혀야 한다(직계만 보면 1건에서 멈춘다).
	impact, err := st.CertificateImpact(ctx, rootID)
	if err != nil {
		t.Fatalf("impact(root): %v", err)
	}
	if impact.Certificate.ID != rootID || !impact.Certificate.IsCA || !impact.Certificate.SelfSigned {
		t.Errorf("subject = %+v, want the self-signed root CA", impact.Certificate)
	}
	if impact.DescendantCACount != 1 {
		t.Errorf("descendant_ca_count = %d, want 1", impact.DescendantCACount)
	}
	if impact.DescendantLeafCount != 2 {
		t.Errorf("descendant_leaf_count = %d, want 2 (전이 확장이 손자 리프까지 세야 한다)",
			impact.DescendantLeafCount)
	}
	if impact.CertificateCount != 4 {
		t.Errorf("certificate_count = %d, want 4 (자신 포함)", impact.CertificateCount)
	}
	if impact.MaxDepth != 2 {
		t.Errorf("max_depth = %d, want 2", impact.MaxDepth)
	}
	if impact.EndpointCount != 1 || impact.ServerCount != 1 || len(impact.Servers) != 1 {
		t.Errorf("endpoints/servers = %d/%d (items %d), want 1/1 (1건)",
			impact.EndpointCount, impact.ServerCount, len(impact.Servers))
	}
	if len(impact.Servers) == 1 && impact.Servers[0].ID != targetID {
		t.Errorf("server = %s, want the fixture host", impact.Servers[0].ID)
	}
	if impact.ListLimit != impactListLimit || impact.DomainsTruncated || impact.ServersTruncated {
		t.Errorf("limit=%d truncated=%v/%v, want %d/false/false",
			impact.ListLimit, impact.DomainsTruncated, impact.ServersTruncated, impactListLimit)
	}

	// 도메인은 비-CA 인증서의 CN·SAN만이다 — CA의 CN이 섞이면 안 된다.
	got := map[string]ImpactDomain{}
	for _, d := range impact.Domains {
		got[d.Name] = d
	}
	if len(got) != 2 || impact.DomainCount != 2 {
		t.Fatalf("domains = %v (count %d), want the 2 leaf names only", got, impact.DomainCount)
	}
	if d, ok := got["issuers-leaf-1.example.test"]; !ok || d.ServerCount != 1 {
		t.Errorf("deployed leaf domain = %+v, want server_count 1", d)
	}
	if d, ok := got["issuers-leaf-2.example.test"]; !ok || d.ServerCount != 0 {
		t.Errorf("undeployed leaf domain = %+v, want server_count 0", d)
	}

	// ② 중간 CA — 자손 CA는 없고 리프 2건.
	impact, err = st.CertificateImpact(ctx, intID)
	if err != nil {
		t.Fatalf("impact(intermediate): %v", err)
	}
	if impact.DescendantCACount != 0 || impact.DescendantLeafCount != 2 || impact.MaxDepth != 1 {
		t.Errorf("intermediate impact = ca %d / leaf %d / depth %d, want 0/2/1",
			impact.DescendantCACount, impact.DescendantLeafCount, impact.MaxDepth)
	}

	// ③ 리프 — CA가 아니어도 404가 아니고, 자기 배포분만 남는다.
	impact, err = st.CertificateImpact(ctx, leaf1ID)
	if err != nil {
		t.Fatalf("impact(leaf): %v", err)
	}
	if impact.Certificate.IsCA || impact.DescendantCACount != 0 || impact.DescendantLeafCount != 0 {
		t.Errorf("leaf impact = %+v, want no descendants", impact)
	}
	if impact.CertificateCount != 1 || impact.ServerCount != 1 {
		t.Errorf("leaf impact = %d certificates / %d servers, want 1/1",
			impact.CertificateCount, impact.ServerCount)
	}

	// ④ 서버 필터는 계열 전체를 본다 — 루트로 걸어도 손자 리프의 호스트가 잡히고,
	//    배포되지 않은 리프로 걸면 0건이다. total은 impact의 server_count와 같아야
	//    한다(같은 술어를 공유하지 않으면 두 화면이 서로를 부정한다).
	for _, tc := range []struct {
		name string
		id   string
		want int
	}{
		{"root", rootID, 1},
		{"intermediate", intID, 1},
		{"deployed leaf", leaf1ID, 1},
		{"undeployed leaf", leaf2ID, 0},
	} {
		list, err := st.ListServers(ctx, ListServersInput{IssuedBy: tc.id})
		if err != nil {
			t.Fatalf("list servers issued_by(%s): %v", tc.name, err)
		}
		if list.Total != tc.want || len(list.Items) != tc.want {
			t.Errorf("issued_by(%s) = total %d items %d, want %d",
				tc.name, list.Total, len(list.Items), tc.want)
		}
		imp, err := st.CertificateImpact(ctx, tc.id)
		if err != nil {
			t.Fatalf("impact(%s): %v", tc.name, err)
		}
		if imp.ServerCount != list.Total {
			t.Errorf("impact(%s).server_count = %d but GET /servers total = %d — 두 API가 같은 술어를 써야 한다",
				tc.name, imp.ServerCount, list.Total)
		}
	}

	// ⑤ 다른 필터와 함께 써도 SQL 술어라 total이 함께 줄어든다.
	noSuchHost := ListServersInput{IssuedBy: rootID, Search: "no-such-host-zzz"}
	list, err := st.ListServers(ctx, noSuchHost)
	if err != nil {
		t.Fatalf("list servers issued_by + q: %v", err)
	}
	if list.Total != 0 {
		t.Errorf("issued_by + q(no match) total = %d, want 0", list.Total)
	}
}

// TestCertificateImpactNotFound는 없는 id·잘못된 형식이 모두 ErrNotFound인지 본다
// (uuid 캐스트 오류가 500으로 새지 않게 — 서버 상세와 같은 규약).
func TestCertificateImpactNotFound(t *testing.T) {
	st := New(testPool(t))
	ctx := context.Background()
	for _, id := range []string{"not-a-uuid", "00000000-0000-0000-0000-000000000000"} {
		if _, err := st.CertificateImpact(ctx, id); !errors.Is(err, ErrNotFound) {
			t.Errorf("impact(%q) err = %v, want ErrNotFound", id, err)
		}
	}
}
