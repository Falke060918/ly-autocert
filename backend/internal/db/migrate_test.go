package db

import (
	"context"
	"database/sql"
	"errors"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/golang-migrate/migrate/v4"
)

// coreTables는 마이그레이션이 만드는 테이블 전체다.
// 이슈 #2: 완전 구현 4개 + placeholder 4개. 이슈 #6: users/sessions/departments.
var coreTables = []string{
	"certificates",
	"certificate_locations",
	"targets",
	"audit_events",
	"certificate_authorities",
	"renewal_jobs",
	"deployment_jobs",
	"policies",
	"users",
	"sessions",
	"departments",
}

// testPool은 TEST_DATABASE_URL로 연결한다. 미설정이면 스킵한다.
// 주의: 이 테스트는 down 마이그레이션으로 스키마를 전부 지우므로 전용 테스트 DB를 쓴다.
func testPool(t *testing.T) *sql.DB {
	t.Helper()
	dsn := os.Getenv("TEST_DATABASE_URL")
	if dsn == "" {
		t.Skip("TEST_DATABASE_URL not set; skipping migration test")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	pool, err := Connect(ctx, dsn)
	if err != nil {
		t.Fatalf("connect: %v", err)
	}
	t.Cleanup(func() { pool.Close() })
	return pool
}

func tableExists(t *testing.T, pool *sql.DB, name string) bool {
	t.Helper()
	var n int
	err := pool.QueryRow(
		`SELECT count(*) FROM information_schema.tables
		 WHERE table_schema = 'public' AND table_name = $1`, name).Scan(&n)
	if err != nil {
		t.Fatalf("table check %s: %v", name, err)
	}
	return n == 1
}

// TestMigrateUpDownUp은 이슈 #2 완료 조건을 검증한다:
// up/down이 모두 성공하고, up 재적용이 안전해야 한다.
func TestMigrateUpDownUp(t *testing.T) {
	pool := testPool(t)
	m, err := newMigrator(pool)
	if err != nil {
		t.Fatalf("new migrator: %v", err)
	}

	// up: 전체 적용
	if err := m.Up(); err != nil && !errors.Is(err, migrate.ErrNoChange) {
		t.Fatalf("migrate up: %v", err)
	}
	for _, tbl := range coreTables {
		if !tableExists(t, pool, tbl) {
			t.Errorf("after up: table %q missing", tbl)
		}
	}

	// down: 전체 롤백
	if err := m.Down(); err != nil && !errors.Is(err, migrate.ErrNoChange) {
		t.Fatalf("migrate down: %v", err)
	}
	for _, tbl := range coreTables {
		if tableExists(t, pool, tbl) {
			t.Errorf("after down: table %q still exists", tbl)
		}
	}

	// up 재적용: 중복 실행 안전
	if err := m.Up(); err != nil && !errors.Is(err, migrate.ErrNoChange) {
		t.Fatalf("migrate up (again): %v", err)
	}
	for _, tbl := range coreTables {
		if !tableExists(t, pool, tbl) {
			t.Errorf("after re-up: table %q missing", tbl)
		}
	}
}

// TestCertificateConstraints는 핵심 제약을 검증한다.
// 픽스처는 합성 값만 쓴다(실제 인증서·키 하드코딩 금지).
func TestCertificateConstraints(t *testing.T) {
	pool := testPool(t)
	if err := Migrate(pool); err != nil {
		t.Fatalf("migrate: %v", err)
	}

	const insertCert = `
		INSERT INTO certificates (
			fingerprint_sha256, serial_number, subject_dn, issuer_dn, common_name,
			not_before, not_after, public_key_algorithm, public_key_size,
			signature_algorithm, certificate_type, status
		) VALUES ($1, '01', 'CN=test-fixture', 'CN=test-fixture-ca', 'test-fixture',
			now(), now() + interval '90 days', 'RSA', 2048, 'SHA256-RSA', 'server', $2)`

	// 트랜잭션 롤백으로 테스트 데이터 잔류를 막는다.
	tx, err := pool.Begin()
	if err != nil {
		t.Fatalf("begin: %v", err)
	}
	defer tx.Rollback() //nolint:errcheck

	fp := strings.Repeat("ab", 32) // 64자 소문자 hex, 합성 값
	if _, err := tx.Exec(insertCert, fp, "active"); err != nil {
		t.Fatalf("insert first cert: %v", err)
	}
	// fingerprint_sha256 UNIQUE: 같은 지문 재삽입은 실패해야 한다.
	if _, err := tx.Exec(insertCert, fp, "active"); err == nil {
		t.Error("duplicate fingerprint insert succeeded; want unique violation")
	}
	_ = tx.Rollback()

	// status CHECK: 정의되지 않은 상태값은 거부해야 한다.
	tx2, err := pool.Begin()
	if err != nil {
		t.Fatalf("begin: %v", err)
	}
	defer tx2.Rollback() //nolint:errcheck
	fp2 := strings.Repeat("cd", 32)
	if _, err := tx2.Exec(insertCert, fp2, "not-a-status"); err == nil {
		t.Error("invalid status insert succeeded; want check violation")
	}
}
