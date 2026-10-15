/**
 * Archify documents as the diagram editor lists them (20-archify-diagrams.md).
 *
 * One suffix, `.archify.json`, so that no other JSON in a repository — a workflow export, a state
 * machine, a Postman collection — is ever taken for one.
 */
export const ARCHIFY_SUFFIX = ".archify.json";

/** The folder the code map writes its documents into, beside the flows it draws. */
export const ARCHIFY_FOLDER = "codemap";

export function isArchifyDocument(path: string): boolean {
  return path.toLowerCase().endsWith(ARCHIFY_SUFFIX);
}

/** The name a document is saved as for a slug and a diagram type: `codemap/checkout-sequence.archify.json`. */
export function archifyDocumentPath(slug: string, diagramType: string, attempt = 1): string {
  const suffix = attempt > 1 ? `-${attempt}` : "";
  return `${ARCHIFY_FOLDER}/${slug}-${diagramType}${suffix}${ARCHIFY_SUFFIX}`;
}

/** A file name a person would choose, from whatever a title says. */
export function archifySlug(title: string): string {
  const slug = title
    .normalize("NFKD")
    .replace(/[̀-ͯ]/g, "")
    .toLowerCase()
    .replace(/[^a-z0-9]+/g, "-")
    .replace(/^-+|-+$/g, "")
    .slice(0, 60)
    .replace(/-+$/, "");
  return slug === "" ? "diagram" : slug;
}

/** The base name an export of a document is offered under. */
export function archifyExportName(path: string): string {
  const name = path.slice(path.lastIndexOf("/") + 1);
  return name.slice(0, name.length - ARCHIFY_SUFFIX.length);
}
