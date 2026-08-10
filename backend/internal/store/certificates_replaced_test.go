package store

// 교체된 인증서의 목록 제외·집계 제외와 목록 파생 열 last_seen_at 회귀 테스트
// (06-rescan-and-replacement 11.2·11.3·11.4).
//
// 핵심 회귀 지점은 **기본 동작 불변**이다: exclude_status를 주지 않으면 결과가
// 예전과 완전히 같아야 한다. 같은 입력 구조체를 CSV 내보내기가 공유하므로
// 여기서 기본값이 흔들리면 아무도 요청하지 않은 내보내기 내용 변경이 따라온다.

import (
	"context"
	"testing"
	"time"

	"github.com/Falke060918/INHERIT_AutoCert/backend/internal/domain"
)

func TestListCertificatesExcludeStatus(t *testing.T) {
	pool := testPool(t)
	st := New(pool)
	ctx := context.Background()
	clearInventory(t, pool)
	t.Cleanup(func() { clearInventory(t, pool) })

	now := time.Now()
	for _, f := range []listFixture{
		{cn: "live.example.test", serial: "e1", notAfter: now.Add(100 * 24 * time.Hour),
			ctype: domain.CertTypeServer, status: domain.CertStatusActive},
		{cn: "old.example.test", serial: "e2", notAfter: now.Add(10 * 24 * time.Hour),
			ctype: domain.CertTypeServer, status: domain.CertStatusReplaced},
		{cn: "dead.example.test", serial: "e3", notAfter: now.Add(50 * 24 * time.Hour),
			ctype: domain.CertTypeServer, status: domain.CertStatusRevoked},
	} {
		insertListFixture(t, pool, f)
	}

	list := func(t *testing.T, in ListCertificatesInput) *CertificateList {
		t.Helper()
		res, err := st.ListCertificates(ctx, in)
		if err != nil {
			t.Fatalf("list: %v", err)
		}
		return res
	}

	t.Run("no exclude_status keeps every row (default unchanged)", func(t *testing.T) {
		res := list(t, ListCertificatesInput{})
		if res.Total != 3 || len(res.Items) != 3 {
			t.Fatalf("total = %d, items = %d, want 3/3 — 서버 기본 동작이 바뀌었다",
				res.Total, len(res.Items))
		}
	})

	t.Run("exclude_status=replaced hides only replaced", func(t *testing.T) {
		res := list(t, ListCertificatesInput{
			ExcludeStatuses: []domain.CertificateStatus{domain.CertStatusReplaced},
		})
		if res.Total != 2 {
			t.Fatalf("total = %d, want 2", res.Total)
		}
		for _, c := range res.Items {
			if c.Status == domain.CertStatusReplaced {
				t.Errorf("replaced 인증서 %s가 남았다", c.CommonName)
			}
		}
	})

	t.Run("multiple exclusions stack", func(t *testing.T) {
		res := list(t, ListCertificatesInput{
			ExcludeStatuses: []domain.CertificateStatus{
				domain.CertStatusReplaced, domain.CertStatusRevoked,
			},
		})
		if res.Total != 1 || res.Items[0].CommonName != "live.example.test" {
			t.Fatalf("total = %d, first = %q, want 1 / live.example.test",
				res.Total, res.Items[0].CommonName)
		}
	})

	t.Run("include and exclude combine with AND", func(t *testing.T) {
		res := list(t, ListCertificatesInput{
			Statuses:        []domain.CertificateStatus{domain.CertStatusReplaced},
			ExcludeStatuses: []domain.CertificateStatus{domain.CertStatusReplaced},
		})
		if res.Total != 0 {
			t.Fatalf("total = %d, want 0 (상충 조건은 빈 결과)", res.Total)
		}
	})

	t.Run("export shares the input and stays full by default", func(t *testing.T) {
		items, err := st.ExportCertificates(ctx, ExportInput{})
		if err != nil {
			t.Fatalf("export: %v", err)
		}
		if len(items) != 3 {
			t.Fatalf("export rows = %d, want 3 — CSV 기본 내용이 조용히 바뀌었다", len(items))
		}
	})
}

func TestListCertificatesLastSeenAt(t *testing.T) {
	pool := testPool(t)
	st := New(pool)
	ctx := context.Background()
	clearInventory(t, pool)
	t.Cleanup(func() { clearInventory(t, pool) })

	now := time.Now().UTC().Truncate(time.Second)
	withLoc := insertListFixture(t, pool, listFixture{
		cn: "seen.example.test", serial: "s1", notAfter: now.Add(90 * 24 * time.Hour),
		ctype: domain.CertTypeServer, status: domain.CertStatusActive,
	})
	insertListFixture(t, pool, listFixture{
		cn: "unseen.example.test", serial: "s2", notAfter: now.Add(80 * 24 * time.Hour),
		ctype: domain.CertTypeServer, status: domain.CertStatusActive,
	})

	var targetID string
	if err := pool.QueryRow(`
		INSERT INTO targets (name, target_type, ip_address, executor_scope)
		VALUES ('last-seen-fixture', 'network', '10.77.0.9'::inet, 'server')
		RETURNING id`).Scan(&targetID); err != nil {
		t.Fatalf("insert target: %v", err)
	}
	// 위치 2행: 최근 확인 시각이 다르다 — max가 뽑히는지 본다.
	older := now.Add(-48 * time.Hour)
	newer := now.Add(-2 * time.Hour)
	for i, at := range []time.Time{older, newer} {
		if _, err := pool.Exec(`
			INSERT INTO certificate_locations (
				certificate_id, target_id, location_type, port, last_verified_at, deployment_status
			) VALUES ($1::uuid, $2::uuid, 'network_endpoint', $3, $4, 'deployed')`,
			withLoc, targetID, 8443+i, at); err != nil {
			t.Fatalf("insert location %d: %v", i, err)
		}
	}

	res, err := st.ListCertificates(ctx, ListCertificatesInput{})
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	byCN := map[string]*time.Time{}
	for _, item := range res.Items {
		byCN[item.CommonName] = item.LastSeenAt
	}
	got := byCN["seen.example.test"]
	if got == nil {
		t.Fatal("last_seen_at = nil, want max(location.last_verified_at)")
	}
	if diff := got.UTC().Sub(newer); diff > time.Second || diff < -time.Second {
		t.Errorf("last_seen_at = %s, want %s (가장 최근 위치)", got.UTC(), newer)
	}
	if byCN["unseen.example.test"] != nil {
		t.Error("위치가 없는 인증서의 last_seen_at은 nil이어야 한다")
	}
}

func TestCertificateSummaryExcludesReplaced(t *testing.T) {
	pool := testPool(t)
	st := New(pool)
	ctx := context.Background()
	clearInventory(t, pool)
	t.Cleanup(func() { clearInventory(t, pool) })

	now := time.Now()
	// 교체된 인증서를 만료 임박 구간(D-5)에 둔다 — status 필터가 없으면
	// total·expiring_7d·expiring_30d 전 구간에 섞여 들어간다.
	insertListFixture(t, pool, listFixture{
		cn: "replaced.example.test", serial: "r1", notAfter: now.Add(5 * 24 * time.Hour),
		ctype: domain.CertTypeServer, status: domain.CertStatusReplaced, selfSigned: true,
	})
	insertListFixture(t, pool, listFixture{
		cn: "current.example.test", serial: "r2", notAfter: now.Add(5 * 24 * time.Hour),
		ctype: domain.CertTypeServer, status: domain.CertStatusActive,
	})

	sum, err := st.CertificateSummary(ctx)
	if err != nil {
		t.Fatalf("summary: %v", err)
	}
	if sum.Total != 1 || sum.Expiring7d != 1 || sum.Expiring30d != 1 {
		t.Errorf("summary = %+v, want total/7d/30d 모두 1 (replaced 제외)", sum)
	}
	if sum.SelfSigned != 0 {
		t.Errorf("self_signed = %d, want 0 — 교체된 자체 서명 인증서가 남았다", sum.SelfSigned)
	}
}
