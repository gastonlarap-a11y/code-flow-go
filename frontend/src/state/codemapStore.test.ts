import { beforeEach, describe, expect, test, vi } from "vitest";
import { detail, flow, impact, overview, symbolRef } from "../lib/codemap/fixtures";
import type { CodemapSymbolRef } from "../types/domain";

/** A promise the test releases by hand, to hold one command in flight while another runs. */
function deferred<T>(): { promise: Promise<T>; release: (value: T) => void } {
  let release: (value: T) => void = () => {};
  const promise = new Promise<T>((resolve) => {
    release = resolve;
  });
  return { promise, release };
}

vi.mock("../lib/ipc/commands", () => ({
  codemapOverview: vi.fn(() => Promise.resolve(overview())),
  codemapSearch: vi.fn(() => Promise.resolve([])),
  codemapSymbol: vi.fn((_root: string, symbol: string) => Promise.resolve({ detail: detail(symbol), candidates: [] })),
  codemapFlow: vi.fn((_root: string, symbol: string) => Promise.resolve(flow(symbol))),
  codemapImpact: vi.fn((_root: string, symbol: string) => Promise.resolve(impact(symbol))),
}));

const toasts: string[] = [];
vi.mock("./toastStore", () => ({
  pushErrorToast: (message: string) => toasts.push(message),
}));

const api = vi.mocked(await import("../lib/ipc/commands"));
const { useCodemapStore } = await import("./codemapStore");

beforeEach(() => {
  vi.clearAllMocks();
  toasts.length = 0;
  useCodemapStore.getState().reset();
});

describe("the code map's state", () => {
  test("opening a repository builds its map and lands on the overview", async () => {
    await useCodemapStore.getState().open("/repo");

    const state = useCodemapStore.getState();
    expect(api.codemapOverview).toHaveBeenCalledWith("/repo");
    expect(state.overview?.status.nodes).toBe(120);
    expect(state.building).toBe(false);
    expect(state.error).toBeNull();
  });

  test("a folder that cannot be mapped says why instead of spinning", async () => {
    api.codemapOverview.mockRejectedValueOnce(new Error("the repository folder does not exist"));

    await useCodemapStore.getState().open("/gone");

    expect(useCodemapStore.getState().error).toContain("does not exist");
    expect(useCodemapStore.getState().building).toBe(false);
  });

  // Typing fires a search per keystroke; a slow answer to "sa" must not replace the one to "save".
  test("a stale search answer is dropped", async () => {
    await useCodemapStore.getState().open("/repo");
    const slow = deferred<CodemapSymbolRef[]>();
    api.codemapSearch.mockReturnValueOnce(slow.promise);
    api.codemapSearch.mockResolvedValueOnce([symbolRef("go:app.Save")]);

    const first = useCodemapStore.getState().search("sa");
    await useCodemapStore.getState().search("save");
    slow.release([symbolRef("go:app.Sale")]);
    await first;

    expect(useCodemapStore.getState().results.map((r) => r.id)).toEqual(["go:app.Save"]);
    expect(useCodemapStore.getState().query).toBe("save");
  });

  test("an empty query clears the results without asking", async () => {
    await useCodemapStore.getState().open("/repo");
    await useCodemapStore.getState().search("   ");

    expect(api.codemapSearch).not.toHaveBeenCalled();
    expect(useCodemapStore.getState().results).toEqual([]);
  });

  test("selecting walks forward and back keeps the trail", async () => {
    await useCodemapStore.getState().open("/repo");

    await useCodemapStore.getState().select("go:app.A");
    await useCodemapStore.getState().select("go:app.B");
    expect(useCodemapStore.getState().trail).toEqual(["go:app.A"]);
    expect(useCodemapStore.getState().detail?.symbol.id).toBe("go:app.B");

    await useCodemapStore.getState().back();
    expect(useCodemapStore.getState().detail?.symbol.id).toBe("go:app.A");
    expect(useCodemapStore.getState().trail).toEqual([]);
  });

  test("a name that matches several symbols asks which one", async () => {
    await useCodemapStore.getState().open("/repo");
    api.codemapSymbol.mockResolvedValueOnce({ detail: null, candidates: [symbolRef("go:a.Save"), symbolRef("ts:b#save")] });

    await useCodemapStore.getState().select("save");

    expect(useCodemapStore.getState().detail).toBeNull();
    expect(useCodemapStore.getState().candidates).toHaveLength(2);
  });

  test("the flow and the impact load when their tab is opened, once", async () => {
    await useCodemapStore.getState().open("/repo");
    await useCodemapStore.getState().select("go:app.Save");
    expect(api.codemapFlow).not.toHaveBeenCalled();

    await useCodemapStore.getState().setTab("flow");
    expect(api.codemapFlow).toHaveBeenCalledWith("/repo", "go:app.Save", { dataOnly: false });
    expect(useCodemapStore.getState().flow?.root.children).toHaveLength(1);

    await useCodemapStore.getState().setTab("impact");
    await useCodemapStore.getState().setTab("flow");
    expect(api.codemapFlow).toHaveBeenCalledTimes(1);
    expect(useCodemapStore.getState().impact?.go_tests[0]?.tests).toEqual(["TestSave"]);
  });

  test("selecting a symbol with a tab open loads that tab for it", async () => {
    await useCodemapStore.getState().open("/repo");
    await useCodemapStore.getState().select("go:app.Save");
    await useCodemapStore.getState().setTab("impact");

    await useCodemapStore.getState().select("go:app.Other");
    await vi.waitFor(() => expect(useCodemapStore.getState().impact?.root.id).toBe("go:app.Other"));
  });

  test("only data asks for the flow again", async () => {
    await useCodemapStore.getState().open("/repo");
    await useCodemapStore.getState().select("go:app.Save");
    await useCodemapStore.getState().setTab("flow");

    await useCodemapStore.getState().setDataOnly(true);

    expect(api.codemapFlow).toHaveBeenLastCalledWith("/repo", "go:app.Save", { dataOnly: true });
  });

  test("a build's stage is shown until it is ready", () => {
    useCodemapStore.getState().setStage("typescript");
    expect(useCodemapStore.getState().stage).toBe("typescript");
    useCodemapStore.getState().setStage("ready");
    expect(useCodemapStore.getState().stage).toBeNull();
  });

  test("switching repository forgets the previous one's answers", async () => {
    await useCodemapStore.getState().open("/repo");
    await useCodemapStore.getState().select("go:app.Save");

    await useCodemapStore.getState().open("/other");

    expect(useCodemapStore.getState().detail).toBeNull();
    expect(useCodemapStore.getState().trail).toEqual([]);
    expect(useCodemapStore.getState().rootPath).toBe("/other");
  });
});
