package api

// 서버(엔드포인트) 축 API의 인증·권한·응답 형태·파라미터 검증.
// TEST_DATABASE_URL 미설정 시 스킵 (전용 테스트 DB — 메인 DB에 돌리지 않는다).
// 지문은 결정적 SHA-256(테스트 라벨) 합성값 — 실제 인증서를 커밋하지 않는다.

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"testing"
	"time"

	"github.com/Falke060918/INHERIT_AutoCert/backend/internal/auth"
	"github.com/Falke060918/INHERIT_AutoCert/backend/internal/domain"
)

type serverItemJSON struct {
	ID               string  `json:"id"`
	Hostname         *string `json:"hostname"`
	IPAddress        *string `json:"ip_address"`
	Scope            string  `json:"scope"`
	SensorName       *string `json:"sensor_name"`
	EndpointCount    int     `json:"endpoint_count"`
	CertificateCount int     `json:"certificate_count"`
	EarliestNotAfter *string `json:"earliest_not_after"`
	LastSeenAt       *string `json:"last_seen_at"`
	StaleCount       int     `json:"stale_count"`
	EstimatedCount   int     `json:"estimated_count"`
	Ports            []int   `json:"ports"`
}

func TestServersReadAPI(t *testing.T) {
	env := newTestEnv(t)
	ctx := context.Background()

	for _, stmt := range []string{
		`DELETE FROM endpoint_monitors`, `DELETE FROM certificate_locations`,
		`DELETE FROM certificates`, `DELETE FROM targets`,
	} {
		if _, err := env.pool.Exec(stmt); err != nil {
			t.Fatalf("cleanup %q: %v", stmt, err)
		}
	}

	// 최소 권한 역할(viewer)로 조회 가능해야 한다 (00-spec 7.11 — 인증서 조회와 같은 규약).
	viewerPW := randomPassword(t)
	if _, err := env.svc.CreateUser(ctx, "srv-viewer", viewerPW, nil, domain.RoleViewer, nil, auth.AuditMeta{}); err != nil {
		t.Fatalf("create viewer: %v", err)
	}
	viewerToken, status := env.login(t, "srv-viewer", viewerPW)
	if status != http.StatusOK {
		t.Fatalf("viewer login = %d, want 200", status)
	}

	// 픽스처: 인증서 1건이 두 호스트에 배포된 상태 + 한쪽만 관측 지문 보유.
	sum := sha256.Sum256([]byte("api-servers-fixture"))
	fingerprint := hex.EncodeToString(sum[:])
	var certID string
	if err := env.pool.QueryRow(`
		INSERT INTO certificates (
			fingerprint_sha256, serial_number, subject_dn, issuer_dn, common_name, sans,
			not_before, not_after, public_key_algorithm, public_key_size,
			signature_algorithm, certificate_type, status
		) VALUES ($1, 'srv-api-01', 'CN=srv.example.test', 'CN=fixture-ca', 'srv.example.test',
			ARRAY['srv.example.test'], $2, $3, 'ECDSA', 256, 'ECDSA-SHA256', 'server', 'active')
		RETURNING id::text`,
		fingerprint, time.Now().Add(-24*time.Hour), time.Now().Add(20*24*time.Hour),
	).Scan(&certID); err != nil {
		t.Fatalf("insert certificate: %v", err)
	}

	newTarget := func(name, ip string) string {
		var id string
		if err := env.pool.QueryRow(`
			INSERT INTO targets (name, target_type, hostname, ip_address, executor_scope)
			VALUES ($1, 'network', $1, $2::inet, 'server') RETURNING id::text`, name, ip).Scan(&id); err != nil {
			t.Fatalf("insert target %s: %v", name, err)
		}
		if _, err := env.pool.Exec(`
			INSERT INTO certificate_locations
				(certificate_id, target_id, location_type, port, last_verified_at, deployment_status)
			VALUES ($1::uuid, $2::uuid, 'network_endpoint', 443, now(), 'deployed')`, certID, id); err != nil {
			t.Fatalf("insert location %s: %v", name, err)
		}
		return id
	}
	confirmedID := newTarget("srv-api-confirmed", "203.0.113.221")
	estimatedID := newTarget("srv-api-estimated", "203.0.113.222")
	if _, err := env.pool.Exec(`
		INSERT INTO endpoint_monitors (target_id, port, current_fingerprint, current_streak, last_success_at, last_probe_at)
		VALUES ($1::uuid, 443, $2, 1, now(), now())`, confirmedID, fingerprint); err != nil {
		t.Fatalf("insert monitor: %v", err)
	}
	if _, err := env.pool.Exec(`
		INSERT INTO endpoint_monitors (target_id, port, last_success_at, last_probe_at)
		VALUES ($1::uuid, 443, now(), now())`, estimatedID); err != nil {
		t.Fatalf("insert monitor: %v", err)
	}

	t.Run("list without token returns 401", func(t *testing.T) {
		if resp := env.do(t, http.MethodGet, "/api/v1/servers", "", nil); resp.StatusCode != http.StatusUnauthorized {
			t.Errorf("list = %d, want 401", resp.StatusCode)
		}
	})

	t.Run("viewer lists servers as one row per host", func(t *testing.T) {
		resp := env.do(t, http.MethodGet, "/api/v1/servers", viewerToken, nil)
		if resp.StatusCode != http.StatusOK {
			t.Fatalf("list = %d, want 200", resp.StatusCode)
		}
		var out struct {
			RequestID string           `json:"request_id"`
			Items     []serverItemJSON `json:"items"`
			Total     int              `json:"total"`
			Page      int              `json:"page"`
			PageSize  int              `json:"page_size"`
		}
		if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
			t.Fatalf("decode list: %v", err)
		}
		if out.Total != 2 || len(out.Items) != 2 {
			t.Fatalf("servers = total %d items %d, want 2 (한 인증서가 두 호스트에 있다)", out.Total, len(out.Items))
		}
		if out.RequestID == "" || out.Page != 1 || out.PageSize != 50 {
			t.Errorf("envelope = %+v", out)
		}
		for _, it := range out.Items {
			if it.EndpointCount != 1 || it.CertificateCount != 1 || len(it.Ports) != 1 || it.Ports[0] != 443 {
				t.Errorf("item %s = %+v, want 1 endpoint/1 certificate/[443]", it.ID, it)
			}
			if it.EarliestNotAfter == nil {
				t.Errorf("item %s earliest_not_after = nil", it.ID)
			}
			want := 0
			if it.ID == estimatedID {
				want = 1
			}
			if it.EstimatedCount != want {
				t.Errorf("item %s estimated_count = %d, want %d", it.ID, it.EstimatedCount, want)
			}
		}
	})

	t.Run("empty result returns items [] not null", func(t *testing.T) {
		resp := env.do(t, http.MethodGet, "/api/v1/servers?q=no-such-host", viewerToken, nil)
		if resp.StatusCode != http.StatusOK {
			t.Fatalf("list = %d, want 200", resp.StatusCode)
		}
		var raw map[string]json.RawMessage
		if err := json.NewDecoder(resp.Body).Decode(&raw); err != nil {
			t.Fatalf("decode: %v", err)
		}
		if string(raw["items"]) != "[]" {
			t.Errorf("items = %s, want []", raw["items"])
		}
	})

	t.Run("detail expands endpoints and marks estimation", func(t *testing.T) {
		resp := env.do(t, http.MethodGet, "/api/v1/servers/"+estimatedID, viewerToken, nil)
		if resp.StatusCode != http.StatusOK {
			t.Fatalf("detail = %d, want 200", resp.StatusCode)
		}
		var out struct {
			RequestID string         `json:"request_id"`
			Server    serverItemJSON `json:"server"`
			Endpoints []struct {
				Port                 int  `json:"port"`
				CertificateEstimated bool `json:"certificate_estimated"`
				Certificate          *struct {
					ID                string `json:"id"`
					FingerprintSHA256 string `json:"fingerprint_sha256"`
				} `json:"certificate"`
				StaleLocations []json.RawMessage `json:"stale_locations"`
			} `json:"endpoints"`
		}
		if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
			t.Fatalf("decode detail: %v", err)
		}
		if out.Server.ID != estimatedID || len(out.Endpoints) != 1 {
			t.Fatalf("detail = %+v, want the estimated host with 1 endpoint", out.Server)
		}
		ep := out.Endpoints[0]
		if ep.Port != 443 || !ep.CertificateEstimated {
			t.Errorf("endpoint = %+v, want port 443 with certificate_estimated true", ep)
		}
		if ep.Certificate == nil || ep.Certificate.ID != certID {
			t.Errorf("endpoint certificate = %+v, want the fixture certificate", ep.Certificate)
		}
		if ep.StaleLocations == nil {
			t.Error("stale_locations = null, want []")
		}
	})

	t.Run("detail of unknown id returns 404", func(t *testing.T) {
		for _, id := range []string{"not-a-uuid", "00000000-0000-0000-0000-000000000000"} {
			resp := env.do(t, http.MethodGet, "/api/v1/servers/"+id, viewerToken, nil)
			if resp.StatusCode != http.StatusNotFound {
				t.Errorf("detail %s = %d, want 404", id, resp.StatusCode)
			}
		}
	})

	t.Run("invalid query params return 400", func(t *testing.T) {
		cases := map[string]string{
			"sort=evil":             "invalid_sort",
			"order=sideways":        "invalid_order",
			"expired=maybe":         "invalid_expired",
			"mismatch=maybe":        "invalid_mismatch",
			"scope=sensor:nope":     "invalid_scope",
			"expires_within_days=0": "invalid_expires_within_days",
			// page_size 상한 200은 서버에만 있다 — 프론트가 대량 조회로 우회하려 하면
			// 로컬 검증이 전부 green이어도 실서버에서만 400이 난다.
			"page_size=500": "invalid_page_size",
			"page_size=0":   "invalid_page_size",
		}
		for query, wantCode := range cases {
			resp := env.do(t, http.MethodGet, "/api/v1/servers?"+query, viewerToken, nil)
			if resp.StatusCode != http.StatusBadRequest {
				t.Errorf("%s = %d, want 400", query, resp.StatusCode)
				continue
			}
			var out struct {
				Error struct {
					Code string `json:"code"`
				} `json:"error"`
			}
			if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
				t.Fatalf("decode %s: %v", query, err)
			}
			if out.Error.Code != wantCode {
				t.Errorf("%s code = %q, want %q", query, out.Error.Code, wantCode)
			}
		}
		// 경계값은 통과해야 한다.
		if resp := env.do(t, http.MethodGet, "/api/v1/servers?page_size=200", viewerToken, nil); resp.StatusCode != http.StatusOK {
			t.Errorf("page_size=200 = %d, want 200 OK", resp.StatusCode)
		}
	})

	t.Run("read-only: no audit event", func(t *testing.T) {
		var before int
		if err := env.pool.QueryRow(`SELECT count(*) FROM audit_events`).Scan(&before); err != nil {
			t.Fatalf("count audit: %v", err)
		}
		env.do(t, http.MethodGet, "/api/v1/servers", viewerToken, nil)
		env.do(t, http.MethodGet, "/api/v1/servers/"+confirmedID, viewerToken, nil)
		var after int
		if err := env.pool.QueryRow(`SELECT count(*) FROM audit_events`).Scan(&after); err != nil {
			t.Fatalf("count audit: %v", err)
		}
		if before != after {
			t.Errorf("audit events %d → %d, want none (조회는 감사 대상이 아니다)", before, after)
		}
	})
}
