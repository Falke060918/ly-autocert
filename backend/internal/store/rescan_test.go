package store

// 재스캔 모니터(0015 endpoint_monitors)의 관측 반영 규약을 고정한다.
// 픽스처는 전부 테스트 실행 시점에 생성한다 (보안 하드 규칙 4).

import (
	"context"
	"crypto/x509"
	"database/sql"
	"errors"
	"testing"
	"time"

	"github.com/Falke060918/INHERIT_AutoCert/backend/internal/certparse"
)

// fingerprintOf는 픽스처 인증서의 저장 지문(소문자 hex 64자)이다.
func fingerprintOf(t *testing.T, cert *x509.Certificate) string {
	t.Helper()
	return certparse.FingerprintSHA256(cert)
}

// newMonitorScanJob은 관측을 붙일 스캔 Job을 만든다(카운터 갱신 대상).
func newMonitorScanJob(t *testing.T, st *Store, pool *sql.DB, ip string, ports []int, reqID string) string {
	t.Helper()
	jobID, err := st.CreateScanJob(context.Background(), ScanInput{
		Targets: []string{ip}, Ports: ports, TimeoutMs: 1000, Concurrency: 1,
		TotalEndpoints: len(ports), RequestID: &reqID,
	})
	if err != nil {
		t.Fatalf("create scan job: %v", err)
	}
	t.Cleanup(func() { cleanupAuditByTargetID(pool, jobID) })
	return jobID
}

// monitorOf는 (스코프, IP, 포트)의 모니터 행을 읽는다. 없으면 nil.
func monitorOf(t *testing.T, st *Store, pool *sql.DB, ip string, port int) *EndpointMonitor {
	t.Helper()
	var targetID string
	err := pool.QueryRow(
		`SELECT id::text FROM targets WHERE ip_address = $1::inet AND target_type = 'network' AND executor_scope = 'server'`,
		ip).Scan(&targetID)
	if errors.Is(err, sql.ErrNoRows) {
		return nil
	}
	if err != nil {
		t.Fatalf("lookup target: %v", err)
	}
	m, err := st.GetEndpointMonitor(context.Background(), targetID, port)
	if errors.Is(err, ErrEndpointMonitorNotFound) {
		return nil
	}
	if err != nil {
		t.Fatalf("get endpoint monitor: %v", err)
	}
	return m
}

// TestEndpointMonitorTracksObservations는 관측 훅의 4가지 경로를 한 번에 본다:
// 최초 관측 / 같은 지문 재관측(streak++) / 지문 변경(previous 승계·streak 리셋) /
// 도달 실패(misses++만, 지문·성공 시각 불변).
func TestEndpointMonitorTracksObservations(t *testing.T) {
	pool := testPool(t)
	st := New(pool)
	ctx := context.Background()

	const ip = "203.0.113.70" // 문서화용 예약 대역 — 실제 주소 아님
	const port = 8443
	reqID := "test-req-monitor-track"
	certA := newTestX509Leaf(t, "monitor-fixture-a.example.test")
	certB := newTestX509Leaf(t, "monitor-fixture-b.example.test")

	t.Cleanup(func() {
		_, _ = pool.Exec(`DELETE FROM certificates WHERE common_name LIKE 'monitor-fixture-%.example.test'`)
		_, _ = pool.Exec(`DELETE FROM targets WHERE ip_address = $1::inet AND target_type = 'network'`, ip)
		_, _ = pool.Exec(`DELETE FROM scan_jobs WHERE targets = ARRAY[$1::text]`, ip)
		cleanupAuditByRequestID(pool, reqID)
	})

	jobID := newMonitorScanJob(t, st, pool, ip, []int{port}, reqID)

	observe := func(chain []*x509.Certificate, reachable bool) {
		t.Helper()
		if err := st.RecordEndpointResult(ctx, jobID, EndpointObservation{
			IP: ip, Port: port, Reachable: reachable, TLSVersion: "TLS 1.3",
			Hostname: "monitor-fixture.example.test", Chain: chain,
		}); err != nil {
			t.Fatalf("record endpoint result: %v", err)
		}
	}

	fpA := fingerprintOf(t, certA)
	fpB := fingerprintOf(t, certB)

	observe([]*x509.Certificate{certA}, true)
	m := monitorOf(t, st, pool, ip, port)
	if m == nil {
		t.Fatal("최초 관측 후 모니터 행이 없다")
	}
	if m.CurrentFingerprint == nil || *m.CurrentFingerprint != fpA {
		t.Errorf("current_fingerprint = %v, want %s", m.CurrentFingerprint, fpA)
	}
	if m.PreviousFingerprint != nil {
		t.Errorf("previous_fingerprint = %v, want nil (최초 관측)", *m.PreviousFingerprint)
	}
	if m.CurrentStreak != 1 {
		t.Errorf("current_streak = %d, want 1", m.CurrentStreak)
	}
	if m.LastProbeAt == nil || m.LastSuccessAt == nil {
		t.Errorf("last_probe_at=%v last_success_at=%v, 둘 다 채워져야 한다", m.LastProbeAt, m.LastSuccessAt)
	}

	observe([]*x509.Certificate{certA}, true)
	m = monitorOf(t, st, pool, ip, port)
	if m.CurrentStreak != 2 {
		t.Errorf("같은 지문 재관측 current_streak = %d, want 2", m.CurrentStreak)
	}
	if m.PreviousFingerprint != nil {
		t.Errorf("같은 지문 재관측인데 previous_fingerprint = %v", *m.PreviousFingerprint)
	}

	observe([]*x509.Certificate{certB}, true)
	m = monitorOf(t, st, pool, ip, port)
	if m.CurrentFingerprint == nil || *m.CurrentFingerprint != fpB {
		t.Errorf("교체 후 current_fingerprint = %v, want %s", m.CurrentFingerprint, fpB)
	}
	if m.PreviousFingerprint == nil || *m.PreviousFingerprint != fpA {
		t.Errorf("교체 후 previous_fingerprint = %v, want %s", m.PreviousFingerprint, fpA)
	}
	if m.CurrentStreak != 1 {
		t.Errorf("교체 후 current_streak = %d, want 1", m.CurrentStreak)
	}

	successAt := *m.LastSuccessAt
	observe(nil, false)
	miss := monitorOf(t, st, pool, ip, port)
	if miss.ConsecutiveMisses != 1 {
		t.Errorf("도달 실패 consecutive_misses = %d, want 1", miss.ConsecutiveMisses)
	}
	if miss.CurrentFingerprint == nil || *miss.CurrentFingerprint != fpB {
		t.Errorf("도달 실패가 지문을 바꿨다: %v", miss.CurrentFingerprint)
	}
	if miss.CurrentStreak != 1 {
		t.Errorf("도달 실패가 streak을 바꿨다: %d", miss.CurrentStreak)
	}
	if !miss.LastSuccessAt.Equal(successAt) {
		t.Errorf("도달 실패가 last_success_at을 바꿨다: %v → %v", successAt, *miss.LastSuccessAt)
	}
	if !miss.LastProbeAt.After(*m.LastProbeAt) && !miss.LastProbeAt.Equal(*m.LastProbeAt) {
		t.Errorf("도달 실패인데 last_probe_at이 갱신되지 않았다: %v", miss.LastProbeAt)
	}

	observe([]*x509.Certificate{certB}, true)
	if back := monitorOf(t, st, pool, ip, port); back.ConsecutiveMisses != 0 {
		t.Errorf("재도달 후 consecutive_misses = %d, want 0", back.ConsecutiveMisses)
	}
}

// TestEndpointMonitorMissWithoutTargetIsSkipped는 한 번도 본 적 없는 엔드포인트의
// 도달 실패가 모니터·대상을 만들지 않는지 본다 — 스윕이 전 포트를 훑으면
// 도달 실패가 수만 건이라 여기서 행을 만들면 인벤토리가 오염된다.
func TestEndpointMonitorMissWithoutTargetIsSkipped(t *testing.T) {
	pool := testPool(t)
	st := New(pool)
	ctx := context.Background()

	const ip = "203.0.113.71" // 문서화용 예약 대역 — 실제 주소 아님
	reqID := "test-req-monitor-miss"
	t.Cleanup(func() {
		_, _ = pool.Exec(`DELETE FROM targets WHERE ip_address = $1::inet AND target_type = 'network'`, ip)
		_, _ = pool.Exec(`DELETE FROM scan_jobs WHERE targets = ARRAY[$1::text]`, ip)
		cleanupAuditByRequestID(pool, reqID)
	})

	jobID := newMonitorScanJob(t, st, pool, ip, []int{9443}, reqID)
	if err := st.RecordEndpointResult(ctx, jobID, EndpointObservation{
		IP: ip, Port: 9443, Reachable: false,
	}); err != nil {
		t.Fatalf("record endpoint result: %v", err)
	}

	var targets int
	if err := pool.QueryRow(
		`SELECT count(*) FROM targets WHERE ip_address = $1::inet AND target_type = 'network'`,
		ip).Scan(&targets); err != nil {
		t.Fatalf("count targets: %v", err)
	}
	if targets != 0 {
		t.Errorf("도달 실패가 대상 %d행을 만들었다, want 0", targets)
	}
}

// TestListDueEndpointMonitors는 재스캔 후보 선정 규약을 본다:
// 주기가 지난 것만, 비활성·은퇴는 제외, interval_seconds가 전역 기본값을 이긴다.
func TestListDueEndpointMonitors(t *testing.T) {
	pool := testPool(t)
	st := New(pool)
	ctx := context.Background()

	const ip = "203.0.113.72" // 문서화용 예약 대역 — 실제 주소 아님
	reqID := "test-req-monitor-due"
	cert := newTestX509Leaf(t, "monitor-due.example.test")
	ports := []int{8501, 8502, 8503, 8504}

	t.Cleanup(func() {
		_, _ = pool.Exec(`DELETE FROM certificates WHERE common_name = 'monitor-due.example.test'`)
		_, _ = pool.Exec(`DELETE FROM targets WHERE ip_address = $1::inet AND target_type = 'network'`, ip)
		_, _ = pool.Exec(`DELETE FROM scan_jobs WHERE targets = ARRAY[$1::text]`, ip)
		cleanupAuditByRequestID(pool, reqID)
	})

	jobID := newMonitorScanJob(t, st, pool, ip, ports, reqID)
	for _, p := range ports {
		if err := st.RecordEndpointResult(ctx, jobID, EndpointObservation{
			IP: ip, Port: p, Reachable: true, TLSVersion: "TLS 1.3",
			Chain: []*x509.Certificate{cert},
		}); err != nil {
			t.Fatalf("record endpoint result: %v", err)
		}
	}

	// 방금 관측했으므로 1시간 주기로는 아무것도 due가 아니다.
	due, err := st.ListDueEndpointMonitors(ctx, time.Hour, 100)
	if err != nil {
		t.Fatalf("list due: %v", err)
	}
	for _, d := range due {
		if d.IP == ip {
			t.Fatalf("방금 프로브한 엔드포인트가 due로 나왔다: %s:%d", d.IP, d.Port)
		}
	}

	// 8501: 2시간 전 프로브 → due / 8502: 비활성 / 8503: 은퇴
	// 8504: 2시간 전 프로브지만 개별 주기 24h → due 아님
	exec := func(q string, args ...any) {
		t.Helper()
		if _, err := pool.Exec(q, args...); err != nil {
			t.Fatalf("fixture update: %v", err)
		}
	}
	exec(`UPDATE endpoint_monitors m SET last_probe_at = now() - interval '2 hour'
	        FROM targets t WHERE t.id = m.target_id AND t.ip_address = $1::inet`, ip)
	exec(`UPDATE endpoint_monitors m SET enabled = false
	        FROM targets t WHERE t.id = m.target_id AND t.ip_address = $1::inet AND m.port = 8502`, ip)
	exec(`UPDATE endpoint_monitors m SET retired_at = now()
	        FROM targets t WHERE t.id = m.target_id AND t.ip_address = $1::inet AND m.port = 8503`, ip)
	exec(`UPDATE endpoint_monitors m SET interval_seconds = 86400
	        FROM targets t WHERE t.id = m.target_id AND t.ip_address = $1::inet AND m.port = 8504`, ip)

	due, err = st.ListDueEndpointMonitors(ctx, time.Hour, 100)
	if err != nil {
		t.Fatalf("list due: %v", err)
	}
	got := map[int]bool{}
	for _, d := range due {
		if d.IP == ip {
			got[d.Port] = true
			if d.Scope != "server" {
				t.Errorf("scope = %q, want server", d.Scope)
			}
		}
	}
	if !got[8501] {
		t.Error("주기가 지난 모니터가 due에 없다 (8501)")
	}
	for _, p := range []int{8502, 8503, 8504} {
		if got[p] {
			t.Errorf("제외 대상이 due에 있다: %d", p)
		}
	}

	// limit은 상한으로 동작한다.
	limited, err := st.ListDueEndpointMonitors(ctx, time.Hour, 1)
	if err != nil {
		t.Fatalf("list due (limit): %v", err)
	}
	if len(limited) > 1 {
		t.Errorf("limit=1인데 %d건 반환", len(limited))
	}
}
