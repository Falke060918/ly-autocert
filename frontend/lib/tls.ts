/**
 * 관측 TLS 버전 열·필터의 계약 모듈 (목록 `tls_version=` / CSV 내보내기 공용).
 *
 * 값의 정본은 백엔드 스캐너(discovery.TLSVersionNames)이고 API는 네 값만 받는다 —
 * 밖의 값은 400 invalid_tls_version이라 목록 전체가 오류 알럿으로 바뀐다. 그래서
 * 옵션도 검증도 **집계 API가 아니라 이 고정 카탈로그**를 본다: 위험·발급기관 필터는
 * 집계 응답이 없으면 Select 자체가 사라져 URL에 남은 필터를 풀 수단이 없어지는데,
 * TLS 필터에는 그 실패 경로가 아예 없다.
 *
 * 목록 응답의 `tls_versions`가 이 필터가 보는 값과 같은 집합이라 화면의 열과 필터
 * 결과가 어긋나지 않는다. 설치 위치 상세의 `supported_tls_versions`는 "서버가 함께
 * 열어 둔 버전"으로 축이 다르다 — 여기서 쓰지 않는다.
 *
 * **정렬 축이 아니다.** 위치 집계 파생값이라 서버 정렬 화이트리스트에 없다
 * (last_seen_at·risk와 같은 처리).
 */

import type { TlsVersion } from "@/lib/api";

export const TLS_VERSION_PARAM = "tls_version";

/**
 * 낮은(위험한) 버전부터. 화면 목록과 URL 값 검증이 함께 쓰는 고정 카탈로그다 —
 * 어떤 API 응답에도 기대지 않는다.
 */
export const TLS_VERSIONS: TlsVersion[] = [
  "TLS 1.0",
  "TLS 1.1",
  "TLS 1.2",
  "TLS 1.3",
];

/** URL 값이 정의된 버전인지 — 손으로 고친 주소로 400을 받지 않게 앞단에서 거른다. */
export function isTlsVersion(v: string | null): v is TlsVersion {
  return v != null && (TLS_VERSIONS as string[]).includes(v);
}

/**
 * URL의 `tls_version` 값을 정규화한다. API가 쉼표 구분 다중값을 OR로 받으므로
 * 화면도 여러 개를 유지하고, 카탈로그 밖 값은 조용히 버린다(400 방지).
 * 순서는 카탈로그 순으로 고정해 같은 필터가 두 가지 주소로 갈라지지 않게 한다.
 */
export function tlsVersionsFromSearch(searchParams: {
  get(name: string): string | null;
}): TlsVersion[] {
  const raw = searchParams.get(TLS_VERSION_PARAM);
  if (!raw) return [];
  const picked = new Set(raw.split(",").map((s) => s.trim()).filter(isTlsVersion));
  return TLS_VERSIONS.filter((v) => picked.has(v));
}

/**
 * 관측 TLS 버전 필터를 listCertificates·exportCertificatesCsv의 URLSearchParams에
 * 붙인다. 빈 배열이면 붙이지 않는다(전체).
 */
export function applyTlsVersionFilter(
  params: URLSearchParams,
  versions: TlsVersion[],
): URLSearchParams {
  if (versions.length > 0) params.set(TLS_VERSION_PARAM, versions.join(","));
  return params;
}

/** 여러 버전을 한 줄로 잇는 표기 — 열 셀과 Select 라벨이 같은 문구를 쓴다. */
export function formatTlsVersions(versions: readonly TlsVersion[]): string {
  return versions.join(" · ");
}
