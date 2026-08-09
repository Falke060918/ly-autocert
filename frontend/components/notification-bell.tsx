"use client";

/**
 * 상단바 알림 벨 — 안 읽은 건수 배지 + 최근 알림 팝오버.
 *
 * 배지는 목록이 아니라 전용 카운트 엔드포인트(/notifications/unread-count)만
 * 주기 폴링한다. 주기가 Discovery(2s)보다 훨씬 긴 이유는 알림 생성 주기가
 * 스캔 진행률과 달리 분 단위이기 때문이다 (NOTIFY_SCAN_INTERVAL 기본 1h).
 * 목록은 팝오버를 열 때만 가져온다.
 */

import * as React from "react";
import Link from "next/link";
import { Bell } from "lucide-react";

import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import {
  Popover,
  PopoverContent,
  PopoverTrigger,
} from "@/components/ui/popover";
import { Skeleton } from "@/components/ui/skeleton";
import {
  getUnreadNotificationCount,
  listNotifications,
  type NotificationItem,
  type NotificationSeverity,
} from "@/lib/api";

/** 안 읽은 건수 폴링 주기. */
const UNREAD_POLL_MS = 60_000;

/** 팝오버에 보여줄 최근 알림 개수. */
const RECENT_LIMIT = 5;

/**
 * 알림함이 바뀌었음을 화면 사이에 알리는 신호.
 * 알림함 화면에서 읽음·보관하면 벨 배지가 즉시 따라가야 하는데, 둘은 부모가
 * 달라 상태를 공유할 수 없다 — 전역 이벤트가 컨텍스트보다 가볍다.
 */
const INBOX_CHANGED_EVENT = "autocert:inbox-changed";

export function notifyInboxChanged(): void {
  if (typeof window === "undefined") return;
  window.dispatchEvent(new Event(INBOX_CHANGED_EVENT));
}

/** 심각도 표기 — 서버가 만료 잔여일로 계산한 4단계 (설계 5.2). */
const SEVERITY_META: Record<
  NotificationSeverity,
  { label: string; className: string }
> = {
  critical: { label: "만료됨", className: "bg-red-600 text-white" },
  error: { label: "긴급", className: "bg-red-100 text-red-800" },
  warning: { label: "주의", className: "bg-amber-100 text-amber-900" },
  info: { label: "안내", className: "bg-muted text-muted-foreground" },
};

export function SeverityBadge({
  severity,
  className,
}: {
  severity: NotificationSeverity;
  className?: string;
}) {
  const meta = SEVERITY_META[severity] ?? SEVERITY_META.info;
  return (
    <Badge
      variant="secondary"
      className={`${meta.className}${className ? ` ${className}` : ""}`}
    >
      {meta.label}
    </Badge>
  );
}

const MINUTE_MS = 60_000;

/** "방금 전" / "12분 전" / "3시간 전" / "5일 전". now는 호출자가 넘긴다(렌더 순수성). */
export function relativeTime(iso: string, now: number): string {
  const diff = now - new Date(iso).getTime();
  if (!Number.isFinite(diff)) return "";
  if (diff < MINUTE_MS) return "방금 전";
  const minutes = Math.floor(diff / MINUTE_MS);
  if (minutes < 60) return `${minutes}분 전`;
  const hours = Math.floor(minutes / 60);
  if (hours < 24) return `${hours}시간 전`;
  const days = Math.floor(hours / 24);
  if (days < 30) return `${days}일 전`;
  return `${Math.floor(days / 30)}개월 전`;
}

/**
 * 주기적으로 갱신되는 현재 시각(ms).
 * 렌더 중 `Date.now()`를 직접 부르면 순수성 규칙(react-hooks/purity)에 걸린다.
 */
export function useTicker(intervalMs: number): number {
  const [now, setNow] = React.useState(() => Date.now());
  React.useEffect(() => {
    const timer = setInterval(() => setNow(Date.now()), intervalMs);
    return () => clearInterval(timer);
  }, [intervalMs]);
  return now;
}

export function NotificationBell() {
  const [unread, setUnread] = React.useState<number | null>(null);
  const [open, setOpen] = React.useState(false);
  const [items, setItems] = React.useState<NotificationItem[] | null>(null);
  const [listError, setListError] = React.useState(false);
  const now = useTicker(MINUTE_MS);

  const refreshCount = React.useCallback(() => {
    getUnreadNotificationCount()
      .then((res) => setUnread(res.unread))
      // 401은 apiFetch가 로그인으로 돌려보낸다. 그 외 실패는 배지를 지우지 않고
      // 직전 값을 유지한다 — 벨은 부수적 UI라 오류 배너를 띄우지 않는다.
      .catch(() => {});
  }, []);

  React.useEffect(() => {
    refreshCount();
    const timer = setInterval(refreshCount, UNREAD_POLL_MS);
    window.addEventListener(INBOX_CHANGED_EVENT, refreshCount);
    return () => {
      clearInterval(timer);
      window.removeEventListener(INBOX_CHANGED_EVENT, refreshCount);
    };
  }, [refreshCount]);

  // 여는 순간 직전 결과를 비운다 — 초기화를 effect 본문에서 하면
  // react-hooks/set-state-in-effect에 걸린다(docs/GOTCHAS.md).
  const handleOpenChange = React.useCallback((next: boolean) => {
    if (next) {
      setItems(null);
      setListError(false);
    }
    setOpen(next);
  }, []);

  // 목록은 열 때만 — 닫힌 벨이 목록 API를 주기 호출하지 않는다.
  React.useEffect(() => {
    if (!open) return;
    let cancelled = false;
    listNotifications("all", { pageSize: RECENT_LIMIT })
      .then((res) => {
        if (cancelled) return;
        setItems(res.items);
        setUnread(res.unread);
      })
      .catch(() => {
        if (!cancelled) setListError(true);
      });
    return () => {
      cancelled = true;
    };
  }, [open]);

  const badgeText = unread == null ? "" : unread > 99 ? "99+" : String(unread);

  return (
    <Popover open={open} onOpenChange={handleOpenChange}>
      <PopoverTrigger asChild>
        <Button
          variant="ghost"
          size="icon-sm"
          className="relative"
          aria-label={
            unread ? `알림 ${unread}건 안 읽음` : "알림"
          }
          title="알림"
        >
          <Bell />
          {unread != null && unread > 0 && (
            <span className="absolute -top-0.5 -right-0.5 grid h-4 min-w-4 place-items-center rounded-full bg-red-600 px-1 text-[10px] leading-none font-medium text-white tabular-nums">
              {badgeText}
            </span>
          )}
        </Button>
      </PopoverTrigger>
      <PopoverContent align="end" className="w-96 p-0">
        <div className="flex items-baseline justify-between border-b px-4 py-3">
          <div className="text-sm font-semibold tracking-tight">알림</div>
          <span className="text-xs text-muted-foreground">
            {unread == null
              ? ""
              : unread > 0
                ? `안 읽음 ${unread}건`
                : "모두 읽음"}
          </span>
        </div>
        <div className="max-h-80 overflow-y-auto">
          {items == null && !listError && (
            <div className="space-y-2 p-4">
              <Skeleton className="h-10 rounded-md" />
              <Skeleton className="h-10 rounded-md" />
            </div>
          )}
          {listError && (
            <p className="px-4 py-8 text-center text-[13px] text-muted-foreground">
              알림을 불러오지 못했습니다. 잠시 후 다시 열어 보십시오.
            </p>
          )}
          {items != null && items.length === 0 && (
            <p className="px-4 py-8 text-center text-[13px] text-muted-foreground">
              받은 알림이 없습니다. 만료가 임박한 인증서가 생기면 알림 정책에
              따라 여기에 쌓입니다.
            </p>
          )}
          {items?.map((item) => (
            <Link
              key={item.id}
              href="/notifications"
              onClick={() => setOpen(false)}
              className="flex flex-col gap-1 border-b px-4 py-3 last:border-b-0 hover:bg-muted/50"
            >
              <div className="flex items-center gap-2">
                <SeverityBadge severity={item.severity} />
                <span
                  className={`flex-1 truncate text-[13px] ${
                    item.read_at ? "text-muted-foreground" : "font-medium"
                  }`}
                >
                  {item.title}
                </span>
                <span className="shrink-0 text-[11px] text-muted-foreground tabular-nums">
                  {relativeTime(item.created_at, now)}
                </span>
              </div>
              <p className="line-clamp-2 text-xs text-muted-foreground">
                {item.body}
              </p>
            </Link>
          ))}
        </div>
        <div className="border-t p-2">
          <Button
            asChild
            variant="ghost"
            size="sm"
            className="w-full justify-center"
          >
            <Link href="/notifications" onClick={() => setOpen(false)}>
              전체 보기
            </Link>
          </Button>
        </div>
      </PopoverContent>
    </Popover>
  );
}
