package main

import (
	"context"
	"flag"
	"fmt"
	"io"
	"os"
	"os/signal"
	"strings"
	"syscall"

	"github.com/Falke060918/INHERIT_AutoCert/backend/internal/agent"
)

func main() {
	if e := run(); e != nil {
		fmt.Fprintln(os.Stderr, e)
		os.Exit(1)
	}
}
func run() error {
	if len(os.Args) < 2 {
		return fmt.Errorf("사용법: autocert-agent init-identity-key|enroll|migrate-identity|run")
	}
	fs := flag.NewFlagSet(os.Args[1], flag.ContinueOnError)
	server := fs.String("server", "", "https://host:8443")
	dir := fs.String("state-dir", "/var/lib/autocert-agent", "mTLS 신원 디렉토리")
	tokenFile := fs.String("token-file", "", "등록 토큰 파일 또는 - (stdin)")
	pin := fs.String("ca-fingerprint", "", "CA SHA-256 지문")
	allowed := fs.String("allow-dirs", "/etc/nginx,/etc/apache2,/etc/httpd,/etc/ssl", "배포 파일을 허용할 디렉토리")
	keyFile := fs.String("identity-key-file", "", "별도 identity.env 파일")
	initKeyFile := fs.String("key-file", "", "초기화할 identity.env 경로")
	jobPin := fs.String("job-signing-public-key-file", "", "관리자가 공급한 작업 서명 공개키")
	if e := fs.Parse(os.Args[2:]); e != nil {
		return e
	}
	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer cancel()
	if os.Args[1] == "init-identity-key" {
		return agent.InitIdentityKey(*initKeyFile, *dir)
	}
	key, e := agent.LoadIdentityKey(*keyFile, *dir)
	if e != nil {
		return e
	}
	defer clear(key)
	if os.Args[1] == "migrate-identity" {
		return agent.MigrateIdentity(*dir, key)
	}
	pub, e := agent.LoadJobSigningPublicKey(*jobPin, *dir)
	if e != nil {
		return e
	}
	switch os.Args[1] {
	case "enroll":
		var r io.Reader
		if *tokenFile == "-" {
			r = os.Stdin
		} else {
			if *tokenFile == "" {
				return fmt.Errorf("--token-file이 필요합니다")
			}
			f, e := os.Open(*tokenFile)
			if e != nil {
				return fmt.Errorf("토큰 파일을 읽을 수 없습니다")
			}
			defer f.Close()
			r = f
		}
		b, e := io.ReadAll(io.LimitReader(r, 1024))
		if e != nil {
			return fmt.Errorf("토큰 읽기 실패")
		}
		defer clear(b)
		id, e := agent.Enroll(ctx, agent.EnrollOptions{ServerURL: *server, StateDir: *dir, Token: strings.TrimSpace(string(b)), CAFingerprint: *pin, IdentityKey: key, JobSigningPublicKey: pub})
		if e == nil {
			fmt.Println("에이전트 등록 완료:", id)
		}
		return e
	case "run":
		return agent.Run(ctx, *server, *dir, strings.Split(*allowed, ","), key, pub)
	default:
		return fmt.Errorf("지원하지 않는 명령")
	}
}
