import { create } from "zustand";
import * as api from "../lib/ipc/commands";
import type { RepoMapView } from "../types/domain";

/**
 * The code map's drawn view (19-repo-map.md): which repository, which way it is drawn, which folder
 * is open, and what the side panel is showing.
 *
 * Every graph answer is checked against the request that asked for it before it is kept — switching
 * folders fast fires a request per click, and a slow answer to an earlier one must not replace the
 * folder the user is now looking at. The way each repository was last drawn is remembered for the
 * session, so coming back to it lands where it was left.
 */

export type MapMode = "project" | "folders";

/** What the side panel shows. */
export type MapPanel =
  | { kind: "home" }
  /** The cards a folder holds beyond the ones drawn. */
  | { kind: "hidden" }
  | { kind: "file"; path: string; line: number | null }
  | { kind: "search"; query: string }
  | { kind: "usages"; name: string; path: string | null };

interface RepoMapState {
  rootPath: string | null;
  /** The mode asked for. It runs ahead of the view while the next one loads. */
  mode: MapMode;
  focus: string;
  view: RepoMapView | null;
  /** The mode the shown view was drawn for — what the canvas draws and fits by. */
  viewMode: MapMode;
  loading: boolean;
  error: string | null;
  /** The card picked on the canvas. */
  selected: string | null;
  panel: MapPanel;

  open: (rootPath: string) => Promise<void>;
  refresh: () => Promise<void>;
  setMode: (mode: MapMode) => Promise<void>;
  /** Opens a folder, which is always the folder view. "" is the repository's root. */
  setFocus: (focus: string) => Promise<void>;
  select: (id: string | null) => void;
  showPanel: (panel: MapPanel) => void;
  reset: () => void;
}

const INITIAL = {
  rootPath: null,
  mode: "project" as MapMode,
  focus: "",
  view: null,
  viewMode: "project" as MapMode,
  loading: false,
  error: null,
  selected: null,
  panel: { kind: "home" } as MapPanel,
} satisfies Partial<RepoMapState>;

/** How each repository was last drawn, for this session. */
const remembered = new Map<string, { mode: MapMode; focus: string }>();

let loadSeq = 0;

export const useRepoMapStore = create<RepoMapState>((set, get) => {
  const load = async () => {
    const { rootPath, mode, focus } = get();
    if (rootPath === null) return;
    const seq = ++loadSeq;
    set({ loading: true });
    try {
      const view = mode === "project" ? await api.repomapProject(rootPath) : await api.repomapFolder(rootPath, focus);
      if (seq !== loadSeq) return;
      // A folder holding only one subfolder answers that subfolder; the view follows.
      const shown = mode === "folders" ? view.graph.focus : focus;
      remembered.set(rootPath, { mode, focus: shown });
      set({ view, viewMode: mode, focus: shown, loading: false, error: null });
    } catch (error: unknown) {
      if (seq !== loadSeq) return;
      set({ loading: false, error: error instanceof Error ? error.message : String(error) });
    }
  };

  return {
    ...INITIAL,

    open: async (rootPath) => {
      if (get().rootPath === rootPath && get().view !== null) {
        await load();
        return;
      }
      const last = remembered.get(rootPath) ?? { mode: "project" as MapMode, focus: "" };
      set({ ...INITIAL, rootPath, mode: last.mode, focus: last.focus });
      await load();
    },

    refresh: load,

    setMode: async (mode) => {
      if (mode === get().mode) return;
      set({ mode, selected: null });
      await load();
    },

    setFocus: async (focus) => {
      set({ mode: "folders", focus, selected: null, panel: { kind: "home" } });
      await load();
    },

    select: (id) => set({ selected: id }),

    showPanel: (panel) => set({ panel }),

    /** Back to nothing, when the last project closes — which also forgets how each one was drawn. */
    reset: () => {
      loadSeq++;
      remembered.clear();
      set(INITIAL);
    },
  };
});
