package main

import (
	"encoding/hex"
	"flag"
	"fmt"
	"github.com/Falke060918/INHERIT_AutoCert/backend/internal/deploysign"
	"os"
)

func main() {
	if run() != nil {
		fmt.Fprintln(os.Stderr, "signing key operation failed")
		os.Exit(1)
	}
}
func run() error {
	if len(os.Args) < 2 {
		return fmt.Errorf("use init|public --file")
	}
	fs := flag.NewFlagSet(os.Args[1], flag.ContinueOnError)
	file := fs.String("file", "", "sealed key path")
	if e := fs.Parse(os.Args[2:]); e != nil {
		return e
	}
	key, e := hex.DecodeString(os.Getenv("ISSUANCE_ENC_KEY"))
	os.Unsetenv("ISSUANCE_ENC_KEY")
	if e != nil || len(key) != 32 {
		return fmt.Errorf("wrapping key required")
	}
	defer clear(key)
	switch os.Args[1] {
	case "init":
		return deploysign.Init(*file, key)
	case "public":
		s, e := deploysign.Load(*file, key)
		if e != nil {
			return e
		}
		fmt.Println(hex.EncodeToString(s.Public()))
		return nil
	default:
		return fmt.Errorf("invalid command")
	}
}
