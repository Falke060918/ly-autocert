package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"time"

	"github.com/Falke060918/INHERIT_AutoCert/backend/internal/issuance/upstream"
)

// IssuanceComparison contains public metadata only, never PEM or private keys.
// It describes the response observed at issuance, not an inferred trust chain.
type IssuanceComparison struct {
	Requested           upstream.Spec           `json:"requested"`
	Actual              IssuedCertificateInfo   `json:"actual"`
	CSRPublicKeySHA256  string                  `json:"csr_public_key_sha256"`
	CSRSignatureValid   bool                    `json:"csr_signature_valid"`
	CSRKeyMatches       *bool                   `json:"csr_key_matches"`
	DNSMatches          bool                    `json:"dns_matches"`
	RequestedKeyMatches bool                    `json:"requested_key_matches"`
	CurrentlyValid      bool                    `json:"currently_valid"`
	ValidityDays        float64                 `json:"validity_days"`
	ValidityMatches     bool                    `json:"validity_matches"`
	Chain               []IssuedCertificateInfo `json:"provided_chain"`
	ChainParseable      bool                    `json:"chain_parseable"`
	ChainSourceSHA256   string                  `json:"chain_source_sha256"`
	Warnings            []IssuanceWarning       `json:"warnings"`
	CheckedAt           time.Time               `json:"checked_at"`
}

type IssuedCertificateInfo struct {
	Subject         string    `json:"subject"`
	Issuer          string    `json:"issuer"`
	CommonName      string    `json:"common_name"`
	DNSNames        []string  `json:"dns_names"`
	SKI             string    `json:"ski"`
	AKI             string    `json:"aki"`
	Fingerprint     string    `json:"fingerprint_sha256"`
	PublicKeySHA256 string    `json:"public_key_sha256"`
	Algorithm       string    `json:"key_algorithm"`
	KeySize         int       `json:"key_size"`
	NotBefore       time.Time `json:"not_before"`
	NotAfter        time.Time `json:"not_after"`
	IsCA            bool      `json:"is_ca"`
	SignsPrevious   *bool     `json:"signs_previous,omitempty"`
}

func (s *Store) certificateIssuanceComparison(ctx context.Context, id string) (*IssuanceComparison, error) {
	var raw []byte
	err := s.db.QueryRowContext(ctx, `SELECT e.detail FROM issuance_orders o JOIN issuance_order_events e ON e.order_id=o.id WHERE o.certificate_id=$1::uuid AND e.kind='certificate_comparison' ORDER BY e.id DESC LIMIT 1`, id).Scan(&raw)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var out IssuanceComparison
	if err = json.Unmarshal(raw, &out); err != nil {
		return nil, err
	}
	return &out, nil
}
