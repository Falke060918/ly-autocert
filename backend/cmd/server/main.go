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

	"github.com/Falke060918/INHERIT_AutoCert/backend/internal/api"
	"github.com/Falke060918/INHERIT_AutoCert/backend/internal/audit"
	"github.com/Falke060918/INHERIT_AutoCert/backend/internal/auth"
	"github.com/Falke060918/INHERIT_AutoCert/backend/internal/config"
	"github.com/Falke060918/INHERIT_AutoCert/backend/internal/db"
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
		// SAN이 비면 서버 인증서를 발급할 수 없다 — 가장 흔한 원인이라 변수명을 문구에 박아 둔다.
		logger.Warn("sensor gateway disabled: startup failed — check SENSOR_ADVERTISED_HOSTS "+
			"(comma-separated DNS names/IPs the sensors connect to) and SENSOR_LISTEN_ADDR; core API keeps running",
			"error", err.Error())
		return nil, func() {}
	}
	return srv, stop
}

func run(logger *slog.Logger) error {
	cfg, err := config.Load()
	if err != nil {
		return err
	}

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
	// users가 비어 있으면 초기 관리자를 만든다 (BOOTSTRAP_ADMIN_PASSWORD 필요).
	if err := authSvc.BootstrapAdmin(ctx, cfg.BootstrapAdminUsername, cfg.BootstrapAdminPassword); err != nil {
		return err
	}

	srv := &http.Server{
		Addr:              cfg.HTTPAddr,
		Handler:           api.NewServer(pool, authSvc, rec, cfg.SensorCAEncKey),
		ReadHeaderTimeout: 10 * time.Second,
	}

	// 서버 재시작으로 실행 주체가 사라진 서버 직접 실행 Job을 회수한다
	// (센서 위임 Job은 리스 만료 reaper가 따로 회수한다).
	if n, err := store.New(pool).ReapOrphanedServerScanJobs(ctx); err != nil {
		// 회수 실패가 기동을 막지는 않는다 — 코어 API는 계속 뜬다.
		logger.Error("reap orphaned server scan jobs failed", "error", err.Error())
	} else if n > 0 {
		logger.Info("reaped orphaned server scan jobs", "count", n)
	}

	errCh := make(chan error, 1)

	// 센서 전용 설정 오류는 게이트웨이만 끄고 코어 API는 그대로 띄운다.
	if cfg.SensorConfigError != nil {
		logger.Warn("sensor gateway disabled: invalid sensor configuration — core API keeps running",
			"error", cfg.SensorConfigError.Error())
	}

	// 센서 게이트웨이(mTLS 8443)는 SENSOR_CA_ENC_KEY가 설정된 경우에만 띄운다 —
	// 미설정이면 리스너 자체를 열지 않아 기존 zero-config 실행에 영향이 없다.
	var sensorSrv *http.Server
	if cfg.SensorCAEncKey != nil {
		var stopGateway func()
		sensorSrv, stopGateway = startSensorGateway(logger, func() (*http.Server, func(), error) {
			gw, err := sensorgw.New(ctx, store.New(pool), rec, cfg.SensorCAEncKey)
			if err != nil {
				return nil, nil, err
			}
			srv, err := gw.NewHTTPServer(cfg.SensorListenAddr, cfg.SensorAdvertisedDNS, cfg.SensorAdvertisedIPs)
			if err != nil {
				return nil, nil, err
			}
			return srv, gw.StartReaper(ctx), nil
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
		logger.Info("sensor gateway disabled (SENSOR_CA_ENC_KEY not set)")
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
