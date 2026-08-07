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
      (j) => j.status === "pending" || j.status === "running",
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
          <ScanForm onCreated={addJob} />
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
                  <ScanJobRow key={id} job={jobs[id]} />
                ))}
              </TableBody>
            </Table>
          )}
        </Card>
      </div>
    </div>
  );
}

function ScanJobRow({ job }: { job?: ScanJob }) {
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
  return (
    <TableRow>
      <TableCell className="max-w-52 truncate font-mono text-xs">
        {job.targets.join(", ")}
        <div className="text-muted-foreground/70">
          포트 {job.ports.join(", ")}
        </div>
      </TableCell>
      <TableCell>
        {job.status === "pending" || job.status === "running" ? (
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
        {job.scanned_endpoints} / {job.total_endpoints}
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

/** 실행기 선택에서 "서버 직접"을 뜻하는 값 — SelectItem은 빈 문자열을 못 쓴다. */
const EXECUTOR_SERVER = "server";

function ScanForm({ onCreated }: { onCreated: (jobId: string) => void }) {
  const [targets, setTargets] = React.useState("");
  const [ports, setPorts] = React.useState("443");
  const [timeoutMs, setTimeoutMs] = React.useState("");
  const [concurrency, setConcurrency] = React.useState("");
  const [executor, setExecutor] = React.useState(EXECUTOR_SERVER);
  const [sensors, setSensors] = React.useState<Sensor[]>([]);
  const [error, setError] = React.useState<string | null>(null);
  const [busy, setBusy] = React.useState(false);

  // 실행기 후보 — active 센서만 위임 대상이 된다.
  React.useEffect(() => {
    let cancelled = false;
    listSensors()
      .then((res) => {
        if (!cancelled)
          setSensors(res.items.filter((s) => s.status === "active"));
      })
      .catch(() => {
        // 센서 목록을 못 가져와도 서버 직접 실행은 가능하다.
      });
    return () => {
      cancelled = true;
    };
  }, []);

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
                {sensors.map((s) => (
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
