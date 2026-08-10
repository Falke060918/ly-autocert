package api

import (
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

// TestSessionCookieSecureFlag는 세션 쿠키의 Secure 플래그가 배선된 설정을 따르는지
// 검증한다. HTTPS 리버스 프록시 뒤(TLS 종단)에서는 켜져야 하고, 평문 HTTP 기본
// 설치에서는 꺼져 있어야 한다 — 켜진 채로 http 접속하면 브라우저가 쿠키를 버려
// 로그인이 조용히 실패한다(docs/manual/TLS.md 7장).
func TestSessionCookieSecureFlag(t *testing.T) {
	tests := []struct {
		name   string
		secure bool
	}{
		{name: "plain http deployment", secure: false},
		{name: "behind tls terminating proxy", secure: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			s := &Server{secureCookies: tt.secure}

			rec := httptest.NewRecorder()
			s.setSessionCookie(rec, "token-value", time.Now().Add(time.Hour))
			got := readCookie(t, rec.Result(), sessionCookieName)
			if got.Secure != tt.secure {
				t.Errorf("login cookie Secure = %v, want %v", got.Secure, tt.secure)
			}
			if !got.HttpOnly || got.SameSite != http.SameSiteLaxMode || got.Path != "/" {
				t.Errorf("login cookie lost its base attributes: %+v", got)
			}

			rec = httptest.NewRecorder()
			s.clearSessionCookie(rec)
			// 삭제 쿠키도 같은 속성이어야 브라우저가 같은 쿠키로 인식한다.
			got = readCookie(t, rec.Result(), sessionCookieName)
			if got.Secure != tt.secure {
				t.Errorf("logout cookie Secure = %v, want %v", got.Secure, tt.secure)
			}
			if got.MaxAge >= 0 {
				t.Errorf("logout cookie MaxAge = %d, want negative", got.MaxAge)
			}
		})
	}
}

func readCookie(t *testing.T, res *http.Response, name string) *http.Cookie {
	t.Helper()
	for _, c := range res.Cookies() {
		if c.Name == name {
			return c
		}
	}
	t.Fatalf("cookie %q not set", name)
	return nil
}
