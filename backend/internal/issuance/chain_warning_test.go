package issuance

import (
	"bytes"
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/base64"
	"encoding/pem"
	"math/big"
	"strings"
	"testing"
	"time"

	"github.com/Falke060918/INHERIT_AutoCert/backend/internal/issuance/upstream"
	"github.com/Falke060918/INHERIT_AutoCert/backend/internal/store"
)

func TestChainWarningsPreserveOriginalAndLeafChecks(t *testing.T) {
	k, err := GenerateKey(specFixture(), randomBytes(t, 32))
	if err != nil {
		t.Fatal(err)
	}
	good := issuedSnapshot(t, k.CSR)
	wrong := issuedSnapshot(t, k.CSR).Certificate.Chain // Same issuer DN, different signing key.
	private, _ := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	privateDER, _ := x509.MarshalPKCS8PrivateKey(private)
	caBlock, _ := pem.Decode([]byte(good.Certificate.Chain))
	for _, tc := range []struct {
		name, chain, warning string
		blocked              bool
	}{
		{"normal", "\n" + good.Certificate.Chain + "\n", "", false},
		{"wrong-key", wrong, "ca_chain_signature_failed", false},
		{"wrong-order", wrong + good.Certificate.Chain, "ca_chain_signature_failed", false},
		{"malformed", "not a certificate", "ca_chain_parse_failed", false},
		{"partial", good.Certificate.Chain + "\n-----BEGIN CERTIFICATE-----\nbroken", "ca_chain_parse_failed", false},
		{"empty", "", "ca_chain_missing", false},
		{"base64-pem", base64.StdEncoding.EncodeToString([]byte(good.Certificate.Chain)), "", false},
		{"private-pem", good.Certificate.Chain + string(pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: privateDER})), "", true},
		{"private-der", base64.StdEncoding.EncodeToString(privateDER), "", true},
		{"private-after-der-cert", base64.StdEncoding.EncodeToString(append(append([]byte{}, caBlock.Bytes...), privateDER...)), "", true},
		{"private-after-malformed", "-----BEGIN CERTIFICATE-----\nbroken\n-----BEGIN PRIVATE KEY-----\nredacted", "", true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			snap := good
			snap.Certificate.Chain = tc.chain
			cert, leaf, chain, comparison, err := InspectIssued(snap, k.CSR, upstream.Spec{DNSNames: specFixture().DNSNames})
			if tc.blocked {
				if err == nil || cert != nil || chain != "" || comparison != nil {
					t.Fatal("security error downgraded or persisted")
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if tc.warning != "" && !hasWarning(comparison, tc.warning) {
				t.Fatalf("missing %s", tc.warning)
			}
			if tc.warning == "" && len(comparison.Warnings) != 0 {
				t.Fatal("false warning")
			}
			if cert == nil || leaf == "" || chain != tc.chain {
				t.Fatal("source chain changed or leaf lost")
			}
			if c, _, _, result, e := InspectIssued(snap, k.CSR, upstream.Spec{DNSNames: []string{"wrong.example.test"}}); e != nil || c == nil || result.DNSMatches || !hasWarning(result, "dns_mismatch") {
				t.Fatal("SAN difference blocked or omitted")
			}
			other, _ := GenerateKey(specFixture(), randomBytes(t, 32))
			if c, _, _, result, e := InspectIssued(snap, other.CSR, specFixture()); e != nil || c == nil || result.CSRKeyMatches == nil || *result.CSRKeyMatches || !hasWarning(result, "csr_key_mismatch") {
				t.Fatal("CSR difference blocked or omitted")
			}
		})
	}
	block, _ := pem.Decode([]byte(k.CSR))
	csr, _ := x509.ParseCertificateRequest(block.Bytes)
	for _, future := range []bool{false, true} {
		from, to := time.Now().Add(-48*time.Hour), time.Now().Add(-time.Hour)
		if future {
			from, to = time.Now().Add(time.Hour), time.Now().Add(48*time.Hour)
		}
		tmpl := &x509.Certificate{SerialNumber: big.NewInt(1), Subject: pkix.Name{CommonName: "app.example.test"}, DNSNames: specFixture().DNSNames, NotBefore: from, NotAfter: to}
		der, _ := x509.CreateCertificate(rand.Reader, tmpl, tmpl, csr.PublicKey, private)
		snap := good
		snap.Certificate.Base64 = base64.StdEncoding.EncodeToString(der)
		snap.Certificate.Chain = wrong
		if c, _, _, result, e := InspectIssued(snap, k.CSR, specFixture()); e != nil || c == nil || result.CurrentlyValid || result.ValidityMatches || !hasWarning(result, "certificate_time_invalid") || !hasWarning(result, "validity_mismatch") {
			t.Fatal("validity difference blocked or omitted")
		}
	}
}

func hasWarning(result *store.IssuanceComparison, code string) bool {
	for _, w := range result.Warnings {
		if w.Code == code {
			return true
		}
	}
	return false
}

func TestMismatchedIssuedCertificateRegistersWithoutFalseKeyLink(t *testing.T) {
	pool, s, connection, fake := integration(t)
	ctx := context.Background()
	actor := store.IssuanceActor{}
	if err := s.Store.SaveIssuanceSettings(ctx, store.IssuanceSettings{ApprovalRequired: false}, actor); err != nil {
		t.Fatal(err)
	}
	defer s.Store.SaveIssuanceSettings(ctx, store.IssuanceSettings{ApprovalRequired: true, AutoRenew: true}, actor)
	fake.ready = true
	client := &snapshotCA{fakeCA: fake}
	s.connect = func(string, string, string) (upstream.Connector, error) { return client, nil }
	request := specFixture()
	request.Algorithm = "rsa3072"
	request.ValidityDays = 90
	o, err := s.Create(ctx, connection.ID, request, actor)
	if err != nil {
		t.Fatal(err)
	}
	if err = s.Tick(ctx); err != nil {
		t.Fatal(err)
	}
	actualSpec := specFixture()
	actualSpec.DNSNames = []string{"different.example.test"}
	actualSpec.Validations[0].Name = "different.example.test"
	other, err := GenerateKey(actualSpec, randomBytes(t, 32))
	if err != nil {
		t.Fatal(err)
	}
	client.snapshot = issuedSnapshot(t, other.CSR)
	if _, err = pool.Exec(`UPDATE issuance_orders SET next_action_at=now() WHERE id=$1`, o.ID); err != nil {
		t.Fatal(err)
	}
	if err = s.Tick(ctx); err != nil {
		t.Fatal(err)
	}
	o, err = s.Store.GetIssuanceOrder(ctx, o.ID)
	if err != nil || o.Status != "completed" || o.CertificateID == nil || o.Comparison == nil || client.gets != 1 || fake.places != 1 {
		t.Fatalf("mismatched leaf was not registered: %v", err)
	}
	result := o.Comparison
	if !hasWarning(result, "csr_key_mismatch") || !hasWarning(result, "dns_mismatch") || !hasWarning(result, "key_algorithm_mismatch") {
		t.Fatal("missing comparison warnings")
	}
	if result.Requested.Algorithm != "rsa3072" || result.Actual.Algorithm != "ECDSA" || result.Actual.DNSNames[0] != "different.example.test" {
		t.Fatal("request/actual information lost")
	}
	var keyCertificate *string
	if err = pool.QueryRow(`SELECT certificate_id FROM issuance_private_keys WHERE order_id=$1`, o.ID).Scan(&keyCertificate); err != nil || keyCertificate != nil {
		t.Fatal("mismatched private key falsely linked")
	}
	stored, err := s.Store.GetCertificateDetail(ctx, *o.CertificateID)
	if err != nil || stored.IssuanceComparison == nil || len(stored.IssuanceWarnings) < 3 {
		t.Fatal("comparison did not survive inventory read")
	}
}

type snapshotCA struct {
	*fakeCA
	snapshot upstream.Snapshot
	gets     int
}

func (f *snapshotCA) FetchOrder(context.Context, string) (upstream.Snapshot, error) {
	f.gets++
	return f.snapshot, nil
}

func TestChainWarningRegistrationPersistsWithoutResubmission(t *testing.T) {
	pool, s, c, fake := integration(t)
	ctx := context.Background()
	actor := store.IssuanceActor{}
	if err := s.Store.SaveIssuanceSettings(ctx, store.IssuanceSettings{ApprovalRequired: false}, actor); err != nil {
		t.Fatal(err)
	}
	defer s.Store.SaveIssuanceSettings(ctx, store.IssuanceSettings{ApprovalRequired: true, AutoRenew: true}, actor)
	fake.ready = true
	client := &snapshotCA{fakeCA: fake}
	s.connect = func(string, string, string) (upstream.Connector, error) { return client, nil }
	o, err := s.Create(ctx, c.ID, specFixture(), actor)
	if err != nil {
		t.Fatal(err)
	}
	if err = s.Tick(ctx); err != nil {
		t.Fatal(err)
	}
	client.snapshot = issuedSnapshot(t, fake.csr)
	client.snapshot.Certificate.Chain = "\n" + issuedSnapshot(t, fake.csr).Certificate.Chain + "\n"
	// Simulate an existing accepted order that previously failed chain validation.
	if _, err = pool.Exec(`UPDATE issuance_orders SET status='issue_failed',upstream_status='issued' WHERE id=$1`, o.ID); err != nil {
		t.Fatal(err)
	}
	if err = s.Store.ChangeIssuanceOrder(ctx, o.ID, "retry", actor); err != nil {
		t.Fatal(err)
	}
	if err = s.Tick(ctx); err != nil {
		t.Fatal(err)
	}
	o, err = s.Store.GetIssuanceOrder(ctx, o.ID)
	if err != nil || o.Status != "completed" || o.CertificateID == nil || fake.places != 1 || client.gets != 1 || o.Code != "fake-order" || o.LastError != "" {
		t.Fatalf("GET-only warning registration failed: %v", err)
	}
	var chain, leaf string
	if err = pool.QueryRow(`SELECT chain_pem,cert_pem FROM issuance_orders WHERE id=$1`, o.ID).Scan(&chain, &leaf); err != nil {
		t.Fatal(err)
	}
	if chain != client.snapshot.Certificate.Chain {
		t.Fatal("CA source chain rewritten")
	}
	data, _ := certificateData(client.snapshot.Certificate.Base64)
	stored, _ := certificateData(leaf)
	x, _ := x509.ParseCertificate(data)
	if x == nil {
		p, _ := pem.Decode(data)
		x, _ = x509.ParseCertificate(p.Bytes)
	}
	p, _ := pem.Decode(stored)
	if p == nil || !bytes.Equal(p.Bytes, x.Raw) {
		t.Fatal("leaf changed")
	}
	var warningEvents int
	if err = pool.QueryRow(`SELECT count(*) FROM issuance_order_events WHERE order_id=$1 AND kind='chain_warning' AND detail->>'code'='ca_chain_signature_failed'`, o.ID).Scan(&warningEvents); err != nil || warningEvents != 1 {
		t.Fatal("warning event missing")
	}
	if _, err = pool.Exec(`INSERT INTO issuance_order_events(order_id,kind,detail) VALUES($1,'deploy_finished','{"status":"completed"}')`, o.ID); err != nil {
		t.Fatal(err)
	}
	detail, err := s.Store.GetCertificateDetail(ctx, *o.CertificateID)
	if err != nil || len(detail.IssuanceWarnings) != 1 || detail.IssuanceWarnings[0].Code != "ca_chain_signature_failed" {
		t.Fatalf("detail lost warning: %v", err)
	}
	list, err := s.Store.ListCertificates(ctx, store.ListCertificatesInput{Search: specFixture().DNSNames[0], PageSize: 200})
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, item := range list.Items {
		if item.ID == *o.CertificateID {
			found = true
			if len(item.IssuanceWarnings) != 1 {
				t.Fatal("list lost warning")
			}
		}
	}
	if !found {
		t.Fatal("registered certificate absent from inventory")
	}
	// A forced warning-event failure must roll back certificate, key linkage,
	// order state and all events together (isolated test database only).
	o2, err := s.Create(ctx, c.ID, specFixture(), actor)
	if err != nil {
		t.Fatal(err)
	}
	claimed, err := s.Store.ClaimIssuanceOrder(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if claimed.ID != o2.ID {
		t.Fatal("unexpected test order claimed")
	}
	_, err = pool.Exec(`CREATE FUNCTION reject_chain_warning_test() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN IF NEW.kind='chain_warning' THEN RAISE EXCEPTION 'test rejection'; END IF; RETURN NEW; END $$; CREATE TRIGGER reject_chain_warning_test BEFORE INSERT ON issuance_order_events FOR EACH ROW EXECUTE FUNCTION reject_chain_warning_test()`)
	if err != nil {
		t.Fatal(err)
	}
	defer pool.Exec(`DROP TRIGGER reject_chain_warning_test ON issuance_order_events; DROP FUNCTION reject_chain_warning_test()`)
	in := store.IssuanceResult{Status: "completed", Code: "fake-order", Kind: "issued", Certificate: &detail.Certificate, CertPEM: leaf, ChainPEM: chain, Comparison: detail.IssuanceComparison}
	if err = s.Store.FinishIssuanceStep(ctx, claimed, in); err == nil {
		t.Fatal("expected transaction failure")
	}
	var id *string
	var count int
	if err = pool.QueryRow(`SELECT certificate_id FROM issuance_orders WHERE id=$1`, o2.ID).Scan(&id); err != nil || id != nil {
		t.Fatal("partial registration survived rollback")
	}
	if err = pool.QueryRow(`SELECT count(*) FROM issuance_order_events WHERE order_id=$1 AND kind='inventory_registered'`, o2.ID).Scan(&count); err != nil || count != 0 {
		t.Fatal("partial event survived rollback")
	}
	if strings.Contains(detail.IssuanceWarnings[0].Message, "BEGIN") {
		t.Fatal("certificate leaked in warning")
	}
}
