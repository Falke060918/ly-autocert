// Command sensor는 원격 수집기(센서) 바이너리다 (docs/00-spec.md 7.8, Phase 3a).
// 서버가 도달하지 못하는 분리망 세그먼트에 설치되어, 아웃바운드 mTLS 1경로로만
// 서버 게이트웨이(/sensor/v1/*)에 접속한다 — 인바운드 리스너가 없다.
//
//	sensor enroll --server https://host:8443 --token <1회용 토큰> --ca-fingerprint <sha256 hex>
//	sensor run    --server https://host:8443
//
// 개인키는 enroll 때 로컬에서 생성되어 이 호스트를 떠나지 않는다.
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"log/slog"
	"os"
	"os/signal"
	"syscall"

	"github.com/Falke060918/INHERIT_AutoCert/backend/internal/sensor"
)

func main() {
	// 구조화 JSON 로그 (00-spec 11장 관측성 고정값).
	logger := slog.New(slog.NewJSONHandler(os.Stdout, nil))
	slog.SetDefault(logger)

	if len(os.Args) < 2 {
		usage()
		os.Exit(2)
	}

	var err error
	switch os.Args[1] {
	case "enroll":
		err = runEnroll(os.Args[2:], logger)
	case "run":
		err = runRun(os.Args[2:], logger)
	default:
		usage()
		os.Exit(2)
	}
	if err != nil {
		// 에러에 토큰·키·인증서 바이트는 담기지 않는다 (sensor 패키지 규약).
		logger.Error("sensor exited with error", "error", err.Error())
		os.Exit(1)
	}
}

func usage() {
	fmt.Fprintln(os.Stderr, "usage: sensor <enroll|run> [flags]")
	fmt.Fprintln(os.Stderr, "  enroll --server <https://host:port> --token <token> --ca-fingerprint <sha256 hex> [--state-dir <dir>]")
	fmt.Fprintln(os.Stderr, "  run    --server <https://host:port> [--state-dir <dir>]")
}

func runEnroll(args []string, logger *slog.Logger) error {
	fs := flag.NewFlagSet("enroll", flag.ExitOnError)
	server := fs.String("server", "", "AutoCert 서버 URL (https://host:port)")
	token := fs.String("token", "", "1회용 등록 토큰")
	fingerprint := fs.String("ca-fingerprint", "", "서버 CA 인증서 SHA-256 지문 (hex)")
	stateDir := fs.String("state-dir", sensor.DefaultStateDir(), "상태 디렉토리")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if *server == "" || *token == "" || *fingerprint == "" {
		return errors.New("sensor: --server, --token, --ca-fingerprint are required")
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	sensorID, err := sensor.Enroll(ctx, sensor.EnrollOptions{
		ServerURL:     *server,
		Token:         *token,
		CAFingerprint: *fingerprint,
		StateDir:      *stateDir,
	})
	if err != nil {
		return err
	}
	logger.Info("enrollment complete", "sensor_id", sensorID, "state_dir", *stateDir)
	return nil
}

func runRun(args []string, logger *slog.Logger) error {
	fs := flag.NewFlagSet("run", flag.ExitOnError)
	server := fs.String("server", "", "AutoCert 서버 URL (https://host:port)")
	stateDir := fs.String("state-dir", sensor.DefaultStateDir(), "상태 디렉토리")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if *server == "" {
		return errors.New("sensor: --server is required")
	}

	id, err := sensor.LoadIdentity(*stateDir)
	if err != nil {
		return err
	}
	client, err := sensor.NewClient(*server, id)
	if err != nil {
		return err
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	logger.Info("sensor running", "server", *server, "sensor_id", id.SensorID)
	runner := &sensor.Runner{Client: client, Identity: id, StateDir: *stateDir, Logger: logger}
	if err := runner.Run(ctx); err != nil {
		return err
	}
	logger.Info("shutdown signal received")
	return nil
}
