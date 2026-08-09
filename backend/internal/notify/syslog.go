package notify

import (
	"context"
	"fmt"
	"io"
	"net"
	"sort"
	"strings"
	"time"
)

// log/syslog는 쓰지 않는다: 동결된 패키지이고 RFC3164만 내며 Windows를 지원하지 않는다.
// 대신 RFC5424를 직접 포맷하고 net.Dial로 보낸다.
// (메시지 포맷팅은 암호 연산이 아니므로 "자체 암호 구현 금지" 규칙과 무관하다.)

const (
	// nilValue는 RFC5424의 NILVALUE다.
	nilValue = "-"
	// syslogSDID는 구조화 데이터 ID다. RFC5424는 사설 SD-ID에 IANA 기업번호(PEN)를
	// 요구한다. 32473은 문서화용으로 예약된 번호이며(RFC5612), 자체 PEN을 받으면 교체한다.
	syslogSDID = "autocert@32473"
	// syslogVersion은 RFC5424 VERSION 필드다.
	syslogVersion = "1"
	// utf8BOM은 MSG가 UTF-8임을 알리는 표식이다 (RFC5424 6.4).
	utf8BOM = "\uFEFF"

	maxHostnameLen = 255
	maxAppNameLen  = 48
	maxProcIDLen   = 128
	maxMsgIDLen    = 32
)

// syslogSeverities는 Message.Severity → RFC5424 severity 숫자다.
var syslogSeverities = map[string]int{
	SeverityCritical: 2, // crit
	SeverityError:    3, // err
	SeverityWarning:  4, // warning
	SeverityInfo:     6, // info
	"debug":          7,
}

// netConn은 syslog가 필요로 하는 연결 동작만 추린 것이다 (테스트 대체 가능).
type netConn interface {
	io.WriteCloser
	SetWriteDeadline(t time.Time) error
}

func dialNet(network, addr string, timeout time.Duration) (netConn, error) {
	conn, err := net.DialTimeout(network, addr, timeout)
	if err != nil {
		return nil, err
	}
	return conn, nil
}

// Channel은 채널 식별자를 반환한다.
func (n *SyslogNotifier) Channel() string { return ChannelSyslog }

// Send는 RFC5424 메시지 한 건을 보낸다.
//
// UDP는 fire-and-forget이다 — nil 반환은 소켓에 썼다는 뜻일 뿐 수집기가 받았다는
// 보장이 아니다. 유실을 허용할 수 없으면 SYSLOG_NETWORK=tcp를 쓴다.
func (n *SyslogNotifier) Send(ctx context.Context, m Message) error {
	if ok, reason := n.Enabled(); !ok {
		return Permanentf("notify/syslog: channel disabled: %s", reason)
	}
	if err := ctx.Err(); err != nil {
		return Temporary(fmt.Errorf("notify/syslog: %w", err))
	}
	frame := []byte(n.frame(m, time.Now()))

	n.mu.Lock()
	defer n.mu.Unlock()

	deadline := time.Now().Add(n.timeout)
	if dl, ok := ctx.Deadline(); ok && dl.Before(deadline) {
		deadline = dl
	}
	err := n.writeLocked(frame, deadline)
	if err == nil {
		return nil
	}
	// 끊긴 연결은 흔하다(수집기 재시작 등). 1회 재연결 후 다시 시도한다.
	n.closeLocked()
	if retryErr := n.writeLocked(frame, deadline); retryErr != nil {
		return Temporaryf("notify/syslog: send to %s/%s failed: %v", n.network, n.addr, retryErr)
	}
	return nil
}

func (n *SyslogNotifier) writeLocked(frame []byte, deadline time.Time) error {
	if n.conn == nil {
		conn, err := n.dial(n.network, n.addr, n.timeout)
		if err != nil {
			return err
		}
		n.conn = conn
	}
	_ = n.conn.SetWriteDeadline(deadline)
	if _, err := n.conn.Write(frame); err != nil {
		return err
	}
	return nil
}

func (n *SyslogNotifier) closeLocked() {
	if n.conn != nil {
		_ = n.conn.Close()
		n.conn = nil
	}
}

// Close는 열린 연결을 닫는다. 종료 경로에서 호출한다.
func (n *SyslogNotifier) Close() error {
	n.mu.Lock()
	defer n.mu.Unlock()
	n.closeLocked()
	return nil
}

// frame은 전송 단위를 만든다. TCP는 RFC6587 octet-counting("<길이> <메시지>")으로
// 감싸고, UDP는 메시지 하나가 데이터그램 하나다.
func (n *SyslogNotifier) frame(m Message, now time.Time) string {
	msg := n.format(m, now)
	if n.network == "tcp" {
		return fmt.Sprintf("%d %s", len(msg), msg)
	}
	return msg
}

// format은 RFC5424 메시지를 만든다.
// <PRI>VERSION SP TIMESTAMP SP HOSTNAME SP APP-NAME SP PROCID SP MSGID SP SD SP BOM MSG
func (n *SyslogNotifier) format(m Message, now time.Time) string {
	severity, ok := syslogSeverities[strings.ToLower(m.Severity)]
	if !ok {
		severity = syslogSeverities[SeverityInfo]
	}
	pri := n.facility*8 + severity

	msgID := sanitizeASCII(m.EventID, maxMsgIDLen)
	if msgID == "" {
		msgID = nilValue
	}
	var b strings.Builder
	fmt.Fprintf(&b, "<%d>%s %s %s %s %s %s %s",
		pri,
		syslogVersion,
		now.UTC().Format("2006-01-02T15:04:05.000000Z07:00"),
		sanitizeASCII(n.hostname, maxHostnameLen),
		sanitizeASCII(n.appName, maxAppNameLen),
		sanitizeASCII(n.procID, maxProcIDLen),
		msgID,
		n.structuredData(m),
	)
	if text := messageLine(m); text != "" {
		// RFC5424: MSG가 UTF-8이면 BOM(U+FEFF)을 앞에 붙인다.
		b.WriteString(" ")
		b.WriteString(utf8BOM)
		b.WriteString(text)
	}
	return b.String()
}

// structuredData는 Data 맵을 RFC5424 STRUCTURED-DATA로 만든다. 키 순서는 정렬해
// 같은 입력이 항상 같은 바이트를 내도록 한다.
func (n *SyslogNotifier) structuredData(m Message) string {
	params := make(map[string]string, len(m.Data)+1)
	for k, v := range m.Data {
		name := sanitizeSDName(k)
		if name == "" {
			continue
		}
		params[name] = fmt.Sprint(v)
	}
	if m.Severity != "" {
		params["severity"] = m.Severity
	}
	if len(params) == 0 {
		return nilValue
	}
	names := make([]string, 0, len(params))
	for k := range params {
		names = append(names, k)
	}
	sort.Strings(names)

	var b strings.Builder
	b.WriteString("[")
	b.WriteString(syslogSDID)
	for _, name := range names {
		fmt.Fprintf(&b, " %s=\"%s\"", name, escapeSDValue(params[name]))
	}
	b.WriteString("]")
	return b.String()
}

// messageLine은 MSG 본문을 한 줄로 만든다. 줄바꿈은 수집기에 따라 레코드 경계로
// 해석되므로 공백으로 접는다.
func messageLine(m Message) string {
	text := m.Title
	if m.TextBody != "" {
		if text != "" {
			text += " | "
		}
		text += m.TextBody
	}
	return strings.Join(strings.Fields(text), " ")
}

// escapeSDValue는 RFC5424 6.3.3의 이스케이프를 적용한다 ('"', '\', ']').
func escapeSDValue(s string) string {
	s = strings.Join(strings.Fields(s), " ")
	r := strings.NewReplacer(`\`, `\\`, `"`, `\"`, `]`, `\]`)
	return r.Replace(s)
}

// sanitizeSDName은 PARAM-NAME 제약(공백·'='·']'·'"' 불가, ASCII)을 적용한다.
func sanitizeSDName(s string) string {
	var b strings.Builder
	for _, r := range s {
		if r < 33 || r > 126 || r == '=' || r == ']' || r == '"' {
			continue
		}
		b.WriteRune(r)
		if b.Len() >= 32 {
			break
		}
	}
	return b.String()
}

// sanitizeASCII는 HEADER 필드 제약(출력 가능 ASCII, 공백 불가)을 적용한다.
func sanitizeASCII(s string, max int) string {
	var b strings.Builder
	for _, r := range s {
		if r < 33 || r > 126 {
			continue
		}
		b.WriteRune(r)
		if b.Len() >= max {
			break
		}
	}
	if b.Len() == 0 {
		return nilValue
	}
	return b.String()
}
