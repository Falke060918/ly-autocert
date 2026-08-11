"use client";

/**
 * 서버 뷰 — 같은 인벤토리를 호스트 관점으로 뒤집어 본다.
 *
 * 인증서 목록은 인증서 1행이라 "이 인증서가 어디에 깔려 있나"는 답해도 "이 서버에
 * 뭐가 깔려 있나"는 답하지 못한다. 1행 = 호스트이고, 행을 펼치면 그 호스트의
 * 포트별 엔드포인트를 지연 로드한다(목록 응답에는 포트 미리보기 5개만 들어 있다).
 *
 * 필터·정렬 상태는 URL에 남기되 목록 뷰와 키가 겹치지 않게 `srv_` 접두사를 쓴다 —
 * 두 뷰의 sort 화이트리스트가 달라 값이 새면 400이 난다.
 *
 * ⚠ 정렬 `risk`는 서버에서 min(not_after)다. 라벨은 "만료 임박순"이며
 * 위험 판정(danger/warn) 배지와 같은 축이 아니다 (lib/servers.ts 주석).
 */

import * as React from "react";
import Link from "next/link";
import { useSearchParams } from "next/navigation";
import { ChevronDown, ChevronRight, Search } from "lucide-react";

import { Alert, AlertDescription } from "@/components/ui/alert";
import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import { Card } from "@/components/ui/card";
import { Input } from "@/components/ui/input";
import { Label } from "@/components/ui/label";
import {
  Select,
  SelectContent,
  SelectItem,
  SelectTrigger,
  SelectValue,
} from "@/components/ui/select";
import { Skeleton } from "@/components/ui/skeleton";
import { Switch } from "@/components/ui/switch";
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
import { formatDate, formatDateTime } from "@/lib/format";
import {
  DEFAULT_SERVER_SORT,
  getServer,
  listServers,
  SERVER_SORT_OPTIONS,
  serverLabel,
  serverScopeLabel,
  type ServerDetailResponse,
  type ServerEndpoint,
  type ServerListItem,
  type ServerListResponse,
} from "@/lib/servers";

/** 서버 뷰 전용 URL 파라미터 — 목록 뷰와 키를 공유하지 않는다. */
const P = {
  q: "srv_q",
  expiry: "srv_expiry",
  mismatch: "srv_mismatch",
  sort: "srv_sort",
  page: "srv_page",
  pageSize: "srv_page_size",
} as const;

const EXPIRY_OPTIONS = [
  { value: "expired", label: "이미 만료된 인증서 있음" },
  { value: "7", label: "7일 이내 만료 있음" },
  { value: "30", label: "30일 이내 만료 있음" },
  { value: "60", label: "60일 이내 만료 있음" },
  { value: "90", label: "90일 이내 만료 있음" },
];

/** 펼친 행의 상세 — 로딩·실패도 상태로 남겨 같은 행을 다시 요청하지 않는다. */
type EndpointState =
  | { kind: "loading" }
  | { kind: "error"; message: string }
  | { kind: "ready"; detail: ServerDetailResponse };

export function ServersView({
  setQuery,
}: {
  setQuery: (u: Record<string, string | null>, resetPage?: boolean) => void;
}) {
  const searchParams = useSearchParams();
  const [data, setData] = React.useState<ServerListResponse | null>(null);
  const [error, setError] = React.useState<string | null>(null);
  const [expanded, setExpanded] = React.useState<Record<string, EndpointState>>({});
  const now = useTicker(60_000);

  const sortValue = searchParams.get(P.sort) ?? DEFAULT_SERVER_SORT;
  const expiryValue = searchParams.get(P.expiry) ?? "all";
  const mismatchOnly = searchParams.get(P.mismatch) === "true";
  const search = searchParams.get(P.q) ?? "";

  const apiQuery = React.useMemo(() => {
    const out = new URLSearchParams();
    if (search) out.set("q", search);
    if (expiryValue === "expired") out.set("expired", "true");
    else if (expiryValue !== "all") out.set("expires_within_days", expiryValue);
    if (mismatchOnly) out.set("mismatch", "true");
    const preset =
      SERVER_SORT_OPTIONS.find((o) => o.value === sortValue) ??
      SERVER_SORT_OPTIONS[0];
    out.set("sort", preset.field);
    out.set("order", preset.order);
    const page = searchParams.get(P.page);
    if (page) out.set("page", page);
    const pageSize = searchParams.get(P.pageSize);
    if (pageSize) out.set("page_size", pageSize);
    return out;
  }, [search, expiryValue, mismatchOnly, sortValue, searchParams]);

  React.useEffect(() => {
    let cancelled = false;
    listServers(apiQuery)
      .then((res) => {
        if (cancelled) return;
        setData(res);
        setError(null);
        // 조건이 바뀌면 행 구성 자체가 달라진다 — 펼친 상세는 버린다.
        setExpanded({});
      })
      .catch((err: unknown) => {
        if (!cancelled)
          setError(
            err instanceof Error ? err.message : "서버 목록 조회에 실패했습니다.",
          );
      });
    return () => {
      cancelled = true;
    };
  }, [apiQuery]);

  const toggle = React.useCallback((id: string) => {
    setExpanded((prev) => {
      if (prev[id]) {
        const next = { ...prev };
        delete next[id];
        return next;
      }
      void getServer(id)
        .then((detail) =>
          setExpanded((cur) =>
            cur[id] ? { ...cur, [id]: { kind: "ready", detail } } : cur,
          ),
        )
        .catch((err: unknown) =>
          setExpanded((cur) =>
            cur[id]
              ? {
                  ...cur,
                  [id]: {
                    kind: "error",
                    message:
                      err instanceof Error
                        ? err.message
                        : "서버 상세 조회에 실패했습니다.",
                  },
                }
              : cur,
          ),
        );
      return { ...prev, [id]: { kind: "loading" } };
    });
  }, []);

  const hasFilter =
    search !== "" || expiryValue !== "all" || mismatchOnly;

  return (
    <div className="space-y-4">
      <div className="flex flex-wrap items-center gap-2">
        <ServerSearchField
          initial={search}
          onSearch={(q) => setQuery({ [P.q]: q, [P.page]: null }, false)}
        />
        <Select
          value={expiryValue}
          onValueChange={(v) =>
            setQuery({ [P.expiry]: v === "all" ? null : v, [P.page]: null }, false)
          }
        >
          <SelectTrigger className="w-[200px]">
            <SelectValue />
          </SelectTrigger>
          <SelectContent>
            <SelectItem value="all">만료: 전체</SelectItem>
            {EXPIRY_OPTIONS.map((o) => (
              <SelectItem key={o.value} value={o.value}>{o.label}</SelectItem>
            ))}
          </SelectContent>
        </Select>
        <Select
          value={sortValue}
          onValueChange={(v) => setQuery({ [P.sort]: v, [P.page]: null }, false)}
        >
          <SelectTrigger className="w-[170px]">
            <SelectValue />
          </SelectTrigger>
          <SelectContent>
            {SERVER_SORT_OPTIONS.map((o) => (
              <SelectItem key={o.value} value={o.value}>{o.label}</SelectItem>
            ))}
          </SelectContent>
        </Select>
        {/*
          구버전 잔존은 "교체는 됐는데 옛 인증서가 아직 어딘가 붙어 있다"이다 —
          정리 작업 목록이라 따로 뽑아 볼 수 있어야 한다.
        */}
        <div className="flex items-center gap-2">
          <Switch
            id="srv-mismatch"
            checked={mismatchOnly}
            onCheckedChange={(v) =>
              setQuery({ [P.mismatch]: v ? "true" : null, [P.page]: null }, false)
            }
          />
          <Label htmlFor="srv-mismatch" className="text-[13px] font-normal">
            구버전 잔존만
          </Label>
        </div>
        {data && (
          <span className="text-[13px] text-muted-foreground">
            서버 {data.total.toLocaleString()}대
          </span>
        )}
        {hasFilter && (
          <Button
            variant="outline"
            size="sm"
            className="ml-auto"
            onClick={() =>
              setQuery(
                {
                  [P.q]: null,
                  [P.expiry]: null,
                  [P.mismatch]: null,
                  [P.page]: null,
                },
                false,
              )
            }
          >
            초기화
          </Button>
        )}
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
              {hasFilter
                ? "조건에 맞는 서버가 없습니다. 필터를 초기화해 보십시오."
                : "아직 확인된 서버가 없습니다. Discovery 스캔을 돌리면 발견한 엔드포인트가 여기에 호스트 단위로 모입니다."}
            </div>
          ) : (
            <Table>
              <TableHeader>
                <TableRow>
                  <TableHead>호스트</TableHead>
                  <TableHead>수집 경로</TableHead>
                  <TableHead className="text-right">엔드포인트</TableHead>
                  <TableHead className="text-right">인증서</TableHead>
                  <TableHead>가장 이른 만료</TableHead>
                  <TableHead>최근 확인</TableHead>
                  <TableHead>포트</TableHead>
                </TableRow>
              </TableHeader>
              <TableBody>
                {data.items.map((s) => {
                  const state = expanded[s.id];
                  return (
                    <React.Fragment key={s.id}>
                      <TableRow>
                        <TableCell>
                          <button
                            type="button"
                            aria-expanded={state != null}
                            className="flex w-full min-w-0 items-center gap-1.5 text-left"
                            onClick={() => toggle(s.id)}
                          >
                            {state ? (
                              <ChevronDown
                                aria-hidden
                                className="size-3.5 shrink-0 text-muted-foreground/60"
                              />
                            ) : (
                              <ChevronRight
                                aria-hidden
                                className="size-3.5 shrink-0 text-muted-foreground/60"
                              />
                            )}
                            <span className="truncate font-mono text-xs font-medium">
                              {serverLabel(s)}
                            </span>
                          </button>
                          {s.hostname && s.ip_address && (
                            <div className="pl-5 font-mono text-[11px] text-muted-foreground/70">
                              {s.ip_address}
                            </div>
                          )}
                        </TableCell>
                        <TableCell className="text-[13px]">
                          {serverScopeLabel(s)}
                        </TableCell>
                        <TableCell className="text-right font-mono text-xs tabular-nums">
                          {s.endpoint_count.toLocaleString()}
                        </TableCell>
                        <TableCell className="text-right font-mono text-xs tabular-nums">
                          {s.certificate_count.toLocaleString()}
                        </TableCell>
                        <TableCell className="whitespace-nowrap">
                          {s.earliest_not_after ? (
                            <>
                              <span className="font-mono text-xs tabular-nums">
                                {formatDate(s.earliest_not_after)}
                              </span>{" "}
                              <DdayText notAfter={s.earliest_not_after} />
                            </>
                          ) : (
                            <span className="text-xs text-muted-foreground/60">
                              인증서 미확인
                            </span>
                          )}
                        </TableCell>
                        <TableCell className="whitespace-nowrap">
                          <FreshnessText iso={s.last_seen_at} now={now} />
                        </TableCell>
                        <TableCell>
                          <div className="flex flex-wrap items-center gap-1.5">
                            <span className="font-mono text-xs text-muted-foreground tabular-nums">
                              {s.ports.length > 0 ? s.ports.join(", ") : "—"}
                              {s.endpoint_count > s.ports.length &&
                                ` 외 ${s.endpoint_count - s.ports.length}`}
                            </span>
                            <ServerFlagBadges server={s} />
                          </div>
                        </TableCell>
                      </TableRow>
                      {state && (
                        <TableRow className="hover:bg-transparent">
                          <TableCell colSpan={7} className="bg-muted/40 p-0">
                            <ExpandedServer state={state} now={now} />
                          </TableCell>
                        </TableRow>
                      )}
                    </React.Fragment>
                  );
                })}
              </TableBody>
            </Table>
          )}
          <ServerPager data={data} setQuery={setQuery} />
        </Card>
      )}
    </div>
  );
}

/**
 * 행 단위 표기 배지.
 * '추정'은 확정과 반드시 구분한다 — 관측 지문이 아니라 최근 live 위치로 채운 값이다.
 */
function ServerFlagBadges({ server }: { server: ServerListItem }) {
  return (
    <>
      {server.estimated_count > 0 && (
        <Badge
          variant="outline"
          className="text-muted-foreground"
          title="재스캔 관측 지문이 없어 가장 최근 확인된 설치 위치로 추정한 값입니다. 재스캔이 한 번 돌면 확정됩니다."
        >
          추정 {server.estimated_count}
        </Badge>
      )}
      {server.stale_count > 0 && (
        <Badge
          variant="warn"
          title="현재 인증서와 다른 위치 행이 아직 남아 있습니다(교체 후 구버전 잔존)."
        >
          구버전 {server.stale_count}
        </Badge>
      )}
    </>
  );
}

/** 펼친 행 본문 — 포트별 엔드포인트. */
function ExpandedServer({ state, now }: { state: EndpointState; now: number }) {
  if (state.kind === "loading") {
    return (
      <div className="space-y-2 px-4 py-3">
        <Skeleton className="h-5 w-64" />
        <Skeleton className="h-5 w-96" />
      </div>
    );
  }
  if (state.kind === "error") {
    return (
      <p className="px-4 py-3 text-[13px] text-muted-foreground">
        {state.message}
      </p>
    );
  }
  const { endpoints } = state.detail;
  if (endpoints.length === 0) {
    return (
      <p className="px-4 py-3 text-[13px] text-muted-foreground">
        확인된 엔드포인트가 없습니다.
      </p>
    );
  }
  return (
    <div className="divide-y">
      {endpoints.map((e) => (
        <EndpointRow key={e.port} endpoint={e} now={now} />
      ))}
    </div>
  );
}

function EndpointRow({
  endpoint: e,
  now,
}: {
  endpoint: ServerEndpoint;
  now: number;
}) {
  return (
    <div className="flex flex-wrap items-start gap-x-4 gap-y-1 px-4 py-2.5 pl-9">
      <span className="w-14 shrink-0 font-mono text-xs font-medium tabular-nums">
        :{e.port}
      </span>
      <div className="min-w-0 flex-1">
        {e.certificate ? (
          <div className="flex flex-wrap items-center gap-1.5">
            <Link
              href={`/certificates/${e.certificate.id}`}
              className="truncate font-mono text-xs hover:underline hover:underline-offset-3"
            >
              {e.certificate.common_name || "(CN 없음)"}
            </Link>
            <CertStatusBadge
              status={e.certificate.status}
              notAfter={e.certificate.not_after}
            />
            <DdayText notAfter={e.certificate.not_after} />
            {e.certificate_estimated && (
              <Badge
                variant="outline"
                className="text-muted-foreground"
                title="재스캔 관측 지문이 없어 가장 최근 확인된 설치 위치로 추정한 값입니다."
              >
                추정
              </Badge>
            )}
          </div>
        ) : (
          <span className="text-xs text-muted-foreground/70">
            붙어 있는 인증서를 특정하지 못했습니다.
          </span>
        )}
        {e.stale_locations.length > 0 && (
          <ul className="mt-1 space-y-0.5">
            {e.stale_locations.map((s) => (
              <li
                key={s.location_id}
                className="flex flex-wrap items-center gap-1.5 text-xs text-muted-foreground"
              >
                <span aria-hidden className="font-mono text-muted-foreground/50">
                  └
                </span>
                <Link
                  href={`/certificates/${s.certificate.id}`}
                  className="truncate font-mono hover:underline hover:underline-offset-3"
                >
                  {s.certificate.common_name || "(CN 없음)"}
                </Link>
                <Badge variant="warn">구버전 잔존</Badge>
                {s.last_verified_at && (
                  <span className="font-mono tabular-nums">
                    {formatDateTime(s.last_verified_at)} 확인
                  </span>
                )}
              </li>
            ))}
          </ul>
        )}
      </div>
      <span className="w-20 shrink-0 font-mono text-xs text-muted-foreground">
        {e.tls_version ?? "—"}
      </span>
      <div className="w-40 shrink-0">
        <FreshnessText iso={e.last_success_at} now={now} />
        {e.consecutive_misses > 0 && (
          <div className="text-[11px] text-warn">
            연속 미관측 {e.consecutive_misses}회
          </div>
        )}
        {e.retired_at && (
          <div className="text-[11px] text-muted-foreground/70">
            은퇴 {formatDate(e.retired_at)}
          </div>
        )}
      </div>
    </div>
  );
}

/** 검색 입력 — 400ms 디바운스 + Enter 즉시 적용 (목록 뷰와 같은 동작). */
function ServerSearchField({
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
        placeholder="호스트명 / IP / 인증서 CN 검색"
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

function ServerPager({
  data,
  setQuery,
}: {
  data: ServerListResponse;
  setQuery: (u: Record<string, string | null>, resetPage?: boolean) => void;
}) {
  const lastPage = Math.max(1, Math.ceil(data.total / data.page_size));
  const from = data.total === 0 ? 0 : (data.page - 1) * data.page_size + 1;
  const to = Math.min(data.total, data.page * data.page_size);

  return (
    <div className="flex items-center gap-3 border-t px-4 py-3 text-[13px] text-muted-foreground">
      <span>
        {data.total.toLocaleString()}대 중 {from}–{to} 표시
      </span>
      <Select
        value={String(data.page_size)}
        onValueChange={(v) =>
          setQuery({ [P.pageSize]: v, [P.page]: null }, false)
        }
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
          onClick={() => setQuery({ [P.page]: String(data.page - 1) }, false)}
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
          onClick={() => setQuery({ [P.page]: String(data.page + 1) }, false)}
        >
          다음
        </Button>
      </div>
    </div>
  );
}
