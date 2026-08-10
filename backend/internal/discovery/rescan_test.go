package discovery

// 주기 재스캔 스케줄러 테스트 (rescan.go).
// 고정하는 규약은 다섯이다: due 선정과 만료 임박 가속 티어 / 포트별 그룹 발주가
// 데카르트 곱을 만들지 않는 것 / 센서 스코프 분기 / claim의 중복 발주 차단 /
// 교체 전이 단계 호출.
//
// 픽스처는 전부 테스트 실행 시점에 생성한다 (보안 하드 규칙 4).
// DB가 필요한 테스트는 TEST_DATABASE_URL 미설정 시 스킵한다.

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"crypto/x509/pkix"
	"database/sql"
	"encoding/hex"
	"math/big"
	"testing"
	"time"

	"github.com/Falke060918/INHERIT_AutoCert/backend/internal/store"
)

// ---------------------------------------------------------------------------
// 픽스처
// ---------------------------------------------------------------------------

// rescanFixture는 재스캔 대상 엔드포인트를 만들고 뒷정리를 건다.
type rescanFixture struct {
	t     *testing.T
	st    *store.Store
	pool  *sql.DB
	ctx   context.Context
	ips   []string
	jobID string
}

// newRescanFixture는 관측 주입용 스캔 Job 1건과 정리 훅을 만든다.
// cnPrefix로 만든 인증서와 ips의 대상 행은 테스트 종료 시 지운다.
func newRescanFixture(t *testing.T, cnPrefix string, ips []string) *rescanFixture {
	t.Helper()
	st, pool := testSweepStore(t)
	ctx := context.Background()

	jobID, err := st.CreateScanJob(ctx, store.ScanInput{
		Targets: ips, Ports: []int{1}, TimeoutMs: 1000, Concurrency: 1, TotalEndpoints: len(ips),
	})
	if err != nil {
		t.Fatalf("create seed scan job: %v", err)
	}

	f := &rescanFixture{t: t, st: st, pool: pool, ctx: ctx, ips: ips, jobID: jobID}
	t.Cleanup(func() {
		f.dropAudit(`DELETE FROM audit_events WHERE target_id IN (
			SELECT id::text FROM scan_jobs WHERE targets && $1::text[] OR id = $2::uuid)`,
			pgTextArray(ips), jobID)
		f.dropAudit(`DELETE FROM audit_events WHERE target_id IN (
			SELECT id::text FROM certificates WHERE common_name LIKE $1)`, cnPrefix+"%")
		for _, ip := range ips {
			_, _ = pool.Exec(`DELETE FROM scan_jobs WHERE $1 = ANY (targets)`, ip)
			_, _ = pool.Exec(`DELETE FROM targets WHERE ip_address = $1::inet AND target_type = 'network'`, ip)
		}
		_, _ = pool.Exec(`DELETE FROM scan_jobs WHERE id = $1::uuid`, jobID)
		_, _ = pool.Exec(`DELETE FROM certificates WHERE common_name LIKE $1`, cnPrefix+"%")
	})
	return f
}

// dropAudit은 append-only 트리거(0007)를 잠시 내리고 감사 행을 지운다.
// 테스트 전용 DB에서만 허용되는 조작이다 (sweep_test와 같은 방식).
func (f *rescanFixture) dropAudit(query string, args ...any) {
	_, _ = f.pool.Exec(`ALTER TABLE audit_events DISABLE TRIGGER USER`)
	_, _ = f.pool.Exec(query, args...)
	_, _ = f.pool.Exec(`ALTER TABLE audit_events ENABLE TRIGGER USER`)
}

// observe는 (IP, 포트)에서 인증서를 관측한 것으로 기록한다 —
// targets·certificates·certificate_locations·endpoint_monitors가 한 번에 생긴다.
func (f *rescanFixture) observe(ip string, port int, cert *x509.Certificate) {
	f.t.Helper()
	if err := f.st.RecordEndpointResult(f.ctx, f.jobID, store.EndpointObservation{
		IP: ip, Port: port, Reachable: true, TLSVersion: "TLS 1.3",
		Chain: []*x509.Certificate{cert},
	}); err != nil {
		f.t.Fatalf("record endpoint result: %v", err)
	}
}

// age는 모니터의 last_probe_at을 과거로 밀어 due 상태를 만든다.
func (f *rescanFixture) age(ip string, port int, d time.Duration) {
	f.t.Helper()
	res, err := f.pool.Exec(`
		UPDATE endpoint_monitors m
		   SET last_probe_at = now() - make_interval(secs => $3)
		  FROM targets t
		 WHERE t.id = m.target_id AND t.ip_address = $1::inet AND m.port = $2`,
		ip, port, int(d/time.Second))
	if err != nil {
		f.t.Fatalf("age monitor: %v", err)
	}
	if n, _ := res.RowsAffected(); n != 1 {
		f.t.Fatalf("age monitor: %d rows updated, want 1", n)
	}
}

// rescanJobs는 이번 테스트가 만든 재스캔 Job을 발주 순서로 읽는다.
func (f *rescanFixture) rescanJobs(ips []string) []rescanJobRow {
	f.t.Helper()
	rows, err := f.pool.Query(`
		SELECT array_to_string(targets, ','), array_to_string(ports, ','), status,
		       executor_type, coalesce(sensor_id::text, ''), total_endpoints, origin
		  FROM scan_jobs
		 WHERE origin = 'rescan' AND targets && $1::text[]
		 ORDER BY created_at, array_to_string(ports, ',')`, pgTextArray(ips))
	if err != nil {
		f.t.Fatalf("query rescan jobs: %v", err)
	}
	defer rows.Close() //nolint:errcheck
	var out []rescanJobRow
	for rows.Next() {
		var j rescanJobRow
		if err := rows.Scan(&j.Targets, &j.Ports, &j.Status, &j.ExecutorType,
			&j.SensorID, &j.Total, &j.Origin); err != nil {
			f.t.Fatalf("scan rescan job: %v", err)
		}
		out = append(out, j)
	}
	if err := rows.Err(); err != nil {
		f.t.Fatalf("iterate rescan jobs: %v", err)
	}
	return out
}

type rescanJobRow struct {
	Targets      string
	Ports        string
	Status       string
	ExecutorType string
	SensorID     string
	Total        int
	Origin       string
}

// pgTextArray는 IP 목록을 text[] 리터럴로 만든다(테스트 조회 전용).
func pgTextArray(ips []string) string {
	out := "{"
	for i, ip := range ips {
		if i > 0 {
			out += ","
		}
		out += ip
	}
	return out + "}"
}

// newRescanLeaf는 만료 시각을 지정한 자기서명 리프 픽스처다.
// 티어 판정은 저장된 status가 아니라 not_after로 하므로 여기서 그 값을 정한다.
func newRescanLeaf(t *testing.T, cn string, validFor time.Duration) *x509.Certificate {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatalf("generate key: %v", err)
	}
	tmpl := &x509.Certificate{
		SerialNumber: big.NewInt(time.Now().UnixNano()),
		Subject:      pkix.Name{CommonName: cn},
		NotBefore:    time.Now().Add(-time.Hour),
		NotAfter:     time.Now().Add(validFor),
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

// testRescanOptions는 테스트용 설정이다(빠른 레이트 — 지연은 리미터 테스트가 본다).
func testRescanOptions() RescanOptions {
	return RescanOptions{
		Enabled:        true,
		Interval:       12 * time.Hour,
		RatePPS:        MaxRatePPS,
		Concurrency:    4,
		BatchIPs:       64,
		ReplaceConfirm: 2,
		RetireMisses:   30,
	}
}

// ---------------------------------------------------------------------------
// 그룹핑 (DB 불필요)
// ---------------------------------------------------------------------------

// TestGroupRescanBatchesKeepsOnePortPerBatch는 발주 단위가 (스코프, 포트)임을 고정한다.
// scan_jobs는 targets × ports 데카르트 곱이라 한 배치에 포트를 여러 개 실으면
// 요청하지 않은 엔드포인트까지 스캔된다 — 낭비가 아니라 범위 위반이다 (설계 4.1).
func TestGroupRescanBatchesKeepsOnePortPerBatch(t *testing.T) {
	due := []store.DueEndpointMonitor{
		{Scope: "sensor:11111111-1111-1111-1111-111111111111", IP: "198.51.100.9", Port: 443},
		{Scope: "server", IP: "198.51.100.1", Port: 443},
		{Scope: "server", IP: "198.51.100.2", Port: 443},
		{Scope: "server", IP: "198.51.100.2", Port: 8443},
	}
	// store가 (scope, port, ip)로 정렬해 주는 순서를 그대로 흉내낸다.
	sorted := []store.DueEndpointMonitor{due[1], due[2], due[3], due[0]}

	got := groupRescanBatches(sorted)
	if len(got) != 3 {
		t.Fatalf("batches = %d, want 3 (server:443, server:8443, sensor:443)", len(got))
	}
	if got[0].scope != "server" || got[0].port != 443 || len(got[0].ips) != 2 {
		t.Errorf("batch0 = %+v, want server/443/2 IP", got[0])
	}
	if got[1].port != 8443 || len(got[1].ips) != 1 {
		t.Errorf("batch1 = %+v, want 8443/1 IP", got[1])
	}
	if got[2].scope != due[0].Scope || len(got[2].ips) != 1 {
		t.Errorf("batch2 = %+v, want 센서 스코프 분리", got[2])
	}
}

// ---------------------------------------------------------------------------
// due 선정 · 발주 (DB 필요)
// ---------------------------------------------------------------------------

// TestRescanDispatchesOneJobPerPort는 tick 1회의 발주 형태를 본다:
// 같은 포트의 IP는 한 Job으로 묶이고, 포트가 다르면 Job이 갈린다.
// 각 Job의 ports는 정확히 1개이고 total_endpoints는 IP 수와 같아야 한다
// (데카르트 곱이 생기면 이 값이 IP 수 × 포트 수로 부푼다).
func TestRescanDispatchesOneJobPerPort(t *testing.T) {
	ips := []string{"198.51.100.11", "198.51.100.12"}
	f := newRescanFixture(t, "rescan-group-", ips)
	stubScan(t, func(ep Endpoint) Observation { return Observation{Endpoint: ep} })

	cert := newRescanLeaf(t, "rescan-group-a.example.test", 365*24*time.Hour)
	f.observe(ips[0], 443, cert)
	f.observe(ips[1], 443, cert)
	f.observe(ips[1], 8443, cert)
	for _, ip := range ips {
		f.age(ip, 443, 24*time.Hour)
	}
	f.age(ips[1], 8443, 24*time.Hour)

	m := NewManager(f.st).UseRescanOptions(testRescanOptions())
	sum, err := m.RunRescanOnce(f.ctx)
	if err != nil {
		t.Fatalf("run rescan once: %v", err)
	}
	if sum.Claimed != 3 {
		t.Fatalf("claimed = %d, want 3", sum.Claimed)
	}
	if sum.ServerJobs != 2 || sum.SensorJobs != 0 {
		t.Fatalf("jobs = (server %d, sensor %d), want (2, 0)", sum.ServerJobs, sum.SensorJobs)
	}

	jobs := f.rescanJobs(ips)
	if len(jobs) != 2 {
		t.Fatalf("rescan jobs = %d, want 2", len(jobs))
	}
	for _, j := range jobs {
		if j.Origin != "rescan" {
			t.Errorf("origin = %q, want rescan", j.Origin)
		}
		if j.Ports != "443" && j.Ports != "8443" {
			t.Errorf("ports = %q, want 단일 포트", j.Ports)
		}
		if j.ExecutorType != "server" {
			t.Errorf("executor_type = %q, want server", j.ExecutorType)
		}
	}
	if jobs[0].Ports != "443" || jobs[0].Total != 2 || jobs[0].Targets != ips[0]+","+ips[1] {
		t.Errorf("443 job = %+v, want IP 2개 · total 2", jobs[0])
	}
	if jobs[1].Ports != "8443" || jobs[1].Total != 1 {
		t.Errorf("8443 job = %+v, want IP 1개 · total 1", jobs[1])
	}
}

// TestRescanClaimBlocksDoubleDispatch는 claim(발주 시점 last_probe_at 갱신)이
// 같은 엔드포인트를 두 Job에 넣지 않는지 본다 (설계 7.2).
func TestRescanClaimBlocksDoubleDispatch(t *testing.T) {
	ips := []string{"198.51.100.21"}
	f := newRescanFixture(t, "rescan-claim-", ips)
	stubScan(t, func(ep Endpoint) Observation { return Observation{Endpoint: ep} })

	f.observe(ips[0], 443, newRescanLeaf(t, "rescan-claim-a.example.test", 365*24*time.Hour))
	f.age(ips[0], 443, 24*time.Hour)

	m := NewManager(f.st).UseRescanOptions(testRescanOptions())
	first, err := m.RunRescanOnce(f.ctx)
	if err != nil {
		t.Fatalf("first tick: %v", err)
	}
	if first.Claimed != 1 || first.ServerJobs != 1 {
		t.Fatalf("first tick = %+v, want claimed 1 / job 1", first)
	}

	second, err := m.RunRescanOnce(f.ctx)
	if err != nil {
		t.Fatalf("second tick: %v", err)
	}
	if second.Claimed != 0 || second.ServerJobs != 0 {
		t.Fatalf("second tick = %+v, want 발주 없음 (claim이 막아야 한다)", second)
	}
	if got := len(f.rescanJobs(ips)); got != 1 {
		t.Errorf("rescan jobs = %d, want 1", got)
	}
}

// TestRescanAcceleratesNearExpiry는 만료 임박 가속 2티어를 고정한다 (설계 3.1).
// 잔여 300일짜리는 기본 간격(12h) 전에는 due가 아니고, 잔여 20일(→6h)·
// 잔여 3일(→1h)짜리는 7시간 전 관측만으로 due여야 한다.
func TestRescanAcceleratesNearExpiry(t *testing.T) {
	const (
		farIP    = "198.51.100.31"
		nearIP   = "198.51.100.32"
		urgentIP = "198.51.100.33"
	)
	ips := []string{farIP, nearIP, urgentIP}
	f := newRescanFixture(t, "rescan-tier-", ips)
	stubScan(t, func(ep Endpoint) Observation { return Observation{Endpoint: ep} })

	f.observe(farIP, 443, newRescanLeaf(t, "rescan-tier-far.example.test", 300*24*time.Hour))
	f.observe(nearIP, 443, newRescanLeaf(t, "rescan-tier-near.example.test", 20*24*time.Hour))
	f.observe(urgentIP, 443, newRescanLeaf(t, "rescan-tier-urgent.example.test", 3*24*time.Hour))
	for _, ip := range ips {
		f.age(ip, 443, 7*time.Hour) // 기본 12h에는 못 미치고 가속 6h·1h는 넘긴다.
	}

	due, locked, err := f.st.ClaimDueEndpointMonitors(f.ctx, testRescanOptions().intervals(), 64)
	if err != nil || !locked {
		t.Fatalf("claim due: err=%v locked=%v", err, locked)
	}
	got := map[string]bool{}
	for _, d := range due {
		got[d.IP] = true
	}
	if got[farIP] {
		t.Error("잔여 300일 엔드포인트가 7시간 만에 due가 됐다 — 기본 간격이 무시됐다")
	}
	if !got[nearIP] {
		t.Error("잔여 20일 엔드포인트가 due가 아니다 — 30일 이하 6h 가속이 걸리지 않았다")
	}
	if !got[urgentIP] {
		t.Error("잔여 3일 엔드포인트가 due가 아니다 — 7일 이하 1h 가속이 걸리지 않았다")
	}
}

// TestRescanSensorScopeDispatchesPendingJob은 센서 스코프 분기를 본다:
// 활성 센서 스코프는 executor_type='sensor'인 pending Job이 되고(센서가 pull 리스로
// 가져간다 — 프로토콜 변경 0), 비활성 센서 스코프는 아예 발주하지 않는다 (설계 5.2).
func TestRescanSensorScopeDispatchesPendingJob(t *testing.T) {
	const (
		activeIP   = "198.51.100.41"
		disabledIP = "198.51.100.42"
	)
	ips := []string{activeIP, disabledIP}
	f := newRescanFixture(t, "rescan-sensor-", ips)

	activeID := f.newSensor("active")
	disabledID := f.newSensor("disabled")
	f.seedSensorMonitor("sensor:"+activeID, activeIP, 443)
	f.seedSensorMonitor("sensor:"+disabledID, disabledIP, 443)

	m := NewManager(f.st).UseRescanOptions(testRescanOptions())
	sum, err := m.RunRescanOnce(f.ctx)
	if err != nil {
		t.Fatalf("run rescan once: %v", err)
	}
	if sum.SensorJobs != 1 || sum.ServerJobs != 0 {
		t.Fatalf("jobs = (server %d, sensor %d), want (0, 1)", sum.ServerJobs, sum.SensorJobs)
	}

	jobs := f.rescanJobs(ips)
	if len(jobs) != 1 {
		t.Fatalf("rescan jobs = %d, want 1 (비활성 센서 스코프는 발주하지 않는다)", len(jobs))
	}
	j := jobs[0]
	if j.Targets != activeIP {
		t.Errorf("targets = %q, want %q", j.Targets, activeIP)
	}
	if j.ExecutorType != "sensor" || j.SensorID != activeID {
		t.Errorf("executor = (%s, %s), want (sensor, %s)", j.ExecutorType, j.SensorID, activeID)
	}
	if j.Status != "pending" {
		t.Errorf("status = %q, want pending (센서가 리스로 가져간다)", j.Status)
	}
}

// newSensor는 지정 상태의 센서 행을 만든다(등록 절차 없이 스코프 판정에만 쓴다).
func (f *rescanFixture) newSensor(status string) string {
	f.t.Helper()
	var id string
	raw := make([]byte, 32)
	if _, err := rand.Read(raw); err != nil {
		f.t.Fatalf("random: %v", err)
	}
	if err := f.pool.QueryRow(`
		INSERT INTO sensors (name, status, enrollment_token_hash, token_expires_at)
		VALUES ('rescan-test-sensor', $1, $2, now() + interval '1 day')
		RETURNING id::text`, status, hex.EncodeToString(raw)).Scan(&id); err != nil {
		f.t.Fatalf("insert sensor: %v", err)
	}
	f.t.Cleanup(func() { _, _ = f.pool.Exec(`DELETE FROM sensors WHERE id = $1::uuid`, id) })
	return id
}

// seedSensorMonitor는 센서 스코프의 대상 + due 상태 모니터를 직접 만든다.
// 인증서를 붙이지 않으므로 티어는 기본 간격이고, last_probe_at을 비워 즉시 due다.
func (f *rescanFixture) seedSensorMonitor(scope, ip string, port int) {
	f.t.Helper()
	var targetID string
	if err := f.pool.QueryRow(`
		INSERT INTO targets (name, target_type, ip_address, executor_scope, connection_status)
		VALUES ($1, 'network', $2::inet, $3, 'reachable')
		RETURNING id::text`, ip, ip, scope).Scan(&targetID); err != nil {
		f.t.Fatalf("insert sensor target: %v", err)
	}
	if _, err := f.pool.Exec(`
		INSERT INTO endpoint_monitors (target_id, port, first_seen_at)
		VALUES ($1::uuid, $2, now())`, targetID, port); err != nil {
		f.t.Fatalf("insert sensor monitor: %v", err)
	}
	f.t.Cleanup(func() { _, _ = f.pool.Exec(`DELETE FROM targets WHERE id = $1::uuid`, targetID) })
}

// TestRescanTickReconcilesReplacement는 tick의 3단계(교체 전이)가 실제로 도는지 본다.
// 관측 트랜잭션이 아니라 이 tick이 전이를 확정한다 (설계 8.5).
func TestRescanTickReconcilesReplacement(t *testing.T) {
	const ip = "198.51.100.51"
	const port = 8443
	f := newRescanFixture(t, "rescan-replace-", []string{ip})
	stubScan(t, func(ep Endpoint) Observation { return Observation{Endpoint: ep} })

	oldCert := newRescanLeaf(t, "rescan-replace-old.example.test", 30*24*time.Hour)
	newCert := newRescanLeaf(t, "rescan-replace-new.example.test", 365*24*time.Hour)
	f.observe(ip, port, oldCert)
	f.observe(ip, port, oldCert)
	f.observe(ip, port, newCert)
	f.observe(ip, port, newCert) // current_streak = 2 (RESCAN_REPLACE_CONFIRM 충족)

	m := NewManager(f.st).UseRescanOptions(testRescanOptions())
	sum, err := m.RunRescanOnce(f.ctx)
	if err != nil {
		t.Fatalf("run rescan once: %v", err)
	}
	if sum.Replaced != 1 {
		t.Fatalf("replaced = %d, want 1", sum.Replaced)
	}

	var status string
	var replacedBy sql.NullString
	if err := f.pool.QueryRow(`
		SELECT status, replaced_by_certificate_id::text FROM certificates
		 WHERE common_name = 'rescan-replace-old.example.test'`).Scan(&status, &replacedBy); err != nil {
		t.Fatalf("read old certificate: %v", err)
	}
	if status != "replaced" || !replacedBy.Valid {
		t.Errorf("old cert = (%s, %v), want replaced + 교체 링크", status, replacedBy)
	}
}

// TestRescanSchedulerDisabled는 RESCAN_ENABLED=false에서 아무것도 띄우지 않는지 본다.
func TestRescanSchedulerDisabled(t *testing.T) {
	m := NewManager(nil).UseRescanOptions(RescanOptions{Enabled: false})
	stop := m.StartRescanScheduler(context.Background())
	stop() // store가 nil이어도 패닉 없이 돌아와야 한다(아무 tick도 돌지 않았다).
}
