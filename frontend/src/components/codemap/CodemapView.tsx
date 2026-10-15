import { useEffect } from "react";
import { Braces, Network, Waypoints } from "lucide-react";
import { EmptyState } from "../common/EmptyState";
import { PanelHeader } from "../common/PanelHeader";
import { Tabs, type TabOption } from "../common/Tabs";
import { useCodemapStore, type CodemapMode } from "../../state/codemapStore";
import { useRepoMapStore } from "../../state/repomapStore";
import { useT } from "../../state/languageStore";
import { useWorkspaceStore } from "../../state/workspaceStore";
import { MapView } from "./map/MapView";
import { SymbolsView } from "./SymbolsView";

const VIEWS: readonly TabOption<CodemapMode>[] = [
  { id: "map", labelKey: "codemap.view.map", icon: Waypoints },
  { id: "symbols", labelKey: "codemap.view.symbols", icon: Braces },
];

/**
 * The code map module: the repository drawn as folders and files joined by their imports
 * (19-repo-map.md), or its Go and TypeScript symbols as a graph to walk (18-code-map.md). The map is
 * where it opens — it reads any repository with nothing installed — and the symbol view builds its
 * own index only when it is chosen.
 */
export function CodemapView() {
  const t = useT();
  const project = useWorkspaceStore((s) => s.activeProject());
  const rootPath = project?.local_path ?? null;
  const mode = useCodemapStore((s) => s.mode);
  const setMode = useCodemapStore((s) => s.setMode);
  const resetSymbols = useCodemapStore((s) => s.reset);
  const resetMap = useRepoMapStore((s) => s.reset);

  // Repo-scoped: both views follow the selected project, back to nothing when the last one closes.
  useEffect(() => {
    if (rootPath !== null) return;
    resetSymbols();
    resetMap();
  }, [rootPath, resetSymbols, resetMap]);

  if (rootPath === null || project === null) {
    return <EmptyState icon={Network} title={t("codemap.noProject")} />;
  }

  return (
    <div className="flex h-full min-h-0 flex-col">
      <PanelHeader title="codemap.title" icon={Network}>
        <Tabs options={VIEWS} activeId={mode} onSelect={setMode} layoutId="codemap-view" label={t("codemap.view.label")} />
      </PanelHeader>
      {mode === "map" ? <MapView rootPath={rootPath} projectName={project.name} /> : <SymbolsView rootPath={rootPath} />}
    </div>
  );
}
