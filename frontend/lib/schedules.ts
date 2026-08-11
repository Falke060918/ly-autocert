/**
 * 정기 스캔 스케줄(0019) API 클라이언트.
 *
 * 종류가 규칙을 가른다 — full·registered는 서버가 시드한 **1건이 정본**이라
 * 만들거나 지울 수 없고 설정만 바꾸며(kind로 지목), preset은 여러 개를 등록하고
 * 지울 수 있다(id로 지목).
 *
 * 대상·포트 문법은 여기서 검증하지 않는다. 파서는 서버 한 곳이고 화면은 400
 * 메시지를 그대로 보여준다 — 흉내 낸 파서는 반드시 서버와 갈라진다.
 */

import { ApiError, apiFetch } from "@/lib/api";

// ── 타입 ──────────────────────────────────────────────────────────────────

export type ScanScheduleKind = "full" | "preset" | "registered";

/** 실행 상태 머신. 스캔 Job·스윕 상태와 값이 겹치지 않는 별개 union이다. */
export type ScanScheduleRunStatus =
  | "running"
  | "completed"
  | "failed"
  | "canceled";

/** schedule=주기 도래 / manual=사용자의 '지금 실행'. */
export type ScanScheduleTrigger = "schedule" | "manual";

export interface ScanSchedule {
  id: string;
  kind: ScanScheduleKind;
  name: string;
  enabled: boolean;
  /** IP/CIDR 입력 원문. registered는 항상 빈 배열이다. */
  targets: string[];
  /** 포트 사양 원문. registered는 항상 null이다. */
  port_spec: string | null;
  /** null이면 서버 기본 주기(registered는 RESCAN_INTERVAL). */
  interval_seconds: number | null;
  /** null이면 서버 기본 레이트. */
  rate_pps: number | null;
  executor_scope: string;
  /** 다음 예정 시각. 꺼져 있으면 null이다. '지금 실행'은 이 값을 바꾸지 않는다. */
  next_run_at: string | null;
  last_run_started_at: string | null;
  last_run_id: string | null;
  updated_by?: string | null;
  created_at: string;
  updated_at: string;
}

export interface ScanScheduleRun {
  id: string;
  schedule_id: string;
  trigger: ScanScheduleTrigger;
  status: ScanScheduleRunStatus;
  /** full·preset이 발주한 스윕. registered 실행은 항상 null이다. */
  sweep_id: string | null;
  dispatched_jobs: number;
  dispatched_endpoints: number;
  started_at: string;
  finished_at: string | null;
  error: string | null;
}

/** 설정 1건 + 실행 문맥. active_run이 있으면 '지금 실행'은 409다. */
export interface ScanScheduleItem extends ScanSchedule {
  active_run: ScanScheduleRun | null;
  last_run: ScanScheduleRun | null;
}

export interface ScanScheduleListResponse {
  request_id: string;
  items: ScanScheduleItem[];
  /** false면 스케줄러 미배선 — 조회·설정 변경은 되고 실행·취소만 503이다. */
  wired: boolean;
}

/**
 * 부분 수정 요청. **보내지 않은 키는 바뀌지 않는다**(undefined는 JSON에서 빠진다).
 * interval_seconds·rate_pps에 null을 명시하면 서버 기본값으로 되돌린다 —
 * "키 없음"과 "값 null"은 서버에서 다른 뜻이다.
 */
export interface ScanScheduleUpdateRequest {
  /** preset은 id로만 지목한다. */
  id?: string;
  /** 시드 1행(full·registered)은 id를 몰라도 kind로 집는다. */
  kind?: "full" | "registered";
  name?: string;
  enabled?: boolean;
  targets?: string[];
  port_spec?: string;
  interval_seconds?: number | null;
  rate_pps?: number | null;
  executor_scope?: string;
}

/** preset 생성 요청. 보내지 않은 주기·속도는 서버 기본값이다. */
export interface ScanScheduleCreateRequest {
  name: string;
  enabled?: boolean;
  targets?: string[];
  port_spec?: string;
  interval_seconds?: number;
  rate_pps?: number;
  executor_scope?: string;
}

// ── 엔드포인트 ────────────────────────────────────────────────────────────

export function listScanSchedules() {
  return apiFetch<ScanScheduleListResponse>("/api/v1/discovery/schedules");
}

export function updateScanSchedule(req: ScanScheduleUpdateRequest) {
  return apiFetch<ScanSchedule>("/api/v1/discovery/schedules", {
    method: "PATCH",
    body: JSON.stringify(req),
  });
}

/** preset만 만들 수 있다 — 서버가 kind를 preset으로 고정한다. */
export function createScanSchedule(req: ScanScheduleCreateRequest) {
  return apiFetch<ScanSchedule>("/api/v1/discovery/schedules", {
    method: "POST",
    body: JSON.stringify(req),
  });
}

/** preset만 지울 수 있다. full·registered는 409(schedule_kind)다. */
export function deleteScanSchedule(id: string) {
  return apiFetch<void>(
    `/api/v1/discovery/schedules/${encodeURIComponent(id)}`,
    { method: "DELETE" },
  );
}

/** '지금 실행'. 예정된 다음 실행은 그대로다 — 자주 눌러도 일정이 밀리지 않는다. */
export function runScanSchedule(id: string) {
  return apiFetch<ScanScheduleRun>(
    `/api/v1/discovery/schedules/${encodeURIComponent(id)}/run`,
    { method: "POST" },
  );
}

export function cancelScanScheduleRun(runID: string) {
  return apiFetch<ScanScheduleRun>(
    `/api/v1/discovery/schedules/runs/${encodeURIComponent(runID)}/cancel`,
    { method: "POST" },
  );
}

// ── 표시 헬퍼 ─────────────────────────────────────────────────────────────

/** full·preset 주기의 서버 하한(1시간). 화면 안내 문구가 이 값을 쓴다. */
export const MIN_SCHEDULE_INTERVAL_SECONDS = 3600;

export const SCHEDULE_KIND_LABEL: Record<ScanScheduleKind, string> = {
  full: "전체 대역 훑기",
  preset: "지정 대역 정기 스캔",
  registered: "등록된 인증서 재확인",
};

export const SCHEDULE_KIND_HINT: Record<ScanScheduleKind, string> = {
  full: "지정한 대역의 모든 포트를 훑어 아직 모르는 인증서를 찾습니다. 오래 걸리므로 기본은 꺼짐입니다.",
  preset: "자주 보는 대역과 포트만 골라 주기적으로 다시 찾습니다. 여러 개를 등록할 수 있습니다.",
  registered:
    "이미 아는 엔드포인트를 다시 확인해 최근 확인 시각을 유지하고 인증서 교체를 감지합니다.",
};

const RUN_STATUS_LABEL: Record<ScanScheduleRunStatus, string> = {
  running: "실행 중",
  completed: "완료",
  failed: "실패",
  canceled: "취소됨",
};

export function runStatusLabel(status: ScanScheduleRunStatus): string {
  return RUN_STATUS_LABEL[status] ?? status;
}

/**
 * "3시간 뒤" / "곧" / "지났음" — 예정 시각까지 남은 시간.
 * now는 호출자가 넘긴다(렌더 순수성 — useTicker 사용).
 */
export function untilLabel(iso: string, now: number): string {
  const diff = new Date(iso).getTime() - now;
  if (!Number.isFinite(diff)) return "—";
  if (diff <= 0) return "곧";
  const minutes = Math.floor(diff / 60_000);
  if (minutes < 1) return "곧";
  if (minutes < 60) return `${minutes}분 뒤`;
  const hours = Math.floor(minutes / 60);
  if (hours < 24) return `${hours}시간 뒤`;
  return `${Math.floor(hours / 24)}일 뒤`;
}

/**
 * 이미 도는 실행이 있을 때(409 schedule_run_active) 그 실행 id.
 * 다른 오류면 null — 그 오류는 평소대로 처리한다.
 */
export function activeScheduleRunId(err: unknown): string | null {
  if (err instanceof ApiError && err.code === "schedule_run_active") {
    const runID = err.details?.run_id;
    if (typeof runID === "string") return runID;
  }
  return null;
}
