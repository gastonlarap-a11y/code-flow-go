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
  codemapAsk: vi.fn(() => Promise.resolve({ answer: "It saves an item.", session_id: "s1" })),
  codemapArchify: vi.fn(() =>
    Promise.resolve({ html_path: "/data/codemap/archify/repo/Save-flow.html", diagram_type: "sequence", report: "{}" }),
  ),
  diagramListDocuments: vi.fn(() => Promise.resolve<string[]>([])),
  createFile: vi.fn(() => Promise.resolve(undefined)),
  writeFileText: vi.fn(() => Promise.resolve(undefined)),
}));

const toasts: string[] = [];
vi.mock("./toastStore", () => ({
  pushErrorToast: (message: string) => toasts.push(message),
}));

const api = vi.mocked(await import("../lib/ipc/commands"));
const { useCodemapStore } = await import("./codemapStore");
const { useDiagramStore } = await import("./diagramStore");
const { useUiStore } = await import("./uiStore");

const diagramLabels = {
  lanes: { ts: "Frontend", go: "Go", sql: "Database" },
  edge: (kind: string) => kind,
  cut: (shown: number, total: number) => `${shown}/${total}`,
};

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

  test("asking about a symbol sends it with the question, and a follow-up continues the conversation", async () => {
    await useCodemapStore.getState().open("/repo");
    await useCodemapStore.getState().select("go:app.Save");

    await useCodemapStore.getState().ask("  How does it work?  ", "es");
    const first = api.codemapAsk.mock.calls[0]![0];
    expect(first).toMatchObject({ rootPath: "/repo", question: "How does it work?", symbol: "go:app.Save", sessionId: "", language: "es" });
    expect(first.runId).toMatch(/^codemap/);
    expect(useCodemapStore.getState().exchanges).toEqual([{ question: "How does it work?", answer: "It saves an item.", error: null }]);
    expect(useCodemapStore.getState().askRunId).toBeNull();

    await useCodemapStore.getState().ask("And its tests?", "es");
    expect(api.codemapAsk.mock.calls[1]![0].sessionId).toBe("s1");
    expect(useCodemapStore.getState().exchanges).toHaveLength(2);
  });

  test("a failed answer says why; a stopped one is not an error", async () => {
    await useCodemapStore.getState().open("/repo");
    await useCodemapStore.getState().select("go:app.Save");

    api.codemapAsk.mockRejectedValueOnce(new Error("claude: not logged in"));
    await useCodemapStore.getState().ask("Why?", "en");
    expect(useCodemapStore.getState().exchanges[0]!.error).toContain("not logged in");

    api.codemapAsk.mockRejectedValueOnce(new Error("RUN_CANCELLED:: stopped by the user"));
    await useCodemapStore.getState().ask("Again?", "en");
    expect(useCodemapStore.getState().exchanges[1]).toMatchObject({ error: null, answer: "" });
  });

  test("one question at a time, and nothing without a symbol", async () => {
    await useCodemapStore.getState().open("/repo");
    await useCodemapStore.getState().ask("Orphan?", "en");
    expect(api.codemapAsk).not.toHaveBeenCalled();

    await useCodemapStore.getState().select("go:app.Save");
    const slow = deferred<{ answer: string; session_id: string }>();
    api.codemapAsk.mockReturnValueOnce(slow.promise);
    const first = useCodemapStore.getState().ask("First", "en");
    await useCodemapStore.getState().ask("Second", "en");
    slow.release({ answer: "done", session_id: "" });
    await first;

    expect(api.codemapAsk).toHaveBeenCalledTimes(1);
    expect(useCodemapStore.getState().exchanges.map((e) => e.question)).toEqual(["First"]);
  });

  test("the conversation starts over with each symbol", async () => {
    await useCodemapStore.getState().open("/repo");
    await useCodemapStore.getState().select("go:app.Save");
    await useCodemapStore.getState().ask("How?", "en");

    await useCodemapStore.getState().select("go:app.Other");

    expect(useCodemapStore.getState().exchanges).toEqual([]);
    expect(useCodemapStore.getState().askSession).toBe("");
  });

  test("a flow becomes a diagram the editor opens, and never overwrites one", async () => {
    await useCodemapStore.getState().open("/repo");
    await useCodemapStore.getState().select("go:app/shop.Save");
    await useCodemapStore.getState().setTab("flow");
    api.diagramListDocuments.mockResolvedValue(["codemap/Save-flow.mmd"]);

    const written = await useCodemapStore.getState().drawFlow("Save — flow", diagramLabels);

    expect(written).toBe("codemap/Save-flow-2.mmd");
    expect(api.createFile).toHaveBeenCalledWith("/repo", "codemap/Save-flow-2.mmd");
    const source = api.writeFileText.mock.calls[0]![2];
    expect(source).toMatch(/^flowchart TD/);
    expect(source).toContain('subgraph lane_go ["Go"]');
    expect(useDiagramStore.getState().pendingOpen).toBe("codemap/Save-flow-2.mmd");
    expect(useUiStore.getState().activeView).toBe("diagram");
    api.diagramListDocuments.mockResolvedValue([]);
  });

  test("no flow on screen, no diagram", async () => {
    await useCodemapStore.getState().open("/repo");
    await useCodemapStore.getState().select("go:app/shop.Save");

    expect(await useCodemapStore.getState().drawFlow("t", diagramLabels)).toBeNull();
    expect(api.createFile).not.toHaveBeenCalled();
  });

  test("Archify draws the selected symbol's flow, or with no symbol the architecture", async () => {
    await useCodemapStore.getState().open("/repo");
    await useCodemapStore.getState().select("go:app/shop.Save");

    expect(await useCodemapStore.getState().archify("flow", "es")).toBe("/data/codemap/archify/repo/Save-flow.html");
    expect(api.codemapArchify).toHaveBeenLastCalledWith("/repo", { symbol: "go:app/shop.Save", language: "es" });
    await useCodemapStore.getState().archify("architecture", "en");
    expect(api.codemapArchify).toHaveBeenLastCalledWith("/repo", { language: "en" });
    expect(useCodemapStore.getState().archifying).toBe(false);
  });

  test("an Archify failure is shown, and a second click while it renders is ignored", async () => {
    await useCodemapStore.getState().open("/repo");
    await useCodemapStore.getState().select("go:app/shop.Save");
    const slow = deferred<{ html_path: string; diagram_type: "sequence"; report: string }>();
    api.codemapArchify.mockReturnValueOnce(slow.promise);

    const first = useCodemapStore.getState().archify("flow", "en");
    expect(await useCodemapStore.getState().archify("flow", "en")).toBeNull();
    slow.release({ html_path: "/a.html", diagram_type: "sequence", report: "" });
    await first;
    expect(api.codemapArchify).toHaveBeenCalledTimes(1);

    api.codemapArchify.mockRejectedValueOnce(new Error("archify could not render the diagram: bad source"));
    expect(await useCodemapStore.getState().archify("flow", "en")).toBeNull();
    expect(toasts).toEqual(["Error: archify could not render the diagram: bad source"]);
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
