import type { DiagramPalette } from "../../lib/diagram/palette";
import { shapeElements, type ShapeElement } from "../../lib/diagram/shapes";
import type { FillToken } from "../../lib/diagram/model";
import type { StencilId } from "../../lib/diagram/stencils";

/**
 * One figure, drawn (DIAG-006).
 *
 * The SVG half of a node, and the same elements the exporter writes — `shapes.ts` decides the
 * outline, this decides nothing. Used by the canvas node and by the palette's previews, so a shape
 * in the palette is the shape you get.
 *
 * No text: the label is HTML on top of this, because HTML wraps and `<text>` does not. The
 * exporter, which has no HTML, wraps by hand in `text.ts`.
 */
export function ShapeGlyph({
  kind,
  width,
  height,
  fill,
  palette,
}: {
  kind: StencilId;
  width: number;
  height: number;
  fill: FillToken;
  palette: DiagramPalette;
}) {
  const colours = palette.fills[fill];

  return (
    <svg
      width={width}
      height={height}
      viewBox={`0 0 ${width} ${height}`}
      // Decorative: the node's label is the accessible name, and announcing "image" before every
      // shape would double the noise on a diagram with forty of them.
      aria-hidden="true"
      className="pointer-events-none absolute inset-0"
    >
      {shapeElements(kind, width, height).map((item, index) => (
        <Element key={index} item={item} colours={colours} />
      ))}
    </svg>
  );
}

function Element({
  item,
  colours,
}: {
  item: ShapeElement;
  colours: { fill: string; stroke: string };
}) {
  const paint = {
    fill: item.role === "body" ? colours.fill : "none",
    stroke: colours.stroke,
    strokeWidth: item.thick === true ? 2.5 : 1.5,
    strokeLinecap: "round" as const,
    strokeLinejoin: "round" as const,
  };

  switch (item.shape) {
    case "rect":
      return <rect x={item.x} y={item.y} width={item.width} height={item.height} rx={item.rx} {...paint} />;
    case "ellipse":
      return <ellipse cx={item.cx} cy={item.cy} rx={item.rx} ry={item.ry} {...paint} />;
    case "path":
      return <path d={item.d} {...paint} />;
    case "line":
      return <line x1={item.x1} y1={item.y1} x2={item.x2} y2={item.y2} {...paint} />;
  }
}
