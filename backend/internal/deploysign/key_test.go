package deploysign

import (
	"bytes"
	"crypto/rand"
	"os"
	"path/filepath"
	"testing"
)

func TestSealedSignerRestartAndNoOverwrite(t *testing.T) {
	key := make([]byte, 32)
	rand.Read(key)
	path := filepath.Join(t.TempDir(), "signer.sealed")
	if e := Init(path, key); e != nil {
		t.Fatal(e)
	}
	s, e := Load(path, key)
	if e != nil {
		t.Fatal(e)
	}
	b, _ := os.ReadFile(path)
	if bytes.Contains(b, s.key.Seed()) {
		t.Fatal("seed stored plaintext")
	}
	if e = Init(path, key); e == nil {
		t.Fatal("overwrote signer")
	}
	other, e := Load(path, key)
	if e != nil || !bytes.Equal(s.Public(), other.Public()) {
		t.Fatal("signer changed on restart")
	}
	wrong := make([]byte, 32)
	rand.Read(wrong)
	if _, e = Load(path, wrong); e == nil {
		t.Fatal("wrong key opened signer")
	}
	b[len(b)/2] ^= 1
	os.WriteFile(path, b, 0600)
	if _, e = Load(path, key); e == nil {
		t.Fatal("tampered signer opened")
	}
}
