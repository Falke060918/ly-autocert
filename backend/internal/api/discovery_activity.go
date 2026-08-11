package api

// 통합 작업 목록 (GET /api/v1/discovery/activity).
//
// 스캔 Job·스윕·정기 스캔 실행을 한 목록으로 합쳐 내려준다. 화면이 세 API를
// 각각 폴링해 머릿속에서 합치던 것을 서버 한 곳으로 옮긴 것이다.
//
// 취소 가능 여부는 서버가 cancel 필드로 판정한다 — 상태 문자열로 추론하면
// 상태 머신이 바뀔 때마다 화면이 조용히 어긋난다(store.ListDiscoveryActivity).
// 읽기 전용이라 전 역할이며 감사 이벤트를 남기지 않는다 (00-spec 7.12).

import (
	"log/slog"
	"net/http"
	"strconv"

	"github.com/Falke060918/INHERIT_AutoCert/backend/internal/store"
)

type activityListResponse struct {
	RequestID string               `json:"request_id"`
	Items     []store.ActivityItem `json:"items"`
	Total     int                  `json:"total"`
	Page      int                  `json:"page"`
	PageSize  int                  `json:"page_size"`
}

// parseActivityParams는 kind·active·page 파라미터를 검증해 store 입력으로 바꾼다.
func parseActivityParams(r *http.Request) (store.ListActivityInput, *paramError) {
	in := store.ListActivityInput{}
	for _, v := range splitMulti(r.URL.Query()["kind"]) {
		switch v {
		case store.ActivityKindScan, store.ActivityKindSweep, store.ActivityKindScheduleRun:
			in.Kinds = append(in.Kinds, v)
		default:
			return in, &paramError{"invalid_kind",
				"kind는 scan/sweep/schedule_run 중 하나여야 합니다."}
		}
	}
	if v := r.URL.Query().Get("active"); v != "" {
		b, err := strconv.ParseBool(v)
		if err != nil {
			return in, &paramError{"invalid_active", "active는 true 또는 false여야 합니다."}
		}
		in.Active = &b
	}
	page, pageSize, perr := parsePageParams(r)
	if perr != nil {
		return in, perr
	}
	in.Page, in.PageSize = page, pageSize
	return in, nil
}

// handleListDiscoveryActivity는 GET /api/v1/discovery/activity다.
func (s *Server) handleListDiscoveryActivity(w http.ResponseWriter, r *http.Request) {
	in, perr := parseActivityParams(r)
	if perr != nil {
		writeError(w, r, http.StatusBadRequest, perr.code, perr.message)
		return
	}
	res, err := s.store.ListDiscoveryActivity(r.Context(), in)
	if err != nil {
		slog.Error("discovery: list activity failed", "error", err.Error(),
			"request_id", deref(requestIDFrom(r.Context())))
		writeError(w, r, http.StatusInternalServerError, "internal_error",
			"작업 목록을 조회할 수 없습니다.")
		return
	}
	writeJSON(w, http.StatusOK, activityListResponse{
		RequestID: deref(requestIDFrom(r.Context())),
		Items:     res.Items,
		Total:     res.Total,
		Page:      res.Page,
		PageSize:  res.PageSize,
	})
}
