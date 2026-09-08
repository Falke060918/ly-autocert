"use client";
import Link from "next/link";
import { IssuanceWarnings } from "@/components/issuance-warnings";
import { IssuanceComparisonPanel } from "@/components/issuance-comparison";
import { useParams } from "next/navigation";
import { useAuth, isSystemAdministrator } from "@/lib/auth";
import { Button } from "@/components/ui/button";
import {
  Notice,
  Panel,
  useResource,
  useAction,
} from "@/components/issuance-common";
import { Deployment } from "@/components/issuance-deployment";
import { send, STATUS, EVENT, type Order } from "@/lib/issuance";
export default function Page() {
  const { id } = useParams<{ id: string }>();
  const { user } = useAuth();
  const { data, error, reload } = useResource<{ order: Order }>(
    user ? `/issuance/orders/${id}` : null,
    5000,
  );
  const action = useAction();
  const o = data?.order;
  const manage =
    !!user && ["system_administrator", "pki_administrator"].includes(user.role);
  const approve = manage || user?.role === "security_administrator";
  const act = (name: string) =>
    void action.run(async () => {
      await send(`/issuance/orders/${id}/${name}`);
      await reload();
    });
  return (
    <div className="max-w-5xl space-y-5">
      <Link className="text-sm underline" href="/issuance">
        발급 목록
      </Link>
      <h1 className="text-2xl font-semibold">발급 주문 상세</h1>
      <Notice text={action.message || error} />
      {!o && !error && <p>불러오는 중…</p>}
      {o && (
        <>
          <Panel title={o.spec.dns_names.join(", ")}>
            <div className="flex flex-wrap items-center gap-3">
              <span className="rounded border px-3 py-1">
                {STATUS[o.status]}
              </span>
              {o.status === "approval_pending" && approve && (
                <Button disabled={action.busy} onClick={() => act("approve")}>
                  승인
                </Button>
              )}
              {o.status === "issue_failed" && manage && (
                <Button disabled={action.busy} onClick={() => act("retry")}>
                  재시도
                </Button>
              )}
              {[
                "approval_pending",
                "issuing",
                "awaiting_validation",
                "issue_failed",
              ].includes(o.status) &&
                manage && (
                  <Button
                    variant="outline"
                    disabled={action.busy}
                    onClick={() => act("cancel")}
                  >
                    발급 취소
                  </Button>
                )}
            </div>
            <p className="text-sm text-muted-foreground">
              {o.spec.key_algorithm} · {o.spec.validity_days}일 · CA 주문{" "}
              {o.upstream_order_code || "접수 전"}
            </p>
            <Notice text={o.last_error} />
            <IssuanceWarnings warnings={o.comparison?.warnings ?? o.events?.filter((e) => ["chain_warning", "validation_warning"].includes(e.kind)).map((e) => ({ code: e.detail.code, message: e.detail.reason }))} />
            {o.certificate_id && (
              <Link
                className="text-sm underline"
                href={`/certificates/${o.certificate_id}`}
              >
                발급된 인증서 보기
              </Link>
            )}
          </Panel>
          {o.domain_validation.length > 0 && (
            <Panel title="도메인 검증 안내">
              <p className="text-sm text-muted-foreground">
                아래 자료를 DNS 또는 웹서버에 등록하면 CA 확인 결과를 자동으로
                반영합니다.
              </p>
              {o.domain_validation.map((v) => (
                <div
                  key={v.domain_name}
                  className="space-y-2 rounded border p-3"
                >
                  <p className="font-medium">
                    {v.domain_name} ·{" "}
                    {v.status === "validated" ? "검증 완료" : "검증 대기"}
                  </p>
                  {[
                    ["DNS TXT", v.dns_txt_record],
                    ["HTTP 파일 URL", v.http_file_url],
                    ["검증 값", v.validation_key],
                  ]
                    .filter(([, value]) => value)
                    .map(([label, value]) => (
                      <div
                        key={label}
                        className="flex flex-wrap items-start gap-2 text-sm"
                      >
                        <span className="text-muted-foreground">{label}</span>
                        <code className="min-w-0 flex-1 break-all whitespace-pre-wrap">
                          {value}
                        </code>
                        <Button
                          size="sm"
                          variant="outline"
                          onClick={() =>
                            void action.run(async () => {
                              await navigator.clipboard.writeText(value);
                              action.setMessage("복사했습니다.");
                            })
                          }
                        >
                          복사
                        </Button>
                      </div>
                    ))}
                </div>
              ))}
            </Panel>
          )}
          <IssuanceComparisonPanel comparison={o.comparison} />
          <Panel title="처리 이력">
            <ol className="space-y-3">
              {o.events?.map((e, i) => (
                <li key={i} className="border-l-2 pl-4">
                  <p className="text-sm">{EVENT[e.kind] ?? e.kind}</p>
                  <time className="text-xs text-muted-foreground">
                    {new Date(e.at).toLocaleString("ko-KR")}
                  </time>
                  {e.detail.reason && (
                    <p className="text-sm">{e.detail.reason}</p>
                  )}
                </li>
              ))}
            </ol>
          </Panel>
          {isSystemAdministrator(user) && <Deployment order={o} />}
        </>
      )}
    </div>
  );
}
