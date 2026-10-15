// @vitest-environment jsdom
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { cleanup, render, screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { translate } from "../../state/languageStore";

vi.mock("../../lib/ipc/commands", () => ({
  archifyStatus: vi.fn(),
  archifyRender: vi.fn(),
  archifyOpenExternal: vi.fn(() => Promise.resolve()),
  readFileText: vi.fn(() => Promise.resolve("{}")),
  saveTextFile: vi.fn(() => Promise.resolve("/out/x.json")),
  writeFileText: vi.fn(() => Promise.resolve()),
  archifyRefine: vi.fn(() => Promise.resolve({ document: '{"v":2}', title: "Shop", diagram_type: "architecture", rounds: 0 })),
}));
vi.mock("../../lib/ui/useCopy", () => ({ copyText: vi.fn(() => Promise.resolve()) }));

const api = vi.mocked(await import("../../lib/ipc/commands"));
const { copyText } = vi.mocked(await import("../../lib/ui/useCopy"));
const { useArchifyStore } = await import("../../state/archifyStore");
const { useThemeStore } = await import("../../state/themeStore");
const { ArchifyActions, ArchifyPane } = await import("./ArchifyPane");

const t = translate;

beforeEach(() => {
  vi.clearAllMocks();
  useArchifyStore.getState().close();
  useThemeStore.setState({ resolved: "light" });
});

afterEach(() => {
  cleanup();
});

describe("ArchifyPane", () => {
  it("frames the drawn page sandboxed, in the app's theme", () => {
    useArchifyStore.setState({
      rootPath: "/repo",
      path: "codemap/shop-class.archify.json",
      status: { installed: true, version: "3.1.0", has_class: true, install: "npx skills add tt-a1i/archify -g" },
      page: { view_url: "/archify-view/abc.html", html_path: "/data/abc.html", report: "" },
    });
    render(<ArchifyPane />);

    const frame = screen.getByTitle(t("diagram.archify.frameTitle"));
    expect(frame.getAttribute("sandbox")).toBe("allow-scripts");
    expect(frame.getAttribute("src")).toBe("/archify-view/abc.html?theme=light");
  });

  it("offers the install command when Archify is missing", async () => {
    useArchifyStore.setState({
      rootPath: "/repo",
      path: "a.archify.json",
      status: { installed: false, version: "", has_class: false, install: "npx skills add tt-a1i/archify -g" },
    });
    render(<ArchifyPane />);

    expect(screen.getByText(t("diagram.archify.notInstalled"))).toBeTruthy();
    expect(screen.getByText("npx skills add tt-a1i/archify -g")).toBeTruthy();
    await userEvent.click(screen.getByRole("button", { name: t("diagram.archify.copyInstall") }));
    expect(copyText).toHaveBeenCalledWith("npx skills add tt-a1i/archify -g");
  });

  it("says why it could not draw, and draws again on request", async () => {
    api.archifyStatus.mockResolvedValue({ installed: true, version: "3.1.0", has_class: true, install: "" });
    api.archifyRender.mockResolvedValue({ view_url: "/archify-view/def.html", html_path: "/data/def.html", report: "" });
    useArchifyStore.setState({ rootPath: "/repo", path: "a.archify.json", error: "archify could not render the diagram: bad id" });
    render(<ArchifyPane />);

    expect(screen.getByText("archify could not render the diagram: bad id")).toBeTruthy();
    await userEvent.click(screen.getByRole("button", { name: t("diagram.archify.redraw") }));
    expect(api.archifyRender).toHaveBeenCalledWith("/repo", "a.archify.json");
  });

  it("opens the page in the browser", async () => {
    useArchifyStore.setState({
      rootPath: "/repo",
      path: "a.archify.json",
      page: { view_url: "/archify-view/abc.html", html_path: "/data/abc.html", report: "" },
    });
    render(<ArchifyActions />);
    await userEvent.click(screen.getByRole("button", { name: t("diagram.archify.openExternal") }));
    expect(api.archifyOpenExternal).toHaveBeenCalledWith("/data/abc.html");
  });

  it("adjusts the document with AI, and one undo writes the old text back", async () => {
    api.archifyStatus.mockResolvedValue({ installed: true, version: "3.1.0", has_class: true, install: "" });
    api.archifyRender.mockResolvedValue({ view_url: "/archify-view/abc.html", html_path: "/data/abc.html", report: "" });
    api.readFileText.mockResolvedValue('{"v":1}');
    useArchifyStore.setState({
      rootPath: "/repo",
      path: "codemap/shop-architecture.archify.json",
      page: { view_url: "/archify-view/abc.html", html_path: "/data/abc.html", report: "" },
    });
    render(<ArchifyActions />);
    expect(screen.queryByRole("button", { name: t("diagram.archify.undo") })).toBeNull();

    await userEvent.click(screen.getByRole("button", { name: t("diagram.archify.refine") }));
    const run = screen.getByRole("button", { name: t("diagram.archify.refineRun") });
    expect((run as HTMLButtonElement).disabled).toBe(true);
    await userEvent.type(screen.getByRole("textbox", { name: t("diagram.archify.refineInstruction") }), "add the cache");
    await userEvent.click(run);

    await vi.waitFor(() => expect(api.writeFileText).toHaveBeenCalledWith("/repo", "codemap/shop-architecture.archify.json", '{"v":2}'));
    expect(api.archifyRefine).toHaveBeenCalledWith(expect.objectContaining({ instruction: "add the cache", path: "codemap/shop-architecture.archify.json" }));
    await vi.waitFor(() => expect(screen.queryByRole("textbox", { name: t("diagram.archify.refineInstruction") })).toBeNull());

    await userEvent.click(await screen.findByRole("button", { name: t("diagram.archify.undo") }));
    await vi.waitFor(() => expect(api.writeFileText).toHaveBeenLastCalledWith("/repo", "codemap/shop-architecture.archify.json", '{"v":1}'));
    await vi.waitFor(() => expect(screen.queryByRole("button", { name: t("diagram.archify.undo") })).toBeNull());
  });

  it("keeps the dialog open with Archify's refusal", async () => {
    api.readFileText.mockResolvedValue('{"v":1}');
    api.archifyRefine.mockRejectedValueOnce(new Error("archify would not draw this diagram: /components/0"));
    useArchifyStore.setState({
      rootPath: "/repo",
      path: "a.archify.json",
      page: { view_url: "/archify-view/abc.html", html_path: "/data/abc.html", report: "" },
    });
    render(<ArchifyActions />);
    await userEvent.click(screen.getByRole("button", { name: t("diagram.archify.refine") }));
    await userEvent.type(screen.getByRole("textbox", { name: t("diagram.archify.refineInstruction") }), "x");
    await userEvent.click(screen.getByRole("button", { name: t("diagram.archify.refineRun") }));
    expect((await screen.findByRole("alert")).textContent).toBe("archify would not draw this diagram: /components/0");
    expect(api.writeFileText).not.toHaveBeenCalled();
  });
});
