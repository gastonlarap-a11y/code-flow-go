import { create } from "zustand";
import * as api from "../lib/ipc/commands";
import type { RequirementTool } from "../types/domain";

/**
 * What of the app's requirements this machine has (REQ-002): Git, Node.js, Go and Archify. The AI
 * engines' own status lives in `providerStatusStore`, which the requirements view reads beside this.
 */
interface RequirementsState {
  tools: RequirementTool[] | null;
  checking: boolean;
  error: string | null;
  /** Looks again — after installing something, the person presses it rather than restarting. */
  check: (rootPath?: string) => Promise<void>;
  toolById: (id: RequirementTool["id"]) => RequirementTool | undefined;
}

let checkSeq = 0;

export const useRequirementsStore = create<RequirementsState>((set, get) => ({
  tools: null,
  checking: false,
  error: null,

  check: async (rootPath) => {
    const seq = ++checkSeq;
    set({ checking: true, error: null });
    try {
      const tools = await api.requirementsStatus(rootPath);
      if (seq === checkSeq) set({ tools, checking: false });
    } catch (e) {
      if (seq === checkSeq) set({ checking: false, error: e instanceof Error ? e.message : String(e) });
    }
  },

  toolById: (id) => get().tools?.find((tool) => tool.id === id),
}));
