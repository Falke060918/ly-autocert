"use client";

/**
 * 만료 지평선 히어로 — 대시보드 시그니처.
 *
 * 오늘을 기준선으로 좌측에 "이미 만료" 블록, 우측에 90일을 주 단위로 나눈
 * 밀도 바 13개를 자작 SVG로 그린다. 시간이 실제 축이므로 구조가 곧 정보다.
 * 상태색 규칙은 cert-badges와 동일: D-7 이하 빨강, D-30 이하 호박, 나머지는
 * 차트 중립 토큰. 각 구간은 링크라서 클릭하면 해당 만료 필터 목록으로 간다.
 *
 * 데이터는 기존 `listCertificates(expires_within_days=90)` 결과를 그대로 받는다
 * (API 변경 없음). 만료 필터가 표준 구간(7/30/60/90)만 지원하므로 주 클릭은
 * 그 주를 포함하는 가장 좁은 표준 구간으로 보낸다.
 */

import * as React from "react";
import Link from "next/link";

import { Button } from "@/components/ui/button";
import { Card } from "@/components/ui/card";
import type { Certificate, CertificateSummary } from "@/lib/api";
import { dnCommonName } from "@/lib/format";
import { cn } from "@/lib/utils";

const DAY_MS = 24 * 60 * 60 * 1000;
const WEEKS = 13; // 13주 = 91일로 90일 구간을 덮는다.
// 툴팁은 Card(overflow-hidden) 안에서 그려지므로 카드 바닥을 넘으면 잘린다 —
// 차트 높이를 줄인 만큼 목록도 4건으로 맞췄다(나머지는 "외 N건"이 받는다).
const TOOLTIP_CNS = 4;

// SVG 좌표계 (preserveAspectRatio="none" — 가로는 백분율처럼 늘어난다).
const VIEW_W = 1000;
const VIEW_H = 130;
const BASE_Y = 120; // 기준선(바닥) y
const MAX_BAR_H = 100;
const EXPIRED_W = 90; // "이미 만료" 블록 = 9%
const WEEK_W = (VIEW_W - EXPIRED_W) / WEEKS; // 주당 70 = 7%

interface WeekBucket {
  /** 주 시작 D-day (0, 7, 14, …). */
  startDay: number;
  count: number;
  /** 만료 임박순 상위 CN — 입력이 not_after 오름차순이라 앞에서 자른다. */
  topCNs: string[];
}

/** 주 클릭이 이동할 표준 만료 필터 — 그 주를 포함하는 가장 좁은 구간. */
function weekFilterDays(weekIndex: number): number {
  const end = (weekIndex + 1) * 7;
  if (end <= 7) return 7;
  if (end <= 30) return 30;
  if (end <= 60) return 60;
  return 90;
}

/** "8.17" — 지평선 축 전용 짧은 날짜. */
function shortDay(ms: number): string {
  const d = new Date(ms);
  return `${d.getMonth() + 1}.${d.getDate()}`;
}

function barFillClass(startDay: number): string {
  if (startDay < 7) return "fill-danger";
  if (startDay < 30) return "fill-warn";
  return "fill-chart-2";
}

type Hover = { kind: "expired" } | { kind: "week"; index: number } | null;

export function ExpiryHorizonHero({
  summary,
  certs,
  total,
}: {
  summary: CertificateSummary;
  /** 90일 이내 만료 인증서, not_after 오름차순 (최대 500건). */
  certs: Certificate[];
  /** 서버가 알려준 90일 이내 전체 건수 — certs가 잘렸는지 판별용. */
  total: number;
}) {
  // 렌더 중 Date.now() 호출은 lint(purity)에 걸린다 — 마운트 시각으로 고정한다.
  const [now] = React.useState(() => Date.now());
  const [hover, setHover] = React.useState<Hover>(null);

  const weeks = React.useMemo<WeekBucket[]>(() => {
    const buckets: WeekBucket[] = Array.from({ length: WEEKS }, (_, i) => ({
      startDay: i * 7,
      count: 0,
      topCNs: [],
    }));
    for (const c of certs) {
      const days = Math.floor((new Date(c.not_after).getTime() - now) / DAY_MS);
      if (days < 0) continue; // 만료분은 좌측 블록(summary.expired) 몫이다.
      const idx = Math.min(WEEKS - 1, Math.floor(days / 7));
      const b = buckets[idx];
      b.count += 1;
      if (b.topCNs.length < TOOLTIP_CNS) {
        b.topCNs.push(c.common_name || dnCommonName(c.subject_dn));
      }
    }
    return buckets;
  }, [certs, now]);

  const maxCount = Math.max(summary.expired, ...weeks.map((w) => w.count), 1);
  const barH = (count: number) =>
    count === 0 ? 0 : Math.max(4, (count / maxCount) * MAX_BAR_H);
  const truncated = certs.length < total;

  // 툴팁 가로 위치(%) — 구간 중앙. 가장자리는 clamp + 정렬 전환으로 지킨다.
  const centerPct = (h: Exclude<Hover, null>) =>
    h.kind === "expired" ? 4.5 : 9 + h.index * 7 + 3.5;
  const align = (h: Exclude<Hover, null>) =>
    h.kind === "expired" ? "left" : h.index >= 10 ? "right" : "center";

  return (
    <Card className="gap-0 overflow-hidden py-0">
      {/* 밀도 우선 — 대시보드 첫 화면에 KPI 밴드까지 함께 들어와야 한다. */}
      <div className="flex flex-wrap items-center gap-x-2 gap-y-1 border-b px-4 py-2">
        <h2 className="text-sm font-semibold tracking-tight">만료 지평선</h2>
        <span className="text-xs text-muted-foreground">
          오늘 기준 90일 · 주 단위 만료 밀도
          {truncated && ` · 상위 ${certs.length}건 기준`}
        </span>
        <Button asChild variant="outline" size="sm" className="ml-auto">
          <Link href="/certificates?expires_within_days=90&sort=not_after&order=asc">
            90일 이내 {summary.expiring_90d}건 보기
          </Link>
        </Button>
      </div>

      <div className="px-4 pt-3 pb-2">
        <div
          className="relative"
          onMouseLeave={() => setHover(null)}
        >
          {/*
            구간 클릭 타겟은 이 SVG 영역 전체 높이다(아래 오버레이가 inset-0) —
            높이를 줄여도 주당 24px 이상은 유지된다.
          */}
          <svg
            viewBox={`0 0 ${VIEW_W} ${VIEW_H}`}
            preserveAspectRatio="none"
            className="h-24 w-full sm:h-28"
            aria-hidden="true"
          >
            <defs>
              <pattern
                id="expired-hatch"
                width="8"
                height="8"
                patternUnits="userSpaceOnUse"
                patternTransform="rotate(45)"
              >
                <line
                  x1="0"
                  y1="0"
                  x2="0"
                  y2="8"
                  strokeWidth="1.5"
                  className="stroke-danger/20"
                />
              </pattern>
            </defs>

            {/* 과거 영역 — 빗금으로 "이미 지났음"을 표시 */}
            <rect x="0" y="0" width={EXPIRED_W} height={BASE_Y} className="fill-danger/5" />
            <rect x="0" y="0" width={EXPIRED_W} height={BASE_Y} fill="url(#expired-hatch)" />

            {/* +30/+60/+90일 경계 그리드 (1일 = 10 단위) */}
            {[30, 60, 90].map((d) => (
              <line
                key={d}
                x1={EXPIRED_W + d * 10}
                y1={BASE_Y - MAX_BAR_H - 6}
                x2={EXPIRED_W + d * 10}
                y2={BASE_Y}
                strokeDasharray="3 5"
                vectorEffect="non-scaling-stroke"
                className="stroke-border"
              />
            ))}

            {/* 기준선(바닥) */}
            <line
              x1="0"
              y1={BASE_Y}
              x2={VIEW_W}
              y2={BASE_Y}
              vectorEffect="non-scaling-stroke"
              className="stroke-border"
            />

            {/* 이미 만료 바 */}
            {summary.expired > 0 && (
              <rect
                x={EXPIRED_W / 2 - 28}
                y={BASE_Y - barH(summary.expired)}
                width="56"
                height={barH(summary.expired)}
                className={cn(
                  "fill-danger transition-opacity",
                  hover && hover.kind !== "expired" && "opacity-50",
                )}
              />
            )}

            {/* 주 단위 밀도 바 */}
            {weeks.map((w, i) => {
              const h = barH(w.count);
              const x = EXPIRED_W + i * WEEK_W + 7;
              return w.count === 0 ? (
                // 0건 주 — 빈 자리임을 알리는 짧은 눈금만 남긴다.
                <rect
                  key={w.startDay}
                  x={x}
                  y={BASE_Y - 2}
                  width={WEEK_W - 14}
                  height="2"
                  className="fill-border"
                />
              ) : (
                <rect
                  key={w.startDay}
                  x={x}
                  y={BASE_Y - h}
                  width={WEEK_W - 14}
                  height={h}
                  className={cn(
                    barFillClass(w.startDay),
                    "transition-opacity",
                    hover && !(hover.kind === "week" && hover.index === i) && "opacity-50",
                  )}
                />
              );
            })}

            {/* 오늘 기준선 */}
            <line
              x1={EXPIRED_W}
              y1="0"
              x2={EXPIRED_W}
              y2={BASE_Y}
              strokeWidth="1.5"
              vectorEffect="non-scaling-stroke"
              className="stroke-foreground"
            />
          </svg>

          {/* 클릭·호버 레이어 — 각 구간이 만료 필터 목록으로 가는 링크다 */}
          <div className="absolute inset-0 flex">
            <Link
              href="/certificates?expired=true"
              aria-label={`이미 만료 ${summary.expired}건 — 목록 보기`}
              className="w-[9%] rounded-sm transition-colors hover:bg-accent/40 focus-visible:ring-3 focus-visible:ring-ring/50 focus-visible:outline-none"
              onMouseEnter={() => setHover({ kind: "expired" })}
              onFocus={() => setHover({ kind: "expired" })}
              onBlur={() => setHover(null)}
            />
            {weeks.map((w, i) => (
              <Link
                key={w.startDay}
                href={`/certificates?expires_within_days=${weekFilterDays(i)}&sort=not_after&order=asc`}
                aria-label={`${i + 1}주차 (D-${w.startDay}~D-${w.startDay + 6}) 만료 ${w.count}건 — ${weekFilterDays(i)}일 이내 목록 보기`}
                className="flex-1 rounded-sm transition-colors hover:bg-accent/40 focus-visible:ring-3 focus-visible:ring-ring/50 focus-visible:outline-none"
                onMouseEnter={() => setHover({ kind: "week", index: i })}
                onFocus={() => setHover({ kind: "week", index: i })}
                onBlur={() => setHover(null)}
              />
            ))}
          </div>

          {/* 툴팁 */}
          {hover && (
            <div
              className="pointer-events-none absolute top-0 z-10 w-56 rounded-md border bg-popover p-2.5 text-popover-foreground shadow-md"
              style={{
                left: `clamp(0px, ${centerPct(hover)}%, 100%)`,
                transform:
                  align(hover) === "left"
                    ? undefined
                    : align(hover) === "right"
                      ? "translateX(-100%)"
                      : "translateX(-50%)",
              }}
            >
              {hover.kind === "expired" ? (
                <>
                  <div className="flex items-baseline gap-2">
                    <span className="text-xs font-medium">이미 만료</span>
                    <span className="ml-auto font-mono text-xs font-semibold text-danger tabular-nums">
                      {summary.expired}건
                    </span>
                  </div>
                  <p className="mt-1 text-xs text-muted-foreground">
                    클릭하면 만료 목록으로 이동합니다.
                  </p>
                </>
              ) : (
                <WeekTooltip week={weeks[hover.index]} now={now} />
              )}
            </div>
          )}
        </div>

        {/* 축 라벨 */}
        <div className="relative mt-0.5 h-4 text-[11px] leading-4 text-muted-foreground">
          <span className="absolute left-0">이미 만료</span>
          <span className="absolute left-[9%] -translate-x-1/2 font-medium text-foreground">
            오늘
          </span>
          <span className="absolute left-[39%] -translate-x-1/2">+30일</span>
          <span className="absolute left-[69%] -translate-x-1/2">+60일</span>
          <span className="absolute right-0">+90일</span>
        </div>
      </div>
    </Card>
  );
}

function WeekTooltip({ week, now }: { week: WeekBucket; now: number }) {
  const start = now + week.startDay * DAY_MS;
  const end = now + (week.startDay + 6) * DAY_MS;
  return (
    <>
      <div className="flex items-baseline gap-2">
        <span className="font-mono text-xs font-medium tabular-nums">
          {shortDay(start)} – {shortDay(end)}
        </span>
        <span className="text-[11px] text-muted-foreground">
          D-{week.startDay}~{week.startDay + 6}
        </span>
        <span className="ml-auto font-mono text-xs font-semibold tabular-nums">
          {week.count}건
        </span>
      </div>
      {week.count === 0 ? (
        <p className="mt-1 text-xs text-muted-foreground">이 주에 만료되는 인증서가 없습니다.</p>
      ) : (
        <ul className="mt-1">
          {week.topCNs.map((cn, i) => (
            // CN은 중복될 수 있어 순번을 키로 쓴다 (정적 목록이라 안전하다).
            <li key={i} className="truncate font-mono text-[11px] leading-4 text-foreground/80">
              {cn}
            </li>
          ))}
          {week.count > week.topCNs.length && (
            <li className="text-[11px] leading-4 text-muted-foreground">
              외 {week.count - week.topCNs.length}건
            </li>
          )}
        </ul>
      )}
    </>
  );
}
