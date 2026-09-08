package issuance

import (
	"bytes"
	"context"
	"crypto/sha256"
	"crypto/x509"
	"database/sql"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"log/slog"
	"sync"
	"time"

	"github.com/Falke060918/INHERIT_AutoCert/backend/internal/certparse"
	"github.com/Falke060918/INHERIT_AutoCert/backend/internal/issuance/upstream"
	"github.com/Falke060918/INHERIT_AutoCert/backend/internal/sensorca"
	"github.com/Falke060918/INHERIT_AutoCert/backend/internal/store"
)

type Service struct {
	Store   *store.Store
	Key     []byte
	mu      sync.Mutex
	clients map[string]upstream.Connector
	connect func(string, string, string) (upstream.Connector, error)
}

func New(st *store.Store, key []byte) *Service {
	return &Service{Store: st, Key: key, clients: map[string]upstream.Connector{}, connect: func(base, app, secret string) (upstream.Connector, error) {
		return upstream.NewTLSManager(base, app, secret)
	}}
}
func (s *Service) Enabled() bool { return len(s.Key) == 32 }
func (s *Service) Connector(ctx context.Context, id string) (upstream.Connector, *store.CAConnection, error) {
	if !s.Enabled() {
		return nil, nil, errors.New("ISSUANCE_ENC_KEY 설정이 필요합니다")
	}
	c, e := s.Store.GetCAConnection(ctx, id)
	if e != nil {
		return nil, nil, e
	}
	if !c.Active {
		return nil, nil, errors.New("CA 연결이 비활성 상태입니다")
	}
	sum := sha256.New()
	sum.Write([]byte(c.BaseURL))
	if c.LegacyAppID || len(c.AppID) == 0 {
		return nil, nil, errors.New("CA 자격증명 이행이 필요합니다")
	}
	sum.Write(c.AppID)
	sum.Write(c.AppIDNonce)
	sum.Write(c.Secret)
	sum.Write(c.Nonce)
	cacheKey := id + hex.EncodeToString(sum.Sum(nil))
	s.mu.Lock()
	defer s.mu.Unlock()
	if client := s.clients[cacheKey]; client != nil {
		return client, c, nil
	}
	secret, e := sensorca.Open(s.Key, c.Secret, c.Nonce)
	if e != nil {
		return nil, nil, errors.New("CA 자격증명 복호화 실패")
	}
	defer clear(secret)
	appID, e := sensorca.Open(s.Key, c.AppID, c.AppIDNonce)
	if e != nil {
		return nil, nil, errors.New("CA 자격증명 복호화 실패")
	}
	defer clear(appID)
	client, e := s.connect(c.BaseURL, string(appID), string(secret))
	if e != nil {
		return nil, nil, e
	}
	if len(s.clients) > 100 {
		s.clients = map[string]upstream.Connector{}
	}
	s.clients[cacheKey] = client
	return client, c, nil
}
func (s *Service) Create(ctx context.Context, conn string, spec upstream.Spec, a store.IssuanceActor) (*store.IssuanceOrder, error) {
	k, e := GenerateKey(spec, s.Key)
	if e != nil {
		return nil, e
	}
	return s.Store.CreateIssuanceOrder(ctx, conn, spec, k.Ciphertext, k.Nonce, k.CSR, a)
}
func (s *Service) Start(ctx context.Context, logger *slog.Logger) func() {
	ctx, cancel := context.WithCancel(ctx)
	done := make(chan struct{})
	go func() {
		defer close(done)
		if !s.Enabled() {
			return
		}
		ticker := time.NewTicker(30 * time.Second)
		defer ticker.Stop()
		for {
			if e := s.Tick(ctx); e != nil && ctx.Err() == nil {
				logger.Error("issuance worker step failed")
			}
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
			}
		}
	}()
	return func() { cancel(); <-done }
}
func (s *Service) Tick(ctx context.Context) error {
	for n := 0; n < 20; n++ {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		o, e := s.Store.ClaimIssuanceOrder(ctx)
		if errors.Is(e, sql.ErrNoRows) {
			return nil
		}
		if e != nil {
			return e
		}
		if e = s.process(ctx, o); e != nil {
			return e
		}
	}
	return nil
}
func (s *Service) process(ctx context.Context, o *store.IssuanceOrder) error {
	result := store.IssuanceResult{Status: o.Status, Code: o.Code, UpstreamStatus: o.UpstreamStatus, Validation: o.Validation, Delay: time.Minute, Attempts: o.Attempts}
	finish := func() error { return s.Store.FinishIssuanceStep(ctx, o, result) }
	// Without a CA idempotency key, a crashed/ambiguous POST must never be replayed.
	if o.Code == "" && o.SubmissionStarted != nil {
		result.Status = "manual_required"
		result.Error = "CA 주문 전송 결과를 확인할 수 없습니다. CA 포털에서 중복 주문 여부를 확인하세요"
		result.Kind = "failed"
		return finish()
	}
	client, c, e := s.Connector(ctx, o.ConnectionID)
	var snap upstream.Snapshot
	submitted := false
	if e == nil {
		if o.Code == "" {
			var csr string
			csr, e = s.Store.StartIssuanceSubmission(ctx, o)
			if e != nil {
				return e
			}
			submitted = true
			snap, e = client.PlaceOrder(ctx, o.Spec, csr, c.Subscriber)
		} else {
			snap, e = client.FetchOrder(ctx, o.Code)
		}
	}
	if e != nil {
		result.Attempts++
		result.Error = "CA 연결 또는 주문 처리 실패"
		var he *upstream.HTTPError
		if errors.As(e, &he) {
			result.Error = he.Error()
		}
		result.Status = "issue_failed"
		result.Kind = "failed"
		if submitted {
			result.Status = "manual_required"
			result.Error = "주문 전송 후 결과 확인이 필요합니다. CA 포털을 확인하세요"
		} else if he != nil && he.Temporary() && result.Attempts <= 10 {
			result.Status = o.Status
			result.Kind = "retry_scheduled"
			delay := time.Minute * time.Duration(1<<min(result.Attempts-1, 5))
			result.Delay = min(delay, 30*time.Minute)
		}
		return finish()
	}
	if snap.Code == "" || o.Code != "" && snap.Code != o.Code {
		result.Status = "manual_required"
		result.Error = "CA 주문 식별자가 일치하지 않습니다"
		result.Kind = "failed"
		return finish()
	}
	result.Code = snap.Code
	result.UpstreamStatus = snap.Status
	result.Validation = snap.Validation
	result.Attempts = 0
	switch snap.Status {
	case "issued":
		csr, e := s.Store.GetIssuanceCSR(ctx, o.ID)
		if e != nil {
			return e
		}
		cert, certPEM, chainPEM, comparison, e := InspectIssued(snap, csr, o.Spec)
		if e != nil {
			result.Status = "issue_failed"
			result.Error = e.Error()
			result.Kind = "failed"
		} else {
			described := certparse.Describe(cert, time.Now())
			result.Certificate = &described
			result.CertPEM = certPEM
			result.ChainPEM = chainPEM
			result.Comparison = comparison
			result.Status = "completed"
			result.Kind = "issued"
		}
	case "ready":
		// The demo CA may accept an order before its asynchronous signing finishes.
		// Keep its code and poll; a fresh POST could create a duplicate order.
		result.Status = "issuing"
		if o.Code == "" {
			result.Kind = "order_placed"
		}
	case "pending":
		result.Status = "issuing"
		for _, v := range snap.Validation {
			if v.Status != "validated" {
				result.Status = "awaiting_validation"
				result.Delay = 2 * time.Minute
				break
			}
		}
		if o.Code == "" {
			result.Kind = "order_placed"
		} else if result.Status != o.Status {
			if result.Status == "awaiting_validation" {
				result.Kind = "validation_pending"
			} else {
				result.Kind = "validated"
			}
		}
	case "cancelled", "refunded", "expired", "error", "revoked":
		result.Status = "issue_failed"
		result.Kind = "failed"
		result.Error = "CA 주문 종결: " + snap.Status
	default:
		result.Status = "issue_failed"
		result.Kind = "failed"
		result.Error = "알 수 없는 CA 주문 상태"
	}
	return finish()
}
func (s *Service) Cancel(ctx context.Context, id string, a store.IssuanceActor) error {
	o, e := s.Store.GetIssuanceOrder(ctx, id)
	if e != nil {
		return e
	}
	if o.Code == "" {
		return s.Store.ChangeIssuanceOrder(ctx, id, "cancel", a)
	}
	o, e = s.Store.ClaimIssuanceCancel(ctx, id)
	if e != nil {
		return e
	}
	result := store.IssuanceResult{Status: o.Status, Code: o.Code, UpstreamStatus: o.UpstreamStatus, Validation: o.Validation, Delay: time.Minute, Actor: a}
	client, _, e := s.Connector(ctx, o.ConnectionID)
	if e == nil {
		e = client.Cancel(ctx, o.Code)
	}
	if e == nil {
		result.Status = "cancelled"
		result.Kind = "cancelled"
	} else {
		result.Error = "CA 취소 실패. CA 상태를 확인한 뒤 다시 시도하세요"
		result.Kind = "cancel_failed"
	}
	if saveErr := s.Store.FinishIssuanceStep(ctx, o, result); saveErr != nil {
		return saveErr
	}
	return e
}
func certificateData(raw string) ([]byte, error) {
	if bytes.Contains([]byte(raw), []byte("-----BEGIN")) {
		return []byte(raw), nil
	}
	return base64.StdEncoding.DecodeString(raw)
}
func ValidateIssued(s upstream.Snapshot, csrPEM string, names []string) (*x509.Certificate, string, string, error) {
	cert, leaf, chain, _, err := InspectIssued(s, csrPEM, upstream.Spec{DNSNames: names})
	return cert, leaf, chain, err
}
