-- 0010_sensor_scan_jobs (up)
-- Phase 3a: 스캔 Job의 센서 위임 실행 (docs/00-spec.md 7.8 작업 분배·결과 수집, 12장 ScanJob).
-- pull 리스 상태 머신: pending → leased(만료 시각 포함) → running(하트비트) → completed/failed.
-- 리스 만료 시 서버가 failed 처리한다. 작업 유형은 폐쇄 enum('scan' 하나로 시작) —
-- 임의 명령 실행 경로를 두지 않는다 (결정론적 자동화 규칙).

ALTER TABLE scan_jobs
    -- 폐쇄 enum. 새 유형은 CHECK 재생성 마이그레이션으로만 추가한다.
    ADD COLUMN job_type         text NOT NULL DEFAULT 'scan'
        CHECK (job_type IN ('scan')),
    ADD COLUMN executor_type    text NOT NULL DEFAULT 'server'
        CHECK (executor_type IN ('server', 'sensor')),
    -- 센서 위임 실행일 때만 채워진다. 리스 시점에 배정된다.
    ADD COLUMN sensor_id        uuid REFERENCES sensors (id),
    ADD COLUMN leased_at        timestamptz,
    ADD COLUMN lease_expires_at timestamptz;

-- status CHECK 재생성: 0006 값들의 상위집합(+'leased').
ALTER TABLE scan_jobs DROP CONSTRAINT scan_jobs_status_check;
ALTER TABLE scan_jobs ADD CONSTRAINT scan_jobs_status_check
    CHECK (status IN ('pending', 'leased', 'running', 'completed', 'failed'));

-- 센서 pull 폴링용: 리스 대기 Job·리스 만료 회수 조회.
CREATE INDEX idx_scan_jobs_executor_status ON scan_jobs (executor_type, status);

-- 다중 세그먼트 환경은 사설 IP 대역이 겹칠 수 있다 (12장 유의점).
-- 위치 유일성 기준을 (ip)에서 (실행기 스코프, ip)로 확장한다.
-- 스코프 값: 서버 직접 스캔은 'server', 센서 스캔은 'sensor:<sensor_id>' 형태를 예정한다.
ALTER TABLE targets
    ADD COLUMN executor_scope text NOT NULL DEFAULT 'server';

DROP INDEX uq_targets_network_ip;
CREATE UNIQUE INDEX uq_targets_network_scope_ip
    ON targets (executor_scope, ip_address) WHERE target_type = 'network';

-- uq_certificate_locations_network_endpoint(0006)는 변경하지 않는다:
-- (certificate_id, target_id, port) 유일성에서 target_id가 이미 스코프 분리된
-- targets 행(스코프별 IP당 1행)을 가리키므로, 위치 유일성은 자동으로
-- (실행기 스코프, ip, port) 기준이 된다 — 인덱스 구조 변경 불필요.

-- Observation 멱등 아비터: (job_id, ip, port) 기준 (00-spec 7.8 결과 수집).
-- 센서가 같은 배치를 다시 POST해도 PK 충돌로 중복 반영을 막는다 —
-- ON CONFLICT DO NOTHING으로 이미 처리된 관측을 건너뛴다.
CREATE TABLE scan_job_endpoints (
    job_id      uuid NOT NULL REFERENCES scan_jobs (id) ON DELETE CASCADE,
    ip          inet NOT NULL,
    port        integer NOT NULL,
    observed_at timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY (job_id, ip, port)
);
