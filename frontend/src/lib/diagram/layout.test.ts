import { describe, expect, test } from "vitest";
import { placeUnpositioned } from "./layout";
import type { DiagramNode } from "./model";
import { stencilById, type StencilId } from "./stencils";

function node(id: string, kind: StencilId = "rect", parent: string | null = null): DiagramNode {
  const stencil = stencilById(kind);
  return {
    id,
    kind,
    x: 0,
    y: 0,
    width: stencil.width,
    height: stencil.height,
    text: id,
    fill: stencil.defaultFill,
    parent,
  };
}

/** The boxes two nodes occupy, for the overlap assertions. */
function overlap(a: DiagramNode, b: DiagramNode): boolean {
  return a.x < b.x + b.width && b.x < a.x + a.width && a.y < b.y + b.height && b.y < a.y + a.height;
}

describe("a file with no positions at all", () => {
  test("a chain is laid out down the page, in order", () => {
    const nodes = [node("a"), node("b"), node("c")];

    placeUnpositioned(nodes, [{ from: "a", to: "b" }, { from: "b", to: "c" }], new Set());

    expect(nodes[0]!.y).toBeLessThan(nodes[1]!.y);
    expect(nodes[1]!.y).toBeLessThan(nodes[2]!.y);
    expect(nodes.some((one, i) => nodes.some((two, j) => i !== j && overlap(one, two)))).toBe(false);
  });

  test("two branches off a decision sit side by side, under it", () => {
    const nodes = [node("check", "diam"), node("yes"), node("no")];

    placeUnpositioned(nodes, [{ from: "check", to: "yes" }, { from: "check", to: "no" }], new Set());

    const [check, yes, no] = nodes as [DiagramNode, DiagramNode, DiagramNode];
    expect(yes.y).toBe(no.y);
    expect(yes.y).toBeGreaterThan(check.y);
    expect(yes.x).toBeLessThan(no.x);
  });

  test("nodes nothing connects still get a row of their own rather than a pile", () => {
    const nodes = [node("a"), node("b"), node("c")];

    placeUnpositioned(nodes, [], new Set());

    expect(new Set(nodes.map((one) => one.x)).size).toBe(3);
    expect(new Set(nodes.map((one) => one.y)).size).toBe(1);
  });

  // A flowchart is allowed to loop; a rank is not. The layout has to terminate and produce
  // something readable anyway, with the back edge simply pointing up the page.
  test("a cycle is laid out instead of hanging", () => {
    const nodes = [node("a"), node("b"), node("c")];
    const edges = [{ from: "a", to: "b" }, { from: "b", to: "c" }, { from: "c", to: "a" }];

    placeUnpositioned(nodes, edges, new Set());

    expect(nodes.some((one, i) => nodes.some((two, j) => i !== j && overlap(one, two)))).toBe(false);
  });

  test("a node that points at itself is not a rank of its own", () => {
    const nodes = [node("a"), node("b")];

    placeUnpositioned(nodes, [{ from: "a", to: "a" }, { from: "a", to: "b" }], new Set());

    expect(nodes[1]!.y).toBeGreaterThan(nodes[0]!.y);
  });
});

describe("a drawing that is already laid out", () => {
  test("what the file positioned is never moved", () => {
    const placed = { ...node("a"), x: 900, y: 40 };
    const nodes = [placed, node("b")];

    placeUnpositioned(nodes, [{ from: "a", to: "b" }], new Set(["a"]));

    expect(placed.x).toBe(900);
    expect(placed.y).toBe(40);
  });

  // Typing a shape into the text pane of a diagram that is already arranged: it has to appear
  // somewhere visible and empty, not on top of the work.
  test("a shape added by hand lands below what is already drawn", () => {
    const placed = { ...node("a"), x: 40, y: 500 };
    const fresh = node("b");

    placeUnpositioned([placed, fresh], [{ from: "a", to: "b" }], new Set(["a"]));

    expect(fresh.y).toBeGreaterThan(placed.y + placed.height);
    expect(overlap(placed, fresh)).toBe(false);
  });
});

describe("containers", () => {
  test("children are placed inside their container, in absolute coordinates", () => {
    const pool = { ...node("pool", "subgraph"), x: 200, y: 300 };
    const task = node("task", "rect", "pool");

    placeUnpositioned([pool, task], [], new Set(["pool"]));

    expect(task.x).toBeGreaterThan(pool.x);
    expect(task.y).toBeGreaterThan(pool.y);
    expect(task.x + task.width).toBeLessThanOrEqual(pool.x + pool.width);
  });

  test("a container the file did not size grows to hold what is in it", () => {
    const pool = node("pool", "subgraph");
    const wide = [node("a", "rect", "pool"), node("b", "rect", "pool"), node("c", "rect", "pool")];
    const edges = [{ from: "a", to: "b" }, { from: "b", to: "c" }];

    placeUnpositioned([pool, ...wide], edges, new Set());

    const last = wide[2]!;
    expect(pool.height).toBeGreaterThanOrEqual(last.y - pool.y + last.height);
    for (const child of wide) {
      expect(child.x).toBeGreaterThanOrEqual(pool.x);
      expect(child.y + child.height).toBeLessThanOrEqual(pool.y + pool.height);
    }
  });

  test("a container inside a container ends up inside both", () => {
    const outer = node("outer", "subgraph");
    const inner = node("inner", "subgraph", "outer");
    const leaf = node("leaf", "rect", "inner");

    placeUnpositioned([outer, inner, leaf], [], new Set());

    expect(inner.x).toBeGreaterThanOrEqual(outer.x);
    expect(leaf.x).toBeGreaterThanOrEqual(inner.x);
    expect(leaf.y).toBeGreaterThanOrEqual(inner.y);
    expect(inner.y).toBeGreaterThanOrEqual(outer.y);
  });

  test("a parent that does not exist is treated as no parent, not as a crash", () => {
    const orphan = node("a", "rect", "nowhere");

    expect(() => placeUnpositioned([orphan], [], new Set())).not.toThrow();
    expect(orphan.x).toBeGreaterThan(0);
  });
});

test("a document that is fully positioned is left exactly as it was", () => {
  const nodes = [
    { ...node("a"), x: 7, y: 11 },
    { ...node("b"), x: 13, y: 17 },
  ];

  placeUnpositioned(nodes, [{ from: "a", to: "b" }], new Set(["a", "b"]));

  expect(nodes.map((one) => [one.x, one.y])).toEqual([
    [7, 11],
    [13, 17],
  ]);
});

test("every coordinate is a whole number, because that is what a pos comment stores", () => {
  const nodes = [node("a", "diam"), node("b"), node("c", "circle")];

  placeUnpositioned(nodes, [{ from: "a", to: "b" }, { from: "a", to: "c" }], new Set());

  for (const one of nodes) {
    expect(Number.isInteger(one.x)).toBe(true);
    expect(Number.isInteger(one.y)).toBe(true);
  }
});
