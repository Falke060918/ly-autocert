"use client";

import * as React from "react";
import { useRouter } from "next/navigation";

import { Alert, AlertDescription } from "@/components/ui/alert";
import { Button } from "@/components/ui/button";
import {
  Card,
  CardContent,
  CardDescription,
  CardHeader,
  CardTitle,
} from "@/components/ui/card";
import { Input } from "@/components/ui/input";
import { Label } from "@/components/ui/label";
import { Skeleton } from "@/components/ui/skeleton";
import { ApiError, createInitialAdmin, fetchSetupStatus } from "@/lib/api";

/** 백엔드 auth.MinPasswordLength와 같은 값 — 서버가 최종 판정한다. */
const MIN_PASSWORD_LENGTH = 12;

/**
 * 초기 설정 화면. 사용자가 0명일 때만 의미가 있으며, 계정이 하나라도 있으면
 * 서버가 상태 조회에서 setup_required=false를 주고 생성 요청은 409로 막는다.
 * 이 화면의 확인은 안내일 뿐이고 실제 관문은 서버다.
 */
export default function SetupPage() {
  const router = useRouter();
  const [checking, setChecking] = React.useState(true);
  const [username, setUsername] = React.useState("");
  const [password, setPassword] = React.useState("");
  const [confirm, setConfirm] = React.useState("");
  const [error, setError] = React.useState<string | null>(null);
  const [submitting, setSubmitting] = React.useState(false);

  React.useEffect(() => {
    let alive = true;
    fetchSetupStatus()
      .then((status) => {
        if (!alive) return;
        // 이미 끝난 설정이면 이 화면은 존재 이유가 없다.
        if (!status.setup_required) {
          router.replace("/login");
          return;
        }
        setChecking(false);
      })
      .catch(() => {
        if (!alive) return;
        // 서버에 닿지 못하면 판단할 수 없다 — 폼을 열되 실패 사유를 알린다.
        setChecking(false);
        setError(
          "서버 상태를 확인하지 못했습니다. 백엔드가 기동 중인지 확인한 뒤 다시 시도하십시오.",
        );
      });
    return () => {
      alive = false;
    };
  }, [router]);

  async function onSubmit(e: React.FormEvent) {
    e.preventDefault();
    setError(null);
    if (password !== confirm) {
      setError("비밀번호와 확인 값이 서로 다릅니다.");
      return;
    }
    setSubmitting(true);
    try {
      await createInitialAdmin(username, password);
      router.replace("/login?setup=complete");
    } catch (err) {
      setSubmitting(false);
      if (err instanceof ApiError && err.status === 409) {
        setError(
          "이미 관리자 계정이 있어 초기 설정이 종료되었습니다. 로그인 화면으로 이동합니다.",
        );
        setTimeout(() => router.replace("/login"), 2000);
      } else if (err instanceof ApiError && err.status === 400) {
        setError(
          `계정을 만들지 못했습니다. 계정명이 비어 있지 않은지, 비밀번호가 ${MIN_PASSWORD_LENGTH}자 이상이며 계정명과 다른지 확인하십시오.`,
        );
      } else {
        setError("계정 생성에 실패했습니다. 서버 상태를 확인한 뒤 다시 시도하십시오.");
      }
    }
  }

  return (
    <div className="flex min-h-screen items-center justify-center bg-muted/40 p-6">
      <Card className="w-full max-w-md">
        <CardHeader>
          <div className="mb-2 flex items-center gap-2.5">
            <div className="grid size-8 place-items-center rounded-md bg-primary font-mono text-xs font-semibold text-primary-foreground">
              IA
            </div>
            <div className="leading-tight">
              <div className="text-sm font-semibold tracking-tight">
                INHERIT AutoCert
              </div>
              <div className="text-xs text-muted-foreground">
                인증서 라이프사이클 관리
              </div>
            </div>
          </div>
          <div className="font-mono text-xs uppercase tracking-widest text-muted-foreground">
            초기 설정
          </div>
          <CardTitle>관리자 계정 만들기</CardTitle>
          <CardDescription>
            등록된 계정이 아직 없습니다. 여기서 만드는 계정이 첫 시스템 관리자가
            되며, 이후 이 화면은 다시 열리지 않습니다.
          </CardDescription>
        </CardHeader>
        <CardContent>
          {checking ? (
            <div className="space-y-4" aria-busy>
              <Skeleton className="h-9 w-full" />
              <Skeleton className="h-9 w-full" />
              <Skeleton className="h-9 w-full" />
            </div>
          ) : (
            <form onSubmit={onSubmit} className="space-y-4">
              <div className="space-y-2">
                <Label htmlFor="username">아이디</Label>
                <Input
                  id="username"
                  autoComplete="username"
                  autoFocus
                  required
                  value={username}
                  onChange={(e) => setUsername(e.target.value)}
                />
              </div>
              <div className="space-y-2">
                <Label htmlFor="password">비밀번호</Label>
                <Input
                  id="password"
                  type="password"
                  autoComplete="new-password"
                  required
                  minLength={MIN_PASSWORD_LENGTH}
                  value={password}
                  onChange={(e) => setPassword(e.target.value)}
                />
                <p className="text-xs text-muted-foreground">
                  최소 {MIN_PASSWORD_LENGTH}자, 아이디와 다른 값이어야 합니다.
                </p>
              </div>
              <div className="space-y-2">
                <Label htmlFor="confirm">비밀번호 확인</Label>
                <Input
                  id="confirm"
                  type="password"
                  autoComplete="new-password"
                  required
                  value={confirm}
                  onChange={(e) => setConfirm(e.target.value)}
                />
              </div>
              {error && (
                <Alert variant="destructive">
                  <AlertDescription>{error}</AlertDescription>
                </Alert>
              )}
              <Button type="submit" className="w-full" disabled={submitting}>
                {submitting ? "생성 중…" : "관리자 계정 만들기"}
              </Button>
            </form>
          )}
        </CardContent>
      </Card>
    </div>
  );
}
