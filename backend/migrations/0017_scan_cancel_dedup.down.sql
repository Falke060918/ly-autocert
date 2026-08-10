-- 0017_scan_cancel_dedup (down)
DROP INDEX uq_scan_jobs_dedup_active;
ALTER TABLE scan_jobs DROP COLUMN dedup_hash;
-- canceled 행이 있으면 CHECK 복원이 실패한다 — 다운그레이드 전에 정리해야 한다.
ALTER TABLE scan_jobs DROP CONSTRAINT scan_jobs_status_check;
ALTER TABLE scan_jobs ADD CONSTRAINT scan_jobs_status_check
    CHECK (status IN ('pending', 'leased', 'running', 'completed', 'failed'));
