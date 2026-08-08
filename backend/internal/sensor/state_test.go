package sensor

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
)

func TestSaveEnrollmentLoadIdentityRoundtrip(t *testing.T) {
	ca := newTestCA(t)
	dir := filepath.Join(t.TempDir(), "state")
	id := newEnrolledIdentity(t, ca, dir, time.Now().Add(24*time.Hour))

	if id.SensorID != "sensor-test-1" {
		t.Fatalf("sensor id = %q, want sensor-test-1", id.SensorID)
	}
	if id.Leaf() == nil || id.Leaf().Subject.CommonName != "test-sensor" {
		t.Fatal("leaf certificate not restored")
	}
	if id.TLSConfig().RootCAs == nil {
		t.Fatal("ca pool not restored")
	}

	if runtime.GOOS != "windows" {
		info, err := os.Stat(dir)
		if err != nil {
			t.Fatalf("stat state dir: %v", err)
		}
		if perm := info.Mode().Perm(); perm != 0o700 {
			t.Fatalf("state dir perm = %o, want 700", perm)
		}
		for _, name := range []string{fileKey, fileClientCert, fileCACert, fileSensorID} {
			info, err := os.Stat(filepath.Join(dir, name))
			if err != nil {
				t.Fatalf("stat %s: %v", name, err)
			}
			if perm := info.Mode().Perm(); perm != 0o600 {
				t.Fatalf("%s perm = %o, want 600", name, perm)
			}
		}
	}
}

func TestSaveEnrollmentRequiresSensorID(t *testing.T) {
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatalf("generate key: %v", err)
	}
	if err := SaveEnrollment(t.TempDir(), "", key, nil, nil); err == nil {
		t.Fatal("expected error for empty sensor id")
	}
}

func TestLoadIdentityMissingState(t *testing.T) {
	_, err := LoadIdentity(filepath.Join(t.TempDir(), "nowhere"))
	if err == nil || !strings.Contains(err.Error(), "run 'enroll' first") {
		t.Fatalf("expected enroll-first guidance, got %v", err)
	}
}

func TestLoadIdentityRejectsForeignCert(t *testing.T) {
	ca := newTestCA(t)
	dir := t.TempDir()
	newEnrolledIdentity(t, ca, dir, time.Now().Add(24*time.Hour))

	// 다른 키의 인증서로 바꿔치기 — 키 불일치를 복원 시점에 잡아야 한다.
	otherKey, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatalf("generate key: %v", err)
	}
	foreign := ca.issueClientCertPEM(t, &otherKey.PublicKey, "foreign", time.Now().Add(24*time.Hour))
	if err := ReplaceClientCert(dir, foreign); err != nil {
		t.Fatalf("replace cert: %v", err)
	}
	if _, err := LoadIdentity(dir); err == nil || !strings.Contains(err.Error(), "does not match") {
		t.Fatalf("expected key mismatch error, got %v", err)
	}
}

func TestReplaceClientCertAtomicUpdate(t *testing.T) {
	ca := newTestCA(t)
	dir := t.TempDir()
	id := newEnrolledIdentity(t, ca, dir, time.Now().Add(24*time.Hour))

	newExpiry := time.Now().Add(48 * time.Hour)
	renewed := ca.issueClientCertPEM(t, id.Leaf().PublicKey.(*ecdsa.PublicKey), "test-sensor", newExpiry)
	if err := ReplaceClientCert(dir, renewed); err != nil {
		t.Fatalf("replace cert: %v", err)
	}

	reloaded, err := LoadIdentity(dir)
	if err != nil {
		t.Fatalf("reload identity: %v", err)
	}
	if !reloaded.Leaf().NotAfter.After(id.Leaf().NotAfter) {
		t.Fatal("replaced certificate not visible on reload")
	}
	if runtime.GOOS != "windows" {
		info, err := os.Stat(filepath.Join(dir, fileClientCert))
		if err != nil {
			t.Fatalf("stat client cert: %v", err)
		}
		if perm := info.Mode().Perm(); perm != 0o600 {
			t.Fatalf("client cert perm = %o, want 600", perm)
		}
	}
	if _, err := os.Stat(filepath.Join(dir, fileClientCert+".tmp")); !os.IsNotExist(err) {
		t.Fatal("temporary file left behind")
	}
}

func TestDefaultStateDir(t *testing.T) {
	got := DefaultStateDir()
	if got == "" {
		t.Fatal("default state dir is empty")
	}
	if runtime.GOOS != "windows" && got != "/var/lib/autocert-sensor" {
		t.Fatalf("default state dir = %q", got)
	}
}
