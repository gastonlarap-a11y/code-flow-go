/**
 * The catalogue of figures, as data (DIAG-006).
 *
 * **Every id is a Mermaid shape name.** `rect`, `diam`, `cyl` — not `rectangle`, `decision`,
 * `database`. That is not a naming preference: the document written to disk is Mermaid, so an id
 * that needed translating would be a table to keep in step, and the first entry somebody forgot
 * would be a shape that saved as something else. The one id that is not a shape name is
 * `subgraph`, because in Mermaid a container is not a shape at all.
 *
 * The catalogue is therefore **what Mermaid can say**, which is smaller than what a drawing tool
 * could draw. Three specific losses, all deliberate:
 *
 * - BPMN's exclusive, parallel and inclusive gateways are one `diam`. Mermaid has no X, + or O
 *   inside a diamond, and inventing one would mean a file only this app could read.
 * - An ellipse is a `stadium`. Mermaid has no ellipse.
 * - A UML use case is also a `stadium`, and a system boundary is a `subgraph`.
 *
 * A stencil holds everything about a kind of shape that is not its outline: how big it starts,
 * where its text goes, whether it holds other shapes, what colour it defaults to. The outline
 * itself is `shapes.ts`.
 */
import type { FillToken } from "./model";
import type { TranslationKey } from "../i18n/translations";

export type StencilGroup = "basic" | "flow" | "container";

export const STENCIL_GROUPS: readonly StencilGroup[] = ["basic", "flow", "container"];

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
 */
export const STENCILS = [
  // ---- the shapes a sketch is made of ----------------------------------------------------------
  { id: "rect", group: "basic", labelKey: "diagram.stencil.rect", width: 160, height: 72, text: "center", defaultFill: "neutral" },
  { id: "rounded", group: "basic", labelKey: "diagram.stencil.rounded", width: 160, height: 72, text: "center", defaultFill: "accent" },
  { id: "stadium", group: "basic", labelKey: "diagram.stencil.stadium", width: 150, height: 60, text: "center", defaultFill: "accent" },
  { id: "diam", group: "basic", labelKey: "diagram.stencil.diam", width: 150, height: 110, text: "center", defaultFill: "warning" },
  { id: "lean-r", group: "basic", labelKey: "diagram.stencil.leanR", width: 170, height: 72, text: "center", defaultFill: "neutral" },
  { id: "div-rect", group: "basic", labelKey: "diagram.stencil.divRect", width: 170, height: 76, text: "center", defaultFill: "neutral" },
  { id: "cyl", group: "basic", labelKey: "diagram.stencil.cyl", width: 130, height: 110, text: "center", defaultFill: "neutral" },
  { id: "doc", group: "basic", labelKey: "diagram.stencil.doc", width: 150, height: 90, text: "center", defaultFill: "neutral" },
  { id: "brace", group: "basic", labelKey: "diagram.stencil.brace", width: 180, height: 70, text: "center", defaultFill: "none" },
  // Mermaid's text block: a label with no outline, for annotating a canvas.
  { id: "text", group: "basic", labelKey: "diagram.stencil.text", width: 160, height: 36, text: "center", defaultFill: "none" },

  // ---- what a flow is punctuated with ----------------------------------------------------------
  { id: "circle", group: "flow", labelKey: "diagram.stencil.circle", width: 48, height: 48, text: "below", defaultFill: "success", fixedRatio: true },
  { id: "dbl-circ", group: "flow", labelKey: "diagram.stencil.dblCirc", width: 48, height: 48, text: "below", defaultFill: "none", fixedRatio: true },
  { id: "fr-circ", group: "flow", labelKey: "diagram.stencil.frCirc", width: 48, height: 48, text: "below", defaultFill: "danger", fixedRatio: true },
  { id: "sm-circ", group: "flow", labelKey: "diagram.stencil.smCirc", width: 32, height: 32, text: "below", defaultFill: "neutral", fixedRatio: true },
  { id: "hex", group: "flow", labelKey: "diagram.stencil.hex", width: 160, height: 80, text: "center", defaultFill: "neutral" },
  { id: "person", group: "flow", labelKey: "diagram.stencil.person", width: 60, height: 96, text: "below", defaultFill: "none", fixedRatio: true },

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
 * name any of Mermaid's ~30 shapes, and most of them are not in this catalogue. `mermaid/parse.ts`
 * decides what to do about it; this only answers the question.
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
