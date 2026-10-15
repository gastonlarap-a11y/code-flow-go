import {
  Blocks,
  Box,
  Braces,
  Combine,
  Hash,
  Layers,
  ListOrdered,
  Shapes,
  SquareFunction,
  Type,
  Variable,
  type LucideIcon,
} from "lucide-react";
import type { TranslationKey } from "../i18n/translations";
import type { RepoMapKind, RepoMapNode } from "../../types/domain";

/**
 * What the drawn map says about each kind of declaration, and how it colours and trims what it draws
 * (19-repo-map.md).
 *
 * Each table is a `Record` over the union, so a kind the backend adds without a label here stops
 * compiling.
 */

const KIND_LABELS: Record<RepoMapKind, TranslationKey> = {
  class: "codemap.map.kind.class",
  interface: "codemap.map.kind.interface",
  enum: "codemap.map.kind.enum",
  struct: "codemap.map.kind.struct",
  record: "codemap.map.kind.record",
  mixin: "codemap.map.kind.mixin",
  type: "codemap.map.kind.type",
  function: "codemap.map.kind.function",
  method: "codemap.map.kind.method",
  const: "codemap.map.kind.const",
  var: "codemap.map.kind.var",
};

/** Read by member access (`DECL_KIND_ICONS[kind]`), so a component stays a static reference. */
export const DECL_KIND_ICONS: Record<RepoMapKind, LucideIcon> = {
  class: Box,
  interface: Shapes,
  enum: ListOrdered,
  struct: Blocks,
  record: Layers,
  mixin: Combine,
  type: Type,
  function: SquareFunction,
  method: Braces,
  const: Hash,
  var: Variable,
};

export const declKindLabel = (kind: RepoMapKind): TranslationKey => KIND_LABELS[kind];

/**
 * The colours the project view gives its top-level folders: the theme's own accents first, then
 * three more that read on both themes. A ninth folder starts the list again.
 */
export const GROUP_PALETTE = [
  "var(--cf-accent)",
  "var(--cf-success)",
  "var(--cf-warning)",
  "var(--cf-info)",
  "var(--cf-danger)",
  "light-dark(#7c3aed, #a78bfa)",
  "light-dark(#0f766e, #2dd4bf)",
  "light-dark(#be185d, #f472b6)",
] as const;

/**
 * Which colour each top-level folder gets: the biggest folder the first colour, so the dominant part
 * of a repository always looks the same. Files at the root (group "") get none.
 */
export function groupColors(nodes: readonly RepoMapNode[]): Map<string, string> {
  const files = new Map<string, number>();
  for (const n of nodes) {
    if (n.group !== "") files.set(n.group, (files.get(n.group) ?? 0) + n.files);
  }
  const ordered = [...files.entries()].sort((a, b) => b[1] - a[1] || a[0].localeCompare(b[0]));
  return new Map(ordered.map(([group], i) => [group, GROUP_PALETTE[i % GROUP_PALETTE.length]!]));
}

/** How many cards a folder view draws; the rest wait in the panel, one click away. */
export const FOLDER_CARDS = 30;

/**
 * The cards worth drawing first — folders, then the most imported, then by path — and the ones left
 * for the panel.
 */
export function drawnFirst(nodes: readonly RepoMapNode[], limit: number): { drawn: RepoMapNode[]; hidden: RepoMapNode[] } {
  const ranked = [...nodes].sort(
    (a, b) => Number(b.folder) - Number(a.folder) || b.imported_by - a.imported_by || a.id.localeCompare(b.id),
  );
  return { drawn: ranked.slice(0, limit), hidden: ranked.slice(limit) };
}

/** The folder a node sits in, written for the line above its name in the project view. */
export function parentOf(id: string): string {
  const slash = id.lastIndexOf("/");
  return slash < 0 ? "" : `${id.slice(0, slash)}/`;
}
