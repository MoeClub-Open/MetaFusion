"use client";

import { useCallback, useEffect, useRef, useState } from "react";
import Link from "next/link";
import { useAuth } from "@/lib/authContext";
import { useI18n } from "@/i18n/I18nProvider";
import { getWorkspaceCommit, type WorkspaceChange, type WorkspaceCommit } from "@/lib/api/collaboration";
import { RevisionDiffInspector } from "@/components/catalog/RevisionDiffInspector";
import { localizeCatalogError } from "@/lib/catalogErrors";
import { CommitVersionTools } from "./CommitVersionTools";

export function CommitDetail({ id }: { id: string }) {
  const { user, loading } = useAuth();
  const { t, locale } = useI18n();
  const [data, setData] = useState<WorkspaceCommit>();
  const [error, setError] = useState("");
  const [busy, setBusy] = useState(false);
  const [diff, setDiff] = useState<WorkspaceChange | null>(null);
  const [loadedActor, setLoadedActor] = useState("");
  const actor = user ? JSON.stringify([user.id, [...(user.permissions || [])].sort()]) : "anonymous";
  const contextKey = `${id}:${actor}`;
  const controller = useRef<AbortController | null>(null);
  useEffect(() => { const scope = new AbortController(); controller.current = scope; return () => scope.abort(); }, [contextKey]);
  const request = useRef(0);
  const load = useCallback(async () => {
    const token = ++request.current;
    const signal = controller.current?.signal;
    setData(undefined);
    setDiff(null);
    if (loading) return;
    setBusy(true); setError("");
    try { const result = await getWorkspaceCommit(id, signal); if (token === request.current && !signal?.aborted) { setData(result); setLoadedActor(contextKey); } }
    catch (e) { if (token === request.current && !signal?.aborted) setError(localizeCatalogError((e as Error).message, t)); }
    finally { if (token === request.current && !signal?.aborted) setBusy(false); }
  }, [id, loading, contextKey, t]);
  useEffect(() => { void load(); }, [load]);
  if (loading || busy) return <p>{t("common.loading")}</p>;
  return <section className="space-y-5">
    {error && <p role="alert" className="text-danger">{error}</p>}
    {data && loadedActor === contextKey && <>
      <header className="space-y-2"><h1 className="whitespace-pre-wrap text-2xl font-semibold">{data.message}</h1>
        <p className="break-all font-mono text-sm text-text-muted">{data.id}</p>
        <div className="flex flex-wrap gap-x-3 gap-y-1 text-sm"><span>{t("collaboration.authors")}:</span>{data.authors.map(author => <Link key={author.id} className="text-primary underline" href={`/users/${author.id}`}>{author.name || author.id}</Link>)}</div>
        <p className="text-sm text-text-muted">{t("collaboration.committer")}: {data.committer.id === "system" ? t("versioning.system") : <Link className="text-primary underline" href={`/users/${data.committer.id}`}>{data.committer.name || data.committer.id}</Link>} · {new Date(data.created_at).toLocaleString(locale)}</p>
        {data.merged_at && <p className="text-sm text-success">{t("collaboration.mergedAt")}: {new Date(data.merged_at).toLocaleString(locale)}{data.merged_by && <> · {t("collaboration.mergedBy")}: {data.merged_by === "system" ? t("versioning.system") : <Link className="text-primary underline" href={`/users/${data.merged_by}`}>{data.merged_by}</Link>}</>}</p>}
        <div className="flex flex-wrap gap-3 text-sm">{data.workspace_id && <Link href={`/workspaces/${data.workspace_id}`} className="text-primary underline">{t("collaboration.backToWorkspace")}</Link>}
          {data.pull_request_id && <Link href={`/pull-requests/${data.pull_request_id}`} className="text-primary underline">{t("collaboration.pullRequest")}</Link>}</div>
      </header>
      <CommitVersionTools key={data.id} commit={data}/>
      <h2 className="font-semibold">{t("collaboration.changeCount", { count: data.changes.length })}</h2>
      <ul className="space-y-2">{data.changes.map(change => <li key={`${change.target}:${change.id}`} className="flex flex-wrap items-center justify-between gap-3 rounded-panel border border-line bg-surface p-4">
        <div className="min-w-0"><p className="break-words font-medium">{String(change.after?.title || change.before?.title || change.id)}</p><p className="text-xs text-text-muted">{t(`collaboration.${change.target}`)} · {t(`collaboration.${change.before === null ? "created" : change.after === null ? "deleted" : "modified"}`)}</p></div>
        <button className="rounded-control border border-line px-3 py-2 text-sm" onClick={() => setDiff(change)}>{t("collaboration.viewDiff")}</button>
      </li>)}</ul>
    </>}
    {diff && <RevisionDiffInspector base={diff.before === null ? null : { label: t("collaboration.base"), snapshot: diff.before, version: diff.base_version }}
      current={{ label: t("collaboration.proposed"), snapshot: diff.after, version: diff.base_version + 1, actor_name: data?.author.name }} onClose={() => setDiff(null)} assetContext={{ kind: "commit", id }} allowOps />}
  </section>;
}
