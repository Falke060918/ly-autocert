-- 0007_audit_chain (up)
-- 이슈 #7: 감사 로그 무결성 — append-only DB 레벨 차단 + SHA-256 해시 체인.
--
-- 설계:
--   * prev_hash: 직전 이벤트의 integrity_hash. 첫 이벤트는 64자리 '0'(genesis).
--   * integrity_hash: 행 필드 전체 + prev_hash의 SHA-256 (pgcrypto digest — 검증된
--     표준 구현만 사용, 자체 암호 구현 금지). BEFORE INSERT 트리거가 계산하므로
--     애플리케이션의 어떤 삽입 경로든 자동으로 체인에 연결된다.
--   * 동시 삽입 직렬화: pg_advisory_xact_lock — head 조회~INSERT~커밋이 트랜잭션
--     단위로 직렬화된다. READ COMMITTED(기본) 전제: 락 대기 후 SELECT가 새 스냅샷으로
--     직전 커밋을 본다. 상위 격리 수준에서 삽입하면 체인이 갈라질 수 있으나, 그 경우
--     uq_audit_events_prev_hash가 삽입을 실패시켜 조용한 분기를 막는다.
--   * UPDATE/DELETE/TRUNCATE는 트리거로 차단한다 (append-only 강제).
--   * 한계: 꼬리(최신) 이벤트 삭제는 체인만으로 탐지할 수 없다 — 검증 API가 반환하는
--     head 해시를 외부 매체에 주기적으로 기록(anchoring)해 보완한다 (OPERATIONS.md 3.1).

-- PG13+에서 trusted 확장이므로 DB 소유자 권한으로 충분하다.
CREATE EXTENSION IF NOT EXISTS pgcrypto;

ALTER TABLE audit_events ADD COLUMN IF NOT EXISTS prev_hash text;

-- 행 → 정본(canonical) 문자열 → SHA-256 hex.
-- NULL 필드는 chr(1), 값은 뒤에 chr(31) 구분자를 붙여 모호성을 없앤다
-- (값에는 제어문자가 올 수 없다: jsonb::text는 제어문자를 \uXXXX로 이스케이프한다).
-- created_at은 epoch(마이크로초 정밀도) 표현을 쓴다 — 삽입·검증이 같은 함수를 쓰므로
-- 표현이 항상 일치한다.
CREATE OR REPLACE FUNCTION audit_event_hash(e audit_events) RETURNS text
LANGUAGE sql STABLE AS $fn$
  SELECT encode(digest(convert_to(
       coalesce(e.id::text                            || chr(31), chr(1))
    || coalesce(e.event_type                          || chr(31), chr(1))
    || coalesce(e.actor_id::text                      || chr(31), chr(1))
    || coalesce(e.target_type                         || chr(31), chr(1))
    || coalesce(e.target_id                           || chr(31), chr(1))
    || coalesce(e.request_id                          || chr(31), chr(1))
    || coalesce(e.before_value::text                  || chr(31), chr(1))
    || coalesce(e.after_value::text                   || chr(31), chr(1))
    || coalesce(e.result                              || chr(31), chr(1))
    || coalesce(e.source_ip::text                     || chr(31), chr(1))
    || coalesce(extract(epoch from e.created_at)::text || chr(31), chr(1))
    || coalesce(e.prev_hash, ''),
    'UTF8'), 'sha256'), 'hex')
$fn$;

-- 기존 이벤트 백필: 이전 임시 해시(단독 SHA-256, 체인 없음)를 새 체인 방식으로
-- 재계산한다. 내용(payload)은 건드리지 않는다. 결정적 연산이라 재실행에 안전하다.
DO $backfill$
DECLARE
  r    audit_events%ROWTYPE;
  prev text := repeat('0', 64);
BEGIN
  FOR r IN SELECT * FROM audit_events ORDER BY id LOOP
    r.prev_hash := prev;
    r.integrity_hash := audit_event_hash(r);
    UPDATE audit_events
       SET prev_hash = r.prev_hash, integrity_hash = r.integrity_hash
     WHERE id = r.id;
    prev := r.integrity_hash;
  END LOOP;
END
$backfill$;

ALTER TABLE audit_events ALTER COLUMN prev_hash SET NOT NULL;

-- 체인 분기(두 행이 같은 직전 해시를 가리킴)를 물리적으로 차단한다.
CREATE UNIQUE INDEX IF NOT EXISTS uq_audit_events_prev_hash ON audit_events (prev_hash);

CREATE OR REPLACE FUNCTION audit_events_set_chain() RETURNS trigger
LANGUAGE plpgsql AS $chain$
DECLARE
  prev text;
BEGIN
  -- 동시 삽입 직렬화 (x'6175646974' = 'audit'): 트랜잭션 종료 시 자동 해제.
  PERFORM pg_advisory_xact_lock(x'6175646974'::bigint);
  -- id를 락 안에서 재발급한다. identity 기본값은 락 대기 **전에** 뽑히므로
  -- 락 획득 순서와 id 순서가 어긋날 수 있고, 그러면 head 조회(ORDER BY id DESC)가
  -- 체인 꼬리가 아닌 행을 반환해 체인이 깨진다(20동시 삽입 실측으로 확인).
  -- 락 안에서 nextval을 다시 뽑으면 id 순서 == 체인(락 획득) 순서가 보장된다.
  NEW.id := nextval(pg_get_serial_sequence('audit_events', 'id'));
  SELECT integrity_hash INTO prev FROM audit_events ORDER BY id DESC LIMIT 1;
  NEW.prev_hash := coalesce(prev, repeat('0', 64));
  NEW.integrity_hash := audit_event_hash(NEW);
  RETURN NEW;
END
$chain$;

CREATE OR REPLACE TRIGGER audit_events_chain
  BEFORE INSERT ON audit_events
  FOR EACH ROW EXECUTE FUNCTION audit_events_set_chain();

CREATE OR REPLACE FUNCTION audit_events_block_change() RETURNS trigger
LANGUAGE plpgsql AS $block$
BEGIN
  RAISE EXCEPTION 'audit_events is append-only (% blocked)', TG_OP;
END
$block$;

CREATE OR REPLACE TRIGGER audit_events_no_change
  BEFORE UPDATE OR DELETE ON audit_events
  FOR EACH ROW EXECUTE FUNCTION audit_events_block_change();

CREATE OR REPLACE TRIGGER audit_events_no_truncate
  BEFORE TRUNCATE ON audit_events
  FOR EACH STATEMENT EXECUTE FUNCTION audit_events_block_change();
