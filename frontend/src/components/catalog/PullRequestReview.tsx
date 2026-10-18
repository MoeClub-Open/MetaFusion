"use client";

import { useCallback, useEffect, useRef, useState } from "react";
import Link from "next/link";
import { GitCommitHorizontal, GitMerge, GitPullRequest, RefreshCw } from "lucide-react";
import { useI18n } from "@/i18n/I18nProvider";
import { useAuth } from "@/lib/authContext";
import { ApiError } from "@/lib/api";
import { can, CATALOG_REVIEW_MANAGE } from "@/lib/permissions";
import {
  getPullRequest, getWorkspace, listPullRequests, mergePullRequest, updatePullRequest,
  type CatalogObject, type PullRequest, type WorkspaceChange,
} from "@/lib/api/collaboration";
import { Card } from "@/components/ui/Card";
import { RevisionDiffInspector, type DiffSnapshot } from "./RevisionDiffInspector";

type DiffSelection = { base: DiffSnapshot | null; current: DiffSnapshot };
type RequestSession = { key: string; controller: AbortController };
const PAGE_SIZE = 25;
const statusColors: Record<PullRequest["status"], string> = {
  open: "border-success/30 bg-success/10 text-success",
  merged: "border-primary/30 bg-primary/10 text-primary",
  closed: "border-line bg-surfaceSubtle text-text-muted",
};

function Status({ status }: { status: PullRequest["status"] }) {
  const { t } = useI18n();
  return <span className={`inline-flex items-center gap-1.5 rounded-control border px-2 py-1 text-xs font-medium ${statusColors[status]}`}>
    {status === "merged" ? <GitMerge className="h-3.5 w-3.5" aria-hidden="true" /> : <GitPullRequest className="h-3.5 w-3.5" aria-hidden="true" />}
    {t(`collaboration.status.${status}`)}
  </span>;
}

function objectTitle(before: CatalogObject | null, after: CatalogObject | null, id: string) {
  const title = after?.title ?? before?.title;
  return typeof title === "string" && title.trim() ? title : id;
}

function objectVersion(document: CatalogObject | null, fallback: number) {
  return typeof document?.version === "number" ? document.version : fallback;
}

function toDiff(change: WorkspaceChange, oldLabel: string, newLabel: string): DiffSelection {
  return {
    base: change.before === null ? null : {
      snapshot: change.before, version: change.base_version, label: oldLabel,
    },
    // A null after is an explicit deletion in this API, rather than absent history.
    current: { snapshot: change.after === null ? {} : change.after,
      version: objectVersion(change.after, change.base_version), label: newLabel },
  };
}

export function PullRequestDetail({ id }: { id: string }) {
  const { t, locale } = useI18n();
  const { user, loading: authLoading } = useAuth();
  const actorKey = JSON.stringify([id, user?.id || null, [...(user?.permissions || [])].sort()]);
  const sessionKey = JSON.stringify([actorKey, authLoading]);
  const session = useRef<RequestSession | null>(null);
  useEffect(() => {
    const current = { key: sessionKey, controller: new AbortController() };
    session.current = current;
    return () => { current.controller.abort(); };
  }, [sessionKey]);
  const [loadedFor, setLoadedFor] = useState("");
  const [loadedData, setData] = useState<PullRequest>();
  // Hide the previous identity's response during render, before effects run.
  const data = !authLoading && loadedFor === actorKey ? loadedData : undefined;
  const [workspaceMember, setWorkspaceMember] = useState(false);
  const [workspaceAccessible, setWorkspaceAccessible] = useState(false);
  const [title, setTitle] = useState("");
  const titleDirty = useRef(false);
  const request = useRef(0);
  const mutation = useRef(0);
  const busyRef = useRef(false);
  const [loading, setLoading] = useState(true);
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState("");
  const [conflict, setConflict] = useState(false);
  const [notice, setNotice] = useState("");
  const [diff, setDiff] = useState<DiffSelection | null>(null);

  const load = useCallback(async () => {
    const scope = session.current;
    if (authLoading || !scope || scope.key !== sessionKey || scope.controller.signal.aborted) return;
    const token = ++request.current;
    setLoading(true);
    try {
      const next = await getPullRequest(id, scope.controller.signal);
      if (scope.controller.signal.aborted || token !== request.current) return;
      setData(next);
      setLoadedFor(actorKey);
      if (!titleDirty.current) setTitle(next.title);
      setError("");
      setConflict(false);
    } catch (caught) {
      if (scope.controller.signal.aborted || token !== request.current) return;
      setData(undefined);
      setDiff(null);
      setError(t(caught instanceof ApiError && [401, 403, 404].includes(caught.status)
        ? "collaboration.unavailable" : "collaboration.loadFailed"));
    } finally {
      if (!scope.controller.signal.aborted && token === request.current) setLoading(false);
    }
  }, [id, actorKey, sessionKey, authLoading, t]);

  useEffect(() => {
    setData(undefined);
    setDiff(null);
    setWorkspaceMember(false);
    setWorkspaceAccessible(false);
    setNotice("");
    mutation.current++;
    busyRef.current = false;
    setBusy(false);
    titleDirty.current = false;
    if (authLoading) {
      setLoading(true);
      setError("");
      return () => { request.current++; mutation.current++; };
    }
    void load();
    return () => { request.current++; mutation.current++; };
  }, [load, user?.id, authLoading, t]);

  useEffect(() => {
    setWorkspaceMember(false);
    setWorkspaceAccessible(false);
    if (!data?.workspace_id || !user) return;
    const scope = session.current;
    if (!scope || scope.key !== sessionKey || scope.controller.signal.aborted) return;
    let live = true;
    // Public PR viewing never depends on access to its private working copy.
    void getWorkspace(data.workspace_id, scope.controller.signal).then((workspace) => {
      if (live && !scope.controller.signal.aborted) {
        setWorkspaceMember(workspace.owner_id === user.id || workspace.members.includes(user.id));
        setWorkspaceAccessible(true);
      }
    }).catch(() => {});
    return () => { live = false; };
  }, [data?.workspace_id, user?.id, sessionKey]);

  const date = (value: string) => new Date(value).toLocaleString(locale);
  const mayManage = can(user, CATALOG_REVIEW_MANAGE);
  const mayMaintain = !!user && !!data && (user.id === data.author_id || workspaceMember || mayManage);

  const mutate = async (action: "title" | "close" | "reopen" | "merge") => {
    if (!data || busyRef.current) return;
    const scope = session.current;
    if (!scope || scope.key !== sessionKey || scope.controller.signal.aborted) return;
    if (action === "title" && !title.trim()) { setError(t("collaboration.titleRequired")); return; }
    const current = data;
    const token = ++mutation.current;
    busyRef.current = true;
    setBusy(true);
    setError("");
    setConflict(false);
    setNotice("");
    try {
      if (action === "merge") {
        const result = await mergePullRequest(id, { expected_version: current.version, expected_head: current.source_head_id }, scope.controller.signal);
        if (scope.controller.signal.aborted || token !== mutation.current) return;
        if (result.pull_request?.id === id) setData(result.pull_request);
        else await load();
        if (scope.controller.signal.aborted || token !== mutation.current) return;
        setNotice(t("collaboration.pullRequestMerged"));
      } else {
        const next = await updatePullRequest(id, { expected_version: current.version,
          ...(action === "title" ? { title: title.trim() } : { status: action === "close" ? "closed" : "open" }) }, scope.controller.signal);
        if (scope.controller.signal.aborted || token !== mutation.current) return;
        setData(next);
        if (action === "title") { setTitle(next.title); titleDirty.current = false; }
        setNotice(t("collaboration.pullRequestUpdated"));
      }
    } catch (caught) {
      if (scope.controller.signal.aborted || token !== mutation.current) return;
      if (caught instanceof ApiError && caught.status === 409) {
        setConflict(true);
        setError(t("collaboration.refreshConflict"));
      } else if (caught instanceof ApiError && [401, 403, 404].includes(caught.status)) {
        setData(undefined);
        setDiff(null);
        setError(t("collaboration.unavailable"));
      } else setError(t("collaboration.operationFailed"));
    } finally {
      if (!scope.controller.signal.aborted && token === mutation.current) { busyRef.current = false; setBusy(false); }
    }
  };

  const openDiff = (change: WorkspaceChange, label = t("collaboration.proposed")) => {
    setDiff(toDiff(change, t("collaboration.base"), label));
  };

  return <section className="space-y-4" aria-busy={loading || busy}>
    <div className="flex flex-wrap items-center justify-between gap-3">
      <Link href="/pull-requests" className="text-sm text-primary hover:underline">{t("collaboration.pullRequests")}</Link>
      <button type="button" disabled={loading || busy} onClick={() => void load()}
        className="inline-flex items-center gap-1.5 rounded-control border border-line px-3 py-2 text-xs text-text-body hover:bg-surfaceHover disabled:opacity-50">
        <RefreshCw className="h-3.5 w-3.5" aria-hidden="true" />{t("common.refresh")}
      </button>
    </div>
    {error && <div role="alert" className="space-y-2 rounded-card border border-danger/30 bg-danger/5 p-3 text-sm text-danger">
      <p>{error}</p>
      {conflict && data && (workspaceAccessible || user?.id === data.author_id) && <Link
        href={`/workspaces/${encodeURIComponent(data.workspace_id)}`} className="inline-block text-primary underline">{t("collaboration.backToWorkspace")}</Link>}
    </div>}
    {notice && <p role="status" className="text-sm text-success">{notice}</p>}
    {!data ? loading && <p className="text-sm text-text-muted">{t("common.loading")}</p> : <>
      <header className="space-y-3">
        <div className="flex flex-wrap items-center gap-2"><Status status={data.status} /><span className="break-all font-mono text-xs text-text-faint">{data.id}</span></div>
        <h1 className="break-words text-xl font-semibold text-text-strong sm:text-2xl">{data.title}</h1>
        <div className="flex flex-wrap gap-x-4 gap-y-1 text-xs text-text-muted">
          <span>{t("collaboration.author")}: <Link href={`/users/${encodeURIComponent(data.author_id)}`} className="text-primary hover:underline">{data.commits.flatMap(commit => [commit.author, ...commit.authors]).find(author => author.id === data.author_id)?.name || data.author_id}</Link></span>
          <span>{t("collaboration.openedAt")} {date(data.created_at)}</span>
          <span>{t("collaboration.updatedAt")} {date(data.updated_at)}</span>
        </div>
        <p className="break-all text-xs text-text-muted">{t("collaboration.sourceHead")}: <Link className="font-mono text-primary hover:underline" href={`/commits/${encodeURIComponent(data.source_head_id)}`}>{data.source_head_id}</Link></p>
        <p className="break-all text-xs text-text-muted">{t("collaboration.sourceBase")}: {data.source_base_id
          ? <Link className="font-mono text-primary hover:underline" href={`/commits/${encodeURIComponent(data.source_base_id)}`}>{data.source_base_id}</Link> : "—"}</p>
        {data.merged_by && <p className="text-xs text-text-muted">{t("collaboration.mergedBy")}: <Link className="text-primary hover:underline" href={`/users/${encodeURIComponent(data.merged_by)}`}>{data.merged_by}</Link>{data.merged_at && ` · ${t("collaboration.mergedAt")} ${date(data.merged_at)}`}</p>}
      </header>

      {mayMaintain && data.status !== "merged" && <Card className="cv-form">
        <label>{t("collaboration.title")}<input value={title} disabled={busy || loading} maxLength={200}
          onChange={event => { setTitle(event.target.value); titleDirty.current = true; }} /></label>
        <div className="flex flex-wrap gap-2">
          <button type="button" disabled={busy || loading || title === data.title} onClick={() => void mutate("title")}>{t("collaboration.saveTitle")}</button>
          <button type="button" disabled={busy || loading} onClick={() => void mutate(data.status === "open" ? "close" : "reopen")}>
            {t(data.status === "open" ? "collaboration.closePullRequest" : "collaboration.reopenPullRequest")}
          </button>
          {workspaceAccessible && <Link className="self-center text-sm text-primary hover:underline" href={`/workspaces/${encodeURIComponent(data.workspace_id)}`}>{t("collaboration.backToWorkspace")}</Link>}
        </div>
      </Card>}

      <Card className="space-y-3">
        <h2 className="text-sm font-semibold text-text-strong">{t("collaboration.changes")} <span className="font-normal text-text-muted">({t("collaboration.changeCount", { count: data.changes.length })})</span></h2>
        {!data.changes.length ? <p className="text-sm text-text-muted">{t("collaboration.noChanges")}</p> : <ul className="divide-y divide-line-subtle">
          {data.changes.map(change => <li key={`${change.target}:${change.id}`} className="flex flex-wrap items-center justify-between gap-2 py-3 first:pt-0 last:pb-0">
            <div className="min-w-0 space-y-1">
              <p className="break-words text-sm text-text-strong">{objectTitle(change.base, change.committed, change.id)}</p>
              <p className="break-all text-xs text-text-muted">{t(`collaboration.${change.target}`)} · {t(change.base === null ? "collaboration.created" : change.committed === null ? "collaboration.deleted" : "collaboration.modified")} · <span className="font-mono">{change.id}</span></p>
            </div>
            <button type="button" className="shrink-0 rounded-control border border-line px-3 py-2 text-xs text-text-body hover:bg-surfaceHover" onClick={() => openDiff({ ...change, before: change.base, after: change.committed })}>{t("collaboration.viewDiff")}</button>
          </li>)}
        </ul>}
      </Card>

      <Card className="space-y-3">
        <h2 className="text-sm font-semibold text-text-strong">{t("collaboration.commits")} <span className="font-normal text-text-muted">({t("collaboration.commitCount", { count: data.commits.length })})</span></h2>
        <ol className="space-y-3">{data.commits.map(commit => <li key={commit.id} className="min-w-0 border-l-2 border-line-subtle pl-3">
          <div className="flex items-start gap-2"><GitCommitHorizontal className="mt-0.5 h-4 w-4 shrink-0 text-text-muted" aria-hidden="true" /><p className="break-words whitespace-pre-wrap text-sm text-text-strong">{commit.message}</p></div>
          <p className="mt-1 flex flex-wrap gap-x-2 gap-y-1 text-xs text-text-muted"><span>{t("collaboration.committer")}: <Link href={`/users/${encodeURIComponent(commit.author.id)}`} className="text-primary hover:underline">{commit.author.name || commit.author.id}</Link></span><span>{date(commit.created_at)}</span><span>{t(commit.kind === "rebase" ? "collaboration.rebaseCommit" : "collaboration.editCommit")}</span><Link href={`/commits/${encodeURIComponent(commit.id)}`} className="break-all font-mono text-primary hover:underline">{commit.id}</Link></p>
          <p className="mt-1 flex flex-wrap gap-x-2 gap-y-1 text-xs text-text-muted"><span>{t("collaboration.authors")}:</span>{commit.authors.length ? commit.authors.map(author => <Link key={author.id} href={`/users/${encodeURIComponent(author.id)}`} className="text-primary hover:underline">{author.name || author.id}</Link>) : "—"}</p>
          {!!commit.changes.length && <ul className="mt-2 flex flex-wrap gap-2">{commit.changes.map(change => <li key={`${change.target}:${change.id}`}><button type="button" onClick={() => openDiff(change, commit.message)}
            className="max-w-full break-all rounded-control border border-line px-2 py-1 text-xs text-primary hover:bg-surfaceHover">{t("collaboration.viewDiff")} · {objectTitle(change.before, change.after, change.id)}</button></li>)}</ul>}
        </li>)}</ol>
      </Card>

      {mayManage && data.status === "open" && <Card className="cv-form">
        <p className="text-sm text-text-muted">{t("collaboration.mergeNotice")}</p>
        <div><button type="button" className="cv-primary inline-flex items-center gap-1.5" disabled={busy || loading || conflict || !data.commits.length} onClick={() => void mutate("merge")}><GitMerge className="h-4 w-4" aria-hidden="true" />{t("collaboration.mergePullRequest")}</button></div>
      </Card>}
    </>}
    {diff && data && <RevisionDiffInspector base={diff.base} current={diff.current} onClose={() => setDiff(null)} assetContext={{ kind: "pull-request", id }} />}
  </section>;
}

export function PullRequestQueue() {
  const { t, locale } = useI18n();
  const { user, loading: authLoading } = useAuth();
  const [loadedItems, setItems] = useState<PullRequest[]>([]);
  const [status, setStatus] = useState<PullRequest["status"]>("open");
  const [page, setPage] = useState(0);
  const [total, setTotal] = useState(0);
  const [loadedFor, setLoadedFor] = useState("");
  const requestedStatus = user ? status : "merged";
  const actorKey = JSON.stringify([user?.id || null, [...(user?.permissions || [])].sort()]);
  const queryKey = JSON.stringify([actorKey, requestedStatus, page]);
  const sessionKey = JSON.stringify([queryKey, authLoading]);
  const session = useRef<RequestSession | null>(null);
  useEffect(() => {
    const current = { key: sessionKey, controller: new AbortController() };
    session.current = current;
    return () => { current.controller.abort(); };
  }, [sessionKey]);
  const items = !authLoading && loadedFor === queryKey ? loadedItems : [];
  const [error, setError] = useState("");
  const [loading, setLoading] = useState(true);
  const request = useRef(0);
  const load = useCallback(async () => {
    const scope = session.current;
    if (authLoading || !scope || scope.key !== sessionKey || scope.controller.signal.aborted) return;
    const token = ++request.current;
    setLoading(true);
    setError("");
    setItems([]);
    try {
      const result = await listPullRequests({ status: requestedStatus, limit: PAGE_SIZE, offset: page * PAGE_SIZE }, scope.controller.signal);
      if (!scope.controller.signal.aborted && token === request.current) { setItems(result.items); setTotal(result.total); setLoadedFor(queryKey); }
    } catch (caught) {
      if (!scope.controller.signal.aborted && token === request.current) setError(t(caught instanceof ApiError && [401, 403].includes(caught.status)
        ? "collaboration.unavailable" : "collaboration.loadFailed"));
    } finally { if (!scope.controller.signal.aborted && token === request.current) setLoading(false); }
  }, [requestedStatus, page, queryKey, sessionKey, authLoading, t]);
  useEffect(() => { setPage(0); }, [actorKey]);
  useEffect(() => {
    setItems([]);
    if (authLoading) {
      setLoading(true);
      setError("");
      return () => { request.current++; };
    }
    void load();
    return () => { request.current++; };
  }, [load, user?.id, authLoading, t]);

  return <section className="space-y-4" aria-busy={loading}>
    <header className="flex flex-wrap items-center justify-between gap-3">
      <h1 className="flex items-center gap-2 text-xl font-semibold text-text-strong"><GitPullRequest className="h-5 w-5 text-primary" aria-hidden="true" />{t("collaboration.pullRequests")}</h1>
      <button type="button" onClick={() => void load()} disabled={loading} className="inline-flex items-center gap-1.5 rounded-control border border-line px-3 py-2 text-xs text-text-body hover:bg-surfaceHover disabled:opacity-50"><RefreshCw className="h-3.5 w-3.5" aria-hidden="true" />{t("common.refresh")}</button>
    </header>
    <div className="flex flex-wrap gap-2" role="group" aria-label={t("collaboration.pullRequests")}>{(user ? ["open", "merged", "closed"] as const : ["merged"] as const).map(value => <button key={value} type="button" aria-pressed={requestedStatus === value}
      className="rounded-control border border-line px-3 py-2 text-xs text-text-body hover:bg-surfaceHover aria-pressed:border-primary/50 aria-pressed:bg-primary/10 aria-pressed:text-primary"
      onClick={() => { setStatus(value); setPage(0); }}>{t(`collaboration.status.${value}`)}</button>)}</div>
    {error && <p role="alert" className="text-sm text-danger">{error}</p>}
    {loading ? <p className="text-sm text-text-muted">{t("common.loading")}</p> : !error && <>
      {!items.length && <Card><p className="text-sm text-text-muted">{t("collaboration.emptyPullRequests")}</p></Card>}
      <ul className="space-y-2">{items.map(item => <li key={item.id}><Card className="space-y-2">
        <div className="flex flex-wrap items-start gap-2"><Status status={item.status} /><Link className="min-w-0 flex-1 break-words text-sm font-semibold text-text-strong hover:text-primary hover:underline" href={`/pull-requests/${encodeURIComponent(item.id)}`}>{item.title}</Link></div>
        <div className="flex flex-wrap gap-x-3 gap-y-1 text-xs text-text-muted"><Link href={`/users/${encodeURIComponent(item.author_id)}`} className="text-primary hover:underline">{item.commits.flatMap(commit => [commit.author, ...commit.authors]).find(author => author.id === item.author_id)?.name || item.author_id}</Link><span>{new Date(item.created_at).toLocaleString(locale)}</span><span>{t("collaboration.commitCount", { count: item.commits.length })}</span><span>{t("collaboration.changeCount", { count: item.changes.length })}</span></div>
      </Card></li>)}</ul>
    </>}
    <nav className="flex items-center justify-between gap-3 text-xs text-text-muted" aria-label={t("collaboration.pullRequests")}>
      <button type="button" disabled={page === 0 || loading} onClick={() => setPage(value => value - 1)} className="rounded-control border border-line px-3 py-2 disabled:opacity-40">{t("catalog.prevPage")}</button>
      <span>{page + 1}</span>
      <button type="button" disabled={(page + 1) * PAGE_SIZE >= total || loadedFor !== queryKey || loading || !!error} onClick={() => setPage(value => value + 1)} className="rounded-control border border-line px-3 py-2 disabled:opacity-40">{t("catalog.nextPage")}</button>
    </nav>
  </section>;
}
