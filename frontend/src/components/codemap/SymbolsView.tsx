import { useEffect, useState } from "react";
import { Database, Flame, LayoutDashboard, Network, PackageCheck, Plug, RefreshCw, ServerCog, TriangleAlert } from "lucide-react";
import { BouncingDots } from "../common/BouncingDots";
import { Chip } from "../common/Chip";
import { CollapsibleSection } from "../common/CollapsibleSection";
import { EmptyState } from "../common/EmptyState";
import { IconButton } from "../common/IconButton";
import { SkeletonRows } from "../common/Skeleton";
import { onCodemapProgress } from "../../lib/ipc/events";
import { stageLabel, stateLabel, stateTone } from "../../lib/codemap/labels";
import { useCodemapStore } from "../../state/codemapStore";
import { useLanguageStore, useT } from "../../state/languageStore";
import { useUiStore } from "../../state/uiStore";
import type { CodemapOverview } from "../../types/domain";
import { SymbolPanel } from "./SymbolPanel";
import { SymbolRow } from "./SymbolRow";

/** How long typing has to pause before the search asks the backend. */
const SEARCH_DEBOUNCE_MS = 150;

/**
 * The code map's symbol view (18-code-map.md): a repository's Go and TypeScript code as one graph
 * to walk.
 *
 * Left, a search over every symbol — or, before anything is typed, the places worth starting from:
 * what is used most, what Go exposes to the frontend, which tables exist. Right, the selected
 * symbol: what is next to it, where it leads, what it affects. It builds its map only when it is
 * shown, so a repository opened on the drawn map never runs go or node for it.
 */
export function SymbolsView({ rootPath }: { rootPath: string }) {
  const t = useT();
  const overview = useCodemapStore((s) => s.overview);
  const building = useCodemapStore((s) => s.building);
  const stage = useCodemapStore((s) => s.stage);
  const error = useCodemapStore((s) => s.error);
  const detail = useCodemapStore((s) => s.detail);
  const candidates = useCodemapStore((s) => s.candidates);
  const loadingSymbol = useCodemapStore((s) => s.loadingSymbol);
  const open = useCodemapStore((s) => s.open);
  const refresh = useCodemapStore((s) => s.refresh);
  const setStage = useCodemapStore((s) => s.setStage);
  const select = useCodemapStore((s) => s.select);
  const archify = useCodemapStore((s) => s.archify);
  const archifying = useCodemapStore((s) => s.archifying);
  const language = useLanguageStore((s) => s.language);

  useEffect(() => {
    void open(rootPath);
  }, [rootPath, open]);

  useEffect(() => {
    const unlisten = onCodemapProgress((event) => {
      if (event.root_path === rootPath) setStage(event.stage);
    });
    return () => void unlisten.then((stop) => stop());
  }, [rootPath, setStage]);

  return (
    <div className="flex min-h-0 flex-1 flex-col">
      <div className="flex h-9 shrink-0 items-center gap-2 border-b border-[var(--cf-border)] px-2">
        <div className="min-w-0 flex-1">{overview && <StatusLine overview={overview} />}</div>
        <div className="ml-auto flex items-center gap-0.5">
          {overview && overview.status.archify !== "" && (
            <IconButton
              label="codemap.archify.architecture"
              icon={LayoutDashboard}
              pending={archifying}
              onClick={() => void archify("architecture", language)}
            />
          )}
          <IconButton label="codemap.refresh" icon={RefreshCw} pending={building} onClick={() => void refresh()} />
        </div>
      </div>

      {overview && <Notes overview={overview} />}

      {error !== null ? (
        <EmptyState icon={TriangleAlert} title={t("codemap.buildFailed")} subtitle={error} />
      ) : overview === null ? (
        <div className="flex flex-1 items-center justify-center text-body text-[var(--cf-text-muted)]">
          <BouncingDots label={stage ? t(stageLabel(stage)) : t("codemap.building")} />
        </div>
      ) : (
        <div className="flex min-h-0 flex-1">
          <aside className="flex w-80 shrink-0 flex-col border-r border-[var(--cf-border)]">
            <Search />
          </aside>
          <main className="min-w-0 flex-1">
            {loadingSymbol && !detail ? (
              <div className="p-3">
                <SkeletonRows count={10} />
              </div>
            ) : detail ? (
              <SymbolPanel detail={detail} />
            ) : candidates.length > 0 ? (
              <div className="flex flex-col gap-1 p-3">
                <p className="pb-1 text-body text-[var(--cf-text-muted)]">{t("codemap.candidates")}</p>
                {candidates.map((symbol) => (
                  <SymbolRow key={symbol.id} symbol={symbol} onSelect={(id) => void select(id)} />
                ))}
              </div>
            ) : (
              <EmptyState icon={Network} title={t("codemap.pickSymbol")} subtitle={t("codemap.pickSymbolHint")} />
            )}
          </main>
        </div>
      )}
    </div>
  );
}

/** Per language, what the map covers — and how big it is and how long it took. */
function StatusLine({ overview }: { overview: CodemapOverview }) {
  const t = useT();
  const { status } = overview;
  return (
    <div className="flex min-w-0 items-center gap-1.5 overflow-hidden">
      {status.languages
        .filter((language) => language.state !== "absent")
        .map((language) => (
          <Chip key={language.lang} tone={stateTone(language.state)}>
            {language.lang.toUpperCase()} · {t(stateLabel(language.state))}
          </Chip>
        ))}
      <span className="truncate text-ui text-[var(--cf-text-muted)]">
        {t("codemap.counts", {
          nodes: status.nodes.toLocaleString(),
          edges: status.edges.toLocaleString(),
          seconds: (status.duration_ms / 1000).toFixed(1),
        })}
      </span>
    </div>
  );
}

/** What the map could not do, said once at the top rather than discovered by its absence. */
function Notes({ overview }: { overview: CodemapOverview }) {
  const t = useT();
  const openSettings = useUiStore((s) => s.openSettings);
  const problems = overview.status.languages.filter(
    (language) => (language.state === "missing_tool" || language.state === "failed") && language.detail,
  );
  const notes = overview.status.notes;
  if (problems.length === 0 && notes.length === 0) return null;
  return (
    <div className="shrink-0 border-b border-[var(--cf-border)] bg-[color-mix(in_oklab,var(--cf-warning)_8%,transparent)] px-3 py-1.5">
      {problems.map((language) => (
        <p key={language.lang} className="text-ui text-[var(--cf-text)]">
          {language.detail}
        </p>
      ))}
      {/* A tool the map could not find is one to install; the list says which and how (REQ-005). */}
      {problems.some((language) => language.state === "missing_tool") && (
        <button
          type="button"
          className="cf-focusable mt-0.5 flex items-center gap-1 text-ui text-[var(--cf-accent)] hover:underline"
          onClick={() => openSettings("requirements")}
        >
          <PackageCheck size={12} aria-hidden />
          {t("requirements.seeAll")}
        </button>
      )}
      {notes.length > 0 && (
        <details className="text-ui text-[var(--cf-text-muted)]">
          <summary className="cursor-pointer">{t("codemap.notes", { count: notes.length })}</summary>
          <ul className="mt-1 list-disc pl-5">
            {notes.map((note) => (
              <li key={note}>{note}</li>
            ))}
          </ul>
        </details>
      )}
    </div>
  );
}

/** The search box, its results, and — while it is empty — where to start. */
function Search() {
  const t = useT();
  const query = useCodemapStore((s) => s.query);
  const results = useCodemapStore((s) => s.results);
  const searching = useCodemapStore((s) => s.searching);
  const search = useCodemapStore((s) => s.search);
  const select = useCodemapStore((s) => s.select);
  const selected = useCodemapStore((s) => s.selected);
  const overview = useCodemapStore((s) => s.overview);
  const [text, setText] = useState(query);

  useEffect(() => {
    if (text === query) return;
    const timer = setTimeout(() => void search(text), SEARCH_DEBOUNCE_MS);
    return () => clearTimeout(timer);
  }, [text, query, search]);

  const onSelect = (id: string) => void select(id);

  return (
    <div className="flex min-h-0 flex-1 flex-col">
      <div className="p-2">
        <input
          type="search"
          value={text}
          onChange={(event) => setText(event.target.value)}
          placeholder={t("codemap.searchPlaceholder")}
          aria-label={t("codemap.searchLabel")}
          className="cf-focusable w-full rounded-control border border-[var(--cf-border)] bg-[var(--cf-bg)] px-2 py-1 text-body text-[var(--cf-text)] outline-none"
        />
      </div>
      <div className="min-h-0 flex-1 overflow-y-auto overflow-x-hidden px-1 pb-2">
        {text.trim() !== "" ? (
          searching && results.length === 0 ? (
            <SkeletonRows count={6} />
          ) : results.length === 0 ? (
            <p className="px-2 py-3 text-body text-[var(--cf-text-muted)]">{t("codemap.noResults")}</p>
          ) : (
            results.map((symbol) => (
              <SymbolRow key={symbol.id} symbol={symbol} onSelect={onSelect} selected={symbol.id === selected} />
            ))
          )
        ) : (
          overview && <StartingPoints overview={overview} onSelect={onSelect} />
        )}
      </div>
    </div>
  );
}

function StartingPoints({ overview, onSelect }: { overview: CodemapOverview; onSelect: (id: string) => void }) {
  const t = useT();
  return (
    <div className="flex flex-col gap-3 px-1">
      <CollapsibleSection icon={Flame} title={t("codemap.hotspots")} defaultOpen>
        {overview.hotspots.map((hotspot) => (
          <SymbolRow
            key={hotspot.symbol.id}
            symbol={hotspot.symbol}
            onSelect={onSelect}
            trail={<Chip>{hotspot.callers}</Chip>}
          />
        ))}
      </CollapsibleSection>
      {overview.services.length > 0 && (
        <CollapsibleSection icon={ServerCog} title={t("codemap.services")} defaultOpen>
          {overview.services.map((service) => (
            <div key={service.name + service.file} className="px-1.5 py-0.5">
              <p className="text-body font-medium text-[var(--cf-text)]">{service.name}</p>
              <p className="text-ui text-[var(--cf-text-muted)]">
                {service.called_by > 0
                  ? t("codemap.serviceMethods", { count: service.methods, callers: service.called_by })
                  : t("codemap.serviceMethodsOnly", { count: service.methods })}
              </p>
            </div>
          ))}
        </CollapsibleSection>
      )}
      {overview.tables.length > 0 && (
        <CollapsibleSection icon={Database} title={t("codemap.tables")}>
          {overview.tables.map((table) => (
            <SymbolRow
              key={table.table.id}
              symbol={table.table}
              onSelect={onSelect}
              trail={
                <span className="shrink-0 text-badge text-[var(--cf-text-muted)]">
                  {t("codemap.tableUse", { readers: table.readers, writers: table.writers })}
                </span>
              }
            />
          ))}
        </CollapsibleSection>
      )}
      {overview.endpoints > 0 && (
        <p className="flex items-center gap-1.5 px-1.5 text-ui text-[var(--cf-text-muted)]">
          <Plug size={14} aria-hidden />
          {t("codemap.endpoints", { count: overview.endpoints })}
        </p>
      )}
    </div>
  );
}
