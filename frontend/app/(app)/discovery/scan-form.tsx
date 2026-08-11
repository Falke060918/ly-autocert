"use client";

/**
 * 검색 실행 — **단일 폼**이다.
 *
 * 예전에는 [스캔 | 전 포트 스윕] 두 탭이었고 사용자가 "이 요청이 단발인지 스윕인지"를
 * 먼저 골라야 했다. 지금은 규모를 보고 **서버가 정한다**(planScan의 자동 승격) —
 * 화면은 무엇이 만들어질지 미리보기로 알려주기만 한다. 고를 것이 없어졌으므로
 * 탭도 없앴다.
 *
 * 대상·포트의 파싱과 규모 계산은 전부 서버(POST /discovery/scans/preview)가 한다.
 */

import * as React from "react";

import { Alert, AlertDescription } from "@/components/ui/alert";
import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import { Card } from "@/components/ui/card";
import { Input } from "@/components/ui/input";
import { Label } from "@/components/ui/label";
import {
  Select,
  SelectContent,
  SelectItem,
  SelectTrigger,
  SelectValue,
} from "@/components/ui/select";
import { ApiError, type ScanAccepted, type Sensor } from "@/lib/api";
import { duplicateScanJobId, type ScanSuggestionsResponse } from "@/lib/scans";
import {
  createScanWithPortSpec,
  formatCount,
  formatDuration,
  previewScan,
  type DiscoveryDefaults,
  type PortPreset,
  type ScanFormRequest,
  type ScanPreview,
} from "@/lib/sweeps";

import { PortSpecField } from "./port-field";
import { TargetField, targetLines } from "./target-field";

/** 실행기 선택에서 "서버 직접"을 뜻하는 값 — SelectItem은 빈 문자열을 못 쓴다. */
const EXECUTOR_SERVER = "server";

/** 입력이 멎은 뒤 미리보기를 요청하기까지 기다리는 시간. */
const PREVIEW_DEBOUNCE_MS = 400;

/** 미리보기 결과는 **어떤 입력으로 계산했는지**와 함께 들고 있는다. */
interface PreviewState {
  key: string;
  data?: ScanPreview;
  error?: string;
}

export function ScanForm({
  canManage,
  sensors,
  presets,
  defaults,
  suggestions,
  onCreated,
  onDuplicate,
}: {
  canManage: boolean;
  sensors: Sensor[];
  presets: PortPreset[];
  /** 서버가 정한 기본값·상한. 아직 못 받았으면 null이다. */
  defaults: DiscoveryDefaults | null;
  suggestions: ScanSuggestionsResponse | null;
  onCreated: (accepted: ScanAccepted) => void;
  onDuplicate: (jobId: string) => void;
}) {
  const [targets, setTargets] = React.useState("");
  const [executor, setExecutor] = React.useState(EXECUTOR_SERVER);
  const [portSpec, setPortSpec] = React.useState("common");
  const [timeoutMs, setTimeoutMs] = React.useState("");
  const [concurrency, setConcurrency] = React.useState("");

  const [preview, setPreview] = React.useState<PreviewState | null>(null);
  const [error, setError] = React.useState<string | null>(null);
  const [tooLong, setTooLong] = React.useState<string | null>(null);
  const [duplicateId, setDuplicateId] = React.useState<string | null>(null);
  const [busy, setBusy] = React.useState(false);

  const targetList = React.useMemo(() => targetLines(targets), [targets]);
  const sensorId = executor === EXECUTOR_SERVER ? null : executor;
  const spec = portSpec.trim();
  /** 미리보기 대상 입력의 지문 — 결과가 이 입력의 것인지 판정한다. */
  const previewKey = React.useMemo(
    () => JSON.stringify([targetList, spec]),
    [targetList, spec],
  );

  // 입력이 멎으면 서버에 규모를 물어본다. setState는 전부 콜백 안에서만 부른다
  // (효과 본문에서 직접 부르면 react-hooks/set-state-in-effect에 걸린다).
  React.useEffect(() => {
    if (!canManage || targetList.length === 0) return;
    let cancelled = false;
    const timer = setTimeout(() => {
      const req: ScanFormRequest = { targets: targetList };
      if (spec) req.port_spec = spec;
      previewScan(req)
        .then((data) => {
          if (!cancelled) setPreview({ key: previewKey, data });
        })
        .catch((err: unknown) => {
          // 입력이 아직 문장 중간일 뿐일 수도 있다 — 제출을 막지 않고 사유만 적는다.
          if (!cancelled) {
            setPreview({
              key: previewKey,
              error:
                err instanceof Error ? err.message : "규모를 계산할 수 없습니다.",
            });
          }
        });
    }, PREVIEW_DEBOUNCE_MS);
    return () => {
      cancelled = true;
      clearTimeout(timer);
    };
  }, [canManage, targetList, spec, previewKey]);

  /** 지금 입력으로 계산된 미리보기만 쓴다(입력이 바뀌면 즉시 무효다). */
  const current = preview?.key === previewKey ? preview : null;
  const plan = current?.data ?? null;

  async function submit(e: React.FormEvent) {
    e.preventDefault();
    setError(null);
    setTooLong(null);
    setDuplicateId(null);

    if (targetList.length === 0) {
      setError("검색 대상(IP 또는 CIDR)을 한 줄에 하나씩 입력하십시오.");
      return;
    }

    setBusy(true);
    try {
      // 포트는 원문 그대로 보낸다. 클라이언트에서 파싱하면 "8443-9443"이 조용히
      // 8443 하나로 잘려 사용자가 요청하지 않은 스캔이 돈다(설계 9.3의 버그).
      const req: ScanFormRequest = { targets: targetList };
      if (spec) req.port_spec = spec;
      if (timeoutMs.trim()) req.timeout_ms = Number.parseInt(timeoutMs, 10);
      if (concurrency.trim()) req.concurrency = Number.parseInt(concurrency, 10);
      if (sensorId) req.sensor_id = sensorId;
      onCreated(await createScanWithPortSpec(req));
    } catch (err) {
      // 중복 409는 실패가 아니다 — 이미 돌고 있는 기존 Job으로 안내한다.
      const dupId = duplicateScanJobId(err);
      if (dupId) {
        setDuplicateId(dupId);
        onDuplicate(dupId);
      } else if (err instanceof ApiError && err.code === "sweep_too_long") {
        // 422는 서버가 **실제 소요 일수**를 담아 보낸다 — 문구를 다시 쓰지 않고
        // 그대로 노출해야 얼마나 줄여야 할지 알 수 있다.
        setTooLong(err.message);
      } else {
        setError(err instanceof Error ? err.message : "검색을 시작할 수 없습니다.");
      }
    } finally {
      setBusy(false);
    }
  }

  if (!canManage) {
    return (
      <Card className="gap-0 py-0">
        <div className="border-b px-4 py-3">
          <h2 className="text-sm font-semibold tracking-tight">검색 실행</h2>
        </div>
        <p className="p-4 text-sm text-muted-foreground">
          검색 시작 권한이 없습니다. 시스템 관리자·PKI 관리자·운영자만 검색을
          시작할 수 있습니다.
        </p>
      </Card>
    );
  }

  return (
    <Card className="gap-0 py-0">
      <div className="border-b px-4 py-3">
        <h2 className="text-sm font-semibold tracking-tight">검색 실행</h2>
        <p className="mt-0.5 text-xs text-muted-foreground">
          대상과 포트를 정하면 규모에 맞는 방식으로 서버가 실행합니다.
        </p>
      </div>

      <form onSubmit={submit} className="space-y-4 p-4">
        <TargetField
          id="run-targets"
          value={targets}
          onChange={setTargets}
          suggestions={suggestions}
        />

        <div className="space-y-2">
          <Label htmlFor="run-executor">실행기</Label>
          <Select value={executor} onValueChange={setExecutor}>
            <SelectTrigger id="run-executor" className="w-full">
              <SelectValue />
            </SelectTrigger>
            <SelectContent>
              <SelectItem value={EXECUTOR_SERVER}>서버 직접</SelectItem>
              {sensors
                .filter((s) => s.status === "active")
                .map((s) => (
                  <SelectItem key={s.id} value={s.id}>
                    센서 · {s.name}
                  </SelectItem>
                ))}
            </SelectContent>
          </Select>
          <p className="text-xs text-muted-foreground">
            서버가 도달할 수 없는 세그먼트는 그곳에 배치한 센서를 선택하십시오.
          </p>
        </div>

        <PortSpecField
          id="run-ports"
          value={portSpec}
          onChange={setPortSpec}
          presets={presets}
        />

        <div className="grid grid-cols-2 gap-3">
          <div className="space-y-2">
            <Label htmlFor="run-timeout">타임아웃 (ms)</Label>
            <Input
              id="run-timeout"
              type="number"
              min={100}
              max={30000}
              className="font-mono text-xs"
              placeholder="기본값"
              value={timeoutMs}
              onChange={(e) => setTimeoutMs(e.target.value)}
            />
          </div>
          <div className="space-y-2">
            <Label htmlFor="run-concurrency">동시 연결 수</Label>
            <Input
              id="run-concurrency"
              type="number"
              min={1}
              max={256}
              className="font-mono text-xs"
              placeholder="기본값"
              value={concurrency}
              onChange={(e) => setConcurrency(e.target.value)}
            />
          </div>
        </div>
        {plan?.promoted && (
          <p className="text-xs text-muted-foreground">
            전 포트 검색으로 전환되면 타임아웃·동시 연결 수 대신 서버가 정한
            저속 설정({formatCount(plan.rate_pps)} pps)으로 실행됩니다.
          </p>
        )}

        <PlanBox
          plan={plan}
          error={current?.error ?? null}
          pending={targetList.length > 0 && current == null}
          maxEndpoints={defaults?.limits.max_endpoints ?? null}
        />

        {tooLong && (
          <Alert variant="warn">
            <AlertDescription>
              {tooLong}
              <span className="mt-1 block">
                대역을 줄이거나(예: /24 하나씩) 포트 사양을 좁혀 다시
                시도하십시오.
              </span>
            </AlertDescription>
          </Alert>
        )}
        {duplicateId && (
          <Alert variant="warn">
            <AlertDescription>
              이미 같은 대상·포트의 검색이 대기·실행 중입니다 — 작업 목록에서 그
              작업을 강조해 표시했습니다.
              <Button
                type="button"
                size="xs"
                variant="outline"
                className="mt-1.5"
                onClick={() => onDuplicate(duplicateId)}
              >
                기존 작업 보기
              </Button>
            </AlertDescription>
          </Alert>
        )}
        {error && (
          <Alert variant="destructive">
            <AlertDescription>{error}</AlertDescription>
          </Alert>
        )}

        <Button type="submit" className="w-full" disabled={busy}>
          {busy
            ? "시작하는 중…"
            : plan?.promoted
              ? "전 포트 검색 시작"
              : "검색 시작"}
        </Button>
      </form>
    </Card>
  );
}

/**
 * 시작 전 규모 미리보기.
 *
 * 422로 거부당한 뒤에야 규모를 아는 일이 없도록 총 엔드포인트·예상 소요와
 * **무엇이 만들어질지**(단발 / 전 포트 스윕)를 먼저 보여준다. 숫자는 전부 서버가
 * 준 값이며 화면에서 다시 계산하지 않는다.
 */
function PlanBox({
  plan,
  error,
  pending,
  maxEndpoints,
}: {
  plan: ScanPreview | null;
  error: string | null;
  pending: boolean;
  /** 요청이 넘을 수 없는 엔드포인트 상한. 못 받았으면 null이라 적지 않는다. */
  maxEndpoints: number | null;
}) {
  return (
    <div className="rounded-md border bg-muted/40 p-3">
      <div className="flex items-baseline justify-between gap-2">
        <span className="text-xs font-medium">예상 규모</span>
        {plan && (
          <Badge variant={plan.promoted ? "secondary" : "outline"}>
            {plan.promoted ? "전 포트 스윕" : "단발 스캔"}
          </Badge>
        )}
      </div>

      {error ? (
        <p className="mt-1 text-xs text-warn">{error}</p>
      ) : pending ? (
        <p className="mt-1 text-xs text-muted-foreground">규모를 확인하는 중…</p>
      ) : !plan ? (
        <p className="mt-1 text-xs text-muted-foreground">
          대상과 포트를 입력하면 총 엔드포인트 수와 예상 소요를 서버가
          계산합니다.
          {maxEndpoints != null &&
            ` 한 요청의 상한은 ${formatCount(maxEndpoints)} 엔드포인트입니다.`}
        </p>
      ) : (
        <dl className="mt-2 space-y-1 text-xs">
          <div className="flex justify-between gap-2">
            <dt className="text-muted-foreground">엔드포인트</dt>
            <dd className="font-mono tabular-nums">
              {formatCount(plan.ip_count)} IP × {formatCount(plan.port_count)} 포트
              = {formatCount(plan.total_endpoints)}
            </dd>
          </div>
          <div className="flex justify-between gap-2">
            <dt className="text-muted-foreground">예상 소요</dt>
            <dd
              className={`font-mono tabular-nums ${plan.too_long ? "text-warn" : ""}`}
            >
              {formatDuration(plan.estimated_seconds)}
            </dd>
          </div>
          {plan.promoted && (
            <div className="flex justify-between gap-2">
              <dt className="text-muted-foreground">청크</dt>
              <dd className="font-mono tabular-nums">
                {formatCount(plan.total_chunks)}개
              </dd>
            </div>
          )}
          {plan.promoted && !plan.too_long && (
            <p className="pt-1 text-muted-foreground">
              한 번에 돌리기엔 커서(임계{" "}
              {formatCount(plan.promotion_threshold)} 엔드포인트) 낮은 속도로
              며칠에 걸쳐 나눠 도는 전 포트 스윕으로 실행됩니다.
            </p>
          )}
          {plan.too_long && (
            <p className="pt-1 text-warn">
              {plan.message ??
                `서버 상한(${plan.max_days}일)을 넘습니다 — 이대로 시작하면 거부됩니다.`}
            </p>
          )}
        </dl>
      )}
    </div>
  );
}
