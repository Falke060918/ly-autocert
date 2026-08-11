-- 0022_notification_ca_thresholds (down)
-- 컬럼만 되돌린다. 이미 만들어진 notification_events는 dedup_key로 남으므로
-- 다시 up 해도 같은 임계값이 재발송되지 않는다.

ALTER TABLE notification_policies
    DROP COLUMN ca_thresholds;
