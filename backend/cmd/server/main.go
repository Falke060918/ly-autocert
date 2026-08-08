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

	errCh := make(chan error, 2)

	// 센서 게이트웨이(mTLS 8443)는 SENSOR_CA_ENC_KEY가 설정된 경우에만 띄운다 —
	// 미설정이면 리스너 자체를 열지 않아 기존 zero-config 실행에 영향이 없다.
	var sensorSrv *http.Server
	if cfg.SensorCAEncKey != nil {
		gw, err := sensorgw.New(ctx, store.New(pool), rec, cfg.SensorCAEncKey)
		if err != nil {
			return err
		}
		sensorSrv, err = gw.NewHTTPServer(cfg.SensorListenAddr, cfg.SensorAdvertisedDNS, cfg.SensorAdvertisedIPs)
		if err != nil {
			return err
		}
		if len(cfg.SensorAdvertisedDNS)+len(cfg.SensorAdvertisedIPs) == 0 {
			// SAN 없는 서버 인증서 — enroll(핀 검증)은 되지만 이후 mTLS 접속은
			// 호스트명 검증에 실패한다. 배선은 유지하되 운영자에게 알린다.
			logger.Warn("sensor gateway: SENSOR_ADVERTISED_HOSTS is empty — enrolled sensors will fail server certificate verification")
		}
		stopReaper := gw.StartReaper(ctx)
		defer stopReaper()
		go func() {
			logger.Info("sensor gateway listening", "addr", cfg.SensorListenAddr)
			// 서버 인증서는 TLSConfig.Certificates에 메모리로 실려 있다 — 파일 인자 불필요.
			if err := sensorSrv.ListenAndServeTLS("", ""); err != nil && !errors.Is(err, http.ErrServerClosed) {
				errCh <- err
			}
		}()
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
