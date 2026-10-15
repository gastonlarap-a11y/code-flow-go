/**
 * Where each card of the code map goes (19-repo-map.md).
 *
 * A layered layout read left to right: what imports sits to the left of what it imports, so the
 * entry points gather on one side and the foundations on the other. It is the classic shape —
 * break the cycles, rank by the longest import chain, order each column to keep lines from
 * crossing — kept small enough to stay predictable: the same graph always lands the same way, and a
 * column too tall for a screen is folded into the next one.
 *
 * Pure and synchronous, so it is tested without a DOM and memoised by the view.
 */

export interface LayoutNode {
  id: string;
  folder: boolean;
  importedBy: number;
  label: string;
}

export interface LayoutEdge {
  from: string;
  to: string;
}

export interface Placement {
  x: number;
  y: number;
}

export const CARD_WIDTH = 210;
export const CARD_HEIGHT = 58;
export const COLUMN_GAP = 90;
export const ROW_GAP = 18;
/** How many cards one column holds before it folds into the next. */
export const COLUMN_ROWS = 12;
/** How many barycentre passes order the columns; more stops paying off on graphs this size. */
const ORDER_PASSES = 6;

const COLUMN_STEP = CARD_WIDTH + COLUMN_GAP;
const ROW_STEP = CARD_HEIGHT + ROW_GAP;

/** The order cards keep when nothing else decides: folders first, then the most imported, then by name. */
function naturalOrder(a: LayoutNode, b: LayoutNode): number {
  if (a.folder !== b.folder) return a.folder ? -1 : 1;
  if (a.importedBy !== b.importedBy) return b.importedBy - a.importedBy;
  return a.label.localeCompare(b.label) || a.id.localeCompare(b.id);
}

export function layoutMap(
  nodes: readonly LayoutNode[],
  edges: readonly LayoutEdge[],
  columnRows = COLUMN_ROWS,
): Map<string, Placement> {
  const known = new Set(nodes.map((n) => n.id));
  const byId = new Map(nodes.map((n) => [n.id, n]));
  const outs = new Map<string, string[]>(nodes.map((n) => [n.id, []]));
  for (const e of edges) {
    if (e.from !== e.to && known.has(e.from) && known.has(e.to)) outs.get(e.from)?.push(e.to);
  }

  const forward = acyclic(nodes, outs);
  const rank = ranks(nodes, forward);
  const columns = orderColumns(nodes, forward, rank, byId);

  const placed = new Map<string, Placement>();
  let column = 0;
  for (const ids of columns) {
    // A column taller than a screen folds: its cards continue in the next physical column.
    for (let start = 0; start < ids.length; start += columnRows) {
      const chunk = ids.slice(start, start + columnRows);
      const top = (-(chunk.length - 1) * ROW_STEP) / 2;
      chunk.forEach((id, i) => placed.set(id, { x: column * COLUMN_STEP, y: top + i * ROW_STEP }));
      column++;
    }
  }
  return placed;
}

/**
 * The edges left once every cycle is broken: a depth-first walk in id order drops each edge that
 * reaches back to a card still on its path. The dropped edges are still drawn; they only stop
 * deciding the columns.
 */
function acyclic(nodes: readonly LayoutNode[], outs: Map<string, string[]>): Map<string, string[]> {
  const forward = new Map<string, string[]>(nodes.map((n) => [n.id, []]));
  const state = new Map<string, "open" | "done">();
  const ids = nodes.map((n) => n.id).sort();
  for (const start of ids) {
    if (state.has(start)) continue;
    const stack: { id: string; next: number }[] = [{ id: start, next: 0 }];
    state.set(start, "open");
    while (stack.length > 0) {
      const top = stack[stack.length - 1]!;
      const targets = [...(outs.get(top.id) ?? [])].sort();
      if (top.next >= targets.length) {
        state.set(top.id, "done");
        stack.pop();
        continue;
      }
      const to = targets[top.next++]!;
      const seen = state.get(to);
      if (seen === "open") continue; // back edge
      forward.get(top.id)?.push(to);
      if (seen === undefined) {
        state.set(to, "open");
        stack.push({ id: to, next: 0 });
      }
    }
  }
  return forward;
}

/** Each card's column: the length of the longest import chain that reaches it. */
function ranks(nodes: readonly LayoutNode[], forward: Map<string, string[]>): Map<string, number> {
  const incoming = new Map<string, number>(nodes.map((n) => [n.id, 0]));
  for (const targets of forward.values()) {
    for (const to of targets) incoming.set(to, (incoming.get(to) ?? 0) + 1);
  }
  const rank = new Map<string, number>(nodes.map((n) => [n.id, 0]));
  const queue = nodes.map((n) => n.id).filter((id) => incoming.get(id) === 0).sort();
  while (queue.length > 0) {
    const id = queue.shift()!;
    for (const to of forward.get(id) ?? []) {
      rank.set(to, Math.max(rank.get(to) ?? 0, (rank.get(id) ?? 0) + 1));
      const left = (incoming.get(to) ?? 1) - 1;
      incoming.set(to, left);
      if (left === 0) queue.push(to);
    }
  }
  return rank;
}

/**
 * The cards of each column, top to bottom: in their natural order first, then moved toward the
 * average height of their neighbours in the column before (on passes going right) or after (going
 * left), which is what untangles the lines between columns.
 */
function orderColumns(
  nodes: readonly LayoutNode[],
  forward: Map<string, string[]>,
  rank: Map<string, number>,
  byId: Map<string, LayoutNode>,
): string[][] {
  const depth = Math.max(0, ...rank.values());
  const columns: string[][] = Array.from({ length: depth + 1 }, () => []);
  for (const n of [...nodes].sort(naturalOrder)) columns[rank.get(n.id) ?? 0]!.push(n.id);

  const backward = new Map<string, string[]>(nodes.map((n) => [n.id, []]));
  for (const [from, targets] of forward) for (const to of targets) backward.get(to)?.push(from);

  const position = new Map<string, number>();
  const remember = () => columns.forEach((ids) => ids.forEach((id, i) => position.set(id, i)));
  remember();

  for (let pass = 0; pass < ORDER_PASSES; pass++) {
    const rightward = pass % 2 === 0;
    const order = rightward ? columns.keys() : [...columns.keys()].reverse();
    for (const c of order) {
      const ids = columns[c]!;
      const neighbours = rightward ? backward : forward;
      const weight = new Map<string, number>();
      for (const id of ids) {
        const around = (neighbours.get(id) ?? []).map((n) => position.get(n)).filter((p) => p !== undefined);
        weight.set(id, around.length > 0 ? around.reduce((a, b) => a + b, 0) / around.length : (position.get(id) ?? 0));
      }
      ids.sort((a, b) => {
        const delta = (weight.get(a) ?? 0) - (weight.get(b) ?? 0);
        if (delta !== 0) return delta;
        return (position.get(a) ?? 0) - (position.get(b) ?? 0) || naturalOrder(byId.get(a)!, byId.get(b)!);
      });
      ids.forEach((id, i) => position.set(id, i));
    }
  }
  return columns;
}
