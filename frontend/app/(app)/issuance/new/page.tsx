"use client";
import { useState } from "react";
import { useRouter } from "next/navigation";
import Link from "next/link";
import { useAuth } from "@/lib/auth";
import { Button } from "@/components/ui/button";
import {
  Field,
  Notice,
  Panel,
  inputClass,
  useResource,
  useAction,
} from "@/components/issuance-common";
import {
  send,
  type CAConnection,
  type Product,
  type KeyAlgorithm,
  type ValidationMethod,
  type Order,
} from "@/lib/issuance";
export default function Page() {
  const { user } = useAuth();
  const router = useRouter();
  const allowed =
    !!user && ["system_administrator", "pki_administrator"].includes(user.role);
  const connections = useResource<{ items: CAConnection[]; enabled: boolean }>(
    allowed ? "/ca-connections" : null,
  );
  const [connection, setConnection] = useState("");
  const products = useResource<{ items: Product[] }>(
    connection ? `/ca-connections/${connection}/products` : null,
  );
  const [product, setProduct] = useState("");
  const [names, setNames] = useState("");
  const [algorithm, setAlgorithm] = useState<KeyAlgorithm>("ecdsa_p256");
  const [days, setDays] = useState(90);
  const [methods, setMethods] = useState<Record<string, ValidationMethod>>({});
  const action = useAction();
  const dns = Array.from(
    new Set(
      names
        .toLowerCase()
        .split(/[\s,]+/)
        .filter(Boolean),
    ),
  );
  if (!user) return <p>불러오는 중…</p>;
  if (!allowed)
    return (
      <Notice text="시스템 또는 PKI 관리자만 발급을 요청할 수 있습니다." />
    );
  return (
    <div className="max-w-3xl space-y-5">
      <Link className="text-sm underline" href="/issuance">
        발급 목록
      </Link>
      <h1 className="text-2xl font-semibold">신규 인증서 발급</h1>
      <Notice text={action.message || connections.error || products.error} />
      {connections.data && !connections.data.enabled && (
        <Notice text="발급 기능이 비활성 상태입니다. 관리자에게 암호화 키 설정을 요청하세요." />
      )}
      <form
        onSubmit={(e) => {
          e.preventDefault();
          void action.run(async () => {
            const out = await send<{ order: Order }>("/issuance/orders", {
              ca_connection_id: connection,
              spec: {
                dns_names: dns,
                key_algorithm: algorithm,
                validity_days: days,
                product_id: Number(product),
                signing_ca_type: "automatic",
                domain_validations: dns.map((name) => ({
                  name,
                  method: name.startsWith("*.")
                    ? "txt_dns_record"
                    : (methods[name] ?? "txt_dns_record"),
                })),
              },
            });
            router.push(`/issuance/${out.order.id}`);
          });
        }}
      >
        <Panel title="발급 요청 정보">
          <div className="grid gap-4 sm:grid-cols-2">
            <Field label="CA 연결">
              <select
                required
                className={inputClass}
                value={connection}
                onChange={(e) => {
                  setConnection(e.target.value);
                  setProduct("");
                }}
              >
                <option value="">선택하세요</option>
                {connections.data?.items
                  .filter((c) => c.active)
                  .map((c) => (
                    <option key={c.id} value={c.id}>
                      {c.name}
                    </option>
                  ))}
              </select>
            </Field>
            <Field label="CA 상품">
              <select
                required
                className={inputClass}
                value={product}
                onChange={(e) => setProduct(e.target.value)}
              >
                <option value="">선택하세요</option>
                {products.data?.items.map((p) => (
                  <option key={p.id} value={p.id}>
                    {p.name}
                  </option>
                ))}
              </select>
            </Field>
            <Field label="키 알고리즘">
              <select
                className={inputClass}
                value={algorithm}
                onChange={(e) => setAlgorithm(e.target.value as KeyAlgorithm)}
              >
                {[
                  "rsa2048",
                  "rsa3072",
                  "rsa4096",
                  "ecdsa_p256",
                  "ecdsa_p384",
                ].map((v) => (
                  <option key={v}>{v}</option>
                ))}
              </select>
            </Field>
            <Field label="유효기간 (일)">
              <input
                required
                type="number"
                min={1}
                max={3650}
                className={inputClass}
                value={days}
                onChange={(e) => setDays(Number(e.target.value))}
              />
            </Field>
          </div>
          <Field label="도메인 (줄바꿈 또는 쉼표로 구분)">
            <textarea
              required
              className={inputClass}
              rows={4}
              placeholder="www.example.com"
              value={names}
              onChange={(e) => setNames(e.target.value)}
            />
          </Field>
          <div className="space-y-3">
            {dns.map((name) => (
              <Field key={name} label={`${name} 검증 방식`}>
                <select
                  className={inputClass}
                  value={
                    name.startsWith("*.")
                      ? "txt_dns_record"
                      : (methods[name] ?? "txt_dns_record")
                  }
                  onChange={(e) =>
                    setMethods({
                      ...methods,
                      [name]: e.target.value as ValidationMethod,
                    })
                  }
                >
                  <option value="txt_dns_record">DNS TXT 레코드</option>
                  {!name.startsWith("*.") && (
                    <option value="txt_http_file">HTTP 파일</option>
                  )}
                </select>
              </Field>
            ))}
          </div>
          <p className="text-sm text-muted-foreground">
            승인 정책에 따라 승인 후 CA에 주문합니다. 도메인 검증 자료는 주문
            상세에서 확인하고 직접 등록해야 합니다.
          </p>
          <Button
            disabled={
              action.busy ||
              !connections.data?.enabled ||
              !product ||
              dns.length === 0
            }
            type="submit"
          >
            {action.busy ? "요청 중…" : "발급 요청"}
          </Button>
        </Panel>
      </form>
    </div>
  );
}
