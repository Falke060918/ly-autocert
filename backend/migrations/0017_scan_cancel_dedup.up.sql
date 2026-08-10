-- 0017_scan_cancel_dedup (up)
-- 스캔 Job 취소 상태 + 중복 발주 가드의 저장 기반.
--
-- 이 마이그레이션은 **추가만 한다**: 기존 유니크 인덱스·ON CONFLICT 아비터 불변
-- (0010 42P10 전례). 새 부분 유니크 인덱스는 아비터로 쓰지 않는다 —
-- INSERT의 23505를 Go 쪽이 잡아 409로 매핑한다.

-- status CHECK 재생성: 0010 값들의 상위집합(+'canceled'). 0010 'leased' 전례.
ALTER TABLE scan_jobs DROP CONSTRAINT scan_jobs_status_check;
ALTER TABLE scan_jobs ADD CONSTRAINT scan_jobs_status_check
    CHECK (status IN ('pending', 'leased', 'running', 'completed', 'failed', 'canceled'));

-- 동일 정규화 요청(실행기·대상·포트)의 지문. 수동 발주(origin='manual')에만
-- 채운다 — 스윕 청크는 seq 유니크가, 재스캔은 tick advisory lock이 이미 중복을
-- 막고, 여기에 채우면 수동 스캔과 백그라운드 발주가 서로를 409로 막는다.
-- 기존 행은 NULL로 남긴다(과거 이력에 가드를 소급할 이유가 없다).
ALTER TABLE scan_jobs ADD COLUMN dedup_hash text
    CHECK (dedup_hash IS NULL OR dedup_hash ~ '^[0-9a-f]{64}$');

-- 활성(pending/leased/running) 상태에서만 유일 강제 — 종료된 Job과 같은 요청은
-- 언제든 다시 발주할 수 있다. canceled도 종료 상태라 가드에서 빠진다.
CREATE UNIQUE INDEX uq_scan_jobs_dedup_active
    ON scan_jobs (dedup_hash)
    WHERE status IN ('pending', 'leased', 'running');
