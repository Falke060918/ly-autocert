CREATE TABLE deployment_targets (
 id uuid PRIMARY KEY DEFAULT gen_random_uuid(), agent_id uuid NOT NULL REFERENCES agents(id),
 kind text NOT NULL CHECK(kind IN ('nginx','apache')),
 service_unit text NOT NULL CHECK(service_unit IN ('nginx','httpd','apache2')),
 cert_path text NOT NULL, key_path text NOT NULL, chain_path text NOT NULL DEFAULT '',
 verify_host text NOT NULL DEFAULT '127.0.0.1', verify_port integer NOT NULL DEFAULT 443 CHECK(verify_port BETWEEN 1 AND 65535),
 sni_name text NOT NULL DEFAULT '', current_certificate_id uuid REFERENCES certificates(id),
 active boolean NOT NULL DEFAULT true, created_by uuid REFERENCES users(id), created_at timestamptz NOT NULL DEFAULT now(),
 CHECK ((kind='nginx' AND service_unit='nginx') OR (kind='apache' AND service_unit IN ('httpd','apache2')))
);
CREATE TABLE deploy_jobs (
 id uuid PRIMARY KEY DEFAULT gen_random_uuid(), deployment_target_id uuid NOT NULL REFERENCES deployment_targets(id),
 agent_id uuid NOT NULL REFERENCES agents(id), order_id uuid NOT NULL REFERENCES issuance_orders(id),
 certificate_id uuid NOT NULL REFERENCES certificates(id), target_snapshot jsonb NOT NULL,
 status text NOT NULL DEFAULT 'pending' CHECK(status IN ('pending','leased','running','completed','failed','rolled_back')),
 leased_until timestamptz, lease_token uuid, steps jsonb NOT NULL DEFAULT '[]', error text NOT NULL DEFAULT '',
 observed_fingerprint text NOT NULL DEFAULT '', created_at timestamptz NOT NULL DEFAULT now(), finished_at timestamptz
);
CREATE UNIQUE INDEX deploy_jobs_one_active_target ON deploy_jobs(deployment_target_id) WHERE status IN ('pending','leased','running');
