import { describe, expect, test } from "vitest";
import { emitMermaid } from "../diagram/mermaid/emit";
import { parseMermaid } from "../diagram/mermaid/parse";
import { flow, symbolRef } from "./fixtures";
import { diagramName, flowToDiagram, MAX_COLUMNS, MAX_DIAGRAM_SYMBOLS, type DiagramLabels } from "./toDiagram";
import type { CodemapFlow, CodemapFlowNode } from "../../types/domain";

const labels: DiagramLabels = {
  lanes: { ts: "Frontend", go: "Backend (Go)", sql: "Database" },
  edge: (kind) => kind,
  cut: (shown, total) => `${shown} of ${total}`,
};

describe("a flow as a diagram", () => {
  test("each side of the code gets a lane, and every symbol sits in its own", () => {
    const doc = flowToDiagram(flow("ts:src/app.ts#save"), "save flow", labels);

    const lanes = doc.nodes.filter((n) => n.kind === "subgraph").map((n) => n.text);
    expect(lanes).toEqual(["Frontend", "Backend (Go)", "Database"]);
    const table = doc.nodes.find((n) => n.text === "items")!;
    expect(table.kind).toBe("cyl");
    expect(table.parent).toBe("lane_sql");
    const endpoint = doc.nodes.find((n) => n.text === "item_save")!;
    expect(endpoint.kind).toBe("stadium");
    expect(endpoint.parent).toBe("lane_go");
    expect(doc.edges.map((e) => [e.kind, e.label])).toEqual([
      ["thick", "cross"],
      ["arrow", "write"],
    ]);
  });

  // The proof that the editor can open it: its own writer, then its own reader, lose nothing.
  test("the editor's own Mermaid writer and reader agree on it", () => {
    const doc = flowToDiagram(flow("ts:src/app.ts#save"), "save flow", labels);

    const parsed = parseMermaid(emitMermaid(doc));
    expect(parsed.ok).toBe(true);
    if (!parsed.ok) return;
    expect(parsed.value.dropped).toBe(0);
    // The reader lists each container with its contents, so the order differs; the shapes do not.
    const shape = (nodes: typeof doc.nodes) =>
      nodes.map((n) => [n.id, n.kind, n.parent, n.text].join("|")).sort();
    expect(shape(parsed.value.doc.nodes)).toEqual(shape(doc.nodes));
    expect(parsed.value.doc.edges).toHaveLength(doc.edges.length);
    expect(parsed.value.doc.title).toBe("save flow");
  });

  // A chain must look like a chain and a fan like a fan: siblings share a row, and their parent
  // sits above them, so no arrow runs behind a sibling.
  test("each level of the flow is a row, its symbols side by side", () => {
    const leaf = (i: number): CodemapFlowNode => ({
      symbol: symbolRef(`ts:src/f.ts#f${i}`, { name: `f${i}` }),
      via: "call",
      file: "src/f.ts",
      line: i + 1,
      children: [],
      repeat: false,
      truncated: false,
    });
    const fan: CodemapFlow = {
      root: { ...flow("ts:root#r").root, children: Array.from({ length: MAX_COLUMNS + 2 }, (_, i) => leaf(i)) },
      steps: MAX_COLUMNS + 2,
      truncated: false,
    };

    const doc = flowToDiagram(fan, "fan", labels);
    const at = (text: string) => doc.nodes.find((n) => n.text === text)!;
    const root = doc.nodes.find((n) => n.id === "n1")!;
    expect(at("f0").y).toBe(at("f1").y);
    expect(at("f1").x).toBeGreaterThan(at("f0").x);
    expect(at("f0").y).toBeGreaterThan(root.y);
    // The level wraps after MAX_COLUMNS, into a row of its own below.
    expect(at(`f${MAX_COLUMNS}`).y).toBeGreaterThan(at("f0").y);
    expect(at(`f${MAX_COLUMNS}`).x).toBe(at("f0").x);
    // And the lane is wide enough to hold the widest row.
    const lane = doc.nodes.find((n) => n.id === "lane_ts")!;
    expect(at(`f${MAX_COLUMNS - 1}`).x + at(`f${MAX_COLUMNS - 1}`).width).toBeLessThanOrEqual(lane.width);
  });

  test("a symbol reached twice is drawn once", () => {
    const shared = { ...flow("ts:a#a").root.children[0]! };
    const twice: CodemapFlow = {
      root: { ...flow("ts:a#a").root, children: [shared, { ...shared, repeat: true, children: [] }] },
      steps: 3,
      truncated: false,
    };
    const doc = flowToDiagram(twice, "t", labels);
    expect(doc.nodes.filter((n) => n.text === "item_save")).toHaveLength(1);
  });

  test("a flow too large to draw is cut, and says so", () => {
    const children: CodemapFlowNode[] = Array.from({ length: MAX_DIAGRAM_SYMBOLS + 10 }, (_, i) => ({
      symbol: symbolRef(`ts:src/f.ts#f${i}`),
      via: "call",
      file: "src/f.ts",
      line: i + 1,
      children: [],
      repeat: false,
      truncated: false,
    }));
    const big: CodemapFlow = { root: { ...flow("ts:root#r").root, children }, steps: children.length, truncated: false };

    const doc = flowToDiagram(big, "big", labels);
    const symbols = doc.nodes.filter((n) => n.kind !== "subgraph" && n.id !== "cut");
    expect(symbols).toHaveLength(MAX_DIAGRAM_SYMBOLS);
    expect(doc.nodes.find((n) => n.id === "cut")?.text).toBe(`${MAX_DIAGRAM_SYMBOLS} of ${MAX_DIAGRAM_SYMBOLS + 11}`);
  });

  test("the file is named after the symbol", () => {
    expect(diagramName("finance.FinanceService.CreateExpense")).toBe("codemap/CreateExpense-flow");
    expect(diagramName('diagram.Register("diagram_list_documents")')).toBe("codemap/Register-flow");
    expect(diagramName("frontend/src/App.tsx")).toBe("codemap/App-tsx-flow");
  });
});
