import { describe, expect, it } from "vitest";
import { AI_PROVIDERS } from "./aiProviders";
import { TOOL_REQUIREMENTS, commandFor, firstLacking, needsHomebrew, toolState } from "./requirements";
import type { RequirementTool } from "../types/domain";

const tool = (id: RequirementTool["id"], installed: boolean, supported = true): RequirementTool => ({
  id,
  installed,
  supported,
  version: installed ? "1.0.0" : "",
  path: installed ? `/bin/${id}` : "",
});

describe("requirements", () => {
  it("answers each platform its own command", () => {
    const command = { macos: "brew install go", windows: "winget install --id GoLang.Go -e" };
    expect(commandFor(command, "macos")).toBe("brew install go");
    expect(commandFor(command, "windows")).toBe("winget install --id GoLang.Go -e");
    expect(commandFor(command, "linux")).toBeNull();
    expect(commandFor({ ...command, linux: "sudo apt install golang" }, "linux")).toBe("sudo apt install golang");
    expect(commandFor(undefined, "macos")).toBeNull();
  });

  it("lists every tool the backend reports, with a command for both release platforms", () => {
    expect(TOOL_REQUIREMENTS.map((r) => r.id).sort()).toEqual(["archify", "git", "go", "node"]);
    for (const requirement of TOOL_REQUIREMENTS) {
      expect(requirement.install.macos.trim(), requirement.id).not.toBe("");
      expect(requirement.install.windows.trim(), requirement.id).not.toBe("");
      expect(requirement.docsUrl).toMatch(/^https:\/\//);
    }
  });

  it("gives every installable AI engine a command for both release platforms", () => {
    for (const provider of AI_PROVIDERS.filter((p) => p.setup?.command)) {
      expect(provider.setup?.command?.macos, provider.id).toBeTruthy();
      expect(provider.setup?.command?.windows, provider.id).toBeTruthy();
      expect(provider.setup?.command?.macos, `${provider.id} on macOS`).not.toMatch(/winget|\birm\b/);
      expect(provider.setup?.command?.windows, `${provider.id} on Windows`).not.toMatch(/\bbrew\b|\| ?(ba)?sh\b/);
    }
  });

  it("names a Homebrew command as one", () => {
    expect(needsHomebrew("brew install node")).toBe(true);
    expect(needsHomebrew("xcode-select --install")).toBe(false);
    expect(needsHomebrew(null)).toBe(false);
  });

  it("tells ready, too old and missing apart", () => {
    expect(toolState(tool("git", true))).toBe("ready");
    expect(toolState(tool("node", true, false))).toBe("outdated");
    expect(toolState(tool("go", false))).toBe("missing");
    expect(toolState(undefined)).toBe("missing");
  });

  it("names the first lacking tool, in the order they must be installed", () => {
    const tools = [tool("node", false), tool("archify", false)];
    expect(firstLacking(tools, ["node", "archify"])).toEqual({ id: "node", state: "missing" });
    expect(firstLacking([tool("node", true), tool("archify", true, false)], ["node", "archify"])).toEqual({ id: "archify", state: "outdated" });
    expect(firstLacking([tool("node", true), tool("archify", true)], ["node", "archify"])).toBeNull();
    expect(firstLacking(null, ["node"])).toBeNull();
  });
});
