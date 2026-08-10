/**
 * 디스커버리 스캔 Job 목록·취소·대역 제안 API 클라이언트 (U-F0 계약 모듈).
 *
 * 스캔 **생성** 계약은 불변이다 — 단발 생성은 `lib/api.ts`(createScan)와
 * `lib/sweeps.ts`(createScanWithPortSpec)에 그대로 있다. 이 모듈은 그 뒤의
 * 관측(목록)·제어(취소)와 입력 보조(대역 제안)만 다룬다.
 */

import { apiFetch, ApiError, type ScanJob } from "@/lib/api";

// ── 스캔 Job 목록 ─────────────────────────────────────────────────────────

export interface ScanJobListResponse {
  request_id: string;
  items: ScanJob[];
  total: number;
  page: number;
  page_size: number;
}

/** 스캔 Job 목록 — 최신순 페이지네이션. 빈 옵션 값은 쿼리에 붙이지 않는다. */
export function listScanJobs(opts?: { page?: number; pageSize?: number }) {
  const params = new URLSearchParams();
  if (opts?.page) params.set("page", String(opts.page));
  if (opts?.pageSize) params.set("page_size", String(opts.pageSize));
  const qs = params.toString();
  return apiFetch<ScanJobListResponse>(
    `/api/v1/discovery/scans${qs ? `?${qs}` : ""}`,
  );
}

// ── 스캔 취소 ─────────────────────────────────────────────────────────────

/**
 * 스캔 취소. pending/leased/running에서만 유효하고 이미 종료된 Job은 409다.
 * 응답은 status가 "canceled"로 바뀐 Job 전체다.
 */
export function cancelScan(id: string) {
  return apiFetch<ScanJob>(
    `/api/v1/discovery/scans/${encodeURIComponent(id)}/cancel`,
    { method: "POST" },
  );
}

// ── 중복 스캔 409 (duplicate_scan) ────────────────────────────────────────

export const DUPLICATE_SCAN_CODE = "duplicate_scan";

/**
 * 동일 대상·포트 스캔이 이미 대기/실행 중일 때 스캔 생성이 돌려주는 409 본문.
 * job_id는 통일 오류 객체(00-spec 13장)의 부가 필드로 실린다.
 */
export interface DuplicateScanErrorBody {
  request_id?: string;
  error: {
    code: typeof DUPLICATE_SCAN_CODE;
    message: string;
    /** 이미 대기/실행 중인 기존 스캔 Job id — 화면은 이 Job으로 안내한다. */
    job_id: string;
  };
}

/**
 * 스캔 생성이 던진 오류에서 중복 스캔의 기존 job_id를 꺼낸다.
 * 중복 스캔 409가 아니면 null — 그 오류는 평소대로 처리한다.
 */
export function duplicateScanJobId(err: unknown): string | null {
  if (err instanceof ApiError && err.code === DUPLICATE_SCAN_CODE) {
    const jobId = err.details?.job_id;
    if (typeof jobId === "string") return jobId;
  }
  return null;
}

// ── 스캔 대역 제안 ────────────────────────────────────────────────────────

/**
 * 대상 입력 보조용 CIDR 제안 3종. 전부 CIDR 문자열 배열이며 비어 있을 수 있다.
 * **제안일 뿐 검증이 아니다** — 실제 전송값은 언제나 사용자가 고른/친 원문이다.
 */
export interface ScanSuggestionsResponse {
  request_id?: string;
  /** 최근 스캔에 실제 사용된 대상 대역 (최신순, 중복 제거). */
  recent_targets: string[];
  /** 알려진 호스트(타깃 인벤토리) IP에서 파생한 /24 대역. */
  known_host_networks: string[];
  /** 설치 시 감지된 서버 NIC 대역. */
  installed_networks: string[];
}

export function getScanSuggestions() {
  return apiFetch<ScanSuggestionsResponse>(
    "/api/v1/discovery/scan-suggestions",
  );
}
