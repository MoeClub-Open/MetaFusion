"use client";

import { useCallback, useEffect, useRef, useState } from "react";
import Link from "next/link";
import { ArrowLeft, GitBranch, GitCommitHorizontal, GitPullRequest, Plus, RefreshCw, Upload, Users } from "lucide-react";
import { useI18n } from "@/i18n/I18nProvider";
import { useAuth } from "@/lib/authContext";
import { ApiError } from "@/lib/api/client";
import {
  createWorkspaceCommit, getWorkspace, listWorkspaceCommits, pushWorkspace, rebaseWorkspace,
  updateWorkspace, updateWorkspaceMembers,
  type CatalogObject, type ConflictResolution, type PushResult, type Workspace,
  type WorkspaceChange, type WorkspaceCommit, type WorkspaceConflict, type WorkspaceObject,
} from "@/lib/api/collaboration";
import { can, CATALOG_REVIEW_BYPASS } from "@/lib/permissions";
import { localizeCatalogError } from "@/lib/catalogErrors";
import { workspaceConflictResolution, type WorkspaceResolutionChoice } from "@/lib/workspaceConflicts";
import { RevisionDiffInspector, type DiffSnapshot } from "@/components/catalog/RevisionDiffInspector";
import { Modal } from "@/components/ui/Modal";
import { PageHeader } from "@/components/ui/PageShell";

const panel = "rounded-panel border border-line bg-surface p-4 sm:p-5";
const button = "inline-flex items-center justify-center gap-2 rounded-md border border-line px-3 py-2 text-sm text-text-body hover:bg-surfaceSubtle disabled:cursor-default disabled:opacity-50";
const primary = "inline-flex items-center justify-center gap-2 rounded-md bg-primary px-3 py-2 text-sm font-medium text-emphasis keep-white hover:opacity-90 disabled:cursor-default disabled:opacity-50";
const input = "w-full min-w-0 rounded-md border border-line bg-background px-3 py-2 text-sm text-text-body focus-visible:outline focus-visible:outline-2 focus-visible:outline-primary";
const uuid = /^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$/i;

function objectTitle(document: CatalogObject | null, id: string): string {
  return typeof document?.title === "string" && document.title ? document.title : id;
}

function editLink(object: WorkspaceObject, workspaceId: string, objects: WorkspaceObject[]): string | null {
  const query = new URLSearchParams({ workspace: workspaceId });
  if (object.target === "entity") {
    if (!object.document) return null;
    if (object.base === null) {
      query.set("entity", object.id);
      if (typeof object.document.kind === "string") query.set("kind", object.document.kind);
      return `/new?${query}`;
    }
    query.set("edit", "1");
    return `/catalog/${object.id}?${query}`;
  }
  const document = object.document ?? object.committed ?? object.base;
  const source = document?.source_id;
  if (typeof source !== "string") return null;
  const draftSource = objects.find(item => item.target === "entity" && item.id === source && item.base === null && item.document);
  if (draftSource) {
    query.set("entity", source);
    if (typeof draftSource.document?.kind === "string") query.set("kind", draftSource.document.kind);
    query.set("section", "relations");
    return `/new?${query}`;
  }
  query.set("edit", "1");
  query.set("section", "relations");
  return `/catalog/${source}?${query}`;
}

function version(document: CatalogObject | null, fallback: number): number {
  return typeof document?.version === "number" ? document.version : fallback;
}

type OpenDiff = { base: DiffSnapshot | null; current: DiffSnapshot };
type ResolutionChoice = WorkspaceResolutionChoice;
type ConflictSession = { conflicts: WorkspaceConflict[]; version: number; head: string; choices: Record<number, ResolutionChoice> };

// 结构化冲突逐字段展示；只有主动选择自定义时才编辑 JSON。
function ConflictValue({ value, present = true, depth = 0 }: { value: unknown; present?: boolean; depth?: number }) {
  const { t } = useI18n();
  if (!present) return <span className="text-text-muted">{t("collaboration.missingField")}</span>;
  if (value === null || value === undefined) return <span className="text-text-muted">{t("collaboration.noValue")}</span>;
  if (typeof value !== "object") return <span className="whitespace-pre-wrap break-words">{typeof value === "boolean" ? t(value ? "collaboration.valueTrue" : "collaboration.valueFalse") : String(value)}</span>;
  if (Array.isArray(value)) return !value.length ? <span className="text-text-muted">{t("collaboration.emptyList")}</span> : <ol className="space-y-2 border-l border-line pl-3">{value.map((item, index) => <li key={index}><ConflictValue value={item} depth={depth + 1} /></li>)}</ol>;
  const entries = Object.entries(value);
  if (!entries.length) return <span className="text-text-muted">{t("collaboration.noValue")}</span>;
  return <dl className={`space-y-2 ${depth ? "border-l border-line pl-3" : ""}`}>{entries.map(([key, item]) => <div key={key} className="min-w-0"><dt className="break-words text-xs font-medium text-text-muted">{key}</dt><dd className="mt-1 min-w-0"><ConflictValue value={item} depth={depth + 1} /></dd></div>)}</dl>;
}

export function WorkspaceDetail({ id }: { id: string }) {
  const { t, locale } = useI18n();
  const { user, loading: authLoading } = useAuth();
  const [workspace, setWorkspace] = useState<Workspace | null>(null);
  const [commits, setCommits] = useState<WorkspaceCommit[]>([]);
  const [loading, setLoading] = useState(true);
  const [busy, setBusy] = useState("");
  const [error, setError] = useState("");
  const [message, setMessage] = useState("");
  const [name, setName] = useState("");
  const [pushTitle, setPushTitle] = useState("");
  const [inviteId, setInviteId] = useState("");
  const [receipt, setReceipt] = useState<PushResult | null>(null);
  const [diff, setDiff] = useState<OpenDiff | null>(null);
  const [conflict, setConflict] = useState<ConflictSession | null>(null);
  const [loadedEpoch, setLoadedEpoch] = useState<number | null>(null);
  const commitAttempt = useRef<{ key: string; id: string } | null>(null);
  const rebaseAttempt = useRef<{ key: string; id: string } | null>(null);
  const busyRef = useRef(false);
  const sessionKey = JSON.stringify([id, user?.id, [...(user?.permissions ?? [])].sort()]);
  const session = useRef<{ key: string; epoch: number; controller: AbortController | null }>({ key: sessionKey, epoch: 0, controller: null });
  // 账号、权限或路由在 render 时立即失效，避免等 effect 后才遮住旧副本。
  if (session.current.key !== sessionKey) session.current = { key: sessionKey, epoch: session.current.epoch + 1, controller: null };
  const epoch = session.current.epoch;
  const reloadSequence = useRef(0);
  const isOwner = !!workspace && workspace.owner_id === user?.id;
  const isMember = !!workspace && !!user && (isOwner || workspace.members.includes(user.id));
  const editable = isMember && workspace?.state === "active";
  const directPush = can(user, CATALOG_REVIEW_BYPASS);

  const errorText = useCallback((cause: unknown) => {
    if (cause instanceof ApiError && cause.status === 409) return t("collaboration.concurrentChanged");
    return localizeCatalogError(cause instanceof Error ? cause.message : String(cause), t);
  }, [t]);
  const errorTextRef = useRef(errorText);
  errorTextRef.current = errorText;

  const reload = useCallback(async (requestSignal?: AbortSignal) => {
    const signal = requestSignal ?? session.current.controller?.signal;
    if (!signal || signal.aborted || session.current.epoch !== epoch) return;
    const requestSequence = ++reloadSequence.current;
    try {
      const [nextWorkspace, history] = await Promise.all([getWorkspace(id, signal), listWorkspaceCommits(id, signal)]);
      if (signal.aborted || session.current.epoch !== epoch || reloadSequence.current !== requestSequence) return;
      setWorkspace(nextWorkspace);
      setCommits(history.items);
      setName(nextWorkspace.name);
      setLoadedEpoch(epoch);
    } catch (cause) {
      if (!signal.aborted && session.current.epoch === epoch && reloadSequence.current === requestSequence) throw cause;
    }
  }, [id, epoch]);

  useEffect(() => {
    setLoading(true); setLoadedEpoch(null); setWorkspace(null); setCommits([]); setError(""); setMessage("");
    setPushTitle(""); setReceipt(null); setDiff(null); setConflict(null); setBusy("");
    commitAttempt.current = null; rebaseAttempt.current = null; busyRef.current = false;
    if (!user) { setLoading(false); return; }
    const controller = new AbortController();
    session.current.controller = controller;
    let active = true;
    reload(controller.signal).catch(cause => { if (active && session.current.epoch === epoch) { setError(errorTextRef.current(cause)); setLoadedEpoch(epoch); } }).finally(() => { if (active && session.current.epoch === epoch) setLoading(false); });
    return () => {
      active = false;
      controller.abort();
      if (session.current.controller === controller) session.current.controller = null;
    };
  }, [epoch, reload, user?.id]);

  const run = async (action: string, operation: (signal: AbortSignal) => Promise<void>) => {
    const signal = session.current.controller?.signal;
    if (busyRef.current || session.current.epoch !== epoch || !signal || signal.aborted) return;
    busyRef.current = true;
    setBusy(action); setError("");
    try { await operation(signal); }
    catch (cause) { if (!signal.aborted && session.current.epoch === epoch) setError(errorText(cause)); }
    finally { if (!signal.aborted && session.current.epoch === epoch) { setBusy(""); busyRef.current = false; } }
  };

  const openDiff = (change: WorkspaceChange, oldLabel: string, newLabel: string, author?: string) => setDiff({
    base: change.before === null ? null : { snapshot: change.before, version: version(change.before, change.base_version), label: oldLabel },
    current: { snapshot: change.after ?? {}, version: version(change.after, change.base_version + 1), label: newLabel, actor_name: author },
  });

  const commit = () => {
    if (!workspace || !message.trim()) return;
    const key = `${workspace.version}:${message}`;
    if (commitAttempt.current?.key !== key) commitAttempt.current = { key, id: crypto.randomUUID() };
    const commitId = commitAttempt.current.id;
    void run("commit", async signal => {
      await createWorkspaceCommit(id, { id: commitId, expected_version: workspace.version, message: message.trim() }, signal);
      // 先确认提交成功再清空说明；网络或版本冲突会保留用户输入。
      if (signal.aborted || session.current.epoch !== epoch) return;
      setMessage(""); commitAttempt.current = null; setReceipt(null);
      await reload(signal);
    });
  };

  const push = () => {
    if (!workspace?.head_commit_id) return;
    void run("push", async signal => {
      const next = await pushWorkspace(id, { expected_version: workspace.version, expected_head: workspace.head_commit_id!, title: pushTitle.trim() || workspace.name }, signal);
      if (signal.aborted || session.current.epoch !== epoch) return;
      setReceipt(next); setWorkspace(next.workspace);
      await reload(signal);
    });
  };

  const rebase = (resolutions?: ConflictResolution[]) => {
    if (!workspace?.head_commit_id) return;
    const expectedVersion = resolutions && conflict ? conflict.version : workspace.version;
    const expectedHead = resolutions && conflict ? conflict.head : workspace.head_commit_id;
    const key = JSON.stringify({ expectedVersion, expectedHead, resolutions });
    if (rebaseAttempt.current?.key !== key) rebaseAttempt.current = { key, id: crypto.randomUUID() };
    const rebaseId = rebaseAttempt.current.id;
    void run("rebase", async signal => {
      try {
        const next = await rebaseWorkspace(id, { id: rebaseId, expected_version: expectedVersion, expected_head: expectedHead, resolutions }, signal);
        if (signal.aborted || session.current.epoch !== epoch) return;
        setConflict(null); setReceipt(null); setWorkspace(next.workspace); rebaseAttempt.current = null;
        await reload(signal);
      } catch (cause) {
        if (signal.aborted || session.current.epoch !== epoch) return;
        const details = cause instanceof ApiError && cause.status === 409 ? cause.details as { conflicts?: WorkspaceConflict[] } | undefined : undefined;
        if (details?.conflicts?.length) {
          setConflict({ conflicts: details.conflicts, version: expectedVersion, head: expectedHead, choices: {} });
          return;
        }
        throw cause;
      }
    });
  };

  const resolve = () => {
    if (!conflict) return;
    try {
      const resolutions: ConflictResolution[] = conflict.conflicts.map((item, index) => {
        const choice = conflict.choices[index];
        if (!choice) throw new Error(t("collaboration.chooseEveryConflict"));
        return workspaceConflictResolution(item, choice);
      });
      rebase(resolutions);
    } catch (cause) { setError(cause instanceof SyntaxError ? t("collaboration.invalidJson") : errorText(cause)); }
  };

  const changeMembers = (members: string[]) => {
    if (!workspace) return;
    void run("members", async signal => {
      const next = await updateWorkspaceMembers(id, { expected_version: workspace.version, user_ids: members }, signal);
      if (signal.aborted || session.current.epoch !== epoch) return;
      setWorkspace(next); setInviteId("");
    });
  };

  const status = (state: Workspace["state"]) => t(`collaboration.state.${state}`);
  const date = (value: string) => new Date(value).toLocaleString(locale);
  const dirty = workspace?.objects.filter(object => object.dirty) ?? [];
  const hasUnmerged = commits.some(item => item.merged_at === null);

  if (authLoading || loading) return <p role="status" className="py-8 text-sm text-text-muted">{t("common.loading")}</p>;
  if (!user) return <section className={panel}><p>{t("collaboration.signInRequired")}</p><Link href="/login" className="mt-3 inline-block text-primary underline">{t("collaboration.signIn")}</Link></section>;
  if (loadedEpoch !== epoch) return <p role="status" className="py-8 text-sm text-text-muted">{t("common.loading")}</p>;

  return <div className="min-w-0 space-y-5">
    <Link href="/workspaces" className="inline-flex items-center gap-2 text-sm text-text-muted hover:text-primary"><ArrowLeft className="h-4 w-4" aria-hidden="true" />{t("collaboration.myWorkspaces")}</Link>
    {error && <div role="alert" className="rounded-md border border-danger/30 bg-danger/5 p-3 text-sm text-danger">{error}</div>}
    {!workspace ? <button type="button" disabled={!!busy} className={button} onClick={() => void run("refresh", reload)}>{t("common.refresh")}</button> : <>
      <PageHeader icon={<GitBranch className="mt-1 h-5 w-5 shrink-0 text-primary" aria-hidden="true" />} title={workspace.name} subtitle={t("collaboration.workspaceDescription")}
        actions={<><span className="rounded-md border border-line bg-surfaceSubtle px-2 py-1 text-xs text-text-muted">{status(workspace.state)}</span><button type="button" className={button} disabled={!!busy} onClick={() => void run("refresh", reload)}><RefreshCw className={`h-4 w-4 ${busy === "refresh" ? "animate-spin" : ""}`} aria-hidden="true" />{t("common.refresh")}</button></>} />

      {receipt && <section role="status" className="rounded-panel border border-success/30 bg-success/5 p-4 text-sm">
        <p className="font-medium text-text-strong">{t(receipt.applied ? "collaboration.pushApplied" : "collaboration.pushRequested")}</p>
        {receipt.pull_request && <Link href={`/pull-requests/${receipt.pull_request.id}`} className="mt-2 inline-flex items-center gap-2 text-primary underline"><GitPullRequest className="h-4 w-4" aria-hidden="true" />{t("collaboration.openPullRequest")}</Link>}
      </section>}

      <div className="grid min-w-0 gap-5 lg:grid-cols-[minmax(0,1fr)_20rem]">
        <div className="min-w-0 space-y-5">
          <section className={panel}>
            <div className="flex flex-wrap items-center justify-between gap-3"><h2 className="font-semibold text-text-strong">{t("collaboration.workingChanges", { count: dirty.length })}</h2>{editable && <Link href={`/new?workspace=${id}`} className={button}><Plus className="h-4 w-4" aria-hidden="true" />{t("collaboration.createRecord")}</Link>}</div>
            <p className="mt-1 text-sm text-text-muted">{t("collaboration.workingChangesHint")}</p>
            {!workspace.objects.length ? <p className="mt-4 text-sm text-text-muted">{t("collaboration.emptyWorkspace")}</p> : <ul className="mt-4 divide-y divide-line">
              {workspace.objects.map(object => {
                const href = editLink(object, id, workspace.objects);
                return <li key={`${object.target}:${object.id}`} className="flex min-w-0 flex-col gap-3 py-3 sm:flex-row sm:items-center sm:justify-between">
                  <div className="min-w-0"><p className="break-words text-sm font-medium text-text-strong">{objectTitle(object.document ?? object.committed ?? object.base, object.id)}</p>
                    <p className="mt-1 flex flex-wrap gap-x-2 gap-y-1 text-xs text-text-muted"><span>{t(`collaboration.${object.target}`)}</span><span>{t(object.dirty ? "collaboration.uncommitted" : "collaboration.committed")}</span>{!object.document && <span className="text-danger">{t("collaboration.removed")}</span>}</p></div>
                  <div className="flex shrink-0 flex-wrap gap-2">
                    {object.dirty && <button type="button" className={button} onClick={() => openDiff({ target: object.target, id: object.id, base_version: object.base_version, base: object.base, before: object.committed, after: object.document }, t("collaboration.committedVersion"), t("collaboration.workingCopy"))}>{t("collaboration.viewDiff")}</button>}
                    {href && editable && <Link href={href} className={button}>{t("collaboration.edit")}</Link>}
                  </div>
                </li>;
              })}
            </ul>}
            {editable && <form className="mt-4 space-y-3 border-t border-line pt-4" onSubmit={event => { event.preventDefault(); commit(); }}>
              <label className="block space-y-1.5 text-sm font-medium text-text-body"><span>{t("collaboration.commitMessage")}</span><textarea className={input} rows={3} value={message} onChange={event => setMessage(event.target.value)} placeholder={t("collaboration.commitMessagePlaceholder")} maxLength={2000} /></label>
              <button type="submit" className={primary} disabled={!!busy || !dirty.length || !message.trim()}><GitCommitHorizontal className="h-4 w-4" aria-hidden="true" />{t(busy === "commit" ? "collaboration.committing" : "collaboration.commitChanges", { count: dirty.length })}</button>
            </form>}
          </section>

          <section className={panel}>
            <h2 className="flex items-center gap-2 font-semibold text-text-strong"><GitCommitHorizontal className="h-4 w-4 text-text-muted" aria-hidden="true" />{t("collaboration.commitHistory")}</h2>
            {!commits.length ? <p className="mt-3 text-sm text-text-muted">{t("collaboration.noCommits")}</p> : <ol className="mt-4 divide-y divide-line">{commits.map(item => <li key={item.id} className="py-3">
              <details><summary className="cursor-pointer text-sm font-medium text-text-strong"><span className="break-words">{item.message}</span><span className="ml-2 font-mono text-xs font-normal text-text-muted">{item.id.slice(0, 8)}</span></summary>
                <dl className="mt-3 space-y-2 text-xs text-text-muted"><div className="flex flex-wrap items-baseline gap-x-2 gap-y-1"><dt>{t("collaboration.authors")}</dt><dd className="flex min-w-0 flex-wrap gap-x-3 gap-y-1">{item.authors.map(author => <Link key={author.id} href={`/users/${author.id}`} className="break-all text-primary hover:underline">{author.name || author.id}</Link>)}</dd></div><div className="flex flex-wrap items-baseline gap-x-2 gap-y-1"><dt>{t("collaboration.committer")}</dt><dd><Link href={`/users/${item.author.id}`} className="break-all text-primary hover:underline">{item.author.name || item.author.id}</Link></dd></div></dl>
                <p className="mt-2 flex flex-wrap gap-x-2 gap-y-1 text-xs text-text-muted"><span>{date(item.created_at)}</span>{item.merged_at && <span>{t("collaboration.mergedOn", { date: date(item.merged_at) })}</span>}</p>
                <div className="mt-2 flex flex-wrap gap-x-3 gap-y-1"><Link href={`/commits/${item.id}`} className="max-w-full break-all font-mono text-xs text-primary underline">{item.id}</Link>
                {item.pull_request_id && <Link href={`/pull-requests/${item.pull_request_id}`} className="text-xs text-primary underline">{t("collaboration.openPullRequest")}</Link>}</div>
                <ul className="mt-3 space-y-2">{item.changes.map(change => <li key={`${change.target}:${change.id}`} className="flex min-w-0 flex-wrap items-center justify-between gap-2 rounded-md border border-line p-3"><span className="min-w-0 break-words text-sm text-text-body">{objectTitle(change.after ?? change.before, change.id)}</span><button type="button" className={button} onClick={() => openDiff(change, t("collaboration.parentCommit"), item.message, item.authors.map(author => author.name || author.id).join(", "))}>{t("collaboration.viewDiff")}</button></li>)}</ul>
              </details>
            </li>)}</ol>}
          </section>
        </div>

        <aside className="min-w-0 space-y-5">
          <section className={panel}>
            <h2 className="flex items-center gap-2 font-semibold text-text-strong"><Upload className="h-4 w-4 text-text-muted" aria-hidden="true" />{t("collaboration.push")}</h2>
            <p className="mt-2 text-sm text-text-muted">{t(directPush ? "collaboration.directPushHint" : "collaboration.pullRequestPushHint")}</p>
            {editable && <div className="mt-4 space-y-3">
              {!directPush && <label className="block space-y-1.5 text-sm text-text-body"><span>{t("collaboration.pullRequestTitle")}</span><input className={input} value={pushTitle} onChange={event => setPushTitle(event.target.value)} placeholder={workspace.name} maxLength={200} /></label>}
              <button type="button" className={`${primary} w-full`} disabled={!!busy || !workspace.head_commit_id || dirty.length > 0 || !hasUnmerged} onClick={push}><Upload className="h-4 w-4" aria-hidden="true" />{t(busy === "push" ? "collaboration.pushing" : "collaboration.push")}</button>
              {dirty.length > 0 && <p className="text-xs text-text-muted">{t("collaboration.commitBeforePush")}</p>}
              <button type="button" className={`${button} w-full`} disabled={!!busy || !workspace.head_commit_id || dirty.length > 0} onClick={() => rebase()}><RefreshCw className="h-4 w-4" aria-hidden="true" />{t("collaboration.syncMain")}</button>
            </div>}
            <dl className="mt-4 space-y-2 border-t border-line pt-4 text-xs text-text-muted"><div className="flex flex-wrap justify-between gap-2"><dt>{t("collaboration.workspaceVersion")}</dt><dd>{workspace.version}</dd></div><div className="flex flex-wrap justify-between gap-2"><dt>{t("collaboration.headCommit")}</dt><dd className="font-mono">{workspace.head_commit_id?.slice(0, 8) || t("collaboration.noCommits")}</dd></div></dl>
          </section>

          <section className={panel}>
            <h2 className="flex items-center gap-2 font-semibold text-text-strong"><Users className="h-4 w-4 text-text-muted" aria-hidden="true" />{t("collaboration.collaborators")}</h2>
            <ul className="mt-3 space-y-3 text-sm"><li className="min-w-0"><Link className="break-all text-primary hover:underline" href={`/users/${workspace.owner_id}`}>{workspace.owner_id}</Link><span className="mt-1 block text-xs text-text-muted">{t("collaboration.owner")}</span></li>
              {workspace.members.filter(member => member !== workspace.owner_id).map(member => <li key={member} className="flex min-w-0 items-start justify-between gap-2"><Link className="min-w-0 break-all text-primary hover:underline" href={`/users/${member}`}>{member}</Link>{isOwner && <button type="button" disabled={!!busy || workspace.state !== "active"} className="shrink-0 text-xs text-danger hover:underline disabled:opacity-50" onClick={() => changeMembers(workspace.members.filter(item => item !== member))}>{t("collaboration.removeMember")}</button>}</li>)}
            </ul>
            {isOwner && <form className="mt-4 space-y-2 border-t border-line pt-4" onSubmit={event => { event.preventDefault(); const memberId = inviteId.trim().toLowerCase(); if (workspace.state === "active" && uuid.test(memberId) && memberId !== workspace.owner_id && !workspace.members.includes(memberId)) changeMembers([...workspace.members, memberId]); }}><label className="block space-y-1.5 text-sm text-text-body"><span>{t("collaboration.inviteByUserId")}</span><input value={inviteId} onChange={event => setInviteId(event.target.value)} className={input} placeholder={t("collaboration.userIdPlaceholder")} autoComplete="off" disabled={workspace.state !== "active"} /></label><button type="submit" className={button} disabled={!!busy || workspace.state !== "active" || !uuid.test(inviteId.trim()) || inviteId.trim().toLowerCase() === workspace.owner_id || workspace.members.includes(inviteId.trim().toLowerCase())}>{t("collaboration.addMember")}</button></form>}
          </section>

          {isOwner && <section className={panel}><h2 className="font-semibold text-text-strong">{t("collaboration.workspaceSettings")}</h2><form className="mt-3 space-y-3" onSubmit={event => { event.preventDefault(); void run("settings", async signal => { const next = await updateWorkspace(id, { expected_version: workspace.version, name: name.trim() }, signal); if (!signal.aborted && session.current.epoch === epoch) setWorkspace(next); }); }}><label className="block space-y-1.5 text-sm text-text-body"><span>{t("collaboration.workspaceName")}</span><input className={input} value={name} onChange={event => setName(event.target.value)} maxLength={200} /></label><button type="submit" className={button} disabled={!!busy || !name.trim() || name.trim() === workspace.name}>{t("collaboration.saveSettings")}</button></form><button type="button" className={`${button} mt-3`} disabled={!!busy} onClick={() => void run("settings", async signal => { const next = await updateWorkspace(id, { expected_version: workspace.version, state: workspace.state === "active" ? "archived" : "active" }, signal); if (!signal.aborted && session.current.epoch === epoch) setWorkspace(next); })}>{t(workspace.state === "active" ? "collaboration.archiveWorkspace" : "collaboration.reopenWorkspace")}</button></section>}
        </aside>
      </div>
    </>}

    {diff && <RevisionDiffInspector base={diff.base} current={diff.current} onClose={() => setDiff(null)} assetContext={{ kind: "workspace", id }} />}
    {conflict && <Modal open onClose={() => { if (!busy) setConflict(null); }} title={t("collaboration.resolveConflicts")} icon={<GitBranch className="h-5 w-5 text-primary" aria-hidden="true" />} maxWidth="max-w-[1440px] h-[90dvh]" footer={<div className="flex flex-wrap items-center justify-between gap-3"><p className="text-xs text-text-muted">{t("collaboration.conflictProgress", { count: Object.keys(conflict.choices).length, total: conflict.conflicts.length })}</p><button type="button" className={primary} disabled={!!busy || Object.keys(conflict.choices).length !== conflict.conflicts.length} onClick={resolve}>{t("collaboration.resolveAndSync")}</button></div>}>
      {error && <p role="alert" className="mb-4 rounded-md border border-danger/30 bg-danger/5 p-3 text-sm text-danger">{error}</p>}
      <p className="mb-5 text-sm text-text-muted">{t("collaboration.conflictHint")}</p>
      <div className="space-y-5">{conflict.conflicts.map((item, index) => {
        const choice = conflict.choices[index];
        return <section key={`${item.target}:${item.id}:${item.pointer}`} className="overflow-hidden rounded-panel border border-line">
          <header className="border-b border-line bg-surfaceSubtle p-3"><h3 className="break-words text-sm font-semibold text-text-strong">{t(`collaboration.${item.target}`)} · {item.id}</h3><p className="mt-1 break-all font-mono text-xs text-text-muted">{item.pointer || t("collaboration.wholeObject")}</p></header>
          <div className="grid min-w-0 divide-y divide-line lg:grid-cols-3 lg:divide-x lg:divide-y-0">{([['base', item.base, item.base_present], ['current', item.current, item.current_present], ['proposed', item.proposed, item.proposed_present]] as const).map(([key, value, present]) => <section key={key} className="min-w-0 p-3"><h4 className="mb-3 text-xs font-semibold text-text-muted">{t(`collaboration.conflictColumn.${key}`)}</h4><div className="max-h-80 overflow-y-auto text-sm text-text-body"><ConflictValue value={value} present={present} /></div></section>)}</div>
          <fieldset className="space-y-3 border-t border-line p-3" disabled={!!busy}><legend className="sr-only">{t("collaboration.chooseResolution")}</legend><div className="flex flex-wrap gap-2">{(["proposed", "current", "custom", "remove"] as const).map(value => <button type="button" key={value} className={`${button} aria-pressed:border-primary aria-pressed:bg-primary/10 aria-pressed:text-primary`} aria-pressed={choice?.choice === value} onClick={() => setConflict(current => current && ({ ...current, choices: { ...current.choices, [index]: { choice: value, custom: current.choices[index]?.custom ?? JSON.stringify(item.proposed ?? null, null, 2) } } }))}>{t(`collaboration.resolution.${value}`)}</button>)}</div>
            {choice?.choice === "custom" && <label className="block space-y-1.5 text-xs text-text-muted"><span>{t("collaboration.customJson")}</span><textarea className={`${input} font-mono`} rows={6} spellCheck={false} value={choice.custom} onChange={event => { const value = event.target.value; setConflict(current => current && ({ ...current, choices: { ...current.choices, [index]: { choice: "custom", custom: value } } })); }} /></label>}
          </fieldset>
        </section>;
      })}</div>
    </Modal>}
  </div>;
}
