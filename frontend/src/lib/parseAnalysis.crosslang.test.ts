import { describe, expect, it } from "vitest";

// Imported as raw text through Vite rather than read with `node:fs`: the renderer has no Node
// types, and one test is not a reason to add them.
import fixture from "../../../backend/review/testdata/crosslang-review.md?raw";

import { parseAnalysis } from "./parseAnalysis";

/**
 * The TypeScript half of `XLANG-001`.
 *
 * The finding format is a three-way contract: a prompt tells the model to write it, the Go backend
 * parses it to reconcile one review against the last, and this file parses it to render. Two
 * parsers that drift do not fail — they silently disagree about how many findings a review has, or
 * about how severe one is.
 *
 * Both read the **same file**, from the backend's own testdata: a copy on each side would drift in
 * exactly the way this test exists to catch. The Go half is
 * `backend/review/memory_test.go` → `TestTheCrossLanguageFixtureParsesTheSameHere`, and the two
 * assert the same three findings.
 */
describe("the review format both parsers read", () => {
  const parsed = parseAnalysis(fixture);

  it("finds the same three findings the Go parser finds", () => {
    expect(parsed.findings).toHaveLength(3);
    expect(parsed.findings.map((f) => f.id)).toEqual(["F-001", "F-002", "F-003"]);
    expect(parsed.findings.map((f) => f.category)).toEqual([
      "race-condition",
      "hardcoded-secret",
      "naming",
    ]);
    expect(parsed.findings.map((f) => f.type)).toEqual(["Bug", "Security Hotspot", "Code Smell"]);
  });

  it("takes the severity from the word, not the emoji", () => {
    // F-002 is `🚨 [Mayor · …]`: the loud emoji with the middling word. Reading the emoji is what
    // once rendered two `Mayor` findings as critical and turned the Quality Gate red for them.
    expect(parsed.findings.map((f) => f.severity)).toEqual(["critical", "warning", "info"]);
  });

  it("anchors the first finding where the Go parser anchors it", () => {
    expect(parsed.findings[0]!.location).toEqual({ file: "src/app.ts", startLine: 12, endLine: 14 });
  });

  it("keeps the strengths bullet's number out of the last finding", () => {
    // The trailing sections are lifted out before the finding blocks are sliced, on both sides.
    expect(parsed.findings[2]!.confidence).toBeNull();
    expect(parsed.strengths).toHaveLength(2);
    expect(parsed.notes).toHaveLength(1);
  });

  it("reads the model's own gate as advisory", () => {
    expect(parsed.selfReportedGate).toBe("FAILED");
    expect(parsed.grades).toEqual({ reliability: "B", security: "C", maintainability: "B" });
  });
});
