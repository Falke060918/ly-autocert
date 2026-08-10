/**
 * 발급기관(Issuer) 집계·필터 API 클라이언트 (U-F0 계약 모듈).
 * 조직(O=) 축은 `certificates.issuer_organization`(마이그레이션 0016)에서,
 * CA 트리 축은 인벤토리의 체인 연결(authority_key_id 등)에서 나온다.
 */

import { apiFetch } from "@/lib/api";
import { dnOrganization } from "@/lib/format";

// ── 집계 (GET /certificates/issuers) ──────────────────────────────────────

/** 발급기관 조직(O=) 1건의 분포 카운트 — 대시보드 분포 차트용. */
export interface IssuerOrgCount {
  /** issuer_dn의 O= 값. 조직이 없는 DN·백필 전 행은 null로 묶인다. */
  organization: string | null;
  count: number;
}

/** 서버 ca_tree 노드의 실제 응답(wire) 형태 — 화면은 아래 IssuerCANode를 쓴다. */
interface CATreeNodeWire {
  /** 인벤토리에 이 CA 인증서가 실제로 있으면 그 id, 없으면 null. */
  id: string | null;
  stored: boolean;
  subject_dn: string;
  common_name: string | null;
  not_after: string | null;
  status: string | null;
  self_signed: boolean;
  leaf_count: number;
  /** 하위(children) 포함 리프 합산 — 구버전 백엔드에는 없어 클라이언트가 보정한다. */
  subtree_leaf_count?: number;
  children: CATreeNodeWire[] | null;
}

/** GET /certificates/issuers의 실제 응답(wire). */
interface IssuerAggregationWire {
  request_id?: string;
  organizations: IssuerOrgCount[] | null;
  ca_tree: CATreeNodeWire[] | null;
  unmatched_leaf_count: number;
}

/**
 * CA 트리 노드 — 루트→중간 **2계층 고정**이다. children은 루트 노드에만
 * 채워지고 중간 CA 노드에서는 빈 배열이다. wire의 `id`를 `certificate_id`로,
 * `organization`은 subject_dn의 O=에서 파생해 소비자 형태로 맞춘다.
 */
export interface IssuerCANode {
  /** 인벤토리에 이 CA 인증서가 실제로 있으면 그 id, 없으면 null (wire의 id). */
  certificate_id: string | null;
  /** 인벤토리 보유 여부 (certificate_id !== null과 같다). */
  stored: boolean;
  subject_dn: string;
  common_name: string | null;
  /** subject_dn의 O= 값 — wire에 없어 클라이언트가 파생한다. */
  organization: string | null;
  not_after: string | null;
  status: string | null;
  self_signed: boolean;
  /** 이 CA가 **직접** 발급한 리프 수 — 하위 중간 CA 발급분은 포함하지 않는다. */
  leaf_count: number;
  /**
   * 하위(children) 포함 리프 합산 — 루트의 "하위 포함 N건" 표기용.
   * 중간 노드는 손자가 없어(2계층 평탄화) leaf_count와 같다.
   */
  subtree_leaf_count: number;
  /** 하위 중간 CA. 중간 노드에서는 빈 배열. */
  children: IssuerCANode[];
}

export interface IssuerAggregationResponse {
  request_id?: string;
  /** 조직별 리프 분포 — count 내림차순. */
  organizations: IssuerOrgCount[];
  /** CA 계층 트리 — 루트 노드 배열 (wire의 ca_tree). */
  roots: IssuerCANode[];
  /** 어떤 CA에도 연결되지 않은 리프 수. */
  unmatched_leaf_count: number;
}

function toCANode(node: CATreeNodeWire): IssuerCANode {
  const children = (node.children ?? []).map(toCANode);
  return {
    certificate_id: node.id,
    stored: node.stored,
    subject_dn: node.subject_dn,
    common_name: node.common_name,
    organization: dnOrganization(node.subject_dn),
    not_after: node.not_after,
    status: node.status,
    self_signed: node.self_signed,
    leaf_count: node.leaf_count,
    // 합산 미제공(구버전 백엔드) 시 직접 발급 + 자식 직접 발급으로 보정한다.
    subtree_leaf_count:
      node.subtree_leaf_count ??
      node.leaf_count + children.reduce((sum, c) => sum + c.leaf_count, 0),
    children,
  };
}

export async function getIssuerAggregation(): Promise<IssuerAggregationResponse> {
  const res = await apiFetch<IssuerAggregationWire>(
    "/api/v1/certificates/issuers",
  );
  return {
    request_id: res.request_id,
    organizations: res.organizations ?? [],
    roots: (res.ca_tree ?? []).map(toCANode),
    unmatched_leaf_count: res.unmatched_leaf_count,
  };
}

// ── 인증서 목록 필터 확장 (issuer_org= · issued_by=) ──────────────────────

/** 조직(O=) 정확 일치 필터. 값 ""(빈 문자열)은 "조직 미상(null)" 행을 뜻한다. */
export const ISSUER_ORG_PARAM = "issuer_org";

/**
 * URL 쿼리에서 "조직 미상(null)"을 가리키는 센티널. API 계약은 빈 문자열이지만
 * URL·Radix Select는 빈 값을 담지 못해(트리거가 공백으로 보인다) 화면 쪽만
 * 이 값을 쓰고 API 호출 직전에 ""로 되돌린다. 링크 생성처는 모두 이 값을 쓴다.
 */
export const ISSUER_ORG_UNKNOWN = "__none__";

/** 발급 CA 필터 — 값은 인벤토리 보유 CA의 certificate_id(트리 노드 선택). */
export const ISSUED_BY_PARAM = "issued_by";

export interface IssuerFilter {
  /**
   * issuer_organization 정확 일치. 빈 문자열은 "조직 미상" 필터로 보내고,
   * null/undefined는 필터 없음(파라미터 미부착)이다.
   */
  issuerOrg?: string | null;
  /** 이 CA(certificate_id)가 직접 발급한 인증서만. */
  issuedBy?: string | null;
}

/**
 * 발급기관 필터를 listCertificates·exportCertificatesCsv의 URLSearchParams에
 * 붙인다. 두 필터는 동시 지정 가능하며(AND), 없는 필터는 붙이지 않는다.
 */
export function applyIssuerFilter(
  params: URLSearchParams,
  filter: IssuerFilter,
): URLSearchParams {
  if (filter.issuerOrg != null) params.set(ISSUER_ORG_PARAM, filter.issuerOrg);
  if (filter.issuedBy != null && filter.issuedBy !== "") {
    params.set(ISSUED_BY_PARAM, filter.issuedBy);
  }
  return params;
}
