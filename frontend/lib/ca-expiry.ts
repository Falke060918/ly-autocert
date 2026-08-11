/**
 * 발급 기관(CA) 만료 표기·정렬·분포 헬퍼.
 *
 * CA 만료는 그 CA가 발급한 인증서 전부가 함께 신뢰를 잃는 사건이라, 트리에서
 * 가장 먼저 눈에 띄어야 한다. 그래서 화면은 만료 임박순을 **기본 정렬**로 쓴다.
 *
 * 정렬은 **형제 노드 사이에서만** 한다 — 부모-자식 관계(계층)는 서버가 만든 그대로
 * 둔다. 계층을 평탄화해 한 줄로 세우면 "누가 누구를 발급했는지"가 사라진다.
 * 재귀 구현이므로 트리가 2계층에서 N계층으로 깊어져도 그대로 동작한다.
 *
 * 판정 기준(임계 구간)은 인증서 만료 배지와 같은 `dday`다 — 두 화면이 다른
 * 기준을 쓰면 목록에서 "만료 임박"인 CA가 발급 기관 화면에서는 평범해 보인다.
 */

/** 정렬에 필요한 최소 형태 — IssuerCANode가 이 형태를 만족한다. */
export interface CAExpiryNode {
  not_after: string | null;
  children: CAExpiryNode[];
}

/** 만료 시각(ms). 값이 없거나 파싱할 수 없으면 null — 정렬에서 맨 뒤로 간다. */
function expiryTime(notAfter: string | null): number | null {
  if (!notAfter) return null;
  const t = new Date(notAfter).getTime();
  return Number.isNaN(t) ? null : t;
}

/**
 * 형제 노드를 만료 임박순(오름차순)으로 세운다. 만료일이 없는 노드(인벤토리에
 * 없는 상위 CA placeholder)는 방향과 무관하게 항상 뒤다 — 목록 정렬의
 * NULLS LAST 규약과 같다. 같은 값이면 서버가 준 순서(리프 수 내림차순)를
 * 그대로 유지한다.
 *
 * 입력을 변형하지 않는다 — 원본 응답은 다른 소비자(필터 칩 라벨 등)가 함께 쓴다.
 */
export function sortCANodesByExpiry<T extends CAExpiryNode>(nodes: T[]): T[] {
  return [...nodes]
    .map((n) => ({ ...n, children: sortCANodesByExpiry(n.children) }) as T)
    .sort((a, b) => {
      const ta = expiryTime(a.not_after);
      const tb = expiryTime(b.not_after);
      if (ta === null && tb === null) return 0;
      if (ta === null) return 1;
      if (tb === null) return -1;
      return ta - tb;
    });
}

// ── 만료 분포 구간화 ──────────────────────────────────────────────────────

/**
 * 영향 범위 응답(`ImpactExpiry`)의 **누적** 만료 분포.
 * 백엔드·대시보드 요약과 같은 규약이라 within_30d는 within_7d를 포함한다.
 */
export interface CumulativeExpiryCounts {
  expired: number;
  within_7d: number;
  within_30d: number;
  within_90d: number;
}

/** 겹치지 않는 만료 구간. 막대·범례가 그대로 쓸 수 있는 형태다. */
export interface ExpiryBuckets {
  expired: number;
  d7: number;
  d30: number;
  d90: number;
  /** 90일 밖 — 아직 여유가 있는 나머지. */
  rest: number;
}

/**
 * 누적 분포를 **겹치지 않는 구간**으로 되돌린다.
 *
 * 누적값을 그대로 쌓으면 D-3 인증서 하나가 7·30·90 세 구간에 세 번 세어져
 * 막대 길이 합이 모집단을 넘는다. 뺄셈 결과는 0으로 하한을 두는데, 두 값이
 * 서로 다른 시점에 계산돼 역전되는 경우에도 음수 폭이 나오지 않게 하기 위함이다.
 *
 * `total`은 모집단(영향 집합 전체 크기)이며 `rest`는 거기서 만료·90일 이내를
 * 뺀 값이다.
 */
export function expiryBuckets(
  expiry: CumulativeExpiryCounts,
  total: number,
): ExpiryBuckets {
  return {
    expired: expiry.expired,
    d7: expiry.within_7d,
    d30: Math.max(0, expiry.within_30d - expiry.within_7d),
    d90: Math.max(0, expiry.within_90d - expiry.within_30d),
    rest: Math.max(0, total - expiry.expired - expiry.within_90d),
  };
}
