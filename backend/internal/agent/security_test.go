package agent

import (
	"bytes"
	"crypto/ecdsa"
	"crypto/ed25519"
	"crypto/rand"
	"crypto/sha256"
	"crypto/x509"
	"encoding/hex"
	"encoding/json"
	"encoding/pem"
	"github.com/Falke060918/INHERIT_AutoCert/backend/internal/agentwire"
	"github.com/Falke060918/INHERIT_AutoCert/backend/internal/deploysign"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func identityFixture(t *testing.T) (string, string, []byte, []byte) {
	t.Helper()
	root, e := filepath.EvalSymlinks(t.TempDir())
	if e != nil {
		t.Fatal(e)
	}
	dir := filepath.Join(root, "state")
	file := filepath.Join(root, "config", "identity.env")
	if e = InitIdentityKey(file, dir); e != nil {
		t.Fatal(e)
	}
	key, e := LoadIdentityKey(file, dir)
	if e != nil {
		t.Fatal(e)
	}
	m, _ := testMaterial(t)
	b, _ := pem.Decode([]byte(m.KeyPEM))
	parsed, e := x509.ParsePKCS8PrivateKey(b.Bytes)
	if e != nil {
		t.Fatal(e)
	}
	if e = SaveEnrollment(dir, "12345678-1234-1234-1234-123456789abc", parsed.(*ecdsa.PrivateKey), []byte(m.CertPEM), []byte(m.CertPEM), key); e != nil {
		t.Fatal(e)
	}
	return dir, file, key, []byte(m.KeyPEM)
}
func TestIdentitySealedMigrationAndRestart(t *testing.T) {
	dir, file, key, legacy := identityFixture(t)
	for _, name := range []string{fileKey, fileClientCert, fileCACert, fileAgentID} {
		b, e := os.ReadFile(filepath.Join(dir, name))
		if e != nil {
			t.Fatal(e)
		}
		if bytes.Contains(b, []byte("PRIVATE KEY")) || bytes.Contains(b, []byte(hex.EncodeToString(key))) {
			t.Fatal("plaintext key in state")
		}
	}
	id, e := LoadIdentity(dir, key)
	if e != nil {
		t.Fatal(e)
	}
	wrong := make([]byte, 32)
	rand.Read(wrong)
	if _, e = LoadIdentity(dir, wrong); e == nil {
		t.Fatal("wrong key opened identity")
	}
	// Model a power loss after the sealed rename and before legacy unlink.
	if e = os.WriteFile(filepath.Join(dir, "key.pem"), legacy, 0600); e != nil {
		t.Fatal(e)
	}
	if _, e = LoadIdentity(dir, key); e == nil {
		t.Fatal("run accepted unfinished migration")
	}
	if e = MigrateIdentity(dir, wrong); e == nil {
		t.Fatal("migration accepted wrong key")
	}
	if b, _ := os.ReadFile(filepath.Join(dir, "key.pem")); !bytes.Equal(b, legacy) {
		t.Fatal("failed migration lost original")
	}
	if e = MigrateIdentity(dir, key); e != nil {
		t.Fatal(e)
	}
	if e = MigrateIdentity(dir, key); e != nil {
		t.Fatal("migration retry", e)
	}
	after, e := LoadIdentity(dir, key)
	if e != nil || after.AgentID != id.AgentID || !bytes.Equal(after.Leaf().Raw, id.Leaf().Raw) {
		t.Fatal("identity changed", e)
	}
	// Also exercise the initial legacy-only migration, which must create a sealed file.
	if e = os.WriteFile(filepath.Join(dir, "key.pem"), legacy, 0600); e != nil {
		t.Fatal(e)
	}
	os.Remove(filepath.Join(dir, fileKey))
	if e = MigrateIdentity(dir, key); e != nil {
		t.Fatal(e)
	}
	pub, signKey, _ := ed25519.GenerateKey(rand.Reader)
	pin := filepath.Join(filepath.Dir(file), "job-signing.pub")
	os.WriteFile(pin, []byte(hex.EncodeToString(pub)+"\n"), 0600)
	l := agentwire.Lease{JobID: "12345678-1234-1234-1234-123456789def", Kind: "deploy", Target: agentwire.Target{AgentID: id.AgentID, Kind: "nginx", ServiceUnit: "nginx", CertPath: "/etc/nginx/a.crt", KeyPath: "/etc/nginx/a.key", VerifyHost: "127.0.0.1", VerifyPort: 443, Active: true}, ExpectedFingerprint: strings.Repeat("a", 64), LeaseToken: "12345678-1234-1234-1234-123456789fed", ExpiresAt: time.Now().Add(90 * time.Second)}
	signed, e := deploysign.New(signKey).Sign(l, time.Now())
	if e != nil {
		t.Fatal(e)
	}
	b, _ := json.Marshal(signed)
	job := filepath.Join(filepath.Dir(dir), "job.json")
	os.WriteFile(job, b, 0600)
	exe, _ := os.Executable()
	sum := sha256.Sum256(id.Leaf().Raw)
	for _, mode := range []string{"accept", "replay"} {
		cmd := exec.Command(exe, "-test.run=^TestIdentityProcessHelper$")
		cmd.Env = append(os.Environ(), "ATC_IDENTITY_HELPER="+mode, "ATC_IDENTITY_STATE="+dir, "ATC_IDENTITY_FILE="+file, "ATC_IDENTITY_PIN="+pin, "ATC_IDENTITY_JOB="+job)
		out, e := cmd.CombinedOutput()
		if e != nil || strings.TrimSpace(string(out)) != hex.EncodeToString(sum[:]) {
			t.Fatalf("child process %s identity/lease failed: %v %s", mode, e, out)
		}
	}
}
func TestIdentityProcessHelper(t *testing.T) {
	mode := os.Getenv("ATC_IDENTITY_HELPER")
	if mode == "" {
		return
	}
	dir := os.Getenv("ATC_IDENTITY_STATE")
	key, e := LoadIdentityKey(os.Getenv("ATC_IDENTITY_FILE"), dir)
	if e != nil {
		os.Exit(10)
	}
	id, e := LoadIdentity(dir, key)
	if e != nil {
		os.Exit(11)
	}
	pub, e := LoadJobSigningPublicKey(os.Getenv("ATC_IDENTITY_PIN"), dir)
	if e != nil {
		os.Exit(12)
	}
	unlock, e := lockState(dir)
	if e != nil {
		os.Exit(13)
	}
	b, _ := os.ReadFile(os.Getenv("ATC_IDENTITY_JOB"))
	var signed agentwire.SignedLease
	json.Unmarshal(b, &signed)
	_, e = acceptLease(dir, pub, signed, id.AgentID, time.Now())
	unlock()
	if mode == "accept" && e != nil || mode == "replay" && e == nil {
		os.Exit(14)
	}
	sum := sha256.Sum256(id.Leaf().Raw)
	os.Stdout.WriteString(hex.EncodeToString(sum[:]) + "\n")
	os.Exit(0)
}
func TestIdentitySourceAndJournalRefusals(t *testing.T) {
	dir, file, key, _ := identityFixture(t)
	before, _ := os.ReadFile(file)
	if e := InitIdentityKey(file, dir); e == nil {
		t.Fatal("key overwritten")
	}
	after, _ := os.ReadFile(file)
	if !bytes.Equal(before, after) {
		t.Fatal("key changed")
	}
	if e := InitIdentityKey(filepath.Join(dir, "identity.env"), dir); e == nil {
		t.Fatal("key in state accepted")
	}
	os.Setenv(identityEnv, hex.EncodeToString(key))
	if _, e := LoadIdentityKey(file, dir); e == nil {
		t.Fatal("two sources accepted")
	}
	if _, ok := os.LookupEnv(identityEnv); ok {
		t.Fatal("environment retained secret")
	}
	os.Setenv(identityEnv, hex.EncodeToString(key))
	v, e := LoadIdentityKey("", dir)
	if e != nil || !bytes.Equal(v, key) {
		t.Fatal("environment key not loaded")
	}
	if os.Getenv(identityEnv) != "" {
		t.Fatal("environment secret retained")
	}
	link := filepath.Join(filepath.Dir(file), "symlink.env")
	os.Symlink(file, link)
	if _, e = LoadIdentityKey(link, dir); e == nil {
		t.Fatal("symlink accepted")
	}
	os.Chmod(file, 0644)
	if _, e = LoadIdentityKey(file, dir); e == nil {
		t.Fatal("weak permissions accepted")
	}
	os.Chmod(file, 0600)
	unlock, e := lockState(dir)
	if e != nil {
		t.Fatal(e)
	}
	if release, e := lockState(dir); e == nil {
		release()
		t.Fatal("concurrent process lock acquired")
	}
	unlock()
	os.WriteFile(filepath.Join(dir, "accepted-jobs.json"), []byte("broken"), 0600)
	if e = consumeLease(dir, agentwire.Lease{}); e == nil {
		t.Fatal("corrupt journal accepted")
	}
}
