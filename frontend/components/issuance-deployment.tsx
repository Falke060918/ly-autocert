"use client";
import { useState } from "react";
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
  JOB_STATUS,
  STEP,
  type Target,
  type Agent,
  type DeployJob,
  type Order,
} from "@/lib/issuance";
export function Deployment({ order }: { order: Order }) {
  const targets = useResource<{ items: Target[] }>("/deployment/targets");
  const agents = useResource<{ items: Agent[] }>("/agents", 15000);
  const jobs = useResource<{ items: DeployJob[] }>(
    `/deployment/jobs?order_id=${order.id}`,
    5000,
  );
  const [selected, setSelected] = useState("");
  const [create, setCreate] = useState(false);
  const [target, setTarget] = useState<Target>({
    agent_id: "",
    kind: "nginx",
    service_unit: "nginx",
    cert_path: "",
    key_path: "",
    chain_path: "",
    verify_host: "127.0.0.1",
    verify_port: 443,
    sni_name: order.spec.dns_names[0].replace(/^\*\./, ""),
    active: true,
  });
  const action = useAction();
  const ready =
    !!order.certificate_id &&
    [
      "issued",
      "completed",
      "deploying",
      "deploy_failed",
      "rolled_back",
    ].includes(order.status);
  return (
    <Panel title="인증서 배포">
      <Notice
        text={action.message || targets.error || agents.error || jobs.error}
      />
      {ready && (
        <>
          <div className="flex flex-wrap items-end gap-3">
            <Field label="기존 배포 대상">
              <select
                className={inputClass}
                value={selected}
                onChange={(e) => setSelected(e.target.value)}
              >
                <option value="">대상을 선택하세요</option>
                {targets.data?.items
                  .filter((t) => t.active)
                  .map((t) => (
                    <option key={t.id} value={t.id}>
                      {agents.data?.items.find((a) => a.id === t.agent_id)
                        ?.name ?? t.agent_id}{" "}
                      · {t.kind} · {t.cert_path}
                    </option>
                  ))}
              </select>
            </Field>
            <Button
              disabled={action.busy || !selected}
              onClick={() =>
                void action.run(async () => {
                  await send(`/issuance/orders/${order.id}/deploy`, {
                    deployment_target_id: selected,
                  });
                  await jobs.reload();
                  action.setMessage("배포 작업을 등록했습니다.");
                })
              }
            >
              배포
            </Button>
            <Button variant="outline" onClick={() => setCreate(!create)}>
              새 대상 등록
            </Button>
          </div>
          {create && (
            <form
              className="space-y-4 rounded-lg border p-4"
              onSubmit={(e) => {
                e.preventDefault();
                void action.run(async () => {
                  const v = await send<{ target: Target }>(
                    "/deployment/targets",
                    target,
                  );
                  await targets.reload();
                  setSelected(v.target.id!);
                  setCreate(false);
                });
              }}
            >
              <div className="grid gap-3 sm:grid-cols-2">
                <Field label="에이전트">
                  <select
                    required
                    className={inputClass}
                    value={target.agent_id}
                    onChange={(e) =>
                      setTarget({ ...target, agent_id: e.target.value })
                    }
                  >
                    <option value="">선택하세요</option>
                    {agents.data?.items
                      .filter((a) => a.status === "active")
                      .map((a) => (
                        <option key={a.id} value={a.id}>
                          {a.name}
                        </option>
                      ))}
                  </select>
                </Field>
                <Field label="서비스">
                  <select
                    className={inputClass}
                    value={target.service_unit}
                    onChange={(e) =>
                      setTarget({
                        ...target,
                        kind: e.target.value === "nginx" ? "nginx" : "apache",
                        service_unit: e.target.value as Target["service_unit"],
                      })
                    }
                  >
                    <option value="nginx">nginx</option>
                    <option value="apache2">Apache (apache2)</option>
                    <option value="httpd">Apache (httpd)</option>
                  </select>
                </Field>
                {(
                  [
                    ["cert_path", "인증서 파일 절대 경로"],
                    ["key_path", "개인키 파일 절대 경로"],
                    ["chain_path", "별도 체인 파일 (Apache, 선택)"],
                    ["verify_host", "검증 IP (루프백)"],
                    ["sni_name", "검증 SNI 이름"],
                  ] as const
                ).map(([k, label]) => (
                  <Field key={k} label={label}>
                    <input
                      className={inputClass}
                      required={k !== "chain_path" && k !== "sni_name"}
                      value={target[k]}
                      onChange={(e) =>
                        setTarget({ ...target, [k]: e.target.value })
                      }
                    />
                  </Field>
                ))}
                <Field label="검증 포트">
                  <input
                    className={inputClass}
                    type="number"
                    min={1}
                    max={65535}
                    required
                    value={target.verify_port}
                    onChange={(e) =>
                      setTarget({
                        ...target,
                        verify_port: Number(e.target.value),
                      })
                    }
                  />
                </Field>
              </div>
              <p className="text-xs text-muted-foreground">
                기존 인증서와 키가 있는 파일 경로를 지정하세요. 에이전트의 허용
                디렉토리 안에 있어야 합니다. 적용은 서비스 reload로 수행합니다.
              </p>
              <Button type="submit" disabled={action.busy}>
                대상 저장
              </Button>
            </form>
          )}
        </>
      )}
      {!ready && (
        <p className="text-sm text-muted-foreground">
          인증서 발급 완료 후 배포할 수 있습니다.
        </p>
      )}
      {jobs.data?.items.map((j) => (
        <div key={j.id} className="space-y-3 rounded-lg border p-4">
          <p className="font-medium">
            {JOB_STATUS[j.status]}{" "}
            <span className="text-xs font-normal text-muted-foreground">
              {new Date(j.created_at).toLocaleString("ko-KR")}
            </span>
          </p>
          <Notice text={j.error} />
          <ol className="space-y-2 text-sm">
            {j.steps.map((s, i) => (
              <li key={i}>
                {s.ok ? "✓" : "×"} {STEP[s.name] ?? s.name} · {s.message}
              </li>
            ))}
          </ol>
          {j.observed_fingerprint && (
            <p className="break-all text-xs text-muted-foreground">
              확인된 SHA-256: {j.observed_fingerprint}
            </p>
          )}
        </div>
      ))}
    </Panel>
  );
}
