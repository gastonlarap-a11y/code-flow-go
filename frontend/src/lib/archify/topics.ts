import type { TranslationKey } from "../i18n/translations";
import type { ArchifyAiKind } from "../../types/domain";

/** The diagrams a model is asked to draw, in the order the dialog offers them (ARCHIFY-007). */
export const AI_KINDS: readonly { kind: ArchifyAiKind; labelKey: TranslationKey; hintKey: TranslationKey; placeholderKey: TranslationKey }[] = [
  { kind: "architecture", labelKey: "codemap.map.ai.kind.architecture", hintKey: "codemap.map.ai.hint.architecture", placeholderKey: "codemap.map.ai.placeholder.architecture" },
  { kind: "workflow", labelKey: "codemap.map.ai.kind.workflow", hintKey: "codemap.map.ai.hint.workflow", placeholderKey: "codemap.map.ai.placeholder.workflow" },
  { kind: "sequence", labelKey: "codemap.map.ai.kind.sequence", hintKey: "codemap.map.ai.hint.sequence", placeholderKey: "codemap.map.ai.placeholder.sequence" },
  { kind: "dataflow", labelKey: "codemap.map.ai.kind.dataflow", hintKey: "codemap.map.ai.hint.dataflow", placeholderKey: "codemap.map.ai.placeholder.dataflow" },
  { kind: "lifecycle", labelKey: "codemap.map.ai.kind.lifecycle", hintKey: "codemap.map.ai.hint.lifecycle", placeholderKey: "codemap.map.ai.placeholder.lifecycle" },
];

/**
 * The topics asked for before, per kind of diagram, newest first — in one app setting, as the recent
 * projects are (`lib/ui/recentProjects.ts`): they survive a restart and stay on this machine.
 */
export const RECENT_TOPICS_KEY = "archify_recent_topics";
export type RecentTopics = Partial<Record<ArchifyAiKind, string[]>>;

/** How many topics a kind remembers. */
const MAX_RECENT_TOPICS = 6;
/** A topic longer than this is not kept: it is a paragraph, not something to pick again. */
const MAX_TOPIC_CHARS = 300;

const KINDS = new Set<string>(AI_KINDS.map((k) => k.kind));

/** Puts a topic first for its kind, without repeating it; a blank one changes nothing. */
export function pushTopic(recent: RecentTopics, kind: ArchifyAiKind, topic: string): RecentTopics {
  const text = topic.trim();
  if (text === "" || text.length > MAX_TOPIC_CHARS) return recent;
  const before = recent[kind] ?? [];
  return { ...recent, [kind]: [text, ...before.filter((t) => t !== text)].slice(0, MAX_RECENT_TOPICS) };
}

/** Reads the setting back; anything that is not the shape written reads as no history. */
export function parseTopics(raw: string | null): RecentTopics {
  if (!raw) return {};
  try {
    const parsed: unknown = JSON.parse(raw);
    if (typeof parsed !== "object" || parsed === null || Array.isArray(parsed)) return {};
    const out: RecentTopics = {};
    for (const [kind, topics] of Object.entries(parsed)) {
      if (!KINDS.has(kind) || !Array.isArray(topics)) continue;
      // KINDS holds exactly the ArchifyAiKind values, checked on the line above.
      out[kind as ArchifyAiKind] = topics.filter((t): t is string => typeof t === "string").slice(0, MAX_RECENT_TOPICS);
    }
    return out;
  } catch {
    return {};
  }
}
