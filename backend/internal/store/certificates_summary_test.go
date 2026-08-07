package store

// 이슈 #8(만료 대시보드) store 계층 테스트 — 요약 집계와 파생 필터(자체 서명·소유자 없음).
// 픽스처는 실행 시점에 생성한다 — 실제 키·인증서를 커밋하지 않는다 (보안 하드 규칙 4).
// TEST_DATABASE_URL 미설정 시 스킵 (전용 테스트 DB — 메인 DB에 돌리지 않는다).

import (
	"context"
	"database/sql"
	"testing"
	"time"

	"github.com/Falke060918/INHERIT_AutoCert/backend/internal/domain"
)

// insertFixtureUser는 owner_id 픽스처용 사용자 1건을 INSERT한다.
// password_hash는 자리 표시 문자열이다 — 로그인에 쓰이지 않는다 (실제 해시 아님).
func insertFixtureUser(t *testing.T, pool *sql.DB, username string) string {
	t.Helper()
	var id string
	if err := pool.QueryRow(`
		INSERT INTO users (username, password_hash, role)
		VALUES ($1, 'test-fixture-not-a-real-hash', 'viewer')
		RETURNING id`, username).Scan(&id); err != nil {
		t.Fatalf("insert fixture user: %v", err)
	}
	t.Cleanup(func() {
		if _, err := pool.Exec(`DELETE FROM users WHERE username = $1`, username); err != nil {
			t.Errorf("cleanup fixture user: %v", err)
		}
	})
	return id
}

// seedSummaryFixtures는 요약·파생 필터 테스트 공용 픽스처를 시딩한다.
//   - 만료 5일 전 1건 (7/30/60/90일 구간에 모두 포함, 자체 서명)
//   - 만료 20일 전 1건 (30/60/90일 구간, 소유자 지정)
//   - 만료 45일 전 1건 (60/90일 구간)
//   - 만료 80일 전 1건 (90일 구간)
//   - 만료 200일 전 1건 (어느 구간에도 없음)
//   - 이미 만료 1건 (자체 서명)
func seedSummaryFixtures(t *testing.T, pool *sql.DB) {
	t.Helper()
	now := time.Now()
	ownerID := insertFixtureUser(t, pool, "summary-fixture-owner")

	fixtures := []listFixture{
		{cn: "d5.example.test", serial: "s1", notAfter: now.Add(5 * 24 * time.Hour),
			ctype: domain.CertTypeServer, status: domain.CertStatusActive, selfSigned: true},
		{cn: "d20.example.test", serial: "s2", notAfter: now.Add(20 * 24 * time.Hour),
			ctype: domain.CertTypeServer, status: domain.CertStatusActive, ownerID: &ownerID},
		{cn: "d45.example.test", serial: "s3", notAfter: now.Add(45 * 24 * time.Hour),
			ctype: domain.CertTypeServer, status: domain.CertStatusActive},
		{cn: "d80.example.test", serial: "s4", notAfter: now.Add(80 * 24 * time.Hour),
			ctype: domain.CertTypeServer, status: domain.CertStatusActive},
		{cn: "d200.example.test", serial: "s5", notAfter: now.Add(200 * 24 * time.Hour),
			ctype: domain.CertTypeServer, status: domain.CertStatusActive},
		{cn: "gone.example.test", serial: "s6", notAfter: now.Add(-10 * 24 * time.Hour),
			ctype: domain.CertTypeServer, status: domain.CertStatusExpired, selfSigned: true},
	}
	for _, f := range fixtures {
		insertListFixture(t, pool, f)
	}
}

func TestCertificateSummary(t *testing.T) {
	pool := testPool(t)
	st := New(pool)
	ctx := context.Background()
	clearInventory(t, pool)
	t.Cleanup(func() { clearInventory(t, pool) })
	seedSummaryFixtures(t, pool)

	sum, err := st.CertificateSummary(ctx)
	if err != nil {
		t.Fatalf("summary: %v", err)
	}
	if sum.Total != 6 {
		t.Errorf("total = %d, want 6", sum.Total)
	}
	if sum.Expiring7d != 1 {
		t.Errorf("expiring_7d = %d, want 1", sum.Expiring7d)
	}
	if sum.Expiring30d != 2 {
		t.Errorf("expiring_30d = %d, want 2", sum.Expiring30d)
	}
	if sum.Expiring60d != 3 {
		t.Errorf("expiring_60d = %d, want 3", sum.Expiring60d)
	}
	if sum.Expiring90d != 4 {
		t.Errorf("expiring_90d = %d, want 4", sum.Expiring90d)
	}
	if sum.Expired != 1 {
		t.Errorf("expired = %d, want 1", sum.Expired)
	}
	if sum.SelfSigned != 2 {
		t.Errorf("self_signed = %d, want 2", sum.SelfSigned)
	}
	if sum.Unowned != 5 {
		t.Errorf("unowned = %d, want 5", sum.Unowned)
	}
}

func TestListCertificatesSelfSignedUnownedFilters(t *testing.T) {
	pool := testPool(t)
	st := New(pool)
	ctx := context.Background()
	clearInventory(t, pool)
	t.Cleanup(func() { clearInventory(t, pool) })
	seedSummaryFixtures(t, pool)

	list := func(t *testing.T, in ListCertificatesInput) *CertificateList {
		t.Helper()
		res, err := st.ListCertificates(ctx, in)
		if err != nil {
			t.Fatalf("list: %v", err)
		}
		return res
	}
	yes, no := true, false

	t.Run("self_signed=true matches subject_dn == issuer_dn", func(t *testing.T) {
		res := list(t, ListCertificatesInput{SelfSigned: &yes})
		if res.Total != 2 {
			t.Errorf("self_signed=true total = %d, want 2", res.Total)
		}
	})
	t.Run("self_signed=false excludes them", func(t *testing.T) {
		if res := list(t, ListCertificatesInput{SelfSigned: &no}); res.Total != 4 {
			t.Errorf("self_signed=false total = %d, want 4", res.Total)
		}
	})
	t.Run("unowned=true matches owner_id IS NULL", func(t *testing.T) {
		res := list(t, ListCertificatesInput{Unowned: &yes})
		if res.Total != 5 {
			t.Errorf("unowned=true total = %d, want 5", res.Total)
		}
	})
	t.Run("unowned=false matches owned only", func(t *testing.T) {
		res := list(t, ListCertificatesInput{Unowned: &no})
		if res.Total != 1 || res.Items[0].CommonName != "d20.example.test" {
			t.Errorf("unowned=false total = %d, want 1 (d20.example.test)", res.Total)
		}
	})
	t.Run("combines with existing derived filters", func(t *testing.T) {
		days := 30
		res := list(t, ListCertificatesInput{SelfSigned: &yes, ExpiresWithinDays: &days})
		if res.Total != 1 || res.Items[0].CommonName != "d5.example.test" {
			t.Errorf("self_signed+expires_within_days total = %d, want 1 (d5.example.test)", res.Total)
		}
	})
}
