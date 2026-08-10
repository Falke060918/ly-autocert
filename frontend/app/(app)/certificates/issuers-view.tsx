"use client";

/**
 * 발급 기관 뷰 — 인벤토리를 CA 관점(루트→중간 2계층)으로 뒤집어 본다.
 * 집계는 목록 페이지가 한 번 받아 내려준다(필터 Select와 공유).
 * 보유 CA를 고르면 issued_by=<certificate_id> 필터로 목록 뷰에 넘긴다.
 */

import * as React from "react";
import Link from "next/link";
import { ChevronRight } from "lucide-react";

import { Alert, AlertDescription } from "@/components/ui/alert";
import { Badge } from "@/components/ui/badge";
import { Card } from "@/components/ui/card";
import { Skeleton } from "@/components/ui/skeleton";
import {
  ISSUED_BY_PARAM,
  type IssuerAggregationResponse,
  type IssuerCANode,
} from "@/lib/issuers";
import { dnCommonName } from "@/lib/format";
import { cn } from "@/lib/utils";

/** 노드 표시 이름 — CN → 조직 → subject DN 파싱 순으로 폴백한다. */
function nodeLabel(node: IssuerCANode): string {
  return node.common_name || node.organization || dnCommonName(node.subject_dn);
}

export function IssuersView({
  agg,
  error,
}: {
  /** 집계 응답. 로딩 중에는 null이다. */
  agg: IssuerAggregationResponse | null;
  /** 집계 API 실패 여부 — 실패면 안내만 보인다. */
  error: boolean;
}) {
  if (error) {
    return (
      <Alert>
        <AlertDescription>
          발급 기관 집계를 불러오지 못했습니다. 잠시 후 다시 시도하거나 목록
          탭에서 인증서를 확인하십시오.
        </AlertDescription>
      </Alert>
    );
  }
  if (!agg) return <Skeleton className="h-72 rounded-lg" />;

  if (agg.roots.length === 0) {
    return (
      <Card className="gap-0 py-0">
        <p className="px-4 py-12 text-center text-sm text-muted-foreground">
          체인이 연결된 발급 기관이 아직 없습니다. 인증서를 등록하거나 Discovery
          스캔으로 체인 정보를 수집하면 여기에 CA 계층이 나타납니다.
        </p>
      </Card>
    );
  }

  return (
    <div className="grid gap-4">
      {agg.roots.map((root, i) => (
        <RootGroup key={root.certificate_id ?? `${root.subject_dn}-${i}`} root={root} />
      ))}
    </div>
  );
}

/** 루트 CA 1개 + 그 아래 중간 CA 목록. */
function RootGroup({ root }: { root: IssuerCANode }) {
  return (
    <Card className="gap-0 overflow-hidden py-0">
      <CANodeRow node={root} depth={0} />
      {root.children.length > 0 && (
        <div className="border-t">
          {root.children.map((child, i) => (
            <CANodeRow
              key={child.certificate_id ?? `${child.subject_dn}-${i}`}
              node={child}
              depth={1}
            />
          ))}
        </div>
      )}
    </Card>
  );
}

/** CA 노드 1행 — 보유 CA는 클릭 시 발급 목록으로, 미보유는 회색 표기만. */
function CANodeRow({ node, depth }: { node: IssuerCANode; depth: 0 | 1 }) {
  const label = nodeLabel(node);
  const clickable = node.stored && node.certificate_id != null;

  const body = (
    <div
      className={cn(
        "flex min-w-0 items-center gap-3 px-4 py-3",
        depth === 1 && "border-b py-2.5 pl-6 last:border-b-0",
      )}
    >
      {depth === 1 && (
        <span
          aria-hidden
          className="shrink-0 font-mono text-xs text-muted-foreground/50"
        >
          └
        </span>
      )}
      <div className="min-w-0 flex-1">
        <div className="flex flex-wrap items-center gap-1.5">
          <span
            className={cn(
              "truncate font-mono text-xs",
              depth === 0 ? "font-semibold" : "font-medium",
              !node.stored && "text-muted-foreground",
            )}
          >
            {label}
          </span>
          <Badge variant="outline" className="text-muted-foreground">
            {depth === 0 ? "루트 CA" : "중간 CA"}
          </Badge>
          {!node.stored && (
            <Badge variant="outline" className="text-muted-foreground/70">
              미보유
            </Badge>
          )}
        </div>
        {node.organization && node.organization !== label && (
          <div className="mt-0.5 truncate text-xs text-muted-foreground">
            {node.organization}
          </div>
        )}
        {!node.stored && (
          <div className="mt-0.5 text-xs text-muted-foreground/70">
            인벤토리에 없는 상위 인증서입니다. 체인 정보로만 확인되었습니다.
          </div>
        )}
      </div>
      <span className="shrink-0 font-mono text-xs text-muted-foreground tabular-nums">
        {depth === 0 && node.children.length > 0
          ? `하위 포함 리프 ${node.leaf_count.toLocaleString()}건`
          : `리프 ${node.leaf_count.toLocaleString()}건`}
      </span>
      {clickable && (
        <ChevronRight aria-hidden className="size-3.5 shrink-0 text-muted-foreground/60" />
      )}
    </div>
  );

  return clickable ? (
    <Link
      href={`/certificates?${ISSUED_BY_PARAM}=${encodeURIComponent(node.certificate_id!)}`}
      className="block transition-colors hover:bg-secondary/60"
    >
      {body}
    </Link>
  ) : (
    body
  );
}

/** 집계 트리에서 certificate_id로 노드를 찾는다 — 필터 칩 라벨·상세 링크 공용. */
export function findCANode(
  agg: IssuerAggregationResponse | null,
  certificateId: string | null,
): IssuerCANode | null {
  if (!agg || !certificateId) return null;
  for (const root of agg.roots) {
    for (const node of [root, ...root.children]) {
      if (node.certificate_id === certificateId) return node;
    }
  }
  return null;
}
