"use client";

import { useCallback, useEffect, useRef, useState } from "react";
import Link from "next/link";
import { Bot, GitBranch, Loader2, Send } from "lucide-react";
import MarkdownRenderer from "@/components/MarkdownRenderer";
import { MissionWorkspacePanel } from "@/components/assistant/MissionWorkspacePanel";
import { useI18n } from "@/i18n/I18nProvider";
import type { AssistantBYOModel, AssistantBYOProvider, AssistantModel, AssistantReasoningEffort, SearchProvider } from "@/lib/assistantApi";
import { curatorRequest, type CuratorFinding, type CuratorPage, type CuratorRun } from "@/lib/curatorApi";

const button = "rounded-control border border-line px-2 py-1.5 text-xs hover:bg-surfaceHover disabled:opacity-40";

function readableAnswer(content: string) {
  try { const value = JSON.parse(content) as { reason?: string }; return typeof value.reason === "string" ? value.reason : content; }
  catch { return content; }
}

function pretty(value: string) { try { return JSON.stringify(JSON.parse(value), null, 2); } catch { return value; } }

export function CuratorWorkspace({
  mayWrite,
  enabled,
  paused,
  models = [],
  byoProviders = [],
  byoModels = [],
  searchProviders = [],
  defaultModelID = "",
  defaultWorkerModelID = "",
  defaultReasoningEffort = "",
  defaultWorkerReasoningEffort = "",
  onQueued,
}: {
  mayWrite: boolean;
  enabled: boolean;
  paused: boolean;
  models?: AssistantModel[];
  byoProviders?: AssistantBYOProvider[];
  byoModels?: AssistantBYOModel[];
  searchProviders?: SearchProvider[];
  defaultModelID?: string;
  defaultWorkerModelID?: string;
  defaultReasoningEffort?: AssistantReasoningEffort;
  defaultWorkerReasoningEffort?: AssistantReasoningEffort;
  onQueued: () => Promise<void>;
}) {
  const { t, locale } = useI18n();
  const [mode, setMode] = useState<"missions" | "sessions">("missions");
  const [page, setPage] = useState<CuratorPage<CuratorRun> | null>(null);
  const [offset, setOffset] = useState(0);
  const [managerID, setManagerID] = useState("");
  const [selectedID, setSelectedID] = useState("");
  const [manager, setManager] = useState<CuratorRun | null>(null);
  const [children, setChildren] = useState<CuratorRun[]>([]);
  const [message, setMessage] = useState("");
  const [queued, setQueued] = useState<CuratorFinding | null>(null);
  const [error, setError] = useState("");
  const [busy, setBusy] = useState(false);
  const [loading, setLoading] = useState(true);
  const endRef = useRef<HTMLDivElement>(null);
  const pending = useRef<{ message: string; id: string } | null>(null);
  const handleMissionChanged = useCallback(() => {
    void onQueued();
  }, [onQueued]);

  const load = useCallback(async () => {
    const next = await curatorRequest<CuratorPage<CuratorRun>>(`/runs?kind=manager&limit=25&offset=${offset}`);
    setPage(next); setManagerID(id => id || next.items[0]?.id || "");
  }, [offset]);

  useEffect(() => {
    let alive = true;
    const refresh = async () => { try { await load(); } catch (cause) { if (alive) setError(cause instanceof Error ? cause.message : t("curator.error")); } finally { if (alive) setLoading(false); } };
    void refresh(); const timer = window.setInterval(() => void refresh(), 10000);
    return () => { alive = false; window.clearInterval(timer); };
  }, [load, t]);

  useEffect(() => {
    if (!managerID) { setManager(null); setChildren([]); return; }
    let alive = true;
    const read = async () => {
      try {
        const [run, agents] = await Promise.all([curatorRequest<CuratorRun>(`/runs/${managerID}`), curatorRequest<CuratorRun[]>(`/runs/${managerID}/children`)]);
        if (alive) { setManager(run); setChildren(agents); }
      } catch (cause) { if (alive) setError(cause instanceof Error ? cause.message : t("curator.error")); }
    };
    setManager(null); setChildren([]); void read();
    const timer = window.setInterval(() => void read(), 4000);
    return () => { alive = false; window.clearInterval(timer); };
  }, [managerID, t]);

  const send = async () => {
    const prompt = message.trim(); if (!prompt || busy || !mayWrite) return;
    if (pending.current?.message !== prompt) pending.current = { message: prompt, id: crypto.randomUUID() };
    setBusy(true); setError("");
    try {
      const finding = await curatorRequest<CuratorFinding>("/tasks", { method: "POST", body: JSON.stringify({ id: pending.current.id, message: prompt }) });
      setQueued(finding); setMessage(""); pending.current = null; await onQueued(); await load();
    } catch (cause) { setError(cause instanceof Error ? cause.message : t("curator.error")); }
    finally { setBusy(false); }
  };
  const run = children.find(child => child.id === selectedID) || manager;
  useEffect(() => { endRef.current?.scrollIntoView({ block: "nearest" }); }, [run?.id, run?.messages?.length]);
  const totalCredits = manager ? manager.credits + children.reduce((sum, child) => sum + child.credits, 0) : 0;
  return <div className="space-y-2.5">
    <div className="flex items-center gap-1.5">
      <button type="button" onClick={() => setMode("missions")} className={`rounded-control px-3 py-1.5 text-xs font-medium ${mode === "missions" ? "bg-primary/10 text-primary" : "text-text-muted hover:bg-surfaceSubtle"}`}>{t("curator.mode.missions")}</button>
      <button type="button" onClick={() => setMode("sessions")} className={`rounded-control px-3 py-1.5 text-xs font-medium ${mode === "sessions" ? "bg-primary/10 text-primary" : "text-text-muted hover:bg-surfaceSubtle"}`}>{t("curator.mode.sessions")}</button>
    </div>
    {mode === "missions" ? (
      <MissionWorkspacePanel scope="curator" request={curatorRequest} mayWrite={mayWrite} models={models} byoProviders={byoProviders} byoModels={byoModels} searchProviders={searchProviders} defaultModelID={defaultModelID} defaultWorkerModelID={defaultWorkerModelID} defaultReasoningEffort={defaultReasoningEffort} defaultWorkerReasoningEffort={defaultWorkerReasoningEffort} defaultBudget={120} onChanged={handleMissionChanged} />
    ) : (
    <div className="overflow-hidden rounded-control border border-line bg-surface">
      {error && <p role="alert" className="border-b border-line px-3 py-2 text-xs text-danger">{error}</p>}
      <div className="grid h-[min(680px,calc(100dvh-260px))] min-h-[420px] grid-rows-[auto_minmax(0,1fr)] lg:h-[min(760px,calc(100dvh-210px))] lg:min-h-[580px] lg:grid-cols-[240px_minmax(0,1fr)] lg:grid-rows-1">
      <aside className="border-b border-line bg-surfaceSubtle lg:border-b-0 lg:border-r">
        <div className="flex items-center justify-between border-b border-line px-3 py-2 text-xs font-medium"><span>{t("curator.sessions")}</span><span className="text-text-muted">{page?.total || 0}</span></div>
        <div className="flex max-h-24 overflow-auto lg:block lg:max-h-[660px]">
          {page?.items.map(item => <button key={item.id} type="button" onClick={() => { setManagerID(item.id); setSelectedID(""); }} className={`block min-w-48 shrink-0 border-b border-line px-3 py-2 text-left text-xs lg:w-full ${item.id === managerID ? "bg-primary/10 text-primary" : "text-text-body hover:bg-surfaceHover"}`}>
            <span className="flex items-center gap-1.5"><Bot className="size-3.5" />{new Date(item.started_at).toLocaleString(locale)}</span>
            <span className="mt-1 block truncate text-[11px] text-text-muted">{item.reason || t(`curator.runStatus.${item.status}`)}</span>
          </button>)}
        </div>
        {page && page.total > 25 && <div className="flex justify-between p-2"><button type="button" className={button} disabled={!offset} onClick={() => { setOffset(Math.max(0, offset - 25)); setManagerID(""); }}>{t("pagination.prev")}</button><button type="button" className={button} disabled={offset + 25 >= page.total} onClick={() => { setOffset(offset + 25); setManagerID(""); }}>{t("pagination.next")}</button></div>}
      </aside>
      <div className="flex min-h-0 min-w-0 flex-col">
        <header className="flex flex-wrap items-center gap-2 border-b border-line px-3 py-2 text-xs">
          <span className={`mr-auto ${paused ? "text-warn" : enabled ? "text-success" : "text-text-muted"}`}>{t(paused ? "curator.status.paused" : enabled ? page?.items.some(item => item.status === "running") ? "curator.runStatus.running" : "curator.waiting" : "curator.status.disabled")}</span>
          {manager && <><span className="text-text-muted">{t("curator.totalCredits", { credits: totalCredits })}</span><button type="button" className={`${button} ${run?.id === manager.id ? "text-primary" : ""}`} onClick={() => setSelectedID("")}>{t("curator.role.manager")}</button></>}
          {children.map(child => <button key={child.id} type="button" onClick={() => setSelectedID(child.id)} className={`${button} inline-flex items-center gap-1 ${selectedID === child.id ? "text-primary" : "text-text-muted"}`}><GitBranch className="size-3" />{t(`curator.role.${child.role}`)} · {t(`curator.runStatus.${child.status}`)}</button>)}
        </header>
        <div className="min-h-0 flex-1 space-y-3 overflow-auto px-4 py-3">
          {loading && <Loader2 className="size-4 animate-spin text-text-muted" />}
          {!loading && !run && <p className="py-16 text-center text-sm text-text-muted">{t("curator.noSessions")}</p>}
          {run?.messages?.filter(item => item.role !== "system").map((item, index) => <article key={index} className="space-y-2 text-sm">
            {item.role === "user" ? <details className="rounded-control border border-line bg-surfaceSubtle p-2 text-xs"><summary className="cursor-pointer text-text-muted">{t("curator.taskContext")}</summary><pre className="mt-2 whitespace-pre-wrap break-words">{item.content}</pre></details>
              : item.role === "tool" ? <details className="rounded-control border border-line-subtle p-2 text-xs"><summary className="cursor-pointer text-text-muted">{t("curator.toolResult")}</summary><pre className="mt-2 max-h-64 overflow-auto whitespace-pre-wrap break-words">{pretty(item.content)}</pre></details>
              : <>{item.content && <MarkdownRenderer content={readableAnswer(item.content)} />}{item.tool_calls?.map(call => <details key={call.id} className="rounded-control border border-line p-2 text-xs"><summary className="cursor-pointer font-mono text-primary">{call.function.name}</summary><pre className="mt-2 whitespace-pre-wrap break-words">{pretty(call.function.arguments)}</pre></details>)}</>}
          </article>)}
          {run?.status === "running" && <p role="status" className="flex items-center gap-2 text-xs text-text-muted"><Loader2 className="size-3.5 animate-spin" />{t("curator.runStatus.running")}</p>}
          {run && run.status !== "running" && <div className="flex flex-wrap gap-3 border-t border-line pt-2 text-xs text-text-muted"><span>{t(`curator.runStatus.${run.status}`)} · {t("curator.totalCredits", { credits: run.credits })}</span>{run.error && <span className="text-danger">{run.error}</span>}{run.pull_request_id && <Link href={`/pull-requests/${run.pull_request_id}`} className="text-primary">{t(run.merged ? "assistant.user.editApplied" : "assistantUI.viewChanges")}</Link>}{run.workspace_id && <Link href={`/workspaces/${run.workspace_id}`} className="text-primary">{t("curator.workspace")}</Link>}</div>}
          {queued && <p role="status" className="border-t border-line pt-2 text-xs text-primary">{t("curator.taskQueued")} · {queued.id.slice(0, 8)}</p>}
          <div ref={endRef} />
        </div>
        {mayWrite && <form className="border-t border-line bg-surface px-3 py-2" onSubmit={event => { event.preventDefault(); void send(); }}>
          <label htmlFor="curator-message" className="sr-only">{t("curator.taskMessage")}</label>
          <textarea id="curator-message" rows={2} maxLength={16000} value={message} disabled={busy} onChange={event => setMessage(event.target.value)} onKeyDown={event => { if (event.key === "Enter" && (event.ctrlKey || event.metaKey)) { event.preventDefault(); void send(); } }} placeholder={t("curator.taskMessage")} className="w-full resize-y rounded-control border border-line bg-background px-3 py-2 text-sm outline-none focus:border-primary" />
          <div className="mt-2 flex items-center justify-end"><button type="submit" disabled={busy || !message.trim()} className={`${button} inline-flex items-center gap-1.5 text-primary`}><Send className="size-3.5" />{t("curator.assignTask")}</button></div>
        </form>}
      </div>
      </div>
    </div>
    )}
  </div>;
}
