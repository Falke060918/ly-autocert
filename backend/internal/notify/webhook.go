package notify

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

// SignatureHeader는 본문 HMAC-SHA256 서명 헤더다. 값 형식은 "sha256=<hex>"이며
// 서명 대상은 전송하는 JSON 본문 바이트 전체다.
const SignatureHeader = "X-AutoCert-Signature"

// webhookPayload는 웹훅 수신 측 계약이다. 필드를 바꾸면 수신 측이 깨지므로
// 추가는 하되 이름 변경·삭제는 하지 않는다.
type webhookPayload struct {
	EventID  string         `json:"event_id"`
	Severity string         `json:"severity"`
	Title    string         `json:"title"`
	Text     string         `json:"text,omitempty"`
	Data     map[string]any `json:"data,omitempty"`
	SentAt   string         `json:"sent_at"`
}

// Channel은 채널 식별자를 반환한다.
func (n *WebhookNotifier) Channel() string { return ChannelWebhook }

// Send는 메시지를 JSON으로 POST한다.
func (n *WebhookNotifier) Send(ctx context.Context, m Message) error {
	if ok, reason := n.Enabled(); !ok {
		return Permanentf("notify/webhook: channel disabled: %s", reason)
	}
	severity := m.Severity
	if severity == "" {
		severity = SeverityInfo
	}
	body, err := json.Marshal(webhookPayload{
		EventID:  m.EventID,
		Severity: severity,
		Title:    m.Title,
		Text:     m.TextBody,
		Data:     m.Data,
		SentAt:   n.now().UTC().Format(time.RFC3339),
	})
	if err != nil {
		// Data에 직렬화 불가능한 값이 들어온 경우다 — 재시도해도 같다.
		return Permanentf("notify/webhook: encode payload failed: %v", err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, n.url, bytes.NewReader(body))
	if err != nil {
		return Permanentf("notify/webhook: build request failed: %v", err)
	}
	req.Header.Set("Content-Type", "application/json; charset=utf-8")
	req.Header.Set("User-Agent", "AutoCert-Notifier")
	if m.EventID != "" {
		req.Header.Set("X-AutoCert-Event", sanitizeHeaderValue(m.EventID))
	}
	if len(n.secret) > 0 {
		req.Header.Set(SignatureHeader, signBody(n.secret, body))
	}
	if n.authName != "" {
		req.Header.Set(n.authName, n.authValue)
	}

	resp, err := n.client.Do(req)
	if err != nil {
		if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
			return Temporaryf("notify/webhook: request aborted: %v", ctx.Err())
		}
		// 연결 실패·타임아웃은 재시도 대상이다.
		return Temporaryf("notify/webhook: request failed: %v", err)
	}
	defer func() {
		_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 1<<16))
		_ = resp.Body.Close()
	}()

	switch {
	case resp.StatusCode >= 200 && resp.StatusCode < 300:
		return nil
	case resp.StatusCode == http.StatusRequestTimeout,
		resp.StatusCode == http.StatusTooManyRequests,
		resp.StatusCode >= 500:
		return Temporaryf("notify/webhook: endpoint returned %d: %s", resp.StatusCode, peekBody(resp.Body))
	default:
		// 나머지 4xx와 예상 밖 코드(1xx·3xx)는 재시도해도 같다.
		return Permanentf("notify/webhook: endpoint returned %d: %s", resp.StatusCode, peekBody(resp.Body))
	}
}

// signBody는 본문 HMAC-SHA256 서명 값을 만든다 (crypto/hmac + crypto/sha256 —
// 자체 암호 구현 금지 규칙에 따라 표준 라이브러리만 쓴다).
func signBody(secret, body []byte) string {
	mac := hmac.New(sha256.New, secret)
	mac.Write(body)
	return "sha256=" + hex.EncodeToString(mac.Sum(nil))
}

// peekBody는 진단용으로 응답 본문 앞부분만 한 줄로 만든다.
func peekBody(r io.Reader) string {
	b, err := io.ReadAll(io.LimitReader(r, 256))
	if err != nil || len(b) == 0 {
		return "(no body)"
	}
	s := strings.Join(strings.Fields(string(b)), " ")
	if len(s) > 200 {
		s = s[:200] + "..."
	}
	return fmt.Sprintf("%q", s)
}

func (n *WebhookNotifier) now() time.Time {
	if n.nowOverride != nil {
		return n.nowOverride()
	}
	return time.Now()
}
