import { AppSidebar } from "@/components/app-sidebar";
import { AppTopbar } from "@/components/app-topbar";
import { AuthProvider } from "@/lib/auth";

// 인증 화면 셸 — /login은 이 그룹 밖이라 사이드바 없이 렌더된다.
// 각 화면은 제목 블록을 두지 않는다(현재 위치는 사이드바 활성 표시가 알린다) —
// 첫 화면 안에 지표가 최대한 들어오도록 세로 여백을 줄인 이유다.
export default function AppLayout({ children }: { children: React.ReactNode }) {
  return (
    <AuthProvider>
      <div className="flex min-h-screen">
        <AppSidebar />
        <div className="flex min-w-0 flex-1 flex-col">
          <AppTopbar />
          <main className="flex-1 overflow-y-auto px-6 py-4">{children}</main>
        </div>
      </div>
    </AuthProvider>
  );
}
