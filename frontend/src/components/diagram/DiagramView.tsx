import { useCallback, useEffect, useMemo, useRef, useState } from "react";
import type { editor as MonacoEditorNS } from "monaco-editor";
import {
  Download,
  FilePlus2,
  Maximize2,
  PanelRightClose,
  PanelRightOpen,
  Redo2,
  Save,
  Undo2,
  Upload,
  Workflow,
  ZoomIn,
  ZoomOut,
} from "lucide-react";
import { Editor, OVERFLOW_SAFE_OPTIONS, monaco, type OnMount } from "../../lib/monacoEditor";
import { EmptyState } from "../common/EmptyState";
import { IconButton } from "../common/IconButton";
import { ResizeHandle } from "../common/ResizeHandle";
import { Select } from "../common/Select";
import { useWorkspaceStore } from "../../state/workspaceStore";
import { useThemeStore } from "../../state/themeStore";
import { useLayoutStore } from "../../state/layoutStore";
import { useT } from "../../state/languageStore";
import { pushErrorToast } from "../../state/toastStore";
import { confirmAction } from "../../state/confirmStore";
import { DOCUMENT_EXTENSION, failureKey, useDiagramStore } from "../../state/diagramStore";
import { addNode } from "../../lib/diagram/edits";
import { documentTitle } from "../../lib/documentPath";
import { parseDocument } from "../../lib/diagram/serialize";
import { exportDiagram, type ExportFormat } from "../../lib/diagram/exportFile";
import type { StencilId } from "../../lib/diagram/stencils";
import { apiPickFile, apiReadTextFile } from "../../lib/ipc/apiCommands";
import type { DiagramDocument } from "../../lib/diagram/model";
import { DiagramCanvas, type DiagramCanvasHandle } from "./DiagramCanvas";
import { DiagramInspector } from "./DiagramInspector";
import { DiagramPalette } from "./DiagramPalette";
import { NewDiagramModal } from "./NewDiagramModal";

/**
 * The diagram editor: the `.mmd` documents of the open folder, a palette, the canvas, and the
 * Mermaid behind it (DIAG-001, DIAG-015).
 *
 * Three panes, the same arrangement the schema designer uses: what you can add, what you are
 * drawing, and the text it is made of. The text pane is resizable and collapsible, its width
 * persisted and its collapsed state deliberately not — hiding it is "get this out of my way while I
 * draw", and an app that reopened with no editor would look like it had lost one.
 *
 * Repo-scoped: a diagram is a file in the project folder, so it is versioned with the code it
 * describes and reviewed in the same pull request.
 */
const EXPORTS: readonly {
  value: ExportFormat;
  labelKey: "diagram.export.png" | "diagram.export.svg" | "diagram.export.json";
}[] = [
  { value: "png", labelKey: "diagram.export.png" },
  { value: "svg", labelKey: "diagram.export.svg" },
  { value: "json", labelKey: "diagram.export.json" },
];

/**
 * How narrow and how wide the Mermaid pane may be dragged.
 *
 * The minimum is not cosmetic: below it Monaco's own gutter and horizontal scrollbar leave no
 * column for text. Anyone wanting less than this wants it gone, which is what the collapse is for.
 */
const TEXT_MIN = 260;
const TEXT_MAX = 900;

export function DiagramView() {
  const t = useT();
  const project = useWorkspaceStore((s) => s.activeProject());
  const rootPath = project?.local_path ?? null;
  const monacoTheme = useThemeStore((s) => s.monacoTheme);

  const documents = useDiagramStore((s) => s.documents);
  const activePath = useDiagramStore((s) => s.activePath);
  const source = useDiagramStore((s) => s.source);
  const dirty = useDiagramStore((s) => s.dirty);
  const saving = useDiagramStore((s) => s.saving);
  const dropped = useDiagramStore((s) => s.dropped);
  const parseError = useDiagramStore((s) => s.parseError);
  const history = useDiagramStore((s) => s.history);

  const loadDocuments = useDiagramStore((s) => s.loadDocuments);
  const openDocument = useDiagramStore((s) => s.openDocument);
  const setSource = useDiagramStore((s) => s.setSource);
  const save = useDiagramStore((s) => s.save);
  const edit = useDiagramStore((s) => s.edit);
  const select = useDiagramStore((s) => s.select);
  const undo = useDiagramStore((s) => s.undo);
  const redo = useDiagramStore((s) => s.redo);
  const reset = useDiagramStore((s) => s.reset);

  const textWidth = useLayoutStore((s) => s.sizes.dbmlEditorWidth);
  const setSize = useLayoutStore((s) => s.setSize);
  const commitSize = useLayoutStore((s) => s.commitSize);
  const [textCollapsed, setTextCollapsed] = useState(false);

  const [creating, setCreating] = useState(false);
  const [imported, setImported] = useState<DiagramDocument | null>(null);
  const canvasRef = useRef<DiagramCanvasHandle>(null);

  const canUndo = history.past.length > 0;
  const canRedo = history.future.length > 0;

  // The module is repo-scoped, so its documents follow the selected project — including back to
  // nothing when the last one is closed.
  useEffect(() => {
    reset();
    if (rootPath !== null) void loadDocuments(rootPath);
  }, [rootPath, loadDocuments, reset]);

  const saveNow = useCallback(() => {
    if (rootPath !== null) void save(rootPath);
  }, [rootPath, save]);

  /**
   * The three bindings a canvas cannot do without.
   *
   * On the window rather than the canvas: after deleting a shape the focus is on nothing in
   * particular, and an undo that depends on where the focus landed is an undo people stop trusting.
   * Skipped while a text field has focus, so undo inside the Mermaid pane is Monaco's own.
   */
  useEffect(() => {
    const onKeyDown = (event: KeyboardEvent) => {
      const target = event.target;
      const typing =
        target instanceof HTMLElement &&
        (target.tagName === "INPUT" || target.tagName === "TEXTAREA" || target.isContentEditable);

      if (!(event.metaKey || event.ctrlKey)) return;

      if (event.key.toLowerCase() === "s") {
        event.preventDefault();
        saveNow();
        return;
      }
      if (typing) return;
      if (event.key.toLowerCase() === "z") {
        event.preventDefault();
        if (event.shiftKey) redo();
        else undo();
      }
    };

    window.addEventListener("keydown", onKeyDown);
    return () => window.removeEventListener("keydown", onKeyDown);
  }, [saveNow, undo, redo]);

  const place = useCallback(
    (kind: StencilId) => {
      const at = canvasRef.current?.centreOfView() ?? { x: 0, y: 0 };
      edit((current) => {
        const added = addNode(current, kind, at);
        // Selected on arrival, so the next thing typed names it and the next drag moves it.
        select({ nodes: [added.id], edges: [] });
        return added.doc;
      });
    },
    [edit, select],
  );

  const pickDocument = useCallback(
    async (relPath: string) => {
      if (rootPath === null || relPath === activePath) return;
      if (dirty && !(await confirmAction(t("diagram.discardConfirm"), true, t("diagram.discard")))) {
        return;
      }
      await openDocument(rootPath, relPath);
      requestAnimationFrame(() => canvasRef.current?.fit());
    },
    [rootPath, activePath, dirty, openDocument, t],
  );

  /**
   * Importing a `.diagram.json`.
   *
   * Which is also the way in for anything drawn with the version before Mermaid: `parseDocument`
   * translates the old catalogue to this one and reports how much it could not keep as written.
   */
  const importFile = useCallback(async () => {
    const path = await apiPickFile(["json"]);
    if (path === null) return;

    try {
      const parsed = parseDocument(await apiReadTextFile(path));
      if (!parsed.ok) {
        pushErrorToast(t(`diagram.error.${parsed.reason}` as "diagram.error.notJson"));
        return;
      }
      // Imports create a document; they never overwrite the one that is open.
      setImported(parsed.value.doc);
    } catch (e) {
      pushErrorToast(String(e));
    }
  }, [t]);

  const exportAs = useCallback(
    async (format: ExportFormat) => {
      if (activePath === null) return;
      try {
        await exportDiagram(history.present, documentTitle(activePath, DOCUMENT_EXTENSION), format);
      } catch (e) {
        pushErrorToast(String(e));
      }
    },
    [activePath, history],
  );

  const onEditorMount: OnMount = (instance: MonacoEditorNS.IStandaloneCodeEditor) => {
    instance.addCommand(monaco.KeyMod.CtrlCmd | monaco.KeyCode.KeyS, () => {
      if (rootPath !== null) void useDiagramStore.getState().save(rootPath);
    });
  };

  const options = useMemo(
    () => documents.map((path) => ({ value: path, label: path })),
    [documents],
  );

  if (project === null || rootPath === null) {
    return <EmptyState icon={Workflow} title={t("diagram.noProject")} />;
  }

  return (
    <div className="flex h-full flex-col">
      <header className="flex items-center gap-2 border-b border-[var(--cf-border)] px-2 py-1.5">
        <Select
          value={activePath ?? ""}
          ariaLabel={t("diagram.documentPicker")}
          size="sm"
          className="max-w-[280px]"
          placeholder={t("diagram.noDocument")}
          options={options}
          onChange={(value) => void pickDocument(value)}
        />

        <IconButton label="diagram.newDocument" icon={FilePlus2} onClick={() => setCreating(true)} />
        <IconButton label="diagram.import" icon={Upload} onClick={() => void importFile()} />

        <span className="mx-1 h-4 w-px bg-[var(--cf-border)]" aria-hidden="true" />

        <IconButton label="diagram.undo" icon={Undo2} disabled={!canUndo} onClick={undo} />
        <IconButton label="diagram.redo" icon={Redo2} disabled={!canRedo} onClick={redo} />

        <span className="mx-1 h-4 w-px bg-[var(--cf-border)]" aria-hidden="true" />

        <IconButton label="diagram.zoomOut" icon={ZoomOut} onClick={() => canvasRef.current?.zoomBy(1 / 1.2)} />
        <IconButton label="diagram.zoomIn" icon={ZoomIn} onClick={() => canvasRef.current?.zoomBy(1.2)} />
        <IconButton label="diagram.fit" icon={Maximize2} onClick={() => canvasRef.current?.fit()} />

        <span className="ml-auto flex items-center gap-2">
          {dropped > 0 && (
            <span role="status" className="text-ui text-[var(--cf-warning)]">
              {t("diagram.droppedShapes", { count: dropped })}
            </span>
          )}
          {dirty && <span className="text-ui text-[var(--cf-text-muted)]">{t("diagram.unsaved")}</span>}

          <Select
            value=""
            ariaLabel={t("diagram.export.action")}
            size="sm"
            placeholder={t("diagram.export.action")}
            options={EXPORTS.map((entry) => ({ value: entry.value, label: t(entry.labelKey) }))}
            disabled={activePath === null}
            onChange={(value) => void exportAs(value as ExportFormat)}
          />
          <IconButton
            label="diagram.toggleText"
            icon={textCollapsed ? PanelRightOpen : PanelRightClose}
            onClick={() => setTextCollapsed((open) => !open)}
          />
          <IconButton
            label="diagram.save"
            icon={Save}
            disabled={!dirty || activePath === null}
            pending={saving}
            onClick={saveNow}
          />
        </span>
      </header>

      {activePath === null ? (
        <EmptyState
          icon={Download}
          title={t("diagram.noDocumentOpen")}
          subtitle={t("diagram.noDocumentOpenHint")}
        />
      ) : (
        <div className="flex min-h-0 flex-1">
          <DiagramPalette onPlace={place} disabled={parseError !== null} />

          <div className="relative min-w-0 flex-1 border-x border-[var(--cf-border)]">
            <DiagramCanvas ref={canvasRef} />
            {parseError !== null && (
              <p
                role="status"
                className="absolute inset-x-3 top-3 rounded-control bg-[var(--cf-surface-raised)] px-3 py-2 text-body text-[var(--cf-danger)] shadow-[var(--cf-shadow)]"
              >
                {t(failureKey(parseError) as "diagram.error.notMermaid")} ·{" "}
                {t("diagram.readOnlyWhileInvalid")}
              </p>
            )}
            <DiagramInspector />
          </div>

          {!textCollapsed && (
            <>
              <ResizeHandle
                axis="x"
                min={TEXT_MIN}
                max={TEXT_MAX}
                value={textWidth}
                onChange={(next) => setSize("dbmlEditorWidth", next)}
                onCommit={(next) => commitSize("dbmlEditorWidth", next)}
              />
              <div className="min-w-0 shrink-0" style={{ width: textWidth }}>
                <Editor
                  value={source}
                  onChange={(next) => setSource(next ?? "")}
                  onMount={onEditorMount}
                  language="mermaid"
                  path={`mermaid:/${activePath}`}
                  theme={monacoTheme}
                  options={{
                    ...OVERFLOW_SAFE_OPTIONS,
                    minimap: { enabled: false },
                    lineNumbers: "on",
                    wordWrap: "on",
                    fontSize: 13,
                    scrollBeyondLastLine: false,
                  }}
                />
              </div>
            </>
          )}
        </div>
      )}

      {creating && <NewDiagramModal rootPath={rootPath} onClose={() => setCreating(false)} />}
      {imported !== null && (
        <NewDiagramModal rootPath={rootPath} contents={imported} onClose={() => setImported(null)} />
      )}
    </div>
  );
}
