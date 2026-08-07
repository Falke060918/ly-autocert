-- 0004_users_rbac (up)
-- 이슈 #6: 로그인 + 기본 RBAC (docs/00-spec.md 7.11장 기본 역할 6종)
-- 0003은 병렬 진행 중인 이슈 #3이 예약 — golang-migrate는 버전 공백을 허용한다.

-- === placeholder: Department (스펙 12장에 미정의 — certificates.department_id의 FK 대상 자리만) ===
CREATE TABLE departments (
    id         uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    name       text NOT NULL,
    created_at timestamptz NOT NULL DEFAULT now()
);

-- === User: 로컬 계정 (00-spec 7.11 인증 방식 MVP) ===
-- password_hash는 bcrypt 결과만 저장한다 — 평문 저장 금지 (CLAUDE.md 보안 하드 규칙 1).
-- auth_provider는 추후 LDAP/AD/OIDC/SAML 연동 확장점 — MVP는 'local'만 쓴다 (CHECK로 막지 않는다).
CREATE TABLE users (
    id            uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    username      text NOT NULL,
    display_name  text,
    password_hash text NOT NULL,
    role          text NOT NULL
        CHECK (role IN ('system_administrator', 'pki_administrator', 'security_administrator',
                        'operator', 'auditor', 'viewer')),
    auth_provider text NOT NULL DEFAULT 'local',
    is_active     boolean NOT NULL DEFAULT true,
    department_id uuid REFERENCES departments (id) ON DELETE SET NULL,
    created_at    timestamptz NOT NULL DEFAULT now(),
    updated_at    timestamptz NOT NULL DEFAULT now(),
    CONSTRAINT users_username_key UNIQUE (username)
);

-- === Session: 서버측 세션. 토큰 원문은 저장하지 않고 SHA-256 해시만 저장한다. ===
CREATE TABLE sessions (
    id         uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    user_id    uuid NOT NULL REFERENCES users (id) ON DELETE CASCADE,
    -- 소문자 hex 64자 (SHA-256). DB가 유출돼도 토큰 원문을 복원할 수 없다.
    token_hash text NOT NULL,
    created_at timestamptz NOT NULL DEFAULT now(),
    expires_at timestamptz NOT NULL,
    revoked_at timestamptz,
    CONSTRAINT sessions_token_hash_key UNIQUE (token_hash),
    CONSTRAINT sessions_token_hash_hex CHECK (token_hash ~ '^[0-9a-f]{64}$')
);

CREATE INDEX idx_sessions_user_id ON sessions (user_id);
CREATE INDEX idx_sessions_expires_at ON sessions (expires_at);

-- 0002에서 미뤄둔 FK를 여기서 추가한다 (0002 주석 참조).
-- audit_events.actor_id는 의도적으로 FK 없음 — 감사 로그는 append-only이며
-- 사용자 삭제가 과거 감사 행을 변경(SET NULL)하면 안 된다.
ALTER TABLE certificates
    ADD CONSTRAINT certificates_owner_id_fkey
    FOREIGN KEY (owner_id) REFERENCES users (id) ON DELETE SET NULL;
ALTER TABLE certificates
    ADD CONSTRAINT certificates_department_id_fkey
    FOREIGN KEY (department_id) REFERENCES departments (id) ON DELETE SET NULL;
