/**
 * What a diagram is, independently of what draws it (DIAG-001).
 *
 * This is the whole contract between the file on disk, the canvas and the exporters. React Flow is
 * the editing surface and its shapes appear nowhere here on purpose: a document written today has
 * to open in a version that swaps the canvas, and the SVG exporter has to produce the same picture
 * without loading a canvas at all.
 *
 * Two rules keep it that way:
 * - **A node carries a `kind`, never an appearance.** `decision`, not "a rotated square". What a
 *   kind looks like is `stencils.ts` and `shapes.ts`, so changing the look of every gateway is one
 *   edit and breaks no saved file.
 * - **Colour is a token, never a colour.** A hex value baked into a document would be unreadable in
 *   whichever theme it was not picked in. `fills.ts` resolves a token per theme, once.
 */
import type { StencilId } from "./stencils";

/**
 * The format marker, written into every document and checked when one is read.
 *
 * Versioned from the first release rather than when it first hurts: a document is a file in the
 * user's repository, so by the time the format needs to change there are diagrams in git history
 * that predate the change, and something has to be able to tell them apart.
 */
export const DIAGRAM_SCHEMA = "codeflow.diagram/1";

/** Where a connector meets a shape. Four sides, so an edge is stable when a node is dragged. */
export type PortId = "top" | "right" | "bottom" | "left";

export const PORT_IDS: readonly PortId[] = ["top", "right", "bottom", "left"];

/**
 * A node's fill, as a name.
 *
 * Deliberately few. A palette with thirty entries produces diagrams nobody can read, and every
 * entry is one more thing to resolve in two themes and in an export that has neither.
 */
export type FillToken = "none" | "neutral" | "accent" | "success" | "warning" | "danger";

export const FILL_TOKENS: readonly FillToken[] = [
  "none",
  "neutral",
  "accent",
  "success",
  "warning",
  "danger",
];

/**
 * What a connector looks like — which, in Mermaid, is all a connector is.
 *
 * Four, because Mermaid writes four: `-->`, `---`, `-.->` and `==>`. The previous catalogue had
 * six, with meanings attached (association, include, extend, generalisation), and those meanings
 * have nowhere to live in a `.mmd`: Mermaid has no stereotypes and no hollow triangle. A
 * relationship that needs a name now carries it as the connector's label, written out — which is
 * also the form a model reads without being taught anything.
 */
export type EdgeKind = "arrow" | "line" | "dotted" | "thick";

export const EDGE_KINDS: readonly EdgeKind[] = ["arrow", "line", "dotted", "thick"];

export interface DiagramNode {
  id: string;
  kind: StencilId;
  x: number;
  y: number;
  width: number;
  height: number;
  /** What is written in, under or beside the shape — `stencils.ts` decides which. */
  text: string;
  fill: FillToken;
  /**
   * The container this node sits in, or `null`.
   *
   * Only a stencil marked `container` may be named here, and `x`/`y` are relative to it — which is
   * what makes dragging a pool carry its tasks along. A node whose parent no longer exists is
   * released to the top level on load rather than dropped (DIAG-003).
   */
  parent: string | null;
}

/**
 * A connector.
 *
 * **No ports.** In Mermaid a connector is `a --> b` and nothing else — which side of each shape it
 * leaves from is the renderer's business. Storing a side would mean a line of metadata per edge
 * for something the drawing can work out, so `routing.chooseSides` works it out: the pair of sides
 * that gives the shortest sensible run between the two boxes, recomputed whenever either moves.
 * Dragging a connector is therefore shape to shape, not port to port.
 */
export interface DiagramEdge {
  id: string;
  from: string;
  to: string;
  kind: EdgeKind;
  /** The text on the connector — "yes"/"no" on a decision's branches, most of the time. */
  label: string;
}

export interface DiagramDocument {
  schema: typeof DIAGRAM_SCHEMA;
  title: string;
  nodes: readonly DiagramNode[];
  edges: readonly DiagramEdge[];
}

/** A document with nothing in it: what the canvas renders before one is open. */
export const emptyDocument: DiagramDocument = {
  schema: DIAGRAM_SCHEMA,
  title: "",
  nodes: [],
  edges: [],
};

export function newDocument(title: string): DiagramDocument {
  return { schema: DIAGRAM_SCHEMA, title, nodes: [], edges: [] };
}

/**
 * The next free node id, as `n1`, `n2`, …
 *
 * Short and Mermaid-safe, because these are written into the document as its node names — a UUID
 * would be legal Mermaid and unreadable, and reading a diagram is the whole point of the format.
 *
 * **Never renumbered.** An id is assigned when a shape is created and kept until it is deleted, so
 * inserting a shape at the top of a diagram does not rewrite every line below it in the next git
 * diff. The counter simply skips what is taken.
 */
export function nextNodeId(doc: DiagramDocument): string {
  const taken = new Set(doc.nodes.map((node) => node.id));
  for (let n = 1; ; n += 1) {
    const id = `n${n}`;
    if (!taken.has(id)) return id;
  }
}

/** The next free edge id, as `e1`, `e2`, … Not written to the document; edges are anonymous in
 * Mermaid, so these are renumbered in document order whenever a file is read. */
export function nextEdgeId(doc: DiagramDocument): string {
  const taken = new Set(doc.edges.map((edge) => edge.id));
  for (let n = 1; ; n += 1) {
    const id = `e${n}`;
    if (!taken.has(id)) return id;
  }
}

export function nodeById(doc: DiagramDocument, id: string): DiagramNode | null {
  return doc.nodes.find((node) => node.id === id) ?? null;
}

/**
 * The box every node fits in, in document coordinates, or `null` for an empty document.
 *
 * Children are resolved through their parents, because a child's `x`/`y` are relative — a pool at
 * (400, 0) holding a task at (20, 20) reaches 420, and an export that fitted to the raw numbers
 * would cut it off.
 */
export function documentBounds(doc: DiagramDocument): {
  x: number;
  y: number;
  width: number;
  height: number;
} | null {
  if (doc.nodes.length === 0) return null;

  let minX = Number.POSITIVE_INFINITY;
  let minY = Number.POSITIVE_INFINITY;
  let maxX = Number.NEGATIVE_INFINITY;
  let maxY = Number.NEGATIVE_INFINITY;

  for (const node of doc.nodes) {
    const at = absolutePosition(doc, node);
    minX = Math.min(minX, at.x);
    minY = Math.min(minY, at.y);
    maxX = Math.max(maxX, at.x + node.width);
    maxY = Math.max(maxY, at.y + node.height);
  }

  return { x: minX, y: minY, width: maxX - minX, height: maxY - minY };
}

/**
 * Where a node actually is, with every parent's offset added in.
 *
 * Walks up rather than recursing so a document whose parents form a cycle — which a hand-edited
 * file can contain — terminates instead of blowing the stack. The guard counts steps rather than
 * tracking visited ids because the depth that matters is two (pool, lane) and the cost of being
 * wrong is an exported picture, not corruption.
 */
export function absolutePosition(doc: DiagramDocument, node: DiagramNode): { x: number; y: number } {
  let x = node.x;
  let y = node.y;
  let parentId = node.parent;

  for (let step = 0; parentId !== null && step < MAX_NESTING; step += 1) {
    const parent = nodeById(doc, parentId);
    if (parent === null) break;
    x += parent.x;
    y += parent.y;
    parentId = parent.parent;
  }

  return { x, y };
}

/** How deep containers may nest before a document is assumed to be malformed: pool → lane → node. */
export const MAX_NESTING = 8;
