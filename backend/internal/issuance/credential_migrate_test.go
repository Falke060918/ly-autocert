package issuance

import (
	"bytes"
	"context"
	"encoding/hex"
	"github.com/Falke060918/INHERIT_AutoCert/backend/internal/sensorca"
	"testing"
)

func TestCredentialMigrationAtomicAndIdempotent(t *testing.T) {
	pool, s, c, _ := integration(t)
	ctx := context.Background()
	app := hex.EncodeToString(randomBytes(t, 8))
	var ids []string
	for i := 0; i < 2; i++ {
		var id string
		e := pool.QueryRow(`INSERT INTO ca_connections(name,base_url,app_id,secret_sealed,secret_nonce,subscriber,active) SELECT name,base_url,$2,secret_sealed,secret_nonce,subscriber,$3 FROM ca_connections WHERE id=$1::uuid RETURNING id`, c.ID, app, i == 0).Scan(&id)
		if e != nil {
			t.Fatal(e)
		}
		ids = append(ids, id)
	}
	t.Cleanup(func() {
		for _, id := range ids {
			pool.Exec(`DELETE FROM ca_connections WHERE id=$1::uuid`, id)
		}
	})
	if _, _, e := s.Connector(ctx, ids[0]); e == nil {
		t.Fatal("legacy plaintext fallback")
	}
	unchanged := func() {
		t.Helper()
		var n int
		pool.QueryRow(`SELECT count(*) FROM ca_connections WHERE id IN ($1::uuid,$2::uuid) AND app_id IS NOT NULL AND app_id_sealed IS NULL`, ids[0], ids[1]).Scan(&n)
		if n != 2 {
			t.Fatal("partial migration committed")
		}
	}
	if _, e := s.Store.MigrateCAAppIDs(ctx, randomBytes(t, 32)); e == nil {
		t.Fatal("wrong key accepted")
	}
	unchanged()
	_, e := pool.Exec(`CREATE FUNCTION security003_fail_migration() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN IF NEW.event_type='ca_connection.credentials.migrate' THEN RAISE EXCEPTION 'test audit failure'; END IF; RETURN NEW; END $$; CREATE TRIGGER security003_fail_migration BEFORE INSERT ON audit_events FOR EACH ROW EXECUTE FUNCTION security003_fail_migration()`)
	if e != nil {
		t.Fatal(e)
	}
	t.Cleanup(func() {
		pool.Exec(`DROP TRIGGER IF EXISTS security003_fail_migration ON audit_events; DROP FUNCTION IF EXISTS security003_fail_migration()`)
	})
	if _, e = s.Store.MigrateCAAppIDs(ctx, s.Key); e == nil {
		t.Fatal("audit failure accepted")
	}
	unchanged()
	if _, e = pool.Exec(`DROP TRIGGER security003_fail_migration ON audit_events; DROP FUNCTION security003_fail_migration()`); e != nil {
		t.Fatal(e)
	}
	if n, e := s.Store.MigrateCAAppIDs(ctx, s.Key); e != nil || n != 2 {
		t.Fatalf("migration count=%d err=%v", n, e)
	}
	if n, e := s.Store.MigrateCAAppIDs(ctx, s.Key); e != nil || n != 0 {
		t.Fatal("migration not idempotent", e)
	}
	for _, id := range ids {
		v, e := s.Store.GetCAConnection(ctx, id)
		if e != nil {
			t.Fatal(e)
		}
		p, e := sensorca.Open(s.Key, v.AppID, v.AppIDNonce)
		if e != nil || string(p) != app || bytes.Equal(v.AppID, []byte(app)) || v.LegacyAppID {
			t.Fatal("invalid migrated credential")
		}
		clear(p)
	}
}
