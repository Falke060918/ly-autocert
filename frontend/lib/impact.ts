/**
 * 영향 범위 집계 API 클라이언트 (U-F0 계약 모듈).
 *
 * "이 CA가 만료되면 무엇을 다시 발급해야 하나"를 한 번에 답하는 축이다.
 * 인증서 목록의 `issued_by=`는 **직계 1단계**만 보는 반면 여기는 계열 전체(전이적
 * 자손)를 본다 — 중간 CA 아래에 달린 실제 서비스 인증서까지 포함한다.
 *
 * 집계는 저장하지 않고 조회할 때마다 계산한다(위험 판정과 같은 규약). 노드를
 * 고를 때마다 새로 부르며 프론트가 캐시로 붙들지 않는다.
 *
 * ⚠ 목록 2종(domains·servers)은 페이지네이션이 없다. `list_limit`(200)에서
 * 잘리고 `*_truncated`가 true가 되므로, 화면은 그 사실과 전체 목록으로 가는
 * 링크를 반드시 함께 보여야 한다 — 잘린 목록을 전부처럼 보이게 하면 재발급
 * 대상이 조용히 빠진다.
 */

import { apiFetch, type CertificateStatus, type CertificateType } from "@/lib/api";
import type { ServerListItem } from "@/lib/servers";

/** 영향을 물은 인증서 자신의 요약. 원문(PEM/DER)·개인키는 담기지 않는다. */
export interface ImpactSubject {
  id: string;
  common_name: string;
  subject_dn: string;
  not_after: string;
  status: CertificateStatus;
  certificate_type: CertificateType;
  self_signed: boolean;
  /** certificate_type === 'ca'. false여도 조회는 성공한다. */
  is_ca: boolean;
}

/**
 * 영향 집합(자신 포함, replaced 제외)의 만료 분포.
 * within_* 는 **누적**이다(within_30d가 within_7d를 포함) — 대시보드 요약과 같은
 * 규약이라 구간 막대로 그리려면 뺄셈이 필요하다.
 */
export interface ImpactExpiry {
  expired: number;
  within_7d: number;
  within_30d: number;
  within_90d: number;
}

/** 영향받는 도메인 1건 — 비-CA 인증서의 CN + SAN을 소문자로 편 값이다. */
export interface ImpactDomain {
  name: string;
  certificate_count: number;
  /** 그 이름의 인증서가 지금 붙어 있는 서버 수. 업로드만 된 인증서는 0이다. */
  server_count: number;
}

export interface CertificateImpactResponse {
  request_id?: string;
  certificate: ImpactSubject;
  /** 전이적 자손 CA 수 (자신 제외, replaced 포함 — CA 트리와 같은 규약). */
  descendant_ca_count: number;
  /** 전이적 자손 비-CA 인증서 수 (replaced 제외). */
  descendant_leaf_count: number;
  /** 영향 집합 전체 크기 (자신 포함, replaced 제외). */
  certificate_count: number;
  /** 자손까지의 최대 발급 단계. 자손이 없으면 0. */
  max_depth: number;
  endpoint_count: number;
  /** GET /servers?issued_by={id}의 total과 같다 (같은 계열 술어). */
  server_count: number;
  domain_count: number;
  expiry: ImpactExpiry;
  domains: ImpactDomain[];
  domains_truncated: boolean;
  servers: ServerListItem[];
  servers_truncated: boolean;
  /** domains·servers에 실은 최대 행 수 (*_truncated의 기준). */
  list_limit: number;
}

export async function getCertificateImpact(
  certificateId: string,
): Promise<CertificateImpactResponse> {
  const res = await apiFetch<CertificateImpactResponse>(
    `/api/v1/certificates/${encodeURIComponent(certificateId)}/impact`,
  );
  return {
    ...res,
    domains: res.domains ?? [],
    servers: res.servers ?? [],
  };
}
