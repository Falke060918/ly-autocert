// Package agent는 원격 수집기(에이전트) 바이너리의 클라이언트 측 구현이다 (docs/00-spec.md 7.8).
// 서버 게이트웨이(/agent/v1/*)와 agentwire DTO로 통신하고, 스캔 자체는
// internal/discovery의 store 비의존 함수(Expand·ScanEndpoint)를 그대로 재사용한다.
//
// 보안: 개인키·인증서·등록 토큰 바이트를 로그·에러 메시지에 절대 넣지 않는다 (보안 하드 규칙 3).
// 개인키는 에이전트 호스트를 떠나지 않으며(전송 금지), 표준 라이브러리 crypto만 사용한다 (규칙 2).
package agent

import (
	"crypto/ecdsa"
	"crypto/x509"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
)

// 상태 디렉토리 파일 이름. 개인키가 있으므로 디렉토리 0700, 파일 0600을 유지한다.
const (
	fileKey        = "identity.sealed" // AES-GCM sealed PKCS#8 DER
	fileClientCert = "client.pem"      // mTLS 클라이언트 인증서 (PEM)
	fileCACert     = "ca.pem"          // 서버 검증용 CA 인증서 (PEM)
	fileAgentID    = "agent_id"        // 서버가 발급한 에이전트 ID
)

const (
	dirPerm  = os.FileMode(0o700)
	filePerm = os.FileMode(0o600)
)

const pemTypePrivateKey = "PRIVATE KEY"

// DefaultStateDir는 OS별 상태 디렉토리 기본값이다. --state-dir로 재정의할 수 있다.
func DefaultStateDir() string {
	if runtime.GOOS == "windows" {
		base := os.Getenv("ProgramData")
		if base == "" {
			base = `C:\ProgramData`
		}
		return filepath.Join(base, "AutocertAgent")
	}
	return "/var/lib/autocert-agent"
}

// SaveEnrollment는 등록 결과(개인키·클라이언트 인증서·CA·에이전트 ID)를 상태 디렉토리에 저장한다.
// 디렉토리는 0700, 파일은 0600이다. 호출자인 Enroll이 기존 신원 덮어쓰기를 거부한다.
func SaveEnrollment(dir, agentID string, key *ecdsa.PrivateKey, clientCertPEM, caCertPEM, wrappingKey []byte) error {
	if agentID == "" {
		return errors.New("agent: agent id is required")
	}
	if err := ensureStateDir(dir); err != nil {
		return err
	}
	keyDER, err := x509.MarshalPKCS8PrivateKey(key)
	if err != nil {
		return errors.New("agent: marshal private key failed")
	}
	defer clear(keyDER)
	keyPEM, err := sealIdentity(wrappingKey, keyDER, agentID)
	if err != nil {
		return err
	}

	// 키를 먼저 쓴다 — 인증서만 있고 키가 없는 상태보다 그 반대가 복구하기 쉽다.
	if err := writeFileAtomic(filepath.Join(dir, fileKey), keyPEM, filePerm); err != nil {
		return err
	}
	if err := writeFileAtomic(filepath.Join(dir, fileClientCert), clientCertPEM, filePerm); err != nil {
		return err
	}
	if err := writeFileAtomic(filepath.Join(dir, fileCACert), caCertPEM, filePerm); err != nil {
		return err
	}
	return writeFileAtomic(filepath.Join(dir, fileAgentID), []byte(agentID+"\n"), filePerm)
}

// ReplaceClientCert는 갱신된 클라이언트 인증서를 원자적으로 교체 저장한다 (임시 파일 + rename).
// 개인키는 재사용하므로 인증서 파일 1개만 바뀐다 — 교체 도중 크래시에도 키/인증서 불일치가 없다.
func ReplaceClientCert(dir string, clientCertPEM []byte) error {
	return writeFileAtomic(filepath.Join(dir, fileClientCert), clientCertPEM, filePerm)
}

// LoadIdentity는 상태 디렉토리에서 에이전트 신원(키·인증서·CA·ID)을 복원한다.
func LoadIdentity(dir string, wrappingKey []byte) (*Identity, error) {
	if err := ensureStateDir(dir); err != nil {
		return nil, err
	}
	if _, err := os.Lstat(filepath.Join(dir, "key.pem")); !os.IsNotExist(err) {
		return nil, errors.New("agent: legacy identity requires explicit migrate-identity")
	}
	idBytes, err := readStateFile(dir, fileAgentID)
	if err != nil {
		return nil, err
	}
	agentID := strings.TrimSpace(string(idBytes))
	if agentID == "" {
		return nil, errors.New("agent: missing agent id")
	}
	keyPEM, err := readStateFile(dir, fileKey)
	if err != nil {
		return nil, fmt.Errorf("agent: state not found in %s — run 'enroll' first", dir)
	}
	der, err := openIdentity(wrappingKey, keyPEM, agentID)
	if err != nil {
		return nil, err
	}
	defer clear(der)
	parsed, err := x509.ParsePKCS8PrivateKey(der)
	if err != nil {
		return nil, errors.New("agent: parse private key failed")
	}
	key, ok := parsed.(*ecdsa.PrivateKey)
	if !ok {
		return nil, errors.New("agent: private key is not an ECDSA key")
	}

	certPEM, err := readStateFile(dir, fileClientCert)
	if err != nil {
		return nil, errors.New("agent: client certificate file missing — run 'enroll' first")
	}
	leaf, err := clientCertForKey(certPEM, key)
	if err != nil {
		return nil, err
	}

	caPEM, err := readStateFile(dir, fileCACert)
	if err != nil {
		return nil, errors.New("agent: ca certificate file missing — run 'enroll' first")
	}
	caPool := x509.NewCertPool()
	if !caPool.AppendCertsFromPEM(caPEM) {
		return nil, errors.New("agent: ca certificate file contains no valid certificate")
	}

	id := &Identity{AgentID: agentID, key: key, caPool: caPool}
	id.install(leaf)
	return id, nil
}

// ensureStateDir는 상태 디렉토리를 0700으로 만든다. 이미 있으면 권한을 조인다.
func ensureStateDir(dir string) error {
	if noSymlinks(dir) != nil {
		return errKeySource
	}
	if err := os.MkdirAll(dir, dirPerm); err != nil {
		return fmt.Errorf("agent: create state dir: %w", err)
	}
	// MkdirAll은 기존 디렉토리 권한을 바꾸지 않는다. (Windows에서는 chmod가 제한적 — best effort)
	st, e := os.Stat(dir)
	if e != nil || !owned(st) {
		return errKeySource
	}
	if err := os.Chmod(dir, dirPerm); err != nil {
		return fmt.Errorf("agent: chmod state dir: %w", err)
	}
	return nil
}

// writeFileAtomic은 임시 파일에 쓰고 rename으로 원자 교체한다.
func writeFileAtomic(path string, data []byte, perm os.FileMode) error {
	if noSymlinks(path) != nil {
		return errKeySource
	}
	f, e := os.CreateTemp(filepath.Dir(path), ".sealed-*")
	if e != nil {
		return e
	}
	tmp := f.Name()
	defer os.Remove(tmp)
	if e = f.Chmod(perm); e == nil {
		_, e = f.Write(data)
	}
	if e == nil {
		e = f.Sync()
	}
	ce := f.Close()
	if e != nil {
		return e
	}
	if ce != nil {
		return ce
	}
	if e = os.Rename(tmp, path); e != nil {
		return e
	}
	return syncDir(filepath.Dir(path))
}
func readStateFile(dir, name string) ([]byte, error) {
	path := filepath.Join(dir, name)
	st, e := os.Lstat(path)
	if e != nil || !st.Mode().IsRegular() || !owned(st) || st.Mode().Perm() != 0600 || st.Size() > 65536 {
		return nil, errors.New("agent: invalid state file")
	}
	return os.ReadFile(path)
}
