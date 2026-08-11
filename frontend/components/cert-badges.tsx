import { Badge } from "@/components/ui/badge";
import { cn } from "@/lib/utils";
import { dday } from "@/lib/format";
import type { CertificateStatus } from "@/lib/api";

/**
 * 상태 뱃지 — 목업 규칙: 상태색은 뱃지·긴급 표기에만 쓴다.
 * 만료·만료 임박은 저장된 status가 아니라 not_after에서 파생 표시한다
 * (백엔드 목록 필터와 동일 기준).
 *
 * 색은 상태 토큰(danger/warn)만 쓴다 — Badge의 상태 variant를 그대로 태우고
 * 색 값을 여기서 다시 만들지 않는다(`.claude/conventions/frontend-ui.md`).
 */
export function CertStatusBadge({
  status,
  notAfter,
}: {
  status: CertificateStatus;
  notAfter: string;
}) {
  const d = dday(notAfter);
  if (d.expired) {
    return <Badge variant="danger">만료</Badge>;
  }
  if (status === "revoked") {
    return <Badge variant="danger">폐기</Badge>;
  }
  if (d.days <= 30) {
    return <Badge variant="warn">만료 임박</Badge>;
  }
  switch (status) {
    case "active":
      return <Badge variant="outline">정상</Badge>;
    case "replaced":
      return <Badge variant="secondary">교체됨</Badge>;
    default:
      return <Badge variant="secondary">미확인</Badge>;
  }
}

/** D-day 표기 — 칩이 아니라 텍스트, 긴급도만 색으로 (목업 .dday). */
export function DdayText({ notAfter }: { notAfter: string }) {
  const d = dday(notAfter);
  return (
    <span
      className={cn(
        "font-mono text-xs font-medium tabular-nums",
        d.tone === "urgent" && "text-danger",
        d.tone === "soon" && "text-warn",
        d.tone === "plain" && "text-muted-foreground/70",
      )}
    >
      {d.label}
    </span>
  );
}
