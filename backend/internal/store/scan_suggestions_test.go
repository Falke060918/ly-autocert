package store

// 스캔 Job 목록·대상 제안 조회 검증 (B2).

import (
	"context"
	"testing"
)

// createTestScanJob은 CreateScanJob으로 Job을 만든다 (정리는 호출자 소관).
func createTestScanJob(t *testing.T, st *Store, reqID string, targets []string) string {
	t.Helper()
	ctx := context.Background()
	id, err := st.CreateScanJob(ctx, ScanInput{
		Targets:        targets,
		Ports:          []int{443},
		TimeoutMs:      1000,
		Concurrency:    1,
		TotalEndpoints: len(targets),
		RequestID:      &reqID,
	})
	if err != nil {
		t.Fatalf("create scan job: %v", err)
	}
	return id
}

func TestListScanJobsFiltersAndPagination(t *testing.T) {
	pool := testPool(t)
	st := New(pool)
	ctx := context.Background()
	reqID := "test-req-list-scans"

	j1 := createTestScanJob(t, st, reqID, []string{"192.0.2.10"})
	j2 := createTestScanJob(t, st, reqID, []string{"192.0.2.11"})
	j3 := createTestScanJob(t, st, reqID, []string{"192.0.2.12"})
	t.Cleanup(func() {
		_, _ = pool.Exec(`DELETE FROM scan_jobs WHERE id IN ($1::uuid, $2::uuid, $3::uuid)`, j1, j2, j3)
		cleanupAuditByRequestID(pool, reqID)
	})
	// j2는 스윕 청크·완료 상태로 바꿔 필터를 검증한다 (테스트 전용 직접 UPDATE).
	if _, err := pool.Exec(
		`UPDATE scan_jobs SET origin = 'sweep', status = 'completed', finished_at = now() WHERE id = $1::uuid`, j2); err != nil {
		t.Fatalf("update j2: %v", err)
	}

	// origin=manual은 j2를 제외해야 한다.
	res, err := st.ListScanJobs(ctx, ListScanJobsInput{Origins: []string{ScanOriginManual}, PageSize: 200})
	if err != nil {
		t.Fatalf("list manual: %v", err)
	}
	seen := map[string]bool{}
	for _, j := range res.Items {
		seen[j.ID] = true
		if j.Origin != ScanOriginManual {
			t.Errorf("origin filter returned job %s with origin %q", j.ID, j.Origin)
		}
	}
	if !seen[j1] || !seen[j3] || seen[j2] {
		t.Errorf("origin=manual seen=%v, want j1·j3 without j2", seen)
	}

	// status=completed는 j2만 (우리 픽스처 중에서).
	res, err = st.ListScanJobs(ctx, ListScanJobsInput{Statuses: []string{ScanStatusCompleted}, PageSize: 200})
	if err != nil {
		t.Fatalf("list completed: %v", err)
	}
	foundJ2 := false
	for _, j := range res.Items {
		if j.Status != ScanStatusCompleted {
			t.Errorf("status filter returned job %s with status %q", j.ID, j.Status)
		}
		if j.ID == j2 {
			foundJ2 = true
			if j.Origin != ScanOriginSweep {
				t.Errorf("j2 origin = %q, want sweep", j.Origin)
			}
		}
		if j.ID == j1 || j.ID == j3 {
			t.Errorf("status=completed must not return pending job %s", j.ID)
		}
	}
	if !foundJ2 {
		t.Error("status=completed missing j2")
	}

	// 최신 생성 순 + 페이지네이션: page_size=1이면 1건씩, j3가 j1보다 앞이다.
	res, err = st.ListScanJobs(ctx, ListScanJobsInput{PageSize: 1})
	if err != nil {
		t.Fatalf("list page: %v", err)
	}
	if len(res.Items) != 1 || res.PageSize != 1 {
		t.Fatalf("page_size=1 items=%d", len(res.Items))
	}
	full, err := st.ListScanJobs(ctx, ListScanJobsInput{PageSize: 200})
	if err != nil {
		t.Fatalf("list all: %v", err)
	}
	pos := map[string]int{}
	for i, j := range full.Items {
		pos[j.ID] = i
	}
	if !(pos[j3] < pos[j1]) {
		t.Errorf("ordering: j3(%d) must come before j1(%d) — created_at DESC", pos[j3], pos[j1])
	}
}

func TestScanSuggestions(t *testing.T) {
	pool := testPool(t)
	st := New(pool)
	ctx := context.Background()
	reqID := "test-req-scan-suggestions"

	j1 := createTestScanJob(t, st, reqID, []string{"10.77.1.0/30"})
	j2 := createTestScanJob(t, st, reqID, []string{"10.77.2.5"})
	j3 := createTestScanJob(t, st, reqID, []string{"10.99.9.9"})
	if _, err := pool.Exec(`UPDATE scan_jobs SET origin = 'rescan' WHERE id = $1::uuid`, j3); err != nil {
		t.Fatalf("update j3 origin: %v", err)
	}
	// 파생 /24용 targets: 10.88.1.x 3개, 10.88.2.x 1개.
	ips := []string{"10.88.1.1", "10.88.1.2", "10.88.1.3", "10.88.2.1"}
	for _, ip := range ips {
		// ON CONFLICT: 이전 실행 잔여 행이 있어도 픽스처 준비가 실패하지 않게.
		// 같은 $n을 text와 ::inet 두 자리에 쓰면 42P08이 난다(GOTCHAS) — 파라미터를 나눈다.
		if _, err := pool.Exec(`
			INSERT INTO targets (name, target_type, ip_address, executor_scope)
			VALUES ($1, 'network', $2::inet, 'server')
			ON CONFLICT (executor_scope, ip_address) WHERE target_type = 'network' DO NOTHING`, ip, ip); err != nil {
			t.Fatalf("insert target %s: %v", ip, err)
		}
	}
	t.Cleanup(func() {
		_, _ = pool.Exec(`DELETE FROM scan_jobs WHERE id IN ($1::uuid, $2::uuid, $3::uuid)`, j1, j2, j3)
		_, _ = pool.Exec(`DELETE FROM targets WHERE target_type = 'network'
			AND (ip_address << '10.88.1.0/24'::inet OR ip_address << '10.88.2.0/24'::inet)`)
		cleanupAuditByRequestID(pool, reqID)
	})

	sug, err := st.ScanSuggestions(ctx, 10, 20)
	if err != nil {
		t.Fatalf("scan suggestions: %v", err)
	}

	// 최근 사용: j2(10.77.2.5)가 j1(10.77.1.0/30)보다 앞, rescan 발주 대상은 없다.
	pos := map[string]int{}
	for i, tgt := range sug.RecentTargets {
		pos[tgt] = i
		if tgt == "10.99.9.9" {
			t.Error("recent_targets must not include rescan-origin targets")
		}
	}
	p2, ok2 := pos["10.77.2.5"]
	p1, ok1 := pos["10.77.1.0/30"]
	if !ok1 || !ok2 {
		t.Fatalf("recent_targets %v missing fixtures", sug.RecentTargets)
	}
	if !(p2 < p1) {
		t.Errorf("recent order: 10.77.2.5(%d) must come before 10.77.1.0/30(%d)", p2, p1)
	}

	// 파생 대역: 10.88.1.0/24(3) → 10.88.2.0/24(1), 카운트 내림차순.
	var n1, n2 *DerivedNetwork
	i1, i2 := -1, -1
	for i := range sug.DerivedNetworks {
		switch sug.DerivedNetworks[i].CIDR {
		case "10.88.1.0/24":
			n1, i1 = &sug.DerivedNetworks[i], i
		case "10.88.2.0/24":
			n2, i2 = &sug.DerivedNetworks[i], i
		}
	}
	if n1 == nil || n2 == nil {
		t.Fatalf("derived_networks %v missing fixtures", sug.DerivedNetworks)
	}
	if n1.TargetCount != 3 || n2.TargetCount != 1 {
		t.Errorf("target counts = %d/%d, want 3/1", n1.TargetCount, n2.TargetCount)
	}
	if !(i1 < i2) {
		t.Errorf("derived order: /24 with 3 targets must come before 1 target (%d vs %d)", i1, i2)
	}
}
