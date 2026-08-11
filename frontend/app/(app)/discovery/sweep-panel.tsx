"use client";

import * as React from "react";

import { Alert, AlertDescription } from "@/components/ui/alert";
import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { Label } from "@/components/ui/label";
import { Progress } from "@/components/ui/progress";
import { ApiError, type Sensor } from "@/lib/api";
import { formatDateTime } from "@/lib/format";
import {
  createSweep,
  elapsedLabel,
  estimateSweep,
  formatCount,
  formatDuration,
  listSweeps,
  specHasPreset,
  STARTTLS_NOTICE,
  sweepSensorID,
  transitionSweep,
  type DiscoveryDefaults,
  type PortPreset,
  type Sweep,
  type SweepRequest,
  type SweepStatus,
  type SweepTransition,
} from "@/lib/sweeps";

/** 스캔 작업 표와 같은 폴링 주기를 쓴다(page.tsx의 POLL_MS와 같은 값). */
const SWEEP_POLL_MS = 2000;

/**
 * 주기적으로 갱신되는 현재 시각(ms). 렌더 중 `Date.now()`를 직접 부르면
 * 순수성 규칙(react-hooks/purity)에 걸리므로 상태로 흘려보낸다.
 */
export function useTicker(intervalMs: number): number {
  const [now, setNow] = React.useState(() => Date.now());
  React.useEffect(() => {
    const timer = setInterval(() => setNow(Date.now()), intervalMs);
    return () => clearInterval(timer);
  }, [intervalMs]);
  return now;
}

// ── 포트 사양 입력 ────────────────────────────────────────────────────────

/** 프리셋 이름별 한국어 라벨. 개수·설명은 서버 응답에서 온다. */
const PRESET_LABELS: Record<string, string> = {
  common: "자주 쓰는 포트",
  web: "웹만",
  all: "전 포트",
};

/** 칩 표시 순서. 목록에 없는 새 프리셋은 뒤에 붙는다. */
const PRESET_ORDER = ["common", "web", "all"];

function orderPresets(presets: PortPreset[]): PortPreset[] {
  return [...presets].sort((a, b) => {
    const ia = PRESET_ORDER.indexOf(a.name);
    const ib = PRESET_ORDER.indexOf(b.name);
    return (ia < 0 ? PRESET_ORDER.length : ia) - (ib < 0 ? PRESET_ORDER.length : ib);
  });
}

/**
 * 포트 사양 입력 — 자유 텍스트 하나 + 프리셋 칩.
 *
 * 클라이언트에서 포트를 파싱하지 않는다. 예전 구현은 `split(",")→parseInt`라
 * `8443-9443`이 조용히 `8443` 하나로 잘려나갔다(설계 9.3). 문법 검증은 전적으로
 * 서버 파서의 400 메시지에 맡긴다 — 파서를 둘 두면 반드시 갈라진다.
 *
 * 스캔 폼과 스윕 폼이 같은 입력을 쓰므로 여기 한 곳에만 둔다.
 */
export function PortSpecField({
  id,
  value,
  onChange,
  presets,
  singleScan = false,
}: {
  id: string;
  value: string;
  onChange: (next: string) => void;
  presets: PortPreset[];
  /** 단발 스캔 폼이면 all 프리셋이 쓸 수 없음을 미리 알린다(서버는 400). */
  singleScan?: boolean;
}) {
  const inputRef = React.useRef<HTMLInputElement>(null);
  const token = value.trim().toLowerCase();
  const selected = presets.find((p) => p.name === token)?.name ?? null;
  const hasAll = specHasPreset(value, "all");

  return (
    <div className="space-y-2">
      <Label htmlFor={id}>포트</Label>
      {presets.length > 0 && (
        <div className="flex flex-wrap gap-1.5">
          {orderPresets(presets).map((p) => (
            <Button
              key={p.name}
              type="button"
              size="xs"
              variant={selected === p.name ? "secondary" : "outline"}
              aria-pressed={selected === p.name}
              title={p.description}
              onClick={() => onChange(p.name)}
            >
              {PRESET_LABELS[p.name] ?? p.name}
              <span className="font-mono text-muted-foreground tabular-nums">
                {formatCount(p.count)}개
              </span>
            </Button>
          ))}
          <Button
            type="button"
            size="xs"
            variant={selected == null ? "secondary" : "outline"}
            aria-pressed={selected == null}
            onClick={() => {
              if (selected != null) onChange("");
              inputRef.current?.focus();
            }}
          >
            직접 입력
          </Button>
        </div>
      )}
      <Input
        id={id}
        ref={inputRef}
        spellCheck={false}
        className="font-mono text-xs"
        placeholder="common"
        value={value}
        onChange={(e) => onChange(e.target.value)}
      />
      <p className="text-xs text-muted-foreground">
        <code className="font-mono">443,8443</code> ·{" "}
        <code className="font-mono">8000-9000</code> ·{" "}
        <code className="font-mono">common</code> 을 쉼표로 조합할 수 있습니다.
      </p>
      {hasAll && singleScan && (
        <p className="text-xs text-amber-700">
          전 포트(all)는 단발 스캔에서 쓸 수 없습니다 — “전 포트 스윕” 탭에서
          시작하십시오.
        </p>
      )}
      {hasAll && (
        <p className="text-xs text-amber-700">{STARTTLS_NOTICE}</p>
      )}
    </div>
  );
}

// ── 스윕 목록 ─────────────────────────────────────────────────────────────

/**
 * 전 포트 백그라운드 스윕 목록 — "작업 현황" 카드의 스윕 탭 콘텐츠.
 * 스캔 Job과 달리 서버에 목록 API가 처음부터 있었으므로 localStorage 추적이 없다.
 */
export function SweepList({
  sensors,
  canManage,
  reloadToken = 0,
}: {
  sensors: Sensor[];
  canManage: boolean;
  /** 값이 바뀌면 즉시 재조회한다 — 생성 폼이 탭 밖(검색 실행 카드)에 있어서다. */
  reloadToken?: number;
}) {
  const [sweeps, setSweeps] = React.useState<Sweep[] | null>(null);
  const [listError, setListError] = React.useState<string | null>(null);
  const [actionError, setActionError] = React.useState<string | null>(null);
  const [busyID, setBusyID] = React.useState<string | null>(null);

  const sensorById = React.useMemo(() => {
    const map: Record<string, Sensor> = {};
    sensors.forEach((s) => {
      map[s.id] = s;
    });
    return map;
  }, [sensors]);

  // setState는 콜백 안에서만 부른다 — 효과 본문에서 직접 부르면
  // react-hooks/set-state-in-effect에 걸린다(GOTCHAS의 경과 시간 항목과 같은 규칙).
  const refresh = React.useCallback(
    () =>
      listSweeps()
        .then((res) => {
          setSweeps(res.items);
          setListError(null);
        })
        .catch((err: unknown) => {
          setListError(
            err instanceof Error ? err.message : "스윕 목록을 불러올 수 없습니다.",
          );
          setSweeps((prev) => prev ?? []);
        }),
    [],
  );

  React.useEffect(() => {
    void refresh();
  }, [refresh, reloadToken]);

  // 진행 중인 스윕이 있을 때만 폴링한다(스캔 표와 같은 규칙).
  const hasActive = (sweeps ?? []).some(
    (s) => s.status === "queued" || s.status === "running",
  );
  React.useEffect(() => {
    if (!hasActive) return;
    const timer = setInterval(() => {
      void refresh();
    }, SWEEP_POLL_MS);
    return () => clearInterval(timer);
  }, [hasActive, refresh]);

  const act = React.useCallback(async (id: string, action: SweepTransition) => {
    setBusyID(id);
    setActionError(null);
    try {
      const updated = await transitionSweep(id, action);
      setSweeps((prev) => (prev ?? []).map((s) => (s.id === updated.id ? updated : s)));
    } catch (err) {
      setActionError(
        err instanceof Error ? err.message : "스윕 상태를 바꿀 수 없습니다.",
      );
    } finally {
      setBusyID(null);
    }
  }, []);

  return (
    <div className="space-y-3 p-4">
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
      {sweeps == null ? (
        <p className="py-8 text-center text-sm text-muted-foreground">
          스윕 목록을 불러오는 중…
        </p>
      ) : sweeps.length === 0 ? (
        <p className="py-8 text-center text-sm text-muted-foreground">
          아직 실행한 스윕이 없습니다.
          {canManage && " 검색 실행의 “전 포트 스윕” 탭에서 시작하십시오."}
        </p>
      ) : (
        sweeps.map((s) => (
          <SweepCard
            key={s.id}
            sweep={s}
            sensorById={sensorById}
            canManage={canManage}
            busy={busyID === s.id}
            onAction={act}
          />
        ))
      )}
    </div>
  );
}

// ── 생성 폼 ───────────────────────────────────────────────────────────────

/**
 * 스윕 생성 폼 — "검색 실행" 카드의 전 포트 스윕 탭 콘텐츠.
 * 대상 목록·실행기는 스캔 탭과 공용이라 부모(page.tsx)가 들고 내려준다.
 */
export function SweepForm({
  presets,
  defaults,
  targetList,
  sensorId,
  onCreated,
}: {
  presets: PortPreset[];
  /**
   * GET /discovery/defaults의 유효값. 아직 못 받았으면 null이고, 그때는
   * 숫자를 지어내지 않는다(placeholder에 숫자 없음·미리보기 비움).
   */
  defaults: DiscoveryDefaults | null;
  /** 공용 대상 입력을 줄 목록으로 정리한 값 — 원문 그대로 서버에 보낸다. */
  targetList: string[];
  /** 실행을 위임할 센서 id. null이면 서버 직접 실행이다. */
  sensorId: string | null;
  onCreated: () => Promise<void> | void;
}) {
  const [name, setName] = React.useState("");
  const [portSpec, setPortSpec] = React.useState("all");
  const [ratePPS, setRatePPS] = React.useState("");
  const [error, setError] = React.useState<string | null>(null);
  const [tooLong, setTooLong] = React.useState<string | null>(null);
  const [busy, setBusy] = React.useState(false);

  const rate = Number.parseInt(ratePPS, 10);
  const estimate = estimateSweep(
    targetList,
    portSpec.trim() || "all",
    presets,
    Number.isFinite(rate) ? rate : (defaults?.sweep.rate_pps ?? null),
    defaults?.sweep.max_days ?? null,
  );

  async function submit(e: React.FormEvent) {
    e.preventDefault();
    setError(null);
    setTooLong(null);
    if (targetList.length === 0) {
      setError("스윕 대상(IP 또는 CIDR)을 한 줄에 하나씩 입력하십시오.");
      return;
    }

    setBusy(true);
    try {
      const req: SweepRequest = { targets: targetList };
      if (name.trim()) req.name = name.trim();
      if (portSpec.trim()) req.port_spec = portSpec.trim();
      if (sensorId) req.sensor_id = sensorId;
      if (Number.isFinite(rate) && rate > 0) req.rate_pps = rate;
      await createSweep(req);
      await onCreated();
    } catch (err) {
      // 422 sweep_too_long은 서버가 **실제 소요 일수**를 담아 보낸다 —
      // 문구를 다시 쓰지 않고 그대로 노출해야 얼마나 줄여야 할지 알 수 있다.
      if (err instanceof ApiError && err.code === "sweep_too_long") {
        setTooLong(err.message);
      } else {
        setError(err instanceof Error ? err.message : "스윕 생성에 실패했습니다.");
      }
    } finally {
      setBusy(false);
    }
  }

  return (
    <form onSubmit={submit} className="space-y-4">
      <div className="space-y-2">
        <Label htmlFor="sweep-name">이름</Label>
        <Input
          id="sweep-name"
          placeholder="예: DMZ 전수 조사"
          value={name}
          onChange={(e) => setName(e.target.value)}
        />
      </div>
      <PortSpecField
        id="sweep-ports"
        value={portSpec}
        onChange={setPortSpec}
        presets={presets}
      />
      <div className="space-y-2">
        <Label htmlFor="sweep-rate">스캔 속도 (초당 프로브)</Label>
        <Input
          id="sweep-rate"
          type="number"
          min={1}
          // 상한도 서버가 정한다(초과분은 서버가 잘라낸다).
          max={defaults?.limits.max_rate_pps}
          className="font-mono text-xs"
          placeholder={
            defaults ? `서버 기본값 (${defaults.sweep.rate_pps})` : "서버 기본값"
          }
          value={ratePPS}
          onChange={(e) => setRatePPS(e.target.value)}
        />
      </div>

      <SweepEstimateBox estimate={estimate} />

      {tooLong && (
        <Alert className="border-amber-200 bg-amber-50 text-amber-800">
          <AlertDescription className="text-amber-800">
            {tooLong}
            <span className="mt-1 block">
              대역을 줄이거나(예: /24 하나씩) 포트 사양을 좁혀 다시
              시도하십시오.
            </span>
          </AlertDescription>
        </Alert>
      )}
      {error && (
        <Alert variant="destructive">
          <AlertDescription>{error}</AlertDescription>
        </Alert>
      )}
      <Button type="submit" className="w-full" disabled={busy}>
        {busy ? "스윕 생성 중…" : "스윕 시작"}
      </Button>
    </form>
  );
}

/**
 * 시작 전 규모 미리보기. 422로 거부당한 뒤에야 규모를 아는 일이 없도록,
 * 총 엔드포인트와 예상 소요를 먼저 보여준다. 값은 중복 제거를 하지 않은
 * **상한 추정**이며 실제 수치는 서버가 확정한다.
 */
function SweepEstimateBox({
  estimate,
}: {
  estimate: ReturnType<typeof estimateSweep>;
}) {
  return (
    <div className="rounded-md border bg-muted/40 p-3">
      <div className="flex items-baseline justify-between">
        <span className="text-xs font-medium">예상 규모</span>
        <span className="text-[11px] text-muted-foreground">최대 추정치</span>
      </div>
      {estimate == null ? (
        <p className="mt-1 text-xs text-muted-foreground">
          대상과 포트를 입력하면 총 엔드포인트 수와 예상 소요를 계산합니다.
        </p>
      ) : (
        <dl className="mt-2 space-y-1 text-xs">
          <div className="flex justify-between gap-2">
            <dt className="text-muted-foreground">엔드포인트</dt>
            <dd className="font-mono tabular-nums">
              {formatCount(estimate.ipCount)} IP × {formatCount(estimate.portCount)}{" "}
              포트 = {formatCount(estimate.totalEndpoints)}
            </dd>
          </div>
          <div className="flex justify-between gap-2">
            <dt className="text-muted-foreground">예상 소요</dt>
            <dd
              className={`font-mono tabular-nums ${
                estimate.exceedsLimit ? "text-amber-700" : ""
              }`}
            >
              {formatDuration(estimate.seconds)}
            </dd>
          </div>
          {estimate.exceedsLimit && (
            <p className="pt-1 text-amber-700">
              서버 상한({estimate.maxDays}일)을 넘습니다 — 이대로 시작하면
              거부됩니다. 대역을 줄이십시오.
            </p>
          )}
        </dl>
      )}
    </div>
  );
}

// ── 스윕 카드 ─────────────────────────────────────────────────────────────

/** 스윕 상태 배지. Job 상태 배지와 어휘를 맞추되 타입은 별개다. */
const SWEEP_STATUS_LABEL: Record<SweepStatus, string> = {
  queued: "대기 중",
  running: "진행 중",
  paused: "일시정지",
  completed: "완료",
  failed: "실패",
  canceled: "취소됨",
};

function SweepStatusBadge({ status }: { status: SweepStatus }) {
  if (status === "failed") {
    return (
      <Badge className="border border-red-200 bg-red-50 text-red-700">
        {SWEEP_STATUS_LABEL.failed}
      </Badge>
    );
  }
  if (status === "running") {
    return (
      <Badge className="border border-emerald-200 bg-emerald-50 text-emerald-700">
        {SWEEP_STATUS_LABEL.running}
      </Badge>
    );
  }
  if (status === "completed") {
    return <Badge variant="outline">{SWEEP_STATUS_LABEL.completed}</Badge>;
  }
  return <Badge variant="secondary">{SWEEP_STATUS_LABEL[status]}</Badge>;
}

function SweepCard({
  sweep,
  sensorById,
  canManage,
  busy,
  onAction,
}: {
  sweep: Sweep;
  sensorById: Record<string, Sensor>;
  canManage: boolean;
  busy: boolean;
  onAction: (id: string, action: SweepTransition) => void;
}) {
  const now = useTicker(1000);
  const p = sweep.progress;
  const pct =
    p.chunks_total > 0 ? Math.round((p.chunks_completed / p.chunks_total) * 100) : 0;

  const sensorID = sweepSensorID(sweep.executor_scope);
  const sensor = sensorID ? sensorById[sensorID] : undefined;
  const executor = sensorID
    ? sensor
      ? `센서 “${sensor.name}”`
      : `센서 ${sensorID.slice(0, 8)}`
    : "서버";

  // stalled_since는 실패가 아니라 "센서가 아직 청크를 집어가지 않았다"는 뜻이다.
  const stalled =
    sweep.stalled_since != null &&
    (sweep.status === "queued" || sweep.status === "running");

  const running = sweep.status === "queued" || sweep.status === "running";
  const canCancel = running || sweep.status === "paused";

  return (
    <div className="rounded-lg border p-3">
      <div className="flex flex-wrap items-start justify-between gap-2">
        <div className="min-w-0">
          <div className="flex flex-wrap items-center gap-1.5">
            <span className="truncate text-sm font-medium">
              {sweep.name?.trim() || sweep.targets.join(", ")}
            </span>
            <SweepStatusBadge status={sweep.status} />
            {stalled && (
              <Badge className="border border-amber-200 bg-amber-50 text-amber-700">
                센서 대기 중
              </Badge>
            )}
          </div>
          <div className="mt-0.5 truncate font-mono text-[11px] text-muted-foreground">
            {sweep.targets.join(", ")} · 포트 {sweep.port_spec} · {executor} ·{" "}
            {sweep.rate_pps} pps
          </div>
        </div>
        {canManage && (
          <div className="flex shrink-0 gap-1.5">
            {running && (
              <Button
                type="button"
                size="xs"
                variant="outline"
                disabled={busy}
                onClick={() => onAction(sweep.id, "pause")}
              >
                일시정지
              </Button>
            )}
            {sweep.status === "paused" && (
              <Button
                type="button"
                size="xs"
                variant="outline"
                disabled={busy}
                onClick={() => onAction(sweep.id, "resume")}
              >
                재개
              </Button>
            )}
            {canCancel && (
              <Button
                type="button"
                size="xs"
                variant="destructive"
                disabled={busy}
                onClick={() => onAction(sweep.id, "cancel")}
              >
                취소
              </Button>
            )}
          </div>
        )}
      </div>

      {stalled && sweep.stalled_since && (
        <p className="mt-2 text-[11px] text-amber-700">
          {executor}가 청크를 아직 수령하지 않았습니다 ·{" "}
          {elapsedLabel(sweep.stalled_since, now)}
          {sensor && sensor.status !== "active" && " · 센서가 활성 상태가 아닙니다"}
        </p>
      )}

      <div className="mt-3 flex items-center gap-2">
        <Progress value={pct} className="h-1.5 flex-1" />
        <span className="font-mono text-xs text-muted-foreground tabular-nums">
          {pct}%
        </span>
      </div>

      <dl className="mt-2 grid grid-cols-2 gap-x-4 gap-y-1 text-[11px] sm:grid-cols-4">
        <Stat
          label="청크"
          value={`${formatCount(p.chunks_completed)} / ${formatCount(sweep.total_chunks)}`}
          hint={p.chunks_active > 0 ? `진행 중 ${p.chunks_active}` : undefined}
        />
        <Stat
          label="엔드포인트"
          value={`${formatCount(p.scanned_endpoints)} / ${formatCount(sweep.total_endpoints)}`}
          hint={`도달 ${formatCount(p.reachable_endpoints)}`}
        />
        <Stat label="발견" value={formatCount(p.certificates_found)} />
        <Stat label="신규" value={formatCount(p.certificates_new)} />
      </dl>

      <div className="mt-2 flex flex-wrap gap-x-4 gap-y-1 text-[11px] text-muted-foreground">
        <span>시작 {formatDateTime(sweep.started_at ?? sweep.created_at)}</span>
        {sweep.finished_at ? (
          <span>종료 {formatDateTime(sweep.finished_at)}</span>
        ) : sweep.estimated_finish_at ? (
          <span>예상 종료 {formatDateTime(sweep.estimated_finish_at)}</span>
        ) : null}
        {p.failed_attempts > 0 && (
          <span className="text-amber-700">
            실패한 청크 시도 {formatCount(p.failed_attempts)}회
          </span>
        )}
      </div>

      {sweep.last_error && (
        <p className="mt-2 truncate text-[11px] text-red-700" title={sweep.last_error}>
          {sweep.last_error}
        </p>
      )}
    </div>
  );
}

function Stat({
  label,
  value,
  hint,
}: {
  label: string;
  value: string;
  hint?: string;
}) {
  return (
    <div>
      <dt className="text-muted-foreground">{label}</dt>
      <dd className="font-mono tabular-nums">
        {value}
        {hint && <span className="ml-1 text-muted-foreground">({hint})</span>}
      </dd>
    </div>
  );
}
