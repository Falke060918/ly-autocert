package store

import (
	"context"
	"strings"
)

// IssuanceWarning is an observed CA response issue, separate from inventory risk.
type IssuanceWarning struct {
	Code    string `json:"code"`
	Message string `json:"message"`
}

// Read warning events, not the latest general event or last_error: later
// deployment events and inventory inference must not erase the CA observation.
func (s *Store) certificateIssuanceWarnings(ctx context.Context, ids []string) (map[string][]IssuanceWarning, error) {
	out := map[string][]IssuanceWarning{}
	if len(ids) == 0 {
		return out, nil
	}
	rows, err := s.db.QueryContext(ctx, `SELECT DISTINCT o.certificate_id::text,e.detail->>'code',e.detail->>'reason'
		FROM issuance_orders o JOIN issuance_order_events e ON e.order_id=o.id
		WHERE o.certificate_id=ANY(string_to_array($1,',')::uuid[]) AND e.kind IN ('chain_warning','validation_warning')
		ORDER BY 1,2,3`, strings.Join(ids, ","))
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var id string
		var w IssuanceWarning
		if err = rows.Scan(&id, &w.Code, &w.Message); err != nil {
			return nil, err
		}
		out[id] = append(out[id], w)
	}
	return out, rows.Err()
}
