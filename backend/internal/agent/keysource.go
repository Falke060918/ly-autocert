package agent

import (
	"crypto/ed25519"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"syscall"
)

const identityEnv = "AGENT_IDENTITY_ENC_KEY"

var errKeySource = errors.New("agent: invalid identity key or public pin configuration")

func noSymlinks(path string) error {
	p, e := filepath.Abs(path)
	if e != nil {
		return e
	}
	for {
		st, e := os.Lstat(p)
		if e != nil && !os.IsNotExist(e) {
			return e
		}
		if e == nil && st.Mode()&os.ModeSymlink != 0 {
			return errKeySource
		}
		parent := filepath.Dir(p)
		if parent == p {
			break
		}
		p = parent
	}
	return nil
}
func owned(st os.FileInfo) bool {
	v, ok := st.Sys().(*syscall.Stat_t)
	if !ok {
		return false
	}
	uid := uint32(os.Geteuid())
	if runtime.GOOS == "linux" {
		uid = 0
	}
	return v.Uid == uid
}
func separate(path, dir string) bool {
	p, e := filepath.Abs(path)
	if e != nil {
		return false
	}
	d, e := filepath.Abs(dir)
	if e != nil {
		return false
	}
	parent := filepath.Dir(p)
	within := func(a, b string) bool {
		rel, e := filepath.Rel(a, b)
		return e == nil && (rel == "." || rel != ".." && !strings.HasPrefix(rel, ".."+string(os.PathSeparator)))
	}
	return !within(d, p) && !within(parent, d)
}
func secureConfig(path, dir string, secret bool) error {
	if !filepath.IsAbs(path) || !separate(path, dir) || noSymlinks(path) != nil || noSymlinks(dir) != nil {
		return errKeySource
	}
	st, e := os.Lstat(path)
	if e != nil || !st.Mode().IsRegular() || !owned(st) || st.Size() > 4096 || st.Mode().Perm()&0022 != 0 {
		return errKeySource
	}
	if secret && st.Mode().Perm() != 0600 {
		return errKeySource
	}
	parent, e := os.Stat(filepath.Dir(path))
	if e != nil || !parent.IsDir() || !owned(parent) || parent.Mode().Perm() != 0700 {
		return errKeySource
	}
	return nil
}

// InitIdentityKey is explicit, exclusive and silent; it never replaces a key.
func InitIdentityKey(path, dir string) error {
	if !filepath.IsAbs(path) || !separate(path, dir) || noSymlinks(path) != nil || noSymlinks(dir) != nil {
		return errKeySource
	}
	if e := os.MkdirAll(filepath.Dir(path), 0700); e != nil {
		return e
	}
	parent, e := os.Stat(filepath.Dir(path))
	if e != nil || !owned(parent) || parent.Mode().Perm() != 0700 {
		return errKeySource
	}
	key := make([]byte, 32)
	if _, e = rand.Read(key); e != nil {
		return e
	}
	defer clear(key)
	b := []byte(identityEnv + "=" + hex.EncodeToString(key) + "\n")
	defer clear(b)
	f, e := os.OpenFile(path, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
	if e != nil {
		return errKeySource
	}
	if _, e = f.Write(b); e == nil {
		e = f.Sync()
	}
	ce := f.Close()
	if e != nil {
		return e
	}
	if ce != nil {
		return ce
	}
	return syncDir(filepath.Dir(path))
}
func LoadIdentityKey(path, dir string) ([]byte, error) {
	env, present := os.LookupEnv(identityEnv)
	os.Unsetenv(identityEnv)
	if path != "" && present {
		return nil, errKeySource
	}
	raw := env
	if path != "" {
		if secureConfig(path, dir, true) != nil {
			return nil, errKeySource
		}
		b, e := os.ReadFile(path)
		if e != nil {
			return nil, errKeySource
		}
		defer clear(b)
		s := strings.TrimSuffix(string(b), "\n")
		if !strings.HasPrefix(s, identityEnv+"=") {
			return nil, errKeySource
		}
		raw = strings.TrimPrefix(s, identityEnv+"=")
	}
	if len(raw) != 64 {
		return nil, errKeySource
	}
	key, e := hex.DecodeString(raw)
	if e != nil || len(key) != 32 {
		clear(key)
		return nil, errKeySource
	}
	return key, nil
}
func LoadJobSigningPublicKey(path, dir string) (ed25519.PublicKey, error) {
	if secureConfig(path, dir, false) != nil {
		return nil, errKeySource
	}
	b, e := os.ReadFile(path)
	if e != nil {
		return nil, errKeySource
	}
	key, e := hex.DecodeString(strings.TrimSuffix(string(b), "\n"))
	if e != nil || len(key) != ed25519.PublicKeySize {
		return nil, errKeySource
	}
	return ed25519.PublicKey(key), nil
}
