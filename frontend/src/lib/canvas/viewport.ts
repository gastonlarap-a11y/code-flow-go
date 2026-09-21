import type { Point, Rect } from "./layout";

/**
 * Pan and zoom for the schema canvas, as pure functions (DBML-008).
 *
 * A screen point is `world × scale + offset`. Kept out of the component so the one property worth
 * testing — zooming keeps the point under the cursor still — is asserted rather than eyeballed.
 */
export interface Viewport {
  x: number;
  y: number;
  scale: number;
}

export interface Size {
  width: number;
  height: number;
}

export const MIN_SCALE = 0.2;
export const MAX_SCALE = 2;
export const IDENTITY: Viewport = { x: 0, y: 0, scale: 1 };

/** How far a floating panel sits from the point it belongs to. */
export const OVERLAY_OFFSET = 14;
/** How close one may come to the edge of the canvas. */
export const OVERLAY_MARGIN = 8;

/** Breathing room kept around the diagram when it is fitted to the view. */
export const FIT_PADDING = 48;

export function clampScale(scale: number): number {
  return Math.min(MAX_SCALE, Math.max(MIN_SCALE, scale));
}

/** Where a screen point falls on the diagram. */
export function toWorld(view: Viewport, screen: Point): Point {
  return { x: (screen.x - view.x) / view.scale, y: (screen.y - view.y) / view.scale };
}

/** Zooms by `factor` around `screen`, so whatever is under that point stays under it. */
export function zoomAt(view: Viewport, screen: Point, factor: number): Viewport {
  const scale = clampScale(view.scale * factor);
  const anchor = toWorld(view, screen);
  return { scale, x: screen.x - anchor.x * scale, y: screen.y - anchor.y * scale };
}

/**
 * The view that shows all of `bounds` inside `size`, centred.
 *
 * Never zooms in past 1: fitting a two-table schema into a wide window should not blow the cards up
 * to twice their size — it should show them at their size, centred.
 */
export function fitBounds(bounds: Rect | null, size: Size, padding = FIT_PADDING): Viewport {
  if (bounds === null || size.width <= 0 || size.height <= 0 || bounds.width <= 0 || bounds.height <= 0) {
    return IDENTITY;
  }
  const available = { width: Math.max(1, size.width - padding * 2), height: Math.max(1, size.height - padding * 2) };
  const scale = clampScale(Math.min(available.width / bounds.width, available.height / bounds.height, 1));
  return {
    scale,
    x: (size.width - bounds.width * scale) / 2 - bounds.x * scale,
    y: (size.height - bounds.height * scale) / 2 - bounds.y * scale,
  };
}

/**
 * Where a tooltip or a menu goes: beside the point it belongs to, and inside the canvas.
 *
 * It flips above the point rather than overflowing below, which is the case that matters — a bubble
 * hanging off the bottom of the diagram is one nobody can read, and a menu that does it is one
 * nobody can click. The margin wins over the offset when the canvas is smaller than the panel, so
 * the panel is clipped from the far side instead of being pushed off the near one.
 */
export function overlayAt(at: Point, canvas: Size, panel: Size): { left: number; top: number } {
  const below = at.y + OVERLAY_OFFSET;
  return {
    left: Math.max(OVERLAY_MARGIN, Math.min(at.x + OVERLAY_OFFSET, canvas.width - panel.width - OVERLAY_MARGIN)),
    top: Math.max(
      OVERLAY_MARGIN,
      below + panel.height > canvas.height ? at.y - OVERLAY_OFFSET - panel.height : below,
    ),
  };
}
