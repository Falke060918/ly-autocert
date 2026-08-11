-- 0021_ca_tree_indexes (down)
-- 인덱스만 되돌린다. 데이터는 건드리지 않았으므로 손실이 없다.
DROP INDEX IF EXISTS idx_certificates_issuer_dn;
DROP INDEX IF EXISTS idx_certificates_authority_key_id;
