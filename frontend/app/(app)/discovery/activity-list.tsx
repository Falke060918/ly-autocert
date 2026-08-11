"use client";

/**
 * 통합 작업 목록 — 단발 스캔·전 포트 스윕·정기 스캔 실행이 한 표에 섞여 최신순으로 선다.
 *
 * 예전에는 [스캔 | 스윕 | 주기 재스캔] 세 탭이었다. "지금 무엇이 돌고 있나"를 알려면
 * 탭을 세 번 눌러야 했고, 방금 시작한 작업이 어느 탭에 들어갔는지는 사용자가
 * 규모를 보고 짐작해야 했다. 서버가 GET /discovery/activity로 합쳐 주므로
 * 화면도 하나다(자동 발주 개별 Job은 서버가 제외한다).
 *
 * **취소 가능 여부는 판정하지 않는다** — `item.cancel`이 서버 판정이다.
 */

import * as React from "react";
import Link from "next/link";

import { Alert, AlertDescription } from "@/components/ui/alert";
import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import {
  Dialog,
  DialogContent,
  DialogDescription,
  DialogFooter,
  DialogHeader,
  DialogTitle,
} from "@/components/ui/dialog";
import { Progress } from "@/components/ui/progress";
import {
  Table,
  TableBody,
  TableCell,
  TableHead,
  TableHeader,
  TableRow,
} from "@/components/ui/table";
import { useTicker } from "@/components/notification-bell";
import type { Sensor } from "@/lib/api";
import {
  cancelActivity,
  listDiscoveryActivity,
  type ActivityItem,
  type ActivityKind,
} from "@/lib/activity";
import { formatDateTime } from "@/lib/format";
import { elapsedLabel, formatCount, sweepSensorID, transitionSweep } from "@/lib/sweeps";

/** 진행 중인 작업이 있을 때의 폴링 주기. */
const POLL_MS = 2000;
/** 한 화면에 싣는 작업 수. 그보다 오래된 것은 이 화면의 관심사가 아니다. */
const PAGE_SIZE = 20;

/**
 * 이 시간을 넘도록 대기 중이면 "지연"으로 본다.
 * 센서 pull 주기는 5초, 백오프 최대 60초, 리스 TTL 90초이므로(manual/SENSOR.md)
 * 정상이라면 90초 안에는 수령된다 — 넘으면 센서 미가동을 의심할 구간이다.
 */
const STALE_WAIT_MS = 90_000;

const KIND_LABELS: Record<ActivityKind, string> = {
  scan: "단발 스캔",
  sweep: "전 포트 스윕",
  schedule_run: "정기 스캔",
};

/**
 * 상태 라벨. 축마다 상태 집합이 달라(스캔 pending/leased·스윕 queued/paused·
 * 실행 running) 하나의 맵이 셋을 모두 덮는다. 모르는 값은 원문을 그대로 보인다.
 */
const STATUS_LABELS: Record<string, string> = {
  pending: "대기 중",
  queued: "대기 중",
  leased: "수령됨",
  running: "진행 중",
  paused: "일시정지",
  completed: "완료",
  failed: "실패",
  canceled: "취소됨",
};

/**
 * 상태 배지 톤. 상태 3색(danger/warn/ok)은 인증서 상태 표현 전용이므로 쓰지 않고,
 * 실패만 destructive로 구분한다(취소는 발주자의 결정이지 오류가 아니다).
 */
function statusVariant(status: string): "default" | "secondary" | "outline" | "destructive" {
  if (status === "failed") return "destructive";
  if (status === "running") return "default";
  if (status === "completed") return "outline";
  return "secondary";
}

export function ActivityList({
  sensors,
  canManage,
  reloadToken = 0,
  highlightId,
}: {
  sensors: Sensor[];
  canManage: boolean;
  /** 값이 바뀌면 즉시 재조회한다 — 발주 폼이 이 목록 밖에 있어서다. */
  reloadToken?: number;
  /** 강조할 작업 id(중복 안내·방금 발주). */
  highlightId?: string | null;
}) {
  const [items, setItems] = React.useState<ActivityItem[] | null>(null);
  const [activeOnly, setActiveOnly] = React.useState(false);
  const [listError, setListError] = React.useState<string | null>(null);
  const [actionError, setActionError] = React.useState<string | null>(null);
  const [busyID, setBusyID] = React.useState<string | null>(null);
  const [cancelTarget, setCancelTarget] = React.useState<ActivityItem | null>(null);

  const sensorById = React.useMemo(() => {
    const map: Record<string, Sensor> = {};
    sensors.forEach((s) => {
      map[s.id] = s;
    });
    return map;
  }, [sensors]);

  // setState는 콜백 안에서만 부른다 — 효과 본문에서 직접 부르면
  // react-hooks/set-state-in-effect에 걸린다.
  const refresh = React.useCallback(
    () =>
      listDiscoveryActivity({
        pageSize: PAGE_SIZE,
        active: activeOnly ? true : undefined,
      })
        .then((res) => {
          setItems(res.items);
          setListError(null);
        })
        .catch((err: unknown) => {
          setListError(
            err instanceof Error ? err.message : "작업 목록을 불러올 수 없습니다.",
          );
          setItems((prev) => prev ?? []);
        }),
    [activeOnly],
  );

  React.useEffect(() => {
    void refresh();
  }, [refresh, reloadToken]);

  // 진행 중인 작업이 있을 때만 폴링한다 — 스캔이 스윕으로 승격돼도 같은 목록에
  // 다른 kind로 설 뿐이라 추적이 끊기지 않는다.
  const hasActive = (items ?? []).some((it) => it.active);
  React.useEffect(() => {
    if (!hasActive) return;
    const timer = setInterval(() => {
      void refresh();
    }, POLL_MS);
    return () => clearInterval(timer);
  }, [hasActive, refresh]);

  // 강조 행으로 스크롤한다(강조 자체는 부모가 잠시 뒤 걷는다).
  React.useEffect(() => {
    if (!highlightId) return;
    document
      .getElementById(`activity-${highlightId}`)
      ?.scrollIntoView({ block: "nearest", behavior: "smooth" });
  }, [highlightId, items]);

  const confirmCancel = React.useCallback(async () => {
    if (!cancelTarget) return;
    setBusyID(cancelTarget.id);
    setActionError(null);
    try {
      await cancelActivity(cancelTarget);
      setCancelTarget(null);
    } catch (err) {
      // 이미 종료된 작업의 409 포함 — 메시지를 그대로 보여준다.
      setActionError(
        err instanceof Error ? err.message : "작업을 취소할 수 없습니다.",
      );
      setCancelTarget(null);
    } finally {
      setBusyID(null);
      // 성공이든 실패든 서버가 정본이다.
      await refresh();
    }
  }, [cancelTarget, refresh]);

  /** 스윕의 일시정지·재개. 취소와 달리 서버 판정 필드가 없어 상태로 분기한다. */
  const pauseResume = React.useCallback(
    async (item: ActivityItem, action: "pause" | "resume") => {
      setBusyID(item.id);
      setActionError(null);
      try {
        await transitionSweep(item.id, action);
      } catch (err) {
        setActionError(
          err instanceof Error ? err.message : "스윕 상태를 바꿀 수 없습니다.",
        );
      } finally {
        setBusyID(null);
        await refresh();
      }
    },
    [refresh],
  );

  return (
    <>
      <div className="flex flex-wrap items-center justify-between gap-2 border-b px-4 py-2">
        <h2 className="text-sm font-semibold tracking-tight">작업 목록</h2>
        <Button
          type="button"
          size="xs"
          variant={activeOnly ? "secondary" : "outline"}
          aria-pressed={activeOnly}
          onClick={() => setActiveOnly((v) => !v)}
        >
          진행 중만
        </Button>
      </div>

      {(actionError ?? listError) && (
        <div className="space-y-2 px-4 pt-3">
          {actionError && (
            <Alert variant="destructive">
              <AlertDescription>{actionError}</AlertDescription>
            </Alert>
          )}
          {listError && (
            <Alert variant="destructive">
              <AlertDescription>{listError}</AlertDescription>
            </Alert>
          )}
        </div>
      )}

      {items == null ? (
        <p className="px-4 py-12 text-center text-sm text-muted-foreground">
          작업 목록을 불러오는 중…
        </p>
      ) : items.length === 0 ? (
        <p className="px-4 py-12 text-center text-sm text-muted-foreground">
          {activeOnly
            ? "지금 진행 중인 작업이 없습니다."
            : "아직 실행한 작업이 없습니다."}
          {canManage && !activeOnly && " 검색 실행에서 대상 대역을 입력해 시작하십시오."}
        </p>
      ) : (
        <Table>
          <TableHeader>
            <TableRow>
              <TableHead>작업</TableHead>
              <TableHead className="w-44">상태</TableHead>
              <TableHead className="text-right">엔드포인트</TableHead>
              <TableHead className="text-right">발견</TableHead>
              <TableHead className="text-right">신규</TableHead>
              <TableHead>시작</TableHead>
              {canManage && <TableHead className="w-32" />}
            </TableRow>
          </TableHeader>
          <TableBody>
            {items.map((item) => (
              <ActivityRow
                key={`${item.kind}-${item.id}`}
                item={item}
                sensorById={sensorById}
                canManage={canManage}
                busy={busyID === item.id}
                highlighted={item.id === highlightId}
                onCancelRequest={setCancelTarget}
                onPauseResume={pauseResume}
              />
            ))}
          </TableBody>
        </Table>
      )}

      <Dialog
        open={cancelTarget != null}
        onOpenChange={(open) => {
          if (!open) setCancelTarget(null);
        }}
      >
        <DialogContent>
          <DialogHeader>
            <DialogTitle>이 작업을 취소하시겠습니까?</DialogTitle>
            <DialogDescription>
              {cancelTarget && KIND_LABELS[cancelTarget.kind]} ·{" "}
              <span className="font-mono">
                {cancelTarget?.targets.join(", ")}
              </span>{" "}
              을(를) 멈춥니다. 이미 수집된 인증서는 인벤토리에 그대로 남습니다.
            </DialogDescription>
          </DialogHeader>
          <DialogFooter>
            <Button
              type="button"
              variant="outline"
              disabled={busyID != null}
              onClick={() => setCancelTarget(null)}
            >
              계속 실행
            </Button>
            <Button
              type="button"
              variant="destructive"
              disabled={busyID != null}
              onClick={() => void confirmCancel()}
            >
              {busyID != null ? "취소 중…" : "작업 취소"}
            </Button>
          </DialogFooter>
        </DialogContent>
      </Dialog>
    </>
  );
}

function ActivityRow({
  item,
  sensorById,
  canManage,
  busy,
  highlighted,
  onCancelRequest,
  onPauseResume,
}: {
  item: ActivityItem;
  sensorById: Record<string, Sensor>;
  canManage: boolean;
  busy: boolean;
  highlighted: boolean;
  onCancelRequest: (item: ActivityItem) => void;
  onPauseResume: (item: ActivityItem, action: "pause" | "resume") => void;
}) {
  const now = useTicker(1000);
  const pct =
    item.total_endpoints > 0
      ? Math.round((item.scanned_endpoints / item.total_endpoints) * 100)
      : 0;
  const waiting = item.status === "pending" || item.status === "queued";
  const sensorID = sweepSensorID(item.executor_scope);
  const sensor = sensorID ? sensorById[sensorID] : undefined;
  const executor = sensorID
    ? sensor
      ? `센서 “${sensor.name}”`
      : `센서 ${sensorID.slice(0, 8)}`
    : "서버";

  // 스윕만 일시정지·재개가 있다. 취소와 달리 서버 판정 필드가 없으므로
  // sweepTransitions의 허용 출발 상태를 그대로 옮긴다.
  const canPause =
    item.kind === "sweep" && (item.status === "queued" || item.status === "running");
  const canResume = item.kind === "sweep" && item.status === "paused";

  const sensorBlocked = sensor != null && sensor.status !== "active";
  const waitedMs = now - new Date(item.started_at).getTime();
  const overdue = waiting && (sensorBlocked || waitedMs > STALE_WAIT_MS);

  return (
    <TableRow
      id={`activity-${item.id}`}
      className={highlighted ? "bg-accent/60 transition-colors" : undefined}
    >
      <TableCell className="max-w-64">
        <div className="flex flex-wrap items-center gap-1.5">
          <Badge variant="outline" className="text-muted-foreground">
            {KIND_LABELS[item.kind]}
          </Badge>
          {item.name && (
            <span className="truncate text-[13px] font-medium">{item.name}</span>
          )}
          {item.origin === "schedule" && (
            <Badge variant="secondary">주기 실행</Badge>
          )}
        </div>
        <div className="truncate font-mono text-xs text-muted-foreground">
          {item.targets.join(", ")}
        </div>
        <div className="truncate font-mono text-[11px] text-muted-foreground/70">
          포트 {item.port_spec || "—"} · {executor}
        </div>
      </TableCell>

      <TableCell>
        {item.status === "running" && item.total_endpoints > 0 ? (
          <div className="flex items-center gap-2">
            <Progress value={pct} className="h-1.5 flex-1" />
            <span className="font-mono text-xs text-muted-foreground tabular-nums">
              {pct}%
            </span>
          </div>
        ) : (
          <Badge variant={statusVariant(item.status)} title={item.error ?? undefined}>
            {STATUS_LABELS[item.status] ?? item.status}
          </Badge>
        )}
        {waiting && (
          // 누구를 기다리는지·얼마나 기다렸는지는 센서가 죽어 작업이 멈춘 경우를
          // 화면에서 바로 진단하는 단서다 (manual/SENSOR.md 문제해결).
          <div
            className={`truncate text-[11px] ${overdue ? "text-warn" : "text-muted-foreground"}`}
          >
            {sensorBlocked
              ? `${executor} ${sensor?.status === "disabled" ? "비활성화됨" : "등록 대기"} — 수령 불가`
              : `${executor} 수령 대기 · ${elapsedLabel(item.started_at, now)}`}
          </div>
        )}
      </TableCell>

      <TableCell className="text-right font-mono text-xs tabular-nums">
        {waiting && item.total_endpoints === 0 ? (
          // 센서 위임 작업은 센서가 대상을 펼치기 전까지 개수를 모른다 — 0/0은 오해를 부른다.
          <span className="text-muted-foreground">—</span>
        ) : (
          `${formatCount(item.scanned_endpoints)} / ${formatCount(item.total_endpoints)}`
        )}
      </TableCell>
      <TableCell className="text-right font-mono text-xs tabular-nums">
        {formatCount(item.certificates_found)}
      </TableCell>
      <TableCell className="text-right font-mono text-xs tabular-nums">
        {item.certificates_new > 0 ? (
          <Link
            href="/certificates?sort=created_at&order=desc"
            className="font-medium hover:underline hover:underline-offset-3"
          >
            {formatCount(item.certificates_new)}
          </Link>
        ) : (
          0
        )}
      </TableCell>
      <TableCell className="font-mono text-xs tabular-nums">
        {formatDateTime(item.started_at)}
      </TableCell>

      {canManage && (
        <TableCell className="text-right">
          <div className="flex justify-end gap-1.5">
            {canPause && (
              <Button
                type="button"
                size="xs"
                variant="outline"
                disabled={busy}
                onClick={() => onPauseResume(item, "pause")}
              >
                일시정지
              </Button>
            )}
            {canResume && (
              <Button
                type="button"
                size="xs"
                variant="outline"
                disabled={busy}
                onClick={() => onPauseResume(item, "resume")}
              >
                재개
              </Button>
            )}
            {/* 취소 가능 여부는 서버 판정이다 — 상태로 추론하지 않는다. */}
            {item.cancel && (
              <Button
                type="button"
                size="xs"
                variant="outline"
                disabled={busy}
                onClick={() => onCancelRequest(item)}
              >
                취소
              </Button>
            )}
          </div>
        </TableCell>
      )}
    </TableRow>
  );
}
