// Package config는 환경변수 기반 서버 설정을 로드한다.
// 폐쇄망 환경을 전제로 하므로 설정 소스는 환경변수뿐이다(외부 설정 서비스 없음).
package config

import (
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"time"
)

// Config는 서버 기동에 필요한 설정값이다.
type Config struct {
	// HTTPAddr은 HTTP 리슨 주소다. 예: ":8080"
	HTTPAddr string
	// DatabaseURL은 PostgreSQL 접속 DSN이다.
	// 예: postgres://user:pass@host:5432/dbname?sslmode=disable
	// secret이 포함되므로 절대 로그에 출력하지 않는다.
	DatabaseURL string
	// SessionTTL은 로그인 세션 유효기간이다 (SESSION_TTL, Go duration 형식).
	SessionTTL time.Duration
	// BootstrapAdminUsername/Password는 users가 비어 있을 때 만들 초기
	// System Administrator 계정이다. 비밀번호는 환경변수로만 주입한다 —
	// 코드·설정 파일에 하드코딩 금지. 로그에 출력하지 않는다.
	BootstrapAdminUsername string
	BootstrapAdminPassword string
	// SensorCAEncKey는 센서 mTLS 내부 CA 개인키를 AES-256-GCM으로 봉인하는
	// 32바이트 키다 (SENSOR_CA_ENC_KEY, hex 64자 — 예: openssl rand -hex 32).
	// 미설정이면 nil — 센서 CA 부트스트랩이 필요한 시점(첫 센서 생성)에만 오류가
	// 된다. secret이므로 절대 로그에 출력하지 않는다.
	SensorCAEncKey []byte
}

// Load는 환경변수에서 설정을 읽는다.
func Load() (Config, error) {
	cfg := Config{
		HTTPAddr:               getenv("HTTP_ADDR", ":8080"),
		DatabaseURL:            os.Getenv("DATABASE_URL"),
		BootstrapAdminUsername: getenv("BOOTSTRAP_ADMIN_USERNAME", "admin"),
		BootstrapAdminPassword: os.Getenv("BOOTSTRAP_ADMIN_PASSWORD"),
	}
	if cfg.DatabaseURL == "" {
		return Config{}, errors.New("config: DATABASE_URL is required")
	}
	ttl, err := time.ParseDuration(getenv("SESSION_TTL", "8h"))
	if err != nil || ttl <= 0 {
		return Config{}, fmt.Errorf("config: SESSION_TTL must be a positive Go duration: %w", err)
	}
	cfg.SessionTTL = ttl
	if raw := os.Getenv("SENSOR_CA_ENC_KEY"); raw != "" {
		// 오류에 키 값을 싣지 않는다 — 형식 요구사항만 알린다.
		key, err := hex.DecodeString(raw)
		if err != nil || len(key) != 32 {
			return Config{}, errors.New("config: SENSOR_CA_ENC_KEY must be 64 hex chars (32 bytes)")
		}
		cfg.SensorCAEncKey = key
	}
	return cfg, nil
}

func getenv(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}
