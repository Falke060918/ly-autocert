-- 0019_scan_schedules (down)
-- 역순으로 되돌린다. scan_jobs는 애초에 손대지 않았으므로 복원할 것이 없다.
DROP INDEX idx_scan_sweeps_schedule;
ALTER TABLE scan_sweeps DROP COLUMN schedule_id;
ALTER TABLE scan_schedules DROP CONSTRAINT scan_schedules_last_run_fkey;
DROP TABLE scan_schedule_runs;
DROP TABLE scan_schedules;
