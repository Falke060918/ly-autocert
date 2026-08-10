"use client";

/**
 * 주기 재스캔 현황 카드 — **읽기 전용**이다(06-rescan-and-replacement 11.5).
 *
 * 주기·임계값을 화면에서 편집 가능하게 만들면 정책 테이블·CRUD API·감사 이벤트·
 * 권한 게이트가 줄줄이 따라온다. 운영자가 실제로 조정하는 빈도는 설치당 0~1회이므로
 * 설정은 환경변수 zero-config로 두고 화면은 "지금 어떻게 돌고 있나"만 보여준다.
 * 여기 있는 유일한 조작은 3역할 전용 "지금 1배치 실행"이다.
 */

import * as React from "react";
import { RefreshCw } from "lucide-react";

import { Alert, AlertDescription } from "@/components/ui/alert";
import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import { Card } from "@/components/ui/card";
import { Skeleton } from "@/components/ui/skeleton";
import { relativeTime } from "@/components/notification-bell";
import {
  formatInterval,
  getRescanStatus,
  runRescanBatch,
  type RescanStatus,
} from "@/lib/rescan";

import { useTicker } from "./sweep-panel";

export function RescanCard({ canManage }: { canManage: boolean }) {
  const [status, setStatus] = React.useState<RescanStatus | null>(null);
  const [error, setError] = React.useState<string | null>(null);
  const [notice, setNotice] = React.useState<string | null>(null);
  const [busy, setBusy] = React.useState(false);
  const now = useTicker(30_000);

  const refresh = React.useCallback(
    () =>
      getRescanStatus()
        .then((res) => {
          setStatus(res);
          setError(null);
        })
        .catch((err: unknown) => {
          setError(
            err instanceof Error ? err.message : "재스캔 현황을 불러올 수 없습니다.",
          );
        }),
    [],
  );

  React.useEffect(() => {
    void refresh();
  }, [refresh]);

  const run = React.useCallback(async () => {
    setBusy(true);
    setNotice(null);
    try {
      const res = await runRescanBatch();
      setNotice(
        res.dispatched_endpoints === 0
          ? "지금 다시 확인할 엔드포인트가 없습니다."
          : `엔드포인트 ${res.dispatched_endpoints.toLocaleString()}건을 ${res.dispatched_jobs}개 작업으로 발주했습니다.`,
      );
      await refresh();
    } catch (err) {
      setError(err instanceof Error ? err.message : "재스캔 배치를 시작할 수 없습니다.");
    } finally {
      setBusy(false);
    }
  }, [refresh]);

  return (
    <Card className="gap-0 overflow-hidden py-0">
      <div className="flex flex-wrap items-center gap-2 border-b px-4 py-3">
        <h2 className="text-sm font-semibold tracking-tight">주기 재스캔</h2>
        <span className="text-xs text-muted-foreground">
          이미 아는 엔드포인트를 다시 확인해 신선도를 유지하고 인증서 교체를 감지합니다
        </span>
        {canManage && status?.enabled && (
          <Button
            variant="outline"
            size="sm"
            className="ml-auto"
            disabled={busy}
            onClick={() => void run()}
          >
            <RefreshCw data-icon="inline-start" />
            {busy ? "발주 중…" : "지금 1배치 실행"}
          </Button>
        )}
      </div>

      {!status && !error && <Skeleton className="m-4 h-16 rounded-md" />}

      {status && (
        <div className="flex flex-wrap items-center gap-x-6 gap-y-3 px-4 py-3">
          <Field label="상태">
            {status.enabled ? (
              <Badge variant="outline">활성</Badge>
            ) : (
              <Badge variant="secondary">
                {status.wired ? "비활성" : "미배선"}
              </Badge>
            )}
          </Field>
          <Field label="간격">{formatInterval(status.interval_seconds)}</Field>
          <Field label="감시 엔드포인트">
            {status.monitored_endpoints.toLocaleString()}
            {status.retired_endpoints > 0 && (
              <span className="ml-1 text-xs text-muted-foreground">
                (은퇴 {status.retired_endpoints.toLocaleString()})
              </span>
            )}
          </Field>
          <Field label="마지막 실행">
            {status.last_run_at ? relativeTime(status.last_run_at, now) : "없음"}
          </Field>
          <Field label="지연">
            {/* 적체는 0이 정상이다 — 쌓이면 스케줄러 미가동이나 배치 상한 부족이다. */}
            <span
              className={
                status.due_endpoints > 0 ? "text-amber-700" : undefined
              }
            >
              {status.due_endpoints.toLocaleString()}
            </span>
          </Field>
        </div>
      )}

      {status && !status.enabled && (
        <div className="border-t px-4 py-2 text-xs text-muted-foreground">
          {status.wired
            ? "서버 설정에서 재스캔이 꺼져 있습니다. 인벤토리의 최근 확인 시각은 갱신되지 않습니다."
            : "이 서버에는 재스캔 스케줄러가 배선되어 있지 않습니다. 수동 스캔·스윕으로만 최근 확인 시각이 갱신됩니다."}
        </div>
      )}

      {notice && (
        <div className="border-t px-4 py-2 text-xs text-muted-foreground">{notice}</div>
      )}

      {error && (
        <div className="px-4 pb-4">
          <Alert variant="destructive">
            <AlertDescription>{error}</AlertDescription>
          </Alert>
        </div>
      )}
    </Card>
  );
}

function Field({
  label,
  children,
}: {
  label: string;
  children: React.ReactNode;
}) {
  return (
    <div className="flex items-center gap-2">
      <span className="text-[13px] text-muted-foreground">{label}</span>
      <span className="text-[13px] font-medium">{children}</span>
    </div>
  );
}
