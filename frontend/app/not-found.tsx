import Link from "next/link";

import { Button } from "@/components/ui/button";

export default function NotFound() {
  return (
    <div className="flex h-full flex-col items-center justify-center gap-4 py-24 text-center">
      <p className="font-mono text-sm text-muted-foreground">404</p>
      <h1 className="text-2xl font-semibold tracking-tight">
        페이지를 찾을 수 없습니다
      </h1>
      <p className="text-sm text-muted-foreground">
        요청한 주소가 존재하지 않거나 이동되었습니다.
      </p>
      <Button asChild variant="outline">
        <Link href="/">대시보드로 돌아가기</Link>
      </Button>
    </div>
  );
}
