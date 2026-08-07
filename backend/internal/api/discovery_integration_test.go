package api

import (
	"encoding/json"
	"net"
	"net/http"
	"net/http/httptest"
	"strconv"
	"testing"
	"time"

	"github.com/Falke060918/INHERIT_AutoCert/backend/internal/domain"
)

// 이슈 #5 완료 조건 검증: 대상을 입력하면 스캔 Job이 생성되고 도달 가능한 TLS
// 엔드포인트의 인증서가 인벤토리에 저장된다. + RBAC/감사.
// TEST_DATABASE_URL 미설정 시 스킵한다 (전용 테스트 DB).

type scanJobJSON struct {
	ID                 string  `json:"id"`
	Status             string  `json:"status"`
	Ports              []int   `json:"ports"`
	TotalEndpoints     int     `json:"total_endpoints"`
	ScannedEndpoints   int     `json:"scanned_endpoints"`
	ReachableEndpoints int     `json:"reachable_endpoints"`
	CertificatesFound  int     `json:"certificates_found"`
	CertificatesNew    int     `json:"certificates_new"`
	Error              *string `json:"error"`
}

func (e *testEnv) cleanInventory(t *testing.T) {
	t.Helper()
	for _, stmt := range []string{
		`DELETE FROM certificate_locations`,
		`DELETE FROM scan_jobs`,
		`DELETE FROM certificates`,
		`DELETE FROM targets`,
	} {
		if _, err := e.pool.Exec(stmt); err != nil {
			t.Fatalf("cleanup %q: %v", stmt, err)
		}
	}
}

func TestDiscoveryScanInventoryAndRBAC(t *testing.T) {
	env := newTestEnv(t)
	env.cleanInventory(t)
	ctx := t.Context()

	adminPW := randomPassword(t)
	viewerPW := randomPassword(t)
	if _, err := env.svc.CreateUser(ctx, "disc-admin", adminPW, nil, domain.RoleSystemAdministrator); err != nil {
		t.Fatalf("create admin: %v", err)
	}
	if _, err := env.svc.CreateUser(ctx, "disc-viewer", viewerPW, nil, domain.RoleViewer); err != nil {
		t.Fatalf("create viewer: %v", err)
	}
	adminToken, status := env.login(t, "disc-admin", adminPW)
	if status != http.StatusOK {
		t.Fatalf("admin login = %d, want 200", status)
	}
	viewerToken, status := env.login(t, "disc-viewer", viewerPW)
	if status != http.StatusOK {
		t.Fatalf("viewer login = %d, want 200", status)
	}

	// 스캔 대상: 프로세스 내 자기서명 TLS 서버.
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}))
	defer srv.Close()
	host, portStr, err := net.SplitHostPort(srv.Listener.Addr().String())
	if err != nil {
		t.Fatalf("split host port: %v", err)
	}
	port, _ := strconv.Atoi(portStr)

	scanBody := map[string]any{
		"targets":    []string{host},
		"ports":      []int{port},
		"timeout_ms": 2000,
	}

	t.Run("viewer is denied scan creation with 403", func(t *testing.T) {
		resp := env.do(t, http.MethodPost, "/api/v1/discovery/scans", viewerToken, scanBody)
		if resp.StatusCode != http.StatusForbidden {
			t.Errorf("viewer create scan = %d, want 403", resp.StatusCode)
		}
	})

	t.Run("invalid input returns 400", func(t *testing.T) {
		resp := env.do(t, http.MethodPost, "/api/v1/discovery/scans", adminToken,
			map[string]any{"targets": []string{"not-an-ip"}, "ports": []int{443}})
		if resp.StatusCode != http.StatusBadRequest {
			t.Errorf("invalid target scan = %d, want 400", resp.StatusCode)
		}
	})

	// 스캔 생성.
	resp := env.do(t, http.MethodPost, "/api/v1/discovery/scans", adminToken, scanBody)
	if resp.StatusCode != http.StatusAccepted {
		t.Fatalf("admin create scan = %d, want 202", resp.StatusCode)
	}
	var created struct {
		JobID          string `json:"job_id"`
		Status         string `json:"status"`
		TotalEndpoints int    `json:"total_endpoints"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&created); err != nil {
		t.Fatalf("decode create response: %v", err)
	}
	if created.JobID == "" {
		t.Fatal("empty job id")
	}
	if created.TotalEndpoints != 1 {
		t.Errorf("total_endpoints = %d, want 1", created.TotalEndpoints)
	}

	// 완료까지 폴링.
	var job scanJobJSON
	deadline := time.Now().Add(15 * time.Second)
	for time.Now().Before(deadline) {
		r := env.do(t, http.MethodGet, "/api/v1/discovery/scans/"+created.JobID, adminToken, nil)
		if r.StatusCode != http.StatusOK {
			t.Fatalf("get scan = %d, want 200", r.StatusCode)
		}
		if err := json.NewDecoder(r.Body).Decode(&job); err != nil {
			t.Fatalf("decode scan job: %v", err)
		}
		if job.Status == "completed" || job.Status == "failed" {
			break
		}
		time.Sleep(200 * time.Millisecond)
	}

	if job.Status != "completed" {
		t.Fatalf("scan status = %q (error=%v), want completed", job.Status, job.Error)
	}
	if job.ReachableEndpoints < 1 {
		t.Errorf("reachable_endpoints = %d, want >= 1", job.ReachableEndpoints)
	}
	if job.CertificatesNew < 1 {
		t.Errorf("certificates_new = %d, want >= 1", job.CertificatesNew)
	}

	// 인벤토리 반영 확인: certificate + network_endpoint 위치가 저장됐다.
	var certCount int
	if err := env.pool.QueryRow(`SELECT count(*) FROM certificates`).Scan(&certCount); err != nil {
		t.Fatalf("count certificates: %v", err)
	}
	if certCount < 1 {
		t.Errorf("certificates in inventory = %d, want >= 1", certCount)
	}
	var locCount int
	if err := env.pool.QueryRow(
		`SELECT count(*) FROM certificate_locations WHERE location_type = 'network_endpoint' AND port = $1`,
		port).Scan(&locCount); err != nil {
		t.Fatalf("count locations: %v", err)
	}
	if locCount < 1 {
		t.Errorf("network_endpoint locations at port %d = %d, want >= 1", port, locCount)
	}

	// 감사: 스캔 생성 + 완료 이벤트가 남았다.
	if n := env.auditCount(t, "discovery.scan", domain.AuditSuccess); n < 2 {
		t.Errorf("discovery.scan success audit events = %d, want >= 2 (created + completed)", n)
	}

	// 재스캔은 중복 인증서를 만들지 않는다(중복 실행 안전).
	resp2 := env.do(t, http.MethodPost, "/api/v1/discovery/scans", adminToken, scanBody)
	if resp2.StatusCode != http.StatusAccepted {
		t.Fatalf("rescan create = %d, want 202", resp2.StatusCode)
	}
	var created2 struct {
		JobID string `json:"job_id"`
	}
	if err := json.NewDecoder(resp2.Body).Decode(&created2); err != nil {
		t.Fatalf("decode rescan response: %v", err)
	}
	deadline = time.Now().Add(15 * time.Second)
	for time.Now().Before(deadline) {
		r := env.do(t, http.MethodGet, "/api/v1/discovery/scans/"+created2.JobID, adminToken, nil)
		if err := json.NewDecoder(r.Body).Decode(&job); err != nil {
			t.Fatalf("decode rescan job: %v", err)
		}
		if job.Status == "completed" || job.Status == "failed" {
			break
		}
		time.Sleep(200 * time.Millisecond)
	}
	var certCountAfter int
	if err := env.pool.QueryRow(`SELECT count(*) FROM certificates`).Scan(&certCountAfter); err != nil {
		t.Fatalf("count certificates after rescan: %v", err)
	}
	if certCountAfter != certCount {
		t.Errorf("certificate count changed on rescan: %d -> %d (fingerprint dedup broken)", certCount, certCountAfter)
	}

	t.Run("unknown job id returns 404", func(t *testing.T) {
		r := env.do(t, http.MethodGet, "/api/v1/discovery/scans/00000000-0000-0000-0000-000000000000", adminToken, nil)
		if r.StatusCode != http.StatusNotFound {
			t.Errorf("unknown scan = %d, want 404", r.StatusCode)
		}
	})

	t.Run("malformed job id returns 404", func(t *testing.T) {
		r := env.do(t, http.MethodGet, "/api/v1/discovery/scans/not-a-uuid", adminToken, nil)
		if r.StatusCode != http.StatusNotFound {
			t.Errorf("malformed scan id = %d, want 404", r.StatusCode)
		}
	})
}
