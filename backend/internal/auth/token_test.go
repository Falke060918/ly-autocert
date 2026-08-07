package auth

import (
	"regexp"
	"testing"
)

var hexHash = regexp.MustCompile(`^[0-9a-f]{64}$`)

func TestNewSessionToken(t *testing.T) {
	token1, hash1, err := NewSessionToken()
	if err != nil {
		t.Fatalf("NewSessionToken: %v", err)
	}
	token2, hash2, err := NewSessionToken()
	if err != nil {
		t.Fatalf("NewSessionToken: %v", err)
	}
	if token1 == token2 {
		t.Error("two tokens are identical; want unique random tokens")
	}
	if !hexHash.MatchString(hash1) || !hexHash.MatchString(hash2) {
		t.Errorf("token hash must be 64 lowercase hex chars (DB CHECK): %q", hash1)
	}
	if hash1 != HashToken(token1) {
		t.Error("returned hash does not match HashToken(token)")
	}
	if hash1 == hash2 {
		t.Error("distinct tokens produced identical hashes")
	}
}
