import { useEffect } from "react";
import { Bot, CircleAlert, CircleCheck, ExternalLink, Loader2, PackageCheck, RotateCw, TriangleAlert, Wrench } from "lucide-react";
import { Button } from "../common/Button";
import { GroupCard } from "./GroupCard";
import { CommandLine } from "./CommandLine";
import { AI_PROVIDERS, type AiProviderOption } from "../../lib/aiProviders";
import { HOMEBREW_URL, TOOL_REQUIREMENTS, commandFor, needsHomebrew, toolState, type ToolRequirement, type ToolState } from "../../lib/requirements";
import { openExternalUrl } from "../../lib/ipc/commands";
import { usePlatform } from "../../lib/platform";
import type { TranslationKey } from "../../lib/i18n/translations";
import { useRequirementsStore } from "../../state/requirementsStore";
import { useProviderStatusStore } from "../../state/providerStatusStore";
import { useWorkspaceStore } from "../../state/workspaceStore";
import { pushErrorToast } from "../../state/toastStore";
import { useT } from "../../state/languageStore";

/** The AI engines a person installs: the command-line ones. The HTTP endpoints need no install. */
const INSTALLABLE_ENGINES = AI_PROVIDERS.filter((p) => p.available && !p.needsApiKey && p.setup?.command);

const STATE_LABELS: Record<ToolState, TranslationKey> = {
  ready: "requirements.state.ready",
  outdated: "requirements.state.outdated",
  missing: "requirements.state.missing",
};

const openDocs = (url: string) => void openExternalUrl(url).catch((e: unknown) => pushErrorToast(String(e)));

/**
 * Settings → Requirements (REQ-003): every program a feature needs and the app does not ship — what
 * it is for, whether this machine has it, and the vendor's own command to get it, to copy or to type
 * into the app's terminal. Someone opening the app for the first time reads here what to install;
 * the features that need one of these say so where they are, and link here.
 */
export function RequirementsSettings() {
  const t = useT();
  const rootPath = useWorkspaceStore((s) => s.activeProject()?.local_path);
  const tools = useRequirementsStore((s) => s.tools);
  const checking = useRequirementsStore((s) => s.checking);
  const error = useRequirementsStore((s) => s.error);
  const check = useRequirementsStore((s) => s.check);
  const checkProviders = useProviderStatusStore((s) => s.checkAll);
  const providersChecking = useProviderStatusStore((s) => s.checking);

  useEffect(() => {
    void check(rootPath);
  }, [check, rootPath]);
  useEffect(() => {
    void checkProviders();
  }, [checkProviders]);

  const recheck = () => {
    void check(rootPath);
    void checkProviders();
  };

  return (
    <div className="space-y-5">
      <div className="flex items-start justify-between gap-3">
        <div>
          <h3 className="mb-1 text-title font-semibold">{t("requirements.title")}</h3>
          <p className="text-body text-[var(--cf-text-muted)]">{t("requirements.subtitle")}</p>
        </div>
        <Button variant="secondary" size="sm" icon={RotateCw} pending={checking || providersChecking} onClick={recheck} className="shrink-0 whitespace-nowrap">
          {t("requirements.recheck")}
        </Button>
      </div>

      {error !== null && (
        <p role="alert" className="flex items-center gap-1.5 text-body text-[var(--cf-danger)]">
          <TriangleAlert size={13} aria-hidden />
          {t("requirements.checkFailed", { error })}
        </p>
      )}

      <GroupCard icon={Wrench} title={t("requirements.toolsTitle")} subtitle={t("requirements.toolsSubtitle")}>
        <ul className="divide-y divide-[var(--cf-border)]">
          {TOOL_REQUIREMENTS.map((requirement) => {
            const found = tools?.find((tool) => tool.id === requirement.id);
            return <ToolRow key={requirement.id} requirement={requirement} state={tools === null ? null : toolState(found)} version={found?.version ?? ""} />;
          })}
        </ul>
      </GroupCard>

      <GroupCard icon={Bot} title={t("requirements.enginesTitle")} subtitle={t("requirements.enginesSubtitle")}>
        <ul className="divide-y divide-[var(--cf-border)]">
          {INSTALLABLE_ENGINES.map((provider) => (
            <EngineRow key={provider.id} provider={provider} />
          ))}
        </ul>
      </GroupCard>

      <p className="flex items-center gap-1.5 text-ui text-[var(--cf-text-muted)]">
        <PackageCheck size={12} aria-hidden />
        {t("requirements.afterInstall")}
      </p>
    </div>
  );
}

function StateBadge({ state, version }: { state: ToolState | null; version: string }) {
  const t = useT();
  if (state === null) return <Loader2 size={12} className="animate-spin text-[var(--cf-text-muted)]" aria-label={t("requirements.checking")} />;
  const ready = state === "ready";
  return (
    <span
      className={`flex shrink-0 items-center gap-1 rounded-full px-1.5 py-0.5 text-badge font-medium ${
        ready
          ? "bg-[color-mix(in_oklab,var(--cf-success)_14%,transparent)] text-[var(--cf-success)]"
          : "bg-[color-mix(in_oklab,var(--cf-warning)_16%,transparent)] text-[var(--cf-warning)]"
      }`}
    >
      {ready ? <CircleCheck size={10} aria-hidden /> : <CircleAlert size={10} aria-hidden />}
      {t(STATE_LABELS[state])}
      {version !== "" && state !== "missing" && <span className="font-mono">{version}</span>}
    </span>
  );
}

/** One tool: what it is for, what was found, and — when it is missing or too old — how to get it. */
function ToolRow({ requirement, state, version }: { requirement: ToolRequirement; state: ToolState | null; version: string }) {
  const t = useT();
  const platform = usePlatform();
  const command = commandFor(requirement.install, platform);
  const needsWork = state === "missing" || state === "outdated";
  return (
    <li className="space-y-1.5 py-2.5 first:pt-0 last:pb-0">
      <div className="flex items-center gap-2">
        <p className="text-body font-medium text-[var(--cf-text)]">{requirement.name}</p>
        <span className="text-badge text-[var(--cf-text-muted)]">
          {t(requirement.need === "required" ? "requirements.need.required" : "requirements.need.feature")}
        </span>
        <span className="ml-auto">
          <StateBadge state={state} version={version} />
        </span>
      </div>
      <p className="text-ui text-[var(--cf-text-muted)]">{t(requirement.purposeKey)}</p>
      {state === "outdated" && requirement.tooOldKey && <p className="text-ui text-[var(--cf-warning)]">{t(requirement.tooOldKey)}</p>}
      {needsWork && command !== null && <InstallCommand command={command} />}
      <Button variant="ghost" size="sm" icon={ExternalLink} onClick={() => openDocs(requirement.docsUrl)}>
        {t("requirements.docs")}
      </Button>
    </li>
  );
}

/** One AI engine, from the same catalogue and status the AI settings use. */
function EngineRow({ provider }: { provider: AiProviderOption }) {
  const t = useT();
  const platform = usePlatform();
  const status = useProviderStatusStore((s) => s.byProvider[provider.id]);
  const setup = provider.setup;
  const command = commandFor(setup?.command, platform);
  // Unknown until the first check answers: no badge rather than a "missing" that flashes.
  const state: ToolState | null = status ? (status.available ? "ready" : "missing") : null;
  const label = provider.label ?? (provider.labelKey ? t(provider.labelKey) : provider.id);
  return (
    <li className="space-y-1.5 py-2.5 first:pt-0 last:pb-0">
      <div className="flex items-center gap-2">
        <p className="text-body font-medium text-[var(--cf-text)]">{label}</p>
        <span className="text-badge text-[var(--cf-text-muted)]">{t("requirements.need.oneEngine")}</span>
        <span className="ml-auto">
          <StateBadge state={state} version="" />
        </span>
      </div>
      {state === "missing" && command !== null && <InstallCommand command={command} />}
      {state === "missing" && setup?.postCommand && (
        <div className="space-y-1">
          <p className="text-badge text-[var(--cf-text-muted)]">{t("requirements.thenRun")}</p>
          <CommandLine command={setup.postCommand} terminal />
        </div>
      )}
      {setup && (
        <Button variant="ghost" size="sm" icon={ExternalLink} onClick={() => openDocs(setup.url)}>
          {t("requirements.docs")}
        </Button>
      )}
    </li>
  );
}

function InstallCommand({ command }: { command: string }) {
  const t = useT();
  return (
    <div className="space-y-1">
      <CommandLine command={command} terminal />
      {needsHomebrew(command) && (
        <p className="text-badge text-[var(--cf-text-muted)]">
          {t("requirements.needsHomebrew")}{" "}
          <button type="button" className="cf-focusable underline hover:text-[var(--cf-text)]" onClick={() => openDocs(HOMEBREW_URL)}>
            brew.sh
          </button>
        </p>
      )}
    </div>
  );
}
