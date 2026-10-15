import type { TranslationKey } from "./i18n/translations";
import type { Platform } from "./platform";
import type { RequirementTool } from "../types/domain";

/**
 * What the app's features need installed and do not ship, and how to get each (21-requirements.md).
 *
 * The commands are each vendor's own recommended one, as of October 2026: a native installer where
 * there is one, else Homebrew on macOS and winget on Windows. They are shown, copied or typed into
 * the app's terminal — never run by the app: installing software is the person's decision.
 */

/** One command per platform: macOS and Windows install nearly everything differently. */
export interface PlatformCommand {
  macos: string;
  windows: string;
  /** Linux is not a release target; a command here is a courtesy for building from source. */
  linux?: string;
}

/** The command for the platform the app runs on, or null where there is none. */
export function commandFor(command: PlatformCommand | undefined, platform: Platform): string | null {
  if (!command) return null;
  switch (platform) {
    case "macos":
      return command.macos;
    case "windows":
      return command.windows;
    case "linux":
      return command.linux ?? null;
    default:
      return null;
  }
}

/** A Homebrew command only works with Homebrew installed, which a fresh Mac does not have. */
export const needsHomebrew = (command: string | null): boolean => command?.startsWith("brew ") ?? false;
export const HOMEBREW_URL = "https://brew.sh";

/** How much a tool matters: the app does not start its work without a required one. */
export type RequirementNeed = "required" | "feature";

export interface ToolRequirement {
  id: RequirementTool["id"];
  name: string;
  need: RequirementNeed;
  /** What it is for, in the person's words: which features stop working without it. */
  purposeKey: TranslationKey;
  /** Said when the version found is too old for what it is needed for. */
  tooOldKey?: TranslationKey;
  install: PlatformCommand;
  docsUrl: string;
}

export const TOOL_REQUIREMENTS: readonly ToolRequirement[] = [
  {
    id: "git",
    name: "Git",
    need: "required",
    purposeKey: "requirements.git.purpose",
    install: { macos: "xcode-select --install", windows: "winget install --id Git.Git -e --source winget", linux: "sudo apt install git" },
    docsUrl: "https://git-scm.com/downloads",
  },
  {
    id: "node",
    name: "Node.js",
    need: "feature",
    purposeKey: "requirements.node.purpose",
    tooOldKey: "requirements.node.tooOld",
    install: { macos: "brew install node", windows: "winget install --id OpenJS.NodeJS.LTS -e" },
    docsUrl: "https://nodejs.org/en/download",
  },
  {
    id: "archify",
    name: "Archify",
    need: "feature",
    purposeKey: "requirements.archify.purpose",
    tooOldKey: "requirements.archify.tooOld",
    install: { macos: "npx skills add tt-a1i/archify -g", windows: "npx skills add tt-a1i/archify -g", linux: "npx skills add tt-a1i/archify -g" },
    docsUrl: "https://github.com/tt-a1i/archify",
  },
  {
    id: "go",
    name: "Go",
    need: "feature",
    purposeKey: "requirements.go.purpose",
    install: { macos: "brew install go", windows: "winget install --id GoLang.Go -e" },
    docsUrl: "https://go.dev/doc/install",
  },
];

/** Where a tool stands: found and good, found but too old, or not found. */
export type ToolState = "ready" | "outdated" | "missing";

export function toolState(tool: RequirementTool | undefined): ToolState {
  if (!tool?.installed) return "missing";
  return tool.supported ? "ready" : "outdated";
}

/**
 * The first of `ids`, in order, that is missing or too old — what a feature needing them all says it
 * lacks, the one to install first listed first. Null while nothing is known yet, so a feature is not
 * blocked before the check answers.
 */
export function firstLacking(
  tools: readonly RequirementTool[] | null,
  ids: readonly RequirementTool["id"][],
): { id: RequirementTool["id"]; state: Exclude<ToolState, "ready"> } | null {
  if (tools === null) return null;
  for (const id of ids) {
    const state = toolState(tools.find((tool) => tool.id === id));
    if (state !== "ready") return { id, state };
  }
  return null;
}
