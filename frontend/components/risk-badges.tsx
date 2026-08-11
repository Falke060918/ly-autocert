/**
 * 위험 판정(certrisk) 표기 — 심각도 배지와 판정 항목 목록.
 *
 * 상태색은 테마 토큰(danger/warn/ok)만 쓴다 — Badge의 상태 variant를 그대로 태우고
 * 색 값을 여기서 다시 만들지 않는다. info는 상태 3색 어디에도 속하지 않으므로
 * 중립 톤(secondary)이다.
 *
 * 판정은 저장값이 아니라 조회 시 계산된 값이라 여기서 재계산·보정하지 않는다.
 */

import { Badge } from "@/components/ui/badge";
import { cn } from "@/lib/utils";
import type { RiskAssessment, RiskFinding, RiskSeverity } from "@/lib/api";
import {
  RISK_SCOPE_LABELS,
  RISK_SEVERITY_LABELS,
  riskBadgeVariant,
  riskIssues,
} from "@/lib/risks";

/**
 * 심각도 배지 1개. 위험 항목이 없을 때(ok)는 기본적으로 아무것도 그리지 않는다 —
 * 인벤토리 대부분이 정상인 화면에서 초록 배지가 줄마다 서면 진짜 경고가 묻힌다.
 * 상세처럼 "판정을 실제로 돌렸다"는 사실이 필요한 자리에서만 showOK로 켠다.
 */
export function RiskSeverityBadge({
  severity,
  count,
  showOK = false,
  title,
  className,
}: {
  severity: RiskSeverity;
  /** 함께 표시할 판정 항목 수. 생략하면 라벨만 나온다. */
  count?: number;
  showOK?: boolean;
  title?: string;
  className?: string;
}) {
  if (severity === "ok" && !showOK) return null;
  return (
    <Badge
      variant={riskBadgeVariant(severity)}
      className={className}
      title={title}
    >
      {RISK_SEVERITY_LABELS[severity]}
      {count != null && count > 0 && (
        <span className="font-mono tabular-nums">{count}</span>
      )}
    </Badge>
  );
}

/**
 * 목록 1행용 요약 배지 — 최고 심각도 + 걸린 항목 수, 항목 이름은 title 속성에 담는다.
 * 행마다 배지를 여러 개 세우면 표가 읽히지 않으므로 하나로 접는다.
 */
export function RiskSummaryBadge({ risk }: { risk: RiskAssessment | undefined }) {
  const issues = riskIssues(risk);
  if (!risk || issues.length === 0) {
    return <span className="text-xs text-muted-foreground/60">—</span>;
  }
  return (
    <RiskSeverityBadge
      severity={risk.severity}
      count={issues.length}
      title={issues.map((f) => f.title).join(" · ")}
    />
  );
}

/**
 * 판정 항목 1건 — 심각도 배지 + 제목 + 사유. 위치 축 항목은 조치 주체가 다르므로
 * 축을 함께 적는다(인증서를 재발급해도 해결되지 않는다).
 */
export function RiskFindingRow({
  finding,
  className,
}: {
  finding: RiskFinding;
  className?: string;
}) {
  return (
    <div className={cn("flex items-start gap-2.5", className)}>
      <RiskSeverityBadge
        severity={finding.severity}
        showOK
        className="mt-0.5 shrink-0"
      />
      <div className="min-w-0 flex-1">
        <div className="flex flex-wrap items-center gap-1.5">
          <span className="text-[13px] font-medium">{finding.title}</span>
          {finding.scope === "location" && (
            <Badge variant="outline" className="text-muted-foreground">
              {RISK_SCOPE_LABELS.location}
            </Badge>
          )}
        </div>
        <p className="mt-0.5 text-xs text-muted-foreground">{finding.message}</p>
      </div>
    </div>
  );
}
