package api

import (
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"time"

	"github.com/Falke060918/INHERIT_AutoCert/backend/internal/audit"
	"github.com/Falke060918/INHERIT_AutoCert/backend/internal/auth"
	"github.com/Falke060918/INHERIT_AutoCert/backend/internal/domain"
)

type loginRequest struct {
	Username string `json:"username"`
	Password string `json:"password"`
}

type loginResponse struct {
	Token     string       `json:"token"`
	ExpiresAt time.Time    `json:"expires_at"`
	User      *domain.User `json:"user"`
}

// handleLogin은 POST /api/v1/auth/login이다. 성공·실패 모두 감사 이벤트를 남긴다
// (00-spec 7.12: 로그인, 로그인 실패). 비밀번호는 어떤 경로로도 로그·감사에 남기지 않는다.
func (s *Server) handleLogin(w http.ResponseWriter, r *http.Request) {
	var req loginRequest
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<20)).Decode(&req); err != nil {
		writeError(w, r, http.StatusBadRequest, "invalid_body", "invalid JSON body")
		return
	}
	// 성공 시 세션 생성+감사 기록은 서비스가 단일 트랜잭션으로 처리한다 (이슈 #7).
	user, token, expiresAt, err := s.auth.Login(r.Context(), req.Username, req.Password,
		auth.AuditMeta{RequestID: requestIDFrom(r.Context()), SourceIP: clientIP(r)})
	if errors.Is(err, auth.ErrInvalidCredentials) {
		if aerr := s.audit.Record(r.Context(), audit.Event{
			Type:       "user.login",
			TargetType: "user",
			RequestID:  requestIDFrom(r.Context()),
			After:      map[string]string{"username": req.Username},
			Result:     domain.AuditFailure,
			SourceIP:   clientIP(r),
		}); aerr != nil {
			slog.Error("audit login failure event failed", "error", aerr.Error())
		}
		writeError(w, r, http.StatusUnauthorized, "invalid_credentials", "invalid username or password")
		return
	}
	if err != nil {
		slog.Error("login failed", "error", err.Error())
		writeError(w, r, http.StatusInternalServerError, "internal", "internal error")
		return
	}
	// 브라우저 클라이언트용 쿠키. 폐쇄망 개발환경은 http이므로 Secure는 두지 않는다
	// (TLS 종단 도입 시 Secure 추가).
	http.SetCookie(w, &http.Cookie{
		Name:     sessionCookieName,
		Value:    token,
		Path:     "/",
		Expires:  expiresAt,
		HttpOnly: true,
		SameSite: http.SameSiteLaxMode,
	})
	writeJSON(w, http.StatusOK, loginResponse{Token: token, ExpiresAt: expiresAt, User: user})
}

// handleLogout은 POST /api/v1/auth/logout이다. 세션 폐기+감사 기록은 서비스가
// 단일 트랜잭션으로 처리한다 — 감사 실패 시 세션도 유지된다 (이슈 #7).
func (s *Server) handleLogout(w http.ResponseWriter, r *http.Request) {
	user := userFrom(r.Context())
	if err := s.auth.Logout(r.Context(), sessionTokenFrom(r.Context()), user.ID,
		auth.AuditMeta{RequestID: requestIDFrom(r.Context()), SourceIP: clientIP(r)}); err != nil {
		slog.Error("logout failed", "error", err.Error())
		writeError(w, r, http.StatusInternalServerError, "internal", "internal error")
		return
	}
	// 쿠키 제거
	http.SetCookie(w, &http.Cookie{
		Name:     sessionCookieName,
		Value:    "",
		Path:     "/",
		MaxAge:   -1,
		HttpOnly: true,
		SameSite: http.SameSiteLaxMode,
	})
	w.WriteHeader(http.StatusNoContent)
}

// handleMe는 GET /api/v1/auth/me다. 현재 인증된 사용자를 반환한다.
func (s *Server) handleMe(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, userFrom(r.Context()))
}
