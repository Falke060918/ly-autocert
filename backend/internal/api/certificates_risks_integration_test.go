package api

// 위험 판정 API 통합 테스트: 인증·권한·응답 형태 + "집계 숫자 → 목록 필터" 이동.
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
	"github.com/Falke060918/INHERIT_AutoCert/backend/internal/certrisk"
	"github.com/Falke060918/INHERIT_AutoCert/backend/internal/domain"
)

type riskAPIFinding struct {
	Code            string `json:"code"`
	Severity        string `json:"severity"`
	Scope           string `json:"scope"`
	Title           string `json:"title"`
	PublicTrustOnly bool   `json:"public_trust_only"`
	Message         string `json:"message"`
	Count           int    `json:"count"`
}

func TestCertificateRisksAPI(t *testing.T) {
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
	if _, err := env.svc.CreateUser(ctx, "risk-viewer", viewerPW, nil, domain.RoleViewer, nil, auth.AuditMeta{}); err != nil {
		t.Fatalf("create viewer: %v", err)
	}
	viewerToken, status := env.login(t, "risk-viewer", viewerPW)
	if status != http.StatusOK {
		t.Fatalf("viewer login = %d, want 200", status)
	}

	insert := func(label, cn, keyAlg string, keySize int, sigAlg string, sans string) {
		sum := sha256.Sum256([]byte("risk-api-fixture:" + label))
		notAfter := time.Now().Add(200 * 24 * time.Hour)
		if _, err := env.pool.Exec(`
			INSERT INTO certificates (
				fingerprint_sha256, serial_number, subject_dn, issuer_dn, common_name, sans,
				not_before, not_after, public_key_algorithm, public_key_size,
				signature_algorithm, certificate_type, status
			) VALUES ($1, $2, $3, 'CN=risk-api-ca', $4,
				coalesce(string_to_array(nullif($5, ''), chr(31)), '{}'),
				$6, $7, $8, $9, $10, 'server', 'active')`,
			hex.EncodeToString(sum[:]), label, "CN="+cn, cn, sans,
			notAfter.Add(-100*24*time.Hour), notAfter, keyAlg, keySize, sigAlg); err != nil {
			t.Fatalf("insert fixture %s: %v", label, err)
		}
	}
	// danger 1건(약한 키) · warn 1건(와일드카드) · ok 1건.
	insert("r1", "weak.example.test", "RSA", 1024, "SHA256-RSA", "weak.example.test")
	insert("r2", "wild.example.test", "ECDSA", 384, "ECDSA-SHA384", "*.example.test")
	insert("r3", "fine.example.test", "ECDSA", 384, "ECDSA-SHA384", "fine.example.test")

	t.Run("risks without token returns 401", func(t *testing.T) {
		if resp := env.do(t, http.MethodGet, "/api/v1/certificates/risks", "", nil); resp.StatusCode != http.StatusUnauthorized {
			t.Errorf("risks = %d, want 401", resp.StatusCode)
		}
	})

	var agg struct {
		RequestID  string `json:"request_id"`
		Total      int    `json:"total"`
		Severities []struct {
			Severity string `json:"severity"`
			Count    int    `json:"count"`
		} `json:"severities"`
		Findings []riskAPIFinding `json:"findings"`
	}

	t.Run("viewer reads the aggregation", func(t *testing.T) {
		resp := env.do(t, http.MethodGet, "/api/v1/certificates/risks", viewerToken, nil)
		if resp.StatusCode != http.StatusOK {
			t.Fatalf("risks = %d, want 200 (literal route must win over {id})", resp.StatusCode)
		}
		if err := json.NewDecoder(resp.Body).Decode(&agg); err != nil {
			t.Fatalf("decode risks: %v", err)
		}
		if agg.RequestID == "" {
			t.Error("request_id empty")
		}
		if agg.Total != 3 {
			t.Errorf("total = %d, want 3", agg.Total)
		}
		// 최고 심각도 분할이므로 합이 total과 같아야 한다.
		sum := 0
		got := map[string]int{}
		for _, s := range agg.Severities {
			sum += s.Count
			got[s.Severity] = s.Count
		}
		if sum != agg.Total {
			t.Errorf("severity counts sum to %d, want %d", sum, agg.Total)
		}
		if got["danger"] != 1 || got["warn"] != 1 || got["ok"] != 1 {
			t.Errorf("severities = %v, want danger/warn/ok = 1/1/1", got)
		}
		// 건수 0인 항목까지 카탈로그 전체가 내려온다.
		if len(agg.Findings) != len(certrisk.Rules()) {
			t.Fatalf("findings = %d, want the whole catalog (%d)", len(agg.Findings), len(certrisk.Rules()))
		}
		for _, f := range agg.Findings {
			if f.Title == "" || f.Severity == "" || f.Scope == "" {
				t.Errorf("finding %q is missing catalog metadata: %+v", f.Code, f)
			}
			if f.PublicTrustOnly {
				t.Errorf("finding %q is public-trust-only; none should be in this unit", f.Code)
			}
		}
	})

	// 집계에서 클릭해 목록으로 이동하는 규약: severity → risk=, code → risk_code=.
	t.Run("aggregation numbers drill through to the list", func(t *testing.T) {
		listTotal := func(query string) int {
			t.Helper()
			resp := env.do(t, http.MethodGet, "/api/v1/certificates?"+query, viewerToken, nil)
			if resp.StatusCode != http.StatusOK {
				t.Fatalf("list?%s = %d, want 200", query, resp.StatusCode)
			}
			var out struct {
				Total int `json:"total"`
				Items []struct {
					CommonName string `json:"common_name"`
					Risk       struct {
						Severity string           `json:"severity"`
						Findings []riskAPIFinding `json:"findings"`
					} `json:"risk"`
				} `json:"items"`
			}
			if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
				t.Fatalf("decode list: %v", err)
			}
			// 목록 원소에 판정이 실려 있어야 화면이 배지를 붙일 수 있다.
			for _, it := range out.Items {
				if it.Risk.Severity == "" {
					t.Errorf("%s: list item has no risk severity", it.CommonName)
				}
			}
			return out.Total
		}

		for _, s := range agg.Severities {
			if got := listTotal("risk=" + s.Severity); got != s.Count {
				t.Errorf("risk=%s list total = %d, aggregation = %d", s.Severity, got, s.Count)
			}
		}
		for _, f := range agg.Findings {
			if got := listTotal("risk_code=" + f.Code); got != f.Count {
				t.Errorf("risk_code=%s list total = %d, aggregation = %d", f.Code, got, f.Count)
			}
		}
	})

	t.Run("unknown filter values are 400", func(t *testing.T) {
		for _, q := range []string{"risk=critical", "risk_code=nope"} {
			resp := env.do(t, http.MethodGet, "/api/v1/certificates?"+q, viewerToken, nil)
			if resp.StatusCode != http.StatusBadRequest {
				t.Errorf("list?%s = %d, want 400", q, resp.StatusCode)
			}
		}
	})

	t.Run("detail carries the assessment", func(t *testing.T) {
		resp := env.do(t, http.MethodGet, "/api/v1/certificates?q=weak.example.test", viewerToken, nil)
		var list struct {
			Items []struct {
				ID string `json:"id"`
			} `json:"items"`
		}
		if err := json.NewDecoder(resp.Body).Decode(&list); err != nil {
			t.Fatalf("decode list: %v", err)
		}
		if len(list.Items) != 1 {
			t.Fatalf("items = %d, want 1", len(list.Items))
		}
		resp = env.do(t, http.MethodGet, "/api/v1/certificates/"+list.Items[0].ID, viewerToken, nil)
		if resp.StatusCode != http.StatusOK {
			t.Fatalf("detail = %d, want 200", resp.StatusCode)
		}
		var detail struct {
			Risk struct {
				Severity string           `json:"severity"`
				Findings []riskAPIFinding `json:"findings"`
			} `json:"risk"`
		}
		if err := json.NewDecoder(resp.Body).Decode(&detail); err != nil {
			t.Fatalf("decode detail: %v", err)
		}
		if detail.Risk.Severity != string(certrisk.SeverityDanger) {
			t.Errorf("detail risk severity = %q, want danger", detail.Risk.Severity)
		}
		var found bool
		for _, f := range detail.Risk.Findings {
			if f.Code == string(certrisk.CodeWeakKey) {
				found = true
				if f.Message == "" {
					t.Error("weak_key finding has no message")
				}
			}
		}
		if !found {
			t.Errorf("detail findings = %+v, want weak_key", detail.Risk.Findings)
		}
	})
}
