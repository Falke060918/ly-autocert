package auth

import (
	"errors"
	"strings"
	"testing"

	"golang.org/x/crypto/bcrypt"
)

// 테스트 비밀번호는 전부 합성 값이다 — 실제 시크릿 하드코딩 금지.

func TestValidatePassword(t *testing.T) {
	tests := []struct {
		name     string
		password string
		username string
		wantErr  error
	}{
		{"ok", "correct-horse-battery", "alice", nil},
		{"too short", "short-pw", "alice", ErrPasswordTooShort},
		{"exactly min length", strings.Repeat("a", MinPasswordLength), "alice", nil},
		{"too long (>72 bytes)", strings.Repeat("a", 73), "alice", ErrPasswordTooLong},
		{"equals username", "administrator-x1", "administrator-x1", ErrPasswordEqidentifier},
		{"equals username case-insensitive", "Administrator-X1", "administrator-x1", ErrPasswordEqidentifier},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := ValidatePassword(tt.password, tt.username)
			if !errors.Is(err, tt.wantErr) {
				t.Errorf("ValidatePassword() = %v, want %v", err, tt.wantErr)
			}
		})
	}
}

func TestHashAndVerifyPassword(t *testing.T) {
	const pw = "test-only-fixture-password" // 합성 값
	hash, err := HashPassword(pw, bcrypt.MinCost)
	if err != nil {
		t.Fatalf("HashPassword: %v", err)
	}
	if hash == pw || strings.Contains(hash, pw) {
		t.Fatal("hash must not contain the plaintext password")
	}
	if !VerifyPassword(hash, pw) {
		t.Error("VerifyPassword: correct password rejected")
	}
	if VerifyPassword(hash, pw+"x") {
		t.Error("VerifyPassword: wrong password accepted")
	}
}
