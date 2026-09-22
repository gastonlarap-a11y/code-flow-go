/**
 * The catalogue of figures, as data (DIAG-006).
 *
 * **Every id is a Mermaid shape name.** `rect`, `diam`, `cyl` — not `rectangle`, `decision`,
 * `database`. That is not a naming preference: the document written to disk is Mermaid, so an id
 * that needed translating would be a table to keep in step, and the first entry somebody forgot
 * would be a shape that saved as something else. The one id that is not a shape name is
 * `subgraph`, because in Mermaid a container is not a shape at all.
 *
 * The catalogue is **Mermaid's published set**, less two families left out on purpose:
 *
 * - the comment shapes (`brace`, `brace-r`, `braces`), which are a remark drawn beside the diagram
 *   rather than a step in it — `text` covers annotating a canvas;
 * - `odd`, which exists for the classic `>text]` syntax and has no meaning of its own.
 *
 * What Mermaid still cannot say, it cannot say: BPMN's exclusive, parallel and inclusive gateways
 * are one `diam` — though `fork` now covers the parallel split — an ellipse is a `stadium`, and a
 * UML use case is a `stadium` inside a `subgraph`.
 *
 * A stencil holds everything about a kind of shape that is not its outline: how big it starts,
 * where its text goes, whether it holds other shapes, what colour it defaults to. The outline
 * itself is `shapes.ts`.
 */
import type { FillToken } from "./model";
import type { TranslationKey } from "../i18n/translations";

/**
 * The families the palette is divided into.
 *
 * Named for what a figure *is used for*, not for what it looks like, because that is how somebody
 * looks for one. With fifty figures the grouping is what makes the palette readable — three groups
 * would put thirty unlabelled glyphs in one grid.
 */
export type StencilGroup =
  | "process"
  | "control"
  | "terminal"
  | "data"
  | "document"
  | "system"
  | "note"
  | "container";

export const STENCIL_GROUPS: readonly StencilGroup[] = [
  "process",
  "control",
  "terminal",
  "data",
  "document",
  "system",
  "note",
  "container",
];

/**
 * Where a shape's text is written.
 *
 * Part of the stencil rather than of the renderer because the canvas and the exporter both need the
 * answer and would otherwise each have their own opinion — which is exactly the bug where the
 * exported picture puts an actor's name across its chest.
 */
export type TextPlacement = "center" | "below" | "band-top";

/** How tall the title band of a container is. */
export const BAND = 28;

export interface Stencil {
  /** The Mermaid shape name, which is also this figure's id. `subgraph` is the one exception. */
  id: string;
  group: StencilGroup;
  labelKey: TranslationKey;
  width: number;
  height: number;
  text: TextPlacement;
  defaultFill: FillToken;
  /** Holds other shapes: drawn behind them, and emitted as a `subgraph`. */
  container?: true;
  /** Keeps its proportions when resized — a circle that is not round is not an event. */
  fixedRatio?: true;
}

/**
 * Every figure the editor offers, and every shape name it will ever write.
 *
 * `as const satisfies` for the same reason `lib/modules.ts` uses it: `StencilId` is derived from
 * this array, so a shape that is drawn but not listed — or listed but not drawn — stops compiling
 * rather than appearing as an empty box at run time.
 *
 * Order within a family is the order the palette shows, so the ones reached for most go first.
 */
export const STENCILS = [
  // ---- steps ----------------------------------------------------------------------------------
  { id: "rect", group: "process", labelKey: "diagram.stencil.rect", width: 160, height: 72, text: "center", defaultFill: "neutral" },
  { id: "rounded", group: "process", labelKey: "diagram.stencil.rounded", width: 160, height: 72, text: "center", defaultFill: "accent" },
  { id: "fr-rect", group: "process", labelKey: "diagram.stencil.frRect", width: 170, height: 76, text: "center", defaultFill: "neutral" },
  { id: "div-rect", group: "process", labelKey: "diagram.stencil.divRect", width: 170, height: 76, text: "center", defaultFill: "neutral" },
  { id: "lin-rect", group: "process", labelKey: "diagram.stencil.linRect", width: 160, height: 72, text: "center", defaultFill: "neutral" },
  { id: "st-rect", group: "process", labelKey: "diagram.stencil.stRect", width: 168, height: 80, text: "center", defaultFill: "neutral" },
  { id: "notch-rect", group: "process", labelKey: "diagram.stencil.notchRect", width: 160, height: 72, text: "center", defaultFill: "neutral" },
  { id: "trap-t", group: "process", labelKey: "diagram.stencil.trapT", width: 175, height: 72, text: "center", defaultFill: "neutral" },
  { id: "trap-b", group: "process", labelKey: "diagram.stencil.trapB", width: 175, height: 72, text: "center", defaultFill: "neutral" },
  { id: "sl-rect", group: "process", labelKey: "diagram.stencil.slRect", width: 160, height: 76, text: "center", defaultFill: "neutral" },
  { id: "win-pane", group: "process", labelKey: "diagram.stencil.winPane", width: 160, height: 88, text: "center", defaultFill: "neutral" },
  { id: "tag-rect", group: "process", labelKey: "diagram.stencil.tagRect", width: 160, height: 78, text: "center", defaultFill: "neutral" },
  { id: "curv-trap", group: "process", labelKey: "diagram.stencil.curvTrap", width: 170, height: 80, text: "center", defaultFill: "neutral" },

  // ---- where a flow branches, waits or rejoins -------------------------------------------------
  { id: "diam", group: "control", labelKey: "diagram.stencil.diam", width: 150, height: 110, text: "center", defaultFill: "warning" },
  { id: "hex", group: "control", labelKey: "diagram.stencil.hex", width: 160, height: 80, text: "center", defaultFill: "neutral" },
  // The bar BPMN draws a parallel gateway as. The catalogue lost that gateway to `diam`; this is
  // the part of it Mermaid can say.
  { id: "fork", group: "control", labelKey: "diagram.stencil.fork", width: 170, height: 18, text: "below", defaultFill: "neutral" },
  { id: "f-circ", group: "control", labelKey: "diagram.stencil.fCirc", width: 34, height: 34, text: "below", defaultFill: "neutral", fixedRatio: true },
  { id: "cross-circ", group: "control", labelKey: "diagram.stencil.crossCirc", width: 48, height: 48, text: "below", defaultFill: "none", fixedRatio: true },
  { id: "notch-pent", group: "control", labelKey: "diagram.stencil.notchPent", width: 160, height: 82, text: "center", defaultFill: "neutral" },
  { id: "delay", group: "control", labelKey: "diagram.stencil.delay", width: 160, height: 72, text: "center", defaultFill: "neutral" },
  { id: "hourglass", group: "control", labelKey: "diagram.stencil.hourglass", width: 56, height: 56, text: "below", defaultFill: "none", fixedRatio: true },
  { id: "bolt", group: "control", labelKey: "diagram.stencil.bolt", width: 52, height: 64, text: "below", defaultFill: "warning" },

  // ---- what a flow starts and stops at ---------------------------------------------------------
  { id: "stadium", group: "terminal", labelKey: "diagram.stencil.stadium", width: 150, height: 60, text: "center", defaultFill: "accent" },
  { id: "circle", group: "terminal", labelKey: "diagram.stencil.circle", width: 48, height: 48, text: "below", defaultFill: "success", fixedRatio: true },
  { id: "sm-circ", group: "terminal", labelKey: "diagram.stencil.smCirc", width: 32, height: 32, text: "below", defaultFill: "neutral", fixedRatio: true },
  { id: "dbl-circ", group: "terminal", labelKey: "diagram.stencil.dblCirc", width: 48, height: 48, text: "below", defaultFill: "none", fixedRatio: true },
  { id: "fr-circ", group: "terminal", labelKey: "diagram.stencil.frCirc", width: 48, height: 48, text: "below", defaultFill: "danger", fixedRatio: true },

  // ---- what a flow reads and writes ------------------------------------------------------------
  { id: "cyl", group: "data", labelKey: "diagram.stencil.cyl", width: 130, height: 110, text: "center", defaultFill: "neutral" },
  { id: "h-cyl", group: "data", labelKey: "diagram.stencil.hCyl", width: 170, height: 90, text: "center", defaultFill: "neutral" },
  { id: "lin-cyl", group: "data", labelKey: "diagram.stencil.linCyl", width: 130, height: 110, text: "center", defaultFill: "neutral" },
  { id: "datastore", group: "data", labelKey: "diagram.stencil.datastore", width: 175, height: 66, text: "center", defaultFill: "neutral" },
  { id: "bow-rect", group: "data", labelKey: "diagram.stencil.bowRect", width: 165, height: 80, text: "center", defaultFill: "neutral" },
  { id: "lean-r", group: "data", labelKey: "diagram.stencil.leanR", width: 170, height: 72, text: "center", defaultFill: "neutral" },
  { id: "lean-l", group: "data", labelKey: "diagram.stencil.leanL", width: 170, height: 72, text: "center", defaultFill: "neutral" },

  // ---- paper ------------------------------------------------------------------------------------
  { id: "doc", group: "document", labelKey: "diagram.stencil.doc", width: 150, height: 90, text: "center", defaultFill: "neutral" },
  { id: "docs", group: "document", labelKey: "diagram.stencil.docs", width: 158, height: 98, text: "center", defaultFill: "neutral" },
  { id: "lin-doc", group: "document", labelKey: "diagram.stencil.linDoc", width: 150, height: 90, text: "center", defaultFill: "neutral" },
  { id: "tag-doc", group: "document", labelKey: "diagram.stencil.tagDoc", width: 150, height: 94, text: "center", defaultFill: "neutral" },
  { id: "flag", group: "document", labelKey: "diagram.stencil.flag", width: 150, height: 92, text: "center", defaultFill: "neutral" },
  { id: "tri", group: "document", labelKey: "diagram.stencil.tri", width: 96, height: 84, text: "below", defaultFill: "neutral" },
  { id: "flip-tri", group: "document", labelKey: "diagram.stencil.flipTri", width: 96, height: 84, text: "below", defaultFill: "neutral" },

  // ---- the things a process runs on --------------------------------------------------------------
  { id: "cloud", group: "system", labelKey: "diagram.stencil.cloud", width: 175, height: 104, text: "center", defaultFill: "neutral" },
  { id: "browser", group: "system", labelKey: "diagram.stencil.browser", width: 175, height: 115, text: "center", defaultFill: "neutral" },
  { id: "console", group: "system", labelKey: "diagram.stencil.console", width: 175, height: 115, text: "center", defaultFill: "neutral" },
  { id: "folder", group: "system", labelKey: "diagram.stencil.folder", width: 150, height: 105, text: "center", defaultFill: "neutral" },
  { id: "bucket", group: "system", labelKey: "diagram.stencil.bucket", width: 125, height: 110, text: "center", defaultFill: "neutral" },
  { id: "bang", group: "system", labelKey: "diagram.stencil.bang", width: 160, height: 110, text: "center", defaultFill: "warning" },

  // ---- what is written beside the diagram rather than in it --------------------------------------
  // Mermaid's text block: a label with no outline.
  { id: "text", group: "note", labelKey: "diagram.stencil.text", width: 160, height: 36, text: "center", defaultFill: "none" },
  { id: "person", group: "note", labelKey: "diagram.stencil.person", width: 60, height: 96, text: "below", defaultFill: "none", fixedRatio: true },

  // ---- the one thing that holds other things ---------------------------------------------------
  // A pool, a lane and a UML system boundary are all this: Mermaid has one container and it is
  // `subgraph`.
  { id: "subgraph", group: "container", labelKey: "diagram.stencil.subgraph", width: 620, height: 300, text: "band-top", defaultFill: "none", container: true },
] as const satisfies readonly Stencil[];

export type RegisteredStencil = (typeof STENCILS)[number];

/** The id of any figure the editor can draw, which is also its Mermaid shape name. */
export type StencilId = RegisteredStencil["id"];

const BY_ID = new Map<string, RegisteredStencil>(STENCILS.map((stencil) => [stencil.id, stencil]));

export function stencilById(id: StencilId): RegisteredStencil {
  // Every `StencilId` comes from `STENCILS`, so the lookup cannot miss.
  return BY_ID.get(id)!;
}

/**
 * Whether a string names a figure this version can draw.
 *
 * The document-reading path needs this: a `.mmd` written by hand, by another tool or by a model can
 * name any Mermaid shape, including the comment family this catalogue leaves out.
 * `mermaid/parse.ts` decides what to do about it; this only answers the question.
 */
export function isStencilId(id: string): id is StencilId {
  return BY_ID.has(id);
}

export function stencilsInGroup(group: StencilGroup): readonly RegisteredStencil[] {
  return STENCILS.filter((stencil) => stencil.group === group);
}

/** Whether this kind of node holds others. */
export function isContainer(id: StencilId): boolean {
  const stencil = stencilById(id);
  return "container" in stencil && stencil.container === true;
}

/** Whether resizing this kind of node must keep its proportions. */
export function isFixedRatio(id: StencilId): boolean {
  const stencil = stencilById(id);
  return "fixedRatio" in stencil && stencil.fixedRatio === true;
}
