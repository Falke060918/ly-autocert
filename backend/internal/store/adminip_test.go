package store

// 접근 IP 허용 목록 store 통합 테스트 (TEST_DATABASE_URL 필요 — 미설정 시 스킵).
// 잠금 방지 판정이 변경과 같은 트랜잭션에서 일어나는지(= 거부 시 행이 남지 않는지)가 핵심이다.

import (
	"context"
	"database/sql"
	"errors"
	"testing"
)

func cleanupAdminIPRules(pool *sql.DB) {
	_, _ = pool.Exec(`DELETE FROM admin_ip_rules`)
}

func newAdminIPStore(t *testing.T) (*Store, *sql.DB) {
	t.Helper()
	pool := testPool(t)
	cleanupAdminIPRules(pool)
	t.Cleanup(func() { cleanupAdminIPRules(pool) })
	return New(pool), pool
}

func countAdminIPRules(t *testing.T, pool *sql.DB) int {
	t.Helper()
	var n int
	if err := pool.QueryRow(`SELECT count(*) FROM admin_ip_rules`).Scan(&n); err != nil {
		t.Fatalf("count admin ip rules: %v", err)
	}
	return n
}

// 첫 규칙이 요청자를 덮지 않으면 거부하고, 행도 감사도 남기지 않는다.
func TestCreateAdminIPRuleRollsBackSelfLockout(t *testing.T) {
	st, pool := newAdminIPStore(t)
	ctx := context.Background()
	reqID := "adminip-store-lockout"
	t.Cleanup(func() { cleanupAuditByRequestID(pool, reqID) })

	_, err := st.CreateAdminIPRule(ctx, CreateAdminIPRuleInput{
		CIDR:                "10.9.9.0/24",
		IsActive:            true,
		AdminIPRuleMutation: AdminIPRuleMutation{ActorIP: ptrString("10.1.2.3"), RequestID: &reqID},
	})
	if !errors.Is(err, ErrAdminIPSelfLockout) {
		t.Fatalf("create = %v, want ErrAdminIPSelfLockout", err)
	}
	if n := countAdminIPRules(t, pool); n != 0 {
		t.Errorf("rows after rejected create = %d, want 0 (rollback)", n)
	}
	if n := countAudit(t, pool, reqID, EventAdminIPRuleCreate, "success"); n != 0 {
		t.Errorf("audit events after rejected create = %d, want 0", n)
	}

	// 같은 대역이라도 비활성으로 넣는 것은 잠금이 아니다 — 판정은 활성 규칙만 본다.
	if _, err := st.CreateAdminIPRule(ctx, CreateAdminIPRuleInput{
		CIDR:                "10.9.9.0/24",
		AdminIPRuleMutation: AdminIPRuleMutation{ActorIP: ptrString("10.1.2.3"), RequestID: &reqID},
	}); err != nil {
		t.Fatalf("create inactive rule: %v", err)
	}
}

// ActorIP가 nil이면 판정을 건너뛴다 (비상 우회 대역으로 들어온 복구 요청).
func TestCreateAdminIPRuleSkipsLockoutCheckWithoutActorIP(t *testing.T) {
	st, pool := newAdminIPStore(t)
	ctx := context.Background()
	reqID := "adminip-store-bootstrap"
	t.Cleanup(func() { cleanupAuditByRequestID(pool, reqID) })

	rule, err := st.CreateAdminIPRule(ctx, CreateAdminIPRuleInput{
		CIDR:                "10.9.9.0/24",
		IsActive:            true,
		AdminIPRuleMutation: AdminIPRuleMutation{RequestID: &reqID},
	})
	if err != nil {
		t.Fatalf("create without actor ip: %v", err)
	}
	if rule.CIDR != "10.9.9.0/24" {
		t.Errorf("cidr = %q, want 10.9.9.0/24", rule.CIDR)
	}
	if n := countAudit(t, pool, reqID, EventAdminIPRuleCreate, "success"); n != 1 {
		t.Errorf("audit events = %d, want 1", n)
	}
}

// 비활성 규칙은 판정에서 빠진다: 활성 규칙이 전부 꺼지면 "제한 없음"이라 잠금이 아니다.
func TestAdminIPRuleActiveToggleAndLockout(t *testing.T) {
	st, pool := newAdminIPStore(t)
	ctx := context.Background()
	me := ptrString("10.1.2.3")

	mine, err := st.CreateAdminIPRule(ctx, CreateAdminIPRuleInput{
		CIDR: "10.1.2.0/24", IsActive: true,
		AdminIPRuleMutation: AdminIPRuleMutation{ActorIP: me},
	})
	if err != nil {
		t.Fatalf("create mine: %v", err)
	}
	other, err := st.CreateAdminIPRule(ctx, CreateAdminIPRuleInput{
		CIDR: "10.9.9.0/24", IsActive: true,
		AdminIPRuleMutation: AdminIPRuleMutation{ActorIP: me},
	})
	if err != nil {
		t.Fatalf("create other: %v", err)
	}

	// 다른 활성 규칙이 남아 있는데 내 규칙을 끄면 잠금이다.
	if _, err := st.SetAdminIPRuleActive(ctx, mine.ID, false,
		AdminIPRuleMutation{ActorIP: me}); !errors.Is(err, ErrAdminIPSelfLockout) {
		t.Fatalf("toggle mine off = %v, want ErrAdminIPSelfLockout", err)
	}
	// 삭제도 같은 판정을 받는다.
	if err := st.DeleteAdminIPRule(ctx, mine.ID,
		AdminIPRuleMutation{ActorIP: me}); !errors.Is(err, ErrAdminIPSelfLockout) {
		t.Fatalf("delete mine = %v, want ErrAdminIPSelfLockout", err)
	}
	if n := countAdminIPRules(t, pool); n != 2 {
		t.Fatalf("rows after rejected changes = %d, want 2", n)
	}

	// 남의 규칙을 지우면 내 규칙만 남으므로 문제없다.
	if err := st.DeleteAdminIPRule(ctx, other.ID, AdminIPRuleMutation{ActorIP: me}); err != nil {
		t.Fatalf("delete other: %v", err)
	}
	// 이제 내 규칙이 마지막 활성 규칙이다 — 끄면 제한 해제라 허용된다.
	if _, err := st.SetAdminIPRuleActive(ctx, mine.ID, false, AdminIPRuleMutation{ActorIP: me}); err != nil {
		t.Fatalf("toggle last active rule off: %v", err)
	}
	cidrs, err := st.ActiveAdminIPCIDRs(ctx)
	if err != nil {
		t.Fatalf("active cidrs: %v", err)
	}
	if len(cidrs) != 0 {
		t.Errorf("active cidrs = %v, want none", cidrs)
	}
}

// 중복 대역과 없는 id는 센티널로 구분된다.
func TestAdminIPRuleSentinels(t *testing.T) {
	st, _ := newAdminIPStore(t)
	ctx := context.Background()
	me := ptrString("10.1.2.3")

	if _, err := st.CreateAdminIPRule(ctx, CreateAdminIPRuleInput{
		CIDR: "10.1.2.0/24", IsActive: true,
		AdminIPRuleMutation: AdminIPRuleMutation{ActorIP: me},
	}); err != nil {
		t.Fatalf("create: %v", err)
	}
	if _, err := st.CreateAdminIPRule(ctx, CreateAdminIPRuleInput{
		CIDR: "10.1.2.0/24", IsActive: true,
		AdminIPRuleMutation: AdminIPRuleMutation{ActorIP: me},
	}); !errors.Is(err, ErrAdminIPRuleExists) {
		t.Errorf("duplicate create = %v, want ErrAdminIPRuleExists", err)
	}

	missing := "00000000-0000-0000-0000-000000000001"
	if _, err := st.SetAdminIPRuleActive(ctx, missing, true,
		AdminIPRuleMutation{ActorIP: me}); !errors.Is(err, ErrAdminIPRuleNotFound) {
		t.Errorf("toggle missing = %v, want ErrAdminIPRuleNotFound", err)
	}
	if err := st.DeleteAdminIPRule(ctx, missing,
		AdminIPRuleMutation{ActorIP: me}); !errors.Is(err, ErrAdminIPRuleNotFound) {
		t.Errorf("delete missing = %v, want ErrAdminIPRuleNotFound", err)
	}
}

// ActiveAdminIPCIDRs는 미들웨어 캐시의 유일한 적재 경로다 — 비활성은 빠져야 한다.
func TestActiveAdminIPCIDRsExcludesInactive(t *testing.T) {
	st, _ := newAdminIPStore(t)
	ctx := context.Background()
	me := ptrString("10.1.2.3")

	if _, err := st.CreateAdminIPRule(ctx, CreateAdminIPRuleInput{
		CIDR: "10.1.2.0/24", IsActive: true,
		AdminIPRuleMutation: AdminIPRuleMutation{ActorIP: me},
	}); err != nil {
		t.Fatalf("create active: %v", err)
	}
	if _, err := st.CreateAdminIPRule(ctx, CreateAdminIPRuleInput{
		CIDR: "10.9.9.0/24", IsActive: false,
		AdminIPRuleMutation: AdminIPRuleMutation{ActorIP: me},
	}); err != nil {
		t.Fatalf("create inactive: %v", err)
	}

	cidrs, err := st.ActiveAdminIPCIDRs(ctx)
	if err != nil {
		t.Fatalf("active cidrs: %v", err)
	}
	if len(cidrs) != 1 || cidrs[0] != "10.1.2.0/24" {
		t.Errorf("active cidrs = %v, want [10.1.2.0/24]", cidrs)
	}
	all, err := st.ListAdminIPRules(ctx)
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(all) != 2 {
		t.Errorf("list = %d rules, want 2 (inactive included)", len(all))
	}
}

func ptrString(s string) *string { return &s }
