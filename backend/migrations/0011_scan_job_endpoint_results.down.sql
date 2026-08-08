-- 0011_scan_job_endpoint_results (down)
-- 테스트 사이클용 최소 복원 — 엔드포인트별 결과 컬럼만 제거한다.
ALTER TABLE scan_job_endpoints
    DROP COLUMN IF EXISTS certificates_found,
    DROP COLUMN IF EXISTS reachable;
