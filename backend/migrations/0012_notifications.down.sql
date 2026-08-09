-- 0012_notifications (down)
-- 생성 역순 복원. 테이블을 지우면 그에 딸린 인덱스·제약도 함께 사라진다.
DROP TABLE IF EXISTS notification_inbox;
DROP TABLE IF EXISTS notification_deliveries;
DROP TABLE IF EXISTS notification_events;
DROP TABLE IF EXISTS notification_policies;

ALTER TABLE users
    DROP CONSTRAINT IF EXISTS users_email_shape,
    DROP COLUMN IF EXISTS email;
