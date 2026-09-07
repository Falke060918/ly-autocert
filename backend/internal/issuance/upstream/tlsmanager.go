package upstream

import (
	"bytes"
	"context"
	"crypto/tls"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"
)

// HTTPError deliberately excludes response bodies, URLs and credentials.
type HTTPError struct {
	Status    int
	Ambiguous bool
}

func (e *HTTPError) Error() string   { return fmt.Sprintf("CA 요청 실패 (HTTP %d)", e.Status) }
func (e *HTTPError) Temporary() bool { return e.Status == 0 || e.Status == 429 || e.Status >= 500 }

type TLSManager struct {
	base, app, secret string
	client            *http.Client
	mu                sync.Mutex
	token             string
	expires           time.Time
}

func NewTLSManager(base, app, secret string) (*TLSManager, error) {
	if e := ValidateURL(base); e != nil {
		return nil, e
	}
	tr := http.DefaultTransport.(*http.Transport).Clone()
	tr.TLSClientConfig = &tls.Config{MinVersion: tls.VersionTLS12}
	return &TLSManager{base: strings.TrimRight(base, "/"), app: app, secret: secret, client: &http.Client{Transport: tr, Timeout: 30 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}}, nil
}
func (c *TLSManager) request(ctx context.Context, method, path, token string, in, out any) error {
	var body io.Reader
	if in != nil {
		b, e := json.Marshal(in)
		if e != nil {
			return errors.New("CA 요청 직렬화 실패")
		}
		body = bytes.NewReader(b)
	}
	req, e := http.NewRequestWithContext(ctx, method, c.base+path, body)
	if e != nil {
		return errors.New("CA 요청 생성 실패")
	}
	req.Header.Set("Content-Type", "application/json")
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	resp, e := c.client.Do(req)
	if e != nil {
		return &HTTPError{Ambiguous: method == "POST"}
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return &HTTPError{Status: resp.StatusCode, Ambiguous: method == "POST" && resp.StatusCode >= 500}
	}
	if out == nil {
		return nil
	}
	b, e := io.ReadAll(io.LimitReader(resp.Body, (4<<20)+1))
	if e != nil || len(b) > 4<<20 {
		return &HTTPError{Ambiguous: method == "POST"}
	}
	var meta struct {
		Code int `json:"response_code"`
	}
	if json.Unmarshal(b, &meta) != nil {
		return &HTTPError{Ambiguous: method == "POST"}
	}
	if meta.Code >= 400 {
		return &HTTPError{Status: meta.Code, Ambiguous: method == "POST" && meta.Code >= 500}
	}
	if json.Unmarshal(b, out) != nil {
		return &HTTPError{Ambiguous: method == "POST"}
	}
	return nil
}
func (c *TLSManager) login(ctx context.Context, force bool) (string, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if !force && c.token != "" && time.Now().Before(c.expires) {
		return c.token, nil
	}
	var out struct {
		Token   string `json:"access_token"`
		Expires int    `json:"expires_in_seconds"`
	}
	if e := c.request(ctx, "POST", "/v1/auth/login", "", map[string]string{"app_id": c.app, "secret": c.secret}, &out); e != nil {
		return "", e
	}
	if out.Token == "" {
		return "", errors.New("CA 로그인 응답에 토큰이 없습니다")
	}
	c.token = out.Token
	c.expires = time.Now().Add(time.Duration(out.Expires-30) * time.Second)
	return c.token, nil
}
func (c *TLSManager) call(ctx context.Context, method, path string, in, out any) error {
	token, e := c.login(ctx, false)
	if e != nil {
		return e
	}
	e = c.request(ctx, method, path, token, in, out)
	var he *HTTPError
	if errors.As(e, &he) && he.Status == 401 {
		token, e = c.login(ctx, true)
		if e != nil {
			return e
		}
		return c.request(ctx, method, path, token, in, out)
	}
	return e
}
func (c *TLSManager) TestConnection(ctx context.Context) error { _, e := c.login(ctx, true); return e }
func (c *TLSManager) ListProducts(ctx context.Context) ([]Product, error) {
	var out struct {
		Data []Product `json:"data"`
	}
	e := c.call(ctx, "GET", "/v1/products", nil, &out)
	if out.Data == nil {
		out.Data = []Product{}
	}
	return out.Data, e
}
func (c *TLSManager) PlaceOrder(ctx context.Context, s Spec, csr string, subscriber Subscriber) (Snapshot, error) {
	var out Snapshot
	if csr == "" {
		return out, errors.New("CSR이 필요합니다")
	}
	// The CA validates the base domain; wildcard SANs stay in the certificate.
	validations := make([]Validation, 0, len(s.Validations))
	methods := make(map[string]string, len(s.Validations))
	for _, validation := range s.Validations {
		validation.Name = strings.TrimPrefix(validation.Name, "*.")
		if method, exists := methods[validation.Name]; exists {
			if method != validation.Method {
				return out, errors.New("같은 도메인의 검증 방식이 서로 다릅니다")
			}
			continue
		}
		methods[validation.Name] = validation.Method
		validations = append(validations, validation)
	}
	in := map[string]any{"product_id": s.ProductID, "signing_ca_type": s.SigningCA, "subscriber": subscriber, "certificate": map[string]any{"csr": csr, "dns_names": s.DNSNames, "validity": map[string]any{"type": "length", "length_in_days": s.ValidityDays}}, "domain_validations": validations}
	e := c.call(ctx, "POST", "/v1/orders", in, &out)
	if e == nil && out.Code == "" {
		e = &HTTPError{Ambiguous: true}
	}
	return out, e
}
func (c *TLSManager) FetchOrder(ctx context.Context, code string) (Snapshot, error) {
	var out Snapshot
	e := c.call(ctx, "GET", "/v1/orders/"+url.PathEscape(code), nil, &out)
	return out, e
}
func (c *TLSManager) Cancel(ctx context.Context, code string) error {
	return c.call(ctx, "POST", "/v1/orders/"+url.PathEscape(code)+"/cancel", map[string]any{"reason": "Cancelled by AutoCert operator", "add_domains_to_blacklist": false}, nil)
}
