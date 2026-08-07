package api

// 감사 로그 조회·무결성 검증 핸들러 (이슈 #7).
// 권한: Security Administrator(감사 로그 조회)·Auditor(감사 로그 및 보고서 조회) —
// 00-spec 7.11. 읽기 전용이라 감사 이벤트를 남기지 않는다 (7.12 기록 대상에 조회 없음).
// 응답의 before/after에는 기록 시점에 이미 민감값이 배제되어 있다 (보안 하드 규칙 3).

import (
	"log/slog"
	"net/http"
	"strconv"
	"time"

	"github.com/Falke060918/INHERIT_AutoCert/backend/internal/audit"
	"github.com/Falke060918/INHERIT_AutoCert/backend/internal/domain"
)

type auditListResponse struct {
	RequestID string              `json:"request_id"`
	Items     []domain.AuditEvent `json:"items"`
	Total     int                 `json:"total"`
	Page      int                 `json:"page"`
	PageSize  int                 `json:"page_size"`
}

type auditVerifyResponse struct {
	RequestID string `json:"request_id"`
	*audit.VerifyResult
}

// parseAuditListParams는 감사 로그 조회 파라미터를 검증한다.
// 파라미터: actor_id, event_type, from, to (RFC3339), page, page_size.
func parseAuditListParams(r *http.Request) (audit.ListInput, *paramError) {
	q := r.URL.Query()
	in := audit.ListInput{Page: 1, PageSize: 50}

	if v := q.Get("actor_id"); v != "" {
		if !isUUID(v) {
			return in, &paramError{"invalid_actor_id", "actor_id는 UUID여야 합니다."}
		}
		in.ActorID = &v
	}
	if v := q.Get("event_type"); v != "" {
		in.EventType = &v
	}
	if v := q.Get("from"); v != "" {
		t, err := time.Parse(time.RFC3339, v)
		if err != nil {
			return in, &paramError{"invalid_from", "from은 RFC3339 형식(예: 2026-01-02T15:04:05Z)이어야 합니다."}
		}
		in.From = &t
	}
	if v := q.Get("to"); v != "" {
		t, err := time.Parse(time.RFC3339, v)
		if err != nil {
			return in, &paramError{"invalid_to", "to는 RFC3339 형식(예: 2026-01-02T15:04:05Z)이어야 합니다."}
		}
		in.To = &t
	}
	if v := q.Get("page"); v != "" {
		n, err := strconv.Atoi(v)
		if err != nil || n < 1 {
			return in, &paramError{"invalid_page", "page는 1 이상 정수여야 합니다."}
		}
		in.Page = n
	}
	if v := q.Get("page_size"); v != "" {
		n, err := strconv.Atoi(v)
		if err != nil || n < 1 || n > 200 {
			return in, &paramError{"invalid_page_size", "page_size는 1~200 사이 정수여야 합니다."}
		}
		in.PageSize = n
	}
	return in, nil
}

// handleListAuditEvents는 GET /api/v1/audit/events를 처리한다
// (이슈 #7 완료 조건: 사용자·이벤트 유형·기간으로 조회).
func (s *Server) handleListAuditEvents(w http.ResponseWriter, r *http.Request) {
	in, perr := parseAuditListParams(r)
	if perr != nil {
		writeError(w, r, http.StatusBadRequest, perr.code, perr.message)
		return
	}
	res, err := s.audit.List(r.Context(), in)
	if err != nil {
		slog.Error("audit list failed", "error", err.Error(), "request_id", deref(requestIDFrom(r.Context())))
		writeError(w, r, http.StatusInternalServerError, "internal_error",
			"감사 로그 조회에 실패했습니다. 잠시 후 다시 시도하십시오.")
		return
	}
	writeJSON(w, http.StatusOK, auditListResponse{
		RequestID: deref(requestIDFrom(r.Context())),
		Items:     res.Items,
		Total:     res.Total,
		Page:      res.Page,
		PageSize:  res.PageSize,
	})
}

// handleVerifyAuditChain은 GET /api/v1/audit/verify를 처리한다.
// 해시 체인 전체를 재계산해 변조 지점(id 단위)을 특정한다.
// head_hash는 외부 매체 기록(anchoring)용이다 — 꼬리 삭제는 체인만으로 탐지 불가.
func (s *Server) handleVerifyAuditChain(w http.ResponseWriter, r *http.Request) {
	res, err := s.audit.Verify(r.Context())
	if err != nil {
		slog.Error("audit verify failed", "error", err.Error(), "request_id", deref(requestIDFrom(r.Context())))
		writeError(w, r, http.StatusInternalServerError, "internal_error",
			"감사 로그 무결성 검증에 실패했습니다. 잠시 후 다시 시도하십시오.")
		return
	}
	writeJSON(w, http.StatusOK, auditVerifyResponse{
		RequestID:    deref(requestIDFrom(r.Context())),
		VerifyResult: res,
	})
}
