// @vitest-environment jsdom
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { cleanup, render, screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import type { Update } from "../../lib/bridge/updater";
import { translate } from "../../state/languageStore";
import { useUpdateStore, type UpdateStatus } from "../../state/updateStore";
import { UpdateAlert } from "./UpdateAlert";

// The store reaches Go through this module; nothing here should, so every call is a stub.
vi.mock("../../lib/bridge/updater", () => ({
  check: vi.fn(),
  getVersion: vi.fn(),
  relaunch: vi.fn(),
}));

function release(installKind: Update["installKind"]): Update {
  return {
    version: "3.8.0",
    currentVersion: "3.7.0",
    installKind,
    downloadAndInstall: vi.fn(),
  };
}

const install = vi.fn(() => Promise.resolve());
const restart = vi.fn(() => Promise.resolve());
const openNotes = vi.fn();

function show(status: UpdateStatus, update: Update | null, extra: { progress?: number; installError?: string } = {}) {
  useUpdateStore.setState({
    status,
    update,
    progress: extra.progress ?? 0,
    installError: extra.installError ?? "",
    install,
    restart,
    openNotes,
  });
  return render(<UpdateAlert />);
}

beforeEach(() => {
  vi.clearAllMocks();
});

afterEach(() => {
  cleanup();
});

// The card *is* the update UI (it cannot be dismissed), so what it offers in each state is the
// whole of what a user can do about an update.
describe("UpdateAlert", () => {
  it("renders nothing without a pending release", () => {
    const { container } = show("uptodate", null);

    expect(container.childElementCount).toBe(0);
  });

  it("offers the update and its notes when one is available", async () => {
    show("available", release("auto"));

    expect(screen.getByText(translate("update.alertTitle"))).toBeTruthy();
    expect(screen.getByText(translate("update.alertBody", { version: "v3.8.0" }))).toBeTruthy();

    await userEvent.click(screen.getByRole("button", { name: translate("update.installNow") }));
    await userEvent.click(screen.getByRole("button", { name: translate("update.seeWhatsNew") }));

    expect(install).toHaveBeenCalledOnce();
    expect(openNotes).toHaveBeenCalledOnce();
  });

  it("shows progress and nothing to press while downloading", () => {
    show("downloading", release("auto"), { progress: 42 });

    expect(screen.getByText(translate("settings.downloadingUpdate", { progress: 42 }))).toBeTruthy();
    expect(screen.queryByRole("button")).toBeNull();
  });

  it("restarts into the new build where the platform can install it", async () => {
    show("ready", release("auto"));

    await userEvent.click(screen.getByRole("button", { name: translate("settings.restartNow") }));

    expect(restart).toHaveBeenCalledOnce();
  });

  // A "Restart now" that changes nothing would be worse than no button: the platform can only
  // hand the installer over, so the card says what to do instead.
  it("offers no restart where the platform cannot finish the job", () => {
    show("ready", release("manual"));

    expect(screen.getByText(translate("update.alertReadyManualBody"))).toBeTruthy();
    expect(screen.queryByRole("button")).toBeNull();
  });

  it("offers a retry, and no notes, after a failed download", async () => {
    show("error", release("auto"), { installError: "network down" });

    expect(screen.getByText(translate("update.alertFailedTitle"))).toBeTruthy();
    expect(screen.queryByRole("button", { name: translate("update.seeWhatsNew") })).toBeNull();

    await userEvent.click(screen.getByRole("button", { name: translate("update.retry") }));

    expect(install).toHaveBeenCalledOnce();
  });

  // A failed *check* is Settings' business, not something to interrupt anyone with.
  it("stays hidden after a failed check that found nothing to install", () => {
    const { container } = show("error", release("auto"));

    expect(container.childElementCount).toBe(0);
  });
});
