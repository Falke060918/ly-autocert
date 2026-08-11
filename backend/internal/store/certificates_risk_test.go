package store

// 위험 판정 SQL 술어의 교차 검증.
//
// 판정 정본은 순수 패키지 internal/certrisk이고 목록 필터·집계는 그것을 SQL로 다시
// 표현한 것이다. 두 구현이 갈라지면 "화면엔 danger 배지인데 danger 필터로는 안 잡히는"
// 상태가 되므로, 여기서 픽스처 전건을 놓고 **Go 판정 결과와 SQL 필터 결과가 같은지**를
// 코드·심각도 전 항목에 대해 대조한다. 집계 카운트도 같은 기준으로 검증한다.
//
// 픽스처는 실행 시점에 만든다 — 실제 키·인증서를 커밋하지 않는다 (보안 하드 규칙 4).
// TEST_DATABASE_URL 미설정 시 스킵.

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/Falke060918/INHERIT_AutoCert/backend/internal/certrisk"
	"github.com/Falke060918/INHERIT_AutoCert/backend/internal/domain"
)

type riskFixture struct {
	label      string
	subjectDN  string
	issuerDN   string // 빈 값이면 "CN=risk-fixture-ca"
	sans       []string
	keyAlg     string // 빈 값이면 ECDSA
	keySize    int    // 0이면 384
	sigAlg     string // 빈 값이면 ECDSA-SHA384
	ctype      domain.CertificateType
	status     domain.CertificateStatus
	validDays  int  // not_after - not_before (0이면 300)
	expired    bool // true면 not_after가 과거
	ski        *string
	aki        *string
	tlsVersion string // 비어 있지 않으면 network_endpoint 위치를 1건 만든다
	// chainComplete는 0018 위치 컬럼이다. nil이면 컬럼도 NULL(미상)이며,
	// tlsVersion이 비어 있어도 값이 있으면 위치를 1건 만든다.
	chainComplete *bool
	locRemoved    bool
}

// insertRiskFixture는 판정 테스트용 인증서(+ 선택적 위치) 1건을 INSERT한다.
func insertRiskFixture(t *testing.T, pool *sql.DB, targetID string, f riskFixture) string {
	t.Helper()
	sum := sha256.Sum256([]byte("risk-fixture:" + f.label))
	subject := f.subjectDN
	if subject == "" {
		subject = "CN=" + f.label
	}
	issuer := f.issuerDN
	if issuer == "" {
		issuer = "CN=risk-fixture-ca"
	}
	keyAlg, keySize := f.keyAlg, f.keySize
	if keyAlg == "" {
		keyAlg = "ECDSA"
	}
	if keySize == 0 {
		keySize = 384
	}
	sigAlg := f.sigAlg
	if sigAlg == "" {
		sigAlg = "ECDSA-SHA384"
	}
	ctype := f.ctype
	if ctype == "" {
		ctype = domain.CertTypeServer
	}
	status := f.status
	if status == "" {
		status = domain.CertStatusActive
	}
	days := f.validDays
	if days == 0 {
		days = 300
	}
	notAfter := time.Now().Add(200 * 24 * time.Hour)
	if f.expired {
		notAfter = time.Now().Add(-10 * 24 * time.Hour)
	}
	notBefore := notAfter.Add(-time.Duration(days) * 24 * time.Hour)

	var id string
	err := pool.QueryRow(`
		INSERT INTO certificates (
			fingerprint_sha256, serial_number, subject_dn, issuer_dn, common_name, sans,
			not_before, not_after, public_key_algorithm, public_key_size,
			signature_algorithm, certificate_type, status, subject_key_id, authority_key_id
		) VALUES (
			$1, $2, $3, $4, $5,
			coalesce(string_to_array(nullif($6, ''), chr(31)), '{}'),
			$7, $8, $9, $10, $11, $12, $13, $14, $15
		) RETURNING id`,
		hex.EncodeToString(sum[:]), f.label, subject, issuer, f.label,
		strings.Join(f.sans, sansSep),
		notBefore, notAfter, keyAlg, keySize, sigAlg, string(ctype), string(status),
		f.ski, f.aki,
	).Scan(&id)
	if err != nil {
		t.Fatalf("insert risk fixture %s: %v", f.label, err)
	}

	if f.tlsVersion != "" || f.chainComplete != nil {
		dep := "unknown"
		if f.locRemoved {
			dep = "removed"
		}
		var tls *string
		if f.tlsVersion != "" {
			v := f.tlsVersion
			tls = &v
		}
		if _, err := pool.Exec(`
			INSERT INTO certificate_locations
				(certificate_id, target_id, location_type, port, tls_version, deployment_status, chain_complete)
			VALUES ($1, $2, 'network_endpoint', 443, $3, $4, $5)`,
			id, targetID, tls, dep, f.chainComplete); err != nil {
			t.Fatalf("insert location for %s: %v", f.label, err)
		}
	}
	return id
}

// riskFixtures는 규칙마다 경계값 양쪽을 포함하는 픽스처 집합이다.
func riskFixtures() []riskFixture {
	ski, other := "aabbccdd", "11223344"
	chainYes, chainNo := true, false
	return []riskFixture{
		{label: "clean-baseline", sans: []string{"clean.example.test"}},

		// 키 강도 경계
		{label: "rsa-2047", keyAlg: "RSA", keySize: 2047, sigAlg: "SHA256-RSA", sans: []string{"a.example.test"}},
		{label: "rsa-2048", keyAlg: "RSA", keySize: 2048, sigAlg: "SHA256-RSA", sans: []string{"b.example.test"}},
		{label: "rsa-3071", keyAlg: "RSA", keySize: 3071, sigAlg: "SHA256-RSA", sans: []string{"c.example.test"}},
		{label: "rsa-3072", keyAlg: "RSA", keySize: 3072, sigAlg: "SHA256-RSA", sans: []string{"d.example.test"}},
		{label: "ecdsa-255", keyAlg: "ECDSA", keySize: 255, sans: []string{"e.example.test"}},
		{label: "ecdsa-256", keyAlg: "ECDSA", keySize: 256, sigAlg: "ECDSA-SHA256", sans: []string{"f.example.test"}},
		{label: "ecdsa-383", keyAlg: "ECDSA", keySize: 383, sans: []string{"g.example.test"}},
		{label: "ecdsa-384", keyAlg: "ECDSA", keySize: 384, sans: []string{"h.example.test"}},
		{label: "ed25519-key", keyAlg: "Ed25519", keySize: 256, sigAlg: "Ed25519", sans: []string{"i.example.test"}},
		{label: "dsa-unknown", keyAlg: "DSA", keySize: 1024, sigAlg: "DSA-SHA256", sans: []string{"j.example.test"}},

		// 서명 알고리즘 경계
		{label: "sig-sha1", sigAlg: "SHA1-RSA", keyAlg: "RSA", keySize: 4096, sans: []string{"k.example.test"}},
		{label: "sig-md5", sigAlg: "MD5-RSA", keyAlg: "RSA", keySize: 4096, sans: []string{"l.example.test"}},
		{label: "sig-ecdsa-sha1", sigAlg: "ECDSA-SHA1", sans: []string{"m.example.test"}},
		{label: "sig-sha512", sigAlg: "SHA512-RSA", keyAlg: "RSA", keySize: 4096, sans: []string{"n.example.test"}},

		// SAN
		{label: "san-none"},
		{label: "san-one", sans: []string{"one.example.test"}},

		// 와일드카드
		{label: "wc-simple", sans: []string{"*.example.com"}},
		{label: "wc-deep", sans: []string{"*.example.co.kr"}},
		{label: "wc-broad-tld", sans: []string{"*.com"}},
		{label: "wc-broad-cc", sans: []string{"*.co.kr"}},
		{label: "wc-broad-private", sans: []string{"*.corp"}},
		{label: "wc-multi", sans: []string{"*.*.example.com"}},
		{label: "wc-mid", sans: []string{"api.*.example.com"}},
		{label: "wc-mixed", sans: []string{"*.example.com", "*.co.kr", "*.*.x.example.com"}},

		// 유효기간 경계
		{label: "validity-824", validDays: 824, sans: []string{"o.example.test"}},
		{label: "validity-825", validDays: 825, sans: []string{"p.example.test"}},
		{label: "validity-826", validDays: 826, sans: []string{"q.example.test"}},

		// 만료 CA / 만료 리프
		{label: "ca-expired", ctype: domain.CertTypeCA, expired: true, status: domain.CertStatusExpired,
			subjectDN: "CN=expired-ca", issuerDN: "CN=upper-ca", sans: []string{"r.example.test"}},
		{label: "ca-live", ctype: domain.CertTypeCA, subjectDN: "CN=live-ca", issuerDN: "CN=upper-ca",
			sans: []string{"s.example.test"}},
		{label: "leaf-expired", expired: true, status: domain.CertStatusExpired, sans: []string{"t.example.test"}},

		// 자체 서명 3분기
		{label: "self-signed-noaki", ctype: domain.CertTypeCA, subjectDN: "CN=root-a", issuerDN: "CN=root-a",
			ski: &ski, sans: []string{"u.example.test"}},
		{label: "self-signed-akimatch", ctype: domain.CertTypeCA, subjectDN: "CN=root-b", issuerDN: "CN=root-b",
			ski: &ski, aki: &ski, sans: []string{"v.example.test"}},
		{label: "chain-anomaly-akimismatch", ctype: domain.CertTypeCA, subjectDN: "CN=root-c", issuerDN: "CN=root-c",
			ski: &ski, aki: &other, sans: []string{"w.example.test"}},
		{label: "chain-anomaly-notca", subjectDN: "CN=selfleaf", issuerDN: "CN=selfleaf",
			ski: &ski, sans: []string{"x.example.test"}},
		{label: "chain-anomaly-noski", ctype: domain.CertTypeCA, subjectDN: "CN=root-d", issuerDN: "CN=root-d",
			aki: &ski, sans: []string{"y.example.test"}},

		// 위치 축 (TLS 버전)
		{label: "tls10", tlsVersion: "TLS 1.0", sans: []string{"tls10.example.test"}},
		{label: "tls11", tlsVersion: "TLS 1.1", sans: []string{"tls11.example.test"}},
		{label: "tls12", tlsVersion: "TLS 1.2", sans: []string{"tls12.example.test"}},
		{label: "tls10-removed", tlsVersion: "TLS 1.0", locRemoved: true, sans: []string{"gone.example.test"}},

		// 위치 축 (제시 체인, 0018). chain-unknown은 위치는 있는데 컬럼이 NULL인
		// 경우다 — 0018 이전 관측이 danger로 뜨면 재스캔 전 인벤토리가 전부 빨간불이 된다.
		{label: "chain-incomplete", chainComplete: &chainNo, sans: []string{"nochain.example.test"}},
		{label: "chain-complete", chainComplete: &chainYes, sans: []string{"fullchain.example.test"}},
		{label: "chain-unknown", tlsVersion: "TLS 1.3", sans: []string{"chainnull.example.test"}},
		{label: "chain-incomplete-removed", chainComplete: &chainNo, locRemoved: true,
			sans: []string{"nochain-gone.example.test"}},

		// 여러 판정이 겹치는 건 (최고 심각도 규약 확인용)
		{label: "combo-danger", keyAlg: "RSA", keySize: 1024, sigAlg: "SHA1-RSA",
			sans: []string{"*.co.kr"}, validDays: 1200, tlsVersion: "TLS 1.0"},
		{label: "combo-warn", validDays: 900, sans: []string{"*.example.com"}},
		{label: "combo-info", keyAlg: "RSA", keySize: 2048, sigAlg: "SHA256-RSA", sans: []string{"z.example.test"}},

		// 집계에서 빠지는 replaced 건
		{label: "replaced-weak", status: domain.CertStatusReplaced, keyAlg: "RSA", keySize: 1024,
			sigAlg: "SHA256-RSA", sans: []string{"old.example.test"}},
	}
}

// loadRiskFixtures는 픽스처를 적재하고 목록 API(=Go 판정)로 읽어 돌려준다.
func loadRiskFixtures(t *testing.T, st *Store, pool *sql.DB) map[string]CertificateListItem {
	t.Helper()
	ctx := context.Background()

	var targetID string
	if err := pool.QueryRow(`
		INSERT INTO targets (name, target_type, hostname, ip_address)
		VALUES ('risk-fixture-target', 'server', 'risk.example.test', '192.0.2.77'::inet)
		RETURNING id`).Scan(&targetID); err != nil {
		t.Fatalf("insert target: %v", err)
	}
	fixtures := riskFixtures()
	for _, f := range fixtures {
		insertRiskFixture(t, pool, targetID, f)
	}

	// page_size 상한은 200이다 — 픽스처 수가 그보다 적음을 전제로 한 페이지로 읽는다.
	if len(fixtures) > maxPageSize {
		t.Fatalf("fixture count %d exceeds page_size cap %d", len(fixtures), maxPageSize)
	}
	res, err := st.ListCertificates(ctx, ListCertificatesInput{PageSize: maxPageSize})
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if res.Total != len(fixtures) {
		t.Fatalf("total = %d, want %d", res.Total, len(fixtures))
	}
	byID := make(map[string]CertificateListItem, len(res.Items))
	for _, it := range res.Items {
		byID[it.ID] = it
	}
	return byID
}

func idsSorted(ids []string) []string {
	sort.Strings(ids)
	return ids
}

func sameIDs(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

func labelsOf(items map[string]CertificateListItem, ids []string) []string {
	out := make([]string, 0, len(ids))
	for _, id := range ids {
		out = append(out, items[id].CommonName)
	}
	sort.Strings(out)
	return out
}

// SQL 필터가 Go 판정과 같은 집합을 돌려주는지 항목 코드 전건으로 대조한다.
func TestRiskCodeFilterMatchesEvaluator(t *testing.T) {
	pool := testPool(t)
	st := New(pool)
	ctx := context.Background()
	clearInventory(t, pool)
	t.Cleanup(func() { clearInventory(t, pool) })

	items := loadRiskFixtures(t, st, pool)

	for _, rule := range certrisk.Rules() {
		var want []string
		for id, it := range items {
			for _, f := range it.Risk.Findings {
				if f.Code == rule.Code {
					want = append(want, id)
					break
				}
			}
		}
		res, err := st.ListCertificates(ctx, ListCertificatesInput{
			RiskCodes: []certrisk.Code{rule.Code}, PageSize: maxPageSize,
		})
		if err != nil {
			t.Fatalf("list %s: %v", rule.Code, err)
		}
		got := make([]string, 0, len(res.Items))
		for _, it := range res.Items {
			got = append(got, it.ID)
		}
		if !sameIDs(idsSorted(got), idsSorted(want)) {
			t.Errorf("%s: SQL filter = %v, evaluator = %v",
				rule.Code, labelsOf(items, got), labelsOf(items, want))
		}
		if res.Total != len(want) {
			t.Errorf("%s: total = %d, want %d", rule.Code, res.Total, len(want))
		}
		// 규칙마다 최소 1건은 걸려야 대조에 의미가 있다.
		if len(want) == 0 {
			t.Errorf("%s: no fixture triggers this rule", rule.Code)
		}
	}
}

// 심각도 필터는 **최고 심각도** 기준이라 4등급이 인벤토리를 겹침 없이 분할한다.
func TestRiskSeverityFilterPartitionsInventory(t *testing.T) {
	pool := testPool(t)
	st := New(pool)
	ctx := context.Background()
	clearInventory(t, pool)
	t.Cleanup(func() { clearInventory(t, pool) })

	items := loadRiskFixtures(t, st, pool)

	covered := 0
	for _, sev := range certrisk.Severities() {
		var want []string
		for id, it := range items {
			if it.Risk.Severity == sev {
				want = append(want, id)
			}
		}
		res, err := st.ListCertificates(ctx, ListCertificatesInput{
			RiskSeverities: []certrisk.Severity{sev}, PageSize: maxPageSize,
		})
		if err != nil {
			t.Fatalf("list %s: %v", sev, err)
		}
		got := make([]string, 0, len(res.Items))
		for _, it := range res.Items {
			got = append(got, it.ID)
		}
		if !sameIDs(idsSorted(got), idsSorted(want)) {
			t.Errorf("%s: SQL filter = %v, evaluator = %v",
				sev, labelsOf(items, got), labelsOf(items, want))
		}
		if len(want) == 0 {
			t.Errorf("%s: no fixture has this maximum severity", sev)
		}
		covered += len(got)
	}
	if covered != len(items) {
		t.Errorf("severity partition covers %d of %d certificates", covered, len(items))
	}
}

// 다른 필터와 겹칠 때 AND로 결합되는지 (필터 계열 간 독립).
func TestRiskFilterCombinesWithOtherFilters(t *testing.T) {
	pool := testPool(t)
	st := New(pool)
	ctx := context.Background()
	clearInventory(t, pool)
	t.Cleanup(func() { clearInventory(t, pool) })

	items := loadRiskFixtures(t, st, pool)

	res, err := st.ListCertificates(ctx, ListCertificatesInput{
		RiskCodes:       []certrisk.Code{certrisk.CodeWeakKey},
		ExcludeStatuses: []domain.CertificateStatus{domain.CertStatusReplaced},
		PageSize:        maxPageSize,
	})
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	for _, it := range res.Items {
		if it.Status == domain.CertStatusReplaced {
			t.Errorf("%s: replaced certificate leaked through exclude_status", it.CommonName)
		}
	}
	// replaced 픽스처(replaced-weak)는 weak_key이므로 제외 필터가 실제로 일을 해야 한다.
	all, err := st.ListCertificates(ctx, ListCertificatesInput{
		RiskCodes: []certrisk.Code{certrisk.CodeWeakKey}, PageSize: maxPageSize,
	})
	if err != nil {
		t.Fatalf("list all: %v", err)
	}
	if all.Total != res.Total+1 {
		t.Errorf("weak_key total = %d with exclude, %d without; want a difference of 1",
			res.Total, all.Total)
	}

	// 코드 필터 여러 개는 OR다.
	multi, err := st.ListCertificates(ctx, ListCertificatesInput{
		RiskCodes: []certrisk.Code{certrisk.CodeNoSAN, certrisk.CodeWildcardBroad},
		PageSize:  maxPageSize,
	})
	if err != nil {
		t.Fatalf("list multi: %v", err)
	}
	var want int
	for _, it := range items {
		for _, f := range it.Risk.Findings {
			if f.Code == certrisk.CodeNoSAN || f.Code == certrisk.CodeWildcardBroad {
				want++
				break
			}
		}
	}
	if multi.Total != want {
		t.Errorf("no_san OR wildcard_broad = %d, want %d", multi.Total, want)
	}
}

// 집계는 최고 심각도 분할(합 = total) + 항목별 건수이며 replaced를 제외한다.
func TestCertificateRisksAggregation(t *testing.T) {
	pool := testPool(t)
	st := New(pool)
	ctx := context.Background()
	clearInventory(t, pool)
	t.Cleanup(func() { clearInventory(t, pool) })

	items := loadRiskFixtures(t, st, pool)

	sum, err := st.CertificateRisks(ctx)
	if err != nil {
		t.Fatalf("risks: %v", err)
	}

	live := map[string]CertificateListItem{}
	for id, it := range items {
		if it.Status != domain.CertStatusReplaced {
			live[id] = it
		}
	}
	if sum.Total != len(live) {
		t.Errorf("total = %d, want %d (replaced excluded)", sum.Total, len(live))
	}

	sevTotal := 0
	for _, sc := range sum.Severities {
		want := 0
		for _, it := range live {
			if it.Risk.Severity == sc.Severity {
				want++
			}
		}
		if sc.Count != want {
			t.Errorf("severity %s count = %d, want %d", sc.Severity, sc.Count, want)
		}
		sevTotal += sc.Count
	}
	if sevTotal != sum.Total {
		t.Errorf("severity counts sum to %d, want total %d", sevTotal, sum.Total)
	}

	if len(sum.Findings) != len(certrisk.Rules()) {
		t.Fatalf("findings = %d, want the full catalog (%d)", len(sum.Findings), len(certrisk.Rules()))
	}
	for _, fc := range sum.Findings {
		want := 0
		for _, it := range live {
			for _, f := range it.Risk.Findings {
				if f.Code == fc.Code {
					want++
					break
				}
			}
		}
		if fc.Count != want {
			t.Errorf("finding %s count = %d, want %d", fc.Code, fc.Count, want)
		}
	}
}

// 상세 조회는 실제 위치로 위치 축을 판정한다 (removed 위치는 빼고).
func TestCertificateDetailCarriesRisk(t *testing.T) {
	pool := testPool(t)
	st := New(pool)
	ctx := context.Background()
	clearInventory(t, pool)
	t.Cleanup(func() { clearInventory(t, pool) })

	items := loadRiskFixtures(t, st, pool)

	find := func(cn string) CertificateListItem {
		for _, it := range items {
			if it.CommonName == cn {
				return it
			}
		}
		t.Fatalf("fixture %q not found", cn)
		return CertificateListItem{}
	}

	legacy := find("tls10")
	d, err := st.GetCertificateDetail(ctx, legacy.ID)
	if err != nil {
		t.Fatalf("detail: %v", err)
	}
	var found bool
	for _, f := range d.Risk.Findings {
		if f.Code == certrisk.CodeLegacyTLS {
			found = true
			if f.Scope != certrisk.ScopeLocation {
				t.Errorf("legacy_tls scope = %q, want location", f.Scope)
			}
		}
	}
	if !found {
		t.Errorf("detail risk = %+v, want legacy_tls", d.Risk.Findings)
	}

	removed := find("tls10-removed")
	d2, err := st.GetCertificateDetail(ctx, removed.ID)
	if err != nil {
		t.Fatalf("detail: %v", err)
	}
	for _, f := range d2.Risk.Findings {
		if f.Code == certrisk.CodeLegacyTLS {
			t.Error("removed location should not produce legacy_tls in detail")
		}
	}
}

// 상세 조회의 중간 CA 누락 판정 (0018). 미상(NULL)은 판정되지 않아야 한다 —
// 모르는 것을 danger로 띄우면 재스캔 전 인벤토리가 전부 빨간불이 된다.
func TestCertificateDetailCarriesIncompleteChain(t *testing.T) {
	pool := testPool(t)
	st := New(pool)
	ctx := context.Background()
	clearInventory(t, pool)
	t.Cleanup(func() { clearInventory(t, pool) })

	items := loadRiskFixtures(t, st, pool)
	find := func(cn string) CertificateListItem {
		for _, it := range items {
			if it.CommonName == cn {
				return it
			}
		}
		t.Fatalf("fixture %q not found", cn)
		return CertificateListItem{}
	}
	hasIncomplete := func(id string) bool {
		t.Helper()
		d, err := st.GetCertificateDetail(ctx, id)
		if err != nil {
			t.Fatalf("detail: %v", err)
		}
		for _, f := range d.Risk.Findings {
			if f.Code == certrisk.CodeIncompleteChain {
				if f.Scope != certrisk.ScopeLocation {
					t.Errorf("incomplete_chain scope = %q, want location", f.Scope)
				}
				return true
			}
		}
		return false
	}

	if !hasIncomplete(find("chain-incomplete").ID) {
		t.Error("chain_complete=false가 상세에서 판정되지 않았다")
	}
	if hasIncomplete(find("chain-complete").ID) {
		t.Error("chain_complete=true가 판정에 걸렸다")
	}
	if hasIncomplete(find("chain-unknown").ID) {
		t.Error("chain_complete=NULL(0018 이전 관측)이 danger로 떴다")
	}
	if hasIncomplete(find("chain-incomplete-removed").ID) {
		t.Error("removed 위치가 판정에 걸렸다")
	}

	// 상세 응답의 위치에도 사실이 실려야 한다(화면이 어느 위치인지 짚을 수 있게).
	d, err := st.GetCertificateDetail(ctx, find("chain-incomplete").ID)
	if err != nil {
		t.Fatalf("detail: %v", err)
	}
	if len(d.Locations) != 1 || d.Locations[0].ChainComplete == nil || *d.Locations[0].ChainComplete {
		t.Errorf("위치의 chain_complete가 응답에 실리지 않았다: %+v", d.Locations)
	}
}
