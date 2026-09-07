CREATE TABLE issuance_orders (
 id uuid PRIMARY KEY DEFAULT gen_random_uuid(), ca_connection_id uuid NOT NULL REFERENCES ca_connections(id),
 origin text NOT NULL DEFAULT 'manual' CHECK(origin IN ('manual','renewal')),
 renewal_of_certificate_id uuid REFERENCES certificates(id),
 status text NOT NULL CHECK(status IN ('requested','approval_pending','issuing','awaiting_validation','issued','deploying','completed','cancelled','issue_failed','deploy_failed','rolled_back','manual_required')),
 spec jsonb NOT NULL, upstream_order_code text NOT NULL DEFAULT '', upstream_status text NOT NULL DEFAULT '',
 domain_validation jsonb NOT NULL DEFAULT '[]', certificate_id uuid REFERENCES certificates(id),
 requested_by uuid REFERENCES users(id), approved_by uuid REFERENCES users(id), approved_at timestamptz,
 next_action_at timestamptz NOT NULL DEFAULT now(), leased_until timestamptz, lease_token uuid,
 submission_started_at timestamptz, attempt_count integer NOT NULL DEFAULT 0, last_error text NOT NULL DEFAULT '',
 cert_pem text NOT NULL DEFAULT '', chain_pem text NOT NULL DEFAULT '',
 created_at timestamptz NOT NULL DEFAULT now(), finished_at timestamptz
);
CREATE UNIQUE INDEX uq_issuance_orders_active_renewal ON issuance_orders(renewal_of_certificate_id) WHERE finished_at IS NULL;
CREATE INDEX issuance_orders_due ON issuance_orders(next_action_at) WHERE status IN ('issuing','awaiting_validation');
CREATE TABLE issuance_order_events (
 id bigserial PRIMARY KEY, order_id uuid NOT NULL REFERENCES issuance_orders(id),
 at timestamptz NOT NULL DEFAULT now(), kind text NOT NULL, detail jsonb NOT NULL DEFAULT '{}'
);
CREATE TABLE issuance_private_keys (
 id uuid PRIMARY KEY DEFAULT gen_random_uuid(), order_id uuid NOT NULL UNIQUE REFERENCES issuance_orders(id),
 certificate_id uuid REFERENCES certificates(id), key_algorithm text NOT NULL CHECK(key_algorithm IN ('rsa2048','rsa3072','rsa4096','ecdsa_p256','ecdsa_p384')),
 key_sealed bytea NOT NULL, key_nonce bytea NOT NULL, csr_pem text NOT NULL,
 created_at timestamptz NOT NULL DEFAULT now()
);
