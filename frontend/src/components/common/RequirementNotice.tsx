import { PackageCheck, TriangleAlert } from "lucide-react";
import { Button } from "./Button";
import { CommandLine } from "../settings/CommandLine";
import { TOOL_REQUIREMENTS, commandFor } from "../../lib/requirements";
import { usePlatform } from "../../lib/platform";
import type { TranslationKey } from "../../lib/i18n/translations";
import type { RequirementTool } from "../../types/domain";
import { useUiStore } from "../../state/uiStore";
import { useT } from "../../state/languageStore";

/**
 * A feature telling, where it is, that it needs a program this machine lacks (REQ-005): what is
 * missing, the command that gets it here, and the way to Settings → Requirements, which lists
 * everything. Said before the person presses the button, rather than as the error after.
 */
export function RequirementNotice({ toolId, messageKey }: { toolId: RequirementTool["id"]; messageKey: TranslationKey }) {
  const t = useT();
  const platform = usePlatform();
  const openSettings = useUiStore((s) => s.openSettings);
  const requirement = TOOL_REQUIREMENTS.find((r) => r.id === toolId);
  const command = commandFor(requirement?.install, platform);
  return (
    <div role="status" className="space-y-1.5 rounded-lg border border-[color-mix(in_oklab,var(--cf-warning)_40%,transparent)] bg-[color-mix(in_oklab,var(--cf-warning)_8%,transparent)] p-2.5">
      <p className="flex items-start gap-1.5 text-body text-[var(--cf-text)]">
        <TriangleAlert size={13} className="mt-0.5 shrink-0 text-[var(--cf-warning)]" aria-hidden />
        {t(messageKey)}
      </p>
      {command !== null && <CommandLine command={command} terminal />}
      <Button variant="ghost" size="sm" icon={PackageCheck} onClick={() => openSettings("requirements")}>
        {t("requirements.seeAll")}
      </Button>
    </div>
  );
}
