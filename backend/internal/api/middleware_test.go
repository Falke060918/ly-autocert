package api

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

// TestSecurityHeaders는 모든 응답에 보안 헤더가 붙는지 검증한다.
// API 서버는 브라우저 렌더링 대상이 아니므로 sniffing·프레이밍·리퍼러 유출을 전부 차단하고,
// 인증서 메타데이터·세션 등 민감 응답이 캐시에 남지 않도록 /api/* 는 no-store를 강제한다.
func TestSecurityHeaders(t *testing.T) {
	h := withSecurityHeaders(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))

	tests := []struct {
		name      string
		path      string
		wantCache string // ""이면 Cache-Control 미설정 기대
	}{
		{name: "api path gets no-store", path: "/api/v1/certificates", wantCache: "no-store"},
		{name: "healthz has no cache directive", path: "/healthz", wantCache: ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			rec := httptest.NewRecorder()
			h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, tt.path, nil))

			want := map[string]string{
				"X-Content-Type-Options":  "nosniff",
				"X-Frame-Options":         "DENY",
				"Referrer-Policy":         "no-referrer",
				"Content-Security-Policy": "default-src 'none'; frame-ancestors 'none'",
				"Cache-Control":           tt.wantCache,
			}
			for key, val := range want {
				if got := rec.Header().Get(key); got != val {
					t.Errorf("%s: got %q, want %q", key, got, val)
				}
			}
		})
	}
}
