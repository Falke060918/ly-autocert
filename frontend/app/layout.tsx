import type { Metadata } from "next";
import "./globals.css";

import { ThemeProvider } from "@/components/theme-provider";

export const metadata: Metadata = {
  title: "INHERIT AutoCert",
  description: "온프레미스 인증서 라이프사이클 관리(CLM) 플랫폼",
};

// FOUC 방지 — 하이드레이션 전에 실행돼 저장된 테마(없으면 시스템 설정)를 html에
// 적용한다. CSP는 script-src 'unsafe-inline'을 허용한다 (next.config.ts 주석 참고).
// 키는 components/theme-provider.tsx의 THEME_STORAGE_KEY와 같아야 한다.
const themeInitScript = `(function(){try{var t=localStorage.getItem("autocert-theme");if(t!=="light"&&t!=="dark"){t=window.matchMedia("(prefers-color-scheme: dark)").matches?"dark":"light"}if(t==="dark"){document.documentElement.classList.add("dark")}}catch(e){}})()`;

export default function RootLayout({ children }: LayoutProps<"/">) {
  return (
    // suppressHydrationWarning: 인라인 스크립트가 하이드레이션 전에 html에 .dark를
    // 붙이므로 서버 마크업과 클래스가 달라질 수 있다 — html 요소 한정 억제.
    <html lang="ko" className="h-full antialiased" suppressHydrationWarning>
      <body className="min-h-full">
        <script dangerouslySetInnerHTML={{ __html: themeInitScript }} />
        <ThemeProvider>{children}</ThemeProvider>
      </body>
    </html>
  );
}
