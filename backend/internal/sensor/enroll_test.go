package sensor

import (
	"context"
	"crypto/ecdsa"
	"crypto/sha256"
	"crypto/tls"
	"crypto/x509"
	"encoding/hex"
	"encoding/json"
	"encoding/pem"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/Falke060918/INHERIT_AutoCert/backend/internal/sensorwire"
)

// newEnrollHandler는 게이트웨이의 enroll 계약을 흉내 낸다:
// 토큰 대조 → CSR 서명 검증 → 클라이언트 인증서 발급. 개인키는 요청에 없어야 정상이다.
func newEnrollHandler(ca *testCA, token, sensorID string) http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("POST /sensor/v1/enroll", func(w http.ResponseWriter, r *http.Request) {
		var req sensorwire.EnrollRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			http.Error(w, "bad request", http.StatusBadRequest)
			return
		}
		if err := req.Validate(); err != nil || req.Token != token {
			http.Error(w, "forbidden", http.StatusForbidden)
			return
		}
		block, _ := pem.Decode([]byte(req.CSRPEM))
		csr, err := x509.ParseCertificateRequest(block.Bytes)
		if err != nil || csr.CheckSignature() != nil {
			http.Error(w, "bad csr", http.StatusBadRequest)
			return
		}
		pub, ok := csr.PublicKey.(*ecdsa.PublicKey)
		if !ok {
			http.Error(w, "bad key type", http.StatusBadRequest)
			return
		}
		certPEM, err := ca.issueClientCert(pub, csr.Subject.CommonName, time.Now().Add(90*24*time.Hour))
		if err != nil {
			http.Error(w, "issue failed", http.StatusInternalServerError)
			return
		}
		_ = json.NewEncoder(w).Encode(sensorwire.EnrollResponse{
			SensorID:      sensorID,
			ClientCertPEM: string(certPEM),
			CACertPEM:     string(ca.certPEM),
		})
	})
	return mux
}

func TestEnrollWithLeafPin(t *testing.T) {
	ca := newTestCA(t)
	token := randomToken(t)
	ts := httptest.NewTLSServer(newEnrollHandler(ca, token, "sensor-enrolled-1"))
	t.Cleanup(ts.Close)

	// 리프 자체를 핀 — 콜론·대문자 표기도 허용되어야 한다.
	pin := sha256.Sum256(ts.Certificate().Raw)
	dir := t.TempDir()
	sensorID, err := Enroll(context.Background(), EnrollOptions{
		ServerURL:     ts.URL,
		Token:         token,
		CAFingerprint: withColons(strings.ToUpper(hex.EncodeToString(pin[:]))),
		StateDir:      dir,
	})
	if err != nil {
		t.Fatalf("enroll: %v", err)
	}
	if sensorID != "sensor-enrolled-1" {
		t.Fatalf("sensor id = %q", sensorID)
	}
	id, err := LoadIdentity(dir)
	if err != nil {
		t.Fatalf("load identity after enroll: %v", err)
	}
	if id.SensorID != "sensor-enrolled-1" {
		t.Fatalf("persisted sensor id = %q", id.SensorID)
	}
}

// TestEnrollWithCAPin은 체인 상위(CA)를 핀했을 때 리프가 그 CA로 검증되는 경로를 본다.
func TestEnrollWithCAPin(t *testing.T) {
	ca := newTestCA(t)
	token := randomToken(t)
	serverDER, serverKey := ca.issueServerCert(t)

	ts := httptest.NewUnstartedServer(newEnrollHandler(ca, token, "sensor-enrolled-2"))
	ts.TLS = &tls.Config{
		MinVersion:   tls.VersionTLS12,
		Certificates: []tls.Certificate{{Certificate: [][]byte{serverDER, ca.cert.Raw}, PrivateKey: serverKey}},
	}
	ts.StartTLS()
	t.Cleanup(ts.Close)

	pin := sha256.Sum256(ca.cert.Raw)
	sensorID, err := Enroll(context.Background(), EnrollOptions{
		ServerURL:     ts.URL,
		Token:         token,
		CAFingerprint: hex.EncodeToString(pin[:]),
		StateDir:      t.TempDir(),
	})
	if err != nil {
		t.Fatalf("enroll with ca pin: %v", err)
	}
	if sensorID != "sensor-enrolled-2" {
		t.Fatalf("sensor id = %q", sensorID)
	}
}

func TestEnrollRejectsWrongPin(t *testing.T) {
	ca := newTestCA(t)
	token := randomToken(t)
	ts := httptest.NewTLSServer(newEnrollHandler(ca, token, "sensor-x"))
	t.Cleanup(ts.Close)

	// 다른 CA의 지문 — 서버 신뢰가 성립하면 안 된다.
	other := newTestCA(t)
	pin := sha256.Sum256(other.cert.Raw)
	_, err := Enroll(context.Background(), EnrollOptions{
		ServerURL:     ts.URL,
		Token:         token,
		CAFingerprint: hex.EncodeToString(pin[:]),
		StateDir:      t.TempDir(),
	})
	if err == nil {
		t.Fatal("expected pin mismatch error")
	}
	if strings.Contains(err.Error(), token) {
		t.Fatal("error message leaks the enrollment token")
	}
}

func TestEnrollRejectedTokenDoesNotLeak(t *testing.T) {
	ca := newTestCA(t)
	ts := httptest.NewTLSServer(newEnrollHandler(ca, randomToken(t), "sensor-x"))
	t.Cleanup(ts.Close)

	pin := sha256.Sum256(ts.Certificate().Raw)
	wrongToken := randomToken(t)
	_, err := Enroll(context.Background(), EnrollOptions{
		ServerURL:     ts.URL,
		Token:         wrongToken,
		CAFingerprint: hex.EncodeToString(pin[:]),
		StateDir:      t.TempDir(),
	})
	if err == nil {
		t.Fatal("expected enrollment rejection")
	}
	if !strings.Contains(err.Error(), "403") {
		t.Fatalf("expected status in error, got %v", err)
	}
	if strings.Contains(err.Error(), wrongToken) {
		t.Fatal("error message leaks the enrollment token")
	}
}

func TestParseFingerprint(t *testing.T) {
	var want [sha256.Size]byte
	for i := range want {
		want[i] = byte(i)
	}
	canonical := hex.EncodeToString(want[:])

	for _, in := range []string{
		canonical,
		strings.ToUpper(canonical),
		withColons(canonical),
		"  " + canonical + "  ",
	} {
		got, err := parseFingerprint(in)
		if err != nil {
			t.Errorf("parseFingerprint(%q): %v", in, err)
			continue
		}
		if got != want {
			t.Errorf("parseFingerprint(%q) mismatch", in)
		}
	}

	for _, in := range []string{"", "abcd", canonical + "00", "zz" + canonical[2:]} {
		if _, err := parseFingerprint(in); err == nil {
			t.Errorf("parseFingerprint(%q): expected error", in)
		}
	}
}

// withColons는 hex 지문을 aa:bb:… 표기로 바꾼다.
func withColons(s string) string {
	var b strings.Builder
	for i := 0; i < len(s); i += 2 {
		if i > 0 {
			b.WriteByte(':')
		}
		b.WriteString(s[i : i+2])
	}
	return b.String()
}
