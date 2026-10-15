import { describe, expect, it } from "vitest";
import { isAgenticProvider } from "./aiProviders";
import { AI_TASKS, AI_TASK_KEYS, canRunTask } from "./aiTasks";

function task(key: string) {
  const found = AI_TASKS.find((t) => t.key === key);
  if (!found) throw new Error(`no task ${key}`);
  return found;
}

describe("AI tasks", () => {
  it("has the eleven keys the backend routes (`AllTasks` in backend/ai/routing.go)", () => {
    expect([...AI_TASK_KEYS].sort()).toEqual(
      ["analyze", "chat", "commit", "conflict", "dbml", "diagram", "fix", "inline", "pr_description", "review", "ticket_review"],
    );
  });

  it("routes repository diagrams only to an engine that can be held to reading (AI-057)", () => {
    const diagram = task("diagram");
    expect(canRunTask(diagram, "claude", isAgenticProvider)).toBe(true);
    expect(canRunTask(diagram, "codex", isAgenticProvider)).toBe(true);
    for (const provider of ["gemini", "opencode", "ollama", "openai"]) {
      expect(canRunTask(diagram, provider, isAgenticProvider)).toBe(false);
    }
  });

  it("keeps fixing on agentic engines and leaves the other tasks unconstrained", () => {
    expect(canRunTask(task("fix"), "ollama", isAgenticProvider)).toBe(false);
    expect(canRunTask(task("chat"), "ollama", isAgenticProvider)).toBe(true);
  });
});
