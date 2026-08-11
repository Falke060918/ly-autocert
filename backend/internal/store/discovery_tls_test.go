package store

// 0020 — 엔드포인트가 협상한 암호 스위트와 서버가 지원하는 TLS 버전 목록의 저장 검증.
//
// 두 값 모두 **인증서의 속성이 아니라 핸드셰이크의 결과**라 저장된 DER에서 소급
// 재계산할 수 없다(0018 raw_der로도 백필 불가). 그래서 검증의 요점은 두 가지다:
//   ① 관측이 값을 실어 오면 그대로 저장된다(그것이 유일한 채우는 경로다)
//   ② 값을 안 실어 온 관측이 **이미 알아낸 값을 지우지 않는다**
//      (지원 버전 탐지는 옵트인이라 켰다 껐다 하는 것이 정상 운용이다)
// 픽스처는 전부 테스트 실행 시점에 생성한다 (보안 하드 규칙 4).

import (
	"context"
	"crypto/x509"
	"database/sql"
	"strings"
	"testing"
)

func readLocationTLSDetails(t *testing.T, pool *sql.DB, ip string, port int) (cipher sql.NullString, versions []string, versionsNull bool) {
	t.Helper()
	var agg sql.NullString
	err := pool.QueryRow(`
		SELECT l.cipher_suite, array_to_string(l.supported_tls_versions, ',')
		  FROM certificate_locations l
		  JOIN targets tg ON tg.id = l.target_id
		 WHERE tg.ip_address = $1::inet AND tg.target_type = 'network' AND l.port = $2`,
		ip, port).Scan(&cipher, &agg)
	if err != nil {
		t.Fatalf("read location tls details: %v", err)
	}
	if !agg.Valid {
		return cipher, nil, true
	}
	if agg.String == "" {
		return cipher, []string{}, false
	}
	return cipher, strings.Split(agg.String, ","), false
}

// TestRecordEndpointResultStoresTLSDetails는 수집 → 저장 경로와, 이어지는 관측이
// 값을 안 실어 왔을 때의 보존 규약을 함께 고정한다.
func TestRecordEndpointResultStoresTLSDetails(t *testing.T) {
	pool := testPool(t)
	st := New(pool)
	ctx := context.Background()

	const ip = "203.0.113.80" // 문서화용 예약 대역 — 실제 주소 아님
	const probedPort, plainPort = 8471, 8472
	reqID := "test-req-tls-details"
	leaf := newTestX509Leaf(t, "tls-detail-fixture.example.test")

	t.Cleanup(func() {
		_, _ = pool.Exec(`DELETE FROM certificates WHERE common_name LIKE 'tls-detail-fixture%'`)
		_, _ = pool.Exec(`DELETE FROM targets WHERE ip_address = $1::inet AND target_type = 'network'`, ip)
		_, _ = pool.Exec(`DELETE FROM scan_jobs WHERE targets = ARRAY[$1::text]`, ip)
		cleanupAuditByRequestID(pool, reqID)
	})

	jobID, err := st.CreateScanJob(ctx, ScanInput{
		Targets: []string{ip}, Ports: []int{probedPort, plainPort}, TimeoutMs: 1000, Concurrency: 2,
		TotalEndpoints: 2, RequestID: &reqID,
	})
	if err != nil {
		t.Fatalf("create scan job: %v", err)
	}

	// 포트 1: 지원 버전 탐지를 켠 스캔(협상 1.2 + 아래 버전 2개 확인).
	if err := st.RecordEndpointResult(ctx, jobID, EndpointObservation{
		IP: ip, Port: probedPort, Reachable: true,
		TLSVersion:           "TLS 1.2",
		CipherSuite:          "TLS_ECDHE_ECDSA_WITH_AES_128_GCM_SHA256",
		SupportedTLSVersions: []string{"TLS 1.0", "TLS 1.1", "TLS 1.2"},
		Chain:                []*x509.Certificate{leaf},
	}); err != nil {
		t.Fatalf("record probed: %v", err)
	}
	// 포트 2: 탐지를 끈 기본 스캔 — 암호 스위트만 실린다.
	if err := st.RecordEndpointResult(ctx, jobID, EndpointObservation{
		IP: ip, Port: plainPort, Reachable: true,
		TLSVersion:  "TLS 1.3",
		CipherSuite: "TLS_AES_128_GCM_SHA256",
		Chain:       []*x509.Certificate{leaf},
	}); err != nil {
		t.Fatalf("record plain: %v", err)
	}

	cipher, versions, versionsNull := readLocationTLSDetails(t, pool, ip, probedPort)
	if !cipher.Valid || cipher.String != "TLS_ECDHE_ECDSA_WITH_AES_128_GCM_SHA256" {
		t.Errorf("probed: cipher_suite = %v, want TLS_ECDHE_ECDSA_WITH_AES_128_GCM_SHA256", cipher)
	}
	if versionsNull || strings.Join(versions, ",") != "TLS 1.0,TLS 1.1,TLS 1.2" {
		t.Errorf("probed: supported_tls_versions = %v (null=%v), want [TLS 1.0 TLS 1.1 TLS 1.2]", versions, versionsNull)
	}

	cipher, _, versionsNull = readLocationTLSDetails(t, pool, ip, plainPort)
	if !cipher.Valid || cipher.String != "TLS_AES_128_GCM_SHA256" {
		t.Errorf("plain: cipher_suite = %v, want TLS_AES_128_GCM_SHA256", cipher)
	}
	// 탐지를 안 한 관측은 NULL(미상)이다 — 빈 배열('지원 버전 없음')로 뭉개면 안 된다.
	if !versionsNull {
		t.Error("plain: supported_tls_versions가 NULL이 아니다 — 탐지하지 않음과 지원 없음이 섞였다")
	}

	// 같은 엔드포인트를 탐지 없이 재스캔해도 이미 알아낸 목록은 남아야 한다.
	// 옵트인 설정을 껐다고 수집한 사실이 사라지면 화면이 매번 뒤집힌다.
	if err := st.RecordEndpointResult(ctx, jobID, EndpointObservation{
		IP: ip, Port: probedPort, Reachable: true,
		TLSVersion:  "TLS 1.2",
		CipherSuite: "TLS_ECDHE_ECDSA_WITH_AES_256_GCM_SHA384",
		Chain:       []*x509.Certificate{leaf},
	}); err != nil {
		t.Fatalf("record probed again: %v", err)
	}
	cipher, versions, versionsNull = readLocationTLSDetails(t, pool, ip, probedPort)
	if !cipher.Valid || cipher.String != "TLS_ECDHE_ECDSA_WITH_AES_256_GCM_SHA384" {
		t.Errorf("rescan: cipher_suite = %v, want 최신 관측값으로 갱신", cipher)
	}
	if versionsNull || strings.Join(versions, ",") != "TLS 1.0,TLS 1.1,TLS 1.2" {
		t.Errorf("rescan: supported_tls_versions = %v (null=%v), want 기존 목록 유지", versions, versionsNull)
	}
}
