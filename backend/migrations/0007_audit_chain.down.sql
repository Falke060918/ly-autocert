-- 0007_audit_chain (down)
-- 트리거·함수 제거만 한다. prev_hash 컬럼과 데이터는 남긴다(감사 데이터 파괴 금지 —
-- 테이블 자체는 0002 down이 관리한다). 함수는 audit_events 행 타입에 의존하므로
-- 남겨두면 0002 down의 DROP TABLE이 실패한다 — 반드시 함께 제거한다.
DROP TRIGGER IF EXISTS audit_events_no_truncate ON audit_events;
DROP TRIGGER IF EXISTS audit_events_no_change ON audit_events;
DROP TRIGGER IF EXISTS audit_events_chain ON audit_events;
DROP FUNCTION IF EXISTS audit_events_block_change();
DROP FUNCTION IF EXISTS audit_events_set_chain();
DROP FUNCTION IF EXISTS audit_event_hash(audit_events);
DROP INDEX IF EXISTS uq_audit_events_prev_hash;
