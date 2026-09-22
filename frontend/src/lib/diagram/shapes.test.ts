import { describe, expect, test } from "vitest";
import { shapeElements } from "./shapes";
import { STENCILS } from "./stencils";

/**
 * Every stencil is drawable, at every size.
 *
 * The switch in `shapes.ts` is exhaustive at compile time, which catches a stencil with no case at
 * all. What it cannot catch is a case that produces `NaN` — from a division by a zero height, or a
 * `Math.min` over an empty range — and `NaN` in a path attribute is a shape that silently does not
 * render. So the whole catalogue is drawn at the sizes that break arithmetic.
 */
const SIZES: readonly (readonly [number, number])[] = [
  [160, 72],
  [16, 16],
  [1, 1],
  [2000, 12],
  [12, 2000],
];

describe("every stencil draws at every size", () => {
  for (const stencil of STENCILS) {
    test(stencil.id, () => {
      for (const [width, height] of SIZES) {
        for (const item of shapeElements(stencil.id, width, height)) {
          for (const value of Object.values(item)) {
            if (typeof value !== "number") continue;
            expect(Number.isFinite(value), `${stencil.id} at ${width}×${height}`).toBe(true);
          }
          if (item.shape === "path") {
            expect(item.d, `${stencil.id} at ${width}×${height}`).not.toMatch(/NaN|Infinity/);
          }
        }
      }
    });
  }
});

// A text block is its text — which is what Mermaid's `text` shape is, and what somebody reaches for
// to annotate a canvas.
test("text is drawn as nothing but its text", () => {
  expect(shapeElements("text", 160, 36)).toEqual([]);
});

// A person has no fill to paint over: every part of the figure is a stroke.
test("a person is drawn entirely in details, never filled", () => {
  const elements = shapeElements("person", 60, 96);

  expect(elements.length).toBeGreaterThan(1);
  expect(elements.every((element) => element.role === "detail")).toBe(true);
});

// A stack of documents is painted back to front: the sheets behind are outlines and go first, and
// the filled front sheet covers the lines that would otherwise run through it. Paint order is
// array order, so this ordering *is* the drawing.
test("a stack of documents paints the sheets behind before the one in front", () => {
  const elements = shapeElements("docs", 158, 98);

  expect(elements).toHaveLength(3);
  expect(elements.map((element) => element.role)).toEqual(["detail", "detail", "body"]);
});

test("a stadium's corners are half its height, which is what makes it a pill", () => {
  const [body] = shapeElements("stadium", 150, 60);

  expect(body).toMatchObject({ shape: "rect", rx: 30 });
});

// The three BPMN gateways became one diamond when the catalogue was cut to what Mermaid can say.
// This is that decision, stated as a test so nobody re-adds an X inside it by accident.
test("a diamond carries no mark inside it", () => {
  expect(shapeElements("diam", 150, 110)).toHaveLength(1);
});

describe("the circles are told apart by their rings", () => {
  test("a plain circle is one ellipse", () => {
    expect(shapeElements("circle", 48, 48)).toHaveLength(1);
  });

  test("a double circle has an inner ring drawn on top", () => {
    const elements = shapeElements("dbl-circ", 48, 48);

    expect(elements).toHaveLength(2);
    expect(elements[1]?.role).toBe("detail");
  });

  test("a framed circle is drawn thick, which is what reads across a room", () => {
    expect(shapeElements("fr-circ", 48, 48)[0]).toMatchObject({ thick: true });
    expect(shapeElements("circle", 48, 48)[0]).not.toMatchObject({ thick: true });
  });
});

test("a person is all outline and no fill", () => {
  const elements = shapeElements("person", 60, 96);

  expect(elements.every((item) => item.role === "detail")).toBe(true);
  expect(elements).toHaveLength(5);
});

test("a container carries the line that separates its title band", () => {
  const elements = shapeElements("subgraph", 400, 200);

  expect(elements[0]).toMatchObject({ shape: "rect", role: "body" });
  expect(elements[1]).toMatchObject({ shape: "line", role: "detail" });
});

test("a subprocess is a rectangle with its two bars", () => {
  const elements = shapeElements("div-rect", 170, 76);

  expect(elements).toHaveLength(3);
  expect(elements.slice(1).every((item) => item.shape === "line")).toBe(true);
});
