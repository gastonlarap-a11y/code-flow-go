import { describe, expect, test } from "vitest";
import { newDocument, type DiagramDocument } from "./model";
import { exportPalette } from "./palette";
import { BAND } from "./stencils";
import { EXPORT_PADDING, documentToSvg } from "./toSvg";

function flow(): DiagramDocument {
  return {
    ...newDocument("Checkout"),
    nodes: [
      { id: "start", kind: "stadium", x: 0, y: 0, width: 140, height: 56, text: "Start", fill: "accent", parent: null },
      { id: "check", kind: "diam", x: 240, y: -30, width: 150, height: 110, text: "Paid?", fill: "warning", parent: null },
    ],
    edges: [{ id: "e1", from: "start", to: "check", kind: "arrow", label: "always" }],
  };
}

test("the file is a parseable SVG document", () => {
  const svg = documentToSvg(flow());

  expect(svg.startsWith('<?xml version="1.0" encoding="UTF-8"?>')).toBe(true);
  expect(svg).toContain('xmlns="http://www.w3.org/2000/svg"');
  expect(svg.trimEnd().endsWith("</svg>")).toBe(true);
});

test("the canvas is sized to the drawing plus its margin", () => {
  const svg = documentToSvg(flow());

  // The drawing runs from x 0 to 390 and y -30 to 80: 390 × 110, plus padding on both sides.
  expect(svg).toContain(`width="${390 + EXPORT_PADDING * 2}"`);
  expect(svg).toContain(`height="${110 + EXPORT_PADDING * 2}"`);
});

// Negative coordinates are ordinary — a shape dragged up and left of where the first one landed —
// and an export that did not shift the origin would cut them off.
test("a drawing that starts at negative coordinates is shifted into view", () => {
  const svg = documentToSvg(flow());

  expect(svg).toContain(`translate(${EXPORT_PADDING} ${EXPORT_PADDING + 30})`);
});

test("an empty document exports a blank page rather than failing", () => {
  const svg = documentToSvg(newDocument("Nothing"));

  expect(svg).toContain("<svg");
  expect(svg).toContain(`fill="${exportPalette.background}"`);
});

describe("the picture", () => {
  test("draws every shape and every connector", () => {
    const svg = documentToSvg(flow());

    // A terminator is a stadium: a rect whose corner radius is half its height.
    expect(svg).toContain('rx="28"');
    // A decision is a diamond, which is a path.
    expect(svg).toContain("<path");
    expect(svg).toContain('marker-end="url(#arrow-closed)"');
  });

  test("uses the token's colour, never the token", () => {
    const svg = documentToSvg(flow());

    expect(svg).toContain(exportPalette.fills.accent.fill);
    expect(svg).toContain(exportPalette.fills.warning.fill);
    expect(svg).not.toContain("accent");
  });

  test("writes the labels", () => {
    const svg = documentToSvg(flow());

    expect(svg).toContain(">Start<");
    expect(svg).toContain(">Paid?<");
    expect(svg).toContain(">always<");
  });

  test("a dotted connector is drawn dashed, with an open head", () => {
    const doc: DiagramDocument = {
      ...flow(),
      edges: [{ id: "e1", from: "start", to: "check", kind: "dotted", label: "" }],
    };

    const svg = documentToSvg(doc);

    expect(svg).toContain("stroke-dasharray");
    expect(svg).toContain('marker-end="url(#arrow-open)"');
  });

  test("a thick connector is drawn heavier", () => {
    const doc: DiagramDocument = {
      ...flow(),
      edges: [{ id: "e1", from: "start", to: "check", kind: "thick", label: "" }],
    };

    expect(documentToSvg(doc)).toContain('stroke-width="3"');
  });

  test("a plain line has no head at all", () => {
    const doc: DiagramDocument = {
      ...flow(),
      edges: [{ id: "e1", from: "start", to: "check", kind: "line", label: "" }],
    };

    expect(documentToSvg(doc)).not.toContain("marker-end");
  });
});

/**
 * A container's title goes across the top, which is where Mermaid draws a subgraph's.
 *
 * It used to run down a left-hand band, and the exporter put it on the wrong side of the band's
 * separator — found by looking at an exported picture, not by a test. The band moved to the top
 * when the catalogue became Mermaid's, and this is the assertion that keeps the canvas and the
 * exported file agreeing about where a title lives.
 */
test("a container's title is written in its band", () => {
  const doc: DiagramDocument = {
    ...newDocument("Pool"),
    nodes: [
      { id: "n1", kind: "subgraph", x: 0, y: 0, width: 620, height: 300, text: "Sales", fill: "none", parent: null },
    ],
    edges: [],
  };

  const svg = documentToSvg(doc);
  const tspan = /<tspan x="([\d.]+)" y="([\d.]+)"/.exec(svg);

  expect(tspan, "the container's name is written").not.toBeNull();
  // Centred across the width, and inside the 28px band rather than down in the body.
  expect(Number(tspan![1])).toBe(310);
  expect(Number(tspan![2])).toBeGreaterThan(0);
  expect(Number(tspan![2])).toBeLessThan(BAND);
});

// A container has to be behind what it holds, or a pool paints over every task in it.
test("containers are drawn before the shapes inside them", () => {
  const doc: DiagramDocument = {
    ...newDocument("Lanes"),
    nodes: [
      { id: "task", kind: "rounded", x: 40, y: 40, width: 160, height: 76, text: "Quote", fill: "accent", parent: "pool" },
      { id: "pool", kind: "subgraph", x: 0, y: 0, width: 720, height: 300, text: "Sales", fill: "none", parent: null },
    ],
    edges: [],
  };

  const svg = documentToSvg(doc);

  expect(svg.indexOf("Sales")).toBeLessThan(svg.indexOf("Quote"));
});

test("a child is drawn at its place inside its container, not at its stored offset", () => {
  const doc: DiagramDocument = {
    ...newDocument("Lanes"),
    nodes: [
      { id: "pool", kind: "subgraph", x: 100, y: 200, width: 720, height: 300, text: "", fill: "none", parent: null },
      { id: "task", kind: "rounded", x: 40, y: 60, width: 160, height: 76, text: "Quote", fill: "accent", parent: "pool" },
    ],
    edges: [],
  };

  const svg = documentToSvg(doc);

  expect(svg).toContain("translate(140 260)");
});

// The exporter builds a string, so a label is the one place an unescaped character produces a file
// nothing can open.
test("a label with XML characters does not break the file", () => {
  const doc: DiagramDocument = {
    ...newDocument("Escapes"),
    nodes: [
      { id: "a", kind: "rect", x: 0, y: 0, width: 200, height: 72, text: "A & B <draft>", fill: "neutral", parent: null },
    ],
    edges: [],
  };

  const svg = documentToSvg(doc);

  expect(svg).toContain("A &amp; B &lt;draft&gt;");
  expect(svg).not.toContain("<draft>");
});

test("a long label is wrapped instead of running out of its shape", () => {
  const doc: DiagramDocument = {
    ...newDocument("Wrap"),
    nodes: [
      {
        id: "a",
        kind: "rect",
        x: 0,
        y: 0,
        width: 160,
        height: 72,
        text: "Validate the customer's tax identifier against the registry",
        fill: "neutral",
        parent: null,
      },
    ],
    edges: [],
  };

  const svg = documentToSvg(doc);
  const lines = svg.match(/<tspan /g) ?? [];

  expect(lines.length).toBeGreaterThan(2);
});

test("an export is drawn light whatever the app is set to", () => {
  const svg = documentToSvg(flow());

  expect(svg).toContain('fill="#ffffff"');
});
