package agent

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"crypto/sha256"
	"crypto/tls"
	"crypto/x509"
	"encoding/hex"
	"encoding/json"
	"encoding/pem"
	"github.com/Falke060918/INHERIT_AutoCert/backend/internal/agentwire"
	"github.com/Falke060918/INHERIT_AutoCert/backend/internal/deploysign"
	"github.com/Falke060918/INHERIT_AutoCert/backend/internal/sensorca"
	"net"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"
)

func TestRunUntrustedLeaseNeverStartsOrReleasesMaterial(t *testing.T) {
	ca, e := sensorca.New("generated test CA")
	if e != nil {
		t.Fatal(e)
	}
	cert, der, e := ca.IssueServerCert("localhost", []string{"localhost"}, []net.IP{net.ParseIP("127.0.0.1")})
	if e != nil {
		t.Fatal(e)
	}
	pair, e := tls.X509KeyPair(append(cert, ca.CertPEM()...), pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: der}))
	if e != nil {
		t.Fatal(e)
	}
	pub, _, _ := ed25519.GenerateKey(rand.Reader)
	_, foreign, _ := ed25519.GenerateKey(rand.Reader)
	key := make([]byte, 32)
	rand.Read(key)
	const id = "12345678-1234-1234-1234-123456789abc"
	var starts, materials, leases atomic.Int32
	srv := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case pathEnroll:
			var req agentwire.EnrollRequest
			json.NewDecoder(r.Body).Decode(&req)
			c, e := ca.SignClientCSR([]byte(req.CSRPEM))
			if e != nil {
				w.WriteHeader(400)
				return
			}
			json.NewEncoder(w).Encode(agentwire.EnrollResponse{AgentID: id, ClientCertPEM: string(c), CACertPEM: string(ca.CertPEM())})
		case "/agent/v1/heartbeat":
			w.WriteHeader(204)
		case pathLease:
			leases.Add(1)
			l := agentwire.Lease{JobID: "12345678-1234-1234-1234-123456789def", Kind: "deploy", Target: agentwire.Target{AgentID: id, Kind: "nginx", ServiceUnit: "nginx", CertPath: "/etc/nginx/a.crt", KeyPath: "/etc/nginx/a.key", VerifyHost: "127.0.0.1", VerifyPort: 443, Active: true}, ExpectedFingerprint: hex.EncodeToString(make([]byte, 32)), LeaseToken: "12345678-1234-1234-1234-123456789fed", ExpiresAt: time.Now().Add(90 * time.Second)}
			env, e := deploysign.New(foreign).Sign(l, time.Now())
			if e != nil {
				w.WriteHeader(500)
				return
			}
			json.NewEncoder(w).Encode(env)
		default:
			if r.Method == "GET" {
				materials.Add(1)
			} else {
				starts.Add(1)
			}
			w.WriteHeader(500)
		}
	}))
	roots := x509.NewCertPool()
	roots.AppendCertsFromPEM(ca.CertPEM())
	srv.TLS = &tls.Config{Certificates: []tls.Certificate{pair}, ClientAuth: tls.VerifyClientCertIfGiven, ClientCAs: roots, MinVersion: tls.VersionTLS12}
	srv.StartTLS()
	defer srv.Close()
	block, _ := pem.Decode(ca.CertPEM())
	fp := sha256.Sum256(block.Bytes)
	dir := t.TempDir()
	token := make([]byte, 32)
	rand.Read(token)
	if _, e = Enroll(context.Background(), EnrollOptions{ServerURL: srv.URL, StateDir: dir, CAFingerprint: hex.EncodeToString(fp[:]), Token: hex.EncodeToString(token), IdentityKey: key, JobSigningPublicKey: pub}); e != nil {
		t.Fatal(e)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	if e = Run(ctx, srv.URL, dir, []string{"/etc/nginx"}, key, pub); e == nil {
		t.Fatal("untrusted job accepted")
	}
	if leases.Load() != 1 || starts.Load() != 0 || materials.Load() != 0 {
		t.Fatalf("calls lease=%d start=%d material=%d", leases.Load(), starts.Load(), materials.Load())
	}
}
