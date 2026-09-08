package store

import (
	"context"
	"fmt"
	"github.com/Falke060918/INHERIT_AutoCert/backend/internal/domain"
	"testing"
	"time"
)

func TestCertificateScopeCurrentBindings(t *testing.T) {
	pool := testPool(t)
	st := New(pool)
	ctx := context.Background()
	clearInventory(t, pool)
	exec := func(q string, args ...any) {
		t.Helper()
		if _, err := pool.Exec(q, args...); err != nil {
			t.Fatal(err)
		}
	}
	id := func(q string, args ...any) string {
		t.Helper()
		var v string
		if err := pool.QueryRow(q, args...).Scan(&v); err != nil {
			t.Fatal(err)
		}
		return v
	}
	target := id(`INSERT INTO targets(name,target_type) VALUES('scope-fixture','server') RETURNING id`)
	agent := id(`INSERT INTO agents(name,status,token_expires_at) VALUES('scope-fixture','active',now()) RETURNING id`)
	ca := id(`INSERT INTO ca_connections(name,base_url,secret_sealed,secret_nonce,subscriber) VALUES('scope-fixture','https://example.test',''::bytea,''::bytea,'{}') RETURNING id`)
	order := id(`INSERT INTO issuance_orders(ca_connection_id,status,spec) VALUES($1::uuid,'completed','{}') RETURNING id`, ca)
	t.Cleanup(func() {
		exec(`DELETE FROM deploy_jobs WHERE order_id=$1::uuid`, order)
		exec(`DELETE FROM deployment_targets WHERE agent_id=$1::uuid`, agent)
		exec(`DELETE FROM issuance_orders WHERE id=$1::uuid`, order)
		exec(`DELETE FROM ca_connections WHERE id=$1::uuid`, ca)
		exec(`DELETE FROM agents WHERE id=$1::uuid`, agent)
		clearInventory(t, pool)
	})
	cert := func(label string, status domain.CertificateStatus, days int, ctype domain.CertificateType) string {
		return insertListFixture(t, pool, listFixture{cn: label, serial: label, notAfter: time.Now().Add(time.Duration(days) * 24 * time.Hour), ctype: ctype, status: status})
	}
	applied := cert("observed", domain.CertStatusActive, 3, domain.CertTypeServer)
	replaced := cert("still-current", domain.CertStatusReplaced, 5, domain.CertTypeServer)
	expired := cert("expired-current", domain.CertStatusExpired, -2, domain.CertTypeServer)
	deployed := cert("verified-deploy", domain.CertStatusActive, 20, domain.CertTypeServer)
	pending := cert("intended-only", domain.CertStatusActive, 2, domain.CertTypeServer)
	old := cert("old-location", domain.CertStatusActive, 4, domain.CertTypeServer)
	retired := cert("retired", domain.CertStatusActive, 4, domain.CertTypeServer)
	unobserved := cert("no-success", domain.CertStatusActive, 4, domain.CertTypeServer)
	chain := cert("chain-ca", domain.CertStatusActive, 4, domain.CertTypeCA)
	badDeploy := cert("mismatched-verification", domain.CertStatusActive, 4, domain.CertTypeServer)
	inactive := cert("inactive-deploy", domain.CertStatusActive, 4, domain.CertTypeServer)
	for i, c := range []string{applied, applied, replaced, expired, retired, unobserved, chain} {
		exec(`INSERT INTO endpoint_monitors(target_id,port,current_fingerprint,last_success_at,enabled,retired_at) SELECT $1::uuid,$2::int,fingerprint_sha256,CASE WHEN $3::bool THEN NULL ELSE now() END,false,CASE WHEN $4::bool THEN now() ELSE NULL END FROM certificates WHERE id=$5::uuid`, target, 443+i, c == unobserved, c == retired, c)
	}
	exec(`UPDATE endpoint_monitors SET previous_fingerprint=(SELECT fingerprint_sha256 FROM certificates WHERE id=$1::uuid) WHERE target_id=$2::uuid`, old, target)
	exec(`INSERT INTO certificate_locations(certificate_id,target_id,location_type,port,deployment_status) VALUES($1::uuid,$2::uuid,'network_endpoint',8443,'deployed')`, old, target)
	for i, c := range []string{deployed, applied, badDeploy, inactive} {
		dt := id(`INSERT INTO deployment_targets(agent_id,kind,service_unit,cert_path,key_path,current_certificate_id,active) VALUES($1::uuid,'nginx','nginx',$2,$3,$4::uuid,$5) RETURNING id`, agent, fmt.Sprintf("/fixture/%d.crt", i), fmt.Sprintf("/fixture/%d.key", i), c, c != inactive)
		exec(`INSERT INTO deploy_jobs(deployment_target_id,agent_id,order_id,certificate_id,target_snapshot,status,observed_fingerprint) SELECT $1::uuid,$2::uuid,$3::uuid,id,'{}','completed',CASE WHEN $4::bool THEN '' ELSE fingerprint_sha256 END FROM certificates WHERE id=$5::uuid`, dt, agent, order, c == badDeploy, c)
		if c == deployed {
			exec(`INSERT INTO deploy_jobs(deployment_target_id,agent_id,order_id,certificate_id,target_snapshot,status) VALUES($1::uuid,$2::uuid,$3::uuid,$4::uuid,'{}','pending')`, dt, agent, order, pending)
		}
	}
	expected := map[string]bool{applied: true, replaced: true, expired: true, deployed: true}
	list, err := st.ListCertificates(ctx, ListCertificatesInput{Scope: CertificateScopeServer})
	if err != nil {
		t.Fatal(err)
	}
	if list.Total != len(expected) {
		t.Fatalf("server total=%d want %d", list.Total, len(expected))
	}
	for _, c := range list.Items {
		if !expected[c.ID] {
			t.Errorf("unexpected current certificate %s", c.CommonName)
		}
	}
	for _, scope := range []CertificateScope{CertificateScopeServer, CertificateScopeInventory} {
		in := ListCertificatesInput{Scope: scope}
		if scope == CertificateScopeInventory {
			in.ExcludeStatuses = []domain.CertificateStatus{domain.CertStatusReplaced}
		}
		all, e := st.ListCertificates(ctx, in)
		if e != nil {
			t.Fatal(e)
		}
		sum, e := st.CertificateSummary(ctx, scope)
		if e != nil {
			t.Fatal(e)
		}
		if sum.Total != all.Total {
			t.Fatalf("%s total mismatch", scope)
		}
		if scope == CertificateScopeServer && (sum.Expiring7d != 2 || sum.Expiring30d != 3 || sum.Expired != 1) {
			t.Fatalf("server buckets %+v", sum)
		}
		weeks := 0
		for _, n := range sum.ExpiryWeeks {
			weeks += n
		}
		if weeks != sum.Expiring90d {
			t.Fatal("weekly sum mismatch")
		}
		risks, e := st.CertificateRisks(ctx, scope)
		if e != nil || risks.Total != sum.Total {
			t.Fatalf("risk mismatch: %v", e)
		}
		org, e := st.CertificateIssuers(ctx, scope)
		if e != nil {
			t.Fatal(e)
		}
		n := 0
		for _, o := range org.Organizations {
			n += o.Count
		}
		in.Types = []domain.CertificateType{domain.CertTypeServer}
		leaf, e := st.ListCertificates(ctx, in)
		if e != nil || leaf.Total != n {
			t.Fatalf("issuer mismatch: %v", e)
		}
		for _, days := range []int{7, 30, 60, 90} {
			in.Types = nil
			in.ExpiresWithinDays = &days
			rows, e := st.ListCertificates(ctx, in)
			if e != nil {
				t.Fatal(e)
			}
			want := map[int]int{7: sum.Expiring7d, 30: sum.Expiring30d, 60: sum.Expiring60d, 90: sum.Expiring90d}[days]
			if rows.Total != want {
				t.Fatalf("%s %dd total %d != %d", scope, days, rows.Total, want)
			}
		}
	}
}

func TestCertificateScopeWeeksBeyondFirst600(t *testing.T) {
	pool := testPool(t)
	clearInventory(t, pool)
	t.Cleanup(func() { clearInventory(t, pool) })
	_, err := pool.Exec(`INSERT INTO certificates(fingerprint_sha256,serial_number,subject_dn,issuer_dn,common_name,not_before,not_after,public_key_algorithm,public_key_size,signature_algorithm,certificate_type,status) SELECT md5(i::text)||md5(i::text),i::text,'CN=synthetic','CN=synthetic','synthetic-'||i::text,now()-interval '1 day',now()+i*interval '3 hours','ECDSA',256,'ECDSA-SHA256','server','active' FROM generate_series(1,650) i`)
	if err != nil {
		t.Fatal(err)
	}
	sum, err := New(pool).CertificateSummary(context.Background(), CertificateScopeInventory)
	if err != nil {
		t.Fatal(err)
	}
	n := 0
	for _, c := range sum.ExpiryWeeks {
		n += c
	}
	if sum.Total != 650 || n != 650 || sum.Expiring90d != 650 {
		t.Fatalf("truncated distribution: %+v", sum)
	}
}
