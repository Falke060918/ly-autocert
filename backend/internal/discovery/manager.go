package discovery

import (
	"context"
	"log/slog"
	"sync"
	"time"

	"github.com/Falke060918/INHERIT_AutoCert/backend/internal/store"
)

// 타임아웃·동시성 기본값·경계값. 사용자 입력을 이 범위로 클램프한다.
const (
	defaultTimeout     = 5 * time.Second
	minTimeout         = 100 * time.Millisecond
	maxTimeout         = 30 * time.Second
	defaultConcurrency = 20
	maxConcurrency     = 256
)

// Manager는 스캔 Job의 생성·백그라운드 실행을 조율한다.
// 실제 인증서 작업(발견)은 AI가 아닌 명시적 상태 머신으로 실행한다 (CLAUDE.md 결정론적 자동화).
type Manager struct {
	store *store.Store
}

// NewManager는 Manager를 만든다.
func NewManager(st *store.Store) *Manager {
	return &Manager{store: st}
}

// StartResult는 스캔 생성 결과다(즉시 반환값).
type StartResult struct {
	JobID          string
	TotalEndpoints int
}

// Start는 입력을 검증·펼쳐 Job을 만들고 백그라운드 스캔을 띄운 뒤 즉시 반환한다.
// 입력 오류는 discovery.ErrInvalidScanInput으로 감싸 반환한다(핸들러가 400 매핑).
func (m *Manager) Start(ctx context.Context, in store.ScanInput) (*StartResult, error) {
	endpoints, err := Expand(in.Targets, in.Ports)
	if err != nil {
		return nil, err
	}

	timeout := clampTimeout(in.TimeoutMs)
	concurrency := clampConcurrency(in.Concurrency)

	jobID, err := m.store.CreateScanJob(ctx, in)
	if err != nil {
		return nil, err
	}

	// 백그라운드 실행은 요청 컨텍스트와 무관하게 살아야 하므로 Background를 쓴다.
	go m.run(context.Background(), jobID, endpoints, concurrency, timeout)

	return &StartResult{JobID: jobID, TotalEndpoints: len(endpoints)}, nil
}

// run은 Job의 상태 머신을 진행한다: running → 엔드포인트 스캔 → completed/failed.
func (m *Manager) run(ctx context.Context, jobID string, endpoints []Endpoint, concurrency int, timeout time.Duration) {
	if err := m.store.MarkScanRunning(ctx, jobID, len(endpoints)); err != nil {
		slog.Error("discovery: mark running failed", "error", err.Error(), "job_id", jobID)
		reason := "failed to start scan"
		if ferr := m.store.MarkScanFinished(ctx, jobID, store.ScanStatusFailed, &reason); ferr != nil {
			slog.Error("discovery: mark failed", "error", ferr.Error(), "job_id", jobID)
		}
		return
	}

	sem := make(chan struct{}, concurrency)
	var wg sync.WaitGroup
	for _, ep := range endpoints {
		sem <- struct{}{}
		wg.Add(1)
		go func(ep Endpoint) {
			defer wg.Done()
			defer func() { <-sem }()

			obs := ScanEndpoint(ctx, ep, timeout)
			// 개별 DB 오류는 로그만 남기고 계속한다 — 한 엔드포인트가 전체 Job을 멈추지 않는다.
			// 로그에는 job_id와 오류 문자열만 남긴다(인증서 본문 금지, 보안 하드 규칙 3).
			if err := m.store.RecordEndpointResult(ctx, jobID, toStoreObs(obs)); err != nil {
				slog.Error("discovery: record endpoint result failed", "error", err.Error(), "job_id", jobID)
			}
		}(ep)
	}
	wg.Wait()

	if err := m.store.MarkScanFinished(ctx, jobID, store.ScanStatusCompleted, nil); err != nil {
		slog.Error("discovery: mark completed failed", "error", err.Error(), "job_id", jobID)
	}
}

// toStoreObs는 스캔 결과를 store 계층 타입으로 변환한다(순환 import 방지).
func toStoreObs(o Observation) store.EndpointObservation {
	return store.EndpointObservation{
		IP:         o.IP,
		Port:       o.Port,
		Reachable:  o.Reachable,
		TLSVersion: o.TLSVersion,
		Hostname:   o.Hostname,
		Chain:      o.Chain,
	}
}

func clampTimeout(ms int) time.Duration {
	if ms <= 0 {
		return defaultTimeout
	}
	d := time.Duration(ms) * time.Millisecond
	if d < minTimeout {
		return minTimeout
	}
	if d > maxTimeout {
		return maxTimeout
	}
	return d
}

func clampConcurrency(n int) int {
	if n <= 0 {
		return defaultConcurrency
	}
	if n > maxConcurrency {
		return maxConcurrency
	}
	return n
}
