package api

// 서버(엔드포인트) 축 조회 핸들러 (GET /api/v1/servers, /api/v1/servers/{id}).
//
// 인증서 목록은 "인증서 1행"이라 같은 인증서가 여러 서버에 배포된 관계를 뭉갠다.
// 이 축은 반대로 호스트 1행이고, 포트별 확장은 상세에서 지연 로드한다 —
// 목록에 전 엔드포인트를 실어 보내는 include= 류는 만들지 않는다(page_size 상한을
// 우회하려다 실서버에서 400을 냈던 전례와 같은 함정).
//
// 읽기 전용이라 감사 이벤트를 남기지 않는다 (00-spec 7.12 기록 대상에 조회 없음;
// summary·issuers와 같은 규약). 권한은 인증서 조회와 동일하게 라우트에서 검사한다.
// 응답에 인증서 원문(DER/PEM)·개인키는 포함되지 않으며 로그에도 남기지 않는다.

import (
	"errors"
	"log/slog"
	"net/http"
	"strconv"
	"strings"
	"unicode/utf8"

	"github.com/Falke060918/INHERIT_AutoCert/backend/internal/store"
)

type serverListResponse struct {
	RequestID string                 `json:"request_id"`
	Items     []store.ServerListItem `json:"items"`
	Total     int                    `json:"total"`
	Page      int                    `json:"page"`
	PageSize  int                    `json:"page_size"`
}

type serverDetailResponse struct {
	RequestID string                 `json:"request_id"`
	Server    store.ServerListItem   `json:"server"`
	Endpoints []store.ServerEndpoint `json:"endpoints"`
}

// parseServerScope는 scope 파라미터를 검증한다.
// targets.executor_scope 값 규약(0010)과 같아야 한다 — 'server' 또는 'sensor:<uuid>'.
func parseServerScope(v string) (string, *paramError) {
	if v == "server" {
		return v, nil
	}
	if rest, ok := strings.CutPrefix(v, "sensor:"); ok && isUUID(rest) {
		return v, nil
	}
	return "", &paramError{"invalid_scope", "scope는 server 또는 sensor:<센서 id> 형식이어야 합니다."}
}

// parseServerListParams는 서버 목록 쿼리 파라미터를 검증해 store 입력으로 바꾼다.
// 파라미터: q, expired, expires_within_days, scope, mismatch, sort, order, page, page_size.
//
// 인증서 목록과 필터를 공유하지 않는다 — 여기의 만료 필터는 EXISTS 의미라
// type·self_signed·issuer_org를 그대로 옮기면 뜻이 조용히 달라진다.
func parseServerListParams(r *http.Request) (store.ListServersInput, *paramError) {
	q := r.URL.Query()
	in := store.ListServersInput{Page: 1, PageSize: 50}

	in.Search = strings.TrimSpace(q.Get("q"))
	if utf8.RuneCountInString(in.Search) > maxSearchLen {
		return in, &paramError{"invalid_search", "검색어는 200자 이하여야 합니다."}
	}
	if v := q.Get("expired"); v != "" {
		b, err := strconv.ParseBool(v)
		if err != nil {
			return in, &paramError{"invalid_expired", "expired는 true 또는 false여야 합니다."}
		}
		in.Expired = &b
	}
	if v := q.Get("expires_within_days"); v != "" {
		n, err := strconv.Atoi(v)
		if err != nil || n < 1 || n > 3650 {
			return in, &paramError{"invalid_expires_within_days",
				"expires_within_days는 1~3650 사이 정수여야 합니다."}
		}
		in.ExpiresWithinDays = &n
	}
	if v := q.Get("scope"); v != "" {
		scope, perr := parseServerScope(v)
		if perr != nil {
			return in, perr
		}
		in.Scope = scope
	}
	if v := q.Get("mismatch"); v != "" {
		b, err := strconv.ParseBool(v)
		if err != nil {
			return in, &paramError{"invalid_mismatch", "mismatch는 true 또는 false여야 합니다."}
		}
		in.Mismatch = &b
	}
	if v := q.Get("sort"); v != "" {
		if !store.IsServerSortField(v) {
			return in, &paramError{"invalid_sort",
				"sort는 risk/hostname/endpoints/certificates/last_seen 중 하나여야 합니다."}
		}
		in.SortField = v
	}
	switch q.Get("order") {
	case "", "asc":
	case "desc":
		in.SortDesc = true
	default:
		return in, &paramError{"invalid_order", "order는 asc 또는 desc여야 합니다."}
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

// handleListServers는 GET /api/v1/servers를 처리한다 (호스트 1행 + 포트 미리보기).
func handleListServers(st *store.Store) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		in, perr := parseServerListParams(r)
		if perr != nil {
			writeError(w, r, http.StatusBadRequest, perr.code, perr.message)
			return
		}
		res, err := st.ListServers(r.Context(), in)
		if err != nil {
			slog.Error("servers list failed", "error", err.Error(),
				"request_id", deref(requestIDFrom(r.Context())))
			writeError(w, r, http.StatusInternalServerError, "internal_error",
				"서버 목록 조회에 실패했습니다. 잠시 후 다시 시도하십시오.")
			return
		}
		writeJSON(w, http.StatusOK, serverListResponse{
			RequestID: deref(requestIDFrom(r.Context())),
			Items:     res.Items,
			Total:     res.Total,
			Page:      res.Page,
			PageSize:  res.PageSize,
		})
	}
}

// handleGetServer는 GET /api/v1/servers/{id}를 처리한다 (포트별 엔드포인트 확장).
func handleGetServer(st *store.Store) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		res, err := st.GetServerDetail(r.Context(), r.PathValue("id"))
		if errors.Is(err, store.ErrNotFound) {
			writeError(w, r, http.StatusNotFound, "not_found", "해당 서버를 찾을 수 없습니다.")
			return
		}
		if err != nil {
			slog.Error("server detail failed", "error", err.Error(),
				"request_id", deref(requestIDFrom(r.Context())))
			writeError(w, r, http.StatusInternalServerError, "internal_error",
				"서버 상세 조회에 실패했습니다. 잠시 후 다시 시도하십시오.")
			return
		}
		writeJSON(w, http.StatusOK, serverDetailResponse{
			RequestID: deref(requestIDFrom(r.Context())),
			Server:    res.Server,
			Endpoints: res.Endpoints,
		})
	}
}
