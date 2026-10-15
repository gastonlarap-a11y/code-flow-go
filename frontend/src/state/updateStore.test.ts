import { beforeEach, describe, expect, test, vi } from "vitest";

const getVersion = vi.fn(() => Promise.resolve("3.9.0"));
const check = vi.fn(() => Promise.resolve(null));

vi.mock("../lib/bridge/updater", () => ({
  getVersion: () => getVersion(),
  check: () => check(),
  relaunch: vi.fn(),
}));

const { isUnstampedVersion, useUpdateStore } = await import("./updateStore");

beforeEach(() => {
  vi.clearAllMocks();
  useUpdateStore.setState({ currentVersion: "", status: "idle", update: null, error: "" });
});

describe("updateStore", () => {
  test("an unstamped build is never checked on its own", async () => {
    getVersion.mockResolvedValueOnce("0.0.0");
    await useUpdateStore.getState().checkNow();
    expect(check).not.toHaveBeenCalled();
    expect(useUpdateStore.getState().status).toBe("idle");
  });

  test("but it is when someone asks", async () => {
    useUpdateStore.setState({ currentVersion: "0.0.0-ci" });
    await useUpdateStore.getState().checkNow(true);
    expect(check).toHaveBeenCalledTimes(1);
    expect(useUpdateStore.getState().status).toBe("uptodate");
  });

  test("a released build checks on its own", async () => {
    await useUpdateStore.getState().checkNow();
    expect(check).toHaveBeenCalledTimes(1);
  });

  test.each([
    ["0.0.0", true],
    ["0.0.0-ci", true],
    ["0.0.0+dirty", true],
    ["3.9.0", false],
    ["0.0.1", false],
    ["", false],
  ])("%s is unstamped: %s", (version, want) => {
    expect(isUnstampedVersion(version)).toBe(want);
  });
});
