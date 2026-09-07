package agent

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"github.com/Falke060918/INHERIT_AutoCert/backend/internal/agentwire"
	"os"
	"path/filepath"
	"syscall"
)

func lockState(dir string) (func(), error) {
	if e := ensureStateDir(dir); e != nil {
		return nil, e
	}
	p := filepath.Join(dir, "run.lock")
	if noSymlinks(p) != nil {
		return nil, errKeySource
	}
	f, e := os.OpenFile(p, os.O_CREATE|os.O_RDWR, 0600)
	if e != nil {
		return nil, e
	}
	if e = syscall.Flock(int(f.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); e != nil {
		f.Close()
		return nil, errors.New("agent: another process holds the state lock")
	}
	return func() { syscall.Flock(int(f.Fd()), syscall.LOCK_UN); f.Close() }, nil
}

// consumeLease must run under the process state lock and precedes any start call.
func consumeLease(dir string, l agentwire.Lease) error {
	p := filepath.Join(dir, "accepted-jobs.json")
	entries := map[string]bool{}
	st, e := os.Lstat(p)
	if e == nil {
		if !st.Mode().IsRegular() || !owned(st) || st.Mode().Perm() != 0600 || st.Size() > 4<<20 {
			return errors.New("agent: invalid replay journal")
		}
		b, e := os.ReadFile(p)
		if e != nil {
			return e
		}
		if json.Unmarshal(b, &entries) != nil || entries == nil {
			return errors.New("agent: corrupted replay journal")
		}
		for k, v := range entries {
			d, e := hex.DecodeString(k)
			if e != nil || len(d) != 32 || !v {
				return errors.New("agent: corrupted replay journal")
			}
		}
	} else if !os.IsNotExist(e) {
		return e
	}
	sum := sha256.Sum256([]byte(l.JobID + "\x00" + l.LeaseToken))
	key := hex.EncodeToString(sum[:])
	if entries[key] {
		return errors.New("agent: deployment replay rejected")
	}
	if len(entries) >= 50000 {
		return errors.New("agent: replay journal requires operator maintenance")
	}
	entries[key] = true
	b, e := json.Marshal(entries)
	if e != nil {
		return e
	}
	return writeFileAtomic(p, b, 0600)
}
