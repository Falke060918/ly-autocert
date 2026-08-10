// Package config는 환경변수 기반 서버 설정을 로드한다.
// 폐쇄망 환경을 전제로 하므로 설정 소스는 환경변수뿐이다(외부 설정 서비스 없음).
package config

import (
	"encoding/hex"
	"errors"
	"fmt"
	"net"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/Falke060918/INHERIT_AutoCert/backend/internal/notify"
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
	// SessionCookieSecure는 세션 쿠키에 Secure 플래그를 붙일지다
	// (SESSION_COOKIE_SECURE, 기본 false). HTTPS 리버스 프록시로 TLS를 종단할 때만
	// true로 둔다 — 평문 HTTP 접속에서 켜면 브라우저가 쿠키를 버려 로그인이 조용히
	// 실패한다 (docs/manual/TLS.md). 백엔드는 프록시 뒤에서 평문 HTTP를 받으므로
	// 요청만 보고는 판별할 수 없어 설정으로 받는다.
	SessionCookieSecure bool
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
	// SensorGatewayPort는 SensorListenAddr에서 뽑은 포트다 (":8443" → "8443").
	// 센서 등록 명령 예시(POST /sensors 응답)가 **실제** 게이트웨이 포트를 안내하도록
	// API 계층에 넘긴다 — 예시가 기본 포트로 굳어 있으면 포트를 바꾼 운영자가
	// 그대로 복사해 접속에 실패한다. 파싱할 수 없으면 기본 포트로 폴백한다(경고).
	SensorGatewayPort string
	// SensorAdvertisedDNS/IPs는 센서가 게이트웨이에 접속할 때 쓰는 주소다
	// (SENSOR_ADVERTISED_HOSTS, 콤마 구분 DNS/IP 혼용). 게이트웨이 서버 인증서의
	// SAN에 들어간다 — 여기 없는 주소로 접속하는 센서는 TLS 검증에 실패한다.
	SensorAdvertisedDNS []string
	SensorAdvertisedIPs []net.IP
	// SensorConfigError는 센서 게이트웨이 전용 설정의 오류다. 센서 설정 하나가
	// 코어 API(8080)까지 죽이지 않도록 Load는 이 오류를 반환하지 않고 여기 담는다 —
	// cmd/server가 경고로 남기고 게이트웨이만 비활성화한다. secret 값은 담지 않는다.
	SensorConfigError error
	// Notify는 알림 기능 설정이다 (NOTIFY_*/SMTP_*/WEBHOOK_*/SYSLOG_*).
	Notify NotifyConfig
	// NotifyChannelErrors는 채널별 설정 오류다 (키: notify.ChannelEmail 등).
	// 항목이 있으면 그 채널만 비활성이고 코어 API·다른 채널은 정상 동작한다 —
	// 센서 게이트웨이와 같은 실패 격리 원칙이다.
	// 값 문자열은 기동 경고 로그와 채널 상태 API의 사유로 그대로 나가므로
	// 비밀번호·시크릿·토큰 값을 절대 담지 않는다 — 변수 이름만 지목한다.
	// "미설정"은 오류가 아니다 — 그 사유는 notify 채널 생성자가 낸다.
	NotifyChannelErrors map[string]string
	// Sweep은 대역 스윕 설정이다 (SCAN_SWEEP_*).
	Sweep SweepConfig
	// Rescan은 주기 재스캔·교체 감지 설정이다 (RESCAN_*).
	Rescan RescanConfig
	// Warnings는 값 오류로 기본값에 폴백했거나 위험한 값을 켠 항목의 경고다.
	// cmd/server가 기동 시 경고 로그로 남긴다. secret 값은 담지 않는다.
	// 채널을 비활성화하는 사유는 여기가 아니라 NotifyChannelErrors에 담긴다.
	Warnings []string
}

// NOTIFY_FIRST_RUN 값. seed는 첫 tick에서 이미 지난 임계값을 발송하지 않고
// 기준선으로만 적재한다 (설치 당일 폭주 방지). send는 전부 발송한다.
const (
	FirstRunSeed = "seed"
	FirstRunSend = "send"
)

// NotifyConfig는 알림 기능 설정이다. 채널 설정은 notify 패키지 타입을 그대로 담는다 —
// notify는 환경변수를 직접 읽지 않고 이 구조체를 인자로 받는다(의존 방향: config → notify).
// Logger·Timeout은 여기서 채우지 않는다 — 호출부(cmd/server)가 채운다.
type NotifyConfig struct {
	// Enabled는 알림 기능 전체 on/off다 (NOTIFY_ENABLED, 기본 true).
	Enabled bool
	// ScanInterval은 스케줄러 tick 주기다 (NOTIFY_SCAN_INTERVAL, 기본 1h).
	ScanInterval time.Duration
	// FirstRun은 FirstRunSeed(기본) 또는 FirstRunSend다 (NOTIFY_FIRST_RUN).
	FirstRun string
	// RetentionDays는 종료 상태 딜리버리 보존 일수다 (NOTIFY_RETENTION_DAYS, 기본 180).
	RetentionDays int
	// BaseURL은 알림 본문 딥링크의 기준 URL이다 (NOTIFY_BASE_URL).
	// 비면 링크를 생략한다 — 오류가 아니다. 끝의 "/"는 제거된 상태다.
	BaseURL string

	Email   notify.EmailConfig
	Webhook notify.WebhookConfig
	Syslog  notify.SyslogConfig
}

// SweepConfig는 대역 스윕 설정이다 (SCAN_SWEEP_*).
type SweepConfig struct {
	// Enabled는 스윕 기능 자체의 on/off다 (기본 true).
	Enabled bool
	// RatePPS는 스윕 기본 레이트다 (기본 10, 상한 sweepMaxRatePPS).
	RatePPS int
	// Concurrency는 스윕 워커 동시성이다 (기본 8).
	Concurrency int
	// ChunkEndpoints는 청크 하나의 최대 엔드포인트 수다 (기본 4096).
	ChunkEndpoints int
	// ChunkGap은 청크 발주 간 공백이다 (기본 5s). 0도 유효한 값이다.
	ChunkGap time.Duration
	// MaxDays는 ETA가 이 일수를 넘으면 스윕 생성을 거부하는 임계값이다 (기본 30).
	MaxDays int
	// StallAfter는 자식 Job이 이만큼 대기 상태에 머무르면 "대기 중"으로 표시하는
	// 기준 시간이다 (기본 10m).
	StallAfter time.Duration
}

// RescanConfig는 주기 재스캔·교체 감지 설정이다 (RESCAN_*).
// 값이 잘못돼도 기동을 막지 않는다 — 경고 + 기본값 폴백이다 (설계 12.2).
type RescanConfig struct {
	// Enabled는 재스캔 기능 전체 on/off다 (기본 true).
	Enabled bool
	// Interval은 기본 재확인 간격이다 (기본 12h). 만료 임박 구간은 discovery가
	// 고정 티어(30일 이하 6h / 7일 이하 1h)로 가속하므로 이 값이 상한이다.
	Interval time.Duration
	// RatePPS는 재스캔 Job의 dial 레이트다 (기본 20 — 단발 50과 스윕 10 사이).
	RatePPS int
	// BatchIPs는 한 tick이 다시 볼 엔드포인트 상한이다 (기본 1024).
	BatchIPs int
	// ReplaceConfirm은 교체 확정에 필요한 연속 관측 횟수다 (기본 2).
	// 1이면 LB 뒤에서 인증서 두 개가 번갈아 보일 때 서로를 무한히 replaced로 만든다.
	ReplaceConfirm int
	// RetireMisses는 은퇴 판정 연속 미관측 횟수다 (기본 30 ≈ 기본 간격으로 보름).
	RetireMisses int
}

// 재스캔 기본값·경계. minRescanInterval은 하한이다 — 1s 같은 값을 그대로 받으면
// 인벤토리 전체를 매 tick 다시 훑어 폐쇄망에서 IPS 오탐을 부른다.
const (
	defaultRescanInterval = 12 * time.Hour
	minRescanInterval     = 1 * time.Minute
)

// sweepMaxRatePPS는 레이트 상한이다. 더 큰 값을 받아도 여기로 깎고 경고를 남긴다
// (IPS 오탐 억제).
const sweepMaxRatePPS = 1000

// 센서 게이트웨이 리슨 주소의 기본값과 그 포트다. 둘은 항상 같은 포트를 가리킨다.
const (
	defaultSensorListenAddr = ":8443"
	defaultSensorPort       = "8443"
)

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
	// 알림·스윕뿐 아니라 센서 주소 검증도 이 파서의 경고 목록을 쓴다 —
	// 잘못된 값은 기동을 막지 않고 경고 + 무시가 이 파일의 일관된 처리다.
	l := &envLoader{channelErrors: map[string]string{}}
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
	cfg.SensorListenAddr = getenv("SENSOR_LISTEN_ADDR", defaultSensorListenAddr)
	cfg.SensorGatewayPort = l.listenPort("SENSOR_LISTEN_ADDR", cfg.SensorListenAddr, defaultSensorPort)
	cfg.SensorAdvertisedDNS, cfg.SensorAdvertisedIPs =
		l.advertisedHosts("SENSOR_ADVERTISED_HOSTS", os.Getenv("SENSOR_ADVERTISED_HOSTS"))
	// 오값이어도 기동을 막지 않는다 — 폴백은 false(평문 HTTP 기본 설치)다.
	cfg.SessionCookieSecure = l.boolVal("SESSION_COOKIE_SECURE", false)
	// 알림·스윕 설정은 값이 잘못돼도 기동을 막지 않는다: 경고 후 기본값 폴백이거나
	// 해당 채널만 비활성이다 (센서 게이트웨이 선례).
	cfg.Notify = l.notify()
	cfg.Sweep = l.sweep()
	cfg.Rescan = l.rescan()
	cfg.NotifyChannelErrors = l.channelErrors
	cfg.Warnings = l.warnings
	return cfg, nil
}

func getenv(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}

// envLoader는 값 오류를 경고로 모으며 환경변수를 읽는 파서다.
// 모든 파서는 오류를 반환하지 않고 기본값으로 폴백한다 — 설정 오타 하나가
// 인벤토리 조회까지 막는 것은 과한 대가다.
type envLoader struct {
	warnings      []string
	channelErrors map[string]string
}

func (l *envLoader) warnf(format string, a ...any) {
	l.warnings = append(l.warnings, fmt.Sprintf(format, a...))
}

// disableChannel은 채널 하나를 비활성화하는 사유를 기록한다.
// 먼저 기록된 사유가 이긴다 — 운영자가 고칠 첫 항목을 가리키기 위해서다.
// reason에 secret 값을 넣지 않는다.
func (l *envLoader) disableChannel(channel, reason string) {
	if _, ok := l.channelErrors[channel]; ok {
		return
	}
	l.channelErrors[channel] = reason
}

// listenPort는 리슨 주소에서 포트만 뽑는다 (":8443" → "8443").
// 파싱 실패는 기동을 막지 않는다 — 리슨은 주소 원문으로 그대로 시도하고,
// 여기서 뽑은 포트는 등록 명령 예시 안내에만 쓰이므로 기본값으로 폴백한다.
func (l *envLoader) listenPort(key, addr, fallback string) string {
	_, port, err := net.SplitHostPort(strings.TrimSpace(addr))
	if err != nil || port == "" {
		l.warnf("%s=%q has no parsable port; the sensor enroll command example falls back to %s",
			key, addr, fallback)
		return fallback
	}
	return port
}

// advertisedHosts는 SENSOR_ADVERTISED_HOSTS(콤마 구분)를 SAN용 DNS 이름과 IP로 나눈다.
// 포트가 붙은 항목("10.0.0.5:9443")과 호스트명 형식이 아닌 항목은 **버리고 경고한다** —
// 예전에는 net.ParseIP에 실패한 값을 전부 DNS 이름으로 받아들여, 포트째로 SAN
// dNSName에 박히고 한참 뒤 센서 접속 단계에서야 TLS 검증 실패로 드러났다.
// 호스트 주소는 secret이 아니므로 어떤 항목이 문제인지 경고에 그대로 지목한다.
func (l *envLoader) advertisedHosts(key, raw string) ([]string, []net.IP) {
	var dnsNames []string
	var ips []net.IP
	for _, h := range strings.Split(raw, ",") {
		h = strings.TrimSpace(h)
		if h == "" {
			continue
		}
		// "[2001:db8::1]"의 대괄호는 주소 표기의 일부가 아니다.
		if len(h) > 2 && strings.HasPrefix(h, "[") && strings.HasSuffix(h, "]") {
			h = h[1 : len(h)-1]
		}
		if ip := net.ParseIP(h); ip != nil {
			ips = append(ips, ip)
			continue
		}
		// IP가 아닌데 host:port로 쪼개진다면 포트를 적은 것이다.
		if _, _, err := net.SplitHostPort(h); err == nil {
			l.warnf("%s entry %q includes a port; list only the DNS name or IP "+
				"(the port comes from SENSOR_LISTEN_ADDR) — entry ignored", key, h)
			continue
		}
		name := strings.TrimSuffix(h, ".")
		if !isDNSName(name) {
			l.warnf("%s entry %q is not a valid DNS name or IP address; entry ignored", key, h)
			continue
		}
		dnsNames = append(dnsNames, name)
	}
	return dnsNames, ips
}

// isDNSName은 SAN dNSName으로 넣어도 되는 호스트명인지 본다 (RFC 1123):
// 라벨은 1~63자의 영숫자·하이픈이고 하이픈으로 시작·끝나지 않으며 전체 253자 이하다.
// 스킴("https://host")·경로·공백·와일드카드는 전부 여기서 걸린다.
func isDNSName(h string) bool {
	if h == "" || len(h) > 253 {
		return false
	}
	for _, label := range strings.Split(h, ".") {
		if len(label) == 0 || len(label) > 63 {
			return false
		}
		if label[0] == '-' || label[len(label)-1] == '-' {
			return false
		}
		for i := 0; i < len(label); i++ {
			c := label[i]
			switch {
			case c >= 'a' && c <= 'z', c >= 'A' && c <= 'Z', c >= '0' && c <= '9', c == '-':
			default:
				return false
			}
		}
	}
	return true
}

// boolVal은 bool 환경변수를 읽는다. 이 헬퍼들은 경고에 값을 그대로 싣으므로
// secret이 담기는 변수에는 절대 쓰지 않는다.
func (l *envLoader) boolVal(key string, fallback bool) bool {
	raw := strings.TrimSpace(os.Getenv(key))
	if raw == "" {
		return fallback
	}
	v, err := strconv.ParseBool(raw)
	if err != nil {
		l.warnf("%s=%q is not a boolean; falling back to %v", key, raw, fallback)
		return fallback
	}
	return v
}

// intVal은 정수 환경변수를 읽고 [min, max] 범위로 강제한다.
// 범위를 벗어나면 상·하한으로 깎고(clamp) 경고한다.
func (l *envLoader) intVal(key string, fallback, minValue, maxValue int) int {
	raw := strings.TrimSpace(os.Getenv(key))
	if raw == "" {
		return fallback
	}
	v, err := strconv.Atoi(raw)
	if err != nil {
		l.warnf("%s=%q is not an integer; falling back to %d", key, raw, fallback)
		return fallback
	}
	switch {
	case v < minValue:
		l.warnf("%s=%d is below the minimum %d; falling back to %d", key, v, minValue, fallback)
		return fallback
	case v > maxValue:
		l.warnf("%s=%d exceeds the maximum %d; clamping to %d", key, v, maxValue, maxValue)
		return maxValue
	}
	return v
}

// durationVal은 Go duration 환경변수를 읽는다. allowZero면 0도 유효한 값이다.
func (l *envLoader) durationVal(key string, fallback time.Duration, allowZero bool) time.Duration {
	raw := strings.TrimSpace(os.Getenv(key))
	if raw == "" {
		return fallback
	}
	v, err := time.ParseDuration(raw)
	if err != nil {
		l.warnf("%s=%q is not a Go duration; falling back to %s", key, raw, fallback)
		return fallback
	}
	if v < 0 || (v == 0 && !allowZero) {
		l.warnf("%s=%s must be positive; falling back to %s", key, raw, fallback)
		return fallback
	}
	return v
}

// notify는 NOTIFY_*/SMTP_*/WEBHOOK_*/SYSLOG_*를 읽는다.
// 여기서는 타입 변환·필수 형식만 검증하고, "값이 비어 채널이 꺼진다"는 판단은
// notify 채널 생성자에 맡긴다 (사유 문자열 출처를 한 곳으로 모으기 위해서다).
func (l *envLoader) notify() NotifyConfig {
	cfg := NotifyConfig{
		Enabled:       l.boolVal("NOTIFY_ENABLED", true),
		ScanInterval:  l.durationVal("NOTIFY_SCAN_INTERVAL", time.Hour, false),
		FirstRun:      FirstRunSeed,
		RetentionDays: l.intVal("NOTIFY_RETENTION_DAYS", 180, 1, 36500),
		BaseURL:       l.baseURL(),
	}
	if raw := strings.ToLower(strings.TrimSpace(os.Getenv("NOTIFY_FIRST_RUN"))); raw != "" {
		switch raw {
		case FirstRunSeed, FirstRunSend:
			cfg.FirstRun = raw
		default:
			l.warnf("NOTIFY_FIRST_RUN=%q must be %s or %s; falling back to %s",
				raw, FirstRunSeed, FirstRunSend, FirstRunSeed)
		}
	}
	cfg.Email = l.email()
	cfg.Webhook = notify.WebhookConfig{
		URL: strings.TrimSpace(os.Getenv("WEBHOOK_URL")),
		// secret 3종은 값 검증 없이 그대로 전달한다 — 경고 문자열에 실릴 여지를 만들지 않는다.
		Secret:     os.Getenv("WEBHOOK_HMAC_SECRET"),
		AuthHeader: os.Getenv("WEBHOOK_AUTH_HEADER"),
		CAFile:     strings.TrimSpace(os.Getenv("WEBHOOK_CA_FILE")),
	}
	cfg.Syslog = l.syslog()
	return cfg
}

// baseURL은 NOTIFY_BASE_URL을 읽는다. 비면 딥링크를 생략할 뿐 오류가 아니다.
func (l *envLoader) baseURL() string {
	raw := strings.TrimSpace(os.Getenv("NOTIFY_BASE_URL"))
	if raw == "" {
		return ""
	}
	if !strings.HasPrefix(raw, "http://") && !strings.HasPrefix(raw, "https://") {
		l.warnf("NOTIFY_BASE_URL=%q must start with http:// or https://; notification deep links are omitted", raw)
		return ""
	}
	return strings.TrimRight(raw, "/")
}

// email은 SMTP_*를 읽는다. 값 오류는 email 채널만 비활성화한다.
func (l *envLoader) email() notify.EmailConfig {
	cfg := notify.EmailConfig{
		Host:     strings.TrimSpace(os.Getenv("SMTP_HOST")),
		From:     strings.TrimSpace(os.Getenv("SMTP_FROM")),
		Username: strings.TrimSpace(os.Getenv("SMTP_USERNAME")),
		Password: os.Getenv("SMTP_PASSWORD"),
		TLSMode:  strings.ToLower(getenv("SMTP_TLS_MODE", notify.TLSModeStartTLS)),
		CAFile:   strings.TrimSpace(os.Getenv("SMTP_CA_FILE")),
	}
	// 포트는 기본값 폴백이 아니라 채널 비활성이다 — 오타 난 포트로 조용히
	// 587에 붙는 것보다 "왜 안 가는지" 사유가 남는 편이 낫다.
	cfg.Port = 587
	if raw := strings.TrimSpace(os.Getenv("SMTP_PORT")); raw != "" {
		port, err := strconv.Atoi(raw)
		if err != nil || port < 1 || port > 65535 {
			l.disableChannel(notify.ChannelEmail, "SMTP_PORT must be an integer between 1 and 65535")
			cfg.Port = 0
		} else {
			cfg.Port = port
		}
	}
	switch cfg.TLSMode {
	case notify.TLSModeStartTLS, notify.TLSModeTLS, notify.TLSModeNone:
	default:
		l.disableChannel(notify.ChannelEmail, "SMTP_TLS_MODE must be one of starttls|tls|none")
	}
	// 파일이 지정되면 파일 내용이 SMTP_PASSWORD보다 우선한다 (Docker secret 경로).
	// 경로는 로그에 남겨도 되지만 내용은 어디에도 남기지 않는다.
	if path := strings.TrimSpace(os.Getenv("SMTP_PASSWORD_FILE")); path != "" {
		b, err := os.ReadFile(path)
		switch {
		case err != nil:
			l.disableChannel(notify.ChannelEmail, fmt.Sprintf("SMTP_PASSWORD_FILE %q is not readable", path))
			cfg.Password = ""
		case len(strings.TrimSpace(string(b))) == 0:
			l.disableChannel(notify.ChannelEmail, fmt.Sprintf("SMTP_PASSWORD_FILE %q is empty", path))
			cfg.Password = ""
		default:
			// secret 파일은 끝에 개행이 붙는 일이 흔하다.
			cfg.Password = strings.TrimSpace(string(b))
		}
	}
	cfg.InsecureSkipVerify = l.boolVal("SMTP_TLS_INSECURE_SKIP_VERIFY", false)
	if cfg.InsecureSkipVerify {
		// 인증서 제품이 스스로 검증을 끄는 설정이다 — 채널은 살리되 반드시 알린다.
		l.warnf("SMTP_TLS_INSECURE_SKIP_VERIFY=true disables SMTP server certificate verification")
	}
	return cfg
}

// syslog는 SYSLOG_*를 읽는다. 네트워크가 udp/tcp가 아니면 syslog 채널만 비활성이다.
func (l *envLoader) syslog() notify.SyslogConfig {
	cfg := notify.SyslogConfig{
		Addr:     strings.TrimSpace(os.Getenv("SYSLOG_ADDR")),
		Network:  strings.ToLower(getenv("SYSLOG_NETWORK", "udp")),
		Facility: strings.ToLower(getenv("SYSLOG_FACILITY", "local0")),
		AppName:  getenv("SYSLOG_APP_NAME", "autocert"),
	}
	if cfg.Network != "udp" && cfg.Network != "tcp" {
		l.disableChannel(notify.ChannelSyslog, "SYSLOG_NETWORK must be udp or tcp")
	}
	return cfg
}

// sweep은 SCAN_SWEEP_*를 읽는다. 값 오류는 전부 경고 + 기본값 폴백이다.
func (l *envLoader) sweep() SweepConfig {
	return SweepConfig{
		Enabled:        l.boolVal("SCAN_SWEEP_ENABLED", true),
		RatePPS:        l.intVal("SCAN_SWEEP_RATE_PPS", 10, 1, sweepMaxRatePPS),
		Concurrency:    l.intVal("SCAN_SWEEP_CONCURRENCY", 8, 1, 1000),
		ChunkEndpoints: l.intVal("SCAN_SWEEP_CHUNK_ENDPOINTS", 4096, 1, 1_000_000),
		ChunkGap:       l.durationVal("SCAN_SWEEP_CHUNK_GAP", 5*time.Second, true),
		MaxDays:        l.intVal("SCAN_SWEEP_MAX_DAYS", 30, 1, 3650),
		StallAfter:     l.durationVal("SCAN_SWEEP_STALL_AFTER", 10*time.Minute, false),
	}
}

// rescan은 RESCAN_*를 읽는다. 값 오류는 전부 경고 + 기본값 폴백이다.
func (l *envLoader) rescan() RescanConfig {
	interval := l.durationVal("RESCAN_INTERVAL", defaultRescanInterval, false)
	if interval < minRescanInterval {
		l.warnf("RESCAN_INTERVAL=%s is below the minimum %s; falling back to %s",
			interval, minRescanInterval, defaultRescanInterval)
		interval = defaultRescanInterval
	}
	return RescanConfig{
		Enabled:  l.boolVal("RESCAN_ENABLED", true),
		Interval: interval,
		// 상한은 스윕과 같은 값을 쓴다 — 둘 다 같은 dial 경로를 지나므로
		// 안전 한계가 다를 이유가 없다.
		RatePPS:        l.intVal("RESCAN_RATE_PPS", 20, 1, sweepMaxRatePPS),
		BatchIPs:       l.intVal("RESCAN_BATCH_IPS", 1024, 1, 65536),
		ReplaceConfirm: l.intVal("RESCAN_REPLACE_CONFIRM", 2, 1, 100),
		RetireMisses:   l.intVal("RESCAN_RETIRE_MISSES", 30, 1, 10000),
	}
}
