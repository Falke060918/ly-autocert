/** 날짜·D-day·DN 표시 헬퍼 — 목업(mockup/index.html)의 표기 규칙을 따른다. */

const DAY_MS = 24 * 60 * 60 * 1000;

/** "2026-08-07" 형식 (로컬 시간대). */
export function formatDate(iso: string): string {
  const d = new Date(iso);
  const p = (n: number) => String(n).padStart(2, "0");
  return `${d.getFullYear()}-${p(d.getMonth() + 1)}-${p(d.getDate())}`;
}

/** "2026-08-07 09:45" 형식 (로컬 시간대). */
export function formatDateTime(iso: string): string {
  const d = new Date(iso);
  const p = (n: number) => String(n).padStart(2, "0");
  return `${formatDate(iso)} ${p(d.getHours())}:${p(d.getMinutes())}`;
}

export interface Dday {
  /** "D-3" / "D+9" — 만료 경과는 D+. */
  label: string;
  /** 긴급도: 만료·7일 이내 urgent, 30일 이내 soon, 그 외 plain. */
  tone: "urgent" | "soon" | "plain";
  /**
   * "완전히 남은 일수". 백엔드 알림 탐지 쿼리의
   * `floor(extract(epoch FROM (not_after - now())) / 86400)`와 같은 값이어야 한다 —
   * 어느 한쪽만 올림하면 메일 제목 D-30과 화면 D-31이 항상 어긋난다.
   * 만료 후에는 음수다.
   */
  days: number;
  /**
   * 만료 여부. days 부호로 판정하지 않는다 — `Math.floor`도 `Math.ceil`도
   * 하루 미만 구간에서 판정을 어긋나게 한다(`Math.ceil(-0.25)`는 `-0`이고 `-0 < 0`은 false다).
   */
  expired: boolean;
}

/** not_after 기준 D-day. 목업 규칙: D-7 이하 빨강, D-30 이하 호박, 나머지 회색. */
export function dday(notAfter: string): Dday {
  const diffMs = new Date(notAfter).getTime() - Date.now();
  const days = Math.floor(diffMs / DAY_MS);
  // 만료 경계는 백엔드와 같다 — not_after == now는 아직 유효다.
  if (diffMs < 0) {
    // 경과 일수는 -days가 아니다: 6시간 경과면 floor(-0.25) = -1이라 D+1이 된다.
    const elapsed = Math.floor(-diffMs / DAY_MS);
    return { label: `D+${elapsed}`, tone: "urgent", days, expired: true };
  }
  if (days <= 7) return { label: `D-${days}`, tone: "urgent", days, expired: false };
  if (days <= 30) return { label: `D-${days}`, tone: "soon", days, expired: false };
  return { label: `D-${days}`, tone: "plain", days, expired: false };
}

/** DN 문자열에서 CN 값을 뽑는다. 없으면 DN 전체를 반환한다. */
export function dnCommonName(dn: string): string {
  const m = dn.match(/(?:^|[,/])\s*CN=([^,/]+)/i);
  return m ? m[1].trim() : dn;
}

/** 자체 서명 판정 — 백엔드 파생 필터(subject_dn = issuer_dn)와 동일 기준. */
export function isSelfSigned(subjectDN: string, issuerDN: string): boolean {
  return subjectDN === issuerDN;
}

/** fingerprint 앞 12자 — 표·피드의 짧은 식별 표기용. */
export function shortFingerprint(fp: string): string {
  return fp.slice(0, 12);
}
