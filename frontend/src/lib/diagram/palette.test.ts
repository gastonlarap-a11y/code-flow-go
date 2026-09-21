import { describe, expect, test } from "vitest";
import { AA_CONTRAST, contrastRatio } from "../ui/contrast";
import { FILL_TOKENS } from "./model";
import { diagramPalette, exportPalette, type ThemeMode } from "./palette";

/**
 * A diagram is text on colour, so every pairing in the table has to be readable — including the
 * ones nobody picks often. Measured rather than judged, on the same `contrastRatio` that caught the
 * accent buttons failing AA on six of eight options.
 *
 * `none` is checked against the canvas background, because that is what its text is actually
 * written on.
 */
const THEMES: readonly ThemeMode[] = ["light", "dark"];

describe("text is readable on every fill", () => {
  for (const theme of THEMES) {
    for (const token of FILL_TOKENS) {
      test(`${theme} / ${token}`, () => {
        const palette = diagramPalette(theme);
        const colours = palette.fills[token];
        const under = colours.fill === "none" ? palette.background : colours.fill;

        expect(contrastRatio(colours.text, under)).toBeGreaterThanOrEqual(AA_CONTRAST);
      });
    }
  }
});

/**
 * A shape's outline is not text, so AA does not apply to it — but an outline that disappears into
 * its own fill is a shape with no edge. 1.6:1 is the floor at which a 1.5px border stays visible,
 * and it is asserted so that a fill lightened later cannot quietly erase the border.
 */
describe("every outline is visible against its own fill", () => {
  for (const theme of THEMES) {
    for (const token of FILL_TOKENS) {
      test(`${theme} / ${token}`, () => {
        const palette = diagramPalette(theme);
        const colours = palette.fills[token];
        const under = colours.fill === "none" ? palette.background : colours.fill;

        expect(contrastRatio(colours.stroke, under)).toBeGreaterThanOrEqual(1.6);
      });
    }
  }
});

test("connectors and their labels read against the canvas", () => {
  for (const theme of THEMES) {
    const palette = diagramPalette(theme);

    expect(contrastRatio(palette.line, palette.background)).toBeGreaterThanOrEqual(3);
    expect(contrastRatio(palette.lineText, palette.background)).toBeGreaterThanOrEqual(AA_CONTRAST);
  }
});

// An export lands on a white page — a ticket, a document, a chat — so it is drawn light whatever
// the app is set to. Pinned because "use the current theme" is the obvious change to make and the
// wrong one.
test("an export is always drawn in the light palette", () => {
  expect(exportPalette).toBe(diagramPalette("light"));
  expect(exportPalette.background).toBe("#ffffff");
});
