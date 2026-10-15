// @vitest-environment jsdom
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { cleanup, render, screen, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { translate } from "../../../state/languageStore";
import type { RepoMapNode, RepoMapView } from "../../../types/domain";

function card(id: string, folder: boolean, importedBy = 0): RepoMapNode {
  return {
    id,
    label: id.slice(id.lastIndexOf("/") + 1),
    folder,
    files: folder ? 3 : 1,
    declarations: 4,
    lines: 40,
    language: "TypeScript",
    imported_by: importedBy,
    group: id.includes("/") || folder ? id.split("/")[0]! : "",
  };
}

function view(focus: string, nodes: RepoMapNode[]): RepoMapView {
  return {
    graph: { focus, nodes, edges: [{ from: "src/app.ts", to: "src/lib", weight: 2 }], files: 12, declarations: 30, languages: [{ language: "TypeScript", files: 12 }] },
    build: { files: 12, parsed: 0, millis: 3, grammars: true, truncated: 0, git: true },
  };
}

vi.mock("../../../lib/ipc/commands", () => ({
  repomapProject: vi.fn(() => Promise.resolve(view("", [card("src/app.ts", false), card("src/lib", true, 2), card("README.ts", false)]))),
  repomapFolder: vi.fn((_root: string, focus: string) => Promise.resolve(view(focus, [card(`${focus}/cart.ts`, false, 1)]))),
  repomapMostUsed: vi.fn(() =>
    Promise.resolve([{ path: "src/lib/price.ts", name: "priceOf", kind: "function", line: 3, end_line: 9, signature: "export function priceOf()", parent: "", used_by: 5 }]),
  ),
  repomapFile: vi.fn((_root: string, path: string) =>
    Promise.resolve({
      path,
      language: "TypeScript",
      lines: 40,
      parsed: true,
      declarations: [
        { name: "Cart", kind: "class", line: 2, end_line: 30, signature: "export class Cart", depth: 0 },
        { name: "total", kind: "method", line: 8, end_line: 12, signature: "total(): number", depth: 1 },
      ],
      imports: ["src/lib/price.ts"],
      imported_by: [],
      external: ["react"],
    }),
  ),
  repomapSearch: vi.fn(() =>
    Promise.resolve([{ path: "src/cart.ts", name: "total", kind: "method", line: 8, end_line: 12, signature: "total(): number", parent: "Cart", used_by: 2 }]),
  ),
  archifyClassDiagram: vi.fn(() => Promise.resolve({ document: '{"diagram_type":"class"}', title: "lib — classes", types: 3, omitted: 0 })),
  diagramListDocuments: vi.fn(() => Promise.resolve(["codemap/lib-class.archify.json"])),
  createFile: vi.fn(() => Promise.resolve()),
  writeFileText: vi.fn(() => Promise.resolve()),
  repomapUsages: vi.fn(() =>
    Promise.resolve({
      declarations: [{ path: "src/cart.ts", name: "total", kind: "method", line: 8, end_line: 12, signature: "total(): number", parent: "Cart", used_by: 2 }],
      usages: [
        { path: "src/page.ts", line: 14, within: "function Checkout()", confirmed: true },
        { path: "legacy/old.js", line: 3, within: "", confirmed: false },
      ],
      excluded: 2,
    }),
  ),
}));

const api = vi.mocked(await import("../../../lib/ipc/commands"));
const { useRepoMapStore } = await import("../../../state/repomapStore");
const { useUiStore } = await import("../../../state/uiStore");
const { MapView } = await import("./MapView");

const t = translate;

/** A card on the canvas, by the path its accessible name starts with. */
const findCard = (id: string) =>
  screen.findByRole("button", { name: new RegExp(`^${id.replace(/[.*+?^${}()|[\]\\/]/g, "\\$&")} — `) });

beforeEach(() => {
  vi.clearAllMocks();
  useRepoMapStore.getState().reset();
  useUiStore.setState({ activeView: "codemap", pendingEditorPath: null, pendingEditorLine: null });
});

afterEach(() => {
  cleanup();
});

describe("MapView", () => {
  it("draws the whole project and what is used most", async () => {
    render(<MapView rootPath="/repo" projectName="shop" />);

    expect(await findCard("src/lib")).toBeTruthy();
    expect(await findCard("src/app.ts")).toBeTruthy();
    expect(api.repomapProject).toHaveBeenCalledWith("/repo");
    expect(screen.getByText(t("codemap.map.stats", { files: "12", declarations: "30" }))).toBeTruthy();
    expect(await screen.findByText("priceOf")).toBeTruthy();
  });

  it("shows a file's outline when its card is picked, and opens a declaration at its line", async () => {
    render(<MapView rootPath="/repo" projectName="shop" />);
    await userEvent.click(await findCard("src/app.ts"));

    expect(api.repomapFile).toHaveBeenCalledWith("/repo", "src/app.ts");
    await userEvent.click(await screen.findByText("total"));
    expect(useUiStore.getState().pendingEditorPath).toBe("src/app.ts");
    expect(useUiStore.getState().pendingEditorLine).toBe(8);
    expect(screen.getByText("react")).toBeTruthy();
  });

  it("opens a folder on double-click, in the folder view with its path to climb back", async () => {
    render(<MapView rootPath="/repo" projectName="shop" />);
    await userEvent.dblClick(await findCard("src/lib"));

    expect(api.repomapFolder).toHaveBeenCalledWith("/repo", "src/lib");
    expect(await findCard("src/lib/cart.ts")).toBeTruthy();
    const crumbs = screen.getByRole("navigation", { name: t("codemap.map.root") });
    expect(within(crumbs).getByText("shop")).toBeTruthy();
    expect(within(crumbs).getByText("lib")).toBeTruthy();
  });

  it("searches on Enter and lists where a name is used, confirmed first", async () => {
    render(<MapView rootPath="/repo" projectName="shop" />);
    await findCard("src/lib");

    await userEvent.type(screen.getByLabelText(t("codemap.map.searchLabel")), "total{Enter}");
    expect(api.repomapSearch).toHaveBeenCalledWith("/repo", "total");
    expect(await screen.findByText("Cart.total")).toBeTruthy();

    await userEvent.click(screen.getAllByRole("button", { name: t("codemap.map.usages") })[0]!);
    expect(api.repomapUsages).toHaveBeenCalledWith("/repo", "Cart.total", "src/cart.ts");
    expect(await screen.findByText("src/page.ts:14")).toBeTruthy();
    expect(screen.getByText(t("codemap.map.nameOnly"))).toBeTruthy();
    expect(screen.getByText(t("codemap.map.excluded", { count: 2, name: "Cart.total" }))).toBeTruthy();
  });

  it("keeps a crowded folder readable: the rest wait in the panel", async () => {
    const many = Array.from({ length: 34 }, (_, i) => card(`src/lib/f${String(i).padStart(2, "0")}.ts`, false));
    api.repomapFolder.mockResolvedValueOnce(view("src/lib", many));
    render(<MapView rootPath="/repo" projectName="shop" />);
    await userEvent.dblClick(await findCard("src/lib"));

    const more = await screen.findByText(t("codemap.map.more", { count: 4 }));
    expect(screen.queryByText("f33.ts")).toBeNull();
    await userEvent.click(more);
    expect(await screen.findByText("f33.ts")).toBeTruthy();
  });

  it("draws the picked folder's classes, saves them beside the flows, and opens them in the diagram editor", async () => {
    const { useDiagramStore } = await import("../../../state/diagramStore");
    render(<MapView rootPath="/repo" projectName="shop" />);
    await userEvent.click(await findCard("src/lib"));
    await userEvent.click(screen.getByRole("button", { name: t("codemap.map.classDiagram", { scope: "src/lib" }) }));

    expect(api.archifyClassDiagram).toHaveBeenCalledWith("/repo", "src/lib", "en");
    // `lib-class` is taken, so the next free name.
    expect(api.createFile).toHaveBeenCalledWith("/repo", "codemap/lib-class-2.archify.json");
    expect(api.writeFileText).toHaveBeenCalledWith("/repo", "codemap/lib-class-2.archify.json", '{"diagram_type":"class"}');
    expect(useDiagramStore.getState().pendingOpen).toBe("codemap/lib-class-2.archify.json");
    expect(useUiStore.getState().activeView).toBe("diagram");
  });

  it("says why when the folder cannot be read", async () => {
    api.repomapProject.mockRejectedValueOnce(new Error("not a folder on this machine"));
    render(<MapView rootPath="/gone" projectName="gone" />);

    expect(await screen.findByText(t("codemap.map.failed"))).toBeTruthy();
    expect(screen.getByText("not a folder on this machine")).toBeTruthy();
  });
});
