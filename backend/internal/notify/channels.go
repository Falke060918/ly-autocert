package notify

import (
	"crypto/tls"
	"crypto/x509"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"strings"
	"sync"
	"time"
)

// 이 패키지는 환경변수를 직접 읽지 않는다 — config 패키지가 읽어 아래 설정 구조체를
// 채워 넣는다. 그래야 테스트가 프로세스 환경에 의존하지 않고, 설정 소스를 한 곳에 모을 수 있다.
// 생성자는 절대 에러를 반환하지 않는다: 알림 설정 오류가 코어 API를 죽이면 안 되므로
// 미설정·오설정은 Enabled() == false + 사유로만 드러난다 (센서 게이트웨이와 같은 원칙).

// TLS 모드 (EmailConfig.TLSMode).
const (
	TLSModeStartTLS = "starttls" // 평문 접속 후 STARTTLS로 승격 (기본)
	TLSModeTLS      = "tls"      // 처음부터 TLS (implicit TLS, 보통 465)
	TLSModeNone     = "none"     // 평문 (폐쇄망 내부 릴레이 전용)
)

const (
	defaultEmailTimeout   = 10 * time.Second
	defaultWebhookTimeout = 10 * time.Second
	defaultSyslogTimeout  = 5 * time.Second
)

// channelState는 모든 채널이 공유하는 활성화 상태다.
type channelState struct {
	// disabledReason이 비어 있으면 활성이다.
	// 이 문자열은 로그·API 응답에 나가므로 secret을 절대 담지 않는다.
	disabledReason string
	log            *slog.Logger
}

func (s channelState) Enabled() (bool, string) {
	if s.disabledReason == "" {
		return true, ""
	}
	return false, s.disabledReason
}

func (s channelState) logger() *slog.Logger {
	if s.log != nil {
		return s.log
	}
	return slog.Default()
}

// EmailConfig는 SMTP 채널 설정이다 (config 패키지가 SMTP_* 환경변수로 채운다).
type EmailConfig struct {
	Host string // SMTP_HOST — 비면 채널 비활성
	Port int    // SMTP_PORT — 0이면 TLSMode에 따라 25/465
	// Username/Password는 SMTP 인증 정보다. Username이 비면 인증을 시도하지 않는다.
	// Password는 secret이다 — 로그·에러·Enabled 사유 어디에도 넣지 않는다.
	Username string
	Password string
	From     string // SMTP_FROM — 비면 채널 비활성
	TLSMode  string // starttls(기본) | tls | none
	// CAFile은 사내 사설 CA PEM 경로다. 지정하면 시스템 신뢰 저장소에 더해 신뢰한다.
	CAFile string
	// InsecureSkipVerify는 서버 인증서 검증을 끈다. 인증서 제품이 스스로 검증을 끄는
	// 설정이므로 켜면 기동 시 경고 로그를 남긴다.
	InsecureSkipVerify bool
	Timeout            time.Duration
	Logger             *slog.Logger
}

// EmailNotifier는 SMTP로 보내는 채널이다.
type EmailNotifier struct {
	channelState
	host     string
	port     int
	username string
	password string
	from     string
	tlsMode  string
	tlsConf  *tls.Config
	timeout  time.Duration
}

// NewEmail은 SMTP 채널을 만든다. 설정이 비거나 잘못돼도 에러를 내지 않고
// Enabled() == false로 표현한다.
func NewEmail(cfg EmailConfig) *EmailNotifier {
	n := &EmailNotifier{
		channelState: channelState{log: cfg.Logger},
		host:         strings.TrimSpace(cfg.Host),
		port:         cfg.Port,
		username:     cfg.Username,
		password:     cfg.Password,
		from:         sanitizeHeaderValue(cfg.From),
		tlsMode:      strings.ToLower(strings.TrimSpace(cfg.TLSMode)),
		timeout:      orDefault(cfg.Timeout, defaultEmailTimeout),
	}
	if n.tlsMode == "" {
		n.tlsMode = TLSModeStartTLS
	}
	switch n.tlsMode {
	case TLSModeStartTLS, TLSModeTLS, TLSModeNone:
	default:
		n.disabledReason = "SMTP_TLS_MODE must be one of starttls|tls|none"
		return n
	}
	if n.port == 0 {
		if n.tlsMode == TLSModeTLS {
			n.port = 465
		} else {
			n.port = 25
		}
	}
	if n.host == "" {
		n.disabledReason = "SMTP_HOST is not set"
		return n
	}
	if n.from == "" {
		n.disabledReason = "SMTP_FROM is not set"
		return n
	}
	if n.tlsMode != TLSModeNone {
		conf, err := tlsConfig(n.host, cfg.CAFile, cfg.InsecureSkipVerify)
		if err != nil {
			n.disabledReason = err.Error()
			return n
		}
		n.tlsConf = conf
		if cfg.InsecureSkipVerify {
			n.logger().Warn("email notifier skips smtp server certificate verification",
				"channel", ChannelEmail, "smtp_host", n.host)
		}
	}
	return n
}

// WebhookConfig는 웹훅 채널 설정이다 (config 패키지가 WEBHOOK_* 환경변수로 채운다).
type WebhookConfig struct {
	URL string // WEBHOOK_URL — 비면 채널 비활성. http/https만 허용
	// Secret은 HMAC-SHA256 서명 키다 (WEBHOOK_SECRET). 설정된 경우에만
	// X-AutoCert-Signature 헤더를 붙인다. secret이므로 로그에 넣지 않는다.
	Secret string
	// AuthHeader는 "Name: value" 형태의 원문 헤더다 (WEBHOOK_AUTH_HEADER).
	// 예: "Authorization: Bearer <token>". value는 secret으로 취급한다.
	AuthHeader         string
	CAFile             string
	InsecureSkipVerify bool
	Timeout            time.Duration
	Logger             *slog.Logger
}

// WebhookNotifier는 JSON을 POST하는 채널이다.
type WebhookNotifier struct {
	channelState
	url         string
	secret      []byte
	authName    string
	authValue   string
	client      *http.Client
	nowOverride func() time.Time // 테스트 전용
}

// NewWebhook은 웹훅 채널을 만든다.
func NewWebhook(cfg WebhookConfig) *WebhookNotifier {
	n := &WebhookNotifier{
		channelState: channelState{log: cfg.Logger},
		url:          strings.TrimSpace(cfg.URL),
		secret:       []byte(cfg.Secret),
	}
	timeout := orDefault(cfg.Timeout, defaultWebhookTimeout)
	if n.url == "" {
		n.disabledReason = "WEBHOOK_URL is not set"
		return n
	}
	if !strings.HasPrefix(n.url, "http://") && !strings.HasPrefix(n.url, "https://") {
		n.disabledReason = "WEBHOOK_URL must start with http:// or https://"
		return n
	}
	if raw := strings.TrimSpace(cfg.AuthHeader); raw != "" {
		name, value, ok := strings.Cut(raw, ":")
		name = strings.TrimSpace(name)
		value = strings.TrimSpace(value)
		// 사유에 헤더 값(토큰)을 절대 싣지 않는다.
		if !ok || name == "" || value == "" {
			n.disabledReason = "WEBHOOK_AUTH_HEADER must be in 'Name: value' form"
			return n
		}
		n.authName = sanitizeHeaderValue(name)
		n.authValue = sanitizeHeaderValue(value)
	}
	conf, err := tlsConfig("", cfg.CAFile, cfg.InsecureSkipVerify)
	if err != nil {
		n.disabledReason = err.Error()
		return n
	}
	if cfg.InsecureSkipVerify {
		n.logger().Warn("webhook notifier skips server certificate verification", "channel", ChannelWebhook)
	}
	n.client = &http.Client{
		Timeout:   timeout,
		Transport: &http.Transport{TLSClientConfig: conf},
	}
	return n
}

// SyslogConfig는 Syslog(RFC5424) 채널 설정이다 (config 패키지가 SYSLOG_* 환경변수로 채운다).
type SyslogConfig struct {
	Network string // SYSLOG_NETWORK — "udp"(기본) | "tcp"
	Addr    string // SYSLOG_ADDR — "host:port". 비면 채널 비활성
	// Facility는 이름(local0, daemon, ...) 또는 숫자(0~23)다. 기본 local0.
	Facility string
	AppName  string // SYSLOG_APP_NAME — RFC5424 APP-NAME. 기본 "autocert"
	Hostname string // 비면 os.Hostname()
	Timeout  time.Duration
	Logger   *slog.Logger
}

// SyslogNotifier는 RFC5424 메시지를 보내는 채널이다.
type SyslogNotifier struct {
	channelState
	network  string
	addr     string
	facility int
	appName  string
	hostname string
	procID   string
	timeout  time.Duration

	mu   sync.Mutex
	conn netConn
	// dial은 테스트에서 교체할 수 있게 분리했다.
	dial func(network, addr string, timeout time.Duration) (netConn, error)
}

// NewSyslog는 Syslog 채널을 만든다.
func NewSyslog(cfg SyslogConfig) *SyslogNotifier {
	n := &SyslogNotifier{
		channelState: channelState{log: cfg.Logger},
		network:      strings.ToLower(strings.TrimSpace(cfg.Network)),
		addr:         strings.TrimSpace(cfg.Addr),
		appName:      strings.TrimSpace(cfg.AppName),
		hostname:     strings.TrimSpace(cfg.Hostname),
		procID:       fmt.Sprintf("%d", os.Getpid()),
		timeout:      orDefault(cfg.Timeout, defaultSyslogTimeout),
		dial:         dialNet,
	}
	if n.network == "" {
		n.network = "udp"
	}
	if n.appName == "" {
		n.appName = "autocert"
	}
	if n.hostname == "" {
		if h, err := os.Hostname(); err == nil {
			n.hostname = h
		} else {
			n.hostname = nilValue
		}
	}
	if n.network != "udp" && n.network != "tcp" {
		n.disabledReason = "SYSLOG_NETWORK must be udp or tcp"
		return n
	}
	facility, err := parseFacility(cfg.Facility)
	if err != nil {
		n.disabledReason = err.Error()
		return n
	}
	n.facility = facility
	if n.addr == "" {
		n.disabledReason = "SYSLOG_ADDR is not set"
		return n
	}
	return n
}

// tlsConfig는 채널 공용 TLS 설정을 만든다. caFile이 있으면 시스템 신뢰 저장소에
// 더해 사내 사설 CA를 신뢰한다 (폐쇄망 전제).
func tlsConfig(serverName, caFile string, insecure bool) (*tls.Config, error) {
	conf := &tls.Config{
		MinVersion:         tls.VersionTLS12,
		ServerName:         serverName,
		InsecureSkipVerify: insecure, //nolint:gosec // 운영자가 명시적으로 켠 경우만. 켜면 경고 로그를 남긴다.
	}
	if caFile == "" {
		return conf, nil
	}
	pool, err := loadRootCAs(caFile)
	if err != nil {
		return nil, err
	}
	conf.RootCAs = pool
	return conf, nil
}

// loadRootCAs는 PEM 파일을 읽어 신뢰 저장소를 만든다.
// 에러 문자열은 경로만 담는다 — 파일 내용(인증서 바이트)을 싣지 않는다 (하드 규칙 3).
func loadRootCAs(caFile string) (*x509.CertPool, error) {
	pem, err := os.ReadFile(caFile)
	if err != nil {
		return nil, fmt.Errorf("ca file %q is not readable", caFile)
	}
	pool, err := x509.SystemCertPool()
	if err != nil || pool == nil {
		// 폐쇄망 이미지에는 시스템 신뢰 저장소가 없을 수 있다. 사내 CA만으로 진행한다.
		pool = x509.NewCertPool()
	}
	if !pool.AppendCertsFromPEM(pem) {
		return nil, fmt.Errorf("ca file %q contains no certificate PEM block", caFile)
	}
	return pool, nil
}

// syslogFacilities는 RFC5424 facility 이름 → 숫자다.
var syslogFacilities = map[string]int{
	"kern": 0, "user": 1, "mail": 2, "daemon": 3, "auth": 4, "syslog": 5,
	"lpr": 6, "news": 7, "uucp": 8, "cron": 9, "authpriv": 10, "ftp": 11,
	"ntp": 12, "security": 13, "console": 14, "solaris-cron": 15,
	"local0": 16, "local1": 17, "local2": 18, "local3": 19,
	"local4": 20, "local5": 21, "local6": 22, "local7": 23,
}

func parseFacility(s string) (int, error) {
	s = strings.ToLower(strings.TrimSpace(s))
	if s == "" {
		return syslogFacilities["local0"], nil
	}
	if v, ok := syslogFacilities[s]; ok {
		return v, nil
	}
	var n int
	if _, err := fmt.Sscanf(s, "%d", &n); err == nil && n >= 0 && n <= 23 && fmt.Sprintf("%d", n) == s {
		return n, nil
	}
	return 0, fmt.Errorf("SYSLOG_FACILITY must be a facility name (e.g. local0) or a number 0-23")
}

// sanitizeHeaderValue는 CR/LF를 제거한다 — 메일 헤더·HTTP 헤더 주입 방지.
func sanitizeHeaderValue(s string) string {
	s = strings.ReplaceAll(s, "\r", "")
	s = strings.ReplaceAll(s, "\n", "")
	return strings.TrimSpace(s)
}

func orDefault(d, fallback time.Duration) time.Duration {
	if d <= 0 {
		return fallback
	}
	return d
}
