package store

// 픽스처는 전부 테스트 실행 시점에 생성한다 — 실제 키·인증서를 커밋하지 않는다
// (CLAUDE.md 보안 하드 규칙 4).
// 주의: db 패키지의 마이그레이션 테스트가 같은 DB를 down/up 하므로
// 전체 테스트는 `go test -p 1 ./...`로 패키지를 직렬 실행한다 (docs/MAP.md).

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"crypto/x509/pkix"
	"database/sql"
	"math/big"
	"os"
	"regexp"
	"testing"
	"time"

	"github.com/Falke060918/INHERIT_AutoCert/backend/internal/certparse"
	"github.com/Falke060918/INHERIT_AutoCert/backend/internal/db"
	"github.com/Falke060918/INHERIT_AutoCert/backend/internal/domain"
)

// testPool은 TEST_DATABASE_URL로 연결한다. 미설정이면 스킵한다.
func testPool(t *testing.T) *sql.DB {
	t.Helper()
	dsn := os.Getenv("TEST_DATABASE_URL")
	if dsn == "" {
		t.Skip("TEST_DATABASE_URL not set; skipping store test")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	pool, err := db.Connect(ctx, dsn)
	if err != nil {
		t.Fatalf("connect: %v", err)
	}
	t.Cleanup(func() { pool.Close() })
	if err := db.Migrate(pool); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	return pool
}

func newTestCert(t *testing.T) domain.Certificate {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatalf("generate key: %v", err)
	}
	tmpl := &x509.Certificate{
		SerialNumber: big.NewInt(7),
		Subject:      pkix.Name{CommonName: "store-fixture.example.test"},
		NotBefore:    time.Now().Add(-time.Hour),
		NotAfter:     time.Now().Add(90 * 24 * time.Hour),
		KeyUsage:     x509.KeyUsageDigitalSignature,
		ExtKeyUsage:  []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
		DNSNames:     []string{"store-fixture.example.test"},
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &key.PublicKey, key)
	if err != nil {
		t.Fatalf("create certificate: %v", err)
	}
	cert, err := x509.ParseCertificate(der)
	if err != nil {
		t.Fatalf("parse certificate: %v", err)
	}
	return certparse.Describe(cert, time.Now())
}

// newTestChain은 [CA, 리프] 순서(리프가 [0]이 아님)의 체인 픽스처를 만든다.
func newTestChain(t *testing.T) (ca, leaf domain.Certificate) {
	t.Helper()
	caKey, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatalf("generate ca key: %v", err)
	}
	caTmpl := &x509.Certificate{
		SerialNumber:          big.NewInt(100),
		Subject:               pkix.Name{CommonName: "store-fixture-ca.example.test"},
		NotBefore:             time.Now().Add(-time.Hour),
		NotAfter:              time.Now().Add(365 * 24 * time.Hour),
		KeyUsage:              x509.KeyUsageCertSign,
		IsCA:                  true,
		BasicConstraintsValid: true,
	}
	caDER, err := x509.CreateCertificate(rand.Reader, caTmpl, caTmpl, &caKey.PublicKey, caKey)
	if err != nil {
		t.Fatalf("create ca certificate: %v", err)
	}
	caCert, err := x509.ParseCertificate(caDER)
	if err != nil {
		t.Fatalf("parse ca certificate: %v", err)
	}

	leafKey, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatalf("generate leaf key: %v", err)
	}
	leafTmpl := &x509.Certificate{
		SerialNumber: big.NewInt(101),
		Subject:      pkix.Name{CommonName: "store-fixture-leaf.example.test"},
		NotBefore:    time.Now().Add(-time.Hour),
		NotAfter:     time.Now().Add(90 * 24 * time.Hour),
		KeyUsage:     x509.KeyUsageDigitalSignature,
		ExtKeyUsage:  []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
		DNSNames:     []string{"store-fixture-leaf.example.test"},
	}
	leafDER, err := x509.CreateCertificate(rand.Reader, leafTmpl, caCert, &leafKey.PublicKey, caKey)
	if err != nil {
		t.Fatalf("create leaf certificate: %v", err)
	}
	leafCert, err := x509.ParseCertificate(leafDER)
	if err != nil {
		t.Fatalf("parse leaf certificate: %v", err)
	}
	now := time.Now()
	return certparse.Describe(caCert, now), certparse.Describe(leafCert, now)
}

// TestImportDeduplicatesByFingerprint는 이슈 #3 완료 조건을 검증한다:
// 동일 인증서를 두 번 업로드해도 Certificate 레코드는 1개이고
// CertificateLocation만 늘어난다. 업로드마다 AuditEvent가 남는다.
func TestImportDeduplicatesByFingerprint(t *testing.T) {
	pool := testPool(t)
	st := New(pool)
	ctx := context.Background()

	d := newTestCert(t)
	fileName := "store-fixture.pem"
	reqID := "test-req-import-dedup"
	srcIP := "203.0.113.7" // 문서화용 예약 대역 — 실제 주소 아님

	t.Cleanup(func() {
		// 테스트 데이터 정리 (locations는 FK CASCADE로 함께 삭제).
		_, _ = pool.Exec(`DELETE FROM certificates WHERE fingerprint_sha256 = $1`, d.FingerprintSHA256)
		_, _ = pool.Exec(`DELETE FROM audit_events WHERE request_id = $1`, reqID)
	})

	in := ImportInput{
		Certificates: []domain.Certificate{d},
		FileName:     &fileName,
		RequestID:    &reqID,
		SourceIP:     &srcIP,
	}

	res1, err := st.ImportCertificates(ctx, in)
	if err != nil {
		t.Fatalf("first import: %v", err)
	}
	if len(res1.Certificates) != 1 || !res1.Certificates[0].Created {
		t.Fatalf("first import: Created = false, want true")
	}
	if res1.LocationID == "" {
		t.Fatal("first import: no location id")
	}

	res2, err := st.ImportCertificates(ctx, in)
	if err != nil {
		t.Fatalf("second import: %v", err)
	}
	if res2.Certificates[0].Created {
		t.Error("second import: Created = true, want false (duplicate)")
	}
	if res2.Certificates[0].Certificate.ID != res1.Certificates[0].Certificate.ID {
		t.Error("second import returned a different certificate id")
	}

	var certCount, locCount, auditCount int
	if err := pool.QueryRow(`SELECT count(*) FROM certificates WHERE fingerprint_sha256 = $1`,
		d.FingerprintSHA256).Scan(&certCount); err != nil {
		t.Fatalf("count certificates: %v", err)
	}
	if certCount != 1 {
		t.Errorf("certificates = %d, want 1", certCount)
	}
	if err := pool.QueryRow(`SELECT count(*) FROM certificate_locations WHERE certificate_id = $1`,
		res1.Certificates[0].Certificate.ID).Scan(&locCount); err != nil {
		t.Fatalf("count locations: %v", err)
	}
	if locCount != 2 {
		t.Errorf("locations = %d, want 2", locCount)
	}
	if err := pool.QueryRow(`SELECT count(*) FROM audit_events WHERE request_id = $1 AND event_type = $2`,
		reqID, EventCertificateImport).Scan(&auditCount); err != nil {
		t.Fatalf("count audit events: %v", err)
	}
	if auditCount != 2 {
		t.Errorf("audit events = %d, want 2", auditCount)
	}

	// inet 컬럼 왕복 확인 (reviewer 지적: *string ↔ inet 변환) — host()로 문자열 복원.
	var gotIP string
	var integrity string
	if err := pool.QueryRow(
		`SELECT host(source_ip), integrity_hash FROM audit_events WHERE request_id = $1 ORDER BY id LIMIT 1`,
		reqID).Scan(&gotIP, &integrity); err != nil {
		t.Fatalf("read audit event: %v", err)
	}
	if gotIP != srcIP {
		t.Errorf("source_ip = %q, want %q", gotIP, srcIP)
	}
	if !regexp.MustCompile(`^[0-9a-f]{64}$`).MatchString(integrity) {
		t.Errorf("integrity_hash %q is not lowercase hex 64", integrity)
	}
}

// TestImportAttachesLocationToLeaf는 체인이 CA 먼저([0]=CA) 순서로 와도
// 설치 위치가 리프(IsCA=false)에 붙는지 검증한다.
func TestImportAttachesLocationToLeaf(t *testing.T) {
	pool := testPool(t)
	st := New(pool)
	ctx := context.Background()

	ca, leaf := newTestChain(t)
	fileName := "store-fixture-chain.pem"
	reqID := "test-req-import-leaf"

	t.Cleanup(func() {
		_, _ = pool.Exec(`DELETE FROM certificates WHERE fingerprint_sha256 IN ($1, $2)`,
			ca.FingerprintSHA256, leaf.FingerprintSHA256)
		_, _ = pool.Exec(`DELETE FROM audit_events WHERE request_id = $1`, reqID)
	})

	res, err := st.ImportCertificates(ctx, ImportInput{
		Certificates: []domain.Certificate{ca, leaf}, // CA가 [0]
		FileName:     &fileName,
		RequestID:    &reqID,
	})
	if err != nil {
		t.Fatalf("import chain: %v", err)
	}

	var gotCertID string
	if err := pool.QueryRow(`SELECT certificate_id FROM certificate_locations WHERE id = $1`,
		res.LocationID).Scan(&gotCertID); err != nil {
		t.Fatalf("read location: %v", err)
	}
	var leafID string
	for _, ic := range res.Certificates {
		if ic.Certificate.FingerprintSHA256 == leaf.FingerprintSHA256 {
			leafID = ic.Certificate.ID
		}
	}
	if leafID == "" {
		t.Fatal("leaf certificate not in import result")
	}
	if gotCertID != leafID {
		t.Errorf("location attached to certificate %s, want leaf %s", gotCertID, leafID)
	}
}

// TestRecordImportFailure는 거부된 업로드도 감사 이벤트를 남기는지 검증한다
// (보안 하드 규칙 5).
func TestRecordImportFailure(t *testing.T) {
	pool := testPool(t)
	st := New(pool)
	reqID := "test-req-import-failure"
	t.Cleanup(func() {
		_, _ = pool.Exec(`DELETE FROM audit_events WHERE request_id = $1`, reqID)
	})

	if err := st.RecordImportFailure(context.Background(), "private_key_rejected", &reqID, nil); err != nil {
		t.Fatalf("record failure: %v", err)
	}
	var result string
	if err := pool.QueryRow(
		`SELECT result FROM audit_events WHERE request_id = $1`, reqID).Scan(&result); err != nil {
		t.Fatalf("read audit event: %v", err)
	}
	if result != string(domain.AuditFailure) {
		t.Errorf("result = %q, want failure", result)
	}
}
