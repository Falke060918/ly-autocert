// Package config는 환경변수 기반 서버 설정을 로드한다.
// 폐쇄망 환경을 전제로 하므로 설정 소스는 환경변수뿐이다(외부 설정 서비스 없음).
package config

import (
	"encoding/hex"
	"errors"
	"fmt"
	"net"
	"os"
	"strings"
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
	// SensorListenAddr는 센서 게이트웨이(mTLS) 리슨 주소다
	// (SENSOR_LISTEN_ADDR, 기본 ":8443"). SENSOR_CA_ENC_KEY가 설정된 경우에만 쓰인다.
	SensorListenAddr string
	// SensorAdvertisedDNS/IPs는 센서가 게이트웨이에 접속할 때 쓰는 주소다
	// (SENSOR_ADVERTISED_HOSTS, 콤마 구분 DNS/IP 혼용). 게이트웨이 서버 인증서의
	// SAN에 들어간다 — 여기 없는 주소로 접속하는 센서는 TLS 검증에 실패한다.
	SensorAdvertisedDNS []string
	SensorAdvertisedIPs []net.IP
	// SensorConfigError는 센서 게이트웨이 전용 설정의 오류다. 센서 설정 하나가
	// 코어 API(8080)까지 죽이지 않도록 Load는 이 오류를 반환하지 않고 여기 담는다 —
	// cmd/server가 경고로 남기고 게이트웨이만 비활성화한다. secret 값은 담지 않는다.
	SensorConfigError error
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
		// 형식이 틀려도 기동은 계속한다: 게이트웨이만 비활성화되고 코어 API는 뜬다.
		key, err := hex.DecodeString(raw)
		if err != nil || len(key) != 32 {
			cfg.SensorConfigError = errors.New("config: SENSOR_CA_ENC_KEY must be 64 hex chars (32 bytes)")
		} else {
			cfg.SensorCAEncKey = key
		}
	}
	cfg.SensorListenAddr = getenv("SENSOR_LISTEN_ADDR", ":8443")
	for _, h := range strings.Split(os.Getenv("SENSOR_ADVERTISED_HOSTS"), ",") {
		h = strings.TrimSpace(h)
		if h == "" {
			continue
		}
		if ip := net.ParseIP(h); ip != nil {
			cfg.SensorAdvertisedIPs = append(cfg.SensorAdvertisedIPs, ip)
		} else {
			cfg.SensorAdvertisedDNS = append(cfg.SensorAdvertisedDNS, h)
		}
	}
	return cfg, nil
}

func getenv(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}
