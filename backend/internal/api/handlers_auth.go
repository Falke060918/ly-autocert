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
	s.setSessionCookie(w, token, expiresAt)
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
	s.clearSessionCookie(w)
	w.WriteHeader(http.StatusNoContent)
}

// setSessionCookie는 브라우저 클라이언트용 세션 쿠키를 내려준다.
// Secure는 SESSION_COOKIE_SECURE 배선(WithSecureCookies)을 따른다 — 백엔드는
// 리버스 프록시 뒤에서 평문 HTTP를 받으므로 요청만 보고 HTTPS 여부를 알 수 없다.
func (s *Server) setSessionCookie(w http.ResponseWriter, token string, expiresAt time.Time) {
	http.SetCookie(w, &http.Cookie{
		Name:     sessionCookieName,
		Value:    token,
		Path:     "/",
		Expires:  expiresAt,
		HttpOnly: true,
		Secure:   s.secureCookies,
		SameSite: http.SameSiteLaxMode,
	})
}

// clearSessionCookie는 세션 쿠키를 즉시 만료시킨다. 속성(Path·Secure·SameSite)은
// 발급 시와 같아야 브라우저가 같은 쿠키로 인식해 지운다.
func (s *Server) clearSessionCookie(w http.ResponseWriter) {
	http.SetCookie(w, &http.Cookie{
		Name:     sessionCookieName,
		Value:    "",
		Path:     "/",
		MaxAge:   -1,
		HttpOnly: true,
		Secure:   s.secureCookies,
		SameSite: http.SameSiteLaxMode,
	})
}

// handleMe는 GET /api/v1/auth/me다. 현재 인증된 사용자를 반환한다.
func (s *Server) handleMe(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, userFrom(r.Context()))
}
