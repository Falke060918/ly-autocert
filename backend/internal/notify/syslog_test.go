package notify

import (
	"bufio"
	"context"
	"io"
	"net"
	"strconv"
	"strings"
	"testing"
	"time"
)

func testMessage() Message {
	return Message{
		Title:    "[인증서 만료 D-7] app.example.test",
		TextBody: "인증서 만료가 임박했습니다.\n\n- 남은 일수: 7일\n",
		Data: map[string]any{
			"common_name":    "app.example.test",
			"days_remaining": 7,
		},
		Severity: SeverityWarning,
		EventID:  EventCertExpiring,
	}
}

func TestSyslogUDPSendsRFC5424(t *testing.T) {
	pc, err := net.ListenPacket("udp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen udp: %v", err)
	}
	defer func() { _ = pc.Close() }()

	n := NewSyslog(SyslogConfig{
		Network:  "udp",
		Addr:     pc.LocalAddr().String(),
		Facility: "local1",
		AppName:  "autocert-test",
		Hostname: "clm-host",
	})
	t.Cleanup(func() { _ = n.Close() })
	if err := n.Send(context.Background(), testMessage()); err != nil {
		t.Fatalf("Send: %v", err)
	}

	buf := make([]byte, 8192)
	_ = pc.SetReadDeadline(time.Now().Add(3 * time.Second))
	count, _, err := pc.ReadFrom(buf)
	if err != nil {
		t.Fatalf("read datagram: %v", err)
	}
	line := string(buf[:count])

	// local1(17)*8 + warning(4) = 140
	if !strings.HasPrefix(line, "<140>1 ") {
		t.Errorf("PRI/VERSION mismatch: %q", line)
	}
	fields := strings.SplitN(line, " ", 7)
	if len(fields) < 7 {
		t.Fatalf("header must have 7 fields: %q", line)
	}
	if _, err := time.Parse("2006-01-02T15:04:05.000000Z07:00", fields[1]); err != nil {
		t.Errorf("timestamp is not RFC5424: %q (%v)", fields[1], err)
	}
	if fields[2] != "clm-host" || fields[3] != "autocert-test" {
		t.Errorf("hostname/app-name mismatch: %q / %q", fields[2], fields[3])
	}
	if _, err := strconv.Atoi(fields[4]); err != nil {
		t.Errorf("procid must be numeric: %q", fields[4])
	}
	if fields[5] != EventCertExpiring {
		t.Errorf("msgid = %q, want %q", fields[5], EventCertExpiring)
	}
	wantSD := `[autocert@32473 common_name="app.example.test" days_remaining="7" severity="warning"]`
	if !strings.Contains(line, wantSD) {
		t.Errorf("structured data mismatch:\ngot  %q\nwant it to contain %q", line, wantSD)
	}
	if !strings.Contains(line, " "+utf8BOM+"[인증서 만료 D-7]") {
		t.Errorf("MSG must start with a UTF-8 BOM: %q", line)
	}
	if strings.ContainsAny(line, "\n\r") {
		t.Errorf("a syslog datagram must be a single line: %q", line)
	}
}

func TestSyslogTCPUsesOctetCounting(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen tcp: %v", err)
	}
	defer func() { _ = ln.Close() }()

	frames := make(chan string, 1)
	go func() {
		conn, err := ln.Accept()
		if err != nil {
			return
		}
		defer func() { _ = conn.Close() }()
		_ = conn.SetReadDeadline(time.Now().Add(3 * time.Second))
		r := bufio.NewReader(conn)
		// RFC6587 octet-counting: "<length> <message>"
		lenField, err := r.ReadString(' ')
		if err != nil {
			return
		}
		size, err := strconv.Atoi(strings.TrimSpace(lenField))
		if err != nil {
			return
		}
		payload := make([]byte, size)
		if _, err := io.ReadFull(r, payload); err != nil {
			return
		}
		frames <- string(payload)
	}()

	n := NewSyslog(SyslogConfig{Network: "tcp", Addr: ln.Addr().String(), Facility: "local0"})
	t.Cleanup(func() { _ = n.Close() })
	if err := n.Send(context.Background(), testMessage()); err != nil {
		t.Fatalf("Send: %v", err)
	}

	select {
	case msg := <-frames:
		// 길이가 정확해야 정확히 이만큼만 읽고 다음 프레임 경계를 잡을 수 있다.
		if !strings.HasPrefix(msg, "<132>1 ") { // local0(16)*8 + warning(4)
			t.Errorf("PRI mismatch: %q", msg)
		}
		if !strings.HasSuffix(msg, "7일") {
			t.Errorf("frame length must cover the whole message, got tail %q", msg[max(0, len(msg)-20):])
		}
	case <-time.After(3 * time.Second):
		t.Fatal("timed out waiting for the syslog frame")
	}
}

func TestSyslogUnknownSeverityFallsBackToInfo(t *testing.T) {
	n := NewSyslog(SyslogConfig{Network: "udp", Addr: "127.0.0.1:514", Facility: "local0", Hostname: "h"})
	line := n.format(Message{Title: "t", Severity: "bogus"}, time.Now())
	if !strings.HasPrefix(line, "<134>1 ") { // 16*8 + info(6)
		t.Errorf("unknown severity must fall back to info: %q", line)
	}
}

func TestSyslogEscapesStructuredDataValues(t *testing.T) {
	n := NewSyslog(SyslogConfig{Network: "udp", Addr: "127.0.0.1:514", Hostname: "h"})
	line := n.format(Message{
		Title:    "t",
		Severity: SeverityInfo,
		Data:     map[string]any{"common_name": `we"ird\value]here`},
	}, time.Now())
	if !strings.Contains(line, `common_name="we\"ird\\value\]here"`) {
		t.Errorf("structured data value is not escaped per RFC5424 6.3.3: %q", line)
	}
}

func TestSyslogEmptyDataYieldsNilValue(t *testing.T) {
	n := NewSyslog(SyslogConfig{Network: "udp", Addr: "127.0.0.1:514", Hostname: "h"})
	line := n.format(Message{Title: "t"}, time.Now())
	fields := strings.SplitN(line, " ", 7)
	if len(fields) < 7 || fields[6] == "" || !strings.HasPrefix(fields[6], "-") {
		t.Errorf("structured data must be NILVALUE when there is nothing to send: %q", line)
	}
}

func TestSyslogUnreachableCollectorIsTemporary(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen tcp: %v", err)
	}
	addr := ln.Addr().String()
	_ = ln.Close() // 아무도 듣지 않는 주소를 만든다

	n := NewSyslog(SyslogConfig{Network: "tcp", Addr: addr, Timeout: time.Second})
	t.Cleanup(func() { _ = n.Close() })
	if err := n.Send(context.Background(), testMessage()); !IsTemporary(err) {
		t.Errorf("unreachable collector must be temporary, got %v", err)
	}
}

// 수집기가 재시작해 연결이 끊겨도 다음 Send가 1회 재연결로 복구되는지 고정한다.
func TestSyslogReconnectsAfterBrokenConnection(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen tcp: %v", err)
	}
	defer func() { _ = ln.Close() }()

	accepted := make(chan net.Conn, 4)
	go func() {
		for {
			conn, err := ln.Accept()
			if err != nil {
				return
			}
			accepted <- conn
		}
	}()

	n := NewSyslog(SyslogConfig{Network: "tcp", Addr: ln.Addr().String(), Timeout: 2 * time.Second})
	t.Cleanup(func() { _ = n.Close() })
	if err := n.Send(context.Background(), testMessage()); err != nil {
		t.Fatalf("first Send: %v", err)
	}
	first := <-accepted
	_ = first.Close() // 수집기 재시작 흉내

	// 끊긴 소켓에 대한 첫 쓰기는 커널 버퍼에 그대로 들어갈 수 있으므로 두 번 보낸다.
	var lastErr error
	for i := 0; i < 3; i++ {
		lastErr = n.Send(context.Background(), testMessage())
		if lastErr != nil {
			t.Fatalf("Send after collector restart must recover: %v", lastErr)
		}
	}
}
