import type { ReactNode } from "react";
import { ArrowLeft, FileCode2, Flame, Folder, FolderOpen, Package, Users } from "lucide-react";
import { Chip } from "../../common/Chip";
import { IconButton } from "../../common/IconButton";
import { SkeletonRows } from "../../common/Skeleton";
import { Tooltip } from "../../common/Tooltip";
import * as api from "../../../lib/ipc/commands";
import { DECL_KIND_ICONS, declKindLabel } from "../../../lib/repomap/labels";
import { useRepoMapStore, type MapPanel as Panel } from "../../../state/repomapStore";
import { useT } from "../../../state/languageStore";
import { useUiStore } from "../../../state/uiStore";
import type { RepoMapDeclHit, RepoMapFileDetail, RepoMapNode, RepoMapOutlineRow } from "../../../types/domain";
import { useAnswer, type Answer } from "./useAnswer";

/**
 * The side panel of the drawn map (REPOMAP-006): what is used most, a file's outline and
 * connections, a search's results, and where a name is used. Every list item leads somewhere — a
 * file to its outline, a declaration to its line in the editor.
 */
export function MapPanel({
  rootPath,
  selectedNode,
  hidden,
}: {
  rootPath: string;
  selectedNode: RepoMapNode | null;
  /** The cards the folder view left undrawn. */
  hidden: readonly RepoMapNode[];
}) {
  const panel = useRepoMapStore((s) => s.panel);
  return (
    <aside className="flex w-80 shrink-0 flex-col border-l border-[var(--cf-border)] bg-[var(--cf-surface)]">
      <div className="min-h-0 flex-1 overflow-y-auto overflow-x-hidden">
        <PanelBody rootPath={rootPath} panel={panel} selectedNode={selectedNode} hidden={hidden} />
      </div>
    </aside>
  );
}

function PanelBody({
  rootPath,
  panel,
  selectedNode,
  hidden,
}: {
  rootPath: string;
  panel: Panel;
  selectedNode: RepoMapNode | null;
  hidden: readonly RepoMapNode[];
}) {
  switch (panel.kind) {
    case "home":
      return <HomePanel rootPath={rootPath} selectedNode={selectedNode} />;
    case "hidden":
      return <HiddenPanel nodes={hidden} />;
    case "file":
      return <FilePanel rootPath={rootPath} path={panel.path} line={panel.line} />;
    case "search":
      return <SearchPanel rootPath={rootPath} query={panel.query} />;
    case "usages":
      return <UsagesPanel rootPath={rootPath} name={panel.name} path={panel.path} />;
  }
}

/** The bar over every screen but the first, with the way back to it. */
function Heading({ children }: { children: ReactNode }) {
  const showPanel = useRepoMapStore((s) => s.showPanel);
  return (
    <div className="sticky top-0 z-10 flex items-center gap-1 border-b border-[var(--cf-border)] bg-[var(--cf-surface)] px-1.5 py-1">
      <IconButton label="codemap.map.close" icon={ArrowLeft} onClick={() => showPanel({ kind: "home" })} />
      <div className="min-w-0 flex-1 truncate text-body font-medium text-[var(--cf-text)]">{children}</div>
    </div>
  );
}

function Section({ title, count, children }: { title: string; count?: number; children: ReactNode }) {
  return (
    <section className="px-2 py-2">
      <h3 className="mb-1 flex items-center gap-1.5 px-1 text-badge font-semibold uppercase tracking-wide text-[var(--cf-text-muted)]">
        {title}
        {count !== undefined && <span className="font-normal normal-case">{count}</span>}
      </h3>
      {children}
    </section>
  );
}

/** What an answer looks like until it is the list it was asked for. */
function Answered<T>({ answer, children }: { answer: Answer<T>; children: (data: T) => ReactNode }) {
  if (answer.state === "loading") return <div className="p-2"><SkeletonRows count={5} /></div>;
  if (answer.state === "failed") return <p className="px-3 py-2 text-ui text-[var(--cf-danger)]">{answer.error}</p>;
  return <>{children(answer.data)}</>;
}

const rowClass =
  "cf-focusable flex w-full min-w-0 items-center gap-1.5 rounded-control px-1.5 py-1 text-left hover:bg-[var(--cf-overlay-hover)]";

function HomePanel({ rootPath, selectedNode }: { rootPath: string; selectedNode: RepoMapNode | null }) {
  const t = useT();
  const setFocus = useRepoMapStore((s) => s.setFocus);
  const showPanel = useRepoMapStore((s) => s.showPanel);
  const mostUsed = useAnswer(`most:${rootPath}`, () => api.repomapMostUsed(rootPath));
  return (
    <>
      {selectedNode?.folder && (
        <div className="m-2 rounded-lg border border-[var(--cf-border)] p-2.5">
          <p className="flex items-center gap-1.5 text-body font-medium text-[var(--cf-text)]">
            <Folder size={14} className="text-[var(--cf-accent)]" aria-hidden />
            <span className="truncate">{selectedNode.id}</span>
          </p>
          <p className="mt-0.5 text-ui text-[var(--cf-text-muted)]">
            {t("codemap.map.stats", { files: selectedNode.files, declarations: selectedNode.declarations })}
          </p>
          <button
            type="button"
            className="cf-focusable mt-1.5 flex items-center gap-1 text-ui text-[var(--cf-accent)] hover:underline"
            onClick={() => void setFocus(selectedNode.id)}
          >
            <FolderOpen size={12} aria-hidden />
            {t("codemap.map.openFolder")}
          </button>
        </div>
      )}
      {selectedNode && !selectedNode.folder && (
        <button type="button" className={`${rowClass} m-1 w-auto`} onClick={() => showPanel({ kind: "file", path: selectedNode.id, line: null })}>
          <FileCode2 size={13} className="shrink-0 text-[var(--cf-text-muted)]" aria-hidden />
          <span className="truncate text-body text-[var(--cf-text)]">{selectedNode.id}</span>
        </button>
      )}
      <Section title={t("codemap.map.mostUsed")}>
        <Answered answer={mostUsed}>
          {(hits) =>
            hits.length === 0 ? (
              <p className="px-1 text-ui text-[var(--cf-text-muted)]">{t("codemap.map.mostUsedEmpty")}</p>
            ) : (
              hits.map((hit) => <DeclRow key={`${hit.path}:${hit.line}:${hit.name}`} hit={hit} icon={Flame} />)
            )
          }
        </Answered>
      </Section>
    </>
  );
}

function HiddenPanel({ nodes }: { nodes: readonly RepoMapNode[] }) {
  const t = useT();
  const setFocus = useRepoMapStore((s) => s.setFocus);
  const showPanel = useRepoMapStore((s) => s.showPanel);
  return (
    <>
      <Heading>{t("codemap.map.moreTitle")}</Heading>
      <div className="p-1">
        {nodes.map((node) => (
          <button
            key={node.id}
            type="button"
            className={rowClass}
            onClick={() => (node.folder ? void setFocus(node.id) : showPanel({ kind: "file", path: node.id, line: null }))}
          >
            {node.folder ? (
              <Folder size={13} className="shrink-0 text-[var(--cf-accent)]" aria-hidden />
            ) : (
              <FileCode2 size={13} className="shrink-0 text-[var(--cf-text-muted)]" aria-hidden />
            )}
            <span className="min-w-0 flex-1 truncate text-body text-[var(--cf-text)]">{node.label}</span>
            {node.imported_by > 0 && <Chip>{node.imported_by}</Chip>}
          </button>
        ))}
      </div>
    </>
  );
}

function FilePanel({ rootPath, path, line }: { rootPath: string; path: string; line: number | null }) {
  const t = useT();
  const openInEditor = useUiStore((s) => s.openInEditor);
  const answer = useAnswer(`file:${rootPath}:${path}`, () => api.repomapFile(rootPath, path));
  return (
    <>
      <Heading>{path.slice(path.lastIndexOf("/") + 1)}</Heading>
      <Answered answer={answer}>
        {(detail) =>
          detail === null ? (
            <p className="px-3 py-2 text-ui text-[var(--cf-text-muted)]">{t("codemap.map.notInMap")}</p>
          ) : (
            <FileDetail detail={detail} highlight={line} onOpen={(at) => openInEditor(detail.path, at)} />
          )
        }
      </Answered>
    </>
  );
}

function FileDetail({ detail, highlight, onOpen }: { detail: RepoMapFileDetail; highlight: number | null; onOpen: (line: number) => void }) {
  const t = useT();
  const showPanel = useRepoMapStore((s) => s.showPanel);
  const toFile = (path: string) => showPanel({ kind: "file", path, line: null });
  return (
    <>
      <div className="px-3 pt-2">
        <button
          type="button"
          className="cf-focusable max-w-full truncate font-mono text-ui text-[var(--cf-accent)] hover:underline"
          aria-label={t("codemap.map.openFile", { path: detail.path })}
          onClick={() => onOpen(1)}
        >
          {detail.path}
        </button>
        <p className="text-ui text-[var(--cf-text-muted)]">{t("codemap.map.fileMeta", { language: detail.language, lines: detail.lines })}</p>
      </div>
      {!detail.parsed ? (
        <p className="px-3 py-2 text-ui text-[var(--cf-text-muted)]">{t("codemap.map.notParsed", { language: detail.language })}</p>
      ) : (
        <Section title={t("codemap.map.declarations")} count={detail.declarations.length}>
          {detail.declarations.length === 0 ? (
            <p className="px-1 text-ui text-[var(--cf-text-muted)]">{t("codemap.map.noDeclarations")}</p>
          ) : (
            detail.declarations.map((row) => (
              <OutlineRow
                key={`${row.line}:${row.name}`}
                row={row}
                path={detail.path}
                highlighted={row.line === highlight}
                onOpen={() => onOpen(row.line)}
              />
            ))
          )}
        </Section>
      )}
      <PathList title={t("codemap.map.imports")} paths={detail.imports} onPick={toFile} />
      <PathList title={t("codemap.map.importedByList")} paths={detail.imported_by} onPick={toFile} />
      {detail.external.length > 0 && (
        <Section title={t("codemap.map.packages")} count={detail.external.length}>
          <div className="flex flex-wrap gap-1 px-1">
            {detail.external.map((name) => (
              <Chip key={name} icon={Package}>
                {name}
              </Chip>
            ))}
          </div>
        </Section>
      )}
    </>
  );
}

function OutlineRow({ row, path, highlighted, onOpen }: { row: RepoMapOutlineRow; path: string; highlighted: boolean; onOpen: () => void }) {
  const t = useT();
  const showPanel = useRepoMapStore((s) => s.showPanel);
  const Icon = DECL_KIND_ICONS[row.kind];
  return (
    <div
      className={`group flex items-center rounded-control ${highlighted ? "bg-[var(--cf-accent-soft)]" : ""}`}
      style={{ paddingLeft: row.depth * 14 }}
    >
      <Tooltip label={row.signature} placement="left">
        <button type="button" className={`${rowClass} min-w-0 flex-1`} onClick={onOpen}>
          <Icon size={12} className="shrink-0 text-[var(--cf-text-muted)]" aria-hidden />
          <span className="min-w-0 truncate text-body text-[var(--cf-text)]">{row.name}</span>
          <span className="shrink-0 text-badge text-[var(--cf-text-muted)]">{t(declKindLabel(row.kind))}</span>
          <span className="ml-auto shrink-0 font-mono text-badge text-[var(--cf-text-muted)]">{row.line}</span>
        </button>
      </Tooltip>
      <IconButton
        label="codemap.map.usages"
        icon={Users}
        className="opacity-0 group-focus-within:opacity-100 group-hover:opacity-100"
        onClick={() => showPanel({ kind: "usages", name: row.name, path })}
      />
    </div>
  );
}

function PathList({ title, paths, onPick }: { title: string; paths: readonly string[]; onPick: (path: string) => void }) {
  if (paths.length === 0) return null;
  return (
    <Section title={title} count={paths.length}>
      {paths.map((path) => (
        <button key={path} type="button" className={rowClass} onClick={() => onPick(path)}>
          <FileCode2 size={12} className="shrink-0 text-[var(--cf-text-muted)]" aria-hidden />
          <span className="truncate font-mono text-ui text-[var(--cf-text)]">{path}</span>
        </button>
      ))}
    </Section>
  );
}

/** A declaration found by name: it opens its file's outline at its line. */
function DeclRow({ hit, icon }: { hit: RepoMapDeclHit; icon?: typeof Flame }) {
  const t = useT();
  const showPanel = useRepoMapStore((s) => s.showPanel);
  const Icon = icon ?? DECL_KIND_ICONS[hit.kind];
  return (
    <div className="group flex items-center">
      <Tooltip label={hit.signature} placement="left">
        <button
          type="button"
          className={`${rowClass} min-w-0 flex-1 flex-col items-start gap-0`}
          onClick={() => showPanel({ kind: "file", path: hit.path, line: hit.line })}
        >
          <span className="flex w-full min-w-0 items-center gap-1.5">
            <Icon size={12} className="shrink-0 text-[var(--cf-text-muted)]" aria-hidden />
            <span className="min-w-0 truncate text-body text-[var(--cf-text)]">{hit.parent ? `${hit.parent}.${hit.name}` : hit.name}</span>
            <span className="shrink-0 text-badge text-[var(--cf-text-muted)]">{t(declKindLabel(hit.kind))}</span>
            {hit.used_by > 0 && (
              <span className="ml-auto flex shrink-0 items-center gap-0.5 text-badge text-[var(--cf-text-muted)]">
                <Users size={11} aria-hidden />
                {hit.used_by}
                <span className="sr-only">{t("codemap.map.usedBy", { count: hit.used_by })}</span>
              </span>
            )}
          </span>
          <span className="w-full truncate pl-[18px] font-mono text-badge text-[var(--cf-text-muted)]">
            {hit.path}:{hit.line}
          </span>
        </button>
      </Tooltip>
      <IconButton
        label="codemap.map.usages"
        icon={Users}
        className="opacity-0 group-focus-within:opacity-100 group-hover:opacity-100"
        onClick={() => showPanel({ kind: "usages", name: hit.parent ? `${hit.parent}.${hit.name}` : hit.name, path: hit.path })}
      />
    </div>
  );
}

function SearchPanel({ rootPath, query }: { rootPath: string; query: string }) {
  const t = useT();
  const answer = useAnswer(`search:${rootPath}:${query}`, () => api.repomapSearch(rootPath, query));
  return (
    <>
      <Heading>{t("codemap.map.searchTitle", { query })}</Heading>
      <div className="p-1">
        <Answered answer={answer}>
          {(hits) =>
            hits.length === 0 ? (
              <p className="px-2 py-2 text-ui text-[var(--cf-text-muted)]">{t("codemap.map.noResults")}</p>
            ) : (
              hits.map((hit) => <DeclRow key={`${hit.path}:${hit.line}:${hit.name}`} hit={hit} />)
            )
          }
        </Answered>
      </div>
    </>
  );
}

function UsagesPanel({ rootPath, name, path }: { rootPath: string; name: string; path: string | null }) {
  const t = useT();
  const openInEditor = useUiStore((s) => s.openInEditor);
  const answer = useAnswer(`usages:${rootPath}:${name}:${path ?? ""}`, () => api.repomapUsages(rootPath, name, path ?? undefined));
  return (
    <>
      <Heading>{t("codemap.map.usagesTitle", { name })}</Heading>
      <Answered answer={answer}>
        {(report) => (
          <>
            <Section title={t("codemap.map.declaredIn")} count={report.declarations.length}>
              {report.declarations.map((hit) => (
                <DeclRow key={`${hit.path}:${hit.line}`} hit={hit} />
              ))}
            </Section>
            <Section title={t("codemap.map.usages")} count={report.usages.length}>
              {report.usages.length === 0 ? (
                <p className="px-1 text-ui text-[var(--cf-text-muted)]">{t("codemap.map.usagesNone")}</p>
              ) : (
                report.usages.map((usage) => (
                  <button
                    key={`${usage.path}:${usage.line}`}
                    type="button"
                    className={`${rowClass} flex-col items-start gap-0`}
                    onClick={() => openInEditor(usage.path, usage.line)}
                  >
                    <span className="flex w-full min-w-0 items-center gap-1.5">
                      <span className="min-w-0 truncate font-mono text-ui text-[var(--cf-text)]">
                        {usage.path}:{usage.line}
                      </span>
                      {!usage.confirmed && (
                        <Tooltip label={t("codemap.map.nameOnlyHint")}>
                          <span className="ml-auto shrink-0">
                            <Chip tone="warning">{t("codemap.map.nameOnly")}</Chip>
                          </span>
                        </Tooltip>
                      )}
                    </span>
                    {usage.within !== "" && <span className="w-full truncate text-badge text-[var(--cf-text-muted)]">{usage.within}</span>}
                  </button>
                ))
              )}
              {report.excluded > 0 && (
                <p className="px-1 pt-1 text-badge text-[var(--cf-text-muted)]">{t("codemap.map.excluded", { count: report.excluded, name })}</p>
              )}
            </Section>
          </>
        )}
      </Answered>
    </>
  );
}
