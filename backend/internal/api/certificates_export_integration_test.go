package api

// 내보내기(M3 감사 축) 통합 테스트: 목록 조회 조건 기반 CSV 내보내기가 동작하고,
// 모든 내보내기가 certificate.export 감사 이벤트(actor_id 포함)를 남긴다
// (02-goals.md 성공 기준 5, 00-spec 7.12 "데이터 내보내기").
// 함께: certificate.import 감사의 actor_id 회귀 테스트 — 로그인 사용자가
// 업로드하면 actor_id가 NULL이 아니어야 한다.
// TEST_DATABASE_URL 미설정 시 스킵 (전용 테스트 DB — 메인 DB에 돌리지 않는다).
// 픽스처 인증서는 실행 시점에 생성한다 — 실제 키·인증서를 커밋하지 않는다.

import (
	"bytes"
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/sha256"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/csv"
	"encoding/hex"
	"encoding/pem"
	"math/big"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/Falke060918/INHERIT_AutoCert/backend/internal/auth"
	"github.com/Falke060918/INHERIT_AutoCert/backend/internal/domain"
)

// doBytes는 원시 바이트 본문 요청을 보낸다 (업로드·CSV 응답용). reqID를 지정하면
// X-Request-ID로 붙여 감사 이벤트를 상호 연관시킨다.
func (e *testEnv) doBytes(t *testing.T, method, path, token, contentType string, body []byte, reqID string) *http.Response {
	t.Helper()
	req, err := http.NewRequest(method, e.server.URL+path, bytes.NewReader(body))
	if err != nil {
		t.Fatalf("new request: %v", err)
	}
	if contentType != "" {
		req.Header.Set("Content-Type", contentType)
	}
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	if reqID != "" {
		req.Header.Set("X-Request-ID", reqID)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("%s %s: %v", method, path, err)
	}
	t.Cleanup(func() { resp.Body.Close() })
	return resp
}

// newTestLeafPEM은 테스트 전용 리프 인증서 PEM을 만든다.
func newTestLeafPEM(t *testing.T, cn string) []byte {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatalf("generate key: %v", err)
	}
	tmpl := &x509.Certificate{
		SerialNumber: big.NewInt(42),
		Subject:      pkix.Name{CommonName: cn},
		NotBefore:    time.Now().Add(-time.Hour),
		NotAfter:     time.Now().Add(90 * 24 * time.Hour),
		KeyUsage:     x509.KeyUsageDigitalSignature,
		ExtKeyUsage:  []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
		DNSNames:     []string{cn},
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &key.PublicKey, key)
	if err != nil {
		t.Fatalf("create certificate: %v", err)
	}
	return pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der})
}

// seedCert는 인증서 1건을 직접 삽입한다 (내보내기 픽스처).
func seedCert(t *testing.T, env *testEnv, label, cn, status string, notAfter time.Time) string {
	t.Helper()
	sum := sha256.Sum256([]byte(label))
	var id string
	if err := env.pool.QueryRow(`
		INSERT INTO certificates (
			fingerprint_sha256, serial_number, subject_dn, issuer_dn, common_name, sans,
			not_before, not_after, public_key_algorithm, public_key_size,
			signature_algorithm, certificate_type, status
		) VALUES (
			$1, $2, $3, 'CN=export-fixture-ca', $4, ARRAY[$4::text],
			$5, $6, 'ECDSA', 256, 'ECDSA-SHA256', 'server', $7
		) RETURNING id`,
		hex.EncodeToString(sum[:]), label, "CN="+cn, cn,
		notAfter.Add(-30*24*time.Hour), notAfter, status,
	).Scan(&id); err != nil {
		t.Fatalf("seed certificate %s: %v", label, err)
	}
	return id
}

func TestExportCertificatesCSV(t *testing.T) {
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

	// 최소 권한 역할(viewer)도 목록과 동일하게 내보낼 수 있다 — 단 전건 감사된다.
	viewerPW := randomPassword(t)
	viewer, err := env.svc.CreateUser(ctx, "exp-viewer", viewerPW, nil, domain.RoleViewer, nil, auth.AuditMeta{})
	if err != nil {
		t.Fatalf("create viewer: %v", err)
	}
	viewerToken, status := env.login(t, "exp-viewer", viewerPW)
	if status != http.StatusOK {
		t.Fatalf("viewer login = %d, want 200", status)
	}

	seedCert(t, env, "exp-active", "export-active.example.test", "active",
		time.Now().Add(45*24*time.Hour))
	seedCert(t, env, "exp-expired", "export-expired.example.test", "expired",
		time.Now().Add(-24*time.Hour))

	t.Run("export without token returns 401", func(t *testing.T) {
		resp := env.doBytes(t, http.MethodGet, "/api/v1/certificates/export", "", "", nil, "")
		if resp.StatusCode != http.StatusUnauthorized {
			t.Errorf("export = %d, want 401", resp.StatusCode)
		}
	})

	t.Run("invalid filter param returns 400", func(t *testing.T) {
		resp := env.doBytes(t, http.MethodGet, "/api/v1/certificates/export?status=evil", viewerToken, "", nil, "")
		if resp.StatusCode != http.StatusBadRequest {
			t.Errorf("export = %d, want 400", resp.StatusCode)
		}
	})

	t.Run("filtered CSV export leaves audit event with actor", func(t *testing.T) {
		reqID := "test-req-export-csv"
		resp := env.doBytes(t, http.MethodGet,
			"/api/v1/certificates/export?status=active&q=export-", viewerToken, "", nil, reqID)
		if resp.StatusCode != http.StatusOK {
			t.Fatalf("export = %d, want 200", resp.StatusCode)
		}
		if ct := resp.Header.Get("Content-Type"); !strings.HasPrefix(ct, "text/csv") {
			t.Errorf("Content-Type = %q, want text/csv", ct)
		}
		if cd := resp.Header.Get("Content-Disposition"); !strings.Contains(cd, "attachment") {
			t.Errorf("Content-Disposition = %q, want attachment", cd)
		}

		var buf bytes.Buffer
		if _, err := buf.ReadFrom(resp.Body); err != nil {
			t.Fatalf("read body: %v", err)
		}
		body := strings.TrimPrefix(buf.String(), "\uFEFF") // Excel용 UTF-8 BOM 허용
		records, err := csv.NewReader(strings.NewReader(body)).ReadAll()
		if err != nil {
			t.Fatalf("parse csv: %v", err)
		}
		if len(records) != 2 { // 헤더 + active 1건 (expired는 필터로 제외)
			t.Fatalf("csv records = %d, want 2 (header + 1 row)", len(records))
		}
		header := strings.Join(records[0], ",")
		for _, col := range []string{"common_name", "fingerprint_sha256", "not_after", "status"} {
			if !strings.Contains(header, col) {
				t.Errorf("csv header %q missing column %s", header, col)
			}
		}
		row := strings.Join(records[1], ",")
		if !strings.Contains(row, "export-active.example.test") {
			t.Errorf("csv row = %q, want export-active.example.test", row)
		}
		if strings.Contains(body, "export-expired.example.test") {
			t.Error("csv contains filtered-out expired certificate")
		}

		// 내보내기는 예외 없이 감사 이벤트를 남긴다 — actor_id 포함 (보안 하드 규칙 5).
		var actorID *string
		var rowCount int
		if err := env.pool.QueryRow(`
			SELECT actor_id, (after_value->>'row_count')::int
			FROM audit_events
			WHERE event_type = 'certificate.export' AND request_id = $1 AND result = 'success'`,
			reqID).Scan(&actorID, &rowCount); err != nil {
			t.Fatalf("read export audit event: %v", err)
		}
		if actorID == nil || *actorID != viewer.ID {
			t.Errorf("export audit actor_id = %v, want %s", actorID, viewer.ID)
		}
		if rowCount != 1 {
			t.Errorf("export audit row_count = %d, want 1", rowCount)
		}
	})

	t.Run("unfiltered export returns all rows", func(t *testing.T) {
		resp := env.doBytes(t, http.MethodGet, "/api/v1/certificates/export", viewerToken, "", nil, "")
		if resp.StatusCode != http.StatusOK {
			t.Fatalf("export = %d, want 200", resp.StatusCode)
		}
		var buf bytes.Buffer
		if _, err := buf.ReadFrom(resp.Body); err != nil {
			t.Fatalf("read body: %v", err)
		}
		body := strings.TrimPrefix(buf.String(), "\uFEFF")
		records, err := csv.NewReader(strings.NewReader(body)).ReadAll()
		if err != nil {
			t.Fatalf("parse csv: %v", err)
		}
		if len(records) != 3 { // 헤더 + 2건
			t.Errorf("csv records = %d, want 3", len(records))
		}
	})
}

// TestImportAuditRecordsActorID는 로그인 사용자의 업로드가 certificate.import
// 감사 이벤트에 actor_id를 남기는지 검증한다 (회귀: 전건 NULL이던 버그).
func TestImportAuditRecordsActorID(t *testing.T) {
	env := newTestEnv(t)
	ctx := context.Background()

	adminPW := randomPassword(t)
	admin, err := env.svc.CreateUser(ctx, "imp-admin", adminPW, nil,
		domain.RoleSystemAdministrator, nil, auth.AuditMeta{})
	if err != nil {
		t.Fatalf("create admin: %v", err)
	}
	adminToken, status := env.login(t, "imp-admin", adminPW)
	if status != http.StatusOK {
		t.Fatalf("admin login = %d, want 200", status)
	}

	pemBytes := newTestLeafPEM(t, "import-actor.example.test")
	var fingerprint string
	t.Cleanup(func() {
		if fingerprint != "" {
			_, _ = env.pool.Exec(`DELETE FROM certificates WHERE fingerprint_sha256 = $1`, fingerprint)
		}
	})

	t.Run("successful import audits actor", func(t *testing.T) {
		reqID := "test-req-import-actor-ok"
		resp := env.doBytes(t, http.MethodPost, "/api/v1/certificates/import",
			adminToken, "application/octet-stream", pemBytes, reqID)
		if resp.StatusCode != http.StatusCreated {
			t.Fatalf("import = %d, want 201", resp.StatusCode)
		}
		block, _ := pem.Decode(pemBytes)
		sum := sha256.Sum256(block.Bytes)
		fingerprint = hex.EncodeToString(sum[:])

		var actorID *string
		if err := env.pool.QueryRow(`
			SELECT actor_id FROM audit_events
			WHERE event_type = 'certificate.import' AND request_id = $1 AND result = 'success'`,
			reqID).Scan(&actorID); err != nil {
			t.Fatalf("read import audit event: %v", err)
		}
		if actorID == nil || *actorID != admin.ID {
			t.Errorf("import audit actor_id = %v, want %s", actorID, admin.ID)
		}
	})

	t.Run("rejected import audits actor too", func(t *testing.T) {
		reqID := "test-req-import-actor-fail"
		resp := env.doBytes(t, http.MethodPost, "/api/v1/certificates/import",
			adminToken, "application/octet-stream", []byte("not a certificate"), reqID)
		if resp.StatusCode != http.StatusBadRequest {
			t.Fatalf("import = %d, want 400", resp.StatusCode)
		}
		var actorID *string
		if err := env.pool.QueryRow(`
			SELECT actor_id FROM audit_events
			WHERE event_type = 'certificate.import' AND request_id = $1 AND result = 'failure'`,
			reqID).Scan(&actorID); err != nil {
			t.Fatalf("read failure audit event: %v", err)
		}
		if actorID == nil || *actorID != admin.ID {
			t.Errorf("failure audit actor_id = %v, want %s", actorID, admin.ID)
		}
	})
}
