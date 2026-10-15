import { useState } from "react";
import { Check, Copy, SquareTerminal } from "lucide-react";
import { IconButton } from "../common/IconButton";
import { Tooltip } from "../common/Tooltip";
import { copyText } from "../../lib/ui/useCopy";
import { typeInTerminal } from "../../lib/ui/typeInTerminal";
import { useWorkspaceStore } from "../../state/workspaceStore";
import { pushErrorToast } from "../../state/toastStore";
import { useT } from "../../state/languageStore";

/**
 * A copyable one-line command, and — with `terminal` — a button that types it into the app's
 * terminal without running it. Installing is the person's call, so this hands them the exact string
 * rather than running anything (REQ-004).
 */
export function CommandLine({ command, terminal = false }: { command: string; terminal?: boolean }) {
  const t = useT();
  const [copied, setCopied] = useState(false);
  const hasProject = useWorkspaceStore((s) => s.activeProject() !== null);
  return (
    <div className="flex items-center gap-1">
      <Tooltip label={t("api.snippet.copy")}>
        <button
          // Named for what pressing it does: the command alone reads as text, not as a control.
          aria-label={`${t("api.snippet.copy")}: ${command}`}
          onClick={() => {
            void copyText(command).then(
              () => {
                setCopied(true);
                setTimeout(() => setCopied(false), 1400);
              },
              (e: unknown) => pushErrorToast(String(e)),
            );
          }}
          className="group flex min-w-0 flex-1 items-center gap-1.5 rounded-md border border-[var(--cf-border)] bg-black/[0.03] px-2 py-1 text-left font-mono text-badge text-[var(--cf-text)] hover:border-[var(--cf-accent)] dark:bg-white/[0.05]"
        >
          <span className="min-w-0 flex-1 truncate">{command}</span>
          {copied ? (
            <Check size={11} aria-hidden className="shrink-0 text-[var(--cf-success)]" />
          ) : (
            /* Decoration on a control that already has a name — revealing it on hover only sharpens
               what is already there. */
            <Copy size={11} aria-hidden className="shrink-0 text-[var(--cf-text-muted)] opacity-0 group-hover:opacity-100" />
          )}
        </button>
      </Tooltip>
      {terminal && (
        <IconButton
          label={hasProject ? "requirements.openInTerminal" : "requirements.openInTerminalNoProject"}
          icon={SquareTerminal}
          disabled={!hasProject}
          onClick={() => void typeInTerminal(command).catch((e: unknown) => pushErrorToast(String(e)))}
        />
      )}
    </div>
  );
}
