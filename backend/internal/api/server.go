// Package api는 HTTP 라우팅과 핸들러를 담당한다.
// 라우터는 표준 net/http의 패턴 매칭(Go 1.22+)만 사용한다.
//
// 라우트 등록 규칙: public()과 protected() 둘뿐이다. protected는 허용 역할을
// 명시해야 하며(빈 목록은 panic), 새 API가 인증·권한 검사를 빠뜨릴 수 없게 한다
// (CLAUDE.md 보안 하드 규칙 6).
package api

import (
	"context"
	"database/sql"
	"encoding/json"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"github.com/Falke060918/INHERIT_AutoCert/backend/internal/audit"
	"github.com/Falke060918/INHERIT_AutoCert/backend/internal/auth"
	"github.com/Falke060918/INHERIT_AutoCert/backend/internal/discovery"
	"github.com/Falke060918/INHERIT_AutoCert/backend/internal/domain"
	"github.com/Falke060918/INHERIT_AutoCert/backend/internal/issuance"
	"github.com/Falke060918/INHERIT_AutoCert/backend/internal/store"
)

// Server는 라우터와 의존성(인증 서비스·감사 기록기)을 묶는다.
// routes는 등록된 "METHOD /path" 패턴 전체다 — OpenAPI 명세 정합 테스트가
// 이 목록과 api/openapi.yaml을 대조한다 (openapi_conformance_test.go).
type Server struct {
	issuance *issuance.Service
	mux      *http.ServeMux
	pool     *sql.DB
	store    *store.Store
	auth     *auth.Service
	audit    *audit.Recorder
	routes   []string

	// 아래 다섯은 배선(cmd/server)이 있어야 채워지는 선택 의존성이다.
	// 비어 있어도 코어 API는 정상 동작한다 (05-notifications 10.1 실패 격리).
	channels      NotificationChannels
	sweepDefaults SweepDefaults
	// rescan은 주기 재스캔 스케줄러다. nil이면 상태 조회가 "미배선"으로 답하고
	// 수동 1배치 실행은 503이다 (06-rescan-and-replacement 11.2).
	rescan RescanController
	// schedules는 정기 스캔 스케줄러다(0019). nil이면 조회·설정 변경은 그대로
	// 동작하고 '지금 실행'·취소만 503이다 — 재스캔과 같은 실패 격리 규약이다.
	schedules ScheduleController
	// sensorGatewayPort는 센서 등록 명령 예시에 넣을 게이트웨이 포트다.
	// 기본값은 defaultSensorGatewayPort이고 WithSensorGatewayPort가 덮어쓴다.
	sensorGatewayPort string
	// scanSuggestedNetworks는 스캔 대상 제안에 실을 운영자 지정 대역이다
	// (SCAN_SUGGESTED_NETWORKS — WithScanSuggestedNetworks). 비면 제안에서 생략된다.
	scanSuggestedNetworks []string

	// secureCookies는 세션 쿠키에 Secure 플래그를 붙일지다 (SESSION_COOKIE_SECURE).
	// 기본 false — 평문 HTTP 기본 설치를 깨지 않는다.
	secureCookies bool

	// adminIP는 접근 IP 허용 목록 판정기다. 항상 존재하며(허용 목록이 비면
	// 제한 없음), 환경변수 유래 설정만 WithAdminIPAccess로 채워진다.
	adminIP *adminIPGuard
}

// Option은 NewServer의 선택 의존성 주입이다.
type Option func(*Server)

// WithNotificationChannels는 알림 채널 상태 조회·테스트 발송 구현을 주입한다.
// 주입하지 않으면 GET /notification-channels는 전 채널을 "배선되지 않음"으로
// 보고하고 테스트 발송은 503이다 — 코어 API는 그대로 뜬다.
func WithNotificationChannels(ch NotificationChannels) Option {
	return func(s *Server) { s.channels = ch }
}

// WithSweepDefaults는 SCAN_SWEEP_* 설정에서 파생한 스윕 기본값을 주입한다.
// 0 필드는 store의 기본값으로 폴백한다.
func WithSweepDefaults(d SweepDefaults) Option {
	return func(s *Server) { s.sweepDefaults = d }
}

// WithRescanController는 주기 재스캔 스케줄러를 주입한다.
// 주입하지 않으면 GET /discovery/rescan/status가 "미배선"으로 답하고
// POST /discovery/rescan/run은 503이다 — 코어 API는 그대로 뜬다.
func WithRescanController(c RescanController) Option {
	return func(s *Server) { s.rescan = c }
}

// WithScheduleController는 정기 스캔 스케줄러를 주입한다 (0019).
// 주입하지 않으면 GET /discovery/schedules는 wired=false로 답하고
// '지금 실행'·실행 취소는 503이다 — 조회·설정 변경은 그대로 동작한다.
func WithScheduleController(c ScheduleController) Option {
	return func(s *Server) { s.schedules = c }
}

// WithSensorGatewayPort는 SENSOR_LISTEN_ADDR에서 파생한 실제 게이트웨이 포트를 주입한다.
// 센서 생성 응답의 등록 명령 예시가 이 포트를 쓴다 — 주입하지 않으면 기본 포트로
// 안내되므로, 게이트웨이 포트를 바꾼 배치에서는 반드시 배선한다.
func WithSensorGatewayPort(port string) Option {
	return func(s *Server) {
		if p := strings.TrimSpace(port); p != "" {
			s.sensorGatewayPort = p
		}
	}
}

// WithScanSuggestedNetworks는 SCAN_SUGGESTED_NETWORKS(정규화된 CIDR 목록)를
// 스캔 대상 제안 API에 잇는다. 주입하지 않아도 최근 사용·파생 대역 제안은 동작한다.
func WithScanSuggestedNetworks(networks []string) Option {
	return func(s *Server) { s.scanSuggestedNetworks = networks }
}

// WithSecureCookies는 세션 쿠키의 Secure 플래그를 켠다 (SESSION_COOKIE_SECURE).
// HTTPS 리버스 프록시로 TLS를 종단하는 배포에서만 켠다 — 평문 HTTP 접속에서 켜면
// 브라우저가 쿠키를 저장하지 않아 로그인이 조용히 실패한다 (docs/manual/TLS.md).
func WithSecureCookies(secure bool) Option {
	return func(s *Server) { s.secureCookies = secure }
}

// NewServer는 모든 라우트가 등록된 HTTP 핸들러를 반환한다.
// sensorCAKey는 센서 mTLS 내부 CA 개인키의 봉인 키(32바이트)다 — 센서 CA를
// 처음 부트스트랩하는 시점(첫 센서 생성)에만 필요하며 nil이어도 기동은 된다.
func NewServer(pool *sql.DB, authSvc *auth.Service, rec *audit.Recorder, sensorCAKey []byte, opts ...Option) http.Handler {
	s := newServer(pool, authSvc, rec, sensorCAKey, opts...)
	// 접근 IP 제한은 인증보다 앞이다 — 차단된 IP는 로그인 화면조차 받지 못한다.
	// Request ID 안쪽에 두어 차단 응답에도 request_id가 실린다.
	return withSecurityHeaders(withRequestID(s.withAdminIPAllow(s.mux)))
}

// newServer는 라우트를 등록한 Server를 만든다. 핸들러를 호출하지 않는 한
// 의존성은 nil이어도 안전하다 — 정합 테스트가 라우트 목록만 뽑을 때 쓴다.
func newServer(pool *sql.DB, authSvc *auth.Service, rec *audit.Recorder, sensorCAKey []byte, opts ...Option) *Server {
	st := store.New(pool)
	disc := discovery.NewManager(st)
	s := &Server{mux: http.NewServeMux(), pool: pool, store: st, auth: authSvc, audit: rec,
		sensorGatewayPort: defaultSensorGatewayPort,
		adminIP:           newAdminIPGuard(st)}
	for _, opt := range opts {
		opt(s)
	}
	// 옵션이 적용된 뒤에 얹는다 — 스캔 Manager는 위에서 이미 만들어졌지만
	// 지원 TLS 버전 탐지 여부는 WithSweepDefaults로 들어오기 때문이다.
	// 여기 값과 planScan의 ETA 배수는 같은 필드에서 나온다(어긋나면 미리보기가 거짓말한다).
	disc.UseTLSVersionProbe(s.sweepDefaults.TLSProbeVersions)

	// 비보호 라우트: 헬스체크·로그인·초기 설정뿐이다.
	s.public("GET /healthz", handleHealthz(pool))
	s.public("POST /api/v1/auth/login", s.handleLogin)
	// 초기 설정 — 사용자가 0명일 때만 열린다. 계정이 1명이라도 생기면 상태 조회는
	// setup_required=false, 생성은 409가 되어 영구히 닫힌다 (handlers_setup.go).
	s.public("GET /api/v1/setup/status", s.handleSetupStatus)
	s.public("POST /api/v1/setup/admin", s.handleCreateInitialAdmin)

	// 보호 라우트: 인증 + 역할 검사.
	s.protected("POST /api/v1/auth/logout", s.handleLogout, domain.AllRoles...)
	s.protected("GET /api/v1/auth/me", s.handleMe, domain.AllRoles...)
	s.protected("POST /api/v1/users", s.handleCreateUser, domain.RoleSystemAdministrator)
	s.protected("GET /api/v1/users", s.handleListUsers, domain.RoleSystemAdministrator)
	// 사용자 수정·활성 토글·비밀번호 재설정 (00-spec 7.11 사용자 관리).
	// 전부 감사 이벤트를 남기며, 비활성화·비밀번호 재설정은 대상의 활성 세션을
	// 즉시 폐기한다 — DB 직접 UPDATE로만 가능하던 퇴사자 차단 경로를 없앤다.
	s.protected("PATCH /api/v1/users/{id}", s.handleUpdateUser, domain.RoleSystemAdministrator)
	s.protected("POST /api/v1/users/{id}/disable", s.handleDisableUser, domain.RoleSystemAdministrator)
	s.protected("POST /api/v1/users/{id}/enable", s.handleEnableUser, domain.RoleSystemAdministrator)
	s.protected("POST /api/v1/users/{id}/password", s.handleResetUserPassword, domain.RoleSystemAdministrator)
	// 업로드는 인벤토리 변경이므로 관리자·운영자급만 (00-spec 7.11).
	s.protected("POST /api/v1/certificates/import", handleImportCertificates(st),
		domain.RoleSystemAdministrator, domain.RolePKIAdministrator, domain.RoleOperator)
	// 조회는 인증된 전 역할 허용 — 7.11에서 6개 역할 모두 인증서 조회 권한을 가진다
	// (Viewer의 "제한된 조회" 세분화는 필드 제한 정의가 생기는 시점에). 읽기 전용이라
	// 감사 이벤트는 남기지 않는다 (7.12 기록 대상에 조회 없음; 내보내기는 별도 라우트).
	s.protected("GET /api/v1/certificates", handleListCertificates(st), domain.AllRoles...)
	// 리터럴 세그먼트가 {id} 와일드카드보다 우선 매칭된다 (Go 1.22+ ServeMux 규칙).
	s.protected("GET /api/v1/certificates/summary", handleCertificateSummary(st), domain.AllRoles...)
	// 발급 기관 분포·CA 트리 집계 — 읽기 전용, 전 역할 (summary와 같은 규약).
	s.protected("GET /api/v1/certificates/issuers", handleCertificateIssuers(st), domain.AllRoles...)
	// 위험 판정 집계(최고 심각도별·판정 항목별 건수) — 읽기 전용, 전 역할.
	// 판정은 저장하지 않고 요청마다 계산한다 (internal/certrisk).
	s.protected("GET /api/v1/certificates/risks", handleCertificateRisks(st), domain.AllRoles...)
	// 내보내기는 화면 한 페이지가 아니라 인벤토리 전체(최대 50000행 — 도메인·지문·
	// 유효기간)를 파일로 반출하는 경로다. 목록 조회와 달리 인증서 관리 책임이 있는
	// 관리자급 2역할로 제한한다 — 7.11에서 "모든 인증서 관리"(System Administrator)와
	// 인증서 발급·폐기(PKI Administrator)뿐이고, Operator·Auditor·Viewer의 권한은
	// 조회(읽기 전용)까지다. 업로드(import)와 역할 집합이 다른 이유는 위험 방향이
	// 반대이기 때문이다(반입 vs 반출).
	// 허용 역할이어도 예외 없이 certificate.export 감사 이벤트를 남긴다
	// (00-spec 7.12 "데이터 내보내기").
	s.protected("GET /api/v1/certificates/export", handleExportCertificates(st),
		domain.RoleSystemAdministrator, domain.RolePKIAdministrator)
	s.protected("GET /api/v1/certificates/{id}", handleGetCertificate(st), domain.AllRoles...)
	// 영향 범위 집계 — "이 CA가 만료되면 무엇이 죽는가"(전이적 자손 + 서버·도메인).
	// 읽기 전용, 전 역할 (summary·issuers·risks와 같은 규약).
	s.protected("GET /api/v1/certificates/{id}/impact", handleCertificateImpact(st), domain.AllRoles...)
	// 서버(엔드포인트) 축 — 같은 인벤토리를 호스트 1행으로 되짚는 읽기 전용 조회다.
	// 권한·감사 규약은 인증서 조회와 같다(전 역할, 감사 이벤트 없음).
	s.protected("GET /api/v1/servers", handleListServers(st), domain.AllRoles...)
	s.protected("GET /api/v1/servers/{id}", handleGetServer(st), domain.AllRoles...)
	// 스캔 생성은 인벤토리를 바꾸므로 관리자·운영자급만. 상태 조회는 읽기 전용이라 전 역할.
	// 총 엔드포인트 수가 임계를 넘으면 단발 대신 스윕으로 자동 승격한다(mode/promoted).
	s.protected("POST /api/v1/discovery/scans", s.handleCreateScan(disc),
		domain.RoleSystemAdministrator, domain.RolePKIAdministrator, domain.RoleOperator)
	// 미리보기는 아무것도 만들지 않지만(감사 없음) 발주 폼의 사전 계산이라 발주와
	// 같은 3역할이다. 리터럴 세그먼트라 {id} 라우트보다 우선 매칭된다.
	s.protected("POST /api/v1/discovery/scans/preview", s.handleScanPreview,
		domain.RoleSystemAdministrator, domain.RolePKIAdministrator, domain.RoleOperator)
	// 스캔 Job 목록·대상 제안 — 읽기 전용이라 전 역할, 감사 이벤트 없음.
	s.protected("GET /api/v1/discovery/scans", s.handleListScans, domain.AllRoles...)
	s.protected("GET /api/v1/discovery/scan-suggestions", s.handleScanSuggestions, domain.AllRoles...)
	// 통합 작업 목록(스캔 Job + 스윕 + 정기 스캔 실행)과 스캔 기본값 노출.
	// 둘 다 읽기 전용이라 전 역할이다 — 조회도 역할 검사를 거친다(하드 규칙 6).
	s.protected("GET /api/v1/discovery/activity", s.handleListDiscoveryActivity, domain.AllRoles...)
	s.protected("GET /api/v1/discovery/defaults", s.handleDiscoveryDefaults, domain.AllRoles...)
	s.protected("GET /api/v1/discovery/scans/{id}", handleGetScan(st), domain.AllRoles...)
	// 취소는 발주와 같은 3역할. 전이 + discovery.scan.cancel 감사는 store가 동일 tx로.
	s.protected("POST /api/v1/discovery/scans/{id}/cancel", handleCancelScan(disc, st),
		domain.RoleSystemAdministrator, domain.RolePKIAdministrator, domain.RoleOperator)
	// 전 포트 백그라운드 스윕 (04-port-autoscan 9.2). 발주·제어는 스캔과 같은 3역할,
	// 조회는 읽기 전용이라 전 역할. 상태 전이는 discovery.sweep 감사를 남긴다(store).
	s.protected("POST /api/v1/discovery/sweeps", s.handleCreateSweep,
		domain.RoleSystemAdministrator, domain.RolePKIAdministrator, domain.RoleOperator)
	s.protected("GET /api/v1/discovery/sweeps", s.handleListSweeps, domain.AllRoles...)
	// 리터럴 세그먼트가 {id}보다 우선 매칭된다 (Go 1.22+ ServeMux 규칙).
	s.protected("GET /api/v1/discovery/port-presets", handleListPortPresets, domain.AllRoles...)
	s.protected("GET /api/v1/discovery/sweeps/{id}", s.handleGetSweep, domain.AllRoles...)
	s.protected("POST /api/v1/discovery/sweeps/{id}/pause", s.handlePauseSweep,
		domain.RoleSystemAdministrator, domain.RolePKIAdministrator, domain.RoleOperator)
	s.protected("POST /api/v1/discovery/sweeps/{id}/resume", s.handleResumeSweep,
		domain.RoleSystemAdministrator, domain.RolePKIAdministrator, domain.RoleOperator)
	s.protected("POST /api/v1/discovery/sweeps/{id}/cancel", s.handleCancelSweep,
		domain.RoleSystemAdministrator, domain.RolePKIAdministrator, domain.RoleOperator)
	// 주기 재스캔 (06-rescan-and-replacement 11.2). 현황은 읽기 전용이라 전 역할,
	// 수동 1배치 발주는 스캔 발주와 같은 3역할 — 새 역할 집합을 만들지 않는다.
	s.protected("GET /api/v1/discovery/rescan/status", s.handleGetRescanStatus, domain.AllRoles...)
	s.protected("POST /api/v1/discovery/rescan/run", s.handleRunRescan,
		domain.RoleSystemAdministrator, domain.RolePKIAdministrator, domain.RoleOperator)
	// 정기 스캔 스케줄 (0019). 조회는 읽기 전용이라 전 역할, 설정 변경·생성·삭제·
	// 실행·취소는 스캔 발주와 같은 3역할이다 — 새 역할 집합을 만들지 않는다.
	// 변경 5종은 discovery.schedule 감사 이벤트를 store가 같은 트랜잭션으로 남긴다.
	// 리터럴 세그먼트(runs)가 {id}보다 우선 매칭된다 (Go 1.22+ ServeMux 규칙).
	s.protected("GET /api/v1/discovery/schedules", s.handleListScanSchedules, domain.AllRoles...)
	s.protected("PATCH /api/v1/discovery/schedules", s.handleUpdateScanSchedule,
		domain.RoleSystemAdministrator, domain.RolePKIAdministrator, domain.RoleOperator)
	// 생성·삭제는 preset 전용이다 — full·registered는 시드 1행이 정본이라 store가 막는다.
	s.protected("POST /api/v1/discovery/schedules", s.handleCreateScanSchedule,
		domain.RoleSystemAdministrator, domain.RolePKIAdministrator, domain.RoleOperator)
	s.protected("DELETE /api/v1/discovery/schedules/{id}", s.handleDeleteScanSchedule,
		domain.RoleSystemAdministrator, domain.RolePKIAdministrator, domain.RoleOperator)
	s.protected("POST /api/v1/discovery/schedules/{id}/run", s.handleRunScanSchedule,
		domain.RoleSystemAdministrator, domain.RolePKIAdministrator, domain.RoleOperator)
	s.protected("POST /api/v1/discovery/schedules/runs/{id}/cancel", s.handleCancelScanScheduleRun,
		domain.RoleSystemAdministrator, domain.RolePKIAdministrator, domain.RoleOperator)
	// 알림함 (05-notifications 9.1) — 전 역할. 본인 것만 강제 필터하며 남의 행은 404다.
	// 읽음·보관은 감사 이벤트를 남기지 않는다 (설계 12 ① — handlers_notifications.go 참조).
	// 리터럴 세그먼트(unread-count·read-all)를 {id} 라우트보다 먼저 등록한다.
	s.protected("GET /api/v1/notifications", handleListNotifications(st), domain.AllRoles...)
	s.protected("GET /api/v1/notifications/unread-count", handleUnreadNotificationCount(st), domain.AllRoles...)
	s.protected("POST /api/v1/notifications/read-all", handleMarkAllNotificationsRead(st), domain.AllRoles...)
	s.protected("POST /api/v1/notifications/{id}/read", handleMarkNotificationRead(st), domain.AllRoles...)
	s.protected("POST /api/v1/notifications/{id}/archive", handleArchiveNotification(st), domain.AllRoles...)
	// 알림 정책·채널 (05-notifications 9.2) — System Administrator + Security
	// Administrator. 후자의 근거는 00-spec 7.11 "보안 관리자 = 정책 설정"이다.
	s.protected("GET /api/v1/notification-policies", s.handleListNotificationPolicies,
		domain.RoleSystemAdministrator, domain.RoleSecurityAdministrator)
	s.protected("POST /api/v1/notification-policies", s.handleCreateNotificationPolicy,
		domain.RoleSystemAdministrator, domain.RoleSecurityAdministrator)
	s.protected("PATCH /api/v1/notification-policies/{id}", s.handleUpdateNotificationPolicy,
		domain.RoleSystemAdministrator, domain.RoleSecurityAdministrator)
	s.protected("DELETE /api/v1/notification-policies/{id}", s.handleDeleteNotificationPolicy,
		domain.RoleSystemAdministrator, domain.RoleSecurityAdministrator)
	s.protected("POST /api/v1/notification-policies/{id}/test", s.handleTestNotificationPolicy,
		domain.RoleSystemAdministrator, domain.RoleSecurityAdministrator)
	s.protected("GET /api/v1/notification-channels", s.handleListNotificationChannels,
		domain.RoleSystemAdministrator, domain.RoleSecurityAdministrator)
	// 발송 이력은 "왜 안 왔는지"의 답이라 감사자도 열람할 수 있다 (설계 14.3).
	s.protected("GET /api/v1/notification-deliveries", s.handleListNotificationDeliveries,
		domain.RoleSystemAdministrator, domain.RoleSecurityAdministrator, domain.RoleAuditor)
	// 센서(원격 수집기, 00-spec 7.8) — 생성(1회용 토큰 발급)·비활성화는 System
	// Administrator 전용, 목록·상세는 읽기 전용이라 전 역할 (감사 이벤트 없음).
	s.protected("POST /api/v1/sensors", handleCreateSensor(st, sensorCAKey, s.sensorGatewayPort),
		domain.RoleSystemAdministrator)
	s.protected("GET /api/v1/sensors", handleListSensors(st), domain.AllRoles...)
	s.protected("GET /api/v1/sensors/{id}", handleGetSensor(st), domain.AllRoles...)
	s.protected("POST /api/v1/sensors/{id}/disable", handleDisableSensor(st),
		domain.RoleSystemAdministrator)
	// 관리 콘솔 접근 IP 허용 목록 (0014) — System Administrator 전용.
	// 조회도 관리자 전용이다: 허용 대역 목록 자체가 우회 대상 정보다.
	// 변경 3종은 admin_ip_rule.* 감사 이벤트를 store가 같은 트랜잭션으로 남긴다.
	s.protected("GET /api/v1/admin-ip-rules", s.handleListAdminIPRules,
		domain.RoleSystemAdministrator)
	s.protected("POST /api/v1/admin-ip-rules", s.handleCreateAdminIPRule,
		domain.RoleSystemAdministrator)
	s.protected("PATCH /api/v1/admin-ip-rules/{id}", s.handleUpdateAdminIPRule,
		domain.RoleSystemAdministrator)
	s.protected("DELETE /api/v1/admin-ip-rules/{id}", s.handleDeleteAdminIPRule,
		domain.RoleSystemAdministrator)
	// 감사 로그 조회·무결성 검증 — 00-spec 7.11에서 감사 로그 조회 권한은
	// Security Administrator와 Auditor에만 명시되어 있다.
	s.protected("GET /api/v1/audit/events", s.handleListAuditEvents,
		domain.RoleSecurityAdministrator, domain.RoleAuditor)
	s.protected("GET /api/v1/audit/verify", s.handleVerifyAuditChain,
		domain.RoleSecurityAdministrator, domain.RoleAuditor)
	// OpenAPI 명세 제공 — 읽기 전용이라 전 역할, 폐쇄망 제품이므로 인증 뒤에 둔다
	// (public은 healthz·login뿐이라는 원칙 유지).
	s.protected("GET /api/v1/openapi.yaml", handleOpenAPISpec, domain.AllRoles...)
	s.registerIssuanceRoutes(sensorCAKey)

	return s
}

// public은 인증 없이 접근 가능한 라우트를 등록한다. 추가는 신중히.
func (s *Server) public(pattern string, h http.HandlerFunc) {
	s.routes = append(s.routes, pattern)
	s.mux.HandleFunc(pattern, h)
}

// protected는 인증 + 역할 검사를 거치는 라우트를 등록한다. roles가 비면 panic.
func (s *Server) protected(pattern string, h http.HandlerFunc, roles ...domain.Role) {
	s.routes = append(s.routes, pattern)
	s.mux.HandleFunc(pattern, s.requireRoles(h, roles...))
}

type healthzResponse struct {
	Status   string `json:"status"`
	Database string `json:"database"`
}

// handleHealthz는 서버 생존 여부와 DB 연결 상태를 반환한다.
// DB ping이 실패하면 503을 반환한다.
func handleHealthz(pool *sql.DB) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		ctx, cancel := context.WithTimeout(r.Context(), 2*time.Second)
		defer cancel()

		if err := pool.PingContext(ctx); err != nil {
			slog.Error("healthz: database ping failed", "error", err.Error())
			writeJSON(w, http.StatusServiceUnavailable, healthzResponse{
				Status:   "unavailable",
				Database: "down",
			})
			return
		}
		writeJSON(w, http.StatusOK, healthzResponse{
			Status:   "ok",
			Database: "up",
		})
	}
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	if err := json.NewEncoder(w).Encode(v); err != nil {
		slog.Error("write json response failed", "error", err.Error())
	}
}

// errorResponse는 통일된 오류 응답 형식이다 (00-spec 13장).
type errorResponse struct {
	Error     errorBody `json:"error"`
	RequestID string    `json:"request_id,omitempty"`
}

type errorBody struct {
	Code    string `json:"code"`
	Message string `json:"message"`
}

func writeError(w http.ResponseWriter, r *http.Request, status int, code, message string) {
	resp := errorResponse{Error: errorBody{Code: code, Message: message}}
	if id := requestIDFrom(r.Context()); id != nil {
		resp.RequestID = *id
	}
	writeJSON(w, status, resp)
}
