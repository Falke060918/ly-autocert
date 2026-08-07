package api

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"net/http"
)

type ctxKey int

const ctxKeyRequestID ctxKey = iota

// withRequestID는 모든 요청에 Request ID를 부여하고 X-Request-ID 응답 헤더로
// 돌려준다 (00-spec 13장: Request ID를 모든 응답에 포함).
// 클라이언트가 보낸 X-Request-ID가 있으면 그대로 전파한다.
func withRequestID(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		id := r.Header.Get("X-Request-ID")
		if id == "" {
			id = newRequestID()
		}
		w.Header().Set("X-Request-ID", id)
		next.ServeHTTP(w, r.WithContext(context.WithValue(r.Context(), ctxKeyRequestID, id)))
	})
}

// newRequestID는 crypto/rand 16바이트를 hex로 만든다 (표준 라이브러리만 사용).
func newRequestID() string {
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "unknown"
	}
	return hex.EncodeToString(b[:])
}

// requestIDFrom은 컨텍스트의 Request ID를 반환한다. 없으면 빈 문자열.
func requestIDFrom(ctx context.Context) string {
	id, _ := ctx.Value(ctxKeyRequestID).(string)
	return id
}

// requirePermission은 역할 기반 권한 검사의 확장점이다 (보안 하드 규칙 6).
// 이슈 #6(로그인+RBAC) 전까지는 통과시키되, 모든 보호 라우트가 이 래퍼를
// 거치게 해 RBAC 도입 시 이 함수 하나만 구현하면 되도록 한다.
func requirePermission(permission string, next http.HandlerFunc) http.HandlerFunc {
	_ = permission // 이슈 #6에서 역할→권한 매핑에 사용
	return next
}
