import { useMemo } from "react";
import { Copy, Trash2 } from "lucide-react";
import { Select } from "../common/Select";
import { IconButton } from "../common/IconButton";
import { duplicate, removeSelection, setEdgeKind, setEdgeLabel, setFill } from "../../lib/diagram/edits";
import { EDGE_KINDS, FILL_TOKENS, type EdgeKind, type FillToken } from "../../lib/diagram/model";
import { diagramPalette } from "../../lib/diagram/palette";
import { useDiagramStore } from "../../state/diagramStore";
import { useThemeStore } from "../../state/themeStore";
import { useT } from "../../state/languageStore";
import type { TranslationKey } from "../../lib/i18n/translations";

const FILL_LABELS = {
  none: "diagram.fill.none",
  neutral: "diagram.fill.neutral",
  accent: "diagram.fill.accent",
  success: "diagram.fill.success",
  warning: "diagram.fill.warning",
  danger: "diagram.fill.danger",
} as const satisfies Record<FillToken, TranslationKey>;

const EDGE_LABELS = {
  arrow: "diagram.edge.arrow",
  line: "diagram.edge.line",
  dotted: "diagram.edge.dotted",
  thick: "diagram.edge.thick",
} as const satisfies Record<EdgeKind, TranslationKey>;

/**
 * What can be changed about whatever is selected (DIAG-006).
 *
 * Shown only when something is: a column of disabled controls beside an empty canvas is a column
 * explaining what you cannot do. The colour is chosen as a **token**, never as a colour, which is
 * what keeps a diagram readable in both themes and in an export that has neither.
 */
export function DiagramInspector() {
  const t = useT();
  const theme = useThemeStore((s) => s.resolved);
  const palette = useMemo(() => diagramPalette(theme), [theme]);

  const selection = useDiagramStore((s) => s.selection);
  const doc = useDiagramStore((s) => s.history.present);
  const edit = useDiagramStore((s) => s.edit);
  const select = useDiagramStore((s) => s.select);

  const hasNodes = selection.nodes.length > 0;
  const hasEdges = selection.edges.length > 0;
  if (!hasNodes && !hasEdges) return null;

  const count = selection.nodes.length + selection.edges.length;

  const firstEdge = doc.edges.find((edge) => edge.id === selection.edges[0]) ?? null;

  return (
    <aside className="flex w-[196px] shrink-0 flex-col gap-3 overflow-y-auto border-l border-[var(--cf-border)] p-3">
      <header className="flex items-center justify-between">
        {/* Two keys rather than one: Spanish has no "1 seleccionados", and a count interpolated
            into a plural phrase reads as a bug the first time anybody selects one thing. */}
        <span className="text-ui text-[var(--cf-text-muted)]">
          {count === 1 ? t("diagram.selectedOne") : t("diagram.selectedMany", { count })}
        </span>
        <span className="flex gap-0.5">
          {hasNodes && (
            <IconButton
              label="diagram.duplicate"
              icon={Copy}
              onClick={() =>
                edit((current) => {
                  const copied = duplicate(current, selection.nodes);
                  // The copy becomes the selection, so the next drag moves what was just made
                  // rather than what it was made from.
                  select({ nodes: copied.ids, edges: [] });
                  return copied.doc;
                })
              }
            />
          )}
          <IconButton
            label="diagram.delete"
            icon={Trash2}
            variant="danger"
            onClick={() => {
              edit((current) => removeSelection(current, selection.nodes, selection.edges));
              select({ nodes: [], edges: [] });
            }}
          />
        </span>
      </header>

      {hasNodes && (
        <section className="flex flex-col gap-1.5">
          <span className="text-ui text-[var(--cf-text-muted)]">{t("diagram.fillLabel")}</span>
          <div className="flex flex-wrap gap-1">
            {FILL_TOKENS.map((token) => {
              const colours = palette.fills[token];
              return (
                <button
                  key={token}
                  type="button"
                  aria-label={t(FILL_LABELS[token])}
                  onClick={() => edit((current) => setFill(current, selection.nodes, token))}
                  className="cf-focusable size-6 rounded-control border"
                  style={{
                    // `none` has no fill to show, so its swatch shows the canvas with its outline —
                    // which is exactly what the shape will look like.
                    background: colours.fill === "none" ? palette.background : colours.fill,
                    borderColor: colours.stroke,
                  }}
                />
              );
            })}
          </div>
        </section>
      )}

      {firstEdge !== null && (
        <section className="flex flex-col gap-1.5">
          <span className="text-ui text-[var(--cf-text-muted)]">{t("diagram.edgeKindLabel")}</span>
          <Select
            value={firstEdge.kind}
            ariaLabel={t("diagram.edgeKindLabel")}
            size="sm"
            options={EDGE_KINDS.map((kind) => ({ value: kind, label: t(EDGE_LABELS[kind]) }))}
            onChange={(value) =>
              edit((current) => setEdgeKind(current, selection.edges, value as EdgeKind))
            }
          />

          <span className="text-ui text-[var(--cf-text-muted)]">{t("diagram.edgeLabelLabel")}</span>
          <input
            value={firstEdge.label}
            onChange={(event) =>
              edit((current) => setEdgeLabel(current, firstEdge.id, event.target.value), {
                // Typing is one gesture, not one step per letter.
                amend: true,
              })
            }
            placeholder={t("diagram.edgeLabelPlaceholder")}
            className="cf-focusable w-full rounded-control border border-[var(--cf-border)] bg-[var(--cf-bg)] px-2 py-1 text-ui text-[var(--cf-text)] outline-none"
          />
        </section>
      )}
    </aside>
  );
}
