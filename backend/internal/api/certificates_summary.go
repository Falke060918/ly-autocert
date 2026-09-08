package api

// 대시보드 요약 핸들러 (이슈 #8).
// 읽기 전용 API라 감사 이벤트를 남기지 않는다 (00-spec 7.12 기록 대상에 조회 없음).

import (
	"log/slog"
	"net/http"

	"github.com/Falke060918/INHERIT_AutoCert/backend/internal/store"
)

type certificateSummaryResponse struct {
	RequestID string `json:"request_id"`
	store.Summary
}

// handleCertificateSummary는 GET /api/v1/certificates/summary를 처리한다.
// 만료 구간(7/30/60/90일)은 누적값이며, 자체 서명·소유자 없음 카운트를 함께 준다.
func handleCertificateSummary(st *store.Store) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		scope, perr := parseCertificateScope(r)
		if perr != nil {
			writeError(w, r, http.StatusBadRequest, perr.code, perr.message)
			return
		}
		sum, err := st.CertificateSummary(r.Context(), scope)
		if err != nil {
			slog.Error("certificate summary failed", "error", err.Error(), "request_id", deref(requestIDFrom(r.Context())))
			writeError(w, r, http.StatusInternalServerError, "internal_error",
				"인증서 요약 조회에 실패했습니다. 잠시 후 다시 시도하십시오.")
			return
		}
		writeJSON(w, http.StatusOK, certificateSummaryResponse{
			RequestID: deref(requestIDFrom(r.Context())),
			Summary:   *sum,
		})
	}
}
