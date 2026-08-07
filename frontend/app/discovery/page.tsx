import {
  Card,
  CardDescription,
  CardHeader,
  CardTitle,
} from "@/components/ui/card";

export default function DiscoveryPage() {
  return (
    <div className="space-y-6">
      <div>
        <h1 className="text-2xl font-semibold tracking-tight">Discovery</h1>
        <p className="text-sm text-muted-foreground">
          네트워크 TLS 스캔으로 인증서를 발견한다. IP/CIDR·포트·타임아웃·동시
          연결 수를 입력해 스캔 Job을 실행한다.
        </p>
      </div>
      <Card>
        <CardHeader>
          <CardTitle>준비 중</CardTitle>
          <CardDescription>
            스캔 Job 생성·이력 화면은 Discovery API 연동 후 구현된다.
          </CardDescription>
        </CardHeader>
      </Card>
    </div>
  );
}
