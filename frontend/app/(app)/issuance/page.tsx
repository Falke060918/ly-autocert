"use client";
import { useState } from "react";
import Link from "next/link";
import { useAuth } from "@/lib/auth";
import { Button } from "@/components/ui/button";
import { Notice, useResource, inputClass } from "@/components/issuance-common";
import { STATUS, orderStatus, type Order } from "@/lib/issuance";
export default function Page() {
  const { user } = useAuth();
  const [status, setStatus] = useState("");
  const [offset, setOffset] = useState(0);
  const { data, error } = useResource<{ items: Order[] }>(
    user
      ? `/issuance/orders?page_size=50&offset=${offset}&status=${status}`
      : null,
    10000,
  );
  const manage =
    user && ["system_administrator", "pki_administrator"].includes(user.role);
  return (
    <div className="space-y-5">
      <div className="flex flex-wrap items-center justify-between gap-3">
        <h1 className="text-2xl font-semibold">인증서 발급</h1>
        {manage && (
          <div className="flex gap-3">
            <Button variant="outline" asChild>
              <Link href="/settings/issuance">CA 연결 설정</Link>
            </Button>
            <Button asChild>
              <Link href="/issuance/new">신규 발급</Link>
            </Button>
          </div>
        )}
      </div>
      <Notice text={error} />
      <label className="flex max-w-sm items-center gap-3 text-sm">
        <span className="shrink-0 whitespace-nowrap">상태</span>
        <select
          aria-label="주문 상태"
          className={`${inputClass} min-w-0`}
          value={status}
          onChange={(e) => {
            setStatus(e.target.value);
            setOffset(0);
          }}
        >
          <option value="">전체</option>
          {Object.entries(STATUS).map(([v, label]) => (
            <option key={v} value={v}>
              {label}
            </option>
          ))}
        </select>
      </label>
      <div className="overflow-x-auto rounded-xl border">
        <table className="w-full text-left text-sm">
          <thead className="bg-muted">
            <tr>
              <th className="p-3">도메인</th>
              <th className="p-3">상태</th>
              <th className="p-3">요청 시각</th>
            </tr>
          </thead>
          <tbody>
            {data?.items.map((o) => (
              <tr key={o.id} className="border-t">
                <td className="p-3">
                  <Link
                    className="underline underline-offset-4"
                    href={`/issuance/${o.id}`}
                  >
                    {o.spec.dns_names.join(", ")}
                  </Link>
                </td>
                <td className="min-w-40 p-3">
                  <span className="whitespace-nowrap">{orderStatus(o)}</span>
                  {[
                    "approval_pending",
                    "awaiting_validation",
                    "manual_required",
                  ].includes(o.status) && (
                    <span className="ml-2 inline-block whitespace-nowrap rounded border px-2 py-0.5">
                      조치 필요
                    </span>
                  )}
                </td>
                <td className="p-3">
                  {new Date(o.created_at).toLocaleString("ko-KR")}
                </td>
              </tr>
            ))}
          </tbody>
        </table>
        {!data && !error && <p className="p-5">불러오는 중…</p>}
        {data?.items.length === 0 && (
          <p className="p-5 text-muted-foreground">발급 요청이 없습니다.</p>
        )}
      </div>
      <div className="flex gap-2">
        <Button
          variant="outline"
          disabled={offset === 0}
          onClick={() => setOffset(Math.max(0, offset - 50))}
        >
          이전
        </Button>
        <Button
          variant="outline"
          disabled={!data || data.items.length < 50}
          onClick={() => setOffset(offset + 50)}
        >
          다음
        </Button>
      </div>
    </div>
  );
}
