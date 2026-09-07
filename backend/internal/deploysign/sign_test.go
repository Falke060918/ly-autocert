package deploysign

import (
	"crypto/ed25519"
	"crypto/rand"
	"encoding/json"
	"github.com/Falke060918/INHERIT_AutoCert/backend/internal/agentwire"
	"strings"
	"testing"
	"time"
)

func validLease() agentwire.Lease {
	return agentwire.Lease{JobID: "e1e5c1d2-924e-4562-9c6a-e391e8621cfa", Kind: "deploy", Target: agentwire.Target{AgentID: "12345678-1234-1234-1234-123456789abc", Kind: "nginx", ServiceUnit: "nginx", CertPath: "/etc/nginx/a.crt", KeyPath: "/etc/nginx/a.key", VerifyHost: "127.0.0.1", VerifyPort: 443, Active: true}, ExpectedFingerprint: strings.Repeat("a", 64), LeaseToken: "12345678-1234-1234-1234-123456789def", ExpiresAt: time.Now().Add(90 * time.Second)}
}
func TestRejectSignedJobTampering(t *testing.T) {
	pub, key, _ := ed25519.GenerateKey(rand.Reader)
	signer := New(key)
	l := validLease()
	now := time.Now()
	env, e := signer.Sign(l, now)
	if e != nil {
		t.Fatal(e)
	}
	if _, e = Verify(pub, env, l.Target.AgentID, now); e != nil {
		t.Fatal(e)
	}
	var fields map[string]json.RawMessage
	json.Unmarshal(env.Payload, &fields)
	for field := range fields {
		t.Run(field, func(t *testing.T) {
			var m map[string]json.RawMessage
			json.Unmarshal(env.Payload, &m)
			m[field] = json.RawMessage(`null`)
			b, _ := json.Marshal(m)
			bad := env
			bad.Payload = b
			if _, e := Verify(pub, bad, l.Target.AgentID, now); e == nil {
				t.Fatal("tamper accepted")
			}
		})
	}
	other, _, _ := ed25519.GenerateKey(rand.Reader)
	for _, tc := range []struct {
		pub ed25519.PublicKey
		id  string
		at  time.Time
	}{{other, l.Target.AgentID, now}, {pub, "other", now}, {pub, l.Target.AgentID, now.Add(91 * time.Second)}, {pub, l.Target.AgentID, now.Add(-31 * time.Second)}} {
		if _, e := Verify(tc.pub, env, tc.id, tc.at); e == nil {
			t.Fatal("invalid authority/time accepted")
		}
	}
	for _, b := range [][]byte{append(append([]byte{}, env.Payload[:len(env.Payload)-1]...), []byte(`,"version":1}`)...), append([]byte(" "), env.Payload...)} {
		bad := agentwire.SignedLease{Payload: b, Signature: ed25519.Sign(key, append([]byte(domain), b...))}
		if _, e := Verify(pub, bad, l.Target.AgentID, now); e == nil {
			t.Fatal("noncanonical JSON accepted")
		}
	}
}
