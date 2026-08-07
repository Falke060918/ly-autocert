-- 0002_core_schema (up)
-- 이슈 #2: 핵심 데이터 모델 (docs/00-spec.md 12장 기준)
-- 완전 구현: certificates, certificate_locations, targets, audit_events
-- 자리만 확보(placeholder, Phase 2~4에서 확장): certificate_authorities, policies,
--   renewal_jobs, deployment_jobs
-- 성능 목표: 인증서 10,000건 인벤토리 조회 (docs/01-plan.md) — 만료·상태·이름 인덱스로 커버.

-- === placeholder: CA (Phase 2 발급 연동에서 스펙 12장 전체 필드로 확장) ===
CREATE TABLE certificate_authorities (
    id         uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    name       text NOT NULL,
    created_at timestamptz NOT NULL DEFAULT now()
);

-- === placeholder: Policy (Phase 2 정책 엔진에서 확장) ===
CREATE TABLE policies (
    id         uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    name       text NOT NULL,
    created_at timestamptz NOT NULL DEFAULT now()
);

-- === Target: 인증서가 설치·발견되는 대상 시스템 ===
CREATE TABLE targets (
    id                uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    name              text NOT NULL,
    target_type       text NOT NULL,
    hostname          text,
    ip_address        inet,
    operating_system  text,
    environment       text,
    -- 에이전트는 Phase 3 범위. FK 없이 컬럼 자리만 둔다.
    agent_id          uuid,
    connection_status text NOT NULL DEFAULT 'unknown'
        CHECK (connection_status IN ('unknown', 'reachable', 'unreachable')),
    tags              text[] NOT NULL DEFAULT '{}'
);

CREATE INDEX idx_targets_hostname ON targets (hostname);

-- === Certificate: 인벤토리의 중심 엔티티 ===
-- 개인키는 이 테이블에 없다. 개인키 저장이 필요해지는 Phase(발급/배포)에서
-- 별도 암호화 저장소로 다룬다 — 평문 저장 금지 (CLAUDE.md 보안 하드 규칙 1).
CREATE TABLE certificates (
    id                   uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    fingerprint_sha256   text NOT NULL,
    serial_number        text NOT NULL,
    subject_dn           text NOT NULL,
    issuer_dn            text NOT NULL,
    common_name          text NOT NULL,
    sans                 text[] NOT NULL DEFAULT '{}',
    not_before           timestamptz NOT NULL,
    not_after            timestamptz NOT NULL,
    public_key_algorithm text NOT NULL,
    public_key_size      integer NOT NULL,
    signature_algorithm  text NOT NULL,
    certificate_type     text NOT NULL
        CHECK (certificate_type IN ('server', 'client', 'ca', 'code_signing', 'email', 'other')),
    -- '만료 임박' 같은 파생 상태는 not_after에서 계산한다. 저장하는 것은 라이프사이클 상태뿐.
    status               text NOT NULL DEFAULT 'active'
        CHECK (status IN ('active', 'expired', 'revoked', 'replaced', 'unknown')),
    ca_id                uuid REFERENCES certificate_authorities (id) ON DELETE SET NULL,
    -- 사용자·부서 테이블은 이슈 #6(로그인+RBAC) 범위. FK는 그때 추가한다.
    owner_id             uuid,
    department_id        uuid,
    environment          text,
    auto_renew           boolean NOT NULL DEFAULT false,
    renewal_policy_id    uuid REFERENCES policies (id) ON DELETE SET NULL,
    -- updated_at은 애플리케이션이 갱신한다 (트리거 없음).
    created_at           timestamptz NOT NULL DEFAULT now(),
    updated_at           timestamptz NOT NULL DEFAULT now(),
    CONSTRAINT certificates_fingerprint_sha256_key UNIQUE (fingerprint_sha256),
    -- SHA-256 지문은 소문자 hex 64자로 정규화해 저장한다 (중복 판정 기준, 이슈 #3).
    CONSTRAINT certificates_fingerprint_sha256_hex CHECK (fingerprint_sha256 ~ '^[0-9a-f]{64}$'),
    CONSTRAINT certificates_validity_order CHECK (not_before <= not_after)
);

CREATE INDEX idx_certificates_not_after ON certificates (not_after);
CREATE INDEX idx_certificates_status ON certificates (status);
CREATE INDEX idx_certificates_common_name ON certificates (common_name);
CREATE INDEX idx_certificates_environment ON certificates (environment);

-- === CertificateLocation: 인증서가 실제로 설치된 위치 (Certificate N:M Target) ===
CREATE TABLE certificate_locations (
    id                uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    certificate_id    uuid NOT NULL REFERENCES certificates (id) ON DELETE CASCADE,
    target_id         uuid NOT NULL REFERENCES targets (id) ON DELETE CASCADE,
    -- Phase 1은 네트워크 스캔이므로 network_endpoint가 주. 나머지는 Phase 3 에이전트용 자리.
    location_type     text NOT NULL
        CHECK (location_type IN ('network_endpoint', 'file', 'windows_store', 'keystore', 'other')),
    file_path         text,
    store_name        text,
    service_name      text,
    port              integer CHECK (port BETWEEN 1 AND 65535),
    last_verified_at  timestamptz,
    deployment_status text NOT NULL DEFAULT 'unknown'
        CHECK (deployment_status IN ('unknown', 'deployed', 'pending', 'failed', 'removed'))
);

CREATE INDEX idx_certificate_locations_certificate_id ON certificate_locations (certificate_id);
CREATE INDEX idx_certificate_locations_target_id ON certificate_locations (target_id);

-- === AuditEvent: 감사 로그. append-only — UPDATE/DELETE 없음 (무결성 검증은 이슈 #7) ===
CREATE TABLE audit_events (
    id             bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    event_type     text NOT NULL,
    actor_id       uuid,
    -- 감사 대상은 임의 리소스이므로 FK 없이 (type, id) 쌍으로 기록한다.
    target_type    text NOT NULL,
    target_id      text,
    request_id     text,
    before_value   jsonb,
    after_value    jsonb,
    result         text NOT NULL CHECK (result IN ('success', 'failure', 'denied')),
    source_ip      inet,
    created_at     timestamptz NOT NULL DEFAULT now(),
    integrity_hash text NOT NULL
);

CREATE INDEX idx_audit_events_created_at ON audit_events (created_at);
CREATE INDEX idx_audit_events_event_type ON audit_events (event_type);
CREATE INDEX idx_audit_events_target ON audit_events (target_type, target_id);

-- === placeholder: RenewalJob (Phase 2 갱신 상태 머신에서 확장) ===
CREATE TABLE renewal_jobs (
    id             uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    certificate_id uuid NOT NULL REFERENCES certificates (id) ON DELETE CASCADE,
    status         text NOT NULL DEFAULT 'pending',
    created_at     timestamptz NOT NULL DEFAULT now()
);

-- === placeholder: DeploymentJob (Phase 3 배포 상태 머신에서 확장) ===
CREATE TABLE deployment_jobs (
    id             uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    certificate_id uuid NOT NULL REFERENCES certificates (id) ON DELETE CASCADE,
    target_id      uuid NOT NULL REFERENCES targets (id) ON DELETE CASCADE,
    status         text NOT NULL DEFAULT 'pending',
    created_at     timestamptz NOT NULL DEFAULT now()
);
