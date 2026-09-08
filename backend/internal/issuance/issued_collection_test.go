package issuance

import (
	"context"
	"errors"
	"testing"

	"github.com/Falke060918/INHERIT_AutoCert/backend/internal/issuance/upstream"
	"github.com/Falke060918/INHERIT_AutoCert/backend/internal/store"
)

type immediateIssuedCA struct {
	*fakeCA
	response  upstream.Snapshot
	getError  error
	gets      int
	afterPost func()
}

func (f *immediateIssuedCA) PlaceOrder(_ context.Context, _ upstream.Spec, csr string, _ upstream.Subscriber) (upstream.Snapshot, error) {
	f.places++
	f.csr = csr
	if f.response.Code == "" {
		f.response = issuedSnapshot(f.t, csr)
	}
	if f.afterPost != nil {
		f.afterPost()
	}
	return f.response, nil
}
func (f *immediateIssuedCA) FetchOrder(context.Context, string) (upstream.Snapshot, error) {
	f.gets++
	return f.response, f.getError
}

func TestCAIssuedSurvivesCollectionAndInventoryFailures(t *testing.T) {
	for _, mode := range []string{"missing_leaf", "malformed_leaf", "csr_missing", "inventory_write"} {
		t.Run(mode, func(t *testing.T) {
			pool, s, c, fake := integration(t)
			ctx := context.Background()
			actor := store.IssuanceActor{}
			if err := s.Store.SaveIssuanceSettings(ctx, store.IssuanceSettings{ApprovalRequired: false}, actor); err != nil {
				t.Fatal(err)
			}
			defer s.Store.SaveIssuanceSettings(ctx, store.IssuanceSettings{ApprovalRequired: true, AutoRenew: true}, actor)
			client := &immediateIssuedCA{fakeCA: fake}
			s.connect = func(string, string, string) (upstream.Connector, error) { return client, nil }
			o, err := s.Create(ctx, c.ID, specFixture(), actor)
			if err != nil {
				t.Fatal(err)
			}
			switch mode {
			case "missing_leaf":
				client.response = upstream.Snapshot{Code: "fake-order", Status: "issued"}
			case "malformed_leaf":
				client.response = upstream.Snapshot{Code: "fake-order", Status: "issued"}
				client.response.Certificate.Base64 = "not-base64"
			case "csr_missing":
				client.afterPost = func() {
					if _, e := pool.Exec(`DELETE FROM issuance_private_keys WHERE order_id=$1`, o.ID); e != nil {
						t.Fatal(e)
					}
				}
			case "inventory_write":
				_, err = pool.Exec(`CREATE FUNCTION fail_issued_inventory_test() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN RAISE EXCEPTION 'test inventory write failure'; END $$; CREATE TRIGGER fail_issued_inventory_test BEFORE INSERT ON certificates FOR EACH ROW EXECUTE FUNCTION fail_issued_inventory_test()`)
				if err != nil {
					t.Fatal(err)
				}
				defer pool.Exec(`DROP TRIGGER IF EXISTS fail_issued_inventory_test ON certificates; DROP FUNCTION IF EXISTS fail_issued_inventory_test()`)
			}
			if err = s.Tick(ctx); err != nil {
				t.Fatal(err)
			}
			o, err = s.Store.GetIssuanceOrder(ctx, o.ID)
			if mode == "csr_missing" {
				if err != nil || o.Status != "completed" || o.CertificateID == nil || o.Comparison == nil || o.Comparison.CSRKeyMatches != nil || !hasWarning(o.Comparison, "csr_key_unknown") {
					t.Fatal("readable leaf with unknown CSR was not registered honestly")
				}
				return
			}
			if err != nil || o.Status != "issued" || o.UpstreamStatus != "issued" || o.Code != "fake-order" || o.CertificateID != nil || o.LastError == "" || client.places != 1 {
				t.Fatalf("CA success lost or false inventory completion: %+v %v", o, err)
			}
			counts := map[string]int{}
			for _, e := range o.Events {
				counts[e.Kind]++
			}
			if counts["ca_issued"] != 1 || counts["collection_warning"] != 1 || counts["failed"] != 0 {
				t.Fatalf("wrong CA/collection event boundary: %v", counts)
			}
			if mode == "inventory_write" {
				if _, err = pool.Exec(`DROP TRIGGER fail_issued_inventory_test ON certificates; DROP FUNCTION fail_issued_inventory_test()`); err != nil {
					t.Fatal(err)
				}
			}
			client.response = issuedSnapshot(t, client.csr)
			if err = s.Store.ChangeIssuanceOrder(ctx, o.ID, "retry", actor); err != nil {
				t.Fatal(err)
			}
			if err = s.Tick(ctx); err != nil {
				t.Fatal(err)
			}
			o, err = s.Store.GetIssuanceOrder(ctx, o.ID)
			if err != nil || o.Status != "completed" || o.CertificateID == nil || client.places != 1 || client.gets != 1 {
				t.Fatal("collection recovery resubmitted or did not register")
			}
		})
	}
}

func TestKnownIssuedNeverRegressesOnFollowupReadErrors(t *testing.T) {
	pool, s, c, fake := integration(t)
	ctx := context.Background()
	actor := store.IssuanceActor{}
	if err := s.Store.SaveIssuanceSettings(ctx, store.IssuanceSettings{ApprovalRequired: false}, actor); err != nil {
		t.Fatal(err)
	}
	defer s.Store.SaveIssuanceSettings(ctx, store.IssuanceSettings{ApprovalRequired: true, AutoRenew: true}, actor)
	client := &immediateIssuedCA{fakeCA: fake, response: upstream.Snapshot{Code: "fake-order", Status: "issued"}}
	s.connect = func(string, string, string) (upstream.Connector, error) { return client, nil }
	o, err := s.Create(ctx, c.ID, specFixture(), actor)
	if err != nil {
		t.Fatal(err)
	}
	if err = s.Tick(ctx); err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		name, status, code string
		err                error
	}{
		{"network", "issued", "fake-order", errors.New("synthetic network error")},
		{"unauthorized", "issued", "fake-order", &upstream.HTTPError{Status: 401}},
		{"missing", "issued", "fake-order", &upstream.HTTPError{Status: 404}},
		{"exhausted", "issued", "fake-order", &upstream.HTTPError{Status: 503}},
		{"wrong-code", "issued", "another-order", nil},
		{"ready", "ready", "fake-order", nil},
		{"pending", "pending", "fake-order", nil},
		{"unknown", "undocumented", "fake-order", nil},
	} {
		t.Run(tc.name, func(t *testing.T) {
			client.getError = tc.err
			client.response.Status = tc.status
			client.response.Code = tc.code
			if _, err = pool.Exec(`UPDATE issuance_orders SET next_action_at=now(),attempt_count=15 WHERE id=$1`, o.ID); err != nil {
				t.Fatal(err)
			}
			if err = s.Tick(ctx); err != nil {
				t.Fatal(err)
			}
			got, err := s.Store.GetIssuanceOrder(ctx, o.ID)
			if err != nil || got.Status != "issued" || got.UpstreamStatus != "issued" || got.Code != "fake-order" || got.CertificateID != nil || client.places != 1 {
				t.Fatal("known issued fact/code regressed")
			}
		})
	}
	// Old issue_failed rows with proven issued must appear under the displayed
	// issued filter while their historical stored state/events remain untouched.
	if _, err = pool.Exec(`UPDATE issuance_orders SET status='issue_failed' WHERE id=$1`, o.ID); err != nil {
		t.Fatal(err)
	}
	items, err := s.Store.ListIssuanceOrders(ctx, "issued", 50, 0)
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, item := range items {
		if item.ID == o.ID {
			found = true
		}
	}
	if !found {
		t.Fatal("issued filter hid historical collection failure")
	}
	// An actual CA terminal state remains distinguishable from issued collection.
	client.getError = nil
	client.response.Status = "revoked"
	client.response.Code = "fake-order"
	if err = s.Store.ChangeIssuanceOrder(ctx, o.ID, "retry", actor); err != nil {
		t.Fatal(err)
	}
	if err = s.Tick(ctx); err != nil {
		t.Fatal(err)
	}
	got, err := s.Store.GetIssuanceOrder(ctx, o.ID)
	if err != nil || got.UpstreamStatus != "revoked" || got.Status != "issue_failed" {
		t.Fatal("actual CA terminal state disguised as success")
	}
}
