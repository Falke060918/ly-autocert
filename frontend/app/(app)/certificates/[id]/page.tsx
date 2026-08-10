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
import {
  ApiError,
  getCertificate,
  type CertificateDetailResponse,
  type CertificateLocation,
} from "@/lib/api";
import { dnCommonName, formatDate, formatDateTime, isSelfSigned } from "@/lib/format";

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
  const cn = c.common_name || dnCommonName(c.subject_dn);
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

  return (
    <div className="mx-auto max-w-[1400px] space-y-4">
      <div className="text-[13px] text-muted-foreground">
        <Link href="/certificates" className="hover:underline hover:underline-offset-3">
          인증서
        </Link>{" "}
        / <span className="text-foreground">{cn}</span>
      </div>

      <div className="flex flex-wrap items-center gap-2">
        <h1 className="font-mono text-lg font-semibold tracking-tight">{cn}</h1>
        <CertStatusBadge status={c.status} notAfter={c.not_after} />
        {isSelfSigned(c.subject_dn, c.issuer_dn) && (
          <Badge variant="secondary">자체 서명</Badge>
        )}
        <DdayText notAfter={c.not_after} />
      </div>
      <div className="flex flex-wrap gap-x-4 gap-y-1 text-[13px] text-muted-foreground">
        <span>
          등록 <b className="font-medium text-foreground">{formatDateTime(c.created_at)}</b>
        </span>
        <span>
          설치 위치 <b className="font-medium text-foreground">{data.locations.length}</b>곳
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
            {/* 자신→상위→루트 순. 저장된 상위는 클릭 시 해당 상세로 이동한다. */}
            <div className="p-2">
              {data.chain.map((item, i) => {
                const label =
                  item.common_name ||
                  dnCommonName(item.subject_dn) ||
                  item.subject_dn ||
                  "(불명)";
                const isCurrent = i === 0;
                const clickable = !isCurrent && item.stored && item.id;
                const row = (
                  <div className="flex min-w-0 items-start gap-1.5">
                    {i > 0 && (
                      <span
                        aria-hidden
                        className="shrink-0 font-mono text-xs leading-5 text-muted-foreground/50"
                        style={{ paddingLeft: (i - 1) * 14 }}
                      >
                        └
                      </span>
                    )}
                    <div className="min-w-0 flex-1">
                      <div className="flex flex-wrap items-center gap-1.5">
                        <span
                          className={`truncate font-mono text-xs ${
                            isCurrent ? "font-semibold" : "text-foreground/80"
                          }`}
                        >
                          {label}
                        </span>
                        {isCurrent && <Badge variant="secondary">현재</Badge>}
                        {item.stored &&
                          item.self_signed &&
                          item.certificate_type === "ca" && (
                            <Badge variant="outline">루트 CA</Badge>
                          )}
                        {!item.stored && (
                          <Badge variant="outline" className="text-muted-foreground">
                            미보유
                          </Badge>
                        )}
                      </div>
                      {item.stored && item.not_after ? (
                        <div className="mt-0.5 flex flex-wrap items-center gap-1.5 text-xs text-muted-foreground">
                          <span className="tabular-nums">
                            만료 {formatDate(item.not_after)}
                          </span>
                          <DdayText notAfter={item.not_after} />
                        </div>
                      ) : !item.stored ? (
                        <div className="mt-0.5 text-xs text-muted-foreground">
                          인벤토리에 없는 상위 인증서입니다.
                        </div>
                      ) : null}
                    </div>
                  </div>
                );
                return clickable ? (
                  <Link
                    key={`${item.id}-${i}`}
                    href={`/certificates/${item.id}`}
                    className="block rounded-md px-2 py-1.5 transition-colors hover:bg-secondary/60"
                  >
                    {row}
                  </Link>
                ) : (
                  <div key={`${item.subject_dn}-${i}`} className="px-2 py-1.5">
                    {row}
                  </div>
                );
              })}
            </div>
          </Card>

          <Card className="gap-0 overflow-hidden py-0">
            <div className="flex items-baseline gap-2 border-b px-4 py-3">
              <h2 className="text-sm font-semibold tracking-tight">설치 위치</h2>
            <span className="text-xs text-muted-foreground">
              {data.locations.length}곳
            </span>
          </div>
          {data.locations.length === 0 ? (
            <p className="px-4 py-6 text-center text-sm text-muted-foreground">
              확인된 설치 위치가 없습니다. Discovery 스캔으로 위치를 찾을 수 있습니다.
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
                {data.locations.map((l) => {
                  const dep = DEPLOYMENT_LABELS[l.deployment_status];
                  return (
                    <TableRow key={l.id}>
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
                      <TableCell className="font-mono text-xs tabular-nums">
                        {l.last_verified_at ? formatDateTime(l.last_verified_at) : "—"}
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
