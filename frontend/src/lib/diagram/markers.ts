/**
 * What a connector looks like, and the arrowheads it ends in (DIAG-006).
 *
 * Both halves live here for the same reason the shapes do: the canvas draws one picture and the
 * exporter draws another, and an arrowhead that differed between them would be a bug nobody sees
 * until a diagram is in a document somewhere.
 *
 * The markers are described as data rather than as markup so that the React canvas can render them
 * as elements and the exporter can write them as a string, from one table.
 */
import type { DiagramEdge, EdgeKind } from "./model";

export type MarkerId = "arrow-closed" | "arrow-open";

export interface EdgeStyle {
  dashed: boolean;
  /** Drawn heavier — Mermaid's `==>`. */
  thick: boolean;
  /** What sits at the target end, or `null` for a plain line. */
  marker: MarkerId | null;
}

const STYLES: Record<EdgeKind, EdgeStyle> = {
  // `-->`: the ordinary arrow, and the default for a new connector.
  arrow: { dashed: false, thick: false, marker: "arrow-closed" },
  // `---`: a line with no head, for an association or anything else that has no direction.
  line: { dashed: false, thick: false, marker: null },
  // `-.->`: dashed, for anything that is not the main flow.
  dotted: { dashed: true, thick: false, marker: "arrow-open" },
  // `==>`: the emphasised path.
  thick: { dashed: false, thick: true, marker: "arrow-closed" },
};

export function edgeStyle(kind: EdgeKind): EdgeStyle {
  return STYLES[kind];
}

/**
 * What is written on a connector.
 *
 * The user's own words and nothing else. The previous version prepended a notation's stereotype
 * (`«include»`) for two of the six kinds; there are four kinds now and none of them mean anything
 * beyond how they are drawn, so a relationship that needs naming is named — in the label, in
 * whatever words the person chose.
 */
export function edgeCaption(edge: DiagramEdge): string {
  return edge.label.trim();
}

/** How long a dash and its gap are, in user units. */
export const DASH = "6 4";

export interface MarkerShape {
  id: MarkerId;
  /** The arrowhead's outline, in a 12×12 box. */
  d: string;
  /** How the head is painted: filled in the line's colour, or drawn as two open strokes. */
  paint: "solid" | "stroke";
  /** Where the line's end sits inside the box, so the head touches the shape and not the line. */
  refX: number;
  size: number;
}

/**
 * The three arrowheads, in one 12×12 box each.
 *
 * `markerUnits` is `userSpaceOnUse` wherever these are rendered: the default scales the head with
 * the line's width, so a connector drawn 2px wide would grow an arrowhead twice the size of every
 * other one on the canvas.
 */
export const MARKER_SHAPES: readonly MarkerShape[] = [
  { id: "arrow-closed", d: "M 1 1 L 11 6 L 1 11 z", paint: "solid", refX: 11, size: 12 },
  { id: "arrow-open", d: "M 1 1 L 11 6 L 1 11", paint: "stroke", refX: 11, size: 12 },
];

/**
 * The DOM id a marker is referenced by.
 *
 * Prefixed, because these live in a hidden `<svg>` in the app's own document alongside whatever
 * else has ids in it, and `url(#arrow-open)` is a name another feature could plausibly take.
 */
export function markerDomId(id: MarkerId): string {
  return `cf-diagram-${id}`;
}
