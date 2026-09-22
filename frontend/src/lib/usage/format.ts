import type { UsageProvider, UsageSnapshot, UsageWindow } from "../../types/domain";

/**
 * Turning measurements into what the pill says (USAGE-010).
 *
 * Pure, so the decisions that matter — which number is the urgent one, when amber becomes red, what
 * is shown when nothing can be measured — are testable without a canvas.
 */

/** How close to a learned ceiling counts as worth noticing, and as nearly out. */
export const WARN_AT = 75;
export const DANGER_AT = 90;

/** The tone a share of a ceiling deserves, matching `Chip`'s own vocabulary. */
export type UsageTone = "success" | "warning" | "danger" | "neutral";

export function toneFor(percent: number | null): UsageTone {
  if (percent === null) return "neutral";
  if (percent >= DANGER_AT) return "danger";
  if (percent >= WARN_AT) return "warning";
  return "success";
}

/**
 * What the collapsed pill shows: the single most pressing thing measured.
 *
 * The highest share of any learned ceiling, because that is the number that decides whether
 * somebody is about to be cut off mid-task. When nothing has a ceiling yet there is no percentage
 * to show at all, and the pill falls back to the resting state rather than inventing one.
 */
export function headline(snapshot: UsageSnapshot | null): { percent: number | null; tone: UsageTone } {
  if (snapshot === null) return { percent: null, tone: "neutral" };

  let highest: number | null = null;
  for (const provider of snapshot.providers) {
    for (const window of [provider.session, provider.week]) {
      if (window?.percent == null) continue;
      if (highest === null || window.percent > highest) highest = window.percent;
    }
  }

  return { percent: highest, tone: toneFor(highest) };
}

/** Whether a provider has anything at all to draw, so an empty row is never rendered. */
export function hasSomethingToShow(provider: UsageProvider): boolean {
  return provider.session !== null || provider.week !== null;
}

/**
 * How long until a window resets, in whole minutes.
 *
 * Negative is clamped to zero: a window whose reset passed between the poll and the render has
 * reset, and counting backwards from it would be nonsense on screen.
 */
export function minutesUntil(resetsAt: string, now: number): number {
  const at = Date.parse(resetsAt);
  if (Number.isNaN(at)) return 0;
  return Math.max(0, Math.round((at - now) / 60_000));
}

/** "2 h 15 min", "45 min" — the reset, said the way somebody would say it. */
export function formatDuration(minutes: number): string {
  if (minutes < 60) return `${minutes} min`;

  const hours = Math.floor(minutes / 60);
  const rest = minutes % 60;
  return rest === 0 ? `${hours} h` : `${hours} h ${rest} min`;
}

/** Tokens, shortened: 1 234 → "1.2k", 1 234 567 → "1.2M". */
export function formatTokens(tokens: number): string {
  if (tokens < 1_000) return String(tokens);
  if (tokens < 1_000_000) return `${(tokens / 1_000).toFixed(1)}k`;
  return `${(tokens / 1_000_000).toFixed(1)}M`;
}

/** Bytes as somebody reads them: "3.4 GB". */
export function formatBytes(bytes: number): string {
  const units = ["B", "KB", "MB", "GB", "TB"];
  let value = bytes;
  let unit = 0;
  while (value >= 1024 && unit < units.length - 1) {
    value /= 1024;
    unit += 1;
  }
  return `${unit === 0 ? value : value.toFixed(1)} ${units[unit]}`;
}

/**
 * When a window will run out at the rate it is going, in minutes, or null when that cannot be said.
 *
 * Needs a learned ceiling and a rate; without either, a projection would be a guess dressed as a
 * warning. Capped at the window's own reset, because a window that resets first never runs out.
 */
export function minutesUntilExhausted(window: UsageWindow, now: number): number | null {
  if (window.ceiling === null || window.burn_per_hour <= 0) return null;

  const left = window.ceiling - window.tokens;
  if (left <= 0) return 0;

  const minutes = Math.round((left / window.burn_per_hour) * 60);
  const untilReset = minutesUntil(window.resets_at, now);
  return minutes >= untilReset ? null : minutes;
}
