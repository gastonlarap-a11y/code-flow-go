import { create } from "zustand";
import * as api from "../lib/ipc/commands";
import { pushErrorToast } from "./toastStore";
import { normalizeDocumentPath, type DocumentPathError } from "../lib/documentPath";
import {
  amend,
  canRedo,
  canUndo,
  commit,
  forget,
  initialHistory,
  redo,
  undo,
  type History,
} from "../lib/diagram/history";
import { emptyDocument, newDocument, type DiagramDocument } from "../lib/diagram/model";
import { emitMermaid } from "../lib/diagram/mermaid/emit";
import { parseMermaid, type ParseFailure } from "../lib/diagram/mermaid/parse";

/**
 * The diagram editor's document state (DIAG-001, DIAG-003, DIAG-008, DIAG-009).
 *
 * Two representations of one thing, kept in step in both directions:
 *
 * - `source` is the Mermaid text — what is on disk and what the editor pane shows.
 * - `history.present` is the document the canvas draws.
 *
 * **The model is what gets written.** A canvas gesture changes the document and the text is
 * regenerated from it, whole. That is why a drag cannot preserve somebody's own line ordering or
 * their comments: it is the price of not carrying a second, surgical editor for the text (the
 * schema designer pays the other side of that trade in `lib/dbml/editDbml.ts`, 30 KB of it).
 *
 * Typing in the pane goes the other way — parse, and adopt the result if it parsed. It **amends**
 * rather than committing, so a keystroke is not an undo step: text undo belongs to Monaco, which
 * already has it.
 */
export const DOCUMENT_EXTENSION = ".mmd";

export interface Selection {
  nodes: readonly string[];
  edges: readonly string[];
}

const NOTHING_SELECTED: Selection = { nodes: [], edges: [] };

interface DiagramState {
  documents: string[];
  activePath: string | null;
  /** The Mermaid text. Authoritative for what is saved; regenerated whenever the model changes. */
  source: string;
  history: History<DiagramDocument>;
  /**
   * Why the text in the pane does not parse, or `null` when it does.
   *
   * While this is set the canvas keeps drawing the last document that parsed, and refuses every
   * edit — dragging against a model the text no longer describes would write back something the
   * user never typed (DIAG-008).
   */
  parseError: ParseFailure | null;
  dirty: boolean;
  /** How many lines the last read could not use as written. */
  dropped: number;
  selection: Selection;
  documentsLoading: boolean;
  docLoading: boolean;
  saving: boolean;

  doc: () => DiagramDocument;
  /** Whether the canvas may be edited at all: only when the text says what the model says. */
  editable: boolean;
  canUndo: () => boolean;
  canRedo: () => boolean;

  loadDocuments: (rootPath: string) => Promise<void>;
  openDocument: (rootPath: string, relPath: string) => Promise<void>;
  createDocument: (
    rootPath: string,
    name: string,
    contents?: DiagramDocument,
  ) => Promise<DocumentPathError | "exists" | null>;
  save: (rootPath: string) => Promise<void>;
  /** What the editor pane calls on every keystroke. */
  setSource: (source: string) => void;
  /** What the canvas calls. Regenerates the text from the result. */
  edit: (change: (doc: DiagramDocument) => DiagramDocument, options?: { amend?: true }) => void;
  select: (selection: Selection) => void;
  undo: () => void;
  redo: () => void;
  reset: () => void;
}

/** Why a file could not be opened, as a translation key the view can render. */
export function failureKey(reason: ParseFailure): string {
  return `diagram.error.${reason}`;
}

const INITIAL = {
  documents: [] as string[],
  activePath: null as string | null,
  source: "",
  history: initialHistory(emptyDocument),
  parseError: null as ParseFailure | null,
  dirty: false,
  dropped: 0,
  selection: NOTHING_SELECTED,
  documentsLoading: false,
  docLoading: false,
  saving: false,
  editable: false,
};

export const useDiagramStore = create<DiagramState>((set, get) => ({
  ...INITIAL,

  doc: () => get().history.present,
  canUndo: () => canUndo(get().history),
  canRedo: () => canRedo(get().history),

  loadDocuments: async (rootPath) => {
    set({ documentsLoading: true });
    try {
      const documents = await api.diagramListDocuments(rootPath);
      set({ documents });
    } catch (e) {
      pushErrorToast(String(e));
    } finally {
      set({ documentsLoading: false });
    }
  },

  openDocument: async (rootPath, relPath) => {
    set({
      activePath: relPath,
      docLoading: true,
      source: "",
      history: initialHistory(emptyDocument),
      parseError: null,
      dirty: false,
      dropped: 0,
      editable: false,
      selection: NOTHING_SELECTED,
    });

    try {
      const text = await api.readFileText(rootPath, relPath);
      // The user can pick another document while the read is in flight; the newer one owns the
      // canvas. Same guard `dbmlStore.openDocument` applies to its own reads.
      if (get().activePath !== relPath) return;

      const parsed = parseMermaid(text);
      if (!parsed.ok) {
        pushErrorToast(failureKey(parsed.reason));
        set({ activePath: null });
        return;
      }

      set({
        source: text,
        history: initialHistory(parsed.value.doc),
        dropped: parsed.value.dropped,
        editable: true,
      });
    } catch (e) {
      if (get().activePath !== relPath) return;
      pushErrorToast(String(e));
      set({ activePath: null });
    } finally {
      if (get().activePath === relPath) set({ docLoading: false });
    }
  },

  createDocument: async (rootPath, name, contents) => {
    const normalized = normalizeDocumentPath(name, DOCUMENT_EXTENSION);
    if (!normalized.ok) return normalized.reason;

    const { relPath } = normalized;
    // Case-insensitively, because macOS and Windows both are: `Checkout` and `checkout` are one
    // file there, and `create_file` would overwrite rather than refuse.
    const taken = get().documents.some((d) => d.toLowerCase() === relPath.toLowerCase());
    if (taken) return "exists";

    const doc = contents ?? newDocument(name.trim());
    const source = emitMermaid(doc);

    try {
      await api.createFile(rootPath, relPath);
      await api.writeFileText(rootPath, relPath, source);
    } catch (e) {
      pushErrorToast(String(e));
      return null;
    }

    await get().loadDocuments(rootPath);
    set({
      activePath: relPath,
      source,
      history: initialHistory(doc),
      parseError: null,
      dirty: false,
      dropped: 0,
      docLoading: false,
      editable: true,
      selection: NOTHING_SELECTED,
    });

    return null;
  },

  save: async (rootPath) => {
    const { activePath, dirty, source } = get();
    if (activePath === null || !dirty) return;

    set({ saving: true });
    try {
      await api.writeFileText(rootPath, activePath, source);
      // Compared against the text that was written, not the current buffer: an edit made while the
      // write was in flight must stay dirty, or it is silently lost at the next switch.
      if (get().source === source) set({ dirty: false });
    } catch (e) {
      pushErrorToast(String(e));
    } finally {
      set({ saving: false });
    }
  },

  setSource: (source) => {
    const parsed = parseMermaid(source);
    if (!parsed.ok) {
      // The canvas keeps the last document that parsed, and stops accepting edits. Mid-typing the
      // text is invalid more often than not; blanking the drawing on every keystroke would throw
      // away the zoom and the selection (DIAG-008).
      set({ source, dirty: true, parseError: parsed.reason, editable: false });
      return;
    }

    set((state) => ({
      source,
      dirty: true,
      parseError: null,
      editable: true,
      dropped: parsed.value.dropped,
      // Amended, not committed: a keystroke is not a step to undo through. Monaco owns text undo.
      history: amend(state.history, parsed.value.doc),
    }));
  },

  edit: (change, options) => {
    const { history, activePath, editable } = get();
    if (activePath === null || !editable) return;

    const next = change(history.present);
    if (next === history.present) return;

    set({
      history: options?.amend === true ? amend(history, next) : commit(history, next),
      source: emitMermaid(next),
      dirty: true,
      parseError: null,
    });
  },

  select: (selection) => set({ selection }),

  undo: () => {
    const history = get().history;
    if (!canUndo(history)) return;
    const next = undo(history);
    // Undoing back to what is on disk does not make the file clean again: the write that would
    // reconcile them has not happened, and claiming otherwise loses the work at the next switch.
    set({ history: next, source: emitMermaid(next.present), dirty: true, selection: NOTHING_SELECTED });
  },

  redo: () => {
    const history = get().history;
    if (!canRedo(history)) return;
    const next = redo(history);
    set({ history: next, source: emitMermaid(next.present), dirty: true, selection: NOTHING_SELECTED });
  },

  reset: () => set({ ...INITIAL, history: forget(initialHistory(emptyDocument)) }),
}));
