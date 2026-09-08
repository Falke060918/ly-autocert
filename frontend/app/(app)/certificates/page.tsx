"use client";

import * as React from "react";
import { Suspense } from "react";
import Link from "next/link";
import { usePathname, useRouter, useSearchParams } from "next/navigation";
import { ArrowDown, ArrowUp, Download, Search, Upload, X } from "lucide-react";

import { Alert, AlertDescription, AlertTitle } from "@/components/ui/alert";
import { Button } from "@/components/ui/button";
import { Card } from "@/components/ui/card";
import { Input } from "@/components/ui/input";
import { Skeleton } from "@/components/ui/skeleton";
import {
  Select,
  SelectContent,
  SelectItem,
  SelectTrigger,
  SelectValue,
} from "@/components/ui/select";
import {
  Table,
  TableBody,
  TableCell,
  TableHead,
  TableHeader,
  TableRow,
} from "@/components/ui/table";
import { Label } from "@/components/ui/label";
import { Switch } from "@/components/ui/switch";
import { Tabs, TabsList, TabsTrigger } from "@/components/ui/tabs";
import { CertStatusBadge, DdayText } from "@/components/cert-badges";
import { IssuanceWarnings } from "@/components/issuance-warnings";
import { RiskSummaryBadge } from "@/components/risk-badges";
import { FreshnessText } from "@/components/freshness";
import { useTicker } from "@/components/notification-bell";
import { canExportInventory, canManageInventory, useAuth } from "@/lib/auth";
import {
  ApiError,
  exportCertificatesCsv,
  importCertificateFile,
  listCertificates,
  type CertificateListResponse,
  type ImportResult,
  type TlsVersion,
} from "@/lib/api";
import {
  applyIssuerFilter,
  getIssuerAggregation,
  ISSUED_BY_PARAM,
  ISSUER_ORG_PARAM,
  ISSUER_ORG_UNKNOWN,
  type IssuerAggregationResponse,
} from "@/lib/issuers";
import {
  applyRiskFilter,
  getCertificateRisks,
  isRiskCode,
  isRiskSeverity,
  RISK_CODE_PARAM,
  RISK_PARAM,
  RISK_SEVERITY_LABELS,
  RISK_SEVERITY_ORDER,
  type CertificateRisksResponse,
} from "@/lib/risks";
import {
  applyTlsVersionFilter,
  formatTlsVersions,
  TLS_VERSION_PARAM,
  TLS_VERSIONS,
  tlsVersionsFromSearch,
} from "@/lib/tls";
import { dnCommonName, dnOrganization, formatDate, isSelfSigned } from "@/lib/format";
import { cn } from "@/lib/utils";
import { findCANode, IssuersView } from "./issuers-view";
import { ServersView } from "./servers-view";

const TYPE_LABELS: Record<string, string> = {
  server: "서버",
  client: "클라이언트",
  ca: "CA",
  code_signing: "코드서명",
  email: "이메일",
  other: "기타",
};

const STATUS_OPTIONS = [
  { value: "active", label: "정상 (active)" },
  { value: "expired", label: "만료 (expired)" },
  { value: "revoked", label: "폐기 (revoked)" },
  { value: "replaced", label: "교체됨 (replaced)" },
  { value: "unknown", label: "미확인 (unknown)" },
];

const EXPIRY_OPTIONS = [
  { value: "expired", label: "이미 만료" },
  { value: "7", label: "7일 이내" },
  { value: "30", label: "30일 이내" },
  { value: "60", label: "60일 이내" },
  { value: "90", label: "90일 이내" },
];

/** 정렬 허용 필드 (백엔드 화이트리스트와 일치하는 부분집합만 헤더에 노출). */
const SORTABLE: Record<string, string> = {
  common_name: "Common Name",
  not_after: "만료일",
  created_at: "등록일",
};

/**
 * URL의 issuer_org 값을 정규화한다 — "조직 미상"은 __none__ 센티널
 * (lib/issuers.ts ISSUER_ORG_UNKNOWN)이 정본 표기이고, 링크 생성처가 빈 값
 * (issuer_org=)을 실어도 동일 취급한다(Select 트리거 공백·빈 항목 방지).
 * 반환값이 null이면 필터 없음이다.
 */
function issuerOrgFromSearch(searchParams: {
  get(name: string): string | null;
}): string | null {
  const raw = searchParams.get(ISSUER_ORG_PARAM);
  return raw === "" ? ISSUER_ORG_UNKNOWN : raw;
}

export default function CertificatesPage() {
  return (
    <Suspense>
      <CertificatesBody />
    </Suspense>
  );
}

function CertificatesBody() {
  const router = useRouter();
  const pathname = usePathname();
  const searchParams = useSearchParams();
  const { user } = useAuth();

  const [data, setData] = React.useState<CertificateListResponse | null>(null);
  const [error, setError] = React.useState<string | null>(null);
  const [uploadOpen, setUploadOpen] = React.useState(false);
  const [reloadKey, setReloadKey] = React.useState(0);
  const [exporting, setExporting] = React.useState(false);
  // 발급 기관 집계 — 필터 Select 옵션·발급 CA 칩 라벨·발급 기관 뷰가 공유한다.
  // 백엔드 병행 구현 중이라 실패할 수 있다 — 실패하면 필터를 숨긴다(목록은 무관).
  const [issuerAgg, setIssuerAgg] =
    React.useState<IssuerAggregationResponse | null>(null);
  const [issuerAggError, setIssuerAggError] = React.useState(false);
  // 위험 판정 집계 — 필터 Select의 건수 라벨용. 판정은 저장값이 아니라 조회 시
  // 계산되므로 목록을 다시 받을 때마다 같이 받는다(캐시하지 않는다).
  // 실패하면 필터만 숨긴다 — 목록의 위험 배지는 목록 응답에 실려 오므로 무관하다.
  const [riskAgg, setRiskAgg] =
    React.useState<CertificateRisksResponse | null>(null);

  // 신선도 표기는 상대 시각이라 주기적으로 다시 그린다(렌더 중 Date.now 금지).
  const now = useTicker(60_000);

  React.useEffect(() => {
    let cancelled = false;
    getIssuerAggregation()
      .then((res) => {
        if (!cancelled) {
          setIssuerAgg(res);
          setIssuerAggError(false);
        }
      })
      .catch(() => {
        if (!cancelled) setIssuerAggError(true);
      });
    getCertificateRisks()
      .then((res) => {
        if (!cancelled) setRiskAgg(res);
      })
      .catch(() => {
        /* 위험 필터만 숨긴다 */
      });
    return () => {
      cancelled = true;
    };
  }, [reloadKey]);

  // URL 쿼리 → API 쿼리 (허용 파라미터만 통과시킨다).
  //
  // 교체된 인증서는 **프론트가 명시적으로** 뺀다 — 서버 목록 API의 기본값은
  // 그대로이고(CSV 내보내기가 같은 필터를 공유한다), 화면 토글로 되돌릴 수 있다.
  // status=replaced를 직접 고른 경우에는 제외하지 않는다(빈 목록 방지).
  const apiQuery = React.useMemo(() => {
    const out = new URLSearchParams();
    for (const key of [
      "q", "status", "type", "expired", "expires_within_days",
      "self_signed", "unowned", "sort", "order", "page", "page_size",
    ]) {
      const v = searchParams.get(key);
      if (v) out.set(key, v);
    }
    const includeReplaced = searchParams.get("include_replaced") === "true";
    const statusFilter = searchParams.get("status") ?? "";
    if (!includeReplaced && !statusFilter.split(",").includes("replaced")) {
      out.set("exclude_status", "replaced");
    }
    const org = issuerOrgFromSearch(searchParams);
    applyIssuerFilter(out, {
      issuerOrg: org === ISSUER_ORG_UNKNOWN ? "" : org,
      issuedBy: searchParams.get(ISSUED_BY_PARAM),
    });
    // 위험 필터는 값을 검증하고 넘긴다 — 손으로 고친 주소로 400을 받으면
    // 목록 전체가 오류 알럿으로 바뀐다.
    const sev = searchParams.get(RISK_PARAM);
    const code = searchParams.get(RISK_CODE_PARAM);
    applyRiskFilter(out, {
      severity: isRiskSeverity(sev) ? sev : null,
      code: isRiskCode(code) ? code : null,
    });
    // 관측 TLS 버전도 같은 이유로 카탈로그 밖 값을 버리고 넘긴다 — 서버는 네 값만
    // 받고 그 밖은 400 invalid_tls_version이다.
    applyTlsVersionFilter(out, tlsVersionsFromSearch(searchParams));
    return out;
  }, [searchParams]);

  React.useEffect(() => {
    let cancelled = false;
    listCertificates(apiQuery)
      .then((res) => {
        if (!cancelled) {
          setData(res);
          setError(null);
        }
      })
      .catch((err: unknown) => {
        if (!cancelled)
          setError(err instanceof Error ? err.message : "목록 조회에 실패했습니다.");
      });
    return () => {
      cancelled = true;
    };
  }, [apiQuery, reloadKey]);

  /** URL 쿼리를 갱신한다. 필터 변경은 page를 1로 되돌린다. */
  const setQuery = React.useCallback(
    (updates: Record<string, string | null>, resetPage = true) => {
      const next = new URLSearchParams(searchParams.toString());
      for (const [k, v] of Object.entries(updates)) {
        if (v == null || v === "") next.delete(k);
        else next.set(k, v);
      }
      if (resetPage) next.delete("page");
      const qs = next.toString();
      router.replace(qs ? `${pathname}?${qs}` : pathname, { scroll: false });
    },
    [router, pathname, searchParams],
  );

  const expiryValue = searchParams.get("expired") === "true"
    ? "expired"
    : searchParams.get("expires_within_days") ?? "all";

  // issuer_org는 빈 값도 필터(조직 미상)라 정규화 값의 존재로 따로 판정한다.
  const issuerOrgValue = issuerOrgFromSearch(searchParams);
  const hasFilter =
    issuerOrgValue != null ||
    [
      "q", "status", "type", "expired", "expires_within_days", "self_signed", "unowned",
      "include_replaced", ISSUED_BY_PARAM, RISK_PARAM, RISK_CODE_PARAM,
      TLS_VERSION_PARAM,
    ].some((k) => searchParams.get(k));

  const includeReplaced = searchParams.get("include_replaced") === "true";

  const sort = searchParams.get("sort") ?? "not_after";
  const order = searchParams.get("order") ?? "asc";
  const canManage = canManageInventory(user);
  const canExport = canExportInventory(user);

  const viewParam = searchParams.get("view");
  const view =
    viewParam === "issuers" || viewParam === "servers" ? viewParam : "list";
  const issuedById = searchParams.get(ISSUED_BY_PARAM);
  const riskSeverity = searchParams.get(RISK_PARAM);
  const riskCode = searchParams.get(RISK_CODE_PARAM);
  // 관측 TLS 버전 필터 — 다중값(OR)을 URL에 그대로 두므로 Select 값도 배열에서 만든다.
  const tlsVersions = tlsVersionsFromSearch(searchParams);
  // 발급 CA 칩 라벨 — 집계 트리에서 이름을 찾고, 못 찾으면 일반 문구로 남긴다.
  const issuedByNode = findCANode(issuerAgg, issuedById);

  /** 현재 필터 조건 전체를 CSV로 내려받는다 (서버가 certificate.export 감사 기록). */
  const exportCsv = React.useCallback(async () => {
    setExporting(true);
    try {
      const params = new URLSearchParams(apiQuery);
      params.delete("page");
      params.delete("page_size");
      const blob = await exportCertificatesCsv(params);
      const url = URL.createObjectURL(blob);
      const a = document.createElement("a");
      a.href = url;
      a.download = `certificates-${new Date().toISOString().slice(0, 10)}.csv`;
      a.click();
      URL.revokeObjectURL(url);
      setError(null);
    } catch (err) {
      setError(err instanceof Error ? err.message : "CSV 내보내기에 실패했습니다.");
    } finally {
      setExporting(false);
    }
  }, [apiQuery]);

  return (
    <div className="mx-auto max-w-[1400px] space-y-4">
      {/* 제목은 화면에서 걷어냈지만 문서 구조(h1)는 스크린리더용으로 남긴다. */}
      <h1 className="sr-only">인증서</h1>

      {/*
        같은 인벤토리를 세 관점으로 본다 — 행 단위 목록 / 호스트(서버) /
        CA 계층(발급 기관). 제목 블록을 없앤 대신 이 줄이 화면 머리 역할을 한다:
        건수와 화면 액션을 탭과 같은 줄에 얹어 세로를 한 줄도 더 쓰지 않는다.
      */}
      <div className="flex flex-wrap items-center gap-x-3 gap-y-2">
        <div className="flex items-center gap-2">
          <span className="text-[13px] text-muted-foreground">보기 방식:</span>
          <Tabs
            value={view}
            onValueChange={(v) =>
              setQuery({ view: v === "list" ? null : v }, false)
            }
          >
            <TabsList>
              <TabsTrigger value="list">도메인별</TabsTrigger>
              <TabsTrigger value="servers">서버별</TabsTrigger>
              <TabsTrigger value="issuers">기관별</TabsTrigger>
            </TabsList>
          </Tabs>
        </div>
        {view === "list" && data && (
          <span className="text-[13px] text-muted-foreground">
            조건에 맞는 인증서 {data.total.toLocaleString()}건
          </span>
        )}
        <div className="ml-auto flex items-center gap-2">
          {canExport && (
            <Button
              variant="outline"
              size="sm"
              disabled={exporting || !data || data.total === 0}
              onClick={() => void exportCsv()}
            >
              <Download data-icon="inline-start" />
              {exporting ? "내보내는 중…" : "CSV 내보내기"}
            </Button>
          )}
          {canManage && (
            <Button size="sm" onClick={() => setUploadOpen((v) => !v)}>
              <Upload data-icon="inline-start" />
              인증서 등록
            </Button>
          )}
        </div>
      </div>

      {canManage && uploadOpen && (
        <UploadPanel
          onImported={() => setReloadKey((k) => k + 1)}
          onClose={() => setUploadOpen(false)}
        />
      )}

      {view === "issuers" ? (
        <IssuersView agg={issuerAgg} error={issuerAggError} />
      ) : view === "servers" ? (
        <ServersView setQuery={setQuery} />
      ) : (
        <>
      <div className="flex flex-wrap items-center gap-2">
        <SearchField
          initial={searchParams.get("q") ?? ""}
          onSearch={(q) => setQuery({ q })}
        />
        <Select
          value={searchParams.get("status") ?? "all"}
          onValueChange={(v) => setQuery({ status: v === "all" ? null : v })}
        >
          <SelectTrigger className="w-[150px]">
            <SelectValue />
          </SelectTrigger>
          <SelectContent>
            <SelectItem value="all">상태: 전체</SelectItem>
            {STATUS_OPTIONS.map((o) => (
              <SelectItem key={o.value} value={o.value}>{o.label}</SelectItem>
            ))}
          </SelectContent>
        </Select>
        <Select
          value={searchParams.get("type") ?? "all"}
          onValueChange={(v) => setQuery({ type: v === "all" ? null : v })}
        >
          <SelectTrigger className="w-[140px]">
            <SelectValue />
          </SelectTrigger>
          <SelectContent>
            <SelectItem value="all">유형: 전체</SelectItem>
            {Object.entries(TYPE_LABELS).map(([v, label]) => (
              <SelectItem key={v} value={v}>{label}</SelectItem>
            ))}
          </SelectContent>
        </Select>
        <Select
          value={expiryValue}
          onValueChange={(v) =>
            setQuery(
              v === "all"
                ? { expired: null, expires_within_days: null }
                : v === "expired"
                  ? { expired: "true", expires_within_days: null }
                  : { expired: null, expires_within_days: v },
            )
          }
        >
          <SelectTrigger className="w-[140px]">
            <SelectValue />
          </SelectTrigger>
          <SelectContent>
            <SelectItem value="all">만료: 전체</SelectItem>
            {EXPIRY_OPTIONS.map((o) => (
              <SelectItem key={o.value} value={o.value}>{o.label}</SelectItem>
            ))}
          </SelectContent>
        </Select>
        {/* 발급기관 필터 — 집계 API 실패(백엔드 미배선 포함) 시에는 숨긴다. */}
        {issuerAgg && (
          <Select
            value={issuerOrgValue ?? "all"}
            onValueChange={(v) =>
              setQuery({ [ISSUER_ORG_PARAM]: v === "all" ? null : v })
            }
          >
            <SelectTrigger className="w-[190px]">
              <SelectValue />
            </SelectTrigger>
            <SelectContent>
              <SelectItem value="all">발급기관: 전체</SelectItem>
              {issuerAgg.organizations.map((o) =>
                o.organization === null ? (
                  <SelectItem key={ISSUER_ORG_UNKNOWN} value={ISSUER_ORG_UNKNOWN}>
                    조직 미상 ({o.count.toLocaleString()})
                  </SelectItem>
                ) : (
                  <SelectItem key={o.organization} value={o.organization}>
                    {o.organization} ({o.count.toLocaleString()})
                  </SelectItem>
                ),
              )}
              {/* URL로 들어온 값이 집계에 없어도 Select가 비지 않게 살린다. */}
              {issuerOrgValue != null &&
                !issuerAgg.organizations.some((o) =>
                  issuerOrgValue === ISSUER_ORG_UNKNOWN
                    ? o.organization === null
                    : o.organization === issuerOrgValue,
                ) && (
                  <SelectItem value={issuerOrgValue}>
                    {issuerOrgValue === ISSUER_ORG_UNKNOWN
                      ? "조직 미상"
                      : issuerOrgValue}
                  </SelectItem>
                )}
            </SelectContent>
          </Select>
        )}
        {/*
          위험 필터 2종 — 심각도(최고 심각도 기준, 겹침 없이 분할)와 판정 항목
          (한 인증서가 여러 항목에 걸린다). 건수는 집계 API가 준 값이고 교체된
          인증서를 뺀 수라 목록의 기본값(교체 제외)과 같은 모수다 — "교체된 인증서
          포함"을 켜면 목록 건수가 라벨보다 커질 수 있다.
        */}
        {riskAgg && (
          <Select
            value={isRiskSeverity(riskSeverity) ? riskSeverity : "all"}
            onValueChange={(v) =>
              setQuery({ [RISK_PARAM]: v === "all" ? null : v })
            }
          >
            <SelectTrigger className="w-[160px]">
              <SelectValue />
            </SelectTrigger>
            <SelectContent>
              <SelectItem value="all">위험: 전체</SelectItem>
              {RISK_SEVERITY_ORDER.map((sev) => {
                const count =
                  riskAgg.severities.find((s) => s.severity === sev)?.count ?? 0;
                return (
                  <SelectItem key={sev} value={sev}>
                    {RISK_SEVERITY_LABELS[sev]} ({count.toLocaleString()})
                  </SelectItem>
                );
              })}
            </SelectContent>
          </Select>
        )}
        {riskAgg && (
          <Select
            value={isRiskCode(riskCode) ? riskCode : "all"}
            onValueChange={(v) =>
              setQuery({ [RISK_CODE_PARAM]: v === "all" ? null : v })
            }
          >
            <SelectTrigger className="w-[200px]">
              <SelectValue />
            </SelectTrigger>
            <SelectContent>
              <SelectItem value="all">판정 항목: 전체</SelectItem>
              {riskAgg.findings.map((f) => (
                <SelectItem key={f.code} value={f.code}>
                  {f.title} ({f.count.toLocaleString()})
                </SelectItem>
              ))}
            </SelectContent>
          </Select>
        )}
        {/*
          관측 TLS 버전 필터 — 옵션이 고정 카탈로그(lib/tls.ts)라 어떤 집계 API에도
          기대지 않는다. 위험·발급기관 필터는 집계가 실패하면 Select째로 사라져
          URL에 남은 필터를 풀 수단이 "초기화"뿐이 되는데, 이 필터에는 그 경로가 없다.
        */}
        <Select
          value={
            tlsVersions.length === 0 ? "all" : tlsVersions.join(",")
          }
          onValueChange={(v) =>
            setQuery({ [TLS_VERSION_PARAM]: v === "all" ? null : v })
          }
        >
          <SelectTrigger className="w-[160px]">
            <SelectValue />
          </SelectTrigger>
          <SelectContent>
            <SelectItem value="all">TLS: 전체</SelectItem>
            {TLS_VERSIONS.map((v) => (
              <SelectItem key={v} value={v}>{v}</SelectItem>
            ))}
            {/*
              서버가 다중값을 OR로 받으므로 주소에 여러 개가 실려 올 수 있다 —
              한 항목으로 살려 두지 않으면 트리거가 비어 필터를 푸는 법이 사라진다.
            */}
            {tlsVersions.length > 1 && (
              <SelectItem value={tlsVersions.join(",")}>
                {formatTlsVersions(tlsVersions)}
              </SelectItem>
            )}
          </SelectContent>
        </Select>
        {/*
          교체된 인증서는 기본으로 숨긴다 — 갱신이 끝나 죽은 인증서가 목록·만료
          집계에 섞이면 이미 처리한 건에 대한 알림처럼 신뢰를 깎는다. 다만 이력
          추적은 필요하므로 토글로 되돌릴 수 있게 두고 상태는 URL에 남긴다.
        */}
        <div className="flex items-center gap-2">
          <Switch
            id="include-replaced"
            checked={includeReplaced}
            onCheckedChange={(v) =>
              setQuery({ include_replaced: v ? "true" : null })
            }
          />
          <Label htmlFor="include-replaced" className="text-[13px] font-normal">
            교체된 인증서 포함
          </Label>
        </div>
        <div className="ml-auto flex items-center gap-2">
          {/* 발급 기관 뷰에서 CA를 골라 넘어온 필터 — Select가 없어 칩으로 세운다. */}
          {issuedById && (
            <FilterChip
              label={
                issuedByNode
                  ? `발급 CA: ${
                      issuedByNode.common_name ??
                      issuedByNode.organization ??
                      dnCommonName(issuedByNode.subject_dn)
                    }`
                  : "발급 CA 필터"
              }
              onRemove={() => setQuery({ [ISSUED_BY_PARAM]: null })}
            />
          )}
          {searchParams.get("self_signed") === "true" && (
            <FilterChip label="자체 서명" onRemove={() => setQuery({ self_signed: null })} />
          )}
          {searchParams.get("unowned") === "true" && (
            <FilterChip label="소유자 미지정" onRemove={() => setQuery({ unowned: null })} />
          )}
          {hasFilter && (
            <Button variant="outline" size="sm" onClick={() => router.replace(pathname)}>
              초기화
            </Button>
          )}
        </div>
      </div>

      {error && (
        <Alert variant="destructive">
          <AlertDescription>{error}</AlertDescription>
        </Alert>
      )}

      {!data && !error && <Skeleton className="h-96 rounded-lg" />}

      {data && (
        <Card className="gap-0 overflow-hidden py-0">
          {data.items.length === 0 ? (
            <div className="px-4 py-12 text-center text-sm text-muted-foreground">
              {hasFilter ? (
                <>조건에 맞는 인증서가 없습니다. 필터를 초기화해 보십시오.</>
              ) : (
                <>
                  {/*
                    교체된 인증서는 기본으로 빠져 있다 — 인벤토리에 그것만 남은
                    설치에서 "등록된 인증서가 없다"고만 말하면 거짓말이 된다.
                  */}
                  아직 등록된 인증서가 없습니다. 교체된 인증서는 기본으로 숨겨져
                  있으니 위의 &ldquo;교체된 인증서 포함&rdquo;도 확인해 보십시오.{" "}
                  {canManage
                    ? "인증서 등록 또는 Discovery 스캔으로 시작하십시오."
                    : "관리자에게 등록을 요청하십시오."}
                </>
              )}
            </div>
          ) : (
            <Table>
              <TableHeader>
                <TableRow>
                  <SortHead field="common_name" sort={sort} order={order} setQuery={setQuery} />
                  <TableHead className="text-right">SAN</TableHead>
                  <TableHead>상태</TableHead>
                  {/*
                    위험은 조회 시 계산된 판정이라 저장 컬럼이 없다 — 서버 정렬
                    화이트리스트에도 없어 정렬 대신 필터로만 좁힌다.
                  */}
                  <TableHead>위험</TableHead>
                  <SortHead field="not_after" sort={sort} order={order} setQuery={setQuery} />
                  <TableHead>키</TableHead>
                  <TableHead>서명 알고리즘</TableHead>
                  <TableHead>발급자</TableHead>
                  <TableHead>유형</TableHead>
                  {/* 정렬 불가 — 위치 집계 파생값이라 서버 정렬 화이트리스트에 없다. */}
                  <TableHead>최근 확인</TableHead>
                  {/* 최근 확인과 같은 위치 집계 파생값이라 역시 정렬 대상이 아니다. */}
                  <TableHead>TLS</TableHead>
                  <SortHead field="created_at" sort={sort} order={order} setQuery={setQuery} />
                </TableRow>
              </TableHeader>
              <TableBody>
                {data.items.map((c) => (
                  <TableRow key={c.id}>
                    <TableCell>
                      <Link
                        href={`/certificates/${c.id}`}
                        className="font-mono text-xs font-medium hover:underline hover:underline-offset-3"
                      >
                        {c.common_name || dnCommonName(c.subject_dn)}
                      </Link>
                    </TableCell>
                    <TableCell className="text-right font-mono text-xs tabular-nums">
                      {c.sans.length}
                    </TableCell>
                    <TableCell>
                      <CertStatusBadge status={c.status} notAfter={c.not_after} />
                      <IssuanceWarnings warnings={c.issuance_warnings} compact />
                    </TableCell>
                    <TableCell>
                      <RiskSummaryBadge risk={c.risk} />
                    </TableCell>
                    <TableCell className="whitespace-nowrap">
                      <span className="font-mono text-xs tabular-nums">
                        {formatDate(c.not_after)}
                      </span>{" "}
                      <DdayText notAfter={c.not_after} />
                    </TableCell>
                    <TableCell className="font-mono text-xs">
                      {c.public_key_algorithm} {c.public_key_size}
                    </TableCell>
                    <TableCell className="font-mono text-xs">
                      {c.signature_algorithm}
                    </TableCell>
                    <TableCell className="max-w-56 truncate text-[13px]">
                      {/*
                        조직(O=) 우선 — 같은 CA 계열은 CN(발급 서버별)이 아니라
                        조직으로 묶여 보이는 게 필터·집계 축과 일치한다.
                        issuer_organization 미배선·백필 전에는 DN 파싱으로 폴백.
                      */}
                      {isSelfSigned(c.subject_dn, c.issuer_dn)
                        ? "자체 서명"
                        : (c.issuer_organization ??
                          dnOrganization(c.issuer_dn) ??
                          dnCommonName(c.issuer_dn))}
                    </TableCell>
                    <TableCell className="text-[13px]">
                      {TYPE_LABELS[c.certificate_type] ?? c.certificate_type}
                    </TableCell>
                    <TableCell className="whitespace-nowrap">
                      <FreshnessText iso={c.last_seen_at} now={now} />
                    </TableCell>
                    <TableCell className="whitespace-nowrap">
                      <TlsVersionsCell
                        versions={c.tls_versions}
                        scanned={c.last_seen_at != null}
                      />
                    </TableCell>
                    <TableCell className="font-mono text-xs tabular-nums">
                      {formatDate(c.created_at)}
                    </TableCell>
                  </TableRow>
                ))}
              </TableBody>
            </Table>
          )}
          <Pager data={data} setQuery={setQuery} />
        </Card>
      )}
        </>
      )}
    </div>
  );
}

/** 검색 입력 — 400ms 디바운스 + Enter 즉시 적용. */
function SearchField({
  initial,
  onSearch,
}: {
  initial: string;
  onSearch: (q: string) => void;
}) {
  const [value, setValue] = React.useState(initial);
  const timer = React.useRef<ReturnType<typeof setTimeout> | null>(null);

  function schedule(v: string) {
    setValue(v);
    if (timer.current) clearTimeout(timer.current);
    timer.current = setTimeout(() => onSearch(v.trim()), 400);
  }

  return (
    <div className="relative w-64">
      <Search className="pointer-events-none absolute top-2 left-2.5 size-3.5 text-muted-foreground/70" />
      <Input
        className="pl-8"
        placeholder="CN / SAN / Serial / Fingerprint 검색"
        value={value}
        onChange={(e) => schedule(e.target.value)}
        onKeyDown={(e) => {
          if (e.key === "Enter") {
            if (timer.current) clearTimeout(timer.current);
            onSearch(value.trim());
          }
        }}
      />
    </div>
  );
}

/**
 * 관측 TLS 버전 셀 — removed 아닌 설치 위치들이 실제로 협상한 버전의 합집합이다.
 * 서버가 함께 열어 둔 다른 버전까지는 아니다(그쪽은 설치 위치 상세의 지원 버전 목록).
 *
 * 빈 배열은 "TLS를 안 쓴다"가 아니라 **관측이 없다**는 뜻이라 '확인 안 됨'으로 적는다 —
 * 모르는 것을 안전으로 세면 레거시 TLS가 남은 서버를 정리 끝난 것으로 보게 된다.
 * 빈 배열만으로는 이유를 못 가리므로 최근 확인 여부(last_seen_at)로 설명을 나눈다.
 */
function TlsVersionsCell({
  versions,
  scanned,
}: {
  versions: TlsVersion[];
  scanned: boolean;
}) {
  if (versions.length === 0) {
    return (
      <span
        className="text-xs text-muted-foreground/70"
        title={
          scanned
            ? "설치 위치는 확인됐지만 TLS 핸드셰이크로 관측된 버전이 없습니다. 파일·스토어처럼 네트워크 위치가 아니거나, 스캔이 협상까지 가지 못한 자리입니다."
            : "네트워크 스캔으로 한 번도 확인되지 않았습니다. Discovery 스캔을 돌리면 채워집니다."
        }
      >
        확인 안 됨
      </span>
    );
  }
  return (
    <span className="font-mono text-xs tabular-nums">
      {formatTlsVersions(versions)}
    </span>
  );
}

function FilterChip({ label, onRemove }: { label: string; onRemove: () => void }) {
  return (
    <span className="inline-flex items-center gap-1 rounded-full border bg-secondary px-2.5 py-0.5 text-xs font-medium">
      {label}
      <button
        type="button"
        aria-label={`${label} 필터 제거`}
        className="text-muted-foreground hover:text-foreground"
        onClick={onRemove}
      >
        <X className="size-3" />
      </button>
    </span>
  );
}

function SortHead({
  field,
  sort,
  order,
  setQuery,
}: {
  field: string;
  sort: string;
  order: string;
  setQuery: (u: Record<string, string | null>, resetPage?: boolean) => void;
}) {
  const active = sort === field;
  return (
    <TableHead>
      <button
        type="button"
        className={cn(
          "inline-flex items-center gap-1 hover:text-foreground",
          active && "text-foreground",
        )}
        onClick={() =>
          setQuery(
            { sort: field, order: active && order === "asc" ? "desc" : "asc" },
            false,
          )
        }
      >
        {SORTABLE[field]}
        {active &&
          (order === "desc" ? (
            <ArrowDown className="size-3" />
          ) : (
            <ArrowUp className="size-3" />
          ))}
      </button>
    </TableHead>
  );
}

function Pager({
  data,
  setQuery,
}: {
  data: CertificateListResponse;
  setQuery: (u: Record<string, string | null>, resetPage?: boolean) => void;
}) {
  const lastPage = Math.max(1, Math.ceil(data.total / data.page_size));
  const from = data.total === 0 ? 0 : (data.page - 1) * data.page_size + 1;
  const to = Math.min(data.total, data.page * data.page_size);

  return (
    <div className="flex items-center gap-3 border-t px-4 py-3 text-[13px] text-muted-foreground">
      <span>
        {data.total.toLocaleString()}건 중 {from}–{to} 표시
      </span>
      <Select
        value={String(data.page_size)}
        onValueChange={(v) => setQuery({ page_size: v })}
      >
        <SelectTrigger size="sm" className="w-[90px]">
          <SelectValue />
        </SelectTrigger>
        <SelectContent>
          {[15, 50, 100].map((n) => (
            <SelectItem key={n} value={String(n)}>{n}줄</SelectItem>
          ))}
        </SelectContent>
      </Select>
      <div className="ml-auto flex items-center gap-1">
        <Button
          variant="ghost"
          size="sm"
          disabled={data.page <= 1}
          onClick={() => setQuery({ page: String(data.page - 1) }, false)}
        >
          이전
        </Button>
        <span className="px-1 font-mono text-xs tabular-nums">
          {data.page} / {lastPage}
        </span>
        <Button
          variant="ghost"
          size="sm"
          disabled={data.page >= lastPage}
          onClick={() => setQuery({ page: String(data.page + 1) }, false)}
        >
          다음
        </Button>
      </div>
    </div>
  );
}

/** 인증서 업로드 패널 — PEM(체인 포함)·DER. 완료 후 목록을 새로고침한다. */
function UploadPanel({
  onImported,
  onClose,
}: {
  onImported: () => void;
  onClose: () => void;
}) {
  const [file, setFile] = React.useState<File | null>(null);
  const [busy, setBusy] = React.useState(false);
  const [result, setResult] = React.useState<ImportResult | null>(null);
  const [error, setError] = React.useState<string | null>(null);
  const inputRef = React.useRef<HTMLInputElement>(null);

  async function submit() {
    if (!file) return;
    setBusy(true);
    setError(null);
    setResult(null);
    try {
      const res = await importCertificateFile(file);
      setResult(res);
      setFile(null);
      if (inputRef.current) inputRef.current.value = "";
      onImported();
    } catch (err) {
      if (err instanceof ApiError && err.code === "private_key_rejected") {
        setError(
          "업로드 파일에 개인키가 포함되어 있어 전체를 거부했습니다. 인증서만 추려 다시 업로드하십시오.",
        );
      } else {
        setError(err instanceof Error ? err.message : "업로드에 실패했습니다.");
      }
    } finally {
      setBusy(false);
    }
  }

  const newCount = result?.imported.filter((c) => !c.duplicate).length ?? 0;
  const dupCount = result?.imported.filter((c) => c.duplicate).length ?? 0;

  return (
    <Card className="gap-0 py-0">
      <div className="flex items-center gap-2 border-b px-4 py-3">
        <h2 className="text-sm font-semibold tracking-tight">인증서 등록</h2>
        <span className="text-xs text-muted-foreground">
          PEM(체인 포함)·DER · 개인키 포함 파일은 거부됩니다
        </span>
        <Button variant="ghost" size="icon-sm" className="ml-auto" aria-label="닫기" onClick={onClose}>
          <X />
        </Button>
      </div>
      <div className="space-y-3 p-4">
        <div className="flex items-center gap-2">
          <Input
            ref={inputRef}
            type="file"
            accept=".pem,.crt,.cer,.der"
            className="max-w-md"
            onChange={(e) => setFile(e.target.files?.[0] ?? null)}
          />
          <Button disabled={!file || busy} onClick={() => void submit()}>
            {busy ? "업로드 중…" : "등록"}
          </Button>
        </div>
        {error && (
          <Alert variant="destructive">
            <AlertDescription>{error}</AlertDescription>
          </Alert>
        )}
        {result && (
          <Alert>
            <AlertTitle>
              등록 완료 — 신규 {newCount}건 · 중복 {dupCount}건
            </AlertTitle>
            <AlertDescription>
              <ul className="mt-1 space-y-0.5">
                {result.imported.map((c) => (
                  <li key={c.id}>
                    <Link
                      href={`/certificates/${c.id}`}
                      className="font-mono text-xs font-medium hover:underline hover:underline-offset-3"
                    >
                      {c.common_name}
                    </Link>{" "}
                    <span className="text-xs text-muted-foreground">
                      {c.duplicate ? "중복 — 기존 레코드 재사용" : "신규 등록"}
                    </span>
                  </li>
                ))}
              </ul>
            </AlertDescription>
          </Alert>
        )}
      </div>
    </Card>
  );
}
