-- 0006_scan_jobs (up)
-- 이슈 #5: 네트워크 TLS 스캔(Discovery) 비동기 Job.
-- 명시적 상태 머신(pending → running → completed/failed)으로 진행 상태를 추적한다.
-- 재시도·중복 실행 안전은 인증서 fingerprint UNIQUE(0002)로 보장한다 —
-- 같은 엔드포인트를 다시 스캔해도 새 Certificate가 아니라 위치 갱신만 일어난다.
-- 0003·0005는 결번(각각 이슈 #3 불필요 / 이슈 #4 예약).

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
