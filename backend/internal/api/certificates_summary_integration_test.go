package api

// 이슈 #8 통합 테스트: 대시보드 요약 API의 인증·응답 형태.
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

func TestCertificateSummaryAPI(t *testing.T) {
	env := newTestEnv(t)
	ctx := context.Background()

	for _, stmt := range []string{
		`DELETE FROM certificate_locations`, `DELETE FROM certificates`, `DELETE FROM targets`,
	} {
		if _, err := env.pool.Exec(stmt); err != nil {
			t.Fatalf("cleanup %q: %v", stmt, err)
		}
	}

	// 최소 권한 역할(viewer)로 조회 가능해야 한다 — 대시보드는 전 역할 화면이다.
	viewerPW := randomPassword(t)
	if _, err := env.svc.CreateUser(ctx, "sum-viewer", viewerPW, nil, domain.RoleViewer, nil, auth.AuditMeta{}); err != nil {
		t.Fatalf("create viewer: %v", err)
	}
	viewerToken, status := env.login(t, "sum-viewer", viewerPW)
	if status != http.StatusOK {
		t.Fatalf("viewer login = %d, want 200", status)
	}

	// 픽스처 2건: 20일 뒤 만료(자체 서명) 1건 + 이미 만료 1건.
	insert := func(label, cn, issuerCN string, notAfter time.Time) {
		sum := sha256.Sum256([]byte("summary-api-fixture:" + label))
		if _, err := env.pool.Exec(`
			INSERT INTO certificates (
				fingerprint_sha256, serial_number, subject_dn, issuer_dn, common_name, sans,
				not_before, not_after, public_key_algorithm, public_key_size,
				signature_algorithm, certificate_type, status
			) VALUES ($1, $2, $3, $4, $5, '{}', $6, $7, 'ECDSA', 256, 'ECDSA-SHA256', 'server', 'active')`,
			hex.EncodeToString(sum[:]), label, "CN="+cn, "CN="+issuerCN, cn,
			notAfter.Add(-90*24*time.Hour), notAfter); err != nil {
			t.Fatalf("insert fixture %s: %v", label, err)
		}
	}
	insert("a1", "soon.example.test", "soon.example.test", time.Now().Add(20*24*time.Hour))
	insert("a2", "gone.example.test", "fixture-ca", time.Now().Add(-3*24*time.Hour))

	t.Run("summary without token returns 401", func(t *testing.T) {
		if resp := env.do(t, http.MethodGet, "/api/v1/certificates/summary", "", nil); resp.StatusCode != http.StatusUnauthorized {
			t.Errorf("summary = %d, want 401", resp.StatusCode)
		}
	})

	t.Run("viewer reads summary counts", func(t *testing.T) {
		resp := env.do(t, http.MethodGet, "/api/v1/certificates/summary", viewerToken, nil)
		if resp.StatusCode != http.StatusOK {
			t.Fatalf("summary = %d, want 200 (literal route must win over {id})", resp.StatusCode)
		}
		var out struct {
			RequestID   string `json:"request_id"`
			Total       int    `json:"total"`
			Expiring7d  int    `json:"expiring_7d"`
			Expiring30d int    `json:"expiring_30d"`
			Expiring60d int    `json:"expiring_60d"`
			Expiring90d int    `json:"expiring_90d"`
			Expired     int    `json:"expired"`
			SelfSigned  int    `json:"self_signed"`
			Unowned     int    `json:"unowned"`
		}
		if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
			t.Fatalf("decode summary: %v", err)
		}
		if out.RequestID == "" {
			t.Error("request_id empty")
		}
		if out.Total != 2 || out.Expiring7d != 0 || out.Expiring30d != 1 ||
			out.Expired != 1 || out.SelfSigned != 1 || out.Unowned != 2 {
			t.Errorf("summary = %+v, want total 2 / 30d 1 / expired 1 / self_signed 1 / unowned 2", out)
		}
	})
}
