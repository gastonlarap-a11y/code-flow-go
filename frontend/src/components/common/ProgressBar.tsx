/**
 * A bar that fills (USAGE-010).
 *
 * Extracted on its fourth caller, not in anticipation of one: the update alert, the settings update
 * section and the release-notes modal each carried a literal copy of the same track-and-fill markup,
 * and the usage panel would have been the fourth. Same reasoning as `docwalk` and the canvas
 * viewport moving when their second consumer appeared.
 *
 * `tone` names a semantic colour rather than a value, so a download (always accent) and a quota
 * (green until it is not) can share one component without it deciding anything about either.
 */
export type ProgressTone = "accent" | "success" | "warning" | "danger" | "neutral";

const FILL: Record<ProgressTone, string> = {
  accent: "var(--cf-accent)",
  success: "var(--cf-success)",
  warning: "var(--cf-warning)",
  danger: "var(--cf-danger)",
  neutral: "var(--cf-text-muted)",
};

/** The two heights the app already used, as a choice rather than a class to override. */
const HEIGHT = { sm: "h-1", md: "h-1.5" } as const;

export function ProgressBar({
  percent,
  tone = "accent",
  size = "sm",
  label,
  className = "",
}: {
  /** 0–100. Clamped, because a measured value can exceed a ceiling learned earlier. */
  percent: number;
  tone?: ProgressTone;
  size?: keyof typeof HEIGHT;
  /**
   * What a screen reader announces. Given one, the bar becomes a real progressbar; without one it
   * stays decorative, which is right when the number beside it already says everything.
   */
  label?: string;
  className?: string;
}) {
  const filled = Math.max(0, Math.min(100, percent));
  const accessibility =
    label === undefined
      ? ({ "aria-hidden": true } as const)
      : ({
          role: "progressbar",
          "aria-label": label,
          "aria-valuenow": Math.round(filled),
          "aria-valuemin": 0,
          "aria-valuemax": 100,
        } as const);

  return (
    <div
      {...accessibility}
      className={`${HEIGHT[size]} w-full overflow-hidden rounded-full bg-[var(--cf-border)] ${className}`}
    >
      <div
        className="h-full rounded-full transition-all"
        style={{ width: `${filled}%`, background: FILL[tone] }}
      />
    </div>
  );
}
