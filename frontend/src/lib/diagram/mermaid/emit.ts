/**
 * The document, written as Mermaid (DIAG-005, DIAG-014).
 *
 * This is what lands on disk. It is a real `flowchart` — paste it into any Mermaid viewer and it
 * draws — and it is the reason the feature exists: a model reading this file sees nodes, edges and
 * labels in words, not a JSON of coordinates that only this app understands.
 *
 * **Deterministic.** The same document produces the same bytes, every time. The editor regenerates
 * the whole file on save rather than editing it in place, so anything non-deterministic here would
 * show up as a diff on a save that changed nothing.
 *
 * What Mermaid cannot carry is where the person put each shape — it has no coordinates, its
 * renderer lays out for itself. So positions go in `%%` comments at the end, which every other
 * viewer ignores and this one reads back.
 */
import { absolutePosition, type DiagramDocument, type DiagramEdge, type DiagramNode } from "../model";
import { exportPalette } from "../palette";
import { isContainer } from "../stencils";
import type { EdgeKind, FillToken } from "../model";

/** The marker that says these comments are ours, and which generation of them this is. */
export const METADATA_TAG = "codeflow";
export const METADATA_VERSION = "v1";

/** How Mermaid draws each of the four connectors. */
const ARROWS: Record<EdgeKind, string> = {
  arrow: "-->",
  line: "---",
  dotted: "-.->",
  thick: "==>",
};

/**
 * Escapes a label for a quoted Mermaid string.
 *
 * Mermaid takes HTML entities inside labels, which is how a quote gets in without ending the
 * string. Newlines become `<br/>`, which is what Mermaid renders as a line break and what
 * `parse.ts` turns back into a newline.
 */
export function escapeLabel(text: string): string {
  return text
    .replaceAll("#", "#35;")
    .replaceAll('"', "#quot;")
    .replaceAll("\n", "<br/>");
}

/** `fill:#…,stroke:#…,color:#…` for one of our tokens, in the palette an export is drawn in. */
function classDefFor(token: FillToken): string {
  const colours = exportPalette.fills[token];
  return `classDef ${token} fill:${colours.fill},stroke:${colours.stroke},color:${colours.text}`;
}

function declare(node: DiagramNode): string {
  return `  ${node.id}@{ shape: ${node.kind}, label: "${escapeLabel(node.text)}" }`;
}

function subgraphOf(doc: DiagramDocument, node: DiagramNode, depth: number): string[] {
  const pad = "  ".repeat(depth);
  const children = doc.nodes.filter((child) => child.parent === node.id);

  const lines = [`${pad}subgraph ${node.id} ["${escapeLabel(node.text)}"]`];
  for (const child of children) {
    lines.push(
      isContainer(child.kind)
        ? subgraphOf(doc, child, depth + 1).join("\n")
        : `${pad}${declare(child)}`,
    );
  }
  lines.push(`${pad}end`);
  return lines;
}

function edgeLine(edge: DiagramEdge): string {
  const arrow = ARROWS[edge.kind];
  const label = edge.label.trim();
  // `-->|text|` is the labelled form; an unlabelled connector takes no pipes at all.
  const middle = label === "" ? arrow : `${arrow}|"${escapeLabel(label)}"|`;
  return `  ${edge.from} ${middle} ${edge.to}`;
}

/**
 * The `%%` block: everything Mermaid has no way to say.
 *
 * One line per shape, holding where it is and how big. Absolute coordinates, not the relative ones
 * the model stores for a child — the file should survive a container being deleted by hand without
 * every shape inside it jumping to the top-left corner.
 */
function metadata(doc: DiagramDocument): string[] {
  const lines = [`%% ${METADATA_TAG}: ${METADATA_VERSION}`];
  // The title is the document's own name, not a node's, and Mermaid's flowchart has nowhere to put
  // one. A blank title writes no line rather than an empty one.
  if (doc.title.trim() !== "") lines.push(`%% ${METADATA_TAG}: title ${doc.title.trim()}`);
  for (const node of doc.nodes) {
    const at = absolutePosition(doc, node);
    lines.push(
      `%% ${METADATA_TAG}: pos ${node.id} ${Math.round(at.x)} ${Math.round(at.y)} ` +
        `${Math.round(node.width)} ${Math.round(node.height)}`,
    );
  }
  return lines;
}

export function emitMermaid(doc: DiagramDocument): string {
  const lines: string[] = ["flowchart TD"];

  // Only the fills the document actually uses. `none` is the absence of a class, so it needs none.
  const used = [...new Set(doc.nodes.map((node) => node.fill))].filter((fill) => fill !== "none");
  if (used.length > 0) {
    for (const token of used) lines.push(`  ${classDefFor(token)}`);
    lines.push("");
  }

  const loose = doc.nodes.filter((node) => node.parent === null && !isContainer(node.kind));
  for (const node of loose) lines.push(declare(node));

  const containers = doc.nodes.filter((node) => node.parent === null && isContainer(node.kind));
  for (const node of containers) {
    lines.push("");
    lines.push(...subgraphOf(doc, node, 1));
  }

  // Grouped by class, so a diagram where everything is one colour is one line rather than forty.
  const byFill = new Map<FillToken, string[]>();
  for (const node of doc.nodes) {
    if (node.fill === "none") continue;
    const ids = byFill.get(node.fill) ?? [];
    ids.push(node.id);
    byFill.set(node.fill, ids);
  }
  if (byFill.size > 0) {
    lines.push("");
    for (const [token, ids] of byFill) lines.push(`  class ${ids.join(",")} ${token}`);
  }

  if (doc.edges.length > 0) {
    lines.push("");
    for (const edge of doc.edges) lines.push(edgeLine(edge));
  }

  lines.push("", ...metadata(doc), "");
  return lines.join("\n");
}
