import { BAND, stencilById, type StencilId } from "../diagram/stencils";
import { DIAGRAM_SCHEMA, type DiagramDocument, type DiagramEdge, type DiagramNode, type EdgeKind, type FillToken } from "../diagram/model";
import type { CodemapEdgeKind, CodemapFlow, CodemapFlowNode, CodemapKind, CodemapSymbolRef } from "../../types/domain";
import { shortName } from "./labels";

/**
 * A code map flow as a diagram the editor opens (CODEMAP-016).
 *
 * The flow becomes a graph — each symbol once, however many branches reach it — laid out in lanes,
 * one per side of the code: the frontend, Go, the database. Each row is one level of the flow,
 * shared by every lane, and the symbols of a level sit side by side: an arrow always leads down to
 * the next level and never runs behind a sibling, so the picture cannot suggest a chain the code
 * does not have. The document goes through the editor's own Mermaid writer, so what is saved is an
 * ordinary `.mmd` the editor, and any Mermaid viewer, can read.
 */

/** The most symbols a diagram carries; a larger flow is cut and says so. */
export const MAX_DIAGRAM_SYMBOLS = 60;

/** How many symbols of one level sit side by side in a lane before the level wraps. */
export const MAX_COLUMNS = 5;

type Lane = "ts" | "go" | "sql";

const LANES: readonly Lane[] = ["ts", "go", "sql"];

const NODE_WIDTH = 200;
const COLUMN_GAP = 32;
const ROW = 96;
const PAD = 24;
const LANE_GAP = 80;

const SHAPES: Record<CodemapKind, StencilId> = {
  func: "rect",
  method: "rect",
  type: "rect",
  interface: "hex",
  interface_method: "hex",
  component: "rounded",
  hook: "rounded",
  test: "rect",
  test_file: "doc",
  module: "doc",
  table: "cyl",
  endpoint: "stadium",
};

const FILLS: Record<Lane, FillToken> = { ts: "accent", go: "success", sql: "warning" };

const ARROWS: Record<CodemapEdgeKind, EdgeKind> = {
  call: "arrow",
  ref: "dotted",
  render: "arrow",
  dispatch: "dotted",
  cross: "thick",
  read: "arrow",
  write: "arrow",
  mention: "dotted",
};

function laneOf(symbol: CodemapSymbolRef): Lane {
  return symbol.lang === "sql" ? "sql" : symbol.lang === "go" ? "go" : "ts";
}

export interface DiagramLabels {
  /** The lane titles, already translated. */
  lanes: Record<Lane, string>;
  /** What an edge says, already translated. */
  edge: (kind: CodemapEdgeKind) => string;
  /** The note a cut flow carries, already translated. */
  cut: (shown: number, total: number) => string;
}

/** Builds the diagram of a flow. */
export function flowToDiagram(flow: CodemapFlow, title: string, labels: DiagramLabels): DiagramDocument {
  // Breadth-first, so a symbol's level is the first one the flow reaches it at.
  const order: { symbol: CodemapSymbolRef; level: number }[] = [];
  const index = new Map<string, number>();
  const links: { from: string; to: string; via: CodemapEdgeKind }[] = [];
  const seenLinks = new Set<string>();
  let total = 0;

  const visit = (symbol: CodemapSymbolRef, level: number) => {
    if (!index.has(symbol.id)) {
      total++;
      if (order.length < MAX_DIAGRAM_SYMBOLS) {
        index.set(symbol.id, order.length);
        order.push({ symbol, level });
      }
    }
  };
  visit(flow.root.symbol, 0);
  const queue: { step: CodemapFlowNode; level: number }[] = [{ step: flow.root, level: 0 }];
  while (queue.length > 0) {
    const { step, level } = queue.shift()!;
    for (const child of step.children) {
      visit(child.symbol, level + 1);
      if (child.via !== "") {
        const key = `${step.symbol.id}|${child.symbol.id}|${child.via}`;
        if (!seenLinks.has(key)) {
          seenLinks.add(key);
          links.push({ from: step.symbol.id, to: child.symbol.id, via: child.via });
        }
      }
      queue.push({ step: child, level: level + 1 });
    }
  }

  const ids = new Map<string, string>();
  order.forEach(({ symbol }, i) => ids.set(symbol.id, `n${i + 1}`));

  // Each symbol's place within its lane and level, and how many rows each level needs: as many as
  // its most crowded lane wraps to.
  const levels = Math.max(0, ...order.map((o) => o.level)) + 1;
  const crowd = new Map<string, number>();
  const column = order.map(({ symbol, level }) => {
    const key = `${laneOf(symbol)}|${level}`;
    const i = crowd.get(key) ?? 0;
    crowd.set(key, i + 1);
    return i;
  });
  const rowsAt = Array.from({ length: levels }, (_, level) =>
    Math.max(1, ...LANES.map((lane) => Math.ceil((crowd.get(`${lane}|${level}`) ?? 0) / MAX_COLUMNS))),
  );
  const firstRow = rowsAt.map((_, level) => rowsAt.slice(0, level).reduce((sum, rows) => sum + rows, 0));
  const rows = rowsAt.reduce((sum, r) => sum + r, 0);

  const used = LANES.filter((lane) => order.some(({ symbol }) => laneOf(symbol) === lane));
  const columnsOf = (lane: Lane) =>
    Math.min(MAX_COLUMNS, Math.max(1, ...Array.from({ length: levels }, (_, level) => crowd.get(`${lane}|${level}`) ?? 0)));
  const laneWidth = (lane: Lane) => columnsOf(lane) * (NODE_WIDTH + COLUMN_GAP) - COLUMN_GAP + PAD * 2;
  const height = BAND + PAD * 2 + rows * ROW;
  const nodes: DiagramNode[] = [];

  let x = 40;
  for (const lane of used) {
    nodes.push({
      id: `lane_${lane}`,
      kind: "subgraph",
      x,
      y: 40,
      width: laneWidth(lane),
      height,
      text: labels.lanes[lane],
      fill: "none",
      parent: null,
    });
    x += laneWidth(lane) + LANE_GAP;
  }

  order.forEach(({ symbol, level }, i) => {
    const kind = SHAPES[symbol.kind];
    const stencil = stencilById(kind);
    const nodeHeight = Math.min(stencil.height, ROW - 16);
    const row = firstRow[level]! + Math.floor(column[i]! / MAX_COLUMNS);
    nodes.push({
      id: ids.get(symbol.id)!,
      kind,
      // Relative to the lane, as a contained node's position is.
      x: PAD + (column[i]! % MAX_COLUMNS) * (NODE_WIDTH + COLUMN_GAP),
      y: BAND + PAD + row * ROW + (ROW - 16 - nodeHeight) / 2,
      width: NODE_WIDTH,
      height: nodeHeight,
      text: shortName(symbol.name),
      fill: FILLS[laneOf(symbol)],
      parent: `lane_${laneOf(symbol)}`,
    });
  });

  const edges: DiagramEdge[] = [];
  for (const link of links) {
    const from = ids.get(link.from);
    const to = ids.get(link.to);
    if (from === undefined || to === undefined || from === to) continue;
    edges.push({ id: `e${edges.length + 1}`, from, to, kind: ARROWS[link.via], label: labels.edge(link.via) });
  }

  if (total > order.length) {
    nodes.push({
      id: "cut",
      kind: "doc",
      x: 40,
      y: 40 + height + 40,
      width: 260,
      height: 72,
      text: labels.cut(order.length, total),
      fill: "neutral",
      parent: null,
    });
  }

  return { schema: DIAGRAM_SCHEMA, title, nodes, edges };
}

/** A file name for a flow's diagram: the symbol's short name, made safe for a path. */
export function diagramName(symbolName: string): string {
  const base = shortName(symbolName)
    .replace(/\(.*$/, "")
    .replace(/[^A-Za-z0-9_-]+/g, "-")
    .replace(/^-+|-+$/g, "");
  return `codemap/${base || "flow"}-flow`;
}
