import { useState } from "react";
import { ChevronDown, ChevronRight, CornerDownRight, Repeat } from "lucide-react";
import { Chip } from "../common/Chip";
import { edgeLabel } from "../../lib/codemap/labels";
import { useT } from "../../state/languageStore";
import type { CodemapEdgeKind, CodemapFlow, CodemapFlowNode } from "../../types/domain";
import { SymbolRow } from "./SymbolRow";

/** Branches open on arrival down to this depth; deeper ones wait to be asked for. */
const OPEN_DEPTH = 4;

/**
 * Everything a symbol leads to (CODEMAP-009), as a tree a person can walk.
 *
 * Each step says *how* it was reached — a call, a render, a dispatch to an implementation, a hop
 * into Go — before *what* it reached, because the edge is the part a reader of a flow is usually
 * checking. A step already expanded higher up is shown once more, marked, and not expanded again.
 */
export function FlowTree({ flow, onSelect }: { flow: CodemapFlow; onSelect: (id: string) => void }) {
  const t = useT();
  if (flow.root.children.length === 0) {
    return <p className="px-2 py-3 text-body text-[var(--cf-text-muted)]">{t("codemap.flowEmpty")}</p>;
  }
  return (
    <div role="tree" aria-label={t("codemap.tab.flow")} className="flex flex-col">
      {flow.root.children.map((child, i) => (
        <FlowBranch key={`${child.symbol.id}-${i}`} node={child} depth={0} onSelect={onSelect} />
      ))}
      {flow.truncated && (
        <p className="px-2 pt-2 text-ui text-[var(--cf-text-muted)]">{t("codemap.truncated", { steps: flow.steps })}</p>
      )}
    </div>
  );
}

function FlowBranch({
  node,
  depth,
  onSelect,
}: {
  node: CodemapFlowNode;
  depth: number;
  onSelect: (id: string) => void;
}) {
  const t = useT();
  const [open, setOpen] = useState(depth < OPEN_DEPTH);
  const hasChildren = node.children.length > 0;
  const Toggle = open ? ChevronDown : ChevronRight;

  return (
    <div role="treeitem" aria-expanded={hasChildren ? open : undefined} aria-level={depth + 1}>
      <div style={{ paddingLeft: depth * 16 }}>
        <SymbolRow
          symbol={node.symbol}
          onSelect={onSelect}
          site={node.file ? { file: node.file, line: node.line } : undefined}
          lead={
            <span className="flex shrink-0 items-center gap-1">
              {hasChildren ? (
                <button
                  type="button"
                  onClick={() => setOpen(!open)}
                  className="cf-focusable flex h-5 w-5 items-center justify-center rounded-control text-[var(--cf-text-muted)]"
                  aria-label={t(open ? "codemap.collapse" : "codemap.expand")}
                >
                  <Toggle size={14} aria-hidden />
                </button>
              ) : (
                <CornerDownRight size={14} className="mx-[3px] text-[var(--cf-text-muted)]" aria-hidden />
              )}
              {node.via && <EdgeChip kind={node.via} />}
            </span>
          }
          trail={
            <>
              {node.repeat && (
                <Chip icon={Repeat} tone="neutral">
                  {t("codemap.repeat")}
                </Chip>
              )}
              {node.truncated && <span className="text-ui text-[var(--cf-text-muted)]">…</span>}
            </>
          }
        />
      </div>
      {open &&
        node.children.map((child, i) => (
          <FlowBranch key={`${child.symbol.id}-${i}`} node={child} depth={depth + 1} onSelect={onSelect} />
        ))}
    </div>
  );
}

/** The edge that led to a step, coloured by what it means for data. */
export function EdgeChip({ kind }: { kind: CodemapEdgeKind }) {
  const t = useT();
  const tone = kind === "write" ? "warning" : kind === "read" ? "info" : kind === "cross" ? "accent" : "neutral";
  return (
    <Chip tone={tone} variant={kind === "cross" ? "solid" : "soft"}>
      {t(edgeLabel(kind))}
    </Chip>
  );
}
