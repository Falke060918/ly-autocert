package api

// 영향 범위 집계 API의 인증·권한·응답 형태 + GET /servers issued_by= 배선 검증.
// TEST_DATABASE_URL 미설정 시 스킵. 지문은 결정적 SHA-256(테스트 라벨) 합성값 —
// 실제 인증서를 커밋하지 않는다 (보안 하드 규칙 4).

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/Falke060918/INHERIT_AutoCert/backend/internal/auth"
	"github.com/Falke060918/INHERIT_AutoCert/backend/internal/domain"
)

type impactJSON struct {
	RequestID   string `json:"request_id"`
	Certificate struct {
		ID   string `json:"id"`
		IsCA bool   `json:"is_ca"`
	} `json:"certificate"`
	DescendantCACount   int `json:"descendant_ca_count"`
	DescendantLeafCount int `json:"descendant_leaf_count"`
	CertificateCount    int `json:"certificate_count"`
	MaxDepth            int `json:"max_depth"`
	EndpointCount       int `json:"endpoint_count"`
	ServerCount         int `json:"server_count"`
	DomainCount         int `json:"domain_count"`
	Expiry              struct {
		Expired   int `json:"expired"`
		Within7d  int `json:"within_7d"`
		Within30d int `json:"within_30d"`
		Within90d int `json:"within_90d"`
	} `json:"expiry"`
	Domains []struct {
		Name             string `json:"name"`
		CertificateCount int    `json:"certificate_count"`
		ServerCount      int    `json:"server_count"`
	} `json:"domains"`
	DomainsTruncated bool             `json:"domains_truncated"`
	Servers          []serverItemJSON `json:"servers"`
	ServersTruncated bool             `json:"servers_truncated"`
	ListLimit        int              `json:"list_limit"`
}

func TestCertificateImpactAPI(t *testing.T) {
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

	// 최소 권한 역할(viewer)로 조회 가능해야 한다 — 인증서 조회와 같은 규약.
	viewerPW := randomPassword(t)
	if _, err := env.svc.CreateUser(ctx, "impact-viewer", viewerPW, nil, domain.RoleViewer, nil, auth.AuditMeta{}); err != nil {
		t.Fatalf("create viewer: %v", err)
	}
	viewerToken, status := env.login(t, "impact-viewer", viewerPW)
	if status != http.StatusOK {
		t.Fatalf("viewer login = %d, want 200", status)
	}

	// 픽스처: 루트 CA → 중간 CA → 리프. 키 식별자(SKI/AKI)로 이어 붙인다.
	fp := func(label string) string {
		sum := sha256.Sum256([]byte("api-impact-" + label))
		return hex.EncodeToString(sum[:])
	}
	insert := func(label, subject, issuer, cn, ctype string, sans []string, ski, aki *string, notAfter time.Time) string {
		var id string
		// text[]는 database/sql이 직접 바인딩하지 못한다 — 쉼표로 이어 보내고
		// SQL에서 편다 (store 패키지의 string_to_array 규약과 같다).
		if err := env.pool.QueryRow(`
			INSERT INTO certificates (
				fingerprint_sha256, serial_number, subject_dn, issuer_dn, common_name, sans,
				not_before, not_after, public_key_algorithm, public_key_size,
				signature_algorithm, certificate_type, status, subject_key_id, authority_key_id
			) VALUES ($1, $2, $3, $4, $5,
				CASE WHEN $6::text = '' THEN '{}'::text[] ELSE string_to_array($6::text, ',') END,
				$7, $8, 'ECDSA', 256, 'ECDSA-SHA256', $9, 'active', $10, $11)
			RETURNING id::text`,
			fp(label), "impact-"+label, subject, issuer, cn, strings.Join(sans, ","),
			time.Now().Add(-24*time.Hour), notAfter, ctype, ski, aki,
		).Scan(&id); err != nil {
			t.Fatalf("insert certificate %s: %v", label, err)
		}
		return id
	}
	rootSKI, intSKI := "aa11", "bb22"
	rootID := insert("root", "CN=impact-root", "CN=impact-root", "impact-root", "ca", nil,
		&rootSKI, &rootSKI, time.Now().Add(3000*24*time.Hour))
	intID := insert("int", "CN=impact-int", "CN=impact-root", "impact-int", "ca", nil,
		&intSKI, &rootSKI, time.Now().Add(800*24*time.Hour))
	leafID := insert("leaf", "CN=impact.example.test", "CN=impact-int", "impact.example.test", "server",
		[]string{"impact.example.test", "alt.impact.example.test"}, nil, &intSKI,
		time.Now().Add(20*24*time.Hour))

	var targetID string
	if err := env.pool.QueryRow(`
		INSERT INTO targets (name, target_type, hostname, ip_address, executor_scope)
		VALUES ('impact-api-host', 'network', 'impact-api-host', '198.51.100.90'::inet, 'server')
		RETURNING id::text`).Scan(&targetID); err != nil {
		t.Fatalf("insert target: %v", err)
	}
	if _, err := env.pool.Exec(`
		INSERT INTO certificate_locations
			(certificate_id, target_id, location_type, port, last_verified_at, deployment_status)
		VALUES ($1::uuid, $2::uuid, 'network_endpoint', 443, now(), 'deployed')`,
		leafID, targetID); err != nil {
		t.Fatalf("insert location: %v", err)
	}

	t.Run("without token returns 401", func(t *testing.T) {
		resp := env.do(t, http.MethodGet, "/api/v1/certificates/"+rootID+"/impact", "", nil)
		if resp.StatusCode != http.StatusUnauthorized {
			t.Errorf("impact = %d, want 401", resp.StatusCode)
		}
	})

	t.Run("viewer sees the transitive family", func(t *testing.T) {
		resp := env.do(t, http.MethodGet, "/api/v1/certificates/"+rootID+"/impact", viewerToken, nil)
		if resp.StatusCode != http.StatusOK {
			t.Fatalf("impact = %d, want 200", resp.StatusCode)
		}
		var out impactJSON
		if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
			t.Fatalf("decode impact: %v", err)
		}
		if out.RequestID == "" || out.Certificate.ID != rootID || !out.Certificate.IsCA {
			t.Fatalf("envelope/subject = %+v", out)
		}
		if out.DescendantCACount != 1 || out.DescendantLeafCount != 1 || out.CertificateCount != 3 {
			t.Errorf("counts = ca %d / leaf %d / total %d, want 1/1/3",
				out.DescendantCACount, out.DescendantLeafCount, out.CertificateCount)
		}
		if out.MaxDepth != 2 || out.EndpointCount != 1 || out.ServerCount != 1 {
			t.Errorf("depth/endpoints/servers = %d/%d/%d, want 2/1/1",
				out.MaxDepth, out.EndpointCount, out.ServerCount)
		}
		// 리프 SAN 2개가 도메인으로 펴져야 한다(CA의 CN은 도메인이 아니다).
		if out.DomainCount != 2 || len(out.Domains) != 2 {
			t.Errorf("domains = %+v (count %d), want the 2 leaf names", out.Domains, out.DomainCount)
		}
		for _, d := range out.Domains {
			if d.Name == "impact-root" || d.Name == "impact-int" {
				t.Errorf("domain %q is a CA common name — must not be listed", d.Name)
			}
		}
		// 리프는 20일 뒤 만료 → 30d·90d 누적에 잡히고 7d에는 없다.
		if out.Expiry.Within7d != 0 || out.Expiry.Within30d != 1 || out.Expiry.Within90d != 1 {
			t.Errorf("expiry = %+v, want 0/1/1 for 7d/30d/90d", out.Expiry)
		}
		if len(out.Servers) != 1 || out.Servers[0].ID != targetID {
			t.Errorf("servers = %+v, want the fixture host", out.Servers)
		}
		if out.ListLimit != 200 || out.DomainsTruncated || out.ServersTruncated {
			t.Errorf("limit/truncated = %d/%v/%v", out.ListLimit, out.DomainsTruncated, out.ServersTruncated)
		}
	})

	t.Run("intermediate drops the root from the family", func(t *testing.T) {
		resp := env.do(t, http.MethodGet, "/api/v1/certificates/"+intID+"/impact", viewerToken, nil)
		if resp.StatusCode != http.StatusOK {
			t.Fatalf("impact = %d, want 200", resp.StatusCode)
		}
		var out impactJSON
		if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
			t.Fatalf("decode impact: %v", err)
		}
		if out.DescendantCACount != 0 || out.DescendantLeafCount != 1 || out.CertificateCount != 2 {
			t.Errorf("intermediate counts = ca %d / leaf %d / total %d, want 0/1/2",
				out.DescendantCACount, out.DescendantLeafCount, out.CertificateCount)
		}
	})

	t.Run("unknown id returns 404", func(t *testing.T) {
		for _, id := range []string{"not-a-uuid", "00000000-0000-0000-0000-000000000000"} {
			resp := env.do(t, http.MethodGet, "/api/v1/certificates/"+id+"/impact", viewerToken, nil)
			if resp.StatusCode != http.StatusNotFound {
				t.Errorf("impact %s = %d, want 404", id, resp.StatusCode)
			}
		}
	})

	t.Run("servers issued_by filters by family and matches impact", func(t *testing.T) {
		resp := env.do(t, http.MethodGet, "/api/v1/servers?issued_by="+rootID, viewerToken, nil)
		if resp.StatusCode != http.StatusOK {
			t.Fatalf("servers = %d, want 200", resp.StatusCode)
		}
		var out struct {
			Items []serverItemJSON `json:"items"`
			Total int              `json:"total"`
		}
		if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
			t.Fatalf("decode servers: %v", err)
		}
		if out.Total != 1 || len(out.Items) != 1 || out.Items[0].ID != targetID {
			t.Errorf("issued_by(root) = total %d items %+v, want the fixture host", out.Total, out.Items)
		}
	})

	t.Run("servers issued_by rejects a malformed id", func(t *testing.T) {
		resp := env.do(t, http.MethodGet, "/api/v1/servers?issued_by=nope", viewerToken, nil)
		if resp.StatusCode != http.StatusBadRequest {
			t.Fatalf("issued_by=nope = %d, want 400", resp.StatusCode)
		}
		var out struct {
			Error struct {
				Code string `json:"code"`
			} `json:"error"`
		}
		if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
			t.Fatalf("decode: %v", err)
		}
		if out.Error.Code != "invalid_issued_by" {
			t.Errorf("code = %q, want invalid_issued_by", out.Error.Code)
		}
	})

	t.Run("read-only: no audit event", func(t *testing.T) {
		var before int
		if err := env.pool.QueryRow(`SELECT count(*) FROM audit_events`).Scan(&before); err != nil {
			t.Fatalf("count audit: %v", err)
		}
		env.do(t, http.MethodGet, "/api/v1/certificates/"+rootID+"/impact", viewerToken, nil)
		env.do(t, http.MethodGet, "/api/v1/servers?issued_by="+rootID, viewerToken, nil)
		var after int
		if err := env.pool.QueryRow(`SELECT count(*) FROM audit_events`).Scan(&after); err != nil {
			t.Fatalf("count audit: %v", err)
		}
		if before != after {
			t.Errorf("audit events %d → %d, want none (조회는 감사 대상이 아니다)", before, after)
		}
	})
}
