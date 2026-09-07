package agent

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"encoding/json"
	"errors"
)

type sealedIdentity struct {
	Version    int    `json:"version"`
	Nonce      []byte `json:"nonce"`
	Ciphertext []byte `json:"ciphertext"`
}

func identityGCM(key []byte) (cipher.AEAD, error) {
	if len(key) != 32 {
		return nil, errKeySource
	}
	block, e := aes.NewCipher(key)
	if e != nil {
		return nil, e
	}
	return cipher.NewGCM(block)
}
func sealIdentity(key, der []byte, id string) ([]byte, error) {
	g, e := identityGCM(key)
	if e != nil {
		return nil, e
	}
	n := make([]byte, g.NonceSize())
	if _, e = rand.Read(n); e != nil {
		return nil, e
	}
	v := sealedIdentity{1, n, g.Seal(nil, n, der, []byte("autocert/agent-identity/v1\x00"+id))}
	return json.Marshal(v)
}
func openIdentity(key, b []byte, id string) ([]byte, error) {
	g, e := identityGCM(key)
	if e != nil {
		return nil, e
	}
	var v sealedIdentity
	if len(b) > 16384 || json.Unmarshal(b, &v) != nil || v.Version != 1 || len(v.Nonce) != g.NonceSize() {
		return nil, errors.New("agent: invalid sealed identity")
	}
	p, e := g.Open(nil, v.Nonce, v.Ciphertext, []byte("autocert/agent-identity/v1\x00"+id))
	if e != nil {
		return nil, errors.New("agent: cannot open identity")
	}
	return p, nil
}
