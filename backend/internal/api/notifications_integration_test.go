package api

import (
	"encoding/json"
	"net/http"
	"testing"

	"github.com/Falke060918/INHERIT_AutoCert/backend/internal/auth"
	"github.com/Falke060918/INHERIT_AutoCert/backend/internal/domain"
)

// 알림함 API 검증 (05-notifications 9.1): 본인 것만 보이고, 남의 알림은 404이며,
// 읽음·보관은 감사 이벤트를 남기지 않는다(설계 12 ① 승인된 예외).
// TEST_DATABASE_URL 미설정 시 스킵한다.

// cleanNotifications는 알림 테이블을 비운다(테스트 전용 DB에서만 하는 정리다).
// 순서가 중요하다 — 이벤트를 먼저 지우면 CASCADE로 알림함까지 사라진다.
func (e *testEnv) cleanNotifications(t *testing.T) {
	t.Helper()
	for _, stmt := range []string{
		`DELETE FROM notification_inbox`,
		`DELETE FROM notification_deliveries`,
		`DELETE FROM notification_events`,
		`DELETE FROM notification_policies`,
	} {
		if _, err := e.pool.Exec(stmt); err != nil {
			t.Fatalf("cleanup %q: %v", stmt, err)
		}
	}
}

// insertNotificationEvent는 알림 이벤트 1건을 직접 넣는다(탐지기 없이 알림함을 채우기 위해).
func (e *testEnv) insertNotificationEvent(t *testing.T, dedupKey string) string {
	t.Helper()
	var id string
	err := e.pool.QueryRow(`
		INSERT INTO notification_events (dedup_key, event_type, severity, payload, status)
		VALUES ($1, 'certificate.expiring', 'warning', '{"common_name":"test"}'::jsonb, 'pending')
		RETURNING id`, dedupKey).Scan(&id)
	if err != nil {
		t.Fatalf("insert notification event: %v", err)
	}
	return id
}

func (e *testEnv) insertInboxItem(t *testing.T, userID, eventID, title string) string {
	t.Helper()
	var id string
	err := e.pool.QueryRow(`
		INSERT INTO notification_inbox (user_id, event_id, title, body, severity)
		VALUES ($1::uuid, $2::uuid, $3, 'body', 'warning')
		RETURNING id`, userID, eventID, title).Scan(&id)
	if err != nil {
		t.Fatalf("insert inbox item: %v", err)
	}
	return id
}

type inboxListJSON struct {
	Items []struct {
		ID    string `json:"id"`
		Title string `json:"title"`
	} `json:"items"`
	Total  int `json:"total"`
	Unread int `json:"unread"`
}

func (e *testEnv) listNotifications(t *testing.T, token, query string) inboxListJSON {
	t.Helper()
	resp := e.do(t, http.MethodGet, "/api/v1/notifications"+query, token, nil)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("list notifications%s = %d, want 200", query, resp.StatusCode)
	}
	var out inboxListJSON
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		t.Fatalf("decode notifications: %v", err)
	}
	return out
}

func (e *testEnv) unreadCount(t *testing.T, token string) int {
	t.Helper()
	resp := e.do(t, http.MethodGet, "/api/v1/notifications/unread-count", token, nil)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("unread-count = %d, want 200", resp.StatusCode)
	}
	var out struct {
		Unread int `json:"unread"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		t.Fatalf("decode unread-count: %v", err)
	}
	return out.Unread
}

func TestNotificationInboxAPI(t *testing.T) {
	env := newTestEnv(t)
	env.cleanNotifications(t)
	ctx := t.Context()

	viewerPW, adminPW := randomPassword(t), randomPassword(t)
	viewer, err := env.svc.CreateUser(ctx, "nb-viewer", viewerPW, nil, domain.RoleViewer, nil, auth.AuditMeta{})
	if err != nil {
		t.Fatalf("create viewer: %v", err)
	}
	admin, err := env.svc.CreateUser(ctx, "nb-admin", adminPW, nil, domain.RoleSystemAdministrator, nil, auth.AuditMeta{})
	if err != nil {
		t.Fatalf("create admin: %v", err)
	}
	viewerToken, status := env.login(t, "nb-viewer", viewerPW)
	if status != http.StatusOK {
		t.Fatalf("viewer login = %d, want 200", status)
	}
	adminToken, status := env.login(t, "nb-admin", adminPW)
	if status != http.StatusOK {
		t.Fatalf("admin login = %d, want 200", status)
	}

	eventID := env.insertNotificationEvent(t, "certificate.expiring:test:30")
	viewerItem := env.insertInboxItem(t, viewer.ID, eventID, "viewer 알림")
	adminItem := env.insertInboxItem(t, admin.ID, eventID, "admin 알림")

	t.Run("unauthenticated returns 401", func(t *testing.T) {
		if resp := env.do(t, http.MethodGet, "/api/v1/notifications", "", nil); resp.StatusCode != http.StatusUnauthorized {
			t.Errorf("list without token = %d, want 401", resp.StatusCode)
		}
	})

	t.Run("only own notifications are listed", func(t *testing.T) {
		list := env.listNotifications(t, viewerToken, "")
		if list.Total != 1 || len(list.Items) != 1 {
			t.Fatalf("viewer inbox total = %d (items %d), want 1", list.Total, len(list.Items))
		}
		if list.Items[0].ID != viewerItem || list.Items[0].Title != "viewer 알림" {
			t.Errorf("viewer sees %+v, want own item %s", list.Items[0], viewerItem)
		}
		if list.Unread != 1 {
			t.Errorf("unread = %d, want 1", list.Unread)
		}
	})

	t.Run("another user's notification is 404, not 403", func(t *testing.T) {
		resp := env.do(t, http.MethodPost, "/api/v1/notifications/"+adminItem+"/read", viewerToken, nil)
		if resp.StatusCode != http.StatusNotFound {
			t.Errorf("read foreign item = %d, want 404", resp.StatusCode)
		}
		resp = env.do(t, http.MethodPost, "/api/v1/notifications/"+adminItem+"/archive", viewerToken, nil)
		if resp.StatusCode != http.StatusNotFound {
			t.Errorf("archive foreign item = %d, want 404", resp.StatusCode)
		}
		// 남의 알림은 여전히 그 소유자에게 안 읽음으로 남아 있다.
		if n := env.unreadCount(t, adminToken); n != 1 {
			t.Errorf("admin unread = %d, want 1 (foreign request must not touch it)", n)
		}
	})

	t.Run("malformed id returns 404", func(t *testing.T) {
		resp := env.do(t, http.MethodPost, "/api/v1/notifications/not-a-uuid/read", viewerToken, nil)
		if resp.StatusCode != http.StatusNotFound {
			t.Errorf("malformed id = %d, want 404", resp.StatusCode)
		}
	})

	t.Run("invalid status filter returns 400", func(t *testing.T) {
		resp := env.do(t, http.MethodGet, "/api/v1/notifications?status=bogus", viewerToken, nil)
		if resp.StatusCode != http.StatusBadRequest {
			t.Errorf("bad status filter = %d, want 400", resp.StatusCode)
		}
	})

	t.Run("read then archive", func(t *testing.T) {
		resp := env.do(t, http.MethodPost, "/api/v1/notifications/"+viewerItem+"/read", viewerToken, nil)
		if resp.StatusCode != http.StatusNoContent {
			t.Fatalf("read = %d, want 204", resp.StatusCode)
		}
		if n := env.unreadCount(t, viewerToken); n != 0 {
			t.Errorf("unread after read = %d, want 0", n)
		}
		if list := env.listNotifications(t, viewerToken, "?status=unread"); list.Total != 0 {
			t.Errorf("unread list total = %d, want 0", list.Total)
		}
		if list := env.listNotifications(t, viewerToken, "?status=all"); list.Total != 1 {
			t.Errorf("all list total = %d, want 1", list.Total)
		}

		resp = env.do(t, http.MethodPost, "/api/v1/notifications/"+viewerItem+"/archive", viewerToken, nil)
		if resp.StatusCode != http.StatusNoContent {
			t.Fatalf("archive = %d, want 204", resp.StatusCode)
		}
		if list := env.listNotifications(t, viewerToken, "?status=archived"); list.Total != 1 {
			t.Errorf("archived list total = %d, want 1", list.Total)
		}
		if list := env.listNotifications(t, viewerToken, "?status=all"); list.Total != 0 {
			t.Errorf("all list total after archive = %d, want 0 (archived is excluded)", list.Total)
		}
	})

	t.Run("read-all marks the rest", func(t *testing.T) {
		second := env.insertNotificationEvent(t, "certificate.expiring:test:60")
		env.insertInboxItem(t, viewer.ID, second, "두 번째 알림")
		third := env.insertNotificationEvent(t, "certificate.expiring:test:90")
		env.insertInboxItem(t, viewer.ID, third, "세 번째 알림")
		if n := env.unreadCount(t, viewerToken); n != 2 {
			t.Fatalf("unread before read-all = %d, want 2", n)
		}

		resp := env.do(t, http.MethodPost, "/api/v1/notifications/read-all", viewerToken, nil)
		if resp.StatusCode != http.StatusOK {
			t.Fatalf("read-all = %d, want 200", resp.StatusCode)
		}
		var out struct {
			Updated int `json:"updated"`
		}
		if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
			t.Fatalf("decode read-all: %v", err)
		}
		if out.Updated != 2 {
			t.Errorf("updated = %d, want 2", out.Updated)
		}
		if n := env.unreadCount(t, viewerToken); n != 0 {
			t.Errorf("unread after read-all = %d, want 0", n)
		}
		// read-all은 본인 것만 건드린다.
		if n := env.unreadCount(t, adminToken); n != 1 {
			t.Errorf("admin unread after viewer read-all = %d, want 1", n)
		}
	})

	// 설계 12 ①: 알림함 읽음·보관은 감사 대상이 아니다. 이 단언은 "감사 누락"이
	// 아니라 승인된 예외임을 코드로 고정한다.
	t.Run("inbox mutations create no audit events", func(t *testing.T) {
		var n int
		if err := env.pool.QueryRow(
			`SELECT count(*) FROM audit_events WHERE event_type LIKE 'notification%'`).Scan(&n); err != nil {
			t.Fatalf("count notification audit events: %v", err)
		}
		if n != 0 {
			t.Errorf("notification audit events = %d, want 0 (inbox state is not auditable)", n)
		}
	})
}
