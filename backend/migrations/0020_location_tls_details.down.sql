-- 0020_location_tls_details (down)
-- 컬럼을 지우면 그 컬럼에 걸린 인덱스도 함께 사라지지만 0016·0018 down과 같은
-- 형태로 명시적으로 지운다. 되돌리면 수집한 값도 함께 사라지고, 다시 채우는
-- 경로는 백필이 아니라 **재스캔**뿐이다(위 up 주석의 소급 불가).
DROP INDEX IF EXISTS idx_certificate_locations_legacy_supported;

ALTER TABLE certificate_locations
    DROP COLUMN supported_tls_versions,
    DROP COLUMN cipher_suite;
