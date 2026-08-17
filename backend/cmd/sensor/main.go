// Command sensor는 원격 수집기(센서) 바이너리다 (docs/00-spec.md 7.8, Phase 3a).
// 서버가 도달하지 못하는 분리망 세그먼트에 설치되어, 아웃바운드 mTLS 1경로로만
// 서버 게이트웨이(/sensor/v1/*)에 접속한다 — 인바운드 리스너가 없다.
//
//	sensor enroll --server https://host:8443 --token-file <토큰 파일> --ca-fingerprint <sha256 hex>
//	sensor enroll --server https://host:8443 --ca-fingerprint <sha256 hex>   # 토큰은 stdin 파이프로
//	sensor run    --server https://host:8443
//	sensor --version
//
// 등록 토큰은 --token-file(경로 또는 '-'=stdin)이나 stdin 파이프로 넘기는 것을 권장한다 —
// --token 플래그도 호환 유지되지만 프로세스 인자(ps·명령 이력)에 토큰이 노출된다.
// 개인키는 enroll 때 로컬에서 생성되어 이 호스트를 떠나지 않는다.
package main

import (
	"bufio"
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"log/slog"
	"os"
	"os/signal"
	"strings"
	"syscall"

	"github.com/Falke060918/INHERIT_AutoCert/backend/internal/sensor"
)

// version은 빌드 시 ldflags로 주입된다 (scripts/build-sensor-dist.sh:
// -X main.version=<VERSION>). 주입 없이 `go build`하면 "dev"로 남는다.
var version = "dev"

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
	case "--version", "-version", "version":
		// 현장에 여러 대가 깔렸을 때 구버전 식별용 — 로컬 출력 전용이다.
		fmt.Printf("autocert-sensor %s\n", version)
		return
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
	fmt.Fprintln(os.Stderr, "  enroll --server <https://host:port> [--token <token> | --token-file <path|- >] --ca-fingerprint <sha256 hex> [--state-dir <dir>]")
	fmt.Fprintln(os.Stderr, "         토큰 플래그가 없으면 stdin 파이프에서 읽는다. --token은 ps에 노출되므로 --token-file 권장")
	fmt.Fprintln(os.Stderr, "  run    --server <https://host:port> [--state-dir <dir>]")
	fmt.Fprintln(os.Stderr, "  --version  빌드 버전 출력")
}

func runEnroll(args []string, logger *slog.Logger) error {
	fs := flag.NewFlagSet("enroll", flag.ExitOnError)
	server := fs.String("server", "", "AutoCert 서버 URL (https://host:port)")
	token := fs.String("token", "", "1회용 등록 토큰 (ps에 노출 — --token-file 권장)")
	tokenFile := fs.String("token-file", "", "1회용 등록 토큰이 든 파일 경로 ('-'는 stdin)")
	fingerprint := fs.String("ca-fingerprint", "", "서버 CA 인증서 SHA-256 지문 (hex)")
	stateDir := fs.String("state-dir", sensor.DefaultStateDir(), "상태 디렉토리")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if *server == "" || *fingerprint == "" {
		return errors.New("sensor: --server and --ca-fingerprint are required")
	}
	enrollToken, err := resolveEnrollToken(*token, *tokenFile, os.Stdin, stdinIsPiped())
	if err != nil {
		return err
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	sensorID, err := sensor.Enroll(ctx, sensor.EnrollOptions{
		ServerURL:     *server,
		Token:         enrollToken,
		CAFingerprint: *fingerprint,
		StateDir:      *stateDir,
	})
	if err != nil {
		return err
	}
	logger.Info("enrollment complete", "sensor_id", sensorID, "state_dir", *stateDir)
	return nil
}

// maxTokenReadBytes는 토큰 입력(파일·stdin)에서 읽는 상한이다.
// 토큰은 짧은 한 줄이므로, 실수로 큰 파일을 지정해도 통째로 읽지 않는다.
const maxTokenReadBytes = 4096

// resolveEnrollToken은 등록 토큰을 --token → --token-file → stdin 파이프 순으로 정한다.
// --token은 프로세스 인자(ps·명령 이력)에 노출되므로 호환용으로만 남긴다.
// stdinPiped가 거짓(터미널)인데 플래그가 없으면 오류다 — 화면에 토큰이 echo되는
// 프롬프트를 만들지 않는다. 오류 메시지에 토큰 값은 싣지 않는다 (sensor 패키지 규약).
func resolveEnrollToken(token, tokenFile string, stdin io.Reader, stdinPiped bool) (string, error) {
	switch {
	case token != "" && tokenFile != "":
		return "", errors.New("sensor: --token and --token-file are mutually exclusive")
	case token != "":
		return token, nil
	case tokenFile == "-":
		return readTokenLine(stdin)
	case tokenFile != "":
		f, err := os.Open(tokenFile)
		if err != nil {
			return "", fmt.Errorf("sensor: open token file: %w", err)
		}
		defer f.Close()
		return readTokenLine(f)
	case stdinPiped:
		return readTokenLine(stdin)
	default:
		return "", errors.New("sensor: enrollment token is required — pass --token-file <path>, pipe the token via stdin, or use --token")
	}
}

// readTokenLine은 첫 줄만 읽어 앞뒤 공백(CR·LF 포함)을 제거한다.
func readTokenLine(r io.Reader) (string, error) {
	line, err := bufio.NewReader(io.LimitReader(r, maxTokenReadBytes)).ReadString('\n')
	if err != nil && !errors.Is(err, io.EOF) {
		return "", fmt.Errorf("sensor: read token: %w", err)
	}
	tok := strings.TrimSpace(line)
	if tok == "" {
		return "", errors.New("sensor: enrollment token is empty")
	}
	return tok, nil
}

// stdinIsPiped는 stdin이 터미널이 아닌지(파이프·리디렉션) 판정한다.
func stdinIsPiped() bool {
	fi, err := os.Stdin.Stat()
	if err != nil {
		return false
	}
	return fi.Mode()&os.ModeCharDevice == 0
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
