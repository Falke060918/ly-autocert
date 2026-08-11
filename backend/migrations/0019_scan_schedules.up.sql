-- 0019_scan_schedules (up)
-- 정기 스캔 3종(전체 full / 사전 설정 preset / 등록된 인증서 registered)의 설정과 실행 이력.
--
-- 실행 엔진을 새로 만들지 않는다: full·preset은 주기마다 기존 스윕(scan_sweeps)을
-- 1건 발주하는 얇은 스케줄이고, registered는 기존 재스캔 엔진(endpoint_monitors)
-- 그대로다. 그래서 이 마이그레이션은 **설정 행과 실행 이력만** 추가하고
-- `scan_jobs.origin` CHECK은 한 글자도 건드리지 않는다 — 정기/수동 구분은
-- scan_sweeps.schedule_id로 한다 (0015 origin 백필 사고, 0010 42P10 전례).
-- 0018은 병렬 진행 중인 작업이 예약 — golang-migrate는 버전 공백을 허용한다.

-- === 정기 스캔 설정 ===
CREATE TABLE scan_schedules (
    id                  uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    kind                text NOT NULL CHECK (kind IN ('full', 'preset', 'registered')),
    -- preset은 여러 개 등록할 수 있어(대역마다 다른 주기·포트) 사람이 구분할 이름이 필요하다.
    -- full·registered는 각각 1행뿐이라 이름이 고정 라벨로만 쓰인다.
    name                text NOT NULL CHECK (btrim(name) <> ''),
    -- full의 기본값은 false다: 신규 설치가 저절로 전 대역을 훑지 않게 한다.
    enabled             boolean NOT NULL DEFAULT false,
    -- 입력 원문(IP/CIDR)을 그대로 보관한다 (0006 scan_jobs.targets · 0013 스윕 규약).
    targets             text[] NOT NULL DEFAULT '{}',
    -- 포트 사양 원문('all', '443,8443' 등). 전개 결과가 아니라 사용자 입력이어야 재현·감사가 된다.
    port_spec           text,
    -- NULL이면 환경변수 기본 주기를 쓴다(엔드포인트별 재정의와 같은 규약 — 0015).
    interval_seconds    integer CHECK (interval_seconds IS NULL OR interval_seconds > 0),
    rate_pps            integer CHECK (rate_pps IS NULL OR rate_pps > 0),
    -- 'server' 또는 'sensor:<uuid>' (0010 targets.executor_scope와 같은 규약).
    executor_scope      text NOT NULL DEFAULT 'server' CHECK (btrim(executor_scope) <> ''),
    -- 다음 예정 실행 시각. NULL이면 예정 없음(비활성 상태의 기본).
    next_run_at         timestamptz,
    -- 마지막으로 **시작된** 실행(수동 실행 포함). next_run_at과 별개다 —
    -- 수동 실행은 주기를 리셋하지 않으므로 여기만 움직인다.
    last_run_started_at timestamptz,
    last_run_id         uuid,
    updated_by          uuid REFERENCES users (id) ON DELETE SET NULL,
    created_at          timestamptz NOT NULL DEFAULT now(),
    -- updated_at은 애플리케이션이 갱신한다 (트리거 없음 — 0002 규약).
    updated_at          timestamptz NOT NULL DEFAULT now(),

    -- registered는 대상·포트를 쓰지 않는다. 이미 등록된 엔드포인트(endpoint_monitors)가
    -- 곧 대상이며, "새 인증서를 찾는 일"과는 별개의 축이다 — 모델에 박아 둔다.
    CONSTRAINT scan_schedules_registered_scope CHECK (
        kind <> 'registered'
        OR (coalesce(cardinality(targets), 0) = 0 AND port_spec IS NULL)),

    -- 켜져 있는데 대상이 없으면 조용히 아무것도 안 하는 스케줄이 된다.
    CONSTRAINT scan_schedules_enabled_needs_targets CHECK (
        kind = 'registered' OR NOT enabled
        OR (cardinality(targets) > 0 AND btrim(coalesce(port_spec, '')) <> ''))
);

-- kind='full'·kind='registered'는 각각 1행 유일, preset은 N행.
-- kind에 단순 UNIQUE를 걸면 preset이 1행으로 묶이므로 부분 유니크로 가른다.
CREATE UNIQUE INDEX uq_scan_schedules_singleton_kind
    ON scan_schedules (kind)
    WHERE kind IN ('full', 'registered');

-- 클레임(주기 도래) 조회 전용 부분 인덱스. 비활성 행은 아예 담지 않는다.
CREATE INDEX idx_scan_schedules_due
    ON scan_schedules (next_run_at)
    WHERE enabled AND kind IN ('full', 'preset');

-- === 실행 이력 ===
CREATE TABLE scan_schedule_runs (
    id                   uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    schedule_id          uuid NOT NULL REFERENCES scan_schedules (id) ON DELETE CASCADE,
    -- 'schedule' = 주기 도래, 'manual' = 사용자의 '지금 실행'.
    trigger              text NOT NULL CHECK (trigger IN ('schedule', 'manual')),
    status               text NOT NULL DEFAULT 'running'
        CHECK (status IN ('running', 'completed', 'failed', 'canceled')),
    -- full·preset이 발주한 스윕. 스윕이 지워져도 실행 이력은 남긴다.
    sweep_id             uuid REFERENCES scan_sweeps (id) ON DELETE SET NULL,
    dispatched_jobs      integer NOT NULL DEFAULT 0,
    -- 전수 스캔은 대상×포트가 수천만이라 integer로는 부족하다 (0013 전례).
    dispatched_endpoints bigint NOT NULL DEFAULT 0,
    started_at           timestamptz NOT NULL DEFAULT now(),
    finished_at          timestamptz,
    error                text
);

-- 겹침 방지의 핵심 제약: 스케줄당 running은 최대 1건이다.
-- 주기가 돌아왔는데 앞 실행이 아직 살아 있으면 그 주기는 **건너뛴다**(store의
-- 클레임이 NOT EXISTS로 거른다). 이 인덱스는 그 판정과 INSERT 사이의 경합까지 막는다.
CREATE UNIQUE INDEX uq_scan_schedule_runs_active
    ON scan_schedule_runs (schedule_id)
    WHERE status = 'running';

CREATE INDEX idx_scan_schedule_runs_schedule
    ON scan_schedule_runs (schedule_id, started_at DESC);

-- 설정 → 마지막 실행 링크. 테이블 생성 순서 때문에 뒤에 붙인다.
ALTER TABLE scan_schedules
    ADD CONSTRAINT scan_schedules_last_run_fkey
    FOREIGN KEY (last_run_id) REFERENCES scan_schedule_runs (id) ON DELETE SET NULL;

-- === 스윕 ← 스케줄 역참조 ===
-- 정기/수동 구분은 이 컬럼으로 한다. scan_jobs.origin CHECK은 건드리지 않는다.
-- 스케줄이 지워져도 스윕 이력은 남긴다(ON DELETE SET NULL).
ALTER TABLE scan_sweeps
    ADD COLUMN schedule_id uuid REFERENCES scan_schedules (id) ON DELETE SET NULL;

CREATE INDEX idx_scan_sweeps_schedule
    ON scan_sweeps (schedule_id)
    WHERE schedule_id IS NOT NULL;

-- === 시드 ===
-- full: 비활성 + 대상 비움. 주기 기본 30일. 업그레이드 직후 전 대역이 도는 일은 없다.
INSERT INTO scan_schedules (kind, name, enabled, targets, port_spec, interval_seconds, executor_scope)
VALUES ('full', '전체 정기 스캔', false, '{}', 'all', 2592000, 'server');

-- registered: 이미 등록된 엔드포인트만 다시 보므로 켜둔다(기존 재스캔 동작과 동일).
-- interval_seconds NULL = RESCAN_INTERVAL 환경변수 기본값.
INSERT INTO scan_schedules (kind, name, enabled, targets, port_spec, interval_seconds, executor_scope)
VALUES ('registered', '등록된 인증서 정기 스캔', true, '{}', NULL, NULL, 'server');

-- preset은 시드하지 않는다 — 어떤 대역을 정기 탐색할지는 사용자가 정한다.
