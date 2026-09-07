package store

import (
	"bytes"
	"context"
	"errors"
	"github.com/Falke060918/INHERIT_AutoCert/backend/internal/sensorca"
)

// MigrateCAAppIDs is an explicit maintenance operation, never called by startup.
// All rows, including inactive connections, migrate in one transaction.
func (s *Store) MigrateCAAppIDs(ctx context.Context, key []byte) (int, error) {
	tx, e := s.db.BeginTx(ctx, nil)
	if e != nil {
		return 0, e
	}
	defer tx.Rollback()
	rows, e := tx.QueryContext(ctx, `SELECT id,app_id,secret_sealed,secret_nonce FROM ca_connections WHERE app_id IS NOT NULL ORDER BY id FOR UPDATE`)
	if e != nil {
		return 0, e
	}
	type legacy struct {
		id, app       string
		secret, nonce []byte
	}
	var items []legacy
	for rows.Next() {
		var v legacy
		if e = rows.Scan(&v.id, &v.app, &v.secret, &v.nonce); e != nil {
			rows.Close()
			return 0, e
		}
		items = append(items, v)
	}
	e = rows.Err()
	rows.Close()
	if e != nil {
		return 0, e
	}
	for _, v := range items {
		// Prove the supplied key can open existing data before clearing any plaintext.
		secret, e := sensorca.Open(key, v.secret, v.nonce)
		if e != nil {
			return 0, errors.New("CA credential migration: existing secret cannot be opened")
		}
		clear(secret)
		if len(v.app) != 16 {
			return 0, errors.New("CA credential migration: invalid legacy app id")
		}
		plain := []byte(v.app)
		sealed, nonce, e := sensorca.Seal(key, plain)
		if e != nil {
			clear(plain)
			return 0, e
		}
		opened, e := sensorca.Open(key, sealed, nonce)
		same := bytes.Equal(plain, opened)
		clear(plain)
		clear(opened)
		if e != nil || !same {
			return 0, errors.New("CA credential migration: verification failed")
		}
		if _, e = tx.ExecContext(ctx, `UPDATE ca_connections SET app_id=NULL,app_id_sealed=$2,app_id_nonce=$3,updated_at=now() WHERE id=$1::uuid`, v.id, sealed, nonce); e != nil {
			return 0, e
		}
		if e = issuanceAudit(ctx, tx, "ca_connection.credentials.migrate", v.id, IssuanceActor{}); e != nil {
			return 0, e
		}
	}
	if e = tx.Commit(); e != nil {
		return 0, e
	}
	return len(items), nil
}
