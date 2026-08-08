package store

// Phase 3a U3: 센서 위임 스캔 Job의 pull 리스 상태 머신·멱등 배치 반영 검증.
// - 리스 동시성: FOR UPDATE SKIP LOCKED로 한 Job은 한 번만 리스된다.
// - 전이 가드: leased→running→completed/failed, 터미널이면 오류.
// - Observation 배치: (job_id, ip, port) 아비터로 재전송이 중복 반영되지 않고,
//   대상 스코프 'sensor:<uuid>'가 서버 스코프 'server'와 분리된다.
// 픽스처는 전부 테스트 실행 시점에 생성한다 (보안 하드 규칙 4).

import (
	"context"
	"crypto/rand"
	"crypto/x509"
	"database/sql"
	"encoding/hex"
	"errors"
	"sync"
	"testing"
	"time"
)

// newTestSensor는 active 센서 행을 직접 삽입한다(등록 흐름은 다른 단위 소유).
// 토큰 해시 자리에는 무작위 64자 hex를 넣는다 — 실제 토큰이 아니다.
func newTestSensor(t *testing.T, pool *sql.DB, name string) string {
	t.Helper()
	buf := make([]byte, 32)
	if _, err := rand.Read(buf); err != nil {
		t.Fatalf("random: %v", err)
	}
	var id string
	err := pool.QueryRow(`
		INSERT INTO sensors (name, status, enrollment_token_hash, token_expires_at, registered_at)
		VALUES ($1, 'active', $2, now() + interval '1 hour', now())
		RETURNING id`,
		name, hex.EncodeToString(buf)).Scan(&id)
	if err != nil {
		t.Fatalf("insert test sensor: %v", err)
	}
	t.Cleanup(func() {
		_, _ = pool.Exec(`DELETE FROM scan_jobs WHERE sensor_id = $1::uuid`, id)
		_, _ = pool.Exec(`DELETE FROM sensors WHERE id = $1::uuid`, id)
	})
	return id
}

// cleanupAuditByTargetID는 request_id 없는 종료·회수 감사 이벤트를 정리한다
// (테스트 전용 DB에서만 허용 — cleanupAuditByRequestID와 동일 규약).
func cleanupAuditByTargetID(pool *sql.DB, targetID string) {
	_, _ = pool.Exec(`ALTER TABLE audit_events DISABLE TRIGGER USER`)
	_, _ = pool.Exec(`DELETE FROM audit_events WHERE target_id = $1`, targetID)
	_, _ = pool.Exec(`ALTER TABLE audit_events ENABLE TRIGGER USER`)
}

// newSensorScanJob은 센서 위임 pending Job을 만든다.
func newSensorScanJob(t *testing.T, st *Store, pool *sql.DB, sensorID, target string, reqID string) string {
	t.Helper()
	jobID, err := st.CreateScanJob(context.Background(), ScanInput{
		Targets: []string{target}, Ports: []int{8443}, TimeoutMs: 1000, Concurrency: 5,
		SensorID: &sensorID, RequestID: &reqID,
	})
	if err != nil {
		t.Fatalf("create sensor scan job: %v", err)
	}
	t.Cleanup(func() {
		cleanupAuditByRequestID(pool, reqID)
		cleanupAuditByTargetID(pool, jobID)
	})
	return jobID
}

func TestCreateScanJobSensorStaysPending(t *testing.T) {
	pool := testPool(t)
	st := New(pool)
	sensorID := newTestSensor(t, pool, "sensor-fixture-pending")
	jobID := newSensorScanJob(t, st, pool, sensorID, "192.0.2.10", "test-req-sensor-pending")

	j, err := st.GetScanJob(context.Background(), jobID)
	if err != nil {
		t.Fatalf("get scan job: %v", err)
	}
	if j.Status != ScanStatusPending {
		t.Errorf("status = %q, want pending", j.Status)
	}
	if j.ExecutorType != "sensor" {
		t.Errorf("executor_type = %q, want sensor", j.ExecutorType)
	}
	if j.SensorID == nil || *j.SensorID != sensorID {
		t.Errorf("sensor_id = %v, want %s", j.SensorID, sensorID)
	}
}

func TestLeaseNextSensorJobConcurrent(t *testing.T) {
	pool := testPool(t)
	st := New(pool)
	ctx := context.Background()
	sensorID := newTestSensor(t, pool, "sensor-fixture-lease")

	jobIDs := make([]string, 3)
	for i := range jobIDs {
		jobIDs[i] = newSensorScanJob(t, st, pool, sensorID, "192.0.2.20", "test-req-sensor-lease")
		// FIFO 검증을 위해 생성 시각을 강제로 벌린다(같은 마이크로초 동률 방지).
		if _, err := pool.Exec(
			`UPDATE scan_jobs SET created_at = now() - make_interval(secs => $2) WHERE id = $1::uuid`,
			jobIDs[i], float64(len(jobIDs)-i)); err != nil {
			t.Fatalf("set created_at: %v", err)
		}
	}

	const workers = 8
	var wg sync.WaitGroup
	leases := make(chan *SensorLease, workers)
	misses := make(chan error, workers)
	for range workers {
		wg.Add(1)
		go func() {
			defer wg.Done()
			l, err := st.LeaseNextSensorJob(ctx, sensorID)
			if err != nil {
				misses <- err
				return
			}
			leases <- l
		}()
	}
	wg.Wait()
	close(leases)
	close(misses)

	got := map[string]bool{}
	for l := range leases {
		if got[l.JobID] {
			t.Errorf("job %s leased twice", l.JobID)
		}
		got[l.JobID] = true
		if l.JobType != "scan" {
			t.Errorf("job_type = %q, want scan", l.JobType)
		}
		if !l.LeaseExpiresAt.After(time.Now()) {
			t.Errorf("lease_expires_at %v not in the future", l.LeaseExpiresAt)
		}
	}
	if len(got) != len(jobIDs) {
		t.Errorf("leased %d jobs, want %d", len(got), len(jobIDs))
	}
	for _, id := range jobIDs {
		if !got[id] {
			t.Errorf("job %s never leased", id)
		}
	}
	missCount := 0
	for err := range misses {
		if !errors.Is(err, ErrNoPendingSensorJob) {
			t.Errorf("unexpected lease error: %v", err)
		}
		missCount++
	}
	if missCount != workers-len(jobIDs) {
		t.Errorf("misses = %d, want %d", missCount, workers-len(jobIDs))
	}

	// 소진 후 재폴링은 ErrNoPendingSensorJob이다.
	if _, err := st.LeaseNextSensorJob(ctx, sensorID); !errors.Is(err, ErrNoPendingSensorJob) {
		t.Errorf("lease after drain = %v, want ErrNoPendingSensorJob", err)
	}
}

func TestLeaseNextSensorJobFIFO(t *testing.T) {
	pool := testPool(t)
	st := New(pool)
	ctx := context.Background()
	sensorID := newTestSensor(t, pool, "sensor-fixture-fifo")

	older := newSensorScanJob(t, st, pool, sensorID, "192.0.2.21", "test-req-sensor-fifo")
	newer := newSensorScanJob(t, st, pool, sensorID, "192.0.2.22", "test-req-sensor-fifo")
	if _, err := pool.Exec(
		`UPDATE scan_jobs SET created_at = now() - interval '10 seconds' WHERE id = $1::uuid`, older); err != nil {
		t.Fatalf("set created_at: %v", err)
	}

	first, err := st.LeaseNextSensorJob(ctx, sensorID)
	if err != nil {
		t.Fatalf("lease first: %v", err)
	}
	if first.JobID != older {
		t.Errorf("first lease = %s, want oldest %s", first.JobID, older)
	}
	second, err := st.LeaseNextSensorJob(ctx, sensorID)
	if err != nil {
		t.Fatalf("lease second: %v", err)
	}
	if second.JobID != newer {
		t.Errorf("second lease = %s, want %s", second.JobID, newer)
	}
}

func TestSensorJobTransitions(t *testing.T) {
	pool := testPool(t)
	st := New(pool)
	ctx := context.Background()
	sensorID := newTestSensor(t, pool, "sensor-fixture-transitions")
	otherSensor := newTestSensor(t, pool, "sensor-fixture-transitions-other")
	jobID := newSensorScanJob(t, st, pool, sensorID, "192.0.2.30", "test-req-sensor-transitions")

	// pending → running은 금지(리스를 거쳐야 한다).
	if err := st.MarkSensorJobRunning(ctx, jobID, sensorID, 1); !errors.Is(err, ErrSensorJobState) {
		t.Errorf("running before lease = %v, want ErrSensorJobState", err)
	}

	if _, err := st.LeaseNextSensorJob(ctx, sensorID); err != nil {
		t.Fatalf("lease: %v", err)
	}

	// 다른 센서의 전이 시도는 Job 부재로 처리한다.
	if err := st.MarkSensorJobRunning(ctx, jobID, otherSensor, 1); !errors.Is(err, ErrScanJobNotFound) {
		t.Errorf("running with wrong sensor = %v, want ErrScanJobNotFound", err)
	}

	// leased 상태의 하트비트는 금지(running에서만).
	if err := st.ExtendSensorJobLease(ctx, jobID, sensorID); !errors.Is(err, ErrSensorJobState) {
		t.Errorf("heartbeat while leased = %v, want ErrSensorJobState", err)
	}

	if err := st.MarkSensorJobRunning(ctx, jobID, sensorID, 1); err != nil {
		t.Fatalf("leased -> running: %v", err)
	}
	if err := st.MarkSensorJobRunning(ctx, jobID, sensorID, 1); !errors.Is(err, ErrSensorJobState) {
		t.Errorf("running twice = %v, want ErrSensorJobState", err)
	}

	// 하트비트가 리스를 연장한다.
	if _, err := pool.Exec(
		`UPDATE scan_jobs SET lease_expires_at = now() + interval '1 second' WHERE id = $1::uuid`, jobID); err != nil {
		t.Fatalf("shrink lease: %v", err)
	}
	if err := st.ExtendSensorJobLease(ctx, jobID, sensorID); err != nil {
		t.Fatalf("heartbeat: %v", err)
	}
	var extended bool
	if err := pool.QueryRow(
		`SELECT lease_expires_at > now() + interval '60 seconds' FROM scan_jobs WHERE id = $1::uuid`,
		jobID).Scan(&extended); err != nil {
		t.Fatalf("read lease: %v", err)
	}
	if !extended {
		t.Errorf("heartbeat did not extend lease_expires_at")
	}

	if err := st.MarkSensorJobFinished(ctx, jobID, sensorID, ScanStatusCompleted, nil); err != nil {
		t.Fatalf("running -> completed: %v", err)
	}

	// 터미널 이후의 모든 전이는 오류다.
	if err := st.ExtendSensorJobLease(ctx, jobID, sensorID); !errors.Is(err, ErrSensorJobState) {
		t.Errorf("heartbeat after completed = %v, want ErrSensorJobState", err)
	}
	reason := "late failure"
	if err := st.MarkSensorJobFinished(ctx, jobID, sensorID, ScanStatusFailed, &reason); !errors.Is(err, ErrSensorJobState) {
		t.Errorf("finish after completed = %v, want ErrSensorJobState", err)
	}
	if _, err := st.RecordObservationBatch(ctx, jobID, sensorID, nil); !errors.Is(err, ErrSensorJobState) {
		t.Errorf("batch after completed = %v, want ErrSensorJobState", err)
	}

	// 종결 상태 enum 밖의 값은 거부한다.
	if err := st.MarkSensorJobFinished(ctx, jobID, sensorID, ScanStatusRunning, nil); !errors.Is(err, ErrSensorJobState) {
		t.Errorf("finish with status running = %v, want ErrSensorJobState", err)
	}

	// leased에서 completed는 금지, failed는 허용(전개 단계 실패 보고).
	jobB := newSensorScanJob(t, st, pool, sensorID, "192.0.2.31", "test-req-sensor-transitions")
	if _, err := st.LeaseNextSensorJob(ctx, sensorID); err != nil {
		t.Fatalf("lease jobB: %v", err)
	}
	if err := st.MarkSensorJobFinished(ctx, jobB, sensorID, ScanStatusCompleted, nil); !errors.Is(err, ErrSensorJobState) {
		t.Errorf("leased -> completed = %v, want ErrSensorJobState", err)
	}
	expandErr := "invalid target"
	if err := st.MarkSensorJobFinished(ctx, jobB, sensorID, ScanStatusFailed, &expandErr); err != nil {
		t.Fatalf("leased -> failed: %v", err)
	}
	j, err := st.GetScanJob(ctx, jobB)
	if err != nil {
		t.Fatalf("get jobB: %v", err)
	}
	if j.Status != ScanStatusFailed || j.Error == nil || *j.Error != expandErr {
		t.Errorf("jobB = (%s, %v), want (failed, %q)", j.Status, j.Error, expandErr)
	}
}

func TestRecordObservationBatchIdempotent(t *testing.T) {
	pool := testPool(t)
	st := New(pool)
	ctx := context.Background()
	sensorID := newTestSensor(t, pool, "sensor-fixture-batch")

	const ip = "203.0.113.77" // 문서화용 예약 대역 — 실제 주소 아님
	jobID := newSensorScanJob(t, st, pool, sensorID, ip, "test-req-sensor-batch")
	serverReqID := "test-req-sensor-batch-server"

	t.Cleanup(func() {
		_, _ = pool.Exec(`DELETE FROM certificates WHERE common_name LIKE 'sensor-batch-%.example.test'`)
		_, _ = pool.Exec(`DELETE FROM targets WHERE ip_address = $1::inet AND target_type = 'network'`, ip)
		_, _ = pool.Exec(`DELETE FROM scan_jobs WHERE targets = ARRAY[$1::text]`, ip)
		cleanupAuditByRequestID(pool, serverReqID)
	})

	if _, err := st.LeaseNextSensorJob(ctx, sensorID); err != nil {
		t.Fatalf("lease: %v", err)
	}
	if err := st.MarkSensorJobRunning(ctx, jobID, sensorID, 2); err != nil {
		t.Fatalf("running: %v", err)
	}

	batch := []EndpointObservation{
		{
			IP: ip, Port: 8443, Reachable: true, TLSVersion: "TLS 1.3",
			Hostname: "sensor-batch.example.test",
			Chain:    []*x509.Certificate{newTestX509Leaf(t, "sensor-batch-leaf.example.test")},
		},
		{IP: ip, Port: 8444, Reachable: false},
	}

	readCounters := func() (scanned, reachable, found, newCerts int) {
		t.Helper()
		if err := pool.QueryRow(`
			SELECT scanned_endpoints, reachable_endpoints, certificates_found, certificates_new
			  FROM scan_jobs WHERE id = $1::uuid`,
			jobID).Scan(&scanned, &reachable, &found, &newCerts); err != nil {
			t.Fatalf("read counters: %v", err)
		}
		return scanned, reachable, found, newCerts
	}

	applied, err := st.RecordObservationBatch(ctx, jobID, sensorID, batch)
	if err != nil {
		t.Fatalf("record batch: %v", err)
	}
	if applied != 2 {
		t.Errorf("applied = %d, want 2", applied)
	}
	if sc, re, fo, nw := readCounters(); sc != 2 || re != 1 || fo != 1 || nw != 1 {
		t.Errorf("counters = (%d,%d,%d,%d), want (2,1,1,1)", sc, re, fo, nw)
	}

	// 같은 배치 재전송 — 아무것도 중복 반영되지 않는다 (00-spec 7.8 멱등).
	applied, err = st.RecordObservationBatch(ctx, jobID, sensorID, batch)
	if err != nil {
		t.Fatalf("record batch again: %v", err)
	}
	if applied != 0 {
		t.Errorf("re-applied = %d, want 0", applied)
	}
	if sc, re, fo, nw := readCounters(); sc != 2 || re != 1 || fo != 1 || nw != 1 {
		t.Errorf("counters after resend = (%d,%d,%d,%d), want (2,1,1,1)", sc, re, fo, nw)
	}

	// 대상 스코프는 'sensor:<uuid>'다.
	var scope string
	if err := pool.QueryRow(
		`SELECT executor_scope FROM targets WHERE ip_address = $1::inet AND target_type = 'network'`,
		ip).Scan(&scope); err != nil {
		t.Fatalf("read scope: %v", err)
	}
	if want := "sensor:" + sensorID; scope != want {
		t.Errorf("executor_scope = %q, want %q", scope, want)
	}

	// 서버 직접 스캔이 같은 IP를 기록하면 별도 스코프 행이 생긴다(사설 IP 겹침 분리).
	serverJobID, err := st.CreateScanJob(ctx, ScanInput{
		Targets: []string{ip}, Ports: []int{8443}, TimeoutMs: 1000, Concurrency: 1,
		RequestID: &serverReqID,
	})
	if err != nil {
		t.Fatalf("create server job: %v", err)
	}
	if err := st.RecordEndpointResult(ctx, serverJobID, EndpointObservation{
		IP: ip, Port: 8443, Reachable: true, TLSVersion: "TLS 1.3",
		Chain: []*x509.Certificate{newTestX509Leaf(t, "sensor-batch-server.example.test")},
	}); err != nil {
		t.Fatalf("server record: %v", err)
	}
	var targetRows int
	if err := pool.QueryRow(
		`SELECT count(*) FROM targets WHERE ip_address = $1::inet AND target_type = 'network'`,
		ip).Scan(&targetRows); err != nil {
		t.Fatalf("count targets: %v", err)
	}
	if targetRows != 2 {
		t.Errorf("targets for %s = %d, want 2 (scope 분리)", ip, targetRows)
	}

	// 다른 센서 소유 Job으로는 반영할 수 없다.
	other := newTestSensor(t, pool, "sensor-fixture-batch-other")
	if _, err := st.RecordObservationBatch(ctx, jobID, other, batch); !errors.Is(err, ErrScanJobNotFound) {
		t.Errorf("batch with wrong sensor = %v, want ErrScanJobNotFound", err)
	}
}

func TestReapExpiredSensorLeases(t *testing.T) {
	pool := testPool(t)
	st := New(pool)
	ctx := context.Background()
	sensorID := newTestSensor(t, pool, "sensor-fixture-reap")

	expired := newSensorScanJob(t, st, pool, sensorID, "192.0.2.40", "test-req-sensor-reap")
	alive := newSensorScanJob(t, st, pool, sensorID, "192.0.2.41", "test-req-sensor-reap")
	for range 2 {
		if _, err := st.LeaseNextSensorJob(ctx, sensorID); err != nil {
			t.Fatalf("lease: %v", err)
		}
	}
	if _, err := pool.Exec(
		`UPDATE scan_jobs SET lease_expires_at = now() - interval '1 second' WHERE id = $1::uuid`, expired); err != nil {
		t.Fatalf("expire lease: %v", err)
	}

	n, err := st.ReapExpiredSensorLeases(ctx)
	if err != nil {
		t.Fatalf("reap: %v", err)
	}
	if n != 1 {
		t.Errorf("reaped = %d, want 1", n)
	}

	j, err := st.GetScanJob(ctx, expired)
	if err != nil {
		t.Fatalf("get expired: %v", err)
	}
	if j.Status != ScanStatusFailed || j.Error == nil || *j.Error != "lease expired" {
		t.Errorf("expired job = (%s, %v), want (failed, \"lease expired\")", j.Status, j.Error)
	}
	if a, err := st.GetScanJob(ctx, alive); err != nil || a.Status != "leased" {
		t.Errorf("alive job = (%v, %v), want leased", a, err)
	}

	// 회수는 종료 감사 이벤트를 남긴다 (보안 하드 규칙 5).
	var audits int
	if err := pool.QueryRow(
		`SELECT count(*) FROM audit_events WHERE target_id = $1 AND after_value->>'status' = 'failed'`,
		expired).Scan(&audits); err != nil {
		t.Fatalf("count audits: %v", err)
	}
	if audits != 1 {
		t.Errorf("reap audit events = %d, want 1", audits)
	}
}
