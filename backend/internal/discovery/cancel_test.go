package discovery

// 실행 중 서버 스캔의 취소 경로 검증 (B3): store 전이(canceled) 후 Manager의
// cancel 레지스트리가 goroutine ctx를 끊고, 물러난 goroutine이 canceled 행을
// completed로 덮어쓰지 않아야 한다 (TEST_DATABASE_URL 필요 — 미설정 시 스킵).

import (
	"context"
	"testing"
	"time"

	"github.com/Falke060918/INHERIT_AutoCert/backend/internal/store"
)

func TestCancelScanStopsRunningServerJob(t *testing.T) {
	st, pool := testSweepStore(t)
	ctx := context.Background()
	reqID := "test-req-cancel-running"

	// 스캔이 ctx 취소까지 블록되게 스텁한다 — 실행 중 상태를 안정적으로 만든다.
	released := make(chan struct{})
	prev := scanEndpoint
	scanEndpoint = func(sctx context.Context, ep Endpoint, _ time.Duration) Observation {
		select {
		case <-sctx.Done():
		case <-time.After(10 * time.Second): // 테스트 실패 안전망
		}
		close(released)
		return Observation{Endpoint: ep}
	}
	t.Cleanup(func() { scanEndpoint = prev })

	m := NewManager(st)
	res, err := m.Start(ctx, store.ScanInput{
		Targets:   []string{"192.0.2.50"},
		Ports:     []int{443},
		RequestID: &reqID,
	})
	if err != nil {
		t.Fatalf("start scan: %v", err)
	}
	jobID := res.JobID
	t.Cleanup(func() {
		_, _ = pool.Exec(`DELETE FROM scan_jobs WHERE id = $1::uuid`, jobID)
		_, _ = pool.Exec(`ALTER TABLE audit_events DISABLE TRIGGER USER`)
		_, _ = pool.Exec(`DELETE FROM audit_events WHERE request_id = $1 OR target_id = $2`, reqID, jobID)
		_, _ = pool.Exec(`ALTER TABLE audit_events ENABLE TRIGGER USER`)
	})

	// running 전이를 기다린다 (waitFor는 sweep_test.go의 공용 헬퍼).
	waitFor(t, 5*time.Second, "job did not reach running", func() bool {
		job, err := st.GetScanJob(ctx, jobID)
		return err == nil && job.Status == store.ScanStatusRunning
	})

	// 취소: 전이 먼저(store), goroutine ctx는 그 다음(레지스트리) — API 핸들러와 같은 순서.
	if _, err := st.CancelScanJob(ctx, jobID, store.ScanCancelInput{RequestID: &reqID}); err != nil {
		t.Fatalf("cancel: %v", err)
	}
	if !m.CancelScan(jobID) {
		t.Fatal("CancelScan = false, want registered cancel for running job")
	}

	// goroutine이 풀려나고, 상태는 canceled로 남아야 한다(completed 덮어쓰기 금지).
	select {
	case <-released:
	case <-time.After(5 * time.Second):
		t.Fatal("scan goroutine did not observe ctx cancel")
	}
	time.Sleep(200 * time.Millisecond) // run의 잔여 경로(MarkScanFinished 시도)까지 대기
	job, err := st.GetScanJob(ctx, jobID)
	if err != nil {
		t.Fatalf("get job: %v", err)
	}
	if job.Status != store.ScanStatusCanceled {
		t.Errorf("status = %q, want canceled", job.Status)
	}

	// 이미 정리된 Job의 재취소 신호는 false다(오류 아님).
	waitFor(t, 2*time.Second, "cancel registry entry was not released",
		func() bool { return !m.CancelScan(jobID) })
}
