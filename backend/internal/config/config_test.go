package config

import (
	"net"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Falke060918/INHERIT_AutoCert/backend/internal/notify"
)

// setRequired는 Load가 요구하는 필수 환경변수를 채우고 센서 변수를 비운다.
// 센서 변수를 비우는 이유: 잘못된 값이 프로세스 환경에 있으면 경고가 섞여
// "경고 없음"을 기대하는 테스트가 환경에 따라 흔들린다.
func setRequired(t *testing.T) {
	t.Helper()
	t.Setenv("DATABASE_URL", "postgres://u:p@localhost:5432/db?sslmode=disable")
	for _, k := range []string{"SENSOR_CA_ENC_KEY", "SENSOR_LISTEN_ADDR", "SENSOR_ADVERTISED_HOSTS"} {
		t.Setenv(k, "")
	}
}

func TestLoadSensorListenAddrDefault(t *testing.T) {
	setRequired(t)
	t.Setenv("SENSOR_LISTEN_ADDR", "")
	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.SensorListenAddr != ":8443" {
		t.Fatalf("SensorListenAddr = %q, want %q", cfg.SensorListenAddr, ":8443")
	}
}

func TestLoadSensorListenAddrOverride(t *testing.T) {
	setRequired(t)
	t.Setenv("SENSOR_LISTEN_ADDR", ":9443")
	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.SensorListenAddr != ":9443" {
		t.Fatalf("SensorListenAddr = %q, want %q", cfg.SensorListenAddr, ":9443")
	}
}

// TestLoadSessionCookieSecure는 SESSION_COOKIE_SECURE의 기본값(평문 HTTP 기본 설치를
// 깨지 않도록 false)과 명시 설정, 오값 폴백을 검증한다.
func TestLoadSessionCookieSecure(t *testing.T) {
	tests := []struct {
		name        string
		value       string
		want        bool
		wantWarning bool
	}{
		{name: "default is off", value: "", want: false},
		{name: "explicit on", value: "true", want: true},
		{name: "explicit off", value: "false", want: false},
		{name: "invalid falls back with warning", value: "https", want: false, wantWarning: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			setRequired(t)
			t.Setenv("SESSION_COOKIE_SECURE", tt.value)
			cfg, err := Load()
			if err != nil {
				t.Fatalf("Load: %v", err)
			}
			if cfg.SessionCookieSecure != tt.want {
				t.Fatalf("SessionCookieSecure = %v, want %v", cfg.SessionCookieSecure, tt.want)
			}
			warned := strings.Contains(strings.Join(cfg.Warnings, "\n"), "SESSION_COOKIE_SECURE")
			if warned != tt.wantWarning {
				t.Fatalf("warning about SESSION_COOKIE_SECURE = %v, want %v (warnings: %v)",
					warned, tt.wantWarning, cfg.Warnings)
			}
		})
	}
}

func TestLoadSensorAdvertisedHosts(t *testing.T) {
	setRequired(t)
	// 콤마 구분, 공백 허용, DNS와 IP 혼합 — IP는 SAN IP로, 나머지는 DNS로 분류.
	t.Setenv("SENSOR_ADVERTISED_HOSTS", "autocert.internal, 10.0.0.5 ,gw.example.com,")
	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	wantDNS := []string{"autocert.internal", "gw.example.com"}
	if len(cfg.SensorAdvertisedDNS) != len(wantDNS) {
		t.Fatalf("SensorAdvertisedDNS = %v, want %v", cfg.SensorAdvertisedDNS, wantDNS)
	}
	for i, d := range wantDNS {
		if cfg.SensorAdvertisedDNS[i] != d {
			t.Fatalf("SensorAdvertisedDNS = %v, want %v", cfg.SensorAdvertisedDNS, wantDNS)
		}
	}
	if len(cfg.SensorAdvertisedIPs) != 1 || !cfg.SensorAdvertisedIPs[0].Equal(net.ParseIP("10.0.0.5")) {
		t.Fatalf("SensorAdvertisedIPs = %v, want [10.0.0.5]", cfg.SensorAdvertisedIPs)
	}
}

// TestLoadInvalidSensorCAEncKeyIsNotFatal은 센서 전용 설정 오류가 코어 기동을
// 막지 않는지 본다: Load는 성공하고, 키는 비고, 오류만 따로 실려 온다
// (cmd/server가 경고 후 게이트웨이만 비활성화한다).
func TestLoadInvalidSensorCAEncKeyIsNotFatal(t *testing.T) {
	setRequired(t)
	t.Setenv("SENSOR_CA_ENC_KEY", "not-hex")
	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load must not fail on a bad sensor key: %v", err)
	}
	if cfg.SensorCAEncKey != nil {
		t.Error("SensorCAEncKey must stay nil when the value is invalid")
	}
	if cfg.SensorConfigError == nil {
		t.Fatal("SensorConfigError = nil, want the format requirement")
	}
	if !strings.Contains(cfg.SensorConfigError.Error(), "SENSOR_CA_ENC_KEY") {
		t.Errorf("SensorConfigError = %q, want it to name the variable", cfg.SensorConfigError)
	}
	if strings.Contains(cfg.SensorConfigError.Error(), "not-hex") {
		t.Error("SensorConfigError must not echo the configured value")
	}
}

// TestLoadSensorGatewayPort는 등록 명령 예시에 쓰이는 포트가 SENSOR_LISTEN_ADDR를
// 따라가는지 본다 — 예시가 8443으로 굳어 있으면 포트를 바꾼 배치에서 명령을
// 그대로 복사한 운영자가 접속에 실패한다.
func TestLoadSensorGatewayPort(t *testing.T) {
	tests := []struct {
		addr     string
		want     string
		wantWarn bool
	}{
		{addr: "", want: defaultSensorPort},
		{addr: ":9443", want: "9443"},
		{addr: "0.0.0.0:9443", want: "9443"},
		{addr: "[::1]:9443", want: "9443"},
		// 포트가 없는 주소는 리슨 자체가 실패할 값이다 — 예시는 기본 포트로 폴백하고 경고한다.
		{addr: "0.0.0.0", want: defaultSensorPort, wantWarn: true},
	}
	for _, tc := range tests {
		t.Run(tc.addr, func(t *testing.T) {
			setRequired(t)
			t.Setenv("SENSOR_LISTEN_ADDR", tc.addr)
			cfg, err := Load()
			if err != nil {
				t.Fatalf("Load: %v", err)
			}
			if cfg.SensorGatewayPort != tc.want {
				t.Errorf("SensorGatewayPort = %q, want %q", cfg.SensorGatewayPort, tc.want)
			}
			warned := strings.Contains(strings.Join(cfg.Warnings, "\n"), "SENSOR_LISTEN_ADDR")
			if warned != tc.wantWarn {
				t.Errorf("warned = %v, want %v (warnings=%v)", warned, tc.wantWarn, cfg.Warnings)
			}
		})
	}
}

// TestLoadSensorAdvertisedHostsRejectsInvalid는 포트가 붙었거나 호스트명이 아닌
// 항목을 기동 시점에 걸러내는지 본다. 예전에는 net.ParseIP 실패분을 전부 DNS 이름으로
// 받아 "10.0.0.5:9443"이 통째로 SAN에 박혔고, 한참 뒤 센서 접속에서야 TLS 검증
// 실패로 드러났다. 유효한 항목은 그대로 살아야 한다.
func TestLoadSensorAdvertisedHostsRejectsInvalid(t *testing.T) {
	setRequired(t)
	t.Setenv("SENSOR_ADVERTISED_HOSTS",
		"10.0.0.5:9443, autocert.internal, https://gw.example.com, gw..example.com, -bad.example.com, ok-1.example.com., [2001:db8::1]:8443, 10.0.0.6")
	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	wantDNS := []string{"autocert.internal", "ok-1.example.com"}
	if len(cfg.SensorAdvertisedDNS) != len(wantDNS) {
		t.Fatalf("SensorAdvertisedDNS = %v, want %v", cfg.SensorAdvertisedDNS, wantDNS)
	}
	for i, d := range wantDNS {
		if cfg.SensorAdvertisedDNS[i] != d {
			t.Fatalf("SensorAdvertisedDNS = %v, want %v", cfg.SensorAdvertisedDNS, wantDNS)
		}
	}
	if len(cfg.SensorAdvertisedIPs) != 1 || !cfg.SensorAdvertisedIPs[0].Equal(net.ParseIP("10.0.0.6")) {
		t.Fatalf("SensorAdvertisedIPs = %v, want [10.0.0.6]", cfg.SensorAdvertisedIPs)
	}
	joined := strings.Join(cfg.Warnings, "\n")
	for _, bad := range []string{"10.0.0.5:9443", "https://gw.example.com", "gw..example.com",
		"-bad.example.com", "[2001:db8::1]:8443"} {
		if !strings.Contains(joined, bad) {
			t.Errorf("warnings do not name the rejected entry %q: %v", bad, cfg.Warnings)
		}
	}
	if !strings.Contains(joined, "SENSOR_LISTEN_ADDR") {
		t.Errorf("the port-attached entry should point at SENSOR_LISTEN_ADDR: %v", cfg.Warnings)
	}
	// 대괄호 표기의 IPv6 리터럴(포트 없음)은 유효한 값이다.
	setRequired(t)
	t.Setenv("SENSOR_ADVERTISED_HOSTS", "[2001:db8::1], localhost")
	cfg, err = Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if len(cfg.SensorAdvertisedIPs) != 1 || !cfg.SensorAdvertisedIPs[0].Equal(net.ParseIP("2001:db8::1")) {
		t.Errorf("SensorAdvertisedIPs = %v, want [2001:db8::1]", cfg.SensorAdvertisedIPs)
	}
	if len(cfg.SensorAdvertisedDNS) != 1 || cfg.SensorAdvertisedDNS[0] != "localhost" {
		t.Errorf("SensorAdvertisedDNS = %v, want [localhost]", cfg.SensorAdvertisedDNS)
	}
	if len(cfg.Warnings) != 0 {
		t.Errorf("valid hosts must not warn: %v", cfg.Warnings)
	}
}

func TestLoadSensorAdvertisedHostsEmpty(t *testing.T) {
	setRequired(t)
	t.Setenv("SENSOR_ADVERTISED_HOSTS", "")
	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if len(cfg.SensorAdvertisedDNS) != 0 || len(cfg.SensorAdvertisedIPs) != 0 {
		t.Fatalf("advertised hosts should be empty, got dns=%v ips=%v",
			cfg.SensorAdvertisedDNS, cfg.SensorAdvertisedIPs)
	}
}

// notifySweepEnv는 알림·스윕 환경변수 전체 목록이다. 테스트는 프로세스 환경에
// 이미 있는 값에 영향받으면 안 되므로 매 테스트에서 전부 비운다.
var notifySweepEnv = []string{
	"NOTIFY_ENABLED", "NOTIFY_SCAN_INTERVAL", "NOTIFY_FIRST_RUN",
	"NOTIFY_RETENTION_DAYS", "NOTIFY_BASE_URL",
	"SMTP_HOST", "SMTP_PORT", "SMTP_FROM", "SMTP_USERNAME", "SMTP_PASSWORD",
	"SMTP_PASSWORD_FILE", "SMTP_TLS_MODE", "SMTP_CA_FILE", "SMTP_TLS_INSECURE_SKIP_VERIFY",
	"WEBHOOK_URL", "WEBHOOK_HMAC_SECRET", "WEBHOOK_AUTH_HEADER", "WEBHOOK_CA_FILE",
	"SYSLOG_ADDR", "SYSLOG_NETWORK", "SYSLOG_FACILITY", "SYSLOG_APP_NAME",
	"SCAN_SWEEP_ENABLED", "SCAN_SWEEP_RATE_PPS", "SCAN_SWEEP_CONCURRENCY",
	"SCAN_SWEEP_CHUNK_ENDPOINTS", "SCAN_SWEEP_CHUNK_GAP", "SCAN_SWEEP_MAX_DAYS",
	"SCAN_SWEEP_STALL_AFTER",
}

// setNotifyRequired는 필수 변수를 채우고 알림·스윕 변수를 모두 비운다.
func setNotifyRequired(t *testing.T) {
	t.Helper()
	setRequired(t)
	for _, k := range notifySweepEnv {
		t.Setenv(k, "")
	}
}

func TestLoadNotifyDefaults(t *testing.T) {
	setNotifyRequired(t)
	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if !cfg.Notify.Enabled {
		t.Error("Notify.Enabled = false, want true")
	}
	if cfg.Notify.ScanInterval != time.Hour {
		t.Errorf("Notify.ScanInterval = %v, want 1h", cfg.Notify.ScanInterval)
	}
	if cfg.Notify.FirstRun != FirstRunSeed {
		t.Errorf("Notify.FirstRun = %q, want %q", cfg.Notify.FirstRun, FirstRunSeed)
	}
	if cfg.Notify.RetentionDays != 180 {
		t.Errorf("Notify.RetentionDays = %d, want 180", cfg.Notify.RetentionDays)
	}
	if cfg.Notify.BaseURL != "" {
		t.Errorf("Notify.BaseURL = %q, want empty", cfg.Notify.BaseURL)
	}
	if cfg.Notify.Email.Port != 587 {
		t.Errorf("Email.Port = %d, want 587", cfg.Notify.Email.Port)
	}
	if cfg.Notify.Email.TLSMode != notify.TLSModeStartTLS {
		t.Errorf("Email.TLSMode = %q, want %q", cfg.Notify.Email.TLSMode, notify.TLSModeStartTLS)
	}
	if cfg.Notify.Syslog.Network != "udp" {
		t.Errorf("Syslog.Network = %q, want udp", cfg.Notify.Syslog.Network)
	}
	if cfg.Notify.Syslog.Facility != "local0" {
		t.Errorf("Syslog.Facility = %q, want local0", cfg.Notify.Syslog.Facility)
	}
	if cfg.Notify.Syslog.AppName != "autocert" {
		t.Errorf("Syslog.AppName = %q, want autocert", cfg.Notify.Syslog.AppName)
	}
	// 미설정은 오류가 아니다 — 채널이 그냥 꺼져 있을 뿐이며 그 사유는 notify가 낸다.
	if len(cfg.NotifyChannelErrors) != 0 {
		t.Errorf("NotifyChannelErrors = %v, want empty", cfg.NotifyChannelErrors)
	}
	if len(cfg.Warnings) != 0 {
		t.Errorf("Warnings = %v, want empty", cfg.Warnings)
	}
}

func TestLoadSweepDefaults(t *testing.T) {
	setNotifyRequired(t)
	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	want := SweepConfig{
		Enabled:        true,
		RatePPS:        10,
		Concurrency:    8,
		ChunkEndpoints: 4096,
		ChunkGap:       5 * time.Second,
		MaxDays:        30,
		StallAfter:     10 * time.Minute,
	}
	if cfg.Sweep != want {
		t.Errorf("Sweep = %+v, want %+v", cfg.Sweep, want)
	}
}

func TestLoadNotifyAndSweepValuesPassThrough(t *testing.T) {
	setNotifyRequired(t)
	t.Setenv("NOTIFY_ENABLED", "false")
	t.Setenv("NOTIFY_SCAN_INTERVAL", "15m")
	t.Setenv("NOTIFY_FIRST_RUN", "send")
	t.Setenv("NOTIFY_RETENTION_DAYS", "30")
	t.Setenv("NOTIFY_BASE_URL", "https://autocert.internal/")
	t.Setenv("SMTP_HOST", "smtp.internal")
	t.Setenv("SMTP_PORT", "2525")
	t.Setenv("SMTP_FROM", "autocert@internal")
	t.Setenv("SMTP_USERNAME", "svc-autocert")
	t.Setenv("SMTP_TLS_MODE", "tls")
	t.Setenv("SMTP_CA_FILE", "/etc/ssl/corp.pem")
	t.Setenv("WEBHOOK_URL", "https://hook.internal/autocert")
	t.Setenv("WEBHOOK_AUTH_HEADER", "Authorization: Bearer t")
	t.Setenv("WEBHOOK_CA_FILE", "/etc/ssl/corp.pem")
	t.Setenv("SYSLOG_ADDR", "10.0.0.9:514")
	t.Setenv("SYSLOG_NETWORK", "tcp")
	t.Setenv("SYSLOG_FACILITY", "local3")
	t.Setenv("SYSLOG_APP_NAME", "autocert-prod")
	t.Setenv("SCAN_SWEEP_ENABLED", "false")
	t.Setenv("SCAN_SWEEP_RATE_PPS", "25")
	t.Setenv("SCAN_SWEEP_CONCURRENCY", "4")
	t.Setenv("SCAN_SWEEP_CHUNK_ENDPOINTS", "1024")
	t.Setenv("SCAN_SWEEP_CHUNK_GAP", "2s")
	t.Setenv("SCAN_SWEEP_MAX_DAYS", "7")
	t.Setenv("SCAN_SWEEP_STALL_AFTER", "3m")

	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if len(cfg.Warnings) != 0 || len(cfg.NotifyChannelErrors) != 0 {
		t.Fatalf("valid values must not warn: warnings=%v errors=%v", cfg.Warnings, cfg.NotifyChannelErrors)
	}
	if cfg.Notify.Enabled {
		t.Error("Notify.Enabled = true, want false")
	}
	if cfg.Notify.ScanInterval != 15*time.Minute || cfg.Notify.FirstRun != FirstRunSend || cfg.Notify.RetentionDays != 30 {
		t.Errorf("Notify common = %+v", cfg.Notify)
	}
	// 딥링크를 붙일 때 슬래시가 겹치지 않도록 끝의 "/"는 떼어낸다.
	if cfg.Notify.BaseURL != "https://autocert.internal" {
		t.Errorf("Notify.BaseURL = %q, want %q", cfg.Notify.BaseURL, "https://autocert.internal")
	}
	email := cfg.Notify.Email
	if email.Host != "smtp.internal" || email.Port != 2525 || email.From != "autocert@internal" ||
		email.Username != "svc-autocert" || email.TLSMode != notify.TLSModeTLS || email.CAFile != "/etc/ssl/corp.pem" {
		t.Errorf("Email = %+v", email)
	}
	hook := cfg.Notify.Webhook
	if hook.URL != "https://hook.internal/autocert" || hook.AuthHeader != "Authorization: Bearer t" ||
		hook.CAFile != "/etc/ssl/corp.pem" {
		t.Errorf("Webhook = %+v", hook)
	}
	sl := cfg.Notify.Syslog
	if sl.Addr != "10.0.0.9:514" || sl.Network != "tcp" || sl.Facility != "local3" || sl.AppName != "autocert-prod" {
		t.Errorf("Syslog = %+v", sl)
	}
	wantSweep := SweepConfig{
		Enabled: false, RatePPS: 25, Concurrency: 4, ChunkEndpoints: 1024,
		ChunkGap: 2 * time.Second, MaxDays: 7, StallAfter: 3 * time.Minute,
	}
	if cfg.Sweep != wantSweep {
		t.Errorf("Sweep = %+v, want %+v", cfg.Sweep, wantSweep)
	}
}

// TestLoadInvalidValuesFallBackWithWarning은 값 오류가 기동을 막지 않고
// 기본값 폴백 + 변수명을 지목하는 경고로만 드러나는지 본다.
func TestLoadInvalidValuesFallBackWithWarning(t *testing.T) {
	setNotifyRequired(t)
	t.Setenv("NOTIFY_ENABLED", "yes-please")
	t.Setenv("NOTIFY_SCAN_INTERVAL", "soon")
	t.Setenv("NOTIFY_FIRST_RUN", "maybe")
	t.Setenv("NOTIFY_RETENTION_DAYS", "-5")
	t.Setenv("NOTIFY_BASE_URL", "autocert.internal")
	t.Setenv("SCAN_SWEEP_RATE_PPS", "fast")
	t.Setenv("SCAN_SWEEP_CONCURRENCY", "0")
	t.Setenv("SCAN_SWEEP_CHUNK_ENDPOINTS", "-1")
	t.Setenv("SCAN_SWEEP_CHUNK_GAP", "later")
	t.Setenv("SCAN_SWEEP_MAX_DAYS", "0")
	t.Setenv("SCAN_SWEEP_STALL_AFTER", "-1m")

	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load must not fail on bad notify/sweep values: %v", err)
	}
	if !cfg.Notify.Enabled || cfg.Notify.ScanInterval != time.Hour ||
		cfg.Notify.FirstRun != FirstRunSeed || cfg.Notify.RetentionDays != 180 {
		t.Errorf("Notify did not fall back to defaults: %+v", cfg.Notify)
	}
	if cfg.Notify.BaseURL != "" {
		t.Errorf("Notify.BaseURL = %q, want empty (link omitted)", cfg.Notify.BaseURL)
	}
	want := SweepConfig{
		Enabled: true, RatePPS: 10, Concurrency: 8, ChunkEndpoints: 4096,
		ChunkGap: 5 * time.Second, MaxDays: 30, StallAfter: 10 * time.Minute,
	}
	if cfg.Sweep != want {
		t.Errorf("Sweep did not fall back to defaults: %+v", cfg.Sweep)
	}
	joined := strings.Join(cfg.Warnings, "\n")
	for _, name := range []string{
		"NOTIFY_ENABLED", "NOTIFY_SCAN_INTERVAL", "NOTIFY_FIRST_RUN", "NOTIFY_RETENTION_DAYS",
		"NOTIFY_BASE_URL", "SCAN_SWEEP_RATE_PPS", "SCAN_SWEEP_CONCURRENCY",
		"SCAN_SWEEP_CHUNK_ENDPOINTS", "SCAN_SWEEP_CHUNK_GAP", "SCAN_SWEEP_MAX_DAYS",
		"SCAN_SWEEP_STALL_AFTER",
	} {
		if !strings.Contains(joined, name) {
			t.Errorf("warnings do not name %s: %v", name, cfg.Warnings)
		}
	}
}

func TestLoadSweepRateIsClamped(t *testing.T) {
	setNotifyRequired(t)
	t.Setenv("SCAN_SWEEP_RATE_PPS", "5000")
	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.Sweep.RatePPS != 1000 {
		t.Errorf("Sweep.RatePPS = %d, want the 1000 clamp", cfg.Sweep.RatePPS)
	}
	if !strings.Contains(strings.Join(cfg.Warnings, "\n"), "SCAN_SWEEP_RATE_PPS") {
		t.Errorf("clamping must warn, got %v", cfg.Warnings)
	}
}

// TestLoadInvalidSMTPPortDisablesEmailOnly는 채널 하나의 설정 오류가
// 코어 API도, 다른 채널도 죽이지 않는지 본다 (센서 게이트웨이 선례).
func TestLoadInvalidSMTPPortDisablesEmailOnly(t *testing.T) {
	setNotifyRequired(t)
	t.Setenv("SMTP_HOST", "smtp.internal")
	t.Setenv("SMTP_FROM", "autocert@internal")
	t.Setenv("SMTP_PORT", "not-a-number")
	t.Setenv("SYSLOG_ADDR", "10.0.0.9:514")

	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load must not fail on a bad SMTP_PORT: %v", err)
	}
	reason := cfg.NotifyChannelErrors[notify.ChannelEmail]
	if reason == "" {
		t.Fatal("NotifyChannelErrors[email] is empty, want the SMTP_PORT cause")
	}
	if !strings.Contains(reason, "SMTP_PORT") {
		t.Errorf("reason = %q, want it to name SMTP_PORT", reason)
	}
	if _, ok := cfg.NotifyChannelErrors[notify.ChannelSyslog]; ok {
		t.Error("a bad SMTP_PORT must not disable the syslog channel")
	}
	if _, ok := cfg.NotifyChannelErrors[notify.ChannelWebhook]; ok {
		t.Error("a bad SMTP_PORT must not disable the webhook channel")
	}
}

func TestLoadInvalidSyslogNetworkDisablesSyslogOnly(t *testing.T) {
	setNotifyRequired(t)
	t.Setenv("SYSLOG_ADDR", "10.0.0.9:514")
	t.Setenv("SYSLOG_NETWORK", "sctp")
	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load must not fail on a bad SYSLOG_NETWORK: %v", err)
	}
	reason := cfg.NotifyChannelErrors[notify.ChannelSyslog]
	if !strings.Contains(reason, "SYSLOG_NETWORK") {
		t.Errorf("NotifyChannelErrors[syslog] = %q, want it to name SYSLOG_NETWORK", reason)
	}
	if _, ok := cfg.NotifyChannelErrors[notify.ChannelEmail]; ok {
		t.Error("a bad SYSLOG_NETWORK must not disable the email channel")
	}
}

func TestLoadSMTPPasswordFileWins(t *testing.T) {
	setNotifyRequired(t)
	path := filepath.Join(t.TempDir(), "smtp-password")
	// Docker secret 파일은 끝에 개행이 붙는 일이 흔하다 — 잘라내야 인증이 된다.
	if err := os.WriteFile(path, []byte("from-file-secret\n"), 0o600); err != nil {
		t.Fatalf("write password file: %v", err)
	}
	t.Setenv("SMTP_HOST", "smtp.internal")
	t.Setenv("SMTP_FROM", "autocert@internal")
	t.Setenv("SMTP_PASSWORD", "from-env-secret")
	t.Setenv("SMTP_PASSWORD_FILE", path)

	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.Notify.Email.Password != "from-file-secret" {
		t.Errorf("Email.Password did not come from SMTP_PASSWORD_FILE")
	}
	if len(cfg.NotifyChannelErrors) != 0 {
		t.Errorf("NotifyChannelErrors = %v, want empty", cfg.NotifyChannelErrors)
	}
}

func TestLoadSMTPPasswordFileUnreadableDisablesEmail(t *testing.T) {
	setNotifyRequired(t)
	path := filepath.Join(t.TempDir(), "missing-secret")
	t.Setenv("SMTP_HOST", "smtp.internal")
	t.Setenv("SMTP_FROM", "autocert@internal")
	t.Setenv("SMTP_PASSWORD", "from-env-secret")
	t.Setenv("SMTP_PASSWORD_FILE", path)

	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load must not fail on an unreadable password file: %v", err)
	}
	reason := cfg.NotifyChannelErrors[notify.ChannelEmail]
	if !strings.Contains(reason, "SMTP_PASSWORD_FILE") {
		t.Errorf("NotifyChannelErrors[email] = %q, want it to name SMTP_PASSWORD_FILE", reason)
	}
	if cfg.Notify.Email.Password != "" {
		t.Error("Email.Password must stay empty when the password file is unreadable")
	}
}

func TestLoadInsecureSkipVerifyWarns(t *testing.T) {
	setNotifyRequired(t)
	t.Setenv("SMTP_HOST", "smtp.internal")
	t.Setenv("SMTP_FROM", "autocert@internal")
	t.Setenv("SMTP_TLS_INSECURE_SKIP_VERIFY", "true")
	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if !cfg.Notify.Email.InsecureSkipVerify {
		t.Fatal("Email.InsecureSkipVerify = false, want true")
	}
	if !strings.Contains(strings.Join(cfg.Warnings, "\n"), "SMTP_TLS_INSECURE_SKIP_VERIFY") {
		t.Errorf("turning off certificate verification must warn, got %v", cfg.Warnings)
	}
	// 검증을 끈 것은 채널을 끄는 사유는 아니다.
	if len(cfg.NotifyChannelErrors) != 0 {
		t.Errorf("NotifyChannelErrors = %v, want empty", cfg.NotifyChannelErrors)
	}
}

// TestLoadWarningsNeverContainSecrets는 경고·채널 사유 문자열에 비밀값이
// 섞이지 않는지 본다. 이 문자열들은 로그와 API 응답으로 나간다 (하드 규칙 3).
func TestLoadWarningsNeverContainSecrets(t *testing.T) {
	setNotifyRequired(t)
	const (
		envPassword  = "env-password-9f3a"
		filePassword = "file-password-7c1b"
		hmacSecret   = "hmac-secret-4d2e"
		authToken    = "auth-token-8b5c"
	)
	path := filepath.Join(t.TempDir(), "smtp-password")
	if err := os.WriteFile(path, []byte(filePassword), 0o600); err != nil {
		t.Fatalf("write password file: %v", err)
	}
	t.Setenv("SMTP_HOST", "smtp.internal")
	t.Setenv("SMTP_FROM", "autocert@internal")
	t.Setenv("SMTP_PASSWORD", envPassword)
	t.Setenv("SMTP_PASSWORD_FILE", path)
	t.Setenv("SMTP_PORT", "not-a-number") // 경고를 강제로 만든다
	t.Setenv("SMTP_TLS_MODE", "quantum")
	t.Setenv("WEBHOOK_URL", "ftp://hook.internal")
	t.Setenv("WEBHOOK_HMAC_SECRET", hmacSecret)
	t.Setenv("WEBHOOK_AUTH_HEADER", "Authorization: Bearer "+authToken)
	t.Setenv("SYSLOG_NETWORK", "sctp")
	t.Setenv("SCAN_SWEEP_RATE_PPS", "fast")

	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	msgs := append([]string{}, cfg.Warnings...)
	for _, reason := range cfg.NotifyChannelErrors {
		msgs = append(msgs, reason)
	}
	if len(msgs) == 0 {
		t.Fatal("expected warnings for this configuration")
	}
	joined := strings.Join(msgs, "\n")
	for _, secret := range []string{envPassword, filePassword, hmacSecret, authToken} {
		if strings.Contains(joined, secret) {
			t.Errorf("a warning leaked a secret value: %s", joined)
		}
	}
}
