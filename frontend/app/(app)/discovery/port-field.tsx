"use client";

/**
 * 포트 사양 입력 — 자유 텍스트 하나 + 프리셋 칩.
 *
 * 클라이언트에서 포트를 파싱하지 않는다. 예전 구현은 `split(",")→parseInt`라
 * `8443-9443`이 조용히 `8443` 하나로 잘려나갔다(설계 9.3). 문법 검증은 전적으로
 * 서버 파서의 400 메시지에 맡긴다 — 파서를 둘 두면 반드시 갈라진다.
 * 개수 계산도 같은 이유로 서버 미리보기(POST /discovery/scans/preview)가 정본이다.
 */

import * as React from "react";

import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { Label } from "@/components/ui/label";
import { formatCount, specHasPreset, STARTTLS_NOTICE, type PortPreset } from "@/lib/sweeps";

/** 프리셋 이름별 한국어 라벨. 개수·설명은 서버 응답에서 온다. */
const PRESET_LABELS: Record<string, string> = {
  common: "자주 쓰는 포트",
  web: "웹만",
  all: "전 포트",
};

/** 칩 표시 순서. 목록에 없는 새 프리셋은 뒤에 붙는다. */
const PRESET_ORDER = ["common", "web", "all"];

function orderPresets(presets: PortPreset[]): PortPreset[] {
  return [...presets].sort((a, b) => {
    const ia = PRESET_ORDER.indexOf(a.name);
    const ib = PRESET_ORDER.indexOf(b.name);
    return (ia < 0 ? PRESET_ORDER.length : ia) - (ib < 0 ? PRESET_ORDER.length : ib);
  });
}

export function PortSpecField({
  id,
  value,
  onChange,
  presets,
}: {
  id: string;
  value: string;
  onChange: (next: string) => void;
  presets: PortPreset[];
}) {
  const inputRef = React.useRef<HTMLInputElement>(null);
  const token = value.trim().toLowerCase();
  const selected = presets.find((p) => p.name === token)?.name ?? null;
  // all은 이제 어느 폼에서도 거부되지 않는다 — 규모가 크면 서버가 스윕으로
  // 승격한다. 남은 안내는 STARTTLS 포트에서 인증서를 못 얻는다는 사실뿐이다.
  const hasAll = specHasPreset(value, "all");

  return (
    <div className="space-y-2">
      <Label htmlFor={id}>포트</Label>
      {presets.length > 0 && (
        <div className="flex flex-wrap gap-1.5">
          {orderPresets(presets).map((p) => (
            <Button
              key={p.name}
              type="button"
              size="xs"
              variant={selected === p.name ? "secondary" : "outline"}
              aria-pressed={selected === p.name}
              title={p.description}
              onClick={() => onChange(p.name)}
            >
              {PRESET_LABELS[p.name] ?? p.name}
              <span className="font-mono text-muted-foreground tabular-nums">
                {formatCount(p.count)}개
              </span>
            </Button>
          ))}
          <Button
            type="button"
            size="xs"
            variant={selected == null ? "secondary" : "outline"}
            aria-pressed={selected == null}
            onClick={() => {
              if (selected != null) onChange("");
              inputRef.current?.focus();
            }}
          >
            직접 입력
          </Button>
        </div>
      )}
      <Input
        id={id}
        ref={inputRef}
        spellCheck={false}
        className="font-mono text-xs"
        placeholder="common"
        value={value}
        onChange={(e) => onChange(e.target.value)}
      />
      <p className="text-xs text-muted-foreground">
        <code className="font-mono">443,8443</code> ·{" "}
        <code className="font-mono">8000-9000</code> ·{" "}
        <code className="font-mono">common</code> 을 쉼표로 조합할 수 있습니다.
      </p>
      {hasAll && <p className="text-xs text-warn">{STARTTLS_NOTICE}</p>}
    </div>
  );
}
