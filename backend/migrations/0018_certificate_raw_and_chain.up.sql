-- 0018_certificate_raw_and_chain (up)
-- 인증서 DER 원문 + 원문에서만 알 수 있는 속성, 그리고 "이 엔드포인트가 실제로
-- 무엇을 제시했는가"의 저장 기반.
--
-- === 왜 원문을 저장하는가 ===
-- 0008 주석이 "원문(DER)을 보관하지 않으므로 기존 행은 백필할 수 없다"고 적었듯,
-- 지금까지 새 속성 컬럼은 전부 재스캔으로만 채워졌다(0008 SKI/AKI가 그랬고
-- cipher suite·지원 TLS 버전도 같은 함정에 있다). 원문을 두면 앞으로 추가되는
-- 속성은 저장된 DER에서 소급 재계산할 수 있다.
-- raw_der는 **인증서의 공개 부분**이다 — 개인키가 아니므로 개인키 평문 저장 금지
-- 규칙(CLAUDE.md 보안 하드 규칙 1) 위반이 아니다. 개인키는 여전히 업로드 시점에
-- 거부한다(certparse.ErrPrivateKeyFound). 용량은 1만 건 × 평균 1.5KB ≈ 15MB다.
--
-- === NULL의 의미 ===
-- 이 마이그레이션은 **백필하지 않는다**(원문이 없으니 불가능하다).
-- 기존 행은 전부 NULL이며 NULL은 "미상"이다. 값이 실제로 없는 경우는 NULL이
-- 아니라 빈 배열('{}')·0·-1로 구분해 넣는다 — "모른다"와 "없다"를 섞으면
-- 판정이 거짓말을 한다. 정상 경로는 재스캔·재업로드로 채워지는 것이다.
--
-- 이 마이그레이션은 **추가만 한다**: 기존 유니크 인덱스·ON CONFLICT 아비터 불변
-- (0010 42P10 전례). 신규 INSERT 경로(upsertCertificateSQL·upsertNetworkLocation)는
-- 같은 커밋에서 함께 고친다 (0015 scan_jobs.origin·0016 전례 — 컬럼만 늘리고
-- INSERT를 빠뜨리면 앞으로 생기는 행이 전부 NULL로 남는다).

ALTER TABLE certificates
    -- DER 원문(공개 부분). 소급 재계산의 근거이며 API로 내보내지 않는다.
    ADD COLUMN raw_der bytea,
    -- SubjectPublicKeyInfo의 SHA-256 (소문자 hex 64자). 공개키 재사용 판정용 —
    -- 지문(fingerprint_sha256)은 인증서 전체라 재발급하면 바뀌지만 이 값은 키를
    -- 그대로 재사용하면 그대로다.
    ADD COLUMN spki_sha256 text
        CONSTRAINT certificates_spki_sha256_hex CHECK (spki_sha256 ~ '^[0-9a-f]{64}$'),
    -- x509 KeyUsage 비트마스크. 0 = KeyUsage 확장 없음, NULL = 미상.
    ADD COLUMN key_usage integer CHECK (key_usage >= 0),
    -- ExtKeyUsage 전체 목록. certificate_type은 classify()가 첫 매칭 1개만 보고
    -- 나머지를 버리므로(그 동작은 유지한다) 원본 목록을 따로 남긴다.
    -- '{}' = EKU 확장 없음, NULL = 미상.
    ADD COLUMN ext_key_usage text[],
    -- BasicConstraints의 CA 여부. certificate_type='ca'로 간접 보유하던 것을
    -- 원본 그대로 남긴다(분류 규칙이 바뀌어도 사실은 안 바뀐다).
    ADD COLUMN is_ca boolean,
    -- BasicConstraints의 pathLenConstraint. -1 = 제약 없음,
    -- NULL = 미상 또는 비적용(BasicConstraints 없음·CA 아님 — is_ca로 구분한다).
    ADD COLUMN path_len_constraint integer CHECK (path_len_constraint >= -1),
    -- 폐기 확인·체인 보완의 근거가 되는 URL들. '{}' = 확장 없음, NULL = 미상.
    ADD COLUMN ocsp_urls text[],
    ADD COLUMN crl_urls text[],
    ADD COLUMN aia_ca_issuer_urls text[];

-- 공개키 재사용(같은 키로 재발급·복사) 조회용.
CREATE INDEX idx_certificates_spki_sha256 ON certificates (spki_sha256);

-- === 엔드포인트가 제시한 체인 ===
-- 지금까지 서버가 제시한 체인은 각각 certificates 행으로 흩어져 들어갈 뿐
-- "이 엔드포인트가 몇 장을 제시했고 그것만으로 체인이 이어지는가"가 남지 않았다.
-- 그래서 다른 엔드포인트가 제시한 중간 CA가 인벤토리에 있으면 체인이 완성된
-- 것처럼 보였다 — 중간 CA 누락(풀체인 미배포) 판정이 불가능했다.
-- 판정은 **그 엔드포인트가 제시한 것만으로** 한다. 인벤토리의 다른 인증서로
-- 보완하면 판정 자체가 무의미해진다(브라우저는 AIA로 보완하지만 curl·Java·
-- Python·모바일 클라이언트는 즉시 실패한다).
ALTER TABLE certificate_locations
    -- 관측 시 서버가 제시한 인증서 장수. NULL = 미상(0018 이전 관측·파일 업로드).
    ADD COLUMN presented_chain_len integer CHECK (presented_chain_len >= 0),
    -- 제시된 체인만으로 리프에서 위로 끊김 없이 이어지고 마지막이 CA(또는 자체
    -- 서명 루트)인가. 루트 자체는 클라이언트 신뢰 저장소에 있으므로 서버가 보내지
    -- 않는 것이 정상 관행이며, 이를 불완전으로 보지 않는다(전건 빨간불 방지).
    -- 네트워크 관측에만 채운다 — 파일 업로드는 엔드포인트 제시가 아니므로 NULL이다.
    ADD COLUMN chain_complete boolean;

-- 중간 CA 누락 판정(certrisk incomplete_chain) 필터 커버. 소수 행만 담는 부분 인덱스다.
CREATE INDEX idx_certificate_locations_chain_incomplete
    ON certificate_locations (certificate_id) WHERE chain_complete = false;
