import { tableKey } from "./identifiers";
import { parseDbmlModel } from "./parse";
import type { Point } from "./layout";
import type { DbmlSchemaModel, ParsedDbml } from "./model";

/**
 * The rules an edit made on a diagram card obeys (DBML-028).
 *
 * The canvas and the view are not testable here — the renderer's Vitest runs without a DOM — so
 * everything in them that is a *decision* lives in this module instead, which is the same reason
 * `documentPath.ts` is a module and not a function inside the new-document dialog. What is left in
 * the components is wiring: which handler goes on which element.
 */

/** What may happen to an edit between the card and the buffer. */
export type EditOutcome =
  | { kind: "apply"; source: string }
  | { kind: "stale" }
  | { kind: "missing" }
  | { kind: "refused"; error: string };

/**
 * Whether an edit made on a card may reach the buffer.
 *
 * Two gates, and both exist because a card can be showing something the buffer no longer says. The
 * diagram deliberately keeps the last model that parsed (`DBML-008`), so while the text is broken a
 * card may be drawing a table that is no longer written — acting through it would edit a document
 * the user is halfway through typing, which is `stale`. And an edit whose *result* does not parse
 * is refused, the same bargain the assistant's proposals make (`DBML-017`): renaming a table onto
 * one that already exists is a reasonable thing to try and an invalid document to keep.
 */
export function planEdit(buffer: ParsedDbml, next: string | null): EditOutcome {
  if (!buffer.ok) return { kind: "stale" };
  // The text operations answer `null` for a table or column they cannot find, which is what a card
  // outliving its table looks like from the other side.
  if (next === null) return { kind: "missing" };

  const candidate = parseDbmlModel(next);
  if (candidate.ok) return { kind: "apply", source: next };

  // The parser reports one line per diagnostic and a toast shows one line, so it shows the first.
  return { kind: "refused", error: candidate.error.split("\n")[0] ?? candidate.error };
}

/** What a cell being typed into should do when it is left. */
export type InlineEditOutcome = { kind: "commit"; value: string } | { kind: "cancel" };

/**
 * Trimmed, and nothing is an edit.
 *
 * Blank cancels rather than deleting the name, because an input emptied by a stray Ctrl+A is a
 * mistake far more often than it is an instruction. Unchanged cancels because committing it would
 * mark the document dirty for nothing.
 */
export function inlineEdit(draft: string, original: string): InlineEditOutcome {
  const value = draft.trim();
  if (value.length === 0 || value === original) return { kind: "cancel" };
  return { kind: "commit", value };
}

/**
 * How many relationships name this table — the number a delete has to warn about.
 *
 * Both directions, and a table that references itself counts once: it is one line in the document
 * and one line is what goes.
 */
export function relationsTouching(model: DbmlSchemaModel, key: string): number {
  return model.refs.filter((ref) => ref.from.tableKey === key || ref.to.tableKey === key).length;
}

/**
 * Where a renamed table's stored position has to move to, or `null` when there is nothing to move.
 *
 * Positions are keyed by the table's key and the key *is* its name (`DBML-005`), so without this a
 * card someone deliberately placed jumps to wherever the auto-layout would have put it. A rename
 * that only changes case is not a move: the key is lower-cased, so it is the same key.
 */
export function carriedPosition(
  positions: Readonly<Record<string, Point>>,
  table: { key: string; schema: string },
  name: string,
): { key: string; point: Point } | null {
  const point = positions[table.key];
  if (point === undefined) return null;

  const next = tableKey(table.schema, name.trim());
  return next === table.key ? null : { key: next, point };
}
