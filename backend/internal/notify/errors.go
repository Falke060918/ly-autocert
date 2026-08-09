package notify

import (
	"errors"
	"fmt"
)

// TemporaryError는 재시도하면 성공할 수 있는 실패다 (네트워크 단절, 4xx SMTP,
// HTTP 408/429/5xx 등). 큐가 백오프 후 다시 시도한다.
type TemporaryError struct{ Err error }

func (e *TemporaryError) Error() string { return e.Err.Error() }
func (e *TemporaryError) Unwrap() error { return e.Err }

// PermanentError는 재시도해도 결과가 같은 실패다 (설정 오류, 주소 거부,
// 5xx SMTP, HTTP 4xx 등). 큐는 즉시 실패로 확정하고 더 시도하지 않는다.
type PermanentError struct{ Err error }

func (e *PermanentError) Error() string { return e.Err.Error() }
func (e *PermanentError) Unwrap() error { return e.Err }

// Temporary는 에러를 재시도 대상으로 표시한다. nil이면 nil을 반환한다.
func Temporary(err error) error {
	if err == nil {
		return nil
	}
	return &TemporaryError{Err: err}
}

// Permanent는 에러를 재시도 금지로 표시한다. nil이면 nil을 반환한다.
func Permanent(err error) error {
	if err == nil {
		return nil
	}
	return &PermanentError{Err: err}
}

// Temporaryf는 포맷 문자열로 재시도 대상 에러를 만든다.
func Temporaryf(format string, args ...any) error {
	return &TemporaryError{Err: fmt.Errorf(format, args...)}
}

// Permanentf는 포맷 문자열로 재시도 금지 에러를 만든다.
func Permanentf(format string, args ...any) error {
	return &PermanentError{Err: fmt.Errorf(format, args...)}
}

// IsTemporary는 에러가 재시도 대상인지 판정한다.
// 분류되지 않은 에러는 false다 — 원인을 모르는 실패를 무한 재시도해 큐를 막는 것보다
// 실패로 확정해 운영자에게 보이는 편이 낫다. 채널 어댑터는 모든 반환 에러를
// Temporary/Permanent 중 하나로 감싸는 것을 규칙으로 한다.
func IsTemporary(err error) bool {
	var t *TemporaryError
	return errors.As(err, &t)
}

// IsPermanent는 에러가 재시도 금지인지 판정한다.
func IsPermanent(err error) bool {
	var p *PermanentError
	return errors.As(err, &p)
}
