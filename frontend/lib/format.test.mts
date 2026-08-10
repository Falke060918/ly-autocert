/**
 * 잔여 일수 회귀 테스트 — `node --test` (Node 내장 러너, 추가 의존성 없음).
 * 실행: npm test
 */

import { strict as assert } from "node:assert";
import { test } from "node:test";

import { dday } from "./format.ts";

const HOUR = 60 * 60 * 1000;
const DAY = 24 * HOUR;

/** 지금으로부터 offsetMs 뒤(음수면 과거) 시각의 ISO 문자열. */
function at(offsetMs: number): string {
  return new Date(Date.now() + offsetMs).toISOString();
}

// 백엔드 탐지 쿼리는 floor(extract(epoch FROM (not_after - now())) / 86400)다.
// 한쪽만 올림하면 메일 제목 D-30과 화면 D-31이 항상 어긋난다.
test("잔여 일수는 백엔드 floor 기준과 같다", () => {
  for (const offsetMs of [30.5 * DAY, 7.9 * DAY, 90 * DAY, 12 * HOUR, 1 * DAY]) {
    const backend = Math.floor(offsetMs / 1000 / 86400); // SQL 식 그대로
    assert.equal(dday(at(offsetMs)).days, backend, `offsetMs=${offsetMs}`);
  }
});

test("잔여 30.5일은 D-30이다 (D-31 아님)", () => {
  const d = dday(at(30.5 * DAY));
  assert.equal(d.days, 30);
  assert.equal(d.label, "D-30");
  assert.equal(d.tone, "soon");
  assert.equal(d.expired, false);
});

// -0 회귀: Math.ceil(-0.25)는 -0이고 -0 < 0은 false라 만료 직후 24시간이
// "만료"가 아니라 "만료 임박"으로, 라벨은 D-0으로 보였다.
test("만료 직후 24시간 안쪽도 만료로 판정한다", () => {
  for (const since of [1 * HOUR, 6 * HOUR, 23.9 * HOUR]) {
    const d = dday(at(-since));
    assert.equal(d.expired, true, `since=${since}`);
    assert.equal(d.tone, "urgent", `since=${since}`);
    assert.equal(d.label, "D+0", `since=${since}`);
  }
});

test("만료 경과 라벨은 실제 경과 일수다", () => {
  assert.equal(dday(at(-36 * HOUR)).label, "D+1");
  assert.equal(dday(at(-10 * DAY)).label, "D+10");
});

test("잔여 12시간은 아직 유효하고 D-0이다", () => {
  const d = dday(at(12 * HOUR));
  assert.equal(d.expired, false);
  assert.equal(d.days, 0);
  assert.equal(d.label, "D-0");
  assert.equal(d.tone, "urgent");
});
