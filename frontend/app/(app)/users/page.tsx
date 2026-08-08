"use client";

import * as React from "react";
import { Suspense } from "react";
import { usePathname, useRouter, useSearchParams } from "next/navigation";
import { KeyRound, Search, UserPlus } from "lucide-react";

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
import {
  Table,
  TableBody,
  TableCell,
  TableHead,
  TableHeader,
  TableRow,
} from "@/components/ui/table";
import { ALL_ROLES, isSystemAdministrator, roleLabel, useAuth } from "@/lib/auth";
import {
  ApiError,
  createUser,
  disableUser,
  enableUser,
  listUsers,
  resetUserPassword,
  updateUser,
  type Role,
  type User,
} from "@/lib/api";
import { formatDate } from "@/lib/format";
import { cn } from "@/lib/utils";

/** 백엔드 auth 정책과 같은 하한 (internal/auth 비밀번호 정책). */
const MIN_PASSWORD_LENGTH = 12;

/**
 * 서버 거부 사유를 운영자 언어로 바꾼다.
 * 코드는 backend/internal/api/handlers_users.go(writeUserMutationError)의 실제 값이고,
 * 명시 코드표 → 키워드 → 서버 메시지 순으로 낮춰 잡는다(키워드 단계는 안전망).
 */
const ERROR_MESSAGES: Record<string, string> = {
  last_admin_protected:
    "마지막 관리자 계정은 비활성화하거나 권한을 낮출 수 없습니다.",
  self_disable_forbidden: "자기 자신의 계정은 비활성화할 수 없습니다.",
  self_role_change_forbidden: "자기 자신의 역할은 변경할 수 없습니다.",
  weak_password: `비밀번호가 정책에 미달합니다. ${MIN_PASSWORD_LENGTH}자 이상, 사용자 이름과 다른 값으로 설정하십시오.`,
  no_fields: "변경할 항목이 없습니다. 역할이나 표시 이름을 바꾸십시오.",
  invalid_body: "요청 형식이 올바르지 않습니다. 입력값을 확인하십시오.",
  username_taken: "이미 사용 중인 사용자 이름입니다.",
  invalid_role: "선택한 역할이 올바르지 않습니다.",
  not_found: "사용자를 찾을 수 없습니다. 목록을 새로 고치십시오.",
  forbidden: "이 작업을 수행할 권한이 없습니다.",
};

function errorMessage(err: unknown, fallback: string): string {
  if (!(err instanceof ApiError)) {
    return err instanceof Error ? err.message : fallback;
  }
  const code = err.code.toLowerCase();
  if (ERROR_MESSAGES[code]) return ERROR_MESSAGES[code];
  if (code.includes("last") && code.includes("admin")) {
    return ERROR_MESSAGES.last_admin_protected;
  }
  if (code.includes("self")) {
    return "자기 자신의 계정에는 적용할 수 없습니다. 다른 관리자에게 요청하십시오.";
  }
  if (code.includes("password")) return ERROR_MESSAGES.weak_password;
  return err.message || fallback;
}

export default function UsersPage() {
  return (
    <Suspense>
      <UsersBody />
    </Suspense>
  );
}

function UsersBody() {
  const router = useRouter();
  const pathname = usePathname();
  const searchParams = useSearchParams();
  const { user: me, loading } = useAuth();
  const canAdmin = isSystemAdministrator(me);

  const [users, setUsers] = React.useState<User[] | null>(null);
  const [error, setError] = React.useState<string | null>(null);
  const [reloadKey, setReloadKey] = React.useState(0);
  const [createOpen, setCreateOpen] = React.useState(false);
  const [editTarget, setEditTarget] = React.useState<User | null>(null);
  const [disableTarget, setDisableTarget] = React.useState<User | null>(null);
  const [passwordTarget, setPasswordTarget] = React.useState<User | null>(null);

  React.useEffect(() => {
    if (!canAdmin) return;
    let cancelled = false;
    listUsers()
      .then((res) => {
        if (!cancelled) {
          setUsers(res.items);
          setError(null);
        }
      })
      .catch((err: unknown) => {
        if (!cancelled) setError(errorMessage(err, "사용자 목록 조회에 실패했습니다."));
      });
    return () => {
      cancelled = true;
    };
  }, [canAdmin, reloadKey]);

  const reload = React.useCallback(() => setReloadKey((k) => k + 1), []);

  const setQuery = React.useCallback(
    (updates: Record<string, string | null>) => {
      const next = new URLSearchParams(searchParams.toString());
      for (const [k, v] of Object.entries(updates)) {
        if (v == null || v === "") next.delete(k);
        else next.set(k, v);
      }
      const qs = next.toString();
      router.replace(qs ? `${pathname}?${qs}` : pathname, { scroll: false });
    },
    [router, pathname, searchParams],
  );

  const q = searchParams.get("q") ?? "";
  const roleFilter = searchParams.get("role") ?? "all";
  const activeFilter = searchParams.get("active") ?? "all";
  const hasFilter = q !== "" || roleFilter !== "all" || activeFilter !== "all";

  /** 목록 API에 조회 조건이 없어 필터는 클라이언트에서 건다 (계정 수는 수백 규모). */
  const visible = React.useMemo(() => {
    if (users == null) return null;
    const needle = q.trim().toLowerCase();
    return users.filter((u) => {
      if (roleFilter !== "all" && u.role !== roleFilter) return false;
      if (activeFilter === "active" && !u.is_active) return false;
      if (activeFilter === "inactive" && u.is_active) return false;
      if (needle === "") return true;
      return (
        u.username.toLowerCase().includes(needle) ||
        (u.display_name ?? "").toLowerCase().includes(needle)
      );
    });
  }, [users, q, roleFilter, activeFilter]);

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
            사용자 관리는 시스템 관리자만 사용할 수 있습니다. 계정 생성·권한
            변경이 필요하면 시스템 관리자에게 요청하십시오.
          </p>
        </Card>
      </div>
    );
  }

  return (
    <div className="mx-auto max-w-[1400px] space-y-4">
      <div className="flex items-end justify-between gap-4">
        <div>
          <h1 className="text-lg font-semibold tracking-tight">사용자</h1>
          <p className="text-[13px] text-muted-foreground">
            계정과 역할을 관리합니다. 퇴사·직무 변경 시 계정을 비활성화하면 이후
            로그인이 즉시 거부됩니다.
          </p>
        </div>
        <Button onClick={() => setCreateOpen(true)}>
          <UserPlus data-icon="inline-start" />
          사용자 추가
        </Button>
      </div>

      <div className="flex flex-wrap items-center gap-2">
        <SearchField initial={q} onSearch={(v) => setQuery({ q: v })} />
        <Select
          value={roleFilter}
          onValueChange={(v) => setQuery({ role: v === "all" ? null : v })}
        >
          <SelectTrigger className="w-[160px]">
            <SelectValue />
          </SelectTrigger>
          <SelectContent>
            <SelectItem value="all">역할: 전체</SelectItem>
            {ALL_ROLES.map((r) => (
              <SelectItem key={r} value={r}>
                {roleLabel(r)}
              </SelectItem>
            ))}
          </SelectContent>
        </Select>
        <Select
          value={activeFilter}
          onValueChange={(v) => setQuery({ active: v === "all" ? null : v })}
        >
          <SelectTrigger className="w-[140px]">
            <SelectValue />
          </SelectTrigger>
          <SelectContent>
            <SelectItem value="all">상태: 전체</SelectItem>
            <SelectItem value="active">활성</SelectItem>
            <SelectItem value="inactive">비활성</SelectItem>
          </SelectContent>
        </Select>
        {hasFilter && (
          <Button
            variant="outline"
            size="sm"
            className="ml-auto"
            onClick={() => router.replace(pathname)}
          >
            초기화
          </Button>
        )}
      </div>

      {error && (
        <Alert variant="destructive">
          <AlertTitle>사용자 목록을 불러오지 못했습니다</AlertTitle>
          <AlertDescription>{error}</AlertDescription>
        </Alert>
      )}

      {users == null && !error && <Skeleton className="h-96 rounded-lg" />}

      {visible != null && (
        <Card className="gap-0 overflow-hidden py-0">
          <div className="flex items-baseline gap-2 border-b px-4 py-3">
            <h2 className="text-sm font-semibold tracking-tight">계정 목록</h2>
            <span className="text-xs text-muted-foreground">
              {visible.length}명
              {users != null && visible.length !== users.length
                ? ` (전체 ${users.length}명)`
                : ""}
            </span>
          </div>
          {visible.length === 0 ? (
            <p className="px-4 py-12 text-center text-sm text-muted-foreground">
              {hasFilter
                ? "조건에 맞는 사용자가 없습니다. 필터를 초기화해 보십시오."
                : "등록된 사용자가 없습니다."}
            </p>
          ) : (
            <Table>
              <TableHeader>
                <TableRow>
                  <TableHead>사용자</TableHead>
                  <TableHead className="w-40">역할</TableHead>
                  <TableHead className="w-28">상태</TableHead>
                  <TableHead className="w-32">인증</TableHead>
                  <TableHead className="w-32">생성일</TableHead>
                  <TableHead className="w-72 text-right" />
                </TableRow>
              </TableHeader>
              <TableBody>
                {visible.map((u) => {
                  const isSelf = u.id === me?.id;
                  return (
                    <TableRow
                      key={u.id}
                      // 비활성 계정은 행 전체를 흐리게 낮춰 활성 계정과 구분한다.
                      className={cn(!u.is_active && "bg-muted/30")}
                    >
                      <TableCell>
                        <div
                          className={cn(
                            "font-mono text-xs font-medium",
                            !u.is_active && "text-muted-foreground line-through",
                          )}
                        >
                          {u.username}
                        </div>
                        <div className="text-[13px] text-muted-foreground">
                          {u.display_name || "표시이름 없음"}
                          {isSelf && (
                            <Badge variant="secondary" className="ml-1.5">
                              나
                            </Badge>
                          )}
                        </div>
                      </TableCell>
                      <TableCell className="text-[13px]">
                        {roleLabel(u.role)}
                      </TableCell>
                      <TableCell>
                        {u.is_active ? (
                          <Badge variant="outline">활성</Badge>
                        ) : (
                          <Badge variant="secondary">비활성</Badge>
                        )}
                      </TableCell>
                      <TableCell className="font-mono text-xs text-muted-foreground">
                        {u.auth_provider}
                      </TableCell>
                      <TableCell className="font-mono text-xs tabular-nums text-muted-foreground">
                        {formatDate(u.created_at)}
                      </TableCell>
                      <TableCell className="text-right whitespace-nowrap">
                        <Button
                          variant="ghost"
                          size="sm"
                          onClick={() => setEditTarget(u)}
                        >
                          수정
                        </Button>
                        <Button
                          variant="ghost"
                          size="sm"
                          onClick={() => setPasswordTarget(u)}
                        >
                          <KeyRound data-icon="inline-start" />
                          비밀번호
                        </Button>
                        {u.is_active ? (
                          <Button
                            variant="ghost"
                            size="sm"
                            className="text-destructive hover:bg-destructive/10 hover:text-destructive"
                            disabled={isSelf}
                            title={
                              isSelf
                                ? "자기 자신의 계정은 비활성화할 수 없습니다."
                                : undefined
                            }
                            onClick={() => setDisableTarget(u)}
                          >
                            비활성화
                          </Button>
                        ) : (
                          <EnableButton
                            user={u}
                            onDone={reload}
                            onError={setError}
                          />
                        )}
                      </TableCell>
                    </TableRow>
                  );
                })}
              </TableBody>
            </Table>
          )}
        </Card>
      )}

      <CreateUserDialog
        open={createOpen}
        onOpenChange={setCreateOpen}
        onCreated={reload}
      />
      <EditUserDialog
        user={editTarget}
        isSelf={editTarget?.id === me?.id}
        onOpenChange={(open) => {
          if (!open) setEditTarget(null);
        }}
        onSaved={reload}
      />
      <DisableUserDialog
        user={disableTarget}
        onOpenChange={(open) => {
          if (!open) setDisableTarget(null);
        }}
        onDisabled={reload}
      />
      <ResetPasswordDialog
        user={passwordTarget}
        onOpenChange={(open) => {
          if (!open) setPasswordTarget(null);
        }}
      />
    </div>
  );
}

/** 검색 입력 — 목록 화면 관례대로 400ms 디바운스 + Enter 즉시 적용. */
function SearchField({
  initial,
  onSearch,
}: {
  initial: string;
  onSearch: (q: string) => void;
}) {
  const [value, setValue] = React.useState(initial);
  const timer = React.useRef<ReturnType<typeof setTimeout> | null>(null);

  function schedule(v: string) {
    setValue(v);
    if (timer.current) clearTimeout(timer.current);
    timer.current = setTimeout(() => onSearch(v.trim()), 400);
  }

  return (
    <div className="relative w-64">
      <Search className="pointer-events-none absolute top-2 left-2.5 size-3.5 text-muted-foreground/70" />
      <Input
        className="pl-8"
        placeholder="사용자 이름 / 표시이름 검색"
        value={value}
        onChange={(e) => schedule(e.target.value)}
        onKeyDown={(e) => {
          if (e.key === "Enter") {
            if (timer.current) clearTimeout(timer.current);
            onSearch(value.trim());
          }
        }}
      />
    </div>
  );
}

/** 활성화는 되돌릴 수 있는 작업이라 확인 없이 즉시 실행한다. */
function EnableButton({
  user,
  onDone,
  onError,
}: {
  user: User;
  onDone: () => void;
  onError: (msg: string) => void;
}) {
  const [busy, setBusy] = React.useState(false);

  async function run() {
    setBusy(true);
    try {
      await enableUser(user.id);
      onDone();
    } catch (err) {
      onError(errorMessage(err, "계정 활성화에 실패했습니다."));
    } finally {
      setBusy(false);
    }
  }

  return (
    <Button variant="ghost" size="sm" disabled={busy} onClick={() => void run()}>
      {busy ? "활성화 중…" : "활성화"}
    </Button>
  );
}

function RoleSelect({
  value,
  onChange,
  disabled,
  id,
}: {
  value: Role;
  onChange: (r: Role) => void;
  disabled?: boolean;
  id?: string;
}) {
  return (
    <Select
      value={value}
      onValueChange={(v) => onChange(v as Role)}
      disabled={disabled}
    >
      <SelectTrigger id={id} className="w-full">
        <SelectValue />
      </SelectTrigger>
      <SelectContent>
        {ALL_ROLES.map((r) => (
          <SelectItem key={r} value={r}>
            {roleLabel(r)}
          </SelectItem>
        ))}
      </SelectContent>
    </Select>
  );
}

function CreateUserDialog({
  open,
  onOpenChange,
  onCreated,
}: {
  open: boolean;
  onOpenChange: (open: boolean) => void;
  onCreated: () => void;
}) {
  const [username, setUsername] = React.useState("");
  const [displayName, setDisplayName] = React.useState("");
  const [password, setPassword] = React.useState("");
  const [role, setRole] = React.useState<Role>("viewer");
  const [busy, setBusy] = React.useState(false);
  const [error, setError] = React.useState<string | null>(null);

  function handleOpenChange(next: boolean) {
    onOpenChange(next);
    if (!next) {
      setUsername("");
      setDisplayName("");
      setPassword("");
      setRole("viewer");
      setError(null);
      setBusy(false);
    }
  }

  async function submit(e: React.FormEvent) {
    e.preventDefault();
    const id = username.trim();
    if (!id) {
      setError("사용자 이름을 입력하십시오.");
      return;
    }
    if (password.length < MIN_PASSWORD_LENGTH) {
      setError(`초기 비밀번호는 ${MIN_PASSWORD_LENGTH}자 이상이어야 합니다.`);
      return;
    }
    setBusy(true);
    setError(null);
    try {
      await createUser({
        username: id,
        password,
        display_name: displayName.trim() || null,
        role,
      });
      onCreated();
      handleOpenChange(false);
    } catch (err) {
      setError(errorMessage(err, "사용자 생성에 실패했습니다."));
      setBusy(false);
    }
  }

  return (
    <Dialog open={open} onOpenChange={handleOpenChange}>
      <DialogContent className="sm:max-w-lg">
        <form onSubmit={submit} className="grid gap-4">
          <DialogHeader>
            <DialogTitle>사용자 추가</DialogTitle>
            <DialogDescription>
              계정을 만들고 역할을 지정합니다. 초기 비밀번호는 첫 로그인 전에
              본인에게 안전한 경로로 전달하십시오.
            </DialogDescription>
          </DialogHeader>
          <div className="space-y-2">
            <Label htmlFor="new-username">
              사용자 이름 <span className="text-red-600">*</span>
            </Label>
            <Input
              id="new-username"
              required
              autoFocus
              autoComplete="off"
              spellCheck={false}
              placeholder="hong.gildong"
              value={username}
              onChange={(e) => setUsername(e.target.value)}
            />
          </div>
          <div className="space-y-2">
            <Label htmlFor="new-display-name">표시이름</Label>
            <Input
              id="new-display-name"
              autoComplete="off"
              placeholder="홍길동"
              value={displayName}
              onChange={(e) => setDisplayName(e.target.value)}
            />
          </div>
          <div className="space-y-2">
            <Label htmlFor="new-role">역할</Label>
            <RoleSelect id="new-role" value={role} onChange={setRole} />
          </div>
          <div className="space-y-2">
            <Label htmlFor="new-password">
              초기 비밀번호 <span className="text-red-600">*</span>
            </Label>
            <Input
              id="new-password"
              type="password"
              required
              autoComplete="new-password"
              value={password}
              onChange={(e) => setPassword(e.target.value)}
            />
            <p className="text-xs text-muted-foreground">
              {MIN_PASSWORD_LENGTH}자 이상, 사용자 이름과 다른 값이어야 합니다.
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
              {busy ? "생성 중…" : "사용자 추가"}
            </Button>
          </DialogFooter>
        </form>
      </DialogContent>
    </Dialog>
  );
}

function EditUserDialog({
  user,
  isSelf,
  onOpenChange,
  onSaved,
}: {
  user: User | null;
  isSelf: boolean;
  onOpenChange: (open: boolean) => void;
  onSaved: () => void;
}) {
  return (
    <Dialog open={user != null} onOpenChange={onOpenChange}>
      <DialogContent className="sm:max-w-lg">
        {user && (
          // key로 대상이 바뀔 때마다 폼을 새로 마운트한다 — 상태를 effect로 되맞추지 않는다.
          <EditUserForm
            key={user.id}
            user={user}
            isSelf={isSelf}
            onClose={() => onOpenChange(false)}
            onSaved={onSaved}
          />
        )}
      </DialogContent>
    </Dialog>
  );
}

function EditUserForm({
  user,
  isSelf,
  onClose,
  onSaved,
}: {
  user: User;
  isSelf: boolean;
  onClose: () => void;
  onSaved: () => void;
}) {
  const [displayName, setDisplayName] = React.useState(user.display_name ?? "");
  const [role, setRole] = React.useState<Role>(user.role);
  const [busy, setBusy] = React.useState(false);
  const [error, setError] = React.useState<string | null>(null);

  async function submit(e: React.FormEvent) {
    e.preventDefault();
    const nextName = displayName.trim() || null;
    const patch: { role?: Role; display_name?: string | null } = {};
    if (role !== user.role) patch.role = role;
    if (nextName !== (user.display_name ?? null)) patch.display_name = nextName;
    if (Object.keys(patch).length === 0) {
      onClose();
      return;
    }
    setBusy(true);
    setError(null);
    try {
      await updateUser(user.id, patch);
      onSaved();
      onClose();
    } catch (err) {
      setError(errorMessage(err, "사용자 수정에 실패했습니다."));
      setBusy(false);
    }
  }

  return (
    <form onSubmit={submit} className="grid gap-4">
      <DialogHeader>
        <DialogTitle>사용자 수정</DialogTitle>
        <DialogDescription>
          <span className="font-mono text-xs">{user.username}</span> 계정의
          표시이름과 역할을 바꿉니다. 역할 변경은 다음 요청부터 적용됩니다.
        </DialogDescription>
      </DialogHeader>
      <div className="space-y-2">
        <Label htmlFor="edit-display-name">표시이름</Label>
        <Input
          id="edit-display-name"
          autoComplete="off"
          placeholder="홍길동"
          value={displayName}
          onChange={(e) => setDisplayName(e.target.value)}
        />
      </div>
      <div className="space-y-2">
        <Label htmlFor="edit-role">역할</Label>
        <RoleSelect
          id="edit-role"
          value={role}
          onChange={setRole}
          disabled={isSelf}
        />
        {isSelf && (
          <p className="text-xs text-muted-foreground">
            자기 자신의 역할은 바꿀 수 없습니다. 다른 시스템 관리자에게
            요청하십시오.
          </p>
        )}
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
          {busy ? "저장 중…" : "변경 사항 저장"}
        </Button>
      </DialogFooter>
    </form>
  );
}

function DisableUserDialog({
  user,
  onOpenChange,
  onDisabled,
}: {
  user: User | null;
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
    if (!user) return;
    setBusy(true);
    setError(null);
    try {
      await disableUser(user.id);
      onDisabled();
      handleOpenChange(false);
    } catch (err) {
      setError(errorMessage(err, "계정 비활성화에 실패했습니다."));
      setBusy(false);
    }
  }

  return (
    <Dialog open={user != null} onOpenChange={handleOpenChange}>
      <DialogContent>
        <DialogHeader>
          <DialogTitle>계정 비활성화</DialogTitle>
          <DialogDescription>
            <strong>{user?.display_name || user?.username}</strong> 계정을
            비활성화합니다. 이후 로그인과 진행 중인 세션이 모두 거부됩니다.
            필요하면 목록에서 다시 활성화할 수 있습니다.
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
            {busy ? "비활성화 중…" : "비활성화"}
          </Button>
        </DialogFooter>
      </DialogContent>
    </Dialog>
  );
}

/**
 * 관리자 비밀번호 재설정. 관리자가 새 비밀번호를 직접 입력하고,
 * 서버는 204(본문 없음)로 답한다 — 임시 비밀번호를 돌려주지 않는다.
 * 재설정 즉시 해당 사용자의 기존 세션은 모두 폐기된다.
 */
function ResetPasswordDialog({
  user,
  onOpenChange,
}: {
  user: User | null;
  onOpenChange: (open: boolean) => void;
}) {
  const [password, setPassword] = React.useState("");
  const [busy, setBusy] = React.useState(false);
  const [error, setError] = React.useState<string | null>(null);
  const [done, setDone] = React.useState(false);

  function handleOpenChange(next: boolean) {
    onOpenChange(next);
    if (!next) {
      setPassword("");
      setError(null);
      setDone(false);
      setBusy(false);
    }
  }

  async function submit(e: React.FormEvent) {
    e.preventDefault();
    if (!user) return;
    if (password.length < MIN_PASSWORD_LENGTH) {
      setError(`새 비밀번호는 ${MIN_PASSWORD_LENGTH}자 이상이어야 합니다.`);
      return;
    }
    setBusy(true);
    setError(null);
    try {
      // 서버는 204(본문 없음)로 답한다 — 응답을 파싱하지 않는다.
      await resetUserPassword(user.id, password);
      setDone(true);
      setPassword("");
    } catch (err) {
      setError(errorMessage(err, "비밀번호 재설정에 실패했습니다."));
    } finally {
      setBusy(false);
    }
  }

  return (
    <Dialog open={user != null} onOpenChange={handleOpenChange}>
      <DialogContent className="sm:max-w-lg">
        {done ? (
          <div className="grid gap-4">
            <DialogHeader>
              <DialogTitle>비밀번호를 재설정했습니다</DialogTitle>
              <DialogDescription>
                <span className="font-mono text-xs">{user?.username}</span>{" "}
                계정의 비밀번호가 재설정되었습니다. 해당 사용자의 세션이 모두
                종료되었습니다. 방금 입력한 새 비밀번호를 본인에게 안전한 경로로
                전달하십시오.
              </DialogDescription>
            </DialogHeader>
            <DialogFooter>
              <Button type="button" onClick={() => handleOpenChange(false)}>
                닫기
              </Button>
            </DialogFooter>
          </div>
        ) : (
          <form onSubmit={submit} className="grid gap-4">
            <DialogHeader>
              <DialogTitle>비밀번호 재설정</DialogTitle>
              <DialogDescription>
                <strong>{user?.display_name || user?.username}</strong> 계정의
                비밀번호를 관리자 권한으로 바꿉니다. 재설정하는 순간 이 계정의
                모든 세션이 로그아웃됩니다.
              </DialogDescription>
            </DialogHeader>
            <div className="space-y-2">
              <Label htmlFor="reset-password">
                새 비밀번호 <span className="text-red-600">*</span>
              </Label>
              <Input
                id="reset-password"
                type="password"
                required
                autoFocus
                autoComplete="new-password"
                value={password}
                onChange={(e) => setPassword(e.target.value)}
              />
              <p className="text-xs text-muted-foreground">
                {MIN_PASSWORD_LENGTH}자 이상, 사용자 이름과 다른 값이어야 합니다.
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
              <Button type="submit" variant="destructive" disabled={busy}>
                {busy ? "재설정 중…" : "비밀번호 재설정"}
              </Button>
            </DialogFooter>
          </form>
        )}
      </DialogContent>
    </Dialog>
  );
}
