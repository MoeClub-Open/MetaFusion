"use client";

import { useCallback, useEffect, useRef, useState } from "react";
import Link from "next/link";
import { useRouter } from "next/navigation";
import { GitBranch, GitPullRequest, Plus, RefreshCw } from "lucide-react";
import { useAuth } from "@/lib/authContext";
import { useI18n } from "@/i18n/I18nProvider";
import { createWorkspace, listWorkspaces, type Workspace } from "@/lib/api/collaboration";
import { localizeCatalogError } from "@/lib/catalogErrors";

export function WorkspaceList() {
  const { user, loading } = useAuth();
  const { t, locale } = useI18n();
  const router = useRouter();
  const [items, setItems] = useState<Workspace[]>([]);
  const [name, setName] = useState("");
  const [error, setError] = useState("");
  const [busy, setBusy] = useState(false);
  const [loaded, setLoaded] = useState(false);
  const [page, setPage] = useState(0);
  const [total, setTotal] = useState(0);
  const [loadedUser, setLoadedUser] = useState("");
  const request = useRef(0);
  const creation=useRef<{key:string;id:string}|null>(null);
  const actor = user ? JSON.stringify([user.id, [...(user.permissions || [])].sort()]) : "anonymous";
  const controller = useRef<AbortController | null>(null);
  const activeActor = useRef(actor);
  activeActor.current = actor;
  useEffect(() => { const scope = new AbortController(); controller.current = scope; return () => scope.abort(); }, [actor]);
  const load = useCallback(async () => {
    const token = ++request.current;
    const signal = controller.current?.signal;
    if (!user) { setItems([]); setLoaded(false); return; }
    setBusy(true);
    setError("");
    try { const result = await listWorkspaces({ limit: 20, offset: page * 20 }, signal); if (token !== request.current || activeActor.current !== actor || signal?.aborted) return; setItems(result.items); setTotal(result.total); setLoaded(true); setLoadedUser(actor); }
    catch (e) { if (token === request.current && activeActor.current === actor && !signal?.aborted) setError(localizeCatalogError((e as Error).message, t)); }
    finally { if (token === request.current && activeActor.current === actor && !signal?.aborted) setBusy(false); }
  }, [user, t, page, actor]);
  useEffect(() => { setName(""); setPage(0); setLoaded(false); }, [actor]);
  useEffect(() => { void load(); }, [load]);
  const create = async () => {
    const signal = controller.current?.signal;
    setBusy(true);
    setError("");
    try {
      const key=JSON.stringify([actor,name.trim()]);if(creation.current?.key!==key)creation.current={key,id:crypto.randomUUID()};
      const workspace = await createWorkspace({id:creation.current.id,...(name.trim()?{name:name.trim()}:{})}, signal);
      if (activeActor.current !== actor || signal?.aborted) return;
      router.push(`/workspaces/${workspace.id}`);
    } catch (e) { if (activeActor.current === actor && !signal?.aborted) { setError(localizeCatalogError((e as Error).message, t)); setBusy(false); } }
  };
  if (loading) return <p>{t("common.loading")}</p>;
  if (!user) return <p><Link href="/login" className="text-primary underline">{t("collaboration.signIn")}</Link></p>;
  return <section className="catalog-v2 cv-form space-y-5">
    <header className="flex flex-wrap items-start justify-between gap-3">
      <div className="space-y-2"><h1 className="flex items-center gap-2 text-2xl font-semibold"><GitBranch aria-hidden="true" className="h-5 w-5"/>{t("collaboration.workspaces")}</h1>
        <p className="max-w-3xl text-sm text-text-muted">{t("collaboration.workspaceIntro")}</p></div>
      <div className="flex flex-wrap gap-4 text-sm"><Link href="/history" className="text-primary underline">{t("versioning.catalogHistory")}</Link><Link href="/pull-requests" className="inline-flex items-center gap-2 text-primary underline"><GitPullRequest className="h-4 w-4" aria-hidden="true"/>{t("collaboration.pullRequests")}</Link></div>
    </header>
    <form className="flex flex-wrap items-end gap-3 rounded-panel border border-line bg-surface p-4" onSubmit={e => { e.preventDefault(); void create(); }}>
      <label className="min-w-48 flex-1 space-y-1 text-sm"><span>{t("collaboration.workspaceName")}</span><input maxLength={200} value={name} onChange={e => setName(e.target.value)} placeholder={t("collaboration.optionalName")} disabled={busy}/></label>
      <button className="cv-primary inline-flex items-center gap-2" disabled={busy}><Plus aria-hidden="true" className="h-4 w-4"/>{t("collaboration.createWorkspace")}</button>
      <button type="button" className="inline-flex items-center gap-2" disabled={busy} onClick={() => void load()}><RefreshCw aria-hidden="true" className="h-4 w-4"/>{t("common.refresh")}</button>
    </form>
    {error && <p role="alert" className="cv-error">{error}</p>}
    {error ? null : (busy && !loaded) || loadedUser !== actor ? <p>{t("common.loading")}</p> : loaded && !items.length ? <p className="rounded-panel border border-dashed border-line p-6 text-text-muted">{t("collaboration.noWorkspaces")}</p> :
      <ul className="space-y-3">{items.map(item => <li key={item.id} className="rounded-panel border border-line bg-surface p-4">
        <div className="flex flex-wrap items-center justify-between gap-2"><Link className="font-medium text-primary hover:underline" href={`/workspaces/${item.id}`}>{item.name || t("collaboration.unnamedWorkspace")}</Link>
          <span className="rounded-control border border-line px-2 py-1 text-xs text-text-muted">{t(`collaboration.state.${item.state}`)}</span></div>
        <p className="mt-2 text-xs text-text-muted">{t("collaboration.workspaceSummary", { dirty: item.dirty_count, members: item.members.length })} · {new Date(item.updated_at).toLocaleString(locale)}</p>
      </li>)}</ul>}
    {loaded && loadedUser === actor && <nav className="flex items-center justify-between gap-3 text-sm" aria-label={t("collaboration.pagination")}>
      <button disabled={busy || page === 0} onClick={() => setPage(p => p - 1)}>{t("catalog.prevPage")}</button>
      <span>{t("collaboration.pageCount", { page: page + 1, pages: Math.max(1, Math.ceil(total / 20)) })}</span>
      <button disabled={busy || (page + 1) * 20 >= total} onClick={() => setPage(p => p + 1)}>{t("catalog.nextPage")}</button>
    </nav>}
  </section>;
}
