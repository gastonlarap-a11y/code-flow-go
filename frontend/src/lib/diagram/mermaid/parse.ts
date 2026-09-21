/**
 * Reading a `.mmd` back into a document (DIAG-003).
 *
 * **Written by hand, on purpose.** Mermaid publishes no parser that returns a flowchart's
 * structure: `mermaid.parse()` answers only "is this valid", `@mermaid-js/parser` does not cover
 * flowcharts yet, and `flowDb` is an internal of an unmaintained Jison grammar. Depending on any of
 * those would tie this feature to an implementation detail across versions — and would pull 2 MB of
 * renderer in for a job that is a scanner.
 *
 * So this reads the subset the editor writes, **plus the subset a person or a model would write**:
 * the classic bracket forms (`a[Text]`, `b{Decision}`) as well as the extended
 * `a@{ shape: rect, label: "…" }`, bare ids that appear only in an edge, and `subgraph` blocks.
 * Anything it cannot use is counted rather than dropped in silence.
 *
 * Line-oriented, because Mermaid is: every statement here lives on its own line.
 */
import {
  DIAGRAM_SCHEMA,
  FILL_TOKENS,
  type DiagramDocument,
  type DiagramEdge,
  type DiagramNode,
  type EdgeKind,
  type FillToken,
} from "../model";
import { placeUnpositioned } from "../layout";
import { isStencilId, stencilById, type StencilId } from "../stencils";
import { METADATA_TAG } from "./emit";

/** Why a file is not a diagram at all. A discriminated reason, so the caller translates it. */
export type ParseFailure = "notMermaid" | "notFlowchart";

export interface ParsedDocument {
  doc: DiagramDocument;
  /**
   * How many lines this version could not use as written.
   *
   * Zero for every document it wrote itself. Above zero means the file came from somewhere else and
   * says something this editor cannot draw — a shape outside the catalogue, an edge to a node that
   * was never declared. The view says so before the user saves over it.
   */
  dropped: number;
}

export type ParseResult = { ok: true; value: ParsedDocument } | { ok: false; reason: ParseFailure };

/** What a shape falls back to when the file names one this catalogue does not have. */
const FALLBACK_SHAPE: StencilId = "rect";

/** The classic bracket forms, longest first so `[[` is tried before `[`. */
const BRACKETS: readonly { open: string; close: string; shape: StencilId }[] = [
  { open: "([", close: "])", shape: "stadium" },
  { open: "[[", close: "]]", shape: "div-rect" },
  { open: "[(", close: ")]", shape: "cyl" },
  { open: "((", close: "))", shape: "circle" },
  { open: "{{", close: "}}", shape: "hex" },
  { open: "[/", close: "/]", shape: "lean-r" },
  { open: "[", close: "]", shape: "rect" },
  { open: "(", close: ")", shape: "rounded" },
  { open: "{", close: "}", shape: "diam" },
  { open: ">", close: "]", shape: "lean-r" },
];

/** Mermaid's arrows, longest first: `-.->`  must be tried before `-->` and `---`. */
const ARROWS: readonly { token: string; kind: EdgeKind }[] = [
  { token: "-.->", kind: "dotted" },
  { token: "-.-", kind: "dotted" },
  { token: "==>", kind: "thick" },
  { token: "===", kind: "thick" },
  { token: "-->", kind: "arrow" },
  { token: "---", kind: "line" },
];

/** Undoes `emit.escapeLabel`, and the entities a hand-written file is likely to use. */
export function unescapeLabel(text: string): string {
  return text
    .replaceAll(/<br\s*\/?>/gi, "\n")
    .replaceAll("#quot;", '"')
    .replaceAll("&quot;", '"')
    .replaceAll("#35;", "#");
}

function stripQuotes(text: string): string {
  const trimmed = text.trim();
  const quoted = trimmed.length >= 2 && trimmed.startsWith('"') && trimmed.endsWith('"');
  return quoted ? trimmed.slice(1, -1) : trimmed;
}

/** A node id as Mermaid writes one: letters, digits, underscores, dashes. */
const ID = /^[A-Za-z_][\w-]*/;

interface Draft {
  id: string;
  kind: StencilId;
  text: string;
  fill: FillToken | null;
  parent: string | null;
}

/** `a@{ shape: rect, label: "Text" }` — the extended form this editor writes. */
function readExtended(line: string): { id: string; kind: StencilId; text: string; known: boolean } | null {
  const match = /^([A-Za-z_][\w-]*)@\{(.*)\}\s*$/.exec(line);
  if (match === null) return null;

  const id = match[1]!;
  const body = match[2]!;
  const shape = /(?:^|,)\s*shape\s*:\s*([\w-]+)/.exec(body)?.[1] ?? "";
  const label = /(?:^|,)\s*label\s*:\s*("(?:[^"]*)"|[^,}]*)/.exec(body)?.[1] ?? "";

  return {
    id,
    kind: isStencilId(shape) ? shape : FALLBACK_SHAPE,
    text: unescapeLabel(stripQuotes(label)),
    known: isStencilId(shape),
  };
}

/** `a[Text]`, `b{Decision}`, `c((Start))` — what a person or a model writes. */
function readClassic(line: string): { id: string; kind: StencilId; text: string } | null {
  const idMatch = ID.exec(line);
  if (idMatch === null) return null;

  const id = idMatch[0];
  const rest = line.slice(id.length);

  for (const form of BRACKETS) {
    if (!rest.startsWith(form.open) || !rest.endsWith(form.close)) continue;
    const inner = rest.slice(form.open.length, rest.length - form.close.length);
    return { id, kind: form.shape, text: unescapeLabel(stripQuotes(inner)) };
  }
  return null;
}

/**
 * Splits an edge statement into its two ends.
 *
 * Returns `null` when the line holds no arrow, which is how a node declaration is told from an
 * edge: both start with an id.
 */
function readEdge(
  line: string,
): { from: string; to: string; kind: EdgeKind; label: string; left: string; right: string } | null {
  for (const arrow of ARROWS) {
    const at = line.indexOf(arrow.token);
    if (at < 0) continue;

    const left = line.slice(0, at).trim();
    let right = line.slice(at + arrow.token.length).trim();

    // `-->|label|target`, with or without quotes round the label.
    let label = "";
    if (right.startsWith("|")) {
      const end = right.indexOf("|", 1);
      if (end < 0) return null;
      label = unescapeLabel(stripQuotes(right.slice(1, end)));
      right = right.slice(end + 1).trim();
    }

    const from = ID.exec(left)?.[0];
    const to = ID.exec(right)?.[0];
    if (from === undefined || to === undefined) return null;

    // The two ends are handed back whole, not just their ids: `a[One] --> b[Two]` declares both
    // shapes as well as joining them, and it is how almost every hand-written flowchart is written.
    return { from, to, kind: arrow.kind, label, left, right };
  }
  return null;
}

/** `%% codeflow: pos n1 40 160 160 72` */
function readPosition(
  line: string,
): { id: string; x: number; y: number; width: number; height: number } | null {
  const match = new RegExp(
    `^%%\\s*${METADATA_TAG}:\\s*pos\\s+([A-Za-z_][\\w-]*)\\s+(-?\\d+)\\s+(-?\\d+)\\s+(\\d+)\\s+(\\d+)\\s*$`,
  ).exec(line);
  if (match === null) return null;

  return {
    id: match[1]!,
    x: Number(match[2]),
    y: Number(match[3]),
    width: Number(match[4]),
    height: Number(match[5]),
  };
}

export function parseMermaid(text: string): ParseResult {
  const lines = text.split(/\r?\n/);

  const header = lines.find((line) => line.trim() !== "" && !line.trim().startsWith("%%"))?.trim() ?? "";
  if (!/^(flowchart|graph)\b/i.test(header)) {
    // A `.mmd` that is a sequence diagram, a gantt or anything else is a real Mermaid file this
    // editor has no canvas for. Refused by name rather than half-read.
    return { ok: false, reason: /^\w+/.test(header) ? "notFlowchart" : "notMermaid" };
  }

  const drafts = new Map<string, Draft>();
  const order: string[] = [];
  const edges: { from: string; to: string; kind: EdgeKind; label: string }[] = [];
  const positions = new Map<string, { x: number; y: number; width: number; height: number }>();
  const classes = new Map<string, FillToken>();
  const stack: string[] = [];
  let dropped = 0;

  /** Records an id the first time it is seen, wherever it was seen. */
  const draftFor = (id: string): Draft => {
    const existing = drafts.get(id);
    if (existing !== undefined) return existing;

    const fresh: Draft = {
      id,
      kind: FALLBACK_SHAPE,
      text: "",
      fill: null,
      parent: stack.length > 0 ? stack[stack.length - 1]! : null,
    };
    drafts.set(id, fresh);
    order.push(id);
    return fresh;
  };

  for (const raw of lines) {
    const line = raw.trim();
    if (line === "") continue;

    if (line.startsWith("%%")) {
      const position = readPosition(line);
      if (position !== null) positions.set(position.id, position);
      continue;
    }

    if (/^(flowchart|graph)\b/i.test(line) || /^direction\b/i.test(line)) continue;

    if (/^subgraph\b/i.test(line)) {
      // `subgraph id ["Title"]`, `subgraph id [Title]`, or `subgraph Title`.
      const withTitle = /^subgraph\s+([A-Za-z_][\w-]*)\s*\[(.*)\]\s*$/i.exec(line);
      const bare = /^subgraph\s+([A-Za-z_][\w-]*)\s*$/i.exec(line);
      const id = withTitle?.[1] ?? bare?.[1];
      if (id === undefined) {
        dropped += 1;
        continue;
      }
      const draft = draftFor(id);
      draft.kind = "subgraph";
      draft.text = unescapeLabel(stripQuotes(withTitle?.[2] ?? ""));
      stack.push(id);
      continue;
    }

    if (/^end$/i.test(line)) {
      stack.pop();
      continue;
    }

    if (/^classDef\b/i.test(line)) continue;

    if (/^class\b/i.test(line)) {
      const match = /^class\s+([\w\-,\s]+?)\s+([\w-]+)\s*$/i.exec(line);
      if (match === null) {
        dropped += 1;
        continue;
      }
      const token = match[2]!;
      if ((FILL_TOKENS as readonly string[]).includes(token)) {
        for (const id of match[1]!.split(",")) {
          const trimmed = id.trim();
          if (trimmed !== "") classes.set(trimmed, token as FillToken);
        }
      }
      continue;
    }

    const edge = readEdge(line);
    if (edge !== null) {
      // Both ends exist from here on, even if neither was ever declared — Mermaid lets an edge
      // introduce a node, and a hand-written file usually does.
      for (const side of [edge.left, edge.right]) {
        const declared = readExtended(side) ?? readClassic(side);
        const draft = draftFor(declared?.id ?? ID.exec(side)![0]);
        if (declared !== null) {
          draft.kind = declared.kind;
          draft.text = declared.text;
        }
      }
      edges.push({ from: edge.from, to: edge.to, kind: edge.kind, label: edge.label });
      continue;
    }

    // `a:::token` — the inline way of setting a class.
    const inline = /^([A-Za-z_][\w-]*)(.*?):::([\w-]+)\s*$/.exec(line);
    const body = inline === null ? line : `${inline[1]}${inline[2]}`;
    if (inline !== null && (FILL_TOKENS as readonly string[]).includes(inline[3]!)) {
      classes.set(inline[1]!, inline[3] as FillToken);
    }

    const extended = readExtended(body);
    if (extended !== null) {
      const draft = draftFor(extended.id);
      draft.kind = extended.kind;
      draft.text = extended.text;
      if (!extended.known) dropped += 1;
      continue;
    }

    const classic = readClassic(body);
    if (classic !== null) {
      const draft = draftFor(classic.id);
      draft.kind = classic.kind;
      draft.text = classic.text;
      continue;
    }

    // A bare id on its own line is a legal, if unusual, node declaration.
    if (new RegExp(`^${ID.source.slice(1)}$`).test(body)) {
      draftFor(body);
      continue;
    }

    dropped += 1;
  }

  const nodes: DiagramNode[] = order.map((id) => {
    const draft = drafts.get(id)!;
    const stencil = stencilById(draft.kind);
    const at = positions.get(id);
    return {
      id,
      kind: draft.kind,
      x: at?.x ?? 0,
      y: at?.y ?? 0,
      width: at?.width ?? stencil.width,
      height: at?.height ?? stencil.height,
      text: draft.text,
      fill: classes.get(id) ?? draft.fill ?? stencil.defaultFill,
      parent: draft.parent,
    };
  });

  // A file nobody drew — one a model wrote, or one being typed into the text pane — carries no
  // `pos` comments at all, and every node in it would otherwise land on the origin in a heap.
  placeUnpositioned(nodes, edges, new Set(positions.keys()));

  // Positions are written absolute; the model keeps a child's relative to its container.
  const byId = new Map(nodes.map((node) => [node.id, node]));
  for (const node of nodes) {
    if (node.parent === null) continue;
    const parent = byId.get(node.parent);
    if (parent === undefined) continue;
    node.x -= parent.x;
    node.y -= parent.y;
  }

  const kept: DiagramEdge[] = [];
  edges.forEach((edge, index) => {
    if (!byId.has(edge.from) || !byId.has(edge.to)) {
      dropped += 1;
      return;
    }
    kept.push({ id: `e${index + 1}`, from: edge.from, to: edge.to, kind: edge.kind, label: edge.label });
  });

  const title =
    new RegExp(`^%%\\s*${METADATA_TAG}:\\s*title\\s+(.*)$`, "im").exec(text)?.[1]?.trim() ?? "";

  return {
    ok: true,
    value: { doc: { schema: DIAGRAM_SCHEMA, title, nodes, edges: kept }, dropped },
  };
}
