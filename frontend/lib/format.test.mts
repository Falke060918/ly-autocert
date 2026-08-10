/**
 * 잔여 일수·DN 파싱 회귀 테스트 — `node --test` (Node 내장 러너, 추가 의존성 없음).
 * 실행: npm test
 */

import { strict as assert } from "node:assert";
import { test } from "node:test";

import { dday, dnCommonName, dnOrganization } from "./format.ts";

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

// ── DN 파싱 (RFC2253 이스케이프 인지) ────────────────────────────────────
// 구현 전 회귀: `\,`가 구분자로 오인돼 CN "Example\, Inc."가 "Example\"로 잘렸다.

test("CN의 이스케이프된 쉼표는 값의 일부다", () => {
  assert.equal(
    dnCommonName("CN=Example\\, Inc.,O=Example,C=KR"),
    "Example, Inc.",
  );
});

test("이스케이프된 백슬래시·따옴표를 언이스케이프한다", () => {
  assert.equal(dnCommonName("CN=a\\\\b,O=X"), "a\\b");
  assert.equal(dnCommonName('CN=say \\"hi\\",O=X'), 'say "hi"');
});

test("RFC1779 레거시 따옴표 값 안의 쉼표는 구분자가 아니다", () => {
  assert.equal(dnCommonName('CN="Example, Inc.",O=X'), "Example, Inc.");
});

test("16진 이스케이프(\\2c)는 해당 문자로 푼다", () => {
  assert.equal(dnCommonName("CN=a\\2c b,O=X"), "a, b");
});

test("멀티밸류 RDN(+)에서는 CN 값만 취한다", () => {
  assert.equal(dnCommonName("CN=web01+OU=Infra,O=X"), "web01");
});

test("openssl 슬래시 표기도 파싱한다", () => {
  assert.equal(dnCommonName("/C=KR/O=Example/CN=web01.example.com"), "web01.example.com");
});

test("CN이 없으면 DN 전체를 반환한다 (기존 동작 유지)", () => {
  assert.equal(dnCommonName("O=Example,C=KR"), "O=Example,C=KR");
});

test("dnOrganization은 O= 값을 뽑고 없으면 null이다", () => {
  assert.equal(dnOrganization("CN=x,O=Example CA\\, Ltd.,C=KR"), "Example CA, Ltd.");
  assert.equal(dnOrganization("CN=x,C=KR"), null);
  assert.equal(dnOrganization("CN=x,OU=Org Unit,C=KR"), null); // OU는 O가 아니다
});
