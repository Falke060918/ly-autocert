package store

// 이슈 #5 reviewer 지적(high) 재현: 같은 IP의 여러 포트가 동시 트랜잭션으로
// 결과를 기록하면 SELECT-후-INSERT 경합으로 targets가 중복 생성되고,
// 재스캔마다 certificate_locations가 늘어난다. 수정 후에는
// targets는 IP당 1행, locations는 (인증서, 대상, 포트)당 1행이어야 한다.
// 픽스처는 전부 테스트 실행 시점에 생성한다 (보안 하드 규칙 4).

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"crypto/x509/pkix"
	"fmt"
	"math/big"
	"sync"
	"testing"
	"time"
)

// newTestX509Leaf는 자기서명 리프(*x509.Certificate) 픽스처를 만든다.
// EndpointObservation.Chain에 직접 넣기 위한 원시 x509 타입이다.
func newTestX509Leaf(t *testing.T, cn string) *x509.Certificate {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatalf("generate key: %v", err)
	}
	tmpl := &x509.Certificate{
		SerialNumber: big.NewInt(time.Now().UnixNano()),
		Subject:      pkix.Name{CommonName: cn},
		NotBefore:    time.Now().Add(-time.Hour),
		NotAfter:     time.Now().Add(90 * 24 * time.Hour),
		KeyUsage:     x509.KeyUsageDigitalSignature,
		ExtKeyUsage:  []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
		DNSNames:     []string{cn},
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &key.PublicKey, key)
	if err != nil {
		t.Fatalf("create certificate: %v", err)
	}
	cert, err := x509.ParseCertificate(der)
	if err != nil {
		t.Fatalf("parse certificate: %v", err)
	}
	return cert
}

// TestCreateScanJobRecordsTotalEndpoints는 생성 응답의 total_endpoints와 저장된
// Job 행이 일치하는지 본다 — 예전엔 /start(서버는 running 전이) 전까지 0이었다.
func TestCreateScanJobRecordsTotalEndpoints(t *testing.T) {
	pool := testPool(t)
	st := New(pool)
	ctx := context.Background()

	const ip = "203.0.113.60" // 문서화용 예약 대역 — 실제 주소 아님
	reqID := "test-req-scan-total"
	t.Cleanup(func() {
		_, _ = pool.Exec(`DELETE FROM scan_jobs WHERE targets = ARRAY[$1::text]`, ip)
		cleanupAuditByRequestID(pool, reqID)
	})

	jobID, err := st.CreateScanJob(ctx, ScanInput{
		Targets: []string{ip}, Ports: []int{8443, 8444}, TimeoutMs: 1000, Concurrency: 5,
		TotalEndpoints: 2, RequestID: &reqID,
	})
	if err != nil {
		t.Fatalf("create scan job: %v", err)
	}
	job, err := st.GetScanJob(ctx, jobID)
	if err != nil {
		t.Fatalf("get scan job: %v", err)
	}
	if job.TotalEndpoints != 2 {
		t.Errorf("total_endpoints = %d, want 2 (생성 응답과 동일)", job.TotalEndpoints)
	}
}

// TestReapOrphanedServerScanJobs는 서버 재시작 회수를 본다: 서버 직접 실행
// Job의 pending/running은 failed로 회수되고, 센서 위임 Job은 손대지 않는다
// (센서 Job은 리스 만료 reaper 소관).
func TestReapOrphanedServerScanJobs(t *testing.T) {
	pool := testPool(t)
	st := New(pool)
	ctx := context.Background()

	const ip = "203.0.113.61" // 문서화용 예약 대역 — 실제 주소 아님
	reqID := "test-req-scan-orphan"
	sensorID := newTestSensor(t, pool, "sensor-fixture-orphan")
	t.Cleanup(func() {
		_, _ = pool.Exec(`DELETE FROM scan_jobs WHERE targets = ARRAY[$1::text]`, ip)
		cleanupAuditByRequestID(pool, reqID)
	})

	// 포트를 호출마다 바꾼다 — 동일 정규화 요청(실행기·대상·포트)의 활성 중복은
	// 0017 dedup 가드가 막으므로 픽스처는 서로 다른 요청이어야 한다.
	port := 8440
	newJob := func(sensor *string) string {
		t.Helper()
		port++
		id, err := st.CreateScanJob(ctx, ScanInput{
			Targets: []string{ip}, Ports: []int{port}, TimeoutMs: 1000, Concurrency: 1,
			TotalEndpoints: 1, SensorID: sensor, RequestID: &reqID,
		})
		if err != nil {
			t.Fatalf("create scan job: %v", err)
		}
		t.Cleanup(func() { cleanupAuditByTargetID(pool, id) })
		return id
	}

	serverRunning := newJob(nil)
	if err := st.MarkScanRunning(ctx, serverRunning, 1); err != nil {
		t.Fatalf("mark running: %v", err)
	}
	serverPending := newJob(nil)
	serverDone := newJob(nil)
	if err := st.MarkScanFinished(ctx, serverDone, ScanStatusCompleted, nil); err != nil {
		t.Fatalf("mark finished: %v", err)
	}
	// 리스는 가장 오래된 pending을 집어간다 — leased 대상 Job을 먼저 만든다.
	sensorLeased := newJob(&sensorID)
	lease, err := st.LeaseNextSensorJob(ctx, sensorID)
	if err != nil {
		t.Fatalf("lease: %v", err)
	}
	if lease.JobID != sensorLeased {
		t.Fatalf("leased job = %s, want %s", lease.JobID, sensorLeased)
	}
	sensorPending := newJob(&sensorID)

	n, err := st.ReapOrphanedServerScanJobs(ctx)
	if err != nil {
		t.Fatalf("reap orphaned server scan jobs: %v", err)
	}
	if n < 2 {
		t.Errorf("reaped = %d, want at least 2 (running + pending 서버 Job)", n)
	}

	status := func(jobID string) (string, *string) {
		t.Helper()
		j, err := st.GetScanJob(ctx, jobID)
		if err != nil {
			t.Fatalf("get scan job: %v", err)
		}
		return j.Status, j.Error
	}

	for _, id := range []string{serverRunning, serverPending} {
		gotStatus, errMsg := status(id)
		if gotStatus != ScanStatusFailed {
			t.Errorf("orphaned server job status = %q, want failed", gotStatus)
		}
		if errMsg == nil || *errMsg != orphanedServerJobError {
			t.Errorf("orphaned server job error = %v, want %q", errMsg, orphanedServerJobError)
		}
	}
	if got, _ := status(serverDone); got != ScanStatusCompleted {
		t.Errorf("completed job status = %q, want completed (재회수 금지)", got)
	}
	// 센서 위임 Job은 그대로다 — 리스 만료 reaper가 따로 다룬다.
	if got, _ := status(sensorPending); got != ScanStatusPending {
		t.Errorf("sensor pending job status = %q, want pending", got)
	}
	if got, _ := status(sensorLeased); got != "leased" {
		t.Errorf("sensor leased job status = %q, want leased", got)
	}

	// 회수 건마다 종료 감사 이벤트가 남는다 (하드 규칙 5).
	var audits int
	if err := pool.QueryRow(
		`SELECT count(*) FROM audit_events WHERE target_id = $1 AND event_type = $2 AND result = 'failure'`,
		serverRunning, EventDiscoveryScan).Scan(&audits); err != nil {
		t.Fatalf("count audit events: %v", err)
	}
	if audits != 1 {
		t.Errorf("audit events for reaped job = %d, want 1", audits)
	}

	// 회수 후 재실행은 0건이다(멱등).
	if n, err := st.ReapOrphanedServerScanJobs(ctx); err != nil || n != 0 {
		t.Errorf("second reap = (%d, %v), want (0, nil)", n, err)
	}
}

// TestRecordEndpointResultConcurrentSameIP는 같은 IP의 여러 포트가 서로 다른
// 인증서를 동시에 기록해도 대상·위치가 중복 생성되지 않는지 검증한다.
func TestRecordEndpointResultConcurrentSameIP(t *testing.T) {
	pool := testPool(t)
	st := New(pool)
	ctx := context.Background()

	const ip = "203.0.113.55" // 문서화용 예약 대역 — 실제 주소 아님
	const hostname = "scan-fixture.example.test"
	ports := []int{8441, 8442, 8443, 8444, 8445}
	reqID := "test-req-scan-concurrent"

	obsList := make([]EndpointObservation, len(ports))
	for i, p := range ports {
		obsList[i] = EndpointObservation{
			IP:         ip,
			Port:       p,
			Reachable:  true,
			TLSVersion: "TLS 1.3",
			Hostname:   hostname,
			Chain:      []*x509.Certificate{newTestX509Leaf(t, fmt.Sprintf("scan-fixture-%d.example.test", p))},
		}
	}

	t.Cleanup(func() {
		_, _ = pool.Exec(`DELETE FROM certificates WHERE common_name LIKE 'scan-fixture-%.example.test'`)
		_, _ = pool.Exec(`DELETE FROM targets WHERE ip_address = $1::inet AND target_type = 'network'`, ip)
		_, _ = pool.Exec(`DELETE FROM scan_jobs WHERE targets = ARRAY[$1::text]`, ip)
		cleanupAuditByRequestID(pool, reqID)
	})

	jobID, err := st.CreateScanJob(ctx, ScanInput{
		Targets: []string{ip}, Ports: ports, TimeoutMs: 1000, Concurrency: 5,
		RequestID: &reqID,
	})
	if err != nil {
		t.Fatalf("create scan job: %v", err)
	}

	// 2라운드: 최초 스캔 + 재스캔. 각 라운드는 전 포트를 동시에 기록한다.
	record := func(round int) {
		t.Helper()
		var wg sync.WaitGroup
		errCh := make(chan error, len(obsList))
		for _, obs := range obsList {
			wg.Add(1)
			go func(obs EndpointObservation) {
				defer wg.Done()
				errCh <- st.RecordEndpointResult(ctx, jobID, obs)
			}(obs)
		}
		wg.Wait()
		close(errCh)
		for err := range errCh {
			if err != nil {
				t.Errorf("round %d: record endpoint result: %v", round, err)
			}
		}
	}

	counts := func() (targets, locations, certs int) {
		t.Helper()
		if err := pool.QueryRow(
			`SELECT count(*) FROM targets WHERE ip_address = $1::inet AND target_type = 'network'`,
			ip).Scan(&targets); err != nil {
			t.Fatalf("count targets: %v", err)
		}
		if err := pool.QueryRow(`
			SELECT count(*) FROM certificate_locations cl
			  JOIN targets tg ON tg.id = cl.target_id
			 WHERE tg.ip_address = $1::inet AND tg.target_type = 'network'
			   AND cl.location_type = 'network_endpoint'`,
			ip).Scan(&locations); err != nil {
			t.Fatalf("count locations: %v", err)
		}
		if err := pool.QueryRow(
			`SELECT count(*) FROM certificates WHERE common_name LIKE 'scan-fixture-%.example.test'`,
		).Scan(&certs); err != nil {
			t.Fatalf("count certificates: %v", err)
		}
		return targets, locations, certs
	}

	record(1)
	targets1, locations1, certs1 := counts()
	if targets1 != 1 {
		t.Errorf("round 1: targets = %d, want 1 (동시 생성 중복)", targets1)
	}
	if locations1 != len(ports) {
		t.Errorf("round 1: locations = %d, want %d", locations1, len(ports))
	}
	if certs1 != len(ports) {
		t.Errorf("round 1: certificates = %d, want %d", certs1, len(ports))
	}

	record(2) // 재스캔 — 위치가 늘어나면 안 된다.
	targets2, locations2, certs2 := counts()
	if targets2 != 1 {
		t.Errorf("round 2: targets = %d, want 1", targets2)
	}
	if locations2 != len(ports) {
		t.Errorf("round 2: locations = %d, want %d (재스캔 증식)", locations2, len(ports))
	}
	if certs2 != len(ports) {
		t.Errorf("round 2: certificates = %d, want %d", certs2, len(ports))
	}

	// TLS 프로토콜 버전이 위치에 저장된다 (스펙 7.1 수집 항목, reviewer medium).
	var tlsVersion string
	if err := pool.QueryRow(`
		SELECT cl.tls_version FROM certificate_locations cl
		  JOIN targets tg ON tg.id = cl.target_id
		 WHERE tg.ip_address = $1::inet AND tg.target_type = 'network'
		   AND cl.location_type = 'network_endpoint' AND cl.port = $2`,
		ip, ports[0]).Scan(&tlsVersion); err != nil {
		t.Fatalf("read tls_version: %v", err)
	}
	if tlsVersion != "TLS 1.3" {
		t.Errorf("tls_version = %q, want %q", tlsVersion, "TLS 1.3")
	}

	// 호스트 이름이 대상에 저장된다 (스펙 7.1 수집 항목, reviewer medium).
	var gotHostname string
	if err := pool.QueryRow(
		`SELECT coalesce(hostname, '') FROM targets WHERE ip_address = $1::inet AND target_type = 'network'`,
		ip).Scan(&gotHostname); err != nil {
		t.Fatalf("read hostname: %v", err)
	}
	if gotHostname != hostname {
		t.Errorf("hostname = %q, want %q", gotHostname, hostname)
	}
}
