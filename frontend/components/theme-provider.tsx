"use client";

import * as React from "react";

/**
 * 자작 테마 배선 — next-themes 미사용(의존성 최소, 폐쇄망 제품).
 * 초기 적용은 layout.tsx의 FOUC 방지 인라인 스크립트가 하이드레이션 전에 끝낸다:
 * localStorage("autocert-theme") 값이 있으면 그것을, 없으면 prefers-color-scheme을 따른다.
 * 여기서는 그 결과(html.dark)를 읽어 상태로 들고, 수동 토글 시에만 localStorage에 저장한다
 * — 저장 전까지는 시스템 설정 추종이 유지된다.
 */

export const THEME_STORAGE_KEY = "autocert-theme";

type Theme = "light" | "dark";

const ThemeContext = React.createContext<{
  theme: Theme;
  toggleTheme: () => void;
} | null>(null);

export function ThemeProvider({ children }: { children: React.ReactNode }) {
  // SSR에는 document가 없으므로 light로 시작 — 실제 클래스는 인라인 스크립트가
  // 이미 맞춰놨고, 토글 아이콘은 CSS dark: 변형으로 그려 하이드레이션 불일치가 없다.
  const [theme, setTheme] = React.useState<Theme>(() =>
    typeof document !== "undefined" &&
    document.documentElement.classList.contains("dark")
      ? "dark"
      : "light",
  );

  const toggleTheme = React.useCallback(() => {
    const next: Theme = document.documentElement.classList.toggle("dark")
      ? "dark"
      : "light";
    try {
      localStorage.setItem(THEME_STORAGE_KEY, next);
    } catch {
      // localStorage 불가(사파리 프라이빗 등) — 세션 내 토글만 동작
    }
    setTheme(next);
  }, []);

  const value = React.useMemo(
    () => ({ theme, toggleTheme }),
    [theme, toggleTheme],
  );

  return (
    <ThemeContext.Provider value={value}>{children}</ThemeContext.Provider>
  );
}

export function useTheme() {
  const ctx = React.useContext(ThemeContext);
  if (!ctx) throw new Error("useTheme는 ThemeProvider 안에서만 쓸 수 있다");
  return ctx;
}
