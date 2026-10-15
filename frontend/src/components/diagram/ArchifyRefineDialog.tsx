import { useId, useState } from "react";
import { Wand2 } from "lucide-react";
import { Modal } from "../common/Modal";
import { Button } from "../common/Button";
import { AiRunLog } from "../ai/AiRunLog";
import { ChatModelPicker } from "../ai/ChatModelPicker";
import { useArchifyStore } from "../../state/archifyStore";
import { useLanguageStore, useT } from "../../state/languageStore";
import { useToastStore } from "../../state/toastStore";

/**
 * Asks the AI to change the open Archify document (ARCHIFY-008). The answer is checked by Archify
 * before it replaces the document, and the text it replaced is kept for one undo. The dialog stays
 * while the run goes — it is where the stop button is.
 */
export function ArchifyRefineDialog({ path, onClose }: { path: string; onClose: () => void }) {
  const t = useT();
  const language = useLanguageStore((s) => s.language);
  const runId = useArchifyStore((s) => s.refineRunId);
  const refine = useArchifyStore((s) => s.refine);
  const [instruction, setInstruction] = useState("");
  const [error, setError] = useState<string | null>(null);
  const [logExpanded, setLogExpanded] = useState(false);
  const hintId = useId();
  const running = runId !== null;

  const run = async () => {
    setError(null);
    try {
      if (await refine(instruction, language)) {
        useToastStore.getState().pushToast(t("diagram.archify.refineDone"), "success");
        onClose();
      }
    } catch (e) {
      setError(e instanceof Error ? e.message : String(e));
    }
  };

  return (
    <Modal
      title="diagram.archify.refineTitle"
      subtitle={path}
      icon={Wand2}
      size="lg"
      dismissible={!running}
      onClose={onClose}
      footer={
        <>
          <Button variant="secondary" onClick={onClose} disabled={running}>
            {t("codemap.map.ai.cancel")}
          </Button>
          <Button variant="primary" icon={Wand2} pending={running} disabled={running || instruction.trim() === ""} onClick={() => void run()}>
            {t("diagram.archify.refineRun")}
          </Button>
        </>
      }
    >
      <div className="flex flex-col gap-4">
        <div className="flex flex-col gap-1.5">
          <label className="flex flex-col gap-1.5">
            <span className="text-relaxed text-[var(--cf-text)]">{t("diagram.archify.refineInstruction")}</span>
            <textarea
              autoFocus
              value={instruction}
              onChange={(event) => setInstruction(event.target.value)}
              placeholder={t("diagram.archify.refinePlaceholder")}
              rows={3}
              disabled={running}
              aria-describedby={hintId}
              className="cf-focusable resize-y rounded-control border border-[var(--cf-border)] bg-[var(--cf-bg)] px-2.5 py-1.5 text-body text-[var(--cf-text)] outline-none"
            />
          </label>
          <span id={hintId} className="text-ui text-[var(--cf-text-muted)]">
            {t("diagram.archify.refineHint")}
          </span>
        </div>
        <div className="flex items-center justify-between gap-2">
          <span className="text-ui text-[var(--cf-text-muted)]">{t("codemap.map.ai.model")}</span>
          <ChatModelPicker liveModel={null} chatActive={false} task="diagram" heading="codemap.map.ai.model" />
        </div>
        {running && <AiRunLog runId={runId} running expanded={logExpanded} onToggle={() => setLogExpanded((open) => !open)} />}
        {error !== null && (
          <p role="alert" className="rounded-control border border-[var(--cf-danger)] px-2.5 py-1.5 text-ui text-[var(--cf-text)]">
            {error}
          </p>
        )}
      </div>
    </Modal>
  );
}
