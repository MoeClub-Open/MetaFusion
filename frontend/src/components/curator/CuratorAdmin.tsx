"use client";

import { useCallback, useEffect, useMemo, useState } from "react";
import Link from "next/link";
import {
  AlertTriangle, Ban, Bot, ExternalLink, Pause, Play, RefreshCw,
  RotateCcw, ScanSearch, ShieldCheck,
} from "lucide-react";
import { useAuth } from "@/lib/authContext";
import { useI18n } from "@/i18n/I18nProvider";
import { ASSISTANT_MANAGE, CATALOG_REVIEW_MANAGE, can } from "@/lib/permissions";
import { assistantRequest, type AssistantModel, type AssistantPage } from "@/lib/assistantApi";
import {
  curatorRequest, type CuratorFinding, type CuratorPage, type CuratorRun,
  type CuratorSettings, type CuratorSummary,
} from "@/lib/curatorApi";

type Tab = "overview" | "findings" | "runs";
const PAGE_SIZE = 25;
const CODES = [
  "track_position_gap", "track_empty_contents", "medium_missing_tracks", "release_missing_media",
  "dangling_reference", "missing_required_field", "duplicate_title", "work_missing_creation_form",
  "tv_anime_without_agent", "release_without_subject", "translation_zh_kana",
  "duplicate_tag_values", "duplicate_picture_values", "missing_picture_with_links",
];
const STATUSES = ["open", "running", "done", "failed", "ignored", "resolved"];

function numberInput(value: number, min: number, max: number, onChange: (n: number) => void, disabled: boolean) {
  return <input type="number" min={min} max={max} value={Number.isFinite(value) ? value : 0} disabled={disabled}
    onChange={(event) => onChange(Math.max(min, Math.min(max, Number(event.target.value) || 0)))}
    className="w-full rounded-lg border border-line bg-surface px-3 py-2 text-sm text-text-body outline-none focus:border-primary disabled:opacity-60" />;
}

/** 持续审核管理台：设置、队列、运行记录与手动触发都走同一套管理员 API。 */
export function CuratorAdmin() {
  const { t } = useI18n();
  const { user } = useAuth();
  const mayView = can(user, ASSISTANT_MANAGE);
  const mayWrite = can(user, CATALOG_REVIEW_MANAGE);
  const [tab, setTab] = useState<Tab>("overview");
  const [summary, setSummary] = useState<CuratorSummary | null>(null);
  const [draft, setDraft] = useState<CuratorSettings | null>(null);
  const [models, setModels] = useState<AssistantModel[]>([]);
  const [findings, setFindings] = useState<CuratorPage<CuratorFinding> | null>(null);
  const [runs, setRuns] = useState<CuratorPage<CuratorRun> | null>(null);
  const [statusFilter, setStatusFilter] = useState("");
  const [codeFilter, setCodeFilter] = useState("");
  const [kindFilter, setKindFilter] = useState("");
  const [runKindFilter, setRunKindFilter] = useState("agent");
  const [offset, setOffset] = useState(0);
  const [loading, setLoading] = useState(true);
  const [busy, setBusy] = useState("");
  const [error, setError] = useState("");
  const [notice, setNotice] = useState("");

  const fail = useCallback((cause: unknown) => setError(cause instanceof Error ? cause.message : t("curator.error")), [t]);

  const loadSummary = useCallback(async (syncDraft = false) => {
    const value = await curatorRequest<CuratorSummary>("/summary");
    setSummary(value);
    if (syncDraft) setDraft(value.settings);
  }, []);

  const loadModels = useCallback(async () => {
    const value = await assistantRequest<AssistantPage<AssistantModel>>("/admin/models?limit=100&offset=0");
    setModels(value.items.filter((model) => model.enabled));
  }, []);

  const loadFindings = useCallback(async () => {
    const params = new URLSearchParams({ limit: String(PAGE_SIZE), offset: String(offset) });
    if (statusFilter) params.set("status", statusFilter);
    if (codeFilter) params.set("code", codeFilter);
    if (kindFilter) params.set("kind", kindFilter);
    setFindings(await curatorRequest<CuratorPage<CuratorFinding>>(`/findings?${params}`));
  }, [offset, statusFilter, codeFilter, kindFilter]);

  const loadRuns = useCallback(async () => {
    const params = new URLSearchParams({ limit: String(PAGE_SIZE), offset: String(offset) });
    if (runKindFilter) params.set("kind", runKindFilter);
    setRuns(await curatorRequest<CuratorPage<CuratorRun>>(`/runs?${params}`));
  }, [offset, runKindFilter]);

  useEffect(() => {
    if (!mayView) { setLoading(false); return; }
    let alive = true;
    setLoading(true); setError("");
    void Promise.all([loadSummary(true), loadModels()]).catch((cause) => { if (alive) fail(cause); }).finally(() => { if (alive) setLoading(false); });
    return () => { alive = false; };
  }, [mayView, loadSummary, loadModels, fail]);

  useEffect(() => {
    if (!mayView || tab === "overview") return;
    let alive = true;
    setLoading(true); setError("");
    const load = tab === "findings" ? loadFindings() : loadRuns();
    void load.catch((cause) => { if (alive) fail(cause); }).finally(() => { if (alive) setLoading(false); });
    return () => { alive = false; };
  }, [tab, mayView, loadFindings, loadRuns, fail]);

  useEffect(() => {
    if (!mayView) return;
    const timer = window.setInterval(() => { void loadSummary().catch(() => undefined); }, 30000);
    return () => window.clearInterval(timer);
  }, [mayView, loadSummary]);

  const pending = useMemo(() => findings?.items.filter((item) => item.status === "open" || item.status === "failed").length ?? 0, [findings]);

  function update<K extends keyof CuratorSettings>(key: K, value: CuratorSettings[K]) {
    setDraft((current) => current ? { ...current, [key]: value } : current);
  }

  async function saveSettings() {
    if (!draft || !mayWrite || busy) return;
    setBusy("save"); setError(""); setNotice("");
    try {
      await curatorRequest<CuratorSettings>("/settings", { method: "PUT", body: JSON.stringify(draft) });
      setNotice(t("curator.saved"));
      await loadSummary(true);
    } catch (cause) { fail(cause); } finally { setBusy(""); }
  }

  async function control(kind: "pause" | "resume" | "sweep") {
    if (!mayWrite || busy) return;
    setBusy(kind); setError(""); setNotice("");
    try {
      if (kind === "sweep") await curatorRequest("/sweep", { method: "POST" });
      else await curatorRequest(`/${kind}`, { method: "POST", body: JSON.stringify(kind === "pause" ? { reason: "manual" } : {}) });
      setNotice(t(kind === "sweep" ? "curator.sweepQueued" : kind === "pause" ? "curator.paused" : "curator.resumed"));
      await loadSummary();
    } catch (cause) { fail(cause); } finally { setBusy(""); }
  }

  async function act(finding: CuratorFinding, action: "reopen" | "ignore") {
    if (!mayWrite || busy) return;
    setBusy(finding.id); setError(""); setNotice("");
    try {
      await curatorRequest(`/findings/${finding.id}/${action}`, { method: "POST" });
      setNotice(t(`curator.finding.${action}Queued`));
      await loadFindings();
      await loadSummary();
    } catch (cause) { fail(cause); } finally { setBusy(""); }
  }

  if (!mayView) {
    return <div className="rounded-xl border border-line bg-surfaceSubtle p-6 text-sm text-text-muted">{t("curator.noPermission")}</div>;
  }

  const status = summary?.settings.paused ? "paused" : summary?.settings.enabled ? "enabled" : "disabled";

  return <div className="space-y-3">
    <div className="flex flex-wrap items-start justify-between gap-3">
      <div>
        <h2 className="flex items-center gap-2 text-base font-semibold text-text-strong"><Bot className="h-4 w-4 text-primary" />{t("curator.title")}</h2>
      </div>
      <div className="flex flex-wrap items-center gap-2">
        <button type="button" disabled={loading} onClick={() => { setLoading(true); void Promise.all([loadSummary(true), tab === "findings" ? loadFindings() : tab === "runs" ? loadRuns() : Promise.resolve()]).catch(fail).finally(() => setLoading(false)); }}
          className="inline-flex items-center gap-1.5 rounded-lg border border-line px-3 py-2 text-xs text-text-body hover:bg-surfaceHover disabled:opacity-50">
          <RefreshCw className={`h-3.5 w-3.5 ${loading ? "animate-spin" : ""}`} />{t("curator.refresh")}
        </button>
        <button type="button" disabled={!mayWrite || !!busy} onClick={() => void control("sweep")}
          className="inline-flex items-center gap-1.5 rounded-lg border border-line px-3 py-2 text-xs text-text-body hover:bg-surfaceHover disabled:opacity-50">
          <ScanSearch className="h-3.5 w-3.5" />{t("curator.sweep")}
        </button>
        {summary?.settings.paused
          ? <button type="button" disabled={!mayWrite || !!busy} onClick={() => void control("resume")}
              className="inline-flex items-center gap-1.5 rounded-lg border border-line px-3 py-2 text-xs text-text-body hover:bg-surfaceHover disabled:opacity-50">
              <Play className="h-3.5 w-3.5" />{t("curator.resume")}
            </button>
          : <button type="button" disabled={!mayWrite || !!busy} onClick={() => void control("pause")}
              className="inline-flex items-center gap-1.5 rounded-lg border border-line px-3 py-2 text-xs text-text-body hover:bg-surfaceHover disabled:opacity-50">
              <Pause className="h-3.5 w-3.5" />{t("curator.pause")}
            </button>}
      </div>
    </div>

    {!mayWrite ? <div className="flex items-start gap-2 rounded-lg border border-warning/40 bg-warning/10 px-3 py-2 text-xs text-text-body">
      <ShieldCheck className="mt-0.5 h-4 w-4 shrink-0 text-warning" />{t("curator.readOnly")}
    </div> : null}
    {error ? <div className="flex items-start gap-2 rounded-lg border border-danger/40 bg-danger/10 px-3 py-2 text-xs text-danger"><AlertTriangle className="mt-0.5 h-4 w-4 shrink-0" />{error}</div> : null}
    {notice ? <div className="rounded-lg border border-success/40 bg-success/10 px-3 py-2 text-xs text-text-body">{notice}</div> : null}

    <div className="flex flex-wrap gap-1 border-b border-line">
      {(["overview", "findings", "runs"] as Tab[]).map((value) => <button key={value} type="button" onClick={() => { setTab(value); setOffset(0); }}
        className={`border-b-2 px-3 py-2 text-xs transition-colors ${tab === value ? "border-primary font-semibold text-primary" : "border-transparent text-text-muted hover:text-text-strong"}`}>
        {t(`curator.tab.${value}`)}{value === "findings" ? ` (${summary?.counts.open ?? 0})` : ""}
      </button>)}
    </div>

    {tab === "overview" ? <div className="space-y-3">
      <div className="grid gap-3 sm:grid-cols-2 xl:grid-cols-4">
        {[
          ["curator.status", t(`curator.status.${status}`), status === "enabled" ? "text-success" : status === "paused" ? "text-warning" : "text-text-muted"],
          ["curator.queue", `${summary?.counts.open ?? 0} / ${summary?.counts.failed ?? 0}`, "text-text-strong"],
          ["curator.runs24h", String(summary?.runs_24h ?? 0), "text-text-strong"],
          ["curator.budget", `${summary?.spent_credits ?? 0} / ${summary?.settings.monthly_credit_allowance ?? 0}`, "text-text-strong"],
        ].map(([label, value, tone]) => <div key={String(label)} className="rounded-xl border border-line bg-surface p-4">
          <div className="text-[11px] font-medium text-text-faint">{t(String(label))}</div>
          <div className={`mt-1 text-lg font-semibold ${tone}`}>{value}</div>
        </div>)}
      </div>

      <div className="grid gap-3 lg:grid-cols-2">
        <div className="space-y-3 rounded-xl border border-line bg-surface p-4">
          <div className="flex items-center justify-between gap-2">
            <h3 className="text-sm font-semibold text-text-strong">{t("curator.settings.title")}</h3>
            <span className={`text-[11px] ${summary?.lease_held ? "text-success" : "text-text-faint"}`}>{summary?.lease_held ? t("curator.lease.held") : t("curator.lease.idle")}</span>
          </div>
          {draft ? <div className="grid gap-3 sm:grid-cols-2">
            <label className="flex items-center gap-2 text-xs text-text-body"><input type="checkbox" checked={draft.enabled} disabled={!mayWrite || !!busy} onChange={(event) => update("enabled", event.target.checked)} />{t("curator.settings.enabled")}</label>
            <label className="flex items-center gap-2 text-xs text-text-body"><input type="checkbox" checked={draft.auto_merge_low_risk} disabled={!mayWrite || !!busy} onChange={(event) => update("auto_merge_low_risk", event.target.checked)} />{t("curator.settings.autoMerge")}</label>
            <label className="space-y-1 text-xs text-text-muted"><span>{t("curator.settings.model")}</span>
              <select value={draft.model_id} disabled={!mayWrite || !!busy} onChange={(event) => update("model_id", event.target.value)} className="w-full rounded-lg border border-line bg-surface px-3 py-2 text-sm text-text-body outline-none focus:border-primary disabled:opacity-60">
                <option value="">{t("curator.settings.defaultModel")}</option>
                {models.map((model) => <option key={model.id} value={model.id}>{model.name}</option>)}
              </select>
            </label>
            <label className="space-y-1 text-xs text-text-muted"><span>{t("curator.settings.interval")}</span>{numberInput(draft.interval_seconds, 30, 86400, (n) => update("interval_seconds", n), !mayWrite || !!busy)}</label>
            <label className="space-y-1 text-xs text-text-muted"><span>{t("curator.settings.maxRuns")}</span>{numberInput(draft.max_runs_per_hour, 1, 240, (n) => update("max_runs_per_hour", n), !mayWrite || !!busy)}</label>
            <label className="space-y-1 text-xs text-text-muted"><span>{t("curator.settings.allowance")}</span>{numberInput(draft.monthly_credit_allowance, 0, 1_000_000_000_000, (n) => update("monthly_credit_allowance", n), !mayWrite || !!busy)}</label>
            <div className="sm:col-span-2 grid gap-2 sm:grid-cols-2">
              {(["allow_structures", "allow_sources", "allow_names", "allow_dedupe"] as const).map((key) => <label key={key} className="flex items-center gap-2 text-xs text-text-body">
                <input type="checkbox" checked={draft[key]} disabled={!mayWrite || !!busy} onChange={(event) => update(key, event.target.checked)} />{t(`curator.settings.${key}`)}
              </label>)}
            </div>
          </div> : <div className="text-xs text-text-muted">{loading ? t("curator.loading") : t("curator.empty")}</div>}
          <div className="flex flex-wrap items-center justify-between gap-2 border-t border-line pt-3">
            <span className="text-[11px] text-text-faint">{summary?.model ? `${summary.model.name} · ${summary.balance.credits} ${t("curator.credits")}` : t("curator.noModel")}</span>
            <button type="button" disabled={!mayWrite || !!busy || !draft} onClick={() => void saveSettings()}
              className="rounded-lg bg-primary px-3 py-2 text-xs font-semibold text-primary-foreground hover:bg-primary/90 disabled:opacity-50">{busy === "save" ? t("curator.saving") : t("curator.save")}</button>
          </div>
        </div>

        <div className="space-y-3 rounded-xl border border-line bg-surface p-4 text-xs text-text-body">
          <div className="grid grid-cols-2 gap-2">
            {STATUSES.map((value) => <div key={value} className="rounded-lg border border-line px-2 py-1.5">
              <div className="text-[11px] text-text-faint">{t(`curator.status.${value}`)}</div>
              <div className="mt-0.5 font-mono text-sm">{summary?.counts[value] ?? 0}</div>
            </div>)}
          </div>
        </div>
      </div>
    </div> : null}

    {tab === "findings" ? <div className="space-y-3">
      <div className="flex flex-wrap items-center gap-2">
        <select value={statusFilter} onChange={(event) => { setStatusFilter(event.target.value); setOffset(0); }} className="rounded-lg border border-line bg-surface px-2 py-1.5 text-xs text-text-body">
          <option value="">{t("curator.filter.allStatus")}</option>
          {STATUSES.map((value) => <option key={value} value={value}>{t(`curator.status.${value}`)}</option>)}
        </select>
        <select value={codeFilter} onChange={(event) => { setCodeFilter(event.target.value); setOffset(0); }} className="rounded-lg border border-line bg-surface px-2 py-1.5 text-xs text-text-body">
          <option value="">{t("curator.filter.allCode")}</option>
          {CODES.map((value) => <option key={value} value={value}>{value}</option>)}
        </select>
        <select value={kindFilter} onChange={(event) => { setKindFilter(event.target.value); setOffset(0); }} className="rounded-lg border border-line bg-surface px-2 py-1.5 text-xs text-text-body">
          <option value="">{t("curator.filter.allKind")}</option>
          {["agent", "collection", "work", "content_unit", "expression", "release", "medium", "track"].map((value) => <option key={value} value={value}>{value}</option>)}
        </select>
        <span className="text-[11px] text-text-faint">{t("curator.filter.pending")} {pending}</span>
      </div>
      <div className="overflow-x-auto rounded-xl border border-line bg-surface">
        <table className="w-full min-w-[860px] text-left text-xs">
          <thead className="border-b border-line bg-surfaceSubtle text-text-faint">
            <tr>{["curator.col.entity", "curator.col.code", "curator.col.status", "curator.col.attempts", "curator.col.decision", "curator.col.updated", "curator.col.actions"].map((key) => <th key={key} className="px-3 py-2 font-medium">{t(key)}</th>)}</tr>
          </thead>
          <tbody>
            {findings?.items.map((finding) => <tr key={finding.id} className="border-b border-line last:border-0">
              <td className="px-3 py-2">
                <Link href={`/catalog/${finding.entity_id}`} target="_blank" className="inline-flex items-center gap-1 text-primary hover:underline">
                  {finding.entity_title || finding.entity_id.slice(0, 8)}<ExternalLink className="h-3 w-3" />
                </Link>
                <div className="mt-0.5 text-[10px] text-text-faint">{finding.entity_kind} · {finding.id.slice(0, 8)}</div>
              </td>
              <td className="px-3 py-2"><span className="font-mono text-[11px]">{finding.code}</span><div className={`mt-0.5 text-[10px] ${finding.severity === "error" ? "text-danger" : finding.severity === "warning" ? "text-warning" : "text-text-faint"}`}>{finding.severity}</div></td>
              <td className="px-3 py-2"><span className={`rounded px-1.5 py-0.5 text-[10px] ${finding.status === "open" ? "bg-primary/10 text-primary" : finding.status === "failed" ? "bg-danger/10 text-danger" : finding.status === "resolved" ? "bg-success/10 text-success" : "bg-surfaceHover text-text-muted"}`}>{t(`curator.status.${finding.status}`)}</span></td>
              <td className="px-3 py-2 font-mono">{finding.attempts}</td>
              <td className="max-w-[260px] px-3 py-2"><div className="truncate text-text-body" title={finding.reason}>{finding.decision || "—"}{finding.reason ? ` · ${finding.reason}` : ""}</div>{finding.last_error ? <div className="mt-0.5 truncate font-mono text-[10px] text-danger" title={finding.last_error}>{finding.last_error}</div> : null}</td>
              <td className="px-3 py-2 font-mono text-[10px] text-text-faint">{new Date(finding.updated_at).toLocaleString()}</td>
              <td className="px-3 py-2"><div className="flex flex-wrap gap-1">
                <button type="button" disabled={!mayWrite || !!busy || finding.status === "running"} onClick={() => void act(finding, "reopen")} title={t("curator.action.reopen")} className="rounded border border-line p-1 text-text-muted hover:text-text-strong disabled:opacity-40"><RotateCcw className="h-3.5 w-3.5" /></button>
                <button type="button" disabled={!mayWrite || !!busy || finding.status === "running"} onClick={() => void act(finding, "ignore")} title={t("curator.action.ignore")} className="rounded border border-line p-1 text-text-muted hover:text-danger disabled:opacity-40"><Ban className="h-3.5 w-3.5" /></button>
              </div></td>
            </tr>)}
          </tbody>
        </table>
        {!findings?.items.length ? <div className="px-3 py-8 text-center text-xs text-text-muted">{loading ? t("curator.loading") : t("curator.empty")}</div> : null}
      </div>
      <Pager page={findings} offset={offset} setOffset={setOffset} />
    </div> : null}

    {tab === "runs" ? <div className="space-y-3">
      <div className="flex items-center gap-2">
        <select value={runKindFilter} onChange={(event) => { setRunKindFilter(event.target.value); setOffset(0); }} className="rounded-lg border border-line bg-surface px-2 py-1.5 text-xs text-text-body">
          {["agent", "quality", "sweep"].map((value) => <option key={value} value={value}>{value}</option>)}
        </select>
        <span className="text-[11px] text-text-faint">{runs?.total ?? 0}</span>
      </div>
      <div className="overflow-x-auto rounded-xl border border-line bg-surface">
        <table className="w-full min-w-[820px] text-left text-xs">
          <thead className="border-b border-line bg-surfaceSubtle text-text-faint">
            <tr>{["curator.col.started", "curator.col.kind", "curator.col.status", "curator.col.decision", "curator.col.credits", "curator.col.result"].map((key) => <th key={key} className="px-3 py-2 font-medium">{t(key)}</th>)}</tr>
          </thead>
          <tbody>
            {runs?.items.map((run) => <tr key={run.id} className="border-b border-line last:border-0">
              <td className="px-3 py-2 font-mono text-[10px] text-text-faint">{new Date(run.started_at).toLocaleString()}</td>
              <td className="px-3 py-2"><span className="font-mono text-[11px]">{run.kind}</span><div className="text-[10px] text-text-faint">{run.trigger}{run.finding_id ? ` · ${run.finding_id.slice(0, 8)}` : ""}</div></td>
              <td className="px-3 py-2"><span className={`rounded px-1.5 py-0.5 text-[10px] ${run.status === "completed" ? "bg-success/10 text-success" : run.status === "failed" ? "bg-danger/10 text-danger" : "bg-surfaceHover text-text-muted"}`}>{run.status}</span></td>
              <td className="max-w-[280px] px-3 py-2"><div className="truncate" title={run.reason}>{run.decision || "—"}{run.reason ? ` · ${run.reason}` : ""}</div>{run.error ? <div className="mt-0.5 truncate font-mono text-[10px] text-danger" title={run.error}>{run.error}</div> : null}</td>
              <td className="px-3 py-2 font-mono">{run.credits}</td>
              <td className="px-3 py-2"><div className="flex flex-wrap gap-2 text-[10px]">
                {run.entity_id ? <Link href={`/catalog/${run.entity_id}`} target="_blank" className="text-primary hover:underline">{t("curator.openEntity")}</Link> : null}
                {run.pull_request_id ? <span className="font-mono text-text-muted" title={run.pull_request_id}>PR {run.pull_request_id.slice(0, 8)} {run.merged ? "✓" : ""}</span> : null}
                {run.auto_merged ? <span className="text-success">{t("curator.autoMerged")}</span> : null}
              </div></td>
            </tr>)}
          </tbody>
        </table>
        {!runs?.items.length ? <div className="px-3 py-8 text-center text-xs text-text-muted">{loading ? t("curator.loading") : t("curator.empty")}</div> : null}
      </div>
      <Pager page={runs} offset={offset} setOffset={setOffset} />
    </div> : null}
  </div>;
}

function Pager({ page, offset, setOffset }: { page: { total: number; limit: number } | null; offset: number; setOffset: (n: number) => void }) {
  const { t } = useI18n();
  if (!page || page.total <= page.limit) return null;
  return <div className="flex items-center justify-end gap-2 text-xs text-text-muted">
    <button type="button" disabled={offset <= 0} onClick={() => setOffset(Math.max(0, offset - page.limit))} className="rounded border border-line px-2 py-1 disabled:opacity-40">{t("curator.prev")}</button>
    <span>{offset + 1}–{Math.min(offset + page.limit, page.total)} / {page.total}</span>
    <button type="button" disabled={offset + page.limit >= page.total} onClick={() => setOffset(offset + page.limit)} className="rounded border border-line px-2 py-1 disabled:opacity-40">{t("curator.next")}</button>
  </div>;
}
