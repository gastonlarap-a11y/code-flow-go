import { useEffect, useId, useState } from "react";
import { Sparkles } from "lucide-react";
import { Modal } from "../../common/Modal";
import { Button } from "../../common/Button";
import { Tooltip } from "../../common/Tooltip";
import { AiRunLog } from "../../ai/AiRunLog";
import { ChatModelPicker } from "../../ai/ChatModelPicker";
import * as api from "../../../lib/ipc/commands";
import { AI_KINDS } from "../../../lib/archify/topics";
import { firstLacking } from "../../../lib/requirements";
import { useRequirementsStore } from "../../../state/requirementsStore";
import { RequirementNotice } from "../../common/RequirementNotice";
import { useArchifyDrawStore } from "../../../state/archifyDrawStore";
import { useLanguageStore, useT } from "../../../state/languageStore";
import type { ArchifyAiKind, ArchifySuggestion } from "../../../types/domain";

/** The topics the map suggests for one kind, once they are known. */
type Suggestions = { kind: ArchifyAiKind } & ({ state: "ready"; items: ArchifySuggestion[] } | { state: "failed"; error: string });

/**
 * Asks the AI for a diagram of the repository (ARCHIFY-007): which kind, about what — typed, picked
 * from what the code declares, or asked before — and with which model. The run belongs to the
 * repository rather than to this dialog: closing it leaves the run going, and the diagram opens in
 * the diagram editor when it is ready.
 */
export function AiDiagramDialog({ rootPath, projectName, onClose }: { rootPath: string; projectName: string; onClose: () => void }) {
  const t = useT();
  const language = useLanguageStore((s) => s.language);
  const run = useArchifyDrawStore((s) => s.runs[rootPath] ?? null);
  const recent = useArchifyDrawStore((s) => s.recent);
  const loadRecent = useArchifyDrawStore((s) => s.loadRecent);
  const draw = useArchifyDrawStore((s) => s.draw);

  const [kind, setKind] = useState<ArchifyAiKind>(run?.kind ?? "architecture");
  const [topic, setTopic] = useState("");
  const [loaded, setLoaded] = useState<Suggestions | null>(null);
  const [logExpanded, setLogExpanded] = useState(false);
  const hintId = useId();

  useEffect(() => {
    void loadRecent();
  }, [loadRecent]);

  // What the diagrams need, said here before the button rather than as the error after it: Node.js
  // first, since without it Archify can neither be installed nor run (REQ-005).
  const tools = useRequirementsStore((s) => s.tools);
  const checkRequirements = useRequirementsStore((s) => s.check);
  useEffect(() => {
    void checkRequirements(rootPath);
  }, [checkRequirements, rootPath]);
  const lacking = firstLacking(tools, ["node", "archify"]);

  // An architecture is of the whole repository, so it has no topics to suggest.
  useEffect(() => {
    if (kind === "architecture") return;
    let live = true;
    api.archifySuggestions(rootPath, kind).then(
      (items) => live && setLoaded({ kind, state: "ready", items }),
      (e: unknown) => live && setLoaded({ kind, state: "failed", error: e instanceof Error ? e.message : String(e) }),
    );
    return () => {
      live = false;
    };
  }, [rootPath, kind]);
  // What was loaded for another kind is not shown while this one's loads.
  const suggestions = loaded?.kind === kind ? loaded : null;

  const active = AI_KINDS.find((k) => k.kind === kind) ?? AI_KINDS[0]!;
  const remembered = recent[kind] ?? [];

  const start = async () => {
    const saved = await draw({ rootPath, projectName, kind, topic, language });
    // The editor now shows it; this dialog would otherwise stay open over it.
    if (saved !== null) onClose();
  };

  return (
    <Modal
      title="codemap.map.ai.title"
      subtitle={t("codemap.map.ai.subtitle", { project: projectName })}
      icon={Sparkles}
      size="lg"
      scroll
      onClose={onClose}
      footer={
        <>
          <Button variant="secondary" onClick={onClose}>
            {t(run ? "codemap.map.ai.hide" : "codemap.map.ai.cancel")}
          </Button>
          <Button variant="primary" icon={Sparkles} pending={run !== null} disabled={run !== null || lacking !== null} onClick={() => void start()}>
            {t("codemap.map.ai.run")}
          </Button>
        </>
      }
    >
      <div className="flex flex-col gap-4">
        {lacking !== null && (
          <RequirementNotice
            toolId={lacking.id}
            messageKey={lacking.id === "node" ? "codemap.map.ai.needsNode" : lacking.state === "outdated" ? "requirements.archify.tooOld" : "codemap.map.ai.needsArchify"}
          />
        )}
        <fieldset className="flex flex-col gap-1.5" disabled={run !== null}>
          <legend className="mb-1.5 text-relaxed text-[var(--cf-text)]">{t("codemap.map.ai.kindLabel")}</legend>
          <div className="grid grid-cols-1 gap-1.5 sm:grid-cols-2">
            {AI_KINDS.map((option) => (
              <button
                key={option.kind}
                type="button"
                aria-pressed={option.kind === kind}
                onClick={() => setKind(option.kind)}
                className={`cf-focusable flex flex-col items-start rounded-control border px-2.5 py-1.5 text-left ${
                  option.kind === kind
                    ? "border-[var(--cf-accent)] bg-[color-mix(in_oklab,var(--cf-accent)_10%,transparent)]"
                    : "border-[var(--cf-border)] hover:border-[var(--cf-text-muted)]"
                }`}
              >
                <span className="text-body font-medium text-[var(--cf-text)]">{t(option.labelKey)}</span>
                <span className="text-ui text-[var(--cf-text-muted)]">{t(option.hintKey)}</span>
              </button>
            ))}
          </div>
        </fieldset>

        <div className="flex flex-col gap-1.5">
          <label className="flex flex-col gap-1.5">
            <span className="text-relaxed text-[var(--cf-text)]">{t("codemap.map.ai.topic")}</span>
            <textarea
              value={topic}
              onChange={(event) => setTopic(event.target.value)}
              placeholder={t(active.placeholderKey)}
              rows={2}
              disabled={run !== null}
              aria-describedby={hintId}
              className="cf-focusable resize-y rounded-control border border-[var(--cf-border)] bg-[var(--cf-bg)] px-2.5 py-1.5 text-body text-[var(--cf-text)] outline-none"
            />
          </label>
          <span id={hintId} className="text-ui text-[var(--cf-text-muted)]">
            {t("codemap.map.ai.topicHint")}
          </span>
        </div>

        <TopicChips title={t("codemap.map.ai.recent")} topics={remembered.map((text) => ({ label: text, topic: text, hint: null }))} onPick={setTopic} disabled={run !== null} />
        {suggestions?.state === "ready" && (
          <TopicChips
            title={t("codemap.map.ai.suggested")}
            topics={suggestions.items.map((s) => ({ label: s.label, topic: s.topic, hint: s.hint }))}
            onPick={setTopic}
            disabled={run !== null}
          />
        )}
        {suggestions?.state === "failed" && <p className="text-ui text-[var(--cf-text-muted)]">{t("codemap.map.ai.suggestionsFailed", { error: suggestions.error })}</p>}

        <div className="flex items-center justify-between gap-2">
          <span className="text-ui text-[var(--cf-text-muted)]">{t("codemap.map.ai.model")}</span>
          <ChatModelPicker liveModel={null} chatActive={false} task="diagram" heading="codemap.map.ai.model" />
        </div>

        {run !== null && (
          <div className="flex flex-col gap-1.5">
            <AiRunLog runId={run.runId} running expanded={logExpanded} onToggle={() => setLogExpanded((open) => !open)} />
            <p className="text-ui text-[var(--cf-text-muted)]">{t("codemap.map.ai.running")}</p>
          </div>
        )}
      </div>
    </Modal>
  );
}

function TopicChips({
  title,
  topics,
  onPick,
  disabled,
}: {
  title: string;
  topics: { label: string; topic: string; hint: string | null }[];
  onPick: (topic: string) => void;
  disabled: boolean;
}) {
  if (topics.length === 0) return null;
  return (
    <div className="flex flex-col gap-1.5">
      <span className="text-ui text-[var(--cf-text-muted)]">{title}</span>
      <div className="flex flex-wrap gap-1.5">
        {topics.map((item) => {
          const chip = (
            <button
              key={item.topic}
              type="button"
              disabled={disabled}
              onClick={() => onPick(item.topic)}
              className="cf-focusable max-w-full truncate rounded-full border border-[var(--cf-border)] px-2.5 py-0.5 text-ui text-[var(--cf-text)] hover:border-[var(--cf-accent)] disabled:opacity-50"
            >
              {item.label}
            </button>
          );
          return item.hint === null ? (
            chip
          ) : (
            <Tooltip key={item.topic} label={item.hint} placement="top">
              {chip}
            </Tooltip>
          );
        })}
      </div>
    </div>
  );
}
