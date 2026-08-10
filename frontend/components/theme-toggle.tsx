"use client";

import { Moon, Sun } from "lucide-react";

import { useTheme } from "@/components/theme-provider";
import { Button } from "@/components/ui/button";

/**
 * 테마 토글 — 아이콘은 상태값이 아니라 CSS dark: 변형으로 갈라 SSR/클라이언트
 * 하이드레이션 불일치를 피한다 (서버는 테마를 모른다).
 */
export function ThemeToggle() {
  const { toggleTheme } = useTheme();

  return (
    <Button
      variant="ghost"
      size="icon-sm"
      aria-label="테마 전환"
      title="테마 전환"
      onClick={toggleTheme}
    >
      <Sun className="hidden dark:block" />
      <Moon className="dark:hidden" />
    </Button>
  );
}
