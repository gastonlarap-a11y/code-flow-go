import {
  forwardRef,
  useCallback,
  useEffect,
  useImperativeHandle,
  useMemo,
  useRef,
  useState,
  type KeyboardEvent,
  type MouseEvent,
  type PointerEvent,
} from "react";
import { KeyRound, MoreVertical, Pencil, StickyNote, Table2, TextSearch, Trash2, type LucideIcon } from "lucide-react";
import type { DbmlSchemaModel, DbmlTableModel } from "../../lib/dbml/model";
import { CARD_PADDING, HEADER_HEIGHT, ROW_HEIGHT, boundsOf, computeLayout, type Point, type Rect } from "../../lib/dbml/layout";
import { routeRelations, type RoutedRelation } from "../../lib/dbml/routing";
import { guessLanguage } from "../../lib/dbml/inflect";
import { describeRelation, renderSentence } from "../../lib/dbml/relationPhrase";
import { IDENTITY, fitBounds, overlayAt, zoomAt, type Viewport } from "../../lib/dbml/viewport";
import { inlineEdit } from "../../lib/dbml/cardEdit";
import { DRAG_THRESHOLD, setDragCursor } from "../../lib/pointerDrag";
import { menuKeyAction, type MenuItemState } from "../../lib/ui/menuNavigation";
import { useLanguageStore, useT } from "../../state/languageStore";
import type { TranslationKey } from "../../lib/i18n/translations";

/** What the toolbar outside the canvas can ask of it. */
export interface DbmlCanvasHandle {
  fit: () => void;
  zoomBy: (factor: number) => void;
}

/**
 * What a card can do to the document behind it (DBML-028).
 *
 * Passing this is what makes the canvas an editor; leaving it out is what keeps the Editor module's
 * quick look a picture. Every entry is an *intent*: the canvas knows which table was acted on, and
 * the owner of the buffer decides whether the edit is one the document can take.
 */
export interface DbmlCanvasEditing {
  renameTable: (tableKey: string, name: string) => void;
  renameColumn: (tableKey: string, column: string, name: string) => void;
  retypeColumn: (tableKey: string, column: string, type: string) => void;
  deleteTable: (tableKey: string) => void;
  /** Put the caret on this table in the source, wherever the source is shown. */
  revealTable: (tableKey: string) => void;
}

interface DbmlCanvasProps {
  model: DbmlSchemaModel;
  /** Changes when a different document is shown, so the canvas fits itself once for it. */
  documentKey: string;
  positions: Readonly<Record<string, Point>>;
  /** A table was dropped, or nudged from the keyboard. */
  onPlace: (tableKey: string, point: Point) => void;
  /** Absent for a read-only diagram. */
  editing?: DbmlCanvasEditing | undefined;
}

/** Arrow-key nudge, and with Shift held. */
const NUDGE = 16;
const NUDGE_FAST = 64;
/** One wheel notch of zoom. */
const WHEEL_ZOOM = 1.1;
/** Background dot spacing at scale 1. */
const GRID = 24;
/** Room around the cards for lines that run outside them. */
const SVG_MARGIN = 240;
/** How wide a line is to the pointer, as opposed to how wide it is drawn. */
const HIT_WIDTH = 14;
/**
 * How big the floating panels are taken to be when they are placed.
 *
 * The width is real — both are given it — and the height is an estimate, because a bubble is as
 * tall as the sentence in it and measuring it would mean rendering it somewhere first. It only
 * decides whether the panel flips above the pointer near the bottom edge, so an estimate that is
 * generous costs an early flip and never a panel off the screen.
 */
const TOOLTIP_PANEL = { width: 320, height: 120 };
const MENU_PANEL = { width: 210, height: 112 };

interface CardDrag {
  key: string;
  pointerId: number;
  start: Point;
  origin: Point;
  scale: number;
  moved: boolean;
}

interface Pan {
  pointerId: number;
  start: Point;
  origin: Viewport;
}

/**
 * What the canvas is explaining, and where on screen the bubble belongs.
 *
 * One state rather than two: a relationship and a note both want the pointer's attention, and
 * showing them at once would put two bubbles in the same place.
 */
type Tip =
  | { kind: "relation"; id: string; at: Point }
  | { kind: "note"; id: string; title: string; body: string; at: Point };

/** The cell a person is typing into. Nothing is being edited when this is null. */
type Edit =
  | { kind: "table"; tableKey: string }
  | { kind: "column"; tableKey: string; column: string }
  | { kind: "type"; tableKey: string; column: string };

interface CardMenu {
  tableKey: string;
  at: Point;
}

/** One entry of a card's menu. It extends `MenuItemState` so `menuKeyAction` can navigate it. */
interface CardAction extends MenuItemState {
  id: string;
  labelKey: TranslationKey;
  icon: LucideIcon;
  /** Rendered in the danger colour: it removes something. */
  danger?: boolean;
  run: () => void;
}

/**
 * The schema diagram: cards placed by `computeLayout`, orthogonal relationship lines that explain
 * themselves on hover or focus, pan and zoom, tables a person can drag — and, when `editing` is
 * given, tables a person can edit in place (DBML-008, DBML-013, DBML-028, DBML-029).
 *
 * While a card is being dragged only that card moves; the layout is recomputed once, on drop. Doing
 * it on every pointer move would let the push-down rule shove the other cards around under the
 * cursor, which reads as the diagram fighting the user.
 */
export const DbmlCanvas = forwardRef<DbmlCanvasHandle, DbmlCanvasProps>(function DbmlCanvas(
  { model, documentKey, positions, onPlace, editing },
  ref,
) {
  const t = useT();
  const interfaceLanguage = useLanguageStore((s) => s.language);
  const containerRef = useRef<HTMLDivElement>(null);
  const [view, setView] = useState<Viewport>(IDENTITY);
  const [size, setSize] = useState({ width: 0, height: 0 });
  const [dragPoint, setDragPoint] = useState<{ key: string; point: Point } | null>(null);
  const [tip, setTip] = useState<Tip | null>(null);
  const [menu, setMenu] = useState<CardMenu | null>(null);
  const [edit, setEdit] = useState<Edit | null>(null);
  const cardDrag = useRef<CardDrag | null>(null);
  const pan = useRef<Pan | null>(null);

  const tablesByKey = useMemo(() => new Map(model.tables.map((table) => [table.key, table])), [model]);
  const refsById = useMemo(() => new Map(model.refs.map((r) => [r.id, r])), [model.refs]);
  const layout = useMemo(() => computeLayout(model, new Map(Object.entries(positions))), [model, positions]);

  const drawn = useMemo(() => {
    if (dragPoint === null) return layout;
    const rect = layout.get(dragPoint.key);
    if (!rect) return layout;
    return new Map(layout).set(dragPoint.key, { ...rect, ...dragPoint.point });
  }, [layout, dragPoint]);

  const routes = useMemo(() => routeRelations(model.refs, tablesByKey, drawn), [model.refs, tablesByKey, drawn]);

  const svgBox = useMemo(() => {
    const bounds = boundsOf(drawn.values());
    if (!bounds) return null;
    return {
      x: bounds.x - SVG_MARGIN,
      y: bounds.y - SVG_MARGIN,
      width: bounds.width + SVG_MARGIN * 2,
      height: bounds.height + SVG_MARGIN * 2,
    };
  }, [drawn]);

  // Nouns follow the language the schema is *named* in; the sentence around them follows the
  // interface. Column names join the table names because they carry most of the signal.
  const namesLanguage = useMemo(
    () =>
      guessLanguage(
        model.tables.flatMap((table) => [table.name, ...table.columns.map((column) => column.name)]),
        interfaceLanguage === "es" ? "es" : "en",
      ),
    [model, interfaceLanguage],
  );

  // Every line's two sentences: shown in the tooltip, and read by a screen reader from the line itself.
  const sentencesById = useMemo(
    () =>
      new Map(
        model.refs.map((r) => {
          const description = describeRelation(r, tablesByKey, namesLanguage);
          return [r.id, description ? description.sentences.map((s) => renderSentence(s, t)) : []] as const;
        }),
      ),
    [model.refs, tablesByKey, namesLanguage, t],
  );

  const fit = useCallback(() => {
    const container = containerRef.current;
    if (!container) return;
    const { width, height } = container.getBoundingClientRect();
    setView(fitBounds(boundsOf(layout.values()), { width, height }));
  }, [layout]);

  const zoomBy = useCallback((factor: number) => {
    const container = containerRef.current;
    if (!container) return;
    const { width, height } = container.getBoundingClientRect();
    setView((current) => zoomAt(current, { x: width / 2, y: height / 2 }, factor));
  }, []);

  useImperativeHandle(ref, () => ({ fit, zoomBy }), [fit, zoomBy]);

  // Fit once per document, as soon as it has something to show — not on every keystroke, which
  // would yank the view around while the user types.
  const fittedFor = useRef<string | null>(null);
  useEffect(() => {
    if (layout.size === 0 || fittedFor.current === documentKey) return;
    fittedFor.current = documentKey;
    fit();
  }, [documentKey, layout.size, fit]);

  // The canvas size, for keeping the tooltip inside it.
  useEffect(() => {
    const container = containerRef.current;
    if (!container) return;
    const observer = new ResizeObserver(([entry]) => {
      if (entry) setSize({ width: entry.contentRect.width, height: entry.contentRect.height });
    });
    observer.observe(container);
    return () => observer.disconnect();
  }, []);

  // A native, non-passive listener: React registers `onWheel` as passive, so `preventDefault` there
  // cannot stop Ctrl+wheel from zooming the whole window instead of the diagram.
  useEffect(() => {
    const container = containerRef.current;
    if (!container) return;
    const onWheel = (event: WheelEvent) => {
      event.preventDefault();
      setMenu(null);
      const box = container.getBoundingClientRect();
      if (event.ctrlKey || event.metaKey) {
        const factor = event.deltaY < 0 ? WHEEL_ZOOM : 1 / WHEEL_ZOOM;
        setView((current) => zoomAt(current, { x: event.clientX - box.left, y: event.clientY - box.top }, factor));
        return;
      }
      // A trackpad's two-finger scroll pans, the way every canvas tool behaves.
      setView((current) => ({ ...current, x: current.x - event.deltaX, y: current.y - event.deltaY }));
    };
    container.addEventListener("wheel", onWheel, { passive: false });
    return () => container.removeEventListener("wheel", onWheel);
  }, []);

  // A table edited out of the document from the source pane leaves its card's menu or input
  // pointing at nothing, so both close with it.
  useEffect(() => {
    if (menu !== null && !tablesByKey.has(menu.tableKey)) setMenu(null);
    if (edit !== null && !tablesByKey.has(edit.tableKey)) setEdit(null);
  }, [tablesByKey, menu, edit]);

  /** Where an event happened, in the canvas's own coordinates. */
  const pointIn = (event: { clientX: number; clientY: number }): Point | null => {
    const box = containerRef.current?.getBoundingClientRect();
    if (!box) return null;
    return { x: event.clientX - box.left, y: event.clientY - box.top };
  };

  // ---------- panning the background ----------

  const onBackgroundPointerDown = (event: PointerEvent<HTMLDivElement>) => {
    if (event.button !== 0 || event.target !== event.currentTarget) return;
    event.currentTarget.setPointerCapture(event.pointerId);
    pan.current = { pointerId: event.pointerId, start: { x: event.clientX, y: event.clientY }, origin: view };
    setTip(null);
    setMenu(null);
    setDragCursor(true);
  };

  const onBackgroundPointerMove = (event: PointerEvent<HTMLDivElement>) => {
    const current = pan.current;
    if (!current || current.pointerId !== event.pointerId) return;
    setView({
      ...current.origin,
      x: current.origin.x + event.clientX - current.start.x,
      y: current.origin.y + event.clientY - current.start.y,
    });
  };

  const endPan = (event: PointerEvent<HTMLDivElement>) => {
    if (pan.current?.pointerId !== event.pointerId) return;
    pan.current = null;
    setDragCursor(false);
  };

  // ---------- dragging a card ----------

  const onCardPointerDown = (key: string, rect: Rect) => (event: PointerEvent<HTMLElement>) => {
    if (event.button !== 0) return;
    event.stopPropagation();
    setMenu(null);
    event.currentTarget.setPointerCapture(event.pointerId);
    cardDrag.current = {
      key,
      pointerId: event.pointerId,
      start: { x: event.clientX, y: event.clientY },
      origin: { x: rect.x, y: rect.y },
      scale: view.scale,
      moved: false,
    };
  };

  const onCardPointerMove = (event: PointerEvent<HTMLElement>) => {
    const drag = cardDrag.current;
    if (!drag || drag.pointerId !== event.pointerId) return;
    const dx = event.clientX - drag.start.x;
    const dy = event.clientY - drag.start.y;
    // Below the threshold a press is still a click (focus), not a move.
    if (!drag.moved && Math.hypot(dx, dy) < DRAG_THRESHOLD) return;
    if (!drag.moved) {
      drag.moved = true;
      setTip(null);
      setDragCursor(true);
    }
    setDragPoint({ key: drag.key, point: { x: drag.origin.x + dx / drag.scale, y: drag.origin.y + dy / drag.scale } });
  };

  const onCardPointerUp = (event: PointerEvent<HTMLElement>) => {
    const drag = cardDrag.current;
    if (!drag || drag.pointerId !== event.pointerId) return;
    cardDrag.current = null;
    if (!drag.moved) return;
    setDragCursor(false);
    const dx = event.clientX - drag.start.x;
    const dy = event.clientY - drag.start.y;
    setDragPoint(null);
    onPlace(drag.key, { x: drag.origin.x + dx / drag.scale, y: drag.origin.y + dy / drag.scale });
  };

  const onCardKeyDown = (key: string, rect: Rect) => (event: KeyboardEvent<HTMLElement>) => {
    // The keyboard route to what a drag does: a pointer-only control cannot be reached by keyboard.
    const step = event.shiftKey ? NUDGE_FAST : NUDGE;
    const delta: Record<string, Point> = {
      ArrowLeft: { x: -step, y: 0 },
      ArrowRight: { x: step, y: 0 },
      ArrowUp: { x: 0, y: -step },
      ArrowDown: { x: 0, y: step },
    };
    const move = delta[event.key];
    if (!move) return;
    event.preventDefault();
    onPlace(key, { x: rect.x + move.x, y: rect.y + move.y });
  };

  // ---------- explaining a relationship, and showing a note ----------

  const busy = () => cardDrag.current?.moved === true || pan.current !== null;

  const showRelationAtPointer = (id: string) => (event: PointerEvent<SVGPathElement>) => {
    // Lines sweep under the pointer while a card is dragged; explaining each one would be noise.
    if (busy()) return;
    const at = pointIn(event);
    if (at) setTip({ kind: "relation", id, at });
  };

  const showRelationAtLabel = (route: RoutedRelation) => () => {
    // Keyboard focus has no pointer position, so the tooltip goes to the middle of the line's lane.
    setTip({
      kind: "relation",
      id: route.id,
      at: { x: route.label.x * view.scale + view.x, y: route.label.y * view.scale + view.y },
    });
  };

  /**
   * A note, shown where the note is.
   *
   * Placed once, on entry, rather than followed: a row is a few pixels tall and a bubble that
   * chased the pointer across it would jitter for no gain. The relationship tooltip follows because
   * a line can cross the whole diagram.
   */
  const showNote = (id: string, title: string, body: string) => (event: PointerEvent<HTMLElement>) => {
    if (busy() || body.length === 0) return;
    const at = pointIn(event);
    if (at) setTip({ kind: "note", id, title, body, at });
  };

  const showNoteAtCard = (id: string, title: string, body: string, rect: Rect, row: number) => () => {
    setTip({
      kind: "note",
      id,
      title,
      body,
      at: { x: (rect.x + rect.width) * view.scale + view.x, y: (rect.y + row) * view.scale + view.y },
    });
  };

  const hide = (id: string) => () => setTip((current) => (current?.id === id ? null : current));

  // ---------- editing a card ----------

  const openMenu = (tableKey: string) => (event: MouseEvent<HTMLElement>) => {
    if (!editing) return;
    // The browser's own menu has nothing to offer over a diagram, and this replaces it.
    event.preventDefault();
    event.stopPropagation();
    const at = pointIn(event);
    if (!at) return;
    setTip(null);
    setEdit(null);
    setMenu({ tableKey, at });
  };

  const startEdit = (next: Edit) => () => {
    if (!editing) return;
    setTip(null);
    setMenu(null);
    setEdit(next);
  };

  const menuActions = useMemo<CardAction[]>(() => {
    if (!editing || menu === null) return [];
    const { tableKey } = menu;
    return [
      { id: "reveal", labelKey: "dbml.table.reveal", icon: TextSearch, run: () => editing.revealTable(tableKey) },
      { id: "rename", labelKey: "dbml.table.rename", icon: Pencil, run: () => setEdit({ kind: "table", tableKey }) },
      {
        id: "delete",
        labelKey: "dbml.table.delete",
        icon: Trash2,
        danger: true,
        run: () => editing.deleteTable(tableKey),
      },
    ];
  }, [editing, menu]);

  const menuRef = useRef<HTMLDivElement>(null);
  const [menuIndex, setMenuIndex] = useState(-1);

  // Focus follows the active item so a screen reader announces each one as it is reached, and so
  // Escape has somewhere to come back from. Opening the menu focuses its first action.
  useEffect(() => {
    if (menu === null) {
      setMenuIndex(-1);
      return;
    }
    setMenuIndex(0);
  }, [menu]);

  useEffect(() => {
    if (menu === null || menuIndex < 0) return;
    menuRef.current?.querySelectorAll<HTMLElement>('[role="menuitem"]')[menuIndex]?.focus();
  }, [menu, menuIndex]);

  const onMenuKeyDown = (event: KeyboardEvent<HTMLDivElement>) => {
    const action = menuKeyAction(event.key, menuActions, menuIndex);
    if (action.kind === "none") return;
    event.preventDefault();
    event.stopPropagation();
    if (action.kind === "close") return setMenu(null);
    if (action.kind === "move") return setMenuIndex(action.index);
    menuActions[action.index]?.run();
    setMenu(null);
  };

  const onCanvasKeyDown = (event: KeyboardEvent<HTMLDivElement>) => {
    if (event.key !== "Escape") return;
    if (menu === null && edit === null) return;
    event.stopPropagation();
    setMenu(null);
    setEdit(null);
  };

  // A reference edited away while its tooltip was open simply has nothing to show.
  const activeRef = tip?.kind === "relation" ? refsById.get(tip.id) : undefined;
  const activeSentences = tip?.kind === "relation" ? (sentencesById.get(tip.id) ?? []) : [];
  const highlighted = activeRef ? new Set([activeRef.from.tableKey, activeRef.to.tableKey]) : null;

  const tipPosition = tip ? overlayAt(tip.at, size, TOOLTIP_PANEL) : null;
  const menuPosition = menu ? overlayAt(menu.at, size, MENU_PANEL) : null;

  const gridSize = GRID * view.scale;

  return (
    <div
      ref={containerRef}
      className="relative h-full w-full touch-none overflow-hidden"
      style={{
        backgroundImage: "radial-gradient(circle, var(--cf-border) 1px, transparent 1px)",
        backgroundSize: `${gridSize}px ${gridSize}px`,
        backgroundPosition: `${view.x}px ${view.y}px`,
      }}
      onPointerDown={onBackgroundPointerDown}
      onPointerMove={onBackgroundPointerMove}
      onPointerUp={endPan}
      onPointerCancel={endPan}
      onKeyDown={onCanvasKeyDown}
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
          >
            <g transform={`translate(${-svgBox.x} ${-svgBox.y})`}>
              {routes.map((route) => {
                const isActive = tip?.kind === "relation" && tip.id === route.id;
                const dimmed = tip?.kind === "relation" && !isActive;
                const r = refsById.get(route.id);
                const label = r
                  ? `${r.from.table}.${r.from.columns.join(", ")} → ${r.to.table}.${r.to.columns.join(", ")}. ${(sentencesById.get(route.id) ?? []).join(". ")}`
                  : route.id;
                return (
                  <g
                    key={route.id}
                    className="transition-opacity duration-150 motion-reduce:transition-none"
                    style={{ opacity: dimmed ? 0.25 : 1 }}
                  >
                    <path
                      d={route.d}
                      fill="none"
                      stroke="var(--cf-accent)"
                      strokeOpacity={isActive ? 1 : 0.8}
                      strokeWidth={isActive ? 2.5 : 1.5}
                    />
                    {route.markers.map((marker, i) => (
                      <path
                        key={i}
                        d={marker}
                        fill="none"
                        stroke="var(--cf-accent)"
                        strokeWidth={isActive ? 2 : 1.5}
                        strokeLinecap="round"
                      />
                    ))}
                    {/* The target the pointer and the keyboard reach: invisible, and wider than the line. */}
                    <path
                      d={route.d}
                      fill="none"
                      stroke="transparent"
                      strokeWidth={HIT_WIDTH}
                      tabIndex={0}
                      role="img"
                      aria-label={label}
                      data-relation={route.id}
                      style={{ pointerEvents: "stroke", cursor: "help", outline: "none" }}
                      onPointerEnter={showRelationAtPointer(route.id)}
                      onPointerMove={showRelationAtPointer(route.id)}
                      onPointerLeave={hide(route.id)}
                      onFocus={showRelationAtLabel(route)}
                      onBlur={hide(route.id)}
                    />
                  </g>
                );
              })}
            </g>
          </svg>
        )}

        {model.tables.map((table) => {
          const rect = drawn.get(table.key);
          if (!rect) return null;
          const isRelated = highlighted?.has(table.key) ?? false;
          return (
            <div
              key={table.key}
              className={`pointer-events-auto absolute overflow-hidden rounded-lg border bg-[var(--cf-surface-raised)] shadow-sm ${
                isRelated ? "border-[var(--cf-accent)]" : "border-[var(--cf-border)]"
              }`}
              style={{ left: rect.x, top: rect.y, width: rect.width, height: rect.height }}
              onContextMenu={openMenu(table.key)}
            >
              <TableHeader
                table={table}
                editing={editing}
                renaming={edit?.kind === "table" && edit.tableKey === table.key}
                onStartRename={startEdit({ kind: "table", tableKey: table.key })}
                onEndRename={() => setEdit(null)}
                onOpenMenu={openMenu(table.key)}
                onPointerDown={onCardPointerDown(table.key, rect)}
                onPointerMove={onCardPointerMove}
                onPointerUp={onCardPointerUp}
                onKeyDown={onCardKeyDown(table.key, rect)}
                onShowNote={showNote(`${table.key}:`, table.name, table.note)}
                onShowNoteFromKeyboard={showNoteAtCard(`${table.key}:`, table.name, table.note, rect, 0)}
                onHideNote={hide(`${table.key}:`)}
              />

              <div style={{ paddingBottom: CARD_PADDING }}>
                {table.columns.map((column, index) => {
                  const noteId = `${table.key}:${column.name}`;
                  const editable = editing !== undefined;
                  return (
                    <div
                      key={column.name}
                      className="flex items-center justify-between gap-2 px-2.5 text-badge"
                      style={{ height: ROW_HEIGHT }}
                      onPointerEnter={showNote(noteId, `${table.name}.${column.name}`, column.note)}
                      onPointerLeave={hide(noteId)}
                    >
                      {edit?.kind === "column" && edit.tableKey === table.key && edit.column === column.name ? (
                        <InlineEdit
                          value={column.name}
                          label={t("dbml.column.rename")}
                          onCommit={(next) => editing?.renameColumn(table.key, column.name, next)}
                          onDone={() => setEdit(null)}
                        />
                      ) : (
                        <span
                          className={`flex min-w-0 items-center gap-1 font-mono text-[var(--cf-text)] ${
                            editable ? "cursor-text rounded-[3px] hover:bg-[var(--cf-accent-soft)]" : ""
                          }`}
                          onDoubleClick={startEdit({ kind: "column", tableKey: table.key, column: column.name })}
                        >
                          {column.pk && <KeyRound size={12} className="shrink-0 text-[var(--cf-warning)]" />}
                          <span className="truncate">{column.name}</span>
                          {column.notNull && <span className="shrink-0 text-[var(--cf-text-muted)]">*</span>}
                          {column.note.length > 0 && (
                            <NoteMark
                              label={`${t("dbml.note")}: ${column.note}`}
                              onFocus={showNoteAtCard(
                                noteId,
                                `${table.name}.${column.name}`,
                                column.note,
                                rect,
                                HEADER_HEIGHT + index * ROW_HEIGHT + ROW_HEIGHT / 2,
                              )}
                              onBlur={hide(noteId)}
                            />
                          )}
                        </span>
                      )}

                      {edit?.kind === "type" && edit.tableKey === table.key && edit.column === column.name ? (
                        <InlineEdit
                          value={column.type}
                          label={t("dbml.column.retype")}
                          onCommit={(next) => editing?.retypeColumn(table.key, column.name, next)}
                          onDone={() => setEdit(null)}
                        />
                      ) : (
                        <span
                          className={`shrink-0 truncate font-mono text-[var(--cf-text-muted)] ${
                            editable ? "cursor-text rounded-[3px] hover:bg-[var(--cf-accent-soft)]" : ""
                          }`}
                          onDoubleClick={startEdit({ kind: "type", tableKey: table.key, column: column.name })}
                        >
                          {column.type}
                        </span>
                      )}
                    </div>
                  );
                })}
              </div>
            </div>
          );
        })}
      </div>

      {tip?.kind === "relation" && activeRef && tipPosition && (
        <div
          role="tooltip"
          className="pointer-events-none absolute rounded-control border border-[var(--cf-border)] bg-[var(--cf-surface)] px-3 py-2 shadow-[var(--cf-shadow)]"
          style={{ ...tipPosition, maxWidth: TOOLTIP_PANEL.width }}
        >
          <p className="mb-1 font-mono text-badge text-[var(--cf-text-muted)]">
            {activeRef.from.table}.{activeRef.from.columns.join(", ")} → {activeRef.to.table}.
            {activeRef.to.columns.join(", ")}
          </p>
          {activeSentences.map((sentence) => (
            <p key={sentence} className="text-ui text-[var(--cf-text)]">
              {sentence}
            </p>
          ))}
          {activeRef.onDelete && (
            <p className="mt-1 text-badge text-[var(--cf-text-muted)]">
              {t("dbml.relation.onDelete", { action: activeRef.onDelete })}
            </p>
          )}
          {activeRef.onUpdate && (
            <p className="text-badge text-[var(--cf-text-muted)]">
              {t("dbml.relation.onUpdate", { action: activeRef.onUpdate })}
            </p>
          )}
        </div>
      )}

      {tip?.kind === "note" && tipPosition && (
        <div
          role="tooltip"
          className="pointer-events-none absolute rounded-control border border-[var(--cf-border)] bg-[var(--cf-surface)] px-3 py-2 shadow-[var(--cf-shadow)]"
          style={{ ...tipPosition, maxWidth: TOOLTIP_PANEL.width }}
        >
          <p className="mb-1 font-mono text-badge text-[var(--cf-text-muted)]">{tip.title}</p>
          {/* `pre-line` because a note written as a `'''…'''` block carries its own line breaks. */}
          <p className="whitespace-pre-line text-ui text-[var(--cf-text)]">{tip.body}</p>
        </div>
      )}

      {menu && menuPosition && (
        <div
          ref={menuRef}
          role="menu"
          aria-label={t("dbml.table.menu")}
          className="absolute z-10 rounded-control border border-[var(--cf-border)] bg-[var(--cf-surface-raised)] p-1 shadow-[var(--cf-shadow)]"
          style={{ ...menuPosition, width: MENU_PANEL.width }}
          onKeyDown={onMenuKeyDown}
          onPointerDown={(event) => event.stopPropagation()}
          onBlur={(event) => {
            // Closes when focus leaves the menu entirely — a click on the diagram, or Tab out.
            if (!event.currentTarget.contains(event.relatedTarget)) setMenu(null);
          }}
        >
          {menuActions.map((action, index) => (
            <button
              key={action.id}
              type="button"
              role="menuitem"
              tabIndex={-1}
              onClick={() => {
                setMenu(null);
                action.run();
              }}
              onPointerEnter={() => setMenuIndex(index)}
              className={`cf-focusable cf-interactive flex h-7 w-full items-center gap-2 rounded-[4px] px-2 text-left text-ui hover:bg-[color-mix(in_oklab,currentColor_calc(var(--cf-overlay-hover)*100%),transparent)] ${
                action.danger ? "text-[var(--cf-danger)]" : "text-[var(--cf-text)]"
              }`}
            >
              <action.icon size={14} className="shrink-0" aria-hidden />
              {t(action.labelKey)}
            </button>
          ))}
        </div>
      )}
    </div>
  );
});

/**
 * The card's title bar: the drag handle, the table's note, and the way into its menu.
 *
 * It swaps from a `<button>` to a plain row while the name is being edited, because an `<input>`
 * inside a button is neither valid HTML nor reachable — the button swallows the click.
 */
function TableHeader({
  table,
  editing,
  renaming,
  onStartRename,
  onEndRename,
  onOpenMenu,
  onPointerDown,
  onPointerMove,
  onPointerUp,
  onKeyDown,
  onShowNote,
  onShowNoteFromKeyboard,
  onHideNote,
}: {
  table: DbmlTableModel;
  editing: DbmlCanvasEditing | undefined;
  renaming: boolean;
  onStartRename: () => void;
  onEndRename: () => void;
  onOpenMenu: (event: MouseEvent<HTMLElement>) => void;
  onPointerDown: (event: PointerEvent<HTMLElement>) => void;
  onPointerMove: (event: PointerEvent<HTMLElement>) => void;
  onPointerUp: (event: PointerEvent<HTMLElement>) => void;
  onKeyDown: (event: KeyboardEvent<HTMLElement>) => void;
  onShowNote: (event: PointerEvent<HTMLElement>) => void;
  onShowNoteFromKeyboard: () => void;
  onHideNote: () => void;
}) {
  const t = useT();
  const chrome = "flex w-full items-center gap-1.5 border-b border-[var(--cf-border)] bg-[var(--cf-accent-soft)] px-2.5 text-left";

  if (renaming) {
    return (
      <div className={chrome} style={{ height: HEADER_HEIGHT }}>
        <Table2 size={14} className="shrink-0 text-[var(--cf-accent)]" />
        <InlineEdit
          value={table.name}
          label={t("dbml.table.rename")}
          onCommit={(next) => editing?.renameTable(table.key, next)}
          onDone={onEndRename}
        />
      </div>
    );
  }

  return (
    // The whole bar drags, so a press on the padding between the title and the badge moves the card
    // like a press on the title does. The button inside it is what the keyboard reaches.
    <div
      className={`${chrome} cursor-grab active:cursor-grabbing`}
      style={{ height: HEADER_HEIGHT }}
      onPointerDown={onPointerDown}
      onPointerMove={onPointerMove}
      onPointerUp={onPointerUp}
      onPointerCancel={onPointerUp}
    >
      <button
        type="button"
        aria-label={`${t("dbml.moveTable")}: ${table.name}`}
        className="cf-focusable flex min-w-0 flex-1 items-center gap-1.5 self-stretch text-left"
        onKeyDown={onKeyDown}
        onPointerEnter={onShowNote}
        onPointerLeave={onHideNote}
        onDoubleClick={onStartRename}
      >
        <Table2 size={14} className="shrink-0 text-[var(--cf-accent)]" />
        <span className="truncate text-ui font-semibold text-[var(--cf-text)]">{table.name}</span>
        {table.note.length > 0 && (
          <NoteMark
            label={`${t("dbml.note")}: ${table.note}`}
            onFocus={onShowNoteFromKeyboard}
            onBlur={onHideNote}
          />
        )}
      </button>

      {table.schema !== "public" && (
        <span className="shrink-0 text-badge text-[var(--cf-text-muted)]">{table.schema}</span>
      )}

      {editing && (
        <button
          type="button"
          aria-label={`${t("dbml.table.menu")}: ${table.name}`}
          aria-haspopup="menu"
          className="cf-focusable shrink-0 rounded-[4px] p-0.5 text-[var(--cf-text-muted)] hover:bg-[color-mix(in_oklab,currentColor_calc(var(--cf-overlay-hover)*100%),transparent)] hover:text-[var(--cf-text)]"
          // A left click here opens the same menu the right click does: a click is what everyone
          // tries first, and the card's own left button is taken by the drag.
          onClick={onOpenMenu}
          onPointerDown={(event) => event.stopPropagation()}
        >
          <MoreVertical size={14} aria-hidden />
        </button>
      )}
    </div>
  );
}

/**
 * The mark that says "there is a note here".
 *
 * Focusable and labelled with the note itself, for the same reason every relationship line is: a
 * thing you can only learn by pointing at it is a thing a keyboard user never learns. It is not a
 * `<button>` — there is nothing to press — which is the same shape the lines use.
 */
function NoteMark({
  label,
  onFocus,
  onBlur,
}: {
  label: string;
  onFocus: () => void;
  onBlur: () => void;
}) {
  return (
    <StickyNote
      size={11}
      role="img"
      tabIndex={0}
      aria-label={label}
      className="shrink-0 cursor-help text-[var(--cf-text-muted)] outline-none"
      onFocus={onFocus}
      onBlur={onBlur}
    />
  );
}

/**
 * One cell, being typed into.
 *
 * Enter and blur both commit, Escape abandons — and `done` is what keeps Enter from committing
 * twice, since the commit moves focus away and that blur would arrive right behind it.
 */
function InlineEdit({
  value,
  label,
  onCommit,
  onDone,
}: {
  value: string;
  label: string;
  onCommit: (next: string) => void;
  onDone: () => void;
}) {
  const [draft, setDraft] = useState(value);
  const input = useRef<HTMLInputElement>(null);
  const done = useRef(false);

  // Selected, not just focused: the common edit is replacing the name, not appending to it.
  useEffect(() => {
    input.current?.select();
  }, []);

  const finish = (commit: boolean) => {
    if (done.current) return;
    done.current = true;
    const outcome = inlineEdit(draft, value);
    if (commit && outcome.kind === "commit") onCommit(outcome.value);
    onDone();
  };

  return (
    <input
      ref={input}
      autoFocus
      value={draft}
      aria-label={label}
      spellCheck={false}
      onChange={(event) => setDraft(event.target.value)}
      onBlur={() => finish(true)}
      // The card is listening for arrow keys to nudge itself, and for Escape to close things.
      onKeyDown={(event) => {
        event.stopPropagation();
        if (event.key === "Enter") finish(true);
        if (event.key === "Escape") finish(false);
      }}
      onPointerDown={(event) => event.stopPropagation()}
      onDoubleClick={(event) => event.stopPropagation()}
      className="min-w-0 flex-1 rounded-[3px] border border-[var(--cf-accent)] bg-[var(--cf-bg)] px-1 py-0 font-mono text-badge text-[var(--cf-text)] outline-none"
    />
  );
}
