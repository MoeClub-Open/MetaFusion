"use client";

import { useEffect, useRef, useState } from "react";
import Link from "next/link";
import { useRouter } from "next/navigation";
import { GitBranch, History, Tag, Archive, GitMerge } from "lucide-react";
import { useI18n } from "@/i18n/I18nProvider";
import { ApiError } from "@/lib/api/client";
import { localizeCatalogError } from "@/lib/catalogErrors";
import { Modal } from "@/components/ui/Modal";
import { ConflictValue } from "./ConflictValue";
import { applyWorkspaceVersion, applyWorkspaceStash, editWorkspaceTag, forkWorkspace, getWorkspaceEvents, getWorkspaceStashes, getWorkspaceTags, rebaseWorkspace, getCatalogHead,
  type Workspace, type WorkspaceCommit, type VersionOperation, type VersionAction, type StashAction, type WorkspaceConflict, type ConflictResolution,
  type VersionEvent, type VersionTag, type VersionStash, type CollaborationPage, type RebaseAction } from "@/lib/api/collaboration";

const button = "inline-flex items-center justify-center gap-2 rounded-md border border-line px-3 py-2 text-sm text-text-body hover:bg-surfaceSubtle disabled:opacity-50";
const input = "w-full min-w-0 rounded-md border border-line bg-background px-3 py-2 text-sm text-text-body focus-visible:outline focus-visible:outline-2 focus-visible:outline-primary";
type ActionOperation = VersionOperation | "rebase";
const operations: ActionOperation[] = ["merge", "rebase", "cherry_pick", "revert", "restore", "amend", "squash", "reset", "discard"];
type Choice = { kind: "current" | "proposed" | "custom" | "remove"; text: string };
type ActionRequest = VersionAction | StashAction | RebaseAction;
type Conflict = { items: WorkspaceConflict[]; request: ActionRequest; kind: "version" | "stash" | "rebase"; choices: Record<number, Choice> };
type Pager<T> = CollaborationPage<T> | null;

export function WorkspaceVersionTools({ workspace, commits, owner, disabled, onChanged }: {
  workspace: Workspace; commits: WorkspaceCommit[]; owner: boolean; disabled: boolean; onChanged: () => Promise<void>;
}) {
  const { t, locale } = useI18n();
  const router = useRouter();
  const [open, setOpen] = useState(false);
  const [tab, setTab] = useState<"actions" | "tags" | "stashes" | "events">("actions");
  const [operation, setOperation] = useState<ActionOperation>("merge");
  const [onto, setOnto] = useState("");
  const [customOrder, setCustomOrder] = useState(false);
  const [source, setSource] = useState("");
  const [message, setMessage] = useState("");
  const [mode, setMode] = useState<"soft" | "hard">("soft");
  const [forkName, setForkName] = useState("");
  const [tagName, setTagName] = useState("");
  const [editingTag, setEditingTag] = useState<VersionTag | null>(null);
  const [tags, setTags] = useState<Pager<VersionTag>>(null);
  const [stashes, setStashes] = useState<Pager<VersionStash>>(null);
  const [events, setEvents] = useState<Pager<VersionEvent>>(null);
  const [offset, setOffset] = useState(0);
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState("");
  const [conflict, setConflict] = useState<Conflict | null>(null);
  const attempt = useRef<{ key: string; id: string } | null>(null);
  const lock = useRef(false);
  const scope = useRef<{ key: string; signal: AbortController }>({ key: workspace.id, signal: new AbortController() });
  if (scope.current.key !== workspace.id) { scope.current.signal.abort(); scope.current = { key: workspace.id, signal: new AbortController() }; }
  useEffect(() => { const current = new AbortController(); scope.current = { key: workspace.id, signal: current }; return () => current.abort(); }, [workspace.id]);
  const signal = () => scope.current.signal.signal;
  const head = workspace.head_commit_id ?? "";
  const editable = !disabled && workspace.state === "active";
  const requestID = (payload: unknown) => { const key = JSON.stringify(payload); if (attempt.current?.key !== key) attempt.current = { key, id: crypto.randomUUID() }; return attempt.current.id; };
  const report = (cause: unknown) => setError(localizeCatalogError(cause instanceof Error ? cause.message : String(cause), t));
  const refreshReferences = async (currentSignal: AbortSignal, page = offset) => {
    if (tab === "tags") { const value = await getWorkspaceTags(workspace.id, page, currentSignal); if (!currentSignal.aborted) setTags(value); }
    if (tab === "stashes") { const value = await getWorkspaceStashes(workspace.id, page, currentSignal); if (!currentSignal.aborted) setStashes(value); }
    if (tab === "events") { const value = await getWorkspaceEvents(workspace.id, page, currentSignal); if (!currentSignal.aborted) setEvents(value); }
  };
  useEffect(() => {
    if (!open || tab === "actions") return;
    const controller = new AbortController();
    void refreshReferences(controller.signal).catch(cause => { if (!controller.signal.aborted) report(cause); });
    return () => controller.abort();
    // Each tab's page is read again after a branch change.
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [open, tab, offset, workspace.id, workspace.version]);

  const run = async (fn: (currentSignal: AbortSignal) => Promise<void>) => {
    if (lock.current) return;
    const currentSignal = signal(); if (currentSignal.aborted) return;
    lock.current = true; setBusy(true); setError("");
    try { await fn(currentSignal); }
    catch (cause) { if (!currentSignal.aborted) report(cause); }
    finally { if (!currentSignal.aborted) setBusy(false); lock.current = false; }
  };
  const execute = async (request: ActionRequest, kind: Conflict["kind"], currentSignal: AbortSignal) => {
    try {
      if (kind === "rebase") await rebaseWorkspace(workspace.id, request as RebaseAction, currentSignal);
      else if (kind === "stash") await applyWorkspaceStash(workspace.id, request as StashAction, currentSignal);
      else await applyWorkspaceVersion(workspace.id, request as VersionAction, currentSignal);
      if (currentSignal.aborted) return;
      setConflict(null); attempt.current = null; setMessage(""); await onChanged(); await refreshReferences(currentSignal);
    } catch (cause) {
      const details = cause instanceof ApiError ? cause.details as { conflicts?: WorkspaceConflict[] } | undefined : undefined;
      if (details?.conflicts?.length) { setConflict({ items: details.conflicts, request, kind, choices: {} }); return; }
      throw cause;
    }
  };
  const commitIds = () => source.split(/[\s,]+/).filter(Boolean).map(value => value.replace(/^.*\/commits\//, "").replace(/[?#].*$/, ""));
  const submit = () => void run(async currentSignal => {
    if (operation === "rebase") {
      const payload = { expected_version: workspace.version, expected_head: head, onto_commit_id: onto.trim() || (await getCatalogHead(currentSignal)).commit_id, ...(customOrder ? { order: commitIds() } : {}), message: message.trim() };
      await execute({ ...payload, id: requestID(payload) }, "rebase", currentSignal); return;
    }
    const payload = { expected_version: workspace.version, expected_head: head, operation, commit_ids: commitIds(), message: message.trim() || t(`versioning.operation.${operation}`), ...(operation === "reset" ? { mode } : {}) };
    await execute({ ...payload, id: requestID(payload) }, "version", currentSignal);
  });
  const stashAction = (kind: StashAction["operation"], stashId?: string) => void run(async currentSignal => {
    const payload = { expected_version: workspace.version, expected_head: head, operation: kind, ...(stashId ? { stash_id: stashId } : { name: message.trim() || t("versioning.savedChanges") }) };
    await execute({ ...payload, id: requestID(payload) }, "stash", currentSignal);
  });
  const saveTag = (tag?: VersionTag, remove = false) => void run(async currentSignal => {
    const payload = { expected_version: workspace.version, expected_head: head, ...(tag ? { tag_id: tag.id } : {}), ...(remove ? { delete: true } : { name: tagName.trim(), commit_id: commitIds()[0] || head, message }) };
    await editWorkspaceTag(workspace.id, { ...payload, id: requestID(payload) }, currentSignal);
    if (currentSignal.aborted) return;
    attempt.current = null; setEditingTag(null); setTagName(""); await onChanged(); await refreshReferences(currentSignal);
  });
  const resolve = () => void run(async currentSignal => {
    if (!conflict) return;
    const resolutions: ConflictResolution[] = conflict.items.map((item, index) => {
      const choice = conflict.choices[index]; if (!choice) throw new Error("invalid_resolution");
      const remove = choice.kind === "remove" || choice.kind === "current" && !item.current_present || choice.kind === "proposed" && !item.proposed_present;
      return { ...(item.source_commit_id ? { commit_id: item.source_commit_id } : {}), target: item.target, id: item.id, pointer: item.pointer, current_version: item.current_version,
        ...(remove ? { remove: true } : { value: choice.kind === "custom" ? (typeof item.proposed === "string" ? choice.text : JSON.parse(choice.text)) : choice.kind === "current" ? item.current : item.proposed }) };
    });
    await execute({ ...conflict.request, resolutions: [...(conflict.request.resolutions ?? []), ...resolutions] }, conflict.kind, currentSignal);
  });
  const activePage = tab === "tags" ? tags : tab === "stashes" ? stashes : events;
  const pending = commits.slice(-Math.min(workspace.pending_commits,commits.length));
  return <>
    <button type="button" className={`${button} w-full`} onClick={() => setOpen(true)}><GitBranch className="h-4 w-4" />{t("versioning.title")}</button>
    {open && <Modal open onClose={() => { if (!busy) setOpen(false); }} title={t("versioning.title")} maxWidth="max-w-4xl">
      <div className="space-y-5">
        <nav className="flex flex-wrap gap-1 border-b border-line pb-3" aria-label={t("versioning.title")}>{([['actions', GitMerge], ['tags', Tag], ['stashes', Archive], ['events', History]] as const).map(([value, Icon]) => <button type="button" className={`${button} aria-pressed:border-primary aria-pressed:text-primary`} aria-pressed={tab === value} key={value} disabled={busy} onClick={() => { setTab(value); setOffset(0); }}><Icon className="h-4 w-4" />{t(`versioning.tab.${value}`)}</button>)}</nav>
        {error && <p role="alert" className="rounded-md border border-danger/30 p-3 text-sm text-danger">{error}</p>}
        {tab === "actions" && <div className="space-y-5">
          <form className="space-y-3 rounded-panel border border-line p-4" onSubmit={event => { event.preventDefault(); submit(); }}>
            <label className="block space-y-1 text-sm"><span>{t("versioning.action")}</span><select className={input} value={operation} disabled={busy} onChange={event => { setOperation(event.target.value as ActionOperation); setSource(""); }}>{operations.map(value => <option key={value} value={value}>{t(`versioning.operation.${value}`)}</option>)}</select></label>
            <p className="text-sm text-text-muted">{t(`versioning.hint.${operation}`)}</p>
            {operation === "rebase" && <><label className="block space-y-1 text-sm"><span>{t("versioning.onto")}</span><input className={input} value={onto} onChange={event => setOnto(event.target.value)} placeholder={t("versioning.currentMain")} /></label><label className="flex items-center gap-2 text-sm"><input type="checkbox" checked={customOrder} onChange={event => setCustomOrder(event.target.checked)} />{t("versioning.customOrder")}</label>{customOrder && <p className="text-sm text-text-muted">{t("versioning.orderHint")}</p>}</>}
            {(!["amend", "discard", "rebase"].includes(operation) || operation === "rebase" && customOrder) && <label className="block space-y-1 text-sm"><span>{t("versioning.sourceCommits")}</span><textarea className={`${input} font-mono`} rows={3} value={source} disabled={busy} onChange={event => setSource(event.target.value)} placeholder={t("versioning.sourceHint")} /></label>}
            {operation === "squash" && <button className={button} type="button" disabled={busy || pending.length < 2} onClick={() => setSource(pending.map(item => item.id).join("\n"))}>{t("versioning.selectPending")}</button>}
            {operation === "reset" && <label className="block space-y-1 text-sm"><span>{t("versioning.resetMode")}</span><select className={input} value={mode} onChange={event => setMode(event.target.value as "soft" | "hard")}><option value="soft">{t("versioning.softReset")}</option><option value="hard">{t("versioning.hardReset")}</option></select></label>}
            {operation !== "discard" && <label className="block space-y-1 text-sm"><span>{t("collaboration.commitMessage")}</span><textarea className={input} rows={2} value={message} disabled={busy} maxLength={2000} onChange={event => setMessage(event.target.value)} /></label>}
            <button className={button} type="submit" disabled={busy || !editable || ["amend", "squash", "reset"].includes(operation) && !owner || !["amend", "discard", "rebase"].includes(operation) && !source.trim()}>{t("versioning.applyCopy")}</button>
          </form>
          <form className="space-y-3 border-t border-line pt-4" onSubmit={event => { event.preventDefault(); void run(async currentSignal => { const payload = { name: forkName.trim() || workspace.name, commit_id: commitIds()[0] || head, entity_ids:workspace.objects.filter(o=>o.target==="entity").map(o=>o.id),relation_ids:workspace.objects.filter(o=>o.target==="relation").map(o=>o.id) }; const result = await forkWorkspace({ ...payload, id: requestID(payload) }, currentSignal); if (!currentSignal.aborted) { attempt.current = null; router.push(`/workspaces/${result.id}`); setOpen(false); } }); }}>
            <h3 className="font-medium">{t("versioning.fork")}</h3><p className="text-sm text-text-muted">{t("versioning.forkHint")}</p>
            <label className="block space-y-1 text-sm"><span>{t("collaboration.workspaceName")}</span><input className={input} value={forkName} onChange={event => setForkName(event.target.value)} maxLength={200} placeholder={workspace.name} /></label>
            <button className={button} disabled={busy || !head}>{t("versioning.fork")}</button>
          </form>
        </div>}
        {tab === "tags" && <div className="space-y-4">
          {owner && editable && <form className="space-y-2" onSubmit={event => { event.preventDefault(); saveTag(editingTag ?? undefined); }}><label className="block text-sm">{t("versioning.tagName")}<input className={`${input} mt-1`} value={tagName} onChange={event => setTagName(event.target.value)} maxLength={200} /></label><label className="block text-sm">{t("collaboration.headCommit")}<input className={`${input} mt-1 font-mono`} value={source} onChange={event => setSource(event.target.value)} placeholder={head} /></label><div className="flex gap-2"><button className={button} disabled={busy || !tagName.trim() || !head}>{t(editingTag ? "common.save" : "versioning.createTag")}</button>{editingTag && <button className={button} type="button" onClick={() => { setEditingTag(null); setTagName(""); setSource(""); }}>{t("common.cancel")}</button>}</div></form>}
          <ul className="divide-y divide-line">{tags?.items.map(tag => <li key={tag.id} className="flex flex-wrap items-center justify-between gap-3 py-3"><div><p className="font-medium">{tag.name}</p><Link className="font-mono text-xs text-primary" href={`/commits/${tag.commit_id}`}>{tag.commit_id.slice(0, 8)}</Link></div>{owner && editable && <div className="flex gap-2"><button className={button} disabled={busy} onClick={() => { setEditingTag(tag); setTagName(tag.name); setSource(tag.commit_id); setMessage(tag.message); }}>{t("collaboration.edit")}</button><button className={button} disabled={busy} onClick={() => saveTag(tag, true)}>{t("common.delete")}</button></div>}</li>)}</ul>
        </div>}
        {tab === "stashes" && <div className="space-y-3"><p className="text-sm text-text-muted">{t("versioning.stashHint")}</p><input className={input} value={message} onChange={event => setMessage(event.target.value)} placeholder={t("versioning.stashName")} /><button className={button} disabled={busy || !editable || !workspace.dirty_count} onClick={() => stashAction("save")}>{t("versioning.stashSave")}</button><ul className="divide-y divide-line">{stashes?.items.map(stash => <li key={stash.id} className="flex flex-wrap items-center justify-between gap-3 py-3"><span>{stash.name || t("versioning.savedChanges")}</span><div className="flex flex-wrap gap-2">{(["apply", "pop", "drop"] as const).map(action => <button key={action} className={button} disabled={busy || !editable} onClick={() => stashAction(action, stash.id)}>{t(`versioning.stash.${action}`)}</button>)}</div></li>)}</ul></div>}
        {tab === "events" && <ol className="space-y-3">{events?.items.map(event => <li key={event.id} className="rounded-panel border border-line p-3"><p className="text-sm font-medium">{t(`versioning.event.${event.action}`)}</p><p className="mt-1 text-xs text-text-muted">{new Date(event.created_at).toLocaleString(locale)} · <Link href={`/users/${event.actor_id}`} className="text-primary">{event.actor_id}</Link></p><div className="mt-2 flex flex-wrap gap-2 font-mono text-xs">{event.old_head && <Link className="text-primary" href={`/commits/${event.old_head}`}>{event.old_head.slice(0, 8)}</Link>}<span>→</span>{event.new_head && <Link className="text-primary" href={`/commits/${event.new_head}`}>{event.new_head.slice(0, 8)}</Link>}</div></li>)}</ol>}
        {tab !== "actions" && activePage && <div className="flex items-center justify-between gap-3 border-t border-line pt-3"><button className={button} disabled={busy || offset === 0} onClick={() => setOffset(Math.max(0, offset - 20))}>{t("catalog.prevPage")}</button><span className="text-xs text-text-muted">{Math.min(offset + 1, activePage.total)}–{Math.min(offset + 20, activePage.total)} / {activePage.total}</span><button className={button} disabled={busy || offset + 20 >= activePage.total} onClick={() => setOffset(offset + 20)}>{t("catalog.nextPage")}</button></div>}
      </div>
    </Modal>}
    {conflict && <Modal open onClose={() => { if (!busy) setConflict(null); }} title={t("collaboration.resolveConflicts")} maxWidth="max-w-6xl" footer={<button className={button} disabled={busy || Object.keys(conflict.choices).length !== conflict.items.length} onClick={resolve}>{t("versioning.applyResolution")}</button>}><div className="space-y-4">{error && <p role="alert" className="text-danger">{error}</p>}{conflict.items.map((item, index) => <section key={`${item.target}:${item.id}:${item.pointer}`} className="rounded-panel border border-line p-3"><p className="mb-3 break-all font-mono text-xs">{item.id} · {item.pointer || t("collaboration.wholeObject")}</p><div className="grid gap-4 lg:grid-cols-3">{([['base', item.base, item.base_present], ['current', item.current, item.current_present], ['proposed', item.proposed, item.proposed_present]] as const).map(([key, value, present]) => <div className="min-w-0" key={key}><p className="mb-2 text-xs text-text-muted">{t(`collaboration.conflictColumn.${key}`)}</p><div className="max-h-64 overflow-auto text-sm"><ConflictValue value={value} present={present} /></div></div>)}</div><div className="mt-3 flex flex-wrap gap-2">{(["current", "proposed", "custom", "remove"] as const).map(kind => <button key={kind} className={`${button} aria-pressed:border-primary`} aria-pressed={conflict.choices[index]?.kind === kind} onClick={() => setConflict(value => value && ({ ...value, choices: { ...value.choices, [index]: { kind, text: value.choices[index]?.text ?? (typeof item.proposed === "string" ? item.proposed : JSON.stringify(item.proposed, null, 2)) } } }))}>{t(`collaboration.resolution.${kind}`)}</button>)}</div>{conflict.choices[index]?.kind === "custom" && <label className="mt-3 block text-sm">{t(typeof item.proposed === "string" ? "versioning.mergedText" : "collaboration.customJson")}<textarea className={`${input} mt-1`} rows={8} value={conflict.choices[index].text} onChange={event => { const text = event.target.value; setConflict(value => value && ({ ...value, choices: { ...value.choices, [index]: { kind: "custom", text } } })); }} /></label>}</section>)}</div></Modal>}
  </>;
}
