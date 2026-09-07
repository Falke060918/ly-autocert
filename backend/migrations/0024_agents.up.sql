CREATE TABLE agents (
 id uuid PRIMARY KEY DEFAULT gen_random_uuid(), name text NOT NULL,
 status text NOT NULL DEFAULT 'pending' CHECK(status IN ('pending','active','disabled')),
 enrollment_token_hash text UNIQUE, token_expires_at timestamptz NOT NULL,
 client_cert_fingerprint_sha256 text UNIQUE, registered_at timestamptz,
 last_seen_at timestamptz, disabled_at timestamptz,
 created_at timestamptz NOT NULL DEFAULT now(), updated_at timestamptz NOT NULL DEFAULT now()
);
