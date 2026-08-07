package auth

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"fmt"
)

// tokenBytes는 세션 토큰 엔트로피(256비트)다.
const tokenBytes = 32

// NewSessionToken은 불투명 세션 토큰과 그 SHA-256 해시(소문자 hex 64자)를 만든다.
// 토큰 원문은 클라이언트에만 전달하고, DB에는 해시만 저장한다.
func NewSessionToken() (token, tokenHash string, err error) {
	b := make([]byte, tokenBytes)
	if _, err := rand.Read(b); err != nil {
		return "", "", fmt.Errorf("auth: generate session token: %w", err)
	}
	token = base64.RawURLEncoding.EncodeToString(b)
	return token, HashToken(token), nil
}

// HashToken은 토큰 원문의 SHA-256 해시를 소문자 hex로 반환한다.
// 조회 키 용도이므로 salt 없는 단순 해시로 충분하다(토큰 자체가 256비트 난수).
func HashToken(token string) string {
	sum := sha256.Sum256([]byte(token))
	return hex.EncodeToString(sum[:])
}
