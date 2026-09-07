ALTER TABLE ca_connections
  ADD COLUMN app_id_sealed bytea,
  ADD COLUMN app_id_nonce bytea,
  ALTER COLUMN app_id DROP NOT NULL,
  ADD CONSTRAINT ca_app_id_sealed_pair CHECK (
    (app_id_sealed IS NULL AND app_id_nonce IS NULL) OR
    (app_id IS NULL AND app_id_sealed IS NOT NULL AND octet_length(app_id_sealed) = 32
     AND app_id_nonce IS NOT NULL AND octet_length(app_id_nonce) = 12));
