import { describe, expect, it } from "vitest";
import { CARD_WIDTH, COLUMN_GAP, layoutMap, type LayoutEdge, type LayoutNode } from "./layout";

const STEP = CARD_WIDTH + COLUMN_GAP;

function node(id: string, folder = false, importedBy = 0): LayoutNode {
  return { id, folder, importedBy, label: id };
}

function column(placed: Map<string, { x: number }>, id: string): number {
  const p = placed.get(id);
  if (!p) throw new Error(`${id} was not placed`);
  return p.x / STEP;
}

describe("layoutMap", () => {
  it("puts what imports to the left of what it imports", () => {
    const placed = layoutMap(
      [node("app"), node("ui"), node("lib")],
      [{ from: "app", to: "ui" }, { from: "ui", to: "lib" }, { from: "app", to: "lib" }],
    );
    expect([column(placed, "app"), column(placed, "ui"), column(placed, "lib")]).toEqual([0, 1, 2]);
  });

  it("places every card of a cycle, and still reads it left to right", () => {
    const placed = layoutMap(
      [node("a"), node("b"), node("c")],
      [{ from: "a", to: "b" }, { from: "b", to: "c" }, { from: "c", to: "a" }],
    );
    expect(placed.size).toBe(3);
    expect(new Set([column(placed, "a"), column(placed, "b"), column(placed, "c")]).size).toBe(3);
  });

  it("orders a column folders first, then the most imported, and centres it", () => {
    const placed = layoutMap([node("x.ts"), node("core", true, 3), node("lib", true, 9)], []);
    const ys = ["lib", "core", "x.ts"].map((id) => placed.get(id)!.y);
    expect(ys[0]).toBeLessThan(ys[1]!);
    expect(ys[1]).toBeLessThan(ys[2]!);
    expect(ys[0]! + ys[2]!).toBeCloseTo(0);
  });

  it("folds a column taller than its limit into the next", () => {
    const nodes = Array.from({ length: 5 }, (_, i) => node(`n${i}`));
    const placed = layoutMap(nodes, [], 2);
    expect(new Set([...placed.values()].map((p) => p.x / STEP))).toEqual(new Set([0, 1, 2]));
  });

  it("uncrosses two parallel chains", () => {
    // a→d and b→c would cross if the second column kept its natural (alphabetical) order.
    const edges: LayoutEdge[] = [{ from: "a", to: "d" }, { from: "b", to: "c" }];
    const placed = layoutMap([node("a"), node("b"), node("c"), node("d")], edges);
    expect(placed.get("a")!.y < placed.get("b")!.y).toBe(placed.get("d")!.y < placed.get("c")!.y);
  });

  it("lands the same graph the same way every time, and ignores edges to cards it does not draw", () => {
    const nodes = [node("a"), node("b"), node("c", true, 2)];
    const edges = [{ from: "a", to: "c" }, { from: "b", to: "c" }, { from: "a", to: "hidden" }];
    expect([...layoutMap(nodes, edges)]).toEqual([...layoutMap([...nodes].reverse(), [...edges].reverse())]);
  });
});
