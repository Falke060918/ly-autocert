package store

// 서버(엔드포인트) 축 조회 규약 고정.
// 픽스처는 전부 실행 시점에 만든다 — 실제 인증서·키를 커밋하지 않는다
// (보안 하드 규칙 4). 지문은 테스트 라벨의 결정적 SHA-256 합성값이다.

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"errors"
	"testing"
	"time"
)

// srvFixturePrefix는 이 파일의 픽스처를 다른 테스트가 남긴 행과 가르는 표식이다.
// 목록 조회는 전부 q=이 접두사로 좁혀 실행한다(테스트 간 격리).
const srvFixturePrefix = "srvaxis-"

func srvFingerprint(label string) string {
	sum := sha256.Sum256([]byte("servers-axis-fixture:" + label))
	return hex.EncodeToString(sum[:])
}

// insertSrvCertificate는 픽스처 인증서 1건을 넣고 id를 반환한다.
func insertSrvCertificate(t *testing.T, pool *sql.DB, label string, notAfter time.Time) string {
	t.Helper()
	cn := srvFixturePrefix + label + ".example.test"
	var id string
	if err := pool.QueryRow(`
		INSERT INTO certificates (
			fingerprint_sha256, serial_number, subject_dn, issuer_dn, common_name, sans,
			not_before, not_after, public_key_algorithm, public_key_size,
			signature_algorithm, certificate_type, status
		) VALUES ($1, $2, $3, 'CN=srvaxis-fixture-ca', $4, ARRAY[$4::text],
			$5, $6, 'ECDSA', 256, 'ECDSA-SHA256', 'server', 'active')
		RETURNING id::text`,
		srvFingerprint(label), "srvaxis-"+label, "CN="+cn, cn,
		notAfter.Add(-365*24*time.Hour), notAfter, // not_before < not_after (validity_order CHECK)
	).Scan(&id); err != nil {
		t.Fatalf("insert certificate %s: %v", label, err)
	}
	return id
}

// insertSrvTarget은 픽스처 대상(호스트) 1건을 넣고 id를 반환한다.
func insertSrvTarget(t *testing.T, pool *sql.DB, label, ip, scope string) string {
	t.Helper()
	host := srvFixturePrefix + label
	var id string
	if err := pool.QueryRow(`
		INSERT INTO targets (name, target_type, hostname, ip_address, executor_scope)
		VALUES ($1, 'network', $1, $2::inet, $3) RETURNING id::text`,
		host, ip, scope,
	).Scan(&id); err != nil {
		t.Fatalf("insert target %s: %v", label, err)
	}
	return id
}

func insertSrvLocation(t *testing.T, pool *sql.DB, certID, targetID string, port int, verified time.Time) {
	t.Helper()
	if _, err := pool.Exec(`
		INSERT INTO certificate_locations
			(certificate_id, target_id, location_type, port, last_verified_at, deployment_status, tls_version)
		VALUES ($1::uuid, $2::uuid, 'network_endpoint', $3, $4, 'deployed', 'TLS 1.2')`,
		certID, targetID, port, verified); err != nil {
		t.Fatalf("insert location: %v", err)
	}
}

// insertSrvMonitor는 재스캔 모니터 1행을 넣는다. fingerprint가 빈 문자열이면
// current_fingerprint를 NULL로 둔다 — 재스캔 OFF/미실행 상태의 재현이다.
func insertSrvMonitor(t *testing.T, pool *sql.DB, targetID string, port int, fingerprint string, lastSuccess time.Time) {
	t.Helper()
	var fp *string
	if fingerprint != "" {
		fp = &fingerprint
	}
	if _, err := pool.Exec(`
		INSERT INTO endpoint_monitors (target_id, port, current_fingerprint, current_streak, last_success_at, last_probe_at)
		VALUES ($1::uuid, $2, $3, 1, $4, $4)`,
		targetID, port, fp, lastSuccess); err != nil {
		t.Fatalf("insert endpoint monitor: %v", err)
	}
}

// insertSrvSensor는 센서 스코프 표기를 검증하기 위한 sensors 행을 만든다.
// 토큰 해시는 형식(소문자 hex 64자)만 맞춘 합성값이다 — 실제 토큰이 아니다.
func insertSrvSensor(t *testing.T, pool *sql.DB, name string) string {
	t.Helper()
	var id string
	if err := pool.QueryRow(`
		INSERT INTO sensors (name, status, enrollment_token_hash, token_expires_at)
		VALUES ($1, 'active', $2, now() + interval '1 day') RETURNING id::text`,
		name, srvFingerprint("sensor-token:"+name),
	).Scan(&id); err != nil {
		t.Fatalf("insert sensor: %v", err)
	}
	return id
}

// srvFixture는 서버 축 테스트가 공유하는 인벤토리다.
type srvFixture struct {
	certNear, certFar, certExpired string
	one, two, three, four, five    string
	sensorID                       string
}

// setupServerAxisFixture는 아래 인벤토리를 만든다.
//
//	one   203.0.113.201 :443  cert Near   (모니터 지문 = Near)      → 관측 확정
//	two   203.0.113.202 :443  cert Near   (모니터 지문 NULL)        → 추정
//	three 203.0.113.203 :8443 cert Far    (모니터 지문 = Far) + 구버전 Expired 위치 잔존
//	four  203.0.113.204 :9443 인증서 없음 (모니터만, 지문 NULL)      → risk NULL
//	five  203.0.113.205 :443  cert Expired(모니터 지문 = Expired)   → 센서 스코프
//
// 같은 인증서(Near)가 one·two 두 호스트에 배포된 상태가 이 축의 존재 이유다.
func setupServerAxisFixture(t *testing.T, pool *sql.DB) srvFixture {
	t.Helper()
	now := time.Now()
	t.Cleanup(func() {
		_, _ = pool.Exec(`DELETE FROM targets WHERE name LIKE $1`, srvFixturePrefix+"%")
		_, _ = pool.Exec(`DELETE FROM certificates WHERE common_name LIKE $1`, srvFixturePrefix+"%")
		_, _ = pool.Exec(`DELETE FROM sensors WHERE name LIKE $1`, srvFixturePrefix+"%")
	})

	f := srvFixture{
		certNear:    insertSrvCertificate(t, pool, "near", now.Add(10*24*time.Hour)),
		certFar:     insertSrvCertificate(t, pool, "far", now.Add(200*24*time.Hour)),
		certExpired: insertSrvCertificate(t, pool, "expired", now.Add(-5*24*time.Hour)),
	}
	f.sensorID = insertSrvSensor(t, pool, srvFixturePrefix+"sensor-a")

	f.one = insertSrvTarget(t, pool, "one", "203.0.113.201", "server")
	insertSrvLocation(t, pool, f.certNear, f.one, 443, now.Add(-time.Hour))
	insertSrvMonitor(t, pool, f.one, 443, srvFingerprint("near"), now.Add(-time.Hour))

	f.two = insertSrvTarget(t, pool, "two", "203.0.113.202", "server")
	insertSrvLocation(t, pool, f.certNear, f.two, 443, now.Add(-2*time.Hour))
	insertSrvMonitor(t, pool, f.two, 443, "", now.Add(-2*time.Hour))

	f.three = insertSrvTarget(t, pool, "three", "203.0.113.203", "server")
	insertSrvLocation(t, pool, f.certExpired, f.three, 8443, now.Add(-30*24*time.Hour))
	insertSrvLocation(t, pool, f.certFar, f.three, 8443, now.Add(-3*time.Hour))
	insertSrvMonitor(t, pool, f.three, 8443, srvFingerprint("far"), now.Add(-3*time.Hour))

	f.four = insertSrvTarget(t, pool, "four", "203.0.113.204", "server")
	insertSrvMonitor(t, pool, f.four, 9443, "", now.Add(-4*time.Hour))

	f.five = insertSrvTarget(t, pool, "five", "203.0.113.205", "sensor:"+f.sensorID)
	insertSrvLocation(t, pool, f.certExpired, f.five, 443, now.Add(-5*time.Hour))
	insertSrvMonitor(t, pool, f.five, 443, srvFingerprint("expired"), now.Add(-5*time.Hour))
	return f
}

// listAxis는 픽스처 범위로 좁힌 목록 조회다(다른 테스트가 남긴 행과 격리).
func listAxis(t *testing.T, st *Store, in ListServersInput) *ServerList {
	t.Helper()
	in.Search = srvFixturePrefix
	res, err := st.ListServers(context.Background(), in)
	if err != nil {
		t.Fatalf("list servers: %v", err)
	}
	return res
}

func hostnamesOf(items []ServerListItem) []string {
	out := make([]string, 0, len(items))
	for _, it := range items {
		if it.Hostname == nil {
			out = append(out, "<nil>")
			continue
		}
		out = append(out, *it.Hostname)
	}
	return out
}

func findServer(items []ServerListItem, id string) *ServerListItem {
	for i := range items {
		if items[i].ID == id {
			return &items[i]
		}
	}
	return nil
}

// TestListServersSplitsSharedCertificateByHost는 이 축의 존재 이유를 고정한다 —
// 같은 인증서가 두 호스트에 배포돼 있으면 서버 행이 둘로 갈라져야 한다.
func TestListServersSplitsSharedCertificateByHost(t *testing.T) {
	pool := testPool(t)
	st := New(pool)
	f := setupServerAxisFixture(t, pool)

	res := listAxis(t, st, ListServersInput{})
	if res.Total != 5 || len(res.Items) != 5 {
		t.Fatalf("servers = total %d items %d, want 5 (%v)", res.Total, len(res.Items), hostnamesOf(res.Items))
	}
	one, two := findServer(res.Items, f.one), findServer(res.Items, f.two)
	if one == nil || two == nil {
		t.Fatalf("shared certificate did not split into two hosts: %v", hostnamesOf(res.Items))
	}
	for _, it := range []*ServerListItem{one, two} {
		if it.EndpointCount != 1 || it.CertificateCount != 1 {
			t.Errorf("%s = endpoints %d certificates %d, want 1/1", *it.Hostname, it.EndpointCount, it.CertificateCount)
		}
		if len(it.Ports) != 1 || it.Ports[0] != 443 {
			t.Errorf("%s ports = %v, want [443]", *it.Hostname, it.Ports)
		}
		if it.EarliestNotAfter == nil {
			t.Errorf("%s earliest_not_after = nil, want the shared certificate's expiry", *it.Hostname)
		}
	}
	if one.IPAddress == nil || *one.IPAddress != "203.0.113.201" {
		t.Errorf("one ip = %v, want 203.0.113.201", one.IPAddress)
	}
	if one.Scope != "server" || one.SensorName != nil {
		t.Errorf("one scope = %q sensor %v, want server/nil", one.Scope, one.SensorName)
	}
	// 센서 스코프는 이름을 실어 보낸다 — 화면이 uuid를 그대로 띄우지 않게.
	five := findServer(res.Items, f.five)
	if five == nil || five.SensorName == nil || *five.SensorName != srvFixturePrefix+"sensor-a" {
		t.Errorf("five sensor_name = %v, want %s", five, srvFixturePrefix+"sensor-a")
	}
}

// TestListServersRiskSortPutsUnknownExpiryLast는 risk 정렬이 min(not_after)
// 오름차순이고 만료 시각을 모르는 서버를 **방향과 무관하게** 뒤로 보내는지 본다.
func TestListServersRiskSortPutsUnknownExpiryLast(t *testing.T) {
	pool := testPool(t)
	st := New(pool)
	f := setupServerAxisFixture(t, pool)

	asc := listAxis(t, st, ListServersInput{SortField: "risk"})
	if len(asc.Items) != 5 {
		t.Fatalf("items = %d, want 5", len(asc.Items))
	}
	// 이미 만료된 five가 맨 앞, 인증서를 못 정한 four가 맨 뒤.
	if asc.Items[0].ID != f.five {
		t.Errorf("risk asc head = %v, want five(expired)", hostnamesOf(asc.Items))
	}
	if asc.Items[len(asc.Items)-1].ID != f.four {
		t.Errorf("risk asc tail = %v, want four(unknown expiry) last", hostnamesOf(asc.Items))
	}
	desc := listAxis(t, st, ListServersInput{SortField: "risk", SortDesc: true})
	if desc.Items[len(desc.Items)-1].ID != f.four {
		t.Errorf("risk desc tail = %v, want four(unknown expiry) last even descending", hostnamesOf(desc.Items))
	}
	if _, err := st.ListServers(context.Background(), ListServersInput{SortField: "evil"}); err == nil {
		t.Error("sort field outside the whitelist was accepted")
	}
}

// TestListServersEstimatesCurrentCertificate는 current_fingerprint가 NULL일 때
// 가장 최근 확인된 live 위치로 추정하고 그 사실을 응답에 싣는지 본다.
func TestListServersEstimatesCurrentCertificate(t *testing.T) {
	pool := testPool(t)
	st := New(pool)
	f := setupServerAxisFixture(t, pool)
	ctx := context.Background()

	res := listAxis(t, st, ListServersInput{})
	one, two := findServer(res.Items, f.one), findServer(res.Items, f.two)
	if one.EstimatedCount != 0 {
		t.Errorf("one estimated_count = %d, want 0 (관측 지문이 있다)", one.EstimatedCount)
	}
	if two.EstimatedCount != 1 {
		t.Errorf("two estimated_count = %d, want 1 (지문 없음 → 추정)", two.EstimatedCount)
	}
	// 추정이어도 만료 지평선은 채워져야 한다 — 재스캔 OFF 서버가 목록에서
	// "만료 시각 모름"으로 사라지면 이 축의 값이 없다.
	if two.EarliestNotAfter == nil {
		t.Fatal("two earliest_not_after = nil, want the estimated certificate's expiry")
	}

	detail, err := st.GetServerDetail(ctx, f.two)
	if err != nil {
		t.Fatalf("get server detail: %v", err)
	}
	if len(detail.Endpoints) != 1 {
		t.Fatalf("two endpoints = %d, want 1", len(detail.Endpoints))
	}
	ep := detail.Endpoints[0]
	if !ep.CertificateEstimated {
		t.Error("two endpoint certificate_estimated = false, want true")
	}
	if ep.Certificate == nil || ep.Certificate.ID != f.certNear {
		t.Errorf("two endpoint certificate = %v, want the near certificate", ep.Certificate)
	}
	confirmed, err := st.GetServerDetail(ctx, f.one)
	if err != nil {
		t.Fatalf("get server detail: %v", err)
	}
	if confirmed.Endpoints[0].CertificateEstimated {
		t.Error("one endpoint certificate_estimated = true, want false (관측 지문 확정)")
	}
	// 인증서를 하나도 특정하지 못한 엔드포인트는 추정도 아니다.
	none, err := st.GetServerDetail(ctx, f.four)
	if err != nil {
		t.Fatalf("get server detail: %v", err)
	}
	if len(none.Endpoints) != 1 || none.Endpoints[0].Certificate != nil || none.Endpoints[0].CertificateEstimated {
		t.Errorf("four endpoint = %+v, want certificate nil / estimated false", none.Endpoints)
	}
}

// TestListServersMismatchFilter는 구버전 잔존 판정을 고정한다.
func TestListServersMismatchFilter(t *testing.T) {
	pool := testPool(t)
	st := New(pool)
	f := setupServerAxisFixture(t, pool)

	yes := true
	only := listAxis(t, st, ListServersInput{Mismatch: &yes})
	if len(only.Items) != 1 || only.Items[0].ID != f.three {
		t.Fatalf("mismatch=true = %v, want only three", hostnamesOf(only.Items))
	}
	if only.Items[0].StaleCount != 1 {
		t.Errorf("three stale_count = %d, want 1", only.Items[0].StaleCount)
	}
	// 현재 인증서는 새 것이고 옛 것은 잔존으로만 센다.
	if only.Items[0].CertificateCount != 1 {
		t.Errorf("three certificate_count = %d, want 1 (구버전은 세지 않는다)", only.Items[0].CertificateCount)
	}
	no := false
	rest := listAxis(t, st, ListServersInput{Mismatch: &no})
	if len(rest.Items) != 4 {
		t.Errorf("mismatch=false = %v, want the other four", hostnamesOf(rest.Items))
	}

	detail, err := st.GetServerDetail(context.Background(), f.three)
	if err != nil {
		t.Fatalf("get server detail: %v", err)
	}
	if len(detail.Endpoints) != 1 || len(detail.Endpoints[0].StaleLocations) != 1 {
		t.Fatalf("three endpoints = %+v, want 1 endpoint with 1 stale location", detail.Endpoints)
	}
	if detail.Endpoints[0].StaleLocations[0].Certificate.ID != f.certExpired {
		t.Errorf("stale certificate = %v, want the expired one", detail.Endpoints[0].StaleLocations[0].Certificate)
	}
	if detail.Endpoints[0].Certificate == nil || detail.Endpoints[0].Certificate.ID != f.certFar {
		t.Errorf("three current certificate = %v, want the far one", detail.Endpoints[0].Certificate)
	}
}

// TestListServersExistsFilters는 만료 필터가 EXISTS 의미인지 본다.
func TestListServersExistsFilters(t *testing.T) {
	pool := testPool(t)
	st := New(pool)
	f := setupServerAxisFixture(t, pool)

	yes := true
	expired := listAxis(t, st, ListServersInput{Expired: &yes})
	if len(expired.Items) != 1 || expired.Items[0].ID != f.five {
		t.Errorf("expired=true = %v, want only five", hostnamesOf(expired.Items))
	}
	days := 30
	soon := listAxis(t, st, ListServersInput{ExpiresWithinDays: &days})
	if len(soon.Items) != 2 {
		t.Fatalf("expires_within_days=30 = %v, want one and two", hostnamesOf(soon.Items))
	}
	for _, it := range soon.Items {
		if it.ID != f.one && it.ID != f.two {
			t.Errorf("expires_within_days=30 included %v, want only one/two", *it.Hostname)
		}
	}
	scoped := listAxis(t, st, ListServersInput{Scope: "sensor:" + f.sensorID})
	if len(scoped.Items) != 1 || scoped.Items[0].ID != f.five {
		t.Errorf("scope filter = %v, want only five", hostnamesOf(scoped.Items))
	}
}

// TestListServersPageSizeBounds는 페이지 크기 경계를 고정한다.
// 상한 200은 서버에만 있어 로컬 검증이 전부 green이어도 실서버에서만 터진다.
func TestListServersPageSizeBounds(t *testing.T) {
	pool := testPool(t)
	st := New(pool)
	setupServerAxisFixture(t, pool)

	def := listAxis(t, st, ListServersInput{PageSize: 0})
	if def.PageSize != defaultPageSize {
		t.Errorf("page_size 0 = %d, want %d", def.PageSize, defaultPageSize)
	}
	clamped := listAxis(t, st, ListServersInput{PageSize: 1000})
	if clamped.PageSize != maxPageSize {
		t.Errorf("page_size 1000 = %d, want clamp to %d", clamped.PageSize, maxPageSize)
	}
	// total은 페이지와 같은 CTE 위에서 세므로 페이지를 잘라도 변하지 않는다.
	paged := listAxis(t, st, ListServersInput{PageSize: 2, Page: 2, SortField: "hostname"})
	if paged.Total != 5 || len(paged.Items) != 2 {
		t.Errorf("page 2 of 2 = total %d items %d, want total 5 / 2 items", paged.Total, len(paged.Items))
	}
	if paged.Page != 2 {
		t.Errorf("page = %d, want 2", paged.Page)
	}
}

// TestGetServerDetailNotFound는 잘못된 id 처리와 엔드포인트 0건 대상 처리를 가른다.
func TestGetServerDetailNotFound(t *testing.T) {
	pool := testPool(t)
	st := New(pool)
	ctx := context.Background()
	setupServerAxisFixture(t, pool)

	if _, err := st.GetServerDetail(ctx, "not-a-uuid"); !errors.Is(err, ErrNotFound) {
		t.Errorf("malformed id = %v, want ErrNotFound", err)
	}
	if _, err := st.GetServerDetail(ctx, "00000000-0000-0000-0000-000000000000"); !errors.Is(err, ErrNotFound) {
		t.Errorf("unknown id = %v, want ErrNotFound", err)
	}
	// 엔드포인트가 하나도 없는 대상은 404가 아니라 0건 서버다.
	bare := insertSrvTarget(t, pool, "bare", "203.0.113.206", "server")
	detail, err := st.GetServerDetail(ctx, bare)
	if err != nil {
		t.Fatalf("bare target detail: %v", err)
	}
	if detail.Server.EndpointCount != 0 || len(detail.Endpoints) != 0 {
		t.Errorf("bare target = %+v, want zero endpoints", detail.Server)
	}
}

// TestCertificateDetailCarriesCurrentEndpointCertificate는 인증서 상세의 위치
// 배열이 "서비스 중 / 다른 인증서로 교체됨 / 확인 안 됨" 3분류의 근거를 싣는지 본다.
func TestCertificateDetailCarriesCurrentEndpointCertificate(t *testing.T) {
	pool := testPool(t)
	st := New(pool)
	ctx := context.Background()
	f := setupServerAxisFixture(t, pool)

	// 서비스 중: one의 위치는 모니터 지문과 같은 인증서를 가리킨다.
	near, err := st.GetCertificateDetail(ctx, f.certNear)
	if err != nil {
		t.Fatalf("certificate detail: %v", err)
	}
	var served, unverified int
	for _, l := range near.Locations {
		switch {
		case l.CurrentFingerprint == nil:
			unverified++
		case l.CurrentCertificateID != nil && *l.CurrentCertificateID == f.certNear:
			served++
		}
	}
	if served != 1 || unverified != 1 {
		t.Errorf("near locations = served %d / unverified %d, want 1/1", served, unverified)
	}

	// 교체됨: expired 인증서의 three 위치는 모니터가 far를 가리킨다.
	old, err := st.GetCertificateDetail(ctx, f.certExpired)
	if err != nil {
		t.Fatalf("certificate detail: %v", err)
	}
	replaced := 0
	for _, l := range old.Locations {
		if l.CurrentCertificateID != nil && *l.CurrentCertificateID == f.certFar {
			replaced++
		}
	}
	if replaced != 1 {
		t.Errorf("expired locations replaced = %d, want 1", replaced)
	}
}
