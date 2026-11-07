"use client";

import { useCallback, useEffect, useState } from "react";
import { Pencil, Plus, RefreshCw } from "lucide-react";
import { useI18n } from "@/i18n/I18nProvider";
import { Modal } from "@/components/ui/Modal";
import { Select } from "@/components/ui/Select";
import { assistantRequest, type AssistantPage, type SearchProvider, type SearchRequestRecord } from "@/lib/assistantApi";

const control = "w-full rounded-control border border-line bg-background px-3 py-2 text-sm text-text-body";
const button = "inline-flex items-center gap-1.5 rounded-control border border-line px-3 py-1.5 text-xs hover:bg-surfaceHover disabled:opacity-50";
type Draft = Pick<SearchProvider, "name" | "kind" | "base_url" | "enabled" | "is_default" | "credits_per_request"> & { id: string; api_key: string; clear_key: boolean };

export function SearchAdmin() {
  const { t, locale } = useI18n();
  const [providers, setProviders] = useState<SearchProvider[]>([]);
  const [records, setRecords] = useState<AssistantPage<SearchRequestRecord> | null>(null);
  const [offset, setOffset] = useState(0);
  const [view, setView] = useState<"providers" | "requests">("providers");
  const [draft, setDraft] = useState<Draft | null>(null);
  const [error, setError] = useState("");
  const [busy, setBusy] = useState(false);
  const load = useCallback(async () => {
    setError("");
    try {
      const [p, r] = await Promise.all([assistantRequest<SearchProvider[]>("/admin/search/providers"), assistantRequest<AssistantPage<SearchRequestRecord>>(`/admin/search/requests?limit=25&offset=${offset}`)]);
      setProviders(p); setRecords(r);
    } catch (cause) { setError(cause instanceof Error ? cause.message : t("curator.error")); }
  }, [offset, t]);
  useEffect(() => { void load(); }, [load]);
  const edit = (p?: SearchProvider) => setDraft({ id: p?.id || "", name: p?.name || "", kind: p?.kind || "deepseek", base_url: p?.base_url || "https://api.deepseek.com", enabled: p?.enabled ?? false, is_default: p?.is_default ?? false, credits_per_request: p?.credits_per_request || 0, api_key: "", clear_key: false });
  const save = async () => {
    if (!draft || busy) return;
    setBusy(true); setError("");
    try {
      const { api_key, clear_key, ...data } = draft;
      await assistantRequest(`/admin/search/providers${draft.id ? `/${draft.id}` : ""}`, { method: draft.id ? "PUT" : "POST", body: JSON.stringify({ ...data, ...(clear_key || api_key ? { api_key: clear_key ? "" : api_key } : {}) }) });
      setDraft(null); await load();
    } catch (cause) { setError(cause instanceof Error ? cause.message : t("curator.error")); }
    finally { setBusy(false); }
  };
  return <div className="space-y-3">
    <div className="flex flex-wrap items-center gap-2 text-xs">
      {(["providers", "requests"] as const).map(key => <button key={key} type="button" onClick={() => setView(key)} className={`${button} ${view === key ? "text-primary" : "text-text-muted"}`}>{t(`assistant.admin.tab.${key}`)}</button>)}
      <button type="button" className={`${button} ml-auto`} onClick={() => void load()} aria-label={t("curator.refresh")}><RefreshCw className="size-3.5" /></button>
      {view === "providers" && <button type="button" className={button} onClick={() => edit()}><Plus className="size-3.5" />{t("assistant.search.add")}</button>}
    </div>
    {error && <p role="alert" className="text-xs text-danger">{error}</p>}
    <div className="overflow-x-auto rounded-control border border-line">
      {view === "providers" ? <table className="w-full text-left text-xs"><thead className="bg-surfaceSubtle text-text-muted"><tr>
        {["name", "base_url", "credits_per_request", "key_hint", "enabled"].map(key => <th key={key} className="px-3 py-2 font-medium">{t(key === "credits_per_request" ? "assistant.search.price" : `assistant.admin.field.${key}`)}</th>)}<th className="w-12"><span className="sr-only">{t("common.edit")}</span></th>
      </tr></thead><tbody>{providers.map(p => <tr key={p.id} className="border-t border-line">
        <td className="px-3 py-2">{p.name}{p.is_default && <span className="ml-2 text-primary">{t("assistant.search.defaultBadge")}</span>}</td><td className="px-3 py-2">{p.base_url}</td><td className="px-3 py-2 tabular-nums">{p.credits_per_request}</td><td className="px-3 py-2">{p.key_hint || "—"}</td><td className="px-3 py-2">{t(p.enabled ? "assistant.admin.enabled" : "assistant.admin.disabled")}</td><td className="p-2"><button type="button" onClick={() => edit(p)} className={button} aria-label={t("common.edit")}><Pencil className="size-3.5" /></button></td>
      </tr>)}</tbody></table> : <table className="w-full text-left text-xs"><thead className="bg-surfaceSubtle text-text-muted"><tr>
        {["created_at", "user_id", "purpose", "status", "credits"].map(key => <th key={key} className="px-3 py-2 font-medium">{t(`assistant.admin.field.${key}`)}</th>)}<th className="px-3 py-2 font-medium">{t("assistant.search.costCNY")}</th>
      </tr></thead><tbody>{records?.items.map(r => <tr key={r.id} className="border-t border-line"><td className="px-3 py-2 whitespace-nowrap">{new Date(r.created_at).toLocaleString(locale)}</td><td className="px-3 py-2 font-mono" title={r.user_id}>{r.user_id.slice(0, 8)}</td><td className="px-3 py-2">{r.operation} · {r.purpose}</td><td className="px-3 py-2" title={r.error}>{r.status}</td><td className="px-3 py-2">{r.credits}</td><td className="px-3 py-2">{r.status === "unknown" ? "—" : (r.upstream_cost_microcny / 1_000_000).toLocaleString(locale, { maximumFractionDigits: 6 })}</td></tr>)}</tbody></table>}
    </div>
    {view === "requests" && records && records.total > 25 && <div className="flex justify-end gap-3 text-xs"><button type="button" disabled={!offset} onClick={() => setOffset(Math.max(0, offset - 25))}>{t("pagination.prev")}</button><span>{offset + 1}–{Math.min(offset + 25, records.total)} / {records.total}</span><button type="button" disabled={offset + 25 >= records.total} onClick={() => setOffset(offset + 25)}>{t("pagination.next")}</button></div>}
    <Modal open={draft !== null} onClose={() => { if (!busy) setDraft(null); }} title={t("assistant.search.provider")}>
      {draft && <form className="space-y-3" onSubmit={event => { event.preventDefault(); void save(); }}>
        {error && <p role="alert" className="text-xs text-danger">{error}</p>}
        <label className="block space-y-1 text-xs"><span>{t("assistant.admin.field.name")}</span><input required value={draft.name} className={control} onChange={e => setDraft({ ...draft, name: e.target.value })} /></label>
        <Select value={draft.kind} onChange={value => setDraft({ ...draft, kind: value as Draft["kind"], base_url: value === "duckduckgo" ? "https://html.duckduckgo.com" : "https://api.deepseek.com", credits_per_request: 0 })} options={[{ value: "duckduckgo", label: "DuckDuckGo" }, { value: "deepseek", label: "DeepSeek" }]} />
        <label className="block space-y-1 text-xs"><span>{t("assistant.admin.field.base_url")}</span><input type="url" required value={draft.base_url} disabled={draft.kind === "duckduckgo"} className={control} onChange={e => setDraft({ ...draft, base_url: e.target.value })} /></label>
        {draft.kind === "deepseek" && <>
          <label className="block space-y-1 text-xs"><span>{t("assistant.admin.field.api_key")}</span><input type="password" autoComplete="new-password" value={draft.api_key} className={control} onChange={e => setDraft({ ...draft, api_key: e.target.value })} /></label>
          <label className="block space-y-1 text-xs"><span>{t("assistant.search.price")}</span><input type="number" min={0} max={1000000} required value={draft.credits_per_request} className={control} onChange={e => setDraft({ ...draft, credits_per_request: Number(e.target.value) })} /></label>
          <label className="flex items-center gap-2 text-xs"><input type="checkbox" checked={draft.clear_key} onChange={e => setDraft({ ...draft, clear_key: e.target.checked })} />{t("assistant.admin.clearKey")}</label>
        </>}
        <div className="flex gap-4 text-xs"><label className="flex items-center gap-2"><input type="checkbox" checked={draft.enabled} onChange={e => setDraft({ ...draft, enabled: e.target.checked, is_default: e.target.checked && draft.is_default })} />{t("assistant.admin.enabled")}</label><label className="flex items-center gap-2"><input type="checkbox" disabled={!draft.enabled} checked={draft.is_default} onChange={e => setDraft({ ...draft, is_default: e.target.checked })} />{t("assistant.search.defaultBadge")}</label></div>
        <button type="submit" disabled={busy} className={`${button} text-primary`}>{t(busy ? "common.saving" : "common.save")}</button>
      </form>}
    </Modal>
  </div>;
}
