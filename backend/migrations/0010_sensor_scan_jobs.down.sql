-- 0010_sensor_scan_jobs (down)
-- 테스트용 최소 복원 (0007 전례). CHECK는 0006 원형으로 복원한다.
DROP TABLE IF EXISTS scan_job_endpoints;

DROP INDEX IF EXISTS uq_targets_network_scope_ip;
-- 0006 원형 복원. 스코프 분리로 같은 IP가 여러 행이 됐다면 재생성이 실패할 수 있으나,
-- down은 테스트 사이클용이므로 데이터 병합은 하지 않는다.
CREATE UNIQUE INDEX uq_targets_network_ip
    ON targets (ip_address) WHERE target_type = 'network';
ALTER TABLE targets DROP COLUMN IF EXISTS executor_scope;

-- 0006 원형 CHECK로 복원. 'leased' 행은 복원 CHECK 위반이므로 failed로 접는다.
UPDATE scan_jobs SET status = 'failed' WHERE status = 'leased';
ALTER TABLE scan_jobs DROP CONSTRAINT scan_jobs_status_check;
ALTER TABLE scan_jobs ADD CONSTRAINT scan_jobs_status_check
    CHECK (status IN ('pending', 'running', 'completed', 'failed'));

DROP INDEX IF EXISTS idx_scan_jobs_executor_status;
ALTER TABLE scan_jobs
    DROP COLUMN IF EXISTS lease_expires_at,
    DROP COLUMN IF EXISTS leased_at,
    DROP COLUMN IF EXISTS sensor_id,
    DROP COLUMN IF EXISTS executor_type,
    DROP COLUMN IF EXISTS job_type;
