import { cn } from "@/lib/utils";

/**
 * 필수 입력 마커 — 규약은 `.claude/conventions/frontend-ui.md`.
 *
 * 색으로 알리지 않는다: 상태 3색(danger/warn/ok)은 인증서 상태 전용이라
 * 폼 별표에 쓰면 같은 화면의 만료 배지가 색의 뜻을 잃는다(필수 입력은 위험이
 * 아니라 서식이다). 필수 여부의 정본은 컨트롤의 `required` 속성이고 이 마커는
 * 눈으로 보는 보조 표기라 `aria-hidden`이다 — 스크린리더가 "별표"를 읽어
 * 라벨을 오염시키지 않게 한다.
 */
export function RequiredMark({ className }: { className?: string }) {
  return (
    <span aria-hidden="true" className={cn("text-muted-foreground", className)}>
      *
    </span>
  );
}
