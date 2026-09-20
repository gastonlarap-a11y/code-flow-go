import { describe, expect, it } from "vitest";
import {
  IDENTITY,
  MAX_SCALE,
  MIN_SCALE,
  OVERLAY_MARGIN,
  OVERLAY_OFFSET,
  fitBounds,
  overlayAt,
  toWorld,
  zoomAt,
} from "./viewport";

describe("zoomAt", () => {
  it("keeps the point under the cursor exactly where it was", () => {
    // The property that makes zooming feel anchored instead of sliding the diagram away.
    const view = { x: 40, y: -25, scale: 0.8 };
    const cursor = { x: 310, y: 190 };

    const before = toWorld(view, cursor);
    const after = toWorld(zoomAt(view, cursor, 1.5), cursor);

    expect(after.x).toBeCloseTo(before.x, 9);
    expect(after.y).toBeCloseTo(before.y, 9);
  });

  it("stops at the scale limits in both directions", () => {
    expect(zoomAt(IDENTITY, { x: 0, y: 0 }, 100).scale).toBe(MAX_SCALE);
    expect(zoomAt(IDENTITY, { x: 0, y: 0 }, 0.001).scale).toBe(MIN_SCALE);
  });
});

describe("fitBounds", () => {
  it("shrinks a large diagram until it fits, centred", () => {
    const bounds = { x: 0, y: 0, width: 2000, height: 1000 };
    const size = { width: 1000, height: 1000 };

    const view = fitBounds(bounds, size, 0);

    expect(view.scale).toBe(0.5);
    // Centred vertically: 1000 of screen, 500 of diagram.
    expect(view.y).toBe(250);
    expect(view.x).toBe(0);
  });

  it("never zooms a small diagram in past its real size", () => {
    const view = fitBounds({ x: 100, y: 100, width: 200, height: 100 }, { width: 1200, height: 800 });

    expect(view.scale).toBe(1);
    // Its centre lands on the screen's centre.
    expect(view.x + (100 + 200 / 2)).toBe(600);
    expect(view.y + (100 + 100 / 2)).toBe(400);
  });

  it("answers the identity for an empty diagram or a view with no size", () => {
    expect(fitBounds(null, { width: 800, height: 600 })).toEqual(IDENTITY);
    expect(fitBounds({ x: 0, y: 0, width: 10, height: 10 }, { width: 0, height: 0 })).toEqual(IDENTITY);
  });
});

describe("overlayAt", () => {
  const canvas = { width: 900, height: 600 };
  const panel = { width: 200, height: 100 };

  it("sits below and to the right of the point it belongs to", () => {
    expect(overlayAt({ x: 100, y: 100 }, canvas, panel)).toEqual({
      left: 100 + OVERLAY_OFFSET,
      top: 100 + OVERLAY_OFFSET,
    });
  });

  it("stops at the right edge instead of hanging off it", () => {
    expect(overlayAt({ x: 880, y: 100 }, canvas, panel).left).toBe(900 - 200 - OVERLAY_MARGIN);
  });

  it("flips above the point when there is no room below", () => {
    // 560 + 14 + 100 is past the bottom, so it goes up instead.
    expect(overlayAt({ x: 100, y: 560 }, canvas, panel).top).toBe(560 - OVERLAY_OFFSET - 100);
  });

  it("keeps the margin when the canvas is too small for the panel either way", () => {
    const position = overlayAt({ x: 4, y: 4 }, { width: 120, height: 60 }, panel);

    expect(position.left).toBe(OVERLAY_MARGIN);
    expect(position.top).toBe(OVERLAY_MARGIN);
  });
});
