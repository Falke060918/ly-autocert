-- 0016_issuer_organization (up)
-- 발급 기관(Organization) 표시·필터·집계의 저장 기반.
-- 인증서 원문은 보관하지 않지만 issuer_dn 문자열에 O= RDN이 들어 있으므로
-- 기존 행도 소급 백필이 가능하다. 신규 행은 Go 쪽 certparse.Describe가
-- x509 pkix.Name에서 직접 뽑아 채운다 (0015 scan_jobs.origin 전례 — 백필만
-- 하고 INSERT 경로를 빠뜨리면 앞으로 생기는 행이 전부 NULL로 남는다).
--
-- 이 마이그레이션은 **추가만 한다**: 기존 유니크 인덱스·ON CONFLICT 아비터 불변
-- (0010 42P10 전례).

ALTER TABLE certificates ADD COLUMN issuer_organization text;

-- 발급기관 필터·분포 집계용.
CREATE INDEX idx_certificates_issuer_org ON certificates (issuer_organization);

-- === issuer_dn 소급 백필 ===
-- issuer_dn은 Go pkix.Name.String()의 RFC2253 표기다: RDN은 ','로, 다중값
-- RDN은 '+'로 구분되고, 값 안의 특수문자(,+"\<>; 등)는 '\'로 이스케이프된다.
-- 따라서 O= 값은 "이스케이프되지 않은 , 또는 +"까지이며, 단순 split(',')은
-- 'O=Acme\, Inc.' 같은 값을 잘라먹는다 (frontend format.ts에 있던 것과 같은 결함).
--
-- 정규식 해설 (ARE):
--   (?:^|(?<!\\)[,+])  — 문자열 시작 또는 이스케이프되지 않은 구분자 뒤
--   O=                 — Organization RDN
--   ((?:[^,+\\]|\\.)*) — 값: 일반 문자 또는 '\x' 이스케이프 시퀀스의 반복
-- 캡처값의 '\x'는 regexp_replace로 원문 문자 x로 되돌린다.
-- 한계: 값이 백슬래시로 끝나는 극단 케이스('\\,')는 건너뛴다(오탐보다 미탐 선택).
UPDATE certificates
   SET issuer_organization = nullif(btrim(regexp_replace(
           substring(issuer_dn from '(?:^|(?<!\\)[,+])O=((?:[^,+\\]|\\.)*)'),
           '\\(.)', '\1', 'g')), '')
 WHERE issuer_organization IS NULL
   AND issuer_dn LIKE '%O=%';
