"use client";

import * as React from "react";
import { Check, Copy, Plus } from "lucide-react";

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
  Table,
  TableBody,
  TableCell,
  TableHead,
  TableHeader,
  TableRow,
} from "@/components/ui/table";
import { isSystemAdministrator, useAuth } from "@/lib/auth";
import {
  createSensor,
  disableSensor,
  listSensors,
  type Sensor,
  type SensorCreateResponse,
  type SensorStatus,
} from "@/lib/api";
import { formatDateTime } from "@/lib/format";

const STATUS_LABELS: Record<SensorStatus, string> = {
  pending: "등록 대기",
  active: "활성",
  disabled: "비활성화",
};

function SensorStatusBadge({ status }: { status: SensorStatus }) {
  if (status === "pending") {
    return (
      <Badge className="border border-amber-200 bg-amber-50 text-amber-700">
        {STATUS_LABELS.pending}
      </Badge>
    );
  }
  if (status === "active") {
    return <Badge variant="outline">{STATUS_LABELS.active}</Badge>;
  }
  return <Badge variant="secondary">{STATUS_LABELS.disabled}</Badge>;
}

export default function SensorsPage() {
  const { user } = useAuth();
  // 센서 등록·비활성화 모두 서버에서 System Administrator 전용이다 (manual/SENSOR.md 5.1).
  const canManage = isSystemAdministrator(user);

  const [sensors, setSensors] = React.useState<Sensor[] | null>(null);
  const [error, setError] = React.useState<string | null>(null);
  const [reloadKey, setReloadKey] = React.useState(0);
  const [createOpen, setCreateOpen] = React.useState(false);
  const [disableTarget, setDisableTarget] = React.useState<Sensor | null>(null);

  React.useEffect(() => {
    let cancelled = false;
    listSensors()
      .then((res) => {
        if (!cancelled) {
          setSensors(res.items);
          setError(null);
        }
      })
      .catch((err: unknown) => {
        if (!cancelled)
          setError(err instanceof Error ? err.message : "센서 목록 조회에 실패했습니다.");
      });
    return () => {
      cancelled = true;
    };
  }, [reloadKey]);

  const reload = React.useCallback(() => setReloadKey((k) => k + 1), []);

  return (
    <div className="mx-auto max-w-[1400px] space-y-4">
      <div className="flex items-end justify-between gap-4">
        <div>
          <h1 className="text-lg font-semibold tracking-tight">센서</h1>
          <p className="text-[13px] text-muted-foreground">
            서버가 직접 도달할 수 없는 세그먼트에 배치하는 원격 수집기입니다.
            센서는 아웃바운드 mTLS 한 경로로만 서버에 접속합니다.
          </p>
        </div>
        {canManage && (
          <Button onClick={() => setCreateOpen(true)}>
            <Plus data-icon="inline-start" />
            센서 등록
          </Button>
        )}
      </div>

      {error && (
        <Alert variant="destructive">
          <AlertTitle>센서 목록을 불러오지 못했습니다</AlertTitle>
          <AlertDescription>{error}</AlertDescription>
        </Alert>
      )}

      <Card className="gap-0 overflow-hidden py-0">
        <div className="flex items-baseline gap-2 border-b px-4 py-3">
          <h2 className="text-sm font-semibold tracking-tight">등록된 센서</h2>
          {sensors != null && (
            <span className="text-xs text-muted-foreground">{sensors.length}대</span>
          )}
        </div>
        {sensors == null && !error ? (
          <p className="px-4 py-12 text-center text-sm text-muted-foreground">
            목록 조회 중…
          </p>
        ) : sensors == null || sensors.length === 0 ? (
          <p className="px-4 py-12 text-center text-sm text-muted-foreground">
            등록된 센서가 없습니다.
            {canManage && " 위의 “센서 등록”으로 등록 토큰을 발급하십시오."}
          </p>
        ) : (
          <Table>
            <TableHeader>
              <TableRow>
                <TableHead>이름</TableHead>
                <TableHead className="w-28">상태</TableHead>
                <TableHead className="w-40">마지막 통신</TableHead>
                <TableHead className="w-40">등록일</TableHead>
                {canManage && <TableHead className="w-24 text-right" />}
              </TableRow>
            </TableHeader>
            <TableBody>
              {sensors.map((sensor) => (
                <TableRow key={sensor.id}>
                  <TableCell className="font-medium">{sensor.name}</TableCell>
                  <TableCell>
                    <SensorStatusBadge status={sensor.status} />
                  </TableCell>
                  <TableCell className="font-mono text-xs tabular-nums text-muted-foreground">
                    {sensor.last_seen_at ? formatDateTime(sensor.last_seen_at) : "—"}
                  </TableCell>
                  <TableCell className="font-mono text-xs tabular-nums text-muted-foreground">
                    {formatDateTime(sensor.created_at)}
                  </TableCell>
                  {canManage && (
                    <TableCell className="text-right">
                      {sensor.status !== "disabled" && (
                        <Button
                          variant="ghost"
                          size="sm"
                          className="text-destructive hover:bg-destructive/10 hover:text-destructive"
                          onClick={() => setDisableTarget(sensor)}
                        >
                          비활성화
                        </Button>
                      )}
                    </TableCell>
                  )}
                </TableRow>
              ))}
            </TableBody>
          </Table>
        )}
      </Card>

      {canManage && (
        <CreateSensorDialog
          open={createOpen}
          onOpenChange={setCreateOpen}
          onCreated={reload}
        />
      )}
      {canManage && (
        <DisableSensorDialog
          sensor={disableTarget}
          onOpenChange={(open) => {
            if (!open) setDisableTarget(null);
          }}
          onDisabled={reload}
        />
      )}
    </div>
  );
}

/** 복사 버튼 — 클립보드 복사 후 잠깐 체크 표시로 바뀐다. */
function CopyButton({ value, label }: { value: string; label: string }) {
  const [copied, setCopied] = React.useState(false);

  async function copy() {
    try {
      await navigator.clipboard.writeText(value);
      setCopied(true);
      setTimeout(() => setCopied(false), 1500);
    } catch {
      // 클립보드 권한이 없으면 사용자가 직접 드래그해 복사한다.
    }
  }

  return (
    <Button type="button" variant="ghost" size="icon-sm" onClick={copy}>
      {copied ? <Check className="text-emerald-600" /> : <Copy />}
      <span className="sr-only">{label} 복사</span>
    </Button>
  );
}

function SecretField({
  label,
  value,
  mono = true,
}: {
  label: string;
  value: string;
  mono?: boolean;
}) {
  return (
    <div className="space-y-1">
      <div className="flex items-center justify-between">
        <span className="text-xs font-medium text-muted-foreground">{label}</span>
        <CopyButton value={value} label={label} />
      </div>
      <pre
        className={
          "overflow-x-auto rounded-md border bg-muted/50 p-2 text-xs break-all whitespace-pre-wrap" +
          (mono ? " font-mono" : "")
        }
      >
        {value}
      </pre>
    </div>
  );
}

function CreateSensorDialog({
  open,
  onOpenChange,
  onCreated,
}: {
  open: boolean;
  onOpenChange: (open: boolean) => void;
  onCreated: () => void;
}) {
  const [name, setName] = React.useState("");
  const [busy, setBusy] = React.useState(false);
  const [error, setError] = React.useState<string | null>(null);
  const [result, setResult] = React.useState<SensorCreateResponse | null>(null);

  function handleOpenChange(next: boolean) {
    onOpenChange(next);
    if (!next) {
      // 닫으면 토큰은 폐기된다 — 서버는 해시만 저장하므로 다시 보여줄 수 없다.
      setName("");
      setError(null);
      setResult(null);
      setBusy(false);
    }
  }

  async function submit(e: React.FormEvent) {
    e.preventDefault();
    const trimmed = name.trim();
    if (!trimmed) {
      setError("센서 이름을 입력하십시오.");
      return;
    }
    setBusy(true);
    setError(null);
    try {
      const res = await createSensor(trimmed);
      setResult(res);
      onCreated();
    } catch (err) {
      setError(err instanceof Error ? err.message : "센서 등록에 실패했습니다.");
    } finally {
      setBusy(false);
    }
  }

  return (
    <Dialog open={open} onOpenChange={handleOpenChange}>
      <DialogContent
        className="sm:max-w-lg"
        // 토큰 표시 중에는 바깥 클릭·ESC로 실수로 닫히지 않게 한다.
        onInteractOutside={(e) => {
          if (result) e.preventDefault();
        }}
        onEscapeKeyDown={(e) => {
          if (result) e.preventDefault();
        }}
      >
        {result == null ? (
          <form onSubmit={submit} className="grid gap-4">
            <DialogHeader>
              <DialogTitle>센서 등록</DialogTitle>
              <DialogDescription>
                이름을 정하면 1회용 등록 토큰이 발급됩니다. 서버는 토큰의
                SHA-256 해시만 저장합니다.
              </DialogDescription>
            </DialogHeader>
            <div className="space-y-2">
              <Label htmlFor="sensor-name">
                센서 이름 <span className="text-red-600">*</span>
              </Label>
              <Input
                id="sensor-name"
                required
                autoFocus
                spellCheck={false}
                placeholder="segment-c-sensor"
                value={name}
                onChange={(e) => setName(e.target.value)}
              />
              <p className="text-xs text-muted-foreground">
                배치할 세그먼트를 알아볼 수 있는 이름을 권장합니다.
              </p>
            </div>
            {error && (
              <Alert variant="destructive">
                <AlertDescription>{error}</AlertDescription>
              </Alert>
            )}
            <DialogFooter>
              <Button
                type="button"
                variant="outline"
                onClick={() => handleOpenChange(false)}
              >
                취소
              </Button>
              <Button type="submit" disabled={busy}>
                {busy ? "발급 중…" : "등록 토큰 발급"}
              </Button>
            </DialogFooter>
          </form>
        ) : (
          <div className="grid gap-4">
            <DialogHeader>
              <DialogTitle>센서 {result.sensor.name} 등록됨</DialogTitle>
              <DialogDescription>
                아래 정보를 센서 호스트에 전달해 등록을 완료하십시오.
              </DialogDescription>
            </DialogHeader>
            <Alert className="border-amber-200 bg-amber-50 text-amber-800">
              <AlertTitle>지금 한 번만 표시됩니다</AlertTitle>
              <AlertDescription className="text-amber-800/90">
                등록 토큰은 이 창을 닫으면 다시 볼 수 없습니다. 분실하면 센서를
                비활성화하고 새로 등록해야 합니다.
              </AlertDescription>
            </Alert>
            <SecretField label="등록 토큰" value={result.enrollment_token} />
            <SecretField label="CA fingerprint" value={result.ca_fingerprint} />
            <SecretField label="등록 명령 예시" value={result.enroll_command_example} />
            <DialogFooter>
              <Button type="button" onClick={() => handleOpenChange(false)}>
                저장했습니다, 닫기
              </Button>
            </DialogFooter>
          </div>
        )}
      </DialogContent>
    </Dialog>
  );
}

function DisableSensorDialog({
  sensor,
  onOpenChange,
  onDisabled,
}: {
  sensor: Sensor | null;
  onOpenChange: (open: boolean) => void;
  onDisabled: () => void;
}) {
  const [busy, setBusy] = React.useState(false);
  const [error, setError] = React.useState<string | null>(null);

  function handleOpenChange(next: boolean) {
    onOpenChange(next);
    if (!next) {
      setError(null);
      setBusy(false);
    }
  }

  async function confirm() {
    if (!sensor) return;
    setBusy(true);
    setError(null);
    try {
      await disableSensor(sensor.id);
      onDisabled();
      handleOpenChange(false);
    } catch (err) {
      setError(err instanceof Error ? err.message : "센서 비활성화에 실패했습니다.");
      setBusy(false);
    }
  }

  return (
    <Dialog open={sensor != null} onOpenChange={handleOpenChange}>
      <DialogContent>
        <DialogHeader>
          <DialogTitle>센서 비활성화</DialogTitle>
          <DialogDescription>
            <strong>{sensor?.name}</strong> 센서를 비활성화합니다. 이후 이 센서의
            접속과 작업 수령이 거부됩니다. 같은 세그먼트를 다시 수집하려면 새
            센서를 등록해야 합니다.
          </DialogDescription>
        </DialogHeader>
        {error && (
          <Alert variant="destructive">
            <AlertDescription>{error}</AlertDescription>
          </Alert>
        )}
        <DialogFooter>
          <Button
            type="button"
            variant="outline"
            onClick={() => handleOpenChange(false)}
          >
            취소
          </Button>
          <Button
            type="button"
            variant="destructive"
            disabled={busy}
            onClick={confirm}
          >
            {busy ? "비활성화 중…" : "비활성화"}
          </Button>
        </DialogFooter>
      </DialogContent>
    </Dialog>
  );
}
