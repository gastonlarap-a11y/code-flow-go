import { useCallback, useEffect, useMemo, useRef, useState, type KeyboardEvent, type PointerEvent } from "react";
import { FileCode2, Folder, Maximize2, Users, ZoomIn, ZoomOut } from "lucide-react";
import { IconButton } from "../../common/IconButton";
import { fitBounds, zoomAt, type Point, type Rect, type Viewport, IDENTITY } from "../../../lib/canvas/viewport";
import { CARD_HEIGHT, CARD_WIDTH, layoutMap } from "../../../lib/repomap/layout";
import { parentOf } from "../../../lib/repomap/labels";
import { DRAG_THRESHOLD, setDragCursor } from "../../../lib/pointerDrag";
import { useT } from "../../../state/languageStore";
import type { RepoMapEdge, RepoMapNode } from "../../../types/domain";

/**
 * The code map's canvas (REPOMAP-005): folders and files as cards, joined by their imports.
 *
 * Built the way the schema designer's and the diagram editor's canvases are — absolutely placed
 * cards in one transformed layer, every line in one `<svg>` under them, the arithmetic in
 * `lib/canvas/viewport.ts` — rather than on a graph library, for the reason DIAG-017 gives: React
 * Flow never measured its nodes inside WKWebView. Nothing here is editable: a card is picked, opened
 * or hovered, and the view pans and zooms.
 */

const WHEEL_ZOOM = 1.1;
const BUTTON_ZOOM = 1.25;
/** How long the spotlight outlives the pointer, so crossing the gap between two cards does not flash the whole map. */
const HOVER_GRACE_MS = 70;
const MARGIN = 400;

export function MapCanvas({
  nodes,
  edges,
  colors,
  project,
  selected,
  onSelect,
  onOpen,
  layoutKey,
}: {
  nodes: readonly RepoMapNode[];
  edges: readonly RepoMapEdge[];
  /** Group → colour, in the project view; empty in a folder view. */
  colors: ReadonlyMap<string, string>;
  /** The project view shows each card's folder above its name. */
  project: boolean;
  selected: string | null;
  onSelect: (id: string | null) => void;
  onOpen: (node: RepoMapNode) => void;
  /** Changes when what is shown changes, which is when the view fits itself again. */
  layoutKey: string;
}) {
  const t = useT();
  const containerRef = useRef<HTMLDivElement>(null);
  const [view, setView] = useState<Viewport>(IDENTITY);
  const [hovered, setHovered] = useState<string | null>(null);
  const hoverTimer = useRef<ReturnType<typeof setTimeout> | null>(null);
  const pan = useRef<{ pointerId: number; start: Point; origin: Viewport; moved: boolean } | null>(null);

  const ids = useMemo(() => new Set(nodes.map((n) => n.id)), [nodes]);
  const drawnEdges = useMemo(() => edges.filter((e) => ids.has(e.from) && ids.has(e.to)), [edges, ids]);

  const placed = useMemo(
    () =>
      layoutMap(
        nodes.map((n) => ({ id: n.id, folder: n.folder, importedBy: n.imported_by, label: n.label })),
        drawnEdges,
      ),
    [nodes, drawnEdges],
  );

  const bounds = useMemo((): Rect | null => {
    if (placed.size === 0) return null;
    const xs = [...placed.values()].map((p) => p.x);
    const ys = [...placed.values()].map((p) => p.y);
    const x = Math.min(...xs);
    const y = Math.min(...ys);
    return { x, y, width: Math.max(...xs) - x + CARD_WIDTH, height: Math.max(...ys) - y + CARD_HEIGHT };
  }, [placed]);

  const fit = useCallback(() => {
    const container = containerRef.current;
    if (!container) return;
    const { width, height } = container.getBoundingClientRect();
    setView(fitBounds(bounds, { width, height }));
  }, [bounds]);

  const zoomBy = (factor: number) => {
    const container = containerRef.current;
    if (!container) return;
    const { width, height } = container.getBoundingClientRect();
    setView((current) => zoomAt(current, { x: width / 2, y: height / 2 }, factor));
  };

  // Fit whenever what is shown changes — another folder, the other mode — and not on a hover.
  const fittedFor = useRef<string | null>(null);
  useEffect(() => {
    if (placed.size === 0 || fittedFor.current === layoutKey) return;
    fittedFor.current = layoutKey;
    fit();
  }, [layoutKey, placed.size, fit]);

  // A native, non-passive listener: React registers `onWheel` as passive, so `preventDefault` there
  // cannot stop Ctrl+wheel from zooming the whole window instead of the map.
  useEffect(() => {
    const container = containerRef.current;
    if (!container) return;
    const onWheel = (event: WheelEvent) => {
      event.preventDefault();
      const box = container.getBoundingClientRect();
      if (event.ctrlKey || event.metaKey) {
        const factor = event.deltaY < 0 ? WHEEL_ZOOM : 1 / WHEEL_ZOOM;
        setView((current) => zoomAt(current, { x: event.clientX - box.left, y: event.clientY - box.top }, factor));
        return;
      }
      setView((current) => ({ ...current, x: current.x - event.deltaX, y: current.y - event.deltaY }));
    };
    container.addEventListener("wheel", onWheel, { passive: false });
    return () => container.removeEventListener("wheel", onWheel);
  }, []);

  useEffect(
    () => () => {
      if (hoverTimer.current) clearTimeout(hoverTimer.current);
    },
    [],
  );

  const hoverIn = (id: string) => {
    if (hoverTimer.current) clearTimeout(hoverTimer.current);
    setHovered(id);
  };
  const hoverOut = () => {
    if (hoverTimer.current) clearTimeout(hoverTimer.current);
    hoverTimer.current = setTimeout(() => setHovered(null), HOVER_GRACE_MS);
  };

  // What the spotlight is on — the hovered card, else the picked one — and what it touches.
  const spotlit = hovered ?? selected;
  const linked = useMemo(() => {
    const out = new Set<string>();
    if (spotlit === null) return out;
    for (const e of drawnEdges) {
      if (e.from === spotlit) out.add(e.to);
      if (e.to === spotlit) out.add(e.from);
    }
    return out;
  }, [spotlit, drawnEdges]);

  // ---------- panning the background ----------

  const onBackgroundPointerDown = (event: PointerEvent<HTMLDivElement>) => {
    if (event.button !== 0 || event.target !== event.currentTarget) return;
    event.currentTarget.setPointerCapture(event.pointerId);
    pan.current = { pointerId: event.pointerId, start: { x: event.clientX, y: event.clientY }, origin: view, moved: false };
  };

  const onBackgroundPointerMove = (event: PointerEvent<HTMLDivElement>) => {
    const current = pan.current;
    if (!current || current.pointerId !== event.pointerId) return;
    const dx = event.clientX - current.start.x;
    const dy = event.clientY - current.start.y;
    if (!current.moved && Math.hypot(dx, dy) < DRAG_THRESHOLD) return;
    if (!current.moved) {
      current.moved = true;
      setDragCursor(true);
    }
    setView({ ...current.origin, x: current.origin.x + dx, y: current.origin.y + dy });
  };

  const endPan = (event: PointerEvent<HTMLDivElement>) => {
    const current = pan.current;
    if (current?.pointerId !== event.pointerId) return;
    pan.current = null;
    setDragCursor(false);
    // A press that never moved is a click on nothing, which lets go of the picked card.
    if (!current.moved && event.type === "pointerup") onSelect(null);
  };

  const onCardKeyDown = (node: RepoMapNode) => (event: KeyboardEvent<HTMLButtonElement>) => {
    if (event.key === "Enter") {
      event.preventDefault();
      onOpen(node);
    }
  };

  const svgBox = bounds && {
    x: bounds.x - MARGIN,
    y: bounds.y - MARGIN,
    width: bounds.width + MARGIN * 2,
    height: bounds.height + MARGIN * 2,
  };
  const gridSize = 22 * view.scale;

  return (
    <div
      ref={containerRef}
      className="relative h-full w-full touch-none overflow-hidden bg-[var(--cf-bg)]"
      style={{
        backgroundImage: "radial-gradient(circle, var(--cf-border) 1px, transparent 1px)",
        backgroundSize: `${gridSize}px ${gridSize}px`,
        backgroundPosition: `${view.x}px ${view.y}px`,
      }}
      onPointerDown={onBackgroundPointerDown}
      onPointerMove={onBackgroundPointerMove}
      onPointerUp={endPan}
      onPointerCancel={endPan}
      data-testid="map-canvas"
    >
      <div
        className="pointer-events-none absolute left-0 top-0"
        style={{ transform: `translate(${view.x}px, ${view.y}px) scale(${view.scale})`, transformOrigin: "0 0" }}
      >
        {svgBox && (
          <svg
            className="absolute overflow-visible"
            style={{ left: svgBox.x, top: svgBox.y }}
            width={svgBox.width}
            height={svgBox.height}
            aria-hidden
          >
            <defs>
              <marker id="cf-map-arrow" viewBox="0 0 8 8" refX="7" refY="4" markerWidth="7" markerHeight="7" orient="auto-start-reverse">
                <path d="M0,0 L8,4 L0,8 z" fill="var(--cf-text-muted)" />
              </marker>
              <marker id="cf-map-arrow-lit" viewBox="0 0 8 8" refX="7" refY="4" markerWidth="7" markerHeight="7" orient="auto-start-reverse">
                <path d="M0,0 L8,4 L0,8 z" fill="var(--cf-accent)" />
              </marker>
            </defs>
            <g transform={`translate(${-svgBox.x} ${-svgBox.y})`}>
              {drawnEdges.map((edge) => {
                const from = placed.get(edge.from);
                const to = placed.get(edge.to);
                if (!from || !to) return null;
                const lit = spotlit !== null && (edge.from === spotlit || edge.to === spotlit);
                const faded = spotlit !== null && !lit;
                return (
                  <path
                    key={`${edge.from}→${edge.to}`}
                    d={curve(from, to)}
                    fill="none"
                    stroke={lit ? "var(--cf-accent)" : "var(--cf-text-muted)"}
                    strokeOpacity={faded ? 0.12 : lit ? 0.95 : 0.4}
                    strokeWidth={edgeWidth(edge.weight) + (lit ? 0.5 : 0)}
                    markerEnd={lit ? "url(#cf-map-arrow-lit)" : "url(#cf-map-arrow)"}
                    className="transition-[stroke-opacity] duration-150 motion-reduce:transition-none"
                  />
                );
              })}
            </g>
          </svg>
        )}

        {nodes.map((node) => {
          const at = placed.get(node.id);
          if (!at) return null;
          const color = colors.get(node.group);
          const dimmed = spotlit !== null && node.id !== spotlit && !linked.has(node.id);
          return (
            <MapCard
              key={node.id}
              node={node}
              at={at}
              color={color}
              project={project}
              selected={node.id === selected}
              dimmed={dimmed}
              onClick={() => onSelect(node.id)}
              onDoubleClick={() => onOpen(node)}
              onKeyDown={onCardKeyDown(node)}
              onPointerEnter={() => hoverIn(node.id)}
              onPointerLeave={hoverOut}
            />
          );
        })}
      </div>

      <div className="absolute bottom-2 right-2 flex items-center gap-0.5 rounded-control border border-[var(--cf-border)] bg-[var(--cf-surface-raised)] p-0.5 shadow-[var(--cf-shadow-near)]">
        <IconButton label="codemap.map.zoomOut" icon={ZoomOut} onClick={() => zoomBy(1 / BUTTON_ZOOM)} />
        <IconButton label="codemap.map.zoomIn" icon={ZoomIn} onClick={() => zoomBy(BUTTON_ZOOM)} />
        <IconButton label="codemap.map.fit" icon={Maximize2} onClick={fit} />
      </div>
      <span className="sr-only">{t("codemap.map.hint")}</span>
    </div>
  );
}

/** A line from the right of the importing card to the left of the imported one. */
function curve(from: Point, to: Point): string {
  const sx = from.x + CARD_WIDTH;
  const sy = from.y + CARD_HEIGHT / 2;
  const tx = to.x;
  const ty = to.y + CARD_HEIGHT / 2;
  // Bent harder when the target sits behind the source, so a cycle's way back reads as one.
  const reach = tx > sx ? Math.max(40, (tx - sx) / 2) : 90;
  return `M ${sx} ${sy} C ${sx + reach} ${sy}, ${tx - reach} ${ty}, ${tx} ${ty}`;
}

/** Thicker for more file-to-file imports, slowly, so one heavy edge never drowns the rest. */
function edgeWidth(weight: number): number {
  return 1.2 + Math.min(2.8, Math.sqrt(Math.max(0, weight - 1)) * 0.6);
}

function MapCard({
  node,
  at,
  color,
  project,
  selected,
  dimmed,
  onClick,
  onDoubleClick,
  onKeyDown,
  onPointerEnter,
  onPointerLeave,
}: {
  node: RepoMapNode;
  at: Point;
  color: string | undefined;
  project: boolean;
  selected: boolean;
  dimmed: boolean;
  onClick: () => void;
  onDoubleClick: () => void;
  onKeyDown: (event: KeyboardEvent<HTMLButtonElement>) => void;
  onPointerEnter: () => void;
  onPointerLeave: () => void;
}) {
  const t = useT();
  const Icon = node.folder ? Folder : FileCode2;
  const meta = node.folder
    ? node.files === 1
      ? t("codemap.map.statsOne", { declarations: node.declarations })
      : t("codemap.map.stats", { files: node.files, declarations: node.declarations })
    : t("codemap.map.fileMeta", { language: node.language, lines: node.lines });
  const label = node.imported_by > 0 ? `${node.id} — ${meta} — ${t("codemap.map.importedBy", { count: node.imported_by })}` : `${node.id} — ${meta}`;
  return (
    <button
      type="button"
      aria-label={label}
      data-node={node.id}
      aria-pressed={selected}
      className={`cf-focusable pointer-events-auto absolute flex flex-col justify-center gap-0.5 rounded-lg border bg-[var(--cf-surface)] px-2.5 text-left transition-[opacity,border-color,box-shadow] duration-150 motion-reduce:transition-none ${
        selected
          ? "border-[var(--cf-accent)] shadow-[0_0_0_3px_color-mix(in_oklab,var(--cf-accent)_20%,transparent)]"
          : "border-[var(--cf-border)] hover:border-[var(--cf-text-muted)]"
      }`}
      style={{
        left: at.x,
        top: at.y,
        width: CARD_WIDTH,
        height: CARD_HEIGHT,
        opacity: dimmed ? 0.3 : 1,
        borderLeft: color ? `3px solid ${color}` : undefined,
      }}
      onClick={onClick}
      onDoubleClick={onDoubleClick}
      onKeyDown={onKeyDown}
      onPointerEnter={onPointerEnter}
      onPointerLeave={onPointerLeave}
    >
      {project && parentOf(node.id) !== "" && (
        <span className="truncate font-mono text-badge leading-none text-[var(--cf-text-muted)]">{parentOf(node.id)}</span>
      )}
      <span className="flex min-w-0 items-center gap-1.5">
        <Icon size={13} className={`shrink-0 ${node.folder ? "text-[var(--cf-accent)]" : "text-[var(--cf-text-muted)]"}`} aria-hidden />
        <span className="min-w-0 flex-1 truncate text-body font-medium text-[var(--cf-text)]">{node.label}</span>
        {node.imported_by > 0 && (
          <span className="flex shrink-0 items-center gap-0.5 text-badge text-[var(--cf-text-muted)]" aria-hidden>
            <Users size={11} />
            {node.imported_by}
          </span>
        )}
      </span>
      <span className="truncate text-badge text-[var(--cf-text-muted)]">{meta}</span>
    </button>
  );
}
