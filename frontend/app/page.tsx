import {
  Card,
  CardDescription,
  CardHeader,
  CardTitle,
} from "@/components/ui/card";

export default function DashboardPage() {
  return (
    <div className="space-y-6">
      <div>
        <h1 className="text-2xl font-semibold tracking-tight">대시보드</h1>
        <p className="text-sm text-muted-foreground">
          전사 인증서 현황 요약. 만료 임박·자체 서명·소유자 없음 집계가
          여기에 표시된다.
        </p>
      </div>
      <Card>
        <CardHeader>
          <CardTitle>준비 중</CardTitle>
          <CardDescription>
            만료 대시보드(7/30/60/90일)는 백엔드 인벤토리 API 연동 후
            구현된다.
          </CardDescription>
        </CardHeader>
      </Card>
    </div>
  );
}
