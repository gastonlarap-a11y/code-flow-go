import { create } from "zustand";
import * as api from "../lib/ipc/commands";
import { isCancellation, newRunId, useAiRunStore } from "./aiRunStore";
import type { ArchifyRendered, ArchifyStatus } from "../types/domain";

/**
 * The Archify document open in the diagram editor (ARCHIFY-005): which one, what the user's install
 * can do, and the page drawn from it for the in-app viewer.
 *
 * Its own store rather than a second kind of document inside the diagram store, which assumes a
 * Mermaid model throughout — its history, its parse, its canvas. Here there is nothing to edit:
 * the document is drawn, shown, exported, and changed only by asking for a new version of it.
 */

interface ArchifyState {
  rootPath: string | null;
  path: string | null;
  status: ArchifyStatus | null;
  page: ArchifyRendered | null;
  rendering: boolean;
  error: string | null;

  /** The document's text before the last adjustment, which one undo writes back (ARCHIFY-008). */
  previous: string | null;
  /** The adjustment being asked of the AI, while it runs. */
  refineRunId: string | null;

  /** Shows a document: checks the install, then draws it. */
  open: (rootPath: string, path: string) => Promise<void>;
  /** Draws the open document again — after it changed on disk, or after Archify was installed. */
  render: () => Promise<void>;
  /**
   * Asks the AI to change the open document, writes the version Archify accepted over it and draws
   * it, keeping the text it replaced for one undo. Throws what refused it; a stop answers false.
   */
  refine: (instruction: string, language: string) => Promise<boolean>;
  /** Writes back the text the last adjustment replaced, and draws it. */
  undo: () => Promise<void>;
  close: () => void;
}

const INITIAL = {
  rootPath: null,
  path: null,
  status: null,
  page: null,
  rendering: false,
  error: null,
  previous: null,
  refineRunId: null,
} satisfies Partial<ArchifyState>;

let renderSeq = 0;

export const useArchifyStore = create<ArchifyState>((set, get) => ({
  ...INITIAL,

  open: async (rootPath, path) => {
    set({ ...INITIAL, rootPath, path });
    await get().render();
  },

  render: async () => {
    const { rootPath, path } = get();
    if (rootPath === null || path === null) return;
    const seq = ++renderSeq;
    set({ rendering: true, error: null });
    try {
      const status = await api.archifyStatus(rootPath);
      if (seq !== renderSeq) return;
      if (!status.installed) {
        set({ status, page: null, rendering: false });
        return;
      }
      const page = await api.archifyRender(rootPath, path);
      if (seq !== renderSeq) return;
      set({ status, page, rendering: false });
    } catch (error: unknown) {
      if (seq !== renderSeq) return;
      set({ rendering: false, page: null, error: error instanceof Error ? error.message : String(error) });
    }
  },

  refine: async (instruction, language) => {
    const { rootPath, path, refineRunId } = get();
    if (rootPath === null || path === null || refineRunId !== null || instruction.trim() === "") return false;
    const runId = newRunId("archify-refine");
    set({ refineRunId: runId });
    useAiRunStore.getState().start(runId);
    try {
      const before = await api.readFileText(rootPath, path);
      const drawn = await api.archifyRefine({ rootPath, path, instruction: instruction.trim(), runId, language });
      // The document the user left open is the one that was asked about; one they opened meanwhile
      // is not overwritten with another's answer.
      if (get().path !== path || get().rootPath !== rootPath) return false;
      await api.writeFileText(rootPath, path, drawn.document);
      set({ previous: before });
      await get().render();
      return true;
    } catch (e) {
      if (isCancellation(e)) return false;
      throw e;
    } finally {
      useAiRunStore.getState().finish(runId);
      if (get().refineRunId === runId) set({ refineRunId: null });
    }
  },

  undo: async () => {
    const { rootPath, path, previous } = get();
    if (rootPath === null || path === null || previous === null) return;
    await api.writeFileText(rootPath, path, previous);
    set({ previous: null });
    await get().render();
  },

  close: () => {
    renderSeq++;
    set(INITIAL);
  },
}));
