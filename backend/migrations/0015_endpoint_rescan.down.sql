-- 0015_endpoint_rescan (down)
-- 테스트 사이클용 최소 복원 (0010·0011·0013 전례). 생성 역순으로 되돌린다.
ALTER TABLE scan_jobs DROP COLUMN IF EXISTS origin;

DROP INDEX IF EXISTS idx_certificates_replaced_by;

ALTER TABLE certificates
    DROP COLUMN IF EXISTS replaced_at,
    DROP COLUMN IF EXISTS replaced_by_certificate_id;

DROP INDEX IF EXISTS idx_endpoint_monitors_due;
DROP TABLE IF EXISTS endpoint_monitors;
