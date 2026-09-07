package agent

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/sha256"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/hex"
	"encoding/pem"
	"errors"
	"github.com/Falke060918/INHERIT_AutoCert/backend/internal/agentwire"
	"math/big"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func testMaterial(t *testing.T) (agentwire.Material, string) {
	t.Helper()
	key, e := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if e != nil {
		t.Fatal(e)
	}
	serial, e := rand.Int(rand.Reader, new(big.Int).Lsh(big.NewInt(1), 128))
	if e != nil {
		t.Fatal(e)
	}
	tpl := &x509.Certificate{SerialNumber: serial, Subject: pkix.Name{CommonName: "app.example.test"}, DNSNames: []string{"app.example.test"}, NotBefore: time.Now().Add(-time.Hour), NotAfter: time.Now().Add(24 * time.Hour), KeyUsage: x509.KeyUsageDigitalSignature}
	der, e := x509.CreateCertificate(rand.Reader, tpl, tpl, &key.PublicKey, key)
	if e != nil {
		t.Fatal(e)
	}
	kd, e := x509.MarshalPKCS8PrivateKey(key)
	if e != nil {
		t.Fatal(e)
	}
	sum := sha256.Sum256(der)
	return agentwire.Material{CertPEM: string(pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der})), KeyPEM: string(pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: kd}))}, hex.EncodeToString(sum[:])
}

type fakeExecutor struct {
	file                               string
	old, new                           string
	configCalls, reloads, probes       int
	failConfig, failReload, failVerify bool
}

func (f *fakeExecutor) ConfigTest(context.Context, agentwire.Target) error {
	f.configCalls++
	if f.failConfig && f.configCalls == 2 {
		return errors.New("fake config failure")
	}
	return nil
}
func (f *fakeExecutor) Reload(context.Context, agentwire.Target) error {
	f.reloads++
	if f.failReload && f.reloads == 1 {
		return errors.New("fake reload failure")
	}
	return nil
}
func (f *fakeExecutor) Fingerprint(context.Context, agentwire.Target) (string, error) {
	f.probes++
	if f.failVerify && f.probes == 2 {
		return f.old, nil
	}
	b, e := os.ReadFile(f.file)
	if e != nil {
		return "", e
	}
	p, _ := pem.Decode(b)
	if p == nil {
		return "", errors.New("bad cert")
	}
	sum := sha256.Sum256(p.Bytes)
	return hex.EncodeToString(sum[:]), nil
}
func TestDeploymentSuccessRollbackAndIsolation(t *testing.T) {
	for _, failure := range []string{"none", "config", "reload", "verify"} {
		t.Run(failure, func(t *testing.T) {
			dir := t.TempDir()
			old, oldFP := testMaterial(t)
			fresh, fp := testMaterial(t)
			cp, kp := filepath.Join(dir, "server.crt"), filepath.Join(dir, "server.key")
			if e := os.WriteFile(cp, []byte(old.CertPEM), 0644); e != nil {
				t.Fatal(e)
			}
			if e := os.WriteFile(kp, []byte(old.KeyPEM), 0600); e != nil {
				t.Fatal(e)
			}
			ex := &fakeExecutor{file: cp, old: oldFP, new: fp, failConfig: failure == "config", failReload: failure == "reload", failVerify: failure == "verify"}
			l := agentwire.Lease{Kind: "deploy", ExpectedFingerprint: fp, Target: agentwire.Target{Kind: "nginx", ServiceUnit: "nginx", CertPath: cp, KeyPath: kp, VerifyHost: "127.0.0.1", VerifyPort: 443}}
			r := (Deployer{Executor: ex, AllowedDirs: []string{dir}}).Deploy(context.Background(), l, fresh)
			want := "completed"
			wantCert := fresh.CertPEM
			if failure != "none" {
				want = "rolled_back"
				wantCert = old.CertPEM
			}
			if r.Status != want {
				t.Fatalf("status=%s steps=%v", r.Status, r.Steps)
			}
			b, _ := os.ReadFile(cp)
			if string(b) != wantCert {
				t.Fatal("wrong certificate after operation")
			}
			st, _ := os.Stat(kp)
			if st.Mode().Perm() != 0600 {
				t.Fatal("key permissions")
			}
			entries, _ := os.ReadDir(dir)
			if len(entries) != 2 {
				t.Fatal("temporary or backup material left behind")
			}
			if failure == "none" {
				r = (Deployer{Executor: ex, AllowedDirs: []string{dir}}).Deploy(context.Background(), l, fresh)
				if r.Status != "completed" || ex.reloads != 1 {
					t.Fatal("idempotent deployment reloaded service")
				}
			}
		})
	}
}
func TestDeploymentRejectsUnsafeInputs(t *testing.T) {
	dir := t.TempDir()
	old, _ := testMaterial(t)
	fresh, fp := testMaterial(t)
	cp, kp := filepath.Join(dir, "c.pem"), filepath.Join(dir, "k.pem")
	os.WriteFile(cp, []byte(old.CertPEM), 0644)
	os.WriteFile(kp, []byte(old.KeyPEM), 0600)
	base := agentwire.Lease{Kind: "deploy", ExpectedFingerprint: fp, Target: agentwire.Target{Kind: "nginx", ServiceUnit: "nginx", CertPath: cp, KeyPath: kp, VerifyHost: "127.0.0.1", VerifyPort: 443}}
	for _, kind := range []string{"command", "outside", "symlink", "mismatched_key"} {
		t.Run(kind, func(t *testing.T) {
			l := base
			m := fresh
			allowed := []string{dir}
			if kind == "command" {
				l.Target.ServiceUnit = "nginx;touch /tmp/bad"
			}
			if kind == "outside" {
				allowed = []string{filepath.Join(dir, "other")}
			}
			if kind == "symlink" {
				link := filepath.Join(dir, "link.pem")
				os.Symlink(cp, link)
				l.Target.CertPath = link
			}
			if kind == "mismatched_key" {
				m.KeyPEM = old.KeyPEM
			}
			ex := &fakeExecutor{file: cp}
			r := (Deployer{Executor: ex, AllowedDirs: allowed}).Deploy(context.Background(), l, m)
			if r.Status != "failed" || ex.reloads != 0 {
				t.Fatal("unsafe deployment was executed")
			}
			b, _ := os.ReadFile(cp)
			if string(b) != old.CertPEM {
				t.Fatal("precheck modified files")
			}
		})
	}
}
