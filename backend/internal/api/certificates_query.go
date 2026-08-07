package api

// 인증서 목록·상세 조회 핸들러 (이슈 #4).
// 읽기 전용 API라 감사 이벤트를 남기지 않는다 (00-spec 7.12 기록 대상에 조회 없음;
// 데이터 내보내기는 이번 범위에 없다). 응답에 인증서 원문(DER/PEM)은 포함되지 않으며
// 로그에도 남기지 않는다 (보안 하드 규칙 3).

import (
	"errors"
	"log/slog"
	"net/http"
	"strconv"
	"strings"
	"unicode/utf8"

	"github.com/Falke060918/INHERIT_AutoCert/backend/internal/domain"
	"github.com/Falke060918/INHERIT_AutoCert/backend/internal/store"
)

// maxSearchLen은 검색어 상한(룬 기준)이다.
const maxSearchLen = 200

type certificateListResponse struct {
	RequestID string               `json:"request_id"`
	Items     []domain.Certificate `json:"items"`
	Total     int                  `json:"total"`
	Page      int                  `json:"page"`
	PageSize  int                  `json:"page_size"`
}

type certificateDetailResponse struct {
	RequestID   string                     `json:"request_id"`
	Certificate domain.Certificate         `json:"certificate"`
	Locations   []store.LocationWithTarget `json:"locations"`
	Chain       []store.ChainItem          `json:"chain"` // 자신→상위→루트 순, 미보유 상위는 stored=false
}

// paramError는 쿼리 파라미터 검증 실패다 (통일 오류 형식의 code·message로 그대로 나간다).
type paramError struct {
	code    string
	message string
}

// splitMulti는 반복 파라미터와 쉼표 구분을 모두 허용해 값 목록으로 편다.
func splitMulti(vals []string) []string {
	var out []string
	for _, v := range vals {
		for _, part := range strings.Split(v, ",") {
			if p := strings.TrimSpace(part); p != "" {
				out = append(out, p)
			}
		}
	}
	return out
}

// parseListParams는 목록 쿼리 파라미터를 검증해 store 입력으로 변환한다.
// 파라미터: q, status, type, expired, expires_within_days, self_signed, unowned,
// sort, order, page, page_size.
func parseListParams(r *http.Request) (store.ListCertificatesInput, *paramError) {
	q := r.URL.Query()
	in := store.ListCertificatesInput{Page: 1, PageSize: 50}

	in.Search = strings.TrimSpace(q.Get("q"))
	if utf8.RuneCountInString(in.Search) > maxSearchLen {
		return in, &paramError{"invalid_search", "검색어는 200자 이하여야 합니다."}
	}
	for _, v := range splitMulti(q["status"]) {
		s := domain.CertificateStatus(v)
		switch s {
		case domain.CertStatusActive, domain.CertStatusExpired, domain.CertStatusRevoked,
			domain.CertStatusReplaced, domain.CertStatusUnknown:
			in.Statuses = append(in.Statuses, s)
		default:
			return in, &paramError{"invalid_status",
				"status는 active/expired/revoked/replaced/unknown 중 하나여야 합니다."}
		}
	}
	for _, v := range splitMulti(q["type"]) {
		ct := domain.CertificateType(v)
		switch ct {
		case domain.CertTypeServer, domain.CertTypeClient, domain.CertTypeCA,
			domain.CertTypeCodeSigning, domain.CertTypeEmail, domain.CertTypeOther:
			in.Types = append(in.Types, ct)
		default:
			return in, &paramError{"invalid_type",
				"type은 server/client/ca/code_signing/email/other 중 하나여야 합니다."}
		}
	}
	if v := q.Get("expired"); v != "" {
		b, err := strconv.ParseBool(v)
		if err != nil {
			return in, &paramError{"invalid_expired", "expired는 true 또는 false여야 합니다."}
		}
		in.Expired = &b
	}
	if v := q.Get("self_signed"); v != "" {
		b, err := strconv.ParseBool(v)
		if err != nil {
			return in, &paramError{"invalid_self_signed", "self_signed는 true 또는 false여야 합니다."}
		}
		in.SelfSigned = &b
	}
	if v := q.Get("unowned"); v != "" {
		b, err := strconv.ParseBool(v)
		if err != nil {
			return in, &paramError{"invalid_unowned", "unowned는 true 또는 false여야 합니다."}
		}
		in.Unowned = &b
	}
	if v := q.Get("expires_within_days"); v != "" {
		n, err := strconv.Atoi(v)
		if err != nil || n < 1 || n > 3650 {
			return in, &paramError{"invalid_expires_within_days",
				"expires_within_days는 1~3650 사이 정수여야 합니다."}
		}
		in.ExpiresWithinDays = &n
	}
	if v := q.Get("sort"); v != "" {
		if !store.IsListSortField(v) {
			return in, &paramError{"invalid_sort",
				"sort는 not_after/not_before/common_name/created_at/updated_at/status/certificate_type/public_key_size 중 하나여야 합니다."}
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

// handleListCertificates는 GET /api/v1/certificates를 처리한다.
// 검색·상태/만료/타입 필터·정렬·페이지네이션을 지원한다 (00-spec 13장·7.3).
func handleListCertificates(st *store.Store) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		in, perr := parseListParams(r)
		if perr != nil {
			writeError(w, r, http.StatusBadRequest, perr.code, perr.message)
			return
		}
		res, err := st.ListCertificates(r.Context(), in)
		if err != nil {
			slog.Error("certificates list failed", "error", err.Error(), "request_id", deref(requestIDFrom(r.Context())))
			writeError(w, r, http.StatusInternalServerError, "internal_error",
				"인증서 목록 조회에 실패했습니다. 잠시 후 다시 시도하십시오.")
			return
		}
		writeJSON(w, http.StatusOK, certificateListResponse{
			RequestID: deref(requestIDFrom(r.Context())),
			Items:     res.Items,
			Total:     res.Total,
			Page:      res.Page,
			PageSize:  res.PageSize,
		})
	}
}

// handleGetCertificate는 GET /api/v1/certificates/{id}를 처리한다 (설치 위치 목록 포함).
func handleGetCertificate(st *store.Store) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		res, err := st.GetCertificateDetail(r.Context(), r.PathValue("id"))
		if errors.Is(err, store.ErrNotFound) {
			writeError(w, r, http.StatusNotFound, "not_found", "해당 인증서를 찾을 수 없습니다.")
			return
		}
		if err != nil {
			slog.Error("certificate detail failed", "error", err.Error(), "request_id", deref(requestIDFrom(r.Context())))
			writeError(w, r, http.StatusInternalServerError, "internal_error",
				"인증서 상세 조회에 실패했습니다. 잠시 후 다시 시도하십시오.")
			return
		}
		writeJSON(w, http.StatusOK, certificateDetailResponse{
			RequestID:   deref(requestIDFrom(r.Context())),
			Certificate: res.Certificate,
			Locations:   res.Locations,
			Chain:       res.Chain,
		})
	}
}

// deref는 *string을 빈 문자열 폴백으로 편다.
func deref(p *string) string {
	if p == nil {
		return ""
	}
	return *p
}
