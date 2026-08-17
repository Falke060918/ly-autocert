"use client";

/**
 * 스캔 현황 카드 — 정기 스캔 3종이 언제 다시 도는지를 한 줄씩 보여준다.
 *
 * 이 카드가 답하는 질문은 "지금 뭐가 돌고 있나"가 아니라 **"다음에 언제 확인하나,
 * 마지막으로 언제 확인했나"** 다. 인벤토리가 낡는 것은 스캔이 실패해서가 아니라
 * 아무도 다음 주기를 안 보기 때문이고, 그 자리를 대시보드가 맡는다.
 * 지금 도는 작업의 진행률은 Discovery 화면 몫이다.
 *
 * '지금 실행'은 예정된 다음 실행을 밀지 않는다(서버 규약) — 자주 눌러도 안전하다.
 *
 * 출처 3곳(정기 스캔 설정·스캔 Job 목록·재스캔 현황)을 allSettled로 각각 가져와서
 * **실패한 출처의 줄만 생략**한다. 셋 다 실패했을 때만 카드 전체를 비운다.
 */

import * as React from "react";
import Link from "next/link";
import { Play } from "lucide-react";

import { Button } from "@/components/ui/button";
import { Card } from "@/components/ui/card";
import { Skeleton } from "@/components/ui/skeleton";
import { relativeTime, useTicker } from "@/components/notification-bell";
import type { ScanJob } from "@/lib/api";
import { canManageInventory, useAuth } from "@/lib/auth";
import { getRescanStatus, type RescanStatus } from "@/lib/rescan";
import { listScanJobs } from "@/lib/scans";
import {
  SCHEDULE_KIND_LABEL,
  listScanSchedules,
  runScanSchedule,
  untilLabel,
  type ScanScheduleItem,
  type ScanScheduleListResponse,
} from "@/lib/schedules";

interface Sources {
  schedules: ScanScheduleListResponse | null;
  jobs: ScanJob[] | null;
  rescan: RescanStatus | null;
}

const ACTIVE_JOB = new Set(["pending", "leased", "running"]);

/** 지정 대역은 여러 개다 — 대시보드에는 임박한 것부터 이만큼만 싣는다. */
const MAX_PRESET_ROWS = 3;

export function ScanStatusCard() {
  const { user } = useAuth();
  const canManage = canManageInventory(user);
  const [sources, setSources] = React.useState<Sources | null>(null);
  const [reloadKey, setReloadKey] = React.useState(0);
  const [busyID, setBusyID] = React.useState<string | null>(null);
  const [error, setError] = React.useState<string | null>(null);
  const now = useTicker(60_000);

  React.useEffect(() => {
    let cancelled = false;
    void Promise.allSettled([
      listScanSchedules(),
      listScanJobs({ pageSize: 50 }),
      getRescanStatus(),
    ]).then(([schedules, jobs, rescan]) => {
      if (cancelled) return;
      setSources({
        schedules: schedules.status === "fulfilled" ? schedules.value : null,
        jobs: jobs.status === "fulfilled" ? jobs.value.items : null,
        rescan: rescan.status === "fulfilled" ? rescan.value : null,
      });
    });
    return () => {
      cancelled = true;
    };
  }, [reloadKey]);

  const runNow = React.useCallback(async (id: string) => {
    setBusyID(id);
    setError(null);
    try {
      await runScanSchedule(id);
      setReloadKey((k) => k + 1);
    } catch (err) {
      setError(
        err instanceof Error && err.message
          ? err.message
          : "지금 실행할 수 없습니다.",
      );
    } finally {
      setBusyID(null);
    }
  }, []);

  return (
    // 높이는 대시보드 이웃 섹션과 통일(lg:h-[30rem]) — 줄이 넘치면 목록만
    // 스크롤하고 하단 요약 줄은 카드 바닥에 남는다.
    <Card className="gap-0 overflow-hidden py-0 lg:h-[30rem]">
      <div className="flex shrink-0 items-center gap-2 border-b px-4 py-3">
        <h2 className="text-sm font-semibold tracking-tight">스캔 현황</h2>
        <Button asChild variant="ghost" size="sm" className="ml-auto">
          <Link href="/settings/scans">스캔 설정</Link>
        </Button>
        <Button asChild variant="outline" size="sm">
          <Link href="/discovery">Discovery</Link>
        </Button>
      </div>
      {sources ? (
        <CardBody
          sources={sources}
          canManage={canManage}
          busyID={busyID}
          error={error}
          now={now}
          onRun={(id) => void runNow(id)}
        />
      ) : (
        <CardSkeleton />
      )}
    </Card>
  );
}

function CardSkeleton() {
  return (
    <div className="space-y-3 p-4">
      {Array.from({ length: 4 }).map((_, i) => (
        <Skeleton key={i} className="h-8" />
      ))}
    </div>
  );
}

/** 다음 주기 한 줄 — 꺼져 있으면 예정이 없다는 사실 자체가 정보다. */
function nextLabel(item: ScanScheduleItem, now: number): string {
  if (item.active_run) return "실행 중";
  if (!item.enabled) return "꺼짐";
  if (!item.next_run_at) return "예정 없음";
  return untilLabel(item.next_run_at, now);
}

function ScheduleRow({
  item,
  canManage,
  wired,
  busy,
  now,
  onRun,
}: {
  item: ScanScheduleItem;
  canManage: boolean;
  wired: boolean;
  busy: boolean;
  now: number;
  onRun: (id: string) => void;
}) {
  const last = item.last_run;
  return (
    <div className="flex items-center gap-2 border-b py-2.5 text-[13px] last:border-b-0">
      <span className="min-w-0 truncate">
        {item.kind === "preset" ? item.name : SCHEDULE_KIND_LABEL[item.kind]}
      </span>
      <span className="ml-auto text-right">
        <span className="font-mono text-sm font-semibold tabular-nums">
          {nextLabel(item, now)}
        </span>
        <span className="block text-[11px] text-muted-foreground">
          {last ? `${relativeTime(last.started_at, now)} 확인` : "확인 이력 없음"}
        </span>
      </span>
      {canManage && (
        <Button
          variant="ghost"
          size="icon"
          aria-label={`${item.name} 지금 실행`}
          title={
            wired ? "지금 실행 (예정된 다음 실행은 그대로)" : "이 서버는 정기 스캔을 돌리지 않습니다."
          }
          disabled={busy || !wired || item.active_run != null}
          onClick={() => onRun(item.id)}
        >
          <Play />
        </Button>
      )}
    </div>
  );
}

function CardBody({
  sources,
  canManage,
  busyID,
  error,
  now,
  onRun,
}: {
  sources: Sources;
  canManage: boolean;
  busyID: string | null;
  error: string | null;
  now: number;
  onRun: (id: string) => void;
}) {
  const { schedules, jobs, rescan } = sources;

  if (!schedules && !jobs && !rescan) {
    return (
      <div className="flex flex-1 flex-col justify-center px-4 py-10 text-center">
        <p className="text-sm font-medium">스캔 현황을 불러오지 못했습니다</p>
        <p className="mt-1 text-xs text-muted-foreground">
          Discovery 화면에서 다시 확인하십시오.
        </p>
      </div>
    );
  }

  // 전체 → 지정 대역(임박한 순) → 등록된 인증서. 꺼진 지정 대역은 뒤로 민다.
  const items = schedules?.items ?? [];
  const presets = items
    .filter((it) => it.kind === "preset")
    .sort((a, b) => {
      const at = a.enabled && a.next_run_at ? Date.parse(a.next_run_at) : Infinity;
      const bt = b.enabled && b.next_run_at ? Date.parse(b.next_run_at) : Infinity;
      return at - bt;
    });
  const rows = [
    ...items.filter((it) => it.kind === "full"),
    ...presets.slice(0, MAX_PRESET_ROWS),
    ...items.filter((it) => it.kind === "registered"),
  ];
  const hiddenPresets = Math.max(0, presets.length - MAX_PRESET_ROWS);

  const activeJobs = jobs?.filter((j) => ACTIVE_JOB.has(j.status)).length ?? null;

  return (
    <>
      {/* flex-1 — 남는 세로를 이 목록이 갖고, 넘치면 여기서만 스크롤한다. */}
      <div className="min-h-0 flex-1 overflow-y-auto px-4 py-1">
        {rows.map((item) => (
          <ScheduleRow
            key={item.id}
            item={item}
            canManage={canManage}
            wired={schedules?.wired ?? false}
            busy={busyID === item.id}
            now={now}
            onRun={onRun}
          />
        ))}
        {schedules && rows.length === 0 && (
          <p className="py-6 text-center text-[13px] text-muted-foreground">
            정기 스캔 설정이 없습니다.
          </p>
        )}
      </div>

      {(activeJobs != null || rescan || hiddenPresets > 0) && (
        <div className="flex flex-wrap items-center gap-x-3 gap-y-1 border-t px-4 py-2 text-[11px] text-muted-foreground">
          {activeJobs != null && <span>실행 중 스캔 {activeJobs}건</span>}
          {rescan && (
            <span className={rescan.due_endpoints > 0 ? "text-warn" : undefined}>
              재스캔 적체 {rescan.due_endpoints.toLocaleString()}건
            </span>
          )}
          {hiddenPresets > 0 && (
            <Link href="/settings/scans" className="underline underline-offset-2">
              지정 대역 {hiddenPresets}건 더 보기
            </Link>
          )}
        </div>
      )}

      {error && (
        <div className="border-t px-4 py-2 text-[11px] text-destructive">{error}</div>
      )}
    </>
  );
}
