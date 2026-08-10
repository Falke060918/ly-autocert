-- 0014_admin_ip_rules (up)
-- 관리 콘솔 접근을 출발지 IP로 제한하는 허용 목록.
--
-- 잠금 방지가 이 테이블의 설계 전제다:
--   * 활성 규칙이 0건이면 "제한 없음"이다. 기본 설치가 스스로 잠기지 않는다.
--   * 따라서 seed 행을 넣지 않는다 — 마이그레이션이 규칙을 하나라도 심으면
--     업그레이드하는 순간 기존 운영자가 전부 차단된다.
--   * 규칙 추가·비활성·삭제로 요청자 자신이 차단되는 변경은 API가 거부한다
--     (판정은 같은 트랜잭션 안에서 한다 — store/adminip.go).

CREATE TABLE admin_ip_rules (
    id          uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    -- 단일 IP도 CIDR로 정규화해 담는다(IPv4는 /32, IPv6는 /128).
    -- cidr 타입은 넷마스크 오른쪽 호스트 비트가 켜진 값을 거부하므로
    -- (예: 192.168.1.5/24) API 계층이 미리 정규화·검증한 값만 넣는다.
    cidr        cidr NOT NULL,
    -- 운영 메모. 누가 왜 열어둔 대역인지 남기는 자리다.
    description text NOT NULL DEFAULT ''
        CHECK (char_length(description) <= 200),
    is_active   boolean NOT NULL DEFAULT true,
    -- 계정이 지워져도 규칙은 남아야 한다 — 지우면 그 순간 잠긴다.
    created_by  uuid REFERENCES users (id) ON DELETE SET NULL,
    created_at  timestamptz NOT NULL DEFAULT now(),
    -- updated_at은 애플리케이션이 갱신한다 (트리거 없음 — 0002 규약).
    updated_at  timestamptz NOT NULL DEFAULT now()
);

-- 같은 대역을 두 번 등록하면 "지웠는데 아직 열려 있다"는 오진이 난다.
CREATE UNIQUE INDEX uq_admin_ip_rules_cidr ON admin_ip_rules (cidr);

-- 미들웨어 캐시 적재 쿼리(활성 규칙 전체)용 부분 인덱스.
CREATE INDEX idx_admin_ip_rules_active ON admin_ip_rules (cidr) WHERE is_active;
