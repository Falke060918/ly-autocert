// Package deploysign authenticates individual deployment jobs independently of mTLS.
package deploysign

import (
	"bytes"
	"crypto/ed25519"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"github.com/Falke060918/INHERIT_AutoCert/backend/internal/agentwire"
	"regexp"
	"time"
)

const domain = "autocert/deploy-job/v1\x00"
const maxPayload = 16384

var errInvalid = errors.New("deployment signature or payload is invalid")
var uuid = regexp.MustCompile(`^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$`)

type payload struct {
	Version             int              `json:"version"`
	KeyID               string           `json:"key_id"`
	JobID               string           `json:"job_id"`
	AgentID             string           `json:"agent_id"`
	Kind                string           `json:"kind"`
	Target              agentwire.Target `json:"target"`
	ExpectedFingerprint string           `json:"expected_fingerprint"`
	LeaseToken          string           `json:"lease_token"`
	IssuedAt            int64            `json:"issued_at"`
	ExpiresAt           int64            `json:"expires_at"`
}
type Signer struct{ key ed25519.PrivateKey }

func New(key ed25519.PrivateKey) *Signer {
	return &Signer{key: append(ed25519.PrivateKey(nil), key...)}
}
func (s *Signer) Public() ed25519.PublicKey {
	if s == nil || len(s.key) != ed25519.PrivateKeySize {
		return nil
	}
	return append(ed25519.PublicKey(nil), s.key.Public().(ed25519.PublicKey)...)
}
func Fingerprint(key ed25519.PublicKey) string {
	h := sha256.Sum256(key)
	return hex.EncodeToString(h[:])
}
func (s *Signer) Sign(l agentwire.Lease, now time.Time) (agentwire.SignedLease, error) {
	if s == nil || len(s.key) != ed25519.PrivateKeySize {
		return agentwire.SignedLease{}, errInvalid
	}
	p := payload{1, Fingerprint(s.Public()), l.JobID, l.Target.AgentID, l.Kind, l.Target, l.ExpectedFingerprint, l.LeaseToken, now.Unix(), l.ExpiresAt.Unix()}
	b, e := json.Marshal(p)
	if e != nil {
		return agentwire.SignedLease{}, errInvalid
	}
	env := agentwire.SignedLease{Payload: b, Signature: ed25519.Sign(s.key, append([]byte(domain), b...))}
	if _, e = Verify(s.Public(), env, p.AgentID, now); e != nil {
		return agentwire.SignedLease{}, e
	}
	return env, nil
}
func Verify(pub ed25519.PublicKey, env agentwire.SignedLease, agentID string, now time.Time) (agentwire.Lease, error) {
	fail := func() (agentwire.Lease, error) { return agentwire.Lease{}, errInvalid }
	if len(pub) != ed25519.PublicKeySize || len(env.Payload) > maxPayload || len(env.Signature) != ed25519.SignatureSize || !ed25519.Verify(pub, append([]byte(domain), env.Payload...), env.Signature) {
		return fail()
	}
	var p payload
	d := json.NewDecoder(bytes.NewReader(env.Payload))
	d.DisallowUnknownFields()
	if d.Decode(&p) != nil {
		return fail()
	}
	canonical, e := json.Marshal(p)
	if e != nil || !bytes.Equal(canonical, env.Payload) {
		return fail()
	}
	if p.Version != 1 || p.KeyID != Fingerprint(pub) || p.AgentID != agentID || p.Target.AgentID != agentID || !uuid.MatchString(p.AgentID) || !uuid.MatchString(p.JobID) || !uuid.MatchString(p.LeaseToken) || p.Kind != "deploy" || !p.Target.Active || p.Target.Validate() != nil || !agentwire.Fingerprint(p.ExpectedFingerprint) || p.Target.CurrentFingerprint != "" && !agentwire.Fingerprint(p.Target.CurrentFingerprint) {
		return fail()
	}
	if p.IssuedAt > now.Unix()+30 || p.ExpiresAt <= now.Unix() || p.ExpiresAt <= p.IssuedAt || p.ExpiresAt-p.IssuedAt > 90 {
		return fail()
	}
	return agentwire.Lease{JobID: p.JobID, Kind: p.Kind, Target: p.Target, ExpectedFingerprint: p.ExpectedFingerprint, LeaseToken: p.LeaseToken, ExpiresAt: time.Unix(p.ExpiresAt, 0).UTC()}, nil
}
