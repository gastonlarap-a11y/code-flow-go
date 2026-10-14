import { create } from "zustand";
import * as api from "../lib/ipc/commands";
import { pushErrorToast } from "./toastStore";
import type {
  CodemapFlow,
  CodemapImpact,
  CodemapOverview,
  CodemapStage,
  CodemapSymbolDetail,
  CodemapSymbolRef,
} from "../types/domain";

/**
 * The code map view's state (18-code-map.md).
 *
 * One repository at a time — the module is repo-scoped, so `open` starts over whenever the selected
 * project changes. Every answer is checked against the request that asked for it before it is
 * kept: typing in the search box fires a request per keystroke, and clicking through symbols fires
 * one per click, and a slow answer to an earlier question must not overwrite a newer one.
 */

export type CodemapTab = "relations" | "flow" | "impact";

/** How many symbols the back button remembers. */
const TRAIL_LIMIT = 50;

interface CodemapState {
  rootPath: string | null;
  overview: CodemapOverview | null;
  /** The stage of the build in progress, from `codemap:progress`; null once it is ready. */
  stage: CodemapStage | null;
  building: boolean;
  /** Why the map could not be built at all — a folder that no longer exists. */
  error: string | null;

  query: string;
  results: CodemapSymbolRef[];
  searching: boolean;

  selected: string | null;
  detail: CodemapSymbolDetail | null;
  /** Several symbols answered one name; the view asks which. */
  candidates: CodemapSymbolRef[];
  /** The symbols visited before this one, most recent last. */
  trail: string[];
  tab: CodemapTab;
  flow: CodemapFlow | null;
  dataOnly: boolean;
  impact: CodemapImpact | null;
  loadingSymbol: boolean;
  loadingTab: boolean;

  open: (rootPath: string) => Promise<void>;
  refresh: () => Promise<void>;
  setStage: (stage: CodemapStage) => void;
  search: (query: string) => Promise<void>;
  select: (symbol: string) => Promise<void>;
  back: () => Promise<void>;
  setTab: (tab: CodemapTab) => Promise<void>;
  setDataOnly: (dataOnly: boolean) => Promise<void>;
  reset: () => void;
}

const INITIAL = {
  rootPath: null,
  overview: null,
  stage: null,
  building: false,
  error: null,
  query: "",
  results: [],
  searching: false,
  selected: null,
  detail: null,
  candidates: [],
  trail: [],
  tab: "relations" as CodemapTab,
  flow: null,
  dataOnly: false,
  impact: null,
  loadingSymbol: false,
  loadingTab: false,
} satisfies Partial<CodemapState>;

/** A counter per kind of request, so an answer can tell whether it is still the latest one. */
let searchSeq = 0;
let symbolSeq = 0;
let tabSeq = 0;

export const useCodemapStore = create<CodemapState>((set, get) => {
  /** Loads what the current tab shows for the selected symbol. */
  const loadTab = async () => {
    const { rootPath, selected, tab, dataOnly } = get();
    if (rootPath === null || selected === null || tab === "relations") return;
    const seq = ++tabSeq;
    set({ loadingTab: true });
    try {
      if (tab === "flow") {
        const flow = await api.codemapFlow(rootPath, selected, { dataOnly });
        if (seq === tabSeq) set({ flow });
      } else {
        const impact = await api.codemapImpact(rootPath, selected);
        if (seq === tabSeq) set({ impact });
      }
    } catch (e) {
      if (seq === tabSeq) pushErrorToast(String(e));
    } finally {
      if (seq === tabSeq) set({ loadingTab: false });
    }
  };

  /** Shows one symbol, without touching the trail. */
  const show = async (symbol: string) => {
    const { rootPath } = get();
    if (rootPath === null) return;
    const seq = ++symbolSeq;
    set({ selected: symbol, loadingSymbol: true, flow: null, impact: null, candidates: [] });
    try {
      const lookup = await api.codemapSymbol(rootPath, symbol);
      if (seq !== symbolSeq) return;
      if (lookup.detail) {
        set({ detail: lookup.detail, selected: lookup.detail.symbol.id });
        void loadTab();
      } else {
        set({ detail: null, selected: null, candidates: lookup.candidates });
      }
    } catch (e) {
      if (seq === symbolSeq) pushErrorToast(String(e));
    } finally {
      if (seq === symbolSeq) set({ loadingSymbol: false });
    }
  };

  return {
    ...INITIAL,

    open: async (rootPath) => {
      searchSeq++;
      symbolSeq++;
      tabSeq++;
      set({ ...INITIAL, rootPath, building: true });
      try {
        const overview = await api.codemapOverview(rootPath);
        if (get().rootPath === rootPath) set({ overview });
      } catch (e) {
        if (get().rootPath === rootPath) set({ error: String(e) });
      } finally {
        if (get().rootPath === rootPath) set({ building: false, stage: null });
      }
    },

    refresh: async () => {
      const { rootPath, selected, query } = get();
      if (rootPath === null) return;
      set({ building: true });
      try {
        const overview = await api.codemapOverview(rootPath);
        if (get().rootPath !== rootPath) return;
        set({ overview, error: null });
      } catch (e) {
        if (get().rootPath === rootPath) set({ error: String(e) });
        return;
      } finally {
        if (get().rootPath === rootPath) set({ building: false, stage: null });
      }
      if (query.trim() !== "") void get().search(query);
      if (selected !== null) void show(selected);
    },

    setStage: (stage) => set({ stage: stage === "ready" ? null : stage }),

    search: async (query) => {
      const { rootPath } = get();
      const seq = ++searchSeq;
      set({ query });
      if (rootPath === null || query.trim() === "") {
        set({ results: [], searching: false });
        return;
      }
      set({ searching: true });
      try {
        const results = await api.codemapSearch(rootPath, query);
        if (seq === searchSeq) set({ results });
      } catch (e) {
        if (seq === searchSeq) pushErrorToast(String(e));
      } finally {
        if (seq === searchSeq) set({ searching: false });
      }
    },

    select: async (symbol) => {
      const { selected, trail } = get();
      if (selected !== null && selected !== symbol) {
        set({ trail: [...trail, selected].slice(-TRAIL_LIMIT) });
      }
      await show(symbol);
    },

    back: async () => {
      const { trail } = get();
      const previous = trail.at(-1);
      if (previous === undefined) return;
      set({ trail: trail.slice(0, -1) });
      await show(previous);
    },

    setTab: async (tab) => {
      set({ tab });
      const { flow, impact } = get();
      if ((tab === "flow" && flow === null) || (tab === "impact" && impact === null)) await loadTab();
    },

    setDataOnly: async (dataOnly) => {
      set({ dataOnly, flow: null });
      await loadTab();
    },

    reset: () => {
      searchSeq++;
      symbolSeq++;
      tabSeq++;
      set(INITIAL);
    },
  };
});
