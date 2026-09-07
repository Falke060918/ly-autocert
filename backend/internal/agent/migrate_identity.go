package agent

import (
	"bytes"
	"crypto/ecdsa"
	"crypto/x509"
	"encoding/pem"
	"errors"
	"os"
	"path/filepath"
	"strings"
)

// MigrateIdentity is explicit, under the same process lock as Run. Legacy PEM is
// removed only after the sealed replacement is durable and verified.
func MigrateIdentity(dir string, wrappingKey []byte) error {
	unlock, e := lockState(dir)
	if e != nil {
		return e
	}
	defer unlock()
	legacy := filepath.Join(dir, "key.pem")
	if _, e = os.Lstat(legacy); os.IsNotExist(e) {
		_, e = LoadIdentity(dir, wrappingKey)
		return e
	}
	raw, e := readStateFile(dir, "key.pem")
	if e != nil {
		return e
	}
	defer clear(raw)
	block, rest := pem.Decode(raw)
	if block == nil || block.Type != pemTypePrivateKey || len(bytes.TrimSpace(rest)) != 0 {
		return errors.New("agent: invalid legacy identity")
	}
	defer clear(block.Bytes)
	parsed, e := x509.ParsePKCS8PrivateKey(block.Bytes)
	if e != nil {
		return errors.New("agent: invalid legacy key")
	}
	key, ok := parsed.(*ecdsa.PrivateKey)
	if !ok {
		return errors.New("agent: invalid legacy key")
	}
	cert, e := readStateFile(dir, fileClientCert)
	if e != nil {
		return e
	}
	if _, e = clientCertForKey(cert, key); e != nil {
		return e
	}
	idBytes, e := readStateFile(dir, fileAgentID)
	if e != nil {
		return e
	}
	id := strings.TrimSpace(string(idBytes))
	if id == "" {
		return errors.New("agent: missing id")
	}
	ca, e := readStateFile(dir, fileCACert)
	if e != nil {
		return e
	}
	pool := x509.NewCertPool()
	if !pool.AppendCertsFromPEM(ca) {
		return errors.New("agent: invalid ca")
	}
	sealedPath := filepath.Join(dir, fileKey)
	if _, e = os.Lstat(sealedPath); os.IsNotExist(e) {
		sealed, e := sealIdentity(wrappingKey, block.Bytes, id)
		if e != nil {
			return e
		}
		if e = writeFileAtomic(sealedPath, sealed, 0600); e != nil {
			return e
		}
	}
	sealed, e := readStateFile(dir, fileKey)
	if e != nil {
		return e
	}
	plain, e := openIdentity(wrappingKey, sealed, id)
	if e != nil {
		return e
	}
	same := bytes.Equal(plain, block.Bytes)
	clear(plain)
	if !same {
		return errors.New("agent: sealed and legacy identities differ")
	}
	if e = syncDir(dir); e != nil {
		return e
	}
	if e = os.Remove(legacy); e != nil {
		return e
	}
	return syncDir(dir)
}
