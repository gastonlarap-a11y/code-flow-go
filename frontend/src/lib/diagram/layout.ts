/**
 * Where a shape goes when the file does not say (DIAG-016).
 *
 * Mermaid has no coordinates. CodeFlow writes its own in `%% codeflow: pos` comments, but the whole
 * point of the format is that something else can write the file — a model asked for a flowchart, or
 * a person typing into the text pane — and nothing else writes those comments. Without this, every
 * such node lands at the origin, stacked, which is the one case the format exists to serve.
 *
 * So: a layered placement, top-down, the same direction `flowchart TD` declares. It is not a
 * replacement for Mermaid's layout engine and does not try to be — no edge-crossing reduction, no
 * splines. It has one job, which is to produce a drawing a person can immediately read and then
 * drag into the shape they wanted. The moment they drag it, the position becomes a `pos` comment
 * and this code never touches that node again.
 */

import type { DiagramNode } from "./model";
import { BAND } from "./stencils";

/**
 * All a layout needs of an arrow: which two nodes it joins.
 *
 * Narrower than `DiagramEdge` on purpose — the parser calls this before its edges have ids, and
 * asking for less means it does not have to invent any.
 */
type Link = { readonly from: string; readonly to: string };

/** Room between two shapes in the same row. */
const GAP_X = 56;
/** Room between two rows. */
const GAP_Y = 72;
/** Where a drawing starts on an otherwise empty page. */
const ORIGIN = 40;
/** Room between a container's edge and what it holds. */
const PAD = 24;

/** A rank per node: how many arrows deep into the flow it sits. */
function ranksOf(group: readonly DiagramNode[], edges: readonly Link[]): Map<string, number> {
  const inside = new Set(group.map((node) => node.id));
  const after = new Map<string, string[]>(group.map((node) => [node.id, []]));
  const before = new Map<string, string[]>(group.map((node) => [node.id, []]));
  const waiting = new Map<string, number>(group.map((node) => [node.id, 0]));

  for (const edge of edges) {
    if (edge.from === edge.to) continue;
    if (!inside.has(edge.from) || !inside.has(edge.to)) continue;
    after.get(edge.from)!.push(edge.to);
    before.get(edge.to)!.push(edge.from);
    waiting.set(edge.to, waiting.get(edge.to)! + 1);
  }

  const rank = new Map<string, number>(group.map((node) => [node.id, 0]));
  const settled = new Set<string>();
  const queue = group.filter((node) => waiting.get(node.id) === 0).map((node) => node.id);

  for (let head = 0; head < queue.length; head += 1) {
    const id = queue[head]!;
    settled.add(id);
    for (const next of after.get(id)!) {
      rank.set(next, Math.max(rank.get(next)!, rank.get(id)! + 1));
      const left = waiting.get(next)! - 1;
      waiting.set(next, left);
      if (left === 0) queue.push(next);
    }
  }

  // What is left is in a cycle, which a flowchart is allowed to contain and a rank cannot describe.
  // One pass in document order puts each node below whichever of its predecessors already has a
  // rank; the back edge then points upwards, which is what a loop looks like when drawn by hand.
  for (const node of group) {
    if (settled.has(node.id)) continue;
    let level = 0;
    for (const previous of before.get(node.id)!) {
      if (settled.has(previous)) level = Math.max(level, rank.get(previous)! + 1);
    }
    rank.set(node.id, level);
    settled.add(node.id);
  }

  return rank;
}

/** Lays a group out in rows from `originX`/`originY`, and answers the box it filled. */
function placeRows(
  group: readonly DiagramNode[],
  edges: readonly Link[],
  originX: number,
  originY: number,
): { width: number; height: number } {
  const rank = ranksOf(group, edges);
  const rows = new Map<number, DiagramNode[]>();
  for (const node of group) {
    const level = rank.get(node.id)!;
    const row = rows.get(level);
    if (row === undefined) rows.set(level, [node]);
    else row.push(node);
  }

  const ordered = [...rows.entries()].sort(([a], [b]) => a - b).map(([, row]) => row);
  const widthOf = (row: readonly DiagramNode[]): number =>
    row.reduce((total, node) => total + node.width, 0) + GAP_X * (row.length - 1);
  const widest = Math.max(...ordered.map(widthOf));

  let y = originY;
  for (const row of ordered) {
    // Rows are centred on each other, so a single shape sits under the middle of the row above it
    // rather than hard against the left margin.
    let x = originX + Math.round((widest - widthOf(row)) / 2);
    for (const node of row) {
      node.x = x;
      node.y = y;
      x += node.width + GAP_X;
    }
    y += Math.max(...row.map((node) => node.height)) + GAP_Y;
  }

  return { width: widest, height: y - originY - GAP_Y };
}

/** How many containers a node sits inside. */
function depthOf(node: DiagramNode, byId: ReadonlyMap<string, DiagramNode>): number {
  let depth = 0;
  let parent = node.parent;
  while (parent !== null && depth < 32) {
    const above = byId.get(parent);
    if (above === undefined) break;
    depth += 1;
    parent = above.parent;
  }
  return depth;
}

/**
 * Gives every node the file had no position for a place to be, in the coordinates the caller works
 * in: absolute, which is what a `pos` comment stores and what the parser converts to relative
 * afterwards. The nodes are mutated — they have just been built and belong to the caller.
 *
 * Nodes already positioned are never moved. New ones go below the existing drawing, so typing a
 * shape into the text pane of a diagram that is already laid out adds to it instead of landing on
 * top of it.
 */
export function placeUnpositioned(
  nodes: DiagramNode[],
  edges: readonly Link[],
  positioned: ReadonlySet<string>,
): void {
  const loose = nodes.filter((node) => !positioned.has(node.id));
  if (loose.length === 0) return;

  const byId = new Map(nodes.map((node) => [node.id, node]));
  const groups = new Map<string | null, DiagramNode[]>();
  for (const node of loose) {
    const key = node.parent !== null && byId.has(node.parent) ? node.parent : null;
    const group = groups.get(key);
    if (group === undefined) groups.set(key, [node]);
    else group.push(node);
  }

  // Deepest containers first: a container has to know how big its contents are before it can be
  // sized, and it only learns its own place afterwards, from the group it belongs to.
  const containers = [...groups.keys()]
    .filter((key): key is string => key !== null)
    .sort((a, b) => depthOf(byId.get(b)!, byId) - depthOf(byId.get(a)!, byId));

  for (const id of containers) {
    const container = byId.get(id)!;
    const group = groups.get(id)!;
    const settled = nodes.filter((node) => node.parent === id && positioned.has(node.id));
    // A sibling that is already placed is stored absolute here, like everything else at this point.
    const below =
      settled.length === 0
        ? BAND + PAD
        : Math.max(...settled.map((node) => node.y + node.height)) - container.y + GAP_Y;

    const box = placeRows(group, edges, PAD, below);

    if (!positioned.has(id)) {
      container.width = Math.max(container.width, box.width + PAD * 2);
      container.height = Math.max(container.height, below + box.height + PAD);
    }
  }

  const roots = groups.get(null);
  if (roots !== undefined) {
    const settled = nodes.filter((node) => node.parent === null && positioned.has(node.id));
    const top =
      settled.length === 0 ? ORIGIN : Math.max(...settled.map((node) => node.y + node.height)) + GAP_Y;
    placeRows(roots, edges, ORIGIN, top);
  }

  // Shallowest first, so a container is absolute before what it holds is shifted onto it.
  for (const id of [...containers].reverse()) {
    const container = byId.get(id)!;
    for (const node of groups.get(id)!) {
      node.x += container.x;
      node.y += container.y;
    }
  }
}
