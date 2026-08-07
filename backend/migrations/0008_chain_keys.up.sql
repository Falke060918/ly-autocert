-- 0008_chain_keys (up)
-- 인증서 체인 관계 조회(02-goals M3): 발급자 매칭용 키 식별자 컬럼.
-- SKI(Subject Key Identifier)·AKI(Authority Key Identifier)는 소문자 hex로 저장한다.
-- 원문(DER)을 보관하지 않으므로 기존 행은 백필할 수 없어 NULL로 남는다 —
-- 체인 조회는 키 식별자가 없으면 issuer_dn = subject_dn DN 매칭으로 폴백하고,
-- 같은 인증서가 재업로드·재스캔되면 upsert가 키 식별자를 백필한다.
ALTER TABLE certificates
    ADD COLUMN subject_key_id   text
        CONSTRAINT certificates_subject_key_id_hex CHECK (subject_key_id ~ '^[0-9a-f]+$'),
    ADD COLUMN authority_key_id text
        CONSTRAINT certificates_authority_key_id_hex CHECK (authority_key_id ~ '^[0-9a-f]+$');

-- 상위 인증서 탐색 커버: AKI = SKI 정확 일치 우선, subject_dn은 DN 폴백 매칭용.
CREATE INDEX idx_certificates_subject_key_id ON certificates (subject_key_id);
CREATE INDEX idx_certificates_subject_dn ON certificates (subject_dn);
