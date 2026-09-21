/**
 * The JSON side: an export format, and the way back in from the version before Mermaid (DIAG-010).
 *
 * This used to be the format on disk. It is not any more — `mermaid/emit.ts` writes what a document
 * is — but it stays for two jobs:
 *
 * - **Export.** A diagram as data, for anything that would rather read fields than parse a flowchart.
 * - **Import, including migration.** A `.diagram.json` written by the previous build is read here,
 *   and its shapes and connectors are translated to the ones Mermaid can say. That translation
 *   loses things — the three BPMN gateways become one diamond, a generalisation becomes an arrow —
 *   and it says how many when it does, rather than quietly flattening someone's diagram.
 *
 * Everything a file can arrive as is checked. A `node.width` of `null` is an `NaN` in a transform,
 * and an `NaN` transform is a blank screen with no error anywhere.
 */
import {
  DIAGRAM_SCHEMA,
  EDGE_KINDS,
  FILL_TOKENS,
  type DiagramDocument,
  type DiagramEdge,
  type DiagramNode,
  type EdgeKind,
  type FillToken,
} from "./model";
import { isContainer, isStencilId, stencilById, type StencilId } from "./stencils";

/** Why a file is not a diagram at all. A discriminated reason, so the caller translates it. */
export type ParseFailure = "notJson" | "notDiagram" | "unknownSchema";

export interface ParsedDocument {
  doc: DiagramDocument;
  /** How many shapes or connectors could not be kept as written. */
  dropped: number;
}

export type ParseResult = { ok: true; value: ParsedDocument } | { ok: false; reason: ParseFailure };

/** The smallest a shape may be and still be grabbable. Also the floor a malformed size lands on. */
export const MIN_SIZE = 16;

/**
 * What the shapes of the pre-Mermaid catalogue became.
 *
 * Two kinds of entry, and the difference is what gets reported:
 *
 * - **A rename.** `process` was always a rectangle and is now called `rect`. Nothing is lost, so
 *   nothing is counted — telling somebody their diagram changed when it did not is noise.
 * - **A collapse** (`lossy`). Several figures became one, because Mermaid has only the one: the
 *   three BPMN gateways are all a diamond, a pool and a lane and a system boundary are all a
 *   container. The drawing still says what it said; the distinction between them does not survive,
 *   and that is worth saying before the file is written back in the new catalogue.
 */
const LEGACY_SHAPES: Record<string, { kind: StencilId; lossy?: true }> = {
  rectangle: { kind: "rect" },
  process: { kind: "rect" },
  "bpmn.task": { kind: "rounded" },
  terminator: { kind: "stadium" },
  ellipse: { kind: "stadium", lossy: true },
  "uml.usecase": { kind: "stadium", lossy: true },
  diamond: { kind: "diam" },
  decision: { kind: "diam" },
  "bpmn.exclusive": { kind: "diam", lossy: true },
  "bpmn.parallel": { kind: "diam", lossy: true },
  "bpmn.inclusive": { kind: "diam", lossy: true },
  parallelogram: { kind: "lean-r" },
  io: { kind: "lean-r" },
  cylinder: { kind: "cyl" },
  document: { kind: "doc" },
  "bpmn.data": { kind: "doc", lossy: true },
  "bpmn.annotation": { kind: "brace" },
  note: { kind: "brace", lossy: true },
  subprocess: { kind: "div-rect" },
  "bpmn.subprocess": { kind: "div-rect" },
  connector: { kind: "sm-circ" },
  "bpmn.start": { kind: "circle" },
  "bpmn.intermediate": { kind: "dbl-circ" },
  "bpmn.end": { kind: "fr-circ" },
  "uml.actor": { kind: "person" },
  "bpmn.pool": { kind: "subgraph", lossy: true },
  "bpmn.lane": { kind: "subgraph", lossy: true },
  "uml.boundary": { kind: "subgraph", lossy: true },
};

/** What the six connectors of the pre-Mermaid catalogue became. */
const LEGACY_EDGES: Record<string, EdgeKind> = {
  flow: "arrow",
  association: "line",
  message: "dotted",
  include: "dotted",
  extend: "dotted",
  generalization: "arrow",
};

function isRecord(value: unknown): value is Record<string, unknown> {
  return typeof value === "object" && value !== null && !Array.isArray(value);
}

/** A finite number, or the fallback. Catches `null`, `"120"`, `NaN` and `Infinity` alike. */
function num(value: unknown, fallback: number): number {
  return typeof value === "number" && Number.isFinite(value) ? value : fallback;
}

function str(value: unknown): string {
  return typeof value === "string" ? value : "";
}

function oneOf<T extends string>(value: unknown, allowed: readonly T[], fallback: T): T {
  return typeof value === "string" && (allowed as readonly string[]).includes(value)
    ? (value as T)
    : fallback;
}

/** The shape a file names, translated if it is one this version no longer draws. */
function shapeOf(raw: string): { kind: StencilId; lossy: boolean } | null {
  if (isStencilId(raw)) return { kind: raw, lossy: false };

  const legacy = LEGACY_SHAPES[raw];
  if (legacy !== undefined) return { kind: legacy.kind, lossy: legacy.lossy === true };

  return null;
}

function parseNode(raw: unknown): { node: DiagramNode; lossy: boolean } | null {
  if (!isRecord(raw)) return null;

  const id = str(raw.id);
  const shape = shapeOf(str(raw.kind));
  // A shape from neither catalogue is dropped rather than substituted: a document from a newer
  // version would otherwise open with every new figure silently turned into a rectangle, and
  // saving it would make that permanent.
  if (id === "" || shape === null) return null;

  const stencil = stencilById(shape.kind);

  return {
    lossy: shape.lossy,
    node: {
      id,
      kind: shape.kind,
      x: num(raw.x, 0),
      y: num(raw.y, 0),
      width: Math.max(MIN_SIZE, num(raw.width, stencil.width)),
      height: Math.max(MIN_SIZE, num(raw.height, stencil.height)),
      text: str(raw.text),
      fill: oneOf<FillToken>(raw.fill, FILL_TOKENS, stencil.defaultFill),
      parent: typeof raw.parent === "string" && raw.parent !== "" ? raw.parent : null,
    },
  };
}

function parseEdge(raw: unknown): DiagramEdge | null {
  if (!isRecord(raw)) return null;

  const id = str(raw.id);
  const from = str(raw.from);
  const to = str(raw.to);
  if (id === "" || from === "" || to === "") return null;

  const written = str(raw.kind);
  const kind = isEdgeKind(written) ? written : (LEGACY_EDGES[written] ?? "arrow");

  // `fromPort`/`toPort` are read and thrown away: sides are chosen from where the shapes are now
  // (`routing.chooseSides`), so a stored side would only be a stale opinion.
  return { id, from, to, kind, label: str(raw.label) };
}

function isEdgeKind(value: string): value is EdgeKind {
  return (EDGE_KINDS as readonly string[]).includes(value);
}

/**
 * Makes the parsed lists internally consistent.
 *
 * Three repairs, each for something a real file can contain: a duplicated id, an edge whose
 * endpoint is gone, and a parent that is missing or is not a container. None of them loses anything
 * a person drew — the shape is still there, it just stops claiming to be inside something that is
 * not.
 */
function reconcile(
  nodes: DiagramNode[],
  edges: DiagramEdge[],
): { nodes: DiagramNode[]; edges: DiagramEdge[]; dropped: number } {
  const seen = new Set<string>();
  const kept: DiagramNode[] = [];
  let dropped = 0;

  for (const node of nodes) {
    if (seen.has(node.id)) {
      dropped += 1;
      continue;
    }
    seen.add(node.id);
    kept.push(node);
  }

  const byId = new Map(kept.map((node) => [node.id, node]));
  const adopted = kept.map((node) => {
    if (node.parent === null) return node;
    const parent = byId.get(node.parent);
    if (parent === undefined || parent.id === node.id || !isContainer(parent.kind)) {
      return { ...node, parent: null };
    }
    return node;
  });

  const edgeIds = new Set<string>();
  const keptEdges: DiagramEdge[] = [];
  for (const edge of edges) {
    if (edgeIds.has(edge.id) || !byId.has(edge.from) || !byId.has(edge.to)) {
      dropped += 1;
      continue;
    }
    edgeIds.add(edge.id);
    keptEdges.push(edge);
  }

  return { nodes: adopted, edges: keptEdges, dropped };
}

export function parseDocument(text: string): ParseResult {
  let raw: unknown;
  try {
    raw = JSON.parse(text);
  } catch {
    return { ok: false, reason: "notJson" };
  }

  if (!isRecord(raw)) return { ok: false, reason: "notDiagram" };
  if (typeof raw.schema !== "string") return { ok: false, reason: "notDiagram" };
  if (raw.schema !== DIAGRAM_SCHEMA) return { ok: false, reason: "unknownSchema" };

  const rawNodes = Array.isArray(raw.nodes) ? raw.nodes : [];
  const rawEdges = Array.isArray(raw.edges) ? raw.edges : [];

  const nodes: DiagramNode[] = [];
  let lost = 0;
  for (const entry of rawNodes) {
    const parsed = parseNode(entry);
    if (parsed === null) lost += 1;
    else {
      nodes.push(parsed.node);
      // A renamed shape is the same drawing and is not reported. One that collapsed into a figure
      // it now shares with others is, before the document is written back that way.
      if (parsed.lossy) lost += 1;
    }
  }

  const edges: DiagramEdge[] = [];
  for (const entry of rawEdges) {
    const edge = parseEdge(entry);
    if (edge === null) lost += 1;
    else edges.push(edge);
  }

  const reconciled = reconcile(nodes, edges);

  return {
    ok: true,
    value: {
      doc: {
        schema: DIAGRAM_SCHEMA,
        title: str(raw.title),
        nodes: reconciled.nodes,
        edges: reconciled.edges,
      },
      dropped: lost + reconciled.dropped,
    },
  };
}

/**
 * The document as JSON.
 *
 * Indented, field order fixed, and with a trailing newline — this is an export somebody may well
 * put in git beside the `.mmd`, and a diff on a save that changed nothing helps nobody.
 */
export function serializeDocument(doc: DiagramDocument): string {
  const ordered = {
    schema: doc.schema,
    title: doc.title,
    nodes: doc.nodes.map((node) => ({
      id: node.id,
      kind: node.kind satisfies StencilId,
      x: Math.round(node.x),
      y: Math.round(node.y),
      width: Math.round(node.width),
      height: Math.round(node.height),
      text: node.text,
      fill: node.fill,
      parent: node.parent,
    })),
    edges: doc.edges.map((edge) => ({
      id: edge.id,
      from: edge.from,
      to: edge.to,
      kind: edge.kind,
      label: edge.label,
    })),
  };

  return `${JSON.stringify(ordered, null, 2)}\n`;
}
