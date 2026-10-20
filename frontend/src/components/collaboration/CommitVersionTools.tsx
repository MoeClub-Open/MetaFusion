"use client";

import { useEffect, useRef, useState } from "react";
import Link from "next/link";
import { useRouter } from "next/navigation";
import { useI18n } from "@/i18n/I18nProvider";
import { useAuth } from "@/lib/authContext";
import { Modal } from "@/components/ui/Modal";
import { RevisionSnapshotView } from "@/components/catalog/RevisionSnapshotView";
import { RevisionDiffInspector } from "@/components/catalog/RevisionDiffInspector";
import { localizeCatalogError } from "@/lib/catalogErrors";
import { ConflictValue } from "./ConflictValue";
import { applyWorkspaceVersion, compareCommits, createWorkspace, forkWorkspace, getCommitSnapshot, getVersionFieldAuthor,
  type WorkspaceCommit, type VersionSnapshot, type WorkspaceChange, type WorkspaceObject } from "@/lib/api/collaboration";

const button="rounded-md border border-line px-3 py-2 text-sm text-text-body hover:bg-surfaceSubtle disabled:opacity-50";
const input="w-full min-w-0 rounded-md border border-line bg-background px-3 py-2 text-sm";
export function CommitVersionTools({ commit }: {commit:WorkspaceCommit}){
  const {t}=useI18n();const {user}=useAuth();const router=useRouter();
  const [snapshot,setSnapshot]=useState<VersionSnapshot|null>(null);const [showSnapshot,setShowSnapshot]=useState(false);
  const [selected,setSelected]=useState<WorkspaceObject|null>(null);const [base,setBase]=useState(commit.parent_id??"");
  const [comparison,setComparison]=useState<WorkspaceChange[]|null>(null);const [diff,setDiff]=useState<WorkspaceChange|null>(null);
  const [compareTotal,setCompareTotal]=useState(0);const [compareOffset,setCompareOffset]=useState(0);
  const [records,setRecords]=useState("");
  const [pointer,setPointer]=useState("/title");const [author,setAuthor]=useState<WorkspaceCommit|null>(null);
  const [busy,setBusy]=useState(false);const [error,setError]=useState("");const [copy,setCopy]=useState<"fork"|"revert"|"restore"|null>(null);
  const [name,setName]=useState("");const [createdCopy,setCreatedCopy]=useState<string|null>(null);
  const scope=useRef<AbortController|null>(null);const attempt=useRef<{key:string;workspace:string;commit:string}|null>(null);const lock=useRef(false);
  useEffect(()=>{const controller=new AbortController();scope.current=controller;setSnapshot(null);setShowSnapshot(false);setSelected(null);setComparison(null);setBase(commit.parent_id??"");setAuthor(null);setError("");setCreatedCopy(null);return()=>controller.abort()},[commit.id,user?.id]);
  const run=async(fn:(signal:AbortSignal)=>Promise<void>)=>{const signal=scope.current?.signal;if(!signal||signal.aborted||lock.current)return;lock.current=true;setBusy(true);setError("");try{await fn(signal)}catch(cause){if(!signal.aborted)setError(localizeCatalogError((cause as Error).message,t))}finally{if(!signal.aborted)setBusy(false);lock.current=false}};
  const loadSnapshot=(offset=0)=>void run(async signal=>{const value=await getCommitSnapshot(commit.id,offset,signal);if(!signal.aborted){setSnapshot(value);setSelected(null);setShowSnapshot(true)}});
  const submit=()=>void run(async signal=>{
    if(!copy)return;const copyName=name.trim()||commit.message.split("\n")[0]||t("collaboration.unnamedWorkspace");const key=JSON.stringify([copy,copyName,commit.id,records]);
    if(attempt.current?.key!==key)attempt.current={key,workspace:crypto.randomUUID(),commit:crypto.randomUUID()};const ids=attempt.current;
    if(copy==="fork"){const result=await forkWorkspace({id:ids.workspace,commit_id:commit.id,name:copyName,...(records.trim()?{entity_ids:records.split(/[\s,]+/).filter(Boolean)}:{})},signal);if(!signal.aborted)router.push(`/workspaces/${result.id}`)}else{
      const workspace=await createWorkspace({id:ids.workspace,name:copyName},signal);if(!signal.aborted)setCreatedCopy(workspace.id);
      await applyWorkspaceVersion(workspace.id,{id:ids.commit,operation:copy,commit_ids:[commit.id],expected_version:1,expected_head:"",message:t(`versioning.operation.${copy}`)},signal);
      if(!signal.aborted)router.push(`/workspaces/${workspace.id}`);
    }
  });
  return <div className="space-y-3">
    <div className="flex flex-wrap gap-2"><button className={button} disabled={busy} onClick={()=>loadSnapshot()}>{t("versioning.snapshot")}</button>{user&&(["fork","revert","restore"] as const).map(action=><button key={action} className={button} disabled={busy} onClick={()=>{setCopy(action);setError("");setCreatedCopy(null)}}>{t(action==="fork"?"versioning.fork":`versioning.operation.${action}`)}</button>)}</div>
    {[commit.parent_id,commit.merge_parent_id].filter(Boolean).length>0&&<div className="flex flex-wrap gap-2 text-xs text-text-muted"><span>{t("versioning.parents")}</span>{[commit.parent_id,commit.merge_parent_id].filter((id):id is string=>!!id).map(id=><Link className="font-mono text-primary" href={`/commits/${id}`} key={id}>{id.slice(0,8)}</Link>)}</div>}
    {commit.replaced_by.length>0&&<div className="flex flex-wrap gap-2 text-sm"><span>{t("versioning.replacedBy")}</span>{commit.replaced_by.map(id=><Link key={id} href={`/commits/${id}`} className="font-mono text-primary">{id.slice(0,8)}</Link>)}</div>}
    {commit.source_ids.length>0&&<div className="flex flex-wrap gap-2 text-xs text-text-muted"><span>{t("versioning.sourceCommits")}</span>{commit.source_ids.map(id=><Link key={id} href={`/commits/${id}`} className="font-mono text-primary">{id.slice(0,8)}</Link>)}</div>}
    <form className="flex flex-wrap items-end gap-2" onSubmit={event=>{event.preventDefault();void run(async signal=>{const value=await compareCommits(base.trim(),commit.id,signal);if(!signal.aborted){setComparison(value.changes);setCompareTotal(value.total);setCompareOffset(0)}})}}><label className="min-w-0 flex-1 text-xs text-text-muted">{t("versioning.compareBase")}<input className={`${input} mt-1 font-mono`} value={base} onChange={event=>setBase(event.target.value)} /></label><button className={button} disabled={busy||!base.trim()}>{t("versioning.compare")}</button></form>
    {error&&!copy&&<p role="alert" className="text-sm text-danger">{error}</p>}
    {showSnapshot&&snapshot&&<Modal open onClose={()=>setShowSnapshot(false)} title={t("versioning.snapshot")} maxWidth="max-w-6xl"><div className="space-y-4">
      {snapshot.scope==="known_records"&&<p className="text-sm text-text-muted">{t("versioning.knownSnapshot")}</p>}
      <div className="grid gap-4 lg:grid-cols-[18rem_minmax(0,1fr)]"><nav aria-label={t("versioning.snapshot")} className="space-y-1">{snapshot.objects.map(object=><button type="button" key={`${object.target}:${object.id}`} className={`${button} w-full break-words text-left aria-pressed:border-primary`} aria-pressed={selected?.target===object.target&&selected.id===object.id} onClick={()=>{setSelected(object);setAuthor(null);setPointer(object.target==="entity"?"/title":"/attributes")}}>{String(object.document?.title||object.base?.title||object.id)}{!object.document&&<span className="ml-2 text-xs text-danger">{t("collaboration.deleted")}</span>}</button>)}</nav><div className="min-w-0 space-y-4">{selected&&(selected.target==="entity"&&selected.document?<RevisionSnapshotView snapshot={selected.document} assetContext={{kind:"commit",id:commit.id}}/>:<ConflictValue value={selected.document} present={!!selected.document}/>)}
        {selected&&<form className="space-y-2 border-t border-line pt-3" onSubmit={event=>{event.preventDefault();void run(async signal=>{const result=await getVersionFieldAuthor(commit.id,selected.target,selected.id,pointer,signal);if(!signal.aborted)setAuthor(result)})}}><label className="block text-xs text-text-muted">{t("versioning.fieldPath")}<input className={`${input} mt-1 font-mono`} value={pointer} onChange={event=>setPointer(event.target.value)}/></label><button className={button} disabled={busy}>{t("versioning.blame")}</button>{author&&<p className="text-sm"><Link className="text-primary" href={`/commits/${author.id}`}>{author.message}</Link> · {author.authors.map(a=>a.name||a.id).join(", ")}</p>}</form>}
      </div></div><div className="flex items-center justify-between gap-3"><button className={button} disabled={busy||snapshot.offset===0} onClick={()=>loadSnapshot(Math.max(0,snapshot.offset-20))}>{t("catalog.prevPage")}</button><span className="text-xs text-text-muted">{Math.min(snapshot.offset+1,snapshot.total)}–{Math.min(snapshot.offset+snapshot.limit,snapshot.total)} / {snapshot.total}</span><button className={button} disabled={busy||snapshot.offset+snapshot.limit>=snapshot.total} onClick={()=>loadSnapshot(snapshot.offset+20)}>{t("catalog.nextPage")}</button></div>
    </div></Modal>}
    {comparison&&<Modal open onClose={()=>setComparison(null)} title={t("versioning.compare")} maxWidth="max-w-4xl"><ul className="divide-y divide-line">{comparison.map(change=><li key={`${change.target}:${change.id}`} className="flex flex-wrap justify-between gap-3 py-3"><span>{String(change.after?.title||change.before?.title||change.id)}</span><button className={button} onClick={()=>setDiff(change)}>{t("collaboration.viewDiff")}</button></li>)}</ul>{comparison.length===0&&<p className="text-sm text-text-muted">{t("versioning.sameSnapshot")}</p>}<div className="mt-4 flex items-center justify-between"><button className={button} disabled={busy||compareOffset===0} onClick={()=>void run(async signal=>{const value=await compareCommits(base.trim(),commit.id,signal,Math.max(0,compareOffset-20));if(!signal.aborted){setComparison(value.changes);setCompareOffset(value.offset);setCompareTotal(value.total)}})}>{t("catalog.prevPage")}</button><span className="text-xs text-text-muted">{Math.min(compareOffset+1,compareTotal)}–{Math.min(compareOffset+20,compareTotal)} / {compareTotal}</span><button className={button} disabled={busy||compareOffset+20>=compareTotal} onClick={()=>void run(async signal=>{const value=await compareCommits(base.trim(),commit.id,signal,compareOffset+20);if(!signal.aborted){setComparison(value.changes);setCompareOffset(value.offset);setCompareTotal(value.total)}})}>{t("catalog.nextPage")}</button></div></Modal>}
    {diff&&<RevisionDiffInspector base={diff.before?{snapshot:diff.before,version:diff.base_version,label:base}:null} current={{snapshot:diff.after,version:diff.base_version+1,label:commit.id}} onClose={()=>setDiff(null)} assetContext={{kind:"commit",id:commit.id}}/>}
    {copy&&<Modal open onClose={()=>{if(!busy)setCopy(null)}} title={t(copy==="fork"?"versioning.fork":`versioning.operation.${copy}`)} maxWidth="max-w-xl"><form className="space-y-4" onSubmit={event=>{event.preventDefault();submit()}}><p className="text-sm text-text-muted">{t(copy==="fork"?"versioning.forkHint":"versioning.newCopyHint")}</p><label className="block text-sm">{t("collaboration.workspaceName")}<input className={`${input} mt-1`} value={name} onChange={event=>setName(event.target.value)} maxLength={200}/></label>{copy==="fork"&&commit.snapshot_scope==="catalog"&&<label className="block text-sm">{t("versioning.forkRecords")}<textarea className={`${input} mt-1 font-mono`} rows={3} value={records} onChange={event=>setRecords(event.target.value)} placeholder={t("versioning.forkRecordsHint")}/></label>}{error&&<p role="alert" className="text-sm text-danger">{error}</p>}{createdCopy&&<Link className="block text-sm text-primary" href={`/workspaces/${createdCopy}`}>{t("collaboration.backToWorkspace")}</Link>}<button className={button} disabled={busy}>{t("versioning.createCopy")}</button></form></Modal>}
  </div>;
}
