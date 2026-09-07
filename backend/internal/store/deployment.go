package store

import (
	"context"
	"crypto/x509"
	"database/sql"
	"encoding/json"
	"encoding/pem"
	"errors"
	"time"

	"github.com/Falke060918/INHERIT_AutoCert/backend/internal/agentwire"
	"github.com/Falke060918/INHERIT_AutoCert/backend/internal/audit"
	"github.com/Falke060918/INHERIT_AutoCert/backend/internal/domain"
	"github.com/Falke060918/INHERIT_AutoCert/backend/internal/sensorca"
)

type DeployJob struct {
	ID                  string          `json:"id"`
	OrderID             string          `json:"order_id"`
	TargetID            string          `json:"deployment_target_id"`
	AgentID             string          `json:"agent_id"`
	Status              string          `json:"status"`
	Steps               json.RawMessage `json:"steps"`
	Error               string          `json:"error"`
	ObservedFingerprint string          `json:"observed_fingerprint"`
	CreatedAt           time.Time       `json:"created_at"`
	FinishedAt          *time.Time      `json:"finished_at"`
}

const deployCols = `id,order_id,deployment_target_id,agent_id,status,steps,error,observed_fingerprint,created_at,finished_at`

func scanDeployJob(row rowScanner) (*DeployJob, error) {
	var j DeployJob
	e := row.Scan(&j.ID, &j.OrderID, &j.TargetID, &j.AgentID, &j.Status, &j.Steps, &j.Error, &j.ObservedFingerprint, &j.CreatedAt, &j.FinishedAt)
	return &j, e
}
func (s *Store) ListDeploymentTargets(ctx context.Context) ([]agentwire.Target, error) {
	rows, e := s.db.QueryContext(ctx, `SELECT to_jsonb(t)||jsonb_build_object('current_fingerprint',coalesce(c.fingerprint_sha256,'')) FROM deployment_targets t LEFT JOIN certificates c ON c.id=t.current_certificate_id ORDER BY t.created_at,t.id`)
	if e != nil {
		return nil, e
	}
	defer rows.Close()
	out := []agentwire.Target{}
	for rows.Next() {
		var b []byte
		var t agentwire.Target
		if e = rows.Scan(&b); e != nil {
			return nil, e
		}
		if e = json.Unmarshal(b, &t); e != nil {
			return nil, e
		}
		out = append(out, t)
	}
	return out, rows.Err()
}
func (s *Store) SaveDeploymentTarget(ctx context.Context, t agentwire.Target, a IssuanceActor) (*agentwire.Target, error) {
	if e := t.Validate(); e != nil {
		return nil, e
	}
	tx, e := s.db.BeginTx(ctx, nil)
	if e != nil {
		return nil, e
	}
	defer tx.Rollback()
	var agent string
	e = tx.QueryRowContext(ctx, `SELECT id FROM agents WHERE id=$1::uuid AND status='active' FOR UPDATE`, t.AgentID).Scan(&agent)
	if e != nil {
		return nil, e
	}
	// Serialize target edits with job creation and reject overlapping paths on one host.
	var overlap bool
	e = tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM deployment_targets WHERE agent_id=$1::uuid AND id<>coalesce(nullif($2::text,'')::uuid,'00000000-0000-0000-0000-000000000000'::uuid) AND active AND (cert_path IN ($3,$4,nullif($5,'')) OR key_path IN ($3,$4,nullif($5,'')) OR nullif(chain_path,'') IN ($3,$4,nullif($5,''))))`, t.AgentID, t.ID, t.CertPath, t.KeyPath, t.ChainPath).Scan(&overlap)
	if e != nil {
		return nil, e
	}
	if overlap {
		return nil, ErrIssuanceConflict
	}
	kind := "deploy.target.create"
	if t.ID == "" {
		e = tx.QueryRowContext(ctx, `INSERT INTO deployment_targets(agent_id,kind,service_unit,cert_path,key_path,chain_path,verify_host,verify_port,sni_name,active,created_by) VALUES($1::uuid,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11::uuid) RETURNING id`, t.AgentID, t.Kind, t.ServiceUnit, t.CertPath, t.KeyPath, t.ChainPath, t.VerifyHost, t.VerifyPort, t.SNIName, t.Active, a.ID).Scan(&t.ID)
	} else {
		kind = "deploy.target.update"
		var id string
		e = tx.QueryRowContext(ctx, `UPDATE deployment_targets SET kind=$3,service_unit=$4,cert_path=$5,key_path=$6,chain_path=$7,verify_host=$8,verify_port=$9,sni_name=$10,active=$11 WHERE id=$1::uuid AND agent_id=$2::uuid AND NOT EXISTS(SELECT 1 FROM deploy_jobs WHERE deployment_target_id=$1::uuid AND status IN ('pending','leased','running')) RETURNING id`, t.ID, t.AgentID, t.Kind, t.ServiceUnit, t.CertPath, t.KeyPath, t.ChainPath, t.VerifyHost, t.VerifyPort, t.SNIName, t.Active).Scan(&id)
	}
	if e != nil {
		return nil, e
	}
	if e = issuanceAudit(ctx, tx, kind, t.ID, a); e != nil {
		return nil, e
	}
	return &t, tx.Commit()
}
func (s *Store) DeleteDeploymentTarget(ctx context.Context, id string, a IssuanceActor) error {
	tx, e := s.db.BeginTx(ctx, nil)
	if e != nil {
		return e
	}
	defer tx.Rollback()
	var found string
	e = tx.QueryRowContext(ctx, `UPDATE deployment_targets SET active=false WHERE id=$1::uuid AND NOT EXISTS(SELECT 1 FROM deploy_jobs WHERE deployment_target_id=$1::uuid AND status IN ('pending','leased','running')) RETURNING id`, id).Scan(&found)
	if e != nil {
		return e
	}
	if e = issuanceAudit(ctx, tx, "deploy.target.delete", id, a); e != nil {
		return e
	}
	return tx.Commit()
}
func (s *Store) CreateDeployJob(ctx context.Context, orderID, targetID string, a IssuanceActor) (*DeployJob, error) {
	tx, e := s.db.BeginTx(ctx, nil)
	if e != nil {
		return nil, e
	}
	defer tx.Rollback()
	var b []byte
	var agentID string
	e = tx.QueryRowContext(ctx, `SELECT a.id FROM agents a JOIN deployment_targets t ON t.agent_id=a.id WHERE t.id=$1::uuid AND a.status='active' FOR UPDATE OF a`, targetID).Scan(&agentID)
	if e != nil {
		return nil, e
	}
	var certID string
	e = tx.QueryRowContext(ctx, `SELECT certificate_id FROM issuance_orders WHERE id=$1::uuid AND status IN ('issued','completed','deploy_failed','rolled_back','deploying') AND certificate_id IS NOT NULL FOR UPDATE`, orderID).Scan(&certID)
	if e != nil {
		return nil, e
	}
	e = tx.QueryRowContext(ctx, `SELECT to_jsonb(t)||jsonb_build_object('current_fingerprint',coalesce(c.fingerprint_sha256,'')) FROM deployment_targets t LEFT JOIN certificates c ON c.id=t.current_certificate_id WHERE t.id=$1::uuid AND t.active FOR UPDATE OF t`, targetID).Scan(&b)
	if e != nil {
		return nil, e
	}
	var t agentwire.Target
	if e = json.Unmarshal(b, &t); e != nil {
		return nil, e
	}
	if e = t.Validate(); e != nil {
		return nil, e
	}
	var valid bool
	e = tx.QueryRowContext(ctx, `SELECT not_after>now() AND status NOT IN ('revoked','replaced') FROM certificates WHERE id=$1::uuid`, certID).Scan(&valid)
	if e != nil {
		return nil, e
	}
	if !valid {
		return nil, ErrIssuanceConflict
	}
	j, e := scanDeployJob(tx.QueryRowContext(ctx, `INSERT INTO deploy_jobs(deployment_target_id,agent_id,order_id,certificate_id,target_snapshot) VALUES($1::uuid,$2::uuid,$3::uuid,$4::uuid,$5::jsonb) RETURNING `+deployCols, targetID, agentID, orderID, certID, string(b)))
	if e != nil {
		return nil, e
	}
	_, e = tx.ExecContext(ctx, `UPDATE issuance_orders SET status='deploying',finished_at=NULL WHERE id=$1::uuid`, orderID)
	if e != nil {
		return nil, e
	}
	if e = orderEvent(ctx, tx, orderID, "deploy_started", map[string]string{"job_id": j.ID}); e != nil {
		return nil, e
	}
	if e = issuanceAudit(ctx, tx, "deploy.job.create", j.ID, a); e != nil {
		return nil, e
	}
	return j, tx.Commit()
}
func (s *Store) ListDeployJobs(ctx context.Context, orderID string) ([]DeployJob, error) {
	rows, e := s.db.QueryContext(ctx, `SELECT `+deployCols+` FROM deploy_jobs WHERE order_id=$1::uuid ORDER BY created_at DESC,id LIMIT 200`, orderID)
	if e != nil {
		return nil, e
	}
	defer rows.Close()
	out := []DeployJob{}
	for rows.Next() {
		v, e := scanDeployJob(rows)
		if e != nil {
			return nil, e
		}
		out = append(out, *v)
	}
	return out, rows.Err()
}
func (s *Store) LeaseDeployJob(ctx context.Context, agentID string) (*agentwire.Lease, error) {
	tx, e := s.db.BeginTx(ctx, nil)
	if e != nil {
		return nil, e
	}
	defer tx.Rollback()
	var agent string
	e = tx.QueryRowContext(ctx, `SELECT id FROM agents WHERE id=$1::uuid AND status='active' FOR UPDATE`, agentID).Scan(&agent)
	if e != nil {
		return nil, e
	}
	var l agentwire.Lease
	var b []byte
	e = tx.QueryRowContext(ctx, `UPDATE deploy_jobs SET status='leased',leased_until=now()+interval '90 seconds',lease_token=gen_random_uuid() WHERE id=(SELECT id FROM deploy_jobs WHERE agent_id=$1::uuid AND status='pending' AND NOT EXISTS(SELECT 1 FROM deploy_jobs WHERE agent_id=$1::uuid AND status IN ('leased','running')) ORDER BY created_at,id FOR UPDATE SKIP LOCKED LIMIT 1) RETURNING id,target_snapshot,lease_token,leased_until,(SELECT fingerprint_sha256 FROM certificates WHERE id=deploy_jobs.certificate_id)`, agentID).Scan(&l.JobID, &b, &l.LeaseToken, &l.ExpiresAt, &l.ExpectedFingerprint)
	if e != nil {
		return nil, e
	}
	l.Kind = "deploy"
	if e = json.Unmarshal(b, &l.Target); e != nil {
		return nil, e
	}
	return &l, tx.Commit()
}
func (s *Store) TouchDeployJob(ctx context.Context, agentID, id, token, action string) error {
	status := "running"
	condition := "running"
	if action == "start" {
		condition = "leased"
	} else if action != "heartbeat" {
		return ErrIssuanceConflict
	}
	res, e := s.db.ExecContext(ctx, `UPDATE deploy_jobs SET status=$5,leased_until=now()+interval '90 seconds' WHERE id=$1::uuid AND agent_id=$2::uuid AND lease_token=$3::uuid AND leased_until>now() AND status=$4`, id, agentID, token, condition, status)
	if e != nil {
		return e
	}
	n, e := res.RowsAffected()
	if e != nil {
		return e
	}
	if n != 1 {
		return ErrIssuanceConflict
	}
	return nil
}
func (s *Store) DeploymentMaterial(ctx context.Context, agentID, id, token string, key []byte) (*agentwire.Material, error) {
	tx, e := s.db.BeginTx(ctx, nil)
	if e != nil {
		return nil, e
	}
	defer tx.Rollback()
	var m agentwire.Material
	var cipher, nonce []byte
	var orderID string
	e = tx.QueryRowContext(ctx, `SELECT o.id,o.cert_pem,o.chain_pem,k.key_sealed,k.key_nonce FROM deploy_jobs j JOIN issuance_orders o ON o.id=j.order_id JOIN issuance_private_keys k ON k.order_id=o.id JOIN agents a ON a.id=j.agent_id WHERE j.id=$1::uuid AND j.agent_id=$2::uuid AND j.lease_token=$3::uuid AND j.status IN ('leased','running') AND j.leased_until>now() AND a.status='active' FOR SHARE OF j,a`, id, agentID, token).Scan(&orderID, &m.CertPEM, &m.ChainPEM, &cipher, &nonce)
	if e != nil {
		return nil, e
	}
	der, e := sensorca.Open(key, cipher, nonce)
	if e != nil {
		return nil, errors.New("개인키 복호화 실패")
	}
	defer clear(der)
	if _, e = x509.ParsePKCS8PrivateKey(der); e != nil {
		return nil, errors.New("개인키 형식 오류")
	}
	if e = audit.Insert(ctx, tx, audit.Event{Type: "issuance.key.release", TargetType: "deploy_job", TargetID: &id, After: map[string]string{"job_id": id, "agent_id": agentID, "order_id": orderID}, Result: domain.AuditSuccess}); e != nil {
		return nil, e
	}
	if e = tx.Commit(); e != nil {
		return nil, e
	}
	m.KeyPEM = string(pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: der}))
	return &m, nil
}
func (s *Store) CompleteDeployJob(ctx context.Context, agentID, id string, r agentwire.Result) error {
	if e := r.Validate(); e != nil {
		return e
	}
	tx, e := s.db.BeginTx(ctx, nil)
	if e != nil {
		return e
	}
	defer tx.Rollback()
	var orderID string
	e = tx.QueryRowContext(ctx, `SELECT order_id FROM deploy_jobs WHERE id=$1::uuid AND agent_id=$2::uuid`, id, agentID).Scan(&orderID)
	if e != nil {
		return e
	}
	var locked string
	e = tx.QueryRowContext(ctx, `SELECT id FROM issuance_orders WHERE id=$1::uuid FOR UPDATE`, orderID).Scan(&locked)
	if e != nil {
		return e
	}
	var targetID, certID, status, expected, old string
	e = tx.QueryRowContext(ctx, `SELECT j.deployment_target_id,j.certificate_id,j.status,c.fingerprint_sha256,coalesce(j.target_snapshot->>'current_fingerprint','') FROM deploy_jobs j JOIN certificates c ON c.id=j.certificate_id WHERE j.id=$1::uuid AND j.agent_id=$2::uuid AND j.lease_token=$3::uuid AND ((j.status='running' AND j.leased_until>now()) OR j.status IN ('completed','failed','rolled_back')) FOR UPDATE OF j`, id, agentID, r.LeaseToken).Scan(&targetID, &certID, &status, &expected, &old)
	if e != nil {
		return e
	}
	if status != "running" {
		if status == r.Status {
			return nil
		}
		return ErrIssuanceConflict
	}
	if r.Status == "completed" && r.ObservedFingerprint != expected {
		return ErrIssuanceConflict
	}
	if r.Status == "rolled_back" && old != "" && r.ObservedFingerprint != old {
		return ErrIssuanceConflict
	}
	// Agent messages are untrusted: persist only fixed summaries, never command output.
	for i := range r.Steps {
		if r.Steps[i].OK {
			r.Steps[i].Message = "완료"
		} else {
			r.Steps[i].Message = "실패"
		}
	}
	if r.Error != "" {
		r.Error = "배포 단계 기록을 확인하세요"
	}
	b, _ := json.Marshal(r.Steps)
	_, e = tx.ExecContext(ctx, `UPDATE deploy_jobs SET status=$2,steps=$3::jsonb,error=$4,observed_fingerprint=$5,finished_at=now(),leased_until=NULL WHERE id=$1::uuid`, id, r.Status, string(b), r.Error, r.ObservedFingerprint)
	if e != nil {
		return e
	}
	if r.Status == "completed" {
		_, e = tx.ExecContext(ctx, `UPDATE deployment_targets SET current_certificate_id=$2::uuid WHERE id=$1::uuid`, targetID, certID)
		if e != nil {
			return e
		}
	}
	if e = refreshDeployOrder(ctx, tx, orderID); e != nil {
		return e
	}
	if e = orderEvent(ctx, tx, orderID, "deploy_finished", map[string]string{"job_id": id, "status": r.Status}); e != nil {
		return e
	}
	if e = issuanceAudit(ctx, tx, "deploy.job.finish", id, IssuanceActor{}); e != nil {
		return e
	}
	return tx.Commit()
}
func refreshDeployOrder(ctx context.Context, tx *sql.Tx, id string) error {
	_, e := tx.ExecContext(ctx, `WITH latest AS (SELECT DISTINCT ON (deployment_target_id) status FROM deploy_jobs WHERE order_id=$1::uuid ORDER BY deployment_target_id,created_at DESC,id DESC), state AS (SELECT CASE WHEN bool_or(status IN ('pending','leased','running')) THEN 'deploying' WHEN bool_or(status='failed') THEN 'deploy_failed' WHEN bool_or(status='rolled_back') THEN 'rolled_back' ELSE 'completed' END AS status FROM latest) UPDATE issuance_orders SET status=state.status,finished_at=CASE WHEN state.status='deploying' THEN NULL ELSE now() END FROM state WHERE id=$1::uuid`, id)
	return e
}
func (s *Store) ReapDeployJobs(ctx context.Context) error {
	rows, e := s.db.QueryContext(ctx, `SELECT id FROM deploy_jobs WHERE status IN ('leased','running') AND leased_until<now() ORDER BY id LIMIT 100`)
	if e != nil {
		return e
	}
	ids := []string{}
	for rows.Next() {
		var id string
		if e = rows.Scan(&id); e != nil {
			rows.Close()
			return e
		}
		ids = append(ids, id)
	}
	e = rows.Err()
	rows.Close()
	if e != nil {
		return e
	}
	for _, id := range ids {
		if e = s.reapDeploy(ctx, id); e != nil {
			return e
		}
	}
	return nil
}
func (s *Store) reapDeploy(ctx context.Context, id string) error {
	tx, e := s.db.BeginTx(ctx, nil)
	if e != nil {
		return e
	}
	defer tx.Rollback()
	var order string
	e = tx.QueryRowContext(ctx, `SELECT o.id FROM issuance_orders o JOIN deploy_jobs j ON j.order_id=o.id WHERE j.id=$1::uuid FOR UPDATE OF o`, id).Scan(&order)
	if e != nil {
		return e
	}
	var done string
	e = tx.QueryRowContext(ctx, `UPDATE deploy_jobs SET status='failed',error='에이전트 리스 만료. 서비스 상태를 확인하세요',finished_at=now() WHERE id=$1::uuid AND status IN ('leased','running') AND leased_until<now() RETURNING id`, id).Scan(&done)
	if errors.Is(e, sql.ErrNoRows) {
		return nil
	}
	if e != nil {
		return e
	}
	if e = refreshDeployOrder(ctx, tx, order); e != nil {
		return e
	}
	if e = orderEvent(ctx, tx, order, "deploy_finished", map[string]string{"job_id": id, "status": "failed", "reason": "lease_expired"}); e != nil {
		return e
	}
	if e = issuanceAudit(ctx, tx, "deploy.job.finish", id, IssuanceActor{}); e != nil {
		return e
	}
	return tx.Commit()
}
