"use client";

/**
 * 스캔 설정 — 정기 스캔 3종(전체 대역 훑기 · 지정 대역 · 등록된 인증서 재확인).
 *
 * 종류가 규칙을 가른다. 전체·등록된 인증서는 서버가 심어 둔 **1건이 정본**이라
 * 만들거나 지울 수 없고 설정만 바꾼다("쓰지 않음"은 삭제가 아니라 끄기다).
 * 지정 대역은 여러 개를 등록하고 지운다.
 *
 * 대상·포트 문법을 화면에서 검사하지 않는다 — 파서는 서버 한 곳이고, 흉내 낸
 * 판정은 반드시 서버와 갈라진다. 여기서는 서버가 돌려준 문구를 그대로 보여준다.
 *
 * 재스캔(이미 아는 엔드포인트 다시 보기) 설정도 이 화면이 갖는다. 예전에는
 * Discovery 화면의 읽기 전용 카드였고 주기·속도는 서버 환경변수뿐이었지만,
 * 이제 '등록된 인증서 재확인' 행이 환경변수를 이기는 설정 자리다.
 */

import * as React from "react";
import { Play, Plus, Trash2, X } from "lucide-react";

import { Alert, AlertDescription, AlertTitle } from "@/components/ui/alert";
import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import { Card } from "@/components/ui/card";
import {
  Dialog,
  DialogContent,
  DialogDescription,
  DialogFooter,
  DialogHeader,
  DialogTitle,
} from "@/components/ui/dialog";
import { Input } from "@/components/ui/input";
import { Label } from "@/components/ui/label";
import {
  Select,
  SelectContent,
  SelectItem,
  SelectTrigger,
  SelectValue,
} from "@/components/ui/select";
import { Skeleton } from "@/components/ui/skeleton";
import { Switch } from "@/components/ui/switch";
import { Textarea } from "@/components/ui/textarea";
import { useTicker } from "@/components/notification-bell";
import { canManageInventory, useAuth } from "@/lib/auth";
import { formatDateTime } from "@/lib/format";
import { formatInterval, getRescanStatus, type RescanStatus } from "@/lib/rescan";
import {
  MIN_SCHEDULE_INTERVAL_SECONDS,
  SCHEDULE_KIND_HINT,
  SCHEDULE_KIND_LABEL,
  cancelScanScheduleRun,
  createScanSchedule,
  deleteScanSchedule,
  listScanSchedules,
  runScanSchedule,
  runStatusLabel,
  untilLabel,
  updateScanSchedule,
  type ScanScheduleItem,
  type ScanScheduleListResponse,
  type ScanScheduleUpdateRequest,
} from "@/lib/schedules";
import { listPortPresets, type PortPreset } from "@/lib/sweeps";

function message(err: unknown, fallback: string): string {
  return err instanceof Error && err.message ? err.message : fallback;
}

export default function ScanSettingsPage() {
  const { user, loading } = useAuth();
  const canManage = canManageInventory(user);

  const [data, setData] = React.useState<ScanScheduleListResponse | null>(null);
  const [rescan, setRescan] = React.useState<RescanStatus | null>(null);
  const [presets, setPresets] = React.useState<PortPreset[]>([]);
  const [error, setError] = React.useState<string | null>(null);
  const [reloadKey, setReloadKey] = React.useState(0);
  const [addOpen, setAddOpen] = React.useState(false);
  const now = useTicker(30_000);

  React.useEffect(() => {
    let cancelled = false;
    listScanSchedules()
      .then((res) => {
        if (cancelled) return;
        setData(res);
        setError(null);
      })
      .catch((err: unknown) => {
        if (!cancelled) setError(message(err, "정기 스캔 설정을 불러오지 못했습니다."));
      });
    return () => {
      cancelled = true;
    };
  }, [reloadKey]);

  // 재스캔 현황·포트 프리셋은 보조 정보다 — 실패해도 설정 편집은 그대로 된다.
  React.useEffect(() => {
    let cancelled = false;
    void Promise.allSettled([getRescanStatus(), listPortPresets()]).then(
      ([status, presetList]) => {
        if (cancelled) return;
        setRescan(status.status === "fulfilled" ? status.value : null);
        setPresets(
          presetList.status === "fulfilled" ? presetList.value.items : [],
        );
      },
    );
    return () => {
      cancelled = true;
    };
  }, [reloadKey]);

  const reload = React.useCallback(() => setReloadKey((k) => k + 1), []);

  if (loading) return <Skeleton className="h-96 rounded-lg" />;

  const items = data?.items ?? [];
  const full = items.filter((it) => it.kind === "full");
  const preset = items.filter((it) => it.kind === "preset");
  const registered = items.filter((it) => it.kind === "registered");

  return (
    <div className="mx-auto max-w-[900px] space-y-4">
      {/* 제목은 화면에서 걷어냈지만 문서 구조(h1)는 스크린리더용으로 남긴다. */}
      <h1 className="sr-only">스캔 설정</h1>

      {error && (
        <Alert variant="destructive">
          <AlertTitle>설정을 불러오지 못했습니다</AlertTitle>
          <AlertDescription>{error}</AlertDescription>
        </Alert>
      )}

      {data && !data.wired && (
        <Alert>
          <AlertTitle>이 서버는 정기 스캔을 돌리지 않습니다</AlertTitle>
          <AlertDescription>
            설정은 저장되지만 예정된 시각이 와도 실행되지 않고 [지금 실행]도
            받지 않습니다. 서버 구성을 확인하십시오.
          </AlertDescription>
        </Alert>
      )}

      {!canManage && data && (
        <Alert>
          <AlertTitle>보기 전용입니다</AlertTitle>
          <AlertDescription>
            정기 스캔 설정은 시스템 관리자·PKI 관리자·운영자만 바꿀 수 있습니다.
          </AlertDescription>
        </Alert>
      )}

      {!data && !error && (
        <>
          <Skeleton className="h-52 rounded-lg" />
          <Skeleton className="h-52 rounded-lg" />
        </>
      )}

      {full.map((item) => (
        <ScheduleCard
          key={`${item.id}:${item.updated_at}`}
          item={item}
          canManage={canManage}
          wired={data?.wired ?? false}
          presets={presets}
          now={now}
          onChanged={reload}
        />
      ))}

      {data && (
        <section className="space-y-3">
          <div className="flex flex-wrap items-center gap-2">
            <h2 className="text-sm font-semibold tracking-tight">
              {SCHEDULE_KIND_LABEL.preset}
            </h2>
            <span className="text-xs text-muted-foreground">
              {SCHEDULE_KIND_HINT.preset}
            </span>
            {canManage && (
              <Button
                size="sm"
                variant="outline"
                className="ml-auto"
                onClick={() => setAddOpen(true)}
              >
                <Plus data-icon="inline-start" />
                대역 추가
              </Button>
            )}
          </div>

          {preset.length === 0 ? (
            <Card className="p-8 text-center">
              <p className="text-[13px] text-muted-foreground">
                등록된 대역이 없습니다. 자주 확인하는 대역을 추가해 두면 그
                대역만 주기적으로 다시 찾습니다.
              </p>
            </Card>
          ) : (
            preset.map((item) => (
              <ScheduleCard
                key={`${item.id}:${item.updated_at}`}
                item={item}
                canManage={canManage}
                wired={data.wired}
                presets={presets}
                now={now}
                onChanged={reload}
              />
            ))
          )}
        </section>
      )}

      {registered.map((item) => (
        <ScheduleCard
          key={`${item.id}:${item.updated_at}`}
          item={item}
          canManage={canManage}
          wired={data?.wired ?? false}
          presets={presets}
          rescan={rescan}
          now={now}
          onChanged={reload}
        />
      ))}

      <AddPresetDialog
        open={addOpen}
        presets={presets}
        onOpenChange={setAddOpen}
        onCreated={() => {
          setAddOpen(false);
          reload();
        }}
      />
    </div>
  );
}

// ── 주기 입력 ─────────────────────────────────────────────────────────────
//
// 초 단위 값을 그대로 치게 하지 않는다. 값은 초로 오가고 화면만 단위를 나눈다.

const UNIT_SECONDS = { minute: 60, hour: 3_600, day: 86_400 } as const;
type IntervalUnit = keyof typeof UNIT_SECONDS;

const UNIT_LABEL: Record<IntervalUnit, string> = {
  minute: "분",
  hour: "시간",
  day: "일",
};

/** 초를 나누어떨어지는 가장 큰 단위로 편다. null이면 빈 입력(서버 기본값). */
function splitInterval(seconds: number | null): {
  value: string;
  unit: IntervalUnit;
} {
  if (seconds == null || seconds <= 0) return { value: "", unit: "hour" };
  if (seconds % UNIT_SECONDS.day === 0)
    return { value: String(seconds / UNIT_SECONDS.day), unit: "day" };
  if (seconds % UNIT_SECONDS.hour === 0)
    return { value: String(seconds / UNIT_SECONDS.hour), unit: "hour" };
  return { value: String(Math.round(seconds / 60)), unit: "minute" };
}

function IntervalField({
  id,
  value,
  unit,
  disabled,
  placeholder,
  onChange,
}: {
  id: string;
  value: string;
  unit: IntervalUnit;
  disabled: boolean;
  placeholder: string;
  onChange: (value: string, unit: IntervalUnit) => void;
}) {
  return (
    <div className="flex items-center gap-2">
      <Input
        id={id}
        inputMode="numeric"
        className="w-24"
        disabled={disabled}
        placeholder={placeholder}
        value={value}
        onChange={(e) => onChange(e.target.value, unit)}
      />
      <Select
        value={unit}
        disabled={disabled}
        onValueChange={(v) => onChange(value, v as IntervalUnit)}
      >
        <SelectTrigger className="w-24" aria-label="주기 단위">
          <SelectValue />
        </SelectTrigger>
        <SelectContent>
          {(Object.keys(UNIT_LABEL) as IntervalUnit[]).map((u) => (
            <SelectItem key={u} value={u}>
              {UNIT_LABEL[u]}마다
            </SelectItem>
          ))}
        </SelectContent>
      </Select>
    </div>
  );
}

/** 포트 사양 입력 + 서버가 준 프리셋 칩. 칩은 입력 보조일 뿐 검증이 아니다. */
function PortField({
  id,
  value,
  presets,
  disabled,
  onChange,
}: {
  id: string;
  value: string;
  presets: PortPreset[];
  disabled: boolean;
  onChange: (next: string) => void;
}) {
  return (
    <div className="space-y-2">
      <Input
        id={id}
        spellCheck={false}
        className="font-mono"
        disabled={disabled}
        placeholder="common"
        value={value}
        onChange={(e) => onChange(e.target.value)}
      />
      {presets.length > 0 && (
        <div className="flex flex-wrap items-center gap-1.5">
          {presets.map((p) => (
            <Button
              key={p.name}
              type="button"
              size="xs"
              variant={value.trim() === p.name ? "secondary" : "outline"}
              disabled={disabled}
              className="font-mono"
              onClick={() => onChange(p.name)}
            >
              {p.name}
              <span className="ml-1 font-sans text-muted-foreground">
                {p.count.toLocaleString()}개
              </span>
            </Button>
          ))}
        </div>
      )}
      <p className="text-xs text-muted-foreground">
        프리셋 이름, 목록(443,8443), 범위(8000-9000)를 섞어 쓸 수 있습니다.
      </p>
    </div>
  );
}

// ── 설정 1건 ──────────────────────────────────────────────────────────────

function ScheduleCard({
  item,
  canManage,
  wired,
  presets,
  rescan,
  now,
  onChanged,
}: {
  item: ScanScheduleItem;
  canManage: boolean;
  wired: boolean;
  presets: PortPreset[];
  /** 등록된 인증서 재확인 카드에만 실린다(재스캔 현황). 못 받았으면 null. */
  rescan?: RescanStatus | null;
  now: number;
  onChanged: () => void;
}) {
  // 대상·포트를 갖지 않는 종류다 — 대상은 이미 등록된 엔드포인트다.
  const scoped = item.kind !== "registered";

  const initialInterval = splitInterval(item.interval_seconds);
  const [enabled, setEnabled] = React.useState(item.enabled);
  const [name, setName] = React.useState(item.name);
  const [targets, setTargets] = React.useState(item.targets.join("\n"));
  const [portSpec, setPortSpec] = React.useState(item.port_spec ?? "");
  const [intervalValue, setIntervalValue] = React.useState(initialInterval.value);
  const [intervalUnit, setIntervalUnit] = React.useState<IntervalUnit>(
    initialInterval.unit,
  );
  const [ratePPS, setRatePPS] = React.useState(
    item.rate_pps == null ? "" : String(item.rate_pps),
  );
  const [busy, setBusy] = React.useState(false);
  const [error, setError] = React.useState<string | null>(null);
  const [notice, setNotice] = React.useState<string | null>(null);
  const [deleteOpen, setDeleteOpen] = React.useState(false);

  const running = item.active_run != null;
  const locked = !canManage || busy;

  const dirty =
    enabled !== item.enabled ||
    name.trim() !== item.name ||
    (scoped &&
      (targets.trim() !== item.targets.join("\n").trim() ||
        portSpec.trim() !== (item.port_spec ?? ""))) ||
    intervalValue.trim() !== initialInterval.value ||
    (intervalValue.trim() !== "" && intervalUnit !== initialInterval.unit) ||
    ratePPS.trim() !== (item.rate_pps == null ? "" : String(item.rate_pps));

  /** 빈 칸은 null(서버 기본값)이고, 숫자가 아니면 문자열로 사유를 돌려준다. */
  function numberOrNull(raw: string, label: string): number | null | string {
    const text = raw.trim();
    if (text === "") return null;
    const n = Number(text);
    if (!Number.isInteger(n) || n <= 0) return `${label}은(는) 1 이상의 정수여야 합니다.`;
    return n;
  }

  const save = async () => {
    setError(null);
    setNotice(null);

    const interval = numberOrNull(intervalValue, "주기");
    if (typeof interval === "string") {
      setError(interval);
      return;
    }
    const rate = numberOrNull(ratePPS, "초당 프로브 수");
    if (typeof rate === "string") {
      setError(rate);
      return;
    }

    const req: ScanScheduleUpdateRequest = {
      id: item.id,
      enabled,
      // null은 "서버 기본값으로 되돌림"이라 키를 항상 싣는다.
      interval_seconds:
        interval == null ? null : interval * UNIT_SECONDS[intervalUnit],
      rate_pps: rate,
    };
    if (scoped) {
      req.name = name.trim();
      req.targets = targets
        .split("\n")
        .map((s) => s.trim())
        .filter(Boolean);
      req.port_spec = portSpec.trim();
    }

    setBusy(true);
    try {
      await updateScanSchedule(req);
      onChanged();
    } catch (err) {
      setError(message(err, "설정을 저장하지 못했습니다."));
    } finally {
      setBusy(false);
    }
  };

  const runNow = async () => {
    setError(null);
    setNotice(null);
    setBusy(true);
    try {
      await runScanSchedule(item.id);
      setNotice("실행을 시작했습니다. 예정된 다음 실행 시각은 그대로입니다.");
      onChanged();
    } catch (err) {
      setError(message(err, "지금 실행할 수 없습니다."));
    } finally {
      setBusy(false);
    }
  };

  const cancelRun = async () => {
    if (!item.active_run) return;
    setError(null);
    setNotice(null);
    setBusy(true);
    try {
      await cancelScanScheduleRun(item.active_run.id);
      onChanged();
    } catch (err) {
      setError(message(err, "실행을 취소하지 못했습니다."));
    } finally {
      setBusy(false);
    }
  };

  const remove = async () => {
    setError(null);
    setBusy(true);
    try {
      await deleteScanSchedule(item.id);
      setDeleteOpen(false);
      onChanged();
    } catch (err) {
      setDeleteOpen(false);
      setError(message(err, "삭제하지 못했습니다."));
    } finally {
      setBusy(false);
    }
  };

  return (
    <Card className="gap-0 overflow-hidden py-0">
      <div className="flex flex-wrap items-center gap-x-3 gap-y-2 border-b px-4 py-3">
        <Switch
          aria-label={`${item.name} 사용`}
          checked={enabled}
          disabled={locked}
          onCheckedChange={setEnabled}
        />
        <div className="min-w-0">
          <div className="flex items-center gap-2">
            <span className="text-sm font-semibold tracking-tight">
              {item.kind === "preset" ? item.name : SCHEDULE_KIND_LABEL[item.kind]}
            </span>
            {running && <Badge variant="outline">실행 중</Badge>}
            {!enabled && <Badge variant="secondary">꺼짐</Badge>}
          </div>
          {item.kind !== "preset" && (
            <p className="text-xs text-muted-foreground">
              {SCHEDULE_KIND_HINT[item.kind]}
            </p>
          )}
        </div>

        <div className="ml-auto flex items-center gap-2">
          {canManage && running && (
            <Button
              variant="outline"
              size="sm"
              disabled={busy}
              onClick={() => void cancelRun()}
            >
              <X data-icon="inline-start" />
              실행 취소
            </Button>
          )}
          {canManage && !running && (
            <Button
              variant="outline"
              size="sm"
              disabled={busy || !wired}
              title={wired ? undefined : "이 서버는 정기 스캔을 돌리지 않습니다."}
              onClick={() => void runNow()}
            >
              <Play data-icon="inline-start" />
              지금 실행
            </Button>
          )}
          {canManage && item.kind === "preset" && (
            <Button
              variant="ghost"
              size="icon"
              aria-label={`${item.name} 삭제`}
              disabled={busy}
              onClick={() => setDeleteOpen(true)}
            >
              <Trash2 />
            </Button>
          )}
        </div>
      </div>

      <div className="space-y-4 px-4 py-4">
        <div className="flex flex-wrap gap-x-6 gap-y-2 text-[13px]">
          <Meta label="다음 실행">
            {item.next_run_at
              ? `${formatDateTime(item.next_run_at)} (${untilLabel(item.next_run_at, now)})`
              : "예정 없음"}
          </Meta>
          <Meta label="마지막 실행">
            {item.last_run
              ? `${formatDateTime(item.last_run.started_at)} · ${runStatusLabel(item.last_run.status)}`
              : "없음"}
          </Meta>
        </div>

        {item.last_run?.error && (
          <p className="text-xs text-warn">
            마지막 실행 오류: {item.last_run.error}
          </p>
        )}

        {rescan && (
          <div className="flex flex-wrap gap-x-6 gap-y-2 rounded-md bg-muted/40 px-3 py-2 text-[13px]">
            <Meta label="감시 엔드포인트">
              {rescan.monitored_endpoints.toLocaleString()}
              {rescan.retired_endpoints > 0 &&
                ` (은퇴 ${rescan.retired_endpoints.toLocaleString()})`}
            </Meta>
            <Meta label="지연">
              {/* 적체는 0이 정상이다 — 쌓이면 스케줄러 미가동이나 배치 상한 부족이다. */}
              <span className={rescan.due_endpoints > 0 ? "text-warn" : undefined}>
                {rescan.due_endpoints.toLocaleString()}
              </span>
            </Meta>
            <Meta label="적용 중인 주기">{formatInterval(rescan.interval_seconds)}</Meta>
          </div>
        )}

        {item.kind === "preset" && (
          <div className="space-y-2">
            <Label htmlFor={`name-${item.id}`}>이름</Label>
            <Input
              id={`name-${item.id}`}
              maxLength={100}
              disabled={locked}
              value={name}
              onChange={(e) => setName(e.target.value)}
            />
          </div>
        )}

        {scoped && (
          <>
            <div className="space-y-2">
              <Label htmlFor={`targets-${item.id}`}>대상 IP / CIDR 대역</Label>
              <Textarea
                id={`targets-${item.id}`}
                spellCheck={false}
                className="min-h-20 font-mono text-xs"
                disabled={locked}
                placeholder={"10.0.0.5\n10.0.0.0/24"}
                value={targets}
                onChange={(e) => setTargets(e.target.value)}
              />
              <p className="text-xs text-muted-foreground">
                한 줄에 하나씩. 켜 두려면 대상이 하나 이상 있어야 합니다.
              </p>
            </div>
            <div className="space-y-2">
              <Label htmlFor={`ports-${item.id}`}>포트</Label>
              <PortField
                id={`ports-${item.id}`}
                value={portSpec}
                presets={presets}
                disabled={locked}
                onChange={setPortSpec}
              />
            </div>
          </>
        )}

        <div className="flex flex-wrap gap-x-8 gap-y-4">
          <div className="space-y-2">
            <Label htmlFor={`interval-${item.id}`}>주기</Label>
            <IntervalField
              id={`interval-${item.id}`}
              value={intervalValue}
              unit={intervalUnit}
              disabled={locked}
              placeholder="기본값"
              onChange={(v, u) => {
                setIntervalValue(v);
                setIntervalUnit(u);
              }}
            />
            <p className="text-xs text-muted-foreground">
              {scoped
                ? `비우면 서버 기본 주기입니다. ${MIN_SCHEDULE_INTERVAL_SECONDS / 3600}시간보다 짧게는 둘 수 없습니다.`
                : "비우면 서버 기본 주기입니다. 만료가 임박한 인증서는 이 값과 무관하게 더 자주 확인합니다."}
            </p>
          </div>
          <div className="space-y-2">
            <Label htmlFor={`rate-${item.id}`}>초당 프로브 수</Label>
            <Input
              id={`rate-${item.id}`}
              inputMode="numeric"
              className="w-28"
              disabled={locked}
              placeholder="기본값"
              value={ratePPS}
              onChange={(e) => setRatePPS(e.target.value)}
            />
            <p className="text-xs text-muted-foreground">
              비우면 서버 기본 속도입니다. 낮출수록 네트워크 부담이 줄고
              오래 걸립니다.
            </p>
          </div>
        </div>

        {error && (
          <Alert variant="destructive">
            <AlertDescription>{error}</AlertDescription>
          </Alert>
        )}
        {notice && <p className="text-xs text-muted-foreground">{notice}</p>}

        {canManage && (
          <div className="flex items-center gap-2">
            <Button size="sm" disabled={busy || !dirty} onClick={() => void save()}>
              변경 저장
            </Button>
            {dirty && (
              <span className="text-xs text-muted-foreground">
                저장하지 않은 변경이 있습니다.
              </span>
            )}
          </div>
        )}
      </div>

      <Dialog open={deleteOpen} onOpenChange={setDeleteOpen}>
        <DialogContent>
          <DialogHeader>
            <DialogTitle>이 대역을 지울까요?</DialogTitle>
            <DialogDescription>
              {item.name}의 정기 스캔이 사라지고 실행 이력도 함께 지워집니다.
              이미 시작된 스캔이 있으면 그 스캔은 계속 돌아갑니다 — 멈추려면
              Discovery 화면에서 취소하십시오.
            </DialogDescription>
          </DialogHeader>
          <DialogFooter>
            <Button variant="outline" onClick={() => setDeleteOpen(false)}>
              취소
            </Button>
            <Button variant="destructive" disabled={busy} onClick={() => void remove()}>
              삭제
            </Button>
          </DialogFooter>
        </DialogContent>
      </Dialog>
    </Card>
  );
}

function Meta({ label, children }: { label: string; children: React.ReactNode }) {
  return (
    <span className="flex items-center gap-2">
      <span className="text-muted-foreground">{label}</span>
      <span className="font-medium">{children}</span>
    </span>
  );
}

// ── 대역 추가 ─────────────────────────────────────────────────────────────

/**
 * 폼 상태는 안쪽 컴포넌트가 들고 있다. Dialog는 닫히면 내용을 언마운트하므로
 * 다시 열 때마다 useState 초기값으로 새로 시작한다 (접근 제한 화면과 같은 규약).
 */
function AddPresetDialog({
  open,
  presets,
  onOpenChange,
  onCreated,
}: {
  open: boolean;
  presets: PortPreset[];
  onOpenChange: (open: boolean) => void;
  onCreated: () => void;
}) {
  return (
    <Dialog open={open} onOpenChange={onOpenChange}>
      <DialogContent>
        <AddPresetForm
          presets={presets}
          onCancel={() => onOpenChange(false)}
          onCreated={onCreated}
        />
      </DialogContent>
    </Dialog>
  );
}

function AddPresetForm({
  presets,
  onCancel,
  onCreated,
}: {
  presets: PortPreset[];
  onCancel: () => void;
  onCreated: () => void;
}) {
  const [name, setName] = React.useState("");
  const [targets, setTargets] = React.useState("");
  const [portSpec, setPortSpec] = React.useState("common");
  const [intervalValue, setIntervalValue] = React.useState("24");
  const [intervalUnit, setIntervalUnit] = React.useState<IntervalUnit>("hour");
  const [enabled, setEnabled] = React.useState(true);
  const [error, setError] = React.useState<string | null>(null);
  const [saving, setSaving] = React.useState(false);

  const submit = async (e: React.FormEvent) => {
    e.preventDefault();
    setError(null);

    const interval = Number(intervalValue.trim());
    if (!Number.isInteger(interval) || interval <= 0) {
      setError("주기는 1 이상의 정수여야 합니다.");
      return;
    }

    setSaving(true);
    try {
      await createScanSchedule({
        name: name.trim(),
        enabled,
        targets: targets
          .split("\n")
          .map((s) => s.trim())
          .filter(Boolean),
        port_spec: portSpec.trim(),
        interval_seconds: interval * UNIT_SECONDS[intervalUnit],
      });
      onCreated();
    } catch (err) {
      setError(message(err, "대역을 추가하지 못했습니다."));
    } finally {
      setSaving(false);
    }
  };

  return (
    <form onSubmit={(e) => void submit(e)}>
      <DialogHeader>
        <DialogTitle>정기 스캔 대역 추가</DialogTitle>
        <DialogDescription>
          켜서 만들어도 지금 바로 돌지는 않습니다. 첫 실행은 주기가 한 번 지난
          뒤이며, 당장 확인하려면 [지금 실행]을 누르십시오.
        </DialogDescription>
      </DialogHeader>

      <div className="space-y-4 py-4">
        <div className="space-y-2">
          <Label htmlFor="preset-name">이름</Label>
          <Input
            id="preset-name"
            required
            autoFocus
            maxLength={100}
            placeholder="본사 서버망"
            value={name}
            onChange={(e) => setName(e.target.value)}
          />
        </div>
        <div className="space-y-2">
          <Label htmlFor="preset-targets">대상 IP / CIDR 대역</Label>
          <Textarea
            id="preset-targets"
            required
            spellCheck={false}
            className="min-h-20 font-mono text-xs"
            placeholder={"10.0.0.5\n10.0.0.0/24"}
            value={targets}
            onChange={(e) => setTargets(e.target.value)}
          />
        </div>
        <div className="space-y-2">
          <Label htmlFor="preset-ports">포트</Label>
          <PortField
            id="preset-ports"
            value={portSpec}
            presets={presets}
            disabled={saving}
            onChange={setPortSpec}
          />
        </div>
        <div className="space-y-2">
          <Label htmlFor="preset-interval">주기</Label>
          <IntervalField
            id="preset-interval"
            value={intervalValue}
            unit={intervalUnit}
            disabled={saving}
            placeholder="24"
            onChange={(v, u) => {
              setIntervalValue(v);
              setIntervalUnit(u);
            }}
          />
          <p className="text-xs text-muted-foreground">
            {MIN_SCHEDULE_INTERVAL_SECONDS / 3600}시간보다 짧게는 둘 수 없습니다.
          </p>
        </div>
        <div className="flex items-center gap-2">
          <Switch
            id="preset-enabled"
            checked={enabled}
            onCheckedChange={setEnabled}
          />
          <Label htmlFor="preset-enabled">추가하면서 바로 켜기</Label>
        </div>
        {error && (
          <Alert variant="destructive">
            <AlertTitle>추가하지 못했습니다</AlertTitle>
            <AlertDescription>{error}</AlertDescription>
          </Alert>
        )}
      </div>

      <DialogFooter>
        <Button type="button" variant="outline" onClick={onCancel}>
          취소
        </Button>
        <Button type="submit" disabled={saving || name.trim() === ""}>
          추가
        </Button>
      </DialogFooter>
    </form>
  );
}
