// @vitest-environment jsdom
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { act, cleanup, render, screen } from "@testing-library/react";
import { translate } from "../../state/languageStore";
import type { ArchifyRendered, Project } from "../../types/domain";

vi.mock("../../lib/monacoEditor", () => ({ Editor: () => null, OVERFLOW_SAFE_OPTIONS: {}, monaco: {} }));
vi.mock("../../lib/ipc/apiCommands", () => ({ apiPickFile: vi.fn(), apiReadTextFile: vi.fn() }));
vi.mock("../../lib/ipc/commands", () => ({
  diagramListDocuments: vi.fn(() => Promise.resolve(["codemap/shop-class.archify.json"])),
  archifyStatus: vi.fn(() => Promise.resolve({ installed: true, version: "3.1.0", has_class: true, install: "" })),
  archifyRender: vi.fn(
    (_root: string, path: string): Promise<ArchifyRendered> =>
      Promise.resolve({ view_url: `/archify-view/${path.includes("sequence") ? "b" : "a"}.html`, html_path: "/data/x.html", report: "" }),
  ),
}));

const api = vi.mocked(await import("../../lib/ipc/commands"));
const { useDiagramStore } = await import("../../state/diagramStore");
const { useArchifyStore } = await import("../../state/archifyStore");
const { useWorkspaceStore } = await import("../../state/workspaceStore");
const { DiagramView } = await import("./DiagramView");

const project = { id: "p1", workspace_id: "w1", name: "shop", local_path: "/repo" } as Project; // only what the view reads

beforeEach(() => {
  vi.clearAllMocks();
  useDiagramStore.getState().reset();
  useArchifyStore.getState().close();
  useWorkspaceStore.setState({ activeWorkspaceId: "w1", activeProjectId: "p1", projectsByWorkspace: { w1: [project] } });
});

afterEach(() => {
  cleanup();
});

describe("DiagramView", () => {
  it("opens a document another module hands over while the view is already mounted", async () => {
    useDiagramStore.getState().openWhenShown("codemap/shop-class.archify.json");
    render(<DiagramView />);
    await vi.waitFor(() => expect(useArchifyStore.getState().page?.view_url).toBe("/archify-view/a.html"));

    // The code map writes a second document after this first visit, and hands it over.
    api.diagramListDocuments.mockResolvedValue(["codemap/shop-class.archify.json", "codemap/checkout-sequence.archify.json"]);
    act(() => useDiagramStore.getState().openWhenShown("codemap/checkout-sequence.archify.json"));

    await vi.waitFor(() => expect(useArchifyStore.getState().path).toBe("codemap/checkout-sequence.archify.json"));
    await vi.waitFor(() => expect(screen.getByTitle(translate("diagram.archify.frameTitle")).getAttribute("src")).toContain("/archify-view/b.html"));
    expect(useDiagramStore.getState().pendingOpen).toBeNull();
    expect(useDiagramStore.getState().documents).toContain("codemap/checkout-sequence.archify.json");
  });
});
