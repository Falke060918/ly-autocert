/**
 * 통합 작업 목록(GET /discovery/activity) API 클라이언트.
 *
 * 단발 스캔·전 포트 스윕·정기 스캔 실행을 한 목록으로 합친 것이 서버 응답이다.
 * 화면이 세 API를 따로 폴링해 머릿속에서 합치던 것을 서버 한 곳으로 옮겼다.
 *
 * **취소 가능 여부는 판정하지 않는다** — 서버가 `cancel`로 내려준다. 클라이언트가
 * 상태 문자열로 추론하면 상태 머신이 바뀔 때 조용히 어긋난다. 취소 **경로**만
 * 축(kind)별로 다르고, 그 분기는 여기 `cancelActivity` 한 곳에 있다.
 */

import { apiFetch } from "@/lib/api";
import { cancelScan } from "@/lib/scans";
import { transitionSweep } from "@/lib/sweeps";

/** 작업 축 3종. 취소 API 경로가 축마다 다르므로 화면은 이 값으로 분기한다. */
export type ActivityKind = "scan" | "sweep" | "schedule_run";

/**
 * 통합 목록의 1행. 축마다 다른 필드는 null로 빈다.
 * 상태 문자열은 축마다 집합이 다르다(스캔 pending/leased/…, 스윕 queued/paused/…,
 * 정기 실행 running/…) — 라벨 맵이 셋을 모두 덮어야 한다.
 */
export interface ActivityItem {
  kind: ActivityKind;
  id: string;
  status: string;
  /** 아직 끝나지 않았다(축별 종료 상태의 여집합). */
  active: boolean;
  /** 지금 취소할 수 있는지의 **서버 판정**. */
  cancel: boolean;
  /** 스윕 이름 또는 정기 스캔 이름. 단발 스캔은 null. */
  name: string | null;
  /** 스캔의 발주 출처(manual) 또는 실행의 발동 주체(schedule|manual). */
  origin: string | null;
  /** "server" 또는 "sensor:<uuid>". */
  executor_scope: string;
  targets: string[];
  /** 입력 원문. 단발 스캔만 전개된 포트 목록("443,8443")이다. */
  port_spec: string;
  total_endpoints: number;
  scanned_endpoints: number;
  reachable_endpoints: number;
  certificates_found: number;
  certificates_new: number;
  error: string | null;
  schedule_id: string | null;
  sweep_id: string | null;
  started_at: string;
  finished_at: string | null;
}

export interface ActivityListResponse {
  request_id: string;
  items: ActivityItem[];
  total: number;
  page: number;
  page_size: number;
}

/** 통합 목록 조회 — 최신순 페이지네이션 + 축·진행 여부 필터. */
export function listDiscoveryActivity(opts?: {
  page?: number;
  pageSize?: number;
  kinds?: ActivityKind[];
  /** true면 진행 중인 작업만. 생략하면 전체다. */
  active?: boolean;
}) {
  const params = new URLSearchParams();
  if (opts?.page) params.set("page", String(opts.page));
  if (opts?.pageSize) params.set("page_size", String(opts.pageSize));
  if (opts?.kinds?.length) params.set("kind", opts.kinds.join(","));
  if (opts?.active != null) params.set("active", String(opts.active));
  const qs = params.toString();
  return apiFetch<ActivityListResponse>(
    `/api/v1/discovery/activity${qs ? `?${qs}` : ""}`,
  );
}

/**
 * 정기 스캔 실행 취소. 서버가 발주된 스윕을 먼저 멈추고 실행 이력을 닫는다.
 * 이미 종료된 실행은 409(schedule_run_state)이고, 스케줄 러너가 배선되지 않은
 * 서버는 503(schedule_unavailable)이다.
 *
 * 응답 본문(실행 행)은 쓰지 않는다 — 목록을 다시 읽는 편이 세 축 모두에 대해
 * 같은 경로가 된다.
 */
export function cancelScanScheduleRun(id: string) {
  return apiFetch<void>(
    `/api/v1/discovery/schedules/runs/${encodeURIComponent(id)}/cancel`,
    { method: "POST" },
  );
}

/**
 * 통합 목록 1행의 취소 — 축별 취소 API로 보낸다.
 * 취소해도 되는지의 판정은 하지 않는다(`item.cancel`이 서버 판정이다).
 */
export async function cancelActivity(item: ActivityItem): Promise<void> {
  switch (item.kind) {
    case "scan":
      await cancelScan(item.id);
      return;
    case "sweep":
      await transitionSweep(item.id, "cancel");
      return;
    case "schedule_run":
      await cancelScanScheduleRun(item.id);
      return;
  }
}
