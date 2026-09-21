/**
 * Every way a diagram changes, as a function from one document to the next (DIAG-007).
 *
 * The store holds documents and a history stack; it does not know what "delete" means. That lives
 * here, pure, so the rules that are easy to get wrong — what happens to a connector when its shape
 * goes, where a task lands when it is dropped into a pool — are asserted in a node test instead of
 * being discovered on the canvas.
 *
 * Nothing mutates: every function returns a new document, which is also what makes the history
 * stack a list of snapshots rather than a list of regrets.
 */
import {
  absolutePosition,
  nextEdgeId,
  nextNodeId,
  nodeById,
  type DiagramDocument,
  type DiagramEdge,
  type DiagramNode,
  type EdgeKind,
  type FillToken,
} from "./model";
import { MIN_SIZE } from "./serialize";
import { isContainer, isFixedRatio, stencilById, type StencilId } from "./stencils";

/** How far a duplicate is offset from its original, so it does not hide underneath it. */
export const DUPLICATE_OFFSET = 20;

/** How far a shape is nudged when the spot it was aimed at is already taken. */
export const CASCADE_STEP = 28;

/**
 * Where a new shape actually lands, given where it was aimed.
 *
 * The palette places into the middle of the view, so clicking three stencils in a row would put
 * three shapes exactly on top of each other — which is what it did, and it reads as the second
 * click having done nothing. Each occupied spot pushes the next one down and right, the way a
 * window manager cascades.
 *
 * Only the centre is compared, not the whole box: shapes that merely overlap are fine — this is a
 * canvas — and it is landing *exactly* on top that hides one behind the other.
 */
export function freeSpot(
  doc: DiagramDocument,
  at: { x: number; y: number },
): { x: number; y: number } {
  const taken = (point: { x: number; y: number }): boolean =>
    doc.nodes.some((node) => {
      const centre = absolutePosition(doc, node);
      return (
        Math.abs(centre.x + node.width / 2 - point.x) < CASCADE_STEP &&
        Math.abs(centre.y + node.height / 2 - point.y) < CASCADE_STEP
      );
    });

  let spot = at;
  // Bounded: a canvas crowded enough to exhaust this is one where the next spot is as good as any.
  for (let step = 0; step < 40 && taken(spot); step += 1) {
    spot = { x: spot.x + CASCADE_STEP, y: spot.y + CASCADE_STEP };
  }
  return spot;
}

export function addNode(
  doc: DiagramDocument,
  kind: StencilId,
  at: { x: number; y: number },
  parent: string | null = null,
): { doc: DiagramDocument; id: string } {
  const stencil = stencilById(kind);
  const id = nextNodeId(doc);
  const spot = parent === null ? freeSpot(doc, at) : at;

  const node: DiagramNode = {
    id,
    kind,
    // Dropped by its centre: the pointer is over the middle of the shape, which is where a person
    // aiming at a spot on the canvas thinks the shape is going.
    x: Math.round(spot.x - stencil.width / 2),
    y: Math.round(spot.y - stencil.height / 2),
    width: stencil.width,
    height: stencil.height,
    text: "",
    fill: stencil.defaultFill,
    parent,
  };

  // Containers are prepended rather than appended: order is paint order, and a pool added after
  // the tasks would cover them.
  const nodes = isContainer(kind) ? [node, ...doc.nodes] : [...doc.nodes, node];

  return { doc: { ...doc, nodes }, id };
}

export function moveNodes(
  doc: DiagramDocument,
  moves: readonly { id: string; x: number; y: number }[],
): DiagramDocument {
  if (moves.length === 0) return doc;

  const byId = new Map(moves.map((move) => [move.id, move]));
  return {
    ...doc,
    nodes: doc.nodes.map((node) => {
      const move = byId.get(node.id);
      return move === undefined ? node : { ...node, x: move.x, y: move.y };
    }),
  };
}

export function resizeNode(
  doc: DiagramDocument,
  id: string,
  box: { x: number; y: number; width: number; height: number },
): DiagramDocument {
  const target = nodeById(doc, id);
  if (target === null) return doc;

  const size = constrainSize(target.kind, box.width, box.height);

  return {
    ...doc,
    nodes: doc.nodes.map((node) =>
      node.id === id ? { ...node, x: box.x, y: box.y, ...size } : node,
    ),
  };
}

/**
 * The size a shape is allowed to take.
 *
 * A floor, so nothing can be dragged down to something there is no way to grab again, and a square
 * for the stencils that are circles: an "event" that is an oval is not the notation's event, and a
 * gateway that is a kite is not a gateway.
 */
export function constrainSize(
  kind: StencilId,
  width: number,
  height: number,
): { width: number; height: number } {
  const w = Math.max(MIN_SIZE, Math.round(width));
  const h = Math.max(MIN_SIZE, Math.round(height));
  if (!isFixedRatio(kind)) return { width: w, height: h };

  const side = Math.max(w, h);
  return { width: side, height: side };
}

export function setNodeText(doc: DiagramDocument, id: string, text: string): DiagramDocument {
  return {
    ...doc,
    nodes: doc.nodes.map((node) => (node.id === id ? { ...node, text } : node)),
  };
}

export function setFill(
  doc: DiagramDocument,
  ids: readonly string[],
  fill: FillToken,
): DiagramDocument {
  const selected = new Set(ids);
  return {
    ...doc,
    nodes: doc.nodes.map((node) => (selected.has(node.id) ? { ...node, fill } : node)),
  };
}

export function setEdgeKind(
  doc: DiagramDocument,
  ids: readonly string[],
  kind: EdgeKind,
): DiagramDocument {
  const selected = new Set(ids);
  return {
    ...doc,
    edges: doc.edges.map((edge) => (selected.has(edge.id) ? { ...edge, kind } : edge)),
  };
}

export function setEdgeLabel(doc: DiagramDocument, id: string, label: string): DiagramDocument {
  return {
    ...doc,
    edges: doc.edges.map((edge) => (edge.id === id ? { ...edge, label } : edge)),
  };
}

export function setTitle(doc: DiagramDocument, title: string): DiagramDocument {
  return { ...doc, title };
}

/**
 * Connects two shapes.
 *
 * The same pair twice is one connector, not two stacked on each other: a second drag between
 * shapes that are already joined is a slip, and the duplicate would only be discoverable by
 * dragging the top one away. There are no sides to distinguish two connectors by any more —
 * `routing.chooseSides` picks them from where the shapes are — so the pair alone is the identity.
 *
 * A shape may be joined to itself; a loop is a real thing to draw.
 */
export function connect(
  doc: DiagramDocument,
  from: string,
  to: string,
  kind: EdgeKind = "arrow",
): DiagramDocument {
  if (nodeById(doc, from) === null || nodeById(doc, to) === null) return doc;
  if (doc.edges.some((edge) => edge.from === from && edge.to === to)) return doc;

  const edge: DiagramEdge = { id: nextEdgeId(doc), from, to, kind, label: "" };
  return { ...doc, edges: [...doc.edges, edge] };
}

/**
 * Deletes shapes and connectors.
 *
 * **A container takes its contents with it**, which is what the notation means: a lane is the set
 * of tasks in it, and leaving them behind as a heap of orphans is not the thing anybody asked for.
 * It is also the destructive answer of the two, which is why the store routes every edit through
 * the history stack — one undo brings the pool and everything in it back.
 *
 * A connector with either end gone goes too. A connector to nowhere is not a diagram.
 */
export function removeSelection(
  doc: DiagramDocument,
  nodeIds: readonly string[],
  edgeIds: readonly string[],
): DiagramDocument {
  const doomed = withDescendants(doc, nodeIds);
  const removedEdges = new Set(edgeIds);

  return {
    nodes: doc.nodes.filter((node) => !doomed.has(node.id)),
    edges: doc.edges.filter(
      (edge) => !removedEdges.has(edge.id) && !doomed.has(edge.from) && !doomed.has(edge.to),
    ),
    schema: doc.schema,
    title: doc.title,
  };
}

/** The given nodes plus everything nested inside them, however deep. */
function withDescendants(doc: DiagramDocument, ids: readonly string[]): Set<string> {
  const doomed = new Set(ids);

  // Repeated passes rather than recursion: the nesting is shallow, and a document whose parents
  // form a cycle terminates here because the set only ever grows.
  let grew = true;
  while (grew) {
    grew = false;
    for (const node of doc.nodes) {
      if (node.parent !== null && doomed.has(node.parent) && !doomed.has(node.id)) {
        doomed.add(node.id);
        grew = true;
      }
    }
  }

  return doomed;
}

/**
 * Moves a node into a container, or out of every container.
 *
 * The position is rewritten as it goes, from the frame it was in to the frame it is going to, so
 * the shape does not jump when it is dropped — which is the whole reason this is not two lines in
 * the drop handler.
 *
 * Refused when it would put a node inside itself or inside its own descendant, because that is a
 * cycle, and a cycle is a shape that renders nowhere.
 */
export function reparent(doc: DiagramDocument, id: string, parent: string | null): DiagramDocument {
  const node = nodeById(doc, id);
  if (node === null || node.parent === parent) return doc;

  if (parent !== null) {
    const container = nodeById(doc, parent);
    if (container === null || !isContainer(container.kind)) return doc;
    if (withDescendants(doc, [id]).has(parent)) return doc;
  }

  const absolute = absolutePosition(doc, node);
  const origin =
    parent === null ? { x: 0, y: 0 } : absolutePosition(doc, nodeById(doc, parent)!);

  return {
    ...doc,
    nodes: doc.nodes.map((entry) =>
      entry.id === id
        ? { ...entry, parent, x: absolute.x - origin.x, y: absolute.y - origin.y }
        : entry,
    ),
  };
}

/**
 * The container a point falls in, if any (DIAG-007).
 *
 * What "dropped into a pool" means, worked out from the document rather than from the canvas, so
 * the rule is one function with a test instead of a hit-test buried in a drag handler.
 *
 * The **innermost** match wins: a lane inside a pool is what a task dropped on it belongs to, and
 * the pool underneath is not a second answer. Nesting is shallow, so depth is the tie-break.
 *
 * `moving` and everything inside it are excluded — a shape cannot be dropped into itself, and a
 * pool dragged over its own lane must not become that lane's child.
 */
export function containerAt(
  doc: DiagramDocument,
  point: { x: number; y: number },
  moving: readonly string[] = [],
): string | null {
  const excluded = withDescendants(doc, moving);

  let best: { id: string; depth: number } | null = null;
  for (const node of doc.nodes) {
    if (excluded.has(node.id) || !isContainer(node.kind)) continue;

    const at = absolutePosition(doc, node);
    const inside =
      point.x >= at.x &&
      point.x <= at.x + node.width &&
      point.y >= at.y &&
      point.y <= at.y + node.height;
    if (!inside) continue;

    const depth = depthOf(doc, node);
    if (best === null || depth >= best.depth) best = { id: node.id, depth };
  }

  return best?.id ?? null;
}

function depthOf(doc: DiagramDocument, node: DiagramNode): number {
  let depth = 0;
  let parentId = node.parent;
  while (parentId !== null && depth < 8) {
    const parent = nodeById(doc, parentId);
    if (parent === null) break;
    depth += 1;
    parentId = parent.parent;
  }
  return depth;
}

/**
 * Copies a selection beside itself.
 *
 * Connectors come along when **both** ends are in the selection: a copy of two joined tasks is two
 * joined tasks, while a copy of one task does not silently rewire the original's connector to the
 * new one. Children of a copied container come too, keeping their place inside it.
 */
export function duplicate(
  doc: DiagramDocument,
  nodeIds: readonly string[],
): { doc: DiagramDocument; ids: string[] } {
  const chosen = withDescendants(doc, nodeIds);
  if (chosen.size === 0) return { doc, ids: [] };

  // Ids are handed out against a document that already holds the copies made so far, so two shapes
  // duplicated in one go cannot both be given `n7`.
  const remap = new Map<string, string>();
  let taken: DiagramDocument = doc;
  for (const node of doc.nodes) {
    if (!chosen.has(node.id)) continue;
    const id = nextNodeId(taken);
    remap.set(node.id, id);
    taken = { ...taken, nodes: [...taken.nodes, { ...node, id }] };
  }

  const copies = doc.nodes
    .filter((node) => chosen.has(node.id))
    .map((node) => {
      const copiedParent = node.parent !== null ? remap.get(node.parent) : undefined;
      // Only the outermost copies are offset. Nudging a child too would move it twice — once with
      // its container and once on its own — and it would drift out of the copy.
      const offset = copiedParent === undefined ? DUPLICATE_OFFSET : 0;
      return {
        ...node,
        id: remap.get(node.id)!,
        parent: copiedParent ?? null,
        x: node.x + offset,
        y: node.y + offset,
      };
    });

  const copiedEdges = doc.edges
    .filter((edge) => remap.has(edge.from) && remap.has(edge.to))
    .map((edge, index) => ({
      ...edge,
      // Numbered past everything the document already has, in one pass for the same reason as the
      // node ids above.
      id: `e${doc.edges.length + index + 1}`,
      from: remap.get(edge.from)!,
      to: remap.get(edge.to)!,
    }));

  return {
    doc: { ...doc, nodes: [...doc.nodes, ...copies], edges: [...doc.edges, ...copiedEdges] },
    ids: [...remap.values()],
  };
}
