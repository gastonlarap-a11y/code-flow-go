import { describe, expect, it } from "vitest";

// Imported as raw text through Vite rather than read with `node:fs`: the renderer has no Node
// types, and one test is not a reason to add them.
import fixture from "../../../backend/tickets/testdata/crosslang-verdict.md?raw";

import { parseAnalysis } from "./parseAnalysis";
import { parseTicketVerdict, splitTicketReview } from "./parseTicketVerdict";

/**
 * The TypeScript half of `XLANG-016`.
 *
 * The verdict block is a three-way contract: a prompt tells the model to write it, the Go backend
 * parses it to store it, and this file parses it to render. Two parsers that drift do not fail —
 * they silently disagree about whether a criterion was met, which is the one thing this whole
 * feature exists to answer.
 *
 * Both read the **same file**, from the backend's own testdata: a copy on each side would drift in
 * exactly the way this test exists to catch. The Go half is
 * `backend/tickets/verdict_test.go` → `TestTheCrossLanguageVerdictParsesTheSameHere`, and the two
 * assert the same four verdicts.
 */
describe("the verdict format both parsers read", () => {
  const parsed = parseTicketVerdict(fixture);

  it("reads the same four criteria the Go parser reads", () => {
    expect(parsed).not.toBeNull();
    expect(parsed!.criteria).toHaveLength(4);
    expect(parsed!.criteria.map((c) => c.id)).toEqual(["AC-1", "AC-2", "AC-3", "AC-4"]);
    expect(parsed!.criteria.map((c) => c.verdict)).toEqual([
      "cumple",
      "parcial",
      "no cumple",
      "no verificable",
    ]);
  });

  it("joins a wrapped evidence line back into one", () => {
    expect(parsed!.criteria[1]!.evidence).toBe(
      "backend/tickets/mirror.go:181-196 — se nombra el adjunto, pero el motivo se pierde " +
        "cuando el error no trae texto propio.",
    );
  });

  it("keeps a missing confidence line null rather than zero", () => {
    expect(parsed!.criteria[0]!.confidence).toBe(90);
    expect(parsed!.criteria[2]!.confidence).toBeNull();
  });

  it("reads the coverage block, relevance included", () => {
    expect(parsed!.coverage).not.toBeNull();
    expect(parsed!.coverage!.coverage).toBe("incompleta");
    expect(parsed!.coverage!.relevant).toBe(true);
    expect(parsed!.coverage!.missing).toContain("AC-3");
    expect(parsed!.coverage!.outOfScope).toContain("otro work item");
    expect(parsed!.coverage!.summary).toContain("dos de cuatro");
  });

  /**
   * The second defence of `XLANG-016`, asserted on the **unsplit** text.
   *
   * `### AC-1:` has no emoji, no bracketed severity and no `F-NNN`, so it can never match a finding
   * header — which is what makes the cut in `splitTicketReview` belt and braces rather than the only
   * thing keeping the two contracts apart.
   */
  it("does not collide with the finding format", () => {
    const whole = parseAnalysis(fixture);
    const head = parseAnalysis(splitTicketReview(fixture).findings);

    expect(whole.findings).toHaveLength(2);
    expect(head.findings.map((f) => f.id)).toEqual(whole.findings.map((f) => f.id));
  });
});
