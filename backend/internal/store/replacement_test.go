package store

// 교체 조정 배치(replacement.go)의 전이 조건을 고정한다.
// 픽스처는 전부 테스트 실행 시점에 생성한다 (보안 하드 규칙 4).

import (
	"context"
	"crypto/x509"
	"database/sql"
	"strings"
	"testing"
)

// replacementFixture는 재스캔 관측을 반복 주입하기 위한 도우미다.
type replacementFixture struct {
	t     *testing.T
	st    *Store
	pool  *sql.DB
	ctx   context.Context
	ip    string
	jobID string
}

func newReplacementFixture(t *testing.T, ip string, ports []int, reqID string) *replacementFixture {
	t.Helper()
	pool := testPool(t)
	st := New(pool)
	t.Cleanup(func() {
		_, _ = pool.Exec(`DELETE FROM certificates WHERE common_name LIKE 'replace-fixture-%.example.test'`)
		_, _ = pool.Exec(`DELETE FROM targets WHERE ip_address = $1::inet AND target_type = 'network'`, ip)
		_, _ = pool.Exec(`DELETE FROM scan_jobs WHERE targets = ARRAY[$1::text]`, ip)
		cleanupAuditByRequestID(pool, reqID)
	})
	return &replacementFixture{
		t: t, st: st, pool: pool, ctx: context.Background(), ip: ip,
		jobID: newMonitorScanJob(t, st, pool, ip, ports, reqID),
	}
}

// observe는 같은 관측을 times번 반복해 current_streak을 쌓는다.
func (f *replacementFixture) observe(port int, cert *x509.Certificate, times int) {
	f.t.Helper()
	for range times {
		if err := f.st.RecordEndpointResult(f.ctx, f.jobID, EndpointObservation{
			IP: f.ip, Port: port, Reachable: true, TLSVersion: "TLS 1.3",
			Chain: []*x509.Certificate{cert},
		}); err != nil {
			f.t.Fatalf("record endpoint result: %v", err)
		}
	}
}

// certRow는 지문으로 인증서의 id·상태·교체 링크를 읽는다.
func (f *replacementFixture) certRow(cert *x509.Certificate) (id, status string, replacedBy *string, replacedAt *sql.NullTime) {
	f.t.Helper()
	var at sql.NullTime
	err := f.pool.QueryRow(`
		SELECT id::text, status, replaced_by_certificate_id::text, replaced_at
		  FROM certificates WHERE fingerprint_sha256 = $1`,
		fingerprintOf(f.t, cert)).Scan(&id, &status, &replacedBy, &at)
	if err != nil {
		f.t.Fatalf("read certificate: %v", err)
	}
	return id, status, replacedBy, &at
}

// locationStatus는 (인증서, 포트) 네트워크 위치의 배포 상태와 최종 확인 시각이다.
func (f *replacementFixture) locationStatus(certID string, port int) (string, sql.NullTime) {
	f.t.Helper()
	var status string
	var verified sql.NullTime
	if err := f.pool.QueryRow(`
		SELECT deployment_status, last_verified_at FROM certificate_locations
		 WHERE certificate_id = $1::uuid AND port = $2 AND location_type = 'network_endpoint'`,
		certID, port).Scan(&status, &verified); err != nil {
		f.t.Fatalf("read location: %v", err)
	}
	return status, verified
}

// TestReconcileReplacedCertificates는 정상 교체 1건의 전 과정을 본다:
// 위치 removed → 상태 replaced + 교체 링크 → pending 알림 종결 → 감사 이벤트.
func TestReconcileReplacedCertificates(t *testing.T) {
	const ip = "203.0.113.80" // 문서화용 예약 대역 — 실제 주소 아님
	const port = 8443
	f := newReplacementFixture(t, ip, []int{port}, "test-req-replace-basic")

	oldCert := newTestX509Leaf(t, "replace-fixture-old.example.test")
	newCert := newTestX509Leaf(t, "replace-fixture-new.example.test")

	f.observe(port, oldCert, 2)
	oldID, _, _, _ := f.certRow(oldCert)
	t.Cleanup(func() { cleanupAuditByTargetID(f.pool, oldID) })

	// 옛 인증서에 발송 대기 중인 만료 알림이 걸려 있다 — 교체가 확정되면
	// 이미 없어진 인증서로 메일이 나가면 안 된다.
	var deliveryID string
	if err := f.pool.QueryRow(`
		WITH e AS (
			INSERT INTO notification_events (dedup_key, event_type, certificate_id, severity, payload, status)
			VALUES ($1, 'certificate.expiring', $2::uuid, 'warning', '{}'::jsonb, 'pending')
			RETURNING id)
		INSERT INTO notification_deliveries (event_id, channel, recipient, status)
		SELECT e.id, 'inbox', 'replace-fixture', 'pending' FROM e
		RETURNING id::text`,
		"test-replace-"+oldID, oldID).Scan(&deliveryID); err != nil {
		t.Fatalf("seed pending delivery: %v", err)
	}
	t.Cleanup(func() {
		_, _ = f.pool.Exec(`DELETE FROM notification_events WHERE dedup_key = $1`, "test-replace-"+oldID)
	})

	_, verifiedBefore := f.locationStatus(oldID, port)

	f.observe(port, newCert, 2)

	sum, err := f.st.ReconcileReplacedCertificates(f.ctx, ReplacementOptions{ConfirmThreshold: 2, Limit: 10})
	if err != nil {
		t.Fatalf("reconcile: %v", err)
	}
	if sum.Replaced != 1 {
		t.Fatalf("replaced = %d, want 1 (summary=%+v)", sum.Replaced, sum)
	}

	newID, _, _, _ := f.certRow(newCert)
	gotID, status, replacedBy, replacedAt := f.certRow(oldCert)
	if gotID != oldID {
		t.Fatalf("certificate id changed: %s → %s", oldID, gotID)
	}
	if status != "replaced" {
		t.Errorf("status = %q, want replaced", status)
	}
	if replacedBy == nil || *replacedBy != newID {
		t.Errorf("replaced_by_certificate_id = %v, want %s", replacedBy, newID)
	}
	if !replacedAt.Valid {
		t.Error("replaced_at이 비어 있다")
	}

	locStatus, verifiedAfter := f.locationStatus(oldID, port)
	if locStatus != "removed" {
		t.Errorf("옛 위치 deployment_status = %q, want removed", locStatus)
	}
	if verifiedBefore.Valid != verifiedAfter.Valid || !verifiedBefore.Time.Equal(verifiedAfter.Time) {
		t.Errorf("last_verified_at이 갱신됐다: %v → %v (마지막 실관측 시각을 보존해야 한다)",
			verifiedBefore.Time, verifiedAfter.Time)
	}

	var deliveryStatus, deliveryErr string
	if err := f.pool.QueryRow(
		`SELECT status, coalesce(last_error, '') FROM notification_deliveries WHERE id = $1::uuid`,
		deliveryID).Scan(&deliveryStatus, &deliveryErr); err != nil {
		t.Fatalf("read delivery: %v", err)
	}
	if deliveryStatus != "skipped" {
		t.Errorf("발송 대기 딜리버리 status = %q, want skipped", deliveryStatus)
	}
	if !strings.Contains(deliveryErr, "replaced") {
		t.Errorf("딜리버리 사유 = %q, 교체 사유가 남아야 한다", deliveryErr)
	}

	// 감사 이벤트는 하드 규칙 5. 인증서 원문·개인키는 절대 담지 않는다(규칙 3).
	var before, after string
	var actor sql.NullString
	if err := f.pool.QueryRow(`
		SELECT coalesce(before_value::text, ''), coalesce(after_value::text, ''), actor_id::text
		  FROM audit_events
		 WHERE event_type = 'certificate.replaced' AND target_id = $1`,
		oldID).Scan(&before, &after, &actor); err != nil {
		t.Fatalf("read audit event: %v", err)
	}
	if actor.Valid {
		t.Errorf("actor_id = %q, want NULL (시스템 전이)", actor.String)
	}
	// jsonb는 키·값 사이에 공백을 넣어 렌더링한다 — 공백을 지우고 대조한다.
	compactBefore := strings.ReplaceAll(before, " ", "")
	compactAfter := strings.ReplaceAll(after, " ", "")
	if !strings.Contains(compactBefore, `"status":"active"`) {
		t.Errorf("before_value = %s, 이전 상태가 없다", before)
	}
	if !strings.Contains(compactAfter, newID) || !strings.Contains(compactAfter, `"confirmations":2`) {
		t.Errorf("after_value = %s, 교체 대상·확증 횟수가 없다", after)
	}
	for _, forbidden := range []string{"BEGIN CERTIFICATE", "PRIVATE KEY"} {
		if strings.Contains(before+after, forbidden) {
			t.Errorf("감사 이벤트에 %q가 들어 있다", forbidden)
		}
	}

	// 멱등: 두 번째 실행은 아무것도 전이하지 않는다.
	sum2, err := f.st.ReconcileReplacedCertificates(f.ctx, ReplacementOptions{ConfirmThreshold: 2, Limit: 10})
	if err != nil {
		t.Fatalf("reconcile (2nd): %v", err)
	}
	if sum2.Replaced != 0 {
		t.Errorf("두 번째 실행 replaced = %d, want 0", sum2.Replaced)
	}
}

// TestReconcileReplacedCertificatesRequiresConfirmations는 확증 임계값을 본다 —
// 1회 관측으로 전이하면 LB 뒤에서 A와 B가 서로를 무한히 replaced로 만든다.
func TestReconcileReplacedCertificatesRequiresConfirmations(t *testing.T) {
	const ip = "203.0.113.81" // 문서화용 예약 대역 — 실제 주소 아님
	const port = 8443
	f := newReplacementFixture(t, ip, []int{port}, "test-req-replace-confirm")

	oldCert := newTestX509Leaf(t, "replace-fixture-c-old.example.test")
	newCert := newTestX509Leaf(t, "replace-fixture-c-new.example.test")

	f.observe(port, oldCert, 2)
	oldID, _, _, _ := f.certRow(oldCert)
	t.Cleanup(func() { cleanupAuditByTargetID(f.pool, oldID) })

	f.observe(port, newCert, 1) // 확증 1회 — 아직 부족하다
	sum, err := f.st.ReconcileReplacedCertificates(f.ctx, ReplacementOptions{ConfirmThreshold: 2, Limit: 10})
	if err != nil {
		t.Fatalf("reconcile: %v", err)
	}
	if sum.Replaced != 0 {
		t.Fatalf("확증 1회인데 replaced = %d, want 0", sum.Replaced)
	}
	if _, status, _, _ := f.certRow(oldCert); status != "active" {
		t.Errorf("status = %q, want active", status)
	}
	if loc, _ := f.locationStatus(oldID, port); loc == "removed" {
		t.Error("확증 전인데 위치가 removed로 바뀌었다")
	}

	f.observe(port, newCert, 1) // 2회째 — 전이한다
	sum, err = f.st.ReconcileReplacedCertificates(f.ctx, ReplacementOptions{ConfirmThreshold: 2, Limit: 10})
	if err != nil {
		t.Fatalf("reconcile: %v", err)
	}
	if sum.Replaced != 1 {
		t.Fatalf("확증 2회인데 replaced = %d, want 1", sum.Replaced)
	}
}

// TestReconcileReplacedCertificatesKeepsUploadedCertificate는 수동 업로드 보호를 본다:
// 업로드는 관측이 아니라 선언이므로 "네트워크에서 안 보인다"가 "폐기됐다"를 뜻하지 않는다
// (아직 배포 전일 수 있다). 위치만 removed로 표시하고 상태는 그대로 둔다.
func TestReconcileReplacedCertificatesKeepsUploadedCertificate(t *testing.T) {
	const ip = "203.0.113.82" // 문서화용 예약 대역 — 실제 주소 아님
	const port = 8443
	f := newReplacementFixture(t, ip, []int{port}, "test-req-replace-upload")

	oldCert := newTestX509Leaf(t, "replace-fixture-u-old.example.test")
	newCert := newTestX509Leaf(t, "replace-fixture-u-new.example.test")

	f.observe(port, oldCert, 2)
	oldID, _, _, _ := f.certRow(oldCert)
	t.Cleanup(func() { cleanupAuditByTargetID(f.pool, oldID) })

	// 수동 업로드 흔적(location_type='file')을 붙인다 — import 경로가 만드는 행이다.
	if _, err := f.pool.Exec(`
		INSERT INTO certificate_locations (certificate_id, target_id, location_type, file_path, deployment_status)
		SELECT $1::uuid, t.id, 'file', 'uploaded.pem', 'unknown'
		  FROM targets t WHERE t.ip_address = $2::inet AND t.target_type = 'network'`,
		oldID, ip); err != nil {
		t.Fatalf("seed file location: %v", err)
	}

	f.observe(port, newCert, 2)
	sum, err := f.st.ReconcileReplacedCertificates(f.ctx, ReplacementOptions{ConfirmThreshold: 2, Limit: 10})
	if err != nil {
		t.Fatalf("reconcile: %v", err)
	}
	if sum.Replaced != 0 {
		t.Errorf("업로드된 인증서가 전이됐다: replaced = %d", sum.Replaced)
	}
	if sum.LocationsOnly != 1 {
		t.Errorf("locations_only = %d, want 1", sum.LocationsOnly)
	}
	if _, status, replacedBy, _ := f.certRow(oldCert); status != "active" || replacedBy != nil {
		t.Errorf("status = %q replaced_by = %v, want active/nil", status, replacedBy)
	}
	if loc, _ := f.locationStatus(oldID, port); loc != "removed" {
		t.Errorf("네트워크 위치 deployment_status = %q, want removed", loc)
	}

	var audits int
	if err := f.pool.QueryRow(
		`SELECT count(*) FROM audit_events WHERE event_type = 'certificate.replaced' AND target_id = $1`,
		oldID).Scan(&audits); err != nil {
		t.Fatalf("count audit events: %v", err)
	}
	if audits != 0 {
		t.Errorf("전이하지 않았는데 감사 이벤트 %d건", audits)
	}
}

// TestReconcileReplacedCertificatesWaitsForAllEndpoints는 조건 2를 본다:
// 다른 엔드포인트에 아직 배포돼 있으면 상태를 바꾸지 않는다.
func TestReconcileReplacedCertificatesWaitsForAllEndpoints(t *testing.T) {
	const ip = "203.0.113.83" // 문서화용 예약 대역 — 실제 주소 아님
	portA, portB := 8443, 8444
	f := newReplacementFixture(t, ip, []int{portA, portB}, "test-req-replace-multi")

	oldCert := newTestX509Leaf(t, "replace-fixture-m-old.example.test")
	newCert := newTestX509Leaf(t, "replace-fixture-m-new.example.test")

	f.observe(portA, oldCert, 2)
	f.observe(portB, oldCert, 2)
	oldID, _, _, _ := f.certRow(oldCert)
	t.Cleanup(func() { cleanupAuditByTargetID(f.pool, oldID) })

	f.observe(portA, newCert, 2) // 포트 A만 교체됐다
	sum, err := f.st.ReconcileReplacedCertificates(f.ctx, ReplacementOptions{ConfirmThreshold: 2, Limit: 10})
	if err != nil {
		t.Fatalf("reconcile: %v", err)
	}
	if sum.Replaced != 0 {
		t.Errorf("포트 B에 남아 있는데 전이했다: replaced = %d", sum.Replaced)
	}
	if _, status, _, _ := f.certRow(oldCert); status != "active" {
		t.Errorf("status = %q, want active", status)
	}
	if loc, _ := f.locationStatus(oldID, portA); loc != "removed" {
		t.Errorf("포트 A 위치 = %q, want removed", loc)
	}

	f.observe(portB, newCert, 2) // 마지막 엔드포인트까지 교체 — 이제 전이한다
	sum, err = f.st.ReconcileReplacedCertificates(f.ctx, ReplacementOptions{ConfirmThreshold: 2, Limit: 10})
	if err != nil {
		t.Fatalf("reconcile: %v", err)
	}
	if sum.Replaced != 1 {
		t.Fatalf("전 엔드포인트가 교체됐는데 replaced = %d, want 1", sum.Replaced)
	}
	if _, status, _, _ := f.certRow(oldCert); status != "replaced" {
		t.Errorf("status = %q, want replaced", status)
	}
}

// TestReconcileReplacedCertificatesRespectsLimit은 tick당 전이 상한을 본다 —
// 첫 가동 시 누적 스톡이 한꺼번에 전이되면 감사 체인이 직렬화되어 로그인·업로드가 막힌다.
func TestReconcileReplacedCertificatesRespectsLimit(t *testing.T) {
	const ip = "203.0.113.84" // 문서화용 예약 대역 — 실제 주소 아님
	portA, portB := 8443, 8444
	f := newReplacementFixture(t, ip, []int{portA, portB}, "test-req-replace-limit")

	oldA := newTestX509Leaf(t, "replace-fixture-l-a-old.example.test")
	newA := newTestX509Leaf(t, "replace-fixture-l-a-new.example.test")
	oldB := newTestX509Leaf(t, "replace-fixture-l-b-old.example.test")
	newB := newTestX509Leaf(t, "replace-fixture-l-b-new.example.test")

	f.observe(portA, oldA, 2)
	f.observe(portB, oldB, 2)
	for _, c := range []*x509.Certificate{oldA, oldB} {
		id, _, _, _ := f.certRow(c)
		t.Cleanup(func() { cleanupAuditByTargetID(f.pool, id) })
	}
	f.observe(portA, newA, 2)
	f.observe(portB, newB, 2)

	sum, err := f.st.ReconcileReplacedCertificates(f.ctx, ReplacementOptions{ConfirmThreshold: 2, Limit: 1})
	if err != nil {
		t.Fatalf("reconcile: %v", err)
	}
	if sum.Replaced != 1 {
		t.Fatalf("limit=1인데 replaced = %d", sum.Replaced)
	}
	sum, err = f.st.ReconcileReplacedCertificates(f.ctx, ReplacementOptions{ConfirmThreshold: 2, Limit: 1})
	if err != nil {
		t.Fatalf("reconcile (2nd): %v", err)
	}
	if sum.Replaced != 1 {
		t.Fatalf("나머지 1건이 다음 tick에 전이되지 않았다: replaced = %d", sum.Replaced)
	}
}
