package store

// 0018 원문·체인 저장의 INSERT 경로 검증.
//
// 컬럼만 늘리고 INSERT 경로를 빠뜨리면 백필은 초록불인데 앞으로 생기는 행이
// 전부 NULL로 남는다 (0015 scan_jobs.origin 전례). 여기서 보는 것은 그 함정이다.
// 픽스처는 전부 테스트 실행 시점에 생성한다 (보안 하드 규칙 4).
// TEST_DATABASE_URL 미설정 시 스킵.

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"crypto/x509/pkix"
	"database/sql"
	"math/big"
	"strings"
	"testing"
	"time"

	"github.com/Falke060918/INHERIT_AutoCert/backend/internal/certparse"
	"github.com/Falke060918/INHERIT_AutoCert/backend/internal/domain"
)

// contentRow는 0018 컬럼을 그대로 읽은 것이다. raw_der는 길이만 본다 —
// 원문 바이트는 테스트 실패 메시지에도 싣지 않는다 (보안 하드 규칙 3).
type contentRow struct {
	rawDERLen  int
	rawDERNull bool
	spki       sql.NullString
	keyUsage   sql.NullInt64
	eku        sql.NullString
	isCA       sql.NullBool
	pathLen    sql.NullInt64
	ocsp       sql.NullString
	crl        sql.NullString
	aia        sql.NullString
}

func readContentRow(t *testing.T, pool *sql.DB, fingerprint string) contentRow {
	t.Helper()
	var r contentRow
	var derLen sql.NullInt64
	err := pool.QueryRow(`
		SELECT length(raw_der), spki_sha256, key_usage,
		       array_to_string(ext_key_usage, chr(31)), is_ca, path_len_constraint,
		       array_to_string(ocsp_urls, chr(31)), array_to_string(crl_urls, chr(31)),
		       array_to_string(aia_ca_issuer_urls, chr(31))
		  FROM certificates WHERE fingerprint_sha256 = $1`, fingerprint).
		Scan(&derLen, &r.spki, &r.keyUsage, &r.eku, &r.isCA, &r.pathLen, &r.ocsp, &r.crl, &r.aia)
	if err != nil {
		t.Fatalf("read content row: %v", err)
	}
	r.rawDERNull = !derLen.Valid
	if derLen.Valid {
		r.rawDERLen = int(derLen.Int64)
	}
	return r
}

// newContentFixtureCert는 0018 컬럼이 전부 값을 갖도록 확장을 채운 리프를 만든다.
func newContentFixtureCert(t *testing.T, cn string) domain.Certificate {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatalf("generate key: %v", err)
	}
	tmpl := &x509.Certificate{
		SerialNumber:          big.NewInt(time.Now().UnixNano()),
		Subject:               pkix.Name{CommonName: cn},
		NotBefore:             time.Now().Add(-time.Hour),
		NotAfter:              time.Now().Add(90 * 24 * time.Hour),
		KeyUsage:              x509.KeyUsageDigitalSignature | x509.KeyUsageKeyEncipherment,
		ExtKeyUsage:           []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth, x509.ExtKeyUsageClientAuth},
		DNSNames:              []string{cn},
		OCSPServer:            []string{"http://ocsp.example.test"},
		CRLDistributionPoints: []string{"http://crl.example.test/a.crl"},
		IssuingCertificateURL: []string{"http://aia.example.test/ca.cer"},
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

// 업로드 경로(ImportCertificates)가 0018 컬럼을 실제로 채우는지.
func TestImportFillsCertificateContent(t *testing.T) {
	pool := testPool(t)
	st := New(pool)
	ctx := context.Background()

	d := newContentFixtureCert(t, "content-import.example.test")
	reqID := "test-req-content-import"
	t.Cleanup(func() {
		_, _ = pool.Exec(`DELETE FROM certificates WHERE fingerprint_sha256 = $1`, d.FingerprintSHA256)
		cleanupAuditByRequestID(pool, reqID)
	})

	if _, err := st.ImportCertificates(ctx, ImportInput{
		Certificates: []domain.Certificate{d}, RequestID: &reqID,
	}); err != nil {
		t.Fatalf("import: %v", err)
	}

	r := readContentRow(t, pool, d.FingerprintSHA256)
	if r.rawDERNull || r.rawDERLen == 0 {
		t.Error("raw_der가 저장되지 않았다 — INSERT 경로 누락")
	}
	if r.rawDERLen != len(d.Content.RawDER) {
		t.Errorf("raw_der 길이 = %d, want %d", r.rawDERLen, len(d.Content.RawDER))
	}
	if !r.spki.Valid || r.spki.String != *d.Content.SPKISHA256 {
		t.Error("spki_sha256가 저장되지 않았거나 값이 다르다")
	}
	if !r.keyUsage.Valid || int(r.keyUsage.Int64) != *d.Content.KeyUsage {
		t.Errorf("key_usage = %v, want %d", r.keyUsage, *d.Content.KeyUsage)
	}
	if !r.eku.Valid || r.eku.String != "server_auth"+sansSep+"client_auth" {
		t.Errorf("ext_key_usage = %q", r.eku.String)
	}
	if !r.isCA.Valid || r.isCA.Bool {
		t.Errorf("is_ca = %v, want false", r.isCA)
	}
	if r.pathLen.Valid {
		t.Errorf("path_len_constraint = %v, want NULL (리프에 비적용)", r.pathLen.Int64)
	}
	if !r.ocsp.Valid || r.ocsp.String != "http://ocsp.example.test" {
		t.Errorf("ocsp_urls = %q", r.ocsp.String)
	}
	if !r.crl.Valid || !r.aia.Valid {
		t.Error("crl_urls·aia_ca_issuer_urls가 저장되지 않았다")
	}
}

// 확장이 없는 인증서는 NULL(미상)이 아니라 빈 배열이어야 한다 —
// 0018 이전 행의 NULL과 구분되지 않으면 소급 재계산 대상을 고를 수 없다.
func TestImportDistinguishesEmptyFromUnknown(t *testing.T) {
	pool := testPool(t)
	st := New(pool)
	ctx := context.Background()

	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatalf("generate key: %v", err)
	}
	tmpl := &x509.Certificate{
		SerialNumber: big.NewInt(time.Now().UnixNano()),
		Subject:      pkix.Name{CommonName: "content-bare.example.test"},
		NotBefore:    time.Now().Add(-time.Hour),
		NotAfter:     time.Now().Add(24 * time.Hour),
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &key.PublicKey, key)
	if err != nil {
		t.Fatalf("create certificate: %v", err)
	}
	cert, err := x509.ParseCertificate(der)
	if err != nil {
		t.Fatalf("parse certificate: %v", err)
	}
	d := certparse.Describe(cert, time.Now())

	reqID := "test-req-content-bare"
	t.Cleanup(func() {
		_, _ = pool.Exec(`DELETE FROM certificates WHERE fingerprint_sha256 = $1`, d.FingerprintSHA256)
		cleanupAuditByRequestID(pool, reqID)
	})
	if _, err := st.ImportCertificates(ctx, ImportInput{
		Certificates: []domain.Certificate{d}, RequestID: &reqID,
	}); err != nil {
		t.Fatalf("import: %v", err)
	}

	var ekuNull, ocspNull bool
	var ekuLen, ocspLen int
	if err := pool.QueryRow(`
		SELECT ext_key_usage IS NULL, coalesce(array_length(ext_key_usage, 1), 0),
		       ocsp_urls IS NULL, coalesce(array_length(ocsp_urls, 1), 0)
		  FROM certificates WHERE fingerprint_sha256 = $1`, d.FingerprintSHA256).
		Scan(&ekuNull, &ekuLen, &ocspNull, &ocspLen); err != nil {
		t.Fatalf("read arrays: %v", err)
	}
	if ekuNull || ocspNull {
		t.Error("값이 없는 확장이 NULL로 들어갔다 — '미상'과 구분되지 않는다")
	}
	if ekuLen != 0 || ocspLen != 0 {
		t.Errorf("빈 배열이 아니다: eku %d / ocsp %d", ekuLen, ocspLen)
	}
}

// 0018 이전 행(원문 없이 들어온 행)은 NULL로 남고, 같은 인증서를 원문과 함께
// 다시 저장하면 그 자리에서 채워진다 — 소급 백필은 불가능하고 재스캔·재업로드가
// 정상 경로라는 계약이다.
func TestUpsertBackfillsContentOnReimport(t *testing.T) {
	pool := testPool(t)
	st := New(pool)
	ctx := context.Background()

	d := newContentFixtureCert(t, "content-legacy.example.test")
	reqID := "test-req-content-legacy"
	t.Cleanup(func() {
		_, _ = pool.Exec(`DELETE FROM certificates WHERE fingerprint_sha256 = $1`, d.FingerprintSHA256)
		cleanupAuditByRequestID(pool, reqID)
	})

	// 원문 없는 저장 = 0018 이전 데이터의 재현.
	legacy := d
	legacy.Content = domain.CertificateContent{}
	if _, err := st.ImportCertificates(ctx, ImportInput{
		Certificates: []domain.Certificate{legacy}, RequestID: &reqID,
	}); err != nil {
		t.Fatalf("import legacy: %v", err)
	}
	before := readContentRow(t, pool, d.FingerprintSHA256)
	if !before.rawDERNull || before.spki.Valid || before.eku.Valid || before.isCA.Valid {
		t.Error("원문 없는 저장이 0018 컬럼을 채웠다 — 미상이어야 한다")
	}

	// 재업로드(=재스캔과 같은 경로)로 채워진다.
	if _, err := st.ImportCertificates(ctx, ImportInput{
		Certificates: []domain.Certificate{d}, RequestID: &reqID,
	}); err != nil {
		t.Fatalf("re-import: %v", err)
	}
	after := readContentRow(t, pool, d.FingerprintSHA256)
	if after.rawDERNull || !after.spki.Valid || !after.eku.Valid || !after.isCA.Valid {
		t.Error("재업로드가 0018 컬럼을 채우지 않았다")
	}
	if !strings.Contains(after.eku.String, "server_auth") {
		t.Errorf("ext_key_usage = %q", after.eku.String)
	}
}

// CA 인증서의 is_ca·path_len_constraint. -1(제약 없음)과 NULL(미상)이 구분돼야 한다.
func TestImportStoresCAPathLen(t *testing.T) {
	pool := testPool(t)
	st := New(pool)
	ctx := context.Background()

	ca, leaf := newTestChain(t)
	reqID := "test-req-content-ca"
	t.Cleanup(func() {
		_, _ = pool.Exec(`DELETE FROM certificates WHERE fingerprint_sha256 = ANY($1::text[])`,
			"{"+ca.FingerprintSHA256+","+leaf.FingerprintSHA256+"}")
		cleanupAuditByRequestID(pool, reqID)
	})
	if _, err := st.ImportCertificates(ctx, ImportInput{
		Certificates: []domain.Certificate{ca, leaf}, RequestID: &reqID,
	}); err != nil {
		t.Fatalf("import: %v", err)
	}

	r := readContentRow(t, pool, ca.FingerprintSHA256)
	if !r.isCA.Valid || !r.isCA.Bool {
		t.Errorf("CA is_ca = %v, want true", r.isCA)
	}
	if !r.pathLen.Valid || r.pathLen.Int64 != -1 {
		t.Errorf("CA path_len_constraint = %v, want -1 (제약 없음)", r.pathLen)
	}
	// certificate_type은 0018로 바뀌지 않는다.
	var ctype string
	if err := pool.QueryRow(`SELECT certificate_type FROM certificates WHERE fingerprint_sha256 = $1`,
		ca.FingerprintSHA256).Scan(&ctype); err != nil {
		t.Fatalf("read type: %v", err)
	}
	if ctype != string(domain.CertTypeCA) {
		t.Errorf("certificate_type = %q, want ca", ctype)
	}
}
