-- 0006_scan_jobs (up)
-- 이슈 #5: 네트워크 TLS 스캔(Discovery) 비동기 Job.
-- 명시적 상태 머신(pending → running → completed/failed)으로 진행 상태를 추적한다.
-- 재시도·중복 실행 안전은 인증서 fingerprint UNIQUE(0002)로 보장한다 —
-- 같은 엔드포인트를 다시 스캔해도 새 Certificate가 아니라 위치 갱신만 일어난다.
-- 0003·0005는 결번(각각 이슈 #3·#4 마이그레이션 불필요).

-- 동시 스캔 경합 방지: 같은 IP의 여러 포트가 동시에 결과를 기록해도
-- 네트워크 대상은 IP당 1행, 네트워크 위치는 (인증서, 대상, 포트)당 1행이다.
-- store는 이 부분 유니크 인덱스를 ON CONFLICT upsert의 아비터로 쓴다.
CREATE UNIQUE INDEX uq_targets_network_ip
    ON targets (ip_address) WHERE target_type = 'network';
CREATE UNIQUE INDEX uq_certificate_locations_network_endpoint
    ON certificate_locations (certificate_id, target_id, port)
    WHERE location_type = 'network_endpoint';

-- 협상된 TLS 프로토콜 버전 (스펙 7.1 수집 항목). 네트워크 스캔 위치에만 채워진다.
ALTER TABLE certificate_locations ADD COLUMN tls_version text;

CREATE TABLE scan_jobs (
    id                   uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    status               text NOT NULL DEFAULT 'pending'
        CHECK (status IN ('pending', 'running', 'completed', 'failed')),
    -- 입력값을 그대로 보관해 재현·감사에 쓴다. IP/CIDR 문자열과 포트 목록.
    targets              text[] NOT NULL,
    ports                integer[] NOT NULL,
    timeout_ms           integer NOT NULL,
    concurrency          integer NOT NULL,
    -- 진행률·결과 요약 카운터. 엔드포인트 단위로 원자적 증가한다.
    total_endpoints      integer NOT NULL DEFAULT 0,
    scanned_endpoints    integer NOT NULL DEFAULT 0,
    reachable_endpoints  integer NOT NULL DEFAULT 0,
    certificates_found   integer NOT NULL DEFAULT 0,
    certificates_new     integer NOT NULL DEFAULT 0,
    error                text,
    created_at           timestamptz NOT NULL DEFAULT now(),
    started_at           timestamptz,
    finished_at          timestamptz
);

CREATE INDEX idx_scan_jobs_status ON scan_jobs (status);
CREATE INDEX idx_scan_jobs_created_at ON scan_jobs (created_at);
