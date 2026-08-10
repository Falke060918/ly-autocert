package store

// 스캔 취소·중복 발주 가드 검증 (0017, B3).

import (
	"context"
	"errors"
	"testing"

	"github.com/Falke060918/INHERIT_AutoCert/backend/internal/domain"
)

func TestCreateScanJobDuplicateGuard(t *testing.T) {
	pool := testPool(t)
	st := New(pool)
	ctx := context.Background()
	reqID := "test-req-scan-dedup"

	in := ScanInput{
		Targets:        []string{"192.0.2.20", "192.0.2.21"},
		Ports:          []int{443, 8443},
		TimeoutMs:      1000,
		Concurrency:    1,
		TotalEndpoints: 4,
		RequestID:      &reqID,
	}
	id1, err := st.CreateScanJob(ctx, in)
	if err != nil {
		t.Fatalf("first create: %v", err)
	}
	var ids []string
	ids = append(ids, id1)
	t.Cleanup(func() {
		for _, id := range ids {
			_, _ = pool.Exec(`DELETE FROM scan_jobs WHERE id = $1::uuid`, id)
		}
		cleanupAuditByRequestID(pool, reqID)
	})

	// 동일 요청 — 대상·포트 순서를 섞어도 같은 정규화 지문이어야 한다.
	shuffled := in
	shuffled.Targets = []string{"192.0.2.21", "192.0.2.20"}
	shuffled.Ports = []int{8443, 443}
	_, err = st.CreateScanJob(ctx, shuffled)
	var dup *DuplicateScanError
	if !errors.As(err, &dup) {
		t.Fatalf("duplicate create err = %v, want *DuplicateScanError", err)
	}
	if dup.JobID != id1 {
		t.Errorf("duplicate job_id = %q, want %q", dup.JobID, id1)
	}

	// 포트가 다르면 다른 요청이다.
	other := in
	other.Ports = []int{443}
	id2, err := st.CreateScanJob(ctx, other)
	if err != nil {
		t.Fatalf("different ports create: %v", err)
	}
	ids = append(ids, id2)

	// 기존 Job이 종료(취소)되면 같은 요청을 다시 발주할 수 있다.
	if _, err := st.CancelScanJob(ctx, id1, ScanCancelInput{RequestID: &reqID}); err != nil {
		t.Fatalf("cancel: %v", err)
	}
	id3, err := st.CreateScanJob(ctx, in)
	if err != nil {
		t.Fatalf("recreate after cancel: %v", err)
	}
	ids = append(ids, id3)
}

func TestCancelScanJobPendingAndStateGuard(t *testing.T) {
	pool := testPool(t)
	st := New(pool)
	ctx := context.Background()
	reqID := "test-req-scan-cancel"

	id := createTestScanJob(t, st, reqID, []string{"192.0.2.30"})
	t.Cleanup(func() {
		_, _ = pool.Exec(`DELETE FROM scan_jobs WHERE id = $1::uuid`, id)
		cleanupAuditByRequestID(pool, reqID)
	})

	job, err := st.CancelScanJob(ctx, id, ScanCancelInput{RequestID: &reqID})
	if err != nil {
		t.Fatalf("cancel pending: %v", err)
	}
	if job.Status != ScanStatusCanceled || job.FinishedAt == nil {
		t.Errorf("job = %q/finished %v, want canceled with finished_at", job.Status, job.FinishedAt)
	}

	// 취소 감사 이벤트가 전이와 같은 트랜잭션으로 남는다 (하드 규칙 5).
	var auditCount int
	if err := pool.QueryRow(`SELECT count(*) FROM audit_events WHERE event_type = $1 AND target_id = $2`,
		EventDiscoveryScanCancel, id).Scan(&auditCount); err != nil {
		t.Fatalf("count cancel audits: %v", err)
	}
	if auditCount != 1 {
		t.Errorf("cancel audit events = %d, want 1", auditCount)
	}

	// 이미 종료된 Job은 다시 취소할 수 없다.
	if _, err := st.CancelScanJob(ctx, id, ScanCancelInput{RequestID: &reqID}); !errors.Is(err, ErrScanJobState) {
		t.Errorf("second cancel err = %v, want ErrScanJobState", err)
	}
	// 없는 Job은 not found다.
	if _, err := st.CancelScanJob(ctx, "00000000-0000-0000-0000-000000000000",
		ScanCancelInput{RequestID: &reqID}); !errors.Is(err, ErrScanJobNotFound) {
		t.Errorf("missing job err = %v, want ErrScanJobNotFound", err)
	}
}

func TestCancelScanJobRejectsSweepChunkAndSensorRun(t *testing.T) {
	pool := testPool(t)
	st := New(pool)
	ctx := context.Background()
	reqID := "test-req-scan-cancel-guards"

	// 스윕 자식 청크 — 개별 취소는 스윕 집계를 깨므로 거부된다.
	sw, err := st.CreateSweep(ctx, SweepInput{
		Targets: []string{"192.0.2.64/30"}, IPCount: 4,
		PortSpec: "443", PortCount: 1,
		ExecutorScope: domain.ExecutorScopeServer,
		RequestID:     &reqID,
	})
	if err != nil {
		t.Fatalf("create sweep: %v", err)
	}
	chunkID := createTestScanJob(t, st, reqID, []string{"192.0.2.64"})
	if _, err := pool.Exec(`
		UPDATE scan_jobs SET sweep_id = $1::uuid, sweep_seq = 0, origin = 'sweep', dedup_hash = NULL
		 WHERE id = $2::uuid`, sw.ID, chunkID); err != nil {
		t.Fatalf("mark chunk: %v", err)
	}
	sensorJobID := createTestScanJob(t, st, reqID, []string{"192.0.2.65"})
	if _, err := pool.Exec(`
		UPDATE scan_jobs SET executor_type = 'sensor', status = 'running', dedup_hash = NULL
		 WHERE id = $1::uuid`, sensorJobID); err != nil {
		t.Fatalf("mark sensor running: %v", err)
	}
	t.Cleanup(func() {
		_, _ = pool.Exec(`DELETE FROM scan_jobs WHERE id IN ($1::uuid, $2::uuid)`, chunkID, sensorJobID)
		_, _ = pool.Exec(`DELETE FROM scan_sweeps WHERE id = $1::uuid`, sw.ID)
		cleanupAuditByRequestID(pool, reqID)
	})

	if _, err := st.CancelScanJob(ctx, chunkID, ScanCancelInput{RequestID: &reqID}); !errors.Is(err, ErrScanJobSweepChunk) {
		t.Errorf("cancel sweep chunk err = %v, want ErrScanJobSweepChunk", err)
	}
	// 센서가 실행 중인 Job은 취소 범위 외다 — "취소됐는데 계속 도는" 상태 금지.
	if _, err := st.CancelScanJob(ctx, sensorJobID, ScanCancelInput{RequestID: &reqID}); !errors.Is(err, ErrScanJobState) {
		t.Errorf("cancel sensor running err = %v, want ErrScanJobState", err)
	}
}

// TestMarkScanFinishedDoesNotOverrideCanceled는 취소와 정상 종료의 경합에서
// 종료 직전 goroutine이 canceled를 completed로 덮어쓰지 않는지 고정한다.
func TestMarkScanFinishedDoesNotOverrideCanceled(t *testing.T) {
	pool := testPool(t)
	st := New(pool)
	ctx := context.Background()
	reqID := "test-req-scan-finish-guard"

	id := createTestScanJob(t, st, reqID, []string{"192.0.2.40"})
	t.Cleanup(func() {
		_, _ = pool.Exec(`DELETE FROM scan_jobs WHERE id = $1::uuid`, id)
		cleanupAuditByRequestID(pool, reqID)
	})
	if err := st.MarkScanRunning(ctx, id, 1); err != nil {
		t.Fatalf("mark running: %v", err)
	}
	if _, err := st.CancelScanJob(ctx, id, ScanCancelInput{RequestID: &reqID}); err != nil {
		t.Fatalf("cancel running server job: %v", err)
	}

	// 경합의 패자 — no-op이어야 하고 오류도 아니어야 한다.
	if err := st.MarkScanFinished(ctx, id, ScanStatusCompleted, nil); err != nil {
		t.Fatalf("finish after cancel: %v", err)
	}
	job, err := st.GetScanJob(ctx, id)
	if err != nil {
		t.Fatalf("get job: %v", err)
	}
	if job.Status != ScanStatusCanceled {
		t.Errorf("status = %q, want canceled (finish must not override)", job.Status)
	}
	// 종료 감사(phase=completed)도 남지 않아야 한다 — 취소 감사 1건뿐.
	var finishAudits int
	if err := pool.QueryRow(`
		SELECT count(*) FROM audit_events
		 WHERE event_type = $1 AND target_id = $2
		   AND after_value::jsonb ->> 'phase' = 'completed'`,
		EventDiscoveryScan, id).Scan(&finishAudits); err != nil {
		t.Fatalf("count finish audits: %v", err)
	}
	if finishAudits != 0 {
		t.Errorf("finish audit events = %d, want 0", finishAudits)
	}
}
