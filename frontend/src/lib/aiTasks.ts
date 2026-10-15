import type { TranslationKey } from "./i18n/translations";

export interface AiTaskDef {
  /** Settings-key fragment. Must match the sidecar's `AiTask` keys — these strings are the contract
   * between the routing UI (`ai_provider_{key}`, `{provider}_{key}_model`) and the backend. */
  key: string;
  labelKey: TranslationKey;
  hintKey: TranslationKey;
  /** Needs a write-capable agentic engine, so local models can't be routed here. */
  agenticOnly?: boolean;
  /** Must read the repository without being able to change it (AI-057), which only these engines
   * can be held to; the backend refuses any other. */
  readOnlyEngines?: readonly string[];
}

/** The engines a read-only repository run can use: Claude bounded by its tool list, Codex by its
 * read-only sandbox. Mirrors `EnforcesReadOnly` in `backend/ai/engine.go`. */
export const READ_ONLY_ENGINES = ["claude", "codex"] as const;

/** Every AI action that can be pointed at its own provider + model, in the order the settings
 * table lists them (most-used first). */
export const AI_TASKS: AiTaskDef[] = [
  { key: "chat", labelKey: "task.chat", hintKey: "task.chatHint" },
  { key: "commit", labelKey: "task.commit", hintKey: "task.commitHint" },
  { key: "analyze", labelKey: "task.analyze", hintKey: "task.analyzeHint" },
  { key: "review", labelKey: "task.review", hintKey: "task.reviewHint" },
  { key: "ticket_review", labelKey: "task.ticketReview", hintKey: "task.ticketReviewHint" },
  { key: "pr_description", labelKey: "task.prDescription", hintKey: "task.prDescriptionHint" },
  { key: "fix", labelKey: "task.fix", hintKey: "task.fixHint", agenticOnly: true },
  { key: "conflict", labelKey: "task.conflict", hintKey: "task.conflictHint" },
  { key: "inline", labelKey: "task.inline", hintKey: "task.inlineHint" },
  { key: "dbml", labelKey: "task.dbml", hintKey: "task.dbmlHint" },
  { key: "diagram", labelKey: "task.diagram", hintKey: "task.diagramHint", readOnlyEngines: READ_ONLY_ENGINES },
];

/** Whether a provider can run a task at all, given what the task needs from its engine. */
export function canRunTask(task: AiTaskDef, provider: string, isAgentic: (provider: string) => boolean): boolean {
  if (task.agenticOnly && !isAgentic(provider)) return false;
  if (task.readOnlyEngines && !task.readOnlyEngines.includes(provider)) return false;
  return true;
}

export const AI_TASK_KEYS = AI_TASKS.map((t) => t.key);
