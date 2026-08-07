// Package api는 HTTP 라우팅과 핸들러를 담당한다.
// 라우터는 표준 net/http의 패턴 매칭(Go 1.22+)만 사용한다.
//
// 라우트 등록 규칙: public()과 protected() 둘뿐이다. protected는 허용 역할을
// 명시해야 하며(빈 목록은 panic), 새 API가 인증·권한 검사를 빠뜨릴 수 없게 한다
// (CLAUDE.md 보안 하드 규칙 6).
package api

import (
	"context"
	"database/sql"
	"encoding/json"
	"log/slog"
	"net/http"
	"time"

	"github.com/Falke060918/INHERIT_AutoCert/backend/internal/audit"
	"github.com/Falke060918/INHERIT_AutoCert/backend/internal/auth"
	"github.com/Falke060918/INHERIT_AutoCert/backend/internal/discovery"
	"github.com/Falke060918/INHERIT_AutoCert/backend/internal/domain"
	"github.com/Falke060918/INHERIT_AutoCert/backend/internal/store"
)

// Server는 라우터와 의존성(인증 서비스·감사 기록기)을 묶는다.
type Server struct {
	mux   *http.ServeMux
	auth  *auth.Service
	audit *audit.Recorder
}

// NewServer는 모든 라우트가 등록된 HTTP 핸들러를 반환한다.
func NewServer(pool *sql.DB, authSvc *auth.Service, rec *audit.Recorder) http.Handler {
	st := store.New(pool)
	disc := discovery.NewManager(st)
	s := &Server{mux: http.NewServeMux(), auth: authSvc, audit: rec}

	// 비보호 라우트: 헬스체크와 로그인뿐이다.
	s.public("GET /healthz", handleHealthz(pool))
	s.public("POST /api/v1/auth/login", s.handleLogin)

	// 보호 라우트: 인증 + 역할 검사.
	s.protected("POST /api/v1/auth/logout", s.handleLogout, domain.AllRoles...)
	s.protected("GET /api/v1/auth/me", s.handleMe, domain.AllRoles...)
	s.protected("POST /api/v1/users", s.handleCreateUser, domain.RoleSystemAdministrator)
	s.protected("GET /api/v1/users", s.handleListUsers, domain.RoleSystemAdministrator)
	// 업로드는 인벤토리 변경이므로 관리자·운영자급만 (00-spec 7.11).
	s.protected("POST /api/v1/certificates/import", handleImportCertificates(st),
		domain.RoleSystemAdministrator, domain.RolePKIAdministrator, domain.RoleOperator)
	// 스캔 생성은 인벤토리를 바꾸므로 관리자·운영자급만. 상태 조회는 읽기 전용이라 전 역할.
	s.protected("POST /api/v1/discovery/scans", handleCreateScan(disc),
		domain.RoleSystemAdministrator, domain.RolePKIAdministrator, domain.RoleOperator)
	s.protected("GET /api/v1/discovery/scans/{id}", handleGetScan(st), domain.AllRoles...)

	return withRequestID(s.mux)
}

// public은 인증 없이 접근 가능한 라우트를 등록한다. 추가는 신중히.
func (s *Server) public(pattern string, h http.HandlerFunc) {
	s.mux.HandleFunc(pattern, h)
}

// protected는 인증 + 역할 검사를 거치는 라우트를 등록한다. roles가 비면 panic.
func (s *Server) protected(pattern string, h http.HandlerFunc, roles ...domain.Role) {
	s.mux.HandleFunc(pattern, s.requireRoles(h, roles...))
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

// errorResponse는 통일된 오류 응답 형식이다 (00-spec 13장).
type errorResponse struct {
	Error     errorBody `json:"error"`
	RequestID string    `json:"request_id,omitempty"`
}

type errorBody struct {
	Code    string `json:"code"`
	Message string `json:"message"`
}

func writeError(w http.ResponseWriter, r *http.Request, status int, code, message string) {
	resp := errorResponse{Error: errorBody{Code: code, Message: message}}
	if id := requestIDFrom(r.Context()); id != nil {
		resp.RequestID = *id
	}
	writeJSON(w, status, resp)
}
