import { create } from "zustand";
import * as api from "../lib/ipc/commands";
import { saveArchifyDocument, showArchifyDocument } from "../lib/archify/save";
import { RECENT_TOPICS_KEY, parseTopics, pushTopic, type RecentTopics } from "../lib/archify/topics";
import { isCancellation, newRunId, useAiRunStore } from "./aiRunStore";
import { pushErrorToast, useToastStore } from "./toastStore";
import { translate } from "./languageStore";
import type { ArchifyAiKind } from "../types/domain";

/**
 * The diagrams a model is drawing from the code map (ARCHIFY-007): one run per repository at a
 * time, which outlives the dialog that started it — the dialog can be closed and the diagram still
 * opens when it is ready.
 */

export interface DrawRun {
  runId: string;
  kind: ArchifyAiKind;
}

interface ArchifyDrawState {
  /** The run in flight, by repository root. */
  runs: Record<string, DrawRun>;
  recent: RecentTopics;
  recentLoaded: boolean;
  /** Reads the topics asked for before. Once is enough: this store is their only writer. */
  loadRecent: () => Promise<void>;
  /**
   * Asks for a diagram, saves it into the repository and opens it. Answers where it was saved, or
   * null when it was not — refused, failed (said in a toast) or stopped by the user.
   */
  draw: (request: { rootPath: string; projectName: string; kind: ArchifyAiKind; topic: string; language: string }) => Promise<string | null>;
}

export const useArchifyDrawStore = create<ArchifyDrawState>((set, get) => ({
  runs: {},
  recent: {},
  recentLoaded: false,

  loadRecent: async () => {
    if (get().recentLoaded) return;
    set({ recentLoaded: true });
    try {
      const stored = parseTopics(await api.getSetting(RECENT_TOPICS_KEY));
      // A topic asked for while the setting was being read is newer than anything in it.
      set({ recent: { ...stored, ...get().recent } });
    } catch (e) {
      set({ recentLoaded: false });
      pushErrorToast(String(e));
    }
  },

  draw: async ({ rootPath, projectName, kind, topic, language }) => {
    if (get().runs[rootPath]) return null;
    const runId = newRunId("archify");
    set({ runs: { ...get().runs, [rootPath]: { runId, kind } } });
    useAiRunStore.getState().start(runId);

    const recent = pushTopic(get().recent, kind, topic);
    if (recent !== get().recent) {
      set({ recent });
      // A topic that could not be remembered costs the next dialog one chip, not this diagram.
      void api.setSetting(RECENT_TOPICS_KEY, JSON.stringify(recent)).catch((e: unknown) => pushErrorToast(String(e)));
    }

    try {
      const drawn = await api.archifyGenerate({ rootPath, kind, topic: topic.trim(), runId, language });
      const path = await saveArchifyDocument(rootPath, drawn.title || projectName, drawn.diagram_type, drawn.document);
      useToastStore.getState().pushToast(translate("codemap.map.ai.saved", { path }), "success");
      showArchifyDocument(path);
      return path;
    } catch (e) {
      // A stop is the user's own decision, not a failure to report.
      if (!isCancellation(e)) pushErrorToast(e instanceof Error ? e.message : String(e));
      return null;
    } finally {
      useAiRunStore.getState().finish(runId);
      const { [rootPath]: _done, ...others } = get().runs;
      set({ runs: others });
    }
  },
}));
