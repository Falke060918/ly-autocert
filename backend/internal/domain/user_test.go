package domain

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestRoleValid(t *testing.T) {
	if len(AllRoles) != 6 {
		t.Fatalf("AllRoles = %d roles, want 6 (00-spec 7.11)", len(AllRoles))
	}
	for _, r := range AllRoles {
		if !r.Valid() {
			t.Errorf("role %q reported invalid", r)
		}
	}
	for _, bad := range []Role{"", "admin", "SYSTEM_ADMINISTRATOR", "root"} {
		if bad.Valid() {
			t.Errorf("role %q reported valid; want invalid", bad)
		}
	}
}

// TestUserJSONExcludesPasswordHash는 User 직렬화에 해시가 절대 포함되지 않음을 검증한다.
func TestUserJSONExcludesPasswordHash(t *testing.T) {
	u := User{
		ID:           "00000000-0000-0000-0000-000000000000",
		Username:     "fixture-user",
		PasswordHash: "fixture-hash-must-not-appear", // 합성 값
		Role:         RoleViewer,
	}
	b, err := json.Marshal(u)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	if strings.Contains(string(b), "fixture-hash-must-not-appear") ||
		strings.Contains(string(b), "password") {
		t.Errorf("serialized user leaks password hash: %s", b)
	}
}
