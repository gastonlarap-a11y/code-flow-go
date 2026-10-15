// @vitest-environment jsdom
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { cleanup, render, screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { detail, flow, impact, overview, symbolRef } from "../../lib/codemap/fixtures";
import { translate } from "../../state/languageStore";
import type { Project } from "../../types/domain";

// The view reaches Go through these two modules; nothing here should, so every call is a stub.
vi.mock("../../lib/ipc/commands", () => ({
  codemapOverview: vi.fn(() => Promise.resolve(overview())),
  codemapSearch: vi.fn(() => Promise.resolve([symbolRef("go:app/shop.Save", { name: "shop.Save", file: "shop/save.go", line: 4 })])),
  codemapSymbol: vi.fn((_root: string, symbol: string) => Promise.resolve({ detail: detail(symbol), candidates: [] })),
  codemapFlow: vi.fn((_root: string, symbol: string) => Promise.resolve(flow(symbol))),
  codemapImpact: vi.fn((_root: string, symbol: string) => Promise.resolve(impact(symbol))),
  codemapAsk: vi.fn(() => Promise.resolve({ answer: "It **saves** one item, through `Save`.", session_id: "s1" })),
  codemapArchify: vi.fn(() => Promise.resolve({ html_path: "/out/Save-flow.html", diagram_type: "sequence", report: "" })),
  cancelAiRun: vi.fn(() => Promise.resolve()),
  repomapProject: vi.fn(() =>
    Promise.resolve({
      graph: { focus: "", nodes: [], edges: [], files: 0, declarations: 0, languages: [] },
      build: { files: 0, parsed: 0, millis: 1, grammars: true, truncated: 0, git: true },
    }),
  ),
  repomapFolder: vi.fn(),
  repomapMostUsed: vi.fn(() => Promise.resolve([])),
}));
vi.mock("../../lib/ipc/events", () => ({
  onCodemapProgress: vi.fn(() => Promise.resolve(() => {})),
  onAiOutput: vi.fn(() => Promise.resolve(() => {})),
}));
vi.mock("../../lib/ui/useCopy", () => ({
  useCopy: () => [false, copied] as const,
  copyText: vi.fn(),
}));
const copied = vi.fn();

const api = vi.mocked(await import("../../lib/ipc/commands"));
const { useWorkspaceStore } = await import("../../state/workspaceStore");
const { useUiStore } = await import("../../state/uiStore");
const { useCodemapStore } = await import("../../state/codemapStore");
const { CodemapView } = await import("./CodemapView");

const t = translate;

function withProject(path: string | null) {
  const project = path === null ? null : ({ id: "p1", name: "shop", local_path: path } as unknown as Project); // a test stand-in carrying only what the view reads
  useWorkspaceStore.setState({ activeProject: () => project });
}

beforeEach(() => {
  vi.clearAllMocks();
  useCodemapStore.getState().reset();
  // These cases are about the symbol view; the drawn map, where the module opens, has its own.
  useCodemapStore.setState({ mode: "symbols" });
  useUiStore.setState({ activeView: "codemap", pendingEditorPath: null, pendingEditorLine: null });
});

afterEach(() => {
  cleanup();
});

describe("CodemapView", () => {
  it("asks for a project before anything else", () => {
    withProject(null);
    render(<CodemapView />);

    expect(screen.getByText(t("codemap.noProject"))).toBeTruthy();
    expect(api.codemapOverview).not.toHaveBeenCalled();
  });

  it("opens on the drawn map, which needs no toolchain, and builds the symbols only when asked", async () => {
    withProject("/repo");
    useCodemapStore.setState({ mode: "map" });
    render(<CodemapView />);

    expect(await screen.findByText(t("codemap.map.empty"))).toBeTruthy();
    expect(api.repomapProject).toHaveBeenCalledWith("/repo");
    expect(api.codemapOverview).not.toHaveBeenCalled();

    await userEvent.click(screen.getByRole("tab", { name: t("codemap.view.symbols") }));
    expect(await screen.findByText(t("codemap.hotspots"))).toBeTruthy();
    expect(api.codemapOverview).toHaveBeenCalledWith("/repo");
  });

  it("builds the map and shows where to start", async () => {
    withProject("/repo");
    render(<CodemapView />);

    expect(await screen.findByText(t("codemap.hotspots"))).toBeTruthy();
    expect(api.codemapOverview).toHaveBeenCalledWith("/repo");
    expect(screen.getByText("invoke")).toBeTruthy();
    expect(screen.getByText(/120 symbols/)).toBeTruthy();
  });

  it("says what is missing when a toolchain is not installed", async () => {
    withProject("/repo");
    const base = overview();
    api.codemapOverview.mockResolvedValueOnce({
      ...base,
      status: {
        ...base.status,
        languages: [{ lang: "go", state: "missing_tool", files: 4, symbols: 0, detail: "Go was not found" }],
      },
    });
    render(<CodemapView />);

    expect(await screen.findByText("Go was not found")).toBeTruthy();
  });

  it("searches, opens a symbol, and walks its flow and impact", async () => {
    const user = userEvent.setup();
    withProject("/repo");
    render(<CodemapView />);
    await screen.findByText(t("codemap.hotspots"));

    await user.type(screen.getByLabelText(t("codemap.searchLabel")), "save");
    const result = await screen.findByRole("button", { name: `${t("codemap.kind.func")} shop.Save` });
    await user.click(result);

    // The detail: its caller, and the table it reaches through Go.
    expect(await screen.findByRole("heading", { name: "shop.Save" })).toBeTruthy();
    expect(screen.getByText("Saves one item.")).toBeTruthy();
    expect(screen.getByText("List")).toBeTruthy();

    await user.click(screen.getByRole("tab", { name: t("codemap.tab.flow") }));
    const tree = await screen.findByRole("tree");
    expect(within(tree).getByText(t("codemap.edge.cross"))).toBeTruthy();
    expect(within(tree).getByText("items")).toBeTruthy();

    await user.click(screen.getByRole("tab", { name: t("codemap.tab.impact") }));
    expect(await screen.findByText("go test -run '^(TestSave)$' ./shop")).toBeTruthy();
    await user.click(screen.getAllByRole("button", { name: t("codemap.copyCommand") })[0]!);
    expect(copied).toHaveBeenCalledWith("go test -run '^(TestSave)$' ./shop");
  });

  it("opens a symbol's file at its line in the editor", async () => {
    const user = userEvent.setup();
    withProject("/repo");
    render(<CodemapView />);
    await screen.findByText(t("codemap.hotspots"));

    await user.click(screen.getByRole("button", { name: t("codemap.openInEditor", { location: "src/app.ts:3" }) }));

    expect(useUiStore.getState().activeView).toBe("editor");
    expect(useUiStore.getState().pendingEditorPath).toBe("src/app.ts");
    expect(useUiStore.getState().pendingEditorLine).toBe(3);
  });

  it("asks the AI about the symbol and shows its answer", async () => {
    const user = userEvent.setup();
    withProject("/repo");
    render(<CodemapView />);
    await screen.findByText(t("codemap.hotspots"));
    await user.click(screen.getByRole("button", { name: `${t("codemap.kind.func")} invoke` }));
    await screen.findByRole("heading", { name: "invoke" });

    await user.click(screen.getByRole("tab", { name: t("codemap.tab.ask") }));
    await user.click(screen.getByRole("button", { name: t("codemap.ask.suggestHow") }));

    const answer = await screen.findByText("saves");
    expect(answer.tagName).toBe("STRONG");
    expect(api.codemapAsk).toHaveBeenCalledWith(
      expect.objectContaining({ rootPath: "/repo", symbol: "ts:src/api.ts#invoke", question: t("codemap.ask.suggestHow") }),
    );
    expect(screen.getByText(t("codemap.ask.suggestHow"))).toBeTruthy();
    expect(screen.getByLabelText(t("codemap.ask.label"))).toBeTruthy();
  });

  it("offers Archify only when it is installed", async () => {
    const user = userEvent.setup();
    withProject("/repo");
    const { unmount } = render(<CodemapView />);
    await screen.findByText(t("codemap.hotspots"));
    await user.click(screen.getByRole("button", { name: `${t("codemap.kind.func")} invoke` }));
    await user.click(await screen.findByRole("tab", { name: t("codemap.tab.flow") }));
    await screen.findByRole("tree");

    expect(screen.queryByRole("button", { name: t("codemap.archify.flow") })).toBeNull();
    expect(screen.queryByRole("button", { name: t("codemap.archify.architecture") })).toBeNull();
    unmount();

    const base = overview();
    api.codemapOverview.mockResolvedValueOnce({ ...base, status: { ...base.status, archify: "3.1.0" } });
    useCodemapStore.getState().reset();
    render(<CodemapView />);
    await screen.findByText(t("codemap.hotspots"));
    await user.click(screen.getByRole("button", { name: t("codemap.archify.architecture") }));
    expect(api.codemapArchify).toHaveBeenLastCalledWith("/repo", { language: expect.any(String) });

    await user.click(screen.getByRole("button", { name: `${t("codemap.kind.func")} invoke` }));
    await user.click(await screen.findByRole("tab", { name: t("codemap.tab.flow") }));
    await screen.findByRole("tree");
    await user.click(screen.getByRole("button", { name: t("codemap.archify.flow") }));
    expect(api.codemapArchify).toHaveBeenLastCalledWith("/repo", { symbol: "ts:src/api.ts#invoke", language: expect.any(String) });
  });

  it("goes back to the symbol it came from", async () => {
    const user = userEvent.setup();
    withProject("/repo");
    render(<CodemapView />);
    await screen.findByText(t("codemap.hotspots"));

    await user.click(screen.getByRole("button", { name: `${t("codemap.kind.func")} invoke` }));
    await screen.findByRole("heading", { name: "invoke" });
    await user.click(screen.getByRole("button", { name: `${t("codemap.kind.component")} List` }));
    await screen.findByRole("heading", { name: "List" });

    await user.click(screen.getByRole("button", { name: t("codemap.back") }));
    await waitFor(() => expect(screen.getByRole("heading", { name: "invoke" })).toBeTruthy());
  });
});
