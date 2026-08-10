/**
 * 주기 재스캔(06-rescan-and-replacement) API 클라이언트.
 *
 * 재스캔은 **이미 아는 엔드포인트를 다시 확인**하는 기능이고, 신규 대역 발견은
 * 수동 스캔·전 포트 스윕의 몫이다. 그래서 이 모듈에는 발주 대상(대역·포트) 입력이
 * 아예 없다 — 현황 조회와 "지금 1배치" 두 개뿐이다.
 *
 * 정책 편집 화면은 만들지 않는다(설계 11.5). 주기·임계값은 서버 환경변수이며,
 * 화면에서 고치게 만들면 정책 테이블·CRUD·감사·권한 게이트가 줄줄이 따라온다.
 */

import { apiFetch } from "@/lib/api";

/** GET /discovery/rescan/status 응답. 모든 수치는 서버가 매번 파생 집계한다. */
export interface RescanStatus {
  request_id: string;
  /** 지금 자동으로 도는가. 배선이 없거나 설정으로 껐으면 false다. */
  enabled: boolean;
  /**
   * 스케줄러 배선 자체의 유무. enabled=false의 두 원인(배선 없음 / 설정으로 끔)을
   * 화면이 구분해야 하므로 별도 필드다 — 전자는 서버 구성 문제, 후자는 의도된 상태다.
   */
  wired: boolean;
  /** 전역 재확인 주기(초). 만료 임박 가속 티어는 서버가 내부에서 적용한다. */
  interval_seconds: number;
  /** enabled=false의 사유. 변수 이름과 상태만 담고 설정값은 담지 않는다. */
  reason?: string;
  monitored_endpoints: number;
  retired_endpoints: number;
  /** 주기가 지났는데 아직 발주되지 않은 수 = 적체. 0이 정상이다. */
  due_endpoints: number;
  /** origin=rescan 스캔 Job이 마지막으로 발주된 시각. 한 번도 없으면 null. */
  last_run_at: string | null;
}

/** POST /discovery/rescan/run 응답(202). 0이면 지금 재확인할 것이 없다는 뜻이다. */
export interface RescanRunResult {
  request_id: string;
  dispatched_jobs: number;
  dispatched_endpoints: number;
}

export function getRescanStatus() {
  return apiFetch<RescanStatus>("/api/v1/discovery/rescan/status");
}

/**
 * due 판정을 무시하고 즉시 1배치를 발주한다.
 * 스캔 발주와 같은 3역할만 호출할 수 있고, 서버가 발주된 Job마다 감사를 남긴다.
 */
export function runRescanBatch() {
  return apiFetch<RescanRunResult>("/api/v1/discovery/rescan/run", {
    method: "POST",
  });
}

/** "12시간" / "30분" / "2일" — 간격 표시용(초 단위 입력). */
export function formatInterval(seconds: number): string {
  if (!Number.isFinite(seconds) || seconds <= 0) return "—";
  if (seconds % 86_400 === 0) return `${seconds / 86_400}일`;
  if (seconds % 3_600 === 0) return `${seconds / 3_600}시간`;
  if (seconds % 60 === 0) return `${seconds / 60}분`;
  return `${seconds}초`;
}
