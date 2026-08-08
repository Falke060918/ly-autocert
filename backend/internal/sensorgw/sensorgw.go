// Package sensorgw는 센서(원격 수집기) 전용 mTLS 게이트웨이다 (docs/00-spec.md 7.8, Phase 3a).
// 관리자용 8080 API와 분리된 8443 리스너에 붙어 /sensor/v1/* 경로만 제공한다.
//
// - enroll을 제외한 모든 경로는 센서 CA가 발급한 클라이언트 인증서를 요구하고,
//   요청마다 fingerprint를 store와 대조해 신원을 확인한다 (보안 하드 규칙 6).
// - TLS ClientAuth는 VerifyClientCertIfGiven — enroll(최초 등록)은 인증서 없이
//   들어오고, 그 외 경로는 requireSensor 미들웨어가 인증서 부재를 401로 거른다.
// - 인증서·개인키·등록 토큰 바이트는 로그·에러 어디에도 넣지 않는다 (보안 하드 규칙 3).
// - 실제 리스너 기동 배선은 cmd/server 몫이다 — 이 패키지는 생성자·핸들러·
//   http.Server 구성 함수까지만 제공한다.
package sensorgw

import (
	"context"
	"crypto/sha256"
	"crypto/tls"
	"crypto/x509"
	"encoding/hex"
	"encoding/pem"
	"errors"
	"fmt"
	"net"
	"net/http"
	"time"

	"github.com/Falke060918/INHERIT_AutoCert/backend/internal/audit"
	"github.com/Falke060918/INHERIT_AutoCert/backend/internal/sensorca"
	"github.com/Falke060918/INHERIT_AutoCert/backend/internal/store"
)

// DefaultAddr는 센서 게이트웨이 기본 수신 주소다 (00-spec 7.8 — 관리자 API와 분리).
const DefaultAddr = ":8443"

// sensorCACommonName은 최초 부트스트랩 시 생성하는 센서 CA의 CN이다.
const sensorCACommonName = "INHERIT AutoCert Sensor CA"

// serverCertCommonName은 게이트웨이 mTLS 리스너 인증서의 CN이다.
const serverCertCommonName = "INHERIT AutoCert Sensor Gateway"

// Gateway는 센서 게이트웨이의 라우터와 의존성(store·감사 기록기·센서 CA)을 묶는다.
// routes는 등록된 "METHOD /path" 패턴 전체다 — OpenAPI 명세 정합 테스트가
// 이 목록과 api/sensor-openapi.yaml을 대조한다 (openapi_conformance_test.go).
type Gateway struct {
	mux    *http.ServeMux
	st     *store.Store
	rec    *audit.Recorder
	ca     *sensorca.CA
	routes []string
}

// New는 센서 CA를 store에서 로드(없으면 생성·봉인 저장)한 뒤 라우트가 등록된
// 게이트웨이를 만든다. sealKey는 CA 개인키 봉인용 AES-256 키(32바이트)다.
func New(ctx context.Context, st *store.Store, rec *audit.Recorder, sealKey []byte) (*Gateway, error) {
	ca, err := loadOrCreateCA(ctx, st, sealKey)
	if err != nil {
		return nil, err
	}
	return newGateway(st, rec, ca), nil
}

// newGateway는 라우트를 등록한 Gateway를 만든다. 핸들러를 호출하지 않는 한
// 의존성은 nil이어도 안전하다 — 정합 테스트가 라우트 목록만 뽑을 때 쓴다.
func newGateway(st *store.Store, rec *audit.Recorder, ca *sensorca.CA) *Gateway {
	g := &Gateway{mux: http.NewServeMux(), st: st, rec: rec, ca: ca}

	// 비인증 라우트: 최초 등록뿐이다 — 1회용 토큰이 인증을 대신한다.
	g.public("POST /sensor/v1/enroll", g.handleEnroll)

	// 인증 라우트: 검증된 클라이언트 인증서 + store 대조 (requireSensor).
	g.authenticated("POST /sensor/v1/renew", g.handleRenew)
	g.authenticated("POST /sensor/v1/jobs/lease", g.handleLease)
	g.authenticated("POST /sensor/v1/jobs/{id}/start", g.handleJobStart)
	g.authenticated("POST /sensor/v1/jobs/{id}/heartbeat", g.handleHeartbeat)
	g.authenticated("POST /sensor/v1/jobs/{id}/observations", g.handleObservations)
	g.authenticated("POST /sensor/v1/jobs/{id}/complete", g.handleComplete)

	return g
}

// public은 클라이언트 인증서 없이 접근 가능한 라우트를 등록한다. 추가는 신중히.
func (g *Gateway) public(pattern string, h http.HandlerFunc) {
	g.routes = append(g.routes, pattern)
	g.mux.HandleFunc(pattern, h)
}

// authenticated는 mTLS 신원 확인(requireSensor)을 거치는 라우트를 등록한다.
func (g *Gateway) authenticated(pattern string, h http.HandlerFunc) {
	g.routes = append(g.routes, pattern)
	g.mux.HandleFunc(pattern, g.requireSensor(h))
}

// Handler는 미들웨어(X-Request-ID·slog JSON 요청 로그)가 적용된 최종 핸들러를 반환한다.
func (g *Gateway) Handler() http.Handler {
	return withRequestID(withRequestLog(g.mux))
}

// CACertPEM은 센서 CA 인증서 PEM을 반환한다 (공개 정보 — 등록 응답·핀 안내용).
func (g *Gateway) CACertPEM() []byte { return g.ca.CertPEM() }

// CAFingerprintSHA256은 센서 CA 인증서의 SHA-256 지문(소문자 hex)을 반환한다.
// 센서 enroll의 --ca-fingerprint 핀 값으로 관리자에게 안내한다.
func (g *Gateway) CAFingerprintSHA256() (string, error) {
	block, _ := pem.Decode(g.ca.CertPEM())
	if block == nil {
		return "", errors.New("sensorgw: decode ca certificate failed")
	}
	sum := sha256.Sum256(block.Bytes)
	return hex.EncodeToString(sum[:]), nil
}

// NewHTTPServer는 8443 전용 mTLS http.Server를 구성한다. 서버 인증서는 기동 시
// 센서 CA로 메모리 발급하며(SAN은 인자), 디스크에 남기지 않는다.
// addr가 비어 있으면 DefaultAddr를 쓴다. 리스너 기동은 호출자(cmd/server) 몫이다 —
// TLSConfig.Certificates가 채워져 있으므로 ServeTLS(ln, "", "")로 띄운다.
func (g *Gateway) NewHTTPServer(addr string, dnsNames []string, ips []net.IP) (*http.Server, error) {
	if addr == "" {
		addr = DefaultAddr
	}
	certPEM, keyDER, err := g.ca.IssueServerCert(serverCertCommonName, dnsNames, ips)
	if err != nil {
		return nil, err
	}
	block, _ := pem.Decode(certPEM)
	if block == nil {
		return nil, errors.New("sensorgw: decode server certificate failed")
	}
	key, err := x509.ParsePKCS8PrivateKey(keyDER)
	if err != nil {
		return nil, errors.New("sensorgw: parse server key failed")
	}
	caBlock, _ := pem.Decode(g.ca.CertPEM())
	if caBlock == nil {
		return nil, errors.New("sensorgw: decode ca certificate failed")
	}
	clientCAs := x509.NewCertPool()
	if !clientCAs.AppendCertsFromPEM(g.ca.CertPEM()) {
		return nil, errors.New("sensorgw: build client ca pool failed")
	}
	return &http.Server{
		Addr:    addr,
		Handler: g.Handler(),
		TLSConfig: &tls.Config{
			MinVersion: tls.VersionTLS12,
			// 제시 체인은 리프+CA다 — 센서 enroll의 --ca-fingerprint 핀은
			// 서버가 제시한 체인 안에서 CA를 찾으므로(sensor.verifyPinnedChain)
			// 리프만 보내면 실환경 등록이 실패한다.
			Certificates: []tls.Certificate{{Certificate: [][]byte{block.Bytes, caBlock.Bytes}, PrivateKey: key}},
			// enroll은 인증서 없이 들어와야 하므로 Require가 아니라 VerifyIfGiven이다.
			// 제시된 인증서는 핸드셰이크에서 센서 CA로 검증되고, 신원 확인(store 대조)은
			// requireSensor 미들웨어가 요청마다 수행한다.
			ClientAuth: tls.VerifyClientCertIfGiven,
			ClientCAs:  clientCAs,
		},
		ReadHeaderTimeout: 10 * time.Second,
	}, nil
}

// loadOrCreateCA는 센서 CA를 store에서 로드한다. 없으면 새로 만들어 개인키를
// sealKey로 봉인해 저장한다. 동시 부트스트랩 경합(ErrSensorCAExists)이면
// 승자의 CA를 다시 읽는다 (store 규약).
func loadOrCreateCA(ctx context.Context, st *store.Store, sealKey []byte) (*sensorca.CA, error) {
	rec, err := st.LoadSensorCA(ctx)
	if errors.Is(err, store.ErrSensorCANotFound) {
		ca, err := sensorca.New(sensorCACommonName)
		if err != nil {
			return nil, err
		}
		keyDER, err := ca.KeyDER()
		if err != nil {
			return nil, err
		}
		ciphertext, nonce, err := sensorca.Seal(sealKey, keyDER)
		if err != nil {
			return nil, err
		}
		saveErr := st.SaveSensorCA(ctx, store.SensorCARecord{
			CertPEM:       ca.CertPEM(),
			KeyCiphertext: ciphertext,
			KeyNonce:      nonce,
		})
		if saveErr == nil {
			return ca, nil
		}
		if !errors.Is(saveErr, store.ErrSensorCAExists) {
			return nil, saveErr
		}
		// 경합에서 졌다 — 방금 만든 CA는 버리고 저장된 쪽을 읽는다.
		rec, err = st.LoadSensorCA(ctx)
		if err != nil {
			return nil, fmt.Errorf("sensorgw: reload sensor ca after race: %w", err)
		}
	} else if err != nil {
		return nil, err
	}
	keyDER, err := sensorca.Open(sealKey, rec.KeyCiphertext, rec.KeyNonce)
	if err != nil {
		return nil, fmt.Errorf("sensorgw: unseal sensor ca key: %w", err)
	}
	return sensorca.Load(rec.CertPEM, keyDER)
}
