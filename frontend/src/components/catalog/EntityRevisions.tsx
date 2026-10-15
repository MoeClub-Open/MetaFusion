"use client";

import { useState, useMemo } from "react";
import { api, Entity } from "./api";
import { useAuth } from "@/lib/authContext";
import { EntityEditor } from "./EntityEditor";
import { canEditRevision, prepareRevisionRestore } from "./revisionData";
import { RevisionDiffInspector } from "./RevisionDiffInspector";
import { useI18n } from "@/i18n/I18nProvider";
import {
  GitBranch,
  GitCompare,
  History,
  ExternalLink,
  FileText,
  Check,
  Copy,
  ChevronDown,
  ChevronUp,
  Clock,
  Eye,
} from "lucide-react";

export interface RevisionItem {
  id?: number | string;
  version: number;
  actor_id?: string;
  actor_name?: string;
  edit_note?: string;
  summary?: string;
  status?: string;
  sources?: Array<{ kind?: string; citation?: string; url?: string; key?: string; pointers?: string[]; scope?: "commit" }>;
  snapshot?: any;
  created_at: string;
}

export function EntityRevisions({
  revisions = [],
  currentEntity,
}: {
  revisions: RevisionItem[];
  currentEntity?: any;
}) {
  const { t } = useI18n();
  const { user } = useAuth();
  const [restore, setRestore] = useState<{ entity: Entity; base: Entity; revision: RevisionItem } | null>(null);
  const [restoreBusy, setRestoreBusy] = useState(false);
  const [restoreError, setRestoreError] = useState("");
  const beginRestore = async (revision: RevisionItem) => {
    if (!currentEntity?.id) return;
    setRestoreBusy(true);
    setRestoreError("");
    try {
      const current = await api<Entity>(`/catalog/entities/${currentEntity.id}`);
      // useAuth 的 user 是 User | null，canEditRevision 收 undefined 表示未登录。
      if (!canEditRevision(current, user ?? undefined)) throw new Error("forbidden");
      setRestore({ entity: prepareRevisionRestore(current, revision.snapshot), base:current, revision });
    } catch {
      setRestoreError(t("revisions.restoreFailed"));
    } finally {
      setRestoreBusy(false);
    }
  };

  // Selected revision for snapshot viewing
  const [inspectingRev, setInspectingRev] = useState<RevisionItem | null>(null);

  // Selected revisions for diff comparison
  const [diffTarget, setDiffTarget] = useState<{ base: RevisionItem | null; current: RevisionItem | null } | null>(null);

  // 任选两版进 /compare 版本模式：按 version 去重，最多两版，排序后 base=旧版。
  const [picked, setPicked] = useState<RevisionItem[]>([]);
  const togglePick = (rev: RevisionItem) => {
    setPicked((prev) => {
      if (prev.some((p) => p.version === rev.version)) return prev.filter((p) => p.version !== rev.version);
      if (prev.length >= 2) return prev;
      return [...prev, rev];
    });
  };
  const pickedSorted = useMemo(() => [...picked].sort((a, b) => (a.version || 0) - (b.version || 0)), [picked]);
  const compareHref =
    currentEntity?.id && pickedSorted.length === 2
      ? `/compare?revisions=${encodeURIComponent(currentEntity.id)}:${pickedSorted[0].version},${encodeURIComponent(currentEntity.id)}:${pickedSorted[1].version}`
      : "";

  const [expandedSources, setExpandedSources] = useState<Record<string, boolean>>({});
  const [copiedId, setCopiedId] = useState<string | null>(null);

  // Sort revisions newest first
  const sortedRevisions = useMemo(() => {
    return [...revisions].sort((a, b) => (b.version || 0) - (a.version || 0));
  }, [revisions]);

  // Contributors aggregate stats
  const anonymousName = t("revisions.anonymousEditor");
  const contributorStats = useMemo(() => {
    const map = new Map<string, { name: string; count: number }>();
    for (const r of sortedRevisions) {
      const name = r.actor_name || anonymousName;
      const existing = map.get(name) || { name, count: 0 };
      existing.count += 1;
      map.set(name, existing);
    }
    return Array.from(map.values());
  }, [sortedRevisions, anonymousName]);

  const handleCopy = (text: string, id: string) => {
    if (typeof navigator !== "undefined" && navigator.clipboard) {
      navigator.clipboard.writeText(text);
      setCopiedId(id);
      setTimeout(() => setCopiedId(null), 1500);
    }
  };

  // Compare with previous revision
  const handleCompareWithPrev = (curr: RevisionItem, idx: number) => {
    const prev = sortedRevisions[idx + 1] || null;
    setDiffTarget({ base: prev, current: curr });
  };

  return (
    <div className="space-y-6">
      {restoreError && <p role="alert" className="text-sm text-red-500">{restoreError}</p>}
      {restore && (
        <section className="rounded-lg border border-primary/30 p-4 space-y-3">
          <p className="text-sm">{t("revisions.restoreReview", { version: restore.revision.version })}</p>
          <button type="button" onClick={() => setRestore(null)} className="text-sm text-primary hover:underline">
            {t("revisions.close")}
          </button>
          <EntityEditor
            key={`${restore.revision.id}:${restore.entity.version}`}
            initial={restore.entity}
            base={restore.base}
            initialEditNote={t("revisions.restoreNote", { version: restore.revision.version, id: String(restore.revision.id) })}
            initialSources={[{ kind: "publication", citation: `catalog:${restore.entity.id}/revisions/${restore.revision.id}` }]}
            onSaved={() => window.location.reload()}
          />
        </section>
      )}
      {/* Top Header & Contributor Summary (Git Insights Style) */}
      <div className="flex flex-col sm:flex-row sm:items-center justify-between gap-4 p-4 rounded-xl bg-surfaceSubtle border border-line-subtle">
        <div className="flex items-center gap-3">
          <div className="w-9 h-9 rounded-lg bg-primary/10 border border-primary/20 flex items-center justify-center text-primary shrink-0 shadow-2xs">
            <GitBranch className="w-5 h-5" />
          </div>
          <div>
            <div className="flex items-center gap-2">
              <span className="font-semibold text-text-strong text-sm">
                {t("revisions.mainLog")}
              </span>
              <span className="px-2 py-0.5 rounded-full bg-primary/15 text-primary text-[10px] font-mono font-bold">
                {t("revisions.commitsUnit", { count: sortedRevisions.length })}
              </span>
            </div>
            <p className="text-xs text-text-faint font-mono mt-0.5">
              {t("revisions.appendOnly")}
            </p>
          </div>
        </div>

        {/* Contributors Avatars */}
        <div className="flex items-center gap-2">
          <span className="text-[11px] font-mono text-text-muted hidden md:inline">
            {t("revisions.contributors")}
          </span>
          <div className="flex items-center -space-x-1.5 overflow-hidden">
            {contributorStats.map((c, idx) => (
              <div
                key={idx}
                title={t("revisions.contribCountTitle", { name: c.name, count: c.count })}
                className="w-7 h-7 rounded-full bg-surface border-2 border-surface flex items-center justify-center text-xs font-bold text-gray-700 dark:text-gray-200 shadow-xs cursor-default ring-1 ring-black/10 dark:ring-white/10"
              >
                {c.name.slice(0, 1).toUpperCase()}
              </div>
            ))}
          </div>
          <span className="text-xs font-mono font-medium text-text-body ml-1">
            {contributorStats.map(c => "@" + c.name).join(", ")}
          </span>
        </div>
      </div>

      {/* Diff Inspector Modal / Banner (If Active) */}
      {diffTarget?.current && (
        <RevisionDiffInspector
          base={diffTarget.base}
          current={diffTarget.current}
          onClose={() => setDiffTarget(null)}
        />
      )}

      {picked.length > 0 && (
        <div className="flex flex-wrap items-center gap-2 p-3 rounded-xl border border-amber-500/30 bg-amber-500/[0.04] text-xs font-mono">
          <GitCompare className="w-4 h-4 text-amber-500 shrink-0" />
          <span className="text-text-body">{t("revisions.pickedCount", { count: picked.length })}</span>
          {compareHref ? (
            <a href={compareHref} className="px-2.5 py-1 rounded-md bg-primary text-white hover:bg-primary/90 transition-colors">
              {t("revisions.openInCompare")}
            </a>
          ) : (
            <span className="text-text-faint">{t("revisions.pickTwo")}</span>
          )}
          <button type="button" onClick={() => setPicked([])} className="ml-auto text-text-faint hover:text-text-body transition-colors">
            {t("revisions.clearPicked")}
          </button>
        </div>
      )}

      {/* Snapshot Inspector Modal / Banner (If Active) */}
      {inspectingRev && (
        <div className="p-5 rounded-xl border border-sky-500/30 bg-sky-500/[0.02] dark:bg-sky-500/[0.04] space-y-3 animate-scale-in shadow-soft">
          <div className="flex items-center justify-between border-b border-sky-500/20 pb-3">
            <div className="flex items-center gap-2">
              <FileText className="w-4 h-4 text-sky-500" />
              <h3 className="font-semibold text-text-strong text-sm">
                {t("revisions.snapshotAt", { version: inspectingRev.version })}
              </h3>
              <span className="text-xs font-mono text-text-faint">
                ({new Date(inspectingRev.created_at).toLocaleString()})
              </span>
            </div>
            <div className="flex items-center gap-2">
              <button
                type="button"
                onClick={() => handleCopy(JSON.stringify(inspectingRev.snapshot || inspectingRev, null, 2), "snap-copy")}
                className="inline-flex items-center gap-1 px-2.5 py-1 rounded-lg border border-line bg-surface text-xs font-mono text-text-body hover:text-primary transition-colors duration-fast ease-soft shadow-2xs"
              >
                {copiedId === "snap-copy" ? <Check className="w-3 h-3 text-emerald-500" /> : <Copy className="w-3 h-3" />}
                <span>{copiedId === "snap-copy" ? t("revisions.copied") : t("revisions.copyJson")}</span>
              </button>
              <button
                type="button"
                onClick={() => setInspectingRev(null)}
                className="px-2.5 py-1 rounded-lg text-xs font-mono bg-black/[0.04] dark:bg-white/[0.06] hover:bg-black/[0.08] dark:hover:bg-white/[0.1] text-text-body transition-colors duration-fast ease-soft"
              >
                {t("revisions.close")}
              </button>
            </div>
          </div>

          <pre className="p-3.5 rounded-lg bg-black/[0.04] dark:bg-black/50 border border-line font-mono text-[11px] text-text-strong overflow-x-auto max-h-80 leading-relaxed">
            {JSON.stringify(inspectingRev.snapshot || inspectingRev, null, 2)}
          </pre>
        </div>
      )}

      {/* Git Commit Tree & History List */}
      <div className="relative pl-6 sm:pl-8 space-y-6 before:absolute before:left-2.5 sm:before:left-3.5 before:top-3 before:bottom-3 before:w-0.5 before:bg-gradient-to-b before:from-primary before:via-primary/30 before:to-transparent">
        {sortedRevisions.map((rev, idx) => {
          const revKey = String(rev.id || idx);
          const author = rev.actor_name || anonymousName;
          const note = rev.edit_note || rev.summary || (idx === sortedRevisions.length - 1 ? t("revisions.initialNote") : t("revisions.updateNote"));
          const sources = rev.sources || [];
          const isLatest = idx === 0;

          return (
            <div key={revKey} className="relative group">
              {/* Commit Node Icon */}
              <div
                className={"absolute -left-6 sm:-left-8 top-1.5 w-5 h-5 rounded-full border-2 flex items-center justify-center shadow-xs transition-transform duration-base ease-soft group-hover:scale-110 " + (
                  isLatest
                    ? "border-primary bg-primary text-white"
                    : "border-black/20 dark:border-white/20 bg-surface text-text-faint group-hover:border-primary group-hover:text-primary"
                )}
              >
                <div className={"w-1.5 h-1.5 rounded-full " + (isLatest ? "bg-white" : "bg-primary")} />
              </div>

              {/* Commit Card */}
              <div className="p-4 rounded-xl border border-line bg-surface hover:shadow-soft transition-all space-y-3">
                {/* Commit Top Bar */}
                <div className="flex flex-col sm:flex-row sm:items-center justify-between gap-2 border-b border-line-subtle pb-2.5">
                  <div className="flex flex-wrap items-center gap-2">
                    <span className={"px-2 py-0.5 rounded-md text-[11px] font-mono font-bold uppercase " + (
                      isLatest
                        ? "bg-primary/15 text-primary border border-primary/30"
                        : "bg-black/[0.04] dark:bg-white/[0.06] text-text-body border border-line"
                    )}>
                      v{rev.version || sortedRevisions.length - idx}
                    </span>

                    {isLatest && (
                      <span className="px-1.5 py-0.2 rounded bg-emerald-500/15 text-emerald-600 dark:text-success text-[10px] font-mono font-bold">
                        {t("revisions.latest")}
                      </span>
                    )}

                    <span className="font-mono text-[11px] text-text-muted">
                      rev #{rev.id || idx + 1}
                    </span>

                    <span className="text-text-body dark:text-gray-600">•</span>

                    <div className="flex items-center gap-1.5 text-xs text-text-body">
                      <div className="w-4 h-4 rounded-full bg-primary/20 text-primary font-bold flex items-center justify-center text-[9px]">
                        {author.slice(0, 1).toUpperCase()}
                      </div>
                      <span className="font-semibold text-text-strong">@{author}</span>
                    </div>
                  </div>

                  <div className="flex items-center gap-1.5 text-xs font-mono text-text-faint">
                    <Clock className="w-3.5 h-3.5" />
                    <span>{new Date(rev.created_at).toLocaleString()}</span>
                  </div>
                </div>

                {/* Commit Message (edit_note) */}
                <div className="text-sm font-medium text-text-strong leading-relaxed">
                  {note}
                </div>

                {/* Commit Evidence / Sources List */}
                {sources.length > 0 && (
                  <div className="pt-1">
                    <button
                      type="button"
                      onClick={() => setExpandedSources(prev => ({ ...prev, [revKey]: !prev[revKey] }))}
                      className="inline-flex items-center gap-1.5 text-[11px] font-mono text-primary hover:underline cursor-pointer"
                    >
                      <ExternalLink className="w-3 h-3" />
                      <span>{t("revisions.evidenceSources", { count: sources.length })}</span>
                      {expandedSources[revKey] ? <ChevronUp className="w-3 h-3" /> : <ChevronDown className="w-3 h-3" />}
                    </button>

                    {expandedSources[revKey] && (
                      <div className="mt-2 p-2.5 rounded-lg bg-surfaceSubtle border border-line-subtle space-y-1.5 text-xs font-mono animate-fade-in">
                        {sources.map((s: any, sIdx: number) => (
                          <div key={sIdx} className="flex items-start gap-2 text-gray-600 dark:text-gray-400">
                            <span className="text-primary font-bold">[{s.kind || "url"}]</span>
                            <span className="text-text-strong font-medium">{s.citation || t("revisions.officialSource")}</span>
                            {/* 历史修订：整笔提交的来源池，不代表支撑本条修订的某个字段。 */}
                            {s.scope === "commit" && (
                              <span className="shrink-0 px-1.5 rounded border border-line text-text-muted" title={t("revisions.scopeCommitHint")}>{t("revisions.scopeCommit")}</span>
                            )}
                            {Array.isArray(s.pointers) && s.pointers.length > 0 && (
                              <span className="text-text-muted">{s.pointers.join(", ")}</span>
                            )}
                            {s.url && (
                              <a
                                href={s.url}
                                target="_blank"
                                rel="noopener noreferrer"
                                className="text-primary hover:underline truncate max-w-xs sm:max-w-md inline-block"
                              >
                                {s.url}
                              </a>
                            )}
                          </div>
                        ))}
                      </div>
                    )}
                  </div>
                )}

                {/* Revision Action Tools */}
                <div className="pt-2 border-t border-line-subtle flex items-center justify-between text-xs font-mono">
                  <div className="flex items-center gap-2">
                    {/* Compare with previous */}
                    {idx < sortedRevisions.length - 1 && (
                      <button
                        type="button"
                        onClick={() => handleCompareWithPrev(rev, idx)}
                        className="inline-flex items-center gap-1 px-2.5 py-1 rounded-md bg-black/[0.04] dark:bg-white/[0.06] hover:bg-primary/15 hover:text-primary text-text-body transition-colors duration-fast ease-soft cursor-pointer shadow-2xs"
                      >
                        <GitCompare className="w-3 h-3 text-amber-500" />
                        <span>{t("revisions.diffVsPrev")}</span>
                      </button>
                    )}

                    <label className="inline-flex items-center gap-1.5 px-2.5 py-1 rounded-md bg-black/[0.04] dark:bg-white/[0.06] hover:bg-amber-500/15 text-text-body transition-colors duration-fast ease-soft cursor-pointer shadow-2xs">
                      <input
                        type="checkbox"
                        checked={picked.some((p) => p.version === rev.version)}
                        onChange={() => togglePick(rev)}
                        className="w-3 h-3 accent-amber-500"
                      />
                      <span>{t("revisions.pick")}</span>
                    </label>

                    {currentEntity && canEditRevision(currentEntity, user ?? undefined) && rev.id && rev.snapshot &&
                      rev.version < currentEntity.version && !["deleted", "merged"].includes((rev.snapshot.state || rev.snapshot.status)) && (
                      <button type="button" disabled={restoreBusy} onClick={() => beginRestore(rev)}
                        className="inline-flex items-center gap-1 px-2.5 py-1 rounded-md text-primary hover:bg-primary/10 disabled:opacity-50">
                        <History className="w-3 h-3" />{t("revisions.restore")}
                      </button>
                    )}
                    {/* View full snapshot */}
                    <button
                      type="button"
                      onClick={() => setInspectingRev(rev)}
                      className="inline-flex items-center gap-1 px-2.5 py-1 rounded-md bg-black/[0.04] dark:bg-white/[0.06] hover:bg-sky-500/15 hover:text-sky-500 text-text-body transition-colors duration-fast ease-soft cursor-pointer shadow-2xs"
                    >
                      <Eye className="w-3 h-3 text-sky-500" />
                      <span>{t("revisions.snapshot")}</span>
                    </button>
                  </div>

                  <span className="text-[10px] text-text-muted">
                    {t("revisions.immutableBadge")}
                  </span>
                </div>
              </div>
            </div>
          );
        })}
      </div>
    </div>
  );
}
