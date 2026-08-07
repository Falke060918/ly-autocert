-- 0006_scan_jobs (down)
ALTER TABLE certificate_locations DROP COLUMN IF EXISTS tls_version;
DROP INDEX IF EXISTS uq_certificate_locations_network_endpoint;
DROP INDEX IF EXISTS uq_targets_network_ip;
DROP TABLE IF EXISTS scan_jobs;
