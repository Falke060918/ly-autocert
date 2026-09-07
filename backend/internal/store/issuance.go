package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"strings"
	"time"

	"github.com/Falke060918/INHERIT_AutoCert/backend/internal/audit"
	"github.com/Falke060918/INHERIT_AutoCert/backend/internal/domain"
	"github.com/Falke060918/INHERIT_AutoCert/backend/internal/issuance/upstream"
)

var ErrIssuanceConflict = errors.New("현재 상태에서는 작업할 수 없습니다")

type IssuanceActor struct{ ID, RequestID, SourceIP *string }

func issuanceAudit(ctx context.Context, tx *sql.Tx, kind, id string, a IssuanceActor) error {
	return audit.Insert(ctx, tx, audit.Event{Type: kind, ActorID: a.ID, RequestID: a.RequestID, SourceIP: a.SourceIP, TargetType: "issuance", TargetID: &id, Result: domain.AuditSuccess})
}

type CAConnection struct {
	ID                string              `json:"id"`
	Name              string              `json:"name"`
	BaseURL           string              `json:"base_url"`
	AppIDConfigured   bool                `json:"app_id_configured"`
	AppID, AppIDNonce []byte              `json:"-"`
	LegacyAppID       bool                `json:"-"`
	Subscriber        upstream.Subscriber `json:"subscriber"`
	Active            bool                `json:"active"`
	SecretConfigured  bool                `json:"secret_configured"`
	Secret, Nonce     []byte              `json:"-"`
}

func scanCA(row rowScanner) (*CAConnection, error) {
	var c CAConnection
	var sub []byte
	e := row.Scan(&c.ID, &c.Name, &c.BaseURL, &c.AppID, &c.AppIDNonce, &c.LegacyAppID, &sub, &c.Active, &c.Secret, &c.Nonce)
	if e != nil {
		return nil, e
	}
	e = json.Unmarshal(sub, &c.Subscriber)
	c.SecretConfigured = len(c.Secret) > 0
	c.AppIDConfigured = len(c.AppID) > 0 || c.LegacyAppID
	return &c, e
}

const caCols = `id,name,base_url,app_id_sealed,app_id_nonce,(app_id IS NOT NULL),subscriber,active,secret_sealed,secret_nonce`

func (s *Store) GetCAConnection(ctx context.Context, id string) (*CAConnection, error) {
	return scanCA(s.db.QueryRowContext(ctx, `SELECT `+caCols+` FROM ca_connections WHERE id=$1::uuid`, id))
}
func (s *Store) ListCAConnections(ctx context.Context) ([]CAConnection, error) {
	rows, e := s.db.QueryContext(ctx, `SELECT `+caCols+` FROM ca_connections ORDER BY created_at,id`)
	if e != nil {
		return nil, e
	}
	defer rows.Close()
	out := []CAConnection{}
	for rows.Next() {
		v, e := scanCA(rows)
		if e != nil {
			return nil, e
		}
		out = append(out, *v)
	}
	return out, rows.Err()
}
func (s *Store) SaveCAConnection(ctx context.Context, c CAConnection, a IssuanceActor) (*CAConnection, error) {
	tx, e := s.db.BeginTx(ctx, nil)
	if e != nil {
		return nil, e
	}
	defer tx.Rollback()
	sub, _ := json.Marshal(c.Subscriber)
	kind := "ca_connection.create"
	if c.ID == "" {
		if len(c.AppID) == 0 {
			return nil, ErrIssuanceConflict
		}
		e = tx.QueryRowContext(ctx, `INSERT INTO ca_connections(name,base_url,app_id_sealed,subscriber,active,secret_sealed,secret_nonce,app_id_nonce) VALUES($1,$2,$3,$4::jsonb,$5,$6,$7,$8) RETURNING id`, c.Name, c.BaseURL, c.AppID, string(sub), c.Active, c.Secret, c.Nonce, c.AppIDNonce).Scan(&c.ID)
	} else {
		kind = "ca_connection.update"
		var id string
		e = tx.QueryRowContext(ctx, `UPDATE ca_connections SET name=$2,base_url=$3,app_id=NULL,app_id_sealed=coalesce($4::bytea,app_id_sealed),app_id_nonce=coalesce($9::bytea,app_id_nonce),subscriber=$5::jsonb,active=$6,secret_sealed=coalesce($7::bytea,secret_sealed),secret_nonce=coalesce($8::bytea,secret_nonce),updated_at=now() WHERE id=$1::uuid AND app_id IS NULL RETURNING id`, c.ID, c.Name, c.BaseURL, c.AppID, string(sub), c.Active, c.Secret, c.Nonce, c.AppIDNonce).Scan(&id)
	}
	if e != nil {
		return nil, e
	}
	if e = issuanceAudit(ctx, tx, kind, c.ID, a); e != nil {
		return nil, e
	}
	if e = tx.Commit(); e != nil {
		return nil, e
	}
	return s.GetCAConnection(ctx, c.ID)
}
func (s *Store) DeleteCAConnection(ctx context.Context, id string, a IssuanceActor) error {
	tx, e := s.db.BeginTx(ctx, nil)
	if e != nil {
		return e
	}
	defer tx.Rollback()
	var found string
	e = tx.QueryRowContext(ctx, `UPDATE ca_connections SET active=false,updated_at=now() WHERE id=$1::uuid RETURNING id`, id).Scan(&found)
	if e != nil {
		return e
	}
	if e = issuanceAudit(ctx, tx, "ca_connection.delete", id, a); e != nil {
		return e
	}
	return tx.Commit()
}
func (s *Store) RecordCATest(ctx context.Context, id string, a IssuanceActor, success bool) error {
	tx, e := s.db.BeginTx(ctx, nil)
	if e != nil {
		return e
	}
	defer tx.Rollback()
	result := domain.AuditSuccess
	if !success {
		result = domain.AuditFailure
	}
	e = audit.Insert(ctx, tx, audit.Event{Type: "ca_connection.test", TargetType: "ca_connection", TargetID: &id, ActorID: a.ID, RequestID: a.RequestID, SourceIP: a.SourceIP, Result: result})
	if e != nil {
		return e
	}
	return tx.Commit()
}

type IssuanceSettings struct {
	ApprovalRequired bool `json:"approval_required_new"`
	AutoRenew        bool `json:"auto_renew"`
}

func (s *Store) GetIssuanceSettings(ctx context.Context) (IssuanceSettings, error) {
	var out IssuanceSettings
	e := s.db.QueryRowContext(ctx, `SELECT approval_required_new,auto_renew FROM issuance_settings WHERE id`).Scan(&out.ApprovalRequired, &out.AutoRenew)
	return out, e
}
func (s *Store) SaveIssuanceSettings(ctx context.Context, in IssuanceSettings, a IssuanceActor) error {
	tx, e := s.db.BeginTx(ctx, nil)
	if e != nil {
		return e
	}
	defer tx.Rollback()
	_, e = tx.ExecContext(ctx, `UPDATE issuance_settings SET approval_required_new=$1,auto_renew=$2 WHERE id`, in.ApprovalRequired, in.AutoRenew)
	if e != nil {
		return e
	}
	e = audit.Insert(ctx, tx, audit.Event{Type: "issuance.settings.update", TargetType: "issuance_settings", ActorID: a.ID, RequestID: a.RequestID, SourceIP: a.SourceIP, After: in, Result: domain.AuditSuccess})
	if e != nil {
		return e
	}
	return tx.Commit()
}

type IssuanceOrder struct {
	ID                string                      `json:"id"`
	ConnectionID      string                      `json:"ca_connection_id"`
	Status            string                      `json:"status"`
	Origin            string                      `json:"origin"`
	Spec              upstream.Spec               `json:"spec"`
	Code              string                      `json:"upstream_order_code"`
	UpstreamStatus    string                      `json:"upstream_status"`
	Validation        []upstream.DomainValidation `json:"domain_validation"`
	CertificateID     *string                     `json:"certificate_id"`
	CreatedAt         time.Time                   `json:"created_at"`
	FinishedAt        *time.Time                  `json:"finished_at"`
	LastError         string                      `json:"last_error"`
	Attempts          int                         `json:"attempt_count"`
	LeaseToken        *string                     `json:"-"`
	SubmissionStarted *time.Time                  `json:"-"`
	LeasedUntil       *time.Time                  `json:"-"`
	Events            []IssuanceEvent             `json:"events,omitempty"`
}
type IssuanceEvent struct {
	At     time.Time       `json:"at"`
	Kind   string          `json:"kind"`
	Detail json.RawMessage `json:"detail"`
}

const orderCols = `id,ca_connection_id,status,origin,spec,upstream_order_code,upstream_status,domain_validation,certificate_id,created_at,finished_at,last_error,attempt_count,lease_token,submission_started_at,leased_until`

func scanOrder(row rowScanner) (*IssuanceOrder, error) {
	var o IssuanceOrder
	var spec, validation []byte
	e := row.Scan(&o.ID, &o.ConnectionID, &o.Status, &o.Origin, &spec, &o.Code, &o.UpstreamStatus, &validation, &o.CertificateID, &o.CreatedAt, &o.FinishedAt, &o.LastError, &o.Attempts, &o.LeaseToken, &o.SubmissionStarted, &o.LeasedUntil)
	if e != nil {
		return nil, e
	}
	if e = json.Unmarshal(spec, &o.Spec); e != nil {
		return nil, e
	}
	e = json.Unmarshal(validation, &o.Validation)
	return &o, e
}
func orderEvent(ctx context.Context, tx *sql.Tx, id, kind string, detail any) error {
	b, e := json.Marshal(detail)
	if e != nil {
		return e
	}
	_, e = tx.ExecContext(ctx, `INSERT INTO issuance_order_events(order_id,kind,detail) VALUES($1::uuid,$2,$3::jsonb)`, id, kind, string(b))
	return e
}
func (s *Store) CreateIssuanceOrder(ctx context.Context, conn string, spec upstream.Spec, cipher, nonce []byte, csr string, a IssuanceActor) (*IssuanceOrder, error) {
	tx, e := s.db.BeginTx(ctx, nil)
	if e != nil {
		return nil, e
	}
	defer tx.Rollback()
	var active bool
	e = tx.QueryRowContext(ctx, `SELECT active FROM ca_connections WHERE id=$1::uuid FOR SHARE`, conn).Scan(&active)
	if e != nil {
		return nil, e
	}
	if !active {
		return nil, ErrIssuanceConflict
	}
	b, _ := json.Marshal(spec)
	o, e := scanOrder(tx.QueryRowContext(ctx, `INSERT INTO issuance_orders(ca_connection_id,status,spec,requested_by) SELECT $1::uuid,CASE WHEN approval_required_new THEN 'approval_pending' ELSE 'issuing' END,$2::jsonb,$3::uuid FROM issuance_settings WHERE id RETURNING `+orderCols, conn, string(b), a.ID))
	if e != nil {
		return nil, e
	}
	_, e = tx.ExecContext(ctx, `INSERT INTO issuance_private_keys(order_id,key_algorithm,key_sealed,key_nonce,csr_pem) VALUES($1::uuid,$2,$3,$4,$5)`, o.ID, spec.Algorithm, cipher, nonce, csr)
	if e != nil {
		return nil, e
	}
	if e = orderEvent(ctx, tx, o.ID, "created", map[string]string{"status": o.Status}); e != nil {
		return nil, e
	}
	if e = issuanceAudit(ctx, tx, "issuance.order.create", o.ID, a); e != nil {
		return nil, e
	}
	return o, tx.Commit()
}
func (s *Store) GetIssuanceOrder(ctx context.Context, id string) (*IssuanceOrder, error) {
	o, e := scanOrder(s.db.QueryRowContext(ctx, `SELECT `+orderCols+` FROM issuance_orders WHERE id=$1::uuid`, id))
	if e != nil {
		return nil, e
	}
	rows, e := s.db.QueryContext(ctx, `SELECT at,kind,detail FROM issuance_order_events WHERE order_id=$1::uuid ORDER BY id`, id)
	if e != nil {
		return nil, e
	}
	defer rows.Close()
	o.Events = []IssuanceEvent{}
	for rows.Next() {
		var v IssuanceEvent
		if e = rows.Scan(&v.At, &v.Kind, &v.Detail); e != nil {
			return nil, e
		}
		o.Events = append(o.Events, v)
	}
	return o, rows.Err()
}
func (s *Store) ListIssuanceOrders(ctx context.Context, status string, limit, offset int) ([]IssuanceOrder, error) {
	rows, e := s.db.QueryContext(ctx, `SELECT `+orderCols+` FROM issuance_orders WHERE ($1::text='' OR status=$1::text) ORDER BY created_at DESC,id LIMIT $2 OFFSET $3`, status, limit, offset)
	if e != nil {
		return nil, e
	}
	defer rows.Close()
	out := []IssuanceOrder{}
	for rows.Next() {
		v, e := scanOrder(rows)
		if e != nil {
			return nil, e
		}
		out = append(out, *v)
	}
	return out, rows.Err()
}
func (s *Store) ChangeIssuanceOrder(ctx context.Context, id, action string, a IssuanceActor) error {
	tx, e := s.db.BeginTx(ctx, nil)
	if e != nil {
		return e
	}
	defer tx.Rollback()
	o, e := scanOrder(tx.QueryRowContext(ctx, `SELECT `+orderCols+` FROM issuance_orders WHERE id=$1::uuid FOR UPDATE`, id))
	if e != nil {
		return e
	}
	if o.LeasedUntil != nil && o.LeasedUntil.After(time.Now()) {
		return ErrIssuanceConflict
	}
	next := "issuing"
	kind := "approved"
	switch action {
	case "approve":
		if o.Status != "approval_pending" {
			return ErrIssuanceConflict
		}
	case "retry":
		if o.Status != "issue_failed" {
			return ErrIssuanceConflict
		}
		kind = "retried"
		if o.Code == "" && o.SubmissionStarted != nil {
			return ErrIssuanceConflict
		}
		switch o.UpstreamStatus {
		case "cancelled", "expired", "refunded":
			o.Code = ""
		case "revoked":
			return ErrIssuanceConflict
		}
	case "cancel":
		if o.Status != "approval_pending" && o.Status != "issuing" && o.Status != "awaiting_validation" && o.Status != "issue_failed" {
			return ErrIssuanceConflict
		}
		if o.Code != "" {
			return ErrIssuanceConflict
		}
		next = "cancelled"
		kind = "cancelled"
	default:
		return ErrIssuanceConflict
	}
	_, e = tx.ExecContext(ctx, `UPDATE issuance_orders SET status=$2,upstream_order_code=$3,submission_started_at=CASE WHEN $3::text='' THEN NULL ELSE submission_started_at END,last_error='',attempt_count=0,next_action_at=now(),leased_until=NULL,lease_token=NULL,approved_by=CASE WHEN $4::text='approve' THEN $5::uuid ELSE approved_by END,approved_at=CASE WHEN $4::text='approve' THEN now() ELSE approved_at END,finished_at=CASE WHEN $2::text='cancelled' THEN now() ELSE NULL END WHERE id=$1::uuid`, id, next, o.Code, action, a.ID)
	if e != nil {
		return e
	}
	if e = orderEvent(ctx, tx, id, kind, map[string]string{"status": next}); e != nil {
		return e
	}
	if e = issuanceAudit(ctx, tx, "issuance.order."+action, id, a); e != nil {
		return e
	}
	return tx.Commit()
}
func (s *Store) ClaimIssuanceOrder(ctx context.Context) (*IssuanceOrder, error) {
	return scanOrder(s.db.QueryRowContext(ctx, `UPDATE issuance_orders SET leased_until=now()+interval '2 minutes',lease_token=gen_random_uuid() WHERE id=(SELECT id FROM issuance_orders WHERE status IN ('issuing','awaiting_validation') AND next_action_at<=now() AND (leased_until IS NULL OR leased_until<now()) ORDER BY next_action_at,id FOR UPDATE SKIP LOCKED LIMIT 1) RETURNING `+orderCols))
}
func (s *Store) ClaimIssuanceCancel(ctx context.Context, id string) (*IssuanceOrder, error) {
	return scanOrder(s.db.QueryRowContext(ctx, `UPDATE issuance_orders SET leased_until=now()+interval '2 minutes',lease_token=gen_random_uuid() WHERE id=$1::uuid AND status IN ('issuing','awaiting_validation','issue_failed') AND upstream_order_code<>'' AND (leased_until IS NULL OR leased_until<now()) RETURNING `+orderCols, id))
}
func (s *Store) StartIssuanceSubmission(ctx context.Context, o *IssuanceOrder) (string, error) {
	tx, e := s.db.BeginTx(ctx, nil)
	if e != nil {
		return "", e
	}
	defer tx.Rollback()
	var csr string
	e = tx.QueryRowContext(ctx, `UPDATE issuance_orders SET submission_started_at=now() WHERE id=$1::uuid AND lease_token=$2::uuid AND leased_until>now() AND submission_started_at IS NULL RETURNING (SELECT csr_pem FROM issuance_private_keys WHERE order_id=$1::uuid)`, o.ID, o.LeaseToken).Scan(&csr)
	if e != nil {
		return "", e
	}
	return csr, tx.Commit()
}

type IssuanceResult struct {
	Status, Code, UpstreamStatus, Error, Kind string
	Validation                                []upstream.DomainValidation
	Delay                                     time.Duration
	Attempts                                  int
	Certificate                               *domain.Certificate
	CertPEM, ChainPEM                         string
	Actor                                     IssuanceActor
}

func (s *Store) FinishIssuanceStep(ctx context.Context, o *IssuanceOrder, in IssuanceResult) error {
	tx, e := s.db.BeginTx(ctx, nil)
	if e != nil {
		return e
	}
	defer tx.Rollback()
	var current string
	e = tx.QueryRowContext(ctx, `SELECT status FROM issuance_orders WHERE id=$1::uuid AND lease_token=$2::uuid AND leased_until>now() FOR UPDATE`, o.ID, o.LeaseToken).Scan(&current)
	if e != nil {
		return e
	}
	var certID *string
	if c := in.Certificate; c != nil {
		var id, status string
		var created, updated time.Time
		var fresh bool
		args := append([]any{c.FingerprintSHA256, c.SerialNumber, c.SubjectDN, c.IssuerDN, c.CommonName, strings.Join(c.SANs, sansSep), c.NotBefore, c.NotAfter, c.PublicKeyAlgorithm, c.PublicKeySize, c.SignatureAlgorithm, string(c.CertificateType), string(c.Status), c.SubjectKeyID, c.AuthorityKeyID, c.IssuerOrganization}, certificateContentArgs(c.Content)...)
		e = tx.QueryRowContext(ctx, upsertCertificateSQL, args...).Scan(&id, &status, &created, &updated, &fresh)
		if e != nil {
			return e
		}
		certID = &id
		_, e = tx.ExecContext(ctx, `UPDATE issuance_private_keys SET certificate_id=$2::uuid WHERE order_id=$1::uuid`, o.ID, id)
		if e != nil {
			return e
		}
		if e = orderEvent(ctx, tx, o.ID, "inventory_registered", map[string]string{"certificate_id": id}); e != nil {
			return e
		}
	}
	v := in.Validation
	if v == nil {
		v = []upstream.DomainValidation{}
	}
	b, _ := json.Marshal(v)
	_, e = tx.ExecContext(ctx, `UPDATE issuance_orders SET status=$2,upstream_order_code=$3,upstream_status=$4,domain_validation=$5::jsonb,last_error=$6,attempt_count=$7,next_action_at=now()+($8::double precision*interval '1 second'),leased_until=NULL,lease_token=NULL,certificate_id=coalesce($9::uuid,certificate_id),cert_pem=CASE WHEN $9::uuid IS NULL THEN cert_pem ELSE $10 END,chain_pem=CASE WHEN $9::uuid IS NULL THEN chain_pem ELSE $11 END,finished_at=CASE WHEN $2::text IN ('completed','cancelled','issue_failed','manual_required') THEN now() ELSE NULL END WHERE id=$1::uuid`, o.ID, in.Status, in.Code, in.UpstreamStatus, string(b), in.Error, in.Attempts, in.Delay.Seconds(), certID, in.CertPEM, in.ChainPEM)
	if e != nil {
		return e
	}
	if in.Kind != "" {
		if e = orderEvent(ctx, tx, o.ID, in.Kind, map[string]string{"status": in.Status, "reason": in.Error}); e != nil {
			return e
		}
	}
	auditKind := ""
	switch in.Status {
	case "completed":
		auditKind = "issued"
	case "issue_failed", "manual_required":
		auditKind = "failed"
	case "cancelled":
		auditKind = "cancel"
	}
	if auditKind != "" {
		if e = issuanceAudit(ctx, tx, "issuance.order."+auditKind, o.ID, in.Actor); e != nil {
			return e
		}
	}
	return tx.Commit()
}
func (s *Store) GetIssuanceCSR(ctx context.Context, id string) (string, error) {
	var csr string
	e := s.db.QueryRowContext(ctx, `SELECT csr_pem FROM issuance_private_keys WHERE order_id=$1::uuid`, id).Scan(&csr)
	return csr, e
}
