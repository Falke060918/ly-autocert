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
)

// NewServer는 모든 라우트가 등록된 HTTP 핸들러를 반환한다.
func NewServer(pool *sql.DB) http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /healthz", handleHealthz(pool))
	return mux
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

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	if err := json.NewEncoder(w).Encode(v); err != nil {
		slog.Error("write json response failed", "error", err.Error())
	}
}
