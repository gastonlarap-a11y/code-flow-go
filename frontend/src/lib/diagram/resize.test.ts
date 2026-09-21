import { describe, expect, test } from "vitest";
import { HANDLES, handleCursor, handlePoint, resizeBox, type Handle } from "./resize";
import type { Box } from "./routing";
import { MIN_SIZE } from "./serialize";

const BOX: Box = { x: 100, y: 200, width: 160, height: 80 };

describe("where the grips sit", () => {
  test("each one is on the edge it is named for", () => {
    expect(handlePoint(BOX, "nw")).toEqual({ x: 100, y: 200 });
    expect(handlePoint(BOX, "n")).toEqual({ x: 180, y: 200 });
    expect(handlePoint(BOX, "ne")).toEqual({ x: 260, y: 200 });
    expect(handlePoint(BOX, "e")).toEqual({ x: 260, y: 240 });
    expect(handlePoint(BOX, "se")).toEqual({ x: 260, y: 280 });
    expect(handlePoint(BOX, "s")).toEqual({ x: 180, y: 280 });
    expect(handlePoint(BOX, "sw")).toEqual({ x: 100, y: 280 });
    expect(handlePoint(BOX, "w")).toEqual({ x: 100, y: 240 });
  });

  test("every grip has a cursor that says which way it goes", () => {
    for (const handle of HANDLES) expect(handleCursor(handle)).toMatch(/-resize$/);
    expect(handleCursor("nw")).toBe(handleCursor("se"));
    expect(handleCursor("n")).toBe("ns-resize");
  });
});

describe("dragging a grip", () => {
  test("the south-east corner only grows the size", () => {
    expect(resizeBox(BOX, "se", 40, 20)).toEqual({ x: 100, y: 200, width: 200, height: 100 });
  });

  // The one that is easy to get subtly wrong: the origin moves and the size shrinks, together.
  test("the north-west corner moves the origin and takes the size with it", () => {
    expect(resizeBox(BOX, "nw", 40, 20)).toEqual({ x: 140, y: 220, width: 120, height: 60 });
  });

  test("a side grip leaves the other axis alone", () => {
    expect(resizeBox(BOX, "e", 40, 999)).toEqual({ x: 100, y: 200, width: 200, height: 80 });
    expect(resizeBox(BOX, "n", 999, -50)).toEqual({ x: 100, y: 150, width: 160, height: 130 });
  });

  test("a grip dragged past the opposite edge flips instead of going negative", () => {
    const flipped = resizeBox(BOX, "e", -400, 0);

    expect(flipped.width).toBeGreaterThan(0);
    expect(flipped.x).toBeLessThan(BOX.x);
  });

  // Dragged exactly onto the opposite edge: the box has collapsed to nothing and the floor holds it
  // open. Further than that is the flip above, not a smaller box.
  test("nothing shrinks below the minimum", () => {
    const tiny = resizeBox(BOX, "se", -BOX.width, -BOX.height);

    expect(tiny.width).toBe(MIN_SIZE);
    expect(tiny.height).toBe(MIN_SIZE);
  });

  test("a drag that has not moved changes nothing", () => {
    for (const handle of HANDLES) expect(resizeBox(BOX, handle, 0, 0)).toEqual(BOX);
  });

  test("the box is always whole pixels", () => {
    const odd = resizeBox(BOX, "se", 10.4, 7.6);

    for (const value of Object.values(odd)) expect(Number.isInteger(value)).toBe(true);
  });
});

describe("a shape that has to stay square", () => {
  test("it takes the larger movement, so it follows the hand", () => {
    const square = resizeBox({ x: 0, y: 0, width: 80, height: 80 }, "se", 40, 10, true);

    expect(square.width).toBe(square.height);
    expect(square.width).toBe(120);
  });

  test("the anchored corner stays where it was", () => {
    const anchors: Record<Handle, (box: Box) => { x: number; y: number }> = {
      nw: (box) => ({ x: box.x + box.width, y: box.y + box.height }),
      n: (box) => ({ x: box.x, y: box.y + box.height }),
      ne: (box) => ({ x: box.x, y: box.y + box.height }),
      e: (box) => ({ x: box.x, y: box.y }),
      se: (box) => ({ x: box.x, y: box.y }),
      s: (box) => ({ x: box.x, y: box.y }),
      sw: (box) => ({ x: box.x + box.width, y: box.y }),
      w: (box) => ({ x: box.x + box.width, y: box.y }),
    };
    const start: Box = { x: 100, y: 100, width: 60, height: 60 };

    for (const handle of HANDLES) {
      const grown = resizeBox(start, handle, 30, 30, true);
      expect(anchors[handle](grown), handle).toEqual(anchors[handle](start));
    }
  });
});
