package notify

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"crypto/tls"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"strings"
	"syscall"
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
		// 여기서 실패하는 원인은 URL 파싱뿐인데 그 오류 문자열은 URL 전문을 품는다.
		// 웹훅 URL은 경로에 토큰이 박히는 경우가 흔하므로 값 없이 변수 이름만 알린다.
		return Permanent(errors.New("notify/webhook: WEBHOOK_URL is not a valid request url"))
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
			return Temporaryf("notify/webhook: request to WEBHOOK_URL aborted (%s)", netErrKind(err))
		}
		// 연결 실패·타임아웃은 재시도 대상이다. net/http 오류 문자열은 요청 URL
		// 전문을 품으므로 값은 버리고 오류 종류만 남긴다.
		return Temporaryf("notify/webhook: request to WEBHOOK_URL failed (%s)", netErrKind(err))
	}
	defer func() {
		_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 1<<16))
		_ = resp.Body.Close()
	}()

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		// 응답 본문은 진단에 쓸모 있지만 수신 측이 요청 URL(경로의 토큰 포함)을
		// 그대로 되비추는 경우가 있다. API 응답·감사로 나가는 에러에는 싣지 않고
		// 운영자가 보는 서버 로그에만 남긴다.
		n.logger().Debug("notify: webhook endpoint returned an error status",
			"channel", ChannelWebhook, "status", resp.StatusCode, "body", peekBody(resp.Body))
	}

	switch {
	case resp.StatusCode >= 200 && resp.StatusCode < 300:
		return nil
	case resp.StatusCode == http.StatusRequestTimeout,
		resp.StatusCode == http.StatusTooManyRequests,
		resp.StatusCode >= 500:
		return Temporaryf("notify/webhook: WEBHOOK_URL endpoint returned %d", resp.StatusCode)
	default:
		// 나머지 4xx와 예상 밖 코드(1xx·3xx)는 재시도해도 같다.
		return Permanentf("notify/webhook: WEBHOOK_URL endpoint returned %d", resp.StatusCode)
	}
}

// netErrKind는 네트워크 계층 오류를 "값이 아니라 종류"로 요약한다.
//
// net/http·net·crypto/tls의 오류 문자열에는 요청 URL과 host:port가 그대로 실려 온다
// (예: `Post "https://hook.example/t/<token>": dial tcp 10.0.0.9:443: connect: connection refused`).
// 이 문자열이 테스트 발송 응답이나 감사 로그로 나가면 설정값 유출이므로, 진단에
// 필요한 종류만 남기고 값은 버린다. webhook·email·syslog 세 채널이 함께 쓴다.
func netErrKind(err error) string {
	if err == nil {
		return ""
	}
	var dnsErr *net.DNSError
	if errors.As(err, &dnsErr) {
		return "dns lookup failed"
	}
	var certErr *tls.CertificateVerificationError
	if errors.As(err, &certErr) {
		return "server certificate verification failed"
	}
	var recordErr tls.RecordHeaderError
	if errors.As(err, &recordErr) {
		return "tls handshake failed"
	}
	var netErr net.Error
	switch {
	case errors.Is(err, context.Canceled):
		return "canceled"
	case errors.Is(err, context.DeadlineExceeded):
		return "timeout"
	case errors.As(err, &netErr) && netErr.Timeout():
		return "timeout"
	case errors.Is(err, syscall.ECONNREFUSED):
		return "connection refused"
	case errors.Is(err, syscall.ECONNRESET), errors.Is(err, syscall.EPIPE):
		return "connection reset"
	case errors.Is(err, io.EOF), errors.Is(err, io.ErrUnexpectedEOF):
		return "connection closed by peer"
	default:
		return "connection failed"
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
