package store

// 0018 — 엔드포인트가 제시한 체인의 저장 검증.
//
// 요점은 "인벤토리로 보완하지 않는다"이다: 중간 CA가 이미 인벤토리에 있어도
// 그것을 제시하지 않은 엔드포인트는 chain_complete=false여야 한다.
// 픽스처는 전부 테스트 실행 시점에 생성한다 (보안 하드 규칙 4).

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"crypto/x509/pkix"
	"database/sql"
	"math/big"
	"testing"
	"time"
)

// newTestX509Tier는 루트 → 중간 → 리프 3단 체인을 원시 x509 타입으로 만든다.
func newTestX509Tier(t *testing.T, suffix string) (root, inter, leaf *x509.Certificate) {
	t.Helper()
	newKey := func() *ecdsa.PrivateKey {
		k, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
		if err != nil {
			t.Fatalf("generate key: %v", err)
		}
		return k
	}
	sign := func(tmpl, parent *x509.Certificate, parentKey *ecdsa.PrivateKey) (*x509.Certificate, *ecdsa.PrivateKey) {
		key := newKey()
		signer, signerKey := parent, parentKey
		if signer == nil {
			signer, signerKey = tmpl, key
		}
		der, err := x509.CreateCertificate(rand.Reader, tmpl, signer, &key.PublicKey, signerKey)
		if err != nil {
			t.Fatalf("create certificate: %v", err)
		}
		cert, err := x509.ParseCertificate(der)
		if err != nil {
			t.Fatalf("parse certificate: %v", err)
		}
		return cert, key
	}
	ca := func(cn string, ski []byte) *x509.Certificate {
		return &x509.Certificate{
			SerialNumber:          big.NewInt(time.Now().UnixNano()),
			Subject:               pkix.Name{CommonName: cn},
			NotBefore:             time.Now().Add(-time.Hour),
			NotAfter:              time.Now().Add(365 * 24 * time.Hour),
			KeyUsage:              x509.KeyUsageCertSign,
			IsCA:                  true,
			BasicConstraintsValid: true,
			SubjectKeyId:          ski,
		}
	}
	root, rootKey := sign(ca("chain-fixture-root-"+suffix, []byte{0x11, byte(len(suffix))}), nil, nil)
	inter, interKey := sign(ca("chain-fixture-inter-"+suffix, []byte{0x22, byte(len(suffix))}), root, rootKey)
	leafTmpl := &x509.Certificate{
		SerialNumber: big.NewInt(time.Now().UnixNano() + 1),
		Subject:      pkix.Name{CommonName: "chain-fixture-leaf-" + suffix + ".example.test"},
		NotBefore:    time.Now().Add(-time.Hour),
		NotAfter:     time.Now().Add(90 * 24 * time.Hour),
		KeyUsage:     x509.KeyUsageDigitalSignature,
		ExtKeyUsage:  []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
		DNSNames:     []string{"chain-fixture-leaf-" + suffix + ".example.test"},
	}
	leaf, _ = sign(leafTmpl, inter, interKey)
	return root, inter, leaf
}

func readLocationChain(t *testing.T, pool *sql.DB, ip string, port int) (chainLen sql.NullInt64, complete sql.NullBool) {
	t.Helper()
	err := pool.QueryRow(`
		SELECT l.presented_chain_len, l.chain_complete
		  FROM certificate_locations l
		  JOIN targets tg ON tg.id = l.target_id
		 WHERE tg.ip_address = $1::inet AND tg.target_type = 'network' AND l.port = $2`,
		ip, port).Scan(&chainLen, &complete)
	if err != nil {
		t.Fatalf("read location chain: %v", err)
	}
	return chainLen, complete
}

// 풀체인을 제시한 엔드포인트와 리프만 제시한 엔드포인트가 갈려야 한다.
// 후자의 중간 CA는 전자 덕분에 이미 인벤토리에 있으므로, 인벤토리로 보완하면
// 둘 다 완전해 보인다 — 그것이 0018 이전의 버그다.
func TestRecordEndpointResultStoresPresentedChain(t *testing.T) {
	pool := testPool(t)
	st := New(pool)
	ctx := context.Background()

	const ip = "203.0.113.70" // 문서화용 예약 대역 — 실제 주소 아님
	const fullPort, leafPort = 8461, 8462
	reqID := "test-req-presented-chain"
	_, inter, leaf := newTestX509Tier(t, "a")

	t.Cleanup(func() {
		_, _ = pool.Exec(`DELETE FROM certificates WHERE common_name LIKE 'chain-fixture-%'`)
		_, _ = pool.Exec(`DELETE FROM targets WHERE ip_address = $1::inet AND target_type = 'network'`, ip)
		_, _ = pool.Exec(`DELETE FROM scan_jobs WHERE targets = ARRAY[$1::text]`, ip)
		cleanupAuditByRequestID(pool, reqID)
	})

	jobID, err := st.CreateScanJob(ctx, ScanInput{
		Targets: []string{ip}, Ports: []int{fullPort, leafPort}, TimeoutMs: 1000, Concurrency: 2,
		TotalEndpoints: 2, RequestID: &reqID,
	})
	if err != nil {
		t.Fatalf("create scan job: %v", err)
	}

	// 포트 1: 리프 + 중간 CA (루트 생략 — 표준 관행).
	if err := st.RecordEndpointResult(ctx, jobID, EndpointObservation{
		IP: ip, Port: fullPort, Reachable: true, TLSVersion: "TLS 1.3",
		Chain: []*x509.Certificate{leaf, inter},
	}); err != nil {
		t.Fatalf("record full chain: %v", err)
	}
	// 포트 2: 같은 리프를 혼자 제시 — 중간 CA 누락.
	if err := st.RecordEndpointResult(ctx, jobID, EndpointObservation{
		IP: ip, Port: leafPort, Reachable: true, TLSVersion: "TLS 1.3",
		Chain: []*x509.Certificate{leaf},
	}); err != nil {
		t.Fatalf("record leaf only: %v", err)
	}

	n, ok := readLocationChain(t, pool, ip, fullPort)
	if !n.Valid || n.Int64 != 2 {
		t.Errorf("full: presented_chain_len = %v, want 2", n)
	}
	if !ok.Valid || !ok.Bool {
		t.Errorf("full: chain_complete = %v, want true (루트 생략은 정상)", ok)
	}

	n, ok = readLocationChain(t, pool, ip, leafPort)
	if !n.Valid || n.Int64 != 1 {
		t.Errorf("leaf-only: presented_chain_len = %v, want 1", n)
	}
	if !ok.Valid || ok.Bool {
		t.Errorf("leaf-only: chain_complete = %v, want false — 인벤토리의 중간 CA로 보완됐다", ok)
	}
}

// 재스캔으로 판정이 갱신된다: 풀체인을 배포하면 false → true로 바뀌어야 한다.
func TestRecordEndpointResultUpdatesPresentedChain(t *testing.T) {
	pool := testPool(t)
	st := New(pool)
	ctx := context.Background()

	const ip = "203.0.113.71" // 문서화용 예약 대역 — 실제 주소 아님
	const port = 8463
	reqID := "test-req-presented-chain-update"
	_, inter, leaf := newTestX509Tier(t, "b")

	t.Cleanup(func() {
		_, _ = pool.Exec(`DELETE FROM certificates WHERE common_name LIKE 'chain-fixture-%'`)
		_, _ = pool.Exec(`DELETE FROM targets WHERE ip_address = $1::inet AND target_type = 'network'`, ip)
		_, _ = pool.Exec(`DELETE FROM scan_jobs WHERE targets = ARRAY[$1::text]`, ip)
		cleanupAuditByRequestID(pool, reqID)
	})

	jobID, err := st.CreateScanJob(ctx, ScanInput{
		Targets: []string{ip}, Ports: []int{port}, TimeoutMs: 1000, Concurrency: 1,
		TotalEndpoints: 1, RequestID: &reqID,
	})
	if err != nil {
		t.Fatalf("create scan job: %v", err)
	}

	if err := st.RecordEndpointResult(ctx, jobID, EndpointObservation{
		IP: ip, Port: port, Reachable: true, Chain: []*x509.Certificate{leaf},
	}); err != nil {
		t.Fatalf("record first: %v", err)
	}
	if _, ok := readLocationChain(t, pool, ip, port); !ok.Valid || ok.Bool {
		t.Fatalf("최초 관측 chain_complete = %v, want false", ok)
	}

	if err := st.RecordEndpointResult(ctx, jobID, EndpointObservation{
		IP: ip, Port: port, Reachable: true, Chain: []*x509.Certificate{leaf, inter},
	}); err != nil {
		t.Fatalf("record second: %v", err)
	}
	n, ok := readLocationChain(t, pool, ip, port)
	if !ok.Valid || !ok.Bool {
		t.Errorf("재관측 chain_complete = %v, want true (서버 설정을 고치면 판정도 바뀌어야 한다)", ok)
	}
	if !n.Valid || n.Int64 != 2 {
		t.Errorf("재관측 presented_chain_len = %v, want 2", n)
	}
}

// 스캔 경로도 인증서 원문을 저장한다 — 업로드만 채우면 소급 재계산 대상이
// 인벤토리의 일부로 갈린다.
func TestRecordEndpointResultStoresRawDER(t *testing.T) {
	pool := testPool(t)
	st := New(pool)
	ctx := context.Background()

	const ip = "203.0.113.72" // 문서화용 예약 대역 — 실제 주소 아님
	const port = 8464
	reqID := "test-req-presented-chain-raw"
	_, inter, leaf := newTestX509Tier(t, "c")

	t.Cleanup(func() {
		_, _ = pool.Exec(`DELETE FROM certificates WHERE common_name LIKE 'chain-fixture-%'`)
		_, _ = pool.Exec(`DELETE FROM targets WHERE ip_address = $1::inet AND target_type = 'network'`, ip)
		_, _ = pool.Exec(`DELETE FROM scan_jobs WHERE targets = ARRAY[$1::text]`, ip)
		cleanupAuditByRequestID(pool, reqID)
	})

	jobID, err := st.CreateScanJob(ctx, ScanInput{
		Targets: []string{ip}, Ports: []int{port}, TimeoutMs: 1000, Concurrency: 1,
		TotalEndpoints: 1, RequestID: &reqID,
	})
	if err != nil {
		t.Fatalf("create scan job: %v", err)
	}
	if err := st.RecordEndpointResult(ctx, jobID, EndpointObservation{
		IP: ip, Port: port, Reachable: true, Chain: []*x509.Certificate{leaf, inter},
	}); err != nil {
		t.Fatalf("record: %v", err)
	}

	// 체인의 두 인증서 모두 원문·SPKI·is_ca가 채워져야 한다.
	var n int
	if err := pool.QueryRow(`
		SELECT count(*) FROM certificates
		 WHERE common_name LIKE 'chain-fixture-%'
		   AND raw_der IS NOT NULL AND spki_sha256 IS NOT NULL AND is_ca IS NOT NULL`).
		Scan(&n); err != nil {
		t.Fatalf("count: %v", err)
	}
	if n != 2 {
		t.Errorf("원문이 저장된 인증서 = %d건, want 2 (리프 + 중간 CA)", n)
	}
}
