-- 0013_scan_sweeps (up)
-- 전 포트(1~65535) 백그라운드 스캔: 부모 스윕 1개 + 자식 청크 Job 다수.
-- 자식은 기존 scan_jobs 행을 그대로 쓴다 — 상태 enum(pending/leased/running/
-- completed/failed)과 기존 ON CONFLICT 아비터·인덱스는 건드리지 않는다(0010 전례).
-- 0012는 병렬 진행 중인 알림 마이그레이션이 예약 — golang-migrate는 버전 공백을 허용한다.

CREATE TABLE scan_sweeps (
    id                   uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    name                 text,
    -- 입력값을 그대로 보관해 재현·감사에 쓴다(0006 scan_jobs.targets 규약).
    targets              text[] NOT NULL,
    -- 포트 원본 문법('1-65535', '443,8443' 등)을 그대로 보존한다.
    -- 청크 전개 결과가 아니라 사용자가 넣은 문자열이어야 재현·감사가 된다.
    port_spec            text NOT NULL,
    -- 'server' 또는 'sensor:<uuid>'. targets.executor_scope(0010)와 같은 규약.
    executor_scope       text NOT NULL,
    status               text NOT NULL DEFAULT 'queued'
        CHECK (status IN ('queued', 'running', 'paused', 'completed', 'failed', 'canceled')),
    -- 전수 스캔은 대상×포트가 수천만이 나온다 — integer로는 부족하다.
    total_endpoints      bigint NOT NULL,
    total_chunks         integer NOT NULL,
    -- 다음에 발주할 청크 순번. 발주 시점에만 증가한다.
    next_seq             integer NOT NULL DEFAULT 0,
    rate_pps             integer NOT NULL,
    chunk_endpoints      integer NOT NULL,
    estimated_finish_at  timestamptz,
    -- 센서가 오래 청크를 집어가지 않을 때 세팅한다(UI "센서 대기" 표시용).
    stalled_since        timestamptz,
    last_error           text,
    created_by           uuid REFERENCES users (id) ON DELETE SET NULL,
    created_at           timestamptz NOT NULL DEFAULT now(),
    -- updated_at은 애플리케이션이 갱신한다 (트리거 없음 — 0002 규약).
    updated_at           timestamptz NOT NULL DEFAULT now(),
    started_at           timestamptz,
    finished_at          timestamptz
);

-- 진행률 카운터 컬럼을 의도적으로 두지 않는다: 진행률은 자식 scan_jobs 집계로
-- 매번 파생한다. 부모에 카운터를 두면 하트비트·관측 배치와 함께 이중 계상된다(0011 전례).
-- 가변 필드는 status·next_seq·total_chunks·stalled_since뿐이다.

CREATE INDEX idx_scan_sweeps_status ON scan_sweeps (status);
CREATE INDEX idx_scan_sweeps_created_at ON scan_sweeps (created_at DESC);

-- 자식 청크 Job 연결. 기존 단발 스캔 행은 전부 NULL이다.
ALTER TABLE scan_jobs
    ADD COLUMN sweep_id  uuid REFERENCES scan_sweeps (id) ON DELETE CASCADE,
    ADD COLUMN sweep_seq integer,
    -- 청크가 실행될 때의 발주 조건. 스윕 값이 중간에 바뀌어도 청크는 발주 시점 값을 쓴다.
    ADD COLUMN rate_pps  integer,
    ADD COLUMN port_spec text;

-- 중복 발주 방지 아비터. status <> 'failed' 조건이 핵심이다:
-- (sweep_id, sweep_seq)를 무조건 유일하게 잡으면 실패한 청크를 다시 발주할 수 없다.
-- 실패 행은 유니크 대상에서 빠지므로, 살아있는 청크는 순번당 최대 1개이면서
-- 실패한 청크의 재시도는 그대로 허용된다.
CREATE UNIQUE INDEX uq_scan_jobs_sweep_seq_active
    ON scan_jobs (sweep_id, sweep_seq)
    WHERE sweep_id IS NOT NULL AND status <> 'failed';

-- 진행률 집계(스윕별 자식 Job 조회)용 일반 인덱스.
CREATE INDEX idx_scan_jobs_sweep_seq ON scan_jobs (sweep_id, sweep_seq);
