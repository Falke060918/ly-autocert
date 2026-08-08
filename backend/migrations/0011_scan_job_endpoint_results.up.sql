-- 0011_scan_job_endpoint_results (up)
-- 센서 Job 진행률의 단일 진실을 만든다 (docs/00-spec.md 7.8 결과 수집).
-- 배경: 관측 배치는 카운터를 증분(+=)하고 하트비트는 센서가 집계한 누적값을 보고한다.
-- 두 값을 같은 컬럼에 그대로 더하면 이중 계상된다. 엔드포인트별 결과를 여기에
-- 남겨 두면 관측 카운터를 절대값으로 재계산할 수 있고, 하트비트는 GREATEST로
-- 앞선 진행률만 반영하면 된다(멱등 재전송에도 안전).
ALTER TABLE scan_job_endpoints
    ADD COLUMN reachable          boolean NOT NULL DEFAULT false,
    ADD COLUMN certificates_found integer NOT NULL DEFAULT 0
        CHECK (certificates_found >= 0);
