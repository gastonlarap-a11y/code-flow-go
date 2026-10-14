import type { ReactNode } from "react";
import { KIND_ICONS, kindLabel, location, qualifier, shortName } from "../../lib/codemap/labels";
import { useT } from "../../state/languageStore";
import { useUiStore } from "../../state/uiStore";
import type { CodemapSymbolRef } from "../../types/domain";

/**
 * One symbol in any list of the code map: what it is, its name, where it lives.
 *
 * Two lines, because a path does not fit beside a name in a 320px column and the name is what a
 * person scans for: the name and its qualifier on top, the location under it. Two controls rather
 * than one row-sized button, because they do different things — the name moves the map to that
 * symbol, the location opens its file at the line — and a button inside a button is not valid HTML.
 */
export function SymbolRow({
  symbol,
  onSelect,
  lead,
  trail,
  site,
  selected = false,
}: {
  symbol: CodemapSymbolRef;
  onSelect: (id: string) => void;
  /** Before the icon: the edge that led here, in a flow. */
  lead?: ReactNode;
  /** At the end of the first line: chips, counts. */
  trail?: ReactNode;
  /** Where the relation happens, when that is not where the symbol is declared — a call site. */
  site?: { file: string; line: number } | undefined;
  selected?: boolean;
}) {
  const t = useT();
  const openInEditor = useUiStore((s) => s.openInEditor);
  const Icon = KIND_ICONS[symbol.kind];
  const declared = location(symbol.file, symbol.line);
  const at = site && (site.file !== symbol.file || site.line !== symbol.line) ? site : null;
  const qualified = qualifier(symbol.name);

  return (
    <div
      className={`cf-interactive flex min-w-0 items-start gap-1.5 rounded-control px-1.5 py-1 ${
        selected
          ? "bg-[var(--cf-accent-soft)]"
          : "hover:bg-[color-mix(in_oklab,currentColor_calc(var(--cf-overlay-hover)*100%),transparent)]"
      }`}
    >
      {lead && <span className="flex shrink-0 items-center gap-1 pt-px">{lead}</span>}
      <Icon size={14} className="mt-[3px] shrink-0 text-[var(--cf-text-muted)]" aria-hidden />
      <div className="flex min-w-0 flex-1 flex-col">
        <div className="flex min-w-0 items-center gap-1.5">
          <button
            type="button"
            onClick={() => onSelect(symbol.id)}
            className="cf-focusable flex min-w-0 items-baseline gap-1.5 rounded-control text-left"
            aria-label={`${t(kindLabel(symbol.kind))} ${symbol.name}`}
          >
            <span className="truncate text-body font-medium text-[var(--cf-text)]">{shortName(symbol.name)}</span>
            {qualified && <span className="truncate text-ui text-[var(--cf-text-muted)]">{qualified}</span>}
          </button>
          {trail && <span className="ml-auto flex shrink-0 items-center gap-1">{trail}</span>}
        </div>
        {(symbol.file || at) && (
          <div className="flex min-w-0 items-center gap-2">
            {symbol.file && (
              <LocationButton file={symbol.file} line={symbol.line} label={declared} onOpen={openInEditor} />
            )}
            {at && (
              <LocationButton
                file={at.file}
                line={at.line}
                label={`↳ ${at.file === symbol.file ? `:${at.line}` : location(at.file, at.line)}`}
                onOpen={openInEditor}
              />
            )}
          </div>
        )}
      </div>
    </div>
  );
}

function LocationButton({
  file,
  line,
  label,
  onOpen,
}: {
  file: string;
  line: number;
  label: string;
  onOpen: (file: string, line?: number) => void;
}) {
  const t = useT();
  return (
    <button
      type="button"
      onClick={() => onOpen(file, line > 0 ? line : undefined)}
      // `direction: rtl` truncates a path at its start, so the file name and line — the part that
      // tells two locations apart — stay visible in a narrow column.
      className="cf-focusable min-w-0 truncate rounded-control text-left font-mono text-badge text-[var(--cf-text-muted)] [direction:rtl] hover:text-[var(--cf-accent)] hover:underline"
      aria-label={t("codemap.openInEditor", { location: location(file, line) })}
    >
      <bdi>{label}</bdi>
    </button>
  );
}
