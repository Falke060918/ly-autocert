"use client";

/**
 * 알림함 (전 역할, 언제나 본인 소유 행만).
 *
 * 읽음·보관은 서버 왕복을 기다리지 않고 화면을 먼저 바꾼다(낙관적 업데이트) —
 * 실패하면 직전 스냅샷으로 되돌리고 사유를 띄운다. 서버가 감사 이벤트를 남기지
 * 않는 유일한 변경 API이기도 하다(설계 05-notifications 12 ① 승인된 예외).
 */

import * as React from "react";
import Link from "next/link";
import { Archive, CheckCheck, RefreshCw } from "lucide-react";

import {
  SeverityBadge,
  notifyInboxChanged,
  relativeTime,
  useTicker,
} from "@/components/notification-bell";
import { Alert, AlertDescription, AlertTitle } from "@/components/ui/alert";
import { Button } from "@/components/ui/button";
import { Card } from "@/components/ui/card";
import { Skeleton } from "@/components/ui/skeleton";
import { Tabs, TabsContent, TabsList, TabsTrigger } from "@/components/ui/tabs";
import {
  ApiError,
  archiveNotification,
  listNotifications,
  markAllNotificationsRead,
  markNotificationRead,
  type NotificationItem,
  type NotificationStatusFilter,
} from "@/lib/api";
import { formatDateTime } from "@/lib/format";

const PAGE_SIZE = 50;

/** 경과 시간 표시 갱신 주기 — 분 단위 표기라 1분이면 충분하다. */
const TICK_MS = 60_000;

const TABS: { value: NotificationStatusFilter; label: string }[] = [
  { value: "unread", label: "안 읽음" },
  { value: "all", label: "전체" },
  { value: "archived", label: "보관" },
];

/** 탭마다 "왜 비어 있는지"가 다르다 — 빈 화면에서 다음 행동을 알 수 있게 적는다. */
const EMPTY_STATE: Record<
  NotificationStatusFilter,
  { title: string; body: string }
> = {
  unread: {
    title: "안 읽은 알림이 없습니다",
    body: "만료가 임박한 인증서가 감지되면 알림 정책에 따라 이곳으로 도착합니다. 지금은 확인할 것이 없다는 뜻입니다.",
  },
  all: {
    title: "아직 도착한 알림이 없습니다",
    body: "서버가 주기적으로 인증서 만료를 검사해 정책 조건(임계일·환경·부서)에 맞는 건만 알림으로 만듭니다. 알림 정책이 꺼져 있거나 조건에 맞는 인증서가 없으면 이 목록은 비어 있습니다.",
  },
  archived: {
    title: "보관한 알림이 없습니다",
    body: "처리를 끝낸 알림을 보관하면 목록에서 빠져 이곳에 모입니다. 기록은 지워지지 않으므로 나중에 다시 확인할 수 있습니다.",
  },
};

function errorMessage(err: unknown, fallback: string): string {
  if (err instanceof ApiError) {
    if (err.status === 404) {
      return "이미 없는 알림입니다. 목록을 새로 고치십시오.";
    }
    return err.message || fallback;
  }
  return err instanceof Error ? err.message : fallback;
}

export default function NotificationsPage() {
  const [tab, setTab] = React.useState<NotificationStatusFilter>("unread");
  const [items, setItems] = React.useState<NotificationItem[] | null>(null);
  const [total, setTotal] = React.useState(0);
  const [unread, setUnread] = React.useState(0);
  const [page, setPage] = React.useState(1);
  const [loadError, setLoadError] = React.useState<string | null>(null);
  const [actionError, setActionError] = React.useState<string | null>(null);
  const [busy, setBusy] = React.useState(false);
  const [reloadKey, setReloadKey] = React.useState(0);
  const now = useTicker(TICK_MS);

  // 탭·페이지가 바뀌면 다시 가져온다. page>1이면 이어붙인다(더 보기).
  React.useEffect(() => {
    let cancelled = false;
    listNotifications(tab, { page, pageSize: PAGE_SIZE })
      .then((res) => {
        if (cancelled) return;
        setItems((prev) =>
          page === 1 || prev == null ? res.items : [...prev, ...res.items],
        );
        setTotal(res.total);
        setUnread(res.unread);
        setLoadError(null);
      })
      .catch((err: unknown) => {
        if (!cancelled) {
          setLoadError(errorMessage(err, "알림 목록을 불러오지 못했습니다."));
        }
      });
    return () => {
      cancelled = true;
    };
  }, [tab, page, reloadKey]);

  function switchTab(next: string) {
    setTab(next as NotificationStatusFilter);
    setItems(null);
    setPage(1);
    setActionError(null);
  }

  function reload() {
    setItems(null);
    setPage(1);
    setReloadKey((k) => k + 1);
  }

  /** 낙관적 변경의 공통 뼈대 — 실패하면 스냅샷으로 되돌린다. */
  async function optimistic(
    apply: (prev: NotificationItem[]) => NotificationItem[],
    unreadDelta: number,
    call: () => Promise<unknown>,
    fallbackMessage: string,
  ) {
    const snapshot = items;
    const prevUnread = unread;
    if (snapshot == null) return;
    setActionError(null);
    setItems(apply(snapshot));
    setUnread(Math.max(0, prevUnread + unreadDelta));
    setBusy(true);
    try {
      await call();
      notifyInboxChanged();
    } catch (err) {
      setItems(snapshot);
      setUnread(prevUnread);
      setActionError(errorMessage(err, fallbackMessage));
    } finally {
      setBusy(false);
    }
  }

  function markRead(item: NotificationItem) {
    const readAt = new Date().toISOString();
    void optimistic(
      (prev) =>
        prev.map((it) => (it.id === item.id ? { ...it, read_at: readAt } : it)),
      -1,
      () => markNotificationRead(item.id),
      "읽음 처리에 실패했습니다.",
    );
  }

  function archive(item: NotificationItem) {
    void optimistic(
      // 보관 탭에서는 그대로 두고(이미 보관함), 다른 탭에서는 목록에서 뺀다.
      (prev) =>
        tab === "archived" ? prev : prev.filter((it) => it.id !== item.id),
      item.read_at ? 0 : -1,
      () => archiveNotification(item.id),
      "보관에 실패했습니다.",
    );
  }

  function markAll() {
    const readAt = new Date().toISOString();
    void optimistic(
      (prev) => prev.map((it) => ({ ...it, read_at: it.read_at ?? readAt })),
      -unread,
      () => markAllNotificationsRead(),
      "전체 읽음 처리에 실패했습니다.",
    );
  }

  const loaded = items != null;
  const hasMore = loaded && items.length < total;

  return (
    <div className="mx-auto max-w-[1000px] space-y-4">
      {/* 제목은 화면에서 걷어냈지만 문서 구조(h1)는 스크린리더용으로 남긴다. */}
      <h1 className="sr-only">알림</h1>

      {loadError && (
        <Alert variant="destructive">
          <AlertTitle>알림을 불러오지 못했습니다</AlertTitle>
          <AlertDescription>{loadError}</AlertDescription>
        </Alert>
      )}
      {actionError && (
        <Alert variant="destructive">
          <AlertTitle>변경 사항을 되돌렸습니다</AlertTitle>
          <AlertDescription>{actionError}</AlertDescription>
        </Alert>
      )}

      <Tabs value={tab} onValueChange={switchTab}>
        {/* 제목 블록을 없앤 대신 탭 줄이 화면 머리다 — 액션도 같은 줄에 얹는다. */}
        <div className="flex flex-wrap items-center gap-2">
          <TabsList>
            {TABS.map((t) => (
              <TabsTrigger key={t.value} value={t.value}>
                {t.label}
                {t.value === "unread" && unread > 0 && (
                  <span className="ml-1.5 rounded-full bg-danger px-1.5 text-[10px] leading-4 font-medium text-danger-foreground tabular-nums">
                    {unread > 99 ? "99+" : unread}
                  </span>
                )}
              </TabsTrigger>
            ))}
          </TabsList>
          <div className="ml-auto flex items-center gap-2">
            <Button variant="outline" size="sm" onClick={reload} disabled={busy}>
              <RefreshCw data-icon="inline-start" />
              새로 고침
            </Button>
            <Button size="sm" onClick={markAll} disabled={busy || unread === 0}>
              <CheckCheck data-icon="inline-start" />
              전체 읽음
            </Button>
          </div>
        </div>

        <TabsContent value={tab} className="mt-3">
          {!loaded && !loadError && (
            <div className="space-y-2">
              <Skeleton className="h-20 rounded-lg" />
              <Skeleton className="h-20 rounded-lg" />
              <Skeleton className="h-20 rounded-lg" />
            </div>
          )}

          {loaded && items.length === 0 && (
            <Card className="p-10 text-center">
              <h2 className="text-sm font-semibold tracking-tight">
                {EMPTY_STATE[tab].title}
              </h2>
              <p className="mx-auto max-w-md text-[13px] text-muted-foreground">
                {EMPTY_STATE[tab].body}
              </p>
            </Card>
          )}

          {loaded && items.length > 0 && (
            <Card className="gap-0 overflow-hidden py-0">
              <ul>
                {items.map((item) => (
                  <NotificationRow
                    key={item.id}
                    item={item}
                    now={now}
                    busy={busy}
                    onRead={() => markRead(item)}
                    onArchive={() => archive(item)}
                  />
                ))}
              </ul>
            </Card>
          )}

          {hasMore && (
            <div className="pt-3 text-center">
              <Button
                variant="outline"
                size="sm"
                onClick={() => setPage((p) => p + 1)}
              >
                더 보기 ({items.length} / {total})
              </Button>
            </div>
          )}
        </TabsContent>
      </Tabs>
    </div>
  );
}

function NotificationRow({
  item,
  now,
  busy,
  onRead,
  onArchive,
}: {
  item: NotificationItem;
  now: number;
  busy: boolean;
  onRead: () => void;
  onArchive: () => void;
}) {
  const isUnread = item.read_at == null;
  return (
    <li
      className={`flex items-start gap-3 border-b px-4 py-3 last:border-b-0 ${
        isUnread ? "bg-background" : "bg-muted/30"
      }`}
    >
      {/* 안 읽음 표시는 색 점 하나로 — 목록 스캔 속도를 해치지 않는다. */}
      <span
        aria-hidden
        className={`mt-2 size-1.5 shrink-0 rounded-full ${
          isUnread ? "bg-danger" : "bg-transparent"
        }`}
      />
      <div className="min-w-0 flex-1 space-y-1">
        <div className="flex flex-wrap items-center gap-2">
          <SeverityBadge severity={item.severity} />
          <span
            className={`text-sm ${isUnread ? "font-medium" : "text-muted-foreground"}`}
          >
            {item.title}
          </span>
          <span
            className="text-xs text-muted-foreground tabular-nums"
            title={formatDateTime(item.created_at)}
          >
            {relativeTime(item.created_at, now)}
          </span>
          {item.archived_at && (
            <span className="text-xs text-muted-foreground">· 보관됨</span>
          )}
        </div>
        <p className="text-[13px] whitespace-pre-line text-muted-foreground">
          {item.body}
        </p>
        {item.certificate_id && (
          <Link
            href={`/certificates/${item.certificate_id}`}
            className="inline-block text-xs font-medium text-primary hover:underline"
          >
            인증서 상세 보기 →
          </Link>
        )}
      </div>
      <div className="flex shrink-0 items-center gap-1">
        {isUnread && (
          <Button variant="ghost" size="sm" disabled={busy} onClick={onRead}>
            읽음
          </Button>
        )}
        {item.archived_at == null && (
          <Button variant="ghost" size="sm" disabled={busy} onClick={onArchive}>
            <Archive data-icon="inline-start" />
            보관
          </Button>
        )}
      </div>
    </li>
  );
}
