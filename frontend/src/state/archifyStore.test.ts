import { beforeEach, describe, expect, test, vi } from "vitest";
import type { ArchifyRendered, ArchifyStatus } from "../types/domain";

const installed: ArchifyStatus = { installed: true, version: "3.1.0", has_class: true, install: "npx skills add tt-a1i/archify -g" };
const page: ArchifyRendered = { view_url: "/archify-view/abc.html", html_path: "/data/view/abc.html", report: "{}" };

const archifyStatus = vi.fn((_root: string) => Promise.resolve(installed));
const archifyRender = vi.fn((_root: string, _path: string) => Promise.resolve(page));
const files = new Map<string, string>();
const readFileText = vi.fn((_root: string, path: string) => Promise.resolve(files.get(path) ?? ""));
const writeFileText = vi.fn((_root: string, path: string, text: string) => {
  files.set(path, text);
  return Promise.resolve();
});
const archifyRefine = vi.fn((_request: { rootPath: string; path: string; instruction: string; runId: string; language: string }) =>
  Promise.resolve({ document: '{"v":2}', title: "Shop", diagram_type: "architecture", rounds: 0 }),
);

vi.mock("../lib/ipc/commands", () => ({
  archifyStatus: (root: string) => archifyStatus(root),
  archifyRender: (root: string, path: string) => archifyRender(root, path),
  readFileText: (root: string, path: string) => readFileText(root, path),
  writeFileText: (root: string, path: string, text: string) => writeFileText(root, path, text),
  archifyRefine: (request: { rootPath: string; path: string; instruction: string; runId: string; language: string }) => archifyRefine(request),
}));

const { useArchifyStore } = await import("./archifyStore");

beforeEach(() => {
  useArchifyStore.getState().close();
  archifyStatus.mockClear();
  archifyRender.mockClear();
  archifyRefine.mockClear();
  writeFileText.mockClear();
  files.clear();
});

describe("archifyStore", () => {
  test("draws the document it opens", async () => {
    await useArchifyStore.getState().open("/repo", "codemap/shop-class.archify.json");
    expect(archifyRender).toHaveBeenCalledWith("/repo", "codemap/shop-class.archify.json");
    expect(useArchifyStore.getState().page).toEqual(page);
  });

  test("asks for nothing to be drawn when Archify is not installed, and says how to install it", async () => {
    archifyStatus.mockResolvedValueOnce({ ...installed, installed: false, version: "" });
    await useArchifyStore.getState().open("/repo", "a.archify.json");
    expect(archifyRender).not.toHaveBeenCalled();
    expect(useArchifyStore.getState().status?.install).toContain("npx skills add");
  });

  test("keeps Archify's own complaint when it cannot draw the document", async () => {
    archifyRender.mockRejectedValueOnce(new Error("archify could not render the diagram: bad id"));
    await useArchifyStore.getState().open("/repo", "a.archify.json");
    expect(useArchifyStore.getState().error).toBe("archify could not render the diagram: bad id");
    expect(useArchifyStore.getState().page).toBeNull();
  });

  test("a document closed while it was being drawn is not shown", async () => {
    let release: (value: ArchifyRendered) => void = () => {};
    archifyRender.mockImplementationOnce(() => new Promise((resolve) => (release = resolve)));
    const opening = useArchifyStore.getState().open("/repo", "a.archify.json");
    await Promise.resolve();
    useArchifyStore.getState().close();
    release(page);
    await opening;
    expect(useArchifyStore.getState().page).toBeNull();
    expect(useArchifyStore.getState().path).toBeNull();
  });
});

describe("adjusting with AI", () => {
  const path = "codemap/shop-architecture.archify.json";

  test("writes the version Archify accepted, draws it, and one undo brings the old one back", async () => {
    files.set(path, '{"v":1}');
    await useArchifyStore.getState().open("/repo", path);
    archifyRender.mockClear();

    expect(await useArchifyStore.getState().refine("  add the cache  ", "es")).toBe(true);
    expect(archifyRefine.mock.calls[0]![0]).toMatchObject({ rootPath: "/repo", path, instruction: "add the cache", language: "es" });
    expect(files.get(path)).toBe('{"v":2}');
    expect(useArchifyStore.getState().previous).toBe('{"v":1}');
    expect(useArchifyStore.getState().refineRunId).toBeNull();
    expect(archifyRender).toHaveBeenCalledTimes(1);

    await useArchifyStore.getState().undo();
    expect(files.get(path)).toBe('{"v":1}');
    expect(useArchifyStore.getState().previous).toBeNull();
    expect(archifyRender).toHaveBeenCalledTimes(2);
  });

  test("a refusal reaches the caller and leaves the document as it was", async () => {
    files.set(path, '{"v":1}');
    await useArchifyStore.getState().open("/repo", path);
    archifyRefine.mockRejectedValueOnce(new Error("archify would not draw this diagram: /meta"));
    await expect(useArchifyStore.getState().refine("break it", "en")).rejects.toThrow("/meta");
    expect(files.get(path)).toBe('{"v":1}');
    expect(useArchifyStore.getState().previous).toBeNull();
    expect(useArchifyStore.getState().refineRunId).toBeNull();
  });

  test("an answer for a document the user left is not written over the one now open", async () => {
    files.set(path, '{"v":1}');
    await useArchifyStore.getState().open("/repo", path);
    let answer: (value: { document: string; title: string; diagram_type: string; rounds: number }) => void = () => {};
    archifyRefine.mockImplementationOnce(() => new Promise((resolve) => (answer = resolve)));
    const refining = useArchifyStore.getState().refine("x", "en");
    await vi.waitFor(() => expect(archifyRefine).toHaveBeenCalled());
    await useArchifyStore.getState().open("/repo", "codemap/other.archify.json");
    answer({ document: '{"v":9}', title: "", diagram_type: "architecture", rounds: 0 });
    expect(await refining).toBe(false);
    expect(writeFileText).not.toHaveBeenCalled();
  });

  test("asks nothing for an empty instruction", async () => {
    await useArchifyStore.getState().open("/repo", path);
    expect(await useArchifyStore.getState().refine("   ", "en")).toBe(false);
    expect(archifyRefine).not.toHaveBeenCalled();
  });
});
