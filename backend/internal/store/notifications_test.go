package store

// 알림 영속화 계층 검증 (docs/design/05-notifications.md, 마이그레이션 0012).
// - 탐지 멱등성: 같은 인증서·임계값을 몇 번 돌려도 이벤트는 1건.
// - 판정 기준: 저장된 status가 아니라 not_after (revoked/replaced만 제외 필터).
// - 팬아웃: 정책이 겹쳐도 수신자는 합집합, 해석 실패는 skipped 사유로 남는다.
// - 리스: FOR UPDATE SKIP LOCKED 동시 리스에서 같은 행이 두 번 나오지 않는다.
// - 알림함: UNIQUE (user_id, event_id)가 중복 삽입을 흡수한다.
// 픽스처는 전부 테스트 실행 시점에 생성한다 (보안 하드 규칙 4).

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"
)

// randomHex는 픽스처용 무작위 hex 문자열이다(지문 자리 채우기 — 실제 인증서 아님).
func randomHex(t *testing.T, n int) string {
	t.Helper()
	buf := make([]byte, n)
	if _, err := rand.Read(buf); err != nil {
		t.Fatalf("random: %v", err)
	}
	return hex.EncodeToString(buf)
}

// isolateNotificationPolicies는 기존 정책(시드 '기본 만료 알림' 포함)을 잠시 끈다.
// 시드 정책은 모든 인증서에 매칭되므로 켜둔 채로는 탐지 결과를 단정할 수 없다.
func isolateNotificationPolicies(t *testing.T, pool *sql.DB) {
	t.Helper()
	rows, err := pool.Query(`UPDATE notification_policies SET enabled = false WHERE enabled RETURNING id`)
	if err != nil {
		t.Fatalf("disable policies: %v", err)
	}
	var ids []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			rows.Close()
			t.Fatalf("scan policy id: %v", err)
		}
		ids = append(ids, id)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		t.Fatalf("disable policies: %v", err)
	}
	t.Cleanup(func() {
		for _, id := range ids {
			_, _ = pool.Exec(`UPDATE notification_policies SET enabled = true WHERE id = $1::uuid`, id)
		}
	})
}

// notifyCertOpts는 탐지 대상 인증서 픽스처의 조건이다.
type notifyCertOpts struct {
	NotAfter    time.Time
	Status      string
	Type        string
	Environment *string
	OwnerID     *string
}

// newNotifyCert는 인증서 행을 직접 삽입한다 — 탐지 쿼리는 컬럼만 읽으므로
// x509 생성이 필요 없다. 지문 자리에는 무작위 64자 hex를 넣는다.
func newNotifyCert(t *testing.T, pool *sql.DB, cn string, o notifyCertOpts) string {
	t.Helper()
	if o.Status == "" {
		o.Status = "active"
	}
	if o.Type == "" {
		o.Type = "server"
	}
	var id string
	err := pool.QueryRow(`
		INSERT INTO certificates (
			fingerprint_sha256, serial_number, subject_dn, issuer_dn, common_name,
			not_before, not_after, public_key_algorithm, public_key_size, signature_algorithm,
			certificate_type, status, environment, owner_id)
		VALUES ($1, '1', 'CN=' || $2, 'CN=' || $2, $2,
		        $3::timestamptz - interval '365 days', $3, 'ECDSA', 256, 'ECDSA-SHA256', $4, $5, $6, $7::uuid)
		RETURNING id`,
		randomHex(t, 32), cn, o.NotAfter, o.Type, o.Status, o.Environment, o.OwnerID).Scan(&id)
	if err != nil {
		t.Fatalf("insert test certificate: %v", err)
	}
	t.Cleanup(func() {
		// 이벤트는 certificate_id ON DELETE SET NULL이라 인증서 삭제로 지워지지 않는다.
		_, _ = pool.Exec(`DELETE FROM notification_events WHERE certificate_id = $1::uuid`, id)
		_, _ = pool.Exec(`DELETE FROM certificates WHERE id = $1::uuid`, id)
	})
	return id
}

// newNotifyUser는 활성 사용자 1명을 만든다(수신자 해석용). password_hash는 검증에
// 쓰이지 않는 자리 채움값이며 실제 자격증명이 아니다.
func newNotifyUser(t *testing.T, pool *sql.DB, username, role, email string) string {
	t.Helper()
	var id string
	var mail any
	if email != "" {
		mail = email
	}
	err := pool.QueryRow(`
		INSERT INTO users (username, display_name, password_hash, role, email)
		VALUES ($1, $1, $2, $3, $4) RETURNING id`,
		username, "x-not-a-hash-"+randomHex(t, 8), role, mail).Scan(&id)
	if err != nil {
		t.Fatalf("insert test user: %v", err)
	}
	t.Cleanup(func() {
		_, _ = pool.Exec(`DELETE FROM users WHERE id = $1::uuid`, id)
	})
	return id
}

// newNotifyPolicy는 정책을 만들고 정리까지 등록한다.
func newNotifyPolicy(t *testing.T, st *Store, pool *sql.DB, in NotificationPolicyInput) *NotificationPolicy {
	t.Helper()
	reqID := "test-req-notify-policy-" + randomHex(t, 6)
	in.RequestID = &reqID
	if in.Name == "" {
		in.Name = "test-policy-" + randomHex(t, 6)
	}
	p, err := st.CreateNotificationPolicy(context.Background(), in)
	if err != nil {
		t.Fatalf("create notification policy: %v", err)
	}
	t.Cleanup(func() {
		_, _ = pool.Exec(`DELETE FROM notification_policies WHERE id = $1::uuid`, p.ID)
		cleanupAuditByRequestID(pool, reqID)
		cleanupAuditByTargetID(pool, p.ID)
	})
	return p
}

// newRawEvent는 정책·인증서 없이 이벤트 1건을 직접 넣는다(딜리버리 상태 머신 테스트용).
func newRawEvent(t *testing.T, pool *sql.DB) string {
	t.Helper()
	var id string
	err := pool.QueryRow(`
		INSERT INTO notification_events (dedup_key, event_type, severity, payload, status)
		VALUES ($1, 'certificate.expiring', 'warning',
		        jsonb_build_object('certificate_id', '', 'common_name', 'fixture',
		                           'not_after', '2030-01-01T00:00:00Z', 'days_remaining', 7,
		                           'environment', '', 'owner', '', 'location_count', 0),
		        'pending')
		RETURNING id`, "test-fixture:"+randomHex(t, 12)).Scan(&id)
	if err != nil {
		t.Fatalf("insert test event: %v", err)
	}
	t.Cleanup(func() {
		_, _ = pool.Exec(`DELETE FROM notification_events WHERE id = $1::uuid`, id)
	})
	return id
}

func countEvents(t *testing.T, pool *sql.DB, dedupKey string) int {
	t.Helper()
	var n int
	if err := pool.QueryRow(`SELECT count(*) FROM notification_events WHERE dedup_key = $1`,
		dedupKey).Scan(&n); err != nil {
		t.Fatalf("count events: %v", err)
	}
	return n
}

func findEvent(events []DetectedEvent, dedupKey string) *DetectedEvent {
	for i := range events {
		if events[i].DedupKey == dedupKey {
			return &events[i]
		}
	}
	return nil
}

// ===== 탐지 =====

// TestDetectExpiryEventsIsIdempotent는 설계 4장의 핵심 성질을 고정한다:
// 같은 인증서·임계값을 몇 번 탐지해도 dedup_key UNIQUE로 이벤트는 1건이고,
// 두 번째 탐지는 그 이벤트를 반환하지 않는다(= 중복 발송이 구조적으로 불가능).
func TestDetectExpiryEventsIsIdempotent(t *testing.T) {
	pool := testPool(t)
	st := New(pool)
	ctx := context.Background()
	isolateNotificationPolicies(t, pool)

	certID := newNotifyCert(t, pool, "notify-idempotent.example.test", notifyCertOpts{
		NotAfter: time.Now().Add(10 * 24 * time.Hour),
	})
	newNotifyPolicy(t, st, pool, NotificationPolicyInput{
		Enabled:    true,
		EventTypes: []string{NotifyEventCertExpiring},
		Thresholds: []int{30},
		Channels:   []string{NotifyChannelInbox},
	})
	key := fmt.Sprintf("certificate.expiring:%s:30", certID)

	first, err := st.DetectExpiryEvents(ctx, DetectModeSend)
	if err != nil {
		t.Fatalf("first detect: %v", err)
	}
	e := findEvent(first, key)
	if e == nil {
		t.Fatalf("first detect did not create %s", key)
	}
	if e.Status != NotifyEventPending {
		t.Errorf("status = %q, want pending", e.Status)
	}
	if e.ThresholdDays == nil || *e.ThresholdDays != 30 {
		t.Errorf("threshold_days = %v, want 30", e.ThresholdDays)
	}
	if e.Payload.DaysRemaining < 9 || e.Payload.DaysRemaining > 10 {
		t.Errorf("days_remaining = %d, want 9 or 10", e.Payload.DaysRemaining)
	}
	if e.Severity != "warning" {
		t.Errorf("severity = %q, want warning (D-10)", e.Severity)
	}

	second, err := st.DetectExpiryEvents(ctx, DetectModeSend)
	if err != nil {
		t.Fatalf("second detect: %v", err)
	}
	if dup := findEvent(second, key); dup != nil {
		t.Errorf("second detect returned %s again", key)
	}
	if n := countEvents(t, pool, key); n != 1 {
		t.Errorf("events with dedup_key %s = %d, want 1", key, n)
	}
}

// TestDetectUsesNotAfterNotStoredStatus는 설계 7.4를 고정한다:
// 만료 판정은 not_after로 하고(저장된 status는 stale이라 신뢰하지 않는다),
// status는 revoked/replaced 제외 필터로만 쓴다. CA 인증서도 대상이다.
func TestDetectUsesNotAfterNotStoredStatus(t *testing.T) {
	pool := testPool(t)
	st := New(pool)
	ctx := context.Background()
	isolateNotificationPolicies(t, pool)

	// status는 active인데 이미 만료된 인증서 — 만료 이벤트가 나와야 한다.
	staleID := newNotifyCert(t, pool, "notify-stale.example.test", notifyCertOpts{
		NotAfter: time.Now().Add(-3 * 24 * time.Hour), Status: "active",
	})
	// 폐기된 인증서는 만료가 임박해도 대상이 아니다.
	revokedID := newNotifyCert(t, pool, "notify-revoked.example.test", notifyCertOpts{
		NotAfter: time.Now().Add(5 * 24 * time.Hour), Status: "revoked",
	})
	// CA도 포함한다 — CA 만료는 리프 전체를 무너뜨린다.
	caID := newNotifyCert(t, pool, "notify-ca.example.test", notifyCertOpts{
		NotAfter: time.Now().Add(5 * 24 * time.Hour), Type: "ca",
	})
	newNotifyPolicy(t, st, pool, NotificationPolicyInput{
		Enabled:    true,
		EventTypes: []string{NotifyEventCertExpiring, NotifyEventCertExpired},
		Thresholds: []int{7},
		Channels:   []string{NotifyChannelInbox},
	})

	events, err := st.DetectExpiryEvents(ctx, DetectModeSend)
	if err != nil {
		t.Fatalf("detect: %v", err)
	}

	expiredKey := "certificate.expired:" + staleID
	e := findEvent(events, expiredKey)
	if e == nil {
		t.Fatalf("expired event missing for a cert with stale status=active")
	}
	if e.Severity != "critical" {
		t.Errorf("expired severity = %q, want critical", e.Severity)
	}
	if e.Payload.DaysRemaining > 0 {
		t.Errorf("days_remaining = %d, want <= 0 for an expired cert", e.Payload.DaysRemaining)
	}
	if findEvent(events, fmt.Sprintf("certificate.expiring:%s:7", revokedID)) != nil {
		t.Error("revoked certificate produced an expiring event")
	}
	if findEvent(events, fmt.Sprintf("certificate.expiring:%s:7", caID)) == nil {
		t.Error("CA certificate produced no expiring event")
	}
}

// TestDetectPayloadCarriesOnlyPublicFields는 하드 규칙 3의 구조적 이행을 고정한다:
// payload에는 공개 필드 스냅샷만 들어가고 인증서 원문이 들어갈 자리가 없다.
func TestDetectPayloadCarriesOnlyPublicFields(t *testing.T) {
	pool := testPool(t)
	st := New(pool)
	ctx := context.Background()
	isolateNotificationPolicies(t, pool)

	ownerID := newNotifyUser(t, pool, "notify-owner-"+randomHex(t, 4), "operator", "")
	env := "prod"
	certID := newNotifyCert(t, pool, "notify-payload.example.test", notifyCertOpts{
		NotAfter: time.Now().Add(3 * 24 * time.Hour), Environment: &env, OwnerID: &ownerID,
	})
	newNotifyPolicy(t, st, pool, NotificationPolicyInput{
		Enabled:    true,
		EventTypes: []string{NotifyEventCertExpiring},
		Thresholds: []int{7},
		Channels:   []string{NotifyChannelInbox},
	})
	if _, err := st.DetectExpiryEvents(ctx, DetectModeSend); err != nil {
		t.Fatalf("detect: %v", err)
	}

	var raw string
	if err := pool.QueryRow(`SELECT payload::text FROM notification_events WHERE dedup_key = $1`,
		fmt.Sprintf("certificate.expiring:%s:7", certID)).Scan(&raw); err != nil {
		t.Fatalf("read payload: %v", err)
	}
	var m map[string]any
	if err := json.Unmarshal([]byte(raw), &m); err != nil {
		t.Fatalf("decode payload: %v", err)
	}
	want := map[string]bool{
		"certificate_id": true, "common_name": true, "not_after": true, "days_remaining": true,
		"environment": true, "owner": true, "location_count": true,
	}
	for k := range m {
		if !want[k] {
			t.Errorf("payload carries unexpected key %q", k)
		}
	}
	for k := range want {
		if _, ok := m[k]; !ok {
			t.Errorf("payload is missing key %q", k)
		}
	}
	if strings.Contains(raw, "BEGIN CERTIFICATE") || strings.Contains(raw, "PRIVATE KEY") {
		t.Error("payload contains certificate/key material")
	}
	if m["owner"] == "" {
		t.Error("owner display name is empty")
	}
}

// TestDetectSeedModeSuppresses는 설계 7.7을 고정한다: 첫 실행 seed 모드는
// 이미 지난 임계값을 suppressed로만 적재하고 딜리버리를 만들지 않는다.
func TestDetectSeedModeSuppresses(t *testing.T) {
	pool := testPool(t)
	st := New(pool)
	ctx := context.Background()
	isolateNotificationPolicies(t, pool)

	certID := newNotifyCert(t, pool, "notify-seed.example.test", notifyCertOpts{
		NotAfter: time.Now().Add(5 * 24 * time.Hour),
	})
	newNotifyPolicy(t, st, pool, NotificationPolicyInput{
		Enabled:    true,
		EventTypes: []string{NotifyEventCertExpiring},
		Thresholds: []int{7, 30},
		Channels:   []string{NotifyChannelInbox},
		Recipients: []NotificationRecipient{{Kind: RecipientRole, Role: "system_administrator"}},
	})

	res, locked, err := st.RunNotificationTick(ctx, DetectModeSeed)
	if err != nil {
		t.Fatalf("seed tick: %v", err)
	}
	if !locked {
		t.Fatal("seed tick did not take the advisory lock")
	}
	if len(res.Events) < 2 {
		t.Fatalf("seed tick created %d events, want >= 2 (D-7, D-30)", len(res.Events))
	}
	if res.Deliveries != 0 {
		t.Errorf("seed tick created %d deliveries, want 0", res.Deliveries)
	}
	for _, e := range res.Events {
		if e.Status != NotifyEventSuppressed {
			t.Errorf("event %s status = %q, want suppressed", e.DedupKey, e.Status)
		}
	}

	// seed가 dedup_key를 선점했으므로 이어지는 send tick은 아무것도 새로 만들지 않는다.
	next, locked, err := st.RunNotificationTick(ctx, DetectModeSend)
	if err != nil {
		t.Fatalf("send tick: %v", err)
	}
	if !locked {
		t.Fatal("send tick did not take the advisory lock")
	}
	if e := findEvent(next.Events, fmt.Sprintf("certificate.expiring:%s:7", certID)); e != nil {
		t.Error("send tick re-created a threshold already seeded")
	}

	var n int
	if err := pool.QueryRow(`
		SELECT count(*) FROM notification_deliveries d
		  JOIN notification_events e ON e.id = d.event_id
		 WHERE e.certificate_id = $1::uuid`, certID).Scan(&n); err != nil {
		t.Fatalf("count deliveries: %v", err)
	}
	if n != 0 {
		t.Errorf("seeded events produced %d deliveries, want 0", n)
	}
}

// TestDetectAppliesPolicyFilters는 환경 필터가 비면 전체 매칭, 값이 있으면
// 그 환경만 매칭하는지 검증한다.
func TestDetectAppliesPolicyFilters(t *testing.T) {
	pool := testPool(t)
	st := New(pool)
	ctx := context.Background()
	isolateNotificationPolicies(t, pool)

	prod, dev := "prod", "dev"
	prodID := newNotifyCert(t, pool, "notify-prod.example.test", notifyCertOpts{
		NotAfter: time.Now().Add(4 * 24 * time.Hour), Environment: &prod,
	})
	devID := newNotifyCert(t, pool, "notify-dev.example.test", notifyCertOpts{
		NotAfter: time.Now().Add(4 * 24 * time.Hour), Environment: &dev,
	})
	p := newNotifyPolicy(t, st, pool, NotificationPolicyInput{
		Enabled:            true,
		EventTypes:         []string{NotifyEventCertExpiring},
		Thresholds:         []int{7},
		Channels:           []string{NotifyChannelInbox},
		FilterEnvironments: []string{"prod"},
	})
	if len(p.FilterEnvironments) != 1 || p.FilterEnvironments[0] != "prod" {
		t.Fatalf("filter_environments round trip = %v", p.FilterEnvironments)
	}

	events, err := st.DetectExpiryEvents(ctx, DetectModeSend)
	if err != nil {
		t.Fatalf("detect: %v", err)
	}
	if findEvent(events, fmt.Sprintf("certificate.expiring:%s:7", prodID)) == nil {
		t.Error("prod certificate did not match the prod filter")
	}
	if findEvent(events, fmt.Sprintf("certificate.expiring:%s:7", devID)) != nil {
		t.Error("dev certificate matched a prod-only policy")
	}
}

// ===== 팬아웃 =====

// TestFanOutUnionsOverlappingPolicies는 설계 4.1을 고정한다: 두 정책이 같은
// 인증서·임계값을 커버해도 이벤트는 1건이고 수신자는 합집합이 된다.
// 재실행해도 UNIQUE (event_id, channel, recipient)가 중복을 흡수한다.
func TestFanOutUnionsOverlappingPolicies(t *testing.T) {
	pool := testPool(t)
	st := New(pool)
	ctx := context.Background()
	isolateNotificationPolicies(t, pool)

	adminID := newNotifyUser(t, pool, "notify-admin-"+randomHex(t, 4), "system_administrator", "")
	certID := newNotifyCert(t, pool, "notify-union.example.test", notifyCertOpts{
		NotAfter: time.Now().Add(4 * 24 * time.Hour),
	})
	newNotifyPolicy(t, st, pool, NotificationPolicyInput{
		Enabled:    true,
		EventTypes: []string{NotifyEventCertExpiring},
		Thresholds: []int{7},
		Channels:   []string{NotifyChannelInbox},
		Recipients: []NotificationRecipient{{Kind: RecipientRole, Role: "system_administrator"}},
	})
	newNotifyPolicy(t, st, pool, NotificationPolicyInput{
		Enabled:    true,
		EventTypes: []string{NotifyEventCertExpiring},
		Thresholds: []int{7},
		Channels:   []string{NotifyChannelWebhook},
		Recipients: []NotificationRecipient{},
	})

	res, _, err := st.RunNotificationTick(ctx, DetectModeSend)
	if err != nil {
		t.Fatalf("tick: %v", err)
	}
	key := fmt.Sprintf("certificate.expiring:%s:7", certID)
	e := findEvent(res.Events, key)
	if e == nil {
		t.Fatalf("event %s not created", key)
	}
	if n := countEvents(t, pool, key); n != 1 {
		t.Errorf("overlapping policies created %d events, want 1", n)
	}

	got := map[string]string{}
	rows, err := pool.Query(
		`SELECT channel, recipient FROM notification_deliveries WHERE event_id = $1::uuid`, e.ID)
	if err != nil {
		t.Fatalf("read deliveries: %v", err)
	}
	for rows.Next() {
		var ch, rcpt string
		if err := rows.Scan(&ch, &rcpt); err != nil {
			rows.Close()
			t.Fatalf("scan delivery: %v", err)
		}
		got[ch] = rcpt
	}
	rows.Close()
	if got[NotifyChannelInbox] != adminID {
		t.Errorf("inbox recipient = %q, want the admin user id", got[NotifyChannelInbox])
	}
	if _, ok := got[NotifyChannelWebhook]; !ok {
		t.Error("webhook delivery missing")
	}
	if got[NotifyChannelWebhook] != "" {
		t.Errorf("webhook recipient = %q, want empty string (설계 3.7)", got[NotifyChannelWebhook])
	}

	// 재팬아웃은 행을 늘리지 않는다.
	before := len(got)
	n, err := st.FanOutDeliveries(ctx, []string{e.ID})
	if err != nil {
		t.Fatalf("re-fanout: %v", err)
	}
	if n != 0 {
		t.Errorf("re-fanout created %d rows, want 0", n)
	}
	var total int
	if err := pool.QueryRow(`SELECT count(*) FROM notification_deliveries WHERE event_id = $1::uuid`,
		e.ID).Scan(&total); err != nil {
		t.Fatalf("count deliveries: %v", err)
	}
	if total != before {
		t.Errorf("deliveries = %d after re-fanout, want %d", total, before)
	}
}

// TestFanOutOwnerFallbackAndSkip은 설계 14.3을 고정한다:
// owner 해석 실패는 static 수신자로 폴백하고, 폴백도 없으면 조용히 사라지지 않고
// skipped + 사유로 남는다(소유자 없는 인증서가 가장 알림이 필요한 케이스다).
func TestFanOutOwnerFallbackAndSkip(t *testing.T) {
	pool := testPool(t)
	st := New(pool)
	ctx := context.Background()
	isolateNotificationPolicies(t, pool)

	orphan := newNotifyCert(t, pool, "notify-orphan.example.test", notifyCertOpts{
		NotAfter: time.Now().Add(4 * 24 * time.Hour),
	})
	newNotifyPolicy(t, st, pool, NotificationPolicyInput{
		Enabled:    true,
		EventTypes: []string{NotifyEventCertExpiring},
		Thresholds: []int{7},
		Channels:   []string{NotifyChannelEmail},
		Recipients: []NotificationRecipient{{Kind: RecipientOwner}},
	})

	res, _, err := st.RunNotificationTick(ctx, DetectModeSend)
	if err != nil {
		t.Fatalf("tick: %v", err)
	}
	e := findEvent(res.Events, fmt.Sprintf("certificate.expiring:%s:7", orphan))
	if e == nil {
		t.Fatal("event for the ownerless certificate was not created")
	}
	var status string
	var lastErr sql.NullString
	if err := pool.QueryRow(`
		SELECT status, last_error FROM notification_deliveries
		 WHERE event_id = $1::uuid AND channel = 'email'`, e.ID).Scan(&status, &lastErr); err != nil {
		t.Fatalf("read delivery: %v", err)
	}
	if status != DeliveryStatusSkipped {
		t.Errorf("status = %q, want skipped", status)
	}
	if lastErr.String != skipReasonOwnerNoFallack {
		t.Errorf("last_error = %q, want the owner-fallback reason", lastErr.String)
	}

	// 같은 조건에 static 폴백을 둔 정책이면 그 주소로 발송 대기 행이 생긴다.
	fallback := "noc@example.test"
	newNotifyPolicy(t, st, pool, NotificationPolicyInput{
		Enabled:    true,
		EventTypes: []string{NotifyEventCertExpiring},
		Thresholds: []int{14},
		Channels:   []string{NotifyChannelEmail},
		Recipients: []NotificationRecipient{{Kind: RecipientOwner}, {Kind: RecipientStatic, Email: fallback}},
	})
	res2, _, err := st.RunNotificationTick(ctx, DetectModeSend)
	if err != nil {
		t.Fatalf("second tick: %v", err)
	}
	e2 := findEvent(res2.Events, fmt.Sprintf("certificate.expiring:%s:14", orphan))
	if e2 == nil {
		t.Fatal("D-14 event was not created")
	}
	var rcpt, st2 string
	if err := pool.QueryRow(`
		SELECT recipient, status FROM notification_deliveries
		 WHERE event_id = $1::uuid AND channel = 'email'`, e2.ID).Scan(&rcpt, &st2); err != nil {
		t.Fatalf("read fallback delivery: %v", err)
	}
	if rcpt != fallback || st2 != DeliveryStatusPending {
		t.Errorf("fallback delivery = (%q, %q), want (%q, pending)", rcpt, st2, fallback)
	}
}

// ===== 리스 · 재시도 =====

func newTestDelivery(t *testing.T, pool *sql.DB, eventID, channel, recipient string) string {
	t.Helper()
	var id string
	if err := pool.QueryRow(`
		INSERT INTO notification_deliveries (event_id, channel, recipient)
		VALUES ($1::uuid, $2, $3) RETURNING id`, eventID, channel, recipient).Scan(&id); err != nil {
		t.Fatalf("insert test delivery: %v", err)
	}
	return id
}

// TestLeaseDeliveriesConcurrentNoDuplicates는 FOR UPDATE SKIP LOCKED 리스가
// 동시 디스패처에서 같은 행을 두 번 내주지 않음을 검증한다.
func TestLeaseDeliveriesConcurrentNoDuplicates(t *testing.T) {
	pool := testPool(t)
	st := New(pool)
	ctx := context.Background()

	eventID := newRawEvent(t, pool)
	const rows = 6
	want := map[string]bool{}
	for i := range rows {
		want[newTestDelivery(t, pool, eventID, NotifyChannelEmail, fmt.Sprintf("r%d@example.test", i))] = true
	}

	const workers = 8
	var wg sync.WaitGroup
	leased := make(chan LeasedDelivery, rows*workers)
	for range workers {
		wg.Add(1)
		go func() {
			defer wg.Done()
			batch, err := st.LeaseDeliveries(ctx, 3)
			if err != nil {
				t.Errorf("lease: %v", err)
				return
			}
			for _, d := range batch {
				leased <- d
			}
		}()
	}
	wg.Wait()
	close(leased)

	seen := map[string]bool{}
	for d := range leased {
		if !want[d.ID] {
			continue // 다른 테스트가 남긴 행 — 이 테스트의 관심사가 아니다.
		}
		if seen[d.ID] {
			t.Errorf("delivery %s leased twice", d.ID)
		}
		seen[d.ID] = true
		if !d.LeaseExpiresAt.After(time.Now()) {
			t.Errorf("lease_expires_at %v is not in the future", d.LeaseExpiresAt)
		}
		if d.EventType != NotifyEventCertExpiring {
			t.Errorf("event_type = %q, want certificate.expiring", d.EventType)
		}
		if d.Payload.CommonName != "fixture" {
			t.Errorf("payload not carried through lease: %+v", d.Payload)
		}
	}
	if len(seen) != rows {
		t.Errorf("leased %d of %d deliveries", len(seen), rows)
	}
}

// TestDeliveryBackoffSchedule은 백오프 계산을 고정한다 (설계 6.1).
func TestDeliveryBackoffSchedule(t *testing.T) {
	cases := []struct {
		attempt int
		want    time.Duration
	}{
		{0, time.Minute}, // 방어적 하한
		{1, time.Minute},
		{2, 2 * time.Minute},
		{3, 4 * time.Minute},
		{4, 8 * time.Minute},
		{5, 16 * time.Minute},
		{10, time.Hour}, // 상한
		{40, time.Hour}, // 상한 (오버플로 없음)
	}
	for _, c := range cases {
		if got := deliveryBackoff(c.attempt); got != c.want {
			t.Errorf("deliveryBackoff(%d) = %v, want %v", c.attempt, got, c.want)
		}
	}
}

// TestMarkDeliveryFailedBacksOffThenFails는 SQL의 백오프 식이 deliveryBackoff와
// 같은 값을 내는지, 그리고 maxDeliveryAttempts회째 실패가 failed로 종결되는지 본다.
func TestMarkDeliveryFailedBacksOffThenFails(t *testing.T) {
	pool := testPool(t)
	st := New(pool)
	ctx := context.Background()

	eventID := newRawEvent(t, pool)
	id := newTestDelivery(t, pool, eventID, NotifyChannelWebhook, "")

	for attempt := 1; attempt < maxDeliveryAttempts; attempt++ {
		before := time.Now()
		a, err := st.MarkDeliveryFailed(ctx, id, "connection refused", false)
		if err != nil {
			t.Fatalf("attempt %d: %v", attempt, err)
		}
		if a.AttemptCount != attempt {
			t.Fatalf("attempt_count = %d, want %d", a.AttemptCount, attempt)
		}
		if a.Status != DeliveryStatusPending {
			t.Fatalf("status = %q, want pending on attempt %d", a.Status, attempt)
		}
		want := before.Add(deliveryBackoff(attempt))
		if d := a.NextAttemptAt.Sub(want); d > 30*time.Second || d < -30*time.Second {
			t.Errorf("attempt %d next_attempt_at off by %v (want ~%v)", attempt, d, deliveryBackoff(attempt))
		}
	}

	a, err := st.MarkDeliveryFailed(ctx, id, "connection refused", false)
	if err != nil {
		t.Fatalf("final attempt: %v", err)
	}
	if a.AttemptCount != maxDeliveryAttempts || a.Status != DeliveryStatusFailed {
		t.Errorf("final = (%d, %q), want (%d, failed)", a.AttemptCount, a.Status, maxDeliveryAttempts)
	}
}

// TestMarkDeliveryPermanentFailureAndTruncation은 영구 실패가 재시도 없이
// failed로 끝나고 last_error가 500자로 절단되는지 검증한다.
func TestMarkDeliveryPermanentFailureAndTruncation(t *testing.T) {
	pool := testPool(t)
	st := New(pool)
	ctx := context.Background()

	eventID := newRawEvent(t, pool)
	id := newTestDelivery(t, pool, eventID, NotifyChannelEmail, "perm@example.test")

	long := strings.Repeat("가", 900) // 룬 단위 절단 확인 (바이트로 자르면 깨진다)
	a, err := st.MarkDeliveryFailed(ctx, id, long, true)
	if err != nil {
		t.Fatalf("permanent failure: %v", err)
	}
	if a.Status != DeliveryStatusFailed {
		t.Errorf("status = %q, want failed", a.Status)
	}
	var stored string
	if err := pool.QueryRow(`SELECT last_error FROM notification_deliveries WHERE id = $1::uuid`,
		id).Scan(&stored); err != nil {
		t.Fatalf("read last_error: %v", err)
	}
	if n := len([]rune(stored)); n != maxDeliveryErrorRunes {
		t.Errorf("last_error rune length = %d, want %d", n, maxDeliveryErrorRunes)
	}
	if !strings.HasPrefix(stored, "가가") {
		t.Error("last_error was truncated mid-rune")
	}
}

// TestReapExpiredDeliveryLeases는 고아 딜리버리가 failed가 아니라 pending으로
// 회수되는지 검증한다 (설계 8 — 스캔 Job과 회수 의미론이 다르다).
func TestReapExpiredDeliveryLeases(t *testing.T) {
	pool := testPool(t)
	st := New(pool)
	ctx := context.Background()

	eventID := newRawEvent(t, pool)
	id := newTestDelivery(t, pool, eventID, NotifyChannelSyslog, "")
	if _, err := pool.Exec(`
		UPDATE notification_deliveries
		   SET status = 'sending', leased_at = now() - interval '10 minutes',
		       lease_expires_at = now() - interval '5 minutes'
		 WHERE id = $1::uuid`, id); err != nil {
		t.Fatalf("simulate orphan: %v", err)
	}

	if _, err := st.ReapExpiredDeliveryLeases(ctx); err != nil {
		t.Fatalf("reap: %v", err)
	}
	var status string
	var leaseExpires sql.NullTime
	var attempts int
	if err := pool.QueryRow(`
		SELECT status, lease_expires_at, attempt_count FROM notification_deliveries WHERE id = $1::uuid`,
		id).Scan(&status, &leaseExpires, &attempts); err != nil {
		t.Fatalf("read delivery: %v", err)
	}
	if status != DeliveryStatusPending {
		t.Errorf("status = %q, want pending", status)
	}
	if leaseExpires.Valid {
		t.Error("lease_expires_at was not cleared")
	}
	if attempts != 0 {
		t.Errorf("attempt_count = %d, want 0 (리스 만료는 시도로 세지 않는다)", attempts)
	}
}

// TestMarkDeliverySent는 성공 기록과 상태 가드를 검증한다.
func TestMarkDeliverySent(t *testing.T) {
	pool := testPool(t)
	st := New(pool)
	ctx := context.Background()

	eventID := newRawEvent(t, pool)
	id := newTestDelivery(t, pool, eventID, NotifyChannelInbox, "u1")
	if err := st.MarkDeliverySent(ctx, id); err != nil {
		t.Fatalf("mark sent: %v", err)
	}
	var status string
	var sentAt sql.NullTime
	if err := pool.QueryRow(`SELECT status, sent_at FROM notification_deliveries WHERE id = $1::uuid`,
		id).Scan(&status, &sentAt); err != nil {
		t.Fatalf("read delivery: %v", err)
	}
	if status != DeliveryStatusSent || !sentAt.Valid {
		t.Errorf("(status, sent_at) = (%q, %v), want (sent, set)", status, sentAt.Valid)
	}
	if err := st.MarkDeliverySent(ctx, id); !errors.Is(err, ErrDeliveryState) {
		t.Errorf("re-mark sent error = %v, want ErrDeliveryState", err)
	}
}

// ===== 알림함 =====

// TestInboxUniqueAbsorbsDuplicate는 발송 재시도로 같은 사건이 같은 사용자에게
// 두 번 꽂히지 않음을 검증한다 (설계 5.4·13).
func TestInboxUniqueAbsorbsDuplicate(t *testing.T) {
	pool := testPool(t)
	st := New(pool)
	ctx := context.Background()

	userID := newNotifyUser(t, pool, "notify-inbox-"+randomHex(t, 4), "viewer", "")
	eventID := newRawEvent(t, pool)
	in := InboxInsert{
		UserID: userID, EventID: eventID,
		Title: "[인증서 만료 D-7] fixture", Body: "본문", Severity: "error",
	}

	created, err := st.InsertInboxItem(ctx, in)
	if err != nil {
		t.Fatalf("first insert: %v", err)
	}
	if !created {
		t.Error("first insert reported created = false")
	}
	created, err = st.InsertInboxItem(ctx, in)
	if err != nil {
		t.Fatalf("second insert: %v", err)
	}
	if created {
		t.Error("duplicate insert reported created = true")
	}
	var n int
	if err := pool.QueryRow(`SELECT count(*) FROM notification_inbox WHERE user_id = $1::uuid`,
		userID).Scan(&n); err != nil {
		t.Fatalf("count inbox: %v", err)
	}
	if n != 1 {
		t.Errorf("inbox rows = %d, want 1", n)
	}
}

// TestInboxReadArchiveAndOwnership은 목록 필터·배지 카운트·읽음·보관과
// "본인 것만" 강제를 검증한다.
func TestInboxReadArchiveAndOwnership(t *testing.T) {
	pool := testPool(t)
	st := New(pool)
	ctx := context.Background()

	mine := newNotifyUser(t, pool, "notify-mine-"+randomHex(t, 4), "viewer", "")
	other := newNotifyUser(t, pool, "notify-other-"+randomHex(t, 4), "viewer", "")
	e1, e2 := newRawEvent(t, pool), newRawEvent(t, pool)
	for _, ev := range []string{e1, e2} {
		if _, err := st.InsertInboxItem(ctx, InboxInsert{
			UserID: mine, EventID: ev, Title: "t", Body: "b", Severity: "info"}); err != nil {
			t.Fatalf("insert inbox: %v", err)
		}
	}

	unread, err := st.CountUnreadInbox(ctx, mine)
	if err != nil {
		t.Fatalf("count unread: %v", err)
	}
	if unread != 2 {
		t.Fatalf("unread = %d, want 2", unread)
	}

	list, err := st.ListInbox(ctx, mine, InboxFilterUnread, 1, 50)
	if err != nil {
		t.Fatalf("list inbox: %v", err)
	}
	if list.Total != 2 || len(list.Items) != 2 {
		t.Fatalf("unread list total = %d, items = %d, want 2/2", list.Total, len(list.Items))
	}

	// 남의 알림은 존재 여부를 흘리지 않고 ErrNotFound.
	if err := st.MarkInboxRead(ctx, other, list.Items[0].ID); !errors.Is(err, ErrNotFound) {
		t.Errorf("cross-user read error = %v, want ErrNotFound", err)
	}
	if err := st.MarkInboxRead(ctx, mine, list.Items[0].ID); err != nil {
		t.Fatalf("mark read: %v", err)
	}
	if n, _ := st.CountUnreadInbox(ctx, mine); n != 1 {
		t.Errorf("unread after read = %d, want 1", n)
	}

	if err := st.ArchiveInboxItem(ctx, mine, list.Items[1].ID); err != nil {
		t.Fatalf("archive: %v", err)
	}
	archived, err := st.ListInbox(ctx, mine, InboxFilterArchived, 1, 50)
	if err != nil {
		t.Fatalf("list archived: %v", err)
	}
	if archived.Total != 1 {
		t.Errorf("archived total = %d, want 1", archived.Total)
	}
	all, err := st.ListInbox(ctx, mine, InboxFilterAll, 1, 50)
	if err != nil {
		t.Fatalf("list all: %v", err)
	}
	if all.Total != 1 {
		t.Errorf("all(=보관 제외) total = %d, want 1", all.Total)
	}

	if n, err := st.MarkAllInboxRead(ctx, mine); err != nil || n != 0 {
		t.Errorf("mark all read = (%d, %v), want (0, nil)", n, err)
	}
	if n, _ := st.CountUnreadInbox(ctx, mine); n != 0 {
		t.Errorf("unread at end = %d, want 0", n)
	}
	otherList, err := st.ListInbox(ctx, other, InboxFilterAll, 1, 50)
	if err != nil {
		t.Fatalf("list other inbox: %v", err)
	}
	if otherList.Total != 0 {
		t.Errorf("other user sees %d items, want 0", otherList.Total)
	}
}

// ===== 이력 조회 · 보존 =====

// TestListNotificationDeliveriesFilters는 이력 조회 필터를 검증한다.
func TestListNotificationDeliveriesFilters(t *testing.T) {
	pool := testPool(t)
	st := New(pool)
	ctx := context.Background()

	eventID := newRawEvent(t, pool)
	newTestDelivery(t, pool, eventID, NotifyChannelEmail, "a@example.test")
	newTestDelivery(t, pool, eventID, NotifyChannelSyslog, "")

	list, err := st.ListNotificationDeliveries(ctx, ListDeliveriesInput{EventID: eventID})
	if err != nil {
		t.Fatalf("list deliveries: %v", err)
	}
	if list.Total != 2 {
		t.Fatalf("total = %d, want 2", list.Total)
	}
	byChannel, err := st.ListNotificationDeliveries(ctx, ListDeliveriesInput{
		EventID: eventID, Channel: NotifyChannelSyslog})
	if err != nil {
		t.Fatalf("list by channel: %v", err)
	}
	if byChannel.Total != 1 || byChannel.Items[0].Channel != NotifyChannelSyslog {
		t.Errorf("channel filter returned %d items", byChannel.Total)
	}
	if byChannel.Items[0].EventType != NotifyEventCertExpiring {
		t.Errorf("event_type = %q, want certificate.expiring", byChannel.Items[0].EventType)
	}
}

// TestPurgeNotificationsKeepsLiveRows는 보존 정리가 종료 상태만 지우고
// 진행 중 딜리버리·안 읽은 알림함은 나이와 무관하게 남기는지 검증한다 (설계 7.6).
func TestPurgeNotificationsKeepsLiveRows(t *testing.T) {
	pool := testPool(t)
	st := New(pool)
	ctx := context.Background()

	userID := newNotifyUser(t, pool, "notify-purge-"+randomHex(t, 4), "viewer", "")
	oldEvent := newRawEvent(t, pool)
	liveEvent := newRawEvent(t, pool)
	sent := newTestDelivery(t, pool, oldEvent, NotifyChannelEmail, "old@example.test")
	pending := newTestDelivery(t, pool, liveEvent, NotifyChannelEmail, "live@example.test")
	if _, err := st.InsertInboxItem(ctx, InboxInsert{
		UserID: userID, EventID: liveEvent, Title: "t", Body: "b", Severity: "info"}); err != nil {
		t.Fatalf("insert inbox: %v", err)
	}

	// 두 이벤트와 종료 딜리버리를 보존 기간 밖으로 밀어낸다.
	if _, err := pool.Exec(`
		UPDATE notification_deliveries SET status = 'sent', sent_at = now(),
		       created_at = now() - interval '400 days' WHERE id = $1::uuid`, sent); err != nil {
		t.Fatalf("age delivery: %v", err)
	}
	if _, err := pool.Exec(
		`UPDATE notification_events SET created_at = now() - interval '400 days' WHERE id = ANY($1::uuid[])`,
		"{"+oldEvent+","+liveEvent+"}"); err != nil {
		t.Fatalf("age events: %v", err)
	}

	res, err := st.PurgeNotifications(ctx, 180)
	if err != nil {
		t.Fatalf("purge: %v", err)
	}
	if res.Deliveries < 1 {
		t.Errorf("purged %d deliveries, want >= 1", res.Deliveries)
	}

	var n int
	if err := pool.QueryRow(`SELECT count(*) FROM notification_deliveries WHERE id = $1::uuid`,
		pending).Scan(&n); err != nil {
		t.Fatalf("count pending delivery: %v", err)
	}
	if n != 1 {
		t.Error("purge deleted a delivery that is still in progress")
	}
	if err := pool.QueryRow(`SELECT count(*) FROM notification_events WHERE id = $1::uuid`,
		liveEvent).Scan(&n); err != nil {
		t.Fatalf("count live event: %v", err)
	}
	if n != 1 {
		t.Error("purge deleted an event that still has references (inbox/delivery)")
	}
	if err := pool.QueryRow(`SELECT count(*) FROM notification_events WHERE id = $1::uuid`,
		oldEvent).Scan(&n); err != nil {
		t.Fatalf("count old event: %v", err)
	}
	if n != 0 {
		t.Error("purge kept an old event with no remaining references")
	}
}

// ===== 정책 CRUD =====

// TestNotificationPolicyCRUD는 생성·수정·삭제와 감사 3종을 검증한다 (하드 규칙 5).
func TestNotificationPolicyCRUD(t *testing.T) {
	pool := testPool(t)
	st := New(pool)
	ctx := context.Background()

	actorID := newNotifyUser(t, pool, "notify-actor-"+randomHex(t, 4), "security_administrator", "")
	reqID := "test-req-notify-crud-" + randomHex(t, 6)
	t.Cleanup(func() { cleanupAuditByRequestID(pool, reqID) })

	name := "정책-" + randomHex(t, 6)
	p, err := st.CreateNotificationPolicy(ctx, NotificationPolicyInput{
		Name:       name,
		Enabled:    true,
		EventTypes: []string{NotifyEventCertExpiring},
		Thresholds: []int{90, 30, 7},
		Channels:   []string{NotifyChannelInbox, NotifyChannelEmail},
		Recipients: []NotificationRecipient{
			{Kind: RecipientOwner},
			{Kind: RecipientRole, Role: "system_administrator"},
			{Kind: RecipientStatic, Email: "noc@example.test"},
		},
		ActorID:   &actorID,
		RequestID: &reqID,
	})
	if err != nil {
		t.Fatalf("create policy: %v", err)
	}
	t.Cleanup(func() {
		_, _ = pool.Exec(`DELETE FROM notification_policies WHERE id = $1::uuid`, p.ID)
		cleanupAuditByTargetID(pool, p.ID)
	})
	if len(p.Thresholds) != 3 || p.Thresholds[0] != 90 {
		t.Errorf("thresholds round trip = %v", p.Thresholds)
	}
	if len(p.Recipients) != 3 || p.Recipients[2].Email != "noc@example.test" {
		t.Errorf("recipients round trip = %+v", p.Recipients)
	}
	if p.FilterEnvironments != nil {
		t.Errorf("empty filter should be NULL, got %v", p.FilterEnvironments)
	}
	if p.CreatedBy == nil || *p.CreatedBy != actorID {
		t.Errorf("created_by = %v, want the actor", p.CreatedBy)
	}

	// 이름 중복은 전용 오류로 구분된다(핸들러가 409로 매핑).
	if _, err := st.CreateNotificationPolicy(ctx, NotificationPolicyInput{
		Name: name, EventTypes: []string{NotifyEventCertExpiring},
		Channels: []string{NotifyChannelInbox}, RequestID: &reqID,
	}); !errors.Is(err, ErrNotificationPolicyNameTaken) {
		t.Errorf("duplicate name error = %v, want ErrNotificationPolicyNameTaken", err)
	}

	disabled := false
	updated, err := st.UpdateNotificationPolicy(ctx, p.ID, NotificationPolicyPatch{
		Enabled: &disabled, ActorID: &actorID, RequestID: &reqID,
	})
	if err != nil {
		t.Fatalf("update policy: %v", err)
	}
	if updated.Enabled {
		t.Error("enabled was not updated")
	}
	if len(updated.Thresholds) != 3 {
		t.Errorf("patch clobbered untouched fields: %v", updated.Thresholds)
	}
	if !updated.UpdatedAt.After(p.UpdatedAt) {
		t.Error("updated_at was not bumped")
	}

	// 알 수 없는 채널·수신자는 거부한다.
	bad := []string{"pigeon"}
	if _, err := st.UpdateNotificationPolicy(ctx, p.ID, NotificationPolicyPatch{
		Channels: &bad, RequestID: &reqID}); err == nil {
		t.Error("unknown channel was accepted")
	}

	if err := st.DeleteNotificationPolicy(ctx, p.ID, &actorID, &reqID, nil); err != nil {
		t.Fatalf("delete policy: %v", err)
	}
	if _, err := st.GetNotificationPolicy(ctx, p.ID); !errors.Is(err, ErrNotFound) {
		t.Errorf("get after delete = %v, want ErrNotFound", err)
	}

	var audits int
	if err := pool.QueryRow(`
		SELECT count(*) FROM audit_events
		 WHERE request_id = $1 AND event_type IN ($2, $3, $4)`,
		reqID, EventNotificationPolicyCreate, EventNotificationPolicyUpdate,
		EventNotificationPolicyDelete).Scan(&audits); err != nil {
		t.Fatalf("count audit events: %v", err)
	}
	if audits != 3 {
		t.Errorf("audit events = %d, want 3 (create/update/delete)", audits)
	}

	// 감사 값에 static 수신자의 메일 주소를 복제하지 않는다.
	var afterValue string
	if err := pool.QueryRow(`
		SELECT after_value::text FROM audit_events
		 WHERE request_id = $1 AND event_type = $2`, reqID, EventNotificationPolicyCreate).
		Scan(&afterValue); err != nil {
		t.Fatalf("read audit after_value: %v", err)
	}
	if strings.Contains(afterValue, "noc@example.test") {
		t.Error("audit value copied a recipient email address")
	}
}
