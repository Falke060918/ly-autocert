package sensor

import (
	"context"
	"crypto/ecdsa"
	"crypto/x509"
	"encoding/base64"
	"encoding/json"
	"encoding/pem"
	"net/http"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/Falke060918/INHERIT_AutoCert/backend/internal/sensorwire"
)

// gatewayRecorder는 모의 게이트웨이가 받은 보고를 기록한다.
type gatewayRecorder struct {
	mu         sync.Mutex
	leases     int
	starts     []sensorwire.JobStartRequest
	heartbeats []sensorwire.HeartbeatRequest
	batches    []sensorwire.ObservationBatch
	completes  []sensorwire.CompleteRequest
	completed  chan struct{}
}

func newGatewayRecorder() *gatewayRecorder {
	return &gatewayRecorder{completed: make(chan struct{})}
}

// mux는 sensorwire 계약대로 Job 하위 경로를 배선한다. job은 첫 lease에만 배정된다(이후 204).
func (g *gatewayRecorder) mux(jobID string, job *sensorwire.LeaseResponse) *http.ServeMux {
	mux := http.NewServeMux()
	mux.HandleFunc("POST /sensor/v1/jobs/lease", func(w http.ResponseWriter, r *http.Request) {
		g.mu.Lock()
		g.leases++
		first := g.leases == 1
		g.mu.Unlock()
		if !first || job == nil {
			w.WriteHeader(http.StatusNoContent)
			return
		}
		_ = json.NewEncoder(w).Encode(job)
	})
	mux.HandleFunc("POST /sensor/v1/jobs/"+jobID+"/start", func(w http.ResponseWriter, r *http.Request) {
		var req sensorwire.JobStartRequest
		_ = json.NewDecoder(r.Body).Decode(&req)
		g.mu.Lock()
		g.starts = append(g.starts, req)
		g.mu.Unlock()
		w.WriteHeader(http.StatusNoContent)
	})
	mux.HandleFunc("POST /sensor/v1/jobs/"+jobID+"/heartbeat", func(w http.ResponseWriter, r *http.Request) {
		var req sensorwire.HeartbeatRequest
		_ = json.NewDecoder(r.Body).Decode(&req)
		g.mu.Lock()
		g.heartbeats = append(g.heartbeats, req)
		g.mu.Unlock()
		w.WriteHeader(http.StatusNoContent)
	})
	mux.HandleFunc("POST /sensor/v1/jobs/"+jobID+"/observations", func(w http.ResponseWriter, r *http.Request) {
		var req sensorwire.ObservationBatch
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil || req.Validate() != nil {
			http.Error(w, "bad batch", http.StatusBadRequest)
			return
		}
		g.mu.Lock()
		g.batches = append(g.batches, req)
		g.mu.Unlock()
		w.WriteHeader(http.StatusNoContent)
	})
	mux.HandleFunc("POST /sensor/v1/jobs/"+jobID+"/complete", func(w http.ResponseWriter, r *http.Request) {
		var req sensorwire.CompleteRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil || req.Validate() != nil {
			http.Error(w, "bad complete", http.StatusBadRequest)
			return
		}
		g.mu.Lock()
		g.completes = append(g.completes, req)
		g.mu.Unlock()
		close(g.completed)
		w.WriteHeader(http.StatusNoContent)
	})
	return mux
}

// TestRunScanJob은 lease → 전개·start → 스캔 → 관측 배치 → complete 전체 상태 머신을 본다.
// 대상 2개: TLS 리스너(수집 성공) + 무응답 리스너(타임아웃, reachable=false).
func TestRunScanJob(t *testing.T) {
	ca := newTestCA(t)
	dir := t.TempDir()
	id := newEnrolledIdentity(t, ca, dir, time.Now().Add(90*24*time.Hour))

	tlsPort, targetDER := newTLSTarget(t)
	stallPort := newStallTarget(t)

	const jobID = "job-0001"
	rec := newGatewayRecorder()
	job := &sensorwire.LeaseResponse{
		JobID:          jobID,
		JobType:        "scan",
		Targets:        []string{"127.0.0.1"},
		Ports:          []int{tlsPort, stallPort},
		TimeoutMs:      500,
		Concurrency:    4,
		LeaseExpiresAt: time.Now().Add(time.Minute),
	}
	ts := newMTLSGateway(t, ca, rec.mux(jobID, job))

	client, err := NewClient(ts.URL, id)
	if err != nil {
		t.Fatalf("new client: %v", err)
	}
	runner := &Runner{
		Client:            client,
		Identity:          id,
		StateDir:          dir,
		Logger:            discardLogger(),
		PollInterval:      10 * time.Millisecond,
		HeartbeatInterval: 20 * time.Millisecond,
	}

	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	done := make(chan error, 1)
	go func() { done <- runner.Run(ctx) }()

	select {
	case <-rec.completed:
	case <-time.After(15 * time.Second):
		t.Fatal("complete report never arrived")
	}
	cancel()
	if err := <-done; err != nil {
		t.Fatalf("runner exited with error: %v", err)
	}

	rec.mu.Lock()
	defer rec.mu.Unlock()

	if len(rec.starts) != 1 || rec.starts[0].TotalEndpoints != 2 {
		t.Fatalf("start reports = %+v, want one report with 2 endpoints", rec.starts)
	}
	if len(rec.heartbeats) == 0 {
		t.Error("expected at least one heartbeat during the stalled scan")
	}

	var all []sensorwire.Observation
	for _, b := range rec.batches {
		all = append(all, b.Observations...)
	}
	if len(all) != 2 {
		t.Fatalf("observations = %d, want 2", len(all))
	}
	byPort := map[int]sensorwire.Observation{}
	for _, o := range all {
		if o.IP != "127.0.0.1" {
			t.Errorf("observation ip = %q", o.IP)
		}
		if o.ObservedAt.IsZero() {
			t.Error("observation missing observed_at")
		}
		byPort[o.Port] = o
	}

	got, ok := byPort[tlsPort]
	if !ok || !got.Reachable {
		t.Fatalf("tls target observation missing or unreachable: %+v", got)
	}
	if got.TLSVersion == "" {
		t.Error("tls target observation missing tls version")
	}
	if len(got.ChainDER) != 1 {
		t.Fatalf("chain length = %d, want 1", len(got.ChainDER))
	}
	der, err := base64.StdEncoding.DecodeString(got.ChainDER[0])
	if err != nil {
		t.Fatalf("chain_der is not base64: %v", err)
	}
	if string(der) != string(targetDER) {
		t.Error("collected certificate does not match the target certificate")
	}

	stalled, ok := byPort[stallPort]
	if !ok || stalled.Reachable || len(stalled.ChainDER) != 0 {
		t.Fatalf("stall target observation wrong: %+v", stalled)
	}

	if len(rec.completes) != 1 {
		t.Fatalf("complete reports = %d, want 1", len(rec.completes))
	}
	c := rec.completes[0]
	if c.Status != sensorwire.CompleteCompleted {
		t.Fatalf("complete status = %q (error %q)", c.Status, c.Error)
	}
	if c.ScannedEndpoints != 2 || c.ReachableEndpoints != 1 || c.CertificatesFound != 1 {
		t.Fatalf("complete counters = %+v", c)
	}
}

// TestRunJobRejectsUnknownType은 폐쇄 enum 규약을 본다 — 모르는 유형은 실행 없이 failed.
func TestRunJobRejectsUnknownType(t *testing.T) {
	ca := newTestCA(t)
	dir := t.TempDir()
	id := newEnrolledIdentity(t, ca, dir, time.Now().Add(90*24*time.Hour))

	const jobID = "job-0002"
	rec := newGatewayRecorder()
	ts := newMTLSGateway(t, ca, rec.mux(jobID, nil))

	client, err := NewClient(ts.URL, id)
	if err != nil {
		t.Fatalf("new client: %v", err)
	}
	runner := &Runner{Client: client, Identity: id, StateDir: dir, Logger: discardLogger()}
	runner.runJob(context.Background(), &sensorwire.LeaseResponse{JobID: jobID, JobType: "deploy"})

	rec.mu.Lock()
	defer rec.mu.Unlock()
	if len(rec.starts) != 0 {
		t.Fatal("unknown job type must not report start")
	}
	if len(rec.completes) != 1 || rec.completes[0].Status != sensorwire.CompleteFailed {
		t.Fatalf("completes = %+v, want one failed", rec.completes)
	}
	if !strings.Contains(rec.completes[0].Error, "unsupported job type") {
		t.Fatalf("complete error = %q", rec.completes[0].Error)
	}
}

// newRenewHandler는 게이트웨이의 renew 계약을 흉내 낸다: CSR 검증 → 90일 인증서 재발급.
func newRenewHandler(ca *testCA, calls *int, mu *sync.Mutex) http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("POST /sensor/v1/renew", func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		*calls++
		mu.Unlock()
		var req sensorwire.RenewRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil || req.Validate() != nil {
			http.Error(w, "bad request", http.StatusBadRequest)
			return
		}
		block, _ := pem.Decode([]byte(req.CSRPEM))
		csr, err := x509.ParseCertificateRequest(block.Bytes)
		if err != nil || csr.CheckSignature() != nil {
			http.Error(w, "bad csr", http.StatusBadRequest)
			return
		}
		certPEM, err := ca.issueClientCert(csr.PublicKey.(*ecdsa.PublicKey), csr.Subject.CommonName, time.Now().Add(90*24*time.Hour))
		if err != nil {
			http.Error(w, "issue failed", http.StatusInternalServerError)
			return
		}
		_ = json.NewEncoder(w).Encode(sensorwire.RenewResponse{ClientCertPEM: string(certPEM)})
	})
	return mux
}

// TestRenewIfNeeded는 잔여 30일 미만이면 새 CSR로 갱신하고 파일·메모리에 반영하는지 본다.
func TestRenewIfNeeded(t *testing.T) {
	ca := newTestCA(t)
	dir := t.TempDir()
	// 잔여 10일 — 기본 RenewBefore(30일) 미만이므로 갱신 대상이다.
	id := newEnrolledIdentity(t, ca, dir, time.Now().Add(10*24*time.Hour))
	oldNotAfter := id.Leaf().NotAfter

	var calls int
	var mu sync.Mutex
	ts := newMTLSGateway(t, ca, newRenewHandler(ca, &calls, &mu))

	client, err := NewClient(ts.URL, id)
	if err != nil {
		t.Fatalf("new client: %v", err)
	}
	runner := &Runner{Client: client, Identity: id, StateDir: dir, Logger: discardLogger()}
	runner.renewIfNeeded(context.Background())

	mu.Lock()
	if calls != 1 {
		mu.Unlock()
		t.Fatalf("renew calls = %d, want 1", calls)
	}
	mu.Unlock()

	if !id.Leaf().NotAfter.After(oldNotAfter) {
		t.Fatal("in-memory certificate not renewed")
	}
	// 파일이 진실이다 — 재복원해도 갱신본이어야 한다.
	reloaded, err := LoadIdentity(dir)
	if err != nil {
		t.Fatalf("reload identity: %v", err)
	}
	if !reloaded.Leaf().NotAfter.After(oldNotAfter) {
		t.Fatal("persisted certificate not renewed")
	}
}

// TestRenewSkippedWhenFresh는 잔여 기간이 충분하면 갱신 요청을 보내지 않는지 본다.
func TestRenewSkippedWhenFresh(t *testing.T) {
	ca := newTestCA(t)
	dir := t.TempDir()
	id := newEnrolledIdentity(t, ca, dir, time.Now().Add(90*24*time.Hour))

	var calls int
	var mu sync.Mutex
	ts := newMTLSGateway(t, ca, newRenewHandler(ca, &calls, &mu))

	client, err := NewClient(ts.URL, id)
	if err != nil {
		t.Fatalf("new client: %v", err)
	}
	runner := &Runner{Client: client, Identity: id, StateDir: dir, Logger: discardLogger()}
	runner.renewIfNeeded(context.Background())

	mu.Lock()
	defer mu.Unlock()
	if calls != 0 {
		t.Fatalf("renew calls = %d, want 0", calls)
	}
}

func TestClampScanParameters(t *testing.T) {
	if got := clampScanTimeout(0); got != defaultScanTimeout {
		t.Errorf("clampScanTimeout(0) = %v", got)
	}
	if got := clampScanTimeout(1); got != minScanTimeout {
		t.Errorf("clampScanTimeout(1) = %v", got)
	}
	if got := clampScanTimeout(10 * 60 * 1000); got != maxScanTimeout {
		t.Errorf("clampScanTimeout(10m) = %v", got)
	}
	if got := clampScanConcurrency(0); got != defaultScanConcurrency {
		t.Errorf("clampScanConcurrency(0) = %d", got)
	}
	if got := clampScanConcurrency(10000); got != maxScanConcurrency {
		t.Errorf("clampScanConcurrency(10000) = %d", got)
	}
	if got := clampScanConcurrency(8); got != 8 {
		t.Errorf("clampScanConcurrency(8) = %d", got)
	}
}
