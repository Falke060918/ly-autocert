"use client";
import { usePolling } from "@/lib/use-polling";

/**
 * 발급기관(O=) 분포 카드 — 상위 5 + 기타 도넛과 목록.
 *
 * 데이터는 `lib/issuers.ts`의 getIssuerAggregation(scope) 하나뿐이다. 백엔드가 병행
 * 구현 중이라 **404·오류를 정상 상태("집계 준비 중")로 처리**한다 — 이 카드의
 * 실패가 대시보드 전체 오류로 번지지 않는다.
 *
 * 색은 --chart-1..5 토큰만 쓴다. "기타"는 5번 토큰의 저채도(투명도) 버전으로
 * 구분한다 — 새 색을 만들지 않기 위해서다.
 */

import * as React from "react";
import Link from "next/link";
import { Cell, Pie, PieChart } from "recharts";

import { Card } from "@/components/ui/card";
import {
  ChartContainer,
  ChartTooltip,
  ChartTooltipContent,
  type ChartConfig,
} from "@/components/ui/chart";
import { Skeleton } from "@/components/ui/skeleton";
import {
  getIssuerAggregation,
  ISSUER_ORG_PARAM,
  ISSUER_ORG_UNKNOWN,
  type IssuerOrgCount,
} from "@/lib/issuers";
import { cn } from "@/lib/utils";

const TOP_N = 5;
const UNKNOWN_LABEL = "조직 미상";
const OTHERS_LABEL = "기타";

interface Slice {
  name: string;
  count: number;
  fill: string;
  fillOpacity?: number;
  /** 목록 색점용 클래스 — fill 값과 같은 토큰이어야 한다. */
  dotClass: string;
  /** 목록에서 누르면 가는 필터 목록 href. 기타·집계 불가 항목은 null. */
  href: string | null;
}

const CHART_VARS = [
  "var(--chart-1)",
  "var(--chart-2)",
  "var(--chart-3)",
  "var(--chart-4)",
  "var(--chart-5)",
];
const DOT_CLASSES = [
  "bg-chart-1",
  "bg-chart-2",
  "bg-chart-3",
  "bg-chart-4",
  "bg-chart-5",
];

function orgHref(organization: string | null, scope?: import("@/lib/api").CertificateScope): string {
  // 조직 미상은 URL 센티널(__none__)로 싣는다 — 목록 페이지의 URL 규약과
  // 통일한다. 빈 값(issuer_org=)을 실으면 Select 트리거가 공백으로 보인다.
  const params = new URLSearchParams({
    [ISSUER_ORG_PARAM]: organization ?? ISSUER_ORG_UNKNOWN,
  });
  if (scope) params.set("scope", scope);
  return `/certificates?${params.toString()}`;
}

function toSlices(organizations: IssuerOrgCount[], scope?: import("@/lib/api").CertificateScope): Slice[] {
  const top: Slice[] = organizations.slice(0, TOP_N).map((o, i) => ({
    name: o.organization ?? UNKNOWN_LABEL,
    count: o.count,
    fill: CHART_VARS[i],
    dotClass: DOT_CLASSES[i],
    href: orgHref(o.organization, scope),
  }));
  const rest = organizations.slice(TOP_N);
  if (rest.length > 0) {
    top.push({
      name: `${OTHERS_LABEL} ${rest.length}개 기관`,
      count: rest.reduce((sum, o) => sum + o.count, 0),
      fill: CHART_VARS[4],
      fillOpacity: 0.35,
      dotClass: cn(DOT_CLASSES[4], "opacity-35"),
      href: null,
    });
  }
  return top;
}

type State =
  | { status: "loading" }
  | { status: "unavailable" }
  | { status: "ready"; slices: Slice[] };

export function IssuerDistributionCard({ scope }: { scope?: import("@/lib/api").CertificateScope }) {
  const [state, setState] = React.useState<State>({ status: "loading" });

  usePolling(async (current) => {
    await getIssuerAggregation(scope)
      .then((res) => {
        if (current())
          setState({ status: "ready", slices: toSlices(res.organizations, scope) });
      })
      .catch(() => {
        // 404 = 집계 API 배선 전. 그 외 오류도 이 카드만 조용히 비운다.
        if (current()) setState({ status: "unavailable" });
      });
  }, [scope]);

  return (
    // 높이는 대시보드 이웃 섹션과 통일(lg:h-[30rem]) — 기관 수가 많아 목록이
    // 넘치면 카드 안에서만 스크롤한다.
    <Card className="gap-0 overflow-hidden py-0 lg:h-[30rem]">
      <div className="flex shrink-0 items-center gap-2 border-b px-4 py-3">
        <h2 className="text-sm font-semibold tracking-tight">발급기관 분포</h2>
        <span className="text-xs text-muted-foreground">issuer O= 기준</span>
      </div>
      <div className="min-h-0 flex-1 overflow-y-auto">
        <CardBody state={state} />
      </div>
    </Card>
  );
}

function CardBody({ state }: { state: State }) {
  if (state.status === "loading") {
    return (
      <div className="space-y-3 p-4">
        <Skeleton className="mx-auto size-36 rounded-full" />
        <Skeleton className="h-56 sm:h-24" />
      </div>
    );
  }
  // 빈 상태 2종은 카드 높이가 고정이라 세로 중앙에 세운다(h-full은 스크롤
  // 래퍼가 flex-1로 확정 높이를 가질 때만 효과가 있다 — 모바일에선 자연 높이).
  if (state.status === "unavailable") {
    return (
      <div className="flex h-full flex-col justify-center px-4 py-10 text-center">
        <p className="text-sm font-medium">집계 준비 중</p>
        <p className="mt-1 text-xs text-muted-foreground">
          발급기관 집계가 아직 제공되지 않습니다. 서버 업데이트 후 표시됩니다.
        </p>
      </div>
    );
  }
  if (state.slices.length === 0) {
    return (
      <div className="flex h-full flex-col justify-center px-4 py-10 text-center">
        <p className="text-sm font-medium">표시할 분포가 없습니다</p>
        <p className="mt-1 text-xs text-muted-foreground">
          인증서가 등록되면 발급기관별 분포가 표시됩니다.
        </p>
      </div>
    );
  }

  const total = state.slices.reduce((sum, s) => sum + s.count, 0);
  const config: ChartConfig = Object.fromEntries(
    state.slices.map((s) => [s.name, { label: s.name }]),
  );

  return (
    <div className="p-4">
      <div className="relative">
        <ChartContainer config={config} className="mx-auto aspect-square h-44 w-full">
          <PieChart>
            <ChartTooltip cursor={false} content={<ChartTooltipContent hideLabel />} />
            <Pie
              data={state.slices}
              dataKey="count"
              nameKey="name"
              innerRadius={52}
              outerRadius={76}
              paddingAngle={2}
              strokeWidth={0}
              isAnimationActive={false}
            >
              {state.slices.map((s) => (
                <Cell key={s.name} fill={s.fill} fillOpacity={s.fillOpacity} />
              ))}
            </Pie>
          </PieChart>
        </ChartContainer>
        <div className="pointer-events-none absolute inset-0 flex flex-col items-center justify-center">
          <span className="font-mono text-xl font-semibold tabular-nums">
            {total.toLocaleString()}
          </span>
          <span className="text-[11px] text-muted-foreground">리프 인증서</span>
        </div>
      </div>

      <ul className="mt-3 border-t pt-1">
        {state.slices.map((s) => {
          const row = (
            <>
              <span className={cn("size-2 shrink-0 rounded-[2px]", s.dotClass)} />
              <span className="min-w-0 truncate">{s.name}</span>
              <span className="ml-auto font-mono text-xs font-medium tabular-nums">
                {s.count.toLocaleString()}
              </span>
              <span className="w-11 shrink-0 text-right font-mono text-xs text-muted-foreground/70 tabular-nums">
                {total > 0 ? `${((s.count / total) * 100).toFixed(1)}%` : "—"}
              </span>
            </>
          );
          return (
            <li key={s.name} className="text-[13px] text-foreground/80">
              {s.href ? (
                <Link
                  href={s.href}
                  className="flex items-center gap-2 rounded-sm py-1.5 transition-colors hover:bg-muted/50 focus-visible:ring-3 focus-visible:ring-ring/50 focus-visible:outline-none"
                >
                  {row}
                </Link>
              ) : (
                <span className="flex items-center gap-2 py-1.5">{row}</span>
              )}
            </li>
          );
        })}
      </ul>
    </div>
  );
}
