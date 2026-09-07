// Command server는 INHERIT_AutoCert 백엔드 HTTP 서버를 기동한다.
package main

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/Falke060918/INHERIT_AutoCert/backend/internal/agentgw"
	"github.com/Falke060918/INHERIT_AutoCert/backend/internal/api"
	"github.com/Falke060918/INHERIT_AutoCert/backend/internal/audit"
	"github.com/Falke060918/INHERIT_AutoCert/backend/internal/auth"
	"github.com/Falke060918/INHERIT_AutoCert/backend/internal/config"
	"github.com/Falke060918/INHERIT_AutoCert/backend/internal/db"
	"github.com/Falke060918/INHERIT_AutoCert/backend/internal/deploysign"
	"github.com/Falke060918/INHERIT_AutoCert/backend/internal/issuance"
	"github.com/Falke060918/INHERIT_AutoCert/backend/internal/sensorca"
	"github.com/Falke060918/INHERIT_AutoCert/backend/internal/sensorgw"
	"github.com/Falke060918/INHERIT_AutoCert/backend/internal/store"
)

func main() {
	// 구조화 JSON 로그 (00-spec 11장 관측성 고정값).
	logger := slog.New(slog.NewJSONHandler(os.Stdout, nil))
	slog.SetDefault(logger)

	if err := run(logger); err != nil {
		logger.Error("server exited with error", "error", err.Error())
		os.Exit(1)
	}
}

// gatewayStarter는 센서 게이트웨이 구성 절차다 (서버 + reaper 정지 함수).
// 실패 경로를 테스트가 주입할 수 있게 함수 타입으로 분리했다.
type gatewayStarter func() (*http.Server, func(), error)

// startSensorGateway는 센서 게이트웨이 구성을 시도하고, 실패하면 게이트웨이만
// 비활성화한 채 (nil, no-op)을 반환한다. 센서 설정 오류가 코어 API(8080)를
// 죽이지 않게 하는 격리 지점이다 — 어떤 설정 조합에서도 8080은 뜬다.
func startSensorGateway(logger *slog.Logger, start gatewayStarter) (*http.Server, func()) {
	srv, stop, err := start()
	if err != nil {
		logger.Warn(sensorGatewayFailureHint(err), "error", err.Error())
		return nil, func() {}
	}
	return srv, stop
}

// sensorGatewayFailureHint는 기동 실패 원인에 맞는 조치 문구를 고른다.
// 원인과 무관하게 한 변수만 지목하면(이전 동작: 항상 SENSOR_ADVERTISED_HOSTS)
// 키 회전·오배포로 CA 봉인 해제가 실패한 경우에도 같은 문구가 나와 오진을 부른다.
// 어떤 분기든 고쳐야 할 환경변수 이름을 문구에 포함한다 — 이름이 없으면
// DB·포트 문제로 읽힌다. 오류 값은 상수 문구뿐이라 키·인증서를 싣지 않는다.
func sensorGatewayFailureHint(err error) string {
	switch {
	case errors.Is(err, sensorca.ErrNoSAN):
		return "sensor gateway disabled: server certificate has no SAN — set SENSOR_ADVERTISED_HOSTS " +
			"(comma-separated DNS names/IPs the sensors connect to); core API keeps running"
	case errors.Is(err, sensorca.ErrOpen):
		return "sensor gateway disabled: stored sensor CA key could not be unsealed — SENSOR_CA_ENC_KEY " +
			"does not match the key that sealed it (rotated or wrong deployment); core API keeps running"
	default:
		return "sensor gateway disabled: startup failed — check SENSOR_CA_ENC_KEY, SENSOR_ADVERTISED_HOSTS " +
			"and SENSOR_LISTEN_ADDR; core API keeps running"
	}
}

// logSensorGatewayDisabled는 게이트웨이를 아예 구성하지 않는 경우의 사유를 한 줄로 남긴다.
// 설정 오류가 있으면 그 사유만 남긴다 — 키 형식 오류는 "값이 있으나 무효"인 상태라
// "not set" INFO를 함께 내면 서로 모순되게 읽힌다.
func logSensorGatewayDisabled(logger *slog.Logger, configErr error) {
	if configErr != nil {
		logger.Warn("sensor gateway disabled: SENSOR_CA_ENC_KEY is invalid — core API keeps running",
			"error", configErr.Error())
		return
	}
	logger.Info("sensor gateway disabled (SENSOR_CA_ENC_KEY not set)")
}

func run(logger *slog.Logger) error {
	cfg, err := config.Load()
	if err != nil {
		return err
	}

	// 값 오류로 기본값에 폴백했거나 채널을 비활성화한 항목을 먼저 알린다.
	// 어느 것도 기동을 막지 않는다 — 사유가 보이지 않으면 진단이 불가능할 뿐이다.
	logStartupWarnings(logger, cfg)

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	pool, err := db.Connect(ctx, cfg.DatabaseURL)
	if err != nil {
		return err
	}
	defer pool.Close()
	logger.Info("database connected")

	if err := db.Migrate(pool); err != nil {
		return err
	}
	logger.Info("migrations applied")

	rec := audit.NewRecorder(pool)
	authSvc := auth.NewService(pool, cfg.SessionTTL, rec)
	// users가 비어 있고 BOOTSTRAP_ADMIN_PASSWORD가 설정돼 있으면 초기 관리자를 만든다
	// (무인 설치용 경로 — 유지). 미설정이면 첫 접속 시 웹 초기 설정 화면에서 만든다.
	if err := authSvc.BootstrapAdmin(ctx, cfg.BootstrapAdminUsername, cfg.BootstrapAdminPassword); err != nil {
		return err
	}

	st := store.New(pool)

	// 서버 재시작으로 실행 주체가 사라진 서버 직접 실행 Job을 회수한다
	// (센서 위임 Job은 리스 만료 reaper가 따로 회수한다).
	if n, err := st.ReapOrphanedServerScanJobs(ctx); err != nil {
		// 회수 실패가 기동을 막지는 않는다 — 코어 API는 계속 뜬다.
		logger.Error("reap orphaned server scan jobs failed", "error", err.Error())
	} else if n > 0 {
		logger.Info("reaped orphaned server scan jobs", "count", n)
	}

	// 알림 탐지 스케줄러 + 딜리버리 디스패처. 채널 미설정·오설정은 그 채널만
	// 비활성이고 코어 API(8080)는 그대로 뜬다 (센서 게이트웨이와 같은 격리 원칙).
	// notifyChannels는 API의 채널 상태 조회·테스트 발송이 쓰는 어댑터이며,
	// NOTIFY_ENABLED=false여도 non-nil이다 — 알림이 안 오는 사유를 화면에서 봐야 한다.
	stopNotify, notifyChannels := startNotify(ctx, logger, cfg, st)
	defer stopNotify()

	// 대역 스윕 발주 루프. API 핸들러의 Manager와 상태를 공유하지 않으므로 별도 인스턴스다.
	stopSweep := startSweepScheduler(ctx, logger, cfg, st)
	defer stopSweep()

	// 주기 재스캔 tick — 이미 아는 엔드포인트의 신선도 유지 + 교체 확정 전이.
	// 스윕과는 목적도 주기도 다른 별개의 발주자다 (docs/design/06 1.3).
	// rescanCtl은 상태 조회·수동 1배치 발주 API가 쓰는 어댑터이며,
	// RESCAN_ENABLED=false여도 non-nil이다 — 꺼진 것과 배선이 없는 것을
	// 화면이 구분해야 한다(알림 채널 어댑터와 같은 규약).
	stopRescan, rescanCtl, discMgr := startRescanScheduler(ctx, logger, cfg, st)
	defer stopRescan()

	// 정기 스캔 스케줄 tick (0019) — 주기가 오면 스윕 1건을 발주하고, 발주한 스윕이
	// 끝나면 그 실행을 닫아 다음 주기를 연다. 재스캔 Manager를 그대로 쓴다:
	// '등록된 인증서' 정기 스캔의 '지금 실행'이 곧 재스캔 1배치 발주이기 때문이다.
	stopSchedules, scheduleCtl := startScanScheduleRunner(ctx, logger, cfg, discMgr)
	defer stopSchedules()
	issueSvc := issuance.New(st, cfg.IssuanceEncKey)
	stopIssuance := issueSvc.Start(ctx, logger)
	defer stopIssuance()

	srv := &http.Server{
		Addr: cfg.HTTPAddr,
		// 선택 의존성 7개를 주입한다. 없어도 코어 API는 뜨지만, 없으면 채널 상태가
		// "배선 없음"이고 스윕 기본값이 SCAN_SWEEP_*를, 등록 예시가 실제 포트를 무시하며
		// 접근 IP 제한이 XFF를 보지 않고 비상 우회 대역도 갖지 않으며, 재스캔 현황이
		// "미배선"으로 나오고 수동 1배치 발주와 정기 스캔 '지금 실행'이 503이 된다.
		// 세션 쿠키 Secure는 SESSION_COOKIE_SECURE(기본 false) — HTTPS 리버스
		// 프록시로 종단할 때만 켠다 (docs/manual/TLS.md).
		Handler: api.NewServer(pool, authSvc, rec, cfg.SensorCAEncKey,
			api.WithIssuance(issueSvc),
			api.WithNotificationChannels(notifyChannels),
			api.WithSweepDefaults(sweepDefaults(cfg.Sweep, cfg.ScanTLSProbeVersions)),
			// 스캔 대상 제안에 운영자 지정 대역(SCAN_SUGGESTED_NETWORKS)을 싣는다.
			api.WithScanSuggestedNetworks(cfg.ScanSuggestedNetworks),
			// 재스캔 스케줄러(discovery)를 API의 상태·수동 발주 엔드포인트에 잇는다.
			api.WithRescanController(rescanCtl),
			// 정기 스캔 스케줄러를 '지금 실행'·실행 취소 엔드포인트에 잇는다.
			api.WithScheduleController(scheduleCtl),
			// 등록 명령 예시가 실제 게이트웨이 포트를 안내하게 한다.
			api.WithSensorGatewayPort(cfg.SensorGatewayPort),
			api.WithSecureCookies(cfg.SessionCookieSecure),
			api.WithAdminIPAccess(api.AdminIPAccess{
				TrustedProxies: cfg.AdminIP.TrustedProxies,
				Bootstrap:      cfg.AdminIP.Bootstrap,
			})),
		ReadHeaderTimeout: 10 * time.Second,
	}

	errCh := make(chan error, 1)

	// 센서 게이트웨이(mTLS 8443)는 SENSOR_CA_ENC_KEY가 설정된 경우에만 띄운다 —
	// 미설정이면 리스너 자체를 열지 않아 기존 zero-config 실행에 영향이 없다.
	var sensorSrv *http.Server
	if cfg.SensorCAEncKey != nil {
		var stopGateway func()
		sensorSrv, stopGateway = startSensorGateway(logger, func() (*http.Server, func(), error) {
			gw, err := sensorgw.New(ctx, st, rec, cfg.SensorCAEncKey)
			if err != nil {
				return nil, nil, err
			}
			srv, err := gw.NewHTTPServer(cfg.SensorListenAddr, cfg.SensorAdvertisedDNS, cfg.SensorAdvertisedIPs)
			if err != nil {
				return nil, nil, err
			}
			var signer *deploysign.Signer
			if cfg.DeploySigningKeyFile != "" {
				signer, err = deploysign.Load(cfg.DeploySigningKeyFile, cfg.IssuanceEncKey)
				if err != nil {
					logger.Error("deployment signer unavailable; job leasing disabled")
					signer = nil
				}
			}
			ag, err := agentgw.New(ctx, st, cfg.SensorCAEncKey, cfg.IssuanceEncKey, signer)
			if err != nil {
				return nil, nil, err
			}
			mux := http.NewServeMux()
			mux.Handle("/agent/", ag.Handler())
			mux.Handle("/", srv.Handler)
			srv.Handler = mux
			stopSensor, stopAgent := gw.StartReaper(ctx), ag.StartReaper(ctx)
			return srv, func() { stopAgent(); stopSensor() }, nil
		})
		defer stopGateway()
		if sensorSrv != nil {
			go func() {
				logger.Info("sensor gateway listening", "addr", cfg.SensorListenAddr)
				// 서버 인증서는 TLSConfig.Certificates에 메모리로 실려 있다 — 파일 인자 불필요.
				// 리스너 실패도 코어 API(8080)를 죽이지 않는다: 게이트웨이만 내려간다.
				if err := sensorSrv.ListenAndServeTLS("", ""); err != nil && !errors.Is(err, http.ErrServerClosed) {
					logger.Error("sensor gateway stopped — core API keeps running", "error", err.Error())
				}
			}()
		}
	} else {
		// 센서 전용 설정 오류는 게이트웨이만 끄고 코어 API는 그대로 띄운다.
		logSensorGatewayDisabled(logger, cfg.SensorConfigError)
	}

	go func() {
		logger.Info("http server listening", "addr", cfg.HTTPAddr)
		if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			errCh <- err
		}
	}()

	select {
	case err := <-errCh:
		return err
	case <-ctx.Done():
		logger.Info("shutdown signal received")
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		var shutdownErrs []error
		if sensorSrv != nil {
			shutdownErrs = append(shutdownErrs, sensorSrv.Shutdown(shutdownCtx))
		}
		shutdownErrs = append(shutdownErrs, srv.Shutdown(shutdownCtx))
		return errors.Join(shutdownErrs...)
	}
}
