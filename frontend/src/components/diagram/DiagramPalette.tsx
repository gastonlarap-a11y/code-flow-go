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
  basic: "diagram.group.basic",
  flow: "diagram.group.flow",
  container: "diagram.group.container",
} as const satisfies Record<StencilGroup, TranslationKey>;

/** The box a preview is drawn in. Everything is scaled to fit it, keeping its proportions. */
const PREVIEW = 46;

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
  // All three open: the catalogue is seventeen figures now, which fits without scrolling. It was
  // thirty when two of the groups started closed.
  const [collapsed, setCollapsed] = useState<ReadonlySet<StencilGroup>>(new Set());

  const toggle = (group: StencilGroup) =>
    setCollapsed((current) => {
      const next = new Set(current);
      if (!next.delete(group)) next.add(group);
      return next;
    });

  return (
    <div className="flex w-[184px] shrink-0 flex-col gap-1 overflow-y-auto p-2">
      {STENCIL_GROUPS.map((group) => {
        const open = !collapsed.has(group);
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
                {STENCILS.filter((stencil) => stencil.group === group).map((stencil) => {
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
