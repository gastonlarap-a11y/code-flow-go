import { useState, type ReactNode } from "react";
import { Copy, ExternalLink, PackageCheck, RefreshCw, TriangleAlert, Undo2, Wand2, Waypoints, type LucideIcon } from "lucide-react";
import { useUiStore } from "../../state/uiStore";
import { ArchifyRefineDialog } from "./ArchifyRefineDialog";
import { BouncingDots } from "../common/BouncingDots";
import { Button } from "../common/Button";
import { IconButton } from "../common/IconButton";
import { Select } from "../common/Select";
import * as api from "../../lib/ipc/commands";
import { archifyExportName } from "../../lib/archify/documents";
import { copyText } from "../../lib/ui/useCopy";
import { useArchifyStore } from "../../state/archifyStore";
import { useT } from "../../state/languageStore";
import { useThemeStore } from "../../state/themeStore";
import { pushErrorToast, useToastStore } from "../../state/toastStore";

/** A centred notice with something to do about it — the empty state, with its way out. */
function Notice({ icon: Icon, title, subtitle, children }: { icon: LucideIcon; title: string; subtitle: string; children: ReactNode }) {
  return (
    <div className="flex h-full w-full flex-col items-center justify-center gap-2 p-8 text-center">
      <Icon size={28} className="mb-2 text-[var(--cf-text-muted)]" aria-hidden />
      <p className="text-relaxed font-medium text-[var(--cf-text)]">{title}</p>
      <p className="max-w-md text-body text-[var(--cf-text-muted)]">{subtitle}</p>
      <div className="mt-2">{children}</div>
    </div>
  );
}

/**
 * An Archify document in the diagram editor (ARCHIFY-004, ARCHIFY-005): the page the user's Archify
 * drew from it, framed in the app.
 *
 * The frame is `sandbox="allow-scripts"` and nothing else, so the page runs in an origin of its
 * own: it can draw, search and zoom itself, and it can reach neither the app nor the network. The
 * app's policy lets it be framed from one path only (`vite.config.ts`).
 */
export function ArchifyPane() {
  const t = useT();
  const theme = useThemeStore((s) => s.resolved);
  const status = useArchifyStore((s) => s.status);
  const page = useArchifyStore((s) => s.page);
  const rendering = useArchifyStore((s) => s.rendering);
  const error = useArchifyStore((s) => s.error);
  const render = useArchifyStore((s) => s.render);
  const openSettings = useUiStore((s) => s.openSettings);

  if (error !== null) {
    return (
      <Notice icon={TriangleAlert} title={t("diagram.archify.failed")} subtitle={error}>
        <Button onClick={() => void render()}>{t("diagram.archify.redraw")}</Button>
      </Notice>
    );
  }
  if (status !== null && !status.installed) {
    return (
      <Notice icon={Waypoints} title={t("diagram.archify.notInstalled")} subtitle={t("diagram.archify.notInstalledHint")}>
        <div className="flex flex-col items-center gap-2">
          <code className="rounded-control bg-[var(--cf-surface-raised)] px-2 py-1 font-mono text-ui text-[var(--cf-text)]">{status.install}</code>
          <div className="flex gap-2">
            <Button
              icon={Copy}
              onClick={() =>
                void copyText(status.install).then(
                  () => useToastStore.getState().pushToast(t("diagram.archify.copied"), "success"),
                  (e: unknown) => pushErrorToast(String(e)),
                )
              }
            >
              {t("diagram.archify.copyInstall")}
            </Button>
            <Button onClick={() => void render()}>{t("diagram.archify.redraw")}</Button>
          </div>
          <Button variant="ghost" size="sm" icon={PackageCheck} onClick={() => openSettings("requirements")}>
            {t("requirements.seeAll")}
          </Button>
        </div>
      </Notice>
    );
  }
  if (page === null) {
    return (
      <div className="flex h-full items-center justify-center text-body text-[var(--cf-text-muted)]">
        <BouncingDots label={t("diagram.archify.rendering")} />
      </div>
    );
  }
  return (
    <div className="relative h-full w-full">
      <iframe
        key={`${page.view_url}:${theme}`}
        title={t("diagram.archify.frameTitle")}
        sandbox="allow-scripts"
        src={`${page.view_url}?theme=${theme}`}
        className="h-full w-full border-0 bg-[var(--cf-bg)]"
      />
      {rendering && (
        <span className="absolute right-3 top-3 rounded-control bg-[var(--cf-surface-raised)] px-2 py-1 text-ui text-[var(--cf-text-muted)] shadow-[var(--cf-shadow-near)]">
          {t("diagram.archify.rendering")}
        </span>
      )}
    </div>
  );
}

type ArchifyExport = "html" | "json";

/** What the editor's bar offers while an Archify document is open. */
export function ArchifyActions() {
  const t = useT();
  const rootPath = useArchifyStore((s) => s.rootPath);
  const path = useArchifyStore((s) => s.path);
  const page = useArchifyStore((s) => s.page);
  const rendering = useArchifyStore((s) => s.rendering);
  const render = useArchifyStore((s) => s.render);
  const canUndo = useArchifyStore((s) => s.previous !== null);
  const undo = useArchifyStore((s) => s.undo);
  const [refining, setRefining] = useState(false);

  const undoLast = async () => {
    try {
      await undo();
      useToastStore.getState().pushToast(t("diagram.archify.undone"), "success");
    } catch (e) {
      pushErrorToast(String(e));
    }
  };

  const exportAs = async (format: ArchifyExport) => {
    if (rootPath === null || path === null) return;
    try {
      const name = archifyExportName(path);
      if (format === "json") {
        await api.saveTextFile(`${name}.archify.json`, await api.readFileText(rootPath, path));
        return;
      }
      if (page === null) return;
      // Fetched from the app's own origin, which `connect-src 'self'` allows; the page is the
      // self-contained file Archify wrote, so the export opens anywhere with no app around it.
      const response = await fetch(page.view_url);
      if (!response.ok) throw new Error(`${response.status} ${response.statusText}`);
      await api.saveTextFile(`${name}.html`, await response.text());
    } catch (e) {
      pushErrorToast(String(e));
    }
  };

  return (
    <>
      {/* Also for a document Archify could not draw: what it refused is what the AI is asked to mend. */}
      <IconButton label="diagram.archify.refine" icon={Wand2} disabled={path === null} onClick={() => setRefining(true)} />
      {canUndo && <IconButton label="diagram.archify.undo" icon={Undo2} onClick={() => void undoLast()} />}
      {refining && path !== null && <ArchifyRefineDialog path={path} onClose={() => setRefining(false)} />}
      <IconButton label="diagram.archify.redraw" icon={RefreshCw} pending={rendering} onClick={() => void render()} />
      <IconButton
        label="diagram.archify.openExternal"
        icon={ExternalLink}
        disabled={page === null}
        onClick={() => page && void api.archifyOpenExternal(page.html_path).catch((e: unknown) => pushErrorToast(String(e)))}
      />
      <Select
        value=""
        ariaLabel={t("diagram.export.action")}
        size="sm"
        placeholder={t("diagram.export.action")}
        options={[
          { value: "html", label: t("diagram.archify.exportHtml") },
          { value: "json", label: t("diagram.archify.exportJson") },
        ]}
        onChange={(value) => void exportAs(value as ArchifyExport)} // the two option values above, and nothing else
      />
    </>
  );
}
