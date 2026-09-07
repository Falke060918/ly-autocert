CREATE TABLE ca_connections (
 id uuid PRIMARY KEY DEFAULT gen_random_uuid(), name text NOT NULL,
 type text NOT NULL DEFAULT 'tlsmanager' CHECK(type='tlsmanager'), base_url text NOT NULL,
 app_id text NOT NULL, secret_sealed bytea NOT NULL, secret_nonce bytea NOT NULL,
 subscriber jsonb NOT NULL, active boolean NOT NULL DEFAULT true,
 created_at timestamptz NOT NULL DEFAULT now(), updated_at timestamptz NOT NULL DEFAULT now()
);
CREATE TABLE issuance_settings (
 id boolean PRIMARY KEY DEFAULT true CHECK(id),
 approval_required_new boolean NOT NULL DEFAULT true, auto_renew boolean NOT NULL DEFAULT true
);
INSERT INTO issuance_settings(id) VALUES(true);
