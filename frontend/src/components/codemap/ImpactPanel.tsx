import { Check, Copy, FlaskConical, Layers, Target } from "lucide-react";
import { CollapsibleSection } from "../common/CollapsibleSection";
import { IconButton } from "../common/IconButton";
import { useCopy } from "../../lib/ui/useCopy";
import { useT } from "../../state/languageStore";
import type { CodemapImpact } from "../../types/domain";
import { SymbolRow } from "./SymbolRow";

/**
 * What may break when a symbol changes (CODEMAP-010): where the change surfaces, the symbols above
 * it level by level, and the tests that would notice — each group with the one command that runs
 * exactly those tests, ready to copy into a terminal.
 */
export function ImpactPanel({ impact, onSelect }: { impact: CodemapImpact; onSelect: (id: string) => void }) {
  const t = useT();
  const hasTests = impact.go_tests.length > 0 || impact.ts_tests.length > 0;

  return (
    <div className="flex flex-col gap-4">
      <CollapsibleSection icon={FlaskConical} title={t("codemap.testsToRun")} defaultOpen>
        {!hasTests && <p className="px-1.5 text-body text-[var(--cf-text-muted)]">{t("codemap.noTests")}</p>}
        <div className="flex flex-col gap-2">
          {impact.go_tests.map((group) => (
            <TestGroup
              key={`go:${group.package_dir}`}
              heading={group.working_dir ? `${group.package_dir} — ${group.working_dir}` : group.package_dir}
              items={group.tests}
              command={group.working_dir ? `cd ${group.working_dir} && ${group.command}` : group.command}
            />
          ))}
          {impact.ts_tests.map((group) => (
            <TestGroup
              key={`ts:${group.package_dir}`}
              heading={group.package_dir || "."}
              items={group.files}
              command={
                group.command ? (group.package_dir ? `cd ${group.package_dir} && ${group.command}` : group.command) : ""
              }
            />
          ))}
        </div>
      </CollapsibleSection>

      <CollapsibleSection icon={Target} title={t("codemap.entryPoints")} defaultOpen>
        {impact.entry_points.length === 0 && (
          <p className="px-1.5 text-body text-[var(--cf-text-muted)]">{t("codemap.none")}</p>
        )}
        {impact.entry_points.map((symbol) => (
          <SymbolRow key={symbol.id} symbol={symbol} onSelect={onSelect} />
        ))}
      </CollapsibleSection>

      <CollapsibleSection icon={Layers} title={t("codemap.levels")} defaultOpen={impact.levels.length > 0}>
        {impact.levels.length === 0 && (
          <p className="px-1.5 text-body text-[var(--cf-text-muted)]">{t("codemap.none")}</p>
        )}
        {impact.levels.map((level, i) => (
          <div key={i} className="mb-2">
            <p className="px-1.5 text-badge font-semibold uppercase tracking-wide text-[var(--cf-text-muted)]">
              {t("codemap.level", { n: i + 1, count: level.length })}
            </p>
            {level.map((symbol) => (
              <SymbolRow key={symbol.id} symbol={symbol} onSelect={onSelect} />
            ))}
          </div>
        ))}
        {impact.truncated && <p className="px-1.5 text-ui text-[var(--cf-text-muted)]">{t("codemap.impactTruncated")}</p>}
      </CollapsibleSection>
    </div>
  );
}

function TestGroup({ heading, items, command }: { heading: string; items: readonly string[]; command: string }) {
  const [copied, copy] = useCopy();

  return (
    <div className="rounded-control border border-[var(--cf-border)] p-2">
      <p className="font-mono text-ui text-[var(--cf-text)]">{heading}</p>
      <p className="mt-0.5 text-ui text-[var(--cf-text-muted)]">{items.join(" · ")}</p>
      {command && (
        <div className="mt-1.5 flex items-center gap-1.5 rounded-control bg-[var(--cf-bg)] px-2 py-1">
          <code className="min-w-0 flex-1 truncate font-mono text-ui text-[var(--cf-text)]">{command}</code>
          <IconButton label={copied ? "codemap.copied" : "codemap.copyCommand"} icon={copied ? Check : Copy} onClick={() => copy(command)} />
        </div>
      )}
    </div>
  );
}
