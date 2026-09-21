/**
 * What every figure looks like, as geometry rather than as markup (DIAG-006).
 *
 * **This is the only description of a shape's outline in the app.** The canvas renders these
 * elements as SVG children; the exporter writes the same elements into a standalone file. One
 * source, so "what I exported is not what I drew" is not a class of bug that exists here.
 *
 * These are drawings of *Mermaid's* shapes — the names are Mermaid's and so is the intent, though
 * the exact proportions are ours. A diagram opened in another Mermaid viewer is laid out by that
 * viewer and will not look identical; what has to match is which shape is which.
 *
 * Pure, DOM-free and sized in the node's own coordinates — the origin is the node's top-left corner
 * and nothing knows about zoom, which is what makes the whole catalogue assertable in a node test.
 */
import { BAND, type StencilId } from "./stencils";

/**
 * What an element is for, which decides how it is painted.
 *
 * - `body` carries the node's fill and its outline: the shape itself.
 * - `detail` is drawn on top with no fill — the bars of a subprocess, a person's limbs. A detail
 *   that were filled would paint over the text.
 */
export type ShapeRole = "body" | "detail";

export type ShapeElement =
  | { shape: "rect"; x: number; y: number; width: number; height: number; rx: number; role: ShapeRole; thick?: true }
  | { shape: "ellipse"; cx: number; cy: number; rx: number; ry: number; role: ShapeRole; thick?: true }
  | { shape: "path"; d: string; role: ShapeRole; thick?: true }
  | { shape: "line"; x1: number; y1: number; x2: number; y2: number; role: ShapeRole; thick?: true };

/** How round a "rounded" corner is. */
const CORNER = 12;

/** Keeps a derived inset sane on a shape somebody has dragged very small. */
function inset(size: number, fraction: number, cap: number): number {
  return Math.max(4, Math.min(cap, size * fraction));
}

function diamond(width: number, height: number): string {
  return `M ${width / 2} 0 L ${width} ${height / 2} L ${width / 2} ${height} L 0 ${height / 2} Z`;
}

/**
 * The outline of one figure at one size.
 *
 * Every branch is exhaustive over `StencilId`: a stencil added to the catalogue without a shape
 * here fails to compile, which is the whole reason the switch has no `default`.
 */
export function shapeElements(kind: StencilId, width: number, height: number): ShapeElement[] {
  switch (kind) {
    case "rect":
      return [{ shape: "rect", x: 0, y: 0, width, height, rx: 0, role: "body" }];

    case "rounded":
      return [{ shape: "rect", x: 0, y: 0, width, height, rx: CORNER, role: "body" }];

    // A pill. Mermaid has no ellipse, so this is also what a UML use case is drawn as.
    case "stadium":
      return [{ shape: "rect", x: 0, y: 0, width, height, rx: height / 2, role: "body" }];

    // One diamond for every decision and every BPMN gateway — Mermaid draws no mark inside it.
    case "diam":
      return [{ shape: "path", d: diamond(width, height), role: "body" }];

    case "lean-r": {
      const skew = inset(width, 0.18, 26);
      return [
        {
          shape: "path",
          d: `M ${skew} 0 L ${width} 0 L ${width - skew} ${height} L 0 ${height} Z`,
          role: "body",
        },
      ];
    }

    case "div-rect": {
      const bar = inset(width, 0.08, 12);
      return [
        { shape: "rect", x: 0, y: 0, width, height, rx: 0, role: "body" },
        { shape: "line", x1: bar, y1: 0, x2: bar, y2: height, role: "detail" },
        { shape: "line", x1: width - bar, y1: 0, x2: width - bar, y2: height, role: "detail" },
      ];
    }

    case "cyl": {
      const ry = inset(height, 0.18, 20);
      return [
        {
          shape: "path",
          d:
            `M 0 ${ry} A ${width / 2} ${ry} 0 0 1 ${width} ${ry} ` +
            `L ${width} ${height - ry} A ${width / 2} ${ry} 0 0 1 0 ${height - ry} Z`,
          role: "body",
        },
        // The far half of the top rim, which is what makes it read as a cylinder.
        { shape: "path", d: `M 0 ${ry} A ${width / 2} ${ry} 0 0 0 ${width} ${ry}`, role: "detail" },
      ];
    }

    case "doc": {
      const wave = inset(height, 0.16, 18);
      return [
        {
          shape: "path",
          d:
            `M 0 0 L ${width} 0 L ${width} ${height - wave} ` +
            `C ${width * 0.72} ${height + wave * 0.4}, ${width * 0.28} ${height - wave * 2.2}, 0 ${height - wave} Z`,
          role: "body",
        },
      ];
    }

    // Mermaid's comment shape: an open bracket, deliberately not a box, so it reads as a remark on
    // the diagram rather than a step in it.
    case "brace": {
      const arm = inset(width, 0.08, 14);
      return [{ shape: "path", d: `M ${arm} 0 L 0 0 L 0 ${height} L ${arm} ${height}`, role: "detail" }];
    }

    // A text block is its text. Drawing a box round it is what the other shapes are for.
    case "text":
      return [];

    case "circle":
    case "sm-circ":
      return [{ shape: "ellipse", cx: width / 2, cy: height / 2, rx: width / 2, ry: height / 2, role: "body" }];

    case "dbl-circ": {
      const gap = inset(Math.min(width, height), 0.1, 6);
      return [
        { shape: "ellipse", cx: width / 2, cy: height / 2, rx: width / 2, ry: height / 2, role: "body" },
        {
          shape: "ellipse",
          cx: width / 2,
          cy: height / 2,
          rx: Math.max(2, width / 2 - gap),
          ry: Math.max(2, height / 2 - gap),
          role: "detail",
        },
      ];
    }

    // A framed circle: the ring is what tells an end event from a start event across a room.
    case "fr-circ":
      return [
        { shape: "ellipse", cx: width / 2, cy: height / 2, rx: width / 2, ry: height / 2, role: "body", thick: true },
      ];

    case "hex": {
      const notch = inset(width, 0.16, 28);
      return [
        {
          shape: "path",
          d:
            `M ${notch} 0 L ${width - notch} 0 L ${width} ${height / 2} ` +
            `L ${width - notch} ${height} L ${notch} ${height} L 0 ${height / 2} Z`,
          role: "body",
        },
      ];
    }

    case "person": {
      // The classic stick figure: a head a fifth of the height, then body, arms and legs off it.
      // Everything is a detail — a person has no fill to paint over.
      const head = Math.max(6, Math.min(width / 2, height * 0.2));
      const cx = width / 2;
      const shoulders = head * 2 + 2;
      const hips = height * 0.62;
      return [
        { shape: "ellipse", cx, cy: head, rx: head, ry: head, role: "detail" },
        { shape: "line", x1: cx, y1: head * 2, x2: cx, y2: hips, role: "detail" },
        { shape: "line", x1: 0, y1: shoulders, x2: width, y2: shoulders, role: "detail" },
        { shape: "line", x1: cx, y1: hips, x2: 0, y2: height, role: "detail" },
        { shape: "line", x1: cx, y1: hips, x2: width, y2: height, role: "detail" },
      ];
    }

    // Mermaid draws a subgraph's title across the top, so that is where the band goes — unlike
    // BPMN, which writes a pool's name down the side.
    case "subgraph":
      return [
        { shape: "rect", x: 0, y: 0, width, height, rx: 0, role: "body" },
        { shape: "line", x1: 0, y1: BAND, x2: width, y2: BAND, role: "detail" },
      ];
  }
}
