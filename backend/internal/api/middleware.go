package api

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"log/slog"
	"net"
	"net/http"
	"strings"

	"github.com/Falke060918/INHERIT_AutoCert/backend/internal/audit"
	"github.com/Falke060918/INHERIT_AutoCert/backend/internal/auth"
	"github.com/Falke060918/INHERIT_AutoCert/backend/internal/domain"
)

type ctxKey int

const (
	ctxKeyRequestID ctxKey = iota
	ctxKeyUser
	ctxKeySessionToken
	ctxKeyClientIP
)

// sessionCookieName은 브라우저 클라이언트용 세션 쿠키다.
// API 클라이언트는 Authorization: Bearer 헤더를 쓴다.
const sessionCookieName = "autocert_session"

// withRequestID는 모든 응답에 Request ID를 부여한다 (00-spec 13장).
// 클라이언트가 X-Request-ID를 보내면 그대로 쓰고, 없으면 생성한다.
func withRequestID(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		id := r.Header.Get("X-Request-ID")
		if id == "" || len(id) > 128 {
			b := make([]byte, 16)
			if _, err := rand.Read(b); err != nil {
				slog.Error("generate request id failed", "error", err.Error())
			}
			id = hex.EncodeToString(b)
		}
		w.Header().Set("X-Request-ID", id)
		next.ServeHTTP(w, r.WithContext(context.WithValue(r.Context(), ctxKeyRequestID, id)))
	})
}

// withSecurityHeaders는 모든 응답에 API 서버용 보안 헤더를 부여한다.
// 이 서버는 JSON API 전용이라 브라우저 렌더링 대상이 아니다 — sniffing·프레이밍·
// 리퍼러 유출을 전부 차단하고, CSP는 응답이 문서로 해석되는 사고에 대비한 방어선이다
// (페이지용 CSP는 frontend/next.config.ts가 별도로 건다).
// /api/* 응답은 인증서 메타데이터·세션 등 민감 정보이므로 no-store로 캐시를 금지한다.
func withSecurityHeaders(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		h := w.Header()
		h.Set("X-Content-Type-Options", "nosniff")
		h.Set("X-Frame-Options", "DENY")
		h.Set("Referrer-Policy", "no-referrer")
		h.Set("Content-Security-Policy", "default-src 'none'; frame-ancestors 'none'")
		if strings.HasPrefix(r.URL.Path, "/api/") {
			h.Set("Cache-Control", "no-store")
		}
		next.ServeHTTP(w, r)
	})
}

// requestIDFrom은 컨텍스트의 Request ID를 반환한다. 없으면 nil.
func requestIDFrom(ctx context.Context) *string {
	if id, ok := ctx.Value(ctxKeyRequestID).(string); ok && id != "" {
		return &id
	}
	return nil
}

// userFrom은 인증 미들웨어가 저장한 현재 사용자를 반환한다.
func userFrom(ctx context.Context) *domain.User {
	u, _ := ctx.Value(ctxKeyUser).(*domain.User)
	return u
}

// sessionTokenFrom은 현재 요청의 세션 토큰 원문을 반환한다 (logout용).
func sessionTokenFrom(ctx context.Context) string {
	t, _ := ctx.Value(ctxKeySessionToken).(string)
	return t
}

// extractToken은 Authorization: Bearer 헤더 또는 세션 쿠키에서 토큰을 꺼낸다.
func extractToken(r *http.Request) string {
	if h := r.Header.Get("Authorization"); h != "" {
		if token, ok := strings.CutPrefix(h, "Bearer "); ok {
			return token
		}
		return ""
	}
	if c, err := r.Cookie(sessionCookieName); err == nil {
		return c.Value
	}
	return ""
}

// clientIP는 요청 출처 IP다 (감사 로그 source_ip·접근 IP 제한 공용).
//
// 판정은 withAdminIPAllow가 하고 결과를 컨텍스트에 넣는다 — 감사에 남는 IP와
// 접근 제한이 판정한 IP가 서로 다르면 "차단 로그를 감사에서 못 찾는" 상황이 된다.
// X-Forwarded-For는 그 미들웨어가 신뢰 프록시 대역에서 온 요청에만 반영한다
// (예전에는 무조건 첫 값을 믿었다 — 헤더 한 줄로 출처를 위조할 수 있었다).
func clientIP(r *http.Request) *string {
	if ip, ok := r.Context().Value(ctxKeyClientIP).(string); ok && ip != "" {
		return &ip
	}
	// 미들웨어를 거치지 않은 경로(단위 테스트 등)는 직접 접속 IP만 쓴다.
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil || net.ParseIP(host) == nil {
		return nil
	}
	return &host
}

// requireRoles는 인증 + 역할 검사 미들웨어다. 모든 보호 라우트가 이것을 거친다 —
// 인증만으로 통과시키지 않는다 (보안 하드 규칙 6).
// 허용 역할이 아니면 403을 반환하고 authz.denied 감사 이벤트를 남긴다.
func (s *Server) requireRoles(next http.HandlerFunc, roles ...domain.Role) http.HandlerFunc {
	if len(roles) == 0 {
		panic("api: protected route registered without roles")
	}
	allowed := make(map[domain.Role]bool, len(roles))
	for _, role := range roles {
		allowed[role] = true
	}
	return func(w http.ResponseWriter, r *http.Request) {
		token := extractToken(r)
		user, err := s.auth.Authenticate(r.Context(), token)
		if err != nil {
			if err == auth.ErrUnauthenticated {
				writeError(w, r, http.StatusUnauthorized, "unauthenticated", "authentication required")
				return
			}
			slog.Error("authenticate failed", "error", err.Error(), "path", r.URL.Path)
			writeError(w, r, http.StatusInternalServerError, "internal", "internal error")
			return
		}
		if !allowed[user.Role] {
			// 권한 거부도 감사 대상이다. 기록 실패는 응답을 막지 않는다(거부는 변경이 아님).
			if err := s.audit.Record(r.Context(), audit.Event{
				Type:       "authz.denied",
				ActorID:    &user.ID,
				TargetType: "route",
				TargetID:   ptr(r.Method + " " + r.URL.Path),
				RequestID:  requestIDFrom(r.Context()),
				Result:     domain.AuditDenied,
				SourceIP:   clientIP(r),
			}); err != nil {
				slog.Error("audit authz.denied failed", "error", err.Error())
			}
			writeError(w, r, http.StatusForbidden, "forbidden", "insufficient role")
			return
		}
		ctx := context.WithValue(r.Context(), ctxKeyUser, user)
		ctx = context.WithValue(ctx, ctxKeySessionToken, token)
		next(w, r.WithContext(ctx))
	}
}

func ptr[T any](v T) *T { return &v }
