"use client";
import { useEffect, useState } from "react";
import { useAuth, isSystemAdministrator } from "@/lib/auth";
import { Button } from "@/components/ui/button";
import {
  Field,
  Notice,
  Panel,
  inputClass,
  useResource,
  useAction,
} from "@/components/issuance-common";
import { send, type Agent } from "@/lib/issuance";
export default function Page() {
  const { user } = useAuth();
  const [now, setNow] = useState(0);
  useEffect(() => {
    const timer = setInterval(() => setNow(Date.now()), 1000);
    return () => clearInterval(timer);
  }, []);
  const allowed = isSystemAdministrator(user);
  const { data, error, reload } = useResource<{ items: Agent[] }>(
    allowed ? "/agents" : null,
    15000,
  );
  const [name, setName] = useState("");
  const [enrollment, setEnrollment] = useState<{
    enrollment_token: string;
    ca_fingerprint: string;
    gateway_port: string;
  } | null>(null);
  const action = useAction();
  if (!user) return <p>불러오는 중…</p>;
  if (!allowed)
    return <Notice text="시스템 관리자만 에이전트를 관리할 수 있습니다." />;
  return (
    <div className="max-w-4xl space-y-5">
      <h1 className="text-2xl font-semibold">배포 에이전트</h1>
      <p className="text-sm text-muted-foreground">
        Linux nginx·Apache 서버에 설치하는 배포 전용 에이전트입니다.
      </p>
      <Notice text={action.message || error} />
      <Panel title="에이전트 등록">
        <form
          className="flex items-end gap-3"
          onSubmit={(e) => {
            e.preventDefault();
            void action.run(async () => {
              setEnrollment(await send("/agents/tokens", { name }));
              setName("");
              await reload();
            });
          }}
        >
          <Field label="에이전트 이름">
            <input
              required
              className={inputClass}
              value={name}
              onChange={(e) => setName(e.target.value)}
            />
          </Field>
          <Button disabled={action.busy} type="submit">
            등록 토큰 발급
          </Button>
        </form>
        {enrollment && (
          <div className="space-y-3 rounded-lg border p-4">
            <p className="text-sm">
              등록 토큰은 지금 한 번만 표시되며 24시간 후 만료됩니다. 대상
              서버의 권한 0600 파일에 저장해 사용하세요.
            </p>
            <code className="block break-all">
              {enrollment.enrollment_token}
            </code>
            <Button
              variant="outline"
              onClick={() =>
                void action.run(async () => {
                  await navigator.clipboard.writeText(
                    enrollment.enrollment_token,
                  );
                  action.setMessage("등록 토큰을 복사했습니다.");
                })
              }
            >
              토큰 복사
            </Button>
            <pre className="overflow-x-auto rounded bg-muted p-3 text-xs">{`sudo autocert-agent enroll --server https://SERVER:${enrollment.gateway_port} --token-file /root/agent-token --ca-fingerprint ${enrollment.ca_fingerprint}`}</pre>
            <p className="text-xs text-muted-foreground">
              SERVER를 관리 서버 주소로 바꾸세요. 설치·실행 방법:
              docs/manual/AGENT.md
            </p>
            <Button variant="outline" onClick={() => setEnrollment(null)}>
              토큰 닫기
            </Button>
          </div>
        )}
      </Panel>
      <Panel title="등록 현황">
        {data?.items.map((a) => (
          <div
            key={a.id}
            className="flex items-center justify-between gap-3 border-b pb-3"
          >
            <div>
              <p>{a.name}</p>
              <p className="text-sm text-muted-foreground">
                {a.status === "pending"
                  ? "등록 대기"
                  : a.status === "disabled"
                    ? "비활성"
                    : a.last_seen_at && now - Date.parse(a.last_seen_at) < 90000
                      ? "온라인"
                      : "오프라인"}{" "}
                · 마지막 통신{" "}
                {a.last_seen_at
                  ? new Date(a.last_seen_at).toLocaleString("ko-KR")
                  : "없음"}
              </p>
            </div>
            <Button
              variant="outline"
              disabled={a.status === "disabled" || action.busy}
              onClick={() =>
                void action.run(async () => {
                  await send(
                    `/agents/${a.id}`,
                    { status: "disabled" },
                    "PATCH",
                  );
                  await reload();
                })
              }
            >
              비활성
            </Button>
          </div>
        ))}
        {data?.items.length === 0 && (
          <p className="text-muted-foreground">등록된 에이전트가 없습니다.</p>
        )}
      </Panel>
    </div>
  );
}
