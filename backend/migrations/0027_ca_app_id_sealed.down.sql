-- Never silently discard sealed credentials or reconstruct plaintext on rollback.
DO $$ BEGIN
  IF EXISTS (SELECT 1 FROM ca_connections WHERE app_id_sealed IS NOT NULL) THEN
    RAISE EXCEPTION 'sealed CA credentials prevent downgrade';
  END IF;
END $$;
ALTER TABLE ca_connections DROP CONSTRAINT ca_app_id_sealed_pair,
  DROP COLUMN app_id_sealed, DROP COLUMN app_id_nonce,
  ALTER COLUMN app_id SET NOT NULL;
