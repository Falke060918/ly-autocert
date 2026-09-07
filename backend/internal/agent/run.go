package agent

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"runtime"
	"time"

	"github.com/Falke060918/INHERIT_AutoCert/backend/internal/agentwire"
	"github.com/Falke060918/INHERIT_AutoCert/backend/internal/deploysign"
)

func (c *Client) request(ctx context.Context, method, path, token string, in, out any) (int, error) {
	var body io.Reader
	if in != nil {
		b, e := json.Marshal(in)
		if e != nil {
			return 0, e
		}
		body = bytes.NewReader(b)
	}
	req, e := http.NewRequestWithContext(ctx, method, c.baseURL+path, body)
	if e != nil {
		return 0, errors.New("요청 생성 실패")
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Deploy-Lease", token)
	resp, e := c.httpc.Do(req)
	if e != nil {
		return 0, errors.New("게이트웨이 요청 실패")
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return resp.StatusCode, errors.New("게이트웨이 요청 거부")
	}
	if out != nil && resp.StatusCode != 204 {
		d := json.NewDecoder(io.LimitReader(resp.Body, 4<<20))
		d.DisallowUnknownFields()
		if e = d.Decode(out); e != nil {
			return resp.StatusCode, errors.New("게이트웨이 응답 형식 오류")
		}
	}
	return resp.StatusCode, nil
}

// Run is Linux-only and serializes the host's deployment process using an
// advisory file lock. The durable replay journal survives process termination.
func Run(ctx context.Context, server, dir string, allowed []string, wrappingKey []byte, pub ed25519.PublicKey) error {
	if runtime.GOOS != "linux" {
		return errors.New("배포 실행은 Linux만 지원합니다")
	}
	if len(pub) != ed25519.PublicKeySize {
		return errKeySource
	}
	unlock, e := lockState(dir)
	if e != nil {
		return e
	}
	defer unlock()
	id, e := LoadIdentity(dir, wrappingKey)
	if e != nil {
		return e
	}
	c, e := NewClient(server, id)
	if e != nil {
		return e
	}
	c.httpc.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	d := Deployer{Executor: SystemExecutor{}, AllowedDirs: allowed}
	ticker := time.NewTicker(10 * time.Second)
	defer ticker.Stop()
	for {
		if ctx.Err() != nil {
			return nil
		}
		if time.Until(id.Leaf().NotAfter) < 30*24*time.Hour {
			csr, e := id.NewCSRPEM()
			if e != nil {
				return e
			}
			var out struct {
				Cert string `json:"client_cert_pem"`
			}
			_, e = c.request(ctx, "POST", pathRenew, "", map[string]string{"csr_pem": string(csr)}, &out)
			if e != nil {
				return e
			}
			if e = id.InstallClientCert([]byte(out.Cert)); e != nil {
				return e
			}
			if e = ReplaceClientCert(dir, []byte(out.Cert)); e != nil {
				return e
			}
		}
		_, _ = c.request(ctx, "POST", "/agent/v1/heartbeat", "", nil, nil)
		var signed agentwire.SignedLease
		status, e := c.request(ctx, "POST", pathLease, "", nil, &signed)
		if e == nil && status != 204 {
			l, e := acceptLease(dir, pub, signed, id.AgentID, time.Now())
			if e != nil {
				return e
			}
			if e = c.execute(ctx, d, l); e != nil {
				return e
			}
		}
		select {
		case <-ctx.Done():
			return nil
		case <-ticker.C:
		}
	}
}
func acceptLease(dir string, pub ed25519.PublicKey, signed agentwire.SignedLease, id string, now time.Time) (agentwire.Lease, error) {
	l, e := deploysign.Verify(pub, signed, id, now)
	if e != nil {
		return l, e
	}
	if e = consumeLease(dir, l); e != nil {
		return agentwire.Lease{}, e
	}
	return l, nil
}
func (c *Client) execute(ctx context.Context, d Deployer, l agentwire.Lease) error {
	if _, e := c.request(ctx, "POST", pathJob(l.JobID, "start"), l.LeaseToken, nil, nil); e != nil {
		return e
	}
	jobCtx, cancel := context.WithCancel(ctx)
	done := make(chan struct{})
	go func() {
		defer close(done)
		t := time.NewTicker(25 * time.Second)
		defer t.Stop()
		for {
			select {
			case <-jobCtx.Done():
				return
			case <-t.C:
				if _, e := c.request(jobCtx, "POST", pathJob(l.JobID, "heartbeat"), l.LeaseToken, nil, nil); e != nil {
					cancel()
					return
				}
			}
		}
	}()
	var m agentwire.Material
	_, e := c.request(jobCtx, "GET", pathJob(l.JobID, "material"), l.LeaseToken, nil, &m)
	result := agentwire.Result{Status: "failed", Error: "배포 자료 수령 실패", LeaseToken: l.LeaseToken, Steps: []agentwire.Step{}}
	if e == nil {
		result = d.Deploy(jobCtx, l, m)
	}
	m.KeyPEM = ""
	cancel()
	<-done
	// Report independently of the caller's cancellation, including rollback outcome.
	reportCtx, stop := context.WithTimeout(context.Background(), 30*time.Second)
	defer stop()
	for n := 0; n < 3; n++ {
		if _, e = c.request(reportCtx, "POST", pathJob(l.JobID, "complete"), l.LeaseToken, result, nil); e == nil {
			return nil
		}
	}
	return errors.New("배포 결과 보고 실패. 콘솔과 대상 서비스 상태를 확인하세요")
}
