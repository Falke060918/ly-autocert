-- 0018_certificate_raw_and_chain (down)
-- 컬럼을 지우면 그 컬럼에 걸린 인덱스도 함께 사라지지만, 0016 down과 같은 형태로
-- 명시적으로 지운다. 되돌리면 저장된 원문도 함께 사라진다 — 다시 채우려면 재스캔이다.
DROP INDEX IF EXISTS idx_certificate_locations_chain_incomplete;

ALTER TABLE certificate_locations
    DROP COLUMN chain_complete,
    DROP COLUMN presented_chain_len;

DROP INDEX IF EXISTS idx_certificates_spki_sha256;

ALTER TABLE certificates
    DROP COLUMN aia_ca_issuer_urls,
    DROP COLUMN crl_urls,
    DROP COLUMN ocsp_urls,
    DROP COLUMN path_len_constraint,
    DROP COLUMN is_ca,
    DROP COLUMN ext_key_usage,
    DROP COLUMN key_usage,
    DROP COLUMN spki_sha256,
    DROP COLUMN raw_der;
