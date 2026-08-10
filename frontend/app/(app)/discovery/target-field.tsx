"use client";

/**
 * 스캔·스윕 공용 대상 입력 — textarea + 제안 칩 3종(최근 사용·알려진 호스트·설치 감지).
 *
 * 칩은 **입력 편집 보조일 뿐**이다 — 클릭은 입력란에 줄을 넣고 빼는 것 이상을 하지
 * 않고, 실제 전송값은 언제나 textarea의 원문이다(스캔 생성 API 계약 불변).
 * CIDR 형식 검사도 힌트만 낸다 — 정본 판정은 서버의 400이다. 여기서 막으면
 * 파서가 둘이 되어 반드시 갈라진다(sweep-panel.tsx PortSpecField와 같은 규칙).
 */

import * as React from "react";

import { Button } from "@/components/ui/button";
import { Label } from "@/components/ui/label";
import { Textarea } from "@/components/ui/textarea";
import type { ScanSuggestionsResponse } from "@/lib/scans";

/** 값을 줄 목록으로 — 폼 제출부와 같은 규칙(trim 후 빈 줄 제거)을 쓴다. */
export function targetLines(value: string): string[] {
  return value
    .split("\n")
    .map((s) => s.trim())
    .filter(Boolean);
}

/**
 * IPv4 또는 IPv4/프리픽스로 보이는지만 본다. 통과 못 해도 **막지 않는다** —
 * 서버가 받는 다른 표기가 있을 수 있으므로 경고 힌트 용도로만 쓴다.
 */
function looksLikeTarget(line: string): boolean {
  const m = /^(\d{1,3})\.(\d{1,3})\.(\d{1,3})\.(\d{1,3})(?:\/(\d{1,2}))?$/.exec(line);
  if (!m) return false;
  for (let i = 1; i <= 4; i++) {
    if (Number(m[i]) > 255) return false;
  }
  return !(m[5] != null && Number(m[5]) > 32);
}

/** 그룹당 칩 상한 — 제안이 폼을 밀어내지 않게 한다. 나머지는 직접 입력으로 충분하다. */
const MAX_CHIPS_PER_GROUP = 4;

export function TargetField({
  id,
  value,
  onChange,
  suggestions,
}: {
  id: string;
  value: string;
  onChange: (next: string) => void;
  /** null = 아직 못 받았거나 제안 API 미가동 — 칩 없이 직접 입력만 보인다. */
  suggestions: ScanSuggestionsResponse | null;
}) {
  const lines = targetLines(value);
  const lineSet = new Set(lines);
  const invalid = lines.filter((l) => !looksLikeTarget(l));

  // 같은 대역이 두 그룹에 있으면 앞 그룹(더 구체적인 근거)에만 보여준다.
  const groups = React.useMemo(() => {
    if (!suggestions) return [];
    const seen = new Set<string>();
    const pick = (items: string[]) => {
      const out: string[] = [];
      for (const cidr of items) {
        if (seen.has(cidr)) continue;
        seen.add(cidr);
        out.push(cidr);
        if (out.length >= MAX_CHIPS_PER_GROUP) break;
      }
      return out;
    };
    return [
      { key: "recent", label: "최근 사용", items: pick(suggestions.recent_targets) },
      {
        key: "known",
        label: "알려진 호스트",
        items: pick(suggestions.known_host_networks),
      },
      {
        key: "installed",
        label: "설치 시 감지",
        items: pick(suggestions.installed_networks),
      },
    ].filter((g) => g.items.length > 0);
  }, [suggestions]);

  /** 칩 토글 — 없으면 줄로 추가, 이미 있으면 그 줄만 제거. */
  function toggle(cidr: string) {
    if (lineSet.has(cidr)) {
      onChange(lines.filter((l) => l !== cidr).join("\n"));
    } else {
      onChange([...lines, cidr].join("\n"));
    }
  }

  return (
    <div className="space-y-2">
      <Label htmlFor={id}>
        IP / CIDR 대역 <span className="text-red-600">*</span>
      </Label>
      <Textarea
        id={id}
        required
        spellCheck={false}
        className="min-h-20 font-mono text-xs"
        placeholder={"10.0.0.5\n10.0.0.0/24"}
        value={value}
        onChange={(e) => onChange(e.target.value)}
      />
      <p className="text-xs text-muted-foreground">
        한 줄에 하나씩 · CIDR 또는 단일 IP
      </p>
      {invalid.length > 0 && (
        <p className="text-xs text-amber-700">
          형식이 확실하지 않은 항목: {invalid.join(", ")} — 최종 판정은 서버가
          합니다.
        </p>
      )}
      {suggestions &&
        (groups.length > 0 ? (
          <div className="space-y-1.5">
            {groups.map((g) => (
              <div key={g.key} className="flex flex-wrap items-center gap-1.5">
                <span className="w-16 shrink-0 text-[11px] text-muted-foreground">
                  {g.label}
                </span>
                {g.items.map((cidr) => (
                  <Button
                    key={cidr}
                    type="button"
                    size="xs"
                    variant={lineSet.has(cidr) ? "secondary" : "outline"}
                    aria-pressed={lineSet.has(cidr)}
                    className="font-mono"
                    onClick={() => toggle(cidr)}
                  >
                    {cidr}
                  </Button>
                ))}
              </div>
            ))}
          </div>
        ) : (
          <p className="text-xs text-muted-foreground">
            첫 스캔 후 최근 대역이 여기 제안됩니다.
          </p>
        ))}
    </div>
  );
}
