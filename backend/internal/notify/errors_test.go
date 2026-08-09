package notify

import (
	"errors"
	"fmt"
	"testing"
)

func TestTemporaryAndPermanentClassification(t *testing.T) {
	base := errors.New("boom")

	if !IsTemporary(Temporary(base)) {
		t.Error("Temporary(err) must be classified as temporary")
	}
	if IsPermanent(Temporary(base)) {
		t.Error("Temporary(err) must not be classified as permanent")
	}
	if !IsPermanent(Permanent(base)) {
		t.Error("Permanent(err) must be classified as permanent")
	}
	if IsTemporary(Permanent(base)) {
		t.Error("Permanent(err) must not be classified as temporary")
	}
	// 분류되지 않은 에러는 재시도 대상이 아니다 (큐를 무한 재시도로 막지 않는다).
	if IsTemporary(base) || IsPermanent(base) {
		t.Error("unclassified error must be neither temporary nor permanent")
	}
}

func TestTemporaryPreservesWrappedError(t *testing.T) {
	sentinel := errors.New("sentinel")
	err := Temporary(fmt.Errorf("context: %w", sentinel))
	if !errors.Is(err, sentinel) {
		t.Error("wrapped sentinel must remain discoverable with errors.Is")
	}
	// 여러 겹 감싸도 판정이 유지된다.
	if !IsTemporary(fmt.Errorf("outer: %w", err)) {
		t.Error("temporary classification must survive further wrapping")
	}
}

func TestTemporaryAndPermanentPassThroughNil(t *testing.T) {
	if Temporary(nil) != nil {
		t.Error("Temporary(nil) must be nil")
	}
	if Permanent(nil) != nil {
		t.Error("Permanent(nil) must be nil")
	}
}

func TestFormattedConstructors(t *testing.T) {
	err := Temporaryf("code %d", 451)
	if !IsTemporary(err) || err.Error() != "code 451" {
		t.Errorf("Temporaryf mismatch: %v", err)
	}
	err = Permanentf("code %d", 550)
	if !IsPermanent(err) || err.Error() != "code 550" {
		t.Errorf("Permanentf mismatch: %v", err)
	}
}
