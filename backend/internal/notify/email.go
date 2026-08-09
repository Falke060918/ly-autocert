package notify

import (
	"context"
	"crypto/tls"
	"encoding/base64"
	"errors"
	"fmt"
	"mime"
	"net"
	"net/smtp"
	"net/textproto"
	"regexp"
	"strconv"
	"strings"
	"time"
)

// Channel은 채널 식별자를 반환한다.
func (n *EmailNotifier) Channel() string { return ChannelEmail }

// Send는 메시지 한 건을 메일로 보낸다.
// 수신자 주소는 로그·에러 메시지에 남기지 않는다 — 서버 응답에 섞여 오는 주소도 지운다.
func (n *EmailNotifier) Send(ctx context.Context, m Message) error {
	if ok, reason := n.Enabled(); !ok {
		return Permanentf("notify/email: channel disabled: %s", reason)
	}
	to := sanitizeHeaderValue(m.Recipient)
	if to == "" {
		return Permanent(errors.New("notify/email: message has no recipient"))
	}
	if err := ctx.Err(); err != nil {
		return Temporary(fmt.Errorf("notify/email: %w", err))
	}

	client, err := n.dial(ctx)
	if err != nil {
		return err
	}
	defer func() { _ = client.Close() }()

	if n.username != "" {
		auth := smtp.PlainAuth("", n.username, n.password, n.host)
		if err := client.Auth(auth); err != nil {
			// 인증 실패는 자격증명 문제다 — 재시도해도 같다.
			return Permanentf("notify/email: smtp auth failed: %s", smtpErrText(err))
		}
	}
	if err := client.Mail(n.from); err != nil {
		return classifySMTPError("mail from rejected", err)
	}
	if err := client.Rcpt(to); err != nil {
		return classifySMTPError("recipient rejected", err)
	}
	w, err := client.Data()
	if err != nil {
		return classifySMTPError("data command rejected", err)
	}
	if _, err := w.Write(n.buildMIME(m, to)); err != nil {
		return Temporaryf("notify/email: write message body failed: %s", smtpErrText(err))
	}
	if err := w.Close(); err != nil {
		return classifySMTPError("message rejected", err)
	}
	if err := client.Quit(); err != nil {
		// 본문은 이미 250으로 확정됐다. QUIT 실패는 성공으로 본다 —
		// 여기서 에러를 내면 재시도가 같은 메일을 중복 발송한다.
		n.logger().Debug("smtp quit failed after successful delivery",
			"channel", ChannelEmail, "smtp_host", n.host)
	}
	return nil
}

// dial은 SMTP 접속을 만든다. TLSMode에 따라 implicit TLS / STARTTLS / 평문으로 갈린다.
func (n *EmailNotifier) dial(ctx context.Context) (*smtp.Client, error) {
	addr := net.JoinHostPort(n.host, strconv.Itoa(n.port))
	deadline := time.Now().Add(n.timeout)
	if dl, ok := ctx.Deadline(); ok && dl.Before(deadline) {
		deadline = dl
	}
	dialer := &net.Dialer{Deadline: deadline}

	var conn net.Conn
	var err error
	if n.tlsMode == TLSModeTLS {
		conn, err = tls.DialWithDialer(dialer, "tcp", addr, n.tlsConf)
	} else {
		conn, err = dialer.DialContext(ctx, "tcp", addr)
	}
	if err != nil {
		// 네트워크 계층 실패는 재시도 대상이다.
		return nil, Temporaryf("notify/email: connect to %s failed: %s", addr, smtpErrText(err))
	}
	_ = conn.SetDeadline(deadline)

	client, err := smtp.NewClient(conn, n.host)
	if err != nil {
		_ = conn.Close()
		return nil, Temporaryf("notify/email: smtp greeting failed: %s", smtpErrText(err))
	}
	if n.tlsMode == TLSModeStartTLS {
		if err := client.StartTLS(n.tlsConf); err != nil {
			_ = client.Close()
			// STARTTLS 미지원·인증서 불신뢰 모두 설정 문제다 — 재시도해도 같다.
			return nil, Permanentf("notify/email: starttls failed: %s", smtpErrText(err))
		}
	}
	return client, nil
}

// buildMIME은 RFC 5322 메일 한 통을 만든다. 본문은 UTF-8 base64로 인코딩해
// 한글 본문과 긴 줄에서 깨지지 않게 한다.
func (n *EmailNotifier) buildMIME(m Message, to string) []byte {
	subject := mime.QEncoding.Encode("UTF-8", sanitizeHeaderValue(m.Title))
	var b strings.Builder
	fmt.Fprintf(&b, "From: %s\r\n", n.from)
	fmt.Fprintf(&b, "To: %s\r\n", to)
	fmt.Fprintf(&b, "Subject: %s\r\n", subject)
	fmt.Fprintf(&b, "Date: %s\r\n", time.Now().Format(time.RFC1123Z))
	if m.EventID != "" {
		fmt.Fprintf(&b, "X-AutoCert-Event: %s\r\n", sanitizeHeaderValue(m.EventID))
	}
	if m.Severity != "" {
		fmt.Fprintf(&b, "X-AutoCert-Severity: %s\r\n", sanitizeHeaderValue(m.Severity))
	}
	b.WriteString("MIME-Version: 1.0\r\n")
	b.WriteString("Content-Type: text/plain; charset=\"UTF-8\"\r\n")
	b.WriteString("Content-Transfer-Encoding: base64\r\n")
	b.WriteString("\r\n")

	encoded := base64.StdEncoding.EncodeToString([]byte(m.TextBody))
	for len(encoded) > 0 {
		chunk := encoded
		if len(chunk) > 76 {
			chunk = chunk[:76]
		}
		encoded = encoded[len(chunk):]
		b.WriteString(chunk)
		b.WriteString("\r\n")
	}
	return []byte(b.String())
}

// classifySMTPError는 SMTP 응답 코드로 재시도 여부를 가른다.
// 5xx = 영구 실패, 4xx = 일시 실패, 코드가 없는 오류(네트워크 단절 등) = 일시 실패.
func classifySMTPError(what string, err error) error {
	var te *textproto.Error
	if errors.As(err, &te) {
		switch {
		case te.Code >= 500 && te.Code < 600:
			return Permanentf("notify/email: %s (%d %s)", what, te.Code, redactAddresses(te.Msg))
		case te.Code >= 400 && te.Code < 500:
			return Temporaryf("notify/email: %s (%d %s)", what, te.Code, redactAddresses(te.Msg))
		}
	}
	return Temporaryf("notify/email: %s: %s", what, smtpErrText(err))
}

// smtpErrText는 에러 문자열에서 메일 주소를 지운다. SMTP 서버 응답과 dial 오류에는
// 수신자 주소가 그대로 섞여 오는데, 이 에러는 로그로 나가므로 그대로 두면 안 된다.
func smtpErrText(err error) string {
	if err == nil {
		return ""
	}
	return redactAddresses(err.Error())
}

var emailAddrRe = regexp.MustCompile(`[^\s<>",;:]+@[^\s<>",;:]+`)

func redactAddresses(s string) string {
	return emailAddrRe.ReplaceAllString(s, redactedMark)
}
