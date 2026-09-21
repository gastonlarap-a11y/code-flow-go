/**
 * What is under the pointer, and what a marquee caught (DIAG-017).
 *
 * The canvas asks these questions constantly and must answer them the same way every time, so they
 * are arithmetic in a module with tests rather than conditions scattered through event handlers.
 * Everything here works in **document coordinates** — the canvas converts from screen with
 * `lib/canvas/viewport.ts` before asking.
 */
import { absolutePosition, type DiagramDocument, type DiagramNode } from "./model";
import { isContainer } from "./stencils";
import { midpointOf, polylinePath, portPoint, routeEdge, chooseSides, type Box, type Point } from "./routing";

/** How far from a connector still counts as being on it. A 1.5px line is nobody's click target. */
export const EDGE_TOLERANCE = 8;

/** Where a node actually is, as a box. */
export function boxOf(doc: DiagramDocument, node: DiagramNode): Box {
  const at = absolutePosition(doc, node);
  return { x: at.x, y: at.y, width: node.width, height: node.height };
}

/**
 * The shape under a point, or `null`.
 *
 * Last in document order wins, because that is the one drawn on top. Containers are only picked
 * where nothing else covers them — dropping a task on a pool has to select the task.
 */
export function nodeAt(doc: DiagramDocument, point: Point): DiagramNode | null {
  let found: DiagramNode | null = null;

  for (const node of doc.nodes) {
    const box = boxOf(doc, node);
    const inside =
      point.x >= box.x &&
      point.x <= box.x + box.width &&
      point.y >= box.y &&
      point.y <= box.y + box.height;
    if (!inside) continue;

    // A container never wins over something on top of it, whatever the order says.
    if (found !== null && isContainer(node.kind) && !isContainer(found.kind)) continue;
    found = node;
  }

  return found;
}

/**
 * The connector under a point, or `null`.
 *
 * Measured against the run's corners rather than the drawn curve: the rounding at an elbow is a few
 * pixels and the tolerance is larger than that, so the difference cannot be felt.
 */
export function edgeAt(doc: DiagramDocument, point: Point): string | null {
  const boxes = new Map(doc.nodes.map((node) => [node.id, boxOf(doc, node)]));

  for (let i = doc.edges.length - 1; i >= 0; i -= 1) {
    const edge = doc.edges[i]!;
    const from = boxes.get(edge.from);
    const to = boxes.get(edge.to);
    if (from === undefined || to === undefined) continue;

    const sides = chooseSides(from, to);
    const points = routeEdge(from, sides.from, to, sides.to);
    for (let p = 1; p < points.length; p += 1) {
      if (distanceToSegment(point, points[p - 1]!, points[p]!) <= EDGE_TOLERANCE) return edge.id;
    }
  }

  return null;
}

/** Shortest distance from a point to a line segment. */
export function distanceToSegment(point: Point, a: Point, b: Point): number {
  const dx = b.x - a.x;
  const dy = b.y - a.y;
  const lengthSquared = dx * dx + dy * dy;
  if (lengthSquared === 0) return Math.hypot(point.x - a.x, point.y - a.y);

  const t = Math.max(0, Math.min(1, ((point.x - a.x) * dx + (point.y - a.y) * dy) / lengthSquared));
  return Math.hypot(point.x - (a.x + t * dx), point.y - (a.y + t * dy));
}

/** The rectangle two corners describe, whichever way round they were dragged. */
export function normalizeRect(a: Point, b: Point): Box {
  return {
    x: Math.min(a.x, b.x),
    y: Math.min(a.y, b.y),
    width: Math.abs(b.x - a.x),
    height: Math.abs(b.y - a.y),
  };
}

/**
 * Everything a marquee caught.
 *
 * **Touching is enough** — a shape does not have to be wholly inside. Requiring containment means
 * a marquee over a pool selects nothing unless it swallows the whole pool, which is not what the
 * gesture looks like it is doing.
 *
 * A container is only caught when the marquee is not simply *inside* it: dragging a box in the
 * empty middle of a pool is how somebody selects the tasks in that pool, not the pool.
 */
export function nodesIn(doc: DiagramDocument, rect: Box): string[] {
  const caught: string[] = [];

  for (const node of doc.nodes) {
    const box = boxOf(doc, node);
    const overlaps =
      box.x <= rect.x + rect.width &&
      box.x + box.width >= rect.x &&
      box.y <= rect.y + rect.height &&
      box.y + box.height >= rect.y;
    if (!overlaps) continue;

    if (isContainer(node.kind)) {
      const marqueeIsInside =
        rect.x >= box.x &&
        rect.y >= box.y &&
        rect.x + rect.width <= box.x + box.width &&
        rect.y + rect.height <= box.y + box.height;
      if (marqueeIsInside) continue;
    }

    caught.push(node.id);
  }

  return caught;
}

/** The connectors whose midpoint the marquee caught — enough to grab a label. */
export function edgesIn(doc: DiagramDocument, rect: Box): string[] {
  const boxes = new Map(doc.nodes.map((node) => [node.id, boxOf(doc, node)]));

  return doc.edges
    .filter((edge) => {
      const from = boxes.get(edge.from);
      const to = boxes.get(edge.to);
      if (from === undefined || to === undefined) return false;

      const sides = chooseSides(from, to);
      const at = midpointOf(routeEdge(from, sides.from, to, sides.to));
      return (
        at.x >= rect.x &&
        at.x <= rect.x + rect.width &&
        at.y >= rect.y &&
        at.y <= rect.y + rect.height
      );
    })
    .map((edge) => edge.id);
}

/** The path a connector is drawn along, for the canvas and nothing else. */
export function edgePath(from: Box, to: Box): { d: string; at: Point } {
  const sides = chooseSides(from, to);
  const points = routeEdge(from, sides.from, to, sides.to);
  return { d: polylinePath(points), at: midpointOf(points) };
}

/** Where a connector would attach on a shape, for drawing the handle a drag starts from. */
export function attachPoint(box: Box, side: Parameters<typeof portPoint>[1]): Point {
  return portPoint(box, side);
}
