package api

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"sync"
	"testing"
	"time"

	"golang.org/x/crypto/bcrypt"

	"github.com/Falke060918/INHERIT_AutoCert/backend/internal/auth"
	"github.com/Falke060918/INHERIT_AutoCert/backend/internal/domain"
)

// 초기 설정(첫 접속 시 관리자 계정 생성) 검증.
// 핵심은 "사용자 0명일 때만"이다 — 이게 뚫리면 누구나 관리자를 만들 수 있다.
// TEST_DATABASE_URL 미설정 시 스킵한다.

func (e *testEnv) setupRequired(t *testing.T) bool {
	t.Helper()
	resp := e.do(t, http.MethodGet, "/api/v1/setup/status", "", nil)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("setup status = %d, want 200", resp.StatusCode)
	}
	var out struct {
		SetupRequired bool `json:"setup_required"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		t.Fatalf("decode setup status: %v", err)
	}
	return out.SetupRequired
}

func TestInitialSetup(t *testing.T) {
	env := newTestEnv(t)

	if !env.setupRequired(t) {
		t.Fatal("setup_required = false with zero users, want true")
	}

	pw := randomPassword(t)

	t.Run("password policy is enforced", func(t *testing.T) {
		// 12자 미만.
		resp := env.do(t, http.MethodPost, "/api/v1/setup/admin", "",
			map[string]string{"username": "setup-admin", "password": "short"})
		if resp.StatusCode != http.StatusBadRequest {
			t.Errorf("short password = %d, want 400", resp.StatusCode)
		}
		// 계정명과 동일.
		resp = env.do(t, http.MethodPost, "/api/v1/setup/admin", "",
			map[string]string{"username": "setup-administrator", "password": "setup-administrator"})
		if resp.StatusCode != http.StatusBadRequest {
			t.Errorf("password equal to username = %d, want 400", resp.StatusCode)
		}
		// 위반 응답에 비밀번호 값이 실리지 않는다 (보안 하드 규칙 3·4).
		body, err := io.ReadAll(resp.Body)
		if err != nil {
			t.Fatalf("read error body: %v", err)
		}
		if strings.Contains(string(body), "setup-administrator") {
			t.Error("weak_password response echoes the submitted credential")
		}
		// 실패했으므로 아직 아무도 만들어지지 않았다.
		if !env.setupRequired(t) {
			t.Error("setup_required = false after failed attempts, want true")
		}
	})

	t.Run("unknown fields are rejected", func(t *testing.T) {
		resp := env.do(t, http.MethodPost, "/api/v1/setup/admin", "",
			map[string]string{"username": "setup-admin", "password": pw, "role": "viewer"})
		if resp.StatusCode != http.StatusBadRequest {
			t.Errorf("unknown field = %d, want 400", resp.StatusCode)
		}
		if !env.setupRequired(t) {
			t.Error("setup_required = false after rejected body, want true")
		}
	})

	t.Run("missing username is rejected", func(t *testing.T) {
		resp := env.do(t, http.MethodPost, "/api/v1/setup/admin", "",
			map[string]string{"username": "", "password": pw})
		if resp.StatusCode != http.StatusBadRequest {
			t.Errorf("empty username = %d, want 400", resp.StatusCode)
		}
	})

	var created domain.User
	t.Run("zero users: the first administrator is created", func(t *testing.T) {
		resp := env.do(t, http.MethodPost, "/api/v1/setup/admin", "",
			map[string]string{"username": "setup-admin", "password": pw})
		if resp.StatusCode != http.StatusCreated {
			t.Fatalf("initial admin = %d, want 201", resp.StatusCode)
		}
		if err := json.NewDecoder(resp.Body).Decode(&created); err != nil {
			t.Fatalf("decode created admin: %v", err)
		}
		if created.Role != domain.RoleSystemAdministrator {
			t.Errorf("role = %s, want system_administrator", created.Role)
		}
		if !created.IsActive {
			t.Error("created admin is not active")
		}
		// 하드 규칙 5: 변경 API는 감사 로그를 남긴다.
		if n := env.auditCount(t, "user.created", domain.AuditSuccess); n != 1 {
			t.Errorf("user.created audit events = %d, want 1", n)
		}
	})

	t.Run("the created account can log in", func(t *testing.T) {
		token, status := env.login(t, "setup-admin", pw)
		if status != http.StatusOK || token == "" {
			t.Fatalf("login = %d, token empty=%v; want 200 with token", status, token == "")
		}
	})

	t.Run("setup closes once a user exists", func(t *testing.T) {
		if env.setupRequired(t) {
			t.Error("setup_required = true after the admin was created, want false")
		}
		resp := env.do(t, http.MethodPost, "/api/v1/setup/admin", "",
			map[string]string{"username": "setup-intruder", "password": randomPassword(t)})
		if resp.StatusCode != http.StatusConflict {
			t.Fatalf("second setup attempt = %d, want 409", resp.StatusCode)
		}
		var n int
		if err := env.pool.QueryRow(`SELECT count(*) FROM users`).Scan(&n); err != nil {
			t.Fatalf("count users: %v", err)
		}
		if n != 1 {
			t.Fatalf("users = %d after rejected setup, want 1", n)
		}
	})
}

// 관리자가 아닌 계정만 존재해도 초기 설정은 닫혀 있어야 한다 —
// 판정 기준은 "관리자 0명"이 아니라 "사용자 0명"이다.
func TestInitialSetupClosedWithNonAdminUser(t *testing.T) {
	env := newTestEnv(t)
	ctx := context.Background()

	if _, err := env.svc.CreateUser(ctx, "setup-viewer", randomPassword(t), nil,
		domain.RoleViewer, nil, auth.AuditMeta{}); err != nil {
		t.Fatalf("create viewer: %v", err)
	}
	if env.setupRequired(t) {
		t.Error("setup_required = true with an existing viewer, want false")
	}
	resp := env.do(t, http.MethodPost, "/api/v1/setup/admin", "",
		map[string]string{"username": "setup-intruder", "password": randomPassword(t)})
	if resp.StatusCode != http.StatusConflict {
		t.Errorf("setup with existing user = %d, want 409", resp.StatusCode)
	}
}

// 경쟁 상태의 결정론적 검증.
// 8개를 동시에 쏘는 것만으로는 창이 너무 좁아 경쟁을 재현하지 못한다(락을 빼도 통과한다).
// 그래서 "앞선 요청이 아직 커밋 전"인 상태를 직접 만든다 — 같은 advisory lock을 쥔 채
// 사용자를 만들고 커밋하는 트랜잭션을 세우고, 그 사이에 들어온 설정 요청이
//  1. 락에서 대기하는지(락을 안 잡으면 그 자리에서 201을 받아 관리자가 2명이 된다)
//  2. 상대가 커밋한 뒤에는 409로 끝나는지
//
// 를 확인한다.
func TestInitialSetupWaitsForConcurrentSetupTransaction(t *testing.T) {
	env := newTestEnv(t)
	ctx := context.Background()

	body, err := json.Marshal(map[string]string{
		"username": "setup-loser", "password": randomPassword(t),
	})
	if err != nil {
		t.Fatalf("encode body: %v", err)
	}
	// 먼저 들어온 설정 요청 역할 — 락을 쥐고 사용자를 만든 뒤 아직 커밋하지 않았다.
	hash, err := auth.HashPassword(randomPassword(t), bcrypt.MinCost)
	if err != nil {
		t.Fatalf("hash password: %v", err)
	}
	tx, err := env.pool.BeginTx(ctx, nil)
	if err != nil {
		t.Fatalf("begin: %v", err)
	}
	defer tx.Rollback() //nolint:errcheck
	if _, err := tx.ExecContext(ctx, `SELECT pg_advisory_xact_lock(x'7365747570'::bigint)`); err != nil {
		t.Fatalf("take setup lock: %v", err)
	}

	done := make(chan int, 1)
	failed := make(chan error, 1)
	go func() {
		resp, err := http.Post(env.server.URL+"/api/v1/setup/admin",
			"application/json", bytes.NewReader(body))
		if err != nil {
			failed <- err
			return
		}
		defer resp.Body.Close()
		done <- resp.StatusCode
	}()

	select {
	case st := <-done:
		t.Fatalf("setup request finished with %d while another setup transaction held the lock; "+
			"it must serialize on the database", st)
	case err := <-failed:
		t.Fatalf("setup request: %v", err)
	case <-time.After(750 * time.Millisecond):
		// 기대한 대로 대기 중이다.
	}

	if _, err := tx.ExecContext(ctx,
		`INSERT INTO users (username, password_hash, role) VALUES ($1, $2, $3)`,
		"setup-winner", hash, domain.RoleSystemAdministrator); err != nil {
		t.Fatalf("insert winner: %v", err)
	}
	if err := tx.Commit(); err != nil {
		t.Fatalf("commit winner: %v", err)
	}

	select {
	case st := <-done:
		if st != http.StatusConflict {
			t.Errorf("setup request after the winner committed = %d, want 409", st)
		}
	case err := <-failed:
		t.Fatalf("setup request: %v", err)
	case <-time.After(10 * time.Second):
		t.Fatal("setup request never completed after the lock was released")
	}

	var n int
	if err := env.pool.QueryRow(`SELECT count(*) FROM users`).Scan(&n); err != nil {
		t.Fatalf("count users: %v", err)
	}
	if n != 1 {
		t.Errorf("users = %d, want 1 (the loser must not have been created)", n)
	}
}

// 동시 요청이 몰려도 정확히 1건만 성공해야 한다 (연기 감지용 — 위 테스트가 본검증이다).
func TestInitialSetupIsAtomicUnderConcurrency(t *testing.T) {
	env := newTestEnv(t)

	const attempts = 8
	// 본문은 미리 만든다 — 고루틴 안에서 t.Fatalf를 부르지 않기 위해서다.
	bodies := make([][]byte, attempts)
	for i := range attempts {
		body, err := json.Marshal(map[string]string{
			"username": "race-admin-" + string(rune('a'+i)),
			"password": randomPassword(t),
		})
		if err != nil {
			t.Fatalf("encode body: %v", err)
		}
		bodies[i] = body
	}

	statuses := make([]int, attempts)
	errs := make([]error, attempts)
	start := make(chan struct{})
	var wg sync.WaitGroup
	for i := range attempts {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			<-start
			resp, err := http.Post(env.server.URL+"/api/v1/setup/admin",
				"application/json", bytes.NewReader(bodies[i]))
			if err != nil {
				errs[i] = err
				return
			}
			defer resp.Body.Close()
			statuses[i] = resp.StatusCode
		}(i)
	}
	close(start)
	wg.Wait()

	created, conflicts := 0, 0
	for i, st := range statuses {
		if errs[i] != nil {
			t.Fatalf("concurrent setup request %d: %v", i, errs[i])
		}
		switch st {
		case http.StatusCreated:
			created++
		case http.StatusConflict:
			conflicts++
		default:
			t.Errorf("unexpected status %d from concurrent setup", st)
		}
	}
	if created != 1 {
		t.Errorf("concurrent setup created %d admins, want exactly 1", created)
	}
	if conflicts != attempts-1 {
		t.Errorf("concurrent setup returned %d conflicts, want %d", conflicts, attempts-1)
	}
	var n int
	if err := env.pool.QueryRow(`SELECT count(*) FROM users`).Scan(&n); err != nil {
		t.Fatalf("count users: %v", err)
	}
	if n != 1 {
		t.Errorf("users = %d after concurrent setup, want 1", n)
	}
}
