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
import {
  getCertificateSummary,
  listCertificates,
  type Certificate,
  type CertificateSummary,
} from "@/lib/api";
import {
  dnCommonName,
  formatDate,
  formatDateTime,
  isSelfSigned,
  shortFingerprint,
} from "@/lib/format";
import { cn } from "@/lib/utils";

interface DashboardData {
  summary: CertificateSummary;
  expiring: Certificate[];
  recent: Certificate[];
}

export default function DashboardPage() {
  const [data, setData] = React.useState<DashboardData | null>(null);
  const [error, setError] = React.useState<string | null>(null);

  React.useEffect(() => {
    let cancelled = false;
    Promise.all([
      getCertificateSummary(),
      listCertificates(
        new URLSearchParams({
          expires_within_days: "30",
          sort: "not_after",
          order: "asc",
          page_size: "8",
        }),
      ),
      listCertificates(
        new URLSearchParams({ sort: "created_at", order: "desc", page_size: "5" }),
      ),
    ])
      .then(([summary, expiring, recent]) => {
        if (!cancelled)
          setData({ summary, expiring: expiring.items, recent: recent.items });
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
      <div>
        <h1 className="text-lg font-semibold tracking-tight">대시보드</h1>
        <p className="text-[13px] text-muted-foreground">
          전사 인증서 현황 요약 · 각 숫자를 누르면 해당 목록으로 이동합니다.
        </p>
      </div>

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
  const kpis: {
    label: string;
    value: number;
    href: string;
    danger?: boolean;
    note: string;
  }[] = [
    { label: "전체 인증서", value: s.total, href: "/certificates", note: "인벤토리 등록 기준" },
    {
      label: "7일 이내 만료", value: s.expiring_7d, href: "/certificates?expires_within_days=7",
      danger: s.expiring_7d > 0, note: s.expiring_7d > 0 ? "즉시 조치 필요" : "해당 없음",
    },
    { label: "30일 이내 만료", value: s.expiring_30d, href: "/certificates?expires_within_days=30", note: "7일 이내 포함" },
    { label: "60일 이내 만료", value: s.expiring_60d, href: "/certificates?expires_within_days=60", note: "30일 이내 포함" },
    { label: "90일 이내 만료", value: s.expiring_90d, href: "/certificates?expires_within_days=90", note: "60일 이내 포함" },
    {
      label: "이미 만료", value: s.expired, href: "/certificates?expired=true",
      danger: s.expired > 0, note: s.expired > 0 ? "정리 또는 갱신 필요" : "해당 없음",
    },
    { label: "자체 서명", value: s.self_signed, href: "/certificates?self_signed=true", note: "내부 CA 이관 검토 대상" },
    { label: "소유자 미지정", value: s.unowned, href: "/certificates?unowned=true", note: "담당자 지정 필요" },
  ];

  return (
    <>
      <div className="grid grid-cols-2 gap-3 lg:grid-cols-4">
        {kpis.map((k) => (
          <Link
            key={k.label}
            href={k.href}
            className="rounded-lg border bg-card p-4 transition-colors hover:bg-muted/50 focus-visible:ring-3 focus-visible:ring-ring/50 focus-visible:outline-none"
          >
            <div className="text-[13px] text-muted-foreground">{k.label}</div>
            <div
              className={cn(
                "mt-1.5 font-mono text-2xl font-semibold tracking-tight tabular-nums",
                k.danger && "text-red-600",
              )}
            >
              {k.value.toLocaleString()}
            </div>
            <div className="mt-1 text-xs text-muted-foreground">{k.note}</div>
          </Link>
        ))}
      </div>

      <div className="grid items-start gap-4 xl:grid-cols-[minmax(0,1fr)_340px]">
        <Card className="gap-0 overflow-hidden py-0">
          <div className="flex items-center gap-2 border-b px-4 py-3">
            <h2 className="text-sm font-semibold tracking-tight">만료 임박 인증서</h2>
            <span className="text-xs text-muted-foreground">30일 이내 · 상위 8건</span>
            <Button asChild variant="outline" size="sm" className="ml-auto">
              <Link href="/certificates?expires_within_days=30">
                전체 {s.expiring_30d}건 보기
              </Link>
            </Button>
          </div>
          {data.expiring.length === 0 ? (
            <p className="px-4 py-8 text-center text-sm text-muted-foreground">
              30일 이내에 만료되는 인증서가 없습니다.
            </p>
          ) : (
            <Table>
              <TableHeader>
                <TableRow>
                  <TableHead>Common Name</TableHead>
                  <TableHead>만료일</TableHead>
                  <TableHead>남은 일수</TableHead>
                  <TableHead>발급자</TableHead>
                  <TableHead>상태</TableHead>
                </TableRow>
              </TableHeader>
              <TableBody>
                {data.expiring.map((c) => (
                  <TableRow key={c.id}>
                    <TableCell>
                      <Link
                        href={`/certificates/${c.id}`}
                        className="font-mono text-xs font-medium hover:underline hover:underline-offset-3"
                      >
                        {c.common_name || dnCommonName(c.subject_dn)}
                      </Link>
                    </TableCell>
                    <TableCell className="font-mono text-xs tabular-nums">
                      {formatDate(c.not_after)}
                    </TableCell>
                    <TableCell>
                      <DdayText notAfter={c.not_after} />
                    </TableCell>
                    <TableCell className="text-[13px]">
                      {isSelfSigned(c.subject_dn, c.issuer_dn)
                        ? "자체 서명"
                        : dnCommonName(c.issuer_dn)}
                    </TableCell>
                    <TableCell>
                      <CertStatusBadge status={c.status} notAfter={c.not_after} />
                    </TableCell>
                  </TableRow>
                ))}
              </TableBody>
            </Table>
          )}
        </Card>

        <div className="grid gap-4">
          <ExpiryHorizon summary={s} />
          <Card className="gap-0 overflow-hidden py-0">
            <div className="flex items-center gap-2 border-b px-4 py-3">
              <h2 className="text-sm font-semibold tracking-tight">최근 등록된 인증서</h2>
              <Button asChild variant="outline" size="sm" className="ml-auto">
                <Link href="/discovery">Discovery</Link>
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
        </div>
      </div>
    </>
  );
}

/** 만료 구간 스택 바 (목업 signature) — 요약 누적값을 구간으로 나눠 그린다. */
function ExpiryHorizon({ summary: s }: { summary: CertificateSummary }) {
  const buckets = [
    { label: "7일 이내", value: s.expiring_7d, className: "bg-red-600" },
    { label: "8–30일", value: s.expiring_30d - s.expiring_7d, className: "bg-amber-500" },
    { label: "31–60일", value: s.expiring_60d - s.expiring_30d, className: "bg-zinc-400" },
    { label: "61–90일", value: s.expiring_90d - s.expiring_60d, className: "bg-zinc-300" },
  ];
  const total = s.expiring_90d;

  return (
    <Card className="gap-0 overflow-hidden py-0">
      <div className="flex items-center gap-2 border-b px-4 py-3">
        <h2 className="text-sm font-semibold tracking-tight">만료 구간</h2>
        <span className="text-xs text-muted-foreground">90일 이내 {total}건</span>
      </div>
      <div className="p-4">
        <div className="mb-4 flex h-2 overflow-hidden rounded-full bg-secondary">
          {total > 0 &&
            buckets.map((b) => (
              <i
                key={b.label}
                className={b.className}
                style={{ width: `${(b.value / total) * 100}%` }}
              />
            ))}
        </div>
        {buckets.map((b) => (
          <div
            key={b.label}
            className="flex items-center gap-2 py-1.5 text-[13px] text-foreground/80"
          >
            <span className={cn("size-2 rounded-[2px]", b.className)} />
            {b.label}
            <span className="ml-auto font-mono text-xs font-medium tabular-nums">
              {b.value}
            </span>
            <span className="w-11 text-right font-mono text-xs text-muted-foreground/70 tabular-nums">
              {total > 0 ? `${((b.value / total) * 100).toFixed(1)}%` : "—"}
            </span>
          </div>
        ))}
        <div className="mt-3 flex justify-between border-t pt-3 text-xs text-muted-foreground">
          <span>이미 만료 {s.expired}건은 구간에서 제외</span>
          <span>기준 {formatDate(new Date().toISOString())}</span>
        </div>
      </div>
    </Card>
  );
}

function DashboardSkeleton() {
  return (
    <div className="space-y-4">
      <div className="grid grid-cols-2 gap-3 lg:grid-cols-4">
        {Array.from({ length: 8 }).map((_, i) => (
          <Skeleton key={i} className="h-24 rounded-lg" />
        ))}
      </div>
      <div className="grid gap-4 xl:grid-cols-[minmax(0,1fr)_340px]">
        <Skeleton className="h-72 rounded-lg" />
        <Skeleton className="h-72 rounded-lg" />
      </div>
    </div>
  );
}
