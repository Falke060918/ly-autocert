package sensor

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

	"github.com/Falke060918/INHERIT_AutoCert/backend/internal/sensorwire"
)

// 서버 게이트웨이 경로 규약 (docs/00-spec.md 7.8). 게이트웨이 유닛과 wire 계약으로 맞춘다.
const (
	pathEnroll = "/sensor/v1/enroll"
	pathRenew  = "/sensor/v1/renew"
	pathLease  = "/sensor/v1/jobs/lease"
)

// pathJob은 Job 하위 액션 경로(start·heartbeat·observations·complete)를 만든다.
func pathJob(jobID, action string) string {
	return "/sensor/v1/jobs/" + url.PathEscape(jobID) + "/" + action
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

// Lease는 작업 1건을 리스한다. 배정할 작업이 없으면 (nil, nil)이다 (서버 204).
func (c *Client) Lease(ctx context.Context) (*sensorwire.LeaseResponse, error) {
	var out sensorwire.LeaseResponse
	status, err := postJSON(ctx, c.httpc, c.baseURL, pathLease, nil, &out)
	if err != nil {
		return nil, err
	}
	if status == http.StatusNoContent {
		return nil, nil
	}
	return &out, nil
}

// Start는 leased → running 전이를 보고한다 (대상 전개 후 총 엔드포인트 수).
func (c *Client) Start(ctx context.Context, jobID string, totalEndpoints int) error {
	req := sensorwire.JobStartRequest{TotalEndpoints: totalEndpoints}
	if err := req.Validate(); err != nil {
		return err
	}
	_, err := postJSON(ctx, c.httpc, c.baseURL, pathJob(jobID, "start"), req, nil)
	return err
}

// Heartbeat는 진행률을 보고하고 리스를 연장한다.
func (c *Client) Heartbeat(ctx context.Context, jobID string, req sensorwire.HeartbeatRequest) error {
	if err := req.Validate(); err != nil {
		return err
	}
	_, err := postJSON(ctx, c.httpc, c.baseURL, pathJob(jobID, "heartbeat"), req, nil)
	return err
}

// SendObservations는 관측 배치를 전송한다. 서버가 (job_id, ip, port)로 멱등 처리하므로
// 같은 배치의 재전송은 안전하다.
func (c *Client) SendObservations(ctx context.Context, jobID string, batch sensorwire.ObservationBatch) error {
	if err := batch.Validate(); err != nil {
		return err
	}
	_, err := postJSON(ctx, c.httpc, c.baseURL, pathJob(jobID, "observations"), batch, nil)
	return err
}

// Complete는 running → completed/failed 전이를 보고한다.
func (c *Client) Complete(ctx context.Context, jobID string, req sensorwire.CompleteRequest) error {
	if err := req.Validate(); err != nil {
		return err
	}
	_, err := postJSON(ctx, c.httpc, c.baseURL, pathJob(jobID, "complete"), req, nil)
	return err
}

// Renew는 새 CSR로 클라이언트 인증서 갱신을 요청한다. mTLS(기존 인증서) 위에서 호출된다.
func (c *Client) Renew(ctx context.Context, csrPEM []byte) (*sensorwire.RenewResponse, error) {
	req := sensorwire.RenewRequest{CSRPEM: string(csrPEM)}
	if err := req.Validate(); err != nil {
		return nil, err
	}
	var out sensorwire.RenewResponse
	if _, err := postJSON(ctx, c.httpc, c.baseURL, pathRenew, req, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// normalizeServerURL은 서버 URL을 검증하고 꼬리 슬래시를 제거한다.
// 센서-서버 통신은 mTLS 1경로뿐이므로 https만 허용한다 (00-spec 7.8 배치 원칙).
func normalizeServerURL(raw string) (string, error) {
	u, err := url.Parse(strings.TrimSpace(raw))
	if err != nil || u.Host == "" {
		return "", errors.New("sensor: --server must be a valid URL (https://host:port)")
	}
	if u.Scheme != "https" {
		return "", errors.New("sensor: --server must use https (mTLS only)")
	}
	if u.Path != "" && u.Path != "/" {
		return "", errors.New("sensor: --server must not contain a path")
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
			return 0, fmt.Errorf("sensor: encode request for %s failed", path)
		}
		body = bytes.NewReader(b)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, baseURL+path, body)
	if err != nil {
		return 0, fmt.Errorf("sensor: build request for %s failed", path)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Request-ID", newRequestID())

	resp, err := hc.Do(req)
	if err != nil {
		if ctx.Err() != nil {
			return 0, ctx.Err()
		}
		// URL 오류는 서버 주소만 담는다 — 요청 본문(토큰·CSR)은 담기지 않는다.
		return 0, fmt.Errorf("sensor: POST %s failed: %w", path, err)
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
				return resp.StatusCode, fmt.Errorf("sensor: decode response of %s failed", path)
			}
		}
		return resp.StatusCode, nil
	default:
		return resp.StatusCode, fmt.Errorf("sensor: POST %s returned status %d", path, resp.StatusCode)
	}
}

// newRequestID는 요청 추적용 128비트 난수 ID를 만든다 (crypto/rand).
func newRequestID() string {
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "sensor-rand-unavailable"
	}
	return hex.EncodeToString(b[:])
}
