/**
 * 서버(엔드포인트) 축 조회 API 클라이언트 (U-F0 계약 모듈).
 *
 * 인증서 목록은 "인증서 1행"이라 같은 인증서가 여러 서버에 배포된 관계를 뭉갠다.
 * 이 축은 반대로 **호스트 1행**이고, 포트별 확장은 상세(GET /servers/{id})에서
 * 지연 로드한다 — 목록에 전 엔드포인트를 싣는 옵션은 계약에 없다.
 *
 * 저장 컬럼이 없는 전부 파생 집계라 프론트가 다시 계산할 값이 없다.
 *
 * ⚠ `sort=risk`는 **만료 임박도(min(not_after))**이지 위험 판정(certrisk)이 아니다.
 * 화면 라벨을 "위험도순"으로 쓰면 danger/warn 배지와 다른 축을 같은 이름으로
 * 부르게 되므로 반드시 "만료 임박순"으로 적는다 (SERVER_SORT_OPTIONS).
 */

import { apiFetch, type CertificateLocation, type CertificateStatus } from "@/lib/api";

// ── 목록·상세 타입 (openapi.yaml components/schemas) ──────────────────────

/** 엔드포인트에 붙은 인증서 요약. 원문(PEM/DER)·개인키는 담기지 않는다. */
export interface ServerCertificateRef {
  id: string;
  common_name: string;
  not_after: string;
  status: CertificateStatus;
  fingerprint_sha256: string;
}

/** 서버 1행 = targets 1행 = (executor_scope, ip_address). */
export interface ServerListItem {
  id: string;
  hostname: string | null;
  ip_address: string | null;
  /** targets.executor_scope — 'server' 또는 'sensor:<센서 id>'. */
  scope: string;
  /** scope가 센서일 때 그 센서 이름. 서버 스코프면 null. */
  sensor_name: string | null;
  endpoint_count: number;
  certificate_count: number;
  /** 서비스 중인 인증서들의 min(not_after). 하나도 특정하지 못하면 null. */
  earliest_not_after: string | null;
  /** 엔드포인트들의 max(last_success_at). */
  last_seen_at: string | null;
  /** 현재 인증서와 다른, 아직 제거되지 않은 위치 행 수(구버전 잔존). */
  stale_count: number;
  /**
   * 현재 인증서를 관측 지문이 아니라 최근 live 위치로 **추정**한 엔드포인트 수.
   * 0보다 크면 화면이 '추정' 배지를 붙인다 — 추정을 확정처럼 보이게 하지 않는다.
   */
  estimated_count: number;
  /** 오름차순 포트 미리보기(최대 5개). 전체 수는 endpoint_count다. */
  ports: number[];
}

export interface ServerListResponse {
  request_id?: string;
  items: ServerListItem[];
  total: number;
  page: number;
  page_size: number;
}

/** 이 포트에 남아 있는 구버전 인증서 위치. */
export interface ServerStaleLocation {
  location_id: string;
  certificate: ServerCertificateRef;
  last_verified_at: string | null;
  deployment_status: CertificateLocation["deployment_status"];
  tls_version: string | null;
}

/** 서버 상세의 포트 1개. */
export interface ServerEndpoint {
  port: number;
  /** 지금 이 포트에 붙어 있다고 판정한 인증서. 못 정하면 null. */
  certificate: ServerCertificateRef | null;
  /** true면 certificate는 관측 지문이 아니라 최근 live 위치로 추정한 값이다. */
  certificate_estimated: boolean;
  tls_version: string | null;
  last_success_at: string | null;
  last_probe_at: string | null;
  consecutive_misses: number;
  /** 연속 미관측으로 은퇴한 시각(0015). */
  retired_at: string | null;
  stale_locations: ServerStaleLocation[];
}

export interface ServerDetailResponse {
  request_id?: string;
  server: ServerListItem;
  endpoints: ServerEndpoint[];
}

// ── 조회 ──────────────────────────────────────────────────────────────────

export async function listServers(
  params: URLSearchParams,
): Promise<ServerListResponse> {
  const qs = params.toString();
  const res = await apiFetch<ServerListResponse>(
    `/api/v1/servers${qs ? `?${qs}` : ""}`,
  );
  return { ...res, items: res.items ?? [] };
}

export async function getServer(id: string): Promise<ServerDetailResponse> {
  const res = await apiFetch<ServerDetailResponse>(
    `/api/v1/servers/${encodeURIComponent(id)}`,
  );
  return { ...res, endpoints: res.endpoints ?? [] };
}

// ── 표시 규약 ─────────────────────────────────────────────────────────────

/**
 * 정렬 프리셋 — 필드와 방향을 묶어 하나로 고른다.
 * `risk`는 서버가 min(not_after)로 정렬하므로 라벨이 "만료 임박순"이다.
 */
export const SERVER_SORT_OPTIONS = [
  { value: "risk:asc", field: "risk", order: "asc", label: "만료 임박순" },
  { value: "last_seen:desc", field: "last_seen", order: "desc", label: "최근 확인순" },
  { value: "hostname:asc", field: "hostname", order: "asc", label: "호스트명순" },
  { value: "endpoints:desc", field: "endpoints", order: "desc", label: "엔드포인트 많은순" },
  { value: "certificates:desc", field: "certificates", order: "desc", label: "인증서 많은순" },
] as const;

export const DEFAULT_SERVER_SORT = "risk:asc";

/** 호스트 표시 이름 — 호스트명이 없으면 IP, 둘 다 없으면 대상 id 앞자리. */
export function serverLabel(s: ServerListItem): string {
  return s.hostname ?? s.ip_address ?? s.id.slice(0, 8);
}

/** 스코프 라벨 — 센서 스코프는 센서 이름을 보여 준다(uuid를 노출하지 않는다). */
export function serverScopeLabel(s: ServerListItem): string {
  if (s.scope === "server") return "서버 직접";
  return s.sensor_name ? `센서 · ${s.sensor_name}` : "센서";
}

// ── 인증서 상세의 배포 현황 (같은 축의 반대편) ────────────────────────────

/**
 * 설치 위치 1행의 서비스 상태. 인증서 상세가 "이 인증서가 지금도 그 자리에
 * 있는가"를 답하는 3분류다 (backend store.LocationWithTarget 주석과 같은 기준).
 *
 *  - `serving`   — 엔드포인트에서 마지막으로 관측된 지문이 이 인증서다.
 *  - `replaced`  — 관측된 지문이 있는데 다른 인증서다(구버전 잔존).
 *  - `unknown`   — 관측 지문이 없다. 재스캔이 꺼져 있거나 아직 미실행이다.
 */
export type LocationServiceState = "serving" | "replaced" | "unknown";

export function locationServiceState(
  location: Pick<CertificateLocation, "current_fingerprint">,
  certificateFingerprint: string,
): LocationServiceState {
  if (!location.current_fingerprint) return "unknown";
  return location.current_fingerprint === certificateFingerprint
    ? "serving"
    : "replaced";
}

/** 인증서 상세 헤더의 "배포 N · 서비스 중 M · 교체됨 K" 요약. */
export interface DeploymentSummary {
  /** 제거되지 않은 설치 위치 수. */
  deployed: number;
  serving: number;
  replaced: number;
  /** 관측 지문이 없어 판정하지 못한 위치 수 (deployed = serving + replaced + unknown). */
  unknown: number;
}

/**
 * 제거되지 않은 위치만 세어 배포 현황을 만든다 — 제거된 위치는 이력이지 현황이 아니다.
 */
export function deploymentSummary(
  locations: CertificateLocation[],
  certificateFingerprint: string,
): DeploymentSummary {
  const out: DeploymentSummary = {
    deployed: 0,
    serving: 0,
    replaced: 0,
    unknown: 0,
  };
  for (const l of locations) {
    if (l.deployment_status === "removed") continue;
    out.deployed += 1;
    out[locationServiceState(l, certificateFingerprint)] += 1;
  }
  return out;
}
