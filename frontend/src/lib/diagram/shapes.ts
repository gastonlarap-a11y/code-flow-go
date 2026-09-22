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
 *
 * **Paint order is array order**, and `role` decides only whether an element is filled. That is
 * what lets a stacked figure work: the copies behind go first as unfilled outlines, and the filled
 * front covers the lines that would otherwise run through it.
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

/** Never lets a derived width or height reach zero, which is a shape that does not render. */
function atLeast(value: number): number {
  return Math.max(1, value);
}

function diamond(width: number, height: number): string {
  return `M ${width / 2} 0 L ${width} ${height / 2} L ${width / 2} ${height} L 0 ${height / 2} Z`;
}

/** One sheet of paper: square except for the wave along the bottom. Shared by the document family. */
function sheet(x: number, y: number, w: number, h: number, wave: number): string {
  return (
    `M ${x} ${y} L ${x + w} ${y} L ${x + w} ${y + h - wave} ` +
    `C ${x + w * 0.72} ${y + h + wave * 0.4}, ${x + w * 0.28} ${y + h - wave * 2.2}, ${x} ${y + h - wave} Z`
  );
}

/** A cylinder standing on end, which is every database in every diagram ever drawn. */
function upright(width: number, height: number, ry: number): string {
  return (
    `M 0 ${ry} A ${width / 2} ${ry} 0 0 1 ${width} ${ry} ` +
    `L ${width} ${height - ry} A ${width / 2} ${ry} 0 0 1 0 ${height - ry} Z`
  );
}

/**
 * The outline of one figure at one size.
 *
 * Every branch is exhaustive over `StencilId`: a stencil added to the catalogue without a shape
 * here fails to compile, which is the whole reason the switch has no `default`.
 */
export function shapeElements(kind: StencilId, width: number, height: number): ShapeElement[] {
  switch (kind) {
    // ---- steps ---------------------------------------------------------------------------------

    case "rect":
      return [{ shape: "rect", x: 0, y: 0, width, height, rx: 0, role: "body" }];

    case "rounded":
      return [{ shape: "rect", x: 0, y: 0, width, height, rx: CORNER, role: "body" }];

    // A subprocess: a box with a frame inside it, meaning "this step is a diagram of its own".
    case "fr-rect": {
      const gap = inset(Math.min(width, height), 0.08, 10);
      return [
        { shape: "rect", x: 0, y: 0, width, height, rx: 0, role: "body" },
        {
          shape: "rect",
          x: gap,
          y: gap,
          width: atLeast(width - gap * 2),
          height: atLeast(height - gap * 2),
          rx: 0,
          role: "detail",
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

    case "lin-rect": {
      const bar = inset(width, 0.08, 12);
      return [
        { shape: "rect", x: 0, y: 0, width, height, rx: 0, role: "body" },
        { shape: "line", x1: bar, y1: 0, x2: bar, y2: height, role: "detail" },
      ];
    }

    // Many of the same step. The two behind are drawn as the corner they show, not as whole boxes,
    // so nothing has to be erased where they pass under the front one.
    case "st-rect": {
      const step = inset(Math.min(width, height), 0.08, 9);
      const w = atLeast(width - step * 2);
      const h = atLeast(height - step * 2);
      return [
        { shape: "rect", x: 0, y: step * 2, width: w, height: h, rx: 0, role: "body" },
        {
          shape: "path",
          d: `M ${step} ${step * 2} L ${step} ${step} L ${step + w} ${step} L ${step + w} ${step + h}`,
          role: "detail",
        },
        {
          shape: "path",
          d: `M ${step * 2} ${step} L ${step * 2} 0 L ${step * 2 + w} 0 L ${step * 2 + w} ${h}`,
          role: "detail",
        },
      ];
    }

    case "notch-rect": {
      const notch = inset(Math.min(width, height), 0.16, 20);
      return [
        {
          shape: "path",
          d: `M ${notch} 0 L ${width} 0 L ${width} ${height} L 0 ${height} L 0 ${notch} Z`,
          role: "body",
        },
      ];
    }

    // A manual operation: wide at the top, narrow at the bottom.
    case "trap-t": {
      const taper = inset(width, 0.14, 26);
      return [
        {
          shape: "path",
          d: `M 0 0 L ${width} 0 L ${width - taper} ${height} L ${taper} ${height} Z`,
          role: "body",
        },
      ];
    }

    case "trap-b": {
      const taper = inset(width, 0.14, 26);
      return [
        {
          shape: "path",
          d: `M ${taper} 0 L ${width - taper} 0 L ${width} ${height} L 0 ${height} Z`,
          role: "body",
        },
      ];
    }

    // Manual input: the top edge slopes, the way the top of a punched card does.
    case "sl-rect": {
      const slant = inset(height, 0.25, 22);
      return [
        {
          shape: "path",
          d: `M 0 ${slant} L ${width} 0 L ${width} ${height} L 0 ${height} Z`,
          role: "body",
        },
      ];
    }

    case "win-pane": {
      const bar = inset(Math.min(width, height), 0.14, 18);
      return [
        { shape: "rect", x: 0, y: 0, width, height, rx: 0, role: "body" },
        { shape: "line", x1: 0, y1: bar, x2: width, y2: bar, role: "detail" },
        { shape: "line", x1: bar, y1: 0, x2: bar, y2: height, role: "detail" },
      ];
    }

    case "tag-rect": {
      const tag = inset(Math.min(width, height), 0.2, 22);
      return [
        { shape: "rect", x: 0, y: 0, width, height, rx: 0, role: "body" },
        { shape: "path", d: `M 0 ${height - tag} L ${tag} ${height}`, role: "detail" },
      ];
    }

    // A display: flat top and bottom, both ends bowed out.
    case "curv-trap": {
      const curve = inset(width, 0.18, 30);
      return [
        {
          shape: "path",
          d:
            `M ${curve} 0 L ${width - curve} 0 ` +
            `C ${width} ${height * 0.2}, ${width} ${height * 0.8}, ${width - curve} ${height} ` +
            `L ${curve} ${height} ` +
            `C 0 ${height * 0.8}, 0 ${height * 0.2}, ${curve} 0 Z`,
          role: "body",
        },
      ];
    }

    // ---- where a flow branches, waits or rejoins -------------------------------------------------

    // One diamond for every decision and every BPMN gateway — Mermaid draws no mark inside it.
    case "diam":
      return [{ shape: "path", d: diamond(width, height), role: "body" }];

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

    // The bar a flow splits at and rejoins on. Heavy on purpose: it is read as a bar, not a box.
    case "fork":
      return [{ shape: "rect", x: 0, y: 0, width, height, rx: 3, role: "body", thick: true }];

    // A junction: a dot inside a ring, which is what tells it from a start event at a glance.
    case "f-circ": {
      const dot = Math.max(1, Math.min(width, height) * 0.22);
      return [
        { shape: "ellipse", cx: width / 2, cy: height / 2, rx: width / 2, ry: height / 2, role: "body" },
        { shape: "ellipse", cx: width / 2, cy: height / 2, rx: dot, ry: dot, role: "detail", thick: true },
      ];
    }

    case "cross-circ": {
      const cx = width / 2;
      const cy = height / 2;
      const arm = (Math.min(width, height) / 2) * 0.707;
      return [
        { shape: "ellipse", cx, cy, rx: width / 2, ry: height / 2, role: "body" },
        { shape: "line", x1: cx - arm, y1: cy - arm, x2: cx + arm, y2: cy + arm, role: "detail" },
        { shape: "line", x1: cx - arm, y1: cy + arm, x2: cx + arm, y2: cy - arm, role: "detail" },
      ];
    }

    case "notch-pent": {
      const notch = inset(width, 0.14, 24);
      return [
        {
          shape: "path",
          d:
            `M ${notch} 0 L ${width - notch} 0 L ${width} ${notch} ` +
            `L ${width} ${height} L 0 ${height} L 0 ${notch} Z`,
          role: "body",
        },
      ];
    }

    // A delay: square on the left, rounded off on the right, like a step waiting on something.
    case "delay": {
      const r = height / 2;
      const straight = Math.max(0, width - r);
      return [
        {
          shape: "path",
          d: `M 0 0 L ${straight} 0 A ${r} ${r} 0 0 1 ${straight} ${height} L 0 ${height} Z`,
          role: "body",
        },
      ];
    }

    case "hourglass":
      return [
        { shape: "path", d: `M 0 0 L ${width} 0 L ${width / 2} ${height / 2} Z`, role: "body" },
        { shape: "path", d: `M 0 ${height} L ${width} ${height} L ${width / 2} ${height / 2} Z`, role: "body" },
      ];

    case "bolt":
      return [
        {
          shape: "path",
          d:
            `M ${width * 0.6} 0 L ${width * 0.1} ${height * 0.56} L ${width * 0.44} ${height * 0.56} ` +
            `L ${width * 0.36} ${height} L ${width * 0.9} ${height * 0.42} L ${width * 0.54} ${height * 0.42} Z`,
          role: "body",
        },
      ];

    // ---- what a flow starts and stops at ---------------------------------------------------------

    // A pill. Mermaid has no ellipse, so this is also what a UML use case is drawn as.
    case "stadium":
      return [{ shape: "rect", x: 0, y: 0, width, height, rx: height / 2, role: "body" }];

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

    // ---- what a flow reads and writes ------------------------------------------------------------

    case "cyl": {
      const ry = inset(height, 0.18, 20);
      return [
        { shape: "path", d: upright(width, height, ry), role: "body" },
        // The far half of the top rim, which is what makes it read as a cylinder.
        { shape: "path", d: `M 0 ${ry} A ${width / 2} ${ry} 0 0 0 ${width} ${ry}`, role: "detail" },
      ];
    }

    // The same cylinder on its side: direct-access storage.
    case "h-cyl": {
      const rx = inset(width, 0.18, 22);
      return [
        {
          shape: "path",
          d:
            `M ${rx} 0 A ${rx} ${height / 2} 0 0 0 ${rx} ${height} ` +
            `L ${width - rx} ${height} A ${rx} ${height / 2} 0 0 0 ${width - rx} 0 Z`,
          role: "body",
        },
        {
          shape: "path",
          d: `M ${width - rx} 0 A ${rx} ${height / 2} 0 0 1 ${width - rx} ${height}`,
          role: "detail",
        },
      ];
    }

    // Disk storage: a cylinder with the platters showing.
    case "lin-cyl": {
      const ry = inset(height, 0.16, 18);
      return [
        { shape: "path", d: upright(width, height, ry), role: "body" },
        { shape: "path", d: `M 0 ${ry} A ${width / 2} ${ry} 0 0 0 ${width} ${ry}`, role: "detail" },
        { shape: "path", d: `M 0 ${ry * 2.2} A ${width / 2} ${ry} 0 0 0 ${width} ${ry * 2.2}`, role: "detail" },
      ];
    }

    // A data store as a data-flow diagram draws one: closed at the left, open to the right.
    case "datastore": {
      const cap = inset(width, 0.14, 24);
      return [
        { shape: "rect", x: 0, y: 0, width, height, rx: 0, role: "body" },
        { shape: "line", x1: cap, y1: 0, x2: cap, y2: height, role: "detail" },
        { shape: "line", x1: 0, y1: height / 2, x2: cap, y2: height / 2, role: "detail" },
      ];
    }

    // Stored data: both ends bowed the same way, so it reads as a tape rather than a box.
    case "bow-rect": {
      const bow = inset(width, 0.12, 22);
      return [
        {
          shape: "path",
          d:
            `M ${bow} 0 L ${width} 0 ` +
            `C ${width - bow} ${height * 0.3}, ${width - bow} ${height * 0.7}, ${width} ${height} ` +
            `L ${bow} ${height} ` +
            `C 0 ${height * 0.7}, 0 ${height * 0.3}, ${bow} 0 Z`,
          role: "body",
        },
      ];
    }

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

    case "lean-l": {
      const skew = inset(width, 0.18, 26);
      return [
        {
          shape: "path",
          d: `M 0 0 L ${width - skew} 0 L ${width} ${height} L ${skew} ${height} Z`,
          role: "body",
        },
      ];
    }

    // ---- paper -------------------------------------------------------------------------------------

    case "doc": {
      const wave = inset(height, 0.16, 18);
      return [{ shape: "path", d: sheet(0, 0, width, height, wave), role: "body" }];
    }

    // Several of them. Same trick as `st-rect`: the sheets behind are outlines, drawn first.
    case "docs": {
      const step = inset(Math.min(width, height), 0.07, 8);
      const w = atLeast(width - step * 2);
      const h = atLeast(height - step * 2);
      const wave = inset(h, 0.16, 16);
      return [
        { shape: "path", d: sheet(step * 2, 0, w, h, wave), role: "detail" },
        { shape: "path", d: sheet(step, step, w, h, wave), role: "detail" },
        { shape: "path", d: sheet(0, step * 2, w, h, wave), role: "body" },
      ];
    }

    case "lin-doc": {
      const wave = inset(height, 0.16, 18);
      const bar = inset(width, 0.08, 12);
      return [
        { shape: "path", d: sheet(0, 0, width, height, wave), role: "body" },
        { shape: "line", x1: bar, y1: 0, x2: bar, y2: height - wave, role: "detail" },
      ];
    }

    case "tag-doc": {
      const wave = inset(height, 0.16, 18);
      const tag = inset(Math.min(width, height), 0.18, 20);
      return [
        { shape: "path", d: sheet(0, 0, width, height, wave), role: "body" },
        { shape: "path", d: `M ${width - tag} 0 L ${width} ${tag}`, role: "detail" },
      ];
    }

    // Paper tape: waved along the top as well as the bottom.
    case "flag": {
      const wave = inset(height, 0.16, 18);
      return [
        {
          shape: "path",
          d:
            `M 0 ${wave} C ${width * 0.28} ${-wave * 1.2}, ${width * 0.72} ${wave * 2.2}, ${width} ${wave} ` +
            `L ${width} ${height - wave} ` +
            `C ${width * 0.72} ${height + wave * 0.4}, ${width * 0.28} ${height - wave * 2.2}, 0 ${height - wave} Z`,
          role: "body",
        },
      ];
    }

    case "tri":
      return [{ shape: "path", d: `M ${width / 2} 0 L ${width} ${height} L 0 ${height} Z`, role: "body" }];

    case "flip-tri":
      return [{ shape: "path", d: `M 0 0 L ${width} 0 L ${width / 2} ${height} Z`, role: "body" }];

    // ---- the things a process runs on ----------------------------------------------------------

    case "cloud": {
      const w = width;
      const h = height;
      return [
        {
          shape: "path",
          d:
            `M ${w * 0.25} ${h * 0.85} ` +
            `C ${w * 0.05} ${h * 0.85}, ${w * 0.02} ${h * 0.5}, ${w * 0.2} ${h * 0.45} ` +
            `C ${w * 0.2} ${h * 0.15}, ${w * 0.55} ${h * 0.05}, ${w * 0.62} ${h * 0.32} ` +
            `C ${w * 0.82} ${h * 0.2}, ${w} ${h * 0.4}, ${w * 0.9} ${h * 0.6} ` +
            `C ${w * 1.02} ${h * 0.78}, ${w * 0.9} ${h * 0.9}, ${w * 0.75} ${h * 0.85} Z`,
          role: "body",
        },
      ];
    }

    case "browser": {
      const bar = inset(height, 0.2, 26);
      const dot = Math.max(1, bar * 0.16);
      return [
        { shape: "rect", x: 0, y: 0, width, height, rx: 6, role: "body" },
        { shape: "line", x1: 0, y1: bar, x2: width, y2: bar, role: "detail" },
        { shape: "ellipse", cx: bar * 0.55, cy: bar / 2, rx: dot, ry: dot, role: "detail" },
        { shape: "ellipse", cx: bar * 1.1, cy: bar / 2, rx: dot, ry: dot, role: "detail" },
        { shape: "ellipse", cx: bar * 1.65, cy: bar / 2, rx: dot, ry: dot, role: "detail" },
      ];
    }

    case "console": {
      const bar = inset(height, 0.2, 26);
      const pad = inset(width, 0.08, 14);
      return [
        { shape: "rect", x: 0, y: 0, width, height, rx: 6, role: "body" },
        { shape: "line", x1: 0, y1: bar, x2: width, y2: bar, role: "detail" },
        // The prompt: a chevron and its caret, so it reads as a terminal and not as a window.
        {
          shape: "path",
          d: `M ${pad} ${bar + pad} L ${pad * 1.9} ${bar + pad * 1.6} L ${pad} ${bar + pad * 2.2}`,
          role: "detail",
        },
      ];
    }

    case "folder": {
      const tab = inset(height, 0.18, 22);
      const notch = inset(width, 0.4, 70);
      return [
        {
          shape: "path",
          d:
            `M 0 ${tab} L ${notch * 0.85} ${tab} L ${notch} 0 L ${width} 0 ` +
            `L ${width} ${height} L 0 ${height} Z`,
          role: "body",
        },
      ];
    }

    case "bucket": {
      const ry = inset(height, 0.14, 16);
      const taper = inset(width, 0.12, 18);
      return [
        {
          shape: "path",
          d:
            `M 0 ${ry} A ${width / 2} ${ry} 0 0 1 ${width} ${ry} ` +
            `L ${width - taper} ${height - ry} ` +
            `A ${atLeast(width / 2 - taper)} ${ry} 0 0 1 ${taper} ${height - ry} Z`,
          role: "body",
        },
        { shape: "path", d: `M 0 ${ry} A ${width / 2} ${ry} 0 0 0 ${width} ${ry}`, role: "detail" },
      ];
    }

    case "bang": {
      const w = width;
      const h = height;
      return [
        {
          shape: "path",
          d:
            `M 0 ${h * 0.55} L ${w * 0.16} ${h * 0.3} L ${w * 0.1} ${h * 0.05} ` +
            `L ${w * 0.38} ${h * 0.14} L ${w * 0.55} 0 L ${w * 0.68} ${h * 0.18} ` +
            `L ${w * 0.95} ${h * 0.12} L ${w * 0.88} ${h * 0.4} L ${w} ${h * 0.62} ` +
            `L ${w * 0.76} ${h * 0.74} L ${w * 0.78} ${h} L ${w * 0.5} ${h * 0.86} ` +
            `L ${w * 0.24} ${h * 0.96} L ${w * 0.22} ${h * 0.7} Z`,
          role: "body",
        },
      ];
    }

    // ---- what is written beside the diagram rather than in it ------------------------------------

    // A text block is its text. Drawing a box round it is what the other shapes are for.
    case "text":
      return [];

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
