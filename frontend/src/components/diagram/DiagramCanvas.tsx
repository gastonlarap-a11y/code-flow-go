import {
  useCallback,
  useEffect,
  useImperativeHandle,
  useMemo,
  useRef,
  useState,
  type PointerEvent as ReactPointerEvent,
  type Ref,
} from "react";
import { DRAG_THRESHOLD, setDragCursor } from "../../lib/pointerDrag";
import { IDENTITY, fitBounds, toWorld, zoomAt, type Viewport } from "../../lib/canvas/viewport";
import {
  connect,
  constrainSize,
  containerAt,
  moveNodes,
  reparent,
  resizeNode,
  setEdgeLabel,
  setNodeText,
} from "../../lib/diagram/edits";
import {
  boxOf,
  edgeAt,
  edgePath,
  edgesIn,
  nodeAt,
  nodesIn,
  normalizeRect,
} from "../../lib/diagram/picking";
import { HANDLES, handleCursor, handlePoint, resizeBox, type Handle } from "../../lib/diagram/resize";

/**
 * The field a connector's label is typed into, in document units.
 *
 * Wider than the caption it replaces, because a label being written is longer than the one already
 * there more often than not.
 */
const EDGE_EDITOR = { width: 150, height: 26 };
import { DASH, edgeCaption, edgeStyle, markerDomId } from "../../lib/diagram/markers";
import { absolutePosition, documentBounds, nodeById, type DiagramNode } from "../../lib/diagram/model";
import { diagramPalette } from "../../lib/diagram/palette";
import { isFixedRatio, stencilById } from "../../lib/diagram/stencils";
import type { Box, Point } from "../../lib/diagram/routing";
import { useDiagramStore } from "../../state/diagramStore";
import { useThemeStore } from "../../state/themeStore";
import { DiagramMarkers } from "./DiagramMarkers";
import { ShapeGlyph } from "./ShapeGlyph";
import { LabelEditor, ShapeLabel } from "./ShapeLabel";

/**
 * The drawing surface (DIAG-017).
 *
 * Absolutely-positioned shapes inside one transformed wrapper, with a single `<svg>` over them for
 * the connectors — the same construction as `components/dbml/DbmlCanvas.tsx`, and for the same
 * reason: a canvas whose geometry is ours is one whose behaviour we can explain.
 *
 * It replaced React Flow, which drew the shapes perfectly well and never measured them: with no
 * measurements it had no handle positions, so it refused to start or accept a connector and said
 * nothing about why (the old `DIAG` open defect). Every piece of arithmetic here is a pure module
 * with a test beside it — `picking.ts`, `resize.ts`, `routing.ts`, `lib/canvas/viewport.ts`.
 */
export interface DiagramCanvasHandle {
  fit: () => void;
  zoomBy: (factor: number) => void;
  /** Where a new shape should go: the middle of what is on screen. */
  centreOfView: () => Point;
}

/** How far apart the grid's dots are, and what a drag snaps to. */
const GRID = 8;

/** What the pointer is in the middle of doing. */
type Gesture =
  | { kind: "none" }
  | { kind: "pan"; from: Point; view: Viewport }
  | { kind: "marquee"; from: Point; to: Point; additive: boolean }
  | { kind: "move"; from: Point; origins: Map<string, Point>; moved: boolean }
  | { kind: "resize"; id: string; handle: Handle; from: Point; box: Box }
  | { kind: "connect"; from: string; at: Point };

function snap(value: number): number {
  return Math.round(value / GRID) * GRID;
}

export function DiagramCanvas({ ref }: { ref?: Ref<DiagramCanvasHandle> }) {
  const theme = useThemeStore((s) => s.resolved);
  const palette = useMemo(() => diagramPalette(theme), [theme]);

  const doc = useDiagramStore((s) => s.history.present);
  const selection = useDiagramStore((s) => s.selection);
  const edit = useDiagramStore((s) => s.edit);
  const select = useDiagramStore((s) => s.select);
  const editable = useDiagramStore((s) => s.editable);

  const surface = useRef<HTMLDivElement>(null);
  const [view, setView] = useState<Viewport>(IDENTITY);
  const [gesture, setGesture] = useState<Gesture>({ kind: "none" });
  const [editingId, setEditingId] = useState<string | null>(null);
  const [editingEdgeId, setEditingEdgeId] = useState<string | null>(null);

  const selectedNodes = useMemo(() => new Set(selection.nodes), [selection.nodes]);
  const selectedEdges = useMemo(() => new Set(selection.edges), [selection.edges]);

  /** A screen event's position in document coordinates. */
  const pointOf = useCallback(
    (event: { clientX: number; clientY: number }): Point => {
      const box = surface.current?.getBoundingClientRect();
      return toWorld(view, {
        x: event.clientX - (box?.left ?? 0),
        y: event.clientY - (box?.top ?? 0),
      });
    },
    [view],
  );

  useImperativeHandle(ref, () => ({
    fit: () => {
      const box = surface.current?.getBoundingClientRect();
      setView(fitBounds(documentBounds(doc), { width: box?.width ?? 0, height: box?.height ?? 0 }));
    },
    zoomBy: (factor: number) => {
      const box = surface.current?.getBoundingClientRect();
      const centre = { x: (box?.width ?? 0) / 2, y: (box?.height ?? 0) / 2 };
      setView((current) => zoomAt(current, centre, factor));
    },
    centreOfView: () => {
      const box = surface.current?.getBoundingClientRect();
      return toWorld(view, { x: (box?.width ?? 0) / 2, y: (box?.height ?? 0) / 2 });
    },
  }));

  /**
   * Zoom, on a native listener.
   *
   * React's `onWheel` is passive and cannot `preventDefault()`, so the page would scroll under the
   * canvas. Same reason and same fix as the schema designer's.
   */
  useEffect(() => {
    const element = surface.current;
    if (element === null) return;

    const onWheel = (event: WheelEvent) => {
      const box = element.getBoundingClientRect();
      const at = { x: event.clientX - box.left, y: event.clientY - box.top };

      if (event.ctrlKey || event.metaKey) {
        event.preventDefault();
        setView((current) => zoomAt(current, at, event.deltaY < 0 ? 1.1 : 1 / 1.1));
        return;
      }
      event.preventDefault();
      setView((current) => ({ ...current, x: current.x - event.deltaX, y: current.y - event.deltaY }));
    };

    element.addEventListener("wheel", onWheel, { passive: false });
    return () => element.removeEventListener("wheel", onWheel);
  }, []);

  const boxes = useMemo(() => {
    const map = new Map<string, Box>();
    for (const node of doc.nodes) map.set(node.id, boxOf(doc, node));
    return map;
  }, [doc]);

  // ---- the gestures ------------------------------------------------------------------------

  const onPointerDown = (event: ReactPointerEvent<HTMLDivElement>) => {
    if (event.button === 1 || event.button === 2) {
      setGesture({ kind: "pan", from: { x: event.clientX, y: event.clientY }, view });
      event.currentTarget.setPointerCapture(event.pointerId);
      return;
    }
    if (event.button !== 0) return;

    const at = pointOf(event);
    event.currentTarget.setPointerCapture(event.pointerId);

    const node = nodeAt(doc, at);
    if (node !== null) {
      const already = selectedNodes.has(node.id);
      const ids = event.shiftKey
        ? already
          ? selection.nodes.filter((id) => id !== node.id)
          : [...selection.nodes, node.id]
        : already
          ? selection.nodes
          : [node.id];
      select({ nodes: ids, edges: event.shiftKey ? selection.edges : [] });

      const origins = new Map<string, Point>();
      for (const id of ids) {
        const moving = nodeById(doc, id);
        if (moving !== null) origins.set(id, { x: moving.x, y: moving.y });
      }
      setGesture({ kind: "move", from: at, origins, moved: false });
      return;
    }

    const edge = edgeAt(doc, at);
    if (edge !== null) {
      select({ nodes: [], edges: [edge] });
      setGesture({ kind: "none" });
      return;
    }

    setGesture({ kind: "marquee", from: at, to: at, additive: event.shiftKey });
  };

  const onPointerMove = (event: ReactPointerEvent<HTMLDivElement>) => {
    if (gesture.kind === "none") return;

    if (gesture.kind === "pan") {
      setView({
        ...gesture.view,
        x: gesture.view.x + (event.clientX - gesture.from.x),
        y: gesture.view.y + (event.clientY - gesture.from.y),
      });
      return;
    }

    const at = pointOf(event);

    if (gesture.kind === "marquee") {
      setGesture({ ...gesture, to: at });
      return;
    }

    if (gesture.kind === "connect") {
      setGesture({ ...gesture, at });
      return;
    }

    if (gesture.kind === "move") {
      const dx = at.x - gesture.from.x;
      const dy = at.y - gesture.from.y;
      if (!gesture.moved && Math.hypot(dx, dy) < DRAG_THRESHOLD) return;
      if (!gesture.moved) setDragCursor(true);

      const moves = [...gesture.origins].map(([id, origin]) => ({
        id,
        x: snap(origin.x + dx),
        y: snap(origin.y + dy),
      }));
      edit((current) => moveNodes(current, moves), { amend: true });
      setGesture({ ...gesture, moved: true });
      return;
    }

    if (gesture.kind === "resize") {
      const node = nodeById(doc, gesture.id);
      if (node === null) return;
      const next = resizeBox(
        gesture.box,
        gesture.handle,
        at.x - gesture.from.x,
        at.y - gesture.from.y,
        isFixedRatio(node.kind),
      );
      // The stored position is relative to a container; the handle worked in absolute space.
      const origin = absolutePosition(doc, node);
      edit(
        (current) =>
          resizeNode(current, gesture.id, {
            x: node.x + (next.x - origin.x),
            y: node.y + (next.y - origin.y),
            ...constrainSize(node.kind, next.width, next.height),
          }),
        { amend: true },
      );
      return;
    }
  };

  const onPointerUp = (event: ReactPointerEvent<HTMLDivElement>) => {
    const at = pointOf(event);
    setDragCursor(false);

    if (gesture.kind === "marquee") {
      const rect = normalizeRect(gesture.from, gesture.to);
      const nodes = nodesIn(doc, rect);
      const edges = edgesIn(doc, rect);
      select(
        gesture.additive
          ? {
              nodes: [...new Set([...selection.nodes, ...nodes])],
              edges: [...new Set([...selection.edges, ...edges])],
            }
          : { nodes, edges },
      );
    }

    if (gesture.kind === "move" && gesture.moved) {
      // Where it landed decides whether it went into a container, out of one, or nowhere.
      edit((current) => {
        let next = current;
        for (const id of gesture.origins.keys()) {
          const node = nodeById(next, id);
          if (node === null) continue;
          const centre = {
            x: absolutePosition(next, node).x + node.width / 2,
            y: absolutePosition(next, node).y + node.height / 2,
          };
          next = reparent(next, id, containerAt(next, centre, [id]));
        }
        return next;
      });
    }

    if (gesture.kind === "connect") {
      const target = nodeAt(doc, at);
      if (target !== null) edit((current) => connect(current, gesture.from, target.id));
    }

    setGesture({ kind: "none" });
  };

  const onDoubleClick = (event: ReactPointerEvent<HTMLDivElement>) => {
    if (!editable) return;

    const at = pointOf(event);
    const node = nodeAt(doc, at);
    if (node !== null) {
      setEditingId(node.id);
      return;
    }

    // Missing every shape, the double click is aimed at whatever connector runs under it — which
    // is how somebody labels an arrow without knowing the inspector exists.
    const edge = edgeAt(doc, at);
    if (edge !== null) setEditingEdgeId(edge);
  };

  // ---- what is drawn -----------------------------------------------------------------------

  const bounds = documentBounds(doc);
  const margin = 400;
  const svgBox = {
    x: (bounds?.x ?? 0) - margin,
    y: (bounds?.y ?? 0) - margin,
    width: (bounds?.width ?? 0) + margin * 2,
    height: (bounds?.height ?? 0) + margin * 2,
  };

  const selectedSingle =
    selection.nodes.length === 1 ? (nodeById(doc, selection.nodes[0]!) ?? null) : null;

  /**
   * The connector being labelled, and where its field goes.
   *
   * Derived rather than held: an edge deleted while its label was open would otherwise leave a
   * field floating over nothing.
   */
  const editingEdge = (() => {
    if (editingEdgeId === null) return null;
    const edge = doc.edges.find((one) => one.id === editingEdgeId);
    if (edge === undefined) return null;

    const from = boxes.get(edge.from);
    const to = boxes.get(edge.to);
    if (from === undefined || to === undefined) return null;

    return { id: edge.id, label: edge.label, at: edgePath(from, to).at };
  })();

  return (
    <div
      ref={surface}
      onPointerDown={onPointerDown}
      onPointerMove={onPointerMove}
      onPointerUp={onPointerUp}
      onPointerCancel={onPointerUp}
      onDoubleClick={onDoubleClick}
      onContextMenu={(event) => event.preventDefault()}
      className="relative h-full w-full overflow-hidden"
      style={{
        background: palette.background,
        backgroundImage: `radial-gradient(circle, ${palette.grid} 1px, transparent 1px)`,
        backgroundSize: `${GRID * 3 * view.scale}px ${GRID * 3 * view.scale}px`,
        backgroundPosition: `${view.x}px ${view.y}px`,
        cursor: gesture.kind === "pan" ? "grabbing" : "default",
      }}
    >
      <DiagramMarkers palette={palette} />

      <div
        className="absolute left-0 top-0 origin-top-left"
        style={{ transform: `translate(${view.x}px, ${view.y}px) scale(${view.scale})` }}
      >
        <svg
          className="pointer-events-none absolute"
          style={{ left: svgBox.x, top: svgBox.y, width: svgBox.width, height: svgBox.height }}
          viewBox={`${svgBox.x} ${svgBox.y} ${svgBox.width} ${svgBox.height}`}
          aria-hidden="true"
        >
          {doc.edges.map((edge) => {
            const from = boxes.get(edge.from);
            const to = boxes.get(edge.to);
            if (from === undefined || to === undefined) return null;

            const style = edgeStyle(edge.kind);
            const { d, at } = edgePath(from, to);
            const chosen = selectedEdges.has(edge.id);
            const caption = edgeCaption(edge);

            return (
              <g key={edge.id}>
                <path
                  d={d}
                  fill="none"
                  stroke={chosen ? palette.selection : palette.line}
                  strokeWidth={chosen ? 3 : style.thick ? 3 : 1.5}
                  strokeLinecap="round"
                  strokeLinejoin="round"
                  strokeDasharray={style.dashed ? DASH : undefined}
                  markerEnd={style.marker === null ? undefined : `url(#${markerDomId(style.marker)})`}
                />
                {caption !== "" && (
                  <>
                    <rect
                      x={at.x - caption.length * 3.4 - 4}
                      y={at.y - 9}
                      width={caption.length * 6.8 + 8}
                      height={18}
                      rx={4}
                      fill={palette.background}
                    />
                    <text x={at.x} y={at.y + 4} textAnchor="middle" fontSize={12} fill={palette.lineText}>
                      {caption}
                    </text>
                  </>
                )}
              </g>
            );
          })}

          {gesture.kind === "connect" && renderPendingConnector(boxes.get(gesture.from), gesture.at, palette.selection)}
        </svg>

        {doc.nodes.map((node) => (
          <Shape
            key={node.id}
            doc={doc}
            node={node}
            palette={palette}
            selected={selectedNodes.has(node.id)}
            editing={editingId === node.id}
            editable={editable}
            onStartConnect={(from) => setGesture({ kind: "connect", from, at: from ? boxCentre(boxes.get(node.id)) : { x: 0, y: 0 } })}
            onStartResize={(handle, at) => {
              const box = boxes.get(node.id);
              if (box !== undefined) setGesture({ kind: "resize", id: node.id, handle, from: at, box });
            }}
            onCommitText={(text) => {
              setEditingId(null);
              edit((current) => setNodeText(current, node.id, text));
            }}
            onCancelText={() => setEditingId(null)}
            pointOf={pointOf}
          />
        ))}

        {/* A connector's label, typed over the middle of the run it belongs to. Inside the
            transformed wrapper like everything else, so it sits on the arrow at every zoom. */}
        {editingEdge !== null && (
          <div
            className="absolute"
            style={{
              left: editingEdge.at.x - EDGE_EDITOR.width / 2,
              top: editingEdge.at.y - EDGE_EDITOR.height / 2,
              width: EDGE_EDITOR.width,
              height: EDGE_EDITOR.height,
              background: palette.background,
            }}
          >
            <LabelEditor
              text={editingEdge.label}
              colour={palette.lineText}
              onCommit={(text) => {
                setEditingEdgeId(null);
                edit((current) => setEdgeLabel(current, editingEdge.id, text));
              }}
              onCancel={() => setEditingEdgeId(null)}
            />
          </div>
        )}

        {gesture.kind === "marquee" && (
          <div
            className="pointer-events-none absolute"
            style={{
              ...rectStyle(normalizeRect(gesture.from, gesture.to)),
              border: `1px solid ${palette.selection}`,
              background: `${palette.selection}22`,
            }}
          />
        )}

        {selectedSingle !== null && editingId === null && (
          <Grips box={boxes.get(selectedSingle.id)!} palette={palette} />
        )}
      </div>
    </div>
  );
}

function boxCentre(box: Box | undefined): Point {
  return box === undefined ? { x: 0, y: 0 } : { x: box.x + box.width / 2, y: box.y + box.height / 2 };
}

function rectStyle(rect: Box) {
  return { left: rect.x, top: rect.y, width: rect.width, height: rect.height };
}

/** The line that follows the pointer while a connector is being drawn. */
function renderPendingConnector(from: Box | undefined, to: Point, colour: string) {
  if (from === undefined) return null;
  const start = boxCentre(from);
  return (
    <path
      d={`M ${start.x} ${start.y} L ${to.x} ${to.y}`}
      fill="none"
      stroke={colour}
      strokeWidth={2}
      strokeDasharray="4 4"
    />
  );
}

/** The eight grips of the selected shape. */
function Grips({ box, palette }: { box: Box; palette: ReturnType<typeof diagramPalette> }) {
  return (
    <>
      <div
        className="pointer-events-none absolute"
        style={{ ...rectStyle(box), border: `1px solid ${palette.selection}` }}
      />
      {HANDLES.map((handle) => {
        const at = handlePoint(box, handle);
        return (
          <div
            key={handle}
            data-grip={handle}
            className="absolute"
            style={{
              left: at.x - 4,
              top: at.y - 4,
              width: 8,
              height: 8,
              background: palette.background,
              border: `1.5px solid ${palette.selection}`,
              cursor: handleCursor(handle),
            }}
          />
        );
      })}
    </>
  );
}

function Shape({
  doc,
  node,
  palette,
  selected,
  editing,
  editable,
  onStartConnect,
  onStartResize,
  onCommitText,
  onCancelText,
  pointOf,
}: {
  doc: ReturnType<typeof useDiagramStore.getState>["history"]["present"];
  node: DiagramNode;
  palette: ReturnType<typeof diagramPalette>;
  selected: boolean;
  editing: boolean;
  editable: boolean;
  onStartConnect: (from: string) => void;
  onStartResize: (handle: Handle, at: Point) => void;
  onCommitText: (text: string) => void;
  onCancelText: () => void;
  pointOf: (event: { clientX: number; clientY: number }) => Point;
}) {
  const at = absolutePosition(doc, node);
  const stencil = stencilById(node.kind);

  return (
    <div className="absolute" style={{ left: at.x, top: at.y, width: node.width, height: node.height }}>
      <ShapeGlyph
        kind={node.kind}
        width={node.width}
        height={node.height}
        fill={node.fill}
        palette={palette}
      />
      <ShapeLabel
        text={node.text}
        placement={stencil.text}
        width={node.width}
        height={node.height}
        colour={palette.fills[node.fill].text}
        editing={editing}
        onCommit={onCommitText}
        onCancel={onCancelText}
      />

      {/* One grab point per side, just outside the outline, for drawing a connector. Shown on
          hover so a canvas of forty shapes is not a canvas of a hundred and sixty dots. */}
      {editable && (
        <div className="absolute inset-0 opacity-0 transition-opacity hover:opacity-100">
          {(["top", "right", "bottom", "left"] as const).map((side) => (
            <button
              key={side}
              type="button"
              aria-hidden="true"
              tabIndex={-1}
              onPointerDown={(event) => {
                event.stopPropagation();
                onStartConnect(node.id);
              }}
              className="absolute"
              style={{
                width: 11,
                height: 11,
                borderRadius: 999,
                background: palette.background,
                border: `1.5px solid ${palette.selection}`,
                cursor: "crosshair",
                ...sideOffset(side, node.width, node.height),
              }}
            />
          ))}
        </div>
      )}

      {selected && (
        <div className="pointer-events-none absolute inset-0">
          {HANDLES.map((handle) => {
            const grip = handlePoint({ x: 0, y: 0, width: node.width, height: node.height }, handle);
            return (
              <span
                key={handle}
                className="pointer-events-auto absolute"
                onPointerDown={(event) => {
                  event.stopPropagation();
                  onStartResize(handle, pointOf(event));
                }}
                style={{
                  left: grip.x - 5,
                  top: grip.y - 5,
                  width: 10,
                  height: 10,
                  cursor: handleCursor(handle),
                }}
              />
            );
          })}
        </div>
      )}
    </div>
  );
}

function sideOffset(side: "top" | "right" | "bottom" | "left", width: number, height: number) {
  switch (side) {
    case "top":
      return { left: width / 2 - 5.5, top: -16 };
    case "right":
      return { left: width + 5, top: height / 2 - 5.5 };
    case "bottom":
      return { left: width / 2 - 5.5, top: height + 5 };
    case "left":
      return { left: -16, top: height / 2 - 5.5 };
  }
}
