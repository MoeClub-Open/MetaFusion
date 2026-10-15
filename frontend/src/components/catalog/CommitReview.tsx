"use client";

import { useCallback, useEffect, useState } from "react";
import Link from "next/link";
import { useI18n } from "@/i18n/I18nProvider";
import { useAuth } from "@/lib/authContext";
import { can, CATALOG_REVIEW_MANAGE } from "@/lib/permissions";
import type { CommitSubmission } from "@/lib/catalogCommits";
import { localizeCatalogError } from "@/lib/catalogErrors";
import { api } from "./api";
import { RevisionDiffInspector } from "./RevisionDiffInspector";

export function CommitReview({id}: {id:string}) {
  const {t} = useI18n(); const {user} = useAuth();
  const [data,setData] = useState<CommitSubmission>();
  const [error,setError] = useState(""); const [busy,setBusy] = useState(false);
  const [note,setNote] = useState("");
  const load = useCallback(async () => {
    try {setData(await api<CommitSubmission>(`/catalog/commits/${id}/submission`));setError("");}
    catch(e){setError(localizeCatalogError((e as Error).message,t));}
  },[id,t]);
  useEffect(() => {void load()},[load]);
  const review = async (decision:"approve"|"reject") => {
    setBusy(true);setError("");
    try {await api(`/catalog/commits/${id}/review`,"POST",{decision,note});await load();}
    catch(e){setError(localizeCatalogError((e as Error).message,t));}
    finally{setBusy(false)}
  };
  return <section className="catalog-v2 cv-form space-y-5">
    {error && <p role="alert" className="cv-error">{error}</p>}
    {!data ? !error && <p>{t("common.loading")}</p> : <>
      <header className="space-y-2">
        <h1>{t("catalog.review.submission")}</h1>
        <p className="break-all font-mono text-sm">{id}</p>
        <p>{data.author_name || data.author_id} · {new Date(data.created_at).toLocaleString()}</p>
        <strong>{t(`catalog.review.${data.receipt.review_status}`)}{data.receipt.review_status === "reviewed" && ` · ${t(`catalog.review.${data.receipt.review_decision}`)}`}</strong>
        <p>{t(data.receipt.applied ? "catalog.review.applied" : "catalog.review.headUnchanged")}</p>
        <p className="whitespace-pre-wrap">{data.request.edit_note}</p>
        {data.receipt.review_note && <p className="whitespace-pre-wrap">{data.receipt.review_note}</p>}
        {data.receipt.reviewed_by && <p>{t("catalog.review.reviewer")}: {data.receipt.reviewed_by} · {data.receipt.reviewed_at && new Date(data.receipt.reviewed_at).toLocaleString()}</p>}
      </header>
      <ul className="space-y-1">{data.request.sources.map((s,i) => <li key={i}>
        {s.url ? <a href={s.url} target="_blank" rel="noopener noreferrer" className="text-primary underline">{s.citation}</a> : s.citation}
      </li>)}</ul>
      {data.receipt.preview?.map((p,i) => <section key={i} className="space-y-2">
        <p>{t(`catalog.review.action.${p.action}`)} · {p.after?.title || p.before?.title || p.id}</p>
        {data.receipt.applied && p.target === "entity" && <Link href={`/catalog/${p.id}`} className="text-primary underline">{t("catalog.review.openEntity")}</Link>}
        <RevisionDiffInspector base={{snapshot:p.before || {},version:p.before?.version || 0}}
          current={{label:t("catalog.review.proposed"),snapshot:p.after || {},version:p.after?.version || 0,actor_name:data.author_name}}/>
      </section>)}
      {can(user,CATALOG_REVIEW_MANAGE) && data.receipt.review_status === "pending_review" && <fieldset className="space-y-3" disabled={busy}>
        <label>{t("catalog.review.note")}<textarea maxLength={2000} value={note} onChange={e => setNote(e.target.value)}/></label>
        <p>{t("catalog.review.revalidate")}</p>
        <div className="flex gap-3"><button className="cv-primary" onClick={() => void review("approve")}>{t("catalog.review.approve")}</button>
          <button onClick={() => void review("reject")}>{t("catalog.review.reject")}</button></div>
      </fieldset>}
    </>}
  </section>;
}

export function CommitReviewQueue() {
  const {t} = useI18n(); const [items,setItems] = useState<CommitSubmission[]>([]);
  const [status,setStatus] = useState("pending_review");const [page,setPage] = useState(0);
  const [error,setError] = useState("");const [busy,setBusy] = useState(false);
  const load = useCallback(async () => {
    setBusy(true);setError("");
    try {setItems(await api<CommitSubmission[]>(`/catalog/commits?review_status=${status}&limit=25&offset=${page*25}`));}
    catch(e){setError(localizeCatalogError((e as Error).message,t))}
    finally{setBusy(false)}
  },[status,page,t]);
  useEffect(() => {void load()},[load]);
  return <section className="space-y-4">
    <h2 className="text-xl font-semibold">{t("catalog.review.queue")}</h2>
    <p>{t("catalog.review.headUnchanged")}</p>
    <div className="flex gap-3">{["pending_review","reviewed"].map(s => <button key={s} aria-pressed={status===s}
      className="rounded border border-line px-3 py-2 aria-pressed:bg-primary/15" onClick={() => {setStatus(s);setPage(0)}}>{t(`catalog.review.${s}`)}</button>)}
      <button onClick={() => void load()} disabled={busy}>{t("common.refresh")}</button></div>
    {error && <p role="alert" className="text-danger">{error}</p>}
    {busy ? <p>{t("common.loading")}</p> : !error && <>
      {!items.length && <p>{t("catalog.review.empty")}</p>}
      <ul className="space-y-2">{items.map(item => <li key={item.receipt.id} className="rounded border border-line p-4">
        <Link className="text-primary underline" href={`/submissions/${item.receipt.id}`}>{item.request.edit_note || item.receipt.id}</Link>
        <p className="text-sm text-text-muted">{item.author_name || item.author_id} · {new Date(item.created_at).toLocaleString()} · {t(`catalog.review.${item.receipt.review_decision}`)}</p>
      </li>)}</ul>
    </>}
    <div className="flex gap-3"><button disabled={page===0||busy} onClick={() => setPage(p=>p-1)}>{t("catalog.prevPage")}</button>
      <span>{page+1}</span><button disabled={items.length<25||busy} onClick={() => setPage(p=>p+1)}>{t("catalog.nextPage")}</button></div>
  </section>;
}
