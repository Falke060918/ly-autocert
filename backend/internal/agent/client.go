package agent

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
)

// 서버 게이트웨이 경로 규약 (docs/00-spec.md 7.8). 게이트웨이 유닛과 wire 계약으로 맞춘다.
const (
	pathEnroll = "/agent/v1/enroll"
	pathRenew  = "/agent/v1/renew"
	pathLease  = "/agent/v1/jobs/lease"
)

// pathJob은 Job 하위 액션 경로(start·heartbeat·observations·complete)를 만든다.
func pathJob(jobID, action string) string {
	return "/agent/v1/jobs/" + url.PathEscape(jobID) + "/" + action
}

// httpTimeout은 요청 1건의 상한이다. 관측 배치(≤100건) 전송도 이 안에 끝난다.
const httpTimeout = 30 * time.Second

// Client는 서버 게이트웨이의 mTLS API 클라이언트다.
type Client struct {
	baseURL string
	httpc   *http.Client
}

// NewClient는 등록된 신원으로 mTLS 클라이언트를 만든다.
func NewClient(serverURL string, id *Identity) (*Client, error) {
	base, err := normalizeServerURL(serverURL)
	if err != nil {
		return nil, err
	}
	return &Client{
		baseURL: base,
		httpc: &http.Client{
			Timeout:   httpTimeout,
			Transport: &http.Transport{TLSClientConfig: id.TLSConfig()},
		},
	}, nil
}

// normalizeServerURL은 서버 URL을 검증하고 꼬리 슬래시를 제거한다.
// 에이전트-서버 통신은 mTLS 1경로뿐이므로 https만 허용한다 (00-spec 7.8 배치 원칙).
func normalizeServerURL(raw string) (string, error) {
	u, err := url.Parse(strings.TrimSpace(raw))
	if err != nil || u.Host == "" {
		return "", errors.New("agent: --server must be a valid URL (https://host:port)")
	}
	if u.Scheme != "https" {
		return "", errors.New("agent: --server must use https (mTLS only)")
	}
	if u.Path != "" && u.Path != "/" {
		return "", errors.New("agent: --server must not contain a path")
	}
	return "https://" + u.Host, nil
}

// postJSON은 JSON POST 1건을 실행한다. 모든 요청에 X-Request-ID를 붙인다 (00-spec 11장).
// 오류에는 상태 코드만 싣는다 — 응답 본문은 에러·로그에 넣지 않는다 (보안 하드 규칙 3).
func postJSON(ctx context.Context, hc *http.Client, baseURL, path string, in, out any) (int, error) {
	var body io.Reader
	if in != nil {
		b, err := json.Marshal(in)
		if err != nil {
			return 0, fmt.Errorf("agent: encode request for %s failed", path)
		}
		body = bytes.NewReader(b)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, baseURL+path, body)
	if err != nil {
		return 0, fmt.Errorf("agent: build request for %s failed", path)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Request-ID", newRequestID())

	resp, err := hc.Do(req)
	if err != nil {
		if ctx.Err() != nil {
			return 0, ctx.Err()
		}
		// URL 오류는 서버 주소만 담는다 — 요청 본문(토큰·CSR)은 담기지 않는다.
		return 0, fmt.Errorf("agent: POST %s failed: %w", path, err)
	}
	defer func() {
		_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 64<<10))
		_ = resp.Body.Close()
	}()

	switch {
	case resp.StatusCode == http.StatusNoContent:
		return resp.StatusCode, nil
	case resp.StatusCode >= 200 && resp.StatusCode < 300:
		if out != nil {
			if err := json.NewDecoder(io.LimitReader(resp.Body, 4<<20)).Decode(out); err != nil {
				return resp.StatusCode, fmt.Errorf("agent: decode response of %s failed", path)
			}
		}
		return resp.StatusCode, nil
	default:
		return resp.StatusCode, fmt.Errorf("agent: POST %s returned status %d", path, resp.StatusCode)
	}
}

// newRequestID는 요청 추적용 128비트 난수 ID를 만든다 (crypto/rand).
func newRequestID() string {
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "agent-rand-unavailable"
	}
	return hex.EncodeToString(b[:])
}
