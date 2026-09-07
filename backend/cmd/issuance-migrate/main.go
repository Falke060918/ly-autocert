// Explicit credential data migration. Apply the schema migration first.
package main

import (
	"context"
	"encoding/hex"
	"fmt"
	"github.com/Falke060918/INHERIT_AutoCert/backend/internal/db"
	"github.com/Falke060918/INHERIT_AutoCert/backend/internal/store"
	"os"
)

func main() {
	if e := run(); e != nil {
		fmt.Fprintln(os.Stderr, "CA credential migration failed; no partial migration committed")
		os.Exit(1)
	}
}
func run() error {
	key, e := hex.DecodeString(os.Getenv("ISSUANCE_ENC_KEY"))
	os.Unsetenv("ISSUANCE_ENC_KEY")
	if e != nil || len(key) != 32 {
		return fmt.Errorf("invalid key")
	}
	defer clear(key)
	pool, e := db.Connect(context.Background(), os.Getenv("DATABASE_URL"))
	if e != nil {
		return e
	}
	defer pool.Close()
	n, e := store.New(pool).MigrateCAAppIDs(context.Background(), key)
	if e == nil {
		fmt.Printf("CA connections migrated: %d\n", n)
	}
	return e
}
