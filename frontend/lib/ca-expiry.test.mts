/**
 * CA 만료 임박순 정렬 회귀 테스트 — `node --test` (Node 내장 러너).
 * 실행: npm test
 *
 * 정렬은 **형제 노드 사이에서만** 일어난다 — 트리의 계층(부모-자식) 자체는
 * 서버가 만든 그대로 둔다. 계층을 평탄화하거나 재구성하면 발급 관계가 사라진다.
 */

import { strict as assert } from "node:assert";
import { test } from "node:test";

import { expiryBuckets, sortCANodesByExpiry } from "./ca-expiry.ts";

/** 테스트용 최소 노드 — 실제 IssuerCANode의 부분집합이다. */
interface Node {
  name: string;
  not_after: string | null;
  children: Node[];
}

function node(name: string, notAfter: string | null, children: Node[] = []): Node {
  return { name, not_after: notAfter, children };
}

const names = (nodes: Node[]) => nodes.map((n) => n.name);

test("형제 노드를 만료 임박 순(오름차순)으로 세운다", () => {
  const roots = [
    node("late", "2030-01-01T00:00:00Z"),
    node("soon", "2026-09-01T00:00:00Z"),
    node("mid", "2027-05-01T00:00:00Z"),
  ];
  assert.deepEqual(names(sortCANodesByExpiry(roots)), ["soon", "mid", "late"]);
});

test("하위 노드도 같은 규칙으로 정렬한다 (계층은 그대로)", () => {
  const roots = [
    node("root", "2031-01-01T00:00:00Z", [
      node("intermediate-late", "2029-01-01T00:00:00Z"),
      node("intermediate-soon", "2026-10-01T00:00:00Z"),
    ]),
  ];
  const sorted = sortCANodesByExpiry(roots);
  assert.deepEqual(names(sorted), ["root"]);
  assert.deepEqual(names(sorted[0]!.children), [
    "intermediate-soon",
    "intermediate-late",
  ]);
});

test("계층이 더 깊어져도(N계층) 각 층의 형제만 정렬한다", () => {
  const roots = [
    node("root", "2031-01-01T00:00:00Z", [
      node("mid", "2030-01-01T00:00:00Z", [
        node("leaf-ca-late", "2029-01-01T00:00:00Z"),
        node("leaf-ca-soon", "2026-01-01T00:00:00Z"),
      ]),
    ]),
  ];
  const sorted = sortCANodesByExpiry(roots);
  const grandchildren = sorted[0]!.children[0]!.children;
  assert.deepEqual(names(grandchildren), ["leaf-ca-soon", "leaf-ca-late"]);
});

test("만료일이 없는 노드(미보유 상위)는 맨 뒤로 간다", () => {
  const roots = [
    node("unknown", null),
    node("known", "2030-01-01T00:00:00Z"),
  ];
  assert.deepEqual(names(sortCANodesByExpiry(roots)), ["known", "unknown"]);
});

test("만료일이 같으면 서버가 준 순서를 유지한다(안정 정렬)", () => {
  const same = "2028-01-01T00:00:00Z";
  const roots = [node("a", same), node("b", same), node("c", same)];
  assert.deepEqual(names(sortCANodesByExpiry(roots)), ["a", "b", "c"]);
});

test("입력 배열·노드를 변형하지 않는다", () => {
  const roots = [
    node("late", "2030-01-01T00:00:00Z", [
      node("y", "2029-01-01T00:00:00Z"),
      node("x", "2027-01-01T00:00:00Z"),
    ]),
    node("soon", "2026-01-01T00:00:00Z"),
  ];
  sortCANodesByExpiry(roots);
  assert.deepEqual(names(roots), ["late", "soon"]);
  assert.deepEqual(names(roots[0]!.children), ["y", "x"]);
});

test("파싱할 수 없는 날짜는 만료일 없음과 같이 뒤로 보낸다", () => {
  const roots = [
    node("broken", "not-a-date"),
    node("known", "2030-01-01T00:00:00Z"),
  ];
  assert.deepEqual(names(sortCANodesByExpiry(roots)), ["known", "broken"]);
});

// ── 만료 분포 구간화 ──────────────────────────────────────────────────────

test("누적 분포를 겹치지 않는 구간으로 되돌린다", () => {
  // 누적: 만료 3 / D-7 이내 5 / D-30 이내 12 / D-90 이내 20, 모집단 50.
  const b = expiryBuckets(
    { expired: 3, within_7d: 5, within_30d: 12, within_90d: 20 },
    50,
  );
  assert.deepEqual(b, { expired: 3, d7: 5, d30: 7, d90: 8, rest: 27 });
});

test("구간 합이 모집단과 같다 — 누적값을 그대로 쌓으면 넘친다", () => {
  const expiry = { expired: 3, within_7d: 5, within_30d: 12, within_90d: 20 };
  const total = 30;
  const b = expiryBuckets(expiry, total);
  assert.equal(b.expired + b.d7 + b.d30 + b.d90 + b.rest, total);
  // 회귀 방어: 누적값을 그대로 더하면 같은 인증서를 여러 번 세어 모집단을 넘는다.
  assert.ok(
    expiry.expired +
      expiry.within_7d +
      expiry.within_30d +
      expiry.within_90d >
      total,
  );
});

test("임박 인증서가 없으면 전부 여유 구간이다", () => {
  const b = expiryBuckets(
    { expired: 0, within_7d: 0, within_30d: 0, within_90d: 0 },
    8,
  );
  assert.deepEqual(b, { expired: 0, d7: 0, d30: 0, d90: 0, rest: 8 });
});

test("누적값이 역전되거나 모집단을 넘어도 음수 폭을 만들지 않는다", () => {
  const b = expiryBuckets(
    { expired: 4, within_7d: 9, within_30d: 6, within_90d: 6 },
    5,
  );
  assert.deepEqual(b, { expired: 4, d7: 9, d30: 0, d90: 0, rest: 0 });
});
