// Package api는 HTTP 라우팅과 핸들러를 담당한다.
// 라우터는 표준 net/http의 패턴 매칭(Go 1.22+)만 사용한다.
package api

import (
	"context"
	"database/sql"
	"encoding/json"
	"log/slog"
	"net/http"
	"time"

	"github.com/Falke060918/INHERIT_AutoCert/backend/internal/store"
)

// NewServer는 모든 라우트가 등록된 HTTP 핸들러를 반환한다.
// /api/v1 아래 보호 라우트는 requirePermission(RBAC 확장점, 이슈 #6)을 거친다.
func NewServer(pool *sql.DB) http.Handler {
	st := store.New(pool)
	mux := http.NewServeMux()
	mux.HandleFunc("GET /healthz", handleHealthz(pool))
	mux.HandleFunc("POST /api/v1/certificates/import",
		requirePermission("certificates:import", handleImportCertificates(st)))
	return withRequestID(mux)
}

type healthzResponse struct {
	Status   string `json:"status"`
	Database string `json:"database"`
}

// handleHealthz는 서버 생존 여부와 DB 연결 상태를 반환한다.
// DB ping이 실패하면 503을 반환한다.
func handleHealthz(pool *sql.DB) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		ctx, cancel := context.WithTimeout(r.Context(), 2*time.Second)
		defer cancel()

		if err := pool.PingContext(ctx); err != nil {
			slog.Error("healthz: database ping failed", "error", err.Error())
			writeJSON(w, http.StatusServiceUnavailable, healthzResponse{
				Status:   "unavailable",
				Database: "down",
			})
			return
		}
		writeJSON(w, http.StatusOK, healthzResponse{
			Status:   "ok",
			Database: "up",
		})
	}
}

type errorBody struct {
	Code    string `json:"code"`
	Message string `json:"message"`
}

type errorResponse struct {
	Error     errorBody `json:"error"`
	RequestID string    `json:"request_id"`
}

// writeError는 통일된 오류 응답 형식으로 쓴다 (00-spec 13장).
func writeError(w http.ResponseWriter, r *http.Request, status int, code, message string) {
	writeJSON(w, status, errorResponse{
		Error:     errorBody{Code: code, Message: message},
		RequestID: requestIDFrom(r.Context()),
	})
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	if err := json.NewEncoder(w).Encode(v); err != nil {
		slog.Error("write json response failed", "error", err.Error())
	}
}
