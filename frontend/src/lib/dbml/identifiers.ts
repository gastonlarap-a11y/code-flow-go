/**
 * How a DBML name is written, and how one is compared (DBML-006).
 *
 * Three rules that each lived in two places: the schema a table falls under when the document names
 * none, the key a table is known by everywhere else in the app, and the quoting a name needs to
 * survive being written back out. `parse.ts` cannot host them — it imports the 15 MB parser, and
 * both the emitter and the text editor need these without it.
 */

/** The schema `@dbml/core` files a table under when the document names none. */
export const DEFAULT_SCHEMA = "public";

/** The identity of a table across re-parses, in stored layouts, and in this module's edits. */
export function tableKey(schema: string | null | undefined, name: string): string {
  return `${schema || DEFAULT_SCHEMA}.${name}`.toLowerCase();
}

/**
 * A DBML identifier: bare when it can be, double-quoted when it cannot.
 *
 * Quoting everything would be simpler and is what `@dbml/core`'s own exporter does, but a document a
 * person is about to edit reads far better without it — and this text is handed straight to the
 * editor, not to a compiler.
 */
export function dbmlIdentifier(raw: string): string {
  return /^[A-Za-z_]\w*$/.test(raw) ? raw : `"${raw.replace(/"/g, '\\"')}"`;
}
