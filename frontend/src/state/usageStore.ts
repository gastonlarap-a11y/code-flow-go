import { create } from "zustand";
import { usageSnapshot } from "../lib/ipc/commands";
import type { UsageSnapshot } from "../types/domain";

/**
 * What the usage indicator knows (USAGE-010).
 *
 * Polled rather than pushed: nothing on the Go side changes these numbers, they are measured when
 * asked, so there is no event to subscribe to. Two cadences, because measuring everything at one
 * frequency would spend battery to redraw a pill nobody is looking at:
 *
 *   - **Collapsed**, the resting state: slow, just often enough that the colour is not stale.
 *   - **Open**: fast, because CPU is a rate and a rate people are watching should move.
 *
 * And nothing at all while the window is unfocused. A background app measuring itself every few
 * seconds is the kind of thing people uninstall a tool over.
 *
 * **A failed read never interrupts.** It leaves the last good snapshot in place and raises no
 * toast: an indicator that pops an error because it could not measure itself is worse than one
 * that quietly says nothing.
 */
interface UsageState {
  snapshot: UsageSnapshot | null;
  /** True once a read has come back, so the pill can stay hidden until there is something to say. */
  loaded: boolean;
  /** True when the last read failed; the panel shows a dash rather than a stale number's meaning. */
  failed: boolean;
  refresh: () => Promise<void>;
  reset: () => void;
}

export const useUsageStore = create<UsageState>((set) => ({
  snapshot: null,
  loaded: false,
  failed: false,

  refresh: async () => {
    try {
      const snapshot = await usageSnapshot();
      set({ snapshot, loaded: true, failed: false });
    } catch {
      // Deliberately silent: see the note above. The previous snapshot stays on screen.
      set({ loaded: true, failed: true });
    }
  },

  reset: () => set({ snapshot: null, loaded: false, failed: false }),
}));

/** How often the indicator re-measures, by whether anybody is looking at it. */
export const POLL_COLLAPSED_MS = 60_000;
export const POLL_OPEN_MS = 4_000;
