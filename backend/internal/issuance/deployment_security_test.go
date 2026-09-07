package issuance

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"errors"
	"github.com/Falke060918/INHERIT_AutoCert/backend/internal/agentwire"
	"github.com/Falke060918/INHERIT_AutoCert/backend/internal/deploysign"
	"github.com/Falke060918/INHERIT_AutoCert/backend/internal/store"
	"sync"
	"testing"
	"time"
)

func leaseForTest(st *store.Store, ctx context.Context, id string) (*agentwire.Lease, error) {
	pub, key, _ := ed25519.GenerateKey(rand.Reader)
	s := deploysign.New(key)
	env, e := st.LeaseDeployJob(ctx, id, func(l agentwire.Lease) (agentwire.SignedLease, error) { return s.Sign(l, time.Now()) })
	if e != nil {
		return nil, e
	}
	l, e := deploysign.Verify(pub, *env, id, time.Now())
	return &l, e
}

func assertLeaseRollbacks(t *testing.T, s *Service, agentID, jobID string) {
	t.Helper()
	ctx := context.Background()
	_, key, _ := ed25519.GenerateKey(rand.Reader)
	signer := deploysign.New(key)
	badIP := "invalid-test-ip"
	for _, tc := range []struct {
		sign func(agentwire.Lease) (agentwire.SignedLease, error)
		meta store.IssuanceActor
	}{
		{nil, store.IssuanceActor{}},
		{func(agentwire.Lease) (agentwire.SignedLease, error) {
			return agentwire.SignedLease{}, errors.New("test signer failure")
		}, store.IssuanceActor{}},
		{func(l agentwire.Lease) (agentwire.SignedLease, error) { return signer.Sign(l, time.Now()) }, store.IssuanceActor{SourceIP: &badIP}},
	} {
		if _, e := s.Store.LeaseDeployJob(ctx, agentID, tc.sign, tc.meta); e == nil {
			t.Fatal("lease failure accepted")
		}
	}
}
func assertConcurrentStart(t *testing.T, s *Service, agentID, jobID, token string) {
	t.Helper()
	ctx := context.Background()
	badIP := "invalid-test-ip"
	if e := s.Store.TouchDeployJob(ctx, agentID, jobID, token, "start", store.IssuanceActor{SourceIP: &badIP}); e == nil {
		t.Fatal("start audit failure accepted")
	}
	var wg sync.WaitGroup
	results := make(chan error, 2)
	for i := 0; i < 2; i++ {
		wg.Add(1)
		go func() { defer wg.Done(); results <- s.Store.TouchDeployJob(ctx, agentID, jobID, token, "start") }()
	}
	wg.Wait()
	close(results)
	success := 0
	for e := range results {
		if e == nil {
			success++
		}
	}
	if success != 1 {
		t.Fatalf("concurrent starts succeeded %d times", success)
	}
}
