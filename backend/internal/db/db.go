// Package db는 PostgreSQL 연결과 SQL 마이그레이션 실행을 담당한다.
package db

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	"github.com/golang-migrate/migrate/v4"
	migratepgx "github.com/golang-migrate/migrate/v4/database/pgx/v5"
	"github.com/golang-migrate/migrate/v4/source/iofs"
	_ "github.com/jackc/pgx/v5/stdlib" // database/sql용 pgx 드라이버 등록

	"github.com/Falke060918/INHERIT_AutoCert/backend/migrations"
)

// Connect는 DSN으로 PostgreSQL에 연결하고 ping이 성공할 때까지 재시도한다.
// docker compose 기동 직후 DB가 아직 준비 중인 상황을 흡수한다.
func Connect(ctx context.Context, dsn string) (*sql.DB, error) {
	pool, err := sql.Open("pgx", dsn)
	if err != nil {
		return nil, fmt.Errorf("db: open: %w", err)
	}

	const (
		maxAttempts = 10
		retryDelay  = 2 * time.Second
	)
	var lastErr error
	for attempt := 1; attempt <= maxAttempts; attempt++ {
		pingCtx, cancel := context.WithTimeout(ctx, 3*time.Second)
		lastErr = pool.PingContext(pingCtx)
		cancel()
		if lastErr == nil {
			return pool, nil
		}
		select {
		case <-ctx.Done():
			pool.Close()
			return nil, ctx.Err()
		case <-time.After(retryDelay):
		}
	}
	pool.Close()
	return nil, fmt.Errorf("db: ping failed after %d attempts: %w", maxAttempts, lastErr)
}

// Migrate는 backend/migrations에 임베드된 SQL 마이그레이션을 최신 버전까지 적용한다.
// 이미 최신이면 아무것도 하지 않는다(중복 실행 안전).
func Migrate(pool *sql.DB) error {
	src, err := iofs.New(migrations.FS, ".")
	if err != nil {
		return fmt.Errorf("db: load migration source: %w", err)
	}
	driver, err := migratepgx.WithInstance(pool, &migratepgx.Config{})
	if err != nil {
		return fmt.Errorf("db: init migration driver: %w", err)
	}
	m, err := migrate.NewWithInstance("iofs", src, "pgx5", driver)
	if err != nil {
		return fmt.Errorf("db: init migrate: %w", err)
	}
	if err := m.Up(); err != nil && !errors.Is(err, migrate.ErrNoChange) {
		return fmt.Errorf("db: migrate up: %w", err)
	}
	return nil
}
