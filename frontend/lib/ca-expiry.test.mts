/**
 * CA 만료 임박순 정렬 회귀 테스트 — `node --test` (Node 내장 러너).
 * 실행: npm test
 *
 * 정렬은 **형제 노드 사이에서만** 일어난다 — 트리의 계층(부모-자식) 자체는
 * 서버가 만든 그대로 둔다. 계층을 평탄화하거나 재구성하면 발급 관계가 사라진다.
 */

import { strict as assert } from "node:assert";
import { test } from "node:test";

import { sortCANodesByExpiry } from "./ca-expiry.ts";

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
