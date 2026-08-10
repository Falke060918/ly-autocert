-- 0015_endpoint_rescan (up)
-- 인증서 자동 재스캔·교체 감지의 저장 기반.
-- 재스캔 대상은 "지금 인증서가 붙어 있는 엔드포인트"이므로 스캔 Job이 아니라
-- (대상, 포트) 단위의 모니터 행으로 관리한다 — 스캔 Job은 실행 이력이라
-- 다음 언제 다시 볼지를 담을 자리가 없다.
--
-- 이 마이그레이션은 **추가만 한다**: 기존 유니크 인덱스의 컬럼 구성과
-- ON CONFLICT 아비터는 건드리지 않는다(0010에서 아비터 스왑으로 42P10을 낸 전례).
-- 0014는 병렬 진행 중인 작업이 예약 — golang-migrate는 버전 공백을 허용한다.

-- === EndpointMonitor: 재스캔 대상 (대상 시스템 × 포트) ===
-- 인증서 id를 갖지 않는다: 엔드포인트에 붙은 인증서는 바뀌는 것이 정상이고,
-- 무엇이 붙어 있었는지는 fingerprint 2개(current/previous)로 충분하다.
CREATE TABLE endpoint_monitors (
    id                   uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    target_id            uuid NOT NULL REFERENCES targets (id) ON DELETE CASCADE,
    port                 integer NOT NULL CHECK (port BETWEEN 1 AND 65535),
    first_seen_at        timestamptz NOT NULL DEFAULT now(),
    -- 도달 실패 포함 마지막 프로브 시각. 재스캔 순서(due)의 기준이다.
    last_probe_at        timestamptz,
    -- 마지막으로 TLS 핸드셰이크가 성공한 시각.
    last_success_at      timestamptz,
    -- 지문은 소문자 hex 64자로 정규화해 저장한다(0002 certificates와 같은 규약).
    -- 인증서 원문·개인키는 저장하지 않는다 (보안 하드 규칙 1·3).
    current_fingerprint  text CHECK (current_fingerprint ~ '^[0-9a-f]{64}$'),
    -- 같은 지문이 연속으로 몇 번 관측됐는지. LB 뒤 다중 인증서가 번갈아 보이는
    -- 상황에서 1회 관측으로 교체를 확정하면 A와 B가 서로를 무한히 replaced로 만든다.
    current_streak       integer NOT NULL DEFAULT 0,
    previous_fingerprint text CHECK (previous_fingerprint ~ '^[0-9a-f]{64}$'),
    consecutive_misses   integer NOT NULL DEFAULT 0,
    enabled              boolean NOT NULL DEFAULT true,
    -- NULL이면 전역 기본 주기를 쓴다(엔드포인트별 재정의용 자리).
    interval_seconds     integer CHECK (interval_seconds IS NULL OR interval_seconds > 0),
    -- 더 이상 보지 않기로 한 시각. 행은 이력을 위해 남긴다(삭제하지 않는다).
    retired_at           timestamptz,
    CONSTRAINT endpoint_monitors_target_port_key UNIQUE (target_id, port)
);

-- 재스캔 후보 조회 전용 부분 인덱스. 은퇴·비활성 행은 아예 담지 않는다.
CREATE INDEX idx_endpoint_monitors_due
    ON endpoint_monitors (last_probe_at)
    WHERE enabled AND retired_at IS NULL;

-- === 교체 링크 ===
-- 옛 인증서 → 새 인증서. 새 인증서가 지워져도 옛 행의 replaced 상태는 남긴다.
ALTER TABLE certificates
    ADD COLUMN replaced_by_certificate_id uuid REFERENCES certificates (id) ON DELETE SET NULL,
    ADD COLUMN replaced_at                timestamptz;

CREATE INDEX idx_certificates_replaced_by ON certificates (replaced_by_certificate_id);

-- === 스캔 Job의 발주 출처 ===
-- 기존 행은 전부 수동('manual')이며, 스윕 자식 청크만 'sweep'으로 되돌린다.
ALTER TABLE scan_jobs
    ADD COLUMN origin text NOT NULL DEFAULT 'manual'
        CHECK (origin IN ('manual', 'sweep', 'rescan'));

UPDATE scan_jobs SET origin = 'sweep' WHERE sweep_id IS NOT NULL;

-- === 기존 네트워크 위치에서 모니터 시딩 ===
-- 이미 발견된 엔드포인트는 업그레이드 직후부터 재스캔 대상이 된다.
-- current_fingerprint는 채우지 않는다: 한 엔드포인트에 여러 인증서 위치 행이
-- 쌓여 있을 수 있어 "지금 붙어 있는 것"을 SQL로 단정할 수 없다. NULL이면 첫
-- 프로브가 관측값을 그대로 기록하고 previous_fingerprint는 NULL로 남아
-- 교체 후보가 되지 않는다 — 시딩이 허위 교체를 만들지 않는다.
INSERT INTO endpoint_monitors (target_id, port, first_seen_at, last_success_at)
SELECT cl.target_id,
       cl.port,
       coalesce(min(cl.last_verified_at), now()),
       max(cl.last_verified_at)
  FROM certificate_locations cl
 WHERE cl.location_type = 'network_endpoint'
   AND cl.port IS NOT NULL
 GROUP BY cl.target_id, cl.port
ON CONFLICT ON CONSTRAINT endpoint_monitors_target_port_key DO NOTHING;
