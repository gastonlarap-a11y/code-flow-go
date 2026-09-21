/**
 * Dragging a shape's corner (DIAG-017).
 *
 * Eight handles, and each one moves two edges of the box at most. The arithmetic is dull and easy
 * to get subtly wrong — dragging the north-west corner has to move the origin *and* shrink the
 * size, and a shape dragged past itself has to flip rather than go negative — so it lives here with
 * a test rather than inside a pointer handler.
 */
import { MIN_SIZE } from "./serialize";
import type { Box } from "./routing";

/** The eight grips, named for where they sit. */
export type Handle = "nw" | "n" | "ne" | "e" | "se" | "s" | "sw" | "w";

export const HANDLES: readonly Handle[] = ["nw", "n", "ne", "e", "se", "s", "sw", "w"];

/** Where a handle sits on a box, in document coordinates. */
export function handlePoint(box: Box, handle: Handle): { x: number; y: number } {
  const left = box.x;
  const middle = box.x + box.width / 2;
  const right = box.x + box.width;
  const top = box.y;
  const centre = box.y + box.height / 2;
  const bottom = box.y + box.height;

  switch (handle) {
    case "nw":
      return { x: left, y: top };
    case "n":
      return { x: middle, y: top };
    case "ne":
      return { x: right, y: top };
    case "e":
      return { x: right, y: centre };
    case "se":
      return { x: right, y: bottom };
    case "s":
      return { x: middle, y: bottom };
    case "sw":
      return { x: left, y: bottom };
    case "w":
      return { x: left, y: centre };
  }
}

/** The CSS cursor for a grip, so the pointer says which way it will go. */
export function handleCursor(handle: Handle): string {
  switch (handle) {
    case "nw":
    case "se":
      return "nwse-resize";
    case "ne":
    case "sw":
      return "nesw-resize";
    case "n":
    case "s":
      return "ns-resize";
    case "e":
    case "w":
      return "ew-resize";
  }
}

/**
 * The box a drag produces.
 *
 * `dx`/`dy` are how far the pointer has moved from where it went down, in document units.
 *
 * `keepRatio` is for the shapes that must stay square — an event that is an oval is not the
 * notation's event. It takes the larger of the two movements so the shape follows the hand rather
 * than the axis that happened to move least.
 */
export function resizeBox(box: Box, handle: Handle, dx: number, dy: number, keepRatio = false): Box {
  const movesLeft = handle === "nw" || handle === "w" || handle === "sw";
  const movesRight = handle === "ne" || handle === "e" || handle === "se";
  const movesTop = handle === "nw" || handle === "n" || handle === "ne";
  const movesBottom = handle === "sw" || handle === "s" || handle === "se";

  let left = box.x + (movesLeft ? dx : 0);
  let right = box.x + box.width + (movesRight ? dx : 0);
  let top = box.y + (movesTop ? dy : 0);
  let bottom = box.y + box.height + (movesBottom ? dy : 0);

  // Dragged past itself: the edge that moved becomes the other one, which is what every editor
  // does and what the hand expects.
  if (right < left) [left, right] = [right, left];
  if (bottom < top) [top, bottom] = [bottom, top];

  let width = Math.max(MIN_SIZE, right - left);
  let height = Math.max(MIN_SIZE, bottom - top);

  if (keepRatio) {
    const side = Math.max(width, height);
    // Grow away from whichever edges are not moving, so the anchored corner stays put.
    if (movesLeft) left = right - side;
    if (movesTop) top = bottom - side;
    width = side;
    height = side;
  }

  return { x: Math.round(left), y: Math.round(top), width: Math.round(width), height: Math.round(height) };
}
