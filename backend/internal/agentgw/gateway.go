package agentgw

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"encoding/pem"
	"errors"
	"net/http"
	"regexp"
	"time"

	"github.com/Falke060918/INHERIT_AutoCert/backend/internal/agentwire"
	"github.com/Falke060918/INHERIT_AutoCert/backend/internal/deploysign"
	"github.com/Falke060918/INHERIT_AutoCert/backend/internal/domain"
	"github.com/Falke060918/INHERIT_AutoCert/backend/internal/sensorca"
	"github.com/Falke060918/INHERIT_AutoCert/backend/internal/store"
	"golang.org/x/time/rate"
)

type Gateway struct {
	st     *store.Store
	ca     *sensorca.CA
	key    []byte
	mux    *http.ServeMux
	lim    *rate.Limiter
	signer *deploysign.Signer
}

func New(ctx context.Context, st *store.Store, caKey, key []byte, signer *deploysign.Signer) (*Gateway, error) {
	rec, e := st.LoadSensorCA(ctx)
	if e != nil {
		return nil, e
	}
	der, e := sensorca.Open(caKey, rec.KeyCiphertext, rec.KeyNonce)
	if e != nil {
		return nil, e
	}
	defer clear(der)
	ca, e := sensorca.Load(rec.CertPEM, der)
	if e != nil {
		return nil, e
	}
	g := newGateway(st, ca, key)
	g.signer = signer
	return g, nil
}

func newGateway(st *store.Store, ca *sensorca.CA, key []byte) *Gateway {
	g := &Gateway{st: st, ca: ca, key: key, mux: http.NewServeMux(), lim: rate.NewLimiter(1, 5)}
	g.mux.HandleFunc("POST /agent/v1/enroll", g.enroll)
	g.mux.HandleFunc("POST /agent/v1/renew", g.auth(g.renew))
	g.mux.HandleFunc("POST /agent/v1/heartbeat", g.auth(g.heartbeat))
	g.mux.HandleFunc("POST /agent/v1/jobs/lease", g.auth(g.lease))
	g.mux.HandleFunc("POST /agent/v1/jobs/{id}/start", g.auth(g.start))
	g.mux.HandleFunc("POST /agent/v1/jobs/{id}/heartbeat", g.auth(g.jobHeartbeat))
	g.mux.HandleFunc("GET /agent/v1/jobs/{id}/material", g.auth(g.material))
	g.mux.HandleFunc("POST /agent/v1/jobs/{id}/complete", g.auth(g.complete))
	return g
}
func (g *Gateway) Handler() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Cache-Control", "no-store")
		w.Header().Set("X-Content-Type-Options", "nosniff")
		g.mux.ServeHTTP(w, r)
	})
}
func reply(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	if v != nil {
		_ = json.NewEncoder(w).Encode(v)
	}
}
func failure(w http.ResponseWriter, status int) {
	reply(w, status, map[string]string{"error": "에이전트 요청을 처리할 수 없습니다"})
}
func decode(w http.ResponseWriter, r *http.Request, out any) bool {
	d := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<20))
	d.DisallowUnknownFields()
	if e := d.Decode(out); e != nil {
		failure(w, 400)
		return false
	}
	return true
}
func certFP(b []byte) string {
	p, _ := pem.Decode(b)
	if p == nil {
		return ""
	}
	h := sha256.Sum256(p.Bytes)
	return hex.EncodeToString(h[:])
}

var uuid = regexp.MustCompile(`^[0-9a-fA-F]{8}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{12}$`)

func (g *Gateway) auth(h func(http.ResponseWriter, *http.Request, *domain.Agent)) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.TLS == nil || len(r.TLS.VerifiedChains) == 0 || len(r.TLS.PeerCertificates) == 0 {
			failure(w, 401)
			return
		}
		fp := sha256.Sum256(r.TLS.PeerCertificates[0].Raw)
		a, e := g.st.GetActiveAgentByFingerprint(r.Context(), hex.EncodeToString(fp[:]))
		if e != nil {
			failure(w, 401)
			return
		}
		if id := r.PathValue("id"); id != "" && !uuid.MatchString(id) {
			failure(w, 400)
			return
		}
		h(w, r, a)
	}
}
func (g *Gateway) enroll(w http.ResponseWriter, r *http.Request) {
	if !g.lim.Allow() {
		failure(w, 429)
		return
	}
	var in agentwire.EnrollRequest
	if !decode(w, r, &in) {
		return
	}
	if in.Validate() != nil {
		failure(w, 400)
		return
	}
	cert, e := g.ca.SignClientCSR([]byte(in.CSRPEM))
	if e != nil {
		failure(w, 400)
		return
	}
	a, e := g.st.EnrollAgent(r.Context(), store.EnrollAgentInput{Token: in.Token, ClientCertFingerprintSHA256: certFP(cert)})
	if e != nil {
		failure(w, 401)
		return
	}
	reply(w, 200, agentwire.EnrollResponse{AgentID: a.ID, ClientCertPEM: string(cert), CACertPEM: string(g.ca.CertPEM())})
}
func (g *Gateway) renew(w http.ResponseWriter, r *http.Request, a *domain.Agent) {
	var in struct {
		CSR string `json:"csr_pem"`
	}
	if !decode(w, r, &in) {
		return
	}
	cert, e := g.ca.SignClientCSR([]byte(in.CSR))
	if e != nil {
		failure(w, 400)
		return
	}
	_, e = g.st.RenewAgentFingerprint(r.Context(), store.RenewAgentInput{OldFingerprint: *a.ClientCertFingerprintSHA256, NewFingerprint: certFP(cert)})
	if e != nil {
		failure(w, 409)
		return
	}
	reply(w, 200, map[string]string{"client_cert_pem": string(cert)})
}
func (g *Gateway) heartbeat(w http.ResponseWriter, r *http.Request, a *domain.Agent) {
	if e := g.st.HeartbeatAgent(r.Context(), a.ID, requestMeta(r)); e != nil {
		failure(w, 500)
		return
	}
	reply(w, 204, nil)
}
func requestMeta(r *http.Request) store.IssuanceActor {
	v := r.Header.Get("X-Request-ID")
	if len(v) > 128 {
		v = ""
	}
	return store.IssuanceActor{RequestID: &v}
}
func (g *Gateway) lease(w http.ResponseWriter, r *http.Request, a *domain.Agent) {
	if g.signer == nil {
		failure(w, 503)
		return
	}
	l, e := g.st.LeaseDeployJob(r.Context(), a.ID, func(l agentwire.Lease) (agentwire.SignedLease, error) { return g.signer.Sign(l, time.Now()) }, requestMeta(r))
	if errors.Is(e, sql.ErrNoRows) {
		reply(w, 204, nil)
		return
	}
	if e != nil {
		failure(w, 500)
		return
	}
	reply(w, 200, l)
}
func leaseToken(w http.ResponseWriter, r *http.Request) string {
	t := r.Header.Get("X-Deploy-Lease")
	if !uuid.MatchString(t) {
		failure(w, 400)
		return ""
	}
	return t
}
func (g *Gateway) touch(w http.ResponseWriter, r *http.Request, a *domain.Agent, action string) {
	token := leaseToken(w, r)
	if token == "" {
		return
	}
	if e := g.st.TouchDeployJob(r.Context(), a.ID, r.PathValue("id"), token, action, requestMeta(r)); e != nil {
		failure(w, 409)
		return
	}
	reply(w, 204, nil)
}
func (g *Gateway) start(w http.ResponseWriter, r *http.Request, a *domain.Agent) {
	g.touch(w, r, a, "start")
}
func (g *Gateway) jobHeartbeat(w http.ResponseWriter, r *http.Request, a *domain.Agent) {
	g.touch(w, r, a, "heartbeat")
}
func (g *Gateway) material(w http.ResponseWriter, r *http.Request, a *domain.Agent) {
	token := leaseToken(w, r)
	if token == "" {
		return
	}
	m, e := g.st.DeploymentMaterial(r.Context(), a.ID, r.PathValue("id"), token, g.key)
	if e != nil {
		failure(w, 403)
		return
	}
	reply(w, 200, m)
	m.KeyPEM = ""
}
func (g *Gateway) complete(w http.ResponseWriter, r *http.Request, a *domain.Agent) {
	var result agentwire.Result
	if !decode(w, r, &result) {
		return
	}
	if !uuid.MatchString(result.LeaseToken) || result.Validate() != nil {
		failure(w, 400)
		return
	}
	if e := g.st.CompleteDeployJob(r.Context(), a.ID, r.PathValue("id"), result); e != nil {
		failure(w, 409)
		return
	}
	reply(w, 204, nil)
}
func (g *Gateway) StartReaper(ctx context.Context) func() {
	ctx, cancel := context.WithCancel(ctx)
	done := make(chan struct{})
	go func() {
		defer close(done)
		t := time.NewTicker(30 * time.Second)
		defer t.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-t.C:
				_ = g.st.ReapDeployJobs(ctx)
			}
		}
	}()
	return func() { cancel(); <-done }
}
