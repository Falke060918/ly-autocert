import { AppSidebar } from "@/components/app-sidebar";
import { AppTopbar } from "@/components/app-topbar";
import { AuthProvider } from "@/lib/auth";

// 인증 화면 셸 — /login은 이 그룹 밖이라 사이드바 없이 렌더된다.
// 각 화면은 제목 블록을 두지 않는다(현재 위치는 사이드바 활성 표시가 알린다) —
// 첫 화면 안에 지표가 최대한 들어오도록 세로 여백을 줄인 이유다.
//
// 스크롤 컨테이너는 **문서(html) 하나로 유지**하고 사이드바·탑바만 position:sticky로
// 고정한다. main을 overflow-y-auto 스크롤 컨테이너로 바꾸면 라우팅 시 상단 복귀,
// scrollIntoView(디스커버리 중복 발주 가드), 앵커 이동이 전부 문서 스크롤 기준이라
// 함께 깨진다. 탑바에 가려지는 문제는 globals.css의 html scroll-padding-top이 막는다.
export default function AppLayout({ children }: { children: React.ReactNode }) {
  return (
    <AuthProvider>
      <div className="flex min-h-screen">
        <AppSidebar />
        <div className="flex min-w-0 flex-1 flex-col">
          <AppTopbar />
          <main className="flex-1 px-6 py-4">{children}</main>
        </div>
      </div>
    </AuthProvider>
  );
}
