package api

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"io"
	"net/http"
	"regexp"
	"strings"
	"testing"

	"github.com/Falke060918/INHERIT_AutoCert/backend/internal/auth"
	"github.com/Falke060918/INHERIT_AutoCert/backend/internal/domain"
	"github.com/Falke060918/INHERIT_AutoCert/backend/internal/store"
)

// Phase 3a U5 검증: 센서 관리자 API의 RBAC(비관리자 403)·토큰 1회 노출·비활성화,
// 그리고 discovery 스캔의 sensor_id 검증(무효·비활성 422, active면 pending Job만 생성).
// TEST_DATABASE_URL 미설정 시 스킵한다 (전용 테스트 DB).

var hex64 = regexp.MustCompile(`^[0-9a-f]{64}$`)

// sensorJSON은 응답의 sensor 필드 디코딩용이다. 토큰 관련 필드는 계약에 없다.
type sensorJSON struct {
	ID                          string  `json:"id"`
	Name                        string  `json:"name"`
	Status                      string  `json:"status"`
	ClientCertFingerprintSHA256 *string `json:"client_cert_fingerprint_sha256"`
	LastSeenAt                  *string `json:"last_seen_at"`
}

type sensorCreateJSON struct {
	RequestID            string     `json:"request_id"`
	Sensor               sensorJSON `json:"sensor"`
	EnrollmentToken      string     `json:"enrollment_token"`
	CAFingerprint        string     `json:"ca_fingerprint"`
	EnrollCommandExample string     `json:"enroll_command_example"`
}

func (e *testEnv) cleanSensors(t *testing.T) {
	t.Helper()
	// scan_jobs가 sensors를 FK로 참조하므로 먼저 지운다. sensor_ca도 지워
	// CA 부트스트랩 경로가 매 실행 검증되게 한다 (테스트 전용 DB에서만 허용).
	for _, stmt := range []string{
		`DELETE FROM scan_jobs`,
		`DELETE FROM sensors`,
		`DELETE FROM sensor_ca`,
	} {
		if _, err := e.pool.Exec(stmt); err != nil {
			t.Fatalf("cleanup %q: %v", stmt, err)
		}
	}
}

// createSensorViaAPI는 관리자 토큰으로 센서를 만들고 생성 응답을 반환한다.
func (e *testEnv) createSensorViaAPI(t *testing.T, adminToken, name string) sensorCreateJSON {
	t.Helper()
	resp := e.do(t, http.MethodPost, "/api/v1/sensors", adminToken, map[string]string{"name": name})
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("create sensor = %d, want 201", resp.StatusCode)
	}
	var out sensorCreateJSON
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		t.Fatalf("decode create sensor response: %v", err)
	}
	return out
}

func TestSensorsAdminAPI(t *testing.T) {
	env := newTestEnv(t)
	env.cleanSensors(t)
	ctx := t.Context()

	adminPW := randomPassword(t)
	operatorPW := randomPassword(t)
	if _, err := env.svc.CreateUser(ctx, "sn-admin", adminPW, nil, domain.RoleSystemAdministrator, nil, auth.AuditMeta{}); err != nil {
		t.Fatalf("create admin: %v", err)
	}
	// operator는 스캔은 할 수 있지만 센서 관리는 못 하는 역할 — RBAC 경계 검증에 쓴다.
	if _, err := env.svc.CreateUser(ctx, "sn-operator", operatorPW, nil, domain.RoleOperator, nil, auth.AuditMeta{}); err != nil {
		t.Fatalf("create operator: %v", err)
	}
	adminToken, status := env.login(t, "sn-admin", adminPW)
	if status != http.StatusOK {
		t.Fatalf("admin login = %d, want 200", status)
	}
	operatorToken, status := env.login(t, "sn-operator", operatorPW)
	if status != http.StatusOK {
		t.Fatalf("operator login = %d, want 200", status)
	}

	t.Run("non-admin cannot create or disable (403)", func(t *testing.T) {
		if resp := env.do(t, http.MethodPost, "/api/v1/sensors", operatorToken,
			map[string]string{"name": "seg-x"}); resp.StatusCode != http.StatusForbidden {
			t.Errorf("operator create sensor = %d, want 403", resp.StatusCode)
		}
		if resp := env.do(t, http.MethodPost,
			"/api/v1/sensors/00000000-0000-0000-0000-000000000000/disable",
			operatorToken, nil); resp.StatusCode != http.StatusForbidden {
			t.Errorf("operator disable sensor = %d, want 403", resp.StatusCode)
		}
	})

	created := env.createSensorViaAPI(t, adminToken, "seg-c")

	t.Run("create issues one-time token and bootstraps CA", func(t *testing.T) {
		if created.Sensor.Status != "pending" {
			t.Errorf("sensor status = %q, want pending", created.Sensor.Status)
		}
		if !hex64.MatchString(created.EnrollmentToken) {
			t.Errorf("enrollment_token is not 64-char lowercase hex")
		}
		if !hex64.MatchString(created.CAFingerprint) {
			t.Errorf("ca_fingerprint is not 64-char lowercase hex")
		}
		if !strings.Contains(created.EnrollCommandExample, created.EnrollmentToken) ||
			!strings.Contains(created.EnrollCommandExample, created.CAFingerprint) {
			t.Errorf("enroll_command_example does not carry token and ca fingerprint")
		}
		if n := env.auditCount(t, store.EventSensorTokenIssue, domain.AuditSuccess); n != 1 {
			t.Errorf("sensor.token.issue audit events = %d, want 1", n)
		}
		// 감사 이벤트에 토큰 평문·해시가 없어야 한다 (보안 하드 규칙).
		sum := sha256.Sum256([]byte(created.EnrollmentToken))
		var leaked int
		if err := env.pool.QueryRow(
			`SELECT count(*) FROM audit_events WHERE after_value::text LIKE '%' || $1 || '%'
			    OR after_value::text LIKE '%' || $2 || '%'`,
			created.EnrollmentToken, hex.EncodeToString(sum[:])).Scan(&leaked); err != nil {
			t.Fatalf("audit leak query: %v", err)
		}
		if leaked != 0 {
			t.Errorf("enrollment token (or its hash) leaked into audit events")
		}
	})

	t.Run("token never exposed after creation", func(t *testing.T) {
		for _, path := range []string{"/api/v1/sensors", "/api/v1/sensors/" + created.Sensor.ID} {
			resp := env.do(t, http.MethodGet, path, operatorToken, nil)
			if resp.StatusCode != http.StatusOK {
				t.Fatalf("GET %s = %d, want 200", path, resp.StatusCode)
			}
			body, err := io.ReadAll(resp.Body)
			if err != nil {
				t.Fatalf("read body: %v", err)
			}
			if strings.Contains(string(body), created.EnrollmentToken) {
				t.Errorf("GET %s response contains the enrollment token", path)
			}
			if strings.Contains(string(body), "enrollment_token") {
				t.Errorf("GET %s response carries an enrollment_token field", path)
			}
		}
	})

	t.Run("list wraps items with request_id", func(t *testing.T) {
		resp := env.do(t, http.MethodGet, "/api/v1/sensors", operatorToken, nil)
		if resp.StatusCode != http.StatusOK {
			t.Fatalf("list sensors = %d, want 200", resp.StatusCode)
		}
		var out struct {
			RequestID string       `json:"request_id"`
			Items     []sensorJSON `json:"items"`
		}
		if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
			t.Fatalf("decode list: %v", err)
		}
		if out.RequestID == "" {
			t.Error("list response request_id is empty")
		}
		if len(out.Items) != 1 || out.Items[0].ID != created.Sensor.ID {
			t.Errorf("list items = %+v, want the created sensor only", out.Items)
		}
	})

	t.Run("ca fingerprint is stable across creations", func(t *testing.T) {
		second := env.createSensorViaAPI(t, adminToken, "seg-d")
		if second.CAFingerprint != created.CAFingerprint {
			t.Errorf("second create ca_fingerprint differs — CA must be bootstrapped once")
		}
		if second.EnrollmentToken == created.EnrollmentToken {
			t.Error("two sensors received the same enrollment token")
		}
	})

	t.Run("get unknown or malformed id returns 404", func(t *testing.T) {
		if resp := env.do(t, http.MethodGet, "/api/v1/sensors/not-a-uuid", operatorToken, nil); resp.StatusCode != http.StatusNotFound {
			t.Errorf("get malformed id = %d, want 404", resp.StatusCode)
		}
		if resp := env.do(t, http.MethodGet,
			"/api/v1/sensors/00000000-0000-0000-0000-000000000000", operatorToken, nil); resp.StatusCode != http.StatusNotFound {
			t.Errorf("get unknown id = %d, want 404", resp.StatusCode)
		}
	})

	t.Run("disable transitions and is not repeatable", func(t *testing.T) {
		resp := env.do(t, http.MethodPost, "/api/v1/sensors/"+created.Sensor.ID+"/disable", adminToken, nil)
		if resp.StatusCode != http.StatusOK {
			t.Fatalf("disable = %d, want 200", resp.StatusCode)
		}
		var out struct {
			Sensor sensorJSON `json:"sensor"`
		}
		if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
			t.Fatalf("decode disable: %v", err)
		}
		if out.Sensor.Status != "disabled" {
			t.Errorf("disabled sensor status = %q, want disabled", out.Sensor.Status)
		}
		if n := env.auditCount(t, store.EventSensorDisable, domain.AuditSuccess); n != 1 {
			t.Errorf("sensor.disable audit events = %d, want 1", n)
		}
		if resp := env.do(t, http.MethodPost,
			"/api/v1/sensors/"+created.Sensor.ID+"/disable", adminToken, nil); resp.StatusCode != http.StatusNotFound {
			t.Errorf("second disable = %d, want 404", resp.StatusCode)
		}
	})
}

func TestScanWithSensorID(t *testing.T) {
	env := newTestEnv(t)
	env.cleanSensors(t)
	ctx := t.Context()

	adminPW := randomPassword(t)
	if _, err := env.svc.CreateUser(ctx, "sn-scan-admin", adminPW, nil, domain.RoleSystemAdministrator, nil, auth.AuditMeta{}); err != nil {
		t.Fatalf("create admin: %v", err)
	}
	adminToken, status := env.login(t, "sn-scan-admin", adminPW)
	if status != http.StatusOK {
		t.Fatalf("admin login = %d, want 200", status)
	}

	created := env.createSensorViaAPI(t, adminToken, "seg-scan")

	scanBody := func(sensorID string) map[string]any {
		return map[string]any{
			"targets":   []string{"192.0.2.10"},
			"ports":     []int{443},
			"sensor_id": sensorID,
		}
	}

	t.Run("pending (not yet enrolled) sensor is rejected with 422", func(t *testing.T) {
		if resp := env.do(t, http.MethodPost, "/api/v1/discovery/scans", adminToken,
			scanBody(created.Sensor.ID)); resp.StatusCode != http.StatusUnprocessableEntity {
			t.Errorf("scan with pending sensor = %d, want 422", resp.StatusCode)
		}
	})

	t.Run("malformed and unknown sensor_id are rejected with 422", func(t *testing.T) {
		if resp := env.do(t, http.MethodPost, "/api/v1/discovery/scans", adminToken,
			scanBody("not-a-uuid")); resp.StatusCode != http.StatusUnprocessableEntity {
			t.Errorf("scan with malformed sensor_id = %d, want 422", resp.StatusCode)
		}
		if resp := env.do(t, http.MethodPost, "/api/v1/discovery/scans", adminToken,
			scanBody("00000000-0000-0000-0000-000000000000")); resp.StatusCode != http.StatusUnprocessableEntity {
			t.Errorf("scan with unknown sensor_id = %d, want 422", resp.StatusCode)
		}
	})

	// 발급받은 토큰으로 등록해 active로 만든다 (토큰은 여기서 1회 소진된다).
	// fingerprint는 테스트 전용 난수 인증서 지문 형태다 — 실제 키·인증서를 쓰지 않는다.
	fpRaw := make([]byte, 32)
	if _, err := rand.Read(fpRaw); err != nil {
		t.Fatalf("random fingerprint: %v", err)
	}
	st := store.New(env.pool)
	if _, err := st.EnrollSensor(ctx, store.EnrollSensorInput{
		Token:                       created.EnrollmentToken,
		ClientCertFingerprintSHA256: hex.EncodeToString(fpRaw),
	}); err != nil {
		t.Fatalf("enroll sensor: %v", err)
	}

	t.Run("active sensor creates a pending sensor job without server execution", func(t *testing.T) {
		resp := env.do(t, http.MethodPost, "/api/v1/discovery/scans", adminToken, scanBody(created.Sensor.ID))
		if resp.StatusCode != http.StatusAccepted {
			t.Fatalf("scan with active sensor = %d, want 202", resp.StatusCode)
		}
		var out struct {
			JobID  string `json:"job_id"`
			Status string `json:"status"`
		}
		if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
			t.Fatalf("decode scan accepted: %v", err)
		}
		if out.Status != "pending" {
			t.Errorf("accepted status = %q, want pending", out.Status)
		}

		var jobStatus, executorType string
		var sensorID *string
		if err := env.pool.QueryRow(
			`SELECT status, executor_type, sensor_id::text FROM scan_jobs WHERE id = $1::uuid`,
			out.JobID).Scan(&jobStatus, &executorType, &sensorID); err != nil {
			t.Fatalf("query scan job: %v", err)
		}
		if jobStatus != "pending" {
			t.Errorf("job status = %q, want pending (server must not run sensor jobs)", jobStatus)
		}
		if executorType != "sensor" {
			t.Errorf("executor_type = %q, want sensor", executorType)
		}
		if sensorID == nil || *sensorID != created.Sensor.ID {
			t.Errorf("job sensor_id = %v, want %s", sensorID, created.Sensor.ID)
		}
	})

	t.Run("disabled sensor is rejected with 422", func(t *testing.T) {
		if resp := env.do(t, http.MethodPost,
			"/api/v1/sensors/"+created.Sensor.ID+"/disable", adminToken, nil); resp.StatusCode != http.StatusOK {
			t.Fatalf("disable = %d, want 200", resp.StatusCode)
		}
		if resp := env.do(t, http.MethodPost, "/api/v1/discovery/scans", adminToken,
			scanBody(created.Sensor.ID)); resp.StatusCode != http.StatusUnprocessableEntity {
			t.Errorf("scan with disabled sensor = %d, want 422", resp.StatusCode)
		}
	})
}
