import { DEFAULT_SCHEMA, dbmlIdentifier, tableKey } from "./identifiers";

/**
 * Editing a DBML document as text (DBML-027).
 *
 * The diagram is drawn from the model `parse.ts` builds, but what the user keeps is the *document*:
 * its comments, its blank lines, the order they wrote things in. So an edit made on a card is a
 * surgical replacement inside the source text, never a re-emission of the model — re-emitting would
 * hand back a file they did not write, with every comment gone, the first time they renamed a
 * column.
 *
 * That means a second reader of DBML, and it is deliberately a shallow one: a brace scanner that
 * knows **where names are written**, not what they mean. It is the same shape `importers/prisma.ts`
 * uses, and like every other module here it is pure, so the renderer's node tests drive it without
 * the 15 MB parser and without a DOM.
 *
 * **The caller parses the result before committing it** (`DBML-028`). This refuses what it cannot
 * find — an unknown table comes back as `null` — but it does not judge what it produces: renaming a
 * table onto one that already exists is a reasonable edit to attempt and an invalid document to
 * have, and the real parser is what says so.
 */

/** A half-open range of the document, in UTF-16 code units. */
export interface Span {
  start: number;
  end: number;
}

/** A name as written, with the text it occupies. */
export interface NameSpan {
  /** Unquoted: `"order details"` reads as `order details`, which is what the model carries. */
  name: string;
  span: Span;
}

/** One end of a relationship, as it is written. */
export interface EndpointSpan {
  /** Only when the endpoint is qualified; an unqualified one may be a table **or an alias**. */
  schema: string | null;
  table: string;
  tableSpan: Span;
  columns: readonly NameSpan[];
}

/** One `[key: value]` entry. */
export interface SettingSpan {
  /** Lower-cased, since DBML's own setting names are not case-sensitive. */
  key: string;
  span: Span;
  /** What follows the colon, when the setting has a value. */
  value: Span | null;
}

export interface SettingsSpan {
  /** The whole list, brackets included. */
  span: Span;
  items: readonly SettingSpan[];
}

/** An inline `[ref: > other.id]` — a relationship written on the column that holds the key. */
export interface InlineRefSpan {
  /** Which entry of the column's settings this is, so several can be cut together. */
  index: number;
  endpoint: EndpointSpan;
}

export interface ColumnSpan {
  name: string;
  nameSpan: Span;
  /** The type as written, arguments and `[]` included. */
  typeSpan: Span;
  settings: SettingsSpan | null;
  inlineRefs: readonly InlineRefSpan[];
}

export interface TableSpan {
  key: string;
  schema: string;
  name: string;
  /** `Table users as U` — refs may name the table through this, and a rename must not touch it. */
  alias: string | null;
  /** The whole `Table … { … }` statement. */
  span: Span;
  nameSpan: Span;
  columns: readonly ColumnSpan[];
  /** Column names written inside this table's `Indexes` block. */
  indexed: readonly NameSpan[];
}

export interface RefSpan {
  /** The whole `Ref …` statement. Several relationships can share one `Ref { … }` block. */
  statement: Span;
  /** This relationship on its own. Equal to `statement` for the `Ref:` form. */
  span: Span;
  endpoints: readonly EndpointSpan[];
}

/** One table named inside a `TableGroup` block. */
export interface GroupEntrySpan {
  key: string;
  /** The whole entry, `schema.table` included. */
  span: Span;
  /** Just the table's own name. */
  nameSpan: Span;
}

export interface DocumentSpans {
  tables: readonly TableSpan[];
  refs: readonly RefSpan[];
  groupEntries: readonly GroupEntrySpan[];
}

// ---------- the public operations ----------

/** Where a table's name is written, for an editor that wants to put the caret on it. */
export function tableNameSpan(source: string, key: string): Span | null {
  return scanDocument(source).tables.find((table) => table.key === key)?.nameSpan ?? null;
}

/** An offset as a 1-based line and column, which is how every editor counts. */
export function lineAndColumn(source: string, offset: number): { line: number; column: number } {
  const upTo = source.slice(0, Math.max(0, Math.min(offset, source.length)));
  const lastBreak = upTo.lastIndexOf("\n");
  return { line: upTo.split("\n").length, column: upTo.length - lastBreak };
}

/**
 * The table, and every relationship that named it.
 *
 * Leaving the relationships behind does not leave a document with a dangling line in it — it leaves
 * one that **does not parse at all**: `@dbml/core` answers a reference to a table it cannot find
 * with `Table 'x' does not exist in Schema 'public'`, and the same is true of a `TableGroup` entry.
 * So they are not tidied up afterwards; they are part of the deletion.
 */
export function removeTable(source: string, key: string): string | null {
  const spans = scanDocument(source);
  const table = spans.tables.find((entry) => entry.key === key);
  if (table === undefined) return null;

  const aliases = aliasIndex(spans.tables);
  const cuts: Span[] = [statementCut(source, table.span)];

  // A `Ref { … }` block can hold more than one relationship. It goes whole only when every one of
  // them named this table; otherwise just the lines that did.
  for (const group of byStatement(spans.refs)) {
    const doomed = group.filter((ref) => ref.endpoints.some((end) => endpointKey(end, aliases) === key));
    if (doomed.length === 0) continue;
    if (doomed.length === group.length) cuts.push(statementCut(source, group[0]!.statement));
    else for (const ref of doomed) cuts.push(statementCut(source, ref.span));
  }

  // Relationships written on another table's column. The deleted table's own are inside its block.
  //
  // Cut per column rather than per relationship: one column can hold two of them, and two cuts
  // computed in isolation both claim the comma between them and overlap.
  for (const other of spans.tables) {
    if (other.key === key) continue;
    for (const column of other.columns) {
      if (column.settings === null) continue;
      const doomed = new Set(
        column.inlineRefs
          .filter((inline) => endpointKey(inline.endpoint, aliases) === key)
          .map((inline) => inline.index),
      );
      cuts.push(...settingCuts(source, column.settings, doomed));
    }
  }

  for (const entry of spans.groupEntries) {
    if (entry.key === key) cuts.push(statementCut(source, entry.span));
  }

  return applyCuts(source, cuts);
}

/** The table's name, wherever the document writes it — its own header, its refs, its groups. */
export function renameTable(source: string, key: string, to: string): string | null {
  const next = to.trim();
  if (next.length === 0 || next.includes("\n")) return null;

  const spans = scanDocument(source);
  const table = spans.tables.find((entry) => entry.key === key);
  if (table === undefined) return null;

  const aliases = aliasIndex(spans.tables);
  const written = dbmlIdentifier(next);
  const edits: Replacement[] = [{ span: table.nameSpan, text: written }];

  const rewrite = (endpoint: EndpointSpan) => {
    // An endpoint written through the alias keeps the alias: this renames the table, not the
    // shorthand the document chose for it.
    if (endpoint.schema === null && aliases.has(endpoint.table.toLowerCase())) return;
    if (tableKey(endpoint.schema, endpoint.table) !== key) return;
    edits.push({ span: endpoint.tableSpan, text: written });
  };

  forEachEndpoint(spans, rewrite);
  for (const entry of spans.groupEntries) {
    if (entry.key === key) edits.push({ span: entry.nameSpan, text: written });
  }

  return applyReplacements(source, edits);
}

/** The column's name, in the table that declares it and in everything that references it. */
export function renameColumn(source: string, key: string, from: string, to: string): string | null {
  const next = to.trim();
  if (next.length === 0 || next.includes("\n")) return null;

  const spans = scanDocument(source);
  const table = spans.tables.find((entry) => entry.key === key);
  const column = table?.columns.find((entry) => entry.name === from);
  if (table === undefined || column === undefined) return null;

  const aliases = aliasIndex(spans.tables);
  const written = dbmlIdentifier(next);
  const edits: Replacement[] = [{ span: column.nameSpan, text: written }];

  forEachEndpoint(spans, (endpoint) => {
    if (endpointKey(endpoint, aliases) !== key) return;
    for (const named of endpoint.columns) {
      if (named.name === from) edits.push({ span: named.span, text: written });
    }
  });

  // An index over a column that no longer exists is the same parse error a dangling ref is.
  for (const indexed of table.indexed) {
    if (indexed.name === from) edits.push({ span: indexed.span, text: written });
  }

  return applyReplacements(source, edits);
}

/** The column's type, which nothing else in the document refers to. */
export function retypeColumn(source: string, key: string, column: string, to: string): string | null {
  const next = to.trim();
  if (next.length === 0 || next.includes("\n")) return null;

  const spans = scanDocument(source);
  const found = spans.tables.find((entry) => entry.key === key)?.columns.find((entry) => entry.name === column);
  if (found === undefined) return null;

  return applyReplacements(source, [{ span: found.typeSpan, text: next }]);
}

// ---------- applying edits ----------

interface Replacement {
  span: Span;
  text: string;
}

/** Applied back to front, so an earlier edit never moves a later one's offsets. */
function applyReplacements(source: string, edits: readonly Replacement[]): string {
  return [...edits]
    .sort((a, b) => b.span.start - a.span.start)
    .reduce((text, edit) => text.slice(0, edit.span.start) + edit.text + text.slice(edit.span.end), source);
}

/**
 * The same, for removals — and it drops a cut that reaches into one already made.
 *
 * Nothing here is built to overlap, but a cut is grown to swallow the blank line it would leave
 * behind, and a wrong one would silently eat a neighbour's text rather than fail.
 */
function applyCuts(source: string, cuts: readonly Span[]): string {
  let out = source;
  let limit = source.length;
  for (const cut of [...cuts].sort((a, b) => b.start - a.start)) {
    if (cut.end > limit) continue;
    out = out.slice(0, cut.start) + out.slice(cut.end);
    limit = cut.start;
  }
  return out;
}

/** A statement, grown to take its own indentation and the blank lines it would leave behind. */
function statementCut(source: string, span: Span): Span {
  let start = span.start;
  while (start > 0 && isBlank(source[start - 1])) start--;

  let end = span.end;
  for (;;) {
    let probe = end;
    while (probe < source.length && isBlank(source[probe])) probe++;
    if (source[probe] !== "\n") break;
    end = probe + 1;
  }
  return { start, end };
}

function isBlank(char: string | undefined): boolean {
  return char === " " || char === "\t" || char === "\r";
}

// ---------- resolving what an endpoint points at ----------

function aliasIndex(tables: readonly TableSpan[]): ReadonlyMap<string, string> {
  const out = new Map<string, string>();
  for (const table of tables) {
    if (table.alias !== null) out.set(table.alias.toLowerCase(), table.key);
  }
  return out;
}

/** An unqualified endpoint may be naming an alias; a qualified one never is. */
function endpointKey(endpoint: EndpointSpan, aliases: ReadonlyMap<string, string>): string {
  if (endpoint.schema === null) {
    const byAlias = aliases.get(endpoint.table.toLowerCase());
    if (byAlias !== undefined) return byAlias;
  }
  return tableKey(endpoint.schema, endpoint.table);
}

/** Every endpoint in the document: the standalone relationships and the inline ones alike. */
function forEachEndpoint(spans: DocumentSpans, visit: (endpoint: EndpointSpan) => void): void {
  for (const ref of spans.refs) for (const endpoint of ref.endpoints) visit(endpoint);
  for (const table of spans.tables) {
    for (const column of table.columns) {
      for (const inline of column.inlineRefs) visit(inline.endpoint);
    }
  }
}

function byStatement(refs: readonly RefSpan[]): RefSpan[][] {
  const groups = new Map<number, RefSpan[]>();
  for (const ref of refs) {
    const group = groups.get(ref.statement.start);
    if (group) group.push(ref);
    else groups.set(ref.statement.start, [ref]);
  }
  return [...groups.values()];
}

// ---------- the scanner ----------

/** A position in the document being read. `at` advances; `text` never changes. */
interface Reader {
  readonly text: string;
  at: number;
}

const QUOTES = new Set(["'", '"', "`"]);
const NAME_CHAR = /[A-Za-z0-9_]/;
/** Longest first, or `<>` would read as `<` followed by a broken endpoint. */
const OPERATORS = ["<>", "<?", "?>", "<", ">", "-"];

/** Every name this module can act on, with the text each one occupies. */
export function scanDocument(source: string): DocumentSpans {
  const reader: Reader = { text: source, at: 0 };
  const tables: TableSpan[] = [];
  const refs: RefSpan[] = [];
  const groupEntries: GroupEntrySpan[] = [];

  while (reader.at < source.length) {
    skipTrivia(reader);
    if (reader.at >= source.length) break;

    const start = reader.at;
    const keyword = readName(reader);
    // Not a word at all: step over it rather than stop. A document being typed into is malformed
    // most of the time, and this still has to find the tables that are whole.
    if (keyword === null) {
      reader.at++;
      continue;
    }

    switch (keyword.name.toLowerCase()) {
      case "table": {
        const table = readTable(reader, start);
        if (table !== null) tables.push(table);
        break;
      }
      case "ref":
        readRef(reader, start, refs);
        break;
      case "tablegroup":
        readGroup(reader, groupEntries);
        break;
      default:
        skipStatement(reader);
    }
  }

  return { tables, refs, groupEntries };
}

/** Past whitespace, line comments and block comments alike. */
function skipTrivia(reader: Reader): void {
  const { text } = reader;
  for (;;) {
    while (reader.at < text.length && /\s/.test(text[reader.at]!)) reader.at++;
    if (text.startsWith("//", reader.at)) {
      const end = text.indexOf("\n", reader.at);
      reader.at = end === -1 ? text.length : end;
      continue;
    }
    if (text.startsWith("/*", reader.at)) {
      const end = text.indexOf("*/", reader.at + 2);
      reader.at = end === -1 ? text.length : end + 2;
      continue;
    }
    return;
  }
}

/** Past the literal starting at the cursor, which the caller has checked is a quote. */
function skipString(reader: Reader): void {
  const { text } = reader;
  const quote = text[reader.at]!;

  // `'''…'''` is a multi-line note, and its fence is three quotes rather than one.
  if (quote === "'" && text.startsWith("'''", reader.at)) {
    const end = text.indexOf("'''", reader.at + 3);
    reader.at = end === -1 ? text.length : end + 3;
    return;
  }

  for (let at = reader.at + 1; at < text.length; at++) {
    if (text[at] === "\\") {
      at++;
      continue;
    }
    if (text[at] === quote) {
      reader.at = at + 1;
      return;
    }
  }
  reader.at = text.length;
}

/** A bare or quoted identifier, after trivia. */
function readName(reader: Reader): NameSpan | null {
  skipTrivia(reader);
  const { text } = reader;
  const start = reader.at;
  const first = text[start];
  if (first === undefined) return null;

  if (QUOTES.has(first)) {
    skipString(reader);
    return { name: unquote(text.slice(start, reader.at)), span: { start, end: reader.at } };
  }

  while (reader.at < text.length && NAME_CHAR.test(text[reader.at]!)) reader.at++;
  if (reader.at === start) return null;
  return { name: text.slice(start, reader.at), span: { start, end: reader.at } };
}

function unquote(raw: string): string {
  if (raw.length >= 2 && QUOTES.has(raw[0]!)) return raw.slice(1, -1).replace(/\\(.)/g, "$1");
  return raw;
}

/** Consumes the given text when it is what comes next, after trivia. */
function eat(reader: Reader, what: string): boolean {
  skipTrivia(reader);
  if (!reader.text.startsWith(what, reader.at)) return false;
  reader.at += what.length;
  return true;
}

/** With the cursor on a `{`, the body between the braces; the cursor ends past the `}`. */
function readBlock(reader: Reader): Span {
  const { text } = reader;
  const open = reader.at;
  let depth = 0;

  while (reader.at < text.length) {
    const char = text[reader.at]!;
    if (QUOTES.has(char)) {
      skipString(reader);
      continue;
    }
    if (text.startsWith("//", reader.at) || text.startsWith("/*", reader.at)) {
      skipTrivia(reader);
      continue;
    }
    if (char === "{") depth++;
    else if (char === "}") {
      depth--;
      reader.at++;
      if (depth === 0) return { start: open + 1, end: reader.at - 1 };
      continue;
    }
    reader.at++;
  }
  return { start: open + 1, end: text.length };
}

/**
 * A statement this module does not act on: an `Enum`, a `Project`, a `Note`, or a keyword that is
 * none of the above.
 *
 * It looks ahead on a copy and only commits when it finds a block, so a stray word cannot swallow
 * the statement that follows it.
 */
function skipStatement(reader: Reader): void {
  const afterKeyword = reader.at;

  if (eat(reader, ":")) {
    skipTrivia(reader);
    if (QUOTES.has(reader.text[reader.at] ?? "")) skipString(reader);
    else readName(reader);
    return;
  }

  const probe: Reader = { text: reader.text, at: reader.at };
  readName(probe);
  readSettings(probe);
  skipTrivia(probe);
  if (probe.text[probe.at] === "{") {
    reader.at = probe.at;
    readBlock(reader);
    return;
  }

  reader.at = afterKeyword;
}

function readTable(reader: Reader, start: number): TableSpan | null {
  const first = readName(reader);
  if (first === null) return null;

  let schema: string | null = null;
  let name = first;
  if (eat(reader, ".")) {
    const qualified = readName(reader);
    if (qualified === null) return null;
    schema = first.name;
    name = qualified;
  }

  // `as` is a word like any other, so it counts as the alias marker only when a name follows it.
  let alias: string | null = null;
  const probe: Reader = { text: reader.text, at: reader.at };
  const marker = readName(probe);
  if (marker !== null && marker.name.toLowerCase() === "as") {
    const written = readName(probe);
    if (written !== null) {
      alias = written.name;
      reader.at = probe.at;
    }
  }

  readSettings(reader);
  skipTrivia(reader);
  if (reader.text[reader.at] !== "{") return null;

  const body = readBlock(reader);
  const { columns, indexed } = readTableBody(reader.text, body);

  return {
    key: tableKey(schema, name.name),
    schema: schema ?? DEFAULT_SCHEMA,
    name: name.name,
    alias,
    span: { start, end: reader.at },
    nameSpan: name.span,
    columns,
    indexed,
  };
}

/**
 * The entries of a table's body.
 *
 * The reader is given the document truncated to the body's end, which keeps every offset absolute
 * while making the body's end the end of the world — so nothing here can run past its own table.
 */
function readTableBody(source: string, body: Span): { columns: ColumnSpan[]; indexed: NameSpan[] } {
  const reader: Reader = { text: source.slice(0, body.end), at: body.start };
  const columns: ColumnSpan[] = [];
  const indexed: NameSpan[] = [];

  while (reader.at < body.end) {
    skipTrivia(reader);
    if (reader.at >= body.end) break;

    const first = readName(reader);
    if (first === null) {
      reader.at++;
      continue;
    }

    // `note` and `indexes` are keywords only when a `:` or a `{` follows them. A column called
    // `note` is an ordinary column, and documents do have one.
    const probe: Reader = { text: reader.text, at: reader.at };
    skipTrivia(probe);
    const next = probe.text[probe.at];
    const keyword = first.name.toLowerCase();

    if (keyword === "note" && (next === ":" || next === "{")) {
      reader.at = probe.at;
      skipStatement(reader);
      continue;
    }
    if (keyword === "indexes" && next === "{") {
      reader.at = probe.at;
      indexed.push(...readIndexes(reader));
      continue;
    }

    const column = readColumn(reader, first);
    if (column !== null) columns.push(column);
  }

  return { columns, indexed };
}

function readColumn(reader: Reader, name: NameSpan): ColumnSpan | null {
  skipTrivia(reader);
  const typeStart = reader.at;
  skipColumnType(reader);
  if (reader.at === typeStart) return null;

  const typeSpan = { start: typeStart, end: reader.at };
  const settings = readSettings(reader);
  return {
    name: name.name,
    nameSpan: name.span,
    typeSpan,
    settings,
    inlineRefs: inlineRefsOf(reader.text, settings),
  };
}

/** A type runs until the settings that follow it — and `int[]` is a type, not an empty setting. */
function skipColumnType(reader: Reader): void {
  const { text } = reader;

  while (reader.at < text.length) {
    if (text.startsWith("//", reader.at) || text.startsWith("/*", reader.at)) return;

    const char = text[reader.at]!;
    if (char === "(") {
      skipParens(reader);
      continue;
    }
    if (char === "[") {
      if (text.startsWith("[]", reader.at)) {
        reader.at += 2;
        continue;
      }
      return;
    }
    if (char === "}" || char === ",") return;
    if (/\s/.test(char)) {
      // `decimal (10, 2)` is one type with a space in it; anything else ends it.
      const probe: Reader = { text, at: reader.at };
      skipTrivia(probe);
      if (text[probe.at] !== "(") return;
      reader.at = probe.at;
      continue;
    }
    reader.at++;
  }
}

function skipParens(reader: Reader): void {
  const { text } = reader;
  let depth = 0;

  while (reader.at < text.length) {
    const char = text[reader.at]!;
    if (QUOTES.has(char)) {
      skipString(reader);
      continue;
    }
    if (char === "(") depth++;
    else if (char === ")") {
      depth--;
      reader.at++;
      if (depth === 0) return;
      continue;
    }
    reader.at++;
  }
}

function readSettings(reader: Reader): SettingsSpan | null {
  skipTrivia(reader);
  if (reader.text[reader.at] !== "[") return null;

  const start = reader.at;
  reader.at++;
  const items: SettingSpan[] = [];

  while (reader.at < reader.text.length) {
    skipTrivia(reader);
    const char = reader.text[reader.at];
    if (char === undefined) break;
    if (char === "]") {
      reader.at++;
      break;
    }
    if (char === ",") {
      reader.at++;
      continue;
    }

    const before = reader.at;
    const item = readSetting(reader);
    if (item !== null) items.push(item);
    if (reader.at === before) reader.at++;
  }

  return { span: { start, end: reader.at }, items };
}

function readSetting(reader: Reader): SettingSpan | null {
  const key = readName(reader);
  if (key === null) return null;

  let value: Span | null = null;
  if (eat(reader, ":")) {
    skipTrivia(reader);
    const from = reader.at;
    skipSettingValue(reader);
    value = { start: from, end: reader.at };
  }

  return { key: key.name.toLowerCase(), span: { start: key.span.start, end: reader.at }, value };
}

/** Everything up to the comma or `]` that ends this setting, nesting and strings included. */
function skipSettingValue(reader: Reader): void {
  const { text } = reader;
  let depth = 0;

  while (reader.at < text.length) {
    const char = text[reader.at]!;
    if (QUOTES.has(char)) {
      skipString(reader);
      continue;
    }
    if (char === "(" || char === "[") depth++;
    else if (char === ")") depth--;
    else if (char === "]") {
      if (depth === 0) return;
      depth--;
    } else if (char === "," && depth === 0) return;
    reader.at++;
  }
}

function inlineRefsOf(source: string, settings: SettingsSpan | null): InlineRefSpan[] {
  if (settings === null) return [];

  const out: InlineRefSpan[] = [];
  settings.items.forEach((item, index) => {
    if (item.key !== "ref" || item.value === null) return;
    const reader: Reader = { text: source.slice(0, item.value.end), at: item.value.start };
    if (!eatOperator(reader)) return;
    const endpoint = readEndpoint(reader);
    if (endpoint === null) return;
    out.push({ index, endpoint });
  });
  return out;
}

/**
 * What to cut so a settings list stays well formed once some of its entries are gone.
 *
 * Takes every entry at once rather than one at a time, because a separator belongs to *two*
 * entries: cutting `[ref: > a.id, ref: > a.name]` one entry at a time makes both cuts claim the
 * comma between them, and the second one to be applied is dropped for overlapping — leaving the
 * first relationship in place, pointing at a table that is no longer there.
 */
function settingCuts(source: string, settings: SettingsSpan, doomed: ReadonlySet<number>): Span[] {
  if (doomed.size === 0) return [];

  if (doomed.size === settings.items.length) {
    // Nothing is left inside the brackets, so they go too — with the space that held them off the
    // type.
    let start = settings.span.start;
    while (start > 0 && isBlank(source[start - 1])) start--;
    return [{ start, end: settings.span.end }];
  }

  const cuts: Span[] = [];
  for (let index = 0; index < settings.items.length; index++) {
    if (!doomed.has(index)) continue;

    // One cut per run of neighbours, so the separators inside a run are never claimed twice.
    const first = index;
    while (doomed.has(index + 1)) index++;

    const after = settings.items[index + 1];
    // A run followed by a survivor takes the separator after it; a run that ends the list takes the
    // one before it — and there is a survivor before it, or the whole-list branch would have run.
    if (after !== undefined) cuts.push({ start: settings.items[first]!.span.start, end: after.span.start });
    else cuts.push({ start: settings.items[first - 1]!.span.end, end: settings.items[index]!.span.end });
  }
  return cuts;
}

/** With the cursor on the `{` of an `Indexes` block: the column names its entries target. */
function readIndexes(reader: Reader): NameSpan[] {
  const body = readBlock(reader);
  const inner: Reader = { text: reader.text.slice(0, body.end), at: body.start };
  const out: NameSpan[] = [];

  while (inner.at < body.end) {
    skipTrivia(inner);
    if (inner.at >= body.end) break;

    const char = inner.text[inner.at];
    if (char === "(") {
      out.push(...readColumnList(inner));
    } else if (char === "`") {
      // An expression index. Whatever is inside is not an identifier to rewrite.
      skipString(inner);
    } else {
      const name = readName(inner);
      if (name === null) {
        inner.at++;
        continue;
      }
      out.push(name);
    }
    readSettings(inner);
  }

  return out.filter((name) => reader.text[name.span.start] !== "`");
}

/** `[schema.]table.column`, or `[schema.]table.(a, b)`. */
function readEndpoint(reader: Reader): EndpointSpan | null {
  const parts: NameSpan[] = [];
  let columns: NameSpan[] | null = null;

  for (;;) {
    skipTrivia(reader);
    if (reader.text[reader.at] === "(") {
      columns = readColumnList(reader);
      break;
    }
    const part = readName(reader);
    if (part === null) return null;
    parts.push(part);
    if (!eat(reader, ".")) break;
  }

  if (columns === null) {
    // Nothing was parenthesised, so the last name written is the column.
    const last = parts.pop();
    if (last === undefined) return null;
    columns = [last];
  }

  const table = parts.pop();
  if (table === undefined) return null;

  return { schema: parts.pop()?.name ?? null, table: table.name, tableSpan: table.span, columns };
}

function readColumnList(reader: Reader): NameSpan[] {
  const out: NameSpan[] = [];
  reader.at++; // past the `(`

  while (reader.at < reader.text.length) {
    skipTrivia(reader);
    const char = reader.text[reader.at];
    if (char === undefined) break;
    if (char === ")") {
      reader.at++;
      break;
    }
    if (char === ",") {
      reader.at++;
      continue;
    }
    const name = readName(reader);
    if (name === null) {
      reader.at++;
      continue;
    }
    out.push(name);
  }

  return out;
}

function eatOperator(reader: Reader): boolean {
  skipTrivia(reader);
  for (const operator of OPERATORS) {
    if (reader.text.startsWith(operator, reader.at)) {
      reader.at += operator.length;
      return true;
    }
  }
  return false;
}

function readRef(reader: Reader, start: number, out: RefSpan[]): void {
  const afterKeyword = reader.at;

  // `Ref name:`, `Ref:`, `Ref name { … }` or `Ref { … }` — the name is optional in both forms.
  const probe: Reader = { text: reader.text, at: reader.at };
  readName(probe);
  skipTrivia(probe);
  const delimiter = probe.text[probe.at];
  if (delimiter === ":" || delimiter === "{") reader.at = probe.at;
  else {
    reader.at = afterKeyword;
    skipTrivia(reader);
  }

  if (reader.text[reader.at] === ":") {
    reader.at++;
    const endpoints = readRelation(reader);
    if (endpoints === null) return;
    const span = { start, end: reader.at };
    out.push({ statement: span, span, endpoints });
    return;
  }

  if (reader.text[reader.at] !== "{") return;

  const body = readBlock(reader);
  const statement = { start, end: reader.at };
  const inner: Reader = { text: reader.text.slice(0, body.end), at: body.start };

  while (inner.at < body.end) {
    skipTrivia(inner);
    if (inner.at >= body.end) break;
    const from = inner.at;
    const endpoints = readRelation(inner);
    if (endpoints === null) {
      inner.at++;
      continue;
    }
    out.push({ statement, span: { start: from, end: inner.at }, endpoints });
  }
}

/** `endpoint OP endpoint [settings]`. */
function readRelation(reader: Reader): EndpointSpan[] | null {
  const from = readEndpoint(reader);
  if (from === null) return null;
  if (!eatOperator(reader)) return null;
  const to = readEndpoint(reader);
  if (to === null) return null;
  readSettings(reader);
  return [from, to];
}

function readGroup(reader: Reader, out: GroupEntrySpan[]): void {
  readName(reader); // the group's own name, when it has one
  readSettings(reader);
  skipTrivia(reader);
  if (reader.text[reader.at] !== "{") return;

  const body = readBlock(reader);
  const inner: Reader = { text: reader.text.slice(0, body.end), at: body.start };

  while (inner.at < body.end) {
    skipTrivia(inner);
    if (inner.at >= body.end) break;

    const first = readName(inner);
    if (first === null) {
      inner.at++;
      continue;
    }

    // A group can carry its own note, which names no table.
    const probe: Reader = { text: inner.text, at: inner.at };
    skipTrivia(probe);
    if (first.name.toLowerCase() === "note" && (probe.text[probe.at] === ":" || probe.text[probe.at] === "{")) {
      inner.at = probe.at;
      skipStatement(inner);
      continue;
    }

    let schema: string | null = null;
    let name = first;
    if (eat(inner, ".")) {
      const qualified = readName(inner);
      if (qualified === null) continue;
      schema = first.name;
      name = qualified;
    }

    out.push({
      key: tableKey(schema, name.name),
      span: { start: first.span.start, end: name.span.end },
      nameSpan: name.span,
    });
  }
}
