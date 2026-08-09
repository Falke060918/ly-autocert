-- 0013_scan_sweeps (down)
-- 테스트 사이클용 최소 복원 (0010·0011 전례). 생성 역순으로 되돌린다.
DROP INDEX IF EXISTS idx_scan_jobs_sweep_seq;
DROP INDEX IF EXISTS uq_scan_jobs_sweep_seq_active;

ALTER TABLE scan_jobs
    DROP COLUMN IF EXISTS port_spec,
    DROP COLUMN IF EXISTS rate_pps,
    DROP COLUMN IF EXISTS sweep_seq,
    DROP COLUMN IF EXISTS sweep_id;

DROP INDEX IF EXISTS idx_scan_sweeps_created_at;
DROP INDEX IF EXISTS idx_scan_sweeps_status;
DROP TABLE IF EXISTS scan_sweeps;
