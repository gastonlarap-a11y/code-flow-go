import { describe, expect, test } from "vitest";
import { escapeXml, estimateWidth, wrapText } from "./text";

const FONT = 13;

describe("wrapping", () => {
  test("a short label stays on one line", () => {
    expect(wrapText("Start", 140, FONT)).toEqual(["Start"]);
  });

  test("a long label is broken on spaces", () => {
    const lines = wrapText("Validate the customer's tax identifier", 140, FONT);

    expect(lines.length).toBeGreaterThan(1);
    expect(lines.join(" ")).toBe("Validate the customer's tax identifier");
  });

  test("every line fits the width it was given", () => {
    const width = 140;
    for (const line of wrapText("Reconcile the settlement report against the ledger", width, FONT)) {
      expect(estimateWidth(line, FONT)).toBeLessThanOrEqual(width);
    }
  });

  // A long identifier in a narrow shape is ordinary in this app, and letting it run out of the box
  // is worse than splitting it.
  test("a single word too long for the box is broken rather than left to overflow", () => {
    const lines = wrapText("ReconcileSettlementReportAgainstLedger", 80, FONT);

    expect(lines.length).toBeGreaterThan(1);
    expect(lines.join("")).toBe("ReconcileSettlementReportAgainstLedger");
  });

  test("a newline the user typed is kept", () => {
    expect(wrapText("First\nSecond", 200, FONT)).toEqual(["First", "Second"]);
  });

  test("nothing to write is no lines at all", () => {
    expect(wrapText("", 140, FONT)).toEqual([]);
    expect(wrapText("   ", 140, FONT)).toEqual([]);
  });

  // A shape dragged down to nothing still has to produce something renderable rather than an
  // infinite loop dividing by a width of zero.
  test("a box with no width still wraps, one word to a line", () => {
    const lines = wrapText("a b c", 0, FONT);

    expect(lines).toEqual(["a", "b", "c"]);
  });
});

describe("escaping", () => {
  test.each([
    ["A & B", "A &amp; B"],
    ["<draft>", "&lt;draft&gt;"],
    ['say "yes"', "say &quot;yes&quot;"],
    ["it's", "it&apos;s"],
  ])("%s", (input, expected) => {
    expect(escapeXml(input)).toBe(expected);
  });

  test("the ampersand is escaped before what follows it, never twice", () => {
    expect(escapeXml("&lt;")).toBe("&amp;lt;");
  });
});
