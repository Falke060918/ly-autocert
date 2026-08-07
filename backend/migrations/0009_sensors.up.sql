-- 0009_sensors (up)
-- Phase 3a 센서(원격 수집기) 기반 스키마 (docs/00-spec.md 7.8 센서·12장 Sensor).
-- 등록 흐름: 관리자가 1회용 등록 토큰 발급 → 서버는 토큰의 SHA-256 해시만 저장 →
-- 센서가 로컬 키쌍 생성 + CSR 제출 → 클라이언트 인증서 수령 → 이후 mTLS.
-- 개인키·토큰 원문은 어디에도 저장하지 않는다 (보안 하드 규칙 1).

CREATE TABLE sensors (
    id                              uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    name                            text NOT NULL,
    -- pending: 토큰 발급됨(미등록) / active: 등록 완료 / disabled: 관리자 비활성화
    status                          text NOT NULL DEFAULT 'pending'
        CHECK (status IN ('pending', 'active', 'disabled')),
    -- 1회용 등록 토큰의 SHA-256 해시(소문자 hex 64자). 원문은 저장하지 않는다.
    enrollment_token_hash           text NOT NULL UNIQUE
        CONSTRAINT sensors_enrollment_token_hash_hex
            CHECK (enrollment_token_hash ~ '^[0-9a-f]{64}$'),
    token_expires_at                timestamptz NOT NULL,
    -- 등록 시 발급한 클라이언트 인증서의 fingerprint. mTLS 아이덴티티 대조 키.
    -- 등록 전(pending)에는 NULL.
    client_cert_fingerprint_sha256  text UNIQUE
        CONSTRAINT sensors_client_cert_fingerprint_sha256_hex
            CHECK (client_cert_fingerprint_sha256 ~ '^[0-9a-f]{64}$'),
    registered_at                   timestamptz,
    last_seen_at                    timestamptz,
    disabled_at                     timestamptz,
    created_at                      timestamptz NOT NULL DEFAULT now(),
    -- updated_at은 애플리케이션이 갱신한다 (트리거 없음 — 0002 규약).
    updated_at                      timestamptz NOT NULL DEFAULT now()
);

CREATE INDEX idx_sensors_status ON sensors (status);

-- 센서 클라이언트 인증서 발급용 내부 CA. 단일 행만 허용한다.
-- 개인키는 AES-256-GCM 암호문(ciphertext + nonce)으로만 저장한다 —
-- 평문 저장 금지 (보안 하드 규칙 1). 봉인/개봉은 internal/sensorca 담당.
CREATE TABLE sensor_ca (
    id                 boolean PRIMARY KEY DEFAULT true CHECK (id),
    ca_cert_pem        text NOT NULL,
    ca_key_ciphertext  bytea NOT NULL,
    ca_key_nonce       bytea NOT NULL,
    created_at         timestamptz NOT NULL DEFAULT now()
);
