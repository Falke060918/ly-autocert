-- 0020_ca_tree_indexes (up)
-- CA 트리를 N계층으로 펼치기 위한 조회 인덱스. **컬럼도 테이블도 추가하지 않는다** —
-- 부모-자식 관계는 여전히 조회 시점에 계산한다(SKI=AKI 우선, 키 식별자가 없으면
-- DN 폴백 — certificates_chain.go findIssuer 규약). 엣지 테이블을 두면 재발급·
-- 재업로드마다 갱신해야 하고 0008 이전 데이터처럼 키 식별자가 없는 행에서
-- 관계가 굳어버린다. 계산 규칙이 바뀌면 다음 조회부터 바로 반영되는 지금 구조를
-- 유지하고, 대신 그 계산이 쓰는 두 술어의 우변에 인덱스를 깐다.
--
-- 술어 (store/certificates_issuers.go issuedByMatchSQL):
--   child.authority_key_id = ca.subject_key_id      ← ca 쪽은 0008이 이미 인덱스
--   child.issuer_dn        = ca.subject_dn          ← 양쪽 다 인덱스 없었다
-- 지금까지는 CA 1건마다 certificates 전체를 훑는 조인이었다. 노드별 리프 수·
-- 리프 미리보기·미매칭 카운트가 전부 이 술어를 쓰므로, 계층이 깊어질수록
-- (노드 수 × 전체 행) 스캔이 된다.
--
-- 추가만 하며 기존 유니크 인덱스·ON CONFLICT 아비터는 건드리지 않는다 (0010 42P10 전례).

-- 자식 방향 조회용: "이 SKI를 상위로 가리키는 인증서". NULL 행이 다수라 부분
-- 인덱스로 두어 크기를 줄인다 — 술어가 NULL을 절대 매칭하지 않으므로(= 비교)
-- 부분 인덱스로도 커버가 빠지지 않는다.
CREATE INDEX idx_certificates_authority_key_id
    ON certificates (authority_key_id) WHERE authority_key_id IS NOT NULL;

-- DN 폴백 조회용: "이 subject_dn을 발급자로 적은 인증서". 키 식별자가 없는
-- (0008 이전·확장 없는) 인증서의 유일한 경로라 전체 행을 담는다.
CREATE INDEX idx_certificates_issuer_dn ON certificates (issuer_dn);
