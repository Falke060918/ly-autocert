"use client";
import { useState } from "react";
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
import { send, type CAConnection, type Subscriber } from "@/lib/issuance";
const empty = {
  name: "",
  base_url: "https://tlsmanager.certifyiddemo.com/api",
  app_id: "",
  secret: "",
  subscriberID: "",
  contactID: "",
  lastName: "",
  email: "",
  company: "",
  country: "KR",
  locality: "",
  province: "",
  phone: "",
  address: "",
};
export default function Page() {
  const { user } = useAuth();
  const allowed =
    !!user && ["system_administrator", "pki_administrator"].includes(user.role);
  const connections = useResource<{ items: CAConnection[]; enabled: boolean }>(
    allowed ? "/ca-connections" : null,
  );
  const policies = useResource<{
    settings: { approval_required_new: boolean; auto_renew: boolean };
    enabled: boolean;
  }>(allowed ? "/issuance/settings" : null);
  const [form, setForm] = useState(empty);
  const [editing, setEditing] = useState("");
  const action = useAction();
  const set = (k: keyof typeof empty, v: string) =>
    setForm((f) => ({ ...f, [k]: v }));
  if (!user) return <p>불러오는 중…</p>;
  if (!allowed)
    return <Notice text="시스템 또는 PKI 관리자만 설정할 수 있습니다." />;
  const fields: [keyof typeof empty, string, string?][] = [
    ["name", "연결 이름"],
    ["base_url", "CA API 주소", "url"],
    ["app_id", editing ? "App ID (비우면 기존 유지)" : "App ID", "password"],
    ["secret", editing ? "Secret (비우면 기존 유지)" : "Secret", "password"],
    ["subscriberID", "기존 가입자 ID (신규 가입자는 비움)", "number"],
    ["contactID", "기존 담당자 ID (신규 담당자는 비움)", "number"],
  ];
  return (
    <div className="max-w-4xl space-y-5">
      <div className="flex items-center justify-between">
        <h1 className="text-2xl font-semibold">CA 연결·발급 정책</h1>
        <Link className="text-sm underline" href="/issuance">
          발급 목록
        </Link>
      </div>
      <Notice text={action.message || connections.error || policies.error} />
      {connections.data && !connections.data.enabled && (
        <Notice text="ISSUANCE_ENC_KEY가 설정되지 않아 연결 저장·발급이 비활성 상태입니다. 운영 매뉴얼의 암호화 키 설정을 확인하세요." />
      )}
      <Panel title="등록된 CA 연결">
        {connections.data?.items.map((c) => (
          <div
            key={c.id}
            className="flex flex-wrap items-center justify-between gap-3 border-b pb-3"
          >
            <div>
              <p>
                {c.name} · {c.active ? "활성" : "비활성"}
              </p>
              <p className="text-sm text-muted-foreground break-all">
                {c.base_url} · App ID {c.app_id_configured ? "설정됨" : "없음"} · Secret {c.secret_configured ? "설정됨" : "없음"}
              </p>
            </div>
            <div className="flex gap-2">
              <Button
                variant="outline"
                disabled={action.busy}
                onClick={() => {
                  const s = c.subscriber;
                  setEditing(c.id);
                  setForm({
                    ...empty,
                    name: c.name,
                    base_url: c.base_url,
                    app_id: "",
                    subscriberID: s.existing_subscriber_id
                      ? String(s.existing_subscriber_id)
                      : "",
                    contactID: s.contact.existing_contact_id
                      ? String(s.contact.existing_contact_id)
                      : "",
                    lastName: s.contact.new_contact?.last_name ?? "",
                    email: s.contact.new_contact?.email ?? "",
                    company: s.new_subscriber?.legal_organization_name ?? "",
                    country: s.new_subscriber?.country_iso_code2 ?? "KR",
                    locality: s.new_subscriber?.locality ?? "",
                    province: s.new_subscriber?.province ?? "",
                    phone: s.new_subscriber?.phone_number ?? "",
                    address: s.new_subscriber?.address ?? "",
                  });
                }}
              >
                편집
              </Button>
              <Button
                variant="outline"
                disabled={action.busy || !c.active}
                onClick={() =>
                  void action.run(async () => {
                    const v = await send<{ message: string }>(
                      `/ca-connections/${c.id}/test`,
                    );
                    action.setMessage(v.message);
                  })
                }
              >
                연결 테스트
              </Button>
              <Button
                variant="outline"
                disabled={action.busy || !c.active}
                onClick={() =>
                  void action.run(async () => {
                    await send(`/ca-connections/${c.id}`, undefined, "DELETE");
                    await connections.reload();
                  })
                }
              >
                비활성
              </Button>
            </div>
          </div>
        ))}
        {connections.data?.items.length === 0 && (
          <p className="text-muted-foreground">등록된 연결이 없습니다.</p>
        )}
      </Panel>
      <form
        onSubmit={(e) => {
          e.preventDefault();
          void action.run(async () => {
            const subscriber: Subscriber = {
              existing_subscriber_id: Number(form.subscriberID) || 0,
              contact: form.contactID
                ? { existing_contact_id: Number(form.contactID) }
                : {
                    new_contact: {
                      last_name: form.lastName,
                      email: form.email,
                      contact_types: ["organization", "technical"],
                    },
                  },
            };
            if (!form.subscriberID)
              subscriber.new_subscriber = {
                legal_organization_name: form.company,
                subscriber_type: "private_organization",
                country_iso_code2: form.country,
                locality: form.locality,
                province: form.province,
                phone_number: form.phone,
                address: form.address,
              };
            await send(
              `/ca-connections${editing ? `/${editing}` : ""}`,
              {
                name: form.name,
                base_url: form.base_url,
                app_id: form.app_id,
                secret: form.secret,
                subscriber,
                active: true,
              },
              editing ? "PATCH" : "POST",
            );
            setEditing("");
            setForm(empty);
            action.setMessage("연결을 저장했습니다.");
            await connections.reload();
          });
        }}
      >
        <Panel title={editing ? "CA 연결 수정" : "CA 연결 추가"}>
          <div className="grid gap-4 sm:grid-cols-2">
            {fields.map(([k, label, type]) => (
              <Field key={k} label={label}>
                <input
                  type={type ?? "text"}
                  className={inputClass}
                  autoComplete={k === "secret" || k === "app_id" ? "new-password" : "off"}
                  value={form[k]}
                  min={type === "number" ? 1 : undefined}
                  required={
                    ["name", "base_url"].includes(k) ||
                    (["secret", "app_id"].includes(k) && !editing)
                  }
                  onChange={(e) => set(k, e.target.value)}
                />
              </Field>
            ))}
            {!form.contactID && (
              <>
                <Field label="담당자 이름">
                  <input
                    required
                    className={inputClass}
                    value={form.lastName}
                    onChange={(e) => set("lastName", e.target.value)}
                  />
                </Field>
                <Field label="담당자 이메일">
                  <input
                    required
                    type="email"
                    className={inputClass}
                    value={form.email}
                    onChange={(e) => set("email", e.target.value)}
                  />
                </Field>
              </>
            )}
            {!form.subscriberID &&
              (
                [
                  ["company", "회사명"],
                  ["country", "국가 코드 (예: KR)"],
                  ["province", "시·도"],
                  ["locality", "도시"],
                  ["address", "주소"],
                  ["phone", "전화번호"],
                ] as [keyof typeof empty, string][]
              ).map(([k, label]) => (
                <Field key={k} label={label}>
                  <input
                    required
                    className={inputClass}
                    value={form[k]}
                    onChange={(e) => set(k, e.target.value)}
                  />
                </Field>
              ))}
          </div>
          <div className="flex gap-2">
            <Button
              type="submit"
              disabled={action.busy || !connections.data?.enabled}
            >
              {action.busy ? "저장 중…" : "저장"}
            </Button>
            {editing && (
              <Button
                type="button"
                variant="outline"
                onClick={() => {
                  setEditing("");
                  setForm(empty);
                }}
              >
                편집 취소
              </Button>
            )}
          </div>
        </Panel>
      </form>
      <Panel title="발급 정책">
        {policies.data && (
          <>
            <label className="flex items-center gap-2 text-sm">
              <input
                type="checkbox"
                checked={policies.data.settings.approval_required_new}
                disabled={action.busy}
                onChange={(e) => {
                  const approval = e.target.checked;
                  void action.run(async () => {
                    await send(
                      "/issuance/settings",
                      {
                        ...policies.data!.settings,
                        approval_required_new: approval,
                      },
                      "PATCH",
                    );
                    await policies.reload();
                  });
                }}
              />
              신규 발급 시 관리자 승인 필요
            </label>
            <p className="text-sm text-muted-foreground">
              자동 갱신·재배포는 후속 작업입니다. 현재 버전에서는 예약 실행되지
              않습니다.
            </p>
          </>
        )}
      </Panel>
    </div>
  );
}
