"use client";

/**
 * Discovery 화면 — 왼쪽 "검색 실행"(단일 폼) + 오른쪽 "작업 목록"(통합).
 *
 * 예전 구성은 카드 두 개에 탭이 각각 [스캔 | 전 포트 스윕], [스캔 | 스윕 | 주기
 * 재스캔]으로 붙어 있었다. 사용자가 발주 전에 "이건 단발인가 스윕인가"를 먼저
 * 골라야 했고, 시작한 작업을 보려면 같은 판단을 한 번 더 해야 했다.
 *
 * 지금은 규모를 서버가 보고 정하고(자동 승격), 작업 목록도 서버가 세 축을 합쳐
 * 내려주므로(GET /discovery/activity) 화면에 고를 것이 남지 않았다.
 * 주기 재스캔 현황은 이 화면의 관심사(사람이 낸 발주)가 아니라 설정 화면으로 옮긴다.
 */

import * as React from "react";

import { Card } from "@/components/ui/card";
import { canManageInventory, useAuth } from "@/lib/auth";
import { listSensors, type ScanAccepted, type Sensor } from "@/lib/api";
import { getScanSuggestions, type ScanSuggestionsResponse } from "@/lib/scans";
import {
  getDiscoveryDefaults,
  listPortPresets,
  type DiscoveryDefaults,
  type PortPreset,
} from "@/lib/sweeps";

import { ActivityList } from "./activity-list";
import { ScanForm } from "./scan-form";

/** 발주·중복 안내로 강조한 행의 강조를 이 시간 뒤에 걷는다. */
const HIGHLIGHT_MS = 5000;

export default function DiscoveryPage() {
  const { user } = useAuth();
  const canManage = canManageInventory(user);

  const [sensors, setSensors] = React.useState<Sensor[]>([]);
  const [presets, setPresets] = React.useState<PortPreset[]>([]);
  const [defaults, setDefaults] = React.useState<DiscoveryDefaults | null>(null);
  const [suggestions, setSuggestions] =
    React.useState<ScanSuggestionsResponse | null>(null);

  const [reloadToken, setReloadToken] = React.useState(0);
  const [highlightId, setHighlightId] = React.useState<string | null>(null);
  const [notice, setNotice] = React.useState<string | null>(null);

  // 포트 프리셋의 단일 진실원천은 서버다 — 이름·개수를 화면에 하드코딩하지 않는다.
  React.useEffect(() => {
    let cancelled = false;
    listPortPresets()
      .then((res) => {
        if (!cancelled) setPresets(res.items);
      })
      .catch(() => {
        // 프리셋을 못 받으면 칩만 사라지고 자유 텍스트 입력은 그대로 쓸 수 있다.
      });
    return () => {
      cancelled = true;
    };
  }, []);

  // 기본값·상한은 서버 설정(SCAN_SWEEP_*)이 정본이다 — 화면은 상수를 들지 않는다.
  React.useEffect(() => {
    let cancelled = false;
    getDiscoveryDefaults()
      .then((res) => {
        if (!cancelled) setDefaults(res);
      })
      .catch(() => {
        // 기본값을 못 받아도 폼 입력·제출은 그대로 된다(값은 서버가 정한다).
      });
    return () => {
      cancelled = true;
    };
  }, []);

  // 센서 목록은 실행기 선택과 대기 작업의 "누구를 기다리는가" 표시에 모두 쓴다.
  // 비활성 센서도 담아둔다 — 대기가 안 풀리는 원인이 바로 그것일 수 있다.
  React.useEffect(() => {
    let cancelled = false;
    listSensors()
      .then((res) => {
        if (!cancelled) setSensors(res.items);
      })
      .catch(() => {
        // 센서 목록을 못 가져와도 서버 직접 실행과 작업 조회는 가능하다.
      });
    return () => {
      cancelled = true;
    };
  }, []);

  // 대역 제안은 입력 보조일 뿐이다 — 못 받으면 칩 없이 직접 입력만 남는다.
  React.useEffect(() => {
    if (!canManage) return;
    let cancelled = false;
    getScanSuggestions()
      .then((res) => {
        if (!cancelled) setSuggestions(res);
      })
      .catch(() => {});
    return () => {
      cancelled = true;
    };
  }, [canManage]);

  // 강조는 잠시 뒤 걷는다(스크롤은 목록 쪽이 한다).
  React.useEffect(() => {
    if (!highlightId) return;
    const timer = setTimeout(() => setHighlightId(null), HIGHLIGHT_MS);
    return () => clearTimeout(timer);
  }, [highlightId]);

  /**
   * 발주 직후. 규모가 커서 승격됐으면 job_id가 아예 없으므로 **mode로 분기**해
   * sweep_id를 추적한다 — job_id를 필수로 보면 여기서 추적이 끊긴다.
   */
  const handleCreated = React.useCallback((accepted: ScanAccepted) => {
    setReloadToken((n) => n + 1);
    if (accepted.mode === "sweep") {
      setHighlightId(accepted.sweep_id ?? null);
      setNotice(
        accepted.promoted
          ? "요청 규모가 커서 전 포트 스윕으로 전환해 시작했습니다 — 작업 목록에서 진행 상황을 볼 수 있습니다."
          : null,
      );
    } else {
      setHighlightId(accepted.job_id ?? null);
      setNotice(null);
    }
  }, []);

  /** 중복 409 — 새 작업 대신 이미 돌고 있는 기존 작업으로 시선을 옮긴다. */
  const handleDuplicate = React.useCallback((jobId: string) => {
    setReloadToken((n) => n + 1);
    setHighlightId(jobId);
    setNotice(null);
  }, []);

  return (
    <div className="mx-auto max-w-[1400px] space-y-4">
      {/* 제목은 화면에서 걷어냈지만 문서 구조(h1)는 스크린리더용으로 남긴다. */}
      <h1 className="sr-only">Discovery</h1>

      <div className="grid items-start gap-4 lg:grid-cols-[340px_minmax(0,1fr)]">
        <ScanForm
          canManage={canManage}
          sensors={sensors}
          presets={presets}
          defaults={defaults}
          suggestions={suggestions}
          onCreated={handleCreated}
          onDuplicate={handleDuplicate}
        />

        <Card className="gap-0 overflow-hidden py-0">
          {notice && (
            <p className="border-b bg-muted/40 px-4 py-2 text-xs text-muted-foreground">
              {notice}
            </p>
          )}
          <ActivityList
            sensors={sensors}
            canManage={canManage}
            reloadToken={reloadToken}
            highlightId={highlightId}
          />
        </Card>
      </div>
    </div>
  );
}
