import {
  Card,
  CardDescription,
  CardHeader,
  CardTitle,
} from "@/components/ui/card";

export default async function CertificateDetailPage({
  params,
}: PageProps<"/certificates/[id]">) {
  const { id } = await params;

  return (
    <div className="space-y-6">
      <div>
        <h1 className="text-2xl font-semibold tracking-tight">인증서 상세</h1>
        <p className="text-sm text-muted-foreground">
          인증서 ID <span className="font-mono">{id}</span>
        </p>
      </div>
      <Card>
        <CardHeader>
          <CardTitle>준비 중</CardTitle>
          <CardDescription>
            주체·발급자·유효기간·fingerprint·발견 위치 등 상세 정보는 상세
            API 연동 후 구현된다.
          </CardDescription>
        </CardHeader>
      </Card>
    </div>
  );
}
