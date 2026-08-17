// middleware.go — 센서 게이트웨이 공통 미들웨어·응답 헬퍼.
// internal/api와 같은 응답 형식(00-spec 13장: error.code/message + request_id)을 쓰되,
// 인증 주체가 사용자 세션이 아니라 mTLS 클라이언트 인증서라는 점만 다르다.
package sensorgw

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"log/slog"
	"net"
	"net/http"
	"time"

	"github.com/Falke060918/INHERIT_AutoCert/backend/internal/domain"
	"github.com/Falke060918/INHERIT_AutoCert/backend/internal/store"
)

type ctxKey int

const (
	ctxKeyRequestID ctxKey = iota
	ctxKeySensor
	ctxKeyClientFingerprint
)

// withRequestID는 모든 응답에 Request ID를 부여한다 (00-spec 13장, internal/api와 동일 규약).
func withRequestID(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		id := r.Header.Get("X-Request-ID")
		if id == "" || len(id) > 128 {
			b := make([]byte, 16)
			if _, err := rand.Read(b); err != nil {
				slog.Error("sensorgw: generate request id failed", "error", err.Error())
			}
			id = hex.EncodeToString(b)
		}
		w.Header().Set("X-Request-ID", id)
		next.ServeHTTP(w, r.WithContext(context.WithValue(r.Context(), ctxKeyRequestID, id)))
	})
}

// statusRecorder는 요청 로그용으로 응답 상태 코드를 붙잡는다.
type statusRecorder struct {
	http.ResponseWriter
	status int
}

func (sr *statusRecorder) WriteHeader(code int) {
	sr.status = code
	sr.ResponseWriter.WriteHeader(code)
}

// withRequestLog는 요청 1건당 구조화 로그 1줄을 남긴다 (slog — main이 JSON 핸들러 구성).
// 본문·토큰·인증서 내용은 로그에 넣지 않는다 (보안 하드 규칙 3).
func withRequestLog(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		sr := &statusRecorder{ResponseWriter: w, status: http.StatusOK}
		next.ServeHTTP(sr, r)
		attrs := []any{
			"method", r.Method,
			"path", r.URL.Path,
			"status", sr.status,
			"duration_ms", time.Since(start).Milliseconds(),
		}
		if id := requestIDFrom(r.Context()); id != nil {
			attrs = append(attrs, "request_id", *id)
		}
		if sn := sensorFrom(r.Context()); sn != nil {
			attrs = append(attrs, "sensor_id", sn.ID)
		}
		slog.Info("sensorgw: request", attrs...)
	})
}

// limitEnroll은 비인증 enroll 경로의 토큰 버킷 게이트다. 상한을 넘긴 요청은
// 429로 즉시 거절한다 — Wait으로 붙잡으면 연결이 쌓여 오히려 소진 공격 표면이 된다.
// 거절은 요청 로그(withRequestLog, 상태 429)로만 남고 감사 이벤트는 만들지 않는다:
// 초과 요청 수는 상한이 없어 건당 감사가 감사 로그 폭주(해시 체인 advisory lock
// 경합)로 이어진다. 토큰 값은 어디에도 싣지 않는다 (보안 하드 규칙 3).
func (g *Gateway) limitEnroll(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if !g.enrollLim.Allow() {
			w.Header().Set("Retry-After", "1")
			writeError(w, r, http.StatusTooManyRequests, "rate_limited",
				"등록 요청이 너무 잦습니다. 잠시 후 다시 시도하십시오.")
			return
		}
		next(w, r)
	}
}

// requireSensor는 mTLS 신원 확인 미들웨어다. 핸드셰이크에서 센서 CA로 검증된
// 클라이언트 인증서의 SHA-256 fingerprint를 요청마다 store와 대조한다
// (status='active'만 통과 — 비활성화 즉시 차단, 보안 하드 규칙 6).
// last_seen_at은 best-effort로 갱신한다 (실패해도 요청은 계속).
func (g *Gateway) requireSensor(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.TLS == nil || len(r.TLS.PeerCertificates) == 0 || len(r.TLS.VerifiedChains) == 0 {
			writeError(w, r, http.StatusUnauthorized, "client_certificate_required",
				"센서 클라이언트 인증서가 필요합니다.")
			return
		}
		fp := certFingerprint(r.TLS.PeerCertificates[0].Raw)
		sn, err := g.st.GetActiveSensorByFingerprint(r.Context(), fp)
		if errors.Is(err, store.ErrSensorNotFound) {
			// 미등록·교체됨·비활성화를 구분하지 않는다 — 구분은 탐색 단서가 된다.
			writeError(w, r, http.StatusUnauthorized, "unknown_client_certificate",
				"등록된 센서 인증서가 아닙니다.")
			return
		}
		if err != nil {
			slog.Error("sensorgw: sensor identity lookup failed", "error", err.Error())
			writeError(w, r, http.StatusInternalServerError, "internal_error", "내부 오류가 발생했습니다.")
			return
		}
		if err := g.st.TouchSensorLastSeen(r.Context(), sn.ID); err != nil {
			slog.Warn("sensorgw: touch sensor last seen failed", "error", err.Error(), "sensor_id", sn.ID)
		}
		ctx := context.WithValue(r.Context(), ctxKeySensor, sn)
		ctx = context.WithValue(ctx, ctxKeyClientFingerprint, fp)
		next(w, r.WithContext(ctx))
	}
}

// certFingerprint는 인증서 DER의 SHA-256 지문(소문자 hex)을 만든다.
func certFingerprint(der []byte) string {
	sum := sha256.Sum256(der)
	return hex.EncodeToString(sum[:])
}

// requestIDFrom은 컨텍스트의 Request ID를 반환한다. 없으면 nil.
func requestIDFrom(ctx context.Context) *string {
	if id, ok := ctx.Value(ctxKeyRequestID).(string); ok && id != "" {
		return &id
	}
	return nil
}

// sensorFrom은 requireSensor가 저장한 현재 센서를 반환한다.
func sensorFrom(ctx context.Context) *domain.Sensor {
	sn, _ := ctx.Value(ctxKeySensor).(*domain.Sensor)
	return sn
}

// clientFingerprintFrom은 현재 요청의 클라이언트 인증서 fingerprint를 반환한다 (renew용).
func clientFingerprintFrom(ctx context.Context) string {
	fp, _ := ctx.Value(ctxKeyClientFingerprint).(string)
	return fp
}

// clientIP는 감사 로그용 요청 출처 IP다. 센서는 서버에 직접 붙으므로 XFF는 보지 않는다.
func clientIP(r *http.Request) *string {
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil || net.ParseIP(host) == nil {
		return nil
	}
	return &host
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	if err := json.NewEncoder(w).Encode(v); err != nil {
		slog.Error("sensorgw: write json response failed", "error", err.Error())
	}
}

// errorResponse는 통일된 오류 응답 형식이다 (00-spec 13장 — internal/api와 동일).
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

// decodeJSON은 크기 제한을 걸고 요청 본문을 디코드한다. 실패 시 400을 쓰고 false를 반환한다.
// 디코드 오류 메시지에 본문 내용은 싣지 않는다 (보안 하드 규칙 3).
func decodeJSON(w http.ResponseWriter, r *http.Request, maxBytes int64, v any) bool {
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, maxBytes)).Decode(v); err != nil {
		writeError(w, r, http.StatusBadRequest, "invalid_request", "요청 본문을 해석할 수 없습니다.")
		return false
	}
	return true
}

// isUUID는 UUID(하이픈 포함 36자, hex) 형식을 가볍게 검증한다.
// 잘못된 경로 값이 DB의 ::uuid 캐스트 오류(500)로 새지 않게 막는다 (internal/api와 동일).
func isUUID(s string) bool {
	if len(s) != 36 {
		return false
	}
	for i, c := range s {
		if i == 8 || i == 13 || i == 18 || i == 23 {
			if c != '-' {
				return false
			}
			continue
		}
		isHex := (c >= '0' && c <= '9') || (c >= 'a' && c <= 'f') || (c >= 'A' && c <= 'F')
		if !isHex {
			return false
		}
	}
	return true
}
