package notify

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"
)

type capturedRequest struct {
	header http.Header
	body   []byte
}

func newWebhookServer(t *testing.T, status int) (*httptest.Server, func() capturedRequest) {
	t.Helper()
	var mu sync.Mutex
	var got capturedRequest
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		mu.Lock()
		got = capturedRequest{header: r.Header.Clone(), body: body}
		mu.Unlock()
		w.WriteHeader(status)
		_, _ = w.Write([]byte(`{"ok":false}`))
	}))
	t.Cleanup(srv.Close)
	return srv, func() capturedRequest {
		mu.Lock()
		defer mu.Unlock()
		return got
	}
}

func TestWebhookSendSuccessAndPayload(t *testing.T) {
	srv, captured := newWebhookServer(t, http.StatusAccepted)
	n := NewWebhook(WebhookConfig{
		URL:        srv.URL,
		Secret:     fakeSecret,
		AuthHeader: "Authorization: Bearer " + fakeToken,
	})
	if ok, reason := n.Enabled(); !ok {
		t.Fatalf("channel must be enabled: %s", reason)
	}
	n.nowOverride = func() time.Time { return time.Date(2026, 8, 9, 1, 2, 3, 0, time.UTC) }

	m := BuildCertExpiryMessage(testFacts())
	if err := n.Send(context.Background(), m); err != nil {
		t.Fatalf("Send: %v", err)
	}

	req := captured()
	if ct := req.header.Get("Content-Type"); !strings.HasPrefix(ct, "application/json") {
		t.Errorf("content type = %q", ct)
	}
	if got := req.header.Get("Authorization"); got != "Bearer "+fakeToken {
		t.Errorf("auth header = %q", got)
	}

	var payload map[string]any
	if err := json.Unmarshal(req.body, &payload); err != nil {
		t.Fatalf("payload is not valid json: %v", err)
	}
	if payload["event_id"] != EventCertExpiring || payload["severity"] != SeverityWarning {
		t.Errorf("payload envelope mismatch: %#v", payload)
	}
	if payload["sent_at"] != "2026-08-09T01:02:03Z" {
		t.Errorf("sent_at = %#v", payload["sent_at"])
	}
	data, ok := payload["data"].(map[string]any)
	if !ok || data["common_name"] != "app.example.test" {
		t.Errorf("payload data mismatch: %#v", payload["data"])
	}
}

func TestWebhookSignsBody(t *testing.T) {
	srv, captured := newWebhookServer(t, http.StatusOK)
	n := NewWebhook(WebhookConfig{URL: srv.URL, Secret: fakeSecret})
	if err := n.Send(context.Background(), Message{Title: "제목", EventID: EventCertExpiring}); err != nil {
		t.Fatalf("Send: %v", err)
	}

	req := captured()
	mac := hmac.New(sha256.New, []byte(fakeSecret))
	mac.Write(req.body)
	want := "sha256=" + hex.EncodeToString(mac.Sum(nil))
	got := req.header.Get(SignatureHeader)
	if got != want {
		t.Errorf("%s = %q, want %q", SignatureHeader, got, want)
	}
	if !strings.HasPrefix(got, "sha256=") || len(got) != len("sha256=")+64 {
		t.Errorf("signature format mismatch: %q", got)
	}
}

func TestWebhookOmitsSignatureWithoutSecret(t *testing.T) {
	srv, captured := newWebhookServer(t, http.StatusOK)
	n := NewWebhook(WebhookConfig{URL: srv.URL})
	if err := n.Send(context.Background(), Message{Title: "제목"}); err != nil {
		t.Fatalf("Send: %v", err)
	}
	if got := captured().header.Get(SignatureHeader); got != "" {
		t.Errorf("signature header must be absent without a secret, got %q", got)
	}
}

func TestWebhookStatusClassification(t *testing.T) {
	cases := []struct {
		status        int
		wantErr       bool
		wantTemporary bool
	}{
		{http.StatusOK, false, false},
		{http.StatusNoContent, false, false},
		{http.StatusBadRequest, true, false},
		{http.StatusUnauthorized, true, false},
		{http.StatusNotFound, true, false},
		{http.StatusRequestTimeout, true, true},
		{http.StatusTooManyRequests, true, true},
		{http.StatusInternalServerError, true, true},
		{http.StatusBadGateway, true, true},
	}
	for _, tc := range cases {
		srv, _ := newWebhookServer(t, tc.status)
		n := NewWebhook(WebhookConfig{URL: srv.URL, Secret: fakeSecret})
		err := n.Send(context.Background(), Message{Title: "제목"})
		switch {
		case !tc.wantErr && err != nil:
			t.Errorf("status %d: unexpected error %v", tc.status, err)
		case tc.wantErr && err == nil:
			t.Errorf("status %d: expected an error", tc.status)
		case tc.wantErr:
			if IsTemporary(err) != tc.wantTemporary {
				t.Errorf("status %d: IsTemporary = %v, want %v (%v)", tc.status, IsTemporary(err), tc.wantTemporary, err)
			}
			if IsPermanent(err) == tc.wantTemporary {
				t.Errorf("status %d: classification must be exactly one of temporary/permanent (%v)", tc.status, err)
			}
			assertNoSecrets(t, "webhook error", err.Error())
		}
	}
}

func TestWebhookUnreachableEndpointIsTemporary(t *testing.T) {
	srv, _ := newWebhookServer(t, http.StatusOK)
	url := srv.URL
	srv.Close() // 리슨 종료 — 연결 실패를 만든다

	n := NewWebhook(WebhookConfig{URL: url, Timeout: 2 * time.Second})
	err := n.Send(context.Background(), Message{Title: "제목"})
	if !IsTemporary(err) {
		t.Errorf("connection failure must be temporary, got %v", err)
	}
}

func TestWebhookHonorsCanceledContext(t *testing.T) {
	srv, _ := newWebhookServer(t, http.StatusOK)
	n := NewWebhook(WebhookConfig{URL: srv.URL})
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := n.Send(ctx, Message{Title: "제목"}); !IsTemporary(err) {
		t.Errorf("canceled context must yield a temporary error, got %v", err)
	}
}
