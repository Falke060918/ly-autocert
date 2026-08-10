package api

// exclude_status 파라미터와 목록 파생 열 last_seen_at의 API 계층 회귀 테스트
// (06-rescan-and-replacement 11.2·11.3).
//
// parseListParams는 목록과 CSV 내보내기가 **공유**한다. 그래서 "파라미터가 없으면
// 아무것도 제외하지 않는다"가 이 파일의 핵심 단언이다 — 여기가 무너지면 아무도
// 요청하지 않은 내보내기 내용 변경이 따라온다(설계 14.2 ⑥).

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/Falke060918/INHERIT_AutoCert/backend/internal/auth"
	"github.com/Falke060918/INHERIT_AutoCert/backend/internal/domain"
)

func TestParseListParamsExcludeStatus(t *testing.T) {
	t.Run("absent means no exclusion", func(t *testing.T) {
		r := httptest.NewRequest("GET", "/api/v1/certificates?status=active", nil)
		in, perr := parseListParams(r)
		if perr != nil {
			t.Fatalf("perr = %+v, want nil", perr)
		}
		if len(in.ExcludeStatuses) != 0 {
			t.Errorf("exclude = %v, want empty — 기본 동작이 바뀌었다", in.ExcludeStatuses)
		}
	})

	t.Run("repeated and comma forms both parse", func(t *testing.T) {
		r := httptest.NewRequest("GET",
			"/api/v1/certificates?exclude_status=replaced&exclude_status=revoked,expired", nil)
		in, perr := parseListParams(r)
		if perr != nil {
			t.Fatalf("perr = %+v, want nil", perr)
		}
		if len(in.ExcludeStatuses) != 3 || in.ExcludeStatuses[0] != domain.CertStatusReplaced {
			t.Errorf("exclude = %v, want [replaced revoked expired]", in.ExcludeStatuses)
		}
	})

	t.Run("unknown value is rejected", func(t *testing.T) {
		r := httptest.NewRequest("GET", "/api/v1/certificates?exclude_status=nope", nil)
		_, perr := parseListParams(r)
		if perr == nil || perr.code != "invalid_exclude_status" {
			t.Errorf("perr = %+v, want code invalid_exclude_status", perr)
		}
	})
}

func TestCertificateListExcludeStatusAPI(t *testing.T) {
	env := newTestEnv(t)
	ctx := context.Background()

	for _, stmt := range []string{
		`DELETE FROM certificate_locations`, `DELETE FROM certificates`, `DELETE FROM targets`,
	} {
		if _, err := env.pool.Exec(stmt); err != nil {
			t.Fatalf("cleanup %q: %v", stmt, err)
		}
	}

	insert := func(label, cn string, status domain.CertificateStatus) string {
		sum := sha256.Sum256([]byte("exclude-status-fixture:" + label))
		var id string
		if err := env.pool.QueryRow(`
			INSERT INTO certificates (
				fingerprint_sha256, serial_number, subject_dn, issuer_dn, common_name, sans,
				not_before, not_after, public_key_algorithm, public_key_size,
				signature_algorithm, certificate_type, status
			) VALUES ($1, $2, $3, 'CN=fixture-ca', $4, '{}', now() - interval '90 days',
				now() + interval '120 days', 'ECDSA', 256, 'ECDSA-SHA256', 'server', $5)
			RETURNING id`,
			hex.EncodeToString(sum[:]), label, "CN="+cn, cn, string(status)).Scan(&id); err != nil {
			t.Fatalf("insert fixture %s: %v", label, err)
		}
		return id
	}
	liveID := insert("x1", "live.example.test", domain.CertStatusActive)
	insert("x2", "old.example.test", domain.CertStatusReplaced)

	// 위치 1행 — last_seen_at 파생 열이 목록에 실리는지 함께 본다.
	var targetID string
	if err := env.pool.QueryRow(`
		INSERT INTO targets (name, target_type, ip_address, executor_scope)
		VALUES ('exclude-status-target', 'network', '10.78.0.4'::inet, 'server')
		RETURNING id`).Scan(&targetID); err != nil {
		t.Fatalf("insert target: %v", err)
	}
	if _, err := env.pool.Exec(`
		INSERT INTO certificate_locations (
			certificate_id, target_id, location_type, port, last_verified_at, deployment_status
		) VALUES ($1::uuid, $2::uuid, 'network_endpoint', 443, now() - interval '3 hours', 'deployed')`,
		liveID, targetID); err != nil {
		t.Fatalf("insert location: %v", err)
	}

	pw := randomPassword(t)
	if _, err := env.svc.CreateUser(ctx, "exclude-admin", pw, nil,
		domain.RoleSystemAdministrator, nil, auth.AuditMeta{}); err != nil {
		t.Fatalf("create admin: %v", err)
	}
	token, status := env.login(t, "exclude-admin", pw)
	if status != http.StatusOK {
		t.Fatalf("login = %d, want 200", status)
	}

	listCNs := func(t *testing.T, query string) ([]string, []*time.Time) {
		t.Helper()
		resp := env.do(t, http.MethodGet, "/api/v1/certificates"+query, token, nil)
		if resp.StatusCode != http.StatusOK {
			t.Fatalf("list%s = %d, want 200", query, resp.StatusCode)
		}
		var out struct {
			Items []struct {
				CommonName string     `json:"common_name"`
				LastSeenAt *time.Time `json:"last_seen_at"`
			} `json:"items"`
		}
		if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
			t.Fatalf("decode: %v", err)
		}
		cns := make([]string, len(out.Items))
		seen := make([]*time.Time, len(out.Items))
		for i, it := range out.Items {
			cns[i] = it.CommonName
			seen[i] = it.LastSeenAt
		}
		return cns, seen
	}

	t.Run("default list still contains replaced", func(t *testing.T) {
		cns, _ := listCNs(t, "")
		if len(cns) != 2 {
			t.Fatalf("items = %v, want 2건 (서버 기본 동작 불변)", cns)
		}
	})

	t.Run("exclude_status=replaced hides it", func(t *testing.T) {
		cns, seen := listCNs(t, "?exclude_status=replaced")
		if len(cns) != 1 || cns[0] != "live.example.test" {
			t.Fatalf("items = %v, want [live.example.test]", cns)
		}
		if seen[0] == nil {
			t.Error("last_seen_at = nil, want 위치의 최근 확인 시각")
		}
	})

	t.Run("invalid exclude_status is a 400", func(t *testing.T) {
		resp := env.do(t, http.MethodGet, "/api/v1/certificates?exclude_status=nope", token, nil)
		if resp.StatusCode != http.StatusBadRequest {
			t.Errorf("list = %d, want 400", resp.StatusCode)
		}
	})

	t.Run("csv export is unchanged without the parameter", func(t *testing.T) {
		resp := env.do(t, http.MethodGet, "/api/v1/certificates/export", token, nil)
		if resp.StatusCode != http.StatusOK {
			t.Fatalf("export = %d, want 200", resp.StatusCode)
		}
		body, err := io.ReadAll(resp.Body)
		if err != nil {
			t.Fatalf("read csv: %v", err)
		}
		if !strings.Contains(string(body), "old.example.test") {
			t.Error("CSV에서 replaced 인증서가 사라졌다 — 아무도 요청하지 않은 내보내기 변경")
		}
	})

	t.Run("csv export honours an explicit exclusion", func(t *testing.T) {
		resp := env.do(t, http.MethodGet,
			"/api/v1/certificates/export?exclude_status=replaced", token, nil)
		if resp.StatusCode != http.StatusOK {
			t.Fatalf("export = %d, want 200", resp.StatusCode)
		}
		body, err := io.ReadAll(resp.Body)
		if err != nil {
			t.Fatalf("read csv: %v", err)
		}
		if strings.Contains(string(body), "old.example.test") {
			t.Error("명시적 exclude_status가 CSV에 반영되지 않았다")
		}
	})
}
