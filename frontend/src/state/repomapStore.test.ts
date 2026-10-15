import { beforeEach, describe, expect, test, vi } from "vitest";
import type { RepoMapView } from "../types/domain";

/** A promise the test releases by hand, to hold one command in flight while another runs. */
function deferred<T>(): { promise: Promise<T>; release: (value: T) => void } {
  let release: (value: T) => void = () => {};
  const promise = new Promise<T>((resolve) => {
    release = resolve;
  });
  return { promise, release };
}

function view(focus: string, ids: string[] = []): RepoMapView {
  return {
    graph: {
      focus,
      nodes: ids.map((id) => ({ id, label: id, folder: false, files: 1, declarations: 0, lines: 1, language: "Go", imported_by: 0, group: "" })),
      edges: [],
      files: ids.length,
      declarations: 0,
      languages: [],
    },
    build: { files: ids.length, parsed: 0, millis: 1, grammars: true, truncated: 0, git: true },
  };
}

const repomapProject = vi.fn((_root: string) => Promise.resolve(view("", ["main.go"])));
const repomapFolder = vi.fn((_root: string, focus: string) => Promise.resolve(view(focus)));

vi.mock("../lib/ipc/commands", () => ({
  repomapProject: (root: string) => repomapProject(root),
  repomapFolder: (root: string, focus: string) => repomapFolder(root, focus),
}));

const { useRepoMapStore } = await import("./repomapStore");

beforeEach(() => {
  useRepoMapStore.getState().reset();
  repomapProject.mockClear();
  repomapFolder.mockClear();
});

describe("repomapStore", () => {
  test("opens a repository on the whole project", async () => {
    await useRepoMapStore.getState().open("/repo");
    const state = useRepoMapStore.getState();
    expect(state.mode).toBe("project");
    expect(state.view?.graph.nodes.map((n) => n.id)).toEqual(["main.go"]);
    expect(repomapProject).toHaveBeenCalledWith("/repo");
  });

  test("opening a folder switches to the folder view, and follows a lone subfolder", async () => {
    repomapFolder.mockImplementationOnce(() => Promise.resolve(view("src/app")));
    await useRepoMapStore.getState().open("/repo");
    await useRepoMapStore.getState().setFocus("src");
    const state = useRepoMapStore.getState();
    expect(state.mode).toBe("folders");
    expect(state.focus).toBe("src/app");
  });

  test("the shown view keeps the mode it was drawn for until the next one arrives", async () => {
    await useRepoMapStore.getState().open("/repo");
    const folders = deferred<RepoMapView>();
    repomapFolder.mockImplementationOnce(() => folders.promise);
    const switching = useRepoMapStore.getState().setMode("folders");
    expect(useRepoMapStore.getState().mode).toBe("folders");
    expect(useRepoMapStore.getState().viewMode).toBe("project");
    folders.release(view("", ["src"]));
    await switching;
    expect(useRepoMapStore.getState().viewMode).toBe("folders");
  });

  test("a slow answer for a folder left behind does not replace the one now shown", async () => {
    await useRepoMapStore.getState().open("/repo");
    const slow = deferred<RepoMapView>();
    repomapFolder.mockImplementationOnce(() => slow.promise);
    const first = useRepoMapStore.getState().setFocus("old");
    await useRepoMapStore.getState().setFocus("new");
    slow.release(view("old"));
    await first;
    expect(useRepoMapStore.getState().focus).toBe("new");
  });

  test("remembers how each repository was drawn, for the session", async () => {
    await useRepoMapStore.getState().open("/a");
    await useRepoMapStore.getState().setFocus("pkg");
    await useRepoMapStore.getState().open("/b");
    expect(useRepoMapStore.getState().mode).toBe("project");
    await useRepoMapStore.getState().open("/a");
    expect(useRepoMapStore.getState().focus).toBe("pkg");
    expect(useRepoMapStore.getState().mode).toBe("folders");
  });

  test("a failure is kept to say why, and cleared by the next answer", async () => {
    repomapProject.mockImplementationOnce(() => Promise.reject(new Error("not a folder on this machine")));
    await useRepoMapStore.getState().open("/gone");
    expect(useRepoMapStore.getState().error).toBe("not a folder on this machine");
    await useRepoMapStore.getState().refresh();
    expect(useRepoMapStore.getState().error).toBeNull();
  });
});
