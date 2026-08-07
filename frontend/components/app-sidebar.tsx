"use client";

import Link from "next/link";
import { usePathname } from "next/navigation";
import { LayoutDashboard, Radar, ShieldCheck } from "lucide-react";

import { cn } from "@/lib/utils";

const NAV_ITEMS = [
  { href: "/", label: "대시보드", icon: LayoutDashboard },
  { href: "/certificates", label: "인증서", icon: ShieldCheck },
  { href: "/discovery", label: "Discovery", icon: Radar },
] as const;

function isActive(pathname: string, href: string) {
  if (href === "/") return pathname === "/";
  return pathname === href || pathname.startsWith(`${href}/`);
}

export function AppSidebar() {
  const pathname = usePathname();

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
        {NAV_ITEMS.map(({ href, label, icon: Icon }) => (
          <Link
            key={href}
            href={href}
            className={cn(
              "flex items-center gap-2.5 rounded-md px-3 py-2 text-sm font-medium transition-colors",
              isActive(pathname, href)
                ? "bg-sidebar-accent text-sidebar-accent-foreground"
                : "text-muted-foreground hover:bg-sidebar-accent/60 hover:text-sidebar-accent-foreground",
            )}
          >
            <Icon className="size-4" />
            {label}
          </Link>
        ))}
      </nav>
      {/* Phase 2+ 영역 — 목업의 잠금 표시를 유지한다 (설계상 확장점). */}
      <nav className="flex flex-col gap-1 p-3 pt-1" aria-label="준비 중 메뉴">
        <div className="px-3 pb-1 text-xs font-medium text-muted-foreground/70">
          관리
        </div>
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
