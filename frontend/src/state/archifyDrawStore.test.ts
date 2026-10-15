import { beforeEach, describe, expect, test, vi } from "vitest";
import type { ArchifyDrawn } from "../types/domain";

const drawn: ArchifyDrawn = { document: '{"diagram_type":"sequence"}', title: "Checkout", diagram_type: "sequence", rounds: 1 };

vi.mock("../lib/ipc/commands", () => ({
  archifyGenerate: vi.fn(() => Promise.resolve(drawn)),
  getSetting: vi.fn(() => Promise.resolve(JSON.stringify({ sequence: ["login"] }))),
  setSetting: vi.fn(() => Promise.resolve()),
}));
vi.mock("../lib/archify/save", () => ({
  saveArchifyDocument: vi.fn(() => Promise.resolve("codemap/checkout-sequence.archify.json")),
  showArchifyDocument: vi.fn(),
}));
vi.mock("./toastStore", () => ({
  pushErrorToast: vi.fn(),
  useToastStore: { getState: () => ({ pushToast: vi.fn() }) },
}));

const api = vi.mocked(await import("../lib/ipc/commands"));
const save = vi.mocked(await import("../lib/archify/save"));
const toasts = vi.mocked(await import("./toastStore"));
const { useArchifyDrawStore } = await import("./archifyDrawStore");
const { useAiRunStore } = await import("./aiRunStore");

const request = { rootPath: "/repo", projectName: "shop", kind: "sequence" as const, topic: " checkout ", language: "es" };

beforeEach(() => {
  useArchifyDrawStore.setState({ runs: {}, recent: {}, recentLoaded: false });
  vi.clearAllMocks();
});

describe("archifyDrawStore", () => {
  test("draws, saves under the diagram's own title, and opens it", async () => {
    expect(await useArchifyDrawStore.getState().draw(request)).toBe("codemap/checkout-sequence.archify.json");
    const asked = api.archifyGenerate.mock.calls[0]![0];
    expect(asked).toMatchObject({ rootPath: "/repo", kind: "sequence", topic: "checkout", language: "es" });
    expect(save.saveArchifyDocument).toHaveBeenCalledWith("/repo", "Checkout", "sequence", drawn.document);
    expect(save.showArchifyDocument).toHaveBeenCalledWith("codemap/checkout-sequence.archify.json");
    expect(useArchifyDrawStore.getState().runs).toEqual({});
    expect(useAiRunStore.getState().active[asked.runId]).toBe(false);
  });

  test("one run per repository at a time", async () => {
    let finish: (value: ArchifyDrawn) => void = () => {};
    api.archifyGenerate.mockImplementationOnce(() => new Promise((resolve) => (finish = resolve)));
    const first = useArchifyDrawStore.getState().draw(request);
    expect(useArchifyDrawStore.getState().runs["/repo"]?.kind).toBe("sequence");
    expect(await useArchifyDrawStore.getState().draw(request)).toBeNull();
    expect(api.archifyGenerate).toHaveBeenCalledTimes(1);
    finish(drawn);
    await first;
    expect(useArchifyDrawStore.getState().runs).toEqual({});
  });

  test("remembers the topic, newest first, beside the ones read back", async () => {
    await useArchifyDrawStore.getState().loadRecent();
    await useArchifyDrawStore.getState().draw(request);
    expect(useArchifyDrawStore.getState().recent).toEqual({ sequence: ["checkout", "login"] });
    expect(api.setSetting).toHaveBeenCalledWith("archify_recent_topics", JSON.stringify({ sequence: ["checkout", "login"] }));
    await useArchifyDrawStore.getState().loadRecent();
    expect(api.getSetting).toHaveBeenCalledTimes(1);
  });

  test("a failure is said, a stop is not, and neither saves anything", async () => {
    api.archifyGenerate.mockRejectedValueOnce(new Error("archify would not draw this diagram: /messages"));
    expect(await useArchifyDrawStore.getState().draw(request)).toBeNull();
    expect(toasts.pushErrorToast).toHaveBeenCalledWith("archify would not draw this diagram: /messages");

    api.archifyGenerate.mockRejectedValueOnce(new Error("RUN_CANCELLED:: stopped"));
    expect(await useArchifyDrawStore.getState().draw(request)).toBeNull();
    expect(toasts.pushErrorToast).toHaveBeenCalledTimes(1);
    expect(save.saveArchifyDocument).not.toHaveBeenCalled();
  });
});
