import { describe, expect, test } from "vitest";
import type { UsageSnapshot, UsageWindow } from "../../types/domain";
import {
  DANGER_AT,
  WARN_AT,
  formatBytes,
  formatDuration,
  formatTokens,
  headline,
  minutesUntil,
  minutesUntilExhausted,
  toneFor,
} from "./format";

const NOW = Date.parse("2026-09-22T10:00:00Z");

function window(overrides: Partial<UsageWindow> = {}): UsageWindow {
  return {
    tokens: 0,
    percent: null,
    source: "",
    ceiling: null,
    started_at: "2026-09-22T09:00:00Z",
    resets_at: "2026-09-22T14:00:00Z",
    burn_per_hour: 0,
    models: [],
    ...overrides,
  };
}

function snapshot(providers: UsageSnapshot["providers"]): UsageSnapshot {
  return {
    providers,
    resources: { cpu_percent: 0, memory_bytes: 0, sampled: false },
    data: { bytes: 0, complete: true },
    activity: { conversations: 0, jobs: 0 },
    taken_at: "2026-09-22T10:00:00Z",
  };
}

describe("tone", () => {
  test("climbs from green through amber to red", () => {
    expect(toneFor(10)).toBe("success");
    expect(toneFor(WARN_AT)).toBe("warning");
    expect(toneFor(DANGER_AT)).toBe("danger");
    expect(toneFor(100)).toBe("danger");
  });

  // No ceiling learned is not "fine": it is "unknown", and colouring it green would claim
  // something nobody measured.
  test("no percentage is neutral, never green", () => {
    expect(toneFor(null)).toBe("neutral");
  });
});

describe("what the collapsed pill says", () => {
  test("the highest share of any ceiling, because that is what cuts you off first", () => {
    const result = headline(
      snapshot([
        { provider: "claude", state: "measured", plan: "pro", session: window({ percent: 40 }), week: null },
        { provider: "codex", state: "measured", plan: "", session: window({ percent: 82 }), week: null },
      ]),
    );

    expect(result.percent).toBe(82);
    expect(result.tone).toBe("warning");
  });

  test("the week counts too, not only the open block", () => {
    const result = headline(
      snapshot([
        { provider: "claude", state: "measured", plan: "pro", session: window({ percent: 10 }), week: window({ percent: 95 }) },
      ]),
    );

    expect(result.percent).toBe(95);
    expect(result.tone).toBe("danger");
  });

  // Consumption without a ceiling has no percentage, and the pill must not invent one.
  test("windows measured but never calibrated leave the pill with no percentage", () => {
    const result = headline(
      snapshot([
        { provider: "claude", state: "measured", plan: "pro", session: window({ tokens: 900_000 }), week: null },
      ]),
    );

    expect(result.percent).toBeNull();
    expect(result.tone).toBe("neutral");
  });

  test("nothing measured at all is neutral and silent", () => {
    expect(headline(null)).toEqual({ percent: null, tone: "neutral" });
    expect(headline(snapshot([])).percent).toBeNull();
  });
});

describe("the reset", () => {
  test("is counted in whole minutes from now", () => {
    expect(minutesUntil("2026-09-22T12:30:00Z", NOW)).toBe(150);
  });

  // A window whose reset passed between the poll and the render has reset; counting backwards from
  // it would put a negative number on screen.
  test("never counts backwards", () => {
    expect(minutesUntil("2026-09-22T09:00:00Z", NOW)).toBe(0);
  });

  test("an unreadable timestamp is zero rather than NaN on screen", () => {
    expect(minutesUntil("not a date", NOW)).toBe(0);
  });

  test("is said the way somebody would say it", () => {
    expect(formatDuration(45)).toBe("45 min");
    expect(formatDuration(60)).toBe("1 h");
    expect(formatDuration(135)).toBe("2 h 15 min");
  });
});

describe("numbers people read", () => {
  test("tokens shorten as they grow", () => {
    expect(formatTokens(842)).toBe("842");
    expect(formatTokens(1_234)).toBe("1.2k");
    expect(formatTokens(1_234_567)).toBe("1.2M");
  });

  test("bytes climb through their units", () => {
    expect(formatBytes(512)).toBe("512 B");
    expect(formatBytes(1536)).toBe("1.5 KB");
    expect(formatBytes(3_650_722_201)).toBe("3.4 GB");
  });
});

describe("running out", () => {
  test("is projected from the rate once a ceiling is known", () => {
    const projection = minutesUntilExhausted(
      window({ tokens: 400, ceiling: 1_000, burn_per_hour: 600, resets_at: "2026-09-22T14:00:00Z" }),
      NOW,
    );

    // 600 tokens left, burning 600 an hour.
    expect(projection).toBe(60);
  });

  // Without a ceiling or a rate the projection would be a guess wearing a warning's clothes.
  test("is not attempted without a ceiling or without a rate", () => {
    expect(minutesUntilExhausted(window({ tokens: 400, burn_per_hour: 600 }), NOW)).toBeNull();
    expect(minutesUntilExhausted(window({ tokens: 400, ceiling: 1_000 }), NOW)).toBeNull();
  });

  // A window that resets before the rate would exhaust it never runs out, and saying otherwise
  // would send somebody to make coffee for no reason.
  test("is not shown when the window resets first", () => {
    const projection = minutesUntilExhausted(
      window({ tokens: 10, ceiling: 1_000_000, burn_per_hour: 60, resets_at: "2026-09-22T10:30:00Z" }),
      NOW,
    );

    expect(projection).toBeNull();
  });

  test("already past the ceiling is zero, not negative", () => {
    const projection = minutesUntilExhausted(
      window({ tokens: 1_200, ceiling: 1_000, burn_per_hour: 600 }),
      NOW,
    );

    expect(projection).toBe(0);
  });
});
