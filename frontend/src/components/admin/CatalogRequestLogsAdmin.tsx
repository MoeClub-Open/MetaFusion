"use client";

import { useCallback, useEffect, useRef, useState } from "react";
import { Activity, RefreshCw } from "lucide-react";
import { useI18n } from "@/i18n/I18nProvider";
import { useAuth } from "@/lib/authContext";
import { ApiError, fetchApi } from "@/lib/api/client";
import { can, CATALOG_LIFECYCLE_MANAGE } from "@/lib/permissions";

interface CatalogRequestLog {
  id: string;
  user_id: string;
  at: string;
  credential_type: string;
  credential_name: string;
  method: string;
  route: string;
  status: number;
  ms: number;
}

interface LogFilters {
  user_id: string;
  credential: string;
  status: string;
  path: string;
}

const emptyFilters: LogFilters = { user_id: "", credential: "", status: "", path: "" };
const PAGE_SIZE = 50;
const inputClass = "w-full rounded-lg border border-line bg-surface px-3 py-2 text-xs text-text-strong placeholder:text-text-faint focus:border-primary focus:outline-none";
const buttonClass = "inline-flex items-center justify-center gap-1.5 rounded-lg border border-line px-3 py-2 text-xs text-text-body hover:bg-surfaceHover disabled:opacity-40";

export function CatalogRequestLogsAdmin() {
  const { t, tr, locale } = useI18n();
  const { user } = useAuth();
  const allowed = can(user, CATALOG_LIFECYCLE_MANAGE);
  const [draft, setDraft] = useState<LogFilters>(emptyFilters);
  const [filters, setFilters] = useState<LogFilters>(emptyFilters);
  const [page, setPage] = useState(1);
  const [items, setItems] = useState<CatalogRequestLog[]>([]);
  const [total, setTotal] = useState<number | null>(null);
  const [hasMore, setHasMore] = useState(false);
  const [loading, setLoading] = useState(false);
  const [errorKey, setErrorKey] = useState("");
  const request = useRef(0);

  const load = useCallback(async () => {
    if (!allowed) return;
    const version = ++request.current;
    setLoading(true);
    setErrorKey("");
    const params = new URLSearchParams({ limit: String(PAGE_SIZE), offset: String((page - 1) * PAGE_SIZE) });
    for (const [key, value] of Object.entries(filters)) if (value) params.set(key, value);
    try {
      const result = await fetchApi<{ items: CatalogRequestLog[]; total: number; has_more: boolean }>(`/admin/catalog-request-logs?${params}`);
      if (request.current !== version) return;
      if (!Array.isArray(result.items) || !Number.isFinite(result.total) || typeof result.has_more !== "boolean") throw new Error("invalid_response");
      setItems(result.items);
      setTotal(result.total);
      setHasMore(result.has_more);
    } catch (error) {
      if (request.current !== version) return;
      setItems([]);
      setTotal(null);
      setHasMore(false);
      setErrorKey(error instanceof ApiError && error.status === 403 ? "forbidden"
        : error instanceof ApiError && error.status === 400 ? "invalidFilters" : "failed");
    } finally {
      if (request.current === version) setLoading(false);
    }
  }, [allowed, filters, page, user?.id]);

  useEffect(() => {
    void load();
    return () => { request.current++; };
  }, [load]);

  if (!allowed) return <p role="alert" className="text-sm text-danger">{t("admin.requestLogs.forbidden")}</p>;

  return (
    <div className="space-y-4">
      <div className="flex flex-wrap items-start justify-between gap-3 rounded-xl border border-line-subtle bg-surfaceSubtle p-4">
        <div>
          <h2 className="flex items-center gap-2 text-base font-semibold text-text-strong"><Activity className="size-4" />{t("admin.requestLogs.title")}</h2>
          <p className="mt-1 max-w-3xl text-xs leading-relaxed text-text-muted">{t("admin.requestLogs.description")}</p>
        </div>
        <button type="button" className={buttonClass} onClick={() => void load()} disabled={loading}>
          <RefreshCw className={`size-3.5 ${loading ? "animate-spin" : ""}`} />{t("common.refresh")}
        </button>
      </div>

      <form className="grid gap-3 rounded-xl border border-line-subtle bg-surfaceSubtle p-4 sm:grid-cols-2 xl:grid-cols-4" onSubmit={(event) => {
        event.preventDefault();
        const next = Object.fromEntries(Object.entries(draft).map(([key, value]) => [key, value.trim()])) as unknown as LogFilters;
        if (page === 1 && JSON.stringify(next) === JSON.stringify(filters)) void load();
        else { setFilters(next); setPage(1); }
      }}>
        <label className="space-y-1 text-xs text-text-muted">
          <span>{t("admin.requestLogs.user")}</span>
          <input className={`${inputClass} font-mono`} value={draft.user_id} maxLength={128} onChange={(event) => setDraft({ ...draft, user_id: event.target.value })} placeholder={t("admin.requestLogs.userPlaceholder")} />
        </label>
        <label className="space-y-1 text-xs text-text-muted">
          <span>{t("admin.requestLogs.credential")}</span>
          <input className={inputClass} value={draft.credential} maxLength={255} onChange={(event) => setDraft({ ...draft, credential: event.target.value })} placeholder={t("admin.requestLogs.credentialPlaceholder")} />
        </label>
        <label className="space-y-1 text-xs text-text-muted">
          <span>{t("admin.requestLogs.status")}</span>
          <input className={`${inputClass} font-mono`} type="number" min={100} max={599} step={1} value={draft.status} onChange={(event) => setDraft({ ...draft, status: event.target.value })} placeholder={t("admin.requestLogs.statusPlaceholder")} />
        </label>
        <label className="space-y-1 text-xs text-text-muted">
          <span>{t("admin.requestLogs.route")}</span>
          <input className={`${inputClass} font-mono`} value={draft.path} maxLength={256} onChange={(event) => setDraft({ ...draft, path: event.target.value })} placeholder={t("admin.requestLogs.routePlaceholder")} />
        </label>
        <div className="flex gap-2 sm:col-span-2 xl:col-span-4">
          <button type="submit" className={buttonClass}>{t("catalog.searchAction")}</button>
          <button type="button" className={buttonClass} onClick={() => { setDraft(emptyFilters); setFilters(emptyFilters); setPage(1); }}>{t("admin.requestLogs.clear")}</button>
        </div>
      </form>

      {loading ? <p role="status" className="py-12 text-center text-xs text-text-muted">{t("common.loading")}</p>
        : errorKey ? <div role="alert" className="space-y-2 rounded-lg border border-danger/30 p-4 text-xs text-danger">
          <p>{t(`admin.requestLogs.${errorKey}`)}</p>
          <button type="button" className={buttonClass} onClick={() => void load()}>{t("catalog.retry")}</button>
        </div>
          : items.length === 0 ? <p className="rounded-lg border border-dashed border-line p-8 text-center text-xs text-text-muted">{t("admin.requestLogs.empty")}</p>
            : <div className="overflow-x-auto rounded-xl border border-line-subtle">
              <table className="w-full min-w-[950px] text-left text-xs">
                <thead className="border-b border-line-subtle bg-surfaceSubtle text-text-muted"><tr>
                  <th className="px-3 py-2.5 font-medium">{t("admin.requestLogs.time")}</th>
                  <th className="px-3 py-2.5 font-medium">{t("admin.requestLogs.user")}</th>
                  <th className="px-3 py-2.5 font-medium">{t("admin.requestLogs.credential")}</th>
                  <th className="px-3 py-2.5 font-medium">{t("admin.requestLogs.method")}</th>
                  <th className="px-3 py-2.5 font-medium">{t("admin.requestLogs.route")}</th>
                  <th className="px-3 py-2.5 font-medium">{t("admin.requestLogs.status")}</th>
                  <th className="px-3 py-2.5 font-medium text-right">{t("admin.requestLogs.duration")}</th>
                </tr></thead>
                <tbody className="divide-y divide-line-subtle">{items.map((item) => <tr key={item.id} className="align-top hover:bg-surfaceSubtle">
                  <td className="whitespace-nowrap px-3 py-2.5 text-text-muted"><time dateTime={item.at}>{Number.isNaN(Date.parse(item.at)) ? item.at : new Date(item.at).toLocaleString(locale)}</time></td>
                  <td className="max-w-56 break-all px-3 py-2.5 font-mono text-text-body">{item.user_id || t("settings.activity.credential.anonymous")}</td>
                  <td className="max-w-48 break-words px-3 py-2.5 text-text-body"><div>{item.credential_name || "—"}</div><div className="mt-0.5 text-text-faint">{tr(`settings.activity.credential.${item.credential_type}`, item.credential_type)}</div></td>
                  <td className="px-3 py-2.5 font-mono text-text-muted">{item.method}</td>
                  <td className="max-w-sm break-all px-3 py-2.5 font-mono text-text-body">{item.route}</td>
                  <td className={`px-3 py-2.5 font-mono ${item.status >= 500 ? "text-danger" : item.status >= 400 ? "text-warn" : "text-success"}`}>{item.status}</td>
                  <td className="whitespace-nowrap px-3 py-2.5 text-right font-mono text-text-muted">{t("admin.requestLogs.milliseconds", { value: item.ms })}</td>
                </tr>)}</tbody>
              </table>
            </div>}

      {!loading && !errorKey && total !== null && <div className="flex flex-wrap items-center justify-between gap-3 text-xs text-text-muted">
        <span>{t("admin.pagination", { page, pages: Math.max(1, Math.ceil(total / PAGE_SIZE)), total })}</span>
        <div className="flex gap-2">
          <button type="button" className={buttonClass} disabled={page === 1} onClick={() => setPage((current) => current - 1)}>{t("catalog.prevPage")}</button>
          <button type="button" className={buttonClass} disabled={!hasMore} onClick={() => setPage((current) => current + 1)}>{t("catalog.nextPage")}</button>
        </div>
      </div>}
    </div>
  );
}
