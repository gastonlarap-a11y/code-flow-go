/** What `normalizeDocumentPath` answers: either the path to create, or why it cannot be one. */
export type DocumentPathResult =
  | { ok: true; relPath: string }
  | { ok: false; reason: DocumentPathError };

/**
 * Why a typed name is not a usable document path.
 *
 * A discriminated reason rather than a message, so the caller renders it through `translations.ts`
 * in the user's own language instead of the store inventing English prose.
 */
export type DocumentPathError = "empty" | "escapes" | "absolute" | "invalidChar";

/** Rejected outright: the printable characters a Windows file name cannot hold. */
const INVALID_CHARACTERS = /[<>:"|?*]/;

/**
 * Control characters, checked by code point rather than folded into the pattern above.
 *
 * A control-character range inside a character class is exactly what ESLint's `no-control-regex`
 * flags, and that rule is right often enough to be worth not silencing — one in a pattern is
 * usually a mistake. Here it is deliberate, since no file name holds one, so it says so in code.
 */
function hasControlCharacter(value: string): boolean {
  return [...value].some((character) => {
    const code = character.codePointAt(0)!;
    return code < 0x20 || code === 0x7f;
  });
}

/**
 * Turns what the user typed into the project-relative path of a new document.
 *
 * Three things happen here, and all three are the reason this is not inline in the modal:
 * - `extension` is appended when it is missing, so "orders" and "orders.dbml" name one file. The
 *   extension is what the backend's walk matches on, so a document saved without it would be
 *   invisible the moment the picker reloaded.
 * - Separators are normalised to `/`, because this path is half of a layout key and the same
 *   document typed as `db\orders` and `db/orders` has to be one row.
 * - Anything that could leave the project folder is refused *here*, not only at the boundary.
 *   `create_file` guards its own path, but a refusal arriving from the backend reaches the user as
 *   a raw error string; this one reaches them as a labelled field.
 *
 * The extension is a parameter because two tools now create documents this way — the schema
 * designer's `.dbml` and the diagram editor's `.mmd`. It is compared in lower case, so
 * "Orders.DBML" is not given a second extension.
 */
export function normalizeDocumentPath(input: string, extension: string): DocumentPathResult {
  const trimmed = input.trim();
  if (trimmed.length === 0) return { ok: false, reason: "empty" };
  if (INVALID_CHARACTERS.test(trimmed) || hasControlCharacter(trimmed)) {
    return { ok: false, reason: "invalidChar" };
  }

  const slashed = trimmed.replace(/\\/g, "/");

  // A drive letter (`C:/…`) is caught by the invalid-character check above, which rejects `:`.
  if (slashed.startsWith("/")) return { ok: false, reason: "absolute" };

  const segments = slashed.split("/").filter((segment) => segment.length > 0);
  if (segments.length === 0) return { ok: false, reason: "empty" };
  if (segments.some((segment) => segment === "." || segment === "..")) {
    return { ok: false, reason: "escapes" };
  }

  const last = segments[segments.length - 1]!;
  const named = last.toLowerCase().endsWith(extension) ? last : `${last}${extension}`;
  // A name that is nothing but the extension (".dbml") names no document.
  if (named.toLowerCase() === extension) return { ok: false, reason: "empty" };

  return { ok: true, relPath: [...segments.slice(0, -1), named].join("/") };
}

/** The file name a document path ends in, for titles and tabs. */
export function documentName(relPath: string): string {
  return relPath.split("/").pop() ?? relPath;
}

/** The name without its extension, for a heading that should not shout ".mmd". */
export function documentTitle(relPath: string, extension: string): string {
  const name = documentName(relPath);
  return name.toLowerCase().endsWith(extension) ? name.slice(0, -extension.length) : name;
}

/** The folder a document sits in, or `null` when it sits in the project root. */
export function documentFolder(relPath: string): string | null {
  const cut = relPath.lastIndexOf("/");
  return cut === -1 ? null : relPath.slice(0, cut);
}

/** A folder and the documents in it, as the picker shows them. */
export interface DocumentFolder {
  /** `null` for the project root, which is shown without a heading. */
  folder: string | null;
  /** Full project-relative paths — the value the picker hands back when one is chosen. */
  paths: string[];
}

/**
 * Groups a flat list of document paths by the folder they are in.
 *
 * Folders are how these documents are organised: creating `procesos/alta-cliente` already makes the
 * folder and puts the file in it, and the walk that finds them already recurses. What was missing
 * was showing it — a picker listing `procesos/alta-cliente.mmd` beside `checkout.mmd` as two equal
 * strings is a folder nobody can see they have.
 *
 * The root goes first and unheaded, because a project with no folders must look exactly as it did.
 * Everything else keeps the order it arrived in, which is the backend's sort, so the list does not
 * reshuffle between loads. The whole folder path is the heading (`a/b`, not `b`): a nested folder
 * indented under its parent is a tree, and a tree is what the Editor module is for.
 */
export function groupByFolder(paths: readonly string[]): DocumentFolder[] {
  const root: string[] = [];
  const folders = new Map<string, string[]>();

  for (const path of paths) {
    const folder = documentFolder(path);
    if (folder === null) {
      root.push(path);
      continue;
    }
    const existing = folders.get(folder);
    if (existing === undefined) folders.set(folder, [path]);
    else existing.push(path);
  }

  const grouped: DocumentFolder[] = root.length > 0 ? [{ folder: null, paths: root }] : [];
  for (const [folder, inIt] of folders) grouped.push({ folder, paths: inIt });
  return grouped;
}
