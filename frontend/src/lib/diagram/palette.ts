/**
 * The colours a diagram is drawn in, resolved per theme (DIAG-006, DIAG-013).
 *
 * Literal values rather than CSS variables, and that is the whole point: the canvas and the SVG
 * exporter have to agree, and an exported file has no app to read variables from. Resolving a token
 * here means the picture on screen and the picture in the file come out of the same table.
 *
 * A document stores the token (`accent`), never the colour, so the same diagram reads correctly in
 * both themes and stays correct when these values change.
 *
 * Every pair is measured, not eyeballed: `palette.test.ts` asserts each fill against the text
 * written on it at WCAG AA, in both themes, using the same `contrastRatio` the accent options are
 * checked with.
 */
import type { FillToken } from "./model";

export type ThemeMode = "light" | "dark";

export interface FillColours {
  /** `"none"` for the shapes that are outlines only, which SVG understands as written. */
  fill: string;
  stroke: string;
  /** The colour of the text written inside this fill. */
  text: string;
}

export interface DiagramPalette {
  fills: Record<FillToken, FillColours>;
  /** Connectors, and the text on them. */
  line: string;
  lineText: string;
  /** What an unfilled shape's text is written on, so `none` can be checked against something. */
  background: string;
  /** The dotted grid behind the canvas. Not exported — paper has no grid. */
  grid: string;
  /** The outline of a selected node, and of the marquee. */
  selection: string;
}

const LIGHT: DiagramPalette = {
  fills: {
    none: { fill: "none", stroke: "#6b6b7d", text: "#1a1a24" },
    neutral: { fill: "#eceef3", stroke: "#8b8ba3", text: "#1a1a24" },
    accent: { fill: "#dbe7fb", stroke: "#4a7fd4", text: "#15305c" },
    success: { fill: "#d7f0df", stroke: "#3c9560", text: "#10401d" },
    warning: { fill: "#fbecc8", stroke: "#b8861d", text: "#453103" },
    danger: { fill: "#fbdede", stroke: "#c95353", text: "#551414" },
  },
  line: "#4a4a5c",
  lineText: "#1a1a24",
  background: "#ffffff",
  grid: "#d7d7e0",
  selection: "#3b82f6",
};

const DARK: DiagramPalette = {
  fills: {
    none: { fill: "none", stroke: "#9797ab", text: "#e9e9f2" },
    neutral: { fill: "#2b2b38", stroke: "#71718a", text: "#e9e9f2" },
    accent: { fill: "#1d3253", stroke: "#5f93df", text: "#d9e7ff" },
    success: { fill: "#163521", stroke: "#4fa872", text: "#d3f1dd" },
    warning: { fill: "#3a2e0f", stroke: "#cfa03c", text: "#f7e9c4" },
    danger: { fill: "#3c1a1a", stroke: "#dc6c6c", text: "#fbdada" },
  },
  line: "#b4b4c6",
  lineText: "#e9e9f2",
  background: "#17171f",
  grid: "#2e2e3c",
  selection: "#60a5fa",
};

export function diagramPalette(theme: ThemeMode): DiagramPalette {
  return theme === "dark" ? DARK : LIGHT;
}

/**
 * The palette an exported file is drawn in.
 *
 * Light, always, and not a preference: an exported diagram is pasted into a ticket, a document or a
 * chat, and every one of those is a white page. A dark export would arrive as a black rectangle in
 * the middle of it.
 */
export const exportPalette: DiagramPalette = LIGHT;
