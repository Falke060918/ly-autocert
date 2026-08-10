"use client";

/**
 * 인벤토리 신선도 표기 — "이 정보를 마지막으로 실제 확인한 게 언제인가".
 *
 * 경고 임계는 **고정값 2단계**다: 7일 초과 앰버 / 30일 초과 레드.
 * 서버의 재스캔 간격을 프론트로 내리지 않는다 — 재스캔이 꺼진 설치에서도
 * "이 정보가 얼마나 낡았나"는 의미가 통해야 하는데, 간격을 기준으로 삼으면
 * 기능이 꺼진 순간 경고가 사라져 가장 낡은 데이터를 볼 때 아무 표시도 안 뜬다
 * (06-rescan-and-replacement 11.3).
 *
 * 상대 시각 포맷터는 알림 벨의 것을 재사용한다 — 두 번째 포맷터를 만들지 않는다.
 */

import { relativeTime } from "@/components/notification-bell";
import { cn } from "@/lib/utils";

const DAY_MS = 24 * 60 * 60 * 1000;

/** 앰버 전환 기준(일). */
export const FRESHNESS_WARN_DAYS = 7;
/** 레드 전환 기준(일). */
export const FRESHNESS_STALE_DAYS = 30;

export type FreshnessTone = "fresh" | "warn" | "stale" | "never";

/** iso 시각의 신선도 등급. now는 호출자가 넘긴다(렌더 순수성 — useTicker 사용). */
export function freshnessTone(iso: string | null, now: number): FreshnessTone {
  if (!iso) return "never";
  const age = now - new Date(iso).getTime();
  if (!Number.isFinite(age)) return "never";
  if (age > FRESHNESS_STALE_DAYS * DAY_MS) return "stale";
  if (age > FRESHNESS_WARN_DAYS * DAY_MS) return "warn";
  return "fresh";
}

/**
 * "3시간 전" / "12일 전" — 낡을수록 색이 올라간다.
 * 한 번도 확인된 적 없으면 값 자체가 없다는 사실을 문구로 드러낸다("—"로 숨기지 않는다).
 */
export function FreshnessText({
  iso,
  now,
  className,
}: {
  iso: string | null;
  now: number;
  className?: string;
}) {
  const tone = freshnessTone(iso, now);
  if (tone === "never" || !iso) {
    return (
      <span className={cn("text-xs text-muted-foreground/60", className)}>
        확인 기록 없음
      </span>
    );
  }
  return (
    <span
      className={cn(
        "font-mono text-xs tabular-nums",
        tone === "fresh" && "text-muted-foreground/70",
        tone === "warn" && "text-amber-700",
        tone === "stale" && "text-red-600",
        className,
      )}
      title={
        tone === "fresh"
          ? undefined
          : `마지막 확인 이후 ${FRESHNESS_WARN_DAYS}일이 지났습니다. 이 위치의 정보는 현재 상태가 아닐 수 있습니다.`
      }
    >
      {relativeTime(iso, now)}
    </span>
  );
}
