import { useState, type ReactNode } from "react";
import {
  ArrowDownToLine,
  ArrowLeft,
  ArrowUpFromLine,
  Database,
  FlaskConical,
  GitFork,
  Layers,
  Presentation,
  Route,
  Share2,
  Sparkles,
  TriangleAlert,
  Waypoints,
  Workflow,
} from "lucide-react";
import { Button } from "../common/Button";
import { Chip } from "../common/Chip";
import { CollapsibleSection } from "../common/CollapsibleSection";
import { IconButton } from "../common/IconButton";
import { SkeletonRows } from "../common/Skeleton";
import { Tabs, tabPanelProps, type TabOption } from "../common/Tabs";
import { Checkbox } from "../common/Checkbox";
import { edgeLabel, KIND_ICONS, kindLabel, location, shortName } from "../../lib/codemap/labels";
import { useLanguageStore, useT } from "../../state/languageStore";
import { useCodemapStore, type CodemapTab } from "../../state/codemapStore";
import { useUiStore } from "../../state/uiStore";
import type { CodemapRelation, CodemapSymbolDetail } from "../../types/domain";
import { AskPanel } from "./AskPanel";
import { EdgeChip, FlowTree } from "./FlowTree";
import { ImpactPanel } from "./ImpactPanel";
import { SymbolRow } from "./SymbolRow";

const TABS: readonly TabOption<CodemapTab>[] = [
  { id: "relations", labelKey: "codemap.tab.relations", icon: Share2 },
  { id: "flow", labelKey: "codemap.tab.flow", icon: Route },
  { id: "impact", labelKey: "codemap.tab.impact", icon: Waypoints },
  { id: "ask", labelKey: "codemap.tab.ask", icon: Sparkles },
];

/** A symbol's header and its three views: what is next to it, where it leads, what it affects. */
export function SymbolPanel({ detail }: { detail: CodemapSymbolDetail }) {
  const t = useT();
  const openInEditor = useUiStore((s) => s.openInEditor);
  const select = useCodemapStore((s) => s.select);
  const back = useCodemapStore((s) => s.back);
  const trail = useCodemapStore((s) => s.trail);
  const tab = useCodemapStore((s) => s.tab);
  const setTab = useCodemapStore((s) => s.setTab);
  const flow = useCodemapStore((s) => s.flow);
  const impact = useCodemapStore((s) => s.impact);
  const loadingTab = useCodemapStore((s) => s.loadingTab);
  const dataOnly = useCodemapStore((s) => s.dataOnly);
  const setDataOnly = useCodemapStore((s) => s.setDataOnly);

  const drawFlowAction = useCodemapStore((s) => s.drawFlow);
  const [drawing, setDrawing] = useState(false);
  const archifyInstalled = useCodemapStore((s) => (s.overview?.status.archify ?? "") !== "");
  const archify = useCodemapStore((s) => s.archify);
  const archifying = useCodemapStore((s) => s.archifying);
  const language = useLanguageStore((s) => s.language);

  const { symbol } = detail;
  const Icon = KIND_ICONS[symbol.kind];
  const onSelect = (id: string) => void select(id);

  const drawFlow = async () => {
    setDrawing(true);
    try {
      await drawFlowAction(t("codemap.diagram.title", { name: shortName(symbol.name) }), {
        lanes: { ts: t("codemap.diagram.laneTs"), go: t("codemap.diagram.laneGo"), sql: t("codemap.diagram.laneSql") },
        edge: (kind) => t(edgeLabel(kind)),
        cut: (shown, total) => t("codemap.diagram.cut", { shown, total }),
      });
    } finally {
      setDrawing(false);
    }
  };

  return (
    <div className="flex h-full min-h-0 flex-col">
      <header className="flex shrink-0 flex-col gap-1 border-b border-[var(--cf-border)] px-3 py-2">
        <div className="flex items-center gap-2">
          <IconButton label="codemap.back" icon={ArrowLeft} disabled={trail.length === 0} onClick={() => void back()} />
          <Icon size={16} className="shrink-0 text-[var(--cf-text-muted)]" aria-hidden />
          <h2 className="min-w-0 truncate text-relaxed font-semibold text-[var(--cf-text)]">{symbol.name}</h2>
          <Chip>{t(kindLabel(symbol.kind))}</Chip>
          {detail.bound && <Chip tone="accent">{t("codemap.bound", { service: detail.bound })}</Chip>}
        </div>
        {symbol.file && (
          <button
            type="button"
            onClick={() => openInEditor(symbol.file, symbol.line > 0 ? symbol.line : undefined)}
            className="cf-focusable self-start rounded-control font-mono text-ui text-[var(--cf-text-muted)] hover:text-[var(--cf-accent)] hover:underline"
            aria-label={t("codemap.openInEditor", { location: location(symbol.file, symbol.line) })}
          >
            {location(symbol.file, symbol.line)}
          </button>
        )}
        {symbol.signature && (
          <code className="block truncate font-mono text-ui text-[var(--cf-text)]">{symbol.signature}</code>
        )}
        {detail.doc && <p className="text-body text-[var(--cf-text-muted)]">{detail.doc}</p>}
      </header>

      <div className="flex shrink-0 items-center gap-3 px-3 pt-2">
        <Tabs
          options={TABS}
          activeId={tab}
          onSelect={(id) => void setTab(id)}
          layoutId="codemap-tabs"
          label={t("codemap.title")}
        />
        {tab === "flow" && (
          <>
            <label className="flex cursor-pointer items-center gap-1.5 text-ui text-[var(--cf-text)]">
              <Checkbox checked={dataOnly} onChange={(checked) => void setDataOnly(checked)} />
              {t("codemap.dataOnly")}
            </label>
            <Button
              size="sm"
              variant="secondary"
              icon={Workflow}
              className="ml-auto"
              disabled={!flow || loadingTab || flow.root.children.length === 0}
              pending={drawing}
              tooltip={t("codemap.diagram.hint")}
              onClick={() => void drawFlow()}
            >
              {t("codemap.diagram.open")}
            </Button>
            {archifyInstalled && (
              <Button
                size="sm"
                variant="secondary"
                icon={Presentation}
                disabled={!flow || loadingTab || flow.root.children.length === 0}
                pending={archifying}
                tooltip={t("codemap.archify.flowHint")}
                onClick={() => void archify("flow", language)}
              >
                {t("codemap.archify.flow")}
              </Button>
            )}
          </>
        )}
      </div>

      <div className="min-h-0 flex-1 overflow-y-auto px-3 py-3" {...tabPanelProps("codemap-tabs", tab)}>
        {tab === "relations" && <Relations detail={detail} onSelect={onSelect} />}
        {tab === "flow" && (loadingTab || !flow ? <SkeletonRows count={8} /> : <FlowTree flow={flow} onSelect={onSelect} />)}
        {tab === "impact" &&
          (loadingTab || !impact ? <SkeletonRows count={8} /> : <ImpactPanel impact={impact} onSelect={onSelect} />)}
        {tab === "ask" && <AskPanel />}
      </div>
    </div>
  );
}

function Relations({ detail, onSelect }: { detail: CodemapSymbolDetail; onSelect: (id: string) => void }) {
  const t = useT();
  return (
    <div className="flex flex-col gap-4">
      <RelationSection icon={ArrowUpFromLine} title={t("codemap.callers")} relations={detail.callers} onSelect={onSelect} open />
      <RelationSection icon={ArrowDownToLine} title={t("codemap.callees")} relations={detail.callees} onSelect={onSelect} open />
      {detail.implementations.length > 0 && (
        <RelationSection icon={GitFork} title={t("codemap.implementations")} relations={detail.implementations} onSelect={onSelect} open />
      )}
      {detail.implements.length > 0 && (
        <RelationSection icon={Layers} title={t("codemap.implements")} relations={detail.implements} onSelect={onSelect} open />
      )}
      <Section icon={Database} title={t("codemap.tablesReached")} count={detail.tables.length} open>
        {detail.tables.map((use) => (
          <SymbolRow
            key={use.table.id}
            symbol={use.table}
            onSelect={onSelect}
            trail={
              <span className="flex shrink-0 items-center gap-1">
                {use.read && <EdgeChip kind="read" />}
                {use.write && <EdgeChip kind="write" />}
                {use.mention && <EdgeChip kind="mention" />}
                {use.via && (
                  <span className="max-w-[16rem] truncate text-ui text-[var(--cf-text-muted)]">
                    {t("codemap.via", { name: use.via.name })}
                  </span>
                )}
              </span>
            }
          />
        ))}
      </Section>
      <Section icon={FlaskConical} title={t("codemap.tests")} count={detail.tests.length}>
        {detail.tests.map((test) => (
          <SymbolRow key={test.id} symbol={test} onSelect={onSelect} />
        ))}
      </Section>
      {detail.unresolved.length > 0 && (
        <Section icon={TriangleAlert} title={t("codemap.unresolved")} count={detail.unresolved.length} open>
          <p className="px-1.5 pb-1 text-ui text-[var(--cf-text-muted)]">{t("codemap.unresolvedHint")}</p>
          {detail.unresolved.map((at) => (
            <UnresolvedRow key={`${at.file}:${at.line}`} file={at.file} line={at.line} />
          ))}
        </Section>
      )}
    </div>
  );
}

function RelationSection({
  icon,
  title,
  relations,
  onSelect,
  open = false,
}: {
  icon: typeof Database;
  title: string;
  relations: readonly CodemapRelation[];
  onSelect: (id: string) => void;
  open?: boolean;
}) {
  const t = useT();
  return (
    <Section icon={icon} title={title} count={relations.length} open={open}>
      {relations.map((relation) => (
        <SymbolRow
          key={`${relation.symbol.id}|${relation.via?.id ?? ""}`}
          symbol={relation.symbol}
          onSelect={onSelect}
          site={relation.file ? { file: relation.file, line: relation.line } : undefined}
          lead={<EdgeChip kind={relation.kind} />}
          trail={
            relation.via && (
              <span className="max-w-[16rem] truncate text-ui text-[var(--cf-text-muted)]">
                {t("codemap.via", { name: relation.via.name })}
              </span>
            )
          }
        />
      ))}
    </Section>
  );
}

function Section({
  icon,
  title,
  count,
  open = false,
  children,
}: {
  icon: typeof Database;
  title: string;
  count: number;
  open?: boolean;
  children: ReactNode;
}) {
  const t = useT();
  return (
    <CollapsibleSection icon={icon} title={`${title} · ${count}`} defaultOpen={open && count > 0}>
      {count === 0 ? <p className="px-1.5 text-body text-[var(--cf-text-muted)]">{t("codemap.none")}</p> : children}
    </CollapsibleSection>
  );
}

function UnresolvedRow({ file, line }: { file: string; line: number }) {
  const t = useT();
  const openInEditor = useUiStore((s) => s.openInEditor);
  return (
    <button
      type="button"
      onClick={() => openInEditor(file, line)}
      className="cf-focusable block rounded-control px-1.5 font-mono text-ui text-[var(--cf-text-muted)] hover:text-[var(--cf-accent)] hover:underline"
      aria-label={t("codemap.openInEditor", { location: location(file, line) })}
    >
      {location(file, line)}
    </button>
  );
}
