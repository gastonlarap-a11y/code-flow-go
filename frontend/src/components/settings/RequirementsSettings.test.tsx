// @vitest-environment jsdom
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { cleanup, render, screen, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { translate } from "../../state/languageStore";
import type { Project, RequirementTool } from "../../types/domain";

const tools: RequirementTool[] = [
  { id: "git", installed: true, version: "2.50.1", path: "/usr/bin/git", supported: true },
  { id: "node", installed: true, version: "20.11.1", path: "/usr/local/bin/node", supported: false },
  { id: "go", installed: false, version: "", path: "", supported: false },
  { id: "archify", installed: false, version: "", path: "", supported: false },
];

vi.mock("../../lib/ipc/commands", () => ({
  requirementsStatus: vi.fn(() => Promise.resolve(tools)),
  checkAiProvider: vi.fn((provider: string) => Promise.resolve({ available: provider === "claude", detail: "", binary: provider })),
  openExternalUrl: vi.fn(() => Promise.resolve()),
  openTerminal: vi.fn(() => Promise.resolve("term-1")),
  writeTerminal: vi.fn(() => Promise.resolve()),
  setSetting: vi.fn(() => Promise.resolve()),
  getSetting: vi.fn(() => Promise.resolve(null)),
}));
vi.mock("../../lib/platform", () => ({ usePlatform: () => "macos", currentPlatform: () => "macos", isMac: () => true }));
vi.mock("../../lib/ui/useCopy", () => ({ copyText: vi.fn(() => Promise.resolve()) }));

const api = vi.mocked(await import("../../lib/ipc/commands"));
const { copyText } = vi.mocked(await import("../../lib/ui/useCopy"));
const { useRequirementsStore } = await import("../../state/requirementsStore");
const { useProviderStatusStore } = await import("../../state/providerStatusStore");
const { useWorkspaceStore } = await import("../../state/workspaceStore");
const { useUiStore } = await import("../../state/uiStore");
const { RequirementsSettings } = await import("./RequirementsSettings");

const t = translate;
const project = { id: "p1", workspace_id: "w1", name: "shop", local_path: "/repo" } as Project; // only what the view reads

const rowOf = (name: string) => screen.getByText(name, { selector: "p" }).closest("li") as HTMLElement; // the row holds the name

beforeEach(() => {
  vi.clearAllMocks();
  useRequirementsStore.setState({ tools: null, checking: false, error: null });
  useProviderStatusStore.setState({ byProvider: {}, checking: false });
  useWorkspaceStore.setState({ activeWorkspaceId: null, activeProjectId: null, projectsByWorkspace: {} });
  HTMLElement.prototype.showPopover = () => {};
  HTMLElement.prototype.hidePopover = () => {};
});

afterEach(() => {
  cleanup();
});

describe("RequirementsSettings", () => {
  it("says of each tool what it is for, whether it is here, and how to get it when it is not", async () => {
    render(<RequirementsSettings />);
    await vi.waitFor(() => expect(within(rowOf("Git")).getByText(t("requirements.state.ready"))).toBeTruthy());
    expect(api.requirementsStatus).toHaveBeenCalled();

    const git = rowOf("Git");
    expect(within(git).getByText("2.50.1")).toBeTruthy();
    expect(within(git).queryByRole("button", { name: /xcode-select/ })).toBeNull(); // installed: no command to run

    const node = rowOf("Node.js");
    expect(within(node).getByText(t("requirements.state.outdated"))).toBeTruthy();
    expect(within(node).getByText(t("requirements.node.tooOld"))).toBeTruthy();
    expect(within(node).getByRole("button", { name: /brew install node/ })).toBeTruthy();
    expect(within(node).getByText(t("requirements.needsHomebrew"), { exact: false })).toBeTruthy();

    const archify = rowOf("Archify");
    expect(within(archify).getByText(t("requirements.state.missing"))).toBeTruthy();
    await userEvent.click(within(archify).getByRole("button", { name: /npx skills add tt-a1i\/archify -g/ }));
    expect(copyText).toHaveBeenCalledWith("npx skills add tt-a1i/archify -g");
  });

  it("lists the AI assistants with their own status and installer", async () => {
    render(<RequirementsSettings />);
    await vi.waitFor(() => expect(within(rowOf("Claude Code")).getByText(t("requirements.state.ready"))).toBeTruthy());
    const codex = rowOf("Codex");
    expect(within(codex).getByText(t("requirements.state.missing"))).toBeTruthy();
    expect(within(codex).getByRole("button", { name: /chatgpt\.com\/codex\/install\.sh/ })).toBeTruthy();
    expect(within(codex).getByRole("button", { name: /codex login/ })).toBeTruthy();
  });

  it("types a command into a new terminal of the open project, without running it", async () => {
    useWorkspaceStore.setState({ activeWorkspaceId: "w1", activeProjectId: "p1", projectsByWorkspace: { w1: [project] } });
    useUiStore.setState({ settingsOpen: true });
    render(<RequirementsSettings />);
    await vi.waitFor(() => expect(within(rowOf("Go")).getByText(t("requirements.state.missing"))).toBeTruthy());

    await userEvent.click(within(rowOf("Go")).getByRole("button", { name: t("requirements.openInTerminal") }));
    await vi.waitFor(() => expect(api.writeTerminal).toHaveBeenCalledWith("term-1", "brew install go"));
    expect(api.openTerminal).toHaveBeenCalledWith("/repo");
    expect(useUiStore.getState().settingsOpen).toBe(false);
  });

  it("offers no terminal without a project, and checks again on request", async () => {
    render(<RequirementsSettings />);
    await vi.waitFor(() => expect(within(rowOf("Go")).getByText(t("requirements.state.missing"))).toBeTruthy());
    const terminal = within(rowOf("Go")).getByRole("button", { name: t("requirements.openInTerminalNoProject") });
    expect((terminal as HTMLButtonElement).disabled).toBe(true);

    await userEvent.click(screen.getByRole("button", { name: t("requirements.recheck") }));
    await vi.waitFor(() => expect(api.requirementsStatus).toHaveBeenCalledTimes(2));
  });
});
