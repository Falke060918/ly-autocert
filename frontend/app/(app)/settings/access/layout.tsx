import type { Metadata } from "next";

// 화면에는 제목 블록을 두지 않는다 — 이 화면의 이름은 브라우저 탭 제목이 알린다.
export const metadata: Metadata = { title: "접근 제한" };

export default function AccessSettingsLayout({ children }: LayoutProps<"/settings/access">) {
  return children;
}
