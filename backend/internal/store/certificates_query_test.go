package store

// 이슈 #4(인증서 목록·상세 API) store 계층 테스트.
// 픽스처는 실행 시점에 생성한다 — 실제 키·인증서를 커밋하지 않는다 (보안 하드 규칙 4).
// fingerprint는 결정적 SHA-256(테스트 라벨)이며 실제 인증서에서 나온 값이 아니다.
// TEST_DATABASE_URL 미설정 시 스킵 (전용 테스트 DB — 메인 DB에 돌리지 않는다).

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/Falke060918/INHERIT_AutoCert/backend/internal/domain"
)

type listFixture struct {
	cn       string
	serial   string
	sans     []string
	notAfter time.Time
	ctype    domain.CertificateType
	status   domain.CertificateStatus
}

// insertListFixture는 목록 테스트용 인증서 1건을 직접 INSERT한다.
func insertListFixture(t *testing.T, pool *sql.DB, f listFixture) string {
	t.Helper()
	sum := sha256.Sum256([]byte("list-fixture:" + f.cn + ":" + f.serial))
	var id string
	err := pool.QueryRow(`
		INSERT INTO certificates (
			fingerprint_sha256, serial_number, subject_dn, issuer_dn, common_name, sans,
			not_before, not_after, public_key_algorithm, public_key_size,
			signature_algorithm, certificate_type, status
		) VALUES (
			$1, $2, $3, $4, $5,
			coalesce(string_to_array(nullif($6, ''), chr(31)), '{}'),
			$7, $8, 'ECDSA', 256, 'ECDSA-SHA256', $9, $10
		) RETURNING id`,
		hex.EncodeToString(sum[:]), f.serial, "CN="+f.cn, "CN=fixture-ca", f.cn,
		strings.Join(f.sans, sansSep),
		f.notAfter.Add(-90*24*time.Hour), f.notAfter, string(f.ctype), string(f.status),
	).Scan(&id)
	if err != nil {
		t.Fatalf("insert fixture %s: %v", f.cn, err)
	}
	return id
}

// clearInventory는 인벤토리 테이블을 비운다 (테스트 전용 DB에서만 허용되는 정리다).
func clearInventory(t *testing.T, pool *sql.DB) {
	t.Helper()
	for _, stmt := range []string{
		`DELETE FROM certificate_locations`, `DELETE FROM certificates`, `DELETE FROM targets`,
	} {
		if _, err := pool.Exec(stmt); err != nil {
			t.Fatalf("cleanup %q: %v", stmt, err)
		}
	}
}

func TestListCertificates(t *testing.T) {
	pool := testPool(t)
	st := New(pool)
	ctx := context.Background()
	clearInventory(t, pool)
	t.Cleanup(func() { clearInventory(t, pool) })

	now := time.Now()
	fixtures := []listFixture{
		{cn: "web.example.test", serial: "01", sans: []string{"web.example.test", "www.example.test"},
			notAfter: now.Add(200 * 24 * time.Hour), ctype: domain.CertTypeServer, status: domain.CertStatusActive},
		{cn: "api.example.test", serial: "02", sans: []string{"api.example.test"},
			notAfter: now.Add(20 * 24 * time.Hour), ctype: domain.CertTypeServer, status: domain.CertStatusActive},
		{cn: "old.example.test", serial: "03",
			notAfter: now.Add(-5 * 24 * time.Hour), ctype: domain.CertTypeServer, status: domain.CertStatusExpired},
		{cn: "vpn-client", serial: "04",
			notAfter: now.Add(100 * 24 * time.Hour), ctype: domain.CertTypeClient, status: domain.CertStatusActive},
		{cn: "fixture-root-ca", serial: "05",
			notAfter: now.Add(3000 * 24 * time.Hour), ctype: domain.CertTypeCA, status: domain.CertStatusActive},
		{cn: "revoked.example.test", serial: "06",
			notAfter: now.Add(50 * 24 * time.Hour), ctype: domain.CertTypeServer, status: domain.CertStatusRevoked},
	}
	for _, f := range fixtures {
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
	cns := func(res *CertificateList) []string {
		out := make([]string, len(res.Items))
		for i, c := range res.Items {
			out[i] = c.CommonName
		}
		return out
	}

	t.Run("default sorts by not_after asc", func(t *testing.T) {
		res := list(t, ListCertificatesInput{})
		if res.Total != 6 || len(res.Items) != 6 {
			t.Fatalf("total = %d, items = %d, want 6/6", res.Total, len(res.Items))
		}
		if res.Items[0].CommonName != "old.example.test" {
			t.Errorf("first = %s, want old.example.test", res.Items[0].CommonName)
		}
		if got := res.Items[0].SANs; got == nil || len(got) != 0 {
			t.Errorf("empty sans = %#v, want empty non-nil slice", got)
		}
	})

	t.Run("search matches common name case-insensitively", func(t *testing.T) {
		res := list(t, ListCertificatesInput{Search: "WEB.example"})
		if res.Total != 1 || res.Items[0].CommonName != "web.example.test" {
			t.Errorf("search WEB.example = %v (total %d), want web.example.test", cns(res), res.Total)
		}
	})

	t.Run("search matches SAN", func(t *testing.T) {
		res := list(t, ListCertificatesInput{Search: "www."})
		if res.Total != 1 || res.Items[0].CommonName != "web.example.test" {
			t.Errorf("search www. = %v (total %d), want web.example.test", cns(res), res.Total)
		}
	})

	t.Run("search escapes LIKE wildcards", func(t *testing.T) {
		if res := list(t, ListCertificatesInput{Search: "%"}); res.Total != 0 {
			t.Errorf("search %% = %d rows, want 0 (wildcard must be escaped)", res.Total)
		}
	})

	t.Run("status filter", func(t *testing.T) {
		if res := list(t, ListCertificatesInput{Statuses: []domain.CertificateStatus{domain.CertStatusActive}}); res.Total != 4 {
			t.Errorf("status=active total = %d, want 4", res.Total)
		}
		res := list(t, ListCertificatesInput{
			Statuses: []domain.CertificateStatus{domain.CertStatusExpired, domain.CertStatusRevoked}})
		if res.Total != 2 {
			t.Errorf("status=expired,revoked total = %d, want 2", res.Total)
		}
	})

	t.Run("type filter", func(t *testing.T) {
		if res := list(t, ListCertificatesInput{Types: []domain.CertificateType{domain.CertTypeServer}}); res.Total != 4 {
			t.Errorf("type=server total = %d, want 4", res.Total)
		}
		res := list(t, ListCertificatesInput{
			Types: []domain.CertificateType{domain.CertTypeClient, domain.CertTypeCA}})
		if res.Total != 2 {
			t.Errorf("type=client,ca total = %d, want 2", res.Total)
		}
	})

	t.Run("expired filter is derived from not_after", func(t *testing.T) {
		yes, no := true, false
		res := list(t, ListCertificatesInput{Expired: &yes})
		if res.Total != 1 || res.Items[0].CommonName != "old.example.test" {
			t.Errorf("expired=true = %v, want [old.example.test]", cns(res))
		}
		if res := list(t, ListCertificatesInput{Expired: &no}); res.Total != 5 {
			t.Errorf("expired=false total = %d, want 5", res.Total)
		}
	})

	t.Run("expires_within_days excludes already expired", func(t *testing.T) {
		days := 30
		res := list(t, ListCertificatesInput{ExpiresWithinDays: &days})
		if res.Total != 1 || res.Items[0].CommonName != "api.example.test" {
			t.Errorf("expires_within_days=30 = %v, want [api.example.test]", cns(res))
		}
	})

	t.Run("sort desc", func(t *testing.T) {
		res := list(t, ListCertificatesInput{SortField: "not_after", SortDesc: true})
		if res.Items[0].CommonName != "fixture-root-ca" {
			t.Errorf("not_after desc first = %s, want fixture-root-ca", res.Items[0].CommonName)
		}
	})

	t.Run("pagination", func(t *testing.T) {
		res := list(t, ListCertificatesInput{SortField: "common_name", Page: 2, PageSize: 2})
		if res.Total != 6 || len(res.Items) != 2 {
			t.Fatalf("page 2 size 2: total %d items %d, want 6/2", res.Total, len(res.Items))
		}
		if got := cns(res); got[0] != "old.example.test" || got[1] != "revoked.example.test" {
			t.Errorf("page 2 = %v, want [old.example.test revoked.example.test]", got)
		}
		if res.Page != 2 || res.PageSize != 2 {
			t.Errorf("echo page/page_size = %d/%d, want 2/2", res.Page, res.PageSize)
		}
	})

	t.Run("invalid sort field is rejected", func(t *testing.T) {
		if _, err := st.ListCertificates(ctx, ListCertificatesInput{SortField: "fingerprint_sha256; DROP TABLE"}); err == nil {
			t.Error("non-whitelisted sort field accepted, want error")
		}
	})
}

func TestGetCertificateDetail(t *testing.T) {
	pool := testPool(t)
	st := New(pool)
	ctx := context.Background()
	clearInventory(t, pool)
	t.Cleanup(func() { clearInventory(t, pool) })

	certID := insertListFixture(t, pool, listFixture{
		cn: "detail.example.test", serial: "d1", sans: []string{"detail.example.test"},
		notAfter: time.Now().Add(60 * 24 * time.Hour),
		ctype:    domain.CertTypeServer, status: domain.CertStatusActive,
	})
	var targetID string
	if err := pool.QueryRow(`
		INSERT INTO targets (name, target_type, hostname, ip_address)
		VALUES ('detail-fixture-target', 'server', 'host1.example.test', '192.0.2.10'::inet)
		RETURNING id`).Scan(&targetID); err != nil {
		t.Fatalf("insert target: %v", err)
	}
	if _, err := pool.Exec(`
		INSERT INTO certificate_locations (certificate_id, target_id, location_type, port, deployment_status)
		VALUES ($1, $2, 'network_endpoint', 443, 'unknown')`, certID, targetID); err != nil {
		t.Fatalf("insert location: %v", err)
	}

	t.Run("returns certificate with locations and target", func(t *testing.T) {
		d, err := st.GetCertificateDetail(ctx, certID)
		if err != nil {
			t.Fatalf("get detail: %v", err)
		}
		if d.Certificate.CommonName != "detail.example.test" {
			t.Errorf("common_name = %s", d.Certificate.CommonName)
		}
		if len(d.Locations) != 1 {
			t.Fatalf("locations = %d, want 1", len(d.Locations))
		}
		loc := d.Locations[0]
		if loc.LocationType != domain.LocationNetworkEndpoint || loc.Port == nil || *loc.Port != 443 {
			t.Errorf("location = %+v, want network_endpoint:443", loc.CertificateLocation)
		}
		if loc.Target.Name != "detail-fixture-target" || loc.Target.IPAddress == nil || *loc.Target.IPAddress != "192.0.2.10" {
			t.Errorf("target = %+v, want detail-fixture-target/192.0.2.10", loc.Target)
		}
	})

	t.Run("unknown id returns ErrNotFound", func(t *testing.T) {
		_, err := st.GetCertificateDetail(ctx, "00000000-0000-4000-8000-000000000000")
		if !errors.Is(err, ErrNotFound) {
			t.Errorf("err = %v, want ErrNotFound", err)
		}
	})

	t.Run("malformed id returns ErrNotFound without SQL error", func(t *testing.T) {
		_, err := st.GetCertificateDetail(ctx, "not-a-uuid")
		if !errors.Is(err, ErrNotFound) {
			t.Errorf("err = %v, want ErrNotFound", err)
		}
	})
}

// TestListCertificates10kUnder2s는 이슈 #4 완료 조건을 검증한다:
// 인증서 10,000건에서 목록 조회가 2초 이내여야 한다.
func TestListCertificates10kUnder2s(t *testing.T) {
	pool := testPool(t)
	st := New(pool)
	ctx := context.Background()
	clearInventory(t, pool)
	t.Cleanup(func() { clearInventory(t, pool) })

	// generate_series로 10,000건 시딩. fingerprint는 md5 2개 연결(64자 소문자 hex) —
	// 합성 값이며 실제 인증서에서 나온 값이 아니다.
	if _, err := pool.Exec(`
		INSERT INTO certificates (
			fingerprint_sha256, serial_number, subject_dn, issuer_dn, common_name, sans,
			not_before, not_after, public_key_algorithm, public_key_size,
			signature_algorithm, certificate_type, status
		)
		SELECT md5(i::text) || md5((i + 7777777)::text),
			'perf-' || i, 'CN=perf-' || i || '.example.test', 'CN=perf-ca',
			'perf-' || i || '.example.test',
			ARRAY['perf-' || i || '.example.test'],
			now() - interval '30 days', now() + (i % 365) * interval '1 day',
			'ECDSA', 256, 'ECDSA-SHA256', 'server', 'active'
		FROM generate_series(1, 10000) AS i`); err != nil {
		t.Fatalf("seed 10k: %v", err)
	}

	days := 30
	cases := map[string]ListCertificatesInput{
		"default page":     {},
		"search":           {Search: "perf-9999"},
		"filter+sort+page": {Statuses: []domain.CertificateStatus{domain.CertStatusActive}, SortField: "common_name", SortDesc: true, Page: 100, PageSize: 50},
		"expires_within":   {ExpiresWithinDays: &days},
	}
	for name, in := range cases {
		t.Run(name, func(t *testing.T) {
			start := time.Now()
			res, err := st.ListCertificates(ctx, in)
			if err != nil {
				t.Fatalf("list: %v", err)
			}
			if elapsed := time.Since(start); elapsed > 2*time.Second {
				t.Errorf("list took %v, want <= 2s (issue #4)", elapsed)
			}
			if res.Total == 0 {
				t.Error("total = 0, want > 0")
			}
		})
	}
}
