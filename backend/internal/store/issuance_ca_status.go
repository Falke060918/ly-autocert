package store

import "context"

// RecordCAIssued persists the CA fact before collection/import can fail. It
// keeps the current lease so only this worker may finish the subsequent step.
func (s *Store) RecordCAIssued(ctx context.Context, o *IssuanceOrder, code string) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	var existing string
	var recorded bool
	err = tx.QueryRowContext(ctx, `SELECT upstream_order_code,EXISTS(SELECT 1 FROM issuance_order_events e WHERE e.order_id=issuance_orders.id AND e.kind='ca_issued') FROM issuance_orders WHERE id=$1::uuid AND lease_token=$2::uuid AND leased_until>now() FOR UPDATE`, o.ID, o.LeaseToken).Scan(&existing, &recorded)
	if err != nil {
		return err
	}
	if code == "" || (existing != "" && existing != code) {
		return ErrIssuanceConflict
	}
	_, err = tx.ExecContext(ctx, `UPDATE issuance_orders SET status='issued',upstream_order_code=$2,upstream_status='issued',last_error='' WHERE id=$1::uuid`, o.ID, code)
	if err != nil {
		return err
	}
	if !recorded {
		if err = orderEvent(ctx, tx, o.ID, "ca_issued", map[string]string{"status": "issued", "upstream_status": "issued"}); err != nil {
			return err
		}
		if err = issuanceAudit(ctx, tx, "issuance.order.ca_issued", o.ID, IssuanceActor{}); err != nil {
			return err
		}
	}
	return tx.Commit()
}
