-- 0012_notifications (up)
-- 알림·경보 기반 스키마 (docs/00-spec.md 7장 만료 알림).
-- 파이프라인: 평가기가 정책에 걸린 사건을 notification_events에 dedup_key로 1건만 남기고,
-- 채널별 발송 시도는 notification_deliveries가 리스·재시도로 굴린다.
-- 알림함(inbox)은 외부 시스템 없이 항상 동작하는 zero-config 기본 채널이다.

-- 이메일 채널 수신자 주소. 0004 users에는 email 컬럼이 없었다.
-- 형식 검증은 '@'가 로컬파트 뒤에 있는지만 본다 — RFC 5322 전체 검증은 DB의 일이 아니다.
ALTER TABLE users
    ADD COLUMN email text,
    ADD CONSTRAINT users_email_shape
        CHECK (email IS NULL OR position('@' in email) > 1);

-- === NotificationPolicy: 어떤 사건을, 언제, 어떤 채널로, 누구에게 ===
CREATE TABLE notification_policies (
    id                    uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    name                  text NOT NULL,
    enabled               boolean NOT NULL DEFAULT true,
    -- 개방 집합. 사건 종류는 애플리케이션이 정의하므로 CHECK로 막지 않는다.
    event_types           text[] NOT NULL,
    -- 만료 D-N 임계값(일). 만료 계열 사건에서만 쓰인다.
    thresholds            integer[] NOT NULL DEFAULT '{90,60,30,7,1}',
    -- 폐쇄 enum. 새 채널은 CHECK 재생성 마이그레이션으로만 추가한다.
    channels              text[] NOT NULL
        CONSTRAINT notification_policies_channels_known
            CHECK (channels <@ ARRAY['email', 'webhook', 'inbox', 'syslog']::text[]),
    -- [{"kind":"role","role":"..."} | {"kind":"user","user_id":"..."} | {"kind":"address", ...}]
    recipients            jsonb NOT NULL DEFAULT '[]',
    -- NULL = 필터 없음(전체 대상). 빈 배열과 구분된다.
    filter_environments   text[],
    filter_department_ids uuid[],
    created_by            uuid REFERENCES users (id) ON DELETE SET NULL,
    created_at            timestamptz NOT NULL DEFAULT now(),
    -- updated_at은 애플리케이션이 갱신한다 (트리거 없음 — 0002 규약).
    updated_at            timestamptz NOT NULL DEFAULT now(),
    CONSTRAINT notification_policies_name_key UNIQUE (name)
);

-- === NotificationEvent: 평가 결과 1건. 발송 시도와 분리한다 ===
CREATE TABLE notification_events (
    id             uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    -- 멱등 아비터. 평가기가 몇 번 돌아도 같은 사건은 1행만 생긴다
    -- (예: '<event_type>:<certificate_id>:<threshold_days>').
    dedup_key      text NOT NULL,
    event_type     text NOT NULL,
    -- 인증서가 지워져도 사건 이력은 남긴다.
    certificate_id uuid REFERENCES certificates (id) ON DELETE SET NULL,
    -- 만료 계열이 아니면 NULL.
    threshold_days integer,
    -- 정책이 지워져도 사건 이력은 남긴다.
    policy_id      uuid REFERENCES notification_policies (id) ON DELETE SET NULL,
    severity       text NOT NULL,
    payload        jsonb NOT NULL,
    -- pending: 발송 대상 / suppressed: 정책·중복 억제로 발송하지 않음.
    -- 발송 진행 상태는 여기가 아니라 notification_deliveries가 갖는다.
    status         text NOT NULL
        CHECK (status IN ('pending', 'suppressed')),
    created_at     timestamptz NOT NULL DEFAULT now(),
    CONSTRAINT notification_events_dedup_key_key UNIQUE (dedup_key)
);

-- === NotificationDelivery: 채널·수신자별 발송 시도 (리스 + 재시도) ===
CREATE TABLE notification_deliveries (
    id               uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    event_id         uuid NOT NULL REFERENCES notification_events (id) ON DELETE CASCADE,
    channel          text NOT NULL
        CHECK (channel IN ('email', 'webhook', 'inbox', 'syslog')),
    -- 수신자 개념이 없는 채널(webhook·syslog)은 NULL이 아니라 빈 문자열을 쓴다.
    -- Postgres UNIQUE는 NULL을 서로 다른 값으로 취급하므로, NULL을 허용하면
    -- 아래 (event_id, channel, recipient) 제약이 그 채널들에 대해 무력화되어
    -- 재시도·재평가 때마다 중복 발송 행이 무제한으로 쌓인다.
    recipient        text NOT NULL DEFAULT '',
    status           text NOT NULL DEFAULT 'pending'
        CHECK (status IN ('pending', 'sending', 'sent', 'failed', 'skipped')),
    attempt_count    integer NOT NULL DEFAULT 0,
    next_attempt_at  timestamptz NOT NULL DEFAULT now(),
    leased_at        timestamptz,
    lease_expires_at timestamptz,
    last_error       text,
    sent_at          timestamptz,
    created_at       timestamptz NOT NULL DEFAULT now(),
    -- updated_at은 애플리케이션이 갱신한다 (트리거 없음 — 0002 규약).
    updated_at       timestamptz NOT NULL DEFAULT now(),
    CONSTRAINT notification_deliveries_event_channel_recipient_key
        UNIQUE (event_id, channel, recipient)
);

-- 발송 워커 폴링용: 발송 대기 중이면서 재시도 시각이 된 행만 훑는다.
CREATE INDEX idx_notification_deliveries_next_attempt_at
    ON notification_deliveries (next_attempt_at) WHERE status = 'pending';

-- === NotificationInbox: 인앱 알림함. 외부 시스템 없이 동작하는 기본 채널 ===
CREATE TABLE notification_inbox (
    id             uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    user_id        uuid NOT NULL REFERENCES users (id) ON DELETE CASCADE,
    event_id       uuid NOT NULL REFERENCES notification_events (id) ON DELETE CASCADE,
    -- 사건 시점의 표시 내용을 그대로 굳힌다(인증서가 지워져도 읽을 수 있게).
    title          text NOT NULL,
    body           text NOT NULL,
    severity       text NOT NULL,
    -- 링크용 참조값. FK 없음 — 인증서 삭제가 과거 알림 행을 바꾸면 안 된다.
    certificate_id uuid,
    read_at        timestamptz,
    archived_at    timestamptz,
    created_at     timestamptz NOT NULL DEFAULT now(),
    -- 발송 재시도로 같은 사건이 같은 사용자에게 두 번 꽂히는 것을 막는다.
    CONSTRAINT notification_inbox_user_event_key UNIQUE (user_id, event_id)
);

-- 알림함 목록(최신순)과 미읽음 배지 카운트.
CREATE INDEX idx_notification_inbox_user_created_at
    ON notification_inbox (user_id, created_at DESC);
CREATE INDEX idx_notification_inbox_user_unread
    ON notification_inbox (user_id) WHERE read_at IS NULL AND archived_at IS NULL;

-- === seed: 기본 정책 ===
-- 설치 직후 아무 설정 없이도 만료 알림이 알림함에 쌓여야 한다(zero-config).
-- 외부 연동이 필요한 채널은 켜지 않는다. created_by는 시스템 생성이므로 NULL.
INSERT INTO notification_policies (name, enabled, event_types, channels, recipients)
VALUES (
    '기본 만료 알림',
    true,
    ARRAY['certificate.expiring', 'certificate.expired'],
    ARRAY['inbox'],
    '[{"kind":"role","role":"system_administrator"}]'
);
