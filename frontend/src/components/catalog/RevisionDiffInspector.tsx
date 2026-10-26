"use client";

import { Fragment, useEffect, useMemo, useRef, useState } from "react";
import { Check, Code, Copy, Eye, GitCompare } from "lucide-react";
import { useI18n } from "@/i18n/I18nProvider";
import { useDefinitions } from "@/lib/definitions";
import { Modal } from "@/components/ui/Modal";
import { RevisionVisualDiff } from "./RevisionVisualDiff";
import { buildRevisionDiff, isRevisionSnapshot, type DiffHunk } from "./revisionDiff";
import { revisionChanges } from "./revisionData";
import type { CatalogAssetContext } from "@/lib/catalogAssetPreview";
import { readerVisibleDiffData } from "@/lib/readerNotes";

export interface DiffSnapshot {
  label?: string;
  snapshot?: unknown;
  version: number;
  actor_name?: string;
}

// 按真实 diff hunk 展示行号，而不是按两份 JSON 的行位置逐行错配。
export function RevisionCodeDiff({ hunks, oldLabel, newLabel }: {
  hunks: DiffHunk[];
  oldLabel: string;
  newLabel: string;
}) {
  const { t } = useI18n();
  if (!hunks.length) return <p className="p-6 text-center text-sm text-text-muted">{t("revisions.noDiff")}</p>;
  return <div className="min-w-0 overflow-x-auto rounded-lg border border-line bg-surface" tabIndex={0} aria-label={t("revisions.unifiedView")}>
    <table className="w-full border-collapse text-left font-mono text-xs leading-6">
      <thead className="sr-only"><tr><th>{oldLabel}</th><th>{newLabel}</th><th>+ / −</th><th>{t("revisions.unifiedView")}</th></tr></thead>
      <tbody>
        <tr className="bg-surfaceSubtle"><td colSpan={4} className="px-4 py-2 whitespace-pre">{`--- ${oldLabel}\n+++ ${newLabel}`}</td></tr>
        {hunks.map((hunk, hunkIndex) => <HunkRows key={hunkIndex} hunk={hunk} />)}
      </tbody>
    </table>
  </div>;
}

function HunkRows({ hunk }: { hunk: DiffHunk }) {
  return <>
    <tr className="border-y border-sky-500/15 bg-sky-500/10 text-sky-800 dark:text-sky-200">
      <td colSpan={4} className="px-4 py-1 whitespace-pre">{hunk.header}</td>
    </tr>
    {hunk.lines.map((line, index) => <Fragment key={index}><tr data-diff={line.kind}
      className={line.kind === "add" ? "bg-emerald-500/10 text-emerald-950 dark:bg-emerald-500/15 dark:text-emerald-100"
        : line.kind === "remove" ? "bg-rose-500/10 text-rose-950 dark:bg-rose-500/15 dark:text-rose-100" : "text-text-body"}>
      <td className="w-10 min-w-10 select-none border-r border-line-subtle px-2 text-right align-top text-text-muted">{line.oldLine}</td>
      <td className="w-10 min-w-10 select-none border-r border-line-subtle px-2 text-right align-top text-text-muted">{line.newLine}</td>
      <td className="w-7 min-w-7 select-none px-2 align-top" aria-hidden="true">{line.kind === "add" ? "+" : line.kind === "remove" ? "−" : " "}</td>
      <td className="pr-4 align-top"><pre className="m-0 min-h-6 whitespace-pre font-mono">{line.content}</pre></td>
    </tr>{line.noNewline && <tr className="text-text-muted"><td colSpan={4} className="px-4 whitespace-pre">{"\\ No newline at end of file"}</td></tr>}</Fragment>)}
  </>;
}

export function RevisionDiffInspector({ base, current, onClose, assetContext, allowOps = false }: {
  base: DiffSnapshot | null;
  current: DiffSnapshot;
  onClose?: () => void;
  assetContext?: CatalogAssetContext;
  /** 有编辑权时直出运维键 / 机器码；未登录与无编辑权读者面过滤。 */
  allowOps?: boolean;
}) {
  const { t } = useI18n();
  const { definitions, kinds } = useDefinitions();
  const [view, setView] = useState<"visual" | "code">("visual");
  const [copyState, setCopyState] = useState<"idle" | "copying" | "copied" | "error">("idle");
  const systemActor = t("revisions.systemActor");
  const initialLabel = t("revisions.initialVersion");
  const comparison = useMemo(() => {
    const oldLabel = base?.label || (base ? `v${base.version} (${base.actor_name || systemActor})` : initialLabel);
    const newLabel = current.label || `v${current.version} (${current.actor_name || systemActor})`;
    // 空基线只用于首版；缺失历史快照不能拿修订元数据冒充实体。
    if ((base !== null && !isRevisionSnapshot(base.snapshot)) || !isRevisionSnapshot(current.snapshot)) {
      return { oldLabel, newLabel, available: false as const };
    }
    const rawBefore = base === null ? null : base.snapshot;
    const rawAfter = current.snapshot;
    // 读者面：可视化与代码差异共用同一套过滤，避免 JSON 视图漏机器码。
    const before = readerVisibleDiffData(rawBefore, { allowOps });
    const after = readerVisibleDiffData(rawAfter, { allowOps });
    return { oldLabel, newLabel, available: true as const, before, after,
      fields: Object.keys(revisionChanges(before, after)).length,
      diff: buildRevisionDiff(before, after, oldLabel, newLabel) };
  }, [base === null, base?.snapshot, base?.label, base?.version, base?.actor_name,
    current.snapshot, current.label, current.version, current.actor_name, systemActor, initialLabel, allowOps]);
  const activeComparison = useRef(comparison);
  activeComparison.current = comparison;

  useEffect(() => { setCopyState("idle"); }, [comparison]);
  useEffect(() => {
    if (copyState !== "copied") return;
    const timer = setTimeout(() => setCopyState("idle"), 2000);
    return () => clearTimeout(timer);
  }, [copyState]);
  const copyDiff = async () => {
    if (!comparison.available) return;
    setCopyState("copying");
    try {
      await navigator.clipboard.writeText(comparison.diff.unified);
      if (activeComparison.current === comparison) setCopyState("copied");
    } catch {
      if (activeComparison.current === comparison) setCopyState("error");
    }
  };

  const summary = <span className="flex min-w-0 flex-col gap-1">
    <span className="font-semibold text-text-strong">{t("revisions.diffInspector")}</span>
    <span className="break-words text-xs font-normal text-text-muted">{comparison.oldLabel} → {comparison.newLabel}</span>
    {comparison.available && <span className="flex flex-wrap gap-x-3 gap-y-1 text-xs font-normal">
      <span className="text-text-muted">{t("revisions.fieldsChanged", { count: comparison.fields })}</span>
      <span className="text-emerald-700 dark:text-success-soft">+ {t("revisions.addedLines", { count: comparison.diff.additions })}</span>
      <span className="text-rose-700 dark:text-danger-soft">− {t("revisions.removedLines", { count: comparison.diff.deletions })}</span>
    </span>}
  </span>;

  const controls = <div className="flex min-w-0 flex-wrap items-center justify-between gap-2">
    <div className="inline-flex min-w-0 rounded-lg border border-line bg-surfaceSubtle p-1 text-xs" role="group" aria-label={t("revisions.diffInspector")}>
      {([['visual', Eye, "revisions.visualView"], ['code', Code, "revisions.unifiedView"]] as const).map(([value, Icon, label]) => <button key={value}
        type="button" aria-pressed={view === value} onClick={() => setView(value)}
        className="inline-flex items-center gap-1.5 rounded-md px-3 py-2 text-text-muted transition-colors aria-pressed:bg-surface aria-pressed:text-text-strong aria-pressed:shadow-2xs focus-visible:outline focus-visible:outline-2 focus-visible:outline-primary">
        <Icon className="h-4 w-4 shrink-0" aria-hidden="true" />{t(label)}
      </button>)}
    </div>
    {comparison.available && <div className="flex flex-wrap items-center gap-2">
      <span role="status" aria-live="polite" className={`text-xs ${copyState === "error" ? "text-danger" : "text-text-muted"}`}>
        {copyState === "copied" ? t("common.copied") : copyState === "error" ? t("revisions.copyDiffFailed") : ""}
      </span>
      <button type="button" disabled={copyState === "copying"} onClick={() => void copyDiff()}
        className="inline-flex items-center gap-1.5 rounded-md border border-line px-3 py-2 text-xs text-text-body hover:bg-surfaceSubtle disabled:opacity-50"
        aria-label={t("revisions.copyDiffTitle")} title={t("revisions.copyDiffTitle")}>
        {copyState === "copied" ? <Check className="h-4 w-4 text-emerald-600" aria-hidden="true" /> : <Copy className="h-4 w-4" aria-hidden="true" />}
        {t("common.copy")}
      </button>
    </div>}
  </div>;

  const content = !comparison.available ? <p role="status" className="p-6 text-center text-sm text-text-muted">{t("revisions.diffUnavailable")}</p>
    : view === "visual" ? <RevisionVisualDiff before={comparison.before} after={comparison.after} definitions={definitions} kinds={kinds}
      oldLabel={comparison.oldLabel} newLabel={comparison.newLabel} assetContext={assetContext} allowOps={allowOps} />
    : <RevisionCodeDiff hunks={comparison.diff.hunks} oldLabel={comparison.oldLabel} newLabel={comparison.newLabel} />;

  // 历史入口使用真正的 body portal 弹窗；审核页各个操作保留内嵌比较。
  if (onClose) return <Modal open onClose={onClose} title={summary} icon={<GitCompare className="h-5 w-5 shrink-0 text-primary" />}
    maxWidth="max-w-[1440px] h-[90dvh]" footer={controls}>
    <div className="min-w-0">{content}</div>
  </Modal>;

  return <section className="min-w-0 overflow-hidden rounded-panel border border-line bg-surface">
    <header className="flex flex-col gap-4 border-b border-line p-4">
      <div className="flex min-w-0 items-start gap-2.5"><GitCompare className="mt-0.5 h-5 w-5 shrink-0 text-primary" />{summary}</div>
      {controls}
    </header>
    <div className="min-w-0 max-h-[70dvh] overflow-y-auto p-3 sm:p-4">{content}</div>
  </section>;
}
