-- 0008_chain_keys (down)
DROP INDEX idx_certificates_subject_dn;
DROP INDEX idx_certificates_subject_key_id;
ALTER TABLE certificates
    DROP COLUMN authority_key_id,
    DROP COLUMN subject_key_id;
