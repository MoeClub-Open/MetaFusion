"use client";

// 版本对比检查器（实体历史与 /compare 版本模式共用）：字段级可视化 diff +
// unified 文本 diff。数据源只认快照对象，不关心调用方是内嵌还是独立页面。

import { useMemo, useState } from "react";
import { useI18n } from "@/i18n/I18nProvider";
import { getKindName, useDefinitions } from "@/lib/definitions";
import { revisionChanges } from "./revisionData";
import { Check, Code, Copy, Eye, GitCompare } from "lucide-react";

export interface DiffSnapshot {
  snapshot?: any;
  version: number;
  actor_name?: string;
}

interface FieldDiff {
  key: string;
  label: string;
  oldVal: any;
  newVal: any;
  type: "added" | "removed" | "modified";
}

function formatVal(v: any, emptyLabel: string): string {
  if (v === null || v === undefined) return emptyLabel;
  if (typeof v === "object") return JSON.stringify(v, null, 2);
  return String(v);
}

function computeDiff(oldSnap: any = {}, newSnap: any = {}): FieldDiff[] {
  return Object.entries(revisionChanges(oldSnap, newSnap)).map(([key, values]) => ({
    key,
    label: key.startsWith("attributes.") ? "attr:" + key.slice(11)
      : key.startsWith("translations.") ? "trans:" + key.slice(13) : "field:" + key,
    oldVal: values.old,
    newVal: values.new,
    type: values.old === undefined ? "added" : values.new === undefined ? "removed" : "modified",
  }));
}

function generateUnifiedDiff(oldObj: any, newObj: any, oldLabel: string, newLabel: string): string {
  const oldLines = JSON.stringify(oldObj, null, 2).split("\n");
  const newLines = JSON.stringify(newObj, null, 2).split("\n");
  const header = [
    "--- " + oldLabel,
    "+++ " + newLabel,
    "@@ -1," + oldLines.length + " +1," + newLines.length + " @@",
  ];
  const diffOutput: string[] = [...header];
  const max = Math.max(oldLines.length, newLines.length);
  for (let i = 0; i < max; i++) {
    const o = oldLines[i];
    const n = newLines[i];
    if (o === n) {
      if (o !== undefined) diffOutput.push("  " + o);
    } else {
      if (o !== undefined) diffOutput.push("- " + o);
      if (n !== undefined) diffOutput.push("+ " + n);
    }
  }
  return diffOutput.join("\n");
}

export function RevisionDiffInspector({
  base,
  current,
  onClose,
}: {
  base: DiffSnapshot | null;
  current: DiffSnapshot;
  onClose?: () => void;
}) {
  const { t, tr, locale } = useI18n();
  const { kinds } = useDefinitions();
  const kindLabel = (code: string) =>
    getKindName(kinds, code, locale, tr(`catalog.kind.${code}`, code));
  const [diffTab, setDiffTab] = useState<"visual" | "unified">("visual");
  const [copied, setCopied] = useState(false);

  const diffLabel = (label: string): string => {
    if (label.startsWith("field:")) {
      const field = label.slice("field:".length);
      const known: Record<string, string> = {
        title: t("revisions.fieldTitle"),
        kind: t("revisions.fieldKind"),
        status: t("revisions.fieldStatus"),
        original_language: t("revisions.fieldOriginalLanguage"),
        pictures: t("revisions.fieldPictures"),
        contents: t("catalog.contents"),
        subjects: t("catalog.subjects"),
        external_links: t("catalog.externalLinks"),
        position: t("catalog.position"),
        number: t("catalog.number"),
        parent_id: t("catalog.parent"),
        work_id: kindLabel("work"),
        release_id: kindLabel("release"),
        medium_id: kindLabel("medium"),
        content_unit_id: kindLabel("content_unit"),
      };
      return known[field] || field;
    }
    if (label.startsWith("attr:")) return t("revisions.fieldAttr", { key: label.slice("attr:".length) });
    if (label.startsWith("trans:")) return t("revisions.fieldTrans", { lang: label.slice("trans:".length) });
    return label;
  };

  const diffVal = (v: any): string => {
    if (v === "\u0000") return t("revisions.emptyValue");
    if (v === "\u0001") return t("revisions.unsetValue");
    if (v === "\u0002") return t("revisions.noPictures");
    return formatVal(v, t("revisions.emptyValue"));
  };

  const systemActor = t("revisions.systemActor");
  const initialLabel = t("revisions.initialVersion");
  const activeDiff = useMemo(() => {
    const baseSnap = base ? base.snapshot || base : {};
    const currSnap = current.snapshot || current;
    const fields = computeDiff(baseSnap, currSnap);
    const oldLabel = base ? "v" + base.version + " (" + (base.actor_name || systemActor) + ")" : initialLabel;
    const newLabel = "v" + current.version + " (" + (current.actor_name || systemActor) + ")";
    const unified = generateUnifiedDiff(baseSnap, currSnap, oldLabel, newLabel);
    return { fields, unified, oldLabel, newLabel };
  }, [base, current, systemActor, initialLabel]);

  return (
    <div className="p-5 rounded-xl border border-primary/30 bg-primary/[0.02] dark:bg-primary/[0.04] space-y-4 animate-scale-in shadow-soft">
      <div className="flex flex-col sm:flex-row sm:items-center justify-between gap-3 border-b border-primary/20 pb-3">
        <div className="flex items-center gap-2.5">
          <GitCompare className="w-5 h-5 text-primary shrink-0" />
          <div>
            <h3 className="font-semibold text-text-strong text-sm flex items-center gap-2">
              <span>{t("revisions.diffInspector")}</span>
              <span className="px-2 py-0.5 rounded bg-primary/20 text-primary text-xs font-mono">
                {activeDiff.oldLabel} → {activeDiff.newLabel}
              </span>
            </h3>
            <p className="text-[11px] font-mono text-text-faint mt-0.5">
              {t("revisions.fieldsChanged", { count: activeDiff.fields.length })}
            </p>
          </div>
        </div>
        <div className="flex items-center gap-2">
          <div className="flex items-center bg-black/[0.04] dark:bg-white/[0.06] p-0.5 rounded-lg border border-line text-xs">
            <button
              type="button"
              onClick={() => setDiffTab("visual")}
              className={"px-2.5 py-1 rounded-md transition-all " + (diffTab === "visual" ? "bg-surface text-primary font-bold shadow-2xs" : "text-text-faint hover:text-gray-900 dark:hover:text-white")}
            >
              <span className="flex items-center gap-1"><Eye className="w-3.5 h-3.5" />{t("revisions.visualView")}</span>
            </button>
            <button
              type="button"
              onClick={() => setDiffTab("unified")}
              className={"px-2.5 py-1 rounded-md transition-all " + (diffTab === "unified" ? "bg-surface text-primary font-bold shadow-2xs" : "text-text-faint hover:text-gray-900 dark:hover:text-white")}
            >
              <span className="flex items-center gap-1"><Code className="w-3.5 h-3.5" />{t("revisions.unifiedView")}</span>
            </button>
          </div>
          {onClose && (
            <button
              type="button"
              onClick={onClose}
              className="px-2.5 py-1 rounded-lg text-xs font-mono bg-black/[0.04] dark:bg-white/[0.06] hover:bg-black/[0.08] dark:hover:bg-white/[0.1] text-text-body transition-colors duration-fast ease-soft"
            >
              {t("revisions.closeDiff")}
            </button>
          )}
        </div>
      </div>
      {diffTab === "visual" ? (
        activeDiff.fields.length === 0 ? (
          <div className="p-6 text-center text-xs font-mono text-text-faint bg-surface rounded-lg border border-line-subtle">
            {t("revisions.noDiff")}
          </div>
        ) : (
          <div className="grid grid-cols-1 gap-2.5 font-mono text-xs">
            {activeDiff.fields.map((f, i) => (
              <div key={i} className="p-3 rounded-lg border border-line bg-surface shadow-2xs space-y-1.5">
                <div className="flex items-center justify-between">
                  <span className="font-bold text-text-strong flex items-center gap-1.5">
                    <span className="w-2 h-2 rounded-full bg-primary" />
                    {diffLabel(f.label)}
                  </span>
                  <span className={"px-1.5 py-0.2 rounded text-[10px] uppercase font-bold " + (f.type === "added"
                    ? "bg-emerald-500/15 text-emerald-600 dark:text-success"
                    : f.type === "removed"
                      ? "bg-rose-500/15 text-rose-600 dark:text-danger"
                      : "bg-amber-500/15 text-amber-600 dark:text-warn")}>
                    {f.type}
                  </span>
                </div>
                <div className="grid grid-cols-1 sm:grid-cols-2 gap-2 pt-1 text-[11px]">
                  <div className="p-2 rounded bg-rose-500/[0.06] dark:bg-rose-500/10 border border-rose-500/20 text-rose-700 dark:text-danger-soft break-all whitespace-pre-wrap">
                    <div className="text-[9px] uppercase tracking-wider text-rose-500 font-bold mb-0.5">{t("revisions.oldVersion", { version: base?.version || 0 })}</div>
                    {diffVal(f.oldVal)}
                  </div>
                  <div className="p-2 rounded bg-emerald-500/[0.06] dark:bg-emerald-500/10 border border-emerald-500/20 text-emerald-700 dark:text-success-soft break-all whitespace-pre-wrap">
                    <div className="text-[9px] uppercase tracking-wider text-emerald-500 font-bold mb-0.5">{t("revisions.newVersion", { version: current.version })}</div>
                    {diffVal(f.newVal)}
                  </div>
                </div>
              </div>
            ))}
          </div>
        )
      ) : (
        <div className="relative">
          <pre className="p-3.5 rounded-lg bg-black/[0.04] dark:bg-black/50 border border-line font-mono text-[11px] text-text-strong overflow-x-auto max-h-96 leading-relaxed">
            {activeDiff.unified}
          </pre>
          <button
            type="button"
            onClick={() => { if (typeof navigator !== "undefined" && navigator.clipboard) { void navigator.clipboard.writeText(activeDiff.unified); setCopied(true); setTimeout(() => setCopied(false), 1500); } }}
            className="absolute top-2 right-2 p-1.5 rounded-md bg-surface border border-line text-gray-600 dark:text-gray-400 hover:text-gray-900 dark:hover:text-white shadow-xs text-xs flex items-center gap-1"
            title={t("revisions.copyDiffTitle")}
          >
            {copied ? <Check className="w-3.5 h-3.5 text-emerald-500" /> : <Copy className="w-3.5 h-3.5" />}
          </button>
        </div>
      )}
    </div>
  );
}
