"use client";

import { useCallback, useEffect, useMemo, useRef, useState } from "react";
import Link from "next/link";
import { Activity, ArrowUpRight, GitBranch, KeyRound, Loader2, MessageSquare, PanelLeft, Plus, Search, Send, Sparkles, Trash2 } from "lucide-react";
import { useI18n, type TranslateFn } from "@/i18n/I18nProvider";
import { useAuth } from "@/lib/authContext";
import {
  ASSISTANT_REASONING_EFFORTS, assistantRequest,
  type AssistantAccount, type AssistantBYOModel, type AssistantBYOProvider,
  type AssistantConversation, type AssistantMessage, type AssistantModel,
  type AssistantReasoningEffort, type AssistantRequestRecord, type AssistantTokenUsage, type SearchProvider,
} from "@/lib/assistantApi";
import {
  assistantAllPages, buildChatTurns, conversationReceipts, describeAssistantError, pushReceiptKey,
  type AssistantAccountResponse, type AssistantPushReceipt,
} from "@/lib/assistantUser";
import { PageShell } from "@/components/ui/PageShell";
import { Card } from "@/components/ui/Card";
import { Modal } from "@/components/ui/Modal";
import { Select } from "@/components/ui/Select";
import { ConfirmDialog } from "@/components/oauth/ConfirmDialog";
import MarkdownRenderer from "@/components/MarkdownRenderer";
import { GeneratedBlocksView } from "./GeneratedBlocks";
import { AssistantSearchPicker } from "./AssistantSearchPicker";
import { AssistantModelPicker } from "./AssistantModelPicker";
import { AssistantContextMeter } from "./AssistantContextMeter";
import { AssistantCredentials } from "./AssistantCredentials";
import { AssistantUsagePanel } from "./AssistantUsagePanel";
import { MissionWorkspacePanel } from "./MissionWorkspacePanel";

interface ChatResult {
  conversation_id: string; request_id?: string; status?: string; answer: string; usage: AssistantTokenUsage;
  credits_spent: number; balance: AssistantAccount; push_receipts?: AssistantPushReceipt[]; error?: string; usage_estimated?: boolean;
}

function describeRuntimeError(code: string | undefined, t: TranslateFn): string {
  if (code) {
    const key = ({
      assistant_insufficient_credits: "creditsLow",
      assistant_budget_exceeded: "budgetReached",
      assistant_disabled: "unavailable",
      assistant_encryption_unavailable: "unavailable",
      assistant_request_conflict: "requestConflict",
      assistant_context_limit: "contextLimit",
    } as Record<string, string>)[code];
    if (key) return t("assistant.user.error." + key);
  }
  return t("assistant.user.error.partial");
}

export function AssistantChat() {
  const { user } = useAuth();
  return <AssistantWorkspace key={user?.id || "anonymous"} />;
}

function AssistantWorkspace() {
  const { t, locale } = useI18n();
  const { user, loading: authLoading } = useAuth();
  const [conversationSearch, setConversationSearch] = useState("");
  const [showConversations, setShowConversations] = useState(false);
  const [tab, setTab] = useState<"chat" | "missions" | "credentials" | "usage">("chat");
  const [models, setModels] = useState<AssistantModel[]>([]);
  const [byoProviders, setBYOProviders] = useState<AssistantBYOProvider[]>([]);
  const [byoModels, setBYOModels] = useState<AssistantBYOModel[]>([]);
  const [account, setAccount] = useState<AssistantAccountResponse | null>(null);
  const [conversations, setConversations] = useState<AssistantConversation[]>([]);
  const [conversationID, setConversationID] = useState("");
  const [activeRequestID, setActiveRequestID] = useState("");
  const [modelID, setModelID] = useState("");
  const [reasoningEffort, setReasoningEffort] = useState<AssistantReasoningEffort>("");
  const [searchProviders, setSearchProviders] = useState<SearchProvider[]>([]);
  const [searchProviderID, setSearchProviderID] = useState("");
  const [searchBYOProviderID, setSearchBYOProviderID] = useState("");
  const [messages, setMessages] = useState<AssistantMessage[]>([]);
  const [message, setMessage] = useState("");
  const [result, setResult] = useState<ChatResult | null>(null);
  const [loading, setLoading] = useState(false);
  const [submitting, setSubmitting] = useState(false);
  const [deletingBusy, setDeletingBusy] = useState(false);
  const [error, setError] = useState("");
  const [deleting, setDeleting] = useState<AssistantConversation | null>(null);
  const currentUser = useRef(user?.id);
  currentUser.current = user?.id;
  const readSequence = useRef(0);
  const pendingRequest = useRef<{ fingerprint: string; id: string } | null>(null);
  const endRef = useRef<HTMLDivElement>(null);

  const activeBYOModels = useMemo(() => {
    const activeProviderIDs = new Set(byoProviders.filter((item) => item.enabled && item.has_key).map((item) => item.id));
    return byoModels.filter((item) => item.enabled && activeProviderIDs.has(item.provider_id));
  }, [byoProviders, byoModels]);

  const currentRunning = submitting || !!activeRequestID;

  const loadBYO = useCallback(async () => {
    const id = user?.id;
    const [nextProviders, nextModels] = await Promise.all([
      assistantAllPages<AssistantBYOProvider>("/byo/providers"),
      assistantAllPages<AssistantBYOModel>("/byo/models"),
    ]);
    if (currentUser.current !== id) return;
    setBYOProviders(nextProviders);
    setBYOModels(nextModels);
    const enabledProviderIDs = new Set(nextProviders.filter((item) => item.enabled && item.has_key).map((item) => item.id));
    const enabledModels = nextModels.filter((item) => item.enabled && enabledProviderIDs.has(item.provider_id));
    setSearchBYOProviderID((previous) => previous && !enabledProviderIDs.has(previous) ? "" : previous);
    setModelID((previous) => {
      if (previous && (models.some((item) => item.id === previous) || enabledModels.some((item) => item.id === previous))) return previous;
      return models.find((item) => item.id === account?.default_model_id)?.id || models[0]?.id || enabledModels[0]?.id || "";
    });
  }, [user?.id, models, account?.default_model_id]);

  useEffect(() => {
    let alive = true;
    setConversationID(""); setActiveRequestID(""); setMessages([]); setResult(null); setAccount(null); setConversations([]); setError("");
    setSearchProviders([]); setSearchProviderID(""); setSearchBYOProviderID("");
    setModels([]); setBYOProviders([]); setBYOModels([]); setModelID(""); pendingRequest.current = null;
    if (!user) return;
    setLoading(true);
    Promise.allSettled([
      assistantAllPages<AssistantModel>("/models"),
      assistantAllPages<AssistantBYOProvider>("/byo/providers"),
      assistantAllPages<AssistantBYOModel>("/byo/models"),
      assistantRequest<AssistantAccountResponse>("/account"),
      assistantAllPages<AssistantConversation>("/conversations"),
      assistantRequest<SearchProvider[]>("/search/providers"),
    ]).then(([modelResult, providerResult, byoModelResult, accountResult, conversationResult, searchResult]) => {
      if (!alive) return;
      const nextModels = modelResult.status === "fulfilled" ? modelResult.value : [];
      const nextBYOProviders = providerResult.status === "fulfilled" ? providerResult.value : [];
      const nextBYOModels = byoModelResult.status === "fulfilled" ? byoModelResult.value : [];
      const nextAccount = accountResult.status === "fulfilled" ? accountResult.value : null;
      const enabledProviderIDs = new Set(nextBYOProviders.filter((item) => item.enabled && item.has_key).map((item) => item.id));
      const nextActiveBYOModels = nextBYOModels.filter((item) => item.enabled && enabledProviderIDs.has(item.provider_id));
      setModels(nextModels);
      setBYOProviders(nextBYOProviders);
      setBYOModels(nextBYOModels);
      if (searchResult.status === "fulfilled") setSearchProviders(searchResult.value);
      setAccount(nextAccount);
      if (conversationResult.status === "fulfilled") setConversations(conversationResult.value);
      setModelID(nextModels.find((model) => model.id === nextAccount?.default_model_id)?.id || nextModels[0]?.id || nextActiveBYOModels[0]?.id || "");
      const failed = [modelResult, providerResult, byoModelResult, accountResult, conversationResult, searchResult].find((item) => item.status === "rejected");
      if (failed?.status === "rejected") setError(describeAssistantError(failed.reason, t));
    }).catch((cause) => { if (alive) setError(describeAssistantError(cause, t)); })
      .finally(() => { if (alive) setLoading(false); });
    return () => { alive = false; ++readSequence.current; };
  }, [user?.id, t]);

  useEffect(() => {
    const desktop = window.matchMedia("(min-width: 1024px)");
    const closeOnDesktop = () => { if (desktop.matches) setShowConversations(false); };
    desktop.addEventListener("change", closeOnDesktop);
    return () => desktop.removeEventListener("change", closeOnDesktop);
  }, []);

  useEffect(() => { endRef.current?.scrollIntoView({ block: "nearest", behavior: "smooth" }); }, [messages, currentRunning]);

  useEffect(() => {
    if (!user || !conversationID || !activeRequestID) return;
    let cancelled = false;
    let finished = false;
    let inFlight = false;
    const ownerID = user.id;
    const targetConvID = conversationID;
    const targetReqID = activeRequestID;
    const poll = async () => {
      if (cancelled || finished || inFlight) return;
      inFlight = true;
      try {
        const [conv, reqRecord] = await Promise.all([
          assistantRequest<AssistantConversation>("/conversations/" + targetConvID),
          assistantRequest<AssistantRequestRecord>("/requests/" + targetReqID),
        ]);
        if (cancelled || currentUser.current !== ownerID) return;
        setMessages(conv.messages || []);
        if (reqRecord.status !== "pending") {
          finished = true;
          setActiveRequestID("");
          const resp = reqRecord.response && typeof reqRecord.response === "object" ? reqRecord.response as Partial<ChatResult> : undefined;
          if (resp?.usage) {
            setResult({
              conversation_id: targetConvID,
              answer: resp.answer || "",
              usage: resp.usage,
              credits_spent: resp.credits_spent || 0,
              balance: resp.balance as AssistantAccount,
              push_receipts: resp.push_receipts,
              error: resp.error,
              usage_estimated: resp.usage_estimated,
            });
          }
          if (resp?.balance?.user_id === ownerID) {
            setAccount((prev) => prev ? { ...prev, balance: resp.balance! } : prev);
          }
          if (resp?.error || reqRecord.status === "failed") {
            setError(describeRuntimeError(resp?.error, t));
          }
          const [list, finalConv] = await Promise.all([
            assistantAllPages<AssistantConversation>("/conversations"),
            assistantRequest<AssistantConversation>("/conversations/" + targetConvID),
          ]);
          if (currentUser.current === ownerID) setConversations(list);
          if (!cancelled && currentUser.current === ownerID) setMessages(finalConv.messages || []);
        }
      } catch {
        /* Poll retries automatically while the background request remains active. */
      } finally {
        inFlight = false;
      }
    };
    const timer = window.setInterval(() => { void poll(); }, 2000);
    return () => { cancelled = true; window.clearInterval(timer); };
  }, [user, conversationID, activeRequestID, t]);

  const hasOtherRunning = conversations.some((item) => !!item.active_request_id && (tab !== "chat" || item.id !== conversationID));
  useEffect(() => {
    if (!user || !hasOtherRunning) return;
    let cancelled = false;
    const ownerID = user.id;
    const timer = window.setInterval(() => {
      void Promise.all([
        assistantAllPages<AssistantConversation>("/conversations"),
        assistantRequest<AssistantAccountResponse>("/account"),
      ]).then(([list, nextAccount]) => {
        if (!cancelled && currentUser.current === ownerID) {
          setConversations(list);
          setAccount(nextAccount);
        }
      }).catch(() => {});
    }, 4000);
    return () => { cancelled = true; window.clearInterval(timer); };
  }, [user, hasOtherRunning, tab, conversationID]);

  const openConversation = async (id: string) => {
    const sequence = ++readSequence.current;
    setLoading(true); setError(""); setResult(null); setActiveRequestID("");
    try {
      const item = await assistantRequest<AssistantConversation>("/conversations/" + id);
      if (sequence !== readSequence.current) return;
      setConversationID(item.id);
      setMessages(item.messages || []);
      setActiveRequestID(item.active_request_id || "");
      setReasoningEffort(item.reasoning_effort || "");
      if (item.model_id && (models.some((entry) => entry.id === item.model_id) || activeBYOModels.some((entry) => entry.id === item.model_id))) {
        setModelID(item.model_id);
      }
      setConversations((prev) => prev.map((entry) => entry.id === item.id ? { ...entry, active_request_id: item.active_request_id } : entry));
      pendingRequest.current = null;
    } catch (cause) { if (sequence === readSequence.current) setError(describeAssistantError(cause, t)); }
    finally { if (sequence === readSequence.current) setLoading(false); }
  };

  const send = async () => {
    const prompt = message.trim();
    if (!prompt || !modelID || currentRunning || loading) return;
    const ownerID = user?.id;
    const request = {
      message: prompt,
      model_id: modelID,
      reasoning_effort: reasoningEffort,
      search_provider_id: searchProviderID,
      search_byo_provider_id: searchBYOProviderID,
      background: true,
      ...(conversationID ? { conversation_id: conversationID } : {}),
    };
    const fingerprint = JSON.stringify(request);
    if (pendingRequest.current?.fingerprint !== fingerprint) pendingRequest.current = { fingerprint, id: crypto.randomUUID() };
    const reqID = pendingRequest.current.id;
    const startSeq = ++readSequence.current;
    setSubmitting(true); setError(""); setResult(null);
    try {
      const next = await assistantRequest<ChatResult>("/chat", { method: "POST", body: JSON.stringify({ ...request, client_request_id: reqID }) });
      if (currentUser.current !== ownerID || startSeq !== readSequence.current) return;
      pendingRequest.current = null;
      setMessage("");
      setConversationID(next.conversation_id);
      if (next.balance?.user_id === ownerID) setAccount((previous) => previous ? { ...previous, balance: next.balance } : previous);
      if (next.status === "pending") {
        setActiveRequestID(next.request_id || reqID);
        setMessages((previous) => [...previous, { role: "user", content: prompt }]);
        void assistantAllPages<AssistantConversation>("/conversations").then((list) => {
          if (currentUser.current === ownerID) setConversations(list);
        }).catch(() => {});
      } else {
        setActiveRequestID("");
        setResult(next);
        setMessages((previous) => [...previous, { role: "user", content: prompt }, ...(next.answer ? [{ role: "assistant", content: next.answer }] : [])]);
        if (next.error) setError(describeRuntimeError(next.error, t));
        try {
          const [nextConversations, saved] = await Promise.all([
            assistantAllPages<AssistantConversation>("/conversations"),
            assistantRequest<AssistantConversation>("/conversations/" + next.conversation_id),
          ]);
          if (currentUser.current === ownerID && startSeq === readSequence.current) {
            setConversations(nextConversations);
            setMessages(saved.messages || []);
            setActiveRequestID(saved.active_request_id || "");
          }
        } catch { /* Saved conversation will refresh on next open. */ }
      }
    } catch (cause) { if (currentUser.current === ownerID && startSeq === readSequence.current) setError(describeAssistantError(cause, t)); }
    finally { if (currentUser.current === ownerID) setSubmitting(false); }
  };

  const removeConversation = async () => {
    if (!deleting) return;
    setDeletingBusy(true); setError("");
    try {
      await assistantRequest("/conversations/" + deleting.id, { method: "DELETE" });
      setConversations((previous) => previous.filter((item) => item.id !== deleting.id));
      if (conversationID === deleting.id) { setConversationID(""); setActiveRequestID(""); setMessages([]); setResult(null); }
      setDeleting(null);
    } catch (cause) { setError(describeAssistantError(cause, t)); }
    finally { setDeletingBusy(false); }
  };

  const startNewChat = () => {
    ++readSequence.current;
    setConversationID("");
    setActiveRequestID("");
    setReasoningEffort("");
    setMessages([]);
    setResult(null);
    setError("");
    setShowConversations(false);
    pendingRequest.current = null;
  };

  const conversationList = <div className="flex h-full min-h-0 flex-col gap-3">
    <button type="button" disabled={submitting} onClick={startNewChat} className="inline-flex items-center justify-center gap-2 rounded-control border border-line bg-surface px-3 py-2 text-sm font-medium disabled:opacity-40"><Plus className="size-4" />{t("assistant.user.newChat")}</button>
    <label className="flex items-center gap-2 rounded-control border border-line bg-background px-2.5"><Search className="size-3.5 shrink-0 text-text-muted" /><input type="search" aria-label={t("assistantUI.searchChats")} placeholder={t("assistantUI.searchChats")} value={conversationSearch} onChange={(event) => setConversationSearch(event.target.value)} className="h-8 min-w-0 w-full bg-transparent text-xs outline-none" /></label>
    <div className="min-h-0 flex-1 space-y-1 overflow-y-auto lg:max-h-[62vh]">
      {conversations.filter((item) => (item.title || "").toLocaleLowerCase().includes(conversationSearch.toLocaleLowerCase())).map((item) => {
        const itemRunning = !!item.active_request_id || (item.id === conversationID && !!activeRequestID);
        return <div key={item.id} className={"group flex rounded-control " + (conversationID === item.id ? "bg-primary/10" : "hover:bg-surfaceHover")}>
          <button type="button" disabled={submitting} onClick={() => { void openConversation(item.id); setShowConversations(false); }} className="min-w-0 flex-1 p-2.5 text-left">
            <span className="flex items-center gap-1.5 text-sm">
              <span className="line-clamp-2 min-w-0 flex-1">{item.title || t("assistant.user.newChat")}</span>
              {itemRunning && <Loader2 className="size-3.5 shrink-0 animate-spin text-primary" />}
            </span>
            <time className="mt-1 block text-xs text-text-faint">{new Date(item.updated_at).toLocaleDateString(locale)}</time>
          </button>
          <button type="button" disabled={deletingBusy || itemRunning} onClick={() => setDeleting(item)} aria-label={t("assistant.user.deleteChat")} className="self-start rounded-control p-2.5 text-text-faint hover:text-danger disabled:opacity-30 lg:opacity-0 lg:group-hover:opacity-100 lg:group-focus-within:opacity-100"><Trash2 className="size-3.5" /></button>
        </div>;
      })}
      {!conversations.length && !loading && <p className="px-2 py-5 text-center text-xs text-text-muted">{t("assistant.user.noConversations")}</p>}
    </div>
  </div>;

  if (authLoading) return <PageShell title={t("assistant.user.title")}><p role="status" className="text-sm text-text-muted">{t("common.loading")}</p></PageShell>;
  if (!user) return <PageShell title={t("assistant.user.title")} subtitle={t("assistant.user.subtitle")}><Card><p className="text-sm text-text-muted">{t("assistant.user.loginRequired")}</p><Link href="/login?redirect=%2Fassistant" className="mt-3 inline-block text-sm text-primary hover:underline">{t("navbar.signIn")}</Link></Card></PageShell>;
  const number = new Intl.NumberFormat(locale);
  const receipts = [...new Map([...conversationReceipts(messages), ...(result?.push_receipts || [])].map((receipt) => [pushReceiptKey(receipt), receipt])).values()];
  const tabs = [{ id: "chat", icon: MessageSquare }, { id: "missions", icon: GitBranch }, { id: "credentials", icon: KeyRound }, { id: "usage", icon: Activity }] as const;
  const canBypassReview = Boolean(user.permissions?.includes("*") || user.permissions?.includes("catalog.review.bypass"));
  const activeModelName = models.find((item) => item.id === modelID)?.name || byoModels.find((item) => item.id === modelID)?.name || t("assistant.user.noModels");
  const activeModelEffort = reasoningEffort || models.find((item) => item.id === modelID)?.reasoning_effort || byoModels.find((item) => item.id === modelID)?.reasoning_effort || "";
  const lastMessage = messages[messages.length - 1];
  const activeToolName = lastMessage?.role === "assistant" && lastMessage.tool_calls?.length ? lastMessage.tool_calls[lastMessage.tool_calls.length - 1]?.function.name : "";
  const chatTurns = useMemo(() => buildChatTurns(messages), [messages]);
  const reasoningOptions = useMemo(() => ASSISTANT_REASONING_EFFORTS.map((value) => ({
    value,
    label: t("assistant.reasoning." + (value || "default")),
  })), [t]);

  return <PageShell title={t("assistant.user.title")} subtitle={t("assistant.user.subtitle")} icon={<Sparkles className="size-5 text-primary" />} spacing="compact">
    <div className="flex flex-wrap items-center justify-between gap-3 border-b border-line-subtle pb-2">
      <div className="flex flex-wrap gap-1" role="tablist" aria-label={t("assistant.user.title")}>{tabs.map(({ id, icon: Icon }) => <button key={id} type="button" role="tab" aria-selected={tab === id} aria-controls={"assistant-panel-" + id} id={"assistant-tab-" + id} onClick={() => setTab(id)} className={"inline-flex shrink-0 items-center gap-2 whitespace-nowrap rounded-control px-3 py-2 text-sm " + (tab === id ? "bg-primary/10 text-primary" : "text-text-muted hover:bg-surfaceSubtle")}><Icon className="size-4 shrink-0" />{t("assistant.user." + id)}</button>)}</div>
      {account && tab !== "chat" && <span className="text-xs text-text-muted">{t("assistant.user.balance", { credits: number.format(account.balance.available_credits) })}</span>}
    </div>
    {error && <p role="alert" className="rounded-control border border-danger/20 bg-danger/5 p-3 text-sm text-danger">{error}</p>}
    {tab === "chat" && <section id="assistant-panel-chat" role="tabpanel" aria-labelledby="assistant-tab-chat" className="overflow-hidden rounded-panel border border-line bg-surface">
      <button type="button" onClick={() => setShowConversations((value) => !value)} aria-expanded={showConversations} className="flex w-full items-center gap-2 border-b border-line-subtle px-4 py-3 text-sm text-text-muted lg:hidden"><PanelLeft className="size-4" />{t("assistantUI.conversations")}</button>
      <div className="grid lg:grid-cols-[15rem_minmax(0,1fr)]">
        <aside className="hidden min-w-0 flex-col border-r border-line-subtle bg-surfaceSubtle p-3 lg:flex">{conversationList}</aside>
        <div className="flex min-w-0 flex-col lg:h-[calc(100dvh-16rem)] lg:min-h-[34rem]">
          <header className="flex flex-wrap items-center justify-between gap-2 border-b border-line-subtle px-4 py-3">
            <span className="truncate text-sm font-medium text-text-strong">{conversations.find((item) => item.id === conversationID)?.title || t("assistant.user.newChat")}</span>
            <span className="text-xs text-text-muted">{activeModelName}{activeModelEffort ? " · " + t("assistant.reasoning." + activeModelEffort) : ""}</span>
          </header>
          <div role="log" aria-live="polite" aria-busy={currentRunning || loading} className="min-h-[22rem] flex-1 space-y-6 overflow-y-auto px-4 py-6 sm:px-6">
            {loading && <p role="status" className="flex items-center gap-2 text-sm text-text-muted"><Loader2 className="size-4 animate-spin" />{t("common.loading")}</p>}
            {!messages.length && !currentRunning && !loading && <div className="flex h-full flex-col justify-center px-4 py-8 text-center">
              <span className="mx-auto mb-4 grid size-10 place-items-center rounded-control border border-primary/20 bg-primary/10 text-primary"><Sparkles className="size-5" /></span>
              <h2 className="text-xl font-semibold text-text-strong">{t("assistantUI.welcome")}</h2><p className="mt-2 text-sm leading-relaxed text-text-muted">{t("assistant.user.startHint")}</p>
              <div className="mt-6 grid gap-2 sm:grid-cols-3">{["find", "relations", "edit"].map((key) => <button type="button" key={key} onClick={() => { setMessage(t("assistantUI.prompt." + key)); document.getElementById("assistant-message")?.focus(); }} className="rounded-control border border-line px-3 py-3 text-left text-xs leading-relaxed text-text-body hover:border-primary/40 hover:bg-primary/5"><ArrowUpRight className="mb-2 size-4 text-primary" />{t("assistantUI.example." + key)}</button>)}</div>
              {!models.length && !activeBYOModels.length && <p className="mt-5 text-xs text-text-muted">{t("assistantUI.modelsUnavailable")}</p>}
            </div>}
            {chatTurns.map((item, index) => <article key={index} className={item.role === "user" ? "ml-auto max-w-[90%] rounded-control border border-line-subtle bg-surfaceSubtle px-4 py-3" : "min-w-0 space-y-2.5"}>
              <p className="mb-1.5 text-xs font-medium text-text-muted">{t(item.role === "user" ? "assistant.user.you" : "assistant.user.assistant")}</p>
              {item.role === "user" ? (
                <p className="whitespace-pre-wrap break-words text-sm">{item.content}</p>
              ) : (
                <>
                  {item.steps.length > 0 && (
                    <details open={currentRunning && index === chatTurns.length - 1} className="rounded-control border border-line-subtle bg-surfaceSubtle/70 px-3 py-2 text-xs">
                      <summary className="cursor-pointer select-none font-medium text-text-muted hover:text-text-strong">
                        {t("assistantUI.toolSteps", { count: item.steps.length })} · {item.steps.map((s) => s.name).filter((v, i, a) => a.indexOf(v) === i).join(", ")}
                      </summary>
                      <div className="mt-2 space-y-1.5 border-t border-line-subtle pt-2">
                        {item.steps.map((step, sIdx) => (
                          <details key={step.id || sIdx} className="rounded border border-line-subtle bg-surface px-2.5 py-1.5">
                            <summary className="cursor-pointer font-mono text-[11px] text-primary">
                              #{sIdx + 1} {step.name}
                            </summary>
                            <div className="mt-1.5 space-y-1.5 text-[11px]">
                              <pre className="max-h-36 overflow-auto rounded bg-surfaceSubtle p-2 font-mono text-text-body whitespace-pre-wrap break-words">{step.arguments}</pre>
                              {step.output && <pre className="max-h-44 overflow-auto rounded bg-surfaceSubtle p-2 font-mono text-text-muted whitespace-pre-wrap break-words">{step.output}</pre>}
                            </div>
                          </details>
                        ))}
                      </div>
                    </details>
                  )}
                  {item.content && <MarkdownRenderer content={item.content} interactive />}
                  {item.entities.length > 0 && (
                    <GeneratedBlocksView blocks={[{ type: "entities", title: t("assistantUI.relatedEntities"), items: item.entities }]} />
                  )}
                </>
              )}
            </article>)}
            {currentRunning && <p role="status" className="flex items-center gap-2 text-xs text-text-muted"><Loader2 className="size-3.5 animate-spin text-primary" /><span>{t("assistant.user.runningInBackground")}{activeToolName ? " · " + activeToolName : ""}</span></p>}
            {receipts.length > 0 && <details className="rounded-control border border-line-subtle p-3 text-xs"><summary className="cursor-pointer text-primary">{t("assistantUI.changes")}</summary><div className="mt-3 space-y-2">{receipts.map((receipt) => <div key={pushReceiptKey(receipt)} className="flex flex-wrap items-center justify-between gap-2"><span className={receipt.applied ? "text-success" : "text-warn"}>{t(receipt.applied ? "assistant.user.editApplied" : "assistant.user.editPending")}</span><Link className="text-primary hover:underline" href={receipt.pull_request ? "/pull-requests/" + receipt.pull_request.id : receipt.workspace ? "/workspaces/" + receipt.workspace.id : "/commits/" + (receipt.commits[0]?.id || "")}>{t("assistantUI.viewChanges")}</Link></div>)}</div></details>}
            <div ref={endRef} />
          </div>
          <footer className="shrink-0 border-t border-line-subtle bg-surface px-4 pb-3 pt-2 sm:px-6">
            <form onSubmit={(event) => { event.preventDefault(); void send(); }} className="rounded-control border border-line bg-background p-2 focus-within:border-primary/50">
              <label htmlFor="assistant-message" className="sr-only">{t("assistant.user.message")}</label><textarea id="assistant-message" rows={2} maxLength={16000} value={message} disabled={currentRunning || loading} onChange={(event) => setMessage(event.target.value)} onKeyDown={(event) => { if (event.key === "Enter" && (event.ctrlKey || event.metaKey)) { event.preventDefault(); void send(); } }} placeholder={t("assistant.user.placeholder")} className="max-h-48 min-h-16 w-full resize-y bg-transparent px-1 py-1 text-sm outline-none disabled:opacity-50" />
              <div className="flex flex-wrap items-start gap-2 border-t border-line-subtle pt-2"><AssistantModelPicker compact models={models} byoProviders={byoProviders} byoModels={byoModels} modelID={modelID} onModel={setModelID} disabled={currentRunning || loading} /><div className="flex items-center" title={t("assistant.user.reasoningEffort")}><Select fullWidth={false} aria-label={t("assistant.user.reasoningEffort")} value={reasoningEffort} disabled={currentRunning || loading} onChange={(val) => setReasoningEffort(val as AssistantReasoningEffort)} options={reasoningOptions} className="!h-8 min-w-[5.25rem] !px-2.5 !text-xs !bg-surface" /></div><button type="submit" disabled={currentRunning || loading || !modelID || !message.trim()} title="Ctrl / ⌘ + Enter" className="inline-flex h-8 shrink-0 items-center gap-1.5 whitespace-nowrap rounded-control bg-primary px-3 text-xs font-medium text-primary-foreground disabled:opacity-40"><Send className="size-3.5 shrink-0" />{t("assistant.user.send")}</button></div>
              <details className="mt-2 border-t border-line-subtle pt-2 text-xs text-text-muted"><summary className="cursor-pointer">{t("assistant.search.settings")}</summary><div className="pt-2"><AssistantSearchPicker providers={searchProviders} byoProviders={byoProviders} providerID={searchProviderID} byoProviderID={searchBYOProviderID} onProvider={setSearchProviderID} onBYOProvider={setSearchBYOProviderID} disabled={currentRunning || loading} /></div></details>
            </form>
            <div className="mt-2 flex flex-wrap items-start justify-between gap-2"><AssistantContextMeter conversationID={conversationID} modelID={modelID} message={message} revision={messages.length} disabled={currentRunning || loading} />{account && <span className="shrink-0 text-xs text-text-muted">{t("assistant.user.balance", { credits: number.format(account.balance.available_credits) })}</span>}</div>
            <div className="mt-2 flex flex-wrap items-center justify-between gap-2 text-xs text-text-faint"><details><summary className="cursor-pointer">{t("assistantUI.editBehavior")}</summary><p className="mt-1 max-w-lg leading-relaxed">{t("assistant.user.editHint")}</p></details>{result && <span>{t("assistant.user.consumed", { input: number.format(result.usage.input_tokens), output: number.format(result.usage.output_tokens), credits: number.format(result.credits_spent) })}{result.usage_estimated && " · " + t("assistant.user.estimated")}</span>}</div>
          </footer>
        </div>
      </div>
    </section>}
    {tab === "missions" && <section id="assistant-panel-missions" role="tabpanel" aria-labelledby="assistant-tab-missions">
      <MissionWorkspacePanel scope="user" request={assistantRequest} allowDelete canBypassReview={canBypassReview} models={models} byoProviders={byoProviders} byoModels={byoModels} searchProviders={searchProviders} defaultModelID={modelID} defaultReasoningEffort={reasoningEffort} onBalance={(balance) => setAccount((prev) => prev ? { ...prev, balance } : prev)} />
    </section>}
    {tab === "credentials" && <div id="assistant-panel-credentials" role="tabpanel" aria-labelledby="assistant-tab-credentials"><AssistantCredentials providers={byoProviders} models={byoModels} onChanged={loadBYO} /></div>}
    {tab === "usage" && <div id="assistant-panel-usage" role="tabpanel" aria-labelledby="assistant-tab-usage"><AssistantUsagePanel models={models} byoModels={byoModels} /></div>}
    <Modal open={showConversations} onClose={() => setShowConversations(false)} title={t("assistantUI.conversations")} placement="left" maxWidth="max-w-[min(20rem,calc(100vw-3rem))]" initialFocus="input[type=search]">{conversationList}</Modal>
    <ConfirmDialog open={!!deleting} title={t("assistant.user.deleteChat")} message={t("assistant.user.deleteChatHint")} confirmLabel={t("assistant.user.delete")} busy={deletingBusy} onClose={() => { if (!deletingBusy) setDeleting(null); }} onConfirm={() => void removeConversation()} />
  </PageShell>;
}
