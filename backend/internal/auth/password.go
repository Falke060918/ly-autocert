// Package auth는 로컬 계정 인증(비밀번호 해시·세션 토큰·로그인)을 담당한다.
// 암호 연산은 검증된 표준 라이브러리(golang.org/x/crypto/bcrypt, crypto/rand,
// crypto/sha256)만 사용한다 — 자체 암호 구현 금지 (CLAUDE.md 보안 하드 규칙 2).
package auth

import (
	"errors"
	"strings"

	"golang.org/x/crypto/bcrypt"
)

// 비밀번호 정책 (docs/01-plan.md MVP 1 "비밀번호 정책").
// 길이 기반 정책만 강제한다 — 구성 규칙(대소문자·특수문자 강제)은
// NIST SP 800-63B가 권장하지 않으므로 두지 않는다.
const (
	// MinPasswordLength는 최소 길이(문자 수)다.
	MinPasswordLength = 12
	// maxPasswordBytes는 bcrypt 입력 상한(72바이트)이다.
	maxPasswordBytes = 72
)

// bcryptCost는 운영 기본 비용이다. 테스트는 Service 생성 시 bcrypt.MinCost로 낮춘다.
const bcryptCost = 12

var (
	ErrPasswordTooShort     = errors.New("auth: password shorter than minimum length")
	ErrPasswordTooLong      = errors.New("auth: password exceeds 72 bytes (bcrypt limit)")
	ErrPasswordEqidentifier = errors.New("auth: password must differ from username")
)

// ValidatePassword는 비밀번호 정책을 검사한다. 비밀번호 값 자체는
// 에러 메시지·로그 어디에도 포함하지 않는다.
func ValidatePassword(password, username string) error {
	if len([]rune(password)) < MinPasswordLength {
		return ErrPasswordTooShort
	}
	if len(password) > maxPasswordBytes {
		return ErrPasswordTooLong
	}
	if strings.EqualFold(password, username) {
		return ErrPasswordEqidentifier
	}
	return nil
}

// HashPassword는 bcrypt 해시를 만든다.
func HashPassword(password string, cost int) (string, error) {
	h, err := bcrypt.GenerateFromPassword([]byte(password), cost)
	if err != nil {
		return "", err
	}
	return string(h), nil
}

// VerifyPassword는 평문과 해시를 비교한다. 일치하면 true.
func VerifyPassword(hash, password string) bool {
	return bcrypt.CompareHashAndPassword([]byte(hash), []byte(password)) == nil
}
