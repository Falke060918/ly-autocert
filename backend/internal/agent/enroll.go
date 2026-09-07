package agent

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/sha256"
	"crypto/tls"
	"crypto/x509"
	"encoding/hex"
	"errors"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/Falke060918/INHERIT_AutoCert/backend/internal/agentwire"
)

// EnrollOptions는 enroll 서브커맨드 입력이다.
type EnrollOptions struct {
	ServerURL           string // https://host:port
	Token               string // 1회용 등록 토큰 — 로그·에러에 넣지 않는다
	CAFingerprint       string // 서버 CA 인증서 SHA-256 지문 (hex, 콜론 허용)
	StateDir            string
	IdentityKey         []byte
	JobSigningPublicKey []byte
}

// Enroll은 에이전트를 서버에 등록한다: 로컬 ECDSA P-256 키 생성(전송 금지) → CSR 제출
// (서버 신뢰는 --ca-fingerprint 핀으로만 판단) → 발급된 클라이언트 인증서·CA를 상태
// 디렉토리에 저장. 성공 시 서버가 발급한 에이전트 ID를 반환한다.
func Enroll(ctx context.Context, opts EnrollOptions) (agentID string, err error) {
	if len(opts.IdentityKey) != 32 || len(opts.JobSigningPublicKey) != 32 {
		return "", errKeySource
	}
	unlock, err := lockState(opts.StateDir)
	if err != nil {
		return "", err
	}
	defer unlock()
	for _, name := range []string{fileKey, "key.pem", fileAgentID} {
		if _, e := os.Lstat(filepath.Join(opts.StateDir, name)); !os.IsNotExist(e) {
			return "", errors.New("agent: existing identity cannot be overwritten")
		}
	}
	pin, err := parseFingerprint(opts.CAFingerprint)
	if err != nil {
		return "", err
	}
	base, err := normalizeServerURL(opts.ServerURL)
	if err != nil {
		return "", err
	}

	// 개인키는 여기서 만들어 이 호스트를 떠나지 않는다. 서버로 가는 것은 CSR뿐이다.
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return "", errors.New("agent: generate private key failed")
	}
	hostname, _ := os.Hostname()
	csrPEM, err := newCSRPEM(key, hostname)
	if err != nil {
		return "", err
	}

	req := agentwire.EnrollRequest{Token: opts.Token, CSRPEM: string(csrPEM)}
	if err := req.Validate(); err != nil {
		return "", err
	}

	// 최초 등록 시점엔 서버 CA를 아직 모른다 — 시스템 신뢰 저장소 대신
	// --ca-fingerprint 핀을 VerifyPeerCertificate로 검증한다 (아래 verifyPinnedChain).
	hc := &http.Client{
		Timeout: httpTimeout,
		Transport: &http.Transport{
			TLSClientConfig: &tls.Config{
				MinVersion:            tls.VersionTLS12,
				InsecureSkipVerify:    true, //nolint:gosec // 핀 검증으로 대체 — verifyPinnedChain이 수행
				VerifyPeerCertificate: verifyPinnedChain(pin),
			},
		},
	}

	var resp agentwire.EnrollResponse
	if _, err := postJSON(ctx, hc, base, pathEnroll, req, &resp); err != nil {
		return "", err
	}
	if resp.AgentID == "" {
		return "", errors.New("agent: enroll response missing agent_id")
	}
	// 발급 인증서가 방금 만든 키와 일치하는지 확인한 뒤에만 저장한다.
	if _, err := clientCertForKey([]byte(resp.ClientCertPEM), key); err != nil {
		return "", err
	}
	if err := SaveEnrollment(opts.StateDir, resp.AgentID, key, []byte(resp.ClientCertPEM), []byte(resp.CACertPEM), opts.IdentityKey); err != nil {
		return "", err
	}
	// 저장본이 온전한지 즉시 복원 검사한다 (ca.pem 파싱 포함).
	if _, err := LoadIdentity(opts.StateDir, opts.IdentityKey); err != nil {
		return "", err
	}
	return resp.AgentID, nil
}

// parseFingerprint는 SHA-256 지문 hex(콜론·공백·대문자 허용)를 32바이트로 파싱한다.
func parseFingerprint(raw string) ([sha256.Size]byte, error) {
	var pin [sha256.Size]byte
	clean := strings.ToLower(strings.NewReplacer(":", "", " ", "").Replace(strings.TrimSpace(raw)))
	b, err := hex.DecodeString(clean)
	if err != nil || len(b) != sha256.Size {
		return pin, errors.New("agent: --ca-fingerprint must be a 64-char hex SHA-256 fingerprint")
	}
	copy(pin[:], b)
	return pin, nil
}

// verifyPinnedChain은 서버가 제시한 체인에서 핀과 일치하는 인증서를 찾아 신뢰를 판단한다.
// 핀이 리프면 그대로 신뢰하고, 체인 상위(CA)면 리프가 그 CA로 검증되는지 확인한다.
// 시간 검증은 x509.Verify가 수행한다. 호스트명 검증은 핀이 대체한다.
func verifyPinnedChain(pin [sha256.Size]byte) func(rawCerts [][]byte, _ [][]*x509.Certificate) error {
	return func(rawCerts [][]byte, _ [][]*x509.Certificate) error {
		if len(rawCerts) == 0 {
			return errors.New("agent: server presented no certificate")
		}
		pinned := -1
		for i, raw := range rawCerts {
			if sha256.Sum256(raw) == pin {
				pinned = i
				break
			}
		}
		if pinned < 0 {
			return errors.New("agent: server certificate chain does not match --ca-fingerprint")
		}
		if pinned == 0 {
			return nil // 리프 자체가 핀 — 그 이상 검증할 것이 없다.
		}
		leaf, err := x509.ParseCertificate(rawCerts[0])
		if err != nil {
			return errors.New("agent: parse server certificate failed")
		}
		pinCert, err := x509.ParseCertificate(rawCerts[pinned])
		if err != nil {
			return errors.New("agent: parse pinned ca certificate failed")
		}
		roots := x509.NewCertPool()
		roots.AddCert(pinCert)
		inters := x509.NewCertPool()
		for i, raw := range rawCerts[1:] {
			if i+1 == pinned {
				continue
			}
			if c, err := x509.ParseCertificate(raw); err == nil {
				inters.AddCert(c)
			}
		}
		if _, err := leaf.Verify(x509.VerifyOptions{
			Roots:         roots,
			Intermediates: inters,
			CurrentTime:   time.Now(),
			KeyUsages:     []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
		}); err != nil {
			return fmt.Errorf("agent: server certificate does not chain to the pinned ca")
		}
		return nil
	}
}
