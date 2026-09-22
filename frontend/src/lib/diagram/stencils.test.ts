import { describe, expect, test } from "vitest";
import { translations } from "../i18n/translations";
import { MIN_SIZE } from "./serialize";
import { STENCILS, STENCIL_GROUPS, isStencilId, stencilById, stencilsInGroup } from "./stencils";

/**
 * The catalogue is fifty entries of data, and the ways it goes wrong are all ways a table goes
 * wrong: a duplicate, a group nothing shows, a label that renders as its own key. TypeScript
 * catches a stencil with no outline and a `labelKey` that is not a key; these are what it cannot.
 */

test("no two figures share an id, because an id is a Mermaid shape name", () => {
  const ids = STENCILS.map((stencil) => stencil.id);

  expect(new Set(ids).size).toBe(ids.length);
});

test("every figure is in a family the palette shows", () => {
  for (const stencil of STENCILS) {
    expect(STENCIL_GROUPS, stencil.id).toContain(stencil.group);
  }
});

// A family with nothing in it is a heading the palette would draw over an empty grid.
test("every family has figures in it", () => {
  for (const group of STENCIL_GROUPS) {
    expect(stencilsInGroup(group).length, group).toBeGreaterThan(0);
  }
});

describe("labels", () => {
  test("every figure's label exists in both languages", () => {
    for (const stencil of STENCILS) {
      expect(translations.en[stencil.labelKey], `${stencil.id} in English`).toBeTruthy();
      expect(translations.es[stencil.labelKey], `${stencil.id} in Spanish`).toBeTruthy();
    }
  });

  test("every family has a heading in both languages", () => {
    for (const group of STENCIL_GROUPS) {
      const key = `diagram.group.${group}` as const;
      expect(translations.en[key], `${group} in English`).toBeTruthy();
      expect(translations.es[key], `${group} in Spanish`).toBeTruthy();
    }
  });

  // The palette's filter matches on the label, so two figures sharing one are two cells that look
  // identical in the search results.
  test("no two figures share a label", () => {
    const english = STENCILS.map((stencil) => translations.en[stencil.labelKey]);

    expect(new Set(english).size).toBe(english.length);
  });
});

test("a figure starts at a size somebody can see and drag", () => {
  for (const stencil of STENCILS) {
    expect(stencil.width, stencil.id).toBeGreaterThanOrEqual(MIN_SIZE);
    expect(stencil.height, stencil.id).toBeGreaterThanOrEqual(MIN_SIZE);
  }
});

// An id is written into the file as a Mermaid shape name. A capital letter or a space in one
// produces a `.mmd` no Mermaid renderer reads.
test("every id is a bare Mermaid shape name", () => {
  for (const stencil of STENCILS) {
    expect(stencil.id, stencil.id).toMatch(/^[a-z][a-z-]*$/);
  }
});

/**
 * The comment family is left out on purpose, and `isStencilId` is what the reader asks before
 * deciding a shape is unknown. If one were added back by accident, a `.mmd` naming it would start
 * parsing instead of being counted — a silent change to what the editor accepts.
 */
test("Mermaid's comment shapes are not in the catalogue", () => {
  for (const id of ["brace", "brace-r", "braces", "odd"]) {
    expect(isStencilId(id), id).toBe(false);
  }
});

test("only the container holds other shapes", () => {
  const holders = STENCILS.filter((stencil) => "container" in stencil).map((stencil) => stencil.id);

  expect(holders).toEqual(["subgraph"]);
  expect(stencilById("subgraph").text).toBe("band-top");
});
