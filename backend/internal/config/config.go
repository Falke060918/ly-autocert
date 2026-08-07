// Package config는 환경변수 기반 서버 설정을 로드한다.
// 폐쇄망 환경을 전제로 하므로 설정 소스는 환경변수뿐이다(외부 설정 서비스 없음).
package config

import (
	"errors"
	"os"
)

// Config는 서버 기동에 필요한 설정값이다.
type Config struct {
	// HTTPAddr은 HTTP 리슨 주소다. 예: ":8080"
	HTTPAddr string
	// DatabaseURL은 PostgreSQL 접속 DSN이다.
	// 예: postgres://user:pass@host:5432/dbname?sslmode=disable
	// secret이 포함되므로 절대 로그에 출력하지 않는다.
	DatabaseURL string
}

// Load는 환경변수에서 설정을 읽는다.
func Load() (Config, error) {
	cfg := Config{
		HTTPAddr:    getenv("HTTP_ADDR", ":8080"),
		DatabaseURL: os.Getenv("DATABASE_URL"),
	}
	if cfg.DatabaseURL == "" {
		return Config{}, errors.New("config: DATABASE_URL is required")
	}
	return cfg, nil
}

func getenv(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}
