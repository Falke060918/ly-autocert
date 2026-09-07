package issuance

import (
	"bytes"
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"crypto/x509/pkix"
	"database/sql"
	"encoding/base64"
	"encoding/json"
	"encoding/pem"
	"errors"
	"math/big"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/Falke060918/INHERIT_AutoCert/backend/internal/agentwire"
	"github.com/Falke060918/INHERIT_AutoCert/backend/internal/db"
	"github.com/Falke060918/INHERIT_AutoCert/backend/internal/issuance/upstream"
	"github.com/Falke060918/INHERIT_AutoCert/backend/internal/sensorca"
	"github.com/Falke060918/INHERIT_AutoCert/backend/internal/store"
)

func specFixture() upstream.Spec {
	return upstream.Spec{DNSNames: []string{"app.example.test"}, Algorithm: "ecdsa_p256", ValidityDays: 90, ProductID: 1, SigningCA: "automatic", Validations: []upstream.Validation{{Name: "app.example.test", Method: "txt_dns_record"}}}
}
func randomBytes(t *testing.T, n int) []byte {
	t.Helper()
	b := make([]byte, n)
	if _, e := rand.Read(b); e != nil {
		t.Fatal(e)
	}
	return b
}
func TestSealedKeyAlgorithmsAndCertificateValidation(t *testing.T) {
	for _, alg := range []string{"rsa2048", "rsa3072", "rsa4096", "ecdsa_p256", "ecdsa_p384"} {
		t.Run(alg, func(t *testing.T) {
			spec := specFixture()
			spec.Algorithm = alg
			seal := randomBytes(t, 32)
			k, e := GenerateKey(spec, seal)
			if e != nil {
				t.Fatal(e)
			}
			der, e := sensorca.Open(seal, k.Ciphertext, k.Nonce)
			if e != nil {
				t.Fatal(e)
			}
			if bytes.Equal(der, k.Ciphertext) || strings.Contains(k.CSR, "PRIVATE KEY") {
				t.Fatal("plaintext key leak")
			}
			if _, e = sensorca.Open(randomBytes(t, 32), k.Ciphertext, k.Nonce); e == nil {
				t.Fatal("wrong encryption key accepted")
			}
			snap := issuedSnapshot(t, k.CSR)
			if _, _, _, e = ValidateIssued(snap, k.CSR, spec.DNSNames); e != nil {
				t.Fatal(e)
			}
			if _, _, _, e = ValidateIssued(snap, k.CSR, []string{"other.example.test"}); e == nil {
				t.Fatal("domain mismatch accepted")
			}
			other, e := GenerateKey(spec, seal)
			if e != nil {
				t.Fatal(e)
			}
			if _, _, _, e = ValidateIssued(snap, other.CSR, spec.DNSNames); e == nil {
				t.Fatal("CSR mismatch accepted")
			}
		})
	}
}
func issuedSnapshot(t *testing.T, csrPEM string) upstream.Snapshot {
	t.Helper()
	p, _ := pem.Decode([]byte(csrPEM))
	csr, e := x509.ParseCertificateRequest(p.Bytes)
	if e != nil {
		t.Fatal(e)
	}
	caKey, e := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if e != nil {
		t.Fatal(e)
	}
	caTemplate := &x509.Certificate{SerialNumber: big.NewInt(1), Subject: pkix.Name{CommonName: "Test CA"}, IsCA: true, BasicConstraintsValid: true, KeyUsage: x509.KeyUsageCertSign, NotBefore: time.Now().Add(-time.Hour), NotAfter: time.Now().Add(365 * 24 * time.Hour)}
	caDER, e := x509.CreateCertificate(rand.Reader, caTemplate, caTemplate, &caKey.PublicKey, caKey)
	if e != nil {
		t.Fatal(e)
	}
	ca, e := x509.ParseCertificate(caDER)
	if e != nil {
		t.Fatal(e)
	}
	serial, e := rand.Int(rand.Reader, new(big.Int).Lsh(big.NewInt(1), 128))
	if e != nil {
		t.Fatal(e)
	}
	cert := &x509.Certificate{SerialNumber: serial, Subject: csr.Subject, DNSNames: csr.DNSNames, NotBefore: time.Now().Add(-time.Minute), NotAfter: time.Now().Add(90 * 24 * time.Hour), KeyUsage: x509.KeyUsageDigitalSignature, ExtKeyUsage: []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth}}
	der, e := x509.CreateCertificate(rand.Reader, cert, ca, csr.PublicKey, caKey)
	if e != nil {
		t.Fatal(e)
	}
	s := upstream.Snapshot{Code: "fake-order", Status: "issued", Validation: []upstream.DomainValidation{{Name: "app.example.test", Status: "validated"}}}
	s.Certificate.Base64 = base64.StdEncoding.EncodeToString(der)
	s.Certificate.Chain = string(pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: caDER}))
	return s
}

type fakeCA struct {
	t      *testing.T
	csr    string
	places int
	fail   error
	issued bool
}

func (f *fakeCA) TestConnection(context.Context) error { return nil }
func (f *fakeCA) ListProducts(context.Context) ([]upstream.Product, error) {
	return []upstream.Product{{ID: 1}}, nil
}
func (f *fakeCA) PlaceOrder(_ context.Context, _ upstream.Spec, csr string, _ upstream.Subscriber) (upstream.Snapshot, error) {
	f.places++
	f.csr = csr
	if f.fail != nil {
		return upstream.Snapshot{}, f.fail
	}
	return upstream.Snapshot{Code: "fake-order", Status: "pending", Validation: []upstream.DomainValidation{{Name: "app.example.test", Status: "pending", TXT: "public-challenge"}}}, nil
}
func (f *fakeCA) FetchOrder(context.Context, string) (upstream.Snapshot, error) {
	if f.fail != nil {
		return upstream.Snapshot{}, f.fail
	}
	return issuedSnapshot(f.t, f.csr), nil
}
func (f *fakeCA) Cancel(context.Context, string) error { return f.fail }
func integration(t *testing.T) (*sql.DB, *Service, *store.CAConnection, *fakeCA) {
	t.Helper()
	dsn := os.Getenv("TEST_DATABASE_URL")
	if dsn == "" {
		t.Skip("TEST_DATABASE_URL not set")
	}
	pool, e := db.Connect(context.Background(), dsn)
	if e != nil {
		t.Fatal(e)
	}
	t.Cleanup(func() { pool.Close() })
	if e = db.Migrate(pool); e != nil {
		t.Fatal(e)
	}
	st := store.New(pool)
	svc := New(st, randomBytes(t, 32))
	secret, nonce, e := sensorca.Seal(svc.Key, randomBytes(t, 32))
	if e != nil {
		t.Fatal(e)
	}
	app, appNonce, e := sensorca.Seal(svc.Key, []byte(base64.RawURLEncoding.EncodeToString(randomBytes(t, 12))))
	if e != nil {
		t.Fatal(e)
	}
	c, e := st.SaveCAConnection(context.Background(), store.CAConnection{Name: t.Name(), BaseURL: "https://ca.example.test/api", AppID: app, AppIDNonce: appNonce, Secret: secret, Nonce: nonce, Active: true, Subscriber: upstream.Subscriber{ExistingID: 1, Contact: upstream.Contact{ExistingID: 1}}}, store.IssuanceActor{})
	if e != nil {
		t.Fatal(e)
	}
	fake := &fakeCA{t: t}
	svc.connect = func(string, string, string) (upstream.Connector, error) { return fake, nil }
	t.Cleanup(func() {
		pool.Exec(`DELETE FROM deploy_jobs WHERE order_id IN (SELECT id FROM issuance_orders WHERE ca_connection_id=$1::uuid)`, c.ID)
		pool.Exec(`DELETE FROM issuance_private_keys WHERE order_id IN (SELECT id FROM issuance_orders WHERE ca_connection_id=$1::uuid)`, c.ID)
		pool.Exec(`DELETE FROM issuance_order_events WHERE order_id IN (SELECT id FROM issuance_orders WHERE ca_connection_id=$1::uuid)`, c.ID)
		pool.Exec(`DELETE FROM issuance_orders WHERE ca_connection_id=$1::uuid`, c.ID)
		pool.Exec(`DELETE FROM ca_connections WHERE id=$1::uuid`, c.ID)
	})
	return pool, svc, c, fake
}
func TestIssuanceToDeploymentLeaseMaterialAndAudit(t *testing.T) {
	pool, s, c, fake := integration(t)
	ctx := context.Background()
	a := store.IssuanceActor{}
	if e := s.Store.SaveIssuanceSettings(ctx, store.IssuanceSettings{ApprovalRequired: true, AutoRenew: true}, a); e != nil {
		t.Fatal(e)
	}
	o, e := s.Create(ctx, c.ID, specFixture(), a)
	if e != nil {
		t.Fatal(e)
	}
	if o.Status != "approval_pending" {
		t.Fatal("approval bypassed")
	}
	if e = s.Tick(ctx); e != nil || fake.places != 0 {
		t.Fatal("unapproved order submitted")
	}
	if e = s.Store.ChangeIssuanceOrder(ctx, o.ID, "approve", a); e != nil {
		t.Fatal(e)
	}
	if e = s.Tick(ctx); e != nil {
		t.Fatal(e)
	}
	o, e = s.Store.GetIssuanceOrder(ctx, o.ID)
	if e != nil || o.Status != "awaiting_validation" || fake.places != 1 {
		t.Fatalf("validation state %v %v", o, e)
	}
	if _, e = pool.Exec(`UPDATE issuance_orders SET next_action_at=now() WHERE id=$1::uuid`, o.ID); e != nil {
		t.Fatal(e)
	}
	if e = s.Tick(ctx); e != nil {
		t.Fatal(e)
	}
	o, e = s.Store.GetIssuanceOrder(ctx, o.ID)
	if e != nil || o.Status != "completed" || o.CertificateID == nil {
		t.Fatalf("issuance result %v %v", o, e)
	}
	if fake.places != 1 {
		t.Fatal("duplicate CA order")
	}
	var locations int
	if e = pool.QueryRow(`SELECT count(*) FROM certificate_locations WHERE certificate_id=$1::uuid`, o.CertificateID).Scan(&locations); e != nil || locations != 0 {
		t.Fatal("issuance fabricated location")
	}
	ag, token, e := s.Store.CreateAgent(ctx, store.CreateAgentInput{Name: t.Name()})
	if e != nil {
		t.Fatal(e)
	}
	ag, e = s.Store.EnrollAgent(ctx, store.EnrollAgentInput{Token: token, ClientCertFingerprintSHA256: strings.Repeat("a", 64)})
	if e != nil {
		t.Fatal(e)
	}
	if _, e = s.Store.EnrollAgent(ctx, store.EnrollAgentInput{Token: token, ClientCertFingerprintSHA256: strings.Repeat("b", 64)}); e == nil {
		t.Fatal("token reused")
	}
	target, e := s.Store.SaveDeploymentTarget(ctx, agentwire.Target{AgentID: ag.ID, Kind: "nginx", ServiceUnit: "nginx", CertPath: "/etc/nginx/test.crt", KeyPath: "/etc/nginx/test.key", VerifyHost: "127.0.0.1", VerifyPort: 443, Active: true}, a)
	if e != nil {
		t.Fatal(e)
	}
	t.Cleanup(func() {
		pool.Exec(`DELETE FROM deploy_jobs WHERE agent_id=$1::uuid`, ag.ID)
		pool.Exec(`DELETE FROM deployment_targets WHERE agent_id=$1::uuid`, ag.ID)
		pool.Exec(`DELETE FROM agents WHERE id=$1::uuid`, ag.ID)
	})
	j, e := s.Store.CreateDeployJob(ctx, o.ID, target.ID, a)
	if e != nil {
		t.Fatal(e)
	}
	if _, e = s.Store.CreateDeployJob(ctx, o.ID, target.ID, a); e == nil {
		t.Fatal("duplicate target job")
	}
	assertLeaseRollbacks(t, s, ag.ID, j.ID)
	var pending string
	if e = pool.QueryRow(`SELECT status FROM deploy_jobs WHERE id=$1::uuid AND lease_token IS NULL`, j.ID).Scan(&pending); e != nil || pending != "pending" {
		t.Fatal("failed lease changed state", e)
	}
	l, e := leaseForTest(s.Store, ctx, ag.ID)
	if e != nil {
		t.Fatal(e)
	}
	b, _ := json.Marshal(l)
	if bytes.Contains(b, []byte("PRIVATE KEY")) || bytes.Contains(b, []byte("key_pem")) {
		t.Fatal("lease disclosed key")
	}
	if _, e = s.Store.DeploymentMaterial(ctx, "00000000-0000-0000-0000-000000000000", j.ID, l.LeaseToken, s.Key); e == nil {
		t.Fatal("foreign agent obtained material")
	}
	if _, e = s.Store.DeploymentMaterial(ctx, ag.ID, j.ID, l.LeaseToken, s.Key); e == nil {
		t.Fatal("material before start")
	}
	assertConcurrentStart(t, s, ag.ID, j.ID, l.LeaseToken)
	var before, after time.Time
	pool.QueryRow(`SELECT leased_until FROM deploy_jobs WHERE id=$1::uuid`, j.ID).Scan(&before)
	badIP := "invalid-test-ip"
	if e = s.Store.TouchDeployJob(ctx, ag.ID, j.ID, l.LeaseToken, "heartbeat", store.IssuanceActor{SourceIP: &badIP}); e == nil {
		t.Fatal("heartbeat audit failure accepted")
	}
	pool.QueryRow(`SELECT leased_until FROM deploy_jobs WHERE id=$1::uuid`, j.ID).Scan(&after)
	if !before.Equal(after) {
		t.Fatal("failed heartbeat extended lease")
	}
	if e = s.Store.TouchDeployJob(ctx, ag.ID, j.ID, l.LeaseToken, "heartbeat"); e != nil {
		t.Fatal(e)
	}
	if e = s.Store.HeartbeatAgent(ctx, ag.ID, store.IssuanceActor{SourceIP: &badIP}); e == nil {
		t.Fatal("agent heartbeat audit failure accepted")
	}
	var seen *time.Time
	pool.QueryRow(`SELECT last_seen_at FROM agents WHERE id=$1::uuid`, ag.ID).Scan(&seen)
	if seen != nil {
		t.Fatal("failed heartbeat updated agent")
	}
	if e = s.Store.HeartbeatAgent(ctx, ag.ID, store.IssuanceActor{}); e != nil {
		t.Fatal(e)
	}
	for _, kind := range []string{"deploy.job.lease", "deploy.job.start", "deploy.job.heartbeat"} {
		var n int
		pool.QueryRow(`SELECT count(*) FROM audit_events WHERE target_id=$1 AND event_type=$2`, j.ID, kind).Scan(&n)
		if n != 1 {
			t.Fatalf("%s audit count %d", kind, n)
		}
	}
	m, e := s.Store.DeploymentMaterial(ctx, ag.ID, j.ID, l.LeaseToken, s.Key)
	if e != nil || m.KeyPEM == "" {
		t.Fatal("material release", e)
	}
	var count int
	if e = pool.QueryRow(`SELECT count(*) FROM audit_events WHERE event_type='issuance.key.release' AND target_id=$1::text`, j.ID).Scan(&count); e != nil || count != 1 {
		t.Fatalf("missing material audit %d %v", count, e)
	}
	if e = s.Store.TouchDeployJob(ctx, ag.ID, j.ID, l.LeaseToken, "start"); e == nil {
		t.Fatal("duplicate start")
	}
	result := agentwire.Result{Status: "completed", ObservedFingerprint: l.ExpectedFingerprint, LeaseToken: l.LeaseToken, Steps: []agentwire.Step{{Name: "verify", OK: true}}}
	if e = s.Store.CompleteDeployJob(ctx, ag.ID, j.ID, result); e != nil {
		t.Fatal(e)
	}
	if e = s.Store.CompleteDeployJob(ctx, ag.ID, j.ID, result); e != nil {
		t.Fatal("completion not idempotent", e)
	}
	if _, e = s.Store.DeploymentMaterial(ctx, ag.ID, j.ID, l.LeaseToken, s.Key); e == nil {
		t.Fatal("completed job key release")
	}
	o, e = s.Store.GetIssuanceOrder(ctx, o.ID)
	if e != nil || o.Status != "completed" {
		t.Fatal("order not completed", e)
	}
	j, e = s.Store.CreateDeployJob(ctx, o.ID, target.ID, a)
	if e != nil {
		t.Fatal(e)
	}
	l, e = leaseForTest(s.Store, ctx, ag.ID)
	if e != nil {
		t.Fatal(e)
	}
	pool.Exec(`UPDATE deploy_jobs SET leased_until=now()-interval '1 second' WHERE id=$1::uuid`, j.ID)
	if _, e = s.Store.DeploymentMaterial(ctx, ag.ID, j.ID, l.LeaseToken, s.Key); e == nil {
		t.Fatal("expired lease key release")
	}
	if e = s.Store.ReapDeployJobs(ctx); e != nil {
		t.Fatal(e)
	}
	o, e = s.Store.GetIssuanceOrder(ctx, o.ID)
	if e != nil || o.Status != "deploy_failed" {
		t.Fatal("expired job not reflected in order", e)
	}
}
func TestAmbiguousSubmissionNeverReplayed(t *testing.T) {
	pool, s, c, fake := integration(t)
	ctx := context.Background()
	s.Store.SaveIssuanceSettings(ctx, store.IssuanceSettings{ApprovalRequired: false}, store.IssuanceActor{})
	defer s.Store.SaveIssuanceSettings(ctx, store.IssuanceSettings{ApprovalRequired: true, AutoRenew: true}, store.IssuanceActor{})
	fake.fail = &upstream.HTTPError{Ambiguous: true}
	o, e := s.Create(ctx, c.ID, specFixture(), store.IssuanceActor{})
	if e != nil {
		t.Fatal(e)
	}
	if e = s.Tick(ctx); e != nil {
		t.Fatal(e)
	}
	o, e = s.Store.GetIssuanceOrder(ctx, o.ID)
	if e != nil || o.Status != "manual_required" {
		t.Fatal("ambiguous request not parked", e)
	}
	if e = s.Tick(ctx); e != nil || fake.places != 1 {
		t.Fatal("ambiguous order replayed")
	}
	if e = s.Store.ChangeIssuanceOrder(ctx, o.ID, "retry", store.IssuanceActor{}); !errors.Is(e, store.ErrIssuanceConflict) {
		t.Fatal("manual confirmation bypass")
	}
	fake.fail = nil
	o, e = s.Create(ctx, c.ID, specFixture(), store.IssuanceActor{})
	if e != nil {
		t.Fatal(e)
	}
	lease, e := s.Store.ClaimIssuanceOrder(ctx)
	if e != nil {
		t.Fatal(e)
	}
	if _, e = s.Store.StartIssuanceSubmission(ctx, lease); e != nil {
		t.Fatal(e)
	}
	pool.Exec(`UPDATE issuance_orders SET leased_until=now()-interval '1 second' WHERE id=$1::uuid`, o.ID)
	if e = s.Tick(ctx); e != nil {
		t.Fatal(e)
	}
	if fake.places != 1 {
		t.Fatal("crashed submission replayed")
	}
	if e = s.Store.FinishIssuanceStep(ctx, lease, store.IssuanceResult{Status: "completed"}); e == nil {
		t.Fatal("stale worker overwrite")
	}
}
