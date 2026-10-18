"use client";

import { useEffect, useMemo, useState } from "react";
import { AlertCircle, ChevronLeft, ChevronRight, History, Loader2, RefreshCw } from "lucide-react";
import { fetchOwnAuditLogs, AUDIT_PAGE_SIZE, type AuditLogEntry } from "@/lib/api";
import type { OwnAuditQuery } from "@/lib/api/audit";
import { ACTIVITY_ACTIONS } from "@/lib/activity-actions";
import { activityTargetName } from "@/lib/activity";
import { isoDate, isoTimestamp } from "@/lib/datetime";
import { useAuth } from "@/lib/authContext";
import { useI18n } from "@/i18n/I18nProvider";
import { ActivityDetail, ActivityResult } from "./ActivityDetail";

const CONTROL = "h-9 w-full min-w-0 rounded-control border border-line bg-background px-2.5 text-sm text-text-body focus-visible:outline-primary";
const BUTTON = "inline-flex min-h-9 items-center justify-center gap-1.5 rounded-control border border-line px-3 text-sm text-text-body hover:bg-surfaceSubtle focus-visible:outline-primary disabled:opacity-40";
const EMPTY_FILTERS = { service: "", action: "", result: "", from: "", to: "", target_id: "", request_id: "", per_page: AUDIT_PAGE_SIZE };
type Filters = typeof EMPTY_FILTERS;

export function MyActivityPanel() {
  const { user } = useAuth();
  // 换身份立即卸载私密数据；请求由各自的 AbortController 取消。
  return user?.id ? <ViewerActivityPanel key={user.id} userId={user.id} /> : null;
}

function ViewerActivityPanel({ userId }: { userId: string }) {
  const { t, tr } = useI18n();
  const [draft, setDraft] = useState<Filters>(EMPTY_FILTERS);
  const [query, setQuery] = useState<OwnAuditQuery>({ page: 1, per_page: AUDIT_PAGE_SIZE });
  const [items, setItems] = useState<AuditLogEntry[]>([]);
  const [total, setTotal] = useState(0);
  const [loading, setLoading] = useState(true);
  const [error, setError] = useState("");
  const [filterError, setFilterError] = useState("");
  const [reload, setReload] = useState(0);
  const [selected, setSelected] = useState<AuditLogEntry | null>(null);
  const page = query.page ?? 1;
  const perPage = query.per_page ?? AUDIT_PAGE_SIZE;
  const pages = Math.max(1, Math.ceil(total / perPage));
  const filtered = Object.entries(query).some(([key, value]) => key !== "page" && key !== "per_page" && !!value);
  const groups = useMemo(() => {
    const days = new Map<string, AuditLogEntry[]>();
    for (const entry of items) {
      const day = isoDate(entry.occurred_at);
      if (!days.has(day)) days.set(day, []);
      days.get(day)!.push(entry);
    }
    return Array.from(days);
  }, [items]);

  useEffect(() => {
    const controller = new AbortController();
    setLoading(true);
    setError("");
    setItems([]);
    setSelected(null);
    fetchOwnAuditLogs(userId, query, controller.signal).then((result) => {
      if (controller.signal.aborted) return;
      const lastPage = Math.max(1, Math.ceil(result.total / result.per_page));
      if (result.page > lastPage) {
        setQuery((current) => ({ ...current, page: lastPage }));
        return;
      }
      setItems(result.items);
      setTotal(result.total);
    }).catch((cause: unknown) => {
      if (controller.signal.aborted) return;
      const status = (cause as { status?: number })?.status;
      setTotal(0);
      setError(t(status === 401 ? "settings.activity.loginRequired" : status === 403 ? "settings.activity.forbidden" : "settings.activity.loadFailed"));
    }).finally(() => {
      if (!controller.signal.aborted) setLoading(false);
    });
    return () => controller.abort();
  }, [userId, query, reload, t]);

  function applyFilters(event: React.FormEvent) {
    event.preventDefault();
    if (draft.from && draft.to && draft.from > draft.to) {
      setFilterError(t("settings.activity.dateInvalid"));
      return;
    }
    setFilterError("");
    setQuery({ ...draft, result: draft.result as OwnAuditQuery["result"], page: 1,
      from: draft.from ? `${draft.from}T00:00:00Z` : "",
      to: draft.to ? `${draft.to}T23:59:59.999999Z` : "" });
  }

  return (
    <section className="space-y-5 p-4 sm:p-5" aria-busy={loading}>
      <header className="flex items-start justify-between gap-3">
        <div>
          <h3 className="flex items-center gap-2 text-sm font-semibold text-text-strong"><History className="size-4 text-primary" />{t("settings.activity.title")}</h3>
          <p className="mt-1 text-sm text-text-muted">{t("settings.activity.desc")}</p>
        </div>
        <button type="button" onClick={() => setReload((n) => n + 1)} disabled={loading} className={`${BUTTON} shrink-0`}>
          <RefreshCw className={`size-3.5 ${loading ? "animate-spin" : ""}`} />{t("common.refresh")}
        </button>
      </header>

      <form onSubmit={applyFilters} className="space-y-3 rounded-control border border-line-subtle bg-surfaceSubtle p-3">
        <div className="grid grid-cols-2 gap-3 lg:grid-cols-4">
          <label className="space-y-1 text-xs text-text-muted">{t("settings.activity.filterService")}
            <select className={CONTROL} value={draft.service} onChange={(e) => setDraft({ ...draft, service: e.target.value, action: "" })}>
              <option value="">{t("settings.activity.allServices")}</option>
              {["catalog", "auth", "community", "storage"].map((s) => <option key={s} value={s}>{tr(`settings.activity.service.${s}`, s)}</option>)}
            </select>
          </label>
          <label className="space-y-1 text-xs text-text-muted">{t("settings.activity.filterAction")}
            <select className={CONTROL} value={draft.action} onChange={(e) => setDraft({ ...draft, action: e.target.value })}>
              <option value="">{t("settings.activity.allActions")}</option>
              {ACTIVITY_ACTIONS.filter((a) => !draft.service || a.service === draft.service).map((a) => <option key={`${a.service}:${a.code}`} value={a.code}>{tr(`settings.activity.action.${a.code}`, a.code)}</option>)}
            </select>
          </label>
          <label className="space-y-1 text-xs text-text-muted">{t("settings.activity.filterResult")}
            <select className={CONTROL} value={draft.result} onChange={(e) => setDraft({ ...draft, result: e.target.value })}>
              <option value="">{t("settings.activity.allResults")}</option>
              <option value="success">{t("settings.activity.resultSuccess")}</option>
              <option value="failure">{t("settings.activity.resultFailure")}</option>
            </select>
          </label>
          <label className="space-y-1 text-xs text-text-muted">{t("settings.activity.pageSize")}
            <select className={CONTROL} value={draft.per_page} onChange={(e) => setDraft({ ...draft, per_page: Number(e.target.value) })}>
              {[20, 50, 100].map((n) => <option key={n} value={n}>{t("settings.activity.perPage", { count: n })}</option>)}
            </select>
          </label>
        </div>
        <details className="text-sm text-text-muted">
          <summary className="w-fit cursor-pointer py-1">{t("settings.activity.moreFilters")}</summary>
          <div className="mt-2 grid gap-3 sm:grid-cols-2">
            {(["from", "to", "target_id", "request_id"] as const).map((field) => <label key={field} className="space-y-1 text-xs">
              {t(`settings.activity.${field === "target_id" ? "colTarget" : field === "request_id" ? "requestIdLabel" : field}`)}{(field === "from" || field === "to") && " (UTC)"}
              <input type={field === "from" || field === "to" ? "date" : "text"} className={CONTROL} value={draft[field]} maxLength={200} onChange={(e) => setDraft({ ...draft, [field]: e.target.value })} />
            </label>)}
          </div>
        </details>
        {filterError && <p role="alert" className="text-sm text-danger">{filterError}</p>}
        <div className="flex flex-wrap gap-2">
          <button type="submit" className={`${BUTTON} border-primary/30 bg-primary/10 text-primary`}>{t("settings.activity.applyFilters")}</button>
          <button type="button" className={BUTTON} onClick={() => { setDraft(EMPTY_FILTERS); setFilterError(""); setQuery({ page: 1, per_page: AUDIT_PAGE_SIZE }); }}>{t("settings.activity.resetFilters")}</button>
        </div>
      </form>

      {error ? <div role="alert" className="flex items-center gap-2 rounded-control border border-danger/30 bg-danger/5 p-3 text-sm text-danger">
        <AlertCircle className="size-4 shrink-0" /><span className="flex-1">{error}</span>
        <button type="button" className={BUTTON} onClick={() => setReload((n) => n + 1)}>{t("common.retry")}</button>
      </div> : loading ? <div role="status" className="flex items-center justify-center gap-2 py-12 text-sm text-text-muted"><Loader2 className="size-4 animate-spin" />{t("common.loading")}</div>
        : items.length === 0 ? <div role="status" className="rounded-control border border-line-subtle py-12 text-center text-sm text-text-muted"><History className="mx-auto mb-3 size-6 text-text-faint" />{t(filtered ? "settings.activity.noMatches" : "settings.activity.empty")}</div>
          : <div className="space-y-5">{groups.map(([day, entries]) => <section key={day}>
            <h4 className="mb-2 text-xs font-medium text-text-muted">{day || "—"} <span className="font-normal text-text-faint">UTC</span></h4>
            <ol className="ml-1.5 border-l border-line">
              {entries.map((entry) => {
                const title = activityTargetName(entry);
                const target = tr(`settings.activity.target.${entry.target_type}`, entry.target_type || t("settings.activity.unknownTarget"));
                return <li key={entry.id} className="relative pl-4">
                  <span aria-hidden="true" className={`absolute -left-[4.5px] top-5 size-2 rounded-full border-2 border-surface ${entry.result === "failure" ? "bg-danger" : "bg-primary"}`} />
                  <button type="button" className="group mb-1 w-full rounded-control p-3 text-left transition-colors hover:bg-surfaceSubtle focus-visible:outline-primary" onClick={() => setSelected(entry)} aria-label={`${t("settings.activity.viewDetails")}: ${tr(`settings.activity.action.${entry.action}`, t("settings.activity.unknownAction"))}`}>
                    <div className="flex flex-wrap items-center gap-x-2 gap-y-1">
                      <span className="text-sm font-medium text-text-strong">{tr(`settings.activity.action.${entry.action}`, t("settings.activity.unknownAction"))}</span>
                      <ActivityResult result={entry.result} />
                      <time className="ml-auto text-xs tabular-nums text-text-faint" dateTime={entry.occurred_at} title={isoTimestamp(entry.occurred_at)}>{isoTimestamp(entry.occurred_at).slice(11, 19) || "—"}</time>
                    </div>
                    <p className="mt-1.5 line-clamp-2 break-words text-sm text-text-body">{title || target}{!title && entry.target_id ? <span className="ml-2 text-xs text-text-muted">{entry.target_id.slice(0, 8)}</span> : null}</p>
                    <div className="mt-1.5 flex flex-wrap items-center gap-x-2 gap-y-1 text-xs text-text-muted">
                      <span>{tr(`settings.activity.service.${entry.service}`, entry.service)}</span>
                      {entry.credential_type && <><span aria-hidden="true">·</span><span>{tr(`settings.activity.credential.${entry.credential_type}`, entry.credential_type)}</span></>}
                      <span className="ml-auto text-primary opacity-0 group-hover:opacity-100 group-focus-visible:opacity-100">{t("settings.activity.details")} →</span>
                    </div>
                  </button>
                </li>;
              })}
            </ol>
          </section>)}</div>}

      {!loading && !error && total > 0 && <nav aria-label={t("settings.activity.title")} className="flex flex-wrap items-center justify-between gap-3 border-t border-line-subtle pt-4 text-xs text-text-muted">
        <span>{t("settings.activity.total", { count: total })}</span>
        <div className="flex items-center gap-2">
          <button type="button" className={BUTTON} disabled={page <= 1} onClick={() => setQuery({ ...query, page: page - 1 })}><ChevronLeft className="size-3.5" /><span className="sr-only sm:not-sr-only">{t("settings.activity.prev")}</span></button>
          <span aria-live="polite">{t("settings.activity.page", { page, pages })}</span>
          <button type="button" className={BUTTON} disabled={page >= pages} onClick={() => setQuery({ ...query, page: page + 1 })}><span className="sr-only sm:not-sr-only">{t("settings.activity.next")}</span><ChevronRight className="size-3.5" /></button>
        </div>
      </nav>}
      <details className="border-t border-line-subtle pt-3 text-xs leading-relaxed text-text-muted">
        <summary className="w-fit cursor-pointer">{t("settings.activity.note")}</summary>
        <p className="mt-2">{t("settings.activity.completeness")}</p>
      </details>
      {selected && <ActivityDetail entry={selected} onClose={() => setSelected(null)} />}
    </section>
  );
}

export default MyActivityPanel;
