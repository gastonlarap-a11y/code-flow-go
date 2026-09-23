import { useEffect, useMemo, useRef, useState } from "react";
import {
  Database,
  Download,
  FilePlus2,
  PanelLeftClose,
  PanelLeftOpen,
  RotateCw,
  Save,
  Sparkles,
  Upload,
} from "lucide-react";
import type { editor as MonacoEditorNS } from "monaco-editor";
import { Editor, OVERFLOW_SAFE_OPTIONS, monaco, type OnMount } from "../../lib/monacoEditor";
import { parseDbmlModel } from "../../lib/dbml/parse";
import { carriedPosition } from "../../lib/dbml/cardEdit";
import { useCardEditing } from "../../lib/dbml/useCardEditing";
import { emptyModel, type DbmlSchemaModel } from "../../lib/dbml/model";
import { useDbmlStore } from "../../state/dbmlStore";
import { useWorkspaceStore } from "../../state/workspaceStore";
import { useThemeStore } from "../../state/themeStore";
import { confirmAction } from "../../state/confirmStore";
import { useT } from "../../state/languageStore";
import { useLayoutStore } from "../../state/layoutStore";
import { EmptyState } from "../common/EmptyState";
import { ResizeHandle } from "../common/ResizeHandle";
import { IconButton } from "../common/IconButton";
import { Button } from "../common/Button";
import { Select, type SelectItems } from "../common/Select";
import { documentName, groupByFolder } from "../../lib/documentPath";
import { DbmlCanvas, type DbmlCanvasHandle } from "./DbmlCanvas";
import { DbmlViewportControls } from "./DbmlViewportControls";
import { NewDbmlModal } from "./NewDbmlModal";
import { ExportDbmlModal } from "./ExportDbmlModal";
import { ImportDbmlModal } from "./ImportDbmlModal";
import { DbmlAiModal } from "./DbmlAiModal";

/**
 * The schema designer: the `.dbml` documents of the open folder, one editor, and the diagram it
 * produces (DBML-002, DBML-008).
 *
 * `@dbml/core` is 15 MB — four times Monaco — so the whole view is behind a `lazy()` in `App.tsx`
 * and only a user who opens this module pays for it. The parse runs on the buffer rather than on
 * disk, which is what makes the diagram live.
 */
/**
 * How narrow and how wide the source pane may be dragged.
 *
 * The minimum is not cosmetic: below it Monaco's own gutter and horizontal scrollbar leave no
 * column for text, so the pane stops being an editor before it stops being visible. Anyone wanting
 * less than this wants it gone, which is what the collapse button is for.
 */
const DBML_EDITOR_MIN = 240;
const DBML_EDITOR_MAX = 900;

export function DbmlView() {
  const t = useT();
  const project = useWorkspaceStore((s) => s.activeProject());
  const rootPath = project?.local_path ?? null;
  const monacoTheme = useThemeStore((s) => s.monacoTheme);

  const documents = useDbmlStore((s) => s.documents);
  const activePath = useDbmlStore((s) => s.activePath);
  const source = useDbmlStore((s) => s.source);
  const dirty = useDbmlStore((s) => s.dirty);
  const saving = useDbmlStore((s) => s.saving);
  const positions = useDbmlStore((s) => s.positions);

  // Grouped by folder, exactly as the diagram editor's picker is: both list documents the same
  // walk found, and two pickers over the same kind of list should not behave differently.
  const documentOptions = useMemo<SelectItems>(
    () =>
      groupByFolder(documents).flatMap(({ folder, paths }): SelectItems =>
        folder === null
          ? paths.map((path) => ({ value: path, label: path }))
          : [{ label: folder, options: paths.map((path) => ({ value: path, label: documentName(path) })) }],
      ),
    [documents],
  );

  const editorWidth = useLayoutStore((s) => s.sizes.dbmlEditorWidth);
  const setSize = useLayoutStore((s) => s.setSize);
  const commitSize = useLayoutStore((s) => s.commitSize);
  // Deliberately not persisted, unlike the width: collapsing is "get this out of my way while I
  // read the diagram", and an app that reopened with no editor would look like it had lost one.
  const [editorCollapsed, setEditorCollapsed] = useState(false);
  const loadDocuments = useDbmlStore((s) => s.loadDocuments);
  const openDocument = useDbmlStore((s) => s.openDocument);
  const setSource = useDbmlStore((s) => s.setSource);
  const save = useDbmlStore((s) => s.save);
  const placeTable = useDbmlStore((s) => s.placeTable);
  const arrangeAll = useDbmlStore((s) => s.arrangeAll);
  const reset = useDbmlStore((s) => s.reset);

  const [creating, setCreating] = useState(false);
  const [exporting, setExporting] = useState(false);
  const [importing, setImporting] = useState(false);
  const [asking, setAsking] = useState(false);
  const canvasRef = useRef<DbmlCanvasHandle>(null);
  const editorRef = useRef<MonacoEditorNS.IStandaloneCodeEditor | null>(null);

  // The module is repo-scoped, so its documents follow the selected project — including back to
  // nothing when the last one is closed.
  useEffect(() => {
    reset();
    if (rootPath !== null) void loadDocuments(rootPath);
  }, [rootPath, loadDocuments, reset]);

  const parsed = useMemo(() => parseDbmlModel(source), [source]);

  // The diagram keeps the last model that parsed. Mid-edit, the text is invalid more often than not;
  // unmounting the canvas on every one of those keystrokes would blank the diagram and reset the
  // zoom, so the error is shown over the last good picture instead.
  //
  // Adjusted during render rather than in an effect — React's documented pattern for state derived
  // from a changing value: an effect would paint one frame of the stale model first. The condition
  // settles immediately, because `parsed` is memoised on `source` and so its model keeps its identity.
  const [model, setModel] = useState<DbmlSchemaModel>(emptyModel);
  if (parsed.ok && parsed.model !== model) setModel(parsed.model);

  // The rules are `useCardEditing`, shared with the Editor's preview; what this passes is what it
  // owns — its buffer, its source pane, and the positions a rename has to carry over (DBML-028).
  //
  // Above the early return below, not after it: it is named and called as a hook so that the day it
  // needs one no call site has to move, and a hook after a conditional return is the hook-order
  // crash `rules-of-hooks` exists for — the view mounts with no project and then gets one.
  const editing = useCardEditing({
    readSource: () => useDbmlStore.getState().source,
    parsed,
    model,
    onSource: setSource,
    onRenamed: (table, name) => {
      const carried = carriedPosition(positions, table, name);
      if (carried && project !== null) void placeTable(project.id, carried.key, carried.point);
    },
    onReveal: (from, to) => {
      setEditorCollapsed(false);
      // Next frame, so a source pane that was collapsed has been laid out before Monaco is asked to
      // scroll inside it — a hidden editor has no viewport to centre a line in.
      requestAnimationFrame(() => {
        const instance = editorRef.current;
        if (!instance) return;
        instance.revealLineInCenter(from.line);
        instance.setSelection({
          startLineNumber: from.line,
          startColumn: from.column,
          endLineNumber: to.line,
          endColumn: to.column,
        });
        instance.focus();
      });
    },
  });

  if (project === null || rootPath === null) {
    return <EmptyState icon={Database} title={t("dbml.noDocumentOpen")} />;
  }

  const arrange = async () => {
    // Nothing to lose when nothing was placed by hand, so nothing to confirm.
    const hasPlacements = Object.keys(positions).length > 0;
    if (hasPlacements && !(await confirmAction(t("dbml.arrangeConfirm"), false, t("dbml.arrange")))) return;
    await arrangeAll(project.id);
    // After the cleared positions have re-rendered the layout, so the fit measures the new one.
    requestAnimationFrame(() => canvasRef.current?.fit());
  };

  const onEditorMount: OnMount = (instance) => {
    editorRef.current = instance;
    instance.addCommand(monaco.KeyMod.CtrlCmd | monaco.KeyCode.KeyS, () => {
      void useDbmlStore.getState().save(rootPath);
    });
  };

  return (
    <div className="flex h-full flex-col overflow-hidden">
      <div className="flex h-9 shrink-0 items-center gap-1.5 border-b border-[var(--cf-border)] px-2">
        <span className="text-badge font-semibold uppercase tracking-wide text-[var(--cf-text-muted)]">
          {t("dbml.documents")}
        </span>

        {/* A select rather than a tree: these are a handful of files, and the picker is not the
            feature. The file explorer stays available in the Editor module. Grouped by folder,
            though — typing `db/orders` when creating one already puts it in a folder, and a flat
            list of full paths is a folder nobody can see they have. */}
        <Select
          value={activePath ?? ""}
          ariaLabel={t("dbml.documents")}
          size="sm"
          className="min-w-0 max-w-[280px] flex-1"
          placeholder={t("dbml.noDocumentOpen")}
          options={documentOptions}
          onChange={(next) => {
            if (next.length > 0) void openDocument(project.id, rootPath, next);
          }}
        />

        {dirty && <span className="text-badge text-[var(--cf-warning)]">{t("dbml.unsaved")}</span>}

        <div className="ml-auto flex items-center gap-1">
          {/* Beside the document's own actions, because it acts on this document's editor rather
              than on the module. Disabled with no document for the same reason the others are:
              there is no editor to collapse. */}
          <IconButton
            label={editorCollapsed ? "dbml.editor.show" : "dbml.editor.hide"}
            icon={editorCollapsed ? PanelLeftOpen : PanelLeftClose}
            size="sm"
            disabled={activePath === null}
            onClick={() => setEditorCollapsed(!editorCollapsed)}
          />
          <Button
            variant="secondary"
            size="sm"
            icon={Save}
            disabled={!dirty || activePath === null}
            pending={saving}
            onClick={() => void save(rootPath)}
          >
            {t("dbml.save")}
          </Button>
          <IconButton
            label="dbml.ai.action"
            icon={Sparkles}
            size="sm"
            disabled={activePath === null}
            onClick={() => setAsking(true)}
          />
          <IconButton
            label="dbml.export.action"
            icon={Upload}
            size="sm"
            disabled={activePath === null}
            onClick={() => setExporting(true)}
          />
          <IconButton label="dbml.import.action" icon={Download} size="sm" onClick={() => setImporting(true)} />
          <IconButton label="dbml.reload" icon={RotateCw} size="sm" onClick={() => void loadDocuments(rootPath)} />
          <IconButton label="dbml.newDocument" icon={FilePlus2} size="sm" onClick={() => setCreating(true)} />
        </div>
      </div>

      {activePath === null ? (
        <EmptyState
          icon={Database}
          title={documents.length === 0 ? t("dbml.noDocuments") : t("dbml.noDocumentOpen")}
          subtitle={documents.length === 0 ? t("dbml.noDocumentsHint") : undefined}
        />
      ) : (
        <div className="flex min-h-0 flex-1">
          {/* The source pane is sized, not shared. It opened at half the window, which is the wrong
              split for a designer: the diagram is what is being read and the source is edited in
              glances. Collapsed it keeps a rail, so getting the editor back is one click rather
              than a menu hunt. The width is remembered per install like every other pane. */}
          <div
            style={{ width: editorCollapsed ? undefined : editorWidth }}
            className={`min-w-0 shrink-0 border-r border-[var(--cf-border)] ${editorCollapsed ? "hidden" : ""}`}
          >
            <Editor
              height="100%"
              // No DBML language in Monaco; `sql` gets the comment and string colouring close
              // enough, and `path` keeps one model per document so undo history survives a switch.
              language="sql"
              path={`dbml:/${activePath}`}
              value={source}
              theme={monacoTheme}
              onChange={(next) => setSource(next ?? "")}
              onMount={onEditorMount}
              options={{
                ...OVERFLOW_SAFE_OPTIONS,
                minimap: { enabled: false },
                fontSize: 12,
                automaticLayout: true,
                scrollBeyondLastLine: false,
                tabSize: 2,
                lineNumbersMinChars: 3,
                overviewRulerLanes: 0,
                padding: { top: 8, bottom: 8 },
              }}
            />
          </div>

          {!editorCollapsed && (
            <ResizeHandle
              axis="x"
              value={editorWidth}
              min={DBML_EDITOR_MIN}
              max={DBML_EDITOR_MAX}
              onChange={(w) => setSize("dbmlEditorWidth", w)}
              onCommit={(w) => commitSize("dbmlEditorWidth", w)}
            />
          )}

          <div className="relative min-w-0 flex-1">
            {model.tables.length === 0 ? (
              <EmptyState icon={Database} title={t("dbml.noTables")} />
            ) : (
              <>
                <DbmlCanvas
                  ref={canvasRef}
                  model={model}
                  documentKey={activePath}
                  positions={positions}
                  onPlace={(key, point) => void placeTable(project.id, key, point)}
                  editing={editing}
                />
                <DbmlViewportControls canvas={canvasRef} onArrange={() => void arrange()} />
              </>
            )}

            {!parsed.ok && (
              <p
                role="status"
                className="absolute inset-x-2 top-2 rounded-md border border-[var(--cf-danger)] bg-[color-mix(in_oklab,var(--cf-danger)_10%,var(--cf-surface))] p-3 font-mono text-ui text-[var(--cf-danger)] shadow-sm"
              >
                {parsed.error}
              </p>
            )}
          </div>
        </div>
      )}

      {creating && <NewDbmlModal rootPath={rootPath} onClose={() => setCreating(false)} />}

      {/* Import writes a new document rather than replacing the open one, so it needs no document
          to be open at all — unlike export and the assistant. */}
      {importing && <ImportDbmlModal rootPath={rootPath} onClose={() => setImporting(false)} />}

      {exporting && activePath !== null && (
        <ExportDbmlModal source={source} relPath={activePath} onClose={() => setExporting(false)} />
      )}

      {/* An accepted proposal lands in the buffer, not on disk: the save button and Ctrl+Z keep
          owning it, which is the same bargain the editor's inline edit makes (DBML-017). */}
      {asking && activePath !== null && (
        <DbmlAiModal
          source={source}
          relPath={activePath}
          onApply={setSource}
          onClose={() => setAsking(false)}
        />
      )}
    </div>
  );
}
