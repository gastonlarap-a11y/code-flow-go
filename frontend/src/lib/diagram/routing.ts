/**
 * Where a connector runs (DIAG-006).
 *
 * React Flow would happily route these itself, and that is exactly why this exists: the exporter
 * cannot call it, so a diagram would leave the app with different lines from the ones drawn in it.
 * Routing here, and handing React Flow the finished path, makes the canvas and the file the same
 * picture by construction.
 *
 * Orthogonal, because these are process diagrams: a sequence flow that curves reads as a
 * suggestion, and a right angle reads as a step.
 */
import type { PortId } from "./model";

export interface Point {
  x: number;
  y: number;
}

export interface Box extends Point {
  width: number;
  height: number;
}

/** How far a connector leaves a shape before it is allowed to turn. */
export const STUB = 22;

/** How round a corner is. Enough to read as drawn rather than as a rendering artefact. */
export const CORNER_RADIUS = 8;

/**
 * Which sides two shapes should be joined through (DIAG-017).
 *
 * The document stores no sides — Mermaid has none, and a connector that remembered one would go on
 * pointing at the back of a shape after somebody dragged it past the other. They are chosen from
 * where the two boxes actually are, so a connector re-aims itself as the diagram is rearranged.
 *
 * The rule is the obvious one: leave by whichever axis the shapes are further apart on, and enter
 * from the facing side. Equal separation goes horizontal, which reads better for a flowchart laid
 * out left to right.
 */
export function chooseSides(source: Box, target: Box): { from: PortId; to: PortId } {
  const sx = source.x + source.width / 2;
  const sy = source.y + source.height / 2;
  const tx = target.x + target.width / 2;
  const ty = target.y + target.height / 2;

  const dx = tx - sx;
  const dy = ty - sy;

  // The gap between the boxes, not between their centres: two tall shapes side by side are
  // horizontally apart even when their centres are nearly level.
  const apartX = Math.abs(dx) - (source.width + target.width) / 2;
  const apartY = Math.abs(dy) - (source.height + target.height) / 2;

  if (apartX >= apartY) {
    return dx >= 0 ? { from: "right", to: "left" } : { from: "left", to: "right" };
  }
  return dy >= 0 ? { from: "bottom", to: "top" } : { from: "top", to: "bottom" };
}

/** Where on a shape's outline a port sits, in document coordinates. */
export function portPoint(box: Box, port: PortId): Point {
  switch (port) {
    case "top":
      return { x: box.x + box.width / 2, y: box.y };
    case "right":
      return { x: box.x + box.width, y: box.y + box.height / 2 };
    case "bottom":
      return { x: box.x + box.width / 2, y: box.y + box.height };
    case "left":
      return { x: box.x, y: box.y + box.height / 2 };
  }
}

/** The direction a connector leaves a port in. */
export function outward(port: PortId): Point {
  switch (port) {
    case "top":
      return { x: 0, y: -1 };
    case "right":
      return { x: 1, y: 0 };
    case "bottom":
      return { x: 0, y: 1 };
    case "left":
      return { x: -1, y: 0 };
  }
}

function isHorizontal(port: PortId): boolean {
  return port === "left" || port === "right";
}

/**
 * The corners a connector turns, from one port to another.
 *
 * Both ends get a stub first, so the line leaves the shape square to its edge whatever happens
 * after — without it a connector between two nearly-aligned shapes leaves at a slight angle and
 * reads as a mistake. The elbow between the stubs follows the source's own axis: a line leaving the
 * right-hand side travels horizontally before it commits to a direction.
 */
export function routeEdge(
  source: Box,
  sourcePort: PortId,
  target: Box,
  targetPort: PortId,
): Point[] {
  return routePorts(portPoint(source, sourcePort), sourcePort, portPoint(target, targetPort), targetPort);
}

/**
 * The same route, from the two points rather than the two boxes.
 *
 * This is the shared half, and it is shared for a concrete reason: on the canvas React Flow already
 * knows where each handle ended up and hands those coordinates to the edge, while the exporter has
 * only the document and works the points out itself. Both arrive here, so both draw the same line.
 */
export function routePorts(
  start: Point,
  sourcePort: PortId,
  end: Point,
  targetPort: PortId,
): Point[] {
  const from = outward(sourcePort);
  const to = outward(targetPort);

  const a = { x: start.x + from.x * STUB, y: start.y + from.y * STUB };
  const b = { x: end.x + to.x * STUB, y: end.y + to.y * STUB };

  const middle: Point[] = [];
  const sameAxis = isHorizontal(sourcePort) === isHorizontal(targetPort);

  if (isHorizontal(sourcePort)) {
    if (sameAxis) {
      // Left to right: meet at a vertical line halfway between the stubs.
      const midX = (a.x + b.x) / 2;
      middle.push({ x: midX, y: a.y }, { x: midX, y: b.y });
    } else {
      // Left to top: one corner is enough, and it belongs where the two runs cross.
      middle.push({ x: b.x, y: a.y });
    }
  } else if (sameAxis) {
    const midY = (a.y + b.y) / 2;
    middle.push({ x: a.x, y: midY }, { x: b.x, y: midY });
  } else {
    middle.push({ x: a.x, y: b.y });
  }

  return dedupe([start, a, ...middle, b, end]);
}

/** Drops points that repeat, which a stub produces whenever two shapes are already aligned. */
function dedupe(points: Point[]): Point[] {
  return points.filter((point, index) => {
    const previous = points[index - 1];
    return previous === undefined || previous.x !== point.x || previous.y !== point.y;
  });
}

/**
 * An SVG path through the given corners, rounded.
 *
 * The radius shrinks to fit the shorter of the two segments meeting at a corner, so a tight elbow
 * bends rather than overshooting into a loop — which is what a fixed radius does the moment two
 * shapes are close together.
 */
export function polylinePath(points: readonly Point[], radius = CORNER_RADIUS): string {
  if (points.length === 0) return "";
  if (points.length === 1) return `M ${points[0]!.x} ${points[0]!.y}`;

  const parts = [`M ${points[0]!.x} ${points[0]!.y}`];

  for (let i = 1; i < points.length - 1; i += 1) {
    const previous = points[i - 1]!;
    const corner = points[i]!;
    const next = points[i + 1]!;

    const r = Math.min(radius, distance(previous, corner) / 2, distance(corner, next) / 2);
    if (r < 1) {
      parts.push(`L ${corner.x} ${corner.y}`);
      continue;
    }

    const before = towards(corner, previous, r);
    const after = towards(corner, next, r);
    parts.push(`L ${before.x} ${before.y}`, `Q ${corner.x} ${corner.y} ${after.x} ${after.y}`);
  }

  const last = points[points.length - 1]!;
  parts.push(`L ${last.x} ${last.y}`);
  return parts.join(" ");
}

function distance(a: Point, b: Point): number {
  return Math.hypot(b.x - a.x, b.y - a.y);
}

/** The point `by` units from `from` in the direction of `to`. */
function towards(from: Point, to: Point, by: number): Point {
  const length = distance(from, to);
  if (length === 0) return from;
  return { x: from.x + ((to.x - from.x) / length) * by, y: from.y + ((to.y - from.y) / length) * by };
}

/** Where a connector's label goes: the midpoint of the run, measured along the line. */
export function midpointOf(points: readonly Point[]): Point {
  if (points.length === 0) return { x: 0, y: 0 };
  if (points.length === 1) return points[0]!;

  const lengths: number[] = [];
  let total = 0;
  for (let i = 1; i < points.length; i += 1) {
    const step = distance(points[i - 1]!, points[i]!);
    lengths.push(step);
    total += step;
  }

  let travelled = 0;
  for (let i = 0; i < lengths.length; i += 1) {
    const step = lengths[i]!;
    if (travelled + step >= total / 2) {
      const into = step === 0 ? 0 : (total / 2 - travelled) / step;
      const a = points[i]!;
      const b = points[i + 1]!;
      return { x: a.x + (b.x - a.x) * into, y: a.y + (b.y - a.y) * into };
    }
    travelled += step;
  }

  return points[points.length - 1]!;
}
