-- 0021_notification_ca_thresholds (up)
-- 발급 기관(CA) 인증서 전용 만료 임계값.
--
-- === 왜 별도 컬럼인가 ===
-- CA 만료는 그 CA가 발급한 인증서 전부가 함께 신뢰를 잃는 사건이고, 대응은
-- "재발급 후 배포"가 아니라 "새 CA 구축 + 전 인증서 재발급 + 신뢰 저장소 갱신"이다.
-- 리프와 같은 D-90에 처음 알리면 이미 늦는다. 그래서 정책 하나 안에서 CA에만
-- 다른(더 이른) 임계값을 줄 수 있게 한다 — 채널·수신자·필터는 그대로 공유한다.
-- 새 사건 종류(certificate.expiring)를 만들지 않는 이유도 같다: 사건이 갈리면
-- 기존 정책·알림함·웹훅 수신 측이 전부 CA를 놓친다.
--
-- === NULL / '{}' / 값의 의미 ===
--   NULL  = CA도 thresholds를 그대로 쓴다 (0021 이전과 동일 — 기본값이 NULL인 이유).
--   '{}'  = CA는 만료 임박 알림을 만들지 않는다 (CA를 별도 절차로 관리하는 조직).
--   값    = CA에만 적용되는 임계값 목록.
-- 세 상태를 구분해야 하므로 DEFAULT '{}'를 두지 않는다 — 기본값을 배열로 두면
-- "미설정"과 "CA 알림 끔"이 같은 값이 되어 되돌릴 수 없다.
--
-- 백필은 0012가 심은 기본 정책 1행에만 한다. 사용자가 직접 만든 정책까지
-- 건드리면 아무도 요청하지 않은 알림이 업그레이드 직후 나간다 —
-- CA 잔여가 임계값 아래인 CA마다 1건씩(임계값당이 아니라 CA당 1건이다.
-- 도달한 임계값 중 최솟값 하나만 발화한다 — 0012 detectExpiryEventsSQL 7.3.1).

ALTER TABLE notification_policies
    ADD COLUMN ca_thresholds integer[];

-- 0012 시드 정책만 CA 전용 임계값을 갖는다(설치 직후 zero-config 동작).
-- created_by IS NULL = 시스템이 심은 행이라는 뜻이다(0012 seed 주석).
UPDATE notification_policies
   SET ca_thresholds = ARRAY[365, 180, 90, 30, 7],
       updated_at    = now()
 WHERE name = '기본 만료 알림'
   AND created_by IS NULL
   AND ca_thresholds IS NULL;
