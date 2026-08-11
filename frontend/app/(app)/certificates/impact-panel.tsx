"use client";

/**
 * 영향 범위 패널 — 트리에서 고른 CA 하나에 대해 "이 CA가 만료되면 무엇을 다시
 * 발급해야 하나"를 답한다.
 *
 * 담당자의 실제 질문이 그 한 줄이므로 정보 순서를 거기에 맞춘다:
 *   ① 마감일(이 CA의 D-day)  ② 다시 발급해야 할 것(자손 CA·리프 수)
 *   ③ 언제까지 몰리는가(만료 분포)  ④ 어디를 손대야 하는가(서버·도메인)
 * 트리 노드가 이미 답하는 "직접 발급 몇 건"은 여기서 반복하지 않는다.
 *
 * ⚠ 축이 두 개라는 점을 라벨이 정확히 지켜야 한다 —
 *   - 인증서 목록 `issued_by=`는 **직계 1단계**다.
 *   - 이 패널의 수치와 서버 목록 `issued_by=`는 **계열 전체**(전이적 자손)다.
 *   같은 이름으로 부르면 "목록 12건인데 영향은 47건"이 오류처럼 보인다.
 *
 * ⚠ 도메인·서버 목록은 페이지네이션이 없고 list_limit(200)에서 잘린다.
 *   `*_truncated`가 true면 잘렸다는 사실과 전체 목록 링크를 반드시 함께 낸다.
 */

import * as React from "react";
import Link from "next/link";
import { ArrowUpRight } from "lucide-react";

import { Alert, AlertDescription } from "@/components/ui/alert";
import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import { Card } from "@/components/ui/card";
import { Skeleton } from "@/components/ui/skeleton";
import { ExpiryDdayBadge } from "@/components/cert-badges";
import { expiryBuckets } from "@/lib/ca-expiry";
import { formatDate } from "@/lib/format";
import {
  getCertificateImpact,
  type CertificateImpactResponse,
} from "@/lib/impact";
import { ISSUED_BY_PARAM, type IssuerCANode } from "@/lib/issuers";
import { serverLabel, SERVER_ISSUED_BY_PARAM } from "@/lib/servers";
import { cn } from "@/lib/utils";

/** 이 CA가 직접 발급한 인증서 목록(직계 1단계). */
export function issuedByListHref(certificateId: string): string {
  return `/certificates?${ISSUED_BY_PARAM}=${encodeURIComponent(certificateId)}`;
}

/** 이 CA 계열(전이적 자손 전체)이 붙어 있는 서버 목록. */
export function issuedByServersHref(certificateId: string): string {
  return `/certificates?view=servers&${SERVER_ISSUED_BY_PARAM}=${encodeURIComponent(certificateId)}`;
}

type PanelState =
  | { kind: "loading" }
  | { kind: "error"; message: string }
  | { kind: "ready"; impact: CertificateImpactResponse };

export function ImpactPanel({ node }: { node: IssuerCANode | null }) {
  const certificateId = node?.certificate_id ?? null;
  /**
   * 응답은 **어느 인증서의 것인지**와 함께 담는다. 선택이 바뀌면 id가 어긋나
   * 자동으로 로딩으로 되돌아가므로, 이펙트 본문에서 상태를 되돌릴 필요가 없다
   * (이펙트 안의 동기 setState는 연쇄 렌더를 만든다).
   */
  const [result, setResult] = React.useState<{
    id: string;
    state: PanelState;
  } | null>(null);

  // 노드를 고를 때마다 지연 호출한다 — 트리를 그릴 때 전건을 미리 받지 않는다.
  React.useEffect(() => {
    if (!certificateId) return;
    let cancelled = false;
    getCertificateImpact(certificateId)
      .then((impact) => {
        if (!cancelled) {
          setResult({ id: certificateId, state: { kind: "ready", impact } });
        }
      })
      .catch((err: unknown) => {
        if (cancelled) return;
        setResult({
          id: certificateId,
          state: {
            kind: "error",
            message:
              err instanceof Error
                ? err.message
                : "영향 범위를 불러오지 못했습니다.",
          },
        });
      });
    return () => {
      cancelled = true;
    };
  }, [certificateId]);

  const state: PanelState =
    result && result.id === certificateId ? result.state : { kind: "loading" };

  if (!node) {
    return (
      <PanelShell>
        <p className="px-4 py-10 text-center text-sm text-muted-foreground">
          왼쪽에서 발급 기관을 고르면 그 기관이 만료될 때 다시 발급해야 할
          인증서와 영향받는 서버가 여기에 나옵니다.
        </p>
      </PanelShell>
    );
  }

  if (!certificateId) {
    return (
      <PanelShell title={node.common_name ?? node.subject_dn}>
        <p className="px-4 py-10 text-center text-sm text-muted-foreground">
          인벤토리에 없는 상위 인증서라 영향 범위를 계산할 수 없습니다. 이 기관의
          인증서를 등록하면 아래 계층까지 한 번에 집계됩니다.
        </p>
      </PanelShell>
    );
  }

  return (
    <PanelShell title={node.common_name ?? node.subject_dn} node={node}>
      {state.kind === "loading" && (
        <div className="space-y-3 px-4 py-4">
          <Skeleton className="h-20 rounded-lg" />
          <Skeleton className="h-24 rounded-lg" />
          <Skeleton className="h-32 rounded-lg" />
        </div>
      )}
      {state.kind === "error" && (
        <div className="px-4 py-4">
          <Alert variant="destructive">
            <AlertDescription>{state.message}</AlertDescription>
          </Alert>
        </div>
      )}
      {state.kind === "ready" && (
        <ImpactBody impact={state.impact} certificateId={certificateId} />
      )}
    </PanelShell>
  );
}

function PanelShell({
  title,
  node,
  children,
}: {
  title?: string;
  node?: IssuerCANode;
  children: React.ReactNode;
}) {
  return (
    <Card className="gap-0 overflow-hidden py-0">
      <div className="border-b px-4 py-3">
        <div className="text-[11px] font-medium tracking-[0.14em] text-muted-foreground uppercase">
          영향 범위
        </div>
        {title ? (
          <div className="mt-1 flex flex-wrap items-center gap-1.5">
            <span className="min-w-0 truncate font-mono text-[13px] font-semibold">
              {title}
            </span>
            {node && <ExpiryDdayBadge notAfter={node.not_after} />}
          </div>
        ) : (
          <div className="mt-1 text-[13px] text-muted-foreground">
            선택된 발급 기관 없음
          </div>
        )}
      </div>
      {children}
    </Card>
  );
}

function ImpactBody({
  impact,
  certificateId,
}: {
  impact: CertificateImpactResponse;
  certificateId: string;
}) {
  const b = expiryBuckets(impact.expiry, impact.certificate_count);
  const total = impact.certificate_count || 1;
  const segments = [
    { key: "expired", label: "이미 만료", n: b.expired, bar: "bg-danger" },
    { key: "d7", label: "7일 이내", n: b.d7, bar: "bg-danger/55" },
    { key: "d30", label: "30일 이내", n: b.d30, bar: "bg-warn" },
    { key: "d90", label: "90일 이내", n: b.d90, bar: "bg-warn/45" },
    { key: "rest", label: "90일 이후", n: b.rest, bar: "bg-ok/45" },
  ].filter((s) => s.n > 0);

  return (
    <div className="divide-y">
      {/* ① 다시 발급해야 할 것 — 이 화면의 답이다. 가장 크게 적는다. */}
      <section className="px-4 py-4">
        <div className="flex items-baseline gap-2">
          <span className="font-mono text-3xl leading-none font-semibold tabular-nums">
            {impact.descendant_leaf_count.toLocaleString()}
          </span>
          <span className="text-sm text-muted-foreground">
            장을 다시 발급해야 합니다
          </span>
        </div>
        <p className="mt-1.5 text-xs text-muted-foreground">
          {impact.descendant_ca_count > 0 ? (
            <>
              먼저 하위 발급 기관{" "}
              <strong className="font-medium text-foreground">
                {impact.descendant_ca_count.toLocaleString()}개
              </strong>
              를 새로 세워야 합니다 — 최대 {impact.max_depth}단계까지 이어집니다.
            </>
          ) : (
            <>하위 발급 기관은 없습니다. 리프 인증서만 재발급하면 됩니다.</>
          )}
        </p>
        <div className="mt-3 flex flex-wrap gap-2">
          <Button asChild size="sm" variant="outline">
            <Link href={issuedByListHref(certificateId)}>
              직접 발급분 목록
              <ArrowUpRight data-icon="inline-end" />
            </Link>
          </Button>
          <Button asChild size="sm" variant="outline">
            <Link href={issuedByServersHref(certificateId)}>
              계열 서버 목록
              <ArrowUpRight data-icon="inline-end" />
            </Link>
          </Button>
        </div>
        {/*
          두 링크의 축이 다르다는 점을 여기서 한 번 못 박는다 — 목록 건수와
          위 숫자가 다른 이유가 화면에 없으면 집계 오류로 읽힌다.
        */}
        <p className="mt-2 text-[11px] leading-relaxed text-muted-foreground/80">
          인증서 목록은 이 기관이 <strong className="font-medium">직접</strong>{" "}
          발급한 것만 보여 줍니다. 하위 기관이 발급한 것은 트리에서 그 기관을
          골라 확인하십시오. 서버 목록은 계열 전체입니다.
        </p>
      </section>

      {/* ② 언제까지 몰리는가 — 재발급 순서를 정하는 축. */}
      <section className="px-4 py-4">
        <SectionTitle>만료 시점 분포</SectionTitle>
        <p className="mt-0.5 text-[11px] text-muted-foreground">
          이 기관 자신을 포함한 계열 {impact.certificate_count.toLocaleString()}장
          기준입니다.
        </p>
        {segments.length === 0 ? (
          <p className="mt-2 text-xs text-muted-foreground">
            집계할 인증서가 없습니다.
          </p>
        ) : (
          <>
            <div
              className="mt-3 flex h-2 w-full overflow-hidden rounded-full bg-muted"
              role="img"
              aria-label={segments
                .map((s) => `${s.label} ${s.n}장`)
                .join(", ")}
            >
              {segments.map((s) => (
                <div
                  key={s.key}
                  className={cn("h-full", s.bar)}
                  style={{ width: `${(s.n / total) * 100}%` }}
                />
              ))}
            </div>
            <dl className="mt-3 grid grid-cols-2 gap-x-4 gap-y-1.5">
              {segments.map((s) => (
                <div key={s.key} className="flex items-center gap-2">
                  <span
                    aria-hidden
                    className={cn("size-2 shrink-0 rounded-full", s.bar)}
                  />
                  <dt className="min-w-0 flex-1 truncate text-xs text-muted-foreground">
                    {s.label}
                  </dt>
                  <dd className="font-mono text-xs tabular-nums">
                    {s.n.toLocaleString()}
                  </dd>
                </div>
              ))}
            </dl>
          </>
        )}
      </section>

      {/* ③ 어디를 손대야 하는가 — 배포 지점. */}
      <section className="px-4 py-4">
        <SectionTitle>배포 현황</SectionTitle>
        <dl className="mt-2 grid grid-cols-3 gap-2">
          <Stat label="서버" value={impact.server_count} />
          <Stat label="엔드포인트" value={impact.endpoint_count} />
          <Stat label="도메인" value={impact.domain_count} />
        </dl>
      </section>

      <ImpactDomains impact={impact} certificateId={certificateId} />
      <ImpactServers impact={impact} certificateId={certificateId} />
    </div>
  );
}

function SectionTitle({ children }: { children: React.ReactNode }) {
  return (
    <h3 className="text-[11px] font-medium tracking-[0.14em] text-muted-foreground uppercase">
      {children}
    </h3>
  );
}

function Stat({ label, value }: { label: string; value: number }) {
  return (
    <div className="rounded-lg border px-2.5 py-2">
      <dt className="text-[11px] text-muted-foreground">{label}</dt>
      <dd className="font-mono text-lg leading-tight font-semibold tabular-nums">
        {value.toLocaleString()}
      </dd>
    </div>
  );
}

/**
 * 잘림 안내 — `*_truncated`가 true일 때만 낸다. 전체 목록 링크를 반드시 함께
 * 낸다(잘린 목록만 보고 재발급 대상을 정하면 조용히 빠지는 것이 생긴다).
 */
function TruncatedNote({
  shown,
  total,
  href,
  linkLabel,
}: {
  shown: number;
  total: number;
  href: string;
  linkLabel: string;
}) {
  return (
    <p className="mt-2 text-[11px] leading-relaxed text-muted-foreground">
      전체 {total.toLocaleString()}건 중 {shown.toLocaleString()}건만 표시했습니다.{" "}
      <Link href={href} className="text-primary underline underline-offset-2">
        {linkLabel}
      </Link>
    </p>
  );
}

function ImpactDomains({
  impact,
  certificateId,
}: {
  impact: CertificateImpactResponse;
  certificateId: string;
}) {
  const [showAll, setShowAll] = React.useState(false);
  const visible = showAll ? impact.domains : impact.domains.slice(0, 8);

  return (
    <section className="px-4 py-4">
      <SectionTitle>영향받는 도메인</SectionTitle>
      {impact.domains.length === 0 ? (
        <p className="mt-2 text-xs text-muted-foreground">
          집계된 도메인이 없습니다. 계열 인증서가 아직 어느 엔드포인트에서도
          관측되지 않았습니다.
        </p>
      ) : (
        <ul className="mt-2 space-y-1">
          {visible.map((d) => (
            <li
              key={d.name}
              className="flex items-baseline gap-2 text-xs"
            >
              <span className="min-w-0 flex-1 truncate font-mono">{d.name}</span>
              <span className="shrink-0 font-mono text-[11px] text-muted-foreground tabular-nums">
                인증서 {d.certificate_count.toLocaleString()} · 서버{" "}
                {d.server_count.toLocaleString()}
              </span>
            </li>
          ))}
        </ul>
      )}
      {impact.domains.length > 8 && (
        <Button
          variant="ghost"
          size="sm"
          className="mt-1 h-7 px-1.5 text-xs"
          onClick={() => setShowAll((v) => !v)}
        >
          {showAll
            ? "접기"
            : `${(impact.domains.length - 8).toLocaleString()}건 더 보기`}
        </Button>
      )}
      {impact.domains_truncated && (
        <TruncatedNote
          shown={impact.domains.length}
          total={impact.domain_count}
          href={issuedByListHref(certificateId)}
          linkLabel="인증서 목록에서 확인"
        />
      )}
    </section>
  );
}

function ImpactServers({
  impact,
  certificateId,
}: {
  impact: CertificateImpactResponse;
  certificateId: string;
}) {
  const [showAll, setShowAll] = React.useState(false);
  const visible = showAll ? impact.servers : impact.servers.slice(0, 8);

  return (
    <section className="px-4 py-4">
      <SectionTitle>영향받는 서버</SectionTitle>
      {impact.servers.length === 0 ? (
        <p className="mt-2 text-xs text-muted-foreground">
          계열 인증서가 붙어 있는 서버가 아직 없습니다.
        </p>
      ) : (
        <ul className="mt-2 space-y-1.5">
          {visible.map((s) => (
            <li key={s.id} className="flex items-center gap-2">
              <span className="min-w-0 flex-1 truncate font-mono text-xs">
                {serverLabel(s)}
              </span>
              <Badge variant="outline" className="shrink-0 tabular-nums">
                {s.endpoint_count.toLocaleString()}개 포트
              </Badge>
              {s.earliest_not_after ? (
                <ExpiryDdayBadge
                  notAfter={s.earliest_not_after}
                  className="shrink-0"
                />
              ) : (
                <span
                  className="shrink-0 text-[11px] text-muted-foreground/70"
                  title="이 서버에서 서비스 중인 인증서를 특정하지 못했습니다."
                >
                  확인 안 됨
                </span>
              )}
            </li>
          ))}
        </ul>
      )}
      {impact.servers.length > 8 && (
        <Button
          variant="ghost"
          size="sm"
          className="mt-1 h-7 px-1.5 text-xs"
          onClick={() => setShowAll((v) => !v)}
        >
          {showAll
            ? "접기"
            : `${(impact.servers.length - 8).toLocaleString()}대 더 보기`}
        </Button>
      )}
      {impact.servers_truncated && (
        <TruncatedNote
          shown={impact.servers.length}
          total={impact.server_count}
          href={issuedByServersHref(certificateId)}
          linkLabel="서버 목록에서 확인"
        />
      )}
      {/* 기준 인증서의 만료일 — 재발급 마감을 패널 끝에서 한 번 더 못 박는다. */}
      <p className="mt-3 text-[11px] text-muted-foreground">
        기준 만료일 {formatDate(impact.certificate.not_after)}
      </p>
    </section>
  );
}
