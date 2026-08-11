"use client";

import * as React from "react";
import Link from "next/link";
import { usePathname } from "next/navigation";
import {
  Bell,
  BellRing,
  CalendarClock,
  LayoutDashboard,
  Lock,
  PanelLeftClose,
  PanelLeftOpen,
  Radar,
  RadioTower,
  ShieldCheck,
  Users,
} from "lucide-react";

import { Button } from "@/components/ui/button";
import { canManageInventory, isSystemAdministrator, useAuth } from "@/lib/auth";
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

/**
 * 스캔 설정은 스캔 발주와 같은 3역할이다 — 백엔드 protected() 등록과 같은 조건이다.
 * 조회 자체는 전 역할이 할 수 있지만, 편집이 없는 화면을 메뉴에 세우지는 않는다.
 */
const SCAN_ADMIN_NAV_ITEMS = [
  { href: "/settings/scans", label: "스캔 설정", icon: CalendarClock },
] as const;

/**
 * 접힘 상태는 3값이다. `null` = "화면 폭에 맡김"이고 lg(1024px) 미만에서 자동으로
 * 아이콘 레일이 된다 — 이 값은 CSS 미디어쿼리가 판정하므로 서버 렌더와 첫 클라이언트
 * 렌더가 같아 하이드레이션 불일치가 없다(폭을 JS로 재서 초기값을 정하면 넓은 화면에서
 * 레일이 한 프레임 번쩍인다). 사용자가 토글을 누른 뒤에만 true/false로 고정된다.
 *
 * 토글은 **사이드바 자신의 바닥**에 둔다 — 탑바 좌측은 추후 섹션/테넌트 탭 자리로
 * 비워둔 칸이다(사용자 확정).
 */
type Collapsed = boolean | null;

const AUTO_EXPAND_QUERY = "(min-width: 64rem)"; // Tailwind lg

type Display = "block" | "inline" | "contents";

/**
 * 접혔을 때 숨기는 요소. 자동 모드면 lg 미만에서 숨는다.
 *
 * 클래스 문자열은 **반드시 리터럴로** 돌려준다 — Tailwind는 소스를 문자열로 훑기
 * 때문에 `` `hidden lg:${display}` `` 처럼 조립하면 그 유틸리티가 CSS에 생성되지 않고
 * 빌드도 조용히 통과한다(화면에서만 안 먹는다).
 */
function hideWhenCollapsed(collapsed: Collapsed, display: Display = "block") {
  if (collapsed === true) return "hidden";
  if (collapsed === false) {
    return display === "block"
      ? "block"
      : display === "inline"
        ? "inline"
        : "contents";
  }
  return display === "block"
    ? "hidden lg:block"
    : display === "inline"
      ? "hidden lg:inline"
      : "hidden lg:contents";
}

/** 접혔을 때만 보이는 요소(아이콘 레일 전용 표기). 리터럴 규칙은 위와 같다. */
function onlyWhenCollapsed(collapsed: Collapsed, display: Display = "block") {
  if (collapsed === false) return "hidden";
  if (collapsed === true) {
    return display === "block"
      ? "block"
      : display === "inline"
        ? "inline"
        : "contents";
  }
  return display === "block"
    ? "block lg:hidden"
    : display === "inline"
      ? "inline lg:hidden"
      : "contents lg:hidden";
}

function isActive(pathname: string, href: string) {
  if (href === "/") return pathname === "/";
  return pathname === href || pathname.startsWith(`${href}/`);
}

function NavLink({
  href,
  label,
  icon: Icon,
  pathname,
  collapsed,
}: {
  href: string;
  label: string;
  icon: React.ComponentType<{ className?: string }>;
  pathname: string;
  collapsed: Collapsed;
}) {
  return (
    <Link
      href={href}
      // 접힌 상태에서는 라벨이 사라지므로 title/aria-label이 이름을 대신한다.
      title={label}
      aria-label={label}
      className={cn(
        "relative flex items-center gap-2.5 rounded-md py-2 text-sm font-medium transition-colors",
        collapsed === null
          ? "justify-center px-0 lg:justify-start lg:px-3"
          : collapsed
            ? "justify-center px-0"
            : "justify-start px-3",
        isActive(pathname, href)
          ? // 활성 항목 — primary 세로 인디케이터 바 (관제 콘솔의 현재 채널 표시)
            "bg-sidebar-accent text-sidebar-accent-foreground before:absolute before:inset-y-2 before:left-0 before:w-0.5 before:rounded-full before:bg-primary"
          : "text-muted-foreground hover:bg-sidebar-accent/60 hover:text-sidebar-accent-foreground",
      )}
    >
      <Icon className="size-4 shrink-0" />
      <span className={hideWhenCollapsed(collapsed, "inline")}>{label}</span>
    </Link>
  );
}

export function AppSidebar() {
  const pathname = usePathname();
  const { user } = useAuth();
  const [collapsed, setCollapsed] = React.useState<Collapsed>(null);
  const canAdminUsers = isSystemAdministrator(user);
  const canAdminScans = canManageInventory(user);
  const canAdminNotifications =
    user?.role === "system_administrator" ||
    user?.role === "security_administrator";

  // 자동 모드에서 처음 누를 때는 "지금 보이는 상태의 반대"로 가야 한다 —
  // 판정은 CSS와 같은 기준(lg)을 matchMedia로 다시 물어 얻는다.
  const toggle = React.useCallback(() => {
    setCollapsed((prev) =>
      prev === null ? window.matchMedia(AUTO_EXPAND_QUERY).matches : !prev,
    );
  }, []);

  // 자동 모드에서는 지금 접혔는지를 CSS가 정하므로 한쪽으로 단정하지 않는다.
  const toggleLabel =
    collapsed === null
      ? "사이드바 접기·펼치기"
      : collapsed
        ? "사이드바 펼치기"
        : "사이드바 접기";

  return (
    // 뷰포트 고정 — 본문이 아무리 길어도 사이드바는 따라 내려가지 않는다.
    // h-dvh(100vh 아님)라 모바일 브라우저의 주소창 접힘에도 바닥이 잘리지 않는다.
    <aside
      className={cn(
        "sticky top-0 z-30 flex h-dvh shrink-0 flex-col border-r bg-sidebar transition-[width]",
        collapsed === null ? "w-14 lg:w-60" : collapsed ? "w-14" : "w-60",
      )}
    >
      <div
        className={cn(
          "flex h-14 shrink-0 flex-col justify-center border-b",
          collapsed === null ? "px-3 lg:px-5" : collapsed ? "px-3" : "px-5",
        )}
      >
        <div className={onlyWhenCollapsed(collapsed)}>
          {/* 접힌 폭에서는 제품명이 들어가지 않는다 — 이니셜만 남긴다. */}
          <div
            className="grid size-8 place-items-center rounded-md bg-sidebar-accent text-xs font-semibold tracking-tight"
            title="INHERIT AutoCert"
          >
            AC
          </div>
        </div>
        <div className={hideWhenCollapsed(collapsed)}>
          <div className="text-sm font-semibold tracking-tight">
            INHERIT AutoCert
          </div>
          <div className="text-xs text-muted-foreground">
            인증서 라이프사이클 관리
          </div>
        </div>
      </div>
      {/* 메뉴가 뷰포트보다 길어지면 브랜드 헤더는 남기고 이 영역만 따로 스크롤한다. */}
      <div className="flex min-h-0 flex-1 flex-col overflow-y-auto">
        <nav className="flex flex-col gap-1 p-3">
          <div
            className={cn(
              "px-3 pb-1 text-xs font-medium text-muted-foreground/70",
              hideWhenCollapsed(collapsed),
            )}
          >
            운영
          </div>
          {NAV_ITEMS.map((item) => (
            <NavLink
              key={item.href}
              {...item}
              pathname={pathname}
              collapsed={collapsed}
            />
          ))}
        </nav>
        {/* 관리 영역 — 구현된 항목만 링크로 두고, 나머지는 목업의 잠금 표시를 유지한다. */}
        <nav className="flex flex-col gap-1 p-3 pt-1" aria-label="관리 메뉴">
          <div
            className={cn(
              "px-3 pb-1 text-xs font-medium text-muted-foreground/70",
              hideWhenCollapsed(collapsed),
            )}
          >
            관리
          </div>
          {canAdminUsers &&
            ADMIN_NAV_ITEMS.map((item) => (
              <NavLink
                key={item.href}
                {...item}
                pathname={pathname}
                collapsed={collapsed}
              />
            ))}
          {canAdminScans &&
            SCAN_ADMIN_NAV_ITEMS.map((item) => (
              <NavLink
                key={item.href}
                {...item}
                pathname={pathname}
                collapsed={collapsed}
              />
            ))}
          {canAdminNotifications &&
            NOTIFY_ADMIN_NAV_ITEMS.map((item) => (
              <NavLink
                key={item.href}
                {...item}
                pathname={pathname}
                collapsed={collapsed}
              />
            ))}
          {/* 준비 중 항목은 아이콘이 없어 레일에서 뜻이 통하지 않는다 — 접히면 감춘다. */}
          <div className={hideWhenCollapsed(collapsed)}>
            {["CA 관리", "정책", "작업", "감사 로그"].map((label) => (
              <div
                key={label}
                className="flex items-center justify-between rounded-md px-3 py-2 text-sm text-muted-foreground/50"
              >
                {label}
                <span className="text-xs">준비 중</span>
              </div>
            ))}
          </div>
        </nav>
      </div>
      <div
        className={cn(
          "flex shrink-0 border-t p-2",
          collapsed === null
            ? "justify-center lg:justify-end lg:px-3"
            : collapsed
              ? "justify-center"
              : "justify-end px-3",
        )}
      >
        <Button
          variant="ghost"
          size="icon-sm"
          onClick={toggle}
          aria-label={toggleLabel}
          title={toggleLabel}
        >
          <span className={onlyWhenCollapsed(collapsed, "contents")}>
            <PanelLeftOpen />
          </span>
          <span className={hideWhenCollapsed(collapsed, "contents")}>
            <PanelLeftClose />
          </span>
        </Button>
      </div>
    </aside>
  );
}
