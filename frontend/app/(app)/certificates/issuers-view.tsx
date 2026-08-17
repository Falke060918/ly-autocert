"use client";

/**
 * 발급 기관(기관별) 뷰 — 인벤토리를 CA 관점으로 뒤집어 본다.
 *
 * 화면이 답해야 하는 질문은 하나다: **"이 루트가 곧 만료되는데 뭘 다시 발급해야
 * 하나."** 그래서 왼쪽은 계층(누가 누구를 발급했나), 오른쪽은 그 답(계열 전체의
 * 재발급 대상·만료 시점·배포 지점)이다.
 *
 * 트리는 **가로(좌→우)** 로 편다 — 루트 CA가 왼쪽, 중간 CA가 가운데, 말단
 * 인증서가 오른쪽이고, 부모-자식은 연결선(가로 스텁 + 세로 트렁크)으로 잇는다.
 * 체인의 방향(발급 흐름)이 읽는 방향과 일치해 전체 구조가 한눈에 보인다.
 *
 * ⚠ 트리는 **N계층**이다. 루트→중간 2단을 가정하고 children을 한 번만 펴면
 * 손자 CA와 그 아래 리프가 통째로 사라진다 — 렌더는 반드시 재귀다.
 *
 * 정렬은 만료 임박순이 기본이다(토글 없음) — CA 만료는 하위 인증서 전부가
 * 함께 신뢰를 잃는 사건이라, 여기서 가장 먼저 보여야 하는 축이 만료다.
 * 형제 노드끼리만 재배열하고 계층은 서버가 준 그대로 둔다.
 *
 * 리프는 노드당 최대 5건(leaf_preview)만 인라인으로 보여 준다. 나머지는
 * 목록 API의 `issued_by=`(직계 1단계)로 넘긴다 — 두 API가 같은 매칭 술어를
 * 쓰므로 노드의 leaf_count와 목록 건수가 일치한다.
 */

import * as React from "react";
import Link from "next/link";
import { ChevronDown, ChevronRight } from "lucide-react";

import { Alert, AlertDescription } from "@/components/ui/alert";
import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import { Card } from "@/components/ui/card";
import { Skeleton } from "@/components/ui/skeleton";
import { ExpiryDdayBadge } from "@/components/cert-badges";
import { sortCANodesByExpiry } from "@/lib/ca-expiry";
import {
  type CALeafPreview,
  type IssuerAggregationResponse,
  type IssuerCANode,
} from "@/lib/issuers";
import { dnCommonName } from "@/lib/format";
import { cn } from "@/lib/utils";
import { ImpactPanel, issuedByListHref } from "./impact-panel";

/** 노드 표시 이름 — CN → 조직 → subject DN 파싱 순으로 폴백한다. */
function nodeLabel(node: IssuerCANode): string {
  return node.common_name || node.organization || dnCommonName(node.subject_dn);
}

/**
 * 노드 식별 키. 미보유(stored=false) 자리 노드는 id가 없어 subject DN으로
 * 잡는다 — 펼침·선택 상태가 노드마다 따로 남아야 한다.
 */
function nodeKey(node: IssuerCANode): string {
  return node.certificate_id ?? `dn:${node.subject_dn}`;
}

/** 계층 라벨 — 자체 서명 최상위만 "루트", 나머지는 깊이와 무관하게 "중간"이다. */
function tierLabel(node: IssuerCANode): string {
  return node.depth === 0 ? "루트 CA" : "중간 CA";
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
  // 접힌 노드만 담는다 — 기본은 전부 펼침이라 첫 화면에서 계층이 다 보인다.
  const [collapsed, setCollapsed] = React.useState<Set<string>>(new Set());
  const [selectedKey, setSelectedKey] = React.useState<string | null>(null);

  // 노드 수가 많아야 수십 개라 메모이즈하지 않는다(정렬 비용보다 캐시 관리가 크다).
  const roots = agg ? sortCANodesByExpiry(agg.roots) : [];
  const selected = findCANodeByKey(roots, selectedKey);

  const toggle = React.useCallback((key: string) => {
    setCollapsed((prev) => {
      const next = new Set(prev);
      if (next.has(key)) next.delete(key);
      else next.add(key);
      return next;
    });
  }, []);

  if (error) {
    return (
      <Alert>
        <AlertDescription>
          발급 기관 집계를 불러오지 못했습니다. 잠시 후 다시 시도하거나 도메인별
          탭에서 인증서를 확인하십시오.
        </AlertDescription>
      </Alert>
    );
  }
  if (!agg) return <Skeleton className="h-72 rounded-lg" />;

  if (roots.length === 0) {
    return (
      <Card className="gap-0 py-0">
        <p className="px-4 py-12 text-center text-sm text-muted-foreground">
          체인이 연결된 발급 기관이 아직 없습니다. 인증서를 등록하거나 Discovery
          스캔으로 체인 정보를 수집하면 여기에 CA 계층이 나타납니다.
        </p>
      </Card>
    );
  }

  const collapsibleKeys = collectCollapsibleKeys(roots);
  const allCollapsed = collapsibleKeys.every((k) => collapsed.has(k));

  return (
    <div className="grid items-start gap-4 lg:grid-cols-[minmax(0,1fr)_26rem]">
      <div className="min-w-0 space-y-2">
        <div className="flex flex-wrap items-center gap-x-3 gap-y-1">
          <span className="text-[13px] text-muted-foreground">
            왼쪽이 루트 CA, 오른쪽 끝이 말단 인증서입니다. 발급 기관을 누르면 그
            가지 전체가 강조되고 오른쪽에 영향 범위가 나옵니다.
          </span>
          {collapsibleKeys.length > 0 && (
            <Button
              variant="ghost"
              size="sm"
              className="ml-auto h-7 px-2 text-xs"
              onClick={() =>
                setCollapsed(allCollapsed ? new Set() : new Set(collapsibleKeys))
              }
            >
              {allCollapsed ? "모두 펼치기" : "모두 접기"}
            </Button>
          )}
        </div>

        {/* 가로 트리라 폭이 화면을 넘을 수 있다 — 카드 안에서만 가로 스크롤한다. */}
        <Card className="gap-0 overflow-x-auto py-0">
          <div className="min-w-max space-y-5 p-4">
            {roots.map((root) => (
              <CATreeBranch
                key={nodeKey(root)}
                node={root}
                collapsed={collapsed}
                onToggle={toggle}
                selectedKey={selectedKey}
                onSelect={setSelectedKey}
                inBranch={false}
              />
            ))}
          </div>
        </Card>

        {agg.unmatched_leaf_count > 0 && (
          <p className="text-[11px] text-muted-foreground">
            어느 발급 기관과도 연결되지 않은 인증서{" "}
            {agg.unmatched_leaf_count.toLocaleString()}건이 있습니다. 발급 기관
            인증서를 등록하면 이 계층에 붙습니다.
          </p>
        )}
      </div>

      {/* 탑바(h-14)가 sticky라 그 높이만큼 내려 붙인다 — globals.css의 scroll-padding-top과 같은 기준. */}
      <div className="lg:sticky lg:top-16">
        <ImpactPanel node={selected} />
      </div>
    </div>
  );
}

/**
 * CA 노드 1개 + 오른쪽으로 이어지는 하위 CA·리프 미리보기 — **재귀**로 편다.
 * `inBranch`는 "선택된 노드의 자손인가"이고 강조 표시가 이 값으로 내려간다.
 *
 * 배치는 flex 한 줄이다: [노드 카드] ─ [자식 세로 묶음]. 각 자식 행은 자기
 * 세로 중앙으로 들어오는 가로 스텁을 갖고, 형제 사이는 세로 트렁크로 잇는다.
 * `items-center`가 부모 카드를 자식 묶음의 세로 중앙에 두므로 스텁·트렁크가
 * 항상 만난다.
 */
function CATreeBranch({
  node,
  collapsed,
  onToggle,
  selectedKey,
  onSelect,
  inBranch,
}: {
  node: IssuerCANode;
  collapsed: Set<string>;
  onToggle: (key: string) => void;
  selectedKey: string | null;
  onSelect: (key: string) => void;
  inBranch: boolean;
}) {
  const key = nodeKey(node);
  const isSelected = selectedKey === key;
  // 선택된 노드 자신부터 아래 전부가 같은 가지다.
  const branch = inBranch || isSelected;
  const hasBody = node.children.length > 0 || node.leaf_preview.length > 0;
  const open = hasBody && !collapsed.has(key);

  // 오른쪽 열의 행들 — 체인을 잇는 하위 CA를 먼저, 말단(리프)을 그 아래에.
  const rows: { key: string; el: React.ReactNode }[] = [];
  if (open) {
    for (const child of node.children) {
      rows.push({
        key: nodeKey(child),
        el: (
          <CATreeBranch
            node={child}
            collapsed={collapsed}
            onToggle={onToggle}
            selectedKey={selectedKey}
            onSelect={onSelect}
            inBranch={branch}
          />
        ),
      });
    }
    for (const leaf of node.leaf_preview) {
      rows.push({ key: leaf.id, el: <LeafItem leaf={leaf} branch={branch} /> });
    }
    const rest = node.leaf_count - node.leaf_preview.length;
    if (rest > 0 && node.certificate_id) {
      rows.push({
        key: "__more__",
        el: (
          <Link
            href={issuedByListHref(node.certificate_id)}
            className="text-[11px] whitespace-nowrap text-primary underline underline-offset-2"
          >
            리프 {rest.toLocaleString()}건 더 — 전체 보기 (
            {node.leaf_count.toLocaleString()}건)
          </Link>
        ),
      });
    }
  }

  return (
    <div className="flex min-w-0 items-center">
      <CANodeCard
        node={node}
        open={open}
        hasBody={hasBody}
        isSelected={isSelected}
        branch={branch}
        onToggle={() => onToggle(key)}
        onSelect={() => onSelect(key)}
      />
      {rows.length > 0 && (
        <>
          {/* 부모 카드 → 트렁크 가로 스텁. 카드 세로 중앙에서 나간다. */}
          <span aria-hidden className="h-px w-5 shrink-0 bg-border" />
          <ul className="flex flex-col">
            {rows.map((row, i) => (
              <li key={row.key} className="relative flex items-center py-1 pl-5">
                {/* 트렁크 → 이 행 세로 중앙으로 들어오는 가로 스텁. */}
                <span
                  aria-hidden
                  className="absolute top-1/2 left-0 h-px w-5 bg-border"
                />
                {/*
                  세로 트렁크 — 행마다 제 구간을 그린다. 행 사이 간격을 li의
                  py로 잡았으므로(마진·gap 아님) 구간이 끊기지 않고 이어진다.
                  첫 행은 중앙→아래, 막 행은 위→중앙, 가운데 행은 전체다.
                */}
                {rows.length > 1 && (
                  <span
                    aria-hidden
                    className={cn(
                      "absolute left-0 w-px bg-border",
                      i === 0
                        ? "top-1/2 bottom-0"
                        : i === rows.length - 1
                          ? "top-0 bottom-1/2"
                          : "top-0 bottom-0",
                    )}
                  />
                )}
                {row.el}
              </li>
            ))}
          </ul>
        </>
      )}
    </div>
  );
}

/** CA 노드 카드 1장. 펼침 버튼과 선택 버튼은 형제다(버튼 중첩은 유효하지 않다). */
function CANodeCard({
  node,
  open,
  hasBody,
  isSelected,
  branch,
  onToggle,
  onSelect,
}: {
  node: IssuerCANode;
  open: boolean;
  hasBody: boolean;
  isSelected: boolean;
  branch: boolean;
  onToggle: () => void;
  onSelect: () => void;
}) {
  const label = nodeLabel(node);
  const Chevron = open ? ChevronDown : ChevronRight;

  return (
    <div
      className={cn(
        // 가지 강조는 색이 아니라 배경 농도로 낸다 — 상태 3색(danger/warn/ok)은
        // 인증서 상태 전용이라 "선택됨"에 쓰지 않는다.
        "flex w-52 shrink-0 items-start gap-1 rounded-lg border py-2 pr-1.5 pl-3",
        branch ? "border-primary/40 bg-primary/[0.04]" : "bg-card",
        isSelected && "border-primary bg-primary/10",
      )}
    >
      <button
        type="button"
        aria-pressed={isSelected}
        className="min-w-0 flex-1 rounded-sm text-left focus-visible:ring-[3px] focus-visible:ring-ring/50 focus-visible:outline-none"
        onClick={onSelect}
        title={
          node.stored
            ? node.subject_dn
            : `${node.subject_dn} — 인벤토리에 없는 상위 인증서입니다. 체인 정보로만 확인되었습니다.`
        }
      >
        <span
          className={cn(
            "block truncate font-mono text-xs",
            node.depth === 0 ? "font-semibold" : "font-medium",
            !node.stored && "text-muted-foreground",
          )}
        >
          {label}
        </span>
        {node.organization && node.organization !== label && (
          <span className="mt-0.5 block truncate text-[11px] text-muted-foreground">
            {node.organization}
          </span>
        )}
        <span className="mt-1 flex flex-wrap items-center gap-1">
          <Badge variant="outline" className="text-muted-foreground">
            {tierLabel(node)}
          </Badge>
          {/*
            CA의 남은 일수. 임계 구간은 인증서 만료 배지와 같은 dday 하나에서 나온다.
            미보유 상위 CA는 만료일을 모르므로 배지가 아예 나오지 않는다.
          */}
          <ExpiryDdayBadge notAfter={node.not_after} />
          {!node.stored && (
            <Badge variant="outline" className="text-muted-foreground/70">
              미보유
            </Badge>
          )}
        </span>
        {/*
          "직접 발급"과 "하위 포함"을 나눠 적는다 — 목록으로 넘어가는 issued_by는
          직계 1단계라 합산 하나만 적으면 노드 건수와 목록 건수가 어긋나 보인다.
        */}
        <span className="mt-1 block font-mono text-[11px] text-muted-foreground tabular-nums">
          {node.children.length > 0
            ? `직접 ${node.leaf_count.toLocaleString()} · 하위 포함 ${node.subtree_leaf_count.toLocaleString()}`
            : `리프 ${node.leaf_count.toLocaleString()}건`}
        </span>
      </button>

      {hasBody && (
        <button
          type="button"
          aria-expanded={open}
          aria-label={open ? `${label} 접기` : `${label} 펼치기`}
          className="shrink-0 rounded-sm p-0.5 text-muted-foreground/60 hover:bg-secondary hover:text-foreground focus-visible:ring-[3px] focus-visible:ring-ring/50 focus-visible:outline-none"
          onClick={onToggle}
        >
          <Chevron aria-hidden className="size-3.5" />
        </button>
      )}
    </div>
  );
}

/** 말단(리프) 인증서 1건 — 트리 오른쪽 끝에 서는 칩. 누르면 상세로 간다. */
function LeafItem({
  leaf,
  branch,
}: {
  leaf: CALeafPreview;
  branch: boolean;
}) {
  return (
    <span
      className={cn(
        "flex w-48 min-w-0 items-center gap-2 rounded-md px-2.5 py-1.5",
        branch ? "bg-primary/[0.07]" : "bg-secondary/50",
      )}
    >
      <Link
        href={`/certificates/${leaf.id}`}
        className="min-w-0 flex-1 truncate font-mono text-[11px] text-muted-foreground hover:text-foreground hover:underline"
        title={leaf.subject_dn}
      >
        {leaf.common_name || dnCommonName(leaf.subject_dn)}
      </Link>
      <ExpiryDdayBadge notAfter={leaf.not_after} className="shrink-0" />
    </span>
  );
}

/** 펼침·접기 대상이 되는 노드 키 전부 (리프 미리보기나 하위 CA가 있는 노드). */
function collectCollapsibleKeys(nodes: IssuerCANode[]): string[] {
  const out: string[] = [];
  const walk = (node: IssuerCANode) => {
    if (node.children.length > 0 || node.leaf_preview.length > 0) {
      out.push(nodeKey(node));
    }
    node.children.forEach(walk);
  };
  nodes.forEach(walk);
  return out;
}

/** 트리에서 노드 키로 노드를 찾는다 — 깊이 제한 없이 재귀한다. */
function findCANodeByKey(
  nodes: IssuerCANode[],
  key: string | null,
): IssuerCANode | null {
  if (!key) return null;
  for (const node of nodes) {
    if (nodeKey(node) === key) return node;
    const found = findCANodeByKey(node.children, key);
    if (found) return found;
  }
  return null;
}

/**
 * 집계 트리에서 certificate_id로 노드를 찾는다 — 필터 칩 라벨·상세 링크 공용.
 * 트리가 N계층이므로 **재귀로** 내려간다(2단만 훑으면 손자 CA를 못 찾는다).
 */
export function findCANode(
  agg: IssuerAggregationResponse | null,
  certificateId: string | null,
): IssuerCANode | null {
  if (!agg || !certificateId) return null;
  const walk = (nodes: IssuerCANode[]): IssuerCANode | null => {
    for (const node of nodes) {
      if (node.certificate_id === certificateId) return node;
      const found = walk(node.children);
      if (found) return found;
    }
    return null;
  };
  return walk(agg.roots);
}
