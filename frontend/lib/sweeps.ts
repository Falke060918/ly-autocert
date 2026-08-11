/**
 * 전 포트 백그라운드 스윕(04-port-autoscan) API 클라이언트와 표시 헬퍼.
 *
 * 스캔 Job(`lib/api.ts`의 ScanJob)과 **별도 타입**이다 — 스윕 상태 enum은
 * Job 상태 union과 값이 겹치지 않으며, 한쪽을 넓히면 다른 쪽 폴링이 깨진다.
 */

import { apiFetch, type ScanAccepted, type ScanMode } from "@/lib/api";

// ── 타입 ──────────────────────────────────────────────────────────────────

/** 스윕 상태 머신(설계 4.2). Job 상태(pending/leased/running/…)와 별개다. */
export type SweepStatus =
  | "queued"
  | "running"
  | "paused"
  | "completed"
  | "failed"
  | "canceled";

/** 자식 Job 집계에서 파생한 진행률 — 부모 행에는 카운터가 없다(설계 6.1). */
export interface SweepProgress {
  chunks_total: number;
  chunks_completed: number;
  chunks_active: number;
  failed_attempts: number;
  scanned_endpoints: number;
  reachable_endpoints: number;
  certificates_found: number;
  certificates_new: number;
}

export interface Sweep {
  id: string;
  name?: string | null;
  status: SweepStatus;
  targets: string[];
  /** 입력 원문 그대로("all", "common,8000-9000" 등). */
  port_spec: string;
  /** "server" 또는 "sensor:<uuid>". */
  executor_scope: string;
  total_endpoints: number;
  total_chunks: number;
  next_seq: number;
  rate_pps: number;
  chunk_endpoints: number;
  estimated_finish_at: string | null;
  /**
   * 센서 스코프 스윕이 청크를 집어가지 않고 대기하기 시작한 시각.
   * **실패가 아니다** — 종료 상태가 아니라 "센서 대기 중" 표시용이다(설계 6.4).
   */
  stalled_since: string | null;
  last_error: string | null;
  created_by?: string | null;
  created_at: string;
  updated_at: string;
  started_at: string | null;
  finished_at: string | null;
  progress: SweepProgress;
}

export interface SweepRequest {
  name?: string;
  targets: string[];
  /** 비우면 서버가 all(1-65535)로 본다 — 스윕의 기본 용도다. */
  port_spec?: string;
  sensor_id?: string;
  rate_pps?: number;
  chunk_endpoints?: number;
}

export interface SweepAccepted {
  sweep_id: string;
  status: SweepStatus;
  total_endpoints: number;
  total_chunks: number;
  estimated_finish_at: string | null;
  request_id?: string;
}

export interface SweepListResponse {
  request_id: string;
  items: Sweep[];
  total: number;
  page: number;
  page_size: number;
}

/** 포트 프리셋 1개. all은 목록이 6.5만 개라 ports가 생략되고 count만 온다. */
export interface PortPreset {
  name: string;
  description: string;
  count: number;
  ports?: number[];
}

export interface PortPresetListResponse {
  request_id?: string;
  /** 포트 미지정 시 서버가 적용하는 기본 사양(common). */
  default?: string;
  items: PortPreset[];
}

// ── 엔드포인트 ────────────────────────────────────────────────────────────

/**
 * 프리셋 목록. **단일 진실원천은 서버**이므로 프론트는 이름·개수를 하드코딩하지
 * 않고 이 응답만 쓴다(설계 3.1).
 */
export function listPortPresets() {
  return apiFetch<PortPresetListResponse>("/api/v1/discovery/port-presets");
}

/** 스캔 발주·미리보기가 공유하는 요청 본문(서버도 같은 구조체로 받는다). */
export interface ScanFormRequest {
  targets: string[];
  port_spec?: string;
  timeout_ms?: number;
  concurrency?: number;
  sensor_id?: string;
}

/**
 * 스캔 생성 — `port_spec` 자유 텍스트 버전.
 * 포트 문법 검증은 전적으로 서버 파서(400 메시지)에 맡긴다.
 *
 * 규모가 크면 서버가 스윕으로 **자동 승격**해 job_id 대신 sweep_id를 돌려준다
 * (ScanAccepted.mode 참고) — 호출자는 둘 중 어느 쪽이 왔는지 반드시 확인한다.
 */
export function createScanWithPortSpec(req: ScanFormRequest) {
  return apiFetch<ScanAccepted>("/api/v1/discovery/scans", {
    method: "POST",
    body: JSON.stringify(req),
  });
}

/**
 * 발주 전 규모 미리보기 — 아무것도 만들지 않는다.
 *
 * 대상·포트 파서는 **서버 한 곳**이 정본이다. 예전에는 프론트가 CIDR 호스트 수와
 * 포트 사양을 따로 세어 미리 보여줬는데, 서버 파서와 조용히 갈라지면 화면이 보여준
 * 숫자와 실제로 도는 작업이 달라진다(설계 9.3). 그래서 계산은 전부 여기로 넘긴다.
 *
 * GET이 아니라 POST인 이유는 대상 목록이 URL 길이 상한을 넘기 때문이다.
 */
export function previewScan(req: ScanFormRequest) {
  return apiFetch<ScanPreview>("/api/v1/discovery/scans/preview", {
    method: "POST",
    body: JSON.stringify(req),
  });
}

/** POST /discovery/scans/preview 응답. 포트 목록 자체는 실리지 않는다(all이면 6.5만 개). */
export interface ScanPreview {
  request_id?: string;
  /** 이대로 발주하면 만들어질 작업 — scan(단발) 또는 sweep(자동 승격). */
  mode: ScanMode;
  promoted: boolean;
  /** 이 엔드포인트 수를 **넘을 때만** 승격한다. */
  promotion_threshold: number;
  targets: string[];
  ip_count: number;
  port_spec: string;
  port_count: number;
  total_endpoints: number;
  rate_pps: number;
  estimated_seconds: number;
  estimated_finish_at: string;
  /** 승격 경로에서만 0이 아니다. */
  total_chunks: number;
  max_days: number;
  /** true면 이대로 발주해도 422(sweep_too_long)로 거부된다 — 미리 알려만 준다. */
  too_long: boolean;
  message?: string;
}

export function createSweep(req: SweepRequest) {
  return apiFetch<SweepAccepted>("/api/v1/discovery/sweeps", {
    method: "POST",
    body: JSON.stringify(req),
  });
}

export function listSweeps() {
  return apiFetch<SweepListResponse>("/api/v1/discovery/sweeps");
}

export function getSweep(id: string) {
  return apiFetch<Sweep>(`/api/v1/discovery/sweeps/${encodeURIComponent(id)}`);
}

/** 상태 전이 3종. 전부 발주자 제어이며 진행 중 청크는 완주한다(설계 6.2). */
export type SweepTransition = "pause" | "resume" | "cancel";

export function transitionSweep(id: string, action: SweepTransition) {
  return apiFetch<Sweep>(
    `/api/v1/discovery/sweeps/${encodeURIComponent(id)}/${action}`,
    { method: "POST" },
  );
}

// ── 서버 기본값 (SCAN_SWEEP_*) ────────────────────────────────────────────
//
// **상수로 들고 있지 않는다.** 환경변수를 바꾼 현장에서 "서버 기본값(10)"이라
// 적힌 칸에 실제로는 40이 적용되면, 그 어긋남은 화면 어디에도 뜨지 않는다.
// 값은 언제나 GET /discovery/defaults의 유효값을 쓰고, 못 받으면 숫자를 지어내지
// 않고 비운다(미리보기는 "—", placeholder는 숫자 없는 문구).

export interface DiscoveryDefaults {
  request_id?: string;
  /** 스윕 생성 폼의 기본값 — 환경변수(SCAN_SWEEP_*)가 반영된 유효값이다. */
  sweep: {
    rate_pps: number;
    chunk_endpoints: number;
    /** 예상 소요가 이 일수를 넘으면 서버가 422로 거부한다. */
    max_days: number;
  };
  /** 포트 미지정 시 서버가 적용하는 사양(common). */
  scan: { port_spec: string };
  /** 요청이 넘을 수 없는 상한. */
  limits: { max_rate_pps: number; max_endpoints: number };
}

export function getDiscoveryDefaults() {
  return apiFetch<DiscoveryDefaults>("/api/v1/discovery/defaults");
}

/**
 * STARTTLS로 승격하는 포트들. 스캐너는 접속 즉시 ClientHello를 보내는
 * **implicit TLS 전용**이라 이 포트에서는 인증서를 원리상 얻을 수 없다.
 * 프리셋에서도 의도적으로 빠져 있고, all을 골라도 결과가 나오지 않는다.
 */
export const STARTTLS_EXCLUDED_PORTS = [
  25, 587, 110, 143, 389, 5432, 3306, 1433, 21, 3389,
];

export const STARTTLS_NOTICE = `전 포트를 돌려도 STARTTLS 승격 포트(${STARTTLS_EXCLUDED_PORTS.join(
  ", ",
)})에서는 인증서를 얻을 수 없습니다 — 스캐너가 implicit TLS 전용입니다.`;

// ── 표시 헬퍼 ─────────────────────────────────────────────────────────────
//
// 규모·소요 **계산은 여기 없다** — 서버의 POST /discovery/scans/preview가 정본이다.
// 예전에는 CIDR 호스트 수·포트 사양 개수를 프론트가 따로 세어 미리보기를 그렸는데,
// 파서를 둘 두면 반드시 갈라진다. 아래는 서버가 준 숫자를 사람이 읽는 표기로
// 바꾸는 것뿐이다.

/** 서버 메시지와 같은 표기 규칙 — 10일 미만은 소수 1자리, 그 이상은 정수. */
function roundDays(days: number): string {
  return days < 10 ? String(Math.round(days * 10) / 10) : String(Math.round(days));
}

/** "약 3.2일" / "약 5시간" / "약 12분" — 미리보기용 소요 표기. */
export function formatDuration(seconds: number): string {
  if (seconds < 60) return "1분 미만";
  if (seconds < 3600) return `약 ${Math.round(seconds / 60)}분`;
  if (seconds < 86400) return `약 ${Math.round(seconds / 3600)}시간`;
  return `약 ${roundDays(seconds / 86400)}일`;
}

/** 큰 수의 자리 구분 — 엔드포인트 수는 수천만까지 간다. */
export function formatCount(n: number): string {
  return n.toLocaleString("ko-KR");
}

/** "3분째" / "12초째" — 대기가 얼마나 길어졌는지 알려주는 경과 표기. */
export function elapsedLabel(fromISO: string, now: number): string {
  const sec = Math.max(0, Math.floor((now - new Date(fromISO).getTime()) / 1000));
  if (sec < 60) return `${sec}초째`;
  const min = Math.floor(sec / 60);
  if (min < 60) return `${min}분째`;
  return `${Math.floor(min / 60)}시간 ${min % 60}분째`;
}

/** 포트 사양에 특정 프리셋 토큰이 들어 있는지(서버 hasAllPreset과 같은 판정). */
export function specHasPreset(spec: string, preset: string): boolean {
  return spec
    .split(",")
    .some((token) => token.trim().toLowerCase() === preset);
}

/** "sensor:<uuid>" 스코프에서 센서 id를 뽑는다. 서버 스코프면 null. */
export function sweepSensorID(scope: string): string | null {
  return scope.startsWith("sensor:") ? scope.slice("sensor:".length) : null;
}
