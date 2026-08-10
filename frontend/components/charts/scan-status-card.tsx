"use client";

/**
 * 스캔 현황 카드 — 실행 중 Job·최근 완료·신규 발견·재스캔 적체를 한눈에.
 *
 * 출처 3곳(스캔 Job 목록·스윕 목록·재스캔 상태)을 allSettled로 각각 가져와서,
 * **실패한 출처의 줄만 생략**한다 — 스캔 목록 API(lib/scans.ts)는 백엔드 병행
 * 구현 중이라 404가 정상 경로다. 셋 다 실패했을 때만 카드 전체를 비운다.
 */

import * as React from "react";
import Link from "next/link";

import { Button } from "@/components/ui/button";
import { Card } from "@/components/ui/card";
import { Skeleton } from "@/components/ui/skeleton";
import type { ScanJob } from "@/lib/api";
import { formatDateTime } from "@/lib/format";
import { getRescanStatus, formatInterval, type RescanStatus } from "@/lib/rescan";
import { listScanJobs } from "@/lib/scans";
import { listSweeps, type Sweep } from "@/lib/sweeps";
import { cn } from "@/lib/utils";

interface Sources {
  jobs: ScanJob[] | null;
  sweeps: Sweep[] | null;
  rescan: RescanStatus | null;
}

const ACTIVE_JOB = new Set(["pending", "leased", "running"]);
const ACTIVE_SWEEP = new Set(["queued", "running"]);

export function ScanStatusCard() {
  const [sources, setSources] = React.useState<Sources | null>(null);

  React.useEffect(() => {
    let cancelled = false;
    void Promise.allSettled([
      listScanJobs({ pageSize: 50 }),
      listSweeps(),
      getRescanStatus(),
    ]).then(([jobs, sweeps, rescan]) => {
      if (cancelled) return;
      setSources({
        jobs: jobs.status === "fulfilled" ? jobs.value.items : null,
        sweeps: sweeps.status === "fulfilled" ? sweeps.value.items : null,
        rescan: rescan.status === "fulfilled" ? rescan.value : null,
      });
    });
    return () => {
      cancelled = true;
    };
  }, []);

  return (
    <Card className="gap-0 overflow-hidden py-0">
      <div className="flex items-center gap-2 border-b px-4 py-3">
        <h2 className="text-sm font-semibold tracking-tight">스캔 현황</h2>
        <Button asChild variant="outline" size="sm" className="ml-auto">
          <Link href="/discovery">Discovery</Link>
        </Button>
      </div>
      {sources ? <CardBody sources={sources} /> : <CardSkeleton />}
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

function StatRow({
  label,
  value,
  sub,
  tone,
}: {
  label: string;
  value: string;
  sub?: string;
  tone?: "danger" | "warn";
}) {
  return (
    <div className="flex items-baseline gap-2 border-b py-2.5 text-[13px] last:border-b-0">
      <span className="text-muted-foreground">{label}</span>
      <span className="ml-auto text-right">
        <span
          className={cn(
            "font-mono text-sm font-semibold tabular-nums",
            tone === "danger" && "text-red-600",
            tone === "warn" && "text-amber-700",
          )}
        >
          {value}
        </span>
        {sub && (
          <span className="block text-[11px] text-muted-foreground">{sub}</span>
        )}
      </span>
    </div>
  );
}

function CardBody({ sources }: { sources: Sources }) {
  const { jobs, sweeps, rescan } = sources;

  if (!jobs && !sweeps && !rescan) {
    return (
      <div className="px-4 py-10 text-center">
        <p className="text-sm font-medium">스캔 현황을 불러오지 못했습니다</p>
        <p className="mt-1 text-xs text-muted-foreground">
          Discovery 화면에서 다시 확인하십시오.
        </p>
      </div>
    );
  }

  const rows: React.ReactNode[] = [];

  if (jobs) {
    const running = jobs.filter((j) => ACTIVE_JOB.has(j.status)).length;
    rows.push(
      <StatRow
        key="running"
        label="실행 중 스캔"
        value={`${running}건`}
        sub={running > 0 ? "대기·수령 포함" : undefined}
      />,
    );

    const lastDone = jobs.find((j) => j.status === "completed");
    if (lastDone) {
      rows.push(
        <StatRow
          key="last-done"
          label="최근 완료"
          value={lastDone.finished_at ? formatDateTime(lastDone.finished_at) : "—"}
          sub={`발견 ${lastDone.certificates_found} · 신규 ${lastDone.certificates_new}`}
        />,
      );
    }

    const newFound = jobs
      .filter((j) => j.status === "completed")
      .reduce((sum, j) => sum + j.certificates_new, 0);
    rows.push(
      <StatRow
        key="new-found"
        label="신규 발견"
        value={`${newFound}건`}
        sub="최근 완료 스캔 기준"
      />,
    );
  }

  if (sweeps) {
    const active = sweeps.filter((s) => ACTIVE_SWEEP.has(s.status)).length;
    const paused = sweeps.filter((s) => s.status === "paused").length;
    if (active > 0 || paused > 0) {
      rows.push(
        <StatRow
          key="sweeps"
          label="전 포트 스윕"
          value={`${active}건 진행`}
          sub={paused > 0 ? `일시정지 ${paused}건` : undefined}
        />,
      );
    }
  }

  if (rescan) {
    rows.push(
      <StatRow
        key="rescan"
        label="재스캔 적체"
        value={`${rescan.due_endpoints}건`}
        tone={rescan.due_endpoints > 0 ? "warn" : undefined}
        sub={
          rescan.enabled
            ? `주기 ${formatInterval(rescan.interval_seconds)} · 감시 ${rescan.monitored_endpoints}`
            : "자동 재스캔 꺼짐"
        }
      />,
    );
  }

  return <div className="px-4 py-1">{rows}</div>;
}
