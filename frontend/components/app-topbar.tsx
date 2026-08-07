export function AppTopbar() {
  return (
    <header className="flex h-14 shrink-0 items-center justify-between border-b bg-background px-6">
      <div className="text-sm text-muted-foreground">
        Phase 1 — 인증서 인벤토리
      </div>
      <div className="flex items-center gap-2 text-sm text-muted-foreground">
        {/* 로그인/RBAC 배선 전 자리 표시 */}
        <span className="size-2 rounded-full bg-emerald-500" aria-hidden />
        <span>온프레미스</span>
      </div>
    </header>
  );
}
