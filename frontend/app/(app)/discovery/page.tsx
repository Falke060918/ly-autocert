"use client";

import * as React from "react";
import Link from "next/link";

import { Alert, AlertDescription } from "@/components/ui/alert";
import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import { Card } from "@/components/ui/card";
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
import { Textarea } from "@/components/ui/textarea";
import {
  Table,
  TableBody,
  TableCell,
  TableHead,
  TableHeader,
  TableRow,
} from "@/components/ui/table";
import { canManageInventory, useAuth } from "@/lib/auth";
import { createScan, getScan, listSensors, type ScanJob, type Sensor } from "@/lib/api";
import { formatDateTime } from "@/lib/format";

/** 이 브라우저에서 시작한 스캔 Job id 목록 (서버에는 목록 API가 없다 — Phase 1 범위). */
const STORAGE_KEY = "autocert.scan_job_ids";
const MAX_TRACKED = 20;
const POLL_MS = 2000;
/**
 * 이 시간을 넘도록 대기 중이면 "지연"으로 표시한다.
 * 센서 pull 주기는 5초, 백오프 최대 60초, 리스 TTL 90초이므로(manual/SENSOR.md)
 * 정상이라면 90초 안에는 수령된다 — 넘으면 센서 미가동을 의심할 구간이다.
 */
const STALE_WAIT_MS = 90_000;

/** "3분째" / "12초째" — 대기가 얼마나 길어졌는지 알려주는 경과 표기. */
function elapsedLabel(fromISO: string, now: number): string {
  const sec = Math.max(0, Math.floor((now - new Date(fromISO).getTime()) / 1000));
  if (sec < 60) return `${sec}초째`;
  const min = Math.floor(sec / 60);
  if (min < 60) return `${min}분째`;
  return `${Math.floor(min / 60)}시간 ${min % 60}분째`;
}

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

export default function DiscoveryPage() {
  const { user } = useAuth();
  const canManage = canManageInventory(user);

  const [jobIds, setJobIds] = React.useState<string[] | null>(null);
  const [jobs, setJobs] = React.useState<Record<string, ScanJob>>({});
  const [sensors, setSensors] = React.useState<Sensor[]>([]);

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

  const sensorById = React.useMemo(() => {
    const map: Record<string, Sensor> = {};
    sensors.forEach((s) => {
      map[s.id] = s;
    });
    return map;
  }, [sensors]);

  // 최초 로드: 저장된 job id들의 현재 상태를 한 번 가져온다.
  // localStorage는 클라이언트 전용이라 마운트 후 비동기로 읽는다 (SSR 불일치 방지).
  React.useEffect(() => {
    const timer = setTimeout(() => {
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
    }, 0);
    return () => clearTimeout(timer);
  }, []);

  // 진행 중인 Job 폴링.
  React.useEffect(() => {
    const active = Object.values(jobs).filter(
      (j) => j.status === "pending" || j.status === "leased" || j.status === "running",
    );
    if (active.length === 0) return;
    const timer = setInterval(() => {
      active.forEach((j) => {
        getScan(j.id)
          .then((job) => setJobs((prev) => ({ ...prev, [job.id]: job })))
          .catch(() => {});
      });
    }, POLL_MS);
    return () => clearInterval(timer);
  }, [jobs]);

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

  return (
    <div className="mx-auto max-w-[1400px] space-y-4">
      <div>
        <h1 className="text-lg font-semibold tracking-tight">Discovery</h1>
        <p className="text-[13px] text-muted-foreground">
          에이전트 없이 네트워크 TLS 스캔으로 인증서를 찾아 인벤토리에 등록합니다.
        </p>
      </div>

      <div className="grid items-start gap-4 lg:grid-cols-[320px_minmax(0,1fr)]">
        {canManage ? (
          <ScanForm sensors={sensors} onCreated={addJob} />
        ) : (
          <Card className="gap-0 py-0">
            <div className="border-b px-4 py-3">
              <h2 className="text-sm font-semibold tracking-tight">스캔 시작</h2>
            </div>
            <p className="p-4 text-sm text-muted-foreground">
              스캔 시작 권한이 없습니다. 시스템 관리자·PKI 관리자·운영자만 스캔을
              만들 수 있습니다.
            </p>
          </Card>
        )}

        <Card className="gap-0 overflow-hidden py-0">
          <div className="flex items-baseline gap-2 border-b px-4 py-3">
            <h2 className="text-sm font-semibold tracking-tight">스캔 작업</h2>
            <span className="text-xs text-muted-foreground">
              이 브라우저에서 시작한 스캔만 표시됩니다
            </span>
          </div>
          {jobIds != null && jobIds.length === 0 ? (
            <p className="px-4 py-12 text-center text-sm text-muted-foreground">
              아직 실행한 스캔이 없습니다.
              {canManage && " 왼쪽에서 대상 대역을 입력해 시작하십시오."}
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
                </TableRow>
              </TableHeader>
              <TableBody>
                {(jobIds ?? []).map((id) => (
                  <ScanJobRow key={id} job={jobs[id]} sensorById={sensorById} />
                ))}
              </TableBody>
            </Table>
          )}
        </Card>
      </div>
    </div>
  );
}

function ScanJobRow({
  job,
  sensorById,
}: {
  job?: ScanJob;
  sensorById: Record<string, Sensor>;
}) {
  if (!job) {
    return (
      <TableRow>
        <TableCell colSpan={6} className="text-sm text-muted-foreground">
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
    <TableRow>
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
    </TableRow>
  );
}

/**
 * 주기적으로 갱신되는 현재 시각(ms). 렌더 중 `Date.now()`를 직접 부르면
 * 순수성 규칙(react-hooks/purity)에 걸리므로 상태로 흘려보낸다.
 */
function useTicker(intervalMs: number): number {
  const [now, setNow] = React.useState(() => Date.now());
  React.useEffect(() => {
    const timer = setInterval(() => setNow(Date.now()), intervalMs);
    return () => clearInterval(timer);
  }, [intervalMs]);
  return now;
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

/** 실행기 선택에서 "서버 직접"을 뜻하는 값 — SelectItem은 빈 문자열을 못 쓴다. */
const EXECUTOR_SERVER = "server";

function ScanForm({
  sensors,
  onCreated,
}: {
  sensors: Sensor[];
  onCreated: (jobId: string) => void;
}) {
  const [targets, setTargets] = React.useState("");
  const [ports, setPorts] = React.useState("443");
  const [timeoutMs, setTimeoutMs] = React.useState("");
  const [concurrency, setConcurrency] = React.useState("");
  const [executor, setExecutor] = React.useState(EXECUTOR_SERVER);
  const [error, setError] = React.useState<string | null>(null);
  const [busy, setBusy] = React.useState(false);

  // 실행기 후보 — active 센서만 위임 대상이 된다.
  const activeSensors = React.useMemo(
    () => sensors.filter((s) => s.status === "active"),
    [sensors],
  );

  async function submit(e: React.FormEvent) {
    e.preventDefault();
    setError(null);

    const targetList = targets
      .split("\n")
      .map((s) => s.trim())
      .filter(Boolean);
    if (targetList.length === 0) {
      setError("스캔 대상(IP 또는 CIDR)을 한 줄에 하나씩 입력하십시오.");
      return;
    }
    const portList = ports
      .split(",")
      .map((s) => Number.parseInt(s.trim(), 10))
      .filter((n) => Number.isFinite(n));

    setBusy(true);
    try {
      const req: Parameters<typeof createScan>[0] = { targets: targetList };
      if (portList.length > 0) req.ports = portList;
      if (timeoutMs.trim()) req.timeout_ms = Number.parseInt(timeoutMs, 10);
      if (concurrency.trim()) req.concurrency = Number.parseInt(concurrency, 10);
      if (executor !== EXECUTOR_SERVER) req.sensor_id = executor;
      const accepted = await createScan(req);
      onCreated(accepted.job_id);
    } catch (err) {
      setError(err instanceof Error ? err.message : "스캔 생성에 실패했습니다.");
    } finally {
      setBusy(false);
    }
  }

  return (
    <Card className="gap-0 py-0">
      <div className="border-b px-4 py-3">
        <h2 className="text-sm font-semibold tracking-tight">스캔 시작</h2>
      </div>
      <form onSubmit={submit}>
        <div className="space-y-4 p-4">
          <div className="space-y-2">
            <Label htmlFor="scan-targets">
              IP / CIDR 대역 <span className="text-red-600">*</span>
            </Label>
            <Textarea
              id="scan-targets"
              required
              spellCheck={false}
              className="min-h-20 font-mono text-xs"
              placeholder={"10.0.0.5\n10.0.0.0/24"}
              value={targets}
              onChange={(e) => setTargets(e.target.value)}
            />
            <p className="text-xs text-muted-foreground">
              한 줄에 하나씩 · CIDR 또는 단일 IP
            </p>
          </div>
          <div className="space-y-2">
            <Label htmlFor="scan-executor">실행기</Label>
            <Select value={executor} onValueChange={setExecutor}>
              <SelectTrigger id="scan-executor" className="w-full">
                <SelectValue />
              </SelectTrigger>
              <SelectContent>
                <SelectItem value={EXECUTOR_SERVER}>서버 직접</SelectItem>
                {activeSensors.map((s) => (
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
          <div className="space-y-2">
            <Label htmlFor="scan-ports">포트 목록</Label>
            <Input
              id="scan-ports"
              className="font-mono text-xs"
              placeholder="443, 8443"
              value={ports}
              onChange={(e) => setPorts(e.target.value)}
            />
            <p className="text-xs text-muted-foreground">쉼표로 구분 · 비우면 443</p>
          </div>
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
          {error && (
            <Alert variant="destructive">
              <AlertDescription>{error}</AlertDescription>
            </Alert>
          )}
        </div>
        <div className="border-t p-4">
          <Button type="submit" className="w-full" disabled={busy}>
            {busy ? "스캔 생성 중…" : "스캔 시작"}
          </Button>
        </div>
      </form>
    </Card>
  );
}
