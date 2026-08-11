"use client";

import * as React from "react";
import Link from "next/link";
import { useParams } from "next/navigation";

import { Alert, AlertDescription } from "@/components/ui/alert";
import { Badge } from "@/components/ui/badge";
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
import { FreshnessText } from "@/components/freshness";
import { useTicker } from "@/components/notification-bell";
import {
  ApiError,
  getCertificate,
  type CertificateDetailResponse,
  type CertificateLocation,
} from "@/lib/api";
import {
  getIssuerAggregation,
  ISSUED_BY_PARAM,
  type IssuerAggregationResponse,
} from "@/lib/issuers";
import { dnCommonName, formatDate, formatDateTime, isSelfSigned } from "@/lib/format";
import { cn } from "@/lib/utils";
import { findCANode } from "../issuers-view";

const TYPE_LABELS: Record<string, string> = {
  server: "서버 인증서",
  client: "클라이언트 인증서",
  ca: "CA 인증서",
  code_signing: "코드서명 인증서",
  email: "이메일 인증서",
  other: "기타",
};

const LOCATION_TYPE_LABELS: Record<CertificateLocation["location_type"], string> = {
  network_endpoint: "네트워크 엔드포인트",
  file: "파일",
  windows_store: "Windows 저장소",
  keystore: "키스토어",
  other: "기타",
};

const DEPLOYMENT_LABELS: Record<
  CertificateLocation["deployment_status"],
  { label: string; className?: string }
> = {
  deployed: { label: "배포 완료" },
  pending: { label: "대기 중" },
  failed: { label: "실패", className: "border-red-200 bg-red-50 text-red-700" },
  removed: { label: "제거됨" },
  unknown: { label: "미확인" },
};

export default function CertificateDetailPage() {
  const { id } = useParams<{ id: string }>();
  const [data, setData] = React.useState<CertificateDetailResponse | null>(null);
  const [notFound, setNotFound] = React.useState(false);
  const [error, setError] = React.useState<string | null>(null);
  // 제거된 위치는 기본으로 접는다 — 지금 어디에 깔려 있는지가 먼저다.
  const [showRemoved, setShowRemoved] = React.useState(false);
  // 체인의 CA 단계에 "이 CA가 발급한 N건" 링크를 달기 위한 집계.
  // 실패(백엔드 미배선 포함)하면 링크만 조용히 빠진다 — 상세 자체는 무관.
  const [issuerAgg, setIssuerAgg] =
    React.useState<IssuerAggregationResponse | null>(null);
  const now = useTicker(60_000);

  React.useEffect(() => {
    let cancelled = false;
    getIssuerAggregation()
      .then((res) => {
        if (!cancelled) setIssuerAgg(res);
      })
      .catch(() => {
        /* 링크 생략 */
      });
    return () => {
      cancelled = true;
    };
  }, []);

  React.useEffect(() => {
    let cancelled = false;
    getCertificate(id)
      .then((res) => {
        if (!cancelled) setData(res);
      })
      .catch((err: unknown) => {
        if (cancelled) return;
        if (err instanceof ApiError && err.status === 404) setNotFound(true);
        else setError(err instanceof Error ? err.message : "상세 조회에 실패했습니다.");
      });
    return () => {
      cancelled = true;
    };
  }, [id]);

  if (notFound) {
    return (
      <div className="mx-auto max-w-[1400px] space-y-4">
        <Alert>
          <AlertDescription>
            해당 인증서를 찾을 수 없습니다. 삭제되었거나 잘못된 주소입니다.
          </AlertDescription>
        </Alert>
        <Button asChild variant="outline">
          <Link href="/certificates">목록으로 돌아가기</Link>
        </Button>
      </div>
    );
  }

  if (error) {
    return (
      <div className="mx-auto max-w-[1400px]">
        <Alert variant="destructive">
          <AlertDescription>{error}</AlertDescription>
        </Alert>
      </div>
    );
  }

  if (!data) {
    return (
      <div className="mx-auto max-w-[1400px] space-y-4">
        <Skeleton className="h-8 w-96" />
        <Skeleton className="h-72 rounded-lg" />
      </div>
    );
  }

  const c = data.certificate;
  // cn은 클래스 유틸(@/lib/utils cn)과 겹쳐 cnLabel로 둔다.
  const cnLabel = c.common_name || dnCommonName(c.subject_dn);
  // 소유자는 owner_id(UUID)가 아니라 사람이 읽는 이름으로 표시한다.
  // 표시 이름이 없으면 계정명만 쓰고, owner_id는 있는데 계정이 사라진 경우에도
  // UUID를 노출하지 않는다(화면이 깨지지 않게 문구로 대체).
  const ownerLabel = data.owner
    ? [
        data.owner.display_name?.trim()
          ? `${data.owner.display_name.trim()} (${data.owner.username})`
          : data.owner.username,
        data.owner.is_active ? "" : " · 비활성 계정",
      ].join("")
    : c.owner_id
      ? "삭제된 사용자"
      : "미지정";
  const validityDays = Math.round(
    (new Date(c.not_after).getTime() - new Date(c.not_before).getTime()) /
      (24 * 60 * 60 * 1000),
  );
  // 제거된 위치는 지우지 않고 남겨 둔다 — "이 인증서가 어디서 언제까지 서비스됐나"는
  // 인증서 제품이 답해야 하는 질문이다. 다만 현재 상태를 가리지 않게 접어 둔다.
  const liveLocations = data.locations.filter(
    (l) => l.deployment_status !== "removed",
  );
  const removedLocations = data.locations.filter(
    (l) => l.deployment_status === "removed",
  );
  const visibleLocations = showRemoved
    ? [...liveLocations, ...removedLocations]
    : liveLocations;

  return (
    <div className="mx-auto max-w-[1400px] space-y-4">
      {/*
        제목은 인증서 자체라 남긴다 — 대신 CN을 두 번 적던 빵부스러기 줄을 없애고
        목록으로 돌아가는 링크만 같은 줄의 머리로 옮겼다.
      */}
      <div className="flex flex-wrap items-center gap-2">
        <span className="text-[13px] text-muted-foreground">
          <Link href="/certificates" className="hover:underline hover:underline-offset-3">
            인증서
          </Link>{" "}
          /
        </span>
        <h1 className="font-mono text-lg font-semibold tracking-tight">{cnLabel}</h1>
        <CertStatusBadge status={c.status} notAfter={c.not_after} />
        {isSelfSigned(c.subject_dn, c.issuer_dn) && (
          <Badge variant="secondary">자체 서명</Badge>
        )}
        {/*
          상태 뱃지는 만료 임박을 우선 표시하므로 replaced가 가려질 수 있다.
          교체 여부는 "이 알림·집계를 무시해도 되는가"를 가르는 사실이라 따로 세운다.
        */}
        {c.status === "replaced" && (
          <Badge className="border border-slate-200 bg-slate-100 text-slate-600">
            교체됨
          </Badge>
        )}
        <DdayText notAfter={c.not_after} />
      </div>

      {c.status === "replaced" && (
        <Alert>
          <AlertDescription>
            <span className="text-foreground">
              같은 엔드포인트에서 다른 인증서로 교체된 것으로 확인되었습니다
              {c.replaced_at ? ` (${formatDateTime(c.replaced_at)})` : ""}.
            </span>{" "}
            만료 알림과 대시보드 집계에서는 제외됩니다.
            {c.replaced_by_certificate_id ? (
              <>
                {" "}
                <Link
                  href={`/certificates/${c.replaced_by_certificate_id}`}
                  className="font-medium hover:underline hover:underline-offset-3"
                >
                  교체한 인증서 →
                </Link>
              </>
            ) : (
              " 교체한 인증서는 인벤토리에서 삭제되었습니다."
            )}
          </AlertDescription>
        </Alert>
      )}
      <div className="flex flex-wrap gap-x-4 gap-y-1 text-[13px] text-muted-foreground">
        <span>
          등록 <b className="font-medium text-foreground">{formatDateTime(c.created_at)}</b>
        </span>
        <span>
          설치 위치 <b className="font-medium text-foreground">{liveLocations.length}</b>곳
          {removedLocations.length > 0 && ` (이전 ${removedLocations.length}곳)`}
        </span>
        <span>
          자동 갱신{" "}
          <b className="font-medium text-foreground">
            {c.auto_renew ? "사용" : "사용 안 함"}
          </b>
        </span>
      </div>

      <div className="grid items-start gap-4 xl:grid-cols-[minmax(0,1fr)_340px]">
        <div className="grid gap-4">
          <Card className="gap-0 overflow-hidden py-0">
            <div className="border-b px-4 py-3">
              <h2 className="text-sm font-semibold tracking-tight">기본 정보</h2>
            </div>
            <dl>
              {(
                [
                  ["Subject DN", c.subject_dn, true],
                  ["Issuer DN", c.issuer_dn, true],
                  ["Serial Number", c.serial_number, true],
                  ["SHA-256 Fingerprint", c.fingerprint_sha256, true],
                  [
                    "유효기간",
                    `${formatDateTime(c.not_before)} ~ ${formatDateTime(c.not_after)} (총 ${validityDays}일)`,
                    true,
                  ],
                  ["공개키", `${c.public_key_algorithm} ${c.public_key_size} bit`, true],
                  ["서명 알고리즘", c.signature_algorithm, true],
                  ["인증서 유형", TYPE_LABELS[c.certificate_type] ?? c.certificate_type, false],
                  ["환경", c.environment ?? "미지정", false],
                  ["소유자", ownerLabel, false],
                ] as [string, string, boolean][]
              ).map(([k, v, mono]) => (
                <div
                  key={k}
                  className="grid grid-cols-[176px_1fr] gap-4 border-b px-4 py-2 last:border-b-0"
                >
                  <dt className="text-[13px] text-muted-foreground">{k}</dt>
                  <dd
                    className={
                      mono
                        ? "font-mono text-xs break-all text-foreground/80 tabular-nums"
                        : "text-[13px]"
                    }
                  >
                    {v}
                  </dd>
                </div>
              ))}
            </dl>
          </Card>

          <Card className="gap-0 overflow-hidden py-0">
            <div className="flex items-baseline gap-2 border-b px-4 py-3">
              <h2 className="text-sm font-semibold tracking-tight">
                SAN (Subject Alternative Name)
              </h2>
              <span className="text-xs text-muted-foreground">{c.sans.length}개</span>
            </div>
            {c.sans.length === 0 ? (
              <p className="px-4 py-6 text-center text-sm text-muted-foreground">
                등록된 SAN이 없습니다.
              </p>
            ) : (
              <div className="flex flex-wrap gap-1.5 p-4">
                {c.sans.map((san) => (
                  <span
                    key={san}
                    className="rounded-md bg-secondary px-2.5 py-0.5 font-mono text-xs"
                  >
                    {san}
                  </span>
                ))}
              </div>
            )}
          </Card>
        </div>

        <div className="grid gap-4 xl:order-none">
          <Card className="gap-0 overflow-hidden py-0">
            <div className="flex items-baseline gap-2 border-b px-4 py-3">
              <h2 className="text-sm font-semibold tracking-tight">인증서 체인</h2>
              <span className="text-xs text-muted-foreground">
                {data.chain.length}단계
              </span>
            </div>
            {/*
              자신→상위→루트 순의 수직 레일 — 단계마다 상태·만료를 병기하고,
              CA 단계에는 그 CA가 발급한 인증서 목록으로 가는 링크를 단다.
              행 전체를 링크로 감싸지 않는다(발급 링크와 중첩 <a> 방지) —
              이름이 상세 링크, 발급 건수가 목록 링크다.
            */}
            <div className="p-4">
              {data.chain.map((item, i) => {
                const label =
                  item.common_name ||
                  dnCommonName(item.subject_dn) ||
                  item.subject_dn ||
                  "(불명)";
                const isCurrent = i === 0;
                const isLast = i === data.chain.length - 1;
                // 집계 트리에 있으면 CA다 — 발급 건수 링크의 근거(N)도 여기서 온다.
                const caNode = item.stored ? findCANode(issuerAgg, item.id) : null;
                return (
                  <div key={`${item.id ?? item.subject_dn}-${i}`} className="flex gap-3">
                    <div className="flex w-3 shrink-0 flex-col items-center">
                      <span
                        aria-hidden
                        className={cn(
                          "mt-1.5 size-2 shrink-0 rounded-full",
                          isCurrent
                            ? "bg-primary"
                            : item.stored
                              ? "bg-muted-foreground/50"
                              : "border border-dashed border-muted-foreground/60",
                        )}
                      />
                      {!isLast && <span aria-hidden className="mt-1 w-px flex-1 bg-border" />}
                    </div>
                    <div className={cn("min-w-0 flex-1", !isLast && "pb-4")}>
                      <div className="flex flex-wrap items-center gap-1.5">
                        {!isCurrent && item.stored && item.id ? (
                          <Link
                            href={`/certificates/${item.id}`}
                            className="truncate font-mono text-xs text-foreground/80 hover:underline hover:underline-offset-3"
                          >
                            {label}
                          </Link>
                        ) : (
                          <span
                            className={cn(
                              "truncate font-mono text-xs",
                              isCurrent
                                ? "font-semibold"
                                : "text-muted-foreground",
                            )}
                          >
                            {label}
                          </span>
                        )}
                        {isCurrent && <Badge variant="secondary">현재</Badge>}
                        {item.stored &&
                          item.certificate_type === "ca" &&
                          (item.self_signed ? (
                            <Badge variant="outline">루트 CA</Badge>
                          ) : (
                            <Badge variant="outline">중간 CA</Badge>
                          ))}
                        {!item.stored && (
                          <Badge variant="outline" className="text-muted-foreground">
                            미보유
                          </Badge>
                        )}
                      </div>
                      {item.stored && item.not_after ? (
                        <div className="mt-1 flex flex-wrap items-center gap-1.5 text-xs text-muted-foreground">
                          {item.status && !isCurrent && (
                            <CertStatusBadge
                              status={item.status}
                              notAfter={item.not_after}
                            />
                          )}
                          <span className="tabular-nums">
                            만료 {formatDate(item.not_after)}
                          </span>
                          <DdayText notAfter={item.not_after} />
                        </div>
                      ) : !item.stored ? (
                        <div className="mt-1 text-xs text-muted-foreground">
                          인벤토리에 없는 상위 인증서입니다.
                        </div>
                      ) : null}
                      {caNode && item.id && (
                        <div className="mt-1">
                          <Link
                            href={`/certificates?${ISSUED_BY_PARAM}=${encodeURIComponent(item.id)}`}
                            className="text-xs font-medium text-muted-foreground hover:text-foreground hover:underline hover:underline-offset-3"
                          >
                            이 CA가 발급한 {caNode.leaf_count.toLocaleString()}건 →
                          </Link>
                        </div>
                      )}
                    </div>
                  </div>
                );
              })}
            </div>
          </Card>

          <Card className="gap-0 overflow-hidden py-0">
            <div className="flex flex-wrap items-baseline gap-2 border-b px-4 py-3">
              <h2 className="text-sm font-semibold tracking-tight">설치 위치</h2>
            <span className="text-xs text-muted-foreground">
              {liveLocations.length}곳
            </span>
            {removedLocations.length > 0 && (
              <button
                type="button"
                className="ml-auto text-xs text-muted-foreground hover:text-foreground hover:underline hover:underline-offset-3"
                onClick={() => setShowRemoved((v) => !v)}
              >
                {showRemoved
                  ? "이전 위치 숨기기"
                  : `이전 위치 보기 (${removedLocations.length})`}
              </button>
            )}
          </div>
          {visibleLocations.length === 0 ? (
            <p className="px-4 py-6 text-center text-sm text-muted-foreground">
              {removedLocations.length > 0
                ? "현재 서비스 중인 위치가 없습니다. 이전 위치만 남아 있습니다."
                : "확인된 설치 위치가 없습니다. Discovery 스캔으로 위치를 찾을 수 있습니다."}
            </p>
          ) : (
            <Table>
              <TableHeader>
                <TableRow>
                  <TableHead>대상</TableHead>
                  <TableHead>유형</TableHead>
                  <TableHead className="text-right">포트</TableHead>
                  <TableHead>TLS</TableHead>
                  <TableHead>최근 확인</TableHead>
                  <TableHead>배포 상태</TableHead>
                </TableRow>
              </TableHeader>
              <TableBody>
                {visibleLocations.map((l) => {
                  const dep = DEPLOYMENT_LABELS[l.deployment_status];
                  const removed = l.deployment_status === "removed";
                  return (
                    <TableRow
                      key={l.id}
                      // 제거된 위치는 이력이지 현황이 아니다 — 회색으로 낮춘다.
                      className={removed ? "text-muted-foreground/60" : undefined}
                    >
                      <TableCell>
                        <div className="text-[13px] font-medium">{l.target.name}</div>
                        <div className="font-mono text-xs text-muted-foreground/70">
                          {l.target.ip_address ?? l.target.hostname ?? "—"}
                        </div>
                      </TableCell>
                      <TableCell className="text-[13px]">
                        {LOCATION_TYPE_LABELS[l.location_type]}
                        {(l.file_path || l.store_name) && (
                          <div className="font-mono text-xs text-muted-foreground/70">
                            {l.file_path ?? l.store_name}
                          </div>
                        )}
                      </TableCell>
                      <TableCell className="text-right font-mono text-xs tabular-nums">
                        {l.port ?? "—"}
                      </TableCell>
                      <TableCell className="font-mono text-xs">
                        {l.tls_version ?? "—"}
                      </TableCell>
                      <TableCell className="whitespace-nowrap">
                        {/*
                          제거된 위치의 last_verified_at은 "언제까지 실제로 서비스
                          중이었나"라 신선도 경고 대상이 아니다 — 절대 시각만 보인다.
                        */}
                        {removed ? (
                          <span className="font-mono text-xs tabular-nums">
                            {l.last_verified_at
                              ? formatDateTime(l.last_verified_at)
                              : "—"}
                          </span>
                        ) : (
                          <>
                            <FreshnessText iso={l.last_verified_at} now={now} />
                            {l.last_verified_at && (
                              <div className="font-mono text-[11px] text-muted-foreground/60 tabular-nums">
                                {formatDateTime(l.last_verified_at)}
                              </div>
                            )}
                          </>
                        )}
                      </TableCell>
                      <TableCell>
                        <Badge variant="outline" className={dep.className}>
                          {dep.label}
                        </Badge>
                      </TableCell>
                    </TableRow>
                  );
                })}
              </TableBody>
            </Table>
          )}
          </Card>
        </div>
      </div>
    </div>
  );
}
