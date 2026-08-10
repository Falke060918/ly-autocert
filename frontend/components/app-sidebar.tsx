"use client";

import Link from "next/link";
import { usePathname } from "next/navigation";
import {
  Bell,
  BellRing,
  LayoutDashboard,
  Lock,
  Radar,
  RadioTower,
  ShieldCheck,
  Users,
} from "lucide-react";

import { isSystemAdministrator, useAuth } from "@/lib/auth";
import { cn } from "@/lib/utils";

const NAV_ITEMS = [
  { href: "/", label: "대시보드", icon: LayoutDashboard },
  { href: "/certificates", label: "인증서", icon: ShieldCheck },
  { href: "/discovery", label: "Discovery", icon: Radar },
  { href: "/sensors", label: "센서", icon: RadioTower },
  { href: "/notifications", label: "알림", icon: Bell },
] as const;

/** System Administrator에게만 보이는 메뉴 — 화면 자체도 같은 역할로 잠겨 있다. */
const ADMIN_NAV_ITEMS = [
  { href: "/users", label: "사용자", icon: Users },
  { href: "/settings/access", label: "접근 제한", icon: Lock },
] as const;

/**
 * 알림 설정은 System + Security Administrator다 (00-spec 7.11 "보안 관리자 =
 * 정책 설정") — 백엔드 protected() 등록과 같은 조건이라 사용자 메뉴와 게이트가 다르다.
 */
const NOTIFY_ADMIN_NAV_ITEMS = [
  { href: "/settings/notifications", label: "알림 설정", icon: BellRing },
] as const;

function isActive(pathname: string, href: string) {
  if (href === "/") return pathname === "/";
  return pathname === href || pathname.startsWith(`${href}/`);
}

function NavLink({
  href,
  label,
  icon: Icon,
  pathname,
}: {
  href: string;
  label: string;
  icon: React.ComponentType<{ className?: string }>;
  pathname: string;
}) {
  return (
    <Link
      href={href}
      className={cn(
        "relative flex items-center gap-2.5 rounded-md px-3 py-2 text-sm font-medium transition-colors",
        isActive(pathname, href)
          ? // 활성 항목 — primary 세로 인디케이터 바 (관제 콘솔의 현재 채널 표시)
            "bg-sidebar-accent text-sidebar-accent-foreground before:absolute before:inset-y-2 before:left-0 before:w-0.5 before:rounded-full before:bg-primary"
          : "text-muted-foreground hover:bg-sidebar-accent/60 hover:text-sidebar-accent-foreground",
      )}
    >
      <Icon className="size-4" />
      {label}
    </Link>
  );
}

export function AppSidebar() {
  const pathname = usePathname();
  const { user } = useAuth();
  const canAdminUsers = isSystemAdministrator(user);
  const canAdminNotifications =
    user?.role === "system_administrator" ||
    user?.role === "security_administrator";

  return (
    <aside className="flex w-60 shrink-0 flex-col border-r bg-sidebar">
      <div className="flex h-14 flex-col justify-center border-b px-5">
        <div className="text-sm font-semibold tracking-tight">
          INHERIT AutoCert
        </div>
        <div className="text-xs text-muted-foreground">
          인증서 라이프사이클 관리
        </div>
      </div>
      <nav className="flex flex-col gap-1 p-3">
        <div className="px-3 pb-1 text-xs font-medium text-muted-foreground/70">
          운영
        </div>
        {NAV_ITEMS.map((item) => (
          <NavLink key={item.href} {...item} pathname={pathname} />
        ))}
      </nav>
      {/* 관리 영역 — 구현된 항목만 링크로 두고, 나머지는 목업의 잠금 표시를 유지한다. */}
      <nav className="flex flex-col gap-1 p-3 pt-1" aria-label="관리 메뉴">
        <div className="px-3 pb-1 text-xs font-medium text-muted-foreground/70">
          관리
        </div>
        {canAdminUsers &&
          ADMIN_NAV_ITEMS.map((item) => (
            <NavLink key={item.href} {...item} pathname={pathname} />
          ))}
        {canAdminNotifications &&
          NOTIFY_ADMIN_NAV_ITEMS.map((item) => (
            <NavLink key={item.href} {...item} pathname={pathname} />
          ))}
        {["CA 관리", "정책", "작업", "감사 로그"].map((label) => (
          <div
            key={label}
            className="flex items-center justify-between rounded-md px-3 py-2 text-sm text-muted-foreground/50"
          >
            {label}
            <span className="text-xs">준비 중</span>
          </div>
        ))}
      </nav>
    </aside>
  );
}
