package api

// 이슈 #7 완료 조건 검증: 감사 이벤트가 사용자·이벤트 유형·기간으로 조회되고,
// 무결성 검증 API가 체인 상태(변조 지점 포함)를 반환한다. 권한은 00-spec 7.11 —
// Security Administrator·Auditor만 감사 로그를 본다.
// TEST_DATABASE_URL 미설정 시 스킵한다 (전용 테스트 DB).

import (
	"encoding/json"
	"net/http"
	"regexp"
	"testing"
	"time"

	"github.com/Falke060918/INHERIT_AutoCert/backend/internal/auth"
	"github.com/Falke060918/INHERIT_AutoCert/backend/internal/domain"
)

type auditListJSON struct {
	Items []struct {
		ID            int64   `json:"id"`
		EventType     string  `json:"event_type"`
		ActorID       *string `json:"actor_id"`
		Result        string  `json:"result"`
		IntegrityHash string  `json:"integrity_hash"`
		PrevHash      string  `json:"prev_hash"`
	} `json:"items"`
	Total    int `json:"total"`
	Page     int `json:"page"`
	PageSize int `json:"page_size"`
}

type auditVerifyJSON struct {
	Valid       bool    `json:"valid"`
	TotalEvents int64   `json:"total_events"`
	HeadHash    *string `json:"head_hash"`
	Invalid     []struct {
		ID           int64 `json:"id"`
		HashMismatch bool  `json:"hash_mismatch"`
		ChainBroken  bool  `json:"chain_broken"`
	} `json:"invalid"`
}

func TestAuditQueryAndVerifyAPI(t *testing.T) {
	env := newTestEnv(t)
	ctx := t.Context()

	auditorPW, viewerPW := randomPassword(t), randomPassword(t)
	auditor, err := env.svc.CreateUser(ctx, "aud-auditor", auditorPW, nil,
		domain.RoleAuditor, nil, auth.AuditMeta{})
	if err != nil {
		t.Fatalf("create auditor: %v", err)
	}
	if _, err := env.svc.CreateUser(ctx, "aud-viewer", viewerPW, nil,
		domain.RoleViewer, nil, auth.AuditMeta{}); err != nil {
		t.Fatalf("create viewer: %v", err)
	}
	auditorToken, status := env.login(t, "aud-auditor", auditorPW)
	if status != http.StatusOK {
		t.Fatalf("auditor login = %d, want 200", status)
	}
	viewerToken, status := env.login(t, "aud-viewer", viewerPW)
	if status != http.StatusOK {
		t.Fatalf("viewer login = %d, want 200", status)
	}

	t.Run("viewer is denied audit access with 403", func(t *testing.T) {
		for _, path := range []string{"/api/v1/audit/events", "/api/v1/audit/verify"} {
			if resp := env.do(t, http.MethodGet, path, viewerToken, nil); resp.StatusCode != http.StatusForbidden {
				t.Errorf("viewer GET %s = %d, want 403", path, resp.StatusCode)
			}
		}
	})

	t.Run("auditor lists events", func(t *testing.T) {
		resp := env.do(t, http.MethodGet, "/api/v1/audit/events", auditorToken, nil)
		if resp.StatusCode != http.StatusOK {
			t.Fatalf("list = %d, want 200", resp.StatusCode)
		}
		var out auditListJSON
		if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
			t.Fatalf("decode: %v", err)
		}
		// 최소: user.created 2건 + user.login 성공 2건 + viewer의 authz.denied 2건.
		if out.Total < 6 || len(out.Items) < 6 {
			t.Errorf("total = %d items = %d, want >= 6", out.Total, len(out.Items))
		}
		hexRe := regexp.MustCompile(`^[0-9a-f]{64}$`)
		for _, it := range out.Items {
			if !hexRe.MatchString(it.IntegrityHash) || !hexRe.MatchString(it.PrevHash) {
				t.Errorf("event %d: integrity/prev hash malformed", it.ID)
			}
		}
	})

	t.Run("filters by actor, event type and period", func(t *testing.T) {
		resp := env.do(t, http.MethodGet,
			"/api/v1/audit/events?event_type=user.login&actor_id="+auditor.ID, auditorToken, nil)
		if resp.StatusCode != http.StatusOK {
			t.Fatalf("filtered list = %d, want 200", resp.StatusCode)
		}
		var out auditListJSON
		if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
			t.Fatalf("decode: %v", err)
		}
		if out.Total != 1 {
			t.Errorf("filtered total = %d, want 1 (auditor 로그인 성공 1건)", out.Total)
		}
		for _, it := range out.Items {
			if it.EventType != "user.login" || it.ActorID == nil || *it.ActorID != auditor.ID {
				t.Errorf("filtered item %d: type=%s actor=%v, want user.login/%s", it.ID, it.EventType, it.ActorID, auditor.ID)
			}
		}

		// 기간 필터: 과거로 한정하면 0건.
		past := time.Now().UTC().Add(-time.Hour).Format(time.RFC3339)
		resp = env.do(t, http.MethodGet, "/api/v1/audit/events?to="+past, auditorToken, nil)
		if resp.StatusCode != http.StatusOK {
			t.Fatalf("period list = %d, want 200", resp.StatusCode)
		}
		out = auditListJSON{}
		if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
			t.Fatalf("decode: %v", err)
		}
		if out.Total != 0 {
			t.Errorf("past-only total = %d, want 0", out.Total)
		}
	})

	t.Run("invalid params return 400", func(t *testing.T) {
		for _, q := range []string{"actor_id=not-a-uuid", "from=yesterday", "page=0", "page_size=9999"} {
			if resp := env.do(t, http.MethodGet, "/api/v1/audit/events?"+q, auditorToken, nil); resp.StatusCode != http.StatusBadRequest {
				t.Errorf("GET ?%s = %d, want 400", q, resp.StatusCode)
			}
		}
	})

	t.Run("verify reports valid chain, then pinpoints manual tampering", func(t *testing.T) {
		verify := func() auditVerifyJSON {
			t.Helper()
			resp := env.do(t, http.MethodGet, "/api/v1/audit/verify", auditorToken, nil)
			if resp.StatusCode != http.StatusOK {
				t.Fatalf("verify = %d, want 200", resp.StatusCode)
			}
			var out auditVerifyJSON
			if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
				t.Fatalf("decode verify: %v", err)
			}
			return out
		}

		out := verify()
		if !out.Valid || out.TotalEvents < 6 || out.HeadHash == nil {
			t.Fatalf("verify = %+v, want valid with head_hash", out)
		}

		// 슈퍼유저(테이블 소유자)가 트리거를 우회해 수동 변조 → 검증이 지점을 특정.
		// 원본 값을 보존했다가 되돌려 체인을 복구한다 (테스트 전용 DB 조작).
		var tamperedID int64
		var origResult string
		if err := env.pool.QueryRow(
			`SELECT id, result FROM audit_events WHERE event_type = 'user.created' ORDER BY id LIMIT 1`).
			Scan(&tamperedID, &origResult); err != nil {
			t.Fatalf("pick row: %v", err)
		}
		setResult := func(v string) {
			t.Helper()
			if _, err := env.pool.Exec(`ALTER TABLE audit_events DISABLE TRIGGER USER`); err != nil {
				t.Fatalf("disable triggers: %v", err)
			}
			if _, err := env.pool.Exec(
				`UPDATE audit_events SET result = $1 WHERE id = $2`, v, tamperedID); err != nil {
				t.Fatalf("tamper update: %v", err)
			}
			if _, err := env.pool.Exec(`ALTER TABLE audit_events ENABLE TRIGGER USER`); err != nil {
				t.Fatalf("enable triggers: %v", err)
			}
		}
		setResult("failure")
		restored := false
		t.Cleanup(func() {
			if !restored {
				setResult(origResult)
			}
		})

		out = verify()
		if out.Valid {
			t.Fatal("verify.Valid = true after manual tampering, want false")
		}
		if len(out.Invalid) != 1 || out.Invalid[0].ID != tamperedID || !out.Invalid[0].HashMismatch {
			t.Fatalf("invalid = %+v, want exactly [{id:%d hash_mismatch}]", out.Invalid, tamperedID)
		}

		setResult(origResult)
		restored = true
		if out = verify(); !out.Valid {
			t.Errorf("verify.Valid = false after restore, want true")
		}
	})
}
