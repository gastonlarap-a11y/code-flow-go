import {
  Anchor,
  Braces,
  Component,
  Database,
  FileCode2,
  FlaskConical,
  Layers,
  Plug,
  Shapes,
  SquareFunction,
  type LucideIcon,
} from "lucide-react";
import type { TranslationKey } from "../i18n/translations";
import type { CodemapEdgeKind, CodemapKind, CodemapLanguageStatus, CodemapStage } from "../../types/domain";

/**
 * What the code map's view says about each kind of node and edge (18-code-map.md).
 *
 * Kept out of the components so the whole vocabulary is one table a test can walk: a kind the
 * backend adds without a label here stops compiling, because each map is a `Record` over the union.
 */

const KIND_LABELS: Record<CodemapKind, TranslationKey> = {
  func: "codemap.kind.func",
  method: "codemap.kind.method",
  type: "codemap.kind.type",
  interface: "codemap.kind.interface",
  interface_method: "codemap.kind.interfaceMethod",
  component: "codemap.kind.component",
  hook: "codemap.kind.hook",
  test: "codemap.kind.test",
  test_file: "codemap.kind.testFile",
  module: "codemap.kind.module",
  table: "codemap.kind.table",
  endpoint: "codemap.kind.endpoint",
};

/**
 * A lookup table rather than a function on purpose: a component read off a module-level map is a
 * static component to the React compiler, while one returned by a call is "created during render".
 */
export const KIND_ICONS: Record<CodemapKind, LucideIcon> = {
  func: SquareFunction,
  method: Braces,
  type: Shapes,
  interface: Layers,
  interface_method: Layers,
  component: Component,
  hook: Anchor,
  test: FlaskConical,
  test_file: FlaskConical,
  module: FileCode2,
  table: Database,
  endpoint: Plug,
};

const EDGE_LABELS: Record<CodemapEdgeKind, TranslationKey> = {
  call: "codemap.edge.call",
  ref: "codemap.edge.ref",
  render: "codemap.edge.render",
  dispatch: "codemap.edge.dispatch",
  cross: "codemap.edge.cross",
  read: "codemap.edge.read",
  write: "codemap.edge.write",
  mention: "codemap.edge.mention",
};

const STAGE_LABELS: Record<CodemapStage, TranslationKey> = {
  scanning: "codemap.stage.scanning",
  go: "codemap.stage.go",
  typescript: "codemap.stage.typescript",
  linking: "codemap.stage.linking",
  ready: "codemap.stage.ready",
};

const STATE_LABELS: Record<CodemapLanguageStatus["state"], TranslationKey> = {
  mapped: "codemap.state.mapped",
  partial: "codemap.state.partial",
  missing_tool: "codemap.state.missingTool",
  absent: "codemap.state.absent",
  failed: "codemap.state.failed",
};

export const kindLabel = (kind: CodemapKind): TranslationKey => KIND_LABELS[kind];
export const edgeLabel = (kind: CodemapEdgeKind): TranslationKey => EDGE_LABELS[kind];
export const stageLabel = (stage: CodemapStage): TranslationKey => STAGE_LABELS[stage];
export const stateLabel = (state: CodemapLanguageStatus["state"]): TranslationKey => STATE_LABELS[state];

/** How healthy a language's share of the map is, as a chip tone. */
export function stateTone(state: CodemapLanguageStatus["state"]): "success" | "warning" | "danger" | "neutral" {
  switch (state) {
    case "mapped":
      return "success";
    case "partial":
    case "missing_tool":
      return "warning";
    case "failed":
      return "danger";
    case "absent":
      return "neutral";
  }
}

/** `file:line`, the form every location in the view is shown and searched in. */
export function location(file: string, line: number): string {
  return line > 0 ? `${file}:${line}` : file;
}

/**
 * The short name a row shows: the last segment of a qualified name, so a column of
 * `finance.FinanceService.CreateExpense` rows does not spend its width on the same prefix.
 * Handlers keep their key — `diagram.Register("diagram_list_documents")` would otherwise read
 * `Register("diagram_list_documents")`, which is fine, but the parenthesised key is the point.
 */
export function shortName(name: string): string {
  const { head, tail, cut } = split(name);
  return (cut >= 0 ? head.slice(cut + 1) : head) + tail;
}

/** The qualifier a short name dropped, for the second line of a row: "finance.FinanceService". */
export function qualifier(name: string): string {
  const { head, cut } = split(name);
  return cut >= 0 ? head.slice(0, cut) : "";
}

/**
 * Where a name divides into its qualifier and its own part. A file is named by its path, and its
 * own part is after the last slash — the last dot would leave `tsx` of `App.tsx`. Anything else is
 * qualified with dots. A handler's parenthesised key is never split.
 */
function split(name: string): { head: string; tail: string; cut: number } {
  const paren = name.indexOf("(");
  const head = paren >= 0 ? name.slice(0, paren) : name;
  const tail = paren >= 0 ? name.slice(paren) : "";
  const cut = head.includes("/") ? head.lastIndexOf("/") : head.lastIndexOf(".");
  return { head, tail, cut };
}
