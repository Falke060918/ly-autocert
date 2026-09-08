package api

// 위험 판정 집계 핸들러 (GET /api/v1/certificates/risks).
// 읽기 전용 API라 감사 이벤트를 남기지 않는다 (00-spec 7.12 기록 대상에 조회 없음).
// 판정은 저장하지 않고 요청마다 계산한다 — 기준선이 2027·2030에 움직이므로
// 저장하면 낡는다 (internal/certrisk 패키지 주석).

import (
	"log/slog"
	"net/http"

	"github.com/Falke060918/INHERIT_AutoCert/backend/internal/store"
)

type certificateRisksResponse struct {
	RequestID string `json:"request_id"`
	store.CertificateRiskSummary
}

// handleCertificateRisks는 GET /api/v1/certificates/risks를 처리한다.
// severities는 최고 심각도 기준 분할(합 = total)이고 findings는 판정 항목별 건수다.
// severities의 값을 목록 API의 risk= 필터에, findings의 code를 risk_code= 필터에
// 넣으면 해당 인증서 목록으로 이동한다 — 두 API가 같은 술어를 쓴다.
// 집계는 status=replaced를 빼므로(대시보드 요약과 같은 규약) 이동할 때
// exclude_status=replaced를 함께 보내야 숫자가 맞는다.
func handleCertificateRisks(st *store.Store) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		scope, perr := parseCertificateScope(r)
		if perr != nil {
			writeError(w, r, http.StatusBadRequest, perr.code, perr.message)
			return
		}
		res, err := st.CertificateRisks(r.Context(), scope)
		if err != nil {
			slog.Error("certificate risks failed", "error", err.Error(),
				"request_id", deref(requestIDFrom(r.Context())))
			writeError(w, r, http.StatusInternalServerError, "internal_error",
				"위험 판정 집계 조회에 실패했습니다. 잠시 후 다시 시도하십시오.")
			return
		}
		writeJSON(w, http.StatusOK, certificateRisksResponse{
			RequestID:              deref(requestIDFrom(r.Context())),
			CertificateRiskSummary: *res,
		})
	}
}
