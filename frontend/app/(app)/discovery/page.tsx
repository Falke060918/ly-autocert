"use client";

/**
 * Discovery 화면 — 2카드 구성.
 *
 * 카드 1 "검색 실행": 탭 [스캔 | 전 포트 스윕]. 대상 입력과 실행기 선택은 두 모드
 * 공용이라 탭 밖에 있고, 포트·타임아웃(스캔) / 속도·예상 규모(스윕)만 탭 안에 있다.
 * 카드 2 "작업 현황": 탭 [스캔 | 스윕 | 주기 재스캔]. 예전의 상하 3섹션(스캔 표·
 * 재스캔 카드·스윕 패널)을 스크롤 없이 한 카드에 모았다.
 *
 * 스캔 목록은 서버 목록 API(lib/scans.ts listScanJobs)가 정본이다. 그 API가 아직
 * 없는 서버(배포 순서상 구버전 백엔드)에서는 예전 방식 — 이 브라우저에서 시작한
 * Job id를 localStorage로 추적 — 로 폴백한다.
 */

import * as React from "react";
import Link from "next/link";

import { Alert, AlertDescription } from "@/components/ui/alert";
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
import { Progress } from "@/components/ui/progress";
import {
  Select,
  SelectContent,
  SelectItem,
  SelectTrigger,
  SelectValue,
} from "@/components/ui/select";
import { Tabs, TabsContent, TabsList, TabsTrigger } from "@/components/ui/tabs";
import {
  Table,
  TableBody,
  TableCell,
  TableHead,
  TableHeader,
  TableRow,
} from "@/components/ui/table";
import { canManageInventory, useAuth } from "@/lib/auth";
import { getScan, listSensors, type ScanJob, type Sensor } from "@/lib/api";
import { formatDateTime } from "@/lib/format";
import {
  cancelScan,
  duplicateScanJobId,
  getScanSuggestions,
  listScanJobs,
  type ScanSuggestionsResponse,
} from "@/lib/scans";
import {
  createScanWithPortSpec,
  elapsedLabel,
  getDiscoveryDefaults,
  listPortPresets,
  type DiscoveryDefaults,
  type PortPreset,
} from "@/lib/sweeps";

// 포트 입력·경과 시각 헬퍼는 스캔 폼과 스윕 폼이 함께 쓴다. 이 페이지가 패널을
// import하므로 공용 조각은 반대 방향(패널 → 페이지)이 아니라 패널 쪽에 둔다.
import { PortSpecField, SweepForm, SweepList, useTicker } from "./sweep-panel";
import { RescanCard } from "./rescan-card";
import { TargetField, targetLines } from "./target-field";

/** 이 브라우저에서 시작한 스캔 Job id 목록 — 서버 목록 API 폴백용. */
const STORAGE_KEY = "autocert.scan_job_ids";
const MAX_TRACKED = 20;
const POLL_MS = 2000;
/**
 * 이 시간을 넘도록 대기 중이면 "지연"으로 표시한다.
 * 센서 pull 주기는 5초, 백오프 최대 60초, 리스 TTL 90초이므로(manual/SENSOR.md)
 * 정상이라면 90초 안에는 수령된다 — 넘으면 센서 미가동을 의심할 구간이다.
 */
const STALE_WAIT_MS = 90_000;

/** 중복 안내로 포커스한 행의 강조를 이 시간 뒤에 걷는다. */
const HIGHLIGHT_MS = 5000;

function loadJobIds(): string[] {
  try {
    const raw = window.localStorage.getItem(STORAGE_KEY);
    const parsed: unknown = raw ? JSON.parse(raw) : [];
    return Array.isArray(parsed) ? parsed.filter((v) => typeof v === "string") : [];
  } catch {
    return [];
  }
}

function saveJobIds(ids: string[]) {
  window.localStorage.setItem(STORAGE_KEY, JSON.stringify(ids.slice(0, MAX_TRACKED)));
}

function isActiveJob(j: ScanJob): boolean {
  return j.status === "pending" || j.status === "leased" || j.status === "running";
}

type RunTab = "scan" | "sweep";
type StatusTab = "scans" | "sweeps" | "rescan";
/** unknown = 서버 목록 API 판정 전. local = 미가동이라 localStorage 폴백. */
type ScanListMode = "unknown" | "server" | "local";

export default function DiscoveryPage() {
  const { user } = useAuth();
  const canManage = canManageInventory(user);

  const [statusTab, setStatusTab] = React.useState<StatusTab>("scans");
  const [sweepReload, setSweepReload] = React.useState(0);

  const [sensors, setSensors] = React.useState<Sensor[]>([]);
  const [presets, setPresets] = React.useState<PortPreset[]>([]);
  const [defaults, setDefaults] = React.useState<DiscoveryDefaults | null>(null);
  const [suggestions, setSuggestions] =
    React.useState<ScanSuggestionsResponse | null>(null);

  // 스캔 목록 — 서버 목록이 정본, 실패 시 localStorage 추적으로 폴백.
  const [listMode, setListMode] = React.useState<ScanListMode>("unknown");
  const [serverJobs, setServerJobs] = React.useState<ScanJob[]>([]);
  const [jobIds, setJobIds] = React.useState<string[] | null>(null);
  const [jobs, setJobs] = React.useState<Record<string, ScanJob>>({});

  const [highlightId, setHighlightId] = React.useState<string | null>(null);
  const [cancelTarget, setCancelTarget] = React.useState<ScanJob | null>(null);
  const [cancelBusy, setCancelBusy] = React.useState(false);
  const [cancelError, setCancelError] = React.useState<string | null>(null);

  // 포트 프리셋의 단일 진실원천은 서버다 — 이름·개수를 화면에 하드코딩하지 않는다.
  // 스캔 폼과 스윕 폼이 같은 목록을 쓰므로 페이지가 한 번만 조회해 내려준다.
  React.useEffect(() => {
    let cancelled = false;
    listPortPresets()
      .then((res) => {
        if (!cancelled) setPresets(res.items);
      })
      .catch(() => {
        // 프리셋을 못 받으면 칩만 사라지고 자유 텍스트 입력은 그대로 쓸 수 있다.
      });
    return () => {
      cancelled = true;
    };
  }, []);

  // 스윕 기본값·상한은 서버 설정(SCAN_SWEEP_*)이 정본이다 — 화면은 상수를 들지
  // 않는다. 못 받으면 placeholder에서 숫자가 빠지고 미리보기가 비는 것으로 끝난다.
  React.useEffect(() => {
    let cancelled = false;
    getDiscoveryDefaults()
      .then((res) => {
        if (!cancelled) setDefaults(res);
      })
      .catch(() => {
        // 기본값을 못 받아도 폼 입력·제출은 그대로 된다(값은 서버가 정한다).
      });
    return () => {
      cancelled = true;
    };
  }, []);

  // 센서 목록은 실행기 선택과 대기 Job의 "누구를 기다리는가" 표시에 모두 쓴다.
  // 비활성 센서도 담아둔다 — 대기가 안 풀리는 원인이 바로 그것일 수 있다.
  React.useEffect(() => {
    let cancelled = false;
    listSensors()
      .then((res) => {
        if (!cancelled) setSensors(res.items);
      })
      .catch(() => {
        // 센서 목록을 못 가져와도 서버 직접 실행과 Job 조회는 가능하다.
      });
    return () => {
      cancelled = true;
    };
  }, []);

  // 대역 제안은 입력 보조일 뿐이다 — 못 받으면 칩 없이 직접 입력만 남는다.
  React.useEffect(() => {
    if (!canManage) return;
    let cancelled = false;
    getScanSuggestions()
      .then((res) => {
        if (!cancelled) setSuggestions(res);
      })
      .catch(() => {});
    return () => {
      cancelled = true;
    };
  }, [canManage]);

  const sensorById = React.useMemo(() => {
    const map: Record<string, Sensor> = {};
    sensors.forEach((s) => {
      map[s.id] = s;
    });
    return map;
  }, [sensors]);

  // 수동 발주만 본다 — 스윕 청크·주기 재스캔 Job까지 실으면 자동 발주가 표를
  // 도배해 방금 시작한 스캔이 밀려난다. 스윕·재스캔 현황은 각자의 탭이 정본이다.
  const refreshServerList = React.useCallback(
    () =>
      listScanJobs({ pageSize: MAX_TRACKED, origins: ["manual"] })
        .then((res) => setServerJobs(res.items))
        .catch(() => {
          // 일시 오류는 다음 폴링이 흡수한다 — 목록을 비우지 않는다.
        }),
    [],
  );

  // 최초 로드: 서버 목록 API를 먼저 시도하고, 실패하면 localStorage 방식으로 간다.
  React.useEffect(() => {
    let cancelled = false;
    listScanJobs({ pageSize: MAX_TRACKED, origins: ["manual"] })
      .then((res) => {
        if (cancelled) return;
        setServerJobs(res.items);
        setListMode("server");
      })
      .catch(() => {
        if (cancelled) return;
        setListMode("local");
        const ids = loadJobIds();
        setJobIds(ids);
        ids.forEach((id) => {
          getScan(id)
            .then((job) => setJobs((prev) => ({ ...prev, [id]: job })))
            .catch(() => {
              // 서버 재기동 등으로 사라진 Job은 표에서 제외한다.
              setJobIds((prev) => {
                const next = (prev ?? []).filter((v) => v !== id);
                saveJobIds(next);
                return next;
              });
            });
        });
      });
    return () => {
      cancelled = true;
    };
  }, []);

  // 서버 모드 폴링 — 진행 중인 Job이 있을 때만 목록을 다시 가져온다.
  React.useEffect(() => {
    if (listMode !== "server") return;
    if (!serverJobs.some(isActiveJob)) return;
    const timer = setInterval(() => {
      void refreshServerList();
    }, POLL_MS);
    return () => clearInterval(timer);
  }, [listMode, serverJobs, refreshServerList]);

  // 폴백(로컬) 모드 폴링 — 예전과 같은 Job 단건 폴링.
  React.useEffect(() => {
    if (listMode !== "local") return;
    const active = Object.values(jobs).filter(isActiveJob);
    if (active.length === 0) return;
    const timer = setInterval(() => {
      active.forEach((j) => {
        getScan(j.id)
          .then((job) => setJobs((prev) => ({ ...prev, [job.id]: job })))
          .catch(() => {});
      });
    }, POLL_MS);
    return () => clearInterval(timer);
  }, [listMode, jobs]);

  // localStorage에는 모드와 무관하게 기록한다 — 다음 방문이 폴백 모드로
  // 떨어져도 이 브라우저에서 시작한 스캔은 계속 보여야 한다.
  const addJob = React.useCallback((id: string) => {
    setJobIds((prev) => {
      const next = [id, ...(prev ?? []).filter((v) => v !== id)].slice(0, MAX_TRACKED);
      saveJobIds(next);
      return next;
    });
    getScan(id)
      .then((job) => setJobs((prev) => ({ ...prev, [id]: job })))
      .catch(() => {});
  }, []);

  /** 스캔 생성 직후 — 현황 탭을 스캔으로 돌리고 목록에 반영한다. */
  const handleScanCreated = React.useCallback(
    (jobId: string) => {
      addJob(jobId);
      if (listMode === "server") void refreshServerList();
      setStatusTab("scans");
    },
    [addJob, listMode, refreshServerList],
  );

  /** 중복 409 — 새 Job 대신 이미 돌고 있는 기존 Job으로 시선을 옮긴다. */
  const focusJob = React.useCallback(
    (jobId: string) => {
      setStatusTab("scans");
      if (listMode === "server") void refreshServerList();
      else addJob(jobId);
      setHighlightId(jobId);
    },
    [addJob, listMode, refreshServerList],
  );

  // 강조 행으로 스크롤하고 잠시 뒤 강조를 걷는다.
  React.useEffect(() => {
    if (!highlightId) return;
    document
      .getElementById(`scan-job-${highlightId}`)
      ?.scrollIntoView({ block: "nearest", behavior: "smooth" });
    const timer = setTimeout(() => setHighlightId(null), HIGHLIGHT_MS);
    return () => clearTimeout(timer);
  }, [highlightId]);

  const handleSweepCreated = React.useCallback(() => {
    setSweepReload((n) => n + 1);
    setStatusTab("sweeps");
  }, []);

  const confirmCancel = React.useCallback(async () => {
    if (!cancelTarget) return;
    setCancelBusy(true);
    setCancelError(null);
    try {
      const updated = await cancelScan(cancelTarget.id);
      setServerJobs((prev) => prev.map((j) => (j.id === updated.id ? updated : j)));
      setJobs((prev) => ({ ...prev, [updated.id]: updated }));
      setCancelTarget(null);
    } catch (err) {
      // 이미 종료된 Job의 409 포함 — 메시지를 그대로 보여주고 최신 상태를 다시 읽는다.
      setCancelError(
        err instanceof Error ? err.message : "스캔을 취소할 수 없습니다.",
      );
      setCancelTarget(null);
      if (listMode === "server") void refreshServerList();
      else
        getScan(cancelTarget.id)
          .then((job) => setJobs((prev) => ({ ...prev, [job.id]: job })))
          .catch(() => {});
    } finally {
      setCancelBusy(false);
    }
  }, [cancelTarget, listMode, refreshServerList]);

  // 렌더용 행 — 서버 모드는 서버 목록 순서, 폴백 모드는 localStorage 순서.
  const scanRows = React.useMemo<{ id: string; job?: ScanJob }[] | null>(() => {
    if (listMode === "server") return serverJobs.map((j) => ({ id: j.id, job: j }));
    if (listMode === "local") return (jobIds ?? []).map((id) => ({ id, job: jobs[id] }));
    return null;
  }, [listMode, serverJobs, jobIds, jobs]);

  return (
    <div className="mx-auto max-w-[1400px] space-y-4">
      {/* 제목은 화면에서 걷어냈지만 문서 구조(h1)는 스크린리더용으로 남긴다. */}
      <h1 className="sr-only">Discovery</h1>

      <div className="grid items-start gap-4 lg:grid-cols-[340px_minmax(0,1fr)]">
        <RunCard
          canManage={canManage}
          sensors={sensors}
          presets={presets}
          defaults={defaults}
          suggestions={suggestions}
          onScanCreated={handleScanCreated}
          onScanDuplicate={focusJob}
          onSweepCreated={handleSweepCreated}
        />

        <Card className="gap-0 overflow-hidden py-0">
          <Tabs
            value={statusTab}
            onValueChange={(v) => setStatusTab(v as StatusTab)}
            className="gap-0"
          >
            <div className="flex flex-wrap items-center justify-between gap-2 border-b px-4 py-2">
              <h2 className="text-sm font-semibold tracking-tight">작업 현황</h2>
              <TabsList>
                <TabsTrigger value="scans">스캔</TabsTrigger>
                <TabsTrigger value="sweeps">스윕</TabsTrigger>
                <TabsTrigger value="rescan">주기 재스캔</TabsTrigger>
              </TabsList>
            </div>

            <TabsContent value="scans">
              {listMode === "local" && (
                <p className="border-b px-4 py-2 text-xs text-muted-foreground">
                  서버 목록을 불러올 수 없어 이 브라우저에서 시작한 스캔만
                  표시합니다.
                </p>
              )}
              {cancelError && (
                <div className="px-4 pt-3">
                  <Alert variant="destructive">
                    <AlertDescription>{cancelError}</AlertDescription>
                  </Alert>
                </div>
              )}
              {scanRows == null ? (
                <p className="px-4 py-12 text-center text-sm text-muted-foreground">
                  스캔 목록을 불러오는 중…
                </p>
              ) : scanRows.length === 0 ? (
                <p className="px-4 py-12 text-center text-sm text-muted-foreground">
                  아직 실행한 스캔이 없습니다.
                  {canManage && " 검색 실행에서 대상 대역을 입력해 시작하십시오."}
                </p>
              ) : (
                <Table>
                  <TableHeader>
                    <TableRow>
                      <TableHead>대상</TableHead>
                      <TableHead className="w-44">상태</TableHead>
                      <TableHead className="text-right">엔드포인트</TableHead>
                      <TableHead className="text-right">발견</TableHead>
                      <TableHead className="text-right">신규</TableHead>
                      <TableHead>시작</TableHead>
                      {canManage && <TableHead className="w-14" />}
                    </TableRow>
                  </TableHeader>
                  <TableBody>
                    {scanRows.map(({ id, job }) => (
                      <ScanJobRow
                        key={id}
                        job={job}
                        sensorById={sensorById}
                        canManage={canManage}
                        highlighted={id === highlightId}
                        onCancelRequest={setCancelTarget}
                      />
                    ))}
                  </TableBody>
                </Table>
              )}
            </TabsContent>

            <TabsContent value="sweeps">
              <SweepList
                sensors={sensors}
                canManage={canManage}
                reloadToken={sweepReload}
              />
            </TabsContent>

            <TabsContent value="rescan">
              <RescanCard canManage={canManage} />
            </TabsContent>
          </Tabs>
        </Card>
      </div>

      <Dialog
        open={cancelTarget != null}
        onOpenChange={(open) => {
          if (!open) setCancelTarget(null);
        }}
      >
        <DialogContent>
          <DialogHeader>
            <DialogTitle>스캔을 취소하시겠습니까?</DialogTitle>
            <DialogDescription>
              대상{" "}
              <span className="font-mono">
                {cancelTarget?.targets.join(", ")}
              </span>{" "}
              스캔을 멈춥니다. 이미 수집된 인증서는 인벤토리에 그대로 남습니다.
            </DialogDescription>
          </DialogHeader>
          <DialogFooter>
            <Button
              type="button"
              variant="outline"
              disabled={cancelBusy}
              onClick={() => setCancelTarget(null)}
            >
              계속 실행
            </Button>
            <Button
              type="button"
              variant="destructive"
              disabled={cancelBusy}
              onClick={() => void confirmCancel()}
            >
              {cancelBusy ? "취소 중…" : "스캔 취소"}
            </Button>
          </DialogFooter>
        </DialogContent>
      </Dialog>
    </div>
  );
}

// ── 검색 실행 카드 ────────────────────────────────────────────────────────

/** 실행기 선택에서 "서버 직접"을 뜻하는 값 — SelectItem은 빈 문자열을 못 쓴다. */
const EXECUTOR_SERVER = "server";

function RunCard({
  canManage,
  sensors,
  presets,
  defaults,
  suggestions,
  onScanCreated,
  onScanDuplicate,
  onSweepCreated,
}: {
  canManage: boolean;
  sensors: Sensor[];
  presets: PortPreset[];
  /** 서버가 정한 스윕 기본값·상한. 아직 못 받았으면 null이다. */
  defaults: DiscoveryDefaults | null;
  suggestions: ScanSuggestionsResponse | null;
  onScanCreated: (jobId: string) => void;
  onScanDuplicate: (jobId: string) => void;
  onSweepCreated: () => void;
}) {
  const [runTab, setRunTab] = React.useState<RunTab>("scan");
  const [targets, setTargets] = React.useState("");
  const [executor, setExecutor] = React.useState(EXECUTOR_SERVER);

  const targetList = React.useMemo(() => targetLines(targets), [targets]);
  const sensorId = executor === EXECUTOR_SERVER ? null : executor;

  if (!canManage) {
    return (
      <Card className="gap-0 py-0">
        <div className="border-b px-4 py-3">
          <h2 className="text-sm font-semibold tracking-tight">검색 실행</h2>
        </div>
        <p className="p-4 text-sm text-muted-foreground">
          스캔·스윕 시작 권한이 없습니다. 시스템 관리자·PKI 관리자·운영자만
          검색을 시작할 수 있습니다.
        </p>
      </Card>
    );
  }

  return (
    <Card className="gap-0 py-0">
      <div className="border-b px-4 py-3">
        <h2 className="text-sm font-semibold tracking-tight">검색 실행</h2>
      </div>
      <Tabs
        value={runTab}
        onValueChange={(v) => setRunTab(v as RunTab)}
        className="gap-4 p-4"
      >
        <div className="space-y-1.5">
          <TabsList className="w-full">
            <TabsTrigger value="scan">스캔</TabsTrigger>
            <TabsTrigger value="sweep">전 포트 스윕</TabsTrigger>
          </TabsList>
          <p className="text-xs text-muted-foreground">
            {runTab === "scan"
              ? "단발 정밀 검색 — 지정한 대상·포트를 지금 즉시 확인합니다."
              : "저속 전수 검색 — 며칠에 걸쳐 낮은 속도로 모든 포트를 훑습니다."}
          </p>
        </div>

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

        {/* forceMount + hidden — 탭을 오가도 입력한 포트·속도 값이 남아 있어야 한다. */}
        <TabsContent value="scan" forceMount className="data-[state=inactive]:hidden">
          <ScanFormFields
            presets={presets}
            targetList={targetList}
            sensorId={sensorId}
            onCreated={onScanCreated}
            onDuplicate={onScanDuplicate}
          />
        </TabsContent>
        <TabsContent value="sweep" forceMount className="data-[state=inactive]:hidden">
          <SweepForm
            presets={presets}
            defaults={defaults}
            targetList={targetList}
            sensorId={sensorId}
            onCreated={onSweepCreated}
          />
        </TabsContent>
      </Tabs>
    </Card>
  );
}

function ScanFormFields({
  presets,
  targetList,
  sensorId,
  onCreated,
  onDuplicate,
}: {
  presets: PortPreset[];
  targetList: string[];
  sensorId: string | null;
  onCreated: (jobId: string) => void;
  onDuplicate: (jobId: string) => void;
}) {
  /** 기본값은 common 프리셋이다(443 단일에서 바뀌었다 — 설계 3.3). */
  const [portSpec, setPortSpec] = React.useState("common");
  const [timeoutMs, setTimeoutMs] = React.useState("");
  const [concurrency, setConcurrency] = React.useState("");
  const [error, setError] = React.useState<string | null>(null);
  const [duplicateId, setDuplicateId] = React.useState<string | null>(null);
  const [busy, setBusy] = React.useState(false);

  async function submit(e: React.FormEvent) {
    e.preventDefault();
    setError(null);
    setDuplicateId(null);

    if (targetList.length === 0) {
      setError("스캔 대상(IP 또는 CIDR)을 한 줄에 하나씩 입력하십시오.");
      return;
    }

    setBusy(true);
    try {
      // 포트는 원문 그대로 보낸다. 클라이언트에서 파싱하면 "8443-9443"이 조용히
      // 8443 하나로 잘려 사용자가 요청하지 않은 스캔이 돈다(설계 9.3의 버그).
      const req: Parameters<typeof createScanWithPortSpec>[0] = { targets: targetList };
      if (portSpec.trim()) req.port_spec = portSpec.trim();
      if (timeoutMs.trim()) req.timeout_ms = Number.parseInt(timeoutMs, 10);
      if (concurrency.trim()) req.concurrency = Number.parseInt(concurrency, 10);
      if (sensorId) req.sensor_id = sensorId;
      const accepted = await createScanWithPortSpec(req);
      onCreated(accepted.job_id);
    } catch (err) {
      // 중복 409는 실패가 아니다 — 이미 돌고 있는 기존 Job으로 안내한다.
      const dupId = duplicateScanJobId(err);
      if (dupId) {
        setDuplicateId(dupId);
        onDuplicate(dupId);
      } else {
        setError(err instanceof Error ? err.message : "스캔 생성에 실패했습니다.");
      }
    } finally {
      setBusy(false);
    }
  }

  return (
    <form onSubmit={submit} className="space-y-4">
      <PortSpecField
        id="scan-ports"
        value={portSpec}
        onChange={setPortSpec}
        presets={presets}
        singleScan
      />
      <div className="grid grid-cols-2 gap-3">
        <div className="space-y-2">
          <Label htmlFor="scan-timeout">타임아웃 (ms)</Label>
          <Input
            id="scan-timeout"
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
          <Label htmlFor="scan-concurrency">동시 연결 수</Label>
          <Input
            id="scan-concurrency"
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
      {duplicateId && (
        <Alert className="border-amber-200 bg-amber-50 text-amber-800">
          <AlertDescription className="text-amber-800">
            이미 같은 대상·포트의 스캔이 대기·실행 중입니다 — 작업 현황에서 그
            스캔을 강조해 표시했습니다.
            <Button
              type="button"
              size="xs"
              variant="outline"
              className="mt-1.5"
              onClick={() => onDuplicate(duplicateId)}
            >
              기존 스캔 보기
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
        {busy ? "스캔 생성 중…" : "스캔 시작"}
      </Button>
    </form>
  );
}

// ── 스캔 Job 행 ───────────────────────────────────────────────────────────

function ScanJobRow({
  job,
  sensorById,
  canManage,
  highlighted,
  onCancelRequest,
}: {
  job?: ScanJob;
  sensorById: Record<string, Sensor>;
  canManage: boolean;
  highlighted: boolean;
  onCancelRequest: (job: ScanJob) => void;
}) {
  if (!job) {
    return (
      <TableRow>
        <TableCell colSpan={canManage ? 7 : 6} className="text-sm text-muted-foreground">
          상태 조회 중…
        </TableCell>
      </TableRow>
    );
  }
  const pct =
    job.total_endpoints > 0
      ? Math.round((job.scanned_endpoints / job.total_endpoints) * 100)
      : 0;
  const waiting = job.status === "pending";
  return (
    <TableRow
      id={`scan-job-${job.id}`}
      className={highlighted ? "bg-accent/60 transition-colors" : undefined}
    >
      <TableCell className="max-w-52 truncate font-mono text-xs">
        {job.targets.join(", ")}
        <div className="text-muted-foreground/70">
          포트 {job.ports.join(", ")}
        </div>
      </TableCell>
      <TableCell>
        {waiting ? (
          // 아직 아무도 집어가지 않은 Job — 진행률 바로 그리면 "0%"가 실행 중처럼 보인다.
          <PendingStatus job={job} sensor={sensorOf(job, sensorById)} />
        ) : job.status === "leased" ? (
          // 센서가 수령했지만 아직 실행 보고 전 — 진행 중이다.
          <div className="space-y-1">
            <Badge className="border border-amber-200 bg-amber-50 text-amber-700">
              수령됨
            </Badge>
            <ExecutorLine
              text={`${executorName(job, sensorOf(job, sensorById))} 실행 준비 중`}
            />
          </div>
        ) : job.status === "running" ? (
          <div className="flex items-center gap-2">
            <Progress value={pct} className="h-1.5 flex-1" />
            <span className="font-mono text-xs text-muted-foreground tabular-nums">
              {pct}%
            </span>
          </div>
        ) : job.status === "completed" ? (
          <Badge variant="outline">완료</Badge>
        ) : job.status === "canceled" ? (
          // 취소는 발주자의 결정이지 오류가 아니다 — 실패 계열로 칠하지 않는다.
          <Badge variant="secondary">취소됨</Badge>
        ) : (
          <Badge className="border border-red-200 bg-red-50 text-red-700" title={job.error ?? undefined}>
            실패
          </Badge>
        )}
      </TableCell>
      <TableCell className="text-right font-mono text-xs tabular-nums">
        {waiting && job.total_endpoints === 0 ? (
          // 센서 위임 Job은 센서가 대상을 펼치기 전까지 개수를 모른다 — 0/0은 오해를 부른다.
          <span className="text-muted-foreground">—</span>
        ) : (
          `${job.scanned_endpoints} / ${job.total_endpoints}`
        )}
      </TableCell>
      <TableCell className="text-right font-mono text-xs tabular-nums">
        {job.certificates_found}
      </TableCell>
      <TableCell className="text-right font-mono text-xs tabular-nums">
        {job.status === "completed" && job.certificates_new > 0 ? (
          <Link
            href="/certificates?sort=created_at&order=desc"
            className="font-medium hover:underline hover:underline-offset-3"
          >
            {job.certificates_new}
          </Link>
        ) : (
          job.certificates_new
        )}
      </TableCell>
      <TableCell className="font-mono text-xs tabular-nums">
        {job.started_at ? formatDateTime(job.started_at) : formatDateTime(job.created_at)}
      </TableCell>
      {canManage && (
        <TableCell className="text-right">
          {/*
            취소는 수동 발주만 — 스윕 청크는 스윕 API로만 제어(409 sweep_chunk)
            하고 재스캔 Job은 스케줄러가 곧 재발주하므로 개별 취소가 무의미하다.
          */}
          {isActiveJob(job) && job.origin !== "sweep" && job.origin !== "rescan" && (
            <Button
              type="button"
              size="xs"
              variant="outline"
              onClick={() => onCancelRequest(job)}
            >
              취소
            </Button>
          )}
        </TableCell>
      )}
    </TableRow>
  );
}

function sensorOf(job: ScanJob, sensorById: Record<string, Sensor>) {
  return job.sensor_id ? sensorById[job.sensor_id] : undefined;
}

/** 이 Job을 집어가야 할 주체의 이름. 센서 목록을 못 받았으면 id 앞자리로 대신한다. */
function executorName(job: ScanJob, sensor?: Sensor): string {
  if (job.executor_type !== "sensor") return "서버";
  if (sensor) return `센서 “${sensor.name}”`;
  return job.sensor_id ? `센서 ${job.sensor_id.slice(0, 8)}` : "센서";
}

/** 상태 배지 아래 한 줄 — 누구를 기다리는지, 얼마나 기다렸는지. */
function ExecutorLine({ text, tone }: { text: string; tone?: "warn" }) {
  return (
    <div
      title={text}
      className={`truncate text-[11px] ${
        tone === "warn" ? "text-amber-700" : "text-muted-foreground"
      }`}
    >
      {text}
    </div>
  );
}

/**
 * 대기(pending) 상태 표시. 진행률 바 대신 배지로 "아직 시작 안 됨"을 못 박고,
 * 누구를 기다리는지와 경과 시간을 함께 보여준다 — 센서가 죽어 Job이 멈춘 경우를
 * 화면에서 바로 진단하기 위한 단서다 (manual/SENSOR.md 문제해결).
 */
function PendingStatus({ job, sensor }: { job: ScanJob; sensor?: Sensor }) {
  const now = useTicker(1000);
  const waitedMs = now - new Date(job.created_at).getTime();
  const sensorBlocked =
    job.executor_type === "sensor" && sensor != null && sensor.status !== "active";
  const overdue = sensorBlocked || waitedMs > STALE_WAIT_MS;
  const who = executorName(job, sensor);
  const detail = sensorBlocked
    ? `${who} ${sensor!.status === "disabled" ? "비활성화됨" : "등록 대기"} — 수령 불가`
    : `${who} 수령 대기 · ${elapsedLabel(job.created_at, now)}`;

  return (
    <div className="space-y-1">
      {overdue ? (
        <Badge className="border border-amber-200 bg-amber-50 text-amber-700">
          대기 중
        </Badge>
      ) : (
        <Badge variant="secondary">대기 중</Badge>
      )}
      <ExecutorLine text={detail} tone={overdue ? "warn" : undefined} />
    </div>
  );
}
