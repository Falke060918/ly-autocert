package deploysign

import (
	"crypto/ed25519"
	"crypto/rand"
	"encoding/json"
	"errors"
	"github.com/Falke060918/INHERIT_AutoCert/backend/internal/sensorca"
	"os"
	"path/filepath"
)

type sealedKey struct {
	Version    int    `json:"version"`
	Ciphertext []byte `json:"ciphertext"`
	Nonce      []byte `json:"nonce"`
}

// Init only creates a new sealed seed, using the existing server wrapping key.
func Init(path string, wrappingKey []byte) error {
	seed := make([]byte, ed25519.SeedSize)
	if _, e := rand.Read(seed); e != nil {
		return e
	}
	defer clear(seed)
	cipher, nonce, e := sensorca.Seal(wrappingKey, append([]byte("autocert/deploy-signing-key/v1\x00"), seed...))
	if e != nil {
		return e
	}
	b, e := json.Marshal(sealedKey{1, cipher, nonce})
	if e != nil {
		return e
	}
	f, e := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
	if e != nil {
		return errors.New("signing key already exists or cannot be created")
	}
	if _, e = f.Write(b); e == nil {
		e = f.Sync()
	}
	closeErr := f.Close()
	if e == nil {
		e = closeErr
	}
	if e != nil {
		return e
	}
	dir, e := os.Open(filepath.Dir(path))
	if e != nil {
		return e
	}
	defer dir.Close()
	return dir.Sync()
}
func Load(path string, wrappingKey []byte) (*Signer, error) {
	st, e := os.Lstat(path)
	if e != nil || !st.Mode().IsRegular() || st.Mode().Perm() != 0600 || st.Size() > 4096 {
		return nil, errors.New("invalid sealed signing key file")
	}
	b, e := os.ReadFile(path)
	if e != nil {
		return nil, e
	}
	var v sealedKey
	if json.Unmarshal(b, &v) != nil || v.Version != 1 {
		return nil, errInvalid
	}
	plain, e := sensorca.Open(wrappingKey, v.Ciphertext, v.Nonce)
	if e != nil {
		return nil, errInvalid
	}
	defer clear(plain)
	prefix := "autocert/deploy-signing-key/v1\x00"
	if len(plain) != len(prefix)+ed25519.SeedSize || string(plain[:len(prefix)]) != prefix {
		return nil, errInvalid
	}
	key := ed25519.NewKeyFromSeed(plain[len(prefix):])
	defer clear(key)
	return New(key), nil
}
