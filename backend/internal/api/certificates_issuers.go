package api

// 발급 기관 분포·CA 트리 집계 핸들러 (GET /api/v1/certificates/issuers).
// 읽기 전용 API라 감사 이벤트를 남기지 않는다 (00-spec 7.12 기록 대상에 조회 없음).

import (
	"log/slog"
	"net/http"

	"github.com/Falke060918/INHERIT_AutoCert/backend/internal/store"
)

type certificateIssuersResponse struct {
	RequestID string `json:"request_id"`
	store.CertificateIssuers
}

// handleCertificateIssuers는 GET /api/v1/certificates/issuers를 처리한다.
// organizations는 발급 기관(issuer_organization)별 비-CA 인증서 분포이고,
// ca_tree는 보유 CA의 **N계층 트리**다 — 중간 CA의 중간 CA도 제 부모 아래에
// 그대로 중첩된다(노드별 직접 발급 리프 수·하위 포함 합산·깊이 포함).
// 리프는 노드당 최대 5건만 leaf_preview로 싣는다. 전체 리프는 노드의 id를
// 목록 API의 issued_by= 필터에 넣어 페이지네이션과 함께 조회한다 —
// 두 API가 같은 매칭 술어를 쓰므로 leaf_count와 목록 건수가 일치한다.
func handleCertificateIssuers(st *store.Store) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		res, err := st.CertificateIssuers(r.Context())
		if err != nil {
			slog.Error("certificate issuers failed", "error", err.Error(),
				"request_id", deref(requestIDFrom(r.Context())))
			writeError(w, r, http.StatusInternalServerError, "internal_error",
				"발급 기관 집계 조회에 실패했습니다. 잠시 후 다시 시도하십시오.")
			return
		}
		writeJSON(w, http.StatusOK, certificateIssuersResponse{
			RequestID:          deref(requestIDFrom(r.Context())),
			CertificateIssuers: *res,
		})
	}
}
