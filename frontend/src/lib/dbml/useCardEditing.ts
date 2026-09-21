import {
  lineAndColumn,
  removeTable,
  renameColumn,
  renameTable,
  retypeColumn,
  tableNameSpan,
} from "./editDbml";
import { planEdit, relationsTouching } from "./cardEdit";
import { parseDbmlModel } from "./parse";
import type { DbmlSchemaModel, DbmlTableModel, ParsedDbml } from "./model";
import type { DbmlCanvasEditing } from "../../components/dbml/DbmlCanvas";
import { confirmAction } from "../../state/confirmStore";
import { pushErrorToast } from "../../state/toastStore";
import { translate } from "../../state/languageStore";

/**
 * What a diagram card can do to the document behind it, for either place the diagram is drawn
 * (DBML-028).
 *
 * The schema module and the Editor's preview reach the same canvas by different doors, and they
 * used to disagree about what it could do — the preview was read-only on the reasoning that the
 * Editor owns its buffer. That reasoning was wrong from the user's side: the Editor is how most
 * people open a `.dbml` in the first place, so the feature was invisible exactly where it was
 * looked for. A buffer with an owner is not a buffer that cannot be written to; it is one that has
 * somewhere to write.
 *
 * So both hosts pass what they own — how to read the buffer, how to replace it, how to show a line
 * — and this owns the rules. The rules themselves are `cardEdit.ts`, which is pure and tested; this
 * is the part that needs a store and a dialog.
 *
 * Named `use…` although it calls no hook of its own: it is called from render, both callers already
 * treat it as one, and the day it needs a `useRef` no call site has to move.
 */
export interface CardEditingHost {
  /**
   * The buffer as it is *now*.
   *
   * A function rather than a value because the delete path asks a question first, and the source
   * pane stays live while a dialog is open.
   */
  readSource: () => string;
  /** The buffer's parse, which the canvas is already drawing from. */
  parsed: ParsedDbml;
  /** The model on the screen, for counting what a delete takes with it. */
  model: DbmlSchemaModel;
  /** Where an accepted edit goes. */
  onSource: (next: string) => void;
  /** Show this span of the source, wherever this host shows source. */
  onReveal: (from: SourcePosition, to: SourcePosition) => void;
  /** After a rename lands — a host that stores positions per table key carries one over here. */
  onRenamed?: (table: DbmlTableModel, name: string) => void;
}

export interface SourcePosition {
  line: number;
  column: number;
}

export function useCardEditing(host: CardEditingHost): DbmlCanvasEditing {
  // `translate` rather than `useT`: this is not a component, and every string here is read once at
  // the moment of the action rather than rendered.
  const report = (next: string | null, buffer: ParsedDbml): boolean => {
    const outcome = planEdit(buffer, next);
    switch (outcome.kind) {
      case "stale":
        pushErrorToast(translate("dbml.edit.invalid"));
        return false;
      case "missing":
        pushErrorToast(translate("dbml.edit.notFound"));
        return false;
      case "refused":
        pushErrorToast(translate("dbml.edit.refused", { error: outcome.error }));
        return false;
      case "apply":
        host.onSource(outcome.source);
        return true;
    }
  };

  const apply = (next: string | null) => report(next, host.parsed);

  return {
    renameTable: (key, name) => {
      const source = host.readSource();
      const table = host.model.tables.find((entry) => entry.key === key);
      if (table === undefined || !apply(renameTable(source, key, name))) return;
      host.onRenamed?.(table, name);
    },

    renameColumn: (key, column, name) => void apply(renameColumn(host.readSource(), key, column, name)),
    retypeColumn: (key, column, type) => void apply(retypeColumn(host.readSource(), key, column, type)),

    deleteTable: (key) => {
      const table = host.model.tables.find((entry) => entry.key === key);
      if (table === undefined) return;

      const related = relationsTouching(host.model, key);
      const message =
        related === 0
          ? translate("dbml.table.deleteConfirm", { name: table.name })
          : translate("dbml.table.deleteConfirmRelated", { name: table.name, n: related });

      void confirmAction(message, true, translate("dbml.table.delete")).then((confirmed) => {
        if (!confirmed) return;
        // Read and re-parse after the dialog rather than closing over what was on screen before it:
        // the source pane is live the whole time it is open.
        const live = host.readSource();
        report(removeTable(live, key), parseDbmlModel(live));
      });
    },

    revealTable: (key) => {
      const source = host.readSource();
      const span = tableNameSpan(source, key);
      if (span === null) {
        pushErrorToast(translate("dbml.edit.notFound"));
        return;
      }
      host.onReveal(lineAndColumn(source, span.start), lineAndColumn(source, span.end));
    },
  };
}
