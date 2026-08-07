-- 0002_core_schema (down)
-- 생성 역순으로 제거한다 (FK 의존 순서).
DROP TABLE IF EXISTS deployment_jobs;
DROP TABLE IF EXISTS renewal_jobs;
DROP TABLE IF EXISTS audit_events;
DROP TABLE IF EXISTS certificate_locations;
DROP TABLE IF EXISTS certificates;
DROP TABLE IF EXISTS targets;
DROP TABLE IF EXISTS policies;
DROP TABLE IF EXISTS certificate_authorities;
