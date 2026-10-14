import { useMemo, useState } from "react";
import { AlertTriangle, Sparkles } from "lucide-react";
import { AiRunLog } from "../ai/AiRunLog";
import { Button } from "../common/Button";
import { renderMarkdown } from "../../lib/markdown";
import type { TranslationKey } from "../../lib/i18n/translations";
import { useCodemapStore, type CodemapExchange } from "../../state/codemapStore";
import { useLanguageStore, useT } from "../../state/languageStore";

/** Questions worth one click: how it works, where it leads, what changing it costs. */
const SUGGESTIONS: readonly TranslationKey[] = [
  "codemap.ask.suggestHow",
  "codemap.ask.suggestFlow",
  "codemap.ask.suggestImpact",
];

/**
 * Asking the AI about the selected symbol (CODEMAP-014).
 *
 * The answer is grounded: the map's facts about the symbol go to the engine with the question, and
 * the map itself is offered to it as tools. The engine may read the repository and ask the map; it
 * cannot change anything. Follow-ups continue the same conversation.
 */
export function AskPanel() {
  const t = useT();
  const language = useLanguageStore((s) => s.language);
  const exchanges = useCodemapStore((s) => s.exchanges);
  const runId = useCodemapStore((s) => s.askRunId);
  const ask = useCodemapStore((s) => s.ask);
  const [question, setQuestion] = useState("");
  const [logExpanded, setLogExpanded] = useState(false);
  const running = runId !== null;

  const send = (text: string) => {
    if (text.trim() === "" || running) return;
    setQuestion("");
    void ask(text, language);
  };

  return (
    <div className="flex flex-col gap-3">
      {exchanges.map((exchange, i) => (
        <Exchange key={i} exchange={exchange} />
      ))}

      {running && (
        <AiRunLog runId={runId} running expanded={logExpanded} onToggle={() => setLogExpanded((open) => !open)} />
      )}

      {exchanges.length === 0 && (
        <div className="flex flex-wrap gap-1.5">
          {SUGGESTIONS.map((key) => (
            <Button key={key} size="sm" variant="secondary" disabled={running} onClick={() => send(t(key))}>
              {t(key)}
            </Button>
          ))}
        </div>
      )}

      <div className="flex flex-col gap-1.5">
        <textarea
          value={question}
          onChange={(event) => setQuestion(event.target.value)}
          onKeyDown={(event) => {
            if (event.key === "Enter" && (event.metaKey || event.ctrlKey)) {
              event.preventDefault();
              send(question);
            }
          }}
          rows={3}
          disabled={running}
          placeholder={t(exchanges.length === 0 ? "codemap.ask.placeholder" : "codemap.ask.followUp")}
          aria-label={t("codemap.ask.label")}
          className="cf-focusable w-full resize-y rounded-control border border-[var(--cf-border)] bg-[var(--cf-bg)] px-2 py-1.5 text-body text-[var(--cf-text)] outline-none disabled:opacity-60"
        />
        <div className="flex items-center gap-2">
          <p className="min-w-0 flex-1 text-badge text-[var(--cf-text-muted)]">{t("codemap.ask.readOnly")}</p>
          <Button
            variant="primary"
            size="sm"
            icon={Sparkles}
            pending={running}
            disabled={question.trim() === ""}
            onClick={() => send(question)}
          >
            {t("codemap.ask.send")}
          </Button>
        </div>
      </div>
    </div>
  );
}

function Exchange({ exchange }: { exchange: CodemapExchange }) {
  const t = useT();
  const html = useMemo(() => (exchange.answer ? renderMarkdown(exchange.answer) : ""), [exchange.answer]);
  return (
    <div className="flex flex-col gap-1.5">
      <p className="self-end rounded-control bg-[var(--cf-accent-soft)] px-2.5 py-1.5 text-body text-[var(--cf-text)]">
        {exchange.question}
      </p>
      {exchange.error !== null && (
        <p className="flex items-start gap-1.5 text-body text-[var(--cf-danger)]">
          <AlertTriangle size={14} className="mt-[3px] shrink-0" aria-hidden />
          {t("codemap.ask.failed", { error: exchange.error })}
        </p>
      )}
      {exchange.answer === "" && <p className="text-ui text-[var(--cf-text-muted)]">{t("codemap.ask.stopped")}</p>}
      {html && (
        // Sanitised by renderMarkdown (DOMPurify): the answer is the model's text, not trusted HTML.
        <div className="cf-markdown-preview select-text" dangerouslySetInnerHTML={{ __html: html }} />
      )}
    </div>
  );
}
