package notify

import (
	"bufio"
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/base64"
	"encoding/pem"
	"fmt"
	"math/big"
	"mime"
	"net"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

// testServerCert는 테스트 전용 자체 서명 인증서를 그 자리에서 만든다.
// 실제 키·인증서를 저장소에 커밋하지 않기 위함이다 (보안 하드 규칙 4).
func testServerCert(t *testing.T) (tls.Certificate, string) {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatalf("generate key: %v", err)
	}
	tmpl := &x509.Certificate{
		SerialNumber:          big.NewInt(1),
		Subject:               pkix.Name{CommonName: "smtp-test"},
		NotBefore:             time.Now().Add(-time.Hour),
		NotAfter:              time.Now().Add(time.Hour),
		IsCA:                  true,
		BasicConstraintsValid: true,
		KeyUsage:              x509.KeyUsageDigitalSignature | x509.KeyUsageCertSign,
		ExtKeyUsage:           []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
		DNSNames:              []string{"localhost"},
		IPAddresses:           []net.IP{net.ParseIP("127.0.0.1")},
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &key.PublicKey, key)
	if err != nil {
		t.Fatalf("create certificate: %v", err)
	}
	certPEM := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der})
	caPath := filepath.Join(t.TempDir(), "ca.pem")
	if err := os.WriteFile(caPath, certPEM, 0o600); err != nil {
		t.Fatalf("write ca file: %v", err)
	}
	return tls.Certificate{Certificate: [][]byte{der}, PrivateKey: key}, caPath
}

// fakeSMTP는 STARTTLS까지 흉내내는 최소 SMTP 서버다.
type fakeSMTP struct {
	ln        net.Listener
	tlsConf   *tls.Config
	rcptReply string

	mu       sync.Mutex
	messages []string
	usedTLS  bool
	authSeen bool
}

func newFakeSMTP(t *testing.T) *fakeSMTP {
	t.Helper()
	cert, _ := testServerCert(t)
	return newFakeSMTPWithCert(t, cert)
}

func newFakeSMTPWithCert(t *testing.T, cert tls.Certificate) *fakeSMTP {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	s := &fakeSMTP{
		ln:        ln,
		tlsConf:   &tls.Config{Certificates: []tls.Certificate{cert}, MinVersion: tls.VersionTLS12},
		rcptReply: "250 OK",
	}
	go s.serve()
	t.Cleanup(func() { _ = ln.Close() })
	return s
}

func (s *fakeSMTP) port(t *testing.T) int {
	t.Helper()
	_, portStr, err := net.SplitHostPort(s.ln.Addr().String())
	if err != nil {
		t.Fatalf("split host port: %v", err)
	}
	var port int
	if _, err := fmt.Sscanf(portStr, "%d", &port); err != nil {
		t.Fatalf("parse port: %v", err)
	}
	return port
}

func (s *fakeSMTP) serve() {
	for {
		conn, err := s.ln.Accept()
		if err != nil {
			return
		}
		go s.handle(conn)
	}
}

func (s *fakeSMTP) handle(conn net.Conn) {
	defer func() { _ = conn.Close() }()
	_ = conn.SetDeadline(time.Now().Add(10 * time.Second))
	r := bufio.NewReader(conn)
	w := bufio.NewWriter(conn)
	reply := func(format string, args ...any) {
		fmt.Fprintf(w, format+"\r\n", args...)
		_ = w.Flush()
	}
	tlsActive := false
	reply("220 fake ESMTP")
	for {
		line, err := r.ReadString('\n')
		if err != nil {
			return
		}
		fields := strings.Fields(line)
		if len(fields) == 0 {
			continue
		}
		switch strings.ToUpper(fields[0]) {
		case "EHLO":
			if tlsActive {
				reply("250-fake\r\n250 AUTH PLAIN")
			} else {
				reply("250-fake\r\n250 STARTTLS")
			}
		case "HELO":
			reply("250 fake")
		case "STARTTLS":
			reply("220 Ready to start TLS")
			tlsConn := tls.Server(conn, s.tlsConf)
			if err := tlsConn.Handshake(); err != nil {
				return
			}
			r = bufio.NewReader(tlsConn)
			w = bufio.NewWriter(tlsConn)
			tlsActive = true
			s.mu.Lock()
			s.usedTLS = true
			s.mu.Unlock()
		case "AUTH":
			s.mu.Lock()
			s.authSeen = true
			s.mu.Unlock()
			reply("235 2.7.0 Accepted")
		case "MAIL":
			reply("250 OK")
		case "RCPT":
			reply("%s", s.rcptReply)
		case "DATA":
			reply("354 End data with <CR><LF>.<CR><LF>")
			var body strings.Builder
			for {
				l, err := r.ReadString('\n')
				if err != nil {
					return
				}
				if l == ".\r\n" || l == ".\n" {
					break
				}
				body.WriteString(l)
			}
			s.mu.Lock()
			s.messages = append(s.messages, body.String())
			s.mu.Unlock()
			reply("250 2.0.0 Ok: queued")
		case "QUIT":
			reply("221 Bye")
			return
		case "RSET", "NOOP":
			reply("250 OK")
		default:
			reply("500 unrecognized command")
		}
	}
}

func (s *fakeSMTP) received() []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]string(nil), s.messages...)
}

func TestEmailSendOverSTARTTLS(t *testing.T) {
	cert, caPath := testServerCert(t)
	srv := newFakeSMTPWithCert(t, cert)

	n := NewEmail(EmailConfig{
		Host:     "127.0.0.1",
		Port:     srv.port(t),
		Username: "autocert",
		Password: fakePassword,
		From:     "autocert@example.test",
		TLSMode:  TLSModeStartTLS,
		CAFile:   caPath,
		Timeout:  5 * time.Second,
	})
	if ok, reason := n.Enabled(); !ok {
		t.Fatalf("channel must be enabled: %s", reason)
	}

	m := BuildCertExpiryMessage(testFacts())
	m.Recipient = "ops@example.test"
	if err := n.Send(context.Background(), m); err != nil {
		t.Fatalf("Send: %v", err)
	}

	srv.mu.Lock()
	usedTLS, authSeen := srv.usedTLS, srv.authSeen
	srv.mu.Unlock()
	if !usedTLS {
		t.Error("the session must be upgraded with STARTTLS")
	}
	if !authSeen {
		t.Error("AUTH must be attempted when a username is configured")
	}

	got := srv.received()
	if len(got) != 1 {
		t.Fatalf("received %d messages, want 1", len(got))
	}
	raw := got[0]
	headers, body, ok := strings.Cut(raw, "\r\n\r\n")
	if !ok {
		t.Fatalf("message has no header/body separator:\n%s", raw)
	}
	if !strings.Contains(headers, "To: ops@example.test") || !strings.Contains(headers, "From: autocert@example.test") {
		t.Errorf("envelope headers missing:\n%s", headers)
	}
	if !strings.Contains(headers, "Content-Type: text/plain; charset=\"UTF-8\"") {
		t.Errorf("content type header missing:\n%s", headers)
	}
	if !strings.Contains(headers, "X-AutoCert-Event: "+EventCertExpiring) {
		t.Errorf("event header missing:\n%s", headers)
	}

	var subject string
	for _, line := range strings.Split(headers, "\r\n") {
		if strings.HasPrefix(line, "Subject: ") {
			subject = strings.TrimPrefix(line, "Subject: ")
		}
	}
	decoded, err := new(mime.WordDecoder).DecodeHeader(subject)
	if err != nil {
		t.Fatalf("decode subject %q: %v", subject, err)
	}
	if decoded != m.Title {
		t.Errorf("subject = %q, want %q", decoded, m.Title)
	}

	decodedBody, err := base64.StdEncoding.DecodeString(strings.ReplaceAll(strings.TrimSpace(body), "\r\n", ""))
	if err != nil {
		t.Fatalf("decode body: %v", err)
	}
	if string(decodedBody) != m.TextBody {
		t.Errorf("body mismatch:\ngot  %q\nwant %q", decodedBody, m.TextBody)
	}
}

func TestEmailSMTPErrorClassification(t *testing.T) {
	cases := []struct {
		name          string
		rcptReply     string
		wantTemporary bool
	}{
		{"permanent 5xx", "550 5.1.1 <ops@example.test>: Recipient address rejected: User unknown", false},
		{"temporary 4xx", "451 4.3.0 <ops@example.test>: Temporary lookup failure", true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			cert, caPath := testServerCert(t)
			srv := newFakeSMTPWithCert(t, cert)
			srv.rcptReply = tc.rcptReply

			n := NewEmail(EmailConfig{
				Host: "127.0.0.1", Port: srv.port(t), From: "autocert@example.test",
				TLSMode: TLSModeStartTLS, CAFile: caPath, Timeout: 5 * time.Second,
			})
			err := n.Send(context.Background(), Message{Title: "t", TextBody: "b", Recipient: "ops@example.test"})
			if err == nil {
				t.Fatal("expected an error")
			}
			if IsTemporary(err) != tc.wantTemporary {
				t.Errorf("IsTemporary = %v, want %v (%v)", IsTemporary(err), tc.wantTemporary, err)
			}
			if IsPermanent(err) == tc.wantTemporary {
				t.Errorf("classification must be exactly one of temporary/permanent: %v", err)
			}
			// 수신자 주소는 서버 응답에 섞여 와도 에러 문자열에 남지 않는다.
			if strings.Contains(err.Error(), "ops@example.test") {
				t.Errorf("error must not carry the recipient address: %v", err)
			}
			assertNoSecrets(t, "email error", err.Error())
		})
	}
}

func TestEmailUntrustedServerCertificateIsPermanent(t *testing.T) {
	srv := newFakeSMTP(t) // 서버 인증서를 신뢰 목록에 넣지 않는다
	n := NewEmail(EmailConfig{
		Host: "127.0.0.1", Port: srv.port(t), From: "autocert@example.test",
		TLSMode: TLSModeStartTLS, Timeout: 5 * time.Second,
	})
	err := n.Send(context.Background(), Message{Title: "t", Recipient: "ops@example.test"})
	if !IsPermanent(err) {
		t.Errorf("an untrusted starttls certificate must be permanent, got %v", err)
	}
}

func TestEmailConnectionFailureIsTemporary(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	_, portStr, _ := net.SplitHostPort(ln.Addr().String())
	var port int
	_, _ = fmt.Sscanf(portStr, "%d", &port)
	_ = ln.Close()

	n := NewEmail(EmailConfig{Host: "127.0.0.1", Port: port, From: "autocert@example.test", Timeout: 2 * time.Second})
	sendErr := n.Send(context.Background(), Message{Title: "t", Recipient: "ops@example.test"})
	if !IsTemporary(sendErr) {
		t.Errorf("connection failure must be temporary, got %v", sendErr)
	}
}

func TestEmailBuildMIMEBlocksHeaderInjection(t *testing.T) {
	n := NewEmail(EmailConfig{Host: "smtp.example.test", From: "autocert@example.test"})
	raw := string(n.buildMIME(Message{
		Title:    "제목\r\nBcc: attacker@example.test",
		TextBody: "본문",
	}, sanitizeHeaderValue("ops@example.test\r\nBcc: attacker@example.test")))
	headers, _, _ := strings.Cut(raw, "\r\n\r\n")
	// CR/LF가 제거되므로 주입 문자열은 새 헤더 줄이 되지 못하고 원래 줄 안에 남는다.
	for _, line := range strings.Split(headers, "\r\n") {
		if strings.HasPrefix(strings.ToLower(line), "bcc:") {
			t.Errorf("header injection must not create a new header line:\n%s", headers)
		}
	}
}

func TestRedactAddresses(t *testing.T) {
	in := "550 5.1.1 <ops@example.test>: User unknown"
	got := redactAddresses(in)
	if strings.Contains(got, "ops@example.test") {
		t.Errorf("address must be redacted: %q", got)
	}
	if !strings.Contains(got, "550") {
		t.Errorf("diagnostic code must survive redaction: %q", got)
	}
}
