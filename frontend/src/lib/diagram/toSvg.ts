/**
 * A diagram as a standalone SVG file (DIAG-010).
 *
 * Pure, DOM-free and built from the document rather than from the canvas — no cloning of nodes out
 * of the page, no `html-to-image`, no hidden render. The reason is not purity: it is that the
 * canvas and this file draw from the same `shapes.ts`, `routing.ts`, `markers.ts` and `palette.ts`,
 * so the picture that comes out is the picture that was drawn, and both can be asserted in a node
 * test.
 *
 * Always the light palette. An exported diagram lands in a ticket, a document or a chat, and all
 * three are white pages.
 */
import { BAND, stencilById, type TextPlacement } from "./stencils";
import { MARKER_SHAPES, DASH, edgeCaption, edgeStyle, type MarkerShape } from "./markers";
import { absolutePosition, documentBounds, type DiagramDocument, type DiagramNode } from "./model";
import { exportPalette, type DiagramPalette } from "./palette";
import { chooseSides, midpointOf, polylinePath, routeEdge, type Box } from "./routing";
import { shapeElements, type ShapeElement } from "./shapes";
import { TEXT_INSET, escapeXml, wrapText } from "./text";
import { isContainer } from "./stencils";

/** White space kept around the drawing, so nothing touches the edge of the image. */
export const EXPORT_PADDING = 24;

/** The label size inside a shape, and on a connector. Matches the canvas's own. */
const FONT_SIZE = 13;
const EDGE_FONT_SIZE = 12;
const LINE_HEIGHT = 18;

const FONT_STACK = "Inter, -apple-system, BlinkMacSystemFont, 'Segoe UI', sans-serif";

export interface SvgOptions {
  palette?: DiagramPalette;
  padding?: number;
}

/** Where a node actually sits, as a routable box. */
function boxOf(doc: DiagramDocument, node: DiagramNode): Box {
  const at = absolutePosition(doc, node);
  return { x: at.x, y: at.y, width: node.width, height: node.height };
}

function element(item: ShapeElement, colours: { fill: string; stroke: string }): string {
  const paint =
    item.role === "body"
      ? `fill="${colours.fill}" stroke="${colours.stroke}"`
      : `fill="none" stroke="${colours.stroke}"`;
  const width = item.thick === true ? 2.5 : 1.5;
  const common = `${paint} stroke-width="${width}" stroke-linecap="round" stroke-linejoin="round"`;

  switch (item.shape) {
    case "rect":
      return `<rect x="${item.x}" y="${item.y}" width="${item.width}" height="${item.height}" rx="${item.rx}" ${common}/>`;
    case "ellipse":
      return `<ellipse cx="${item.cx}" cy="${item.cy}" rx="${item.rx}" ry="${item.ry}" ${common}/>`;
    case "path":
      return `<path d="${item.d}" ${common}/>`;
    case "line":
      return `<line x1="${item.x1}" y1="${item.y1}" x2="${item.x2}" y2="${item.y2}" ${common}/>`;
  }
}

/**
 * The box a node's text is written in, in the node's own coordinates.
 *
 * One function for the three placements, because where the text goes is a property of the stencil
 * and the canvas has to agree with it — a title drawn in one place here and another there is the
 * classic "the export looks different" bug, and this feature has already had one of those.
 */
export function textBox(
  placement: TextPlacement,
  width: number,
  height: number,
): { x: number; y: number; width: number; height: number } {
  switch (placement) {
    case "center":
      return { x: TEXT_INSET, y: 0, width: width - TEXT_INSET * 2, height };
    // Under the shape: a person's name, an event's label. The shape is too small to hold it.
    case "below":
      return { x: -width / 2, y: height + 4, width: width * 2, height: LINE_HEIGHT * 2 };
    // A container's title, across the top, where Mermaid draws a subgraph's.
    case "band-top":
      return { x: TEXT_INSET, y: 0, width: width - TEXT_INSET * 2, height: BAND };
  }
}

function nodeText(node: DiagramNode, palette: DiagramPalette): string {
  if (node.text.trim() === "") return "";

  const stencil = stencilById(node.kind);
  const area = textBox(stencil.text, node.width, node.height);
  const lines = wrapText(node.text, area.width, FONT_SIZE);
  if (lines.length === 0) return "";

  const colour = palette.fills[node.fill].text;
  const block = lines.length * LINE_HEIGHT;
  const centreY = area.y + area.height / 2 - block / 2 + LINE_HEIGHT * 0.75;

  const tspans = lines
    .map(
      (line, index) =>
        `<tspan x="${area.x + area.width / 2}" y="${centreY + index * LINE_HEIGHT}">${escapeXml(line)}</tspan>`,
    )
    .join("");

  return (
    `<text font-family="${FONT_STACK}" font-size="${FONT_SIZE}" fill="${colour}" ` +
    `text-anchor="middle" dominant-baseline="auto">${tspans}</text>`
  );
}

function markerDef(shape: MarkerShape, palette: DiagramPalette): string {
  const fill = shape.paint === "solid" ? palette.line : "none";
  return (
    `<marker id="${shape.id}" viewBox="0 0 ${shape.size} ${shape.size}" refX="${shape.refX}" refY="${shape.size / 2}" ` +
    `markerWidth="${shape.size}" markerHeight="${shape.size}" markerUnits="userSpaceOnUse" orient="auto">` +
    `<path d="${shape.d}" fill="${fill}" stroke="${palette.line}" stroke-width="1.5" stroke-linejoin="round"/>` +
    `</marker>`
  );
}

function renderNode(doc: DiagramDocument, node: DiagramNode, palette: DiagramPalette): string {
  const at = absolutePosition(doc, node);
  const colours = palette.fills[node.fill];
  const body = shapeElements(node.kind, node.width, node.height)
    .map((item) => element(item, colours))
    .join("");

  return `<g transform="translate(${at.x} ${at.y})">${body}${nodeText(node, palette)}</g>`;
}

function renderEdge(
  edge: DiagramDocument["edges"][number],
  boxes: Map<string, Box>,
  palette: DiagramPalette,
): string {
  const from = boxes.get(edge.from);
  const to = boxes.get(edge.to);
  if (from === undefined || to === undefined) return "";

  const style = edgeStyle(edge.kind);
  // The sides are chosen from where the two shapes are, not read off the edge: the document has
  // none to read (DIAG-017).
  const sides = chooseSides(from, to);
  const points = routeEdge(from, sides.from, to, sides.to);
  const marker = style.marker === null ? "" : ` marker-end="url(#${style.marker})"`;
  const dash = style.dashed ? ` stroke-dasharray="${DASH}"` : "";
  const weight = style.thick ? 3 : 1.5;

  const line =
    `<path d="${polylinePath(points)}" fill="none" stroke="${palette.line}" stroke-width="${weight}" ` +
    `stroke-linecap="round" stroke-linejoin="round"${dash}${marker}/>`;

  const caption = edgeCaption(edge);
  if (caption === "") return line;

  const at = midpointOf(points);
  // A plate under the label, because a connector runs straight through the middle of it otherwise.
  const width = caption.length * EDGE_FONT_SIZE * 0.55 + 8;
  const plate =
    `<rect x="${at.x - width / 2}" y="${at.y - 9}" width="${width}" height="18" rx="4" ` +
    `fill="${palette.background}" stroke="none"/>`;
  const text =
    `<text x="${at.x}" y="${at.y + 4}" font-family="${FONT_STACK}" font-size="${EDGE_FONT_SIZE}" ` +
    `fill="${palette.lineText}" text-anchor="middle">${escapeXml(caption)}</text>`;

  return `${line}${plate}${text}`;
}

/**
 * The whole document as one SVG file.
 *
 * Drawing order is containers, then connectors, then everything else: a pool has to sit behind the
 * tasks inside it, and a connector has to pass under a shape rather than over its label.
 */
export function documentToSvg(doc: DiagramDocument, options: SvgOptions = {}): string {
  const palette = options.palette ?? exportPalette;
  const padding = options.padding ?? EXPORT_PADDING;
  const bounds = documentBounds(doc);

  // An empty document still exports: a blank page is a truthful picture of a blank diagram, and it
  // beats a failure the user has to interpret.
  const width = Math.max(1, (bounds?.width ?? 0) + padding * 2);
  const height = Math.max(1, (bounds?.height ?? 0) + padding * 2);
  const originX = padding - (bounds?.x ?? 0);
  const originY = padding - (bounds?.y ?? 0);

  const boxes = new Map(doc.nodes.map((node) => [node.id, boxOf(doc, node)]));
  const containers = doc.nodes.filter((node) => isContainer(node.kind));
  const shapes = doc.nodes.filter((node) => !isContainer(node.kind));

  const defs = MARKER_SHAPES.map((shape) => markerDef(shape, palette)).join("");
  const body = [
    ...containers.map((node) => renderNode(doc, node, palette)),
    ...doc.edges.map((edge) => renderEdge(edge, boxes, palette)),
    ...shapes.map((node) => renderNode(doc, node, palette)),
  ].join("");

  const title = doc.title.trim() === "" ? "" : `<title>${escapeXml(doc.title)}</title>`;

  return (
    `<?xml version="1.0" encoding="UTF-8"?>\n` +
    `<svg xmlns="http://www.w3.org/2000/svg" width="${width}" height="${height}" ` +
    `viewBox="0 0 ${width} ${height}">` +
    title +
    `<rect width="${width}" height="${height}" fill="${palette.background}"/>` +
    `<defs>${defs}</defs>` +
    `<g transform="translate(${originX} ${originY})">${body}</g>` +
    `</svg>\n`
  );
}
