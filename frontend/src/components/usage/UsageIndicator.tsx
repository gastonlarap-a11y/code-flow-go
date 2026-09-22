import { useEffect, useRef, useState } from "react";
import { Activity, Cpu, HardDrive, Sparkles } from "lucide-react";
import { ProgressBar, type ProgressTone } from "../common/ProgressBar";
import { Chip } from "../common/Chip";
import { useT } from "../../state/languageStore";
import {
  POLL_COLLAPSED_MS,
  POLL_OPEN_MS,
  useUsageStore,
} from "../../state/usageStore";
import {
  formatBytes,
  formatDuration,
  formatTokens,
  hasSomethingToShow,
  headline,
  minutesUntil,
  minutesUntilExhausted,
  toneFor,
} from "../../lib/usage/format";
import type { UsageProvider, UsageWindow } from "../../types/domain";

/**
 * What the app and its agents are consuming, in the corner (USAGE-010).
 *
 * **A pill, not a status bar.** The window's bottom strip was removed on purpose in the redesign
 * (`App.tsx`, `HeaderGitActions.tsx` both say so) and its pieces were redistributed; putting a
 * permanent band back would undo that decision to show four numbers most people glance at twice a
 * day. So: a dot and a number at rest, the whole breakdown on hover or on click.
 *
 * Two cadences and a focus check, because a background app measuring itself every four seconds is
 * the sort of thing people uninstall a tool over.
 */
export function UsageIndicator() {
  const t = useT();
  const snapshot = useUsageStore((s) => s.snapshot);
  const loaded = useUsageStore((s) => s.loaded);
  const refresh = useUsageStore((s) => s.refresh);

  const [open, setOpen] = useState(false);
  const panel = useRef<HTMLDivElement>(null);

  // Polling: fast while somebody is reading it, slow while it is only a dot, and stopped whenever
  // the window is in the background.
  useEffect(() => {
    let timer: ReturnType<typeof setInterval> | null = null;

    const start = () => {
      if (timer !== null) return;
      void refresh();
      timer = setInterval(() => void refresh(), open ? POLL_OPEN_MS : POLL_COLLAPSED_MS);
    };
    const stop = () => {
      if (timer === null) return;
      clearInterval(timer);
      timer = null;
    };

    if (document.hasFocus()) start();
    window.addEventListener("focus", start);
    window.addEventListener("blur", stop);

    return () => {
      stop();
      window.removeEventListener("focus", start);
      window.removeEventListener("blur", stop);
    };
  }, [refresh, open]);

  // Escape closes, which is what the rest of the app's overlays do.
  useEffect(() => {
    if (!open) return;

    const onKeyDown = (event: KeyboardEvent) => {
      if (event.key === "Escape") setOpen(false);
    };
    window.addEventListener("keydown", onKeyDown);
    return () => window.removeEventListener("keydown", onKeyDown);
  }, [open]);

  // Nothing measured yet is nothing to show: the corner stays empty rather than holding a
  // placeholder that says "—" forever on a machine with no agents installed.
  if (!loaded || snapshot === null) return null;

  const { percent, tone } = headline(snapshot);

  return (
    <div
      className="pointer-events-auto fixed bottom-3 right-3 z-30 flex flex-col items-end gap-1.5"
      onMouseEnter={() => setOpen(true)}
      onMouseLeave={() => setOpen(false)}
    >
      {open && <UsagePanel ref={panel} />}

      <button
        type="button"
        onClick={() => setOpen((was) => !was)}
        aria-expanded={open}
        aria-label={t("usage.title")}
        className="cf-focusable flex items-center gap-1.5 rounded-full border border-[var(--cf-border)] bg-[var(--cf-surface-raised)] px-2 py-1 text-badge text-[var(--cf-text-muted)] shadow-[var(--cf-shadow-near)] hover:text-[var(--cf-text)]"
      >
        <span
          aria-hidden
          className="size-2 shrink-0 rounded-full"
          style={{ background: DOT[tone] }}
        />
        {percent === null ? t("usage.noLimit") : `${Math.round(percent)}%`}
      </button>
    </div>
  );
}

/** The resting dot's colour, by how close the nearest ceiling is. */
const DOT: Record<ReturnType<typeof toneFor>, string> = {
  success: "var(--cf-success)",
  warning: "var(--cf-warning)",
  danger: "var(--cf-danger)",
  neutral: "var(--cf-text-muted)",
};

function UsagePanel({ ref }: { ref: React.Ref<HTMLDivElement> }) {
  const t = useT();
  const snapshot = useUsageStore((s) => s.snapshot);
  const failed = useUsageStore((s) => s.failed);
  const now = Date.now();

  if (snapshot === null) return null;

  const measurable = snapshot.providers.filter(hasSomethingToShow);

  return (
    <div
      ref={ref}
      className="flex w-[290px] flex-col gap-3 rounded-lg border border-[var(--cf-border)] bg-[var(--cf-surface-raised)] p-3 shadow-[var(--cf-shadow)]"
    >
      <section className="flex flex-col gap-2">
        <Heading icon={Sparkles} label={t("usage.agents")} />
        {measurable.length === 0 ? (
          // Honest rather than empty: no agent here writes a transcript this app can read.
          <p className="text-badge text-[var(--cf-text-muted)]">{t("usage.nothingMeasured")}</p>
        ) : (
          measurable.map((provider) => <ProviderRow key={provider.provider} provider={provider} now={now} />)
        )}
      </section>

      <section className="flex flex-col gap-1.5">
        <Heading icon={Cpu} label={t("usage.process")} />
        <Line
          label={t("usage.cpu")}
          value={snapshot.resources.sampled ? `${snapshot.resources.cpu_percent.toFixed(1)}%` : "—"}
        />
        <Line label={t("usage.memory")} value={formatBytes(snapshot.resources.memory_bytes)} />
        {/* The renderer is a separate WebView process nothing here can see, so the panel says which
            process these numbers are about instead of implying they are the whole app. */}
        <p className="text-badge leading-snug text-[var(--cf-text-muted)]">{t("usage.processNote")}</p>
      </section>

      <section className="flex flex-col gap-1.5">
        <Heading icon={HardDrive} label={t("usage.data")} />
        <Line
          label={t("usage.onDisk")}
          value={`${snapshot.data.complete ? "" : "≥ "}${formatBytes(snapshot.data.bytes)}`}
        />
      </section>

      <section className="flex flex-col gap-1.5">
        <Heading icon={Activity} label={t("usage.activity")} />
        <Line label={t("usage.conversations")} value={String(snapshot.activity.conversations)} />
        <Line label={t("usage.jobs")} value={String(snapshot.activity.jobs)} />
      </section>

      {failed && <p className="text-badge text-[var(--cf-warning)]">{t("usage.stale")}</p>}
    </div>
  );
}

function ProviderRow({ provider, now }: { provider: UsageProvider; now: number }) {
  const t = useT();
  // The block is what somebody is about to hit; the week is the longer leash behind it. Both are
  // shown when both are known, labelled, because a single unlabelled percentage invites the reader
  // to assume it is the one they were worried about.
  const windows = [
    { label: t("usage.session"), window: provider.session },
    { label: t("usage.week"), window: provider.week },
  ].filter((entry): entry is { label: string; window: UsageWindow } => entry.window !== null);

  if (windows.length === 0) return null;
  const models = windows[0]!.window.models;

  return (
    <div className="flex flex-col gap-1.5">
      <div className="flex items-center justify-between gap-2">
        <span className="truncate text-ui text-[var(--cf-text)]">{provider.provider}</span>
        {provider.plan !== "" && <Chip tone="neutral">{provider.plan}</Chip>}
      </div>

      {windows.map(({ label, window }) => (
        <div key={label} className="flex flex-col gap-1">
          <div className="flex items-center justify-between gap-2">
            <span className="text-badge text-[var(--cf-text-muted)]">{label}</span>
            <span className="shrink-0 text-badge text-[var(--cf-text-muted)]">
              {t("usage.resetsIn", { time: formatDuration(minutesUntil(window.resets_at, now)) })}
            </span>
          </div>
          <WindowRow window={window} now={now} />
        </div>
      ))}

      {models.slice(0, 3).map((model) => (
        <div key={model.model} className="flex items-center justify-between gap-2 pl-2">
          <span className="truncate text-badge text-[var(--cf-text-muted)]">{model.model}</span>
          <span className="shrink-0 text-badge text-[var(--cf-text-muted)]">
            {formatTokens(model.tokens)}
          </span>
        </div>
      ))}
    </div>
  );
}

function WindowRow({ window, now }: { window: UsageWindow; now: number }) {
  const t = useT();
  const exhausted = minutesUntilExhausted(window, now);

  // Neither the provider's report nor a learned ceiling had anything to say, so there is nothing
  // to divide by. Saying so is the whole point: a percentage here would be invented (USAGE-006).
  if (window.percent === null) {
    return (
      <div className="flex items-center justify-between gap-2">
        <span className="text-badge text-[var(--cf-text-muted)]">{t("usage.notCalibrated")}</span>
        <span className="shrink-0 text-ui text-[var(--cf-text)]">{formatTokens(window.tokens)}</span>
      </div>
    );
  }

  return (
    <>
      <div className="flex items-center justify-between gap-2">
        <span className="text-ui text-[var(--cf-text)]">{Math.round(window.percent)}%</span>
        <span className="shrink-0 text-badge text-[var(--cf-text-muted)]">
          {formatTokens(window.tokens)}
          {window.ceiling !== null && ` / ${formatTokens(window.ceiling)}`}
        </span>
      </div>
      <ProgressBar
        percent={window.percent}
        tone={toneFor(window.percent) as ProgressTone}
        label={t("usage.title")}
      />
      {/* A figure the provider gave stands on its own; one this app worked out from watching the
          user run out says so, because the two are not the same claim (USAGE-011). */}
      {window.source === "observed" && (
        <p className="text-badge text-[var(--cf-text-muted)]">{t("usage.measuredHere")}</p>
      )}
      {exhausted !== null && (
        <p className="text-badge text-[var(--cf-warning)]">
          {t("usage.exhaustedIn", { time: formatDuration(exhausted) })}
        </p>
      )}
    </>
  );
}

function Heading({ icon: Icon, label }: { icon: typeof Cpu; label: string }) {
  return (
    <span className="flex items-center gap-1.5 text-badge font-semibold uppercase tracking-wide text-[var(--cf-text-muted)]">
      <Icon size={11} aria-hidden />
      {label}
    </span>
  );
}

function Line({ label, value }: { label: string; value: string }) {
  return (
    <div className="flex items-center justify-between gap-2">
      <span className="truncate text-badge text-[var(--cf-text-muted)]">{label}</span>
      <span className="shrink-0 text-ui text-[var(--cf-text)]">{value}</span>
    </div>
  );
}
