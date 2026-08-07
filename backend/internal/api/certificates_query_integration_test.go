package api

// 이슈 #4 통합 테스트: 목록·상세 API의 인증·권한·응답 형태.
// TEST_DATABASE_URL 미설정 시 스킵 (전용 테스트 DB — 메인 DB에 돌리지 않는다).
// fingerprint는 결정적 SHA-256(테스트 라벨) 합성 값 — 실제 인증서를 커밋하지 않는다.

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

func TestCertificatesReadAPI(t *testing.T) {
	env := newTestEnv(t)
	ctx := context.Background()

	// 인벤토리 초기화 (테스트 전용 DB에서만 허용되는 정리다).
	for _, stmt := range []string{
		`DELETE FROM certificate_locations`, `DELETE FROM certificates`, `DELETE FROM targets`,
	} {
		if _, err := env.pool.Exec(stmt); err != nil {
			t.Fatalf("cleanup %q: %v", stmt, err)
		}
	}

	// 최소 권한 역할(viewer)로 조회 가능해야 한다 (00-spec 7.11).
	viewerPW := randomPassword(t)
	if _, err := env.svc.CreateUser(ctx, "inv-viewer", viewerPW, nil, domain.RoleViewer, nil, auth.AuditMeta{}); err != nil {
		t.Fatalf("create viewer: %v", err)
	}
	viewerToken, status := env.login(t, "inv-viewer", viewerPW)
	if status != http.StatusOK {
		t.Fatalf("viewer login = %d, want 200", status)
	}

	// 픽스처: 인증서 1건 + 대상 + 설치 위치.
	sum := sha256.Sum256([]byte("api-read-fixture"))
	var certID string
	if err := env.pool.QueryRow(`
		INSERT INTO certificates (
			fingerprint_sha256, serial_number, subject_dn, issuer_dn, common_name, sans,
			not_before, not_after, public_key_algorithm, public_key_size,
			signature_algorithm, certificate_type, status
		) VALUES (
			$1, 'api-01', 'CN=read.example.test', 'CN=fixture-ca', 'read.example.test',
			ARRAY['read.example.test'], $2, $3, 'ECDSA', 256, 'ECDSA-SHA256', 'server', 'active'
		) RETURNING id`,
		hex.EncodeToString(sum[:]),
		time.Now().Add(-24*time.Hour), time.Now().Add(45*24*time.Hour),
	).Scan(&certID); err != nil {
		t.Fatalf("insert certificate: %v", err)
	}
	var targetID string
	if err := env.pool.QueryRow(`
		INSERT INTO targets (name, target_type, hostname)
		VALUES ('api-read-target', 'server', 'read-host.example.test') RETURNING id`).Scan(&targetID); err != nil {
		t.Fatalf("insert target: %v", err)
	}
	if _, err := env.pool.Exec(`
		INSERT INTO certificate_locations (certificate_id, target_id, location_type, port, deployment_status)
		VALUES ($1, $2, 'network_endpoint', 8443, 'unknown')`, certID, targetID); err != nil {
		t.Fatalf("insert location: %v", err)
	}

	t.Run("list without token returns 401", func(t *testing.T) {
		if resp := env.do(t, http.MethodGet, "/api/v1/certificates", "", nil); resp.StatusCode != http.StatusUnauthorized {
			t.Errorf("list = %d, want 401", resp.StatusCode)
		}
	})

	t.Run("viewer lists certificates", func(t *testing.T) {
		resp := env.do(t, http.MethodGet, "/api/v1/certificates?status=active&type=server&q=read", viewerToken, nil)
		if resp.StatusCode != http.StatusOK {
			t.Fatalf("list = %d, want 200", resp.StatusCode)
		}
		var out struct {
			RequestID string               `json:"request_id"`
			Items     []domain.Certificate `json:"items"`
			Total     int                  `json:"total"`
			Page      int                  `json:"page"`
			PageSize  int                  `json:"page_size"`
		}
		if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
			t.Fatalf("decode list: %v", err)
		}
		if out.Total != 1 || len(out.Items) != 1 || out.Items[0].CommonName != "read.example.test" {
			t.Errorf("list = total %d items %d, want 1 read.example.test", out.Total, len(out.Items))
		}
		if out.RequestID == "" || out.Page != 1 || out.PageSize != 50 {
			t.Errorf("envelope = %+v", out)
		}
	})

	t.Run("empty result returns items [] not null", func(t *testing.T) {
		resp := env.do(t, http.MethodGet, "/api/v1/certificates?q=no-such-cert", viewerToken, nil)
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

	t.Run("invalid query param returns 400", func(t *testing.T) {
		resp := env.do(t, http.MethodGet, "/api/v1/certificates?sort=evil", viewerToken, nil)
		if resp.StatusCode != http.StatusBadRequest {
			t.Fatalf("list = %d, want 400", resp.StatusCode)
		}
		var out struct {
			Error struct {
				Code string `json:"code"`
			} `json:"error"`
		}
		if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
			t.Fatalf("decode error: %v", err)
		}
		if out.Error.Code != "invalid_sort" {
			t.Errorf("code = %s, want invalid_sort", out.Error.Code)
		}
	})

	t.Run("viewer reads detail with locations", func(t *testing.T) {
		resp := env.do(t, http.MethodGet, "/api/v1/certificates/"+certID, viewerToken, nil)
		if resp.StatusCode != http.StatusOK {
			t.Fatalf("detail = %d, want 200", resp.StatusCode)
		}
		var out struct {
			RequestID   string             `json:"request_id"`
			Certificate domain.Certificate `json:"certificate"`
			Locations   []struct {
				LocationType string `json:"location_type"`
				Port         *int   `json:"port"`
				Target       struct {
					Name     string  `json:"name"`
					Hostname *string `json:"hostname"`
				} `json:"target"`
			} `json:"locations"`
		}
		if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
			t.Fatalf("decode detail: %v", err)
		}
		if out.Certificate.ID != certID || out.Certificate.CommonName != "read.example.test" {
			t.Errorf("certificate = %s/%s", out.Certificate.ID, out.Certificate.CommonName)
		}
		if len(out.Locations) != 1 {
			t.Fatalf("locations = %d, want 1", len(out.Locations))
		}
		loc := out.Locations[0]
		if loc.LocationType != "network_endpoint" || loc.Port == nil || *loc.Port != 8443 ||
			loc.Target.Name != "api-read-target" {
			t.Errorf("location = %+v", loc)
		}
	})

	t.Run("unknown certificate returns 404", func(t *testing.T) {
		resp := env.do(t, http.MethodGet, "/api/v1/certificates/00000000-0000-4000-8000-000000000000", viewerToken, nil)
		if resp.StatusCode != http.StatusNotFound {
			t.Errorf("detail = %d, want 404", resp.StatusCode)
		}
	})

	t.Run("malformed id returns 404", func(t *testing.T) {
		resp := env.do(t, http.MethodGet, "/api/v1/certificates/not-a-uuid", viewerToken, nil)
		if resp.StatusCode != http.StatusNotFound {
			t.Errorf("detail = %d, want 404", resp.StatusCode)
		}
	})
}
