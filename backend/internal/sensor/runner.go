package sensor

import (
	"context"
	"encoding/base64"
	"log/slog"
	"sync"
	"sync/atomic"
	"time"

	"github.com/Falke060918/INHERIT_AutoCert/backend/internal/discovery"
	"github.com/Falke060918/INHERIT_AutoCert/backend/internal/sensorwire"
)

// jobTypeScan은 폐쇄 enum의 유일한 정의값이다 (sensorwire.LeaseResponse.JobType).
// 센서는 모르는 유형을 실행하지 않고 거부한다 (결정론적 자동화 규칙).
const jobTypeScan = "scan"

// 폴링·하트비트·갱신 기본값. 테스트에서 Runner 필드로 재정의한다.
const (
	defaultPollInterval       = 5 * time.Second
	maxPollInterval           = 60 * time.Second
	defaultHeartbeatInterval  = 30 * time.Second
	defaultRenewCheckInterval = 24 * time.Hour
	defaultRenewBefore        = 30 * 24 * time.Hour
	// defaultFlushInterval은 관측 배치의 시간 기반 플러시 주기다. 배치 상한(100건)에
	// 못 미치는 스캔도 이 주기마다 서버에 반영돼 진행 상황이 보인다.
	defaultFlushInterval = 30 * time.Second

	// maxSendBatch는 관측 전송 배치 상한이다. wire 상한(1000)보다 작게 유지한다.
	maxSendBatch = 100
	// sendAttempts는 배치 전송 재시도 횟수다. 서버 멱등 처리로 재전송이 안전하다.
	sendAttempts = 3
)

// 스캔 파라미터 경계값 — 서버 manager와 같은 정책으로 리스 값을 클램프한다.
const (
	defaultScanTimeout     = 5 * time.Second
	minScanTimeout         = 100 * time.Millisecond
	maxScanTimeout         = 30 * time.Second
	defaultScanConcurrency = 20
	maxScanConcurrency     = 256
)

// Runner는 센서의 폴링 루프다: lease → 전개·start → 스캔 수집 → 관측 배치 전송 → complete.
// 인증서 갱신(renew)은 기동 시와 일 1회 검사한다.
type Runner struct {
	Client   *Client
	Identity *Identity
	StateDir string
	Logger   *slog.Logger

	// 0이면 기본값을 쓴다. 테스트 재정의용.
	PollInterval       time.Duration
	HeartbeatInterval  time.Duration
	FlushInterval      time.Duration
	RenewCheckInterval time.Duration
	RenewBefore        time.Duration
}

func (r *Runner) log() *slog.Logger {
	if r.Logger != nil {
		return r.Logger
	}
	return slog.Default()
}

func (r *Runner) pollInterval() time.Duration {
	if r.PollInterval > 0 {
		return r.PollInterval
	}
	return defaultPollInterval
}

func (r *Runner) heartbeatInterval() time.Duration {
	if r.HeartbeatInterval > 0 {
		return r.HeartbeatInterval
	}
	return defaultHeartbeatInterval
}

func (r *Runner) flushInterval() time.Duration {
	if r.FlushInterval > 0 {
		return r.FlushInterval
	}
	return defaultFlushInterval
}

func (r *Runner) renewCheckInterval() time.Duration {
	if r.RenewCheckInterval > 0 {
		return r.RenewCheckInterval
	}
	return defaultRenewCheckInterval
}

func (r *Runner) renewBefore() time.Duration {
	if r.RenewBefore > 0 {
		return r.RenewBefore
	}
	return defaultRenewBefore
}

// Run은 종료 신호(ctx 취소)까지 폴링 루프를 돈다. 작업이 없거나(204) 서버에 닿지
// 못하면 지수 백오프로 물러난다. 아웃바운드 1경로만 쓰므로 인바운드 리스너는 없다.
func (r *Runner) Run(ctx context.Context) error {
	r.renewIfNeeded(ctx) // 기동 시 1회

	renewTick := time.NewTicker(r.renewCheckInterval())
	defer renewTick.Stop()

	backoff := r.pollInterval()
	for {
		if ctx.Err() != nil {
			return nil
		}
		job, err := r.Client.Lease(ctx)
		switch {
		case err != nil:
			if ctx.Err() != nil {
				return nil
			}
			r.log().Warn("lease failed", "error", err.Error())
		case job != nil:
			r.runJob(ctx, job)
			backoff = r.pollInterval()
			continue // 작업을 마쳤으면 즉시 다음 리스를 시도한다.
		}

		select {
		case <-ctx.Done():
			return nil
		case <-renewTick.C:
			r.renewIfNeeded(ctx)
		case <-time.After(backoff):
			backoff = min(backoff*2, maxPollInterval)
		}
	}
}

// progress는 스캔 진행 카운터다. 하트비트·완료 보고가 공유한다.
type progress struct {
	scanned   atomic.Int64
	reachable atomic.Int64
	certs     atomic.Int64
}

func (p *progress) heartbeat() sensorwire.HeartbeatRequest {
	return sensorwire.HeartbeatRequest{
		ScannedEndpoints:   int(p.scanned.Load()),
		ReachableEndpoints: int(p.reachable.Load()),
		CertificatesFound:  int(p.certs.Load()),
	}
}

// runJob은 리스된 작업 1건의 상태 머신을 진행한다.
// 개별 엔드포인트 실패는 조용히 기록하고 계속한다 — 전체 Job을 멈추지 않는다.
func (r *Runner) runJob(ctx context.Context, job *sensorwire.LeaseResponse) {
	log := r.log().With("job_id", job.JobID)

	if job.JobType != jobTypeScan {
		// 폐쇄 enum — 모르는 유형은 실행하지 않고 실패로 종결한다.
		log.Error("unsupported job type", "job_type", job.JobType)
		r.complete(ctx, log, job.JobID, sensorwire.CompleteRequest{
			Status: sensorwire.CompleteFailed,
			Error:  "unsupported job type: " + job.JobType,
		})
		return
	}

	endpoints, err := discovery.Expand(job.Targets, job.Ports)
	if err != nil {
		log.Error("expand targets failed", "error", err.Error())
		r.complete(ctx, log, job.JobID, sensorwire.CompleteRequest{
			Status: sensorwire.CompleteFailed,
			Error:  "invalid scan input",
		})
		return
	}

	if err := r.Client.Start(ctx, job.JobID, len(endpoints)); err != nil {
		// running 전이를 못 알렸으면 실행하지 않는다 — 리스 만료로 서버가 회수한다.
		log.Error("job start report failed", "error", err.Error())
		return
	}
	log.Info("scan job started", "total_endpoints", len(endpoints))

	timeout := clampScanTimeout(job.TimeoutMs)
	concurrency := clampScanConcurrency(job.Concurrency)
	prog := &progress{}
	var uploadFailed atomic.Bool

	// 수집 → 배치 전송 파이프라인. 배치 상한마다, 그리고 플러시 주기마다 흘려보낸다 —
	// 상한(100건)에 못 미치는 스캔도 주기적으로 서버 진행률에 반영된다.
	obsCh := make(chan sensorwire.Observation, concurrency)
	var collectorWG sync.WaitGroup
	collectorWG.Add(1)
	go func() {
		defer collectorWG.Done()
		batch := make([]sensorwire.Observation, 0, maxSendBatch)
		flush := func() {
			if len(batch) == 0 {
				return
			}
			if err := r.sendWithRetry(ctx, job.JobID, batch); err != nil {
				uploadFailed.Store(true)
				log.Error("observation upload failed", "error", err.Error(), "batch_size", len(batch))
			}
			batch = batch[:0]
		}
		flushTick := time.NewTicker(r.flushInterval())
		defer flushTick.Stop()
		for {
			select {
			case obs, ok := <-obsCh:
				if !ok {
					flush() // 수집 종료 — 남은 관측을 마저 보낸다.
					return
				}
				batch = append(batch, obs)
				if len(batch) >= maxSendBatch {
					flush()
				}
			case <-flushTick.C:
				flush()
			}
		}
	}()

	// 30s 주기 하트비트 — 진행률 보고 겸 리스 연장.
	hbCtx, hbCancel := context.WithCancel(ctx)
	var hbWG sync.WaitGroup
	hbWG.Add(1)
	go func() {
		defer hbWG.Done()
		tick := time.NewTicker(r.heartbeatInterval())
		defer tick.Stop()
		for {
			select {
			case <-hbCtx.Done():
				return
			case <-tick.C:
				if err := r.Client.Heartbeat(hbCtx, job.JobID, prog.heartbeat()); err != nil && hbCtx.Err() == nil {
					log.Warn("heartbeat failed", "error", err.Error())
				}
			}
		}
	}()

	// 세마포어 동시성 제한 스캔 (discovery.ScanEndpoint 재사용 — store 비의존).
	sem := make(chan struct{}, concurrency)
	var scanWG sync.WaitGroup
	for _, ep := range endpoints {
		if ctx.Err() != nil {
			break
		}
		sem <- struct{}{}
		scanWG.Add(1)
		go func(ep discovery.Endpoint) {
			defer scanWG.Done()
			defer func() { <-sem }()
			obs := discovery.ScanEndpoint(ctx, ep, timeout)
			prog.scanned.Add(1)
			if obs.Reachable {
				prog.reachable.Add(1)
				prog.certs.Add(int64(len(obs.Chain)))
			}
			obsCh <- toWireObservation(obs)
		}(ep)
	}
	scanWG.Wait()
	close(obsCh)
	collectorWG.Wait()
	hbCancel()
	hbWG.Wait()

	if ctx.Err() != nil {
		// 종료 신호로 중단 — 종결 보고 없이 리스 만료에 맡긴다.
		log.Info("scan job interrupted by shutdown")
		return
	}

	req := sensorwire.CompleteRequest{
		Status:             sensorwire.CompleteCompleted,
		ScannedEndpoints:   int(prog.scanned.Load()),
		ReachableEndpoints: int(prog.reachable.Load()),
		CertificatesFound:  int(prog.certs.Load()),
	}
	if uploadFailed.Load() {
		req.Status = sensorwire.CompleteFailed
		req.Error = "observation upload failed"
	}
	r.complete(ctx, log, job.JobID, req)
	log.Info("scan job finished", "status", string(req.Status),
		"scanned", req.ScannedEndpoints, "reachable", req.ReachableEndpoints, "certificates", req.CertificatesFound)
}

// complete는 종결 보고를 보낸다. 실패해도 루프는 계속한다 — 리스 만료가 안전망이다.
func (r *Runner) complete(ctx context.Context, log *slog.Logger, jobID string, req sensorwire.CompleteRequest) {
	if err := r.Client.Complete(ctx, jobID, req); err != nil && ctx.Err() == nil {
		log.Error("job complete report failed", "error", err.Error())
	}
}

// sendWithRetry는 관측 배치를 최대 sendAttempts회 전송한다.
// 서버가 (job_id, ip, port)로 멱등 처리하므로 재전송은 중복을 만들지 않는다.
func (r *Runner) sendWithRetry(ctx context.Context, jobID string, obs []sensorwire.Observation) error {
	batch := sensorwire.ObservationBatch{Observations: append([]sensorwire.Observation(nil), obs...)}
	var err error
	for attempt := 1; attempt <= sendAttempts; attempt++ {
		if err = r.Client.SendObservations(ctx, jobID, batch); err == nil {
			return nil
		}
		select {
		case <-ctx.Done():
			return err
		case <-time.After(time.Duration(attempt) * time.Second):
		}
	}
	return err
}

// renewIfNeeded는 클라이언트 인증서 잔여 유효기간이 RenewBefore(기본 30일) 미만이면
// 기존 키로 새 CSR을 만들어 갱신하고, 검증 → 파일 원자 교체 → 메모리 반영 순서로 적용한다.
func (r *Runner) renewIfNeeded(ctx context.Context) {
	leaf := r.Identity.Leaf()
	if time.Until(leaf.NotAfter) > r.renewBefore() {
		return
	}
	log := r.log()
	log.Info("client certificate expiring soon, renewing", "not_after", leaf.NotAfter.UTC().Format(time.RFC3339))

	csrPEM, err := r.Identity.NewCSRPEM()
	if err != nil {
		log.Error("renew: create csr failed", "error", err.Error())
		return
	}
	resp, err := r.Client.Renew(ctx, csrPEM)
	if err != nil {
		if ctx.Err() == nil {
			log.Error("renew request failed", "error", err.Error())
		}
		return
	}
	// 파일이 진실이다: 검증(clientCertForKey) → 원자 교체 저장 → 메모리 스왑.
	certPEM := []byte(resp.ClientCertPEM)
	if _, err := clientCertForKey(certPEM, r.Identity.key); err != nil {
		log.Error("renew: issued certificate rejected", "error", err.Error())
		return
	}
	if err := ReplaceClientCert(r.StateDir, certPEM); err != nil {
		log.Error("renew: persist certificate failed", "error", err.Error())
		return
	}
	if err := r.Identity.InstallClientCert(certPEM); err != nil {
		log.Error("renew: install certificate failed", "error", err.Error())
		return
	}
	log.Info("client certificate renewed", "not_after", r.Identity.Leaf().NotAfter.UTC().Format(time.RFC3339))
}

// toWireObservation은 스캔 결과를 wire DTO로 바꾼다. 체인은 base64(DER) 배열이다.
func toWireObservation(o discovery.Observation) sensorwire.Observation {
	w := sensorwire.Observation{
		IP:         o.IP,
		Port:       o.Port,
		Reachable:  o.Reachable,
		TLSVersion: o.TLSVersion,
		// 암호 스위트는 추가 프로브 없이 얻으므로 센서도 그대로 싣는다.
		// 지원 버전 목록은 센서가 아직 탐지하지 않아 항상 비어 있다 —
		// 리스 응답에 옵션을 실어 보내는 프로토콜 변경이 선행돼야 한다.
		CipherSuite:          o.CipherSuite,
		SupportedTLSVersions: o.SupportedTLSVersions,
		Hostname:             o.Hostname,
		ObservedAt:           time.Now().UTC(),
	}
	for _, c := range o.Chain {
		w.ChainDER = append(w.ChainDER, base64.StdEncoding.EncodeToString(c.Raw))
	}
	return w
}

func clampScanTimeout(ms int) time.Duration {
	if ms <= 0 {
		return defaultScanTimeout
	}
	d := time.Duration(ms) * time.Millisecond
	if d < minScanTimeout {
		return minScanTimeout
	}
	if d > maxScanTimeout {
		return maxScanTimeout
	}
	return d
}

func clampScanConcurrency(n int) int {
	if n <= 0 {
		return defaultScanConcurrency
	}
	if n > maxScanConcurrency {
		return maxScanConcurrency
	}
	return n
}
