"use client";

import { LogOut } from "lucide-react";

import { NotificationBell } from "@/components/notification-bell";
import { ThemeToggle } from "@/components/theme-toggle";
import { Button } from "@/components/ui/button";
import { Skeleton } from "@/components/ui/skeleton";
import { roleLabel, useAuth } from "@/lib/auth";

export function AppTopbar() {
  const { user, loading, logout } = useAuth();

  return (
    // 뷰포트 고정 — 본문만 스크롤한다. 아래로 지나가는 콘텐츠를 가려야 하므로
    // 배경은 반드시 불투명(bg-background)이고, z는 사이드바와 같은 층이되
    // 다이얼로그·팝오버(z-50 포털)보다는 아래다.
    <header className="sticky top-0 z-30 flex h-14 shrink-0 items-center justify-between border-b bg-background px-6">
      {/* 좌측은 의도적으로 비워둔다 — 추후 탭 자리(사용자 확정: Phase 문구 삭제). */}
      <div />
      <div className="flex items-center gap-3">
        {/* 테마 토글은 세션 상태와 무관하게 항상 노출 */}
        <ThemeToggle />
        {loading ? (
          <Skeleton className="h-8 w-40" />
        ) : user ? (
          <>
            {/* 벨은 세션이 확인된 뒤에만 — 미인증 상태에서 폴링을 시작하지 않는다. */}
            <NotificationBell />
            <div className="flex items-center gap-2.5">
              <div className="grid size-7 place-items-center rounded-full border bg-secondary text-xs font-medium text-secondary-foreground">
                {(user.display_name ?? user.username).slice(0, 1).toUpperCase()}
              </div>
              <div className="leading-tight">
                <div className="text-[13px] font-medium">
                  {user.display_name ?? user.username}
                </div>
                <div className="text-xs text-muted-foreground">
                  {roleLabel(user.role)}
                </div>
              </div>
            </div>
            <Button
              variant="ghost"
              size="icon-sm"
              aria-label="로그아웃"
              title="로그아웃"
              onClick={() => void logout()}
            >
              <LogOut />
            </Button>
          </>
        ) : null}
      </div>
    </header>
  );
}
