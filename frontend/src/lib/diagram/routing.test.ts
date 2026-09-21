import { describe, expect, test } from "vitest";
import {
  STUB,
  midpointOf,
  outward,
  polylinePath,
  portPoint,
  routeEdge,
  type Box,
  type Point,
} from "./routing";

const box: Box = { x: 100, y: 100, width: 160, height: 80 };

describe("a port sits on the outline", () => {
  test.each([
    ["top", { x: 180, y: 100 }],
    ["right", { x: 260, y: 140 }],
    ["bottom", { x: 180, y: 180 }],
    ["left", { x: 100, y: 140 }],
  ] as const)("%s", (port, expected) => {
    expect(portPoint(box, port)).toEqual(expected);
  });
});

test("every segment of a route is horizontal or vertical", () => {
  const target: Box = { x: 500, y: 320, width: 160, height: 80 };

  for (const [from, to] of [
    ["right", "left"],
    ["bottom", "top"],
    ["right", "top"],
    ["top", "left"],
    ["left", "right"],
    ["bottom", "right"],
  ] as const) {
    const points = routeEdge(box, from, target, to);

    for (let i = 1; i < points.length; i += 1) {
      const a = points[i - 1]!;
      const b = points[i]!;
      const orthogonal = a.x === b.x || a.y === b.y;
      expect(orthogonal, `${from}→${to} segment ${i} runs diagonally`).toBe(true);
    }
  }
});

test("a connector leaves square to the edge it starts on", () => {
  const target: Box = { x: 600, y: 400, width: 160, height: 80 };
  const points = routeEdge(box, "right", target, "left");

  const start = points[0]!;
  const afterStub = points[1]!;

  expect(start).toEqual(portPoint(box, "right"));
  expect(afterStub).toEqual({ x: start.x + STUB, y: start.y });
});

test("it arrives square to the edge it ends on", () => {
  const target: Box = { x: 600, y: 400, width: 160, height: 80 };
  const points = routeEdge(box, "right", target, "top");

  const end = points[points.length - 1]!;
  const beforeStub = points[points.length - 2]!;

  expect(end).toEqual(portPoint(target, "top"));
  expect(beforeStub).toEqual({ x: end.x, y: end.y - STUB });
});

// Two shapes on the same line would otherwise produce a stub sitting on top of the point it came
// from, and a zero-length segment is a corner the rounding cannot resolve.
test("aligned shapes produce no repeated points", () => {
  const target: Box = { x: 500, y: 100, width: 160, height: 80 };
  const points = routeEdge(box, "right", target, "left");

  for (let i = 1; i < points.length; i += 1) {
    expect(points[i]).not.toEqual(points[i - 1]);
  }
});

describe("the rounded path", () => {
  test("starts and ends where the route does", () => {
    const points: Point[] = [
      { x: 0, y: 0 },
      { x: 100, y: 0 },
      { x: 100, y: 100 },
    ];

    const path = polylinePath(points);

    expect(path.startsWith("M 0 0")).toBe(true);
    expect(path.endsWith("L 100 100")).toBe(true);
    expect(path).toContain("Q 100 0");
  });

  // A fixed radius on a 4px elbow draws a curve that overshoots both segments and loops.
  test("a corner between two short segments bends rather than loops", () => {
    const path = polylinePath([
      { x: 0, y: 0 },
      { x: 4, y: 0 },
      { x: 4, y: 4 },
    ]);

    expect(path).toContain("Q 4 0");
    expect(path).toContain("L 2 0");
  });

  test("a straight run needs no curve at all", () => {
    expect(polylinePath([{ x: 0, y: 0 }, { x: 50, y: 0 }])).toBe("M 0 0 L 50 0");
  });

  test("nothing to draw is an empty path, not a broken one", () => {
    expect(polylinePath([])).toBe("");
  });
});

describe("the label's place", () => {
  test("is halfway along the line, not halfway between the ends", () => {
    // An L: 100 across then 300 down. Half the length is 200, which is 100 down the second leg.
    const at = midpointOf([
      { x: 0, y: 0 },
      { x: 100, y: 0 },
      { x: 100, y: 300 },
    ]);

    expect(at).toEqual({ x: 100, y: 100 });
  });

  test("of a straight line is its middle", () => {
    expect(midpointOf([{ x: 0, y: 0 }, { x: 80, y: 0 }])).toEqual({ x: 40, y: 0 });
  });
});

test("outward directions are unit vectors pointing away from the shape", () => {
  expect(outward("top")).toEqual({ x: 0, y: -1 });
  expect(outward("right")).toEqual({ x: 1, y: 0 });
  expect(outward("bottom")).toEqual({ x: 0, y: 1 });
  expect(outward("left")).toEqual({ x: -1, y: 0 });
});
