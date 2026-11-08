"use client";

import { useCallback, useEffect, useMemo, useRef, useState } from "react";
import Link from "next/link";
import { CheckCircle2, Circle, Clock, ExternalLink, GitBranch, Loader2, Pause, Play, Plus, Search, Send, Square, Trash2, XCircle } from "lucide-react";
import { useI18n } from "@/i18n/I18nProvider";
import {
  ASSISTANT_REASONING_EFFORTS,
  type AssistantAccount, type AssistantBYOModel, type AssistantBYOProvider, type AssistantMessage,
  type AssistantModel, type AssistantPage, type AssistantReasoningEffort, type Mission, type MissionDetail, type MissionPlannerRun, type MissionTask, type SearchProvider,
} from "@/lib/assistantApi";
import { describeAssistantError } from "@/lib/assistantUser";
import MarkdownRenderer from "@/components/MarkdownRenderer";
import { Select } from "@/components/ui/Select";
import { ConfirmDialog } from "@/components/oauth/ConfirmDialog";
import { AssistantModelPicker } from "./AssistantModelPicker";
import { AssistantSearchPicker } from "./AssistantSearchPicker";

const ctlBtn = "inline-flex items-center gap-1 rounded-control border border-line bg-surface px-2.5 py-1.5 text-xs hover:bg-surfaceHover disabled:opacity-40";

function prettyJSON(raw: string): string {
  try { return JSON.stringify(JSON.parse(raw), null, 2); } catch { return raw; }
}

export interface MissionWorkspacePanelProps {
  scope: "user" | "curator";
  request: <T>(path: string, options?: RequestInit) => Promise<T>;
  mayWrite?: boolean;
  allowDelete?: boolean;
  canBypassReview?: boolean;
  models?: AssistantModel[];
  byoProviders?: AssistantBYOProvider[];
  byoModels?: AssistantBYOModel[];
  searchProviders?: SearchProvider[];
  defaultModelID?: string;
  defaultWorkerModelID?: string;
  defaultReasoningEffort?: AssistantReasoningEffort;
  defaultWorkerReasoningEffort?: AssistantReasoningEffort;
  defaultBudget?: number;
  onBalance?: (balance: AssistantAccount) => void;
  onChanged?: () => void;
}

export function MissionWorkspacePanel({
  scope,
  request,
  mayWrite = true,
  allowDelete = false,
  canBypassReview = false,
  models = [],
  byoProviders = [],
  byoModels = [],
  searchProviders = [],
  defaultModelID = "",
  defaultWorkerModelID = "",
  defaultReasoningEffort = "",
  defaultWorkerReasoningEffort = "",
  defaultBudget = 120,
  onBalance,
  onChanged,
}: MissionWorkspacePanelProps) {
  const { t, locale } = useI18n();
  const number = useMemo(() => new Intl.NumberFormat(locale), [locale]);

  const [missions, setMissions] = useState<Mission[]>([]);
  const [filterText, setFilterText] = useState("");
  const [selectedID, setSelectedID] = useState("");
  const [detail, setDetail] = useState<MissionDetail | null>(null);
  const [selectedTrace, setSelectedTrace] = useState<{ kind: "task" | "planner"; id: string } | null>(null);
  const [traceMessages, setTraceMessages] = useState<AssistantMessage[]>([]);
  const [traceLoading, setTraceLoading] = useState(false);

  const [goal, setGoal] = useState("");
  const [budget, setBudget] = useState(String(defaultBudget));
  const [maxWorkers, setMaxWorkers] = useState(2);
  const [maxTasks, setMaxTasks] = useState(12);
  const [modelID, setModelID] = useState(defaultModelID);
  const [workerModelID, setWorkerModelID] = useState(defaultWorkerModelID);
  const [reasoningEffort, setReasoningEffort] = useState<AssistantReasoningEffort>(defaultReasoningEffort);
  const [workerReasoningEffort, setWorkerReasoningEffort] = useState<AssistantReasoningEffort>(defaultWorkerReasoningEffort);
  const [searchProviderID, setSearchProviderID] = useState("");
  const [searchBYOProviderID, setSearchBYOProviderID] = useState("");
  const [preferPR, setPreferPR] = useState(true);

  const [steerNote, setSteerNote] = useState("");
  const [steerBudget, setSteerBudget] = useState("");
  const [loading, setLoading] = useState(true);
  const [busy, setBusy] = useState(false);
  const [deleting, setDeleting] = useState<Mission | null>(null);
  const [error, setError] = useState("");
  const pendingCreate = useRef<{ fp: string; id: string } | null>(null);
  const requestRef = useRef(request);
  requestRef.current = request;
  const onBalanceRef = useRef(onBalance);
  onBalanceRef.current = onBalance;
  const onChangedRef = useRef(onChanged);
  onChangedRef.current = onChanged;
  const tRef = useRef(t);
  tRef.current = t;

  useEffect(() => {
    if (!modelID && defaultModelID) setModelID(defaultModelID);
  }, [defaultModelID, modelID]);
  useEffect(() => {
    if (!workerModelID && defaultWorkerModelID) setWorkerModelID(defaultWorkerModelID);
  }, [defaultWorkerModelID, workerModelID]);
  useEffect(() => {
    if (!reasoningEffort && defaultReasoningEffort) setReasoningEffort(defaultReasoningEffort);
  }, [defaultReasoningEffort, reasoningEffort]);
  useEffect(() => {
    if (!workerReasoningEffort && defaultWorkerReasoningEffort) setWorkerReasoningEffort(defaultWorkerReasoningEffort);
  }, [defaultWorkerReasoningEffort, workerReasoningEffort]);

  const loadList = useCallback(async (autoSelectFirst = false) => {
    const page = await requestRef.current<AssistantPage<Mission>>("/missions?limit=50&offset=0");
    const items = Array.isArray(page?.items) ? page.items : [];
    setMissions(items);
    if (autoSelectFirst && items.length > 0) {
      setSelectedID((prev) => prev || items[0].id);
    }
    return items;
  }, []);

  const loadDetail = useCallback(async (id: string) => {
    const next = await requestRef.current<MissionDetail>("/missions/" + id);
    setDetail(next);
    setSteerBudget((prev) => prev || String(next.mission.budget_credits));
    if (next.balance && onBalanceRef.current) onBalanceRef.current(next.balance);
    setMissions((prev) => prev.some((m) => m.id === next.mission.id)
      ? prev.map((m) => (m.id === next.mission.id ? next.mission : m))
      : [next.mission, ...prev]);
    return next;
  }, []);

  useEffect(() => {
    let alive = true;
    setLoading(true);
    loadList(true)
      .catch((cause) => { if (alive) setError(describeAssistantError(cause, tRef.current)); })
      .finally(() => { if (alive) setLoading(false); });
    return () => { alive = false; };
  }, [loadList]);

  useEffect(() => {
    if (!selectedID) {
      setDetail(null);
      setSelectedTrace(null);
      setTraceMessages([]);
      return;
    }
    let alive = true;
    setSteerBudget("");
    setSelectedTrace(null);
    setTraceMessages([]);
    void loadDetail(selectedID).then((next) => {
      if (!alive) return;
      const tasks = Array.isArray(next?.tasks) ? next.tasks : [];
      const runs = Array.isArray(next?.planner_runs) ? next.planner_runs : [];
      const runningTask = tasks.find((item) => item.status === "running");
      const latestTask = tasks[tasks.length - 1];
      const latestPlanner = runs[runs.length - 1];
      const autoTarget = runningTask
        ? { kind: "task" as const, id: runningTask.id }
        : latestTask
          ? { kind: "task" as const, id: latestTask.id }
          : latestPlanner
            ? { kind: "planner" as const, id: latestPlanner.id }
            : null;
      if (autoTarget) {
        setSelectedTrace(autoTarget);
        setTraceLoading(true);
        const subPath = autoTarget.kind === "task" ? `/missions/${selectedID}/tasks/${autoTarget.id}` : `/missions/${selectedID}/planner-runs/${autoTarget.id}`;
        void requestRef.current<{ messages?: AssistantMessage[] }>(subPath).then((res) => {
          if (alive) setTraceMessages(Array.isArray(res?.messages) ? res.messages : []);
        }).catch(() => {}).finally(() => {
          if (alive) setTraceLoading(false);
        });
      }
    }).catch((cause) => { if (alive) setError(describeAssistantError(cause, tRef.current)); });
    return () => { alive = false; };
  }, [selectedID, loadDetail]);

  const anyRunning = detail?.mission.status === "running" || missions.some((m) => m.status === "running");
  useEffect(() => {
    if (!anyRunning) return;
    let alive = true;
    const timer = window.setInterval(() => {
      void loadList(false).catch(() => {});
      if (selectedID) {
        void loadDetail(selectedID).catch(() => {});
      }
      if (selectedID && selectedTrace) {
        const subPath = selectedTrace.kind === "task"
          ? `/missions/${selectedID}/tasks/${selectedTrace.id}`
          : `/missions/${selectedID}/planner-runs/${selectedTrace.id}`;
        void requestRef.current<{ messages?: AssistantMessage[] }>(subPath).then((res) => {
          if (alive) setTraceMessages(Array.isArray(res?.messages) ? res.messages : []);
        }).catch(() => {});
      }
    }, 2500);
    return () => { alive = false; window.clearInterval(timer); };
  }, [anyRunning, selectedID, selectedTrace, loadList, loadDetail]);

  const openTrace = async (kind: "task" | "planner", id: string) => {
    if (!selectedID) return;
    if (selectedTrace?.kind === kind && selectedTrace.id === id) {
      setSelectedTrace(null);
      setTraceMessages([]);
      return;
    }
    setSelectedTrace({ kind, id });
    setTraceLoading(true);
    try {
      const subPath = kind === "task" ? `/missions/${selectedID}/tasks/${id}` : `/missions/${selectedID}/planner-runs/${id}`;
      const res = await requestRef.current<{ messages?: AssistantMessage[] }>(subPath);
      setTraceMessages(Array.isArray(res?.messages) ? res.messages : []);
    } catch (cause) {
      setError(describeAssistantError(cause, t));
    } finally {
      setTraceLoading(false);
    }
  };

  const createMission = async () => {
    const trimmed = goal.trim();
    if (!trimmed || busy || !mayWrite) return;
    const parsedBudget = Math.max(0, Number.parseInt(budget || "0", 10) || 0);
    const payload = scope === "user" ? {
      goal: trimmed,
      budget_credits: parsedBudget,
      max_workers: maxWorkers,
      max_tasks: maxTasks,
      model_id: modelID || undefined,
      worker_model_id: workerModelID || undefined,
      reasoning_effort: reasoningEffort || undefined,
      worker_reasoning_effort: workerReasoningEffort || undefined,
      search_provider_id: searchProviderID || undefined,
      search_byo_provider_id: searchBYOProviderID || undefined,
      prefer_pull_request: canBypassReview ? preferPR : true,
    } : {
      goal: trimmed,
      budget_credits: parsedBudget,
      max_workers: maxWorkers,
      max_tasks: maxTasks,
      model_id: modelID || undefined,
      worker_model_id: workerModelID || undefined,
      reasoning_effort: reasoningEffort || undefined,
      worker_reasoning_effort: workerReasoningEffort || undefined,
    };
    const fp = JSON.stringify(payload);
    if (pendingCreate.current?.fp !== fp) pendingCreate.current = { fp, id: crypto.randomUUID() };
    setBusy(true); setError("");
    try {
      const created = await requestRef.current<MissionDetail>("/missions", {
        method: "POST",
        body: JSON.stringify({ ...payload, client_request_id: pendingCreate.current.id }),
      });
      pendingCreate.current = null;
      setGoal("");
      setDetail(created);
      setSelectedID(created.mission.id);
      setSteerBudget(String(created.mission.budget_credits));
      if (created.balance && onBalanceRef.current) onBalanceRef.current(created.balance);
      setMissions((prev) => [created.mission, ...prev.filter((m) => m.id !== created.mission.id)]);
      onChangedRef.current?.();
    } catch (cause) {
      setError(describeAssistantError(cause, t));
    } finally {
      setBusy(false);
    }
  };

  const updateStatus = async (action: "pause" | "resume" | "stop") => {
    if (!detail || busy || !mayWrite) return;
    setBusy(true); setError("");
    try {
      await requestRef.current<Mission>(`/missions/${detail.mission.id}/${action}`, { method: "POST" });
      await loadDetail(detail.mission.id);
      onChangedRef.current?.();
    } catch (cause) {
      setError(describeAssistantError(cause, t));
    } finally {
      setBusy(false);
    }
  };

  const submitSteer = async () => {
    if (!detail || busy || !mayWrite) return;
    const note = steerNote.trim();
    const nextBudget = steerBudget.trim() !== "" ? Number.parseInt(steerBudget, 10) : undefined;
    const budgetChanged = nextBudget !== undefined && !Number.isNaN(nextBudget) && nextBudget !== detail.mission.budget_credits;
    if (!note && !budgetChanged) return;
    setBusy(true); setError("");
    try {
      await requestRef.current<Mission>(`/missions/${detail.mission.id}/steer`, {
        method: "POST",
        body: JSON.stringify({
          ...(note ? { message: note } : {}),
          ...(budgetChanged ? { budget_credits: nextBudget } : {}),
        }),
      });
      setSteerNote("");
      await loadDetail(detail.mission.id);
      onChangedRef.current?.();
    } catch (cause) {
      setError(describeAssistantError(cause, t));
    } finally {
      setBusy(false);
    }
  };

  const confirmDelete = async () => {
    if (!deleting || busy) return;
    setBusy(true); setError("");
    try {
      await requestRef.current(`/missions/${deleting.id}`, { method: "DELETE" });
      setMissions((prev) => prev.filter((m) => m.id !== deleting.id));
      if (selectedID === deleting.id) {
        setSelectedID("");
        setDetail(null);
      }
      setDeleting(null);
      onChangedRef.current?.();
    } catch (cause) {
      setError(describeAssistantError(cause, t));
    } finally {
      setBusy(false);
    }
  };

  const safeMissions = Array.isArray(missions) ? missions : [];
  const safeModels = Array.isArray(models) ? models : [];
  const safeBYOProviders = Array.isArray(byoProviders) ? byoProviders : [];
  const safeBYOModels = Array.isArray(byoModels) ? byoModels : [];
  const safeSearchProviders = Array.isArray(searchProviders) ? searchProviders : [];
  const safeTasks = Array.isArray(detail?.tasks) ? detail.tasks : [];
  const safePlannerRuns = Array.isArray(detail?.planner_runs) ? detail.planner_runs : [];
  const safeTraceMessages = Array.isArray(traceMessages) ? traceMessages : [];
  const filtered = safeMissions.filter((m) => ((m?.title || "") + " " + (m?.goal || "")).toLocaleLowerCase().includes(filterText.toLocaleLowerCase()));
  const mission = detail?.mission;
  const safePlan = Array.isArray(mission?.plan) ? mission.plan : [];
  const seqByID = useMemo(() => new Map(safeTasks.map((item) => [item.id, item.seq])), [safeTasks]);
  const reasoningOptions = useMemo(() => ASSISTANT_REASONING_EFFORTS.map((v) => ({
    value: v,
    label: t("assistant.reasoning." + (v || "default")),
  })), [t]);
  const workerModelOptions = useMemo(() => [
    { value: "", label: t("curator.settings.sameModel") },
    ...safeModels.map((m) => ({ value: m.id, label: m.name })),
    ...safeBYOModels.filter((m) => m.enabled).map((m) => ({ value: m.id, label: `${m.name} (${t("assistant.user.byo")})` })),
  ], [safeModels, safeBYOModels, t]);
  const toolOutputByCallID = useMemo(() => {
    const map = new Map<string, string>();
    for (const msg of safeTraceMessages) {
      if (msg?.role === "tool" && msg.tool_call_id) map.set(msg.tool_call_id, msg.content || "");
    }
    return map;
  }, [safeTraceMessages]);

  const statusColor = (st: string) =>
    st === "running" ? "text-primary" : st === "completed" ? "text-success" : st === "paused" ? "text-warn" : st === "failed" ? "text-danger" : "text-text-muted";

  return <div className="overflow-hidden rounded-panel border border-line bg-surface">
    {error && <p role="alert" className="border-b border-line px-3 py-2 text-xs text-danger">{error}</p>}
    <div className="grid lg:grid-cols-[15rem_minmax(0,1fr)]">
      <aside className="flex min-w-0 flex-col gap-2.5 border-b border-line-subtle bg-surfaceSubtle p-3 lg:border-b-0 lg:border-r">
        {mayWrite && <button type="button" disabled={busy} onClick={() => { setSelectedID(""); setDetail(null); setError(""); }} className="inline-flex items-center justify-center gap-1.5 rounded-control border border-line bg-surface px-3 py-2 text-xs font-medium hover:bg-surfaceHover disabled:opacity-40"><Plus className="size-3.5" /><span>{t("assistant.mission.new")}</span></button>}
        <label className="flex items-center gap-2 rounded-control border border-line bg-background px-2.5">
          <Search className="size-3.5 shrink-0 text-text-muted" />
          <input type="search" value={filterText} onChange={(e) => setFilterText(e.target.value)} placeholder={t("assistant.mission.search")} aria-label={t("assistant.mission.search")} className="h-8 min-w-0 w-full bg-transparent text-xs outline-none" />
        </label>
        <div className="max-h-44 space-y-1 overflow-y-auto lg:max-h-[60vh]">
          {filtered.map((item) => <div key={item.id} className={"group flex rounded-control " + (selectedID === item.id ? "bg-primary/10" : "hover:bg-surfaceHover")}>
            <button type="button" onClick={() => setSelectedID(item.id)} className="min-w-0 flex-1 p-2 text-left text-xs">
              <span className="flex items-center gap-1.5 font-medium text-text-strong">
                <span className="truncate flex-1">{item.title}</span>
                {item.status === "running" && <Loader2 className="size-3.5 shrink-0 animate-spin text-primary" />}
              </span>
              <span className="mt-1 flex items-center justify-between gap-2 text-[11px] text-text-muted">
                <span className={statusColor(item.status)}>{t(`assistant.mission.status.${item.status}`)}</span>
                <span className="tabular-nums">{number.format(item.spent_credits)} / {number.format(item.budget_credits)}</span>
              </span>
            </button>
            {allowDelete && item.status !== "running" && <button type="button" onClick={() => setDeleting(item)} aria-label={t("assistant.mission.delete")} className="self-start rounded-control p-2 text-text-faint hover:text-danger lg:opacity-0 lg:group-hover:opacity-100"><Trash2 className="size-3.5" /></button>}
          </div>)}
          {!filtered.length && !loading && <p className="px-2 py-4 text-center text-xs text-text-muted">{t("assistant.mission.empty")}</p>}
        </div>
      </aside>

      <div className="flex min-w-0 flex-col lg:min-h-[34rem]">
        {!selectedID ? (
          <form onSubmit={(e) => { e.preventDefault(); void createMission(); }} className="space-y-4 p-4 sm:p-5">
            <div>
              <label htmlFor={`mission-goal-${scope}`} className="mb-1.5 block text-xs font-medium text-text-muted">{t("assistant.mission.goal")}</label>
              <textarea id={`mission-goal-${scope}`} rows={4} maxLength={16000} value={goal} disabled={busy || !mayWrite} onChange={(e) => setGoal(e.target.value)} placeholder={t("assistant.mission.goalPlaceholder")} className="w-full resize-y rounded-control border border-line bg-background px-3 py-2 text-sm outline-none focus:border-primary/50" />
            </div>
            <div className="grid gap-3 sm:grid-cols-3">
              <label className="block text-xs text-text-muted">
                <span className="mb-1 block">{t("assistant.mission.budget")}</span>
                <input type="number" min={0} max={100000000} value={budget} disabled={busy || !mayWrite} onChange={(e) => setBudget(e.target.value)} className="h-8 w-full rounded-control border border-line bg-background px-2.5 text-xs tabular-nums text-text-strong outline-none focus:border-primary/50" />
              </label>
              <label className="block text-xs text-text-muted">
                <span className="mb-1 block">{t("assistant.mission.maxWorkers")}</span>
                <Select aria-label={t("assistant.mission.maxWorkers")} value={String(maxWorkers)} disabled={busy || !mayWrite} onChange={(val) => setMaxWorkers(Number(val))} options={[1, 2, 3, 4].map((n) => ({ value: String(n), label: String(n) }))} className="!h-8 !px-2.5 !text-xs" />
              </label>
              <label className="block text-xs text-text-muted">
                <span className="mb-1 block">{t("assistant.mission.maxTasks")}</span>
                <input type="number" min={1} max={50} value={maxTasks} disabled={busy || !mayWrite} onChange={(e) => setMaxTasks(Math.min(50, Math.max(1, Number(e.target.value) || 1)))} className="h-8 w-full rounded-control border border-line bg-background px-2.5 text-xs tabular-nums text-text-strong outline-none focus:border-primary/50" />
              </label>
            </div>
            {(scope === "user" || safeModels.length > 0 || safeBYOModels.length > 0) && <div className="grid gap-3 sm:grid-cols-2">
              <div className="space-y-1">
                <span className="block text-xs text-text-muted">{t("assistant.mission.plannerModel")}</span>
                <div className="flex items-center gap-1.5">
                  <AssistantModelPicker compact models={safeModels} byoProviders={safeBYOProviders} byoModels={safeBYOModels} modelID={modelID} onModel={setModelID} disabled={busy || !mayWrite} />
                  <div title={t("assistant.mission.plannerReasoning")} className="shrink-0">
                    <Select fullWidth={false} aria-label={t("assistant.mission.plannerReasoning")} value={reasoningEffort} disabled={busy || !mayWrite} onChange={(val) => setReasoningEffort(val as AssistantReasoningEffort)} options={reasoningOptions} className="!h-8 min-w-[5.25rem] !px-2.5 !text-xs" />
                  </div>
                </div>
              </div>
              <div className="space-y-1">
                <span className="block text-xs text-text-muted">{t("assistant.mission.workerModel")}</span>
                <div className="flex items-center gap-1.5">
                  <div className="min-w-0 flex-1">
                    <Select aria-label={t("assistant.mission.workerModel")} value={workerModelID} disabled={busy || !mayWrite} onChange={setWorkerModelID} options={workerModelOptions} className="!h-8 !px-2.5 !text-xs" />
                  </div>
                  <div title={t("assistant.mission.workerReasoning")} className="shrink-0">
                    <Select fullWidth={false} aria-label={t("assistant.mission.workerReasoning")} value={workerReasoningEffort} disabled={busy || !mayWrite} onChange={(val) => setWorkerReasoningEffort(val as AssistantReasoningEffort)} options={reasoningOptions} className="!h-8 min-w-[5.25rem] !px-2.5 !text-xs" />
                  </div>
                </div>
              </div>
            </div>}
            {scope === "user" && <details className="rounded-control border border-line-subtle p-2.5 text-xs text-text-muted">
              <summary className="cursor-pointer">{t("assistant.search.settings")}</summary>
              <div className="pt-2"><AssistantSearchPicker providers={safeSearchProviders} byoProviders={safeBYOProviders} providerID={searchProviderID} byoProviderID={searchBYOProviderID} onProvider={setSearchProviderID} onBYOProvider={setSearchBYOProviderID} disabled={busy} /></div>
            </details>}
            {scope === "user" && canBypassReview && <label className="flex items-center gap-2 text-xs text-text-body">
              <input type="checkbox" checked={preferPR} onChange={(e) => setPreferPR(e.target.checked)} className="rounded border-line" />
              <span>{t("assistant.mission.preferPR")}</span>
            </label>}
            <div className="flex justify-end">
              <button type="submit" disabled={busy || !mayWrite || !goal.trim() || (scope === "user" && !modelID)} className="inline-flex h-8 items-center gap-1.5 rounded-control bg-primary px-3.5 text-xs font-medium text-primary-foreground disabled:opacity-40">
                {busy ? <Loader2 className="size-3.5 animate-spin" /> : <Send className="size-3.5" />}<span>{t("assistant.mission.start")}</span>
              </button>
            </div>
          </form>
        ) : !mission ? (
          <div className="flex flex-1 items-center justify-center p-8 text-xs text-text-muted"><Loader2 className="mr-2 size-4 animate-spin" /><span>{t("common.loading")}</span></div>
        ) : (
          <div className="flex min-h-0 flex-1 flex-col">
            <header className="flex flex-wrap items-center gap-2 border-b border-line-subtle px-4 py-2.5 text-xs">
              <span className="font-medium text-text-strong truncate max-w-md">{mission.title}</span>
              <span className={`font-medium ${statusColor(mission.status)}`}>{t(`assistant.mission.status.${mission.status}`)}</span>
              <span className="text-text-muted tabular-nums">{t("assistant.mission.creditsUsed", { spent: number.format(mission.spent_credits), budget: number.format(mission.budget_credits) })}</span>
              {(mission.reasoning_effort || mission.worker_reasoning_effort) && <span className="text-text-muted">{t("assistant.user.reasoningEffort")}: {t("assistant.reasoning." + (mission.reasoning_effort || "default"))}{mission.worker_reasoning_effort ? " / " + t("assistant.reasoning." + mission.worker_reasoning_effort) : ""}</span>}
              <span className="text-text-muted tabular-nums mr-auto">{t("assistant.mission.rounds", { current: mission.planner_rounds, max: mission.max_planner_rounds })}</span>
              {mayWrite && mission.status === "running" && <button type="button" disabled={busy} onClick={() => void updateStatus("pause")} className={ctlBtn}><Pause className="size-3.5" /><span>{t("curator.pause")}</span></button>}
              {mayWrite && (mission.status === "paused" || mission.status === "failed") && <button type="button" disabled={busy} onClick={() => void updateStatus("resume")} className={ctlBtn}><Play className="size-3.5" /><span>{t("curator.resume")}</span></button>}
              {mayWrite && (mission.status === "running" || mission.status === "paused") && <button type="button" disabled={busy} onClick={() => void updateStatus("stop")} className={`${ctlBtn} text-danger`}><Square className="size-3.5" /><span>{t("assistant.mission.stop")}</span></button>}
            </header>

            <div className="min-h-0 flex-1 space-y-4 overflow-y-auto p-4">
              <div className="rounded-control border border-line-subtle bg-surfaceSubtle p-3 text-xs space-y-1.5">
                <p className="whitespace-pre-wrap break-words text-text-body">{mission.goal}</p>
                {mission.steering && <p className="border-t border-line-subtle pt-1.5 whitespace-pre-wrap break-words text-primary">{t("assistant.mission.steeringLabel")}：{mission.steering}</p>}
                {mission.error && <p className="text-danger">{mission.error}</p>}
              </div>

              {mission.summary && <div className="rounded-control border border-line p-3 text-sm"><MarkdownRenderer content={mission.summary} interactive /></div>}

              {safePlan.length > 0 && <div className="rounded-control border border-line-subtle p-3">
                <div className="mb-2 text-xs font-medium text-text-muted">{t("assistant.mission.plan")}</div>
                <ul className="space-y-1.5 text-xs">
                  {safePlan.map((item) => <li key={item.id} className="flex items-start gap-2">
                    {item.status === "completed" ? <CheckCircle2 className="mt-0.5 size-3.5 shrink-0 text-success" />
                      : item.status === "in_progress" ? <Loader2 className="mt-0.5 size-3.5 shrink-0 animate-spin text-primary" />
                      : item.status === "cancelled" ? <XCircle className="mt-0.5 size-3.5 shrink-0 text-text-faint" />
                      : <Circle className="mt-0.5 size-3.5 shrink-0 text-text-faint" />}
                    <span className={item.status === "cancelled" ? "line-through text-text-faint" : "text-text-body"}>{item.content}</span>
                  </li>)}
                </ul>
              </div>}

              {safePlannerRuns.length > 0 && <div className="space-y-1.5">
                <div className="text-xs font-medium text-text-muted">{t("assistant.mission.plannerRuns")}</div>
                <div className="grid gap-1.5 sm:grid-cols-2">
                  {safePlannerRuns.map((run: MissionPlannerRun) => {
                    const active = selectedTrace?.kind === "planner" && selectedTrace.id === run.id;
                    return <button key={run.id} type="button" onClick={() => void openTrace("planner", run.id)} className={`flex flex-col items-start gap-1 rounded-control border px-3 py-2 text-left text-xs transition-colors ${active ? "border-primary bg-primary/10" : "border-line bg-surface hover:bg-surfaceHover"}`}>
                      <div className="flex w-full items-center justify-between gap-2">
                        <span className={`inline-flex items-center gap-1.5 font-medium ${active ? "text-primary" : "text-text-strong"}`}>
                          {run.status === "running" ? <Loader2 className="size-3 animate-spin text-primary" /> : <Clock className="size-3 text-text-muted" />}
                          <span>Planner #{run.round} · {run.trigger}</span>
                        </span>
                        <span className="text-[11px] text-text-faint tabular-nums">{t("curator.totalCredits", { credits: run.credits })}</span>
                      </div>
                      {run.summary && <p className="line-clamp-2 text-[11px] text-text-body">{run.summary}</p>}
                      {run.error && <p className="line-clamp-1 text-[11px] text-danger">{run.error}</p>}
                    </button>;
                  })}
                </div>
              </div>}

              <div className="space-y-2">
                <div className="text-xs font-medium text-text-muted">{t("assistant.mission.workerTasks")} ({safeTasks.length} / {mission.max_tasks})</div>
                {!safeTasks.length && <p className="text-xs text-text-muted">{mission.status === "running" ? t("assistant.mission.planning") : t("curator.empty")}</p>}
                <div className="grid gap-2 sm:grid-cols-2">
                  {safeTasks.map((task: MissionTask) => {
                    const active = selectedTrace?.kind === "task" && selectedTrace.id === task.id;
                    const deps = Array.isArray(task.depends_on) ? task.depends_on : [];
                    const anchors = Array.isArray(task.anchor_entity_ids) ? task.anchor_entity_ids : [];
                    const srcs = Array.isArray(task.sources) ? task.sources : [];
                    return <div key={task.id} className={`rounded-control border p-2.5 text-xs space-y-1.5 ${active ? "border-primary bg-primary/5" : "border-line bg-surface"}`}>
                      <div className="flex items-center justify-between gap-2">
                        <button type="button" onClick={() => void openTrace("task", task.id)} className="flex min-w-0 flex-1 items-center gap-1.5 text-left font-medium text-text-strong hover:text-primary">
                          <GitBranch className="size-3.5 shrink-0 text-primary" />
                          <span className="font-mono text-[11px] text-text-muted">#{task.seq}</span>
                          <span className="rounded-control bg-surfaceSubtle px-1.5 py-0.5 text-[11px] text-text-muted">{t(`assistant.mission.role.${task.role}`)}</span>
                          <span className="truncate">{task.title}</span>
                        </button>
                        <span className={`shrink-0 ${statusColor(task.status)}`}>{t(`assistant.mission.taskStatus.${task.status}`)}</span>
                      </div>
                      {task.result_summary && <p className="line-clamp-3 whitespace-pre-wrap break-words text-text-body">{task.result_summary}</p>}
                      {task.error && <p className="text-danger">{task.error}</p>}
                      <div className="flex flex-wrap items-center gap-2 pt-0.5 text-[11px] text-text-muted">
                        <button type="button" onClick={() => void openTrace("task", task.id)} className={`font-medium hover:underline ${active ? "text-primary" : "text-text-body"}`}>{t("assistant.mission.viewTrace")}</button>
                        <span className="tabular-nums">{t("curator.totalCredits", { credits: task.credits })}</span>
                        {deps.length > 0 && <span>{t("assistant.mission.dependsOn")}: {deps.map((id) => `#${seqByID.get(id) || "?"}`).join(", ")}</span>}
                        {task.pull_request_id && <Link href={`/pull-requests/${task.pull_request_id}`} className="text-primary hover:underline">{t(task.merged ? "assistant.user.editApplied" : "assistantUI.viewChanges")}</Link>}
                        {task.workspace_id && <Link href={`/workspaces/${task.workspace_id}`} className="text-primary hover:underline">{t("curator.workspace")}</Link>}
                        {anchors.slice(0, 3).map((eid) => <Link key={eid} href={`/catalog/${eid}`} className="font-mono text-primary hover:underline">{eid.slice(0, 8)}</Link>)}
                        {srcs.slice(0, 2).map((src, i) => <a key={i} href={src} target="_blank" rel="noreferrer noopener" className="inline-flex items-center gap-0.5 text-primary hover:underline"><ExternalLink className="size-3" /><span>URL</span></a>)}
                      </div>
                    </div>;
                  })}
                </div>
              </div>

              {selectedTrace && <div className="rounded-control border border-line bg-surfaceSubtle p-3 space-y-2.5">
                <div className="flex items-center justify-between text-xs font-medium text-text-strong">
                  <span>{t("assistant.mission.trace")} · {selectedTrace.kind === "task" ? `#${seqByID.get(selectedTrace.id) || ""} ${safeTasks.find((item) => item.id === selectedTrace.id)?.title || ""}` : `Planner #${safePlannerRuns.find((item) => item.id === selectedTrace.id)?.round || ""}`}</span>
                  <button type="button" onClick={() => { setSelectedTrace(null); setTraceMessages([]); }} className="text-text-muted hover:text-text-strong">×</button>
                </div>
                {traceLoading && <Loader2 className="size-4 animate-spin text-text-muted" />}
                {!traceLoading && safeTraceMessages.filter((m) => m?.role === "user" || m?.role === "assistant").map((m, idx) => <div key={idx} className="space-y-1.5 text-xs">
                  {m.role === "user" ? (
                    <details className="rounded-control border border-line bg-surface p-2"><summary className="cursor-pointer text-text-muted">{t("curator.taskContext")}</summary><pre className="mt-1.5 max-h-48 overflow-auto whitespace-pre-wrap break-words">{m.content}</pre></details>
                  ) : (
                    <div className="space-y-1.5 rounded-control border border-line bg-surface p-2.5">
                      {m.content && <MarkdownRenderer content={m.content} interactive />}
                      {m.tool_calls?.map((call) => {
                        const toolOut = toolOutputByCallID.get(call.id);
                        return <details key={call.id} className="rounded-control border border-line-subtle bg-surfaceSubtle/60 p-2">
                          <summary className="cursor-pointer font-mono text-primary">{call?.function?.name || "tool"}</summary>
                          <div className="mt-1.5 space-y-1.5">
                            <pre className="max-h-40 overflow-auto rounded bg-surface p-2 font-mono whitespace-pre-wrap break-words">{prettyJSON(call?.function?.arguments || "{}")}</pre>
                            {toolOut && <pre className="max-h-52 overflow-auto rounded bg-surface p-2 font-mono text-text-muted whitespace-pre-wrap break-words">{prettyJSON(toolOut)}</pre>}
                          </div>
                        </details>;
                      })}
                    </div>
                  )}
                </div>)}
              </div>}
            </div>

            {mayWrite && mission.status !== "stopped" && <footer className="border-t border-line-subtle bg-surface px-4 py-2.5">
              <form onSubmit={(e) => { e.preventDefault(); void submitSteer(); }} className="flex flex-wrap items-center gap-2">
                <input type="text" maxLength={8000} value={steerNote} disabled={busy} onChange={(e) => setSteerNote(e.target.value)} placeholder={t("assistant.mission.steerPlaceholder")} aria-label={t("assistant.mission.steerPlaceholder")} className="h-8 min-w-48 flex-1 rounded-control border border-line bg-background px-2.5 text-xs outline-none focus:border-primary/50" />
                <label className="flex items-center gap-1.5 text-xs text-text-muted">
                  <span>{t("assistant.mission.budget")}</span>
                  <input type="number" min={mission.spent_credits} max={100000000} value={steerBudget} disabled={busy} onChange={(e) => setSteerBudget(e.target.value)} className="h-8 w-24 rounded-control border border-line bg-background px-2 text-xs tabular-nums text-text-strong outline-none" />
                </label>
                <button type="submit" disabled={busy || (!steerNote.trim() && (steerBudget.trim() === "" || Number.parseInt(steerBudget, 10) === mission.budget_credits))} className="inline-flex h-8 items-center gap-1 rounded-control bg-primary px-3 text-xs font-medium text-primary-foreground disabled:opacity-40">
                  <Send className="size-3.5" /><span>{t("assistant.mission.steerSubmit")}</span>
                </button>
              </form>
            </footer>}
          </div>
        )}
      </div>
    </div>
    <ConfirmDialog open={!!deleting} title={t("assistant.mission.delete")} message={t("assistant.mission.deleteHint")} confirmLabel={t("assistant.user.delete")} busy={busy} onClose={() => { if (!busy) setDeleting(null); }} onConfirm={() => void confirmDelete()} />
  </div>;
}
