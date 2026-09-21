import { MARKER_SHAPES, markerDomId } from "../../lib/diagram/markers";
import type { DiagramPalette } from "../../lib/diagram/palette";

/**
 * The arrowheads, defined once for the whole canvas (DIAG-006).
 *
 * React Flow draws each edge in its own element, so a `<defs>` inside one edge would not be
 * reachable from the next. A zero-sized `<svg>` mounted beside the canvas is the usual answer:
 * `marker-end="url(#id)"` resolves against the whole document, so every edge finds them.
 *
 * The same three shapes the exporter writes into its own `<defs>` (`toSvg.ts`), from the same
 * table, which is what keeps an exported arrowhead identical to the one on screen. The hollow
 * triangle is the reason any of this exists: UML's generalisation has no SVG default.
 */
export function DiagramMarkers({ palette }: { palette: DiagramPalette }) {
  return (
    <svg width="0" height="0" aria-hidden="true" className="absolute">
      <defs>
        {MARKER_SHAPES.map((shape) => (
          <marker
            key={shape.id}
            id={markerDomId(shape.id)}
            viewBox={`0 0 ${shape.size} ${shape.size}`}
            refX={shape.refX}
            refY={shape.size / 2}
            markerWidth={shape.size}
            markerHeight={shape.size}
            // Not the default `strokeWidth`, which would scale every head with its line's width.
            markerUnits="userSpaceOnUse"
            orient="auto"
          >
            <path
              d={shape.d}
              fill={shape.paint === "solid" ? palette.line : "none"}
              stroke={palette.line}
              strokeWidth={1.5}
              strokeLinejoin="round"
            />
          </marker>
        ))}
      </defs>
    </svg>
  );
}
