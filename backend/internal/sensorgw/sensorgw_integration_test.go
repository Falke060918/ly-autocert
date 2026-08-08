package sensorgw

// 센서 게이트웨이 mTLS 왕복 통합 테스트 (TEST_DATABASE_URL 필요 — 미설정 시 스킵).
// 실제 리스너(NewHTTPServer + ServeTLS)로 enroll → lease → start → heartbeat →
// observations(멱등 재전송) → complete 전 구간을 검증한다.
// 픽스처(키·인증서·CSR)는 전부 런타임 생성 — 실제 키·인증서를 커밋하지 않는다
// (보안 하드 규칙 4).

import (
	"bytes"
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/base64"
	"encoding/json"
	"encoding/pem"
	"fmt"
	"io"
	"math/big"
	"net"
	"net/http"
	"os"
	"testing"
	"time"

	"github.com/Falke060918/INHERIT_AutoCert/backend/internal/audit"
	"github.com/Falke060918/INHERIT_AutoCert/backend/internal/db"
	"github.com/Falke060918/INHERIT_AutoCert/backend/internal/sensor"
	"github.com/Falke060918/INHERIT_AutoCert/backend/internal/sensorwire"
	"github.com/Falke060918/INHERIT_AutoCert/backend/internal/store"
)

// gwEnv는 통합 테스트용 게이트웨이 실행 환경이다.
type gwEnv struct {
	st      *store.Store
	gw      *Gateway
	baseURL string
	rootCAs *x509.CertPool
	sealKey []byte
}

func newGatewayEnv(t *testing.T) *gwEnv {
	t.Helper()
	dsn := os.Getenv("TEST_DATABASE_URL")
	if dsn == "" {
		t.Skip("TEST_DATABASE_URL not set; skipping sensor gateway integration test")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	conn, err := db.Connect(ctx, dsn)
	if err != nil {
		t.Fatalf("connect: %v", err)
	}
	t.Cleanup(func() { conn.Close() })
	if err := db.Migrate(conn); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	// 테스트 격리: 이전 실행 잔류 제거 (테스트 전용 DB에서만 허용되는 정리다).
	// audit_events는 append-only 트리거(0007)가 DELETE를 차단하므로 잠시 내리고 지운다.
	for _, stmt := range []string{
		`DELETE FROM certificate_locations`,
		`DELETE FROM certificates`,
		`DELETE FROM targets`,
		`DELETE FROM scan_job_endpoints`,
		`DELETE FROM scan_jobs`,
		`DELETE FROM sensors`,
		`DELETE FROM sensor_ca`,
		`ALTER TABLE audit_events DISABLE TRIGGER USER`,
		`DELETE FROM audit_events`,
		`ALTER TABLE audit_events ENABLE TRIGGER USER`,
	} {
		if _, err := conn.Exec(stmt); err != nil {
			t.Fatalf("cleanup %q: %v", stmt, err)
		}
	}

	st := store.New(conn)
	rec := audit.NewRecorder(conn)
	sealKey := make([]byte, 32)
	if _, err := rand.Read(sealKey); err != nil {
		t.Fatalf("seal key: %v", err)
	}
	gw, err := New(ctx, st, rec, sealKey)
	if err != nil {
		t.Fatalf("new gateway: %v", err)
	}

	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	srv, err := gw.NewHTTPServer("", nil, []net.IP{net.ParseIP("127.0.0.1")})
	if err != nil {
		t.Fatalf("new http server: %v", err)
	}
	go srv.ServeTLS(ln, "", "") //nolint:errcheck
	t.Cleanup(func() { srv.Close() })

	roots := x509.NewCertPool()
	if !roots.AppendCertsFromPEM(gw.CACertPEM()) {
		t.Fatal("append ca cert to pool failed")
	}
	return &gwEnv{
		st:      st,
		gw:      gw,
		baseURL: "https://" + ln.Addr().String(),
		rootCAs: roots,
		sealKey: sealKey,
	}
}

// newSensorKeyCSR은 센서 측 키쌍과 CSR PEM을 런타임 생성한다.
func newSensorKeyCSR(t *testing.T, cn string) (*ecdsa.PrivateKey, string) {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatalf("generate key: %v", err)
	}
	der, err := x509.CreateCertificateRequest(rand.Reader,
		&x509.CertificateRequest{Subject: pkix.Name{CommonName: cn}}, key)
	if err != nil {
		t.Fatalf("create csr: %v", err)
	}
	return key, string(pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE REQUEST", Bytes: der}))
}

// plainClient는 클라이언트 인증서 없는 TLS 클라이언트다 (enroll·인증 거부 검증용).
func (e *gwEnv) plainClient() *http.Client {
	return &http.Client{
		Timeout: 10 * time.Second,
		Transport: &http.Transport{
			TLSClientConfig: &tls.Config{MinVersion: tls.VersionTLS12, RootCAs: e.rootCAs},
		},
	}
}

// mtlsClient는 발급된 클라이언트 인증서로 mTLS 클라이언트를 만든다.
func (e *gwEnv) mtlsClient(t *testing.T, key *ecdsa.PrivateKey, certPEM string) *http.Client {
	t.Helper()
	block, _ := pem.Decode([]byte(certPEM))
	if block == nil || block.Type != "CERTIFICATE" {
		t.Fatal("client cert is not a certificate PEM block")
	}
	return &http.Client{
		Timeout: 10 * time.Second,
		Transport: &http.Transport{
			TLSClientConfig: &tls.Config{
				MinVersion:   tls.VersionTLS12,
				RootCAs:      e.rootCAs,
				Certificates: []tls.Certificate{{Certificate: [][]byte{block.Bytes}, PrivateKey: key}},
			},
		},
	}
}

// post는 JSON POST 1건을 보내고 상태 코드와 (있으면) 본문 디코드 결과를 돌려준다.
func (e *gwEnv) post(t *testing.T, hc *http.Client, path string, in, out any) int {
	t.Helper()
	var body io.Reader
	if in != nil {
		b, err := json.Marshal(in)
		if err != nil {
			t.Fatalf("marshal request: %v", err)
		}
		body = bytes.NewReader(b)
	}
	req, err := http.NewRequest(http.MethodPost, e.baseURL+path, body)
	if err != nil {
		t.Fatalf("new request: %v", err)
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := hc.Do(req)
	if err != nil {
		t.Fatalf("POST %s: %v", path, err)
	}
	defer resp.Body.Close()
	if out != nil && resp.StatusCode >= 200 && resp.StatusCode < 300 && resp.StatusCode != http.StatusNoContent {
		if err := json.NewDecoder(resp.Body).Decode(out); err != nil {
			t.Fatalf("decode response of %s: %v", path, err)
		}
	}
	return resp.StatusCode
}

// enroll은 센서 생성(토큰 발급)부터 등록까지 마치고 mTLS 클라이언트를 돌려준다.
func (e *gwEnv) enroll(t *testing.T, name string) (sensorID string, key *ecdsa.PrivateKey, certPEM string) {
	t.Helper()
	_, token, err := e.st.CreateSensor(context.Background(), store.CreateSensorInput{Name: name})
	if err != nil {
		t.Fatalf("create sensor: %v", err)
	}
	key, csrPEM := newSensorKeyCSR(t, name)
	var resp sensorwire.EnrollResponse
	status := e.post(t, e.plainClient(), "/sensor/v1/enroll",
		sensorwire.EnrollRequest{Token: token, CSRPEM: csrPEM}, &resp)
	if status != http.StatusOK {
		t.Fatalf("enroll = %d, want 200", status)
	}
	if resp.SensorID == "" || resp.ClientCertPEM == "" || resp.CACertPEM == "" {
		t.Fatal("enroll response missing fields")
	}
	return resp.SensorID, key, resp.ClientCertPEM
}

// testLeafCertDER는 관측 픽스처용 자체 서명 리프 인증서 DER를 런타임 생성한다.
func testLeafCertDER(t *testing.T, cn string) []byte {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatalf("generate fixture key: %v", err)
	}
	serial, err := rand.Int(rand.Reader, new(big.Int).Lsh(big.NewInt(1), 128))
	if err != nil {
		t.Fatalf("serial: %v", err)
	}
	tmpl := &x509.Certificate{
		SerialNumber: serial,
		Subject:      pkix.Name{CommonName: cn},
		NotBefore:    time.Now().Add(-time.Hour),
		NotAfter:     time.Now().Add(24 * time.Hour),
		DNSNames:     []string{cn},
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &key.PublicKey, key)
	if err != nil {
		t.Fatalf("create fixture certificate: %v", err)
	}
	return der
}

func TestSensorGatewayEndToEnd(t *testing.T) {
	env := newGatewayEnv(t)
	ctx := context.Background()

	sensorID, key, certPEM := env.enroll(t, "gw-e2e")
	mtls := env.mtlsClient(t, key, certPEM)

	// 등록 직후 store에도 active로 보여야 한다.
	sn, err := env.st.GetSensor(ctx, sensorID)
	if err != nil || string(sn.Status) != "active" {
		t.Fatalf("sensor after enroll = %+v, err %v; want active", sn, err)
	}

	// 배정된 작업이 없으면 204.
	if status := env.post(t, mtls, "/sensor/v1/jobs/lease", nil, nil); status != http.StatusNoContent {
		t.Fatalf("empty lease = %d, want 204", status)
	}

	// 센서 위임 스캔 Job 생성 → 리스.
	jobID, err := env.st.CreateScanJob(ctx, store.ScanInput{
		Targets: []string{"192.0.2.10"}, Ports: []int{443},
		TimeoutMs: 1000, Concurrency: 4, SensorID: &sensorID,
	})
	if err != nil {
		t.Fatalf("create scan job: %v", err)
	}
	var lease sensorwire.LeaseResponse
	if status := env.post(t, mtls, "/sensor/v1/jobs/lease", nil, &lease); status != http.StatusOK {
		t.Fatalf("lease = %d, want 200", status)
	}
	if lease.JobID != jobID || lease.JobType != "scan" {
		t.Fatalf("lease = %+v, want job %s type scan", lease, jobID)
	}
	if len(lease.Targets) != 1 || lease.Targets[0] != "192.0.2.10" || len(lease.Ports) != 1 || lease.Ports[0] != 443 {
		t.Fatalf("lease payload = %+v, want targets/ports round-tripped", lease)
	}

	// leased → running.
	if status := env.post(t, mtls, "/sensor/v1/jobs/"+jobID+"/start",
		sensorwire.JobStartRequest{TotalEndpoints: 2}, nil); status != http.StatusNoContent {
		t.Fatalf("start = %d, want 204", status)
	}

	// running 하트비트.
	if status := env.post(t, mtls, "/sensor/v1/jobs/"+jobID+"/heartbeat",
		sensorwire.HeartbeatRequest{ScannedEndpoints: 1}, nil); status != http.StatusNoContent {
		t.Fatalf("heartbeat = %d, want 204", status)
	}

	// 관측 배치: 도달 1건(체인 1개) + 미도달 1건.
	leafDER := testLeafCertDER(t, "svc.example.internal")
	batch := sensorwire.ObservationBatch{Observations: []sensorwire.Observation{
		{
			IP: "192.0.2.10", Port: 443, Reachable: true, TLSVersion: "TLS 1.3",
			Hostname:   "svc.example.internal",
			ChainDER:   []string{base64.StdEncoding.EncodeToString(leafDER)},
			ObservedAt: time.Now(),
		},
		{IP: "192.0.2.11", Port: 443, Reachable: false, ObservedAt: time.Now()},
	}}
	if status := env.post(t, mtls, "/sensor/v1/jobs/"+jobID+"/observations", batch, nil); status != http.StatusNoContent {
		t.Fatalf("observations = %d, want 204", status)
	}
	// 같은 배치 재전송 — 멱등이어야 한다 (204 + 카운터 불변).
	if status := env.post(t, mtls, "/sensor/v1/jobs/"+jobID+"/observations", batch, nil); status != http.StatusNoContent {
		t.Fatalf("observations resend = %d, want 204", status)
	}
	job, err := env.st.GetScanJob(ctx, jobID)
	if err != nil {
		t.Fatalf("get scan job: %v", err)
	}
	if job.ScannedEndpoints != 2 || job.ReachableEndpoints != 1 || job.CertificatesFound != 1 || job.CertificatesNew != 1 {
		t.Fatalf("counters after idempotent resend = scanned %d reachable %d found %d new %d; want 2/1/1/1",
			job.ScannedEndpoints, job.ReachableEndpoints, job.CertificatesFound, job.CertificatesNew)
	}

	// running → completed.
	if status := env.post(t, mtls, "/sensor/v1/jobs/"+jobID+"/complete",
		sensorwire.CompleteRequest{Status: sensorwire.CompleteCompleted, ScannedEndpoints: 2, ReachableEndpoints: 1, CertificatesFound: 1},
		nil); status != http.StatusNoContent {
		t.Fatalf("complete = %d, want 204", status)
	}
	// 터미널 상태 재보고는 409.
	if status := env.post(t, mtls, "/sensor/v1/jobs/"+jobID+"/complete",
		sensorwire.CompleteRequest{Status: sensorwire.CompleteCompleted},
		nil); status != http.StatusConflict {
		t.Fatalf("complete after terminal = %d, want 409", status)
	}
	job, err = env.st.GetScanJob(ctx, jobID)
	if err != nil || job.Status != "completed" {
		t.Fatalf("final job = %+v, err %v; want completed", job, err)
	}
}

// TestSensorGatewayEnrollWithCAPin은 실제 게이트웨이 TLS 체인을 대상으로 센서
// 바이너리의 enroll(--ca-fingerprint = CA 지문 핀, sensor.Enroll의 verifyPinnedChain)이
// 통과하는지 본다. 회귀 방지: 서버가 리프 인증서만 제시하면 체인에서 CA 핀을 찾지
// 못해 실환경 등록이 실패한다 — 제시 체인은 리프+CA여야 한다.
func TestSensorGatewayEnrollWithCAPin(t *testing.T) {
	env := newGatewayEnv(t)
	ctx := context.Background()

	_, token, err := env.st.CreateSensor(ctx, store.CreateSensorInput{Name: "gw-capin"})
	if err != nil {
		t.Fatalf("create sensor: %v", err)
	}
	pin, err := env.gw.CAFingerprintSHA256()
	if err != nil {
		t.Fatalf("ca fingerprint: %v", err)
	}

	sensorID, err := sensor.Enroll(ctx, sensor.EnrollOptions{
		ServerURL:     env.baseURL,
		Token:         token,
		CAFingerprint: pin,
		StateDir:      t.TempDir(),
	})
	if err != nil {
		t.Fatalf("enroll with ca pin against live gateway tls chain: %v", err)
	}
	sn, err := env.st.GetSensor(ctx, sensorID)
	if err != nil || string(sn.Status) != "active" {
		t.Fatalf("sensor after pinned enroll = %+v, err %v; want active", sn, err)
	}
}

func TestSensorGatewayAuthRejections(t *testing.T) {
	env := newGatewayEnv(t)

	// 클라이언트 인증서 없이 보호 경로 접근 → 401.
	if status := env.post(t, env.plainClient(), "/sensor/v1/jobs/lease", nil, nil); status != http.StatusUnauthorized {
		t.Fatalf("lease without client cert = %d, want 401", status)
	}

	// 무효 토큰 enroll → 401 (무효·소진·만료 미구분).
	_, csrPEM := newSensorKeyCSR(t, "gw-badtoken")
	status := env.post(t, env.plainClient(), "/sensor/v1/enroll",
		sensorwire.EnrollRequest{Token: "not-a-real-token", CSRPEM: csrPEM}, nil)
	if status != http.StatusUnauthorized {
		t.Fatalf("enroll with bad token = %d, want 401", status)
	}

	// CSR 불량 → 400.
	_, token, err := env.st.CreateSensor(context.Background(), store.CreateSensorInput{Name: "gw-badcsr"})
	if err != nil {
		t.Fatalf("create sensor: %v", err)
	}
	status = env.post(t, env.plainClient(), "/sensor/v1/enroll",
		sensorwire.EnrollRequest{Token: token, CSRPEM: "not a csr"}, nil)
	if status != http.StatusBadRequest {
		t.Fatalf("enroll with bad csr = %d, want 400", status)
	}

	// 잘못된 UUID 경로값 → 404 (인증은 통과해야 하므로 등록 센서로).
	sensorID, ckey, certPEM := env.enroll(t, "gw-auth")
	_ = sensorID
	mtls := env.mtlsClient(t, ckey, certPEM)
	if status := env.post(t, mtls, "/sensor/v1/jobs/not-a-uuid/start",
		sensorwire.JobStartRequest{TotalEndpoints: 1}, nil); status != http.StatusNotFound {
		t.Fatalf("start with bad uuid = %d, want 404", status)
	}
}

func TestSensorGatewayRenew(t *testing.T) {
	env := newGatewayEnv(t)

	_, oldKey, oldCertPEM := env.enroll(t, "gw-renew")
	oldClient := env.mtlsClient(t, oldKey, oldCertPEM)

	// 새 키·CSR로 갱신.
	newKey, newCSR := newSensorKeyCSR(t, "gw-renew")
	var resp sensorwire.RenewResponse
	if status := env.post(t, oldClient, "/sensor/v1/renew",
		sensorwire.RenewRequest{CSRPEM: newCSR}, &resp); status != http.StatusOK {
		t.Fatalf("renew = %d, want 200", status)
	}

	// 새 인증서로는 통과, 옛 인증서는 401 (fingerprint 원자 교체됨).
	newClient := env.mtlsClient(t, newKey, resp.ClientCertPEM)
	if status := env.post(t, newClient, "/sensor/v1/jobs/lease", nil, nil); status != http.StatusNoContent {
		t.Fatalf("lease with renewed cert = %d, want 204", status)
	}
	if status := env.post(t, oldClient, "/sensor/v1/jobs/lease", nil, nil); status != http.StatusUnauthorized {
		t.Fatalf("lease with replaced cert = %d, want 401", status)
	}

	// 옛 인증서로의 재갱신 시도는 409 (이미 교체됨).
	_, retryCSR := newSensorKeyCSR(t, "gw-renew")
	if status := env.post(t, oldClient, "/sensor/v1/renew",
		sensorwire.RenewRequest{CSRPEM: retryCSR}, nil); status != http.StatusUnauthorized {
		// 옛 fingerprint는 이미 store에서 제거되어 requireSensor에서 401로 걸린다.
		t.Fatalf("renew with replaced cert = %d, want 401", status)
	}
}

func TestSensorCABootstrapAndReload(t *testing.T) {
	env := newGatewayEnv(t)
	ctx := context.Background()

	fp1, err := env.gw.CAFingerprintSHA256()
	if err != nil {
		t.Fatalf("ca fingerprint: %v", err)
	}

	// 같은 sealKey로 다시 만들면 저장된 CA를 로드해야 한다 (재생성 금지).
	gw2, err := New(ctx, env.st, nil, env.sealKey)
	if err != nil {
		t.Fatalf("reload gateway: %v", err)
	}
	fp2, err := gw2.CAFingerprintSHA256()
	if err != nil {
		t.Fatalf("ca fingerprint 2: %v", err)
	}
	if fp1 != fp2 {
		t.Fatalf("reloaded CA fingerprint differs: %s vs %s", fp1, fp2)
	}

	// 다른 sealKey로는 개봉 실패해야 한다 (봉인 저장 검증).
	wrongKey := make([]byte, 32)
	if _, err := rand.Read(wrongKey); err != nil {
		t.Fatalf("wrong key: %v", err)
	}
	if _, err := New(ctx, env.st, nil, wrongKey); err == nil {
		t.Fatal("New with wrong seal key succeeded, want unseal error")
	}
}

func TestSensorGatewayReaper(t *testing.T) {
	env := newGatewayEnv(t)
	ctx := context.Background()

	sensorID, key, certPEM := env.enroll(t, "gw-reaper")
	mtls := env.mtlsClient(t, key, certPEM)

	jobID, err := env.st.CreateScanJob(ctx, store.ScanInput{
		Targets: []string{"192.0.2.20"}, Ports: []int{443},
		TimeoutMs: 1000, Concurrency: 1, SensorID: &sensorID,
	})
	if err != nil {
		t.Fatalf("create scan job: %v", err)
	}
	if status := env.post(t, mtls, "/sensor/v1/jobs/lease", nil, &sensorwire.LeaseResponse{}); status != http.StatusOK {
		t.Fatalf("lease = %d, want 200", status)
	}

	// 리스를 강제로 만료시키고 reaper가 failed로 회수하는지 본다.
	if err := expireLease(env, jobID); err != nil {
		t.Fatalf("expire lease: %v", err)
	}
	stop := env.gw.startReaper(ctx, 20*time.Millisecond)
	defer stop()

	deadline := time.Now().Add(5 * time.Second)
	for {
		job, err := env.st.GetScanJob(ctx, jobID)
		if err != nil {
			t.Fatalf("get scan job: %v", err)
		}
		if job.Status == "failed" {
			if job.Error == nil || *job.Error != "lease expired" {
				t.Fatalf("reaped job error = %v, want 'lease expired'", job.Error)
			}
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("job not reaped in time; status = %s", job.Status)
		}
		time.Sleep(20 * time.Millisecond)
	}

	// 회수된 Job의 하트비트는 409.
	if status := env.post(t, mtls, "/sensor/v1/jobs/"+jobID+"/heartbeat",
		sensorwire.HeartbeatRequest{}, nil); status != http.StatusConflict {
		t.Fatalf("heartbeat after reap = %d, want 409", status)
	}
}

// expireLease는 테스트 전용으로 리스 만료 시각을 과거로 돌린다.
func expireLease(env *gwEnv, jobID string) error {
	dsn := os.Getenv("TEST_DATABASE_URL")
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	conn, err := db.Connect(ctx, dsn)
	if err != nil {
		return err
	}
	defer conn.Close()
	res, err := conn.ExecContext(ctx,
		`UPDATE scan_jobs SET lease_expires_at = now() - interval '1 minute' WHERE id = $1::uuid`, jobID)
	if err != nil {
		return err
	}
	n, err := res.RowsAffected()
	if err != nil {
		return err
	}
	if n != 1 {
		return fmt.Errorf("expire lease affected %d rows, want 1", n)
	}
	return nil
}
