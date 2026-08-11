package api

// 영향 범위 집계 핸들러 (GET /api/v1/certificates/{id}/impact).
// "이 CA가 만료되면 무엇이 죽는가"를 한 번에 답한다 — 전이적 자손 CA·리프,
// 그 인증서들이 지금 붙어 있는 서버·엔드포인트, SAN까지 편 도메인, 만료 분포.
//
// 읽기 전용이라 감사 이벤트를 남기지 않는다 (00-spec 7.12 기록 대상에 조회 없음;
// summary·issuers·risks와 같은 규약). 권한은 라우트에서 검사한다(하드 규칙 6).
// 응답에 인증서 원문(DER/PEM)·개인키는 없으며 로그에도 남기지 않는다.

import (
	"errors"
	"log/slog"
	"net/http"

	"github.com/Falke060918/INHERIT_AutoCert/backend/internal/store"
)

type certificateImpactResponse struct {
	RequestID string `json:"request_id"`
	store.CertificateImpact
}

// handleCertificateImpact는 GET /api/v1/certificates/{id}/impact를 처리한다.
// server_count는 GET /api/v1/servers?issued_by={id}의 total과 같다(같은 술어).
func handleCertificateImpact(st *store.Store) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		res, err := st.CertificateImpact(r.Context(), r.PathValue("id"))
		if errors.Is(err, store.ErrNotFound) {
			writeError(w, r, http.StatusNotFound, "not_found", "해당 인증서를 찾을 수 없습니다.")
			return
		}
		if err != nil {
			slog.Error("certificate impact failed", "error", err.Error(),
				"request_id", deref(requestIDFrom(r.Context())))
			writeError(w, r, http.StatusInternalServerError, "internal_error",
				"영향 범위 집계 조회에 실패했습니다. 잠시 후 다시 시도하십시오.")
			return
		}
		writeJSON(w, http.StatusOK, certificateImpactResponse{
			RequestID:         deref(requestIDFrom(r.Context())),
			CertificateImpact: *res,
		})
	}
}
