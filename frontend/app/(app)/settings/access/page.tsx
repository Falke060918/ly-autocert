"use client";

/**
 * 접근 제한 (System Administrator 전용).
 *
 * 이 화면의 첫 번째 목적은 "운영자가 자기 자신을 막지 않게 하는 것"이다.
 * 그래서 서버가 판정한 내 출발지 IP(access.client_ip)를 항상 가장 크게 보여주고,
 * 추가 폼의 기본값으로도 그 값을 채워 넣는다. 서버도 같은 판정을 트랜잭션 안에서
 * 다시 하므로(409 self_lockout_forbidden), 화면은 안내이고 서버가 최종 방어선이다.
 *
 * 활성 규칙이 0건이면 제한 자체가 없다 — "규칙을 다 끄면 잠긴다"가 아니라 "다 열린다".
 */

import * as React from "react";
import { Plus, ShieldAlert, Trash2 } from "lucide-react";

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
import { isSystemAdministrator, useAuth } from "@/lib/auth";
import {
  ApiError,
  createAdminIPRule,
  deleteAdminIPRule,
  listAdminIPRules,
  setAdminIPRuleActive,
  type AdminIPAccessState,
  type AdminIPRule,
} from "@/lib/api";
import { formatDateTime } from "@/lib/format";

/**
 * 서버 거부 사유를 운영자 언어로 바꾼다.
 * 코드는 backend/internal/api/adminip.go(writeAdminIPMutationError)의 실제 값이다.
 * self_lockout_forbidden만은 서버 메시지에 "막히는 주소"가 들어 있으므로 그대로 쓴다.
 */
const ERROR_MESSAGES: Record<string, string> = {
  cidr_taken: "이미 등록된 IP 또는 대역입니다.",
  client_ip_unknown:
    "서버가 이 요청의 출발지 IP를 판정하지 못했습니다. 프록시 뒤라면 ADMIN_IP_TRUSTED_PROXIES 설정을 확인하십시오.",
  invalid_cidr: "IP 주소나 CIDR 대역 형식이 아닙니다.",
  invalid_description: "설명이 너무 깁니다(200자 이하).",
  invalid_body: "요청 형식이 올바르지 않습니다. 입력값을 확인하십시오.",
  no_fields: "변경할 항목이 없습니다.",
  not_found: "규칙을 찾을 수 없습니다. 목록을 새로 고치십시오.",
  forbidden: "이 작업을 수행할 권한이 없습니다.",
  ip_not_allowed: "현재 접속한 IP는 이 콘솔에 접근할 수 없습니다.",
};

function errorMessage(err: unknown, fallback: string): string {
  if (!(err instanceof ApiError)) {
    return err instanceof Error ? err.message : fallback;
  }
  if (err.code === "self_lockout_forbidden") {
    // 서버 메시지에 차단될 주소가 들어 있다 — 번역보다 원문이 더 쓸모 있다.
    return `이 변경은 지금 접속 중인 주소를 차단합니다. 먼저 내 IP를 덮는 규칙을 추가하십시오. (${err.message})`;
  }
  return ERROR_MESSAGES[err.code] ?? err.message ?? fallback;
}

export default function AccessSettingsPage() {
  const { user, loading } = useAuth();
  const canAdmin = isSystemAdministrator(user);

  const [rules, setRules] = React.useState<AdminIPRule[] | null>(null);
  const [access, setAccess] = React.useState<AdminIPAccessState | null>(null);
  const [error, setError] = React.useState<string | null>(null);
  const [actionError, setActionError] = React.useState<string | null>(null);
  const [reloadKey, setReloadKey] = React.useState(0);
  const [addOpen, setAddOpen] = React.useState(false);
  const [deleteTarget, setDeleteTarget] = React.useState<AdminIPRule | null>(
    null,
  );
  const [busyId, setBusyId] = React.useState<string | null>(null);

  React.useEffect(() => {
    if (!canAdmin) return;
    let cancelled = false;
    listAdminIPRules()
      .then((res) => {
        if (cancelled) return;
        setRules(res.items);
        setAccess(res.access);
        setError(null);
      })
      .catch((err: unknown) => {
        if (!cancelled) setError(errorMessage(err, "허용 목록 조회에 실패했습니다."));
      });
    return () => {
      cancelled = true;
    };
  }, [canAdmin, reloadKey]);

  const reload = React.useCallback(() => setReloadKey((k) => k + 1), []);

  const toggle = React.useCallback(
    async (rule: AdminIPRule) => {
      setBusyId(rule.id);
      setActionError(null);
      try {
        await setAdminIPRuleActive(rule.id, !rule.is_active);
        reload();
      } catch (err) {
        setActionError(errorMessage(err, "규칙 상태를 바꾸지 못했습니다."));
      } finally {
        setBusyId(null);
      }
    },
    [reload],
  );

  const remove = React.useCallback(async () => {
    if (!deleteTarget) return;
    setBusyId(deleteTarget.id);
    setActionError(null);
    try {
      await deleteAdminIPRule(deleteTarget.id);
      setDeleteTarget(null);
      reload();
    } catch (err) {
      setActionError(errorMessage(err, "규칙을 삭제하지 못했습니다."));
    } finally {
      setBusyId(null);
    }
  }, [deleteTarget, reload]);

  if (loading) {
    return <Skeleton className="h-96 rounded-lg" />;
  }

  // 화면 자체가 System Administrator 전용이다 — 사이드바 메뉴도 같은 조건으로 숨긴다.
  if (!canAdmin) {
    return (
      <div className="mx-auto max-w-2xl">
        <Card className="p-8 text-center">
          <h1 className="text-base font-semibold tracking-tight">
            접근 권한이 없습니다
          </h1>
          <p className="text-[13px] text-muted-foreground">
            접근 제한 설정은 시스템 관리자만 사용할 수 있습니다.
          </p>
        </Card>
      </div>
    );
  }

  const activeCount = (rules ?? []).filter((r) => r.is_active).length;
  const myIP = access?.client_ip ?? "";
  const covered =
    activeCount === 0 ||
    (rules ?? []).some((r) => r.is_active && cidrCovers(r.cidr, myIP));

  return (
    <div className="mx-auto max-w-[1100px] space-y-4">
      <div className="flex items-end justify-between gap-4">
        <div>
          <h1 className="text-lg font-semibold tracking-tight">접근 제한</h1>
          <p className="text-[13px] text-muted-foreground">
            이 콘솔에 접속할 수 있는 출발지 IP를 제한합니다. 허용 목록이 비어
            있으면 제한이 없습니다.
          </p>
        </div>
        <Button onClick={() => setAddOpen(true)}>
          <Plus data-icon="inline-start" />
          허용 IP 추가
        </Button>
      </div>

      <CurrentAccessCard
        access={access}
        activeCount={activeCount}
        covered={covered}
      />

      {error && (
        <Alert variant="destructive">
          <AlertTitle>허용 목록을 불러오지 못했습니다</AlertTitle>
          <AlertDescription>{error}</AlertDescription>
        </Alert>
      )}
      {actionError && (
        <Alert variant="destructive">
          <ShieldAlert />
          <AlertTitle>변경이 거부되었습니다</AlertTitle>
          <AlertDescription>{actionError}</AlertDescription>
        </Alert>
      )}

      <Card className="overflow-hidden p-0">
        <Table>
          <TableHeader>
            <TableRow>
              <TableHead className="w-[220px]">IP / 대역</TableHead>
              <TableHead>설명</TableHead>
              <TableHead className="w-[150px]">등록</TableHead>
              <TableHead className="w-[110px]">활성</TableHead>
              <TableHead className="w-[80px]" />
            </TableRow>
          </TableHeader>
          <TableBody>
            {rules == null && (
              <TableRow>
                <TableCell colSpan={5}>
                  <Skeleton className="h-8" />
                </TableCell>
              </TableRow>
            )}
            {rules?.length === 0 && (
              <TableRow>
                <TableCell
                  colSpan={5}
                  className="py-10 text-center text-[13px] text-muted-foreground"
                >
                  허용 규칙이 없습니다 — 모든 출발지에서 접속할 수 있습니다.
                </TableCell>
              </TableRow>
            )}
            {rules?.map((rule) => {
              const isMine = cidrCovers(rule.cidr, myIP);
              return (
                <TableRow key={rule.id}>
                  <TableCell className="font-mono text-[13px]">
                    <div className="flex items-center gap-2">
                      {rule.cidr}
                      {isMine && (
                        <Badge variant="secondary" className="font-sans">
                          현재 내 접속
                        </Badge>
                      )}
                    </div>
                  </TableCell>
                  <TableCell className="text-[13px] text-muted-foreground">
                    {rule.description || "—"}
                  </TableCell>
                  <TableCell className="text-[13px] text-muted-foreground">
                    {formatDateTime(rule.created_at)}
                  </TableCell>
                  <TableCell>
                    <Switch
                      aria-label={`${rule.cidr} 활성`}
                      checked={rule.is_active}
                      disabled={busyId === rule.id}
                      onCheckedChange={() => void toggle(rule)}
                    />
                  </TableCell>
                  <TableCell>
                    <Button
                      variant="ghost"
                      size="icon"
                      aria-label={`${rule.cidr} 삭제`}
                      disabled={busyId === rule.id}
                      onClick={() => {
                        setActionError(null);
                        setDeleteTarget(rule);
                      }}
                    >
                      <Trash2 />
                    </Button>
                  </TableCell>
                </TableRow>
              );
            })}
          </TableBody>
        </Table>
      </Card>

      <AddRuleDialog
        open={addOpen}
        defaultCIDR={myIP}
        onOpenChange={setAddOpen}
        onCreated={() => {
          setAddOpen(false);
          setActionError(null);
          reload();
        }}
      />

      <Dialog
        open={deleteTarget != null}
        onOpenChange={(open) => !open && setDeleteTarget(null)}
      >
        <DialogContent>
          <DialogHeader>
            <DialogTitle>허용 규칙을 삭제할까요?</DialogTitle>
            <DialogDescription>
              {deleteTarget?.cidr}에서의 접속이 즉시 차단됩니다. 이 삭제로 지금
              접속 중인 주소가 막히면 서버가 거부합니다.
            </DialogDescription>
          </DialogHeader>
          <DialogFooter>
            <Button variant="outline" onClick={() => setDeleteTarget(null)}>
              취소
            </Button>
            <Button
              variant="destructive"
              disabled={busyId != null}
              onClick={() => void remove()}
            >
              삭제
            </Button>
          </DialogFooter>
        </DialogContent>
      </Dialog>
    </div>
  );
}

/** 서버가 본 내 출발지와 지금 제한이 걸려 있는지를 한눈에 보여준다. */
function CurrentAccessCard({
  access,
  activeCount,
  covered,
}: {
  access: AdminIPAccessState | null;
  activeCount: number;
  covered: boolean;
}) {
  if (access == null) {
    return <Skeleton className="h-24 rounded-lg" />;
  }
  return (
    <Card className="space-y-3 p-4">
      <div className="flex flex-wrap items-baseline gap-x-3 gap-y-1">
        <span className="text-[13px] text-muted-foreground">
          서버가 본 현재 내 접속 IP
        </span>
        <span className="font-mono text-lg font-semibold tracking-tight">
          {access.client_ip || "판정 불가"}
        </span>
        {access.enforced ? (
          <Badge variant="secondary">제한 적용 중 · 활성 {activeCount}건</Badge>
        ) : (
          <Badge variant="outline">제한 없음</Badge>
        )}
        {access.client_via_bootstrap && (
          <Badge variant="outline">비상 우회 대역으로 접속 중</Badge>
        )}
      </div>

      {!access.trusted_proxy_configured && (
        <Alert>
          <AlertTitle>이 값이 내 PC의 주소가 맞는지 확인하십시오</AlertTitle>
          <AlertDescription>
            신뢰 프록시(ADMIN_IP_TRUSTED_PROXIES)가 설정되어 있지 않아 서버는
            직접 접속한 상대의 IP만 봅니다. 브라우저와 백엔드 사이에 프록시(이
            제품의 프론트엔드 컨테이너 포함)가 있으면 여기 보이는 값은 그
            프록시의 주소이며, 그 값으로 규칙을 만들면 모두가 통과하게 됩니다.
          </AlertDescription>
        </Alert>
      )}
      {access.enforced && !covered && !access.client_via_bootstrap && (
        <Alert variant="destructive">
          <ShieldAlert />
          <AlertTitle>지금 접속 IP가 허용 목록에 없습니다</AlertTitle>
          <AlertDescription>
            목록을 새로 고치면 접근이 거부될 수 있습니다. 내 IP를 덮는 규칙을
            먼저 추가하십시오.
          </AlertDescription>
        </Alert>
      )}
      {!access.bootstrap_configured && (
        <p className="text-xs text-muted-foreground">
          비상 우회 대역(ADMIN_IP_ALLOW_BOOTSTRAP)이 설정되어 있지 않습니다.
          설정해 두면 실수로 자신을 막았을 때 그 대역에서 복구할 수 있습니다.
        </p>
      )}
    </Card>
  );
}

/**
 * 폼 상태는 안쪽 컴포넌트가 들고 있다. Dialog는 닫히면 내용을 언마운트하므로
 * 다시 열 때마다 useState 초기값(= 지금 내 IP)으로 새로 시작한다 —
 * 이펙트 안에서 setState 하는 방식은 eslint(react-hooks/set-state-in-effect)가 막는다.
 */
function AddRuleDialog({
  open,
  defaultCIDR,
  onOpenChange,
  onCreated,
}: {
  open: boolean;
  defaultCIDR: string;
  onOpenChange: (open: boolean) => void;
  onCreated: () => void;
}) {
  return (
    <Dialog open={open} onOpenChange={onOpenChange}>
      <DialogContent>
        <AddRuleForm
          defaultCIDR={defaultCIDR}
          onCancel={() => onOpenChange(false)}
          onCreated={onCreated}
        />
      </DialogContent>
    </Dialog>
  );
}

function AddRuleForm({
  defaultCIDR,
  onCancel,
  onCreated,
}: {
  defaultCIDR: string;
  onCancel: () => void;
  onCreated: () => void;
}) {
  const [cidr, setCIDR] = React.useState(defaultCIDR);
  const [description, setDescription] = React.useState("");
  const [error, setError] = React.useState<string | null>(null);
  const [saving, setSaving] = React.useState(false);

  const submit = async (e: React.FormEvent) => {
    e.preventDefault();
    setSaving(true);
    setError(null);
    try {
      await createAdminIPRule({ cidr: cidr.trim(), description: description.trim() });
      onCreated();
    } catch (err) {
      setError(errorMessage(err, "규칙을 추가하지 못했습니다."));
    } finally {
      setSaving(false);
    }
  };

  return (
    <form onSubmit={(e) => void submit(e)}>
      <DialogHeader>
        <DialogDescription>
          단일 IP(10.0.0.5)와 대역(10.0.0.0/24)을 모두 받습니다. 첫 규칙을 넣는
          순간부터 목록 밖의 출발지는 차단됩니다.
        </DialogDescription>
      </DialogHeader>

      <div className="space-y-4 py-4">
        <div className="space-y-2">
          <Label htmlFor="admin-ip-cidr">IP 또는 CIDR 대역</Label>
          <Input
            id="admin-ip-cidr"
            required
            autoFocus
            className="font-mono"
            placeholder="10.0.0.0/24"
            value={cidr}
            onChange={(e) => setCIDR(e.target.value)}
          />
          {defaultCIDR !== "" && (
            <p className="text-xs text-muted-foreground">
              현재 내 접속 IP는 <span className="font-mono">{defaultCIDR}</span>
              입니다.
            </p>
          )}
        </div>
        <div className="space-y-2">
          <Label htmlFor="admin-ip-description">설명 (선택)</Label>
          <Input
            id="admin-ip-description"
            maxLength={200}
            placeholder="본사 운영망"
            value={description}
            onChange={(e) => setDescription(e.target.value)}
          />
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
        <Button type="submit" disabled={saving || cidr.trim() === ""}>
          추가
        </Button>
      </DialogFooter>
    </form>
  );
}

/**
 * 규칙이 이 IP를 덮는지 화면에서 미리 본다 (IPv4 전용 근사).
 * 서버 판정(postgres inet <<=)이 정본이며, 여기서 판단이 어긋나도 서버가 막는다.
 * IPv6는 표기 정규화가 필요해 화면 힌트에서는 문자열 일치만 본다.
 */
function cidrCovers(cidr: string, ip: string): boolean {
  if (ip === "") return false;
  const [network, bitsRaw] = cidr.split("/");
  const bits = Number(bitsRaw);
  const target = ipv4ToInt(ip);
  const base = ipv4ToInt(network);
  if (target == null || base == null || !Number.isFinite(bits)) {
    return network === ip;
  }
  if (bits <= 0) return true;
  // >>> 0으로 부호 없는 32비트로 되돌린다 (자바스크립트 비트 연산은 부호 있는 32비트다).
  const mask = bits >= 32 ? 0xffffffff : (~0 << (32 - bits)) >>> 0;
  return ((target & mask) >>> 0) === ((base & mask) >>> 0);
}

function ipv4ToInt(value: string): number | null {
  const parts = value.split(".");
  if (parts.length !== 4) return null;
  let out = 0;
  for (const part of parts) {
    if (!/^\d{1,3}$/.test(part)) return null;
    const n = Number(part);
    if (n > 255) return null;
    out = ((out << 8) | n) >>> 0;
  }
  return out;
}
