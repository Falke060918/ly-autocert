-- 0014_admin_ip_rules (down)
-- 테이블을 지우면 그에 딸린 인덱스·제약도 함께 사라진다.
-- 규칙이 사라지면 접근 제한도 사라진다(= 제한 없음) — 다운그레이드가 잠금을 남기지 않는다.
DROP TABLE IF EXISTS admin_ip_rules;
