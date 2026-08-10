-- 0016_issuer_organization (down)
DROP INDEX idx_certificates_issuer_org;
ALTER TABLE certificates DROP COLUMN issuer_organization;
