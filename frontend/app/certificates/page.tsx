import {
  Card,
  CardDescription,
  CardHeader,
  CardTitle,
} from "@/components/ui/card";

export default function CertificatesPage() {
  return (
    <div className="space-y-6">
      <div>
        <h1 className="text-2xl font-semibold tracking-tight">인증서</h1>
        <p className="text-sm text-muted-foreground">
          인벤토리에 등록된 인증서 목록. 검색·정렬·필터·페이지네이션을
          제공한다.
        </p>
      </div>
      <Card>
        <CardHeader>
          <CardTitle>준비 중</CardTitle>
          <CardDescription>
            인증서 목록 테이블은 목록 API 연동 후 구현된다.
          </CardDescription>
        </CardHeader>
      </Card>
    </div>
  );
}
