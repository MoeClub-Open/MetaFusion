"use client";

import { useEffect, useState } from "react";
import Link from "next/link";
import { GitBranch, GitCommitHorizontal } from "lucide-react";
import { useAuth } from "@/lib/authContext";
import { useI18n } from "@/i18n/I18nProvider";
import { getCatalogHead, getCatalogHistory, type CollaborationPage, type WorkspaceCommit } from "@/lib/api/collaboration";
import { localizeCatalogError } from "@/lib/catalogErrors";

export function CatalogHistory(){
  const {user,loading}=useAuth();const {t,locale}=useI18n();const [page,setPage]=useState(0);const [data,setData]=useState<CollaborationPage<WorkspaceCommit>|null>(null);
  const [head,setHead]=useState<{commit_id:string;sequence:number}|null>(null);const [busy,setBusy]=useState(false);const [error,setError]=useState("");
  const actor=JSON.stringify([user?.id,...(user?.permissions??[])]);const [loadedActor,setLoadedActor]=useState("");
  useEffect(()=>{if(loading)return;const controller=new AbortController();setBusy(true);setError("");setData(null);setHead(null);Promise.all([getCatalogHistory(page*20,controller.signal),getCatalogHead(controller.signal)]).then(([history,current])=>{if(!controller.signal.aborted){setData(history);setHead(current);setLoadedActor(actor)}}).catch(cause=>{if(!controller.signal.aborted)setError(localizeCatalogError((cause as Error).message,t))}).finally(()=>{if(!controller.signal.aborted)setBusy(false)});return()=>controller.abort()},[loading,actor,page,t]);
  return <section className="space-y-5"><header className="flex flex-wrap items-start justify-between gap-3"><div className="space-y-2"><h1 className="flex items-center gap-2 text-2xl font-semibold"><GitCommitHorizontal className="h-5 w-5 text-primary"/>{t("versioning.catalogHistory")}</h1><p className="max-w-3xl text-sm text-text-muted">{t("versioning.catalogHistoryHint")}</p></div><Link className="flex items-center gap-2 text-sm text-primary" href="/workspaces"><GitBranch className="h-4 w-4"/>{t("collaboration.myWorkspaces")}</Link></header>
    {error&&<p role="alert" className="text-sm text-danger">{error}</p>}{(busy||loading)&&<p role="status" className="text-sm text-text-muted">{t("common.loading")}</p>}
    {data&&head&&loadedActor===actor&&<><p className="flex flex-wrap gap-2 rounded-panel border border-line bg-surface px-4 py-3 text-xs text-text-muted"><span>{t("versioning.officialHead")}</span><span className="font-mono">{head.commit_id}</span></p><ol className="space-y-3">{data.items.map(commit=><li key={commit.id} className="rounded-panel border border-line bg-surface p-4"><div className="flex flex-wrap items-start justify-between gap-3"><Link href={`/commits/${commit.id}`} className="min-w-0 break-words font-medium text-primary">{commit.operation==="baseline"?t("versioning.baseline"):commit.message}</Link><span className="font-mono text-xs text-text-muted">{commit.id.slice(0,8)}</span></div><p className="mt-2 flex flex-wrap gap-2 text-xs text-text-muted"><span>{new Date(commit.merged_at??commit.created_at).toLocaleString(locale)}</span><span>{commit.author.id==="system"?t("versioning.system"):commit.author.name}</span><span>{t("collaboration.changeCount",{count:commit.changes.length})}</span></p><div className="mt-2 flex flex-wrap gap-3 text-xs">{[commit.parent_id,commit.merge_parent_id].filter((id):id is string=>!!id).map(id=><Link key={id} href={`/commits/${id}`} className="font-mono text-primary">{id.slice(0,8)}</Link>)}</div></li>)}</ol><nav className="flex items-center justify-between gap-3 text-sm" aria-label={t("collaboration.pagination")}><button disabled={busy||page===0} onClick={()=>setPage(value=>value-1)}>{t("catalog.prevPage")}</button><span>{t("collaboration.pageCount",{page:page+1,pages:Math.max(1,Math.ceil(data.total/20))})}</span><button disabled={busy||(page+1)*20>=data.total} onClick={()=>setPage(value=>value+1)}>{t("catalog.nextPage")}</button></nav></>}
  </section>
}
