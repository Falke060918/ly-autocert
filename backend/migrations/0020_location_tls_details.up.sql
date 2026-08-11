-- 0020_location_tls_details (up)
-- 엔드포인트가 실제로 협상한 암호 스위트와, 그 서버가 받아 주는 TLS 버전 목록.
--
-- === 소급 불가 ===
-- 0018이 인증서 DER 원문을 저장하기 시작하면서 "인증서에서 읽어내는 속성"은
-- 앞으로 소급 재계산할 수 있게 됐다. 그러나 이 두 컬럼은 **인증서의 속성이 아니라
-- 핸드셰이크의 결과**다 — 서버 설정(암호 스위트 우선순위·활성 프로토콜 버전)이
-- 정하는 값이라 저장된 DER 어디에도 없다. 그래서 0018의 raw_der로도 백필할 수 없고,
-- 유일한 채우는 경로는 **재스캔**이다. 이 마이그레이션은 백필하지 않는다.
--
-- === NULL의 의미 ===
-- 두 컬럼 모두 NULL은 "미상(관측한 적 없음)"이지 "없음"이 아니다 (0018과 같은 규약).
--   cipher_suite            NULL = 0020 이전 관측 또는 파일 업로드 위치
--   supported_tls_versions  NULL = 지원 버전 탐지를 켠 적이 없거나 탐지가 중단됨
-- 특히 supported_tls_versions는 **옵트인 수집**이다(SCAN_TLS_PROBE_VERSIONS, 기본 off) —
-- 엔드포인트당 핸드셰이크를 최대 4회 걸어야 해서 레이트·스윕 ETA에 4배 비용이 붙는다.
-- 켜지 않은 설치에서는 이 컬럼이 전건 NULL인 것이 정상이고, 그 상태를 "TLS 1.0만
-- 지원" 같은 판정으로 읽어서는 안 된다. 판정(certrisk legacy_tls)은 지금도 협상
-- 버전(tls_version) 하나만 본다 — 이 컬럼은 아직 판정에 쓰지 않는다.
--
-- 추가만 한다: 기존 유니크 인덱스·ON CONFLICT 아비터 불변(0010 42P10 전례).
-- INSERT 경로(upsertNetworkLocation)는 같은 커밋에서 함께 고친다
-- (0015 scan_jobs.origin·0018 전례 — 컬럼만 늘리면 앞으로 생기는 행도 NULL로 남는다).

ALTER TABLE certificate_locations
    -- 마지막 관측에서 협상된 암호 스위트 이름(crypto/tls의 tls.CipherSuiteName 표기,
    -- 예 'TLS_AES_128_GCM_SHA256'). 미등록 ID는 '0x____' 형태로 들어온다.
    -- 추가 프로브 없이 이미 하는 핸드셰이크에서 읽으므로 스캔 비용이 늘지 않는다.
    ADD COLUMN cipher_suite text
        CONSTRAINT certificate_locations_cipher_suite_len CHECK (length(cipher_suite) <= 128),
    -- 서버가 지원한다고 확인된 프로토콜 버전 목록(오름차순, 예 {'TLS 1.2','TLS 1.3'}).
    -- 값 표기는 tls_version 컬럼과 같다 — 두 곳이 갈라지면 필터가 조용히 어긋난다.
    -- '{}'는 쓰지 않는다: 도달에 성공했다면 협상 버전이 최소 1개는 들어간다.
    ADD COLUMN supported_tls_versions text[];

-- "TLS 1.0/1.1을 아직 받아 주는 엔드포인트" 조회용. 소수 행만 담는 부분 인덱스다.
-- 협상 버전이 1.3이어도 1.0을 함께 열어 둔 서버는 tls_version만으로는 안 잡힌다 —
-- 이 목록이 있어야 보이는 사각지대라 인덱스를 따로 둔다.
CREATE INDEX idx_certificate_locations_legacy_supported
    ON certificate_locations (certificate_id)
    WHERE supported_tls_versions && ARRAY['TLS 1.0', 'TLS 1.1']::text[];
