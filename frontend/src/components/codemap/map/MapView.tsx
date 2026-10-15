import { useEffect, useMemo, useState, type KeyboardEvent } from "react";
import { Boxes, ChevronRight, FolderTree, Info, Loader2, Network, RefreshCw, Search, Sparkles, TriangleAlert } from "lucide-react";
import { BouncingDots } from "../../common/BouncingDots";
import { EmptyState } from "../../common/EmptyState";
import { IconButton } from "../../common/IconButton";
import { Tabs, type TabOption } from "../../common/Tabs";
import { Tooltip } from "../../common/Tooltip";
import * as api from "../../../lib/ipc/commands";
import { saveArchifyDocument, showArchifyDocument } from "../../../lib/archify/save";
import { FOLDER_CARDS, drawnFirst, groupColors } from "../../../lib/repomap/labels";
import { useArchifyDrawStore } from "../../../state/archifyDrawStore";
import { useRepoMapStore, type MapMode } from "../../../state/repomapStore";
import { useLanguageStore, useT } from "../../../state/languageStore";
import { pushErrorToast, useToastStore } from "../../../state/toastStore";
import { useUiStore } from "../../../state/uiStore";
import type { RepoMapNode } from "../../../types/domain";
import { AiDiagramDialog } from "./AiDiagramDialog";
import { MapCanvas } from "./MapCanvas";
import { MapPanel } from "./MapPanel";

const MODES: readonly TabOption<MapMode>[] = [
  { id: "project", labelKey: "codemap.map.modeProject", icon: Network },
  { id: "folders", labelKey: "codemap.map.modeFolders", icon: FolderTree },
];

/** How long typing has to pause before the panel searches; Enter searches at once. */
const SEARCH_DEBOUNCE_MS = 200;
/** A search starts at this many characters, so a single letter does not list half the repository. */
const SEARCH_MIN_CHARS = 2;

/**
 * The code map's drawn view (19-repo-map.md): the repository as cards joined by their imports, with
 * a panel for what each card declares and who uses it.
 */
export function MapView({ rootPath, projectName }: { rootPath: string; projectName: string }) {
  const t = useT();
  const mode = useRepoMapStore((s) => s.mode);
  const focus = useRepoMapStore((s) => s.focus);
  const view = useRepoMapStore((s) => s.view);
  const viewMode = useRepoMapStore((s) => s.viewMode);
  const loading = useRepoMapStore((s) => s.loading);
  const error = useRepoMapStore((s) => s.error);
  const selected = useRepoMapStore((s) => s.selected);
  const open = useRepoMapStore((s) => s.open);
  const refresh = useRepoMapStore((s) => s.refresh);
  const setMode = useRepoMapStore((s) => s.setMode);
  const setFocus = useRepoMapStore((s) => s.setFocus);
  const select = useRepoMapStore((s) => s.select);
  const showPanel = useRepoMapStore((s) => s.showPanel);
  const openInEditor = useUiStore((s) => s.openInEditor);
  const shownRoot = useRepoMapStore((s) => s.rootPath);

  useEffect(() => {
    void open(rootPath);
  }, [rootPath, open]);

  const [text, setText] = useState("");
  useEffect(() => {
    const query = text.trim();
    if (query.length < SEARCH_MIN_CHARS) return;
    const timer = setTimeout(() => showPanel({ kind: "search", query }), SEARCH_DEBOUNCE_MS);
    return () => clearTimeout(timer);
  }, [text, showPanel]);
  const onSearchKey = (event: KeyboardEvent<HTMLInputElement>) => {
    if (event.key === "Enter" && text.trim() !== "") showPanel({ kind: "search", query: text.trim() });
    if (event.key === "Escape") {
      setText("");
      showPanel({ kind: "home" });
    }
  };

  // A view of the repository the user just left is never drawn for the one they opened. The view
  // is drawn as what it was loaded for: a mode just switched waits for its own view, so the canvas
  // fits once, to the cards it ends up drawing.
  const current = shownRoot === rootPath ? view : null;
  const nodes = useMemo(() => current?.graph.nodes ?? [], [current]);
  const { drawn, hidden } = useMemo(
    () => (viewMode === "folders" ? drawnFirst(nodes, FOLDER_CARDS) : { drawn: nodes, hidden: [] }),
    [viewMode, nodes],
  );
  const colors = useMemo(() => (viewMode === "project" ? groupColors(nodes) : new Map<string, string>()), [viewMode, nodes]);
  const selectedNode = nodes.find((n) => n.id === selected) ?? null;

  const onSelect = (id: string | null) => {
    select(id);
    const node = nodes.find((n) => n.id === id);
    showPanel(node && !node.folder ? { kind: "file", path: node.id, line: null } : { kind: "home" });
  };
  const onOpen = (node: RepoMapNode) => (node.folder ? void setFocus(node.id) : openInEditor(node.id, 1));

  const crumbs = mode === "folders" && focus !== "" ? focus.split("/") : [];

  // What a class diagram is drawn of: the picked card, else the open folder, else the repository.
  const language = useLanguageStore((s) => s.language);
  const [drawingClasses, setDrawingClasses] = useState(false);
  const classScope = selectedNode?.id ?? (viewMode === "folders" ? (current?.graph.focus ?? "") : "");
  const drawClasses = async () => {
    setDrawingClasses(true);
    try {
      const diagram = await api.archifyClassDiagram(rootPath, classScope, language);
      const name = classScope === "" ? projectName : classScope.slice(classScope.lastIndexOf("/") + 1);
      const path = await saveArchifyDocument(rootPath, name, "class", diagram.document);
      useToastStore.getState().pushToast(t("codemap.map.classDiagramSaved", { path }), "success");
      showArchifyDocument(path);
    } catch (e) {
      pushErrorToast(e instanceof Error ? e.message : String(e));
    } finally {
      setDrawingClasses(false);
    }
  };

  // A diagram the AI is drawing of this repository; its dialog can be closed and reopened meanwhile.
  const [aiOpen, setAiOpen] = useState(false);
  const aiRunning = useArchifyDrawStore((s) => s.runs[rootPath] !== undefined);

  return (
    <div className="flex min-h-0 flex-1 flex-col">
      <div className="flex h-9 shrink-0 items-center gap-2 border-b border-[var(--cf-border)] px-2">
        <Tabs options={MODES} activeId={mode} onSelect={(m) => void setMode(m)} layoutId="repomap-mode" label={t("codemap.map.modeLabel")} />
        {mode === "folders" && (
          <nav className="flex min-w-0 items-center gap-0.5 overflow-hidden text-ui" aria-label={t("codemap.map.root")}>
            <button type="button" className="cf-focusable shrink-0 rounded px-1 text-[var(--cf-text-muted)] hover:text-[var(--cf-text)]" onClick={() => void setFocus("")}>
              {projectName}
            </button>
            {crumbs.map((part, i) => (
              <span key={crumbs.slice(0, i + 1).join("/")} className="flex min-w-0 items-center gap-0.5">
                <ChevronRight size={12} className="shrink-0 text-[var(--cf-text-muted)]" aria-hidden />
                <button
                  type="button"
                  className={`cf-focusable truncate rounded px-1 ${i === crumbs.length - 1 ? "text-[var(--cf-text)]" : "text-[var(--cf-text-muted)] hover:text-[var(--cf-text)]"}`}
                  onClick={() => void setFocus(crumbs.slice(0, i + 1).join("/"))}
                >
                  {part}
                </button>
              </span>
            ))}
          </nav>
        )}
        {current && (
          <Tooltip label={current.graph.languages.map((l) => `${l.language}: ${l.files}`).join(" · ")} placement="bottom">
            <span className="hidden shrink-0 truncate text-ui text-[var(--cf-text-muted)] lg:inline" tabIndex={0}>
              {t("codemap.map.stats", { files: current.graph.files.toLocaleString(), declarations: current.graph.declarations.toLocaleString() })}
            </span>
          </Tooltip>
        )}
        {loading && current && <Loader2 size={13} className="shrink-0 animate-spin text-[var(--cf-text-muted)]" aria-hidden />}
        <div className="ml-auto flex items-center gap-1">
          <label className="relative flex items-center">
            <Search size={12} className="pointer-events-none absolute left-2 text-[var(--cf-text-muted)]" aria-hidden />
            <input
              type="search"
              value={text}
              onChange={(event) => setText(event.target.value)}
              onKeyDown={onSearchKey}
              placeholder={t("codemap.map.searchPlaceholder")}
              aria-label={t("codemap.map.searchLabel")}
              className="cf-focusable w-56 rounded-control border border-[var(--cf-border)] bg-[var(--cf-bg)] py-0.5 pl-6 pr-2 text-ui text-[var(--cf-text)] outline-none"
            />
          </label>
          <IconButton
            label="codemap.map.classDiagram"
            labelParams={{ scope: classScope === "" ? projectName : classScope }}
            icon={Boxes}
            pending={drawingClasses}
            disabled={current === null || !current.build.grammars}
            onClick={() => void drawClasses()}
          />
          <IconButton label="codemap.map.ai.open" icon={Sparkles} pending={aiRunning} disabled={current === null} onClick={() => setAiOpen(true)} />
          <IconButton label="codemap.refresh" icon={RefreshCw} pending={loading && current !== null} onClick={() => void refresh()} />
        </div>
      </div>

      {current && !current.build.grammars && <Notice>{t("codemap.map.noGrammars")}</Notice>}
      {current && current.build.truncated > 0 && <Notice>{t("codemap.map.truncated", { count: current.build.truncated.toLocaleString() })}</Notice>}

      <div className="flex min-h-0 flex-1">
        <main className="relative min-w-0 flex-1">
          {error !== null && current === null ? (
            <EmptyState icon={TriangleAlert} title={t("codemap.map.failed")} subtitle={error} />
          ) : current === null ? (
            <div className="flex h-full items-center justify-center text-body text-[var(--cf-text-muted)]">
              <BouncingDots label={t("codemap.map.reading")} />
            </div>
          ) : drawn.length === 0 ? (
            <EmptyState icon={Network} title={t("codemap.map.empty")} />
          ) : (
            <>
              <MapCanvas
                nodes={drawn}
                edges={current.graph.edges}
                colors={colors}
                project={viewMode === "project"}
                selected={selected}
                onSelect={onSelect}
                onOpen={onOpen}
                layoutKey={`${rootPath}:${viewMode}:${current.graph.focus}`}
              />
              {hidden.length > 0 && (
                <Tooltip label={t("codemap.map.moreTitle")} placement="left">
                  <button
                    type="button"
                    className="cf-focusable absolute right-2 top-2 rounded-full border border-[var(--cf-border)] bg-[var(--cf-surface-raised)] px-2.5 py-0.5 text-ui text-[var(--cf-text)] shadow-[var(--cf-shadow-near)] hover:border-[var(--cf-accent)]"
                    onClick={() => showPanel({ kind: "hidden" })}
                  >
                    {t("codemap.map.more", { count: hidden.length })}
                  </button>
                </Tooltip>
              )}
              <Legend colors={colors} />
            </>
          )}
        </main>
        <MapPanel rootPath={rootPath} selectedNode={selectedNode} hidden={hidden} />
      </div>
      {aiOpen && <AiDiagramDialog rootPath={rootPath} projectName={projectName} onClose={() => setAiOpen(false)} />}
    </div>
  );
}

function Notice({ children }: { children: string }) {
  return (
    <p className="flex shrink-0 items-center gap-1.5 border-b border-[var(--cf-border)] bg-[color-mix(in_oklab,var(--cf-warning)_8%,transparent)] px-3 py-1 text-ui text-[var(--cf-text)]">
      <Info size={12} className="shrink-0 text-[var(--cf-warning)]" aria-hidden />
      {children}
    </p>
  );
}

/** The project view's colours, by top-level folder, and the hint for what a click does. */
function Legend({ colors }: { colors: ReadonlyMap<string, string> }) {
  const t = useT();
  return (
    <div className="pointer-events-none absolute bottom-2 left-2 flex max-w-[60%] flex-col gap-1">
      {colors.size > 0 && (
        <div className="flex flex-wrap gap-x-2.5 gap-y-1 rounded-control bg-[color-mix(in_oklab,var(--cf-surface)_85%,transparent)] px-2 py-1">
          {[...colors].map(([group, color]) => (
            <span key={group} className="flex items-center gap-1 text-badge text-[var(--cf-text-muted)]">
              <span className="h-2 w-2 rounded-full" style={{ background: color }} aria-hidden />
              {group}
            </span>
          ))}
        </div>
      )}
      <span className="text-badge text-[var(--cf-text-muted)]">{t("codemap.map.hint")}</span>
    </div>
  );
}
