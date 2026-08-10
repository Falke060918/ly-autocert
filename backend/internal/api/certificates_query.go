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
	RequestID string `json:"request_id"`
	// 목록 원소는 인증서 + last_seen_at 파생 열이다(상세·CSV에는 없다).
	Items    []store.CertificateListItem `json:"items"`
	Total    int                         `json:"total"`
	Page     int                         `json:"page"`
	PageSize int                         `json:"page_size"`
}

type certificateDetailResponse struct {
	RequestID   string             `json:"request_id"`
	Certificate domain.Certificate `json:"certificate"`
	// Owner는 certificate.owner_id가 가리키는 계정의 표시용 요약이다.
	// 미지정이거나 계정이 사라졌으면 null — 화면이 UUID를 그대로 띄우지 않게 한다.
	Owner     *store.OwnerRef            `json:"owner"`
	Locations []store.LocationWithTarget `json:"locations"`
	Chain     []store.ChainItem          `json:"chain"` // 자신→상위→루트 순, 미보유 상위는 stored=false
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

// parseCertStatuses는 status 계열 파라미터 값을 검증해 상태 목록으로 편다.
// 반복 지정·쉼표 구분을 모두 받는다(splitMulti). param은 오류 코드·문구에 쓴다.
func parseCertStatuses(vals []string, param string) ([]domain.CertificateStatus, *paramError) {
	var out []domain.CertificateStatus
	for _, v := range splitMulti(vals) {
		s := domain.CertificateStatus(v)
		switch s {
		case domain.CertStatusActive, domain.CertStatusExpired, domain.CertStatusRevoked,
			domain.CertStatusReplaced, domain.CertStatusUnknown:
			out = append(out, s)
		default:
			return nil, &paramError{"invalid_" + param,
				param + "는 active/expired/revoked/replaced/unknown 중 하나여야 합니다."}
		}
	}
	return out, nil
}

// parseListParams는 목록 쿼리 파라미터를 검증해 store 입력으로 변환한다.
// 파라미터: q, status, exclude_status, type, expired, expires_within_days,
// self_signed, unowned, sort, order, page, page_size.
//
// exclude_status를 주지 않으면 제외는 일어나지 않는다 — 이 함수는 CSV 내보내기와
// 공유되므로 서버 기본값을 바꾸면 아무도 요청하지 않은 내보내기 내용 변경이
// 따라온다(설계 06 14.2 ⑥). 교체된 인증서 숨기기는 프론트가 명시적으로 요청한다.
func parseListParams(r *http.Request) (store.ListCertificatesInput, *paramError) {
	q := r.URL.Query()
	in := store.ListCertificatesInput{Page: 1, PageSize: 50}

	in.Search = strings.TrimSpace(q.Get("q"))
	if utf8.RuneCountInString(in.Search) > maxSearchLen {
		return in, &paramError{"invalid_search", "검색어는 200자 이하여야 합니다."}
	}
	statuses, perr := parseCertStatuses(q["status"], "status")
	if perr != nil {
		return in, perr
	}
	in.Statuses = statuses
	excluded, perr := parseCertStatuses(q["exclude_status"], "exclude_status")
	if perr != nil {
		return in, perr
	}
	in.ExcludeStatuses = excluded
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
	if q.Has("issuer_org") {
		// 존재 여부로 판정한다 — 존재하며 빈 값이면 "조직 미상"
		// (issuer_organization IS NULL) 필터이고, 부재만 필터 없음이다.
		v := q.Get("issuer_org")
		if utf8.RuneCountInString(v) > maxSearchLen {
			return in, &paramError{"invalid_issuer_org", "issuer_org는 200자 이하여야 합니다."}
		}
		in.IssuerOrg = &v
	}
	if v := q.Get("issued_by"); v != "" {
		// 잘못된 형식은 DB ::uuid 캐스트 500으로 새지 않게 여기서 거른다.
		if !isUUID(v) {
			return in, &paramError{"invalid_issued_by", "issued_by는 인증서 id(UUID)여야 합니다."}
		}
		in.IssuedBy = &v
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
			Owner:       res.Owner,
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
