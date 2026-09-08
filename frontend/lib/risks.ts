/**
 * 인증서 위험 판정(certrisk) 집계·필터 API 클라이언트 (U-F0 계약 모듈).
 *
 * 판정은 **저장값이 아니라 조회 시 계산한 값**이다 — 기준선이 2027·2030에 움직이므로
 * (NIST IR 8547, CA/B Forum) 프론트도 결과를 캐시하지 않고 매 조회 응답을 그대로 쓴다.
 *
 * 축이 두 개다:
 *   - 심각도(`risk=`)  — **최고 심각도** 기준이라 네 값이 인벤토리를 겹침 없이 나눈다.
 *   - 판정 항목(`risk_code=`) — 한 인증서가 여러 항목에 걸린다(합 ≠ total).
 *
 * 집계(GET /certificates/risks)는 status=replaced를 빼고 센다. 목록으로 이동할 때
 * 교체된 인증서를 함께 보면 숫자가 어긋나므로, 목록 화면의 기본값
 * (exclude_status=replaced)을 그대로 두고 이동해야 한다.
 */

import {
  apiFetch,
  type RiskAssessment,
  type RiskCode,
  type RiskFinding,
  type RiskScope,
  type RiskSeverity,
} from "@/lib/api";

// ── 집계 (GET /certificates/risks) ────────────────────────────────────────

/** 최고 심각도별 인증서 수 — count의 합이 total과 같다. */
export interface RiskSeverityCount {
  severity: RiskSeverity;
  count: number;
}

/** 판정 항목별 인증서 수 + 카탈로그 메타데이터. 건수 0인 항목도 내려온다. */
export interface RiskFindingCount {
  code: RiskCode;
  severity: RiskSeverity;
  scope: RiskScope;
  title: string;
  public_trust_only: boolean;
  count: number;
}

export interface CertificateRisksResponse {
  request_id?: string;
  /** 집계 대상 인증서 수 (status=replaced 제외). */
  total: number;
  severities: RiskSeverityCount[];
  findings: RiskFindingCount[];
}

export async function getCertificateRisks(scope?: import("./api").CertificateScope): Promise<CertificateRisksResponse> {
  const res = await apiFetch<CertificateRisksResponse>(
    `/api/v1/certificates/risks${scope ? `?scope=${scope}` : ""}`,
  );
  return {
    request_id: res.request_id,
    total: res.total,
    severities: res.severities ?? [],
    findings: res.findings ?? [],
  };
}

// ── 표시 규약 ─────────────────────────────────────────────────────────────

/** 심각한 순 — 집계 응답 순서와 같지만 화면이 응답 순서에 기대지 않게 고정한다. */
export const RISK_SEVERITY_ORDER: RiskSeverity[] = [
  "danger",
  "warn",
  "info",
  "ok",
];

/**
 * 심각도 라벨 — 색이 아니라 **사용자가 취할 행동**으로 적는다.
 * "danger/warn"을 그대로 쓰면 화면이 심각도를 등급이 아니라 색으로 읽게 만든다.
 */
export const RISK_SEVERITY_LABELS: Record<RiskSeverity, string> = {
  danger: "즉시 조치",
  warn: "점검 필요",
  info: "참고",
  ok: "문제 없음",
};

/** 심각도 → Badge variant. 상태 3색 토큰(danger/warn/ok)만 쓰고 하드코딩하지 않는다. */
export function riskBadgeVariant(
  severity: RiskSeverity,
): "danger" | "warn" | "ok" | "secondary" {
  switch (severity) {
    case "danger":
      return "danger";
    case "warn":
      return "warn";
    case "ok":
      return "ok";
    default:
      // info는 상태 3색 어디에도 속하지 않는다 — 중립 톤으로 둔다.
      return "secondary";
  }
}

/** 판정 축 라벨 — 조치 주체가 갈리는 사실이라 화면에 드러낸다. */
export const RISK_SCOPE_LABELS: Record<RiskScope, string> = {
  certificate: "인증서",
  location: "설치 위치",
};

/**
 * 판정 결과에서 심각도가 실제로 올라간 항목만 추린다 — ok 등급 항목
 * (key_strong 등)은 "문제없음"의 근거라 배지로 띄우면 위험 표기가 희석된다.
 */
export function riskIssues(risk: RiskAssessment | undefined): RiskFinding[] {
  return (risk?.findings ?? []).filter((f) => f.severity !== "ok");
}

// ── 목록 필터 (risk= · risk_code=) ────────────────────────────────────────

export const RISK_PARAM = "risk";
export const RISK_CODE_PARAM = "risk_code";

export interface RiskFilter {
  /** 최고 심각도 필터. null/undefined면 필터 없음. */
  severity?: RiskSeverity | null;
  /** 판정 항목 코드 필터. null/undefined면 필터 없음. */
  code?: RiskCode | null;
}

/**
 * 위험 필터를 listCertificates·exportCertificatesCsv의 URLSearchParams에 붙인다.
 * 두 필터는 동시 지정 가능하며(AND), 없는 필터는 붙이지 않는다.
 */
export function applyRiskFilter(
  params: URLSearchParams,
  filter: RiskFilter,
): URLSearchParams {
  if (filter.severity) params.set(RISK_PARAM, filter.severity);
  if (filter.code) params.set(RISK_CODE_PARAM, filter.code);
  return params;
}

/**
 * 판정 항목 코드 카탈로그 — 백엔드 certrisk.Rules()와 같은 순서다.
 * 화면 목록은 집계 응답의 findings를 쓰고, 이 배열은 URL 값 검증에만 쓴다
 * (집계를 못 받은 상태에서도 손으로 고친 주소를 걸러야 한다).
 */
export const RISK_CODES: RiskCode[] = [
  "weak_key",
  "key_deprecated_2030",
  "key_strong",
  "ed25519_key",
  "weak_signature",
  "no_san",
  "wildcard_broad",
  "wildcard",
  "wildcard_multi_label",
  "long_validity",
  "expired_ca",
  "self_signed",
  "chain_anomaly",
  "legacy_tls",
  "incomplete_chain",
];

/** URL 값이 정의된 심각도인지 — 손으로 고친 주소로 400을 받지 않게 앞단에서 거른다. */
export function isRiskSeverity(v: string | null): v is RiskSeverity {
  return v != null && (RISK_SEVERITY_ORDER as string[]).includes(v);
}

/** URL 값이 정의된 판정 항목 코드인지. */
export function isRiskCode(v: string | null): v is RiskCode {
  return v != null && (RISK_CODES as string[]).includes(v);
}
