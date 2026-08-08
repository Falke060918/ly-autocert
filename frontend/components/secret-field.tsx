"use client";

/**
 * 1회만 표시되는 비밀값(센서 등록 토큰·CA fingerprint 등)의 공통 표기.
 * 서버가 해시만 보관해 다시 조회할 수 없는 값이므로, 값 옆에는 항상 복사 버튼을 둔다.
 */

import * as React from "react";
import { Check, Copy } from "lucide-react";

import { Button } from "@/components/ui/button";

/** 복사 버튼 — 클립보드 복사 후 잠깐 체크 표시로 바뀐다. */
export function CopyButton({ value, label }: { value: string; label: string }) {
  const [copied, setCopied] = React.useState(false);

  async function copy() {
    try {
      await navigator.clipboard.writeText(value);
      setCopied(true);
      setTimeout(() => setCopied(false), 1500);
    } catch {
      // 클립보드 권한이 없으면 사용자가 직접 드래그해 복사한다.
    }
  }

  return (
    <Button type="button" variant="ghost" size="icon-sm" onClick={copy}>
      {copied ? <Check className="text-emerald-600" /> : <Copy />}
      <span className="sr-only">{label} 복사</span>
    </Button>
  );
}

export function SecretField({
  label,
  value,
  mono = true,
}: {
  label: string;
  value: string;
  mono?: boolean;
}) {
  return (
    <div className="space-y-1">
      <div className="flex items-center justify-between">
        <span className="text-xs font-medium text-muted-foreground">{label}</span>
        <CopyButton value={value} label={label} />
      </div>
      <pre
        className={
          "overflow-x-auto rounded-md border bg-muted/50 p-2 text-xs break-all whitespace-pre-wrap" +
          (mono ? " font-mono" : "")
        }
      >
        {value}
      </pre>
    </div>
  );
}
