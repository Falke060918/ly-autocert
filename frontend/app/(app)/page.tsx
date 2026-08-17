"use client";

import * as React from "react";
import Link from "next/link";

import { Alert, AlertDescription } from "@/components/ui/alert";
import { Button } from "@/components/ui/button";
import { Card } from "@/components/ui/card";
import { Skeleton } from "@/components/ui/skeleton";
import {
  Table,
  TableBody,
  TableCell,
  TableHead,
  TableHeader,
  TableRow,
} from "@/components/ui/table";
import { CertStatusBadge, DdayText } from "@/components/cert-badges";
import { ExpiryHorizonHero } from "@/components/charts/expiry-horizon";
import { IssuerDistributionCard } from "@/components/charts/issuer-distribution";
import { ScanStatusCard } from "@/components/charts/scan-status-card";
import {
  getCertificateSummary,
  listCertificates,
  type Certificate,
  type CertificateSummary,
} from "@/lib/api";
import {
  dnCommonName,
  dday,
  formatDate,
  formatDateTime,
  isSelfSigned,
  shortFingerprint,
} from "@/lib/format";
import { cn } from "@/lib/utils";

interface DashboardData {
  summary: CertificateSummary;
  /** 90일 이내 만료, not_after 오름차순 (최대 600건) — 히어로·임박 표 공용. */
  horizon: Certificate[];
  horizonTotal: number;
  recent: Certificate[];
}

/** 백엔드 page_size 상한(200) — 넘겨 보내면 400이라 페이지 루프로 모은다. */
const HORIZON_PAGE_SIZE = 200;
/** 히어로 그래프용 안전 상한 — 3페이지(600건)면 시각화에 충분하다. */
const HORIZON_MAX_PAGES = 3;

/**
 * 만료 지평선 히어로용 90일 목록 — 30일 임박 표도 이 목록에서 앞부분을 자른다.
 * 90일치가 상한을 넘을 수 있어 page_size=200으로 최대 3페이지까지 이어 받는다.
 * total은 서버가 준 전체 건수라 잘린 경우에도 히어로가 실제 규모를 안다.
 */
async function fetchExpiryHorizon(): Promise<{
  items: Certificate[];
  total: number;
}> {
  const items: Certificate[] = [];
  let total = 0;
  for (let page = 1; page <= HORIZON_MAX_PAGES; page++) {
    const res = await listCertificates(
      new URLSearchParams({
        expires_within_days: "90",
        sort: "not_after",
        order: "asc",
        page: String(page),
        page_size: String(HORIZON_PAGE_SIZE),
      }),
    );
    items.push(...res.items);
    total = res.total;
    if (items.length >= res.total || res.items.length < HORIZON_PAGE_SIZE) break;
  }
  return { items, total };
}

export default function DashboardPage() {
  const [data, setData] = React.useState<DashboardData | null>(null);
  const [error, setError] = React.useState<string | null>(null);

  React.useEffect(() => {
    let cancelled = false;
    Promise.all([
      getCertificateSummary(),
      fetchExpiryHorizon(),
      listCertificates(
        new URLSearchParams({ sort: "created_at", order: "desc", page_size: "6" }),
      ),
    ])
      .then(([summary, horizon, recent]) => {
        if (!cancelled)
          setData({
            summary,
            horizon: horizon.items,
            horizonTotal: horizon.total,
            recent: recent.items,
          });
      })
      .catch((err: unknown) => {
        if (!cancelled)
          setError(err instanceof Error ? err.message : "요약 조회에 실패했습니다.");
      });
    return () => {
      cancelled = true;
    };
  }, []);

  return (
    <div className="mx-auto max-w-[1400px] space-y-4">
      {/* 제목은 화면에서 걷어냈지만 문서 구조(h1)는 스크린리더용으로 남긴다. */}
      <h1 className="sr-only">대시보드</h1>

      {error && (
        <Alert variant="destructive">
          <AlertDescription>{error}</AlertDescription>
        </Alert>
      )}

      {data ? <DashboardBody data={data} /> : !error && <DashboardSkeleton />}
    </div>
  );
}

function DashboardBody({ data }: { data: DashboardData }) {
  const s = data.summary;
  // 30일 임박 표 — 히어로용 90일 목록(만료 임박순)에서 파생한다.
  // dday().days는 백엔드 필터의 floor(epoch/86400)와 같은 값이다 (lib/format.ts).
  // 섹션 높이가 고정되고 표가 내부 스크롤이라 상한은 DOM 안전장치(20건)만 둔다.
  const expiring = data.horizon
    .filter((c) => dday(c.not_after).days <= 30)
    .slice(0, 20);

  return (
    <>
      <ExpiryHorizonHero summary={s} certs={data.horizon} total={data.horizonTotal} />

      <KpiBand summary={s} />

      {/*
        세 섹션은 같은 행에서 높이를 lg:h-[30rem]으로 통일한다 — 넘치는 내용은
        각 섹션 안에서 스크롤한다(문서 스크롤은 그대로 둔다, GOTCHAS 참조).
        만료 임박 표가 가로 스크롤 없이 들어가도록 옆 두 카드는 300px로 잡는다.
      */}
      <div className="grid gap-4 lg:grid-cols-2 xl:grid-cols-[minmax(0,1fr)_300px_300px]">
        <Card className="gap-0 overflow-hidden py-0 lg:col-span-2 lg:h-[30rem] xl:col-span-1">
          <div className="flex shrink-0 items-center gap-2 border-b px-4 py-3">
            <h2 className="text-sm font-semibold tracking-tight">만료 임박 인증서</h2>
            <span className="text-xs text-muted-foreground">30일 이내 · 임박순</span>
            <Button asChild variant="outline" size="sm" className="ml-auto">
              <Link href="/certificates?expires_within_days=30">
                전체 {s.expiring_30d}건 보기
              </Link>
            </Button>
          </div>
          {expiring.length === 0 ? (
            <p className="px-4 py-8 text-center text-sm text-muted-foreground">
              30일 이내에 만료되는 인증서가 없습니다.
            </p>
          ) : (
            <div className="min-h-0 flex-1 overflow-y-auto">
              <Table>
                <TableHeader className="sticky top-0 z-10 bg-card">
                  <TableRow>
                    <TableHead>Common Name</TableHead>
                    {/* 만료일과 남은 일수는 한 열로 합친다 — 폭을 아껴 가로 스크롤을 없앤다. */}
                    <TableHead>만료</TableHead>
                    <TableHead>발급자</TableHead>
                    <TableHead>상태</TableHead>
                  </TableRow>
                </TableHeader>
                <TableBody>
                  {expiring.map((c) => (
                    <TableRow key={c.id}>
                      <TableCell className="max-w-56">
                        <Link
                          href={`/certificates/${c.id}`}
                          className="block truncate font-mono text-xs font-medium hover:underline hover:underline-offset-3"
                          title={c.common_name || dnCommonName(c.subject_dn)}
                        >
                          {c.common_name || dnCommonName(c.subject_dn)}
                        </Link>
                      </TableCell>
                      <TableCell>
                        <span className="font-mono text-xs tabular-nums">
                          {formatDate(c.not_after)}
                        </span>{" "}
                        <DdayText notAfter={c.not_after} />
                      </TableCell>
                      <TableCell className="max-w-40 truncate text-[13px]">
                        {isSelfSigned(c.subject_dn, c.issuer_dn)
                          ? "자체 서명"
                          : (c.issuer_organization ?? dnCommonName(c.issuer_dn))}
                      </TableCell>
                      <TableCell>
                        <CertStatusBadge status={c.status} notAfter={c.not_after} />
                      </TableCell>
                    </TableRow>
                  ))}
                </TableBody>
              </Table>
            </div>
          )}
        </Card>

        <IssuerDistributionCard />
        <ScanStatusCard />
      </div>

      <Card className="gap-0 overflow-hidden py-0">
        <div className="flex items-center gap-2 border-b px-4 py-3">
          <h2 className="text-sm font-semibold tracking-tight">최근 등록된 인증서</h2>
          <Button asChild variant="outline" size="sm" className="ml-auto">
            <Link href="/certificates?sort=created_at&order=desc">전체 목록</Link>
          </Button>
        </div>
        {data.recent.length === 0 ? (
          <p className="px-4 py-8 text-center text-sm text-muted-foreground">
            아직 등록된 인증서가 없습니다. 업로드 또는 스캔으로 시작하십시오.
          </p>
        ) : (
          <ul>
            {data.recent.map((c) => (
              <li
                key={c.id}
                className="flex items-baseline gap-3 border-b px-4 py-2.5 last:border-b-0"
              >
                <div className="min-w-0">
                  <Link
                    href={`/certificates/${c.id}`}
                    className="block truncate font-mono text-xs font-medium hover:underline hover:underline-offset-3"
                  >
                    {c.common_name || dnCommonName(c.subject_dn)}
                  </Link>
                  <div className="mt-0.5 font-mono text-xs text-muted-foreground/70">
                    {shortFingerprint(c.fingerprint_sha256)}
                  </div>
                </div>
                <div className="ml-auto shrink-0 font-mono text-xs text-muted-foreground/70">
                  {formatDateTime(c.created_at)}
                </div>
              </li>
            ))}
          </ul>
        )}
      </Card>
    </>
  );
}

/**
 * KPI 밴드 — 8개 지표를 한 줄 하이라인으로 압축한다.
 * 각 숫자는 해당 필터 목록으로 가는 링크다 (02-goals 성공 기준 3 — 반드시 유지).
 * 상태색은 위험 값(만료·7일 이내 > 0)에만 쓴다.
 */
function KpiBand({ summary: s }: { summary: CertificateSummary }) {
  const kpis: { label: string; value: number; href: string; danger?: boolean }[] = [
    { label: "전체 인증서", value: s.total, href: "/certificates" },
    {
      label: "이미 만료",
      value: s.expired,
      href: "/certificates?expired=true",
      danger: s.expired > 0,
    },
    {
      label: "7일 이내",
      value: s.expiring_7d,
      href: "/certificates?expires_within_days=7",
      danger: s.expiring_7d > 0,
    },
    { label: "30일 이내", value: s.expiring_30d, href: "/certificates?expires_within_days=30" },
    { label: "60일 이내", value: s.expiring_60d, href: "/certificates?expires_within_days=60" },
    { label: "90일 이내", value: s.expiring_90d, href: "/certificates?expires_within_days=90" },
    { label: "자체 서명", value: s.self_signed, href: "/certificates?self_signed=true" },
    { label: "소유자 미지정", value: s.unowned, href: "/certificates?unowned=true" },
  ];

  return (
    <div className="grid grid-cols-2 gap-px overflow-hidden rounded-lg border bg-border sm:grid-cols-4 xl:grid-cols-8">
      {kpis.map((k) => (
        <Link
          key={k.label}
          href={k.href}
          // 제목 블록을 걷어내며 "각 숫자를 누르면 목록으로 간다"는 안내 문구도 같이
          // 사라졌다 — 세로를 쓰지 않는 툴팁으로 그 정보를 대신 남긴다.
          title={`${k.label} 목록 보기`}
          className="bg-card px-4 py-3 transition-colors hover:bg-muted/50 focus-visible:z-10 focus-visible:ring-3 focus-visible:ring-ring/50 focus-visible:outline-none"
        >
          <div className="text-xs text-muted-foreground">{k.label}</div>
          <div
            className={cn(
              "mt-1 font-mono text-xl font-semibold tracking-tight tabular-nums",
              k.danger && "text-danger",
            )}
          >
            {k.value.toLocaleString()}
          </div>
        </Link>
      ))}
    </div>
  );
}

function DashboardSkeleton() {
  return (
    <div className="space-y-4">
      {/* 만료 지평선 히어로 자리 — 실제 높이(헤더 49 + 본문 134)에 맞춘다. */}
      <Skeleton className="h-48 rounded-lg" />
      <div className="grid grid-cols-2 gap-px sm:grid-cols-4 xl:grid-cols-8">
        {Array.from({ length: 8 }).map((_, i) => (
          <Skeleton key={i} className="h-[70px] rounded-none first:rounded-l-lg last:rounded-r-lg" />
        ))}
      </div>
      <div className="grid gap-4 lg:grid-cols-2 xl:grid-cols-[minmax(0,1fr)_300px_300px]">
        <Skeleton className="h-80 rounded-lg lg:col-span-2 lg:h-[30rem] xl:col-span-1" />
        <Skeleton className="h-80 rounded-lg lg:h-[30rem]" />
        <Skeleton className="h-80 rounded-lg lg:h-[30rem]" />
      </div>
      <Skeleton className="h-56 rounded-lg" />
    </div>
  );
}
