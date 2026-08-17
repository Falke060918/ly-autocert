package sensorgw

import (
	"encoding/json"
	"net/http/httptest"
	"strings"
	"testing"
)

// TestEnrollRateLimitRejectsBurstOverflow는 비인증 enroll 경로의 요청 속도 제한을
// 고정한다: 버스트 안의 요청은 핸들러까지 도달하고(빈 본문이라 400), 버스트를 넘긴
// 요청은 429(rate_limited)로 즉시 거절된다. DB가 필요 없다 — 리미터·본문 검증 모두
// store 앞에서 끝난다(httpserver_test.go와 같은 이유로 store·recorder는 nil).
func TestEnrollRateLimitRejectsBurstOverflow(t *testing.T) {
	gw := newGateway(nil, nil, nil)
	h := gw.Handler()

	for i := 0; i < enrollBurst; i++ {
		rr := httptest.NewRecorder()
		h.ServeHTTP(rr, httptest.NewRequest("POST", "/sensor/v1/enroll", strings.NewReader("{}")))
		if rr.Code != 400 {
			t.Fatalf("request %d within burst: status = %d, want 400 (limiter must not reject)", i, rr.Code)
		}
	}

	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, httptest.NewRequest("POST", "/sensor/v1/enroll", strings.NewReader("{}")))
	if rr.Code != 429 {
		t.Fatalf("request beyond burst: status = %d, want 429", rr.Code)
	}
	if rr.Header().Get("Retry-After") == "" {
		t.Fatal("429 response has no Retry-After header")
	}
	var resp struct {
		Error struct {
			Code string `json:"code"`
		} `json:"error"`
	}
	if err := json.Unmarshal(rr.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode 429 body: %v", err)
	}
	if resp.Error.Code != "rate_limited" {
		t.Fatalf("429 error code = %q, want %q", resp.Error.Code, "rate_limited")
	}

	// 리미터는 enroll 전용이다 — 소진된 뒤에도 다른 경로는 429가 아니라
	// 기존 인증 오류(클라이언트 인증서 부재 401)를 그대로 낸다.
	rr = httptest.NewRecorder()
	h.ServeHTTP(rr, httptest.NewRequest("POST", "/sensor/v1/renew", strings.NewReader("{}")))
	if rr.Code != 401 {
		t.Fatalf("renew after enroll limiter exhausted: status = %d, want 401", rr.Code)
	}
}
