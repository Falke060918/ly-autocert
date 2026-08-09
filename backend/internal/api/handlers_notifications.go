package api

// 알림함 API (docs/design/05-notifications.md 9.1). 전 역할이 쓰며 대상은 언제나
// **본인 소유 행**이다 — 목록·개수 쿼리는 세션 사용자 id를 항상 바인드 조건으로
// 넣고(store.ListInbox), 남의 행을 지정한 읽음·보관은 403이 아니라 404로 답한다
// (존재 여부를 흘리지 않는다, 설계 9.1).
//
// 감사 이벤트를 남기지 않는 것은 누락이 아니라 **승인된 예외**다 (설계 12 ①):
// 여기서 바뀌는 것은 본인 소유 행의 개인적 읽음 상태뿐이고 인벤토리도 설정도
// 바뀌지 않는다. 보안 하드 규칙 5의 "모든 변경 API는 감사 로그를 생성한다"가
// 겨냥하는 대상은 변경 API이지 "내 UI 상태"가 아니다. 정책·채널 변경(감사 대상)은
// handlers_notification_policies.go에 있다.

import (
	"errors"
	"log/slog"
	"net/http"
	"strconv"

	"github.com/Falke060918/INHERIT_AutoCert/backend/internal/store"
)

type notificationListResponse struct {
	RequestID string            `json:"request_id"`
	Items     []store.InboxItem `json:"items"`
	Total     int               `json:"total"`
	Unread    int               `json:"unread"`
	Page      int               `json:"page"`
	PageSize  int               `json:"page_size"`
}

// parsePageParams는 page·page_size를 검증한다(목록 API 공통 규칙과 같은 상한).
func parsePageParams(r *http.Request) (page, pageSize int, perr *paramError) {
	page, pageSize = 1, 50
	q := r.URL.Query()
	if v := q.Get("page"); v != "" {
		n, err := strconv.Atoi(v)
		if err != nil || n < 1 {
			return 0, 0, &paramError{"invalid_page", "page는 1 이상 정수여야 합니다."}
		}
		page = n
	}
	if v := q.Get("page_size"); v != "" {
		n, err := strconv.Atoi(v)
		if err != nil || n < 1 || n > 200 {
			return 0, 0, &paramError{"invalid_page_size", "page_size는 1~200 사이 정수여야 합니다."}
		}
		pageSize = n
	}
	return page, pageSize, nil
}

// handleListNotifications는 GET /api/v1/notifications다.
// status=unread(기본)|all|archived. 본인 알림만 반환한다.
func handleListNotifications(st *store.Store) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		filter := r.URL.Query().Get("status")
		switch filter {
		case "":
			filter = store.InboxFilterUnread
		case store.InboxFilterUnread, store.InboxFilterAll, store.InboxFilterArchived:
		default:
			writeError(w, r, http.StatusBadRequest, "invalid_status",
				"status는 unread/all/archived 중 하나여야 합니다.")
			return
		}
		page, pageSize, perr := parsePageParams(r)
		if perr != nil {
			writeError(w, r, http.StatusBadRequest, perr.code, perr.message)
			return
		}

		user := userFrom(r.Context())
		res, err := st.ListInbox(r.Context(), user.ID, filter, page, pageSize)
		if err != nil {
			slog.Error("notifications list failed", "error", err.Error(),
				"request_id", deref(requestIDFrom(r.Context())))
			writeError(w, r, http.StatusInternalServerError, "internal_error",
				"알림 목록 조회에 실패했습니다. 잠시 후 다시 시도하십시오.")
			return
		}
		writeJSON(w, http.StatusOK, notificationListResponse{
			RequestID: deref(requestIDFrom(r.Context())),
			Items:     res.Items,
			Total:     res.Total,
			Unread:    res.Unread,
			Page:      res.Page,
			PageSize:  res.PageSize,
		})
	}
}

// handleUnreadNotificationCount는 GET /api/v1/notifications/unread-count다(배지용).
func handleUnreadNotificationCount(st *store.Store) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		user := userFrom(r.Context())
		n, err := st.CountUnreadInbox(r.Context(), user.ID)
		if err != nil {
			slog.Error("notifications unread count failed", "error", err.Error(),
				"request_id", deref(requestIDFrom(r.Context())))
			writeError(w, r, http.StatusInternalServerError, "internal_error",
				"안 읽은 알림 수를 조회할 수 없습니다.")
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{
			"request_id": deref(requestIDFrom(r.Context())),
			"unread":     n,
		})
	}
}

// handleMarkNotificationRead는 POST /api/v1/notifications/{id}/read다.
func handleMarkNotificationRead(st *store.Store) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		user := userFrom(r.Context())
		writeInboxMutation(w, r, st.MarkInboxRead(r.Context(), user.ID, r.PathValue("id")),
			"notification mark read failed")
	}
}

// handleArchiveNotification은 POST /api/v1/notifications/{id}/archive다(읽음도 함께 처리).
func handleArchiveNotification(st *store.Store) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		user := userFrom(r.Context())
		writeInboxMutation(w, r, st.ArchiveInboxItem(r.Context(), user.ID, r.PathValue("id")),
			"notification archive failed")
	}
}

// writeInboxMutation은 알림함 단건 변경의 공통 응답이다.
// 남의 행·없는 행·형식이 잘못된 id는 모두 404다 — 소유 여부를 응답으로 흘리지 않는다.
func writeInboxMutation(w http.ResponseWriter, r *http.Request, err error, logMsg string) {
	switch {
	case err == nil:
		w.WriteHeader(http.StatusNoContent)
	case errors.Is(err, store.ErrNotFound):
		writeError(w, r, http.StatusNotFound, "not_found", "해당 알림을 찾을 수 없습니다.")
	default:
		slog.Error(logMsg, "error", err.Error(), "request_id", deref(requestIDFrom(r.Context())))
		writeError(w, r, http.StatusInternalServerError, "internal_error",
			"알림 상태를 변경할 수 없습니다. 잠시 후 다시 시도하십시오.")
	}
}

// handleMarkAllNotificationsRead는 POST /api/v1/notifications/read-all이다.
func handleMarkAllNotificationsRead(st *store.Store) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		user := userFrom(r.Context())
		n, err := st.MarkAllInboxRead(r.Context(), user.ID)
		if err != nil {
			slog.Error("notifications mark all read failed", "error", err.Error(),
				"request_id", deref(requestIDFrom(r.Context())))
			writeError(w, r, http.StatusInternalServerError, "internal_error",
				"알림을 모두 읽음 처리할 수 없습니다.")
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{
			"request_id": deref(requestIDFrom(r.Context())),
			"updated":    n,
		})
	}
}
