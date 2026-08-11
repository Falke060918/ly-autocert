"use client";

/**
 * 알림 설정 (System Administrator + Security Administrator).
 *
 * 화면 게이트는 백엔드 protected() 등록과 같은 2역할이다 — 00-spec 7.11
 * "보안 관리자 = 정책 설정"이 근거이며 사용자 관리(시스템 관리자 단독)와 다르다.
 *
 * 채널 상태는 {channel, enabled, reason} 3필드가 전부다. 호스트·계정·URL 같은
 * 설정값은 응답에 아예 없으므로(설계 9.3) 화면도 사유 문자열만 보여준다.
 */

import * as React from "react";
import { Plus, RefreshCw, Send, Trash2 } from "lucide-react";

import { Alert, AlertDescription, AlertTitle } from "@/components/ui/alert";
import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import { Card } from "@/components/ui/card";
import { Checkbox } from "@/components/ui/checkbox";
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
import {
  Table,
  TableBody,
  TableCell,
  TableHead,
  TableHeader,
  TableRow,
} from "@/components/ui/table";
import { ALL_ROLES, roleLabel, useAuth } from "@/lib/auth";
import {
  ApiError,
  createNotificationPolicy,
  deleteNotificationPolicy,
  listNotificationChannels,
  listNotificationDeliveries,
  listNotificationPolicies,
  testNotificationPolicy,
  updateNotificationPolicy,
  type NotificationChannel,
  type NotificationChannelStatus,
  type NotificationDelivery,
  type NotificationDeliveryStatus,
  type NotificationEventType,
  type NotificationPolicy,
  type NotificationPolicyRequest,
  type NotificationRecipient,
  type NotificationRecipientKind,
  type NotificationTestChannelResult,
  type Role,
} from "@/lib/api";
import { formatDateTime } from "@/lib/format";

/** 서버 CHECK와 같은 4채널. 순서도 채널 상태 응답과 맞춘다. */
const CHANNELS: { value: NotificationChannel; label: string; hint: string }[] = [
  { value: "email", label: "이메일", hint: "SMTP_* 환경변수로 설정합니다." },
  { value: "webhook", label: "웹훅", hint: "WEBHOOK_* 환경변수로 설정합니다." },
  {
    value: "inbox",
    label: "알림함",
    hint: "제품 내부 알림함 — 설정 없이 항상 동작합니다.",
  },
  { value: "syslog", label: "Syslog", hint: "SYSLOG_* 환경변수로 설정합니다." },
];

const CHANNEL_LABELS: Record<NotificationChannel, string> = {
  email: "이메일",
  webhook: "웹훅",
  inbox: "알림함",
  syslog: "Syslog",
};

const EVENT_TYPES: { value: NotificationEventType; label: string }[] = [
  { value: "certificate.expiring", label: "만료 임박" },
  { value: "certificate.expired", label: "만료됨" },
];

const EVENT_LABELS: Record<string, string> = {
  "certificate.expiring": "만료 임박",
  "certificate.expired": "만료됨",
};

/** 0012 마이그레이션의 thresholds 기본값과 같은 값이다. */
const DEFAULT_THRESHOLDS = [90, 60, 30, 7, 1];

/**
 * 0020 마이그레이션이 시드 정책에 넣는 CA 전용 임계값과 같은 값이다.
 * 리프보다 훨씬 이르다 — CA 교체는 재발급·재배포가 아니라 신뢰 저장소 갱신까지
 * 따라오는 작업이라 D-90에 처음 알면 늦는다.
 */
const DEFAULT_CA_THRESHOLDS = [365, 180, 90, 30, 7];

const RECIPIENT_KINDS: { value: NotificationRecipientKind; label: string }[] = [
  { value: "owner", label: "인증서 소유자" },
  { value: "role", label: "역할" },
  { value: "user", label: "특정 사용자" },
  { value: "static", label: "고정 메일 주소" },
];

const DELIVERY_STATUSES: NotificationDeliveryStatus[] = [
  "pending",
  "sending",
  "sent",
  "failed",
  "skipped",
];

const DELIVERY_STATUS_LABELS: Record<NotificationDeliveryStatus, string> = {
  pending: "대기",
  sending: "발송 중",
  sent: "발송됨",
  failed: "실패",
  skipped: "건너뜀",
};

function errorMessage(err: unknown, fallback: string): string {
  if (err instanceof ApiError) {
    if (err.code === "channels_unavailable" || err.status === 503) {
      return "서버에 알림 채널이 배선되지 않았습니다. 이 서버에서는 실제 발송이 일어나지 않습니다 — 관리자에게 SMTP_*·WEBHOOK_*·SYSLOG_* 설정과 NOTIFY_ENABLED 값을 확인하도록 요청하십시오.";
    }
    if (err.code === "policy_name_taken") {
      return "같은 이름의 알림 정책이 이미 있습니다.";
    }
    if (err.status === 404) {
      return "해당 정책을 찾을 수 없습니다. 목록을 새로 고치십시오.";
    }
    return err.message || fallback;
  }
  return err instanceof Error ? err.message : fallback;
}

export default function NotificationSettingsPage() {
  const { user, loading } = useAuth();
  const canAdmin =
    user?.role === "system_administrator" ||
    user?.role === "security_administrator";

  if (loading) {
    return <Skeleton className="h-96 rounded-lg" />;
  }

  if (!canAdmin) {
    return (
      <div className="mx-auto max-w-2xl">
        <Card className="p-8 text-center">
          <h1 className="text-base font-semibold tracking-tight">
            접근 권한이 없습니다
          </h1>
          <p className="text-[13px] text-muted-foreground">
            알림 정책 설정은 시스템 관리자와 보안 관리자만 사용할 수 있습니다.
            받은 알림은 좌측 메뉴의 &ldquo;알림&rdquo;에서 확인할 수 있습니다.
          </p>
        </Card>
      </div>
    );
  }

  return (
    <div className="mx-auto max-w-[1400px] space-y-4">
      {/* 제목은 화면에서 걷어냈지만 문서 구조(h1)는 스크린리더용으로 남긴다. */}
      <h1 className="sr-only">알림 설정</h1>

      <ChannelStatusCard />
      <PolicySection />
      <DeliveryHistoryCard />
    </div>
  );
}

// ===== 채널 상태 =====

function ChannelStatusCard() {
  const [items, setItems] = React.useState<NotificationChannelStatus[] | null>(
    null,
  );
  const [error, setError] = React.useState<string | null>(null);

  React.useEffect(() => {
    let cancelled = false;
    listNotificationChannels()
      .then((res) => {
        if (!cancelled) setItems(res.items);
      })
      .catch((err: unknown) => {
        if (!cancelled) setError(errorMessage(err, "채널 상태를 불러오지 못했습니다."));
      });
    return () => {
      cancelled = true;
    };
  }, []);

  return (
    <Card className="gap-0 overflow-hidden py-0">
      <div className="border-b px-4 py-3">
        <h2 className="text-sm font-semibold tracking-tight">채널 상태</h2>
        <p className="text-xs text-muted-foreground">
          서버가 실제로 보낼 수 있는 채널입니다. 설정값(호스트·계정·URL)은 보안상
          응답에 포함되지 않으므로, 값 자체는 서버 환경변수에서 확인하십시오.
        </p>
      </div>
      {error && (
        <div className="p-4">
          <Alert variant="destructive">
            <AlertDescription>{error}</AlertDescription>
          </Alert>
        </div>
      )}
      {items == null && !error && (
        <div className="p-4">
          <Skeleton className="h-24 rounded-md" />
        </div>
      )}
      {items != null && (
        <Table>
          <TableHeader>
            <TableRow>
              <TableHead className="w-40">채널</TableHead>
              <TableHead className="w-28">상태</TableHead>
              <TableHead>사유 / 설정 위치</TableHead>
            </TableRow>
          </TableHeader>
          <TableBody>
            {items.map((ch) => {
              const meta = CHANNELS.find((c) => c.value === ch.channel);
              return (
                <TableRow key={ch.channel}>
                  <TableCell className="text-[13px] font-medium">
                    {meta?.label ?? ch.channel}
                  </TableCell>
                  <TableCell>
                    {ch.enabled ? (
                      <Badge variant="outline">사용 가능</Badge>
                    ) : (
                      <Badge variant="secondary">비활성</Badge>
                    )}
                  </TableCell>
                  <TableCell className="text-[13px] text-muted-foreground">
                    {ch.enabled ? meta?.hint : ch.reason || "사유가 보고되지 않았습니다."}
                  </TableCell>
                </TableRow>
              );
            })}
          </TableBody>
        </Table>
      )}
    </Card>
  );
}

// ===== 정책 =====

function PolicySection() {
  const [policies, setPolicies] = React.useState<NotificationPolicy[] | null>(
    null,
  );
  const [error, setError] = React.useState<string | null>(null);
  const [reloadKey, setReloadKey] = React.useState(0);
  const [editing, setEditing] = React.useState<NotificationPolicy | null>(null);
  const [creating, setCreating] = React.useState(false);
  const [testTarget, setTestTarget] = React.useState<NotificationPolicy | null>(
    null,
  );
  const [deleteTarget, setDeleteTarget] =
    React.useState<NotificationPolicy | null>(null);

  React.useEffect(() => {
    let cancelled = false;
    listNotificationPolicies()
      .then((res) => {
        if (cancelled) return;
        setPolicies(res.items);
        setError(null);
      })
      .catch((err: unknown) => {
        if (!cancelled) setError(errorMessage(err, "정책 목록을 불러오지 못했습니다."));
      });
    return () => {
      cancelled = true;
    };
  }, [reloadKey]);

  const reload = React.useCallback(() => setReloadKey((k) => k + 1), []);

  async function toggleEnabled(policy: NotificationPolicy, next: boolean) {
    // 목록 상태를 먼저 바꾸고 실패하면 되돌린다 — 스위치는 즉시 반응해야 한다.
    const snapshot = policies;
    setPolicies(
      (prev) =>
        prev?.map((p) => (p.id === policy.id ? { ...p, enabled: next } : p)) ??
        prev,
    );
    try {
      await updateNotificationPolicy(policy.id, { enabled: next });
      setError(null);
    } catch (err) {
      setPolicies(snapshot);
      setError(errorMessage(err, "정책 활성 상태를 바꾸지 못했습니다."));
    }
  }

  return (
    <Card className="gap-0 overflow-hidden py-0">
      <div className="flex items-center justify-between gap-4 border-b px-4 py-3">
        <div>
          <h2 className="text-sm font-semibold tracking-tight">알림 정책</h2>
          <p className="text-xs text-muted-foreground">
            임계일마다 사건이 한 번씩 만들어지고, 정책이 겹쳐도 알림은 1건입니다
            (수신자는 합집합).
          </p>
        </div>
        <Button size="sm" onClick={() => setCreating(true)}>
          <Plus data-icon="inline-start" />
          정책 추가
        </Button>
      </div>

      {error && (
        <div className="p-4">
          <Alert variant="destructive">
            <AlertDescription>{error}</AlertDescription>
          </Alert>
        </div>
      )}
      {policies == null && !error && (
        <div className="p-4">
          <Skeleton className="h-32 rounded-md" />
        </div>
      )}

      {policies != null && policies.length === 0 && (
        <p className="px-4 py-10 text-center text-[13px] text-muted-foreground">
          등록된 알림 정책이 없습니다. 정책이 하나도 없으면 만료가 임박해도
          알림이 만들어지지 않습니다 — &ldquo;정책 추가&rdquo;로 임계일과 채널을
          정하십시오.
        </p>
      )}

      {policies != null && policies.length > 0 && (
        <Table>
          <TableHeader>
            <TableRow>
              <TableHead>이름</TableHead>
              <TableHead className="w-24">활성</TableHead>
              <TableHead className="w-32">사건</TableHead>
              <TableHead className="w-44">임계값(일)</TableHead>
              <TableHead className="w-40">채널</TableHead>
              <TableHead>수신자 / 필터</TableHead>
              <TableHead className="w-56 text-right" />
            </TableRow>
          </TableHeader>
          <TableBody>
            {policies.map((p) => (
              <TableRow key={p.id} className={p.enabled ? undefined : "bg-muted/30"}>
                <TableCell className="text-[13px] font-medium">{p.name}</TableCell>
                <TableCell>
                  <Switch
                    checked={p.enabled}
                    onCheckedChange={(v) => void toggleEnabled(p, v)}
                    aria-label={`${p.name} 활성`}
                  />
                </TableCell>
                <TableCell className="text-[13px]">
                  {p.event_types.map((t) => EVENT_LABELS[t] ?? t).join(", ")}
                </TableCell>
                <TableCell className="font-mono text-xs tabular-nums">
                  <div>{[...p.thresholds].sort((a, b) => b - a).join(" · ")}</div>
                  {/* CA 전용 임계값은 미설정(리프와 동일)일 때 줄을 만들지 않는다. */}
                  {p.ca_thresholds != null && (
                    <div className="text-muted-foreground">
                      CA:{" "}
                      {p.ca_thresholds.length === 0
                        ? "알림 없음"
                        : [...p.ca_thresholds].sort((a, b) => b - a).join(" · ")}
                    </div>
                  )}
                </TableCell>
                <TableCell className="text-[13px]">
                  {p.channels.map((c) => CHANNEL_LABELS[c] ?? c).join(", ")}
                </TableCell>
                <TableCell className="text-[13px] text-muted-foreground">
                  <div>{recipientSummary(p.recipients)}</div>
                  <div className="text-xs">{filterSummary(p)}</div>
                </TableCell>
                <TableCell className="text-right whitespace-nowrap">
                  <Button
                    variant="ghost"
                    size="sm"
                    onClick={() => setTestTarget(p)}
                  >
                    <Send data-icon="inline-start" />
                    테스트
                  </Button>
                  <Button variant="ghost" size="sm" onClick={() => setEditing(p)}>
                    수정
                  </Button>
                  <Button
                    variant="ghost"
                    size="sm"
                    className="text-destructive hover:bg-destructive/10 hover:text-destructive"
                    onClick={() => setDeleteTarget(p)}
                  >
                    <Trash2 data-icon="inline-start" />
                    삭제
                  </Button>
                </TableCell>
              </TableRow>
            ))}
          </TableBody>
        </Table>
      )}

      <PolicyDialog
        open={creating}
        policy={null}
        onOpenChange={(open) => setCreating(open)}
        onSaved={reload}
      />
      <PolicyDialog
        open={editing != null}
        policy={editing}
        onOpenChange={(open) => {
          if (!open) setEditing(null);
        }}
        onSaved={reload}
      />
      <TestDialog
        policy={testTarget}
        onOpenChange={(open) => {
          if (!open) setTestTarget(null);
        }}
      />
      <DeletePolicyDialog
        policy={deleteTarget}
        onOpenChange={(open) => {
          if (!open) setDeleteTarget(null);
        }}
        onDeleted={reload}
      />
    </Card>
  );
}

function recipientSummary(recipients: NotificationRecipient[]): string {
  if (recipients.length === 0) {
    return "수신자 없음 (소유자·역할을 지정하지 않음)";
  }
  return recipients
    .map((r) => {
      switch (r.kind) {
        case "owner":
          return "인증서 소유자";
        case "role":
          return `역할 ${roleLabel(r.role as Role)}`;
        case "user":
          return `사용자 ${(r.user_id ?? "").slice(0, 8)}`;
        case "static":
          return r.email ?? "고정 주소";
        default:
          return r.kind;
      }
    })
    .join(", ");
}

function filterSummary(p: NotificationPolicy): string {
  const parts: string[] = [];
  if (p.filter_environments?.length) {
    parts.push(`환경: ${p.filter_environments.join("/")}`);
  }
  if (p.filter_department_ids?.length) {
    parts.push(`부서 ${p.filter_department_ids.length}개`);
  }
  return parts.length ? parts.join(" · ") : "전체 인증서";
}

// ===== 정책 생성·수정 =====

interface PolicyFormState {
  name: string;
  enabled: boolean;
  eventTypes: NotificationEventType[];
  thresholds: number[];
  /** null = CA도 위 임계값을 쓴다(미설정). 빈 배열이면 CA는 알리지 않는다. */
  caThresholds: number[] | null;
  channels: NotificationChannel[];
  recipients: NotificationRecipient[];
  environments: string;
  departmentIds: string;
}

function toForm(policy: NotificationPolicy | null): PolicyFormState {
  if (!policy) {
    return {
      name: "",
      enabled: true,
      eventTypes: ["certificate.expiring"],
      thresholds: [...DEFAULT_THRESHOLDS],
      caThresholds: null,
      channels: ["inbox"],
      recipients: [{ kind: "owner" }],
      environments: "",
      departmentIds: "",
    };
  }
  return {
    name: policy.name,
    enabled: policy.enabled,
    eventTypes: [...policy.event_types],
    thresholds: [...policy.thresholds],
    // undefined(구버전 응답)와 null은 둘 다 미설정이다.
    caThresholds: policy.ca_thresholds == null ? null : [...policy.ca_thresholds],
    channels: [...policy.channels],
    recipients: policy.recipients.map((r) => ({ ...r })),
    environments: (policy.filter_environments ?? []).join(", "),
    departmentIds: (policy.filter_department_ids ?? []).join(", "),
  };
}

function splitList(value: string): string[] {
  return value
    .split(",")
    .map((v) => v.trim())
    .filter((v) => v !== "");
}

function PolicyDialog({
  open,
  policy,
  onOpenChange,
  onSaved,
}: {
  open: boolean;
  policy: NotificationPolicy | null;
  onOpenChange: (open: boolean) => void;
  onSaved: () => void;
}) {
  return (
    <Dialog open={open} onOpenChange={onOpenChange}>
      <DialogContent className="max-h-[85vh] overflow-y-auto sm:max-w-2xl">
        {/* key로 대상이 바뀔 때마다 폼을 새로 마운트한다 — 상태를 effect로 되맞추지 않는다. */}
        <PolicyForm
          key={policy?.id ?? "new"}
          policy={policy}
          onClose={() => onOpenChange(false)}
          onSaved={onSaved}
        />
      </DialogContent>
    </Dialog>
  );
}

function PolicyForm({
  policy,
  onClose,
  onSaved,
}: {
  policy: NotificationPolicy | null;
  onClose: () => void;
  onSaved: () => void;
}) {
  const [form, setForm] = React.useState<PolicyFormState>(() => toForm(policy));
  const [customDay, setCustomDay] = React.useState("");
  const [busy, setBusy] = React.useState(false);
  const [error, setError] = React.useState<string | null>(null);

  function patch(next: Partial<PolicyFormState>) {
    setForm((prev) => ({ ...prev, ...next }));
  }

  function toggleThreshold(day: number) {
    patch({
      thresholds: form.thresholds.includes(day)
        ? form.thresholds.filter((d) => d !== day)
        : [...form.thresholds, day],
    });
  }

  function addCustomDay() {
    const day = Number(customDay);
    if (!Number.isInteger(day) || day < 1 || day > 3650) {
      setError("임계값은 1~3650 사이 정수여야 합니다.");
      return;
    }
    if (!form.thresholds.includes(day)) {
      patch({ thresholds: [...form.thresholds, day] });
    }
    setCustomDay("");
    setError(null);
  }

  function toggleCAThreshold(day: number) {
    const cur = form.caThresholds ?? [];
    patch({
      caThresholds: cur.includes(day)
        ? cur.filter((d) => d !== day)
        : [...cur, day],
    });
  }

  function toggleChannel(channel: NotificationChannel) {
    patch({
      channels: form.channels.includes(channel)
        ? form.channels.filter((c) => c !== channel)
        : [...form.channels, channel],
    });
  }

  function toggleEventType(value: NotificationEventType) {
    patch({
      eventTypes: form.eventTypes.includes(value)
        ? form.eventTypes.filter((t) => t !== value)
        : [...form.eventTypes, value],
    });
  }

  function setRecipient(index: number, next: NotificationRecipient) {
    patch({
      recipients: form.recipients.map((r, i) => (i === index ? next : r)),
    });
  }

  async function submit(e: React.FormEvent) {
    e.preventDefault();
    if (form.name.trim() === "") {
      setError("정책 이름을 입력하십시오.");
      return;
    }
    if (form.eventTypes.length === 0) {
      setError("사건 종류를 하나 이상 선택하십시오.");
      return;
    }
    if (form.thresholds.length === 0) {
      setError("임계값을 하나 이상 지정하십시오.");
      return;
    }
    if (form.channels.length === 0) {
      setError("채널을 하나 이상 선택하십시오.");
      return;
    }

    const body: NotificationPolicyRequest = {
      name: form.name.trim(),
      enabled: form.enabled,
      event_types: form.eventTypes,
      thresholds: [...form.thresholds].sort((a, b) => b - a),
      // null을 그대로 보낸다 — 서버는 "키 없음(유지)"과 "null(해제)"을 구분한다.
      ca_thresholds:
        form.caThresholds == null
          ? null
          : [...form.caThresholds].sort((a, b) => b - a),
      channels: form.channels,
      recipients: form.recipients,
      filter_environments: splitList(form.environments),
      filter_department_ids: splitList(form.departmentIds),
    };

    setBusy(true);
    setError(null);
    try {
      if (policy) {
        await updateNotificationPolicy(policy.id, body);
      } else {
        await createNotificationPolicy(body);
      }
      onSaved();
      onClose();
    } catch (err) {
      setError(errorMessage(err, "정책을 저장하지 못했습니다."));
      setBusy(false);
    }
  }

  const customDays = form.thresholds
    .filter((d) => !DEFAULT_THRESHOLDS.includes(d))
    .sort((a, b) => b - a);

  // API로만 넣을 수 있는 CA 임계값(프리셋 밖의 값)도 화면에서 보이고 뺄 수 있어야 한다.
  const caCustomDays = (form.caThresholds ?? [])
    .filter((d) => !DEFAULT_CA_THRESHOLDS.includes(d))
    .sort((a, b) => b - a);

  return (
    <form onSubmit={submit} className="grid gap-5">
      <DialogHeader>
        <DialogTitle>{policy ? "알림 정책 수정" : "알림 정책 추가"}</DialogTitle>
        <DialogDescription>
          만료 임박을 언제 감지해 어떤 채널로 누구에게 보낼지 정합니다. 저장하면
          변경 내역이 감사 로그에 남습니다.
        </DialogDescription>
      </DialogHeader>

      <div className="space-y-2">
        <Label htmlFor="policy-name">
          이름 <span className="text-red-600">*</span>
        </Label>
        <Input
          id="policy-name"
          required
          autoFocus
          placeholder="운영 서버 만료 알림"
          value={form.name}
          onChange={(e) => patch({ name: e.target.value })}
        />
      </div>

      <div className="flex items-center gap-3">
        <Switch
          id="policy-enabled"
          checked={form.enabled}
          onCheckedChange={(v) => patch({ enabled: v })}
        />
        <Label htmlFor="policy-enabled" className="font-normal">
          정책 활성 — 끄면 이 정책으로는 알림이 만들어지지 않습니다.
        </Label>
      </div>

      <div className="space-y-2">
        <Label>사건 종류</Label>
        <div className="flex flex-wrap gap-4">
          {EVENT_TYPES.map((t) => (
            <label key={t.value} className="flex items-center gap-2 text-[13px]">
              <Checkbox
                checked={form.eventTypes.includes(t.value)}
                onCheckedChange={() => toggleEventType(t.value)}
              />
              {t.label}
            </label>
          ))}
        </div>
      </div>

      <div className="space-y-2">
        <Label>임계값 (만료 며칠 전)</Label>
        <div className="flex flex-wrap items-center gap-2">
          {DEFAULT_THRESHOLDS.map((day) => (
            <Button
              key={day}
              type="button"
              size="sm"
              variant={form.thresholds.includes(day) ? "default" : "outline"}
              onClick={() => toggleThreshold(day)}
            >
              {day}일
            </Button>
          ))}
          {customDays.map((day) => (
            <Button
              key={day}
              type="button"
              size="sm"
              variant="secondary"
              onClick={() => toggleThreshold(day)}
              title="클릭하면 제거합니다"
            >
              {day}일 ×
            </Button>
          ))}
          <Input
            className="w-24"
            inputMode="numeric"
            placeholder="일수"
            value={customDay}
            onChange={(e) => setCustomDay(e.target.value)}
            onKeyDown={(e) => {
              if (e.key === "Enter") {
                e.preventDefault();
                addCustomDay();
              }
            }}
          />
          <Button type="button" variant="outline" size="sm" onClick={addCustomDay}>
            추가
          </Button>
        </div>
        <p className="text-xs text-muted-foreground">
          기본값 90 / 60 / 30 / 7 / 1일. 임계일마다 인증서 1건당 알림이 한 번씩
          만들어집니다.
        </p>
      </div>

      {/*
        발급 기관(CA)은 만료의 파급 범위가 다르다 — 그 CA가 발급한 인증서가 전부
        함께 신뢰를 잃고, 대응도 재발급이 아니라 새 CA 구축·배포다. 사건 종류·채널·
        수신자는 그대로 공유하고 임계값만 따로 둔다.
      */}
      <div className="space-y-2">
        <div className="flex items-center gap-3">
          <Switch
            id="policy-ca-thresholds"
            checked={form.caThresholds != null}
            onCheckedChange={(v) =>
              patch({ caThresholds: v ? [...DEFAULT_CA_THRESHOLDS] : null })
            }
          />
          <Label htmlFor="policy-ca-thresholds" className="font-normal">
            발급 기관(CA) 인증서는 별도 임계값 사용
          </Label>
        </div>
        {form.caThresholds != null && (
          <div className="flex flex-wrap items-center gap-2 pl-11">
            {DEFAULT_CA_THRESHOLDS.map((day) => (
              <Button
                key={day}
                type="button"
                size="sm"
                variant={form.caThresholds!.includes(day) ? "default" : "outline"}
                onClick={() => toggleCAThreshold(day)}
              >
                {day}일
              </Button>
            ))}
            {caCustomDays.map((day) => (
              <Button
                key={day}
                type="button"
                size="sm"
                variant="secondary"
                onClick={() => toggleCAThreshold(day)}
                title="클릭하면 제거합니다"
              >
                {day}일 ×
              </Button>
            ))}
          </div>
        )}
        <p className="pl-11 text-xs text-muted-foreground">
          {form.caThresholds == null
            ? "끄면 CA 인증서도 위의 임계값을 그대로 씁니다."
            : form.caThresholds.length === 0
              ? "임계값이 하나도 없으면 CA 인증서는 만료 임박 알림을 만들지 않습니다(만료된 뒤의 알림은 그대로 나갑니다)."
              : "CA는 교체에 시간이 오래 걸리므로 리프보다 이른 임계값을 권장합니다."}
        </p>
      </div>

      <div className="space-y-2">
        <Label>채널</Label>
        <div className="flex flex-wrap gap-4">
          {CHANNELS.map((c) => (
            <label key={c.value} className="flex items-center gap-2 text-[13px]">
              <Checkbox
                checked={form.channels.includes(c.value)}
                onCheckedChange={() => toggleChannel(c.value)}
              />
              {c.label}
            </label>
          ))}
        </div>
        <p className="text-xs text-muted-foreground">
          서버에 설정되지 않은 채널을 골라도 발송은 &ldquo;건너뜀&rdquo;으로
          기록됩니다 — 위의 채널 상태를 함께 확인하십시오.
        </p>
      </div>

      <div className="space-y-2">
        <Label>수신자</Label>
        <div className="space-y-2">
          {form.recipients.map((r, i) => (
            <RecipientRow
              key={i}
              recipient={r}
              onChange={(next) => setRecipient(i, next)}
              onRemove={() =>
                patch({ recipients: form.recipients.filter((_, j) => j !== i) })
              }
            />
          ))}
          <Button
            type="button"
            variant="outline"
            size="sm"
            onClick={() =>
              patch({ recipients: [...form.recipients, { kind: "owner" }] })
            }
          >
            <Plus data-icon="inline-start" />
            수신자 추가
          </Button>
        </div>
        <p className="text-xs text-muted-foreground">
          소유자를 찾지 못하면 고정 주소로 대체하고, 그것도 없으면 발송을 건너뛴
          기록을 남깁니다. 알림함 채널은 사용자 계정에만 꽂힙니다.
        </p>
      </div>

      <div className="grid gap-4 sm:grid-cols-2">
        <div className="space-y-2">
          <Label htmlFor="policy-envs">환경 필터</Label>
          <Input
            id="policy-envs"
            placeholder="production, staging"
            value={form.environments}
            onChange={(e) => patch({ environments: e.target.value })}
          />
          <p className="text-xs text-muted-foreground">
            쉼표로 구분합니다. 비우면 전체 환경입니다.
          </p>
        </div>
        <div className="space-y-2">
          <Label htmlFor="policy-depts">부서 필터</Label>
          <Input
            id="policy-depts"
            placeholder="부서 UUID, 쉼표 구분"
            value={form.departmentIds}
            onChange={(e) => patch({ departmentIds: e.target.value })}
          />
          <p className="text-xs text-muted-foreground">
            부서 id(UUID) 목록입니다. 비우면 전체 부서입니다.
          </p>
        </div>
      </div>

      {error && (
        <Alert variant="destructive">
          <AlertDescription>{error}</AlertDescription>
        </Alert>
      )}

      <DialogFooter>
        <Button type="button" variant="outline" onClick={onClose}>
          취소
        </Button>
        <Button type="submit" disabled={busy}>
          {busy ? "저장 중…" : policy ? "변경 사항 저장" : "정책 추가"}
        </Button>
      </DialogFooter>
    </form>
  );
}

function RecipientRow({
  recipient,
  onChange,
  onRemove,
}: {
  recipient: NotificationRecipient;
  onChange: (next: NotificationRecipient) => void;
  onRemove: () => void;
}) {
  return (
    <div className="flex items-center gap-2">
      <Select
        value={recipient.kind}
        onValueChange={(v) =>
          // 종류를 바꾸면 이전 종류의 참조값은 버린다(서버 검증이 kind별로 다르다).
          onChange({ kind: v as NotificationRecipientKind })
        }
      >
        <SelectTrigger className="w-44">
          <SelectValue />
        </SelectTrigger>
        <SelectContent>
          {RECIPIENT_KINDS.map((k) => (
            <SelectItem key={k.value} value={k.value}>
              {k.label}
            </SelectItem>
          ))}
        </SelectContent>
      </Select>

      {recipient.kind === "role" && (
        <Select
          value={recipient.role ?? ""}
          onValueChange={(v) => onChange({ kind: "role", role: v as Role })}
        >
          <SelectTrigger className="flex-1">
            <SelectValue placeholder="역할 선택" />
          </SelectTrigger>
          <SelectContent>
            {ALL_ROLES.map((r) => (
              <SelectItem key={r} value={r}>
                {roleLabel(r)}
              </SelectItem>
            ))}
          </SelectContent>
        </Select>
      )}
      {recipient.kind === "user" && (
        <Input
          className="flex-1"
          placeholder="사용자 UUID"
          value={recipient.user_id ?? ""}
          onChange={(e) => onChange({ kind: "user", user_id: e.target.value })}
        />
      )}
      {recipient.kind === "static" && (
        <Input
          className="flex-1"
          type="email"
          placeholder="ops@example.com"
          value={recipient.email ?? ""}
          onChange={(e) => onChange({ kind: "static", email: e.target.value })}
        />
      )}
      {recipient.kind === "owner" && (
        <span className="flex-1 text-xs text-muted-foreground">
          인증서에 지정된 소유자에게 보냅니다.
        </span>
      )}

      <Button type="button" variant="ghost" size="icon-sm" onClick={onRemove} aria-label="수신자 삭제">
        <Trash2 />
      </Button>
    </div>
  );
}

function DeletePolicyDialog({
  policy,
  onOpenChange,
  onDeleted,
}: {
  policy: NotificationPolicy | null;
  onOpenChange: (open: boolean) => void;
  onDeleted: () => void;
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
    if (!policy) return;
    setBusy(true);
    setError(null);
    try {
      await deleteNotificationPolicy(policy.id);
      onDeleted();
      handleOpenChange(false);
    } catch (err) {
      setError(errorMessage(err, "정책 삭제에 실패했습니다."));
      setBusy(false);
    }
  }

  return (
    <Dialog open={policy != null} onOpenChange={handleOpenChange}>
      <DialogContent>
        <DialogHeader>
          <DialogTitle>알림 정책 삭제</DialogTitle>
          <DialogDescription>
            <strong>{policy?.name}</strong> 정책을 삭제합니다. 이미 만들어진
            알림과 발송 이력은 그대로 남고, 앞으로 이 조건으로는 알림이 만들어지지
            않습니다.
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
            onClick={() => void confirm()}
          >
            {busy ? "삭제 중…" : "삭제"}
          </Button>
        </DialogFooter>
      </DialogContent>
    </Dialog>
  );
}

// ===== 테스트 발송 =====

function TestDialog({
  policy,
  onOpenChange,
}: {
  policy: NotificationPolicy | null;
  onOpenChange: (open: boolean) => void;
}) {
  const [channel, setChannel] = React.useState("all");
  const [recipient, setRecipient] = React.useState("");
  const [results, setResults] = React.useState<
    NotificationTestChannelResult[] | null
  >(null);
  const [busy, setBusy] = React.useState(false);
  const [error, setError] = React.useState<string | null>(null);

  function handleOpenChange(next: boolean) {
    onOpenChange(next);
    if (!next) {
      setChannel("all");
      setRecipient("");
      setResults(null);
      setError(null);
      setBusy(false);
    }
  }

  async function send() {
    if (!policy) return;
    setBusy(true);
    setError(null);
    setResults(null);
    try {
      const res = await testNotificationPolicy(policy.id, {
        channel: channel === "all" ? undefined : (channel as NotificationChannel),
        recipient: recipient.trim() || undefined,
      });
      setResults(res.results);
    } catch (err) {
      // 배선이 없으면 503(channels_unavailable)이다 — 시도 자체는 감사에 남는다.
      setError(errorMessage(err, "테스트 발송에 실패했습니다."));
    } finally {
      setBusy(false);
    }
  }

  return (
    <Dialog open={policy != null} onOpenChange={handleOpenChange}>
      <DialogContent className="sm:max-w-lg">
        <DialogHeader>
          <DialogTitle>테스트 발송</DialogTitle>
          <DialogDescription>
            <strong>{policy?.name}</strong> 정책이 쓰는 채널로 테스트 알림을 한 번
            보냅니다. 실제 만료 알림은 만들어지지 않으며, 발송 시도는 감사 로그에
            남습니다.
          </DialogDescription>
        </DialogHeader>

        <div className="space-y-2">
          <Label htmlFor="test-channel">채널</Label>
          <Select value={channel} onValueChange={setChannel}>
            <SelectTrigger id="test-channel" className="w-full">
              <SelectValue />
            </SelectTrigger>
            <SelectContent>
              <SelectItem value="all">정책의 전 채널</SelectItem>
              {policy?.channels.map((c) => (
                <SelectItem key={c} value={c}>
                  {CHANNEL_LABELS[c] ?? c}
                </SelectItem>
              ))}
            </SelectContent>
          </Select>
        </div>
        <div className="space-y-2">
          <Label htmlFor="test-recipient">수신자 (선택)</Label>
          <Input
            id="test-recipient"
            placeholder="비우면 채널 기본 수신자로 보냅니다"
            value={recipient}
            onChange={(e) => setRecipient(e.target.value)}
          />
        </div>

        {error && (
          <Alert variant="destructive">
            <AlertTitle>보내지 못했습니다</AlertTitle>
            <AlertDescription>{error}</AlertDescription>
          </Alert>
        )}

        {results != null && (
          <div className="rounded-md border">
            {results.length === 0 ? (
              <p className="p-3 text-[13px] text-muted-foreground">
                시도한 채널이 없습니다.
              </p>
            ) : (
              results.map((r) => (
                <div
                  key={r.channel}
                  className="flex items-start gap-2 border-b p-3 last:border-b-0"
                >
                  <span className="w-16 text-[13px] font-medium">
                    {CHANNEL_LABELS[r.channel] ?? r.channel}
                  </span>
                  <TestResultBadge status={r.status} />
                  <span className="flex-1 text-xs text-muted-foreground">
                    {r.status === "sent"
                      ? "발송했습니다."
                      : r.reason || "사유가 보고되지 않았습니다."}
                  </span>
                </div>
              ))
            )}
          </div>
        )}

        <DialogFooter>
          <Button
            type="button"
            variant="outline"
            onClick={() => handleOpenChange(false)}
          >
            닫기
          </Button>
          <Button type="button" disabled={busy} onClick={() => void send()}>
            {busy ? "보내는 중…" : "테스트 보내기"}
          </Button>
        </DialogFooter>
      </DialogContent>
    </Dialog>
  );
}

function TestResultBadge({ status }: { status: "sent" | "skipped" | "failed" }) {
  if (status === "sent") return <Badge variant="outline">발송</Badge>;
  if (status === "skipped") {
    return (
      <Badge variant="secondary" title="보낼 수 없어 건너뛰었습니다(실패 아님).">
        건너뜀
      </Badge>
    );
  }
  return <Badge variant="destructive">실패</Badge>;
}

// ===== 발송 이력 =====

function DeliveryStatusBadge({ status }: { status: NotificationDeliveryStatus }) {
  const label = DELIVERY_STATUS_LABELS[status] ?? status;
  switch (status) {
    case "sent":
      return <Badge variant="outline">{label}</Badge>;
    case "failed":
      return <Badge variant="destructive">{label}</Badge>;
    case "skipped":
      return (
        <Badge
          variant="secondary"
          title="보낼 수 없어 건너뛴 상태입니다 — 실패가 아니며 재시도하지 않습니다."
        >
          {label}
        </Badge>
      );
    default:
      return <Badge variant="secondary">{label}</Badge>;
  }
}

function DeliveryHistoryCard() {
  const [items, setItems] = React.useState<NotificationDelivery[] | null>(null);
  const [total, setTotal] = React.useState(0);
  const [status, setStatus] = React.useState("all");
  const [channel, setChannel] = React.useState("all");
  const [error, setError] = React.useState<string | null>(null);
  const [reloadKey, setReloadKey] = React.useState(0);

  React.useEffect(() => {
    let cancelled = false;
    listNotificationDeliveries({
      status:
        status === "all" ? undefined : (status as NotificationDeliveryStatus),
      channel: channel === "all" ? undefined : (channel as NotificationChannel),
      pageSize: 50,
    })
      .then((res) => {
        if (cancelled) return;
        setItems(res.items);
        setTotal(res.total);
        setError(null);
      })
      .catch((err: unknown) => {
        if (!cancelled) setError(errorMessage(err, "발송 이력을 불러오지 못했습니다."));
      });
    return () => {
      cancelled = true;
    };
  }, [status, channel, reloadKey]);

  return (
    <Card className="gap-0 overflow-hidden py-0">
      <div className="flex flex-wrap items-center justify-between gap-3 border-b px-4 py-3">
        <div>
          <h2 className="text-sm font-semibold tracking-tight">발송 이력</h2>
          <p className="text-xs text-muted-foreground">
            &ldquo;왜 안 왔는지&rdquo;를 확인하는 곳입니다.{" "}
            <strong>건너뜀</strong>은 실패가 아니라 보낼 수 없어 넘어간 상태입니다
            — 채널이 꺼져 있거나 수신자를 찾지 못한 경우이며 재시도하지 않습니다.
            <strong> 실패</strong>는 5회까지 간격을 늘려 가며 재시도합니다.
          </p>
        </div>
        <div className="flex items-center gap-2">
          <Select value={status} onValueChange={setStatus}>
            <SelectTrigger className="w-[140px]">
              <SelectValue />
            </SelectTrigger>
            <SelectContent>
              <SelectItem value="all">상태: 전체</SelectItem>
              {DELIVERY_STATUSES.map((s) => (
                <SelectItem key={s} value={s}>
                  {DELIVERY_STATUS_LABELS[s]}
                </SelectItem>
              ))}
            </SelectContent>
          </Select>
          <Select value={channel} onValueChange={setChannel}>
            <SelectTrigger className="w-[140px]">
              <SelectValue />
            </SelectTrigger>
            <SelectContent>
              <SelectItem value="all">채널: 전체</SelectItem>
              {CHANNELS.map((c) => (
                <SelectItem key={c.value} value={c.value}>
                  {c.label}
                </SelectItem>
              ))}
            </SelectContent>
          </Select>
          <Button
            variant="outline"
            size="sm"
            onClick={() => setReloadKey((k) => k + 1)}
          >
            <RefreshCw data-icon="inline-start" />
            새로 고침
          </Button>
        </div>
      </div>

      {error && (
        <div className="p-4">
          <Alert variant="destructive">
            <AlertDescription>{error}</AlertDescription>
          </Alert>
        </div>
      )}
      {items == null && !error && (
        <div className="p-4">
          <Skeleton className="h-32 rounded-md" />
        </div>
      )}

      {items != null && items.length === 0 && (
        <p className="px-4 py-10 text-center text-[13px] text-muted-foreground">
          조건에 맞는 발송 기록이 없습니다. 알림이 한 건도 만들어지지 않았거나,
          만료 검사 주기가 아직 돌지 않았을 수 있습니다.
        </p>
      )}

      {items != null && items.length > 0 && (
        <>
          <Table>
            <TableHeader>
              <TableRow>
                <TableHead className="w-40">시각</TableHead>
                <TableHead className="w-24">채널</TableHead>
                <TableHead className="w-24">상태</TableHead>
                <TableHead className="w-40">사건</TableHead>
                <TableHead className="w-48">수신자</TableHead>
                <TableHead className="w-16">시도</TableHead>
                <TableHead>마지막 오류</TableHead>
              </TableRow>
            </TableHeader>
            <TableBody>
              {items.map((d) => (
                <TableRow key={d.id}>
                  <TableCell className="font-mono text-xs tabular-nums">
                    {formatDateTime(d.sent_at ?? d.created_at)}
                  </TableCell>
                  <TableCell className="text-[13px]">
                    {CHANNEL_LABELS[d.channel] ?? d.channel}
                  </TableCell>
                  <TableCell>
                    <DeliveryStatusBadge status={d.status} />
                  </TableCell>
                  <TableCell className="text-[13px]">
                    {EVENT_LABELS[d.event_type] ?? d.event_type}
                    {d.threshold_days != null && (
                      <span className="text-muted-foreground">
                        {" "}
                        D-{d.threshold_days}
                      </span>
                    )}
                  </TableCell>
                  <TableCell className="truncate font-mono text-xs">
                    {d.recipient || "—"}
                  </TableCell>
                  <TableCell className="text-xs tabular-nums">
                    {d.attempt_count}
                  </TableCell>
                  <TableCell className="text-xs text-muted-foreground">
                    {d.last_error ?? "—"}
                  </TableCell>
                </TableRow>
              ))}
            </TableBody>
          </Table>
          {total > items.length && (
            <p className="border-t px-4 py-2 text-xs text-muted-foreground">
              최근 {items.length}건을 보여줍니다 (전체 {total}건). 상태·채널
              필터로 범위를 좁히십시오.
            </p>
          )}
        </>
      )}
    </Card>
  );
}
