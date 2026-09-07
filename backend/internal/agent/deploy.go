package agent

import (
	"context"
	"crypto/sha256"
	"crypto/tls"
	"encoding/hex"
	"errors"
	"fmt"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/Falke060918/INHERIT_AutoCert/backend/internal/agentwire"
)

type Executor interface {
	ConfigTest(context.Context, agentwire.Target) error
	Reload(context.Context, agentwire.Target) error
	Fingerprint(context.Context, agentwire.Target) (string, error)
}
type SystemExecutor struct{}

func (SystemExecutor) ConfigTest(ctx context.Context, t agentwire.Target) error {
	if e := t.Validate(); e != nil {
		return e
	}
	name, args := "nginx", []string{"-t"}
	if t.ServiceUnit == "apache2" {
		name, args = "apachectl", []string{"configtest"}
	}
	if t.ServiceUnit == "httpd" {
		name, args = "httpd", []string{"-t"}
	}
	return fixedCommand(ctx, name, args...)
}
func (SystemExecutor) Reload(ctx context.Context, t agentwire.Target) error {
	if e := t.Validate(); e != nil {
		return e
	}
	return fixedCommand(ctx, "systemctl", "reload", t.ServiceUnit)
}
func fixedCommand(ctx context.Context, name string, args ...string) error {
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	// Resolve executables from administrator-owned system directories, never PATH.
	for _, dir := range []string{"/usr/sbin", "/usr/bin", "/sbin", "/bin"} {
		p := filepath.Join(dir, name)
		if st, e := os.Stat(p); e == nil && !st.IsDir() {
			cmd := exec.CommandContext(ctx, p, args...)
			cmd.Env = []string{"PATH=/usr/sbin:/usr/bin:/sbin:/bin", "LANG=C"}
			if e = cmd.Run(); e != nil {
				return errors.New("서비스 명령 실패")
			}
			return nil
		}
	}
	return errors.New("서비스 실행 파일을 찾을 수 없습니다")
}
func (SystemExecutor) Fingerprint(ctx context.Context, t agentwire.Target) (string, error) {
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	d := tls.Dialer{NetDialer: &net.Dialer{Timeout: 10 * time.Second}, Config: &tls.Config{MinVersion: tls.VersionTLS12, InsecureSkipVerify: true, ServerName: t.SNIName}} // Exact leaf fingerprint verification follows; this is an observation probe.
	c, e := d.DialContext(ctx, "tcp", net.JoinHostPort(t.VerifyHost, fmt.Sprint(t.VerifyPort)))
	if e != nil {
		return "", errors.New("TLS 검증 접속 실패")
	}
	defer c.Close()
	certs := c.(*tls.Conn).ConnectionState().PeerCertificates
	if len(certs) == 0 {
		return "", errors.New("TLS 인증서 없음")
	}
	sum := sha256.Sum256(certs[0].Raw)
	return hex.EncodeToString(sum[:]), nil
}

type Deployer struct {
	Executor    Executor
	AllowedDirs []string
}
type savedFile struct {
	path, backup, temp string
	mode               os.FileMode
	swapped            bool
}

func (d Deployer) safePath(p string) error {
	allowed := false
	for _, root := range d.AllowedDirs {
		if !filepath.IsAbs(root) || filepath.Clean(root) == "/" {
			continue
		}
		rel, e := filepath.Rel(filepath.Clean(root), p)
		if e == nil && rel != "." && rel != ".." && !strings.HasPrefix(rel, "../") {
			allowed = true
		}
	}
	if !allowed {
		return errors.New("에이전트의 허용 디렉토리 밖 경로입니다")
	}
	for cur := p; cur != "/"; cur = filepath.Dir(cur) {
		st, e := os.Lstat(cur)
		if e != nil || st.Mode()&os.ModeSymlink != 0 {
			return errors.New("경로가 없거나 심볼릭 링크입니다")
		}
		if cur == p && !st.Mode().IsRegular() {
			return errors.New("일반 파일만 교체할 수 있습니다")
		}
	}
	return nil
}
func (d Deployer) Deploy(ctx context.Context, l agentwire.Lease, m agentwire.Material) agentwire.Result {
	r := agentwire.Result{Status: "failed", Steps: []agentwire.Step{}, LeaseToken: l.LeaseToken}
	step := func(name string, e error) {
		msg := "완료"
		if e != nil {
			msg = "실패"
		}
		r.Steps = append(r.Steps, agentwire.Step{At: time.Now().UTC(), Name: name, OK: e == nil, Message: msg})
	}
	fail := func(name string, e error) agentwire.Result {
		step(name, e)
		r.Error = name + " 단계 실패"
		return r
	}
	if d.Executor == nil {
		return fail("precheck", errors.New("실행기 없음"))
	}
	if e := l.Target.Validate(); e != nil {
		return fail("precheck", e)
	}
	if l.Kind != "deploy" || !agentwire.Fingerprint(l.ExpectedFingerprint) {
		return fail("precheck", errors.New("잘못된 작업"))
	}
	pair, e := tls.X509KeyPair([]byte(m.CertPEM), []byte(m.KeyPEM))
	if e != nil || len(pair.Certificate) == 0 {
		return fail("precheck", errors.New("키 불일치"))
	}
	sum := sha256.Sum256(pair.Certificate[0])
	if hex.EncodeToString(sum[:]) != l.ExpectedFingerprint {
		return fail("precheck", errors.New("지문 불일치"))
	}
	paths := []string{l.Target.CertPath, l.Target.KeyPath}
	contents := [][]byte{[]byte(m.CertPEM + m.ChainPEM), []byte(m.KeyPEM)}
	defer clear(contents[1])
	m.KeyPEM = ""
	if l.Target.Kind == "apache" && l.Target.ChainPath != "" {
		paths = append(paths, l.Target.ChainPath)
		contents[0] = []byte(m.CertPEM)
		contents = append(contents, []byte(m.ChainPEM))
	}
	files := make([]savedFile, len(paths))
	for i, p := range paths {
		if e = d.safePath(p); e != nil {
			return fail("precheck", e)
		}
		st, e := os.Stat(p)
		if e != nil {
			return fail("precheck", e)
		}
		files[i] = savedFile{path: p, mode: st.Mode().Perm()}
	}
	oldCert, e := os.ReadFile(paths[0])
	if e != nil {
		return fail("precheck", e)
	}
	oldKey, e := os.ReadFile(paths[1])
	if e != nil {
		return fail("precheck", e)
	}
	oldPair, e := tls.X509KeyPair(oldCert, oldKey)
	clear(oldKey)
	if e != nil {
		return fail("precheck", errors.New("기존 인증서 키 불일치"))
	}
	oldSum := sha256.Sum256(oldPair.Certificate[0])
	oldFP := hex.EncodeToString(oldSum[:])
	if e = d.Executor.ConfigTest(ctx, l.Target); e != nil {
		return fail("precheck", e)
	}
	served, e := d.Executor.Fingerprint(ctx, l.Target)
	if e != nil || served != oldFP {
		return fail("precheck", errors.New("기존 서비스 지문과 파일 불일치"))
	}
	step("precheck", nil)
	if oldFP == l.ExpectedFingerprint {
		r.Status = "completed"
		r.ObservedFingerprint = served
		return r
	}
	cleanupTemps := func() {
		for _, f := range files {
			if f.temp != "" {
				_ = os.Remove(f.temp)
			}
		}
	}
	defer cleanupTemps()
	removeBackups := func() error {
		for _, f := range files {
			if f.backup != "" {
				if e := os.Remove(f.backup); e != nil {
					return e
				}
			}
		}
		return nil
	}
	for i := range files {
		b, e := os.ReadFile(files[i].path)
		if e != nil {
			_ = removeBackups()
			return fail("backup", e)
		}
		files[i].backup, e = durableTemp(files[i].path, ".autocert-bak-", b, files[i].mode)
		clear(b)
		if e != nil {
			_ = removeBackups()
			return fail("backup", e)
		}
	}
	step("backup", nil)
	for i := range files {
		mode := os.FileMode(0644)
		if i == 1 {
			mode = 0600
		}
		files[i].temp, e = durableTemp(files[i].path, ".autocert-new-", contents[i], mode)
		if e != nil {
			_ = removeBackups()
			return fail("write_temp", e)
		}
	}
	step("write_temp", nil)
	rollback := func(failedStep string, cause error) agentwire.Result {
		step(failedStep, cause)
		r.Error = failedStep + " 단계 실패"
		rollbackCtx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
		defer cancel()
		var restoreErr error
		for i := range files {
			f := &files[i]
			if f.swapped {
				if e := os.Rename(f.backup, f.path); e != nil {
					restoreErr = e
				} else {
					f.backup = ""
					if e = syncDir(filepath.Dir(f.path)); e != nil {
						restoreErr = e
					}
				}
			}
		}
		if restoreErr == nil {
			restoreErr = d.Executor.ConfigTest(rollbackCtx, l.Target)
		}
		if restoreErr == nil {
			restoreErr = d.Executor.Reload(rollbackCtx, l.Target)
		}
		if restoreErr == nil {
			var fp string
			fp, restoreErr = d.Executor.Fingerprint(rollbackCtx, l.Target)
			if restoreErr == nil && fp != oldFP {
				restoreErr = errors.New("롤백 지문 불일치")
			}
			r.ObservedFingerprint = fp
		}
		step("rollback", restoreErr)
		if restoreErr == nil {
			r.Status = "rolled_back"
			_ = removeBackups()
		} else {
			r.Error += "; 복원 실패, 대상 디렉토리의 백업으로 수동 복구 필요"
		}
		return r
	}
	for i := range files {
		if e = ctx.Err(); e != nil {
			return rollback("swap", e)
		}
		if e = d.safePath(files[i].path); e != nil {
			return rollback("swap", e)
		}
		if e = os.Rename(files[i].temp, files[i].path); e != nil {
			return rollback("swap", e)
		}
		files[i].swapped = true
		files[i].temp = ""
		if e = syncDir(filepath.Dir(files[i].path)); e != nil {
			return rollback("swap", e)
		}
	}
	step("swap", nil)
	if e = d.Executor.ConfigTest(ctx, l.Target); e != nil {
		return rollback("config_test", e)
	}
	step("config_test", nil)
	if e = d.Executor.Reload(ctx, l.Target); e != nil {
		return rollback("reload", e)
	}
	step("reload", nil)
	fp, e := d.Executor.Fingerprint(ctx, l.Target)
	if e != nil || fp != l.ExpectedFingerprint {
		return rollback("verify", errors.New("배포 지문 불일치"))
	}
	step("verify", nil)
	r.ObservedFingerprint = fp
	r.Status = "completed"
	if e = removeBackups(); e != nil {
		step("cleanup", e)
		r.Error = "배포 완료, 백업 파일 수동 정리 필요"
	} else {
		step("cleanup", nil)
	}
	return r
}
func durableTemp(dest, prefix string, data []byte, mode os.FileMode) (string, error) {
	f, e := os.CreateTemp(filepath.Dir(dest), filepath.Base(dest)+prefix)
	if e != nil {
		return "", e
	}
	name := f.Name()
	ok := false
	defer func() {
		f.Close()
		if !ok {
			_ = os.Remove(name)
		}
	}()
	if e = f.Chmod(mode); e != nil {
		return "", e
	}
	if e = preserveOwner(dest, name); e != nil {
		return "", e
	}
	if _, e = f.Write(data); e != nil {
		return "", e
	}
	if e = f.Sync(); e != nil {
		return "", e
	}
	if e = f.Close(); e != nil {
		return "", e
	}
	ok = true
	return name, syncDir(filepath.Dir(dest))
}
func syncDir(dir string) error {
	f, e := os.Open(dir)
	if e != nil {
		return e
	}
	defer f.Close()
	return f.Sync()
}
