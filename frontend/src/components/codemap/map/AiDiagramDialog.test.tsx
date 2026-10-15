// @vitest-environment jsdom
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { cleanup, render, screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { translate } from "../../../state/languageStore";
import type { ArchifyDrawn } from "../../../types/domain";

const drawn: ArchifyDrawn = { document: "{}", title: "Checkout", diagram_type: "sequence", rounds: 0 };

vi.mock("../../../lib/ipc/commands", () => ({
  archifySuggestions: vi.fn(() => Promise.resolve([{ label: "OrdersController", hint: "api/orders.ts:3", topic: "the request flow through OrdersController (api/orders.ts)" }])),
  archifyGenerate: vi.fn(() => Promise.resolve(drawn)),
  getSetting: vi.fn(() => Promise.resolve(JSON.stringify({ sequence: ["the login"] }))),
  setSetting: vi.fn(() => Promise.resolve()),
  diagramListDocuments: vi.fn(() => Promise.resolve([])),
  createFile: vi.fn(() => Promise.resolve()),
  writeFileText: vi.fn(() => Promise.resolve()),
  requirementsStatus: vi.fn(() =>
    Promise.resolve([
      { id: "node", installed: true, version: "24.22.0", path: "/bin/node", supported: true },
      { id: "archify", installed: true, version: "3.1.0", path: "/skills/archify/bin/archify.mjs", supported: true },
    ]),
  ),
}));

const api = vi.mocked(await import("../../../lib/ipc/commands"));
const { useArchifyDrawStore } = await import("../../../state/archifyDrawStore");
const { useUiStore } = await import("../../../state/uiStore");
const { AiDiagramDialog } = await import("./AiDiagramDialog");

const t = translate;

beforeEach(() => {
  vi.clearAllMocks();
  useArchifyDrawStore.setState({ runs: {}, recent: {}, recentLoaded: false });
  // jsdom has no Popover API; a suggestion's tooltip opens one when the pointer reaches it.
  HTMLElement.prototype.showPopover = () => {};
  HTMLElement.prototype.hidePopover = () => {};
});

afterEach(() => {
  cleanup();
});

describe("AiDiagramDialog", () => {
  it("offers what the code declares and what was asked before, and draws the picked topic", async () => {
    const onClose = vi.fn();
    render(<AiDiagramDialog rootPath="/repo" projectName="shop" onClose={onClose} />);
    expect(api.archifySuggestions).not.toHaveBeenCalled(); // an architecture is of the whole repository

    await userEvent.click(screen.getByRole("button", { name: new RegExp(t("codemap.map.ai.kind.sequence")) }));
    await userEvent.click(await screen.findByRole("button", { name: "OrdersController" }));
    expect(api.archifySuggestions).toHaveBeenCalledWith("/repo", "sequence");
    expect(screen.getByRole("button", { name: "the login" })).toBeTruthy();
    const topic = screen.getByRole("textbox", { name: t("codemap.map.ai.topic") });
    expect((topic as HTMLTextAreaElement).value).toBe("the request flow through OrdersController (api/orders.ts)");

    await userEvent.click(screen.getByRole("button", { name: t("codemap.map.ai.run") }));
    await vi.waitFor(() => expect(onClose).toHaveBeenCalled());
    expect(api.archifyGenerate.mock.calls[0]![0]).toMatchObject({ rootPath: "/repo", kind: "sequence", topic: "the request flow through OrdersController (api/orders.ts)" });
    expect(api.writeFileText).toHaveBeenCalledWith("/repo", "codemap/checkout-sequence.archify.json", "{}");
    expect(useUiStore.getState().activeView).toBe("diagram");
  });

  it("says what is missing before the button, Node.js first, and does not offer to draw", async () => {
    api.requirementsStatus.mockResolvedValueOnce([
      { id: "node", installed: false, version: "", path: "", supported: false },
      { id: "archify", installed: false, version: "", path: "", supported: false },
    ]);
    render(<AiDiagramDialog rootPath="/repo" projectName="shop" onClose={vi.fn()} />);

    expect(await screen.findByText(t("codemap.map.ai.needsNode"))).toBeTruthy();
    expect((screen.getByRole("button", { name: t("codemap.map.ai.run") }) as HTMLButtonElement).disabled).toBe(true);
    expect(screen.getByRole("button", { name: t("requirements.seeAll") })).toBeTruthy();
    expect(api.requirementsStatus).toHaveBeenCalledWith("/repo");
  });

  it("shows the run going on when it is reopened, and lets it be closed without stopping it", async () => {
    useArchifyDrawStore.setState({ runs: { "/repo": { runId: "archify-1", kind: "lifecycle" } } });
    const onClose = vi.fn();
    render(<AiDiagramDialog rootPath="/repo" projectName="shop" onClose={onClose} />);

    expect(screen.getByText(t("codemap.map.ai.running"))).toBeTruthy();
    expect(screen.getByRole("button", { name: new RegExp(t("codemap.map.ai.kind.lifecycle")) }).getAttribute("aria-pressed")).toBe("true");
    expect((screen.getByRole("button", { name: t("codemap.map.ai.run") }) as HTMLButtonElement).disabled).toBe(true);
    await userEvent.click(screen.getByRole("button", { name: t("codemap.map.ai.hide") }));
    expect(onClose).toHaveBeenCalled();
    expect(useArchifyDrawStore.getState().runs["/repo"]).toBeDefined();
  });
});
