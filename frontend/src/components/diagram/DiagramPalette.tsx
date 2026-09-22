import { useMemo, useState } from "react";
import { ChevronDown } from "lucide-react";
import { STENCILS, STENCIL_GROUPS, type StencilGroup, type StencilId } from "../../lib/diagram/stencils";
import { diagramPalette } from "../../lib/diagram/palette";
import { shapeElements } from "../../lib/diagram/shapes";
import { useThemeStore } from "../../state/themeStore";
import { useT } from "../../state/languageStore";
import type { TranslationKey } from "../../lib/i18n/translations";
import { ShapeGlyph } from "./ShapeGlyph";

/**
 * The figures, to pick from (DIAG-006, DIAG-012).
 *
 * Every entry is drawn with the same `shapeElements` the canvas uses, at a preview size, so what is
 * in the palette is what lands on the canvas — a picture of a shape drawn separately is a picture
 * that goes stale.
 *
 * **Click to place, not drag to place.** Hand-rolled pointer drags are what this app does
 * everywhere else (`lib/pointerDrag.ts`) because a native webview drag handler swallows HTML5
 * drag-and-drop, and a palette whose only way in is a gesture that silently does nothing on one of
 * the two platforms is worse than a click. The shape lands in the middle of the view and is
 * selected, ready to be dragged where it belongs.
 */
const GROUP_LABELS = {
  process: "diagram.group.process",
  control: "diagram.group.control",
  terminal: "diagram.group.terminal",
  data: "diagram.group.data",
  document: "diagram.group.document",
  system: "diagram.group.system",
  note: "diagram.group.note",
  container: "diagram.group.container",
} as const satisfies Record<StencilGroup, TranslationKey>;

/** The box a preview is drawn in. Everything is scaled to fit it, keeping its proportions. */
const PREVIEW = 46;

/**
 * Matching is done on the **translated** label, because that is the word somebody has in mind.
 * Accents are folded away so "decision" finds "decisión": typing the accent is work, and not
 * finding the shape because of it reads as the shape not existing.
 */
function fold(text: string): string {
  return text
    .normalize("NFD")
    .replace(/\p{Diacritic}/gu, "")
    .toLowerCase();
}

export function DiagramPalette({
  onPlace,
  disabled,
}: {
  onPlace: (kind: StencilId) => void;
  disabled: boolean;
}) {
  const t = useT();
  const theme = useThemeStore((s) => s.resolved);
  const palette = useMemo(() => diagramPalette(theme), [theme]);
  const [collapsed, setCollapsed] = useState<ReadonlySet<StencilGroup>>(new Set());
  const [filter, setFilter] = useState("");

  const toggle = (group: StencilGroup) =>
    setCollapsed((current) => {
      const next = new Set(current);
      if (!next.delete(group)) next.add(group);
      return next;
    });

  // Fifty figures is more than anybody scans. The filter is what makes the catalogue reachable:
  // it is matched against the label, and a group with nothing left in it disappears rather than
  // showing an empty heading.
  const needle = fold(filter.trim());
  const matching = useMemo(
    () =>
      needle === ""
        ? STENCILS
        : STENCILS.filter((stencil) => fold(t(stencil.labelKey)).includes(needle)),
    [needle, t],
  );

  return (
    <div className="flex w-[184px] shrink-0 flex-col gap-1 overflow-y-auto p-2">
      <input
        type="search"
        value={filter}
        onChange={(event) => setFilter(event.target.value)}
        placeholder={t("diagram.paletteFilter")}
        aria-label={t("diagram.paletteFilter")}
        className="cf-focusable mb-1 w-full rounded-control border border-[var(--cf-border)] bg-[var(--cf-bg)] px-1.5 py-1 text-ui text-[var(--cf-text)] outline-none"
      />

      {matching.length === 0 && (
        <p className="px-1 py-2 text-ui text-[var(--cf-text-muted)]">{t("diagram.paletteNoMatch")}</p>
      )}

      {STENCIL_GROUPS.map((group) => {
        const inGroup = matching.filter((stencil) => stencil.group === group);
        if (inGroup.length === 0) return null;
        // While filtering, a collapsed group would hide the very thing that was searched for.
        const open = needle !== "" || !collapsed.has(group);
        return (
          <section key={group}>
            <button
              type="button"
              onClick={() => toggle(group)}
              aria-expanded={open}
              className="cf-focusable flex w-full items-center gap-1 rounded-control px-1 py-1 text-ui text-[var(--cf-text-muted)] hover:text-[var(--cf-text)]"
            >
              <ChevronDown
                size={12}
                className={open ? "transition-transform" : "-rotate-90 transition-transform"}
                aria-hidden="true"
              />
              {t(GROUP_LABELS[group])}
            </button>

            {open && (
              <div className="grid grid-cols-3 gap-1 p-1">
                {inGroup.map((stencil) => {
                  const scale = PREVIEW / Math.max(stencil.width, stencil.height);
                  const width = Math.round(stencil.width * scale);
                  const height = Math.round(stencil.height * scale);

                  return (
                    <button
                      key={stencil.id}
                      type="button"
                      disabled={disabled}
                      onClick={() => onPlace(stencil.id)}
                      aria-label={t(stencil.labelKey)}
                      className="cf-focusable flex aspect-square items-center justify-center rounded-control border border-transparent hover:border-[var(--cf-border)] hover:bg-[var(--cf-surface-raised)] disabled:opacity-40"
                    >
                      {/* A stencil with no outline — the plain text label — draws nothing at all,
                          so its cell was an invisible button. It shows what it writes instead. */}
                      {shapeElements(stencil.id, width, height).length === 0 ? (
                        <span className="text-relaxed" style={{ color: palette.fills.none.text }}>
                          T
                        </span>
                      ) : (
                        <span className="relative block" style={{ width, height }}>
                          <ShapeGlyph
                            kind={stencil.id}
                            width={width}
                            height={height}
                            fill={stencil.defaultFill}
                            palette={palette}
                          />
                        </span>
                      )}
                    </button>
                  );
                })}
              </div>
            )}
          </section>
        );
      })}
    </div>
  );
}
