package agentgw

import (
	"context"
	"crypto/ecdsa"
	"crypto/ed25519"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"

	"github.com/Falke060918/INHERIT_AutoCert/backend/internal/agent"
	"github.com/Falke060918/INHERIT_AutoCert/backend/internal/db"
	"github.com/Falke060918/INHERIT_AutoCert/backend/internal/sensorca"
	"github.com/Falke060918/INHERIT_AutoCert/backend/internal/store"
)

func TestEnrollMTLSAndSensorIsolation(t *testing.T) {
	dsn := os.Getenv("TEST_DATABASE_URL")
	if dsn == "" {
		t.Skip("TEST_DATABASE_URL not set")
	}
	ctx := context.Background()
	pool, e := db.Connect(ctx, dsn)
	if e != nil {
		t.Fatal(e)
	}
	defer pool.Close()
	if e = db.Migrate(pool); e != nil {
		t.Fatal(e)
	}
	st := store.New(pool)
	ca, e := sensorca.New("Agent test CA")
	if e != nil {
		t.Fatal(e)
	}
	key := make([]byte, 32)
	rand.Read(key)
	g := newGateway(st, ca, key)
	serverPEM, der, e := ca.IssueServerCert("localhost", []string{"localhost"}, []net.IP{net.ParseIP("127.0.0.1")})
	if e != nil {
		t.Fatal(e)
	}
	kp := pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: der})
	pair, e := tls.X509KeyPair(append(serverPEM, ca.CertPEM()...), kp)
	if e != nil {
		t.Fatal(e)
	}
	roots := x509.NewCertPool()
	roots.AppendCertsFromPEM(ca.CertPEM())
	srv := httptest.NewUnstartedServer(g.Handler())
	srv.TLS = &tls.Config{Certificates: []tls.Certificate{pair}, ClientAuth: tls.VerifyClientCertIfGiven, ClientCAs: roots, MinVersion: tls.VersionTLS12}
	srv.StartTLS()
	defer srv.Close()
	ag, token, e := st.CreateAgent(ctx, store.CreateAgentInput{Name: t.Name()})
	if e != nil {
		t.Fatal(e)
	}
	defer pool.Exec(`DELETE FROM agents WHERE id=$1::uuid`, ag.ID)
	dir := t.TempDir()
	pub, _, _ := ed25519.GenerateKey(rand.Reader)
	id, e := agent.Enroll(ctx, agent.EnrollOptions{ServerURL: srv.URL, StateDir: dir, Token: token, CAFingerprint: certFP(ca.CertPEM()), IdentityKey: key, JobSigningPublicKey: pub})
	if e != nil || id != ag.ID {
		t.Fatal("agent enrollment", e)
	}
	identity, e := agent.LoadIdentity(dir, key)
	if e != nil {
		t.Fatal(e)
	}
	hc := &http.Client{Transport: &http.Transport{TLSClientConfig: identity.TLSConfig()}}
	call := func(client *http.Client, want int) {
		t.Helper()
		req, _ := http.NewRequest("POST", srv.URL+"/agent/v1/heartbeat", nil)
		res, e := client.Do(req)
		if e != nil {
			t.Fatal(e)
		}
		defer res.Body.Close()
		if res.StatusCode != want {
			t.Fatalf("status=%d want=%d", res.StatusCode, want)
		}
		if res.Header.Get("Cache-Control") != "no-store" {
			t.Fatal("missing no-store")
		}
	}
	call(hc, 204)
	call(&http.Client{Transport: &http.Transport{TLSClientConfig: &tls.Config{RootCAs: roots, MinVersion: tls.VersionTLS12}}}, 401)
	sensorKey, e := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if e != nil {
		t.Fatal(e)
	}
	csr, e := x509.CreateCertificateRequest(rand.Reader, &x509.CertificateRequest{Subject: pkix.Name{CommonName: "sensor-only"}}, sensorKey)
	if e != nil {
		t.Fatal(e)
	}
	cert, e := ca.SignClientCSR(pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE REQUEST", Bytes: csr}))
	if e != nil {
		t.Fatal(e)
	}
	sensor, tok, e := st.CreateSensor(ctx, store.CreateSensorInput{Name: t.Name()})
	if e != nil {
		t.Fatal(e)
	}
	defer pool.Exec(`DELETE FROM sensors WHERE id=$1::uuid`, sensor.ID)
	if _, e = st.EnrollSensor(ctx, store.EnrollSensorInput{Token: tok, ClientCertFingerprintSHA256: certFP(cert)}); e != nil {
		t.Fatal(e)
	}
	keyDER, _ := x509.MarshalPKCS8PrivateKey(sensorKey)
	clientPair, e := tls.X509KeyPair(cert, pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: keyDER}))
	if e != nil {
		t.Fatal(e)
	}
	call(&http.Client{Transport: &http.Transport{TLSClientConfig: &tls.Config{RootCAs: roots, Certificates: []tls.Certificate{clientPair}, MinVersion: tls.VersionTLS12}}}, 401)
	if _, e = st.DisableAgent(ctx, store.DisableAgentInput{AgentID: ag.ID}); e != nil {
		t.Fatal(e)
	}
	call(hc, 401)
}
