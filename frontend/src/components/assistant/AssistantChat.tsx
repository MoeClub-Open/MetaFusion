"use client";

import { useCallback, useEffect, useRef, useState } from "react";
import Link from "next/link";
import { Activity, KeyRound, Loader2, MessageSquare, Plus, Send, Sparkles, Trash2 } from "lucide-react";
import { useI18n } from "@/i18n/I18nProvider";
import { useAuth } from "@/lib/authContext";
import { assistantRequest, type AssistantAccount, type AssistantConversation, type AssistantCredential, type AssistantMessage, type AssistantModel, type AssistantTokenUsage } from "@/lib/assistantApi";
import { assistantAllPages, conversationReceipts, pushReceiptKey, describeAssistantError, type AssistantAccountResponse, type AssistantPushReceipt } from "@/lib/assistantUser";
import { PageShell } from "@/components/ui/PageShell";
import { Card } from "@/components/ui/Card";
import { ConfirmDialog } from "@/components/oauth/ConfirmDialog";
import MarkdownRenderer from "@/components/MarkdownRenderer";
import { AssistantModelPicker } from "./AssistantModelPicker";
import { AssistantCredentials } from "./AssistantCredentials";
import { AssistantUsagePanel } from "./AssistantUsagePanel";

interface ChatResult {
  conversation_id: string; answer: string; usage: AssistantTokenUsage;
  credits_spent: number; balance: AssistantAccount; push_receipts?: AssistantPushReceipt[]; error?: string; usage_estimated?: boolean;
}

export function AssistantChat() {
  const { t, locale } = useI18n();
  const { user, loading: authLoading } = useAuth();
  const [tab, setTab] = useState<"chat" | "credentials" | "usage">("chat");
  const [models, setModels] = useState<AssistantModel[]>([]);
  const [credentials, setCredentials] = useState<AssistantCredential[]>([]);
  const [account, setAccount] = useState<AssistantAccountResponse | null>(null);
  const [conversations, setConversations] = useState<AssistantConversation[]>([]);
  const [conversationID, setConversationID] = useState("");
  const [modelID, setModelID] = useState("");
  const [credentialID, setCredentialID] = useState("");
  const [messages, setMessages] = useState<AssistantMessage[]>([]);
  const [message, setMessage] = useState("");
  const [result, setResult] = useState<ChatResult | null>(null);
  const [loading, setLoading] = useState(false);
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState("");
  const [deleting, setDeleting] = useState<AssistantConversation | null>(null);
  const currentUser = useRef(user?.id);
  currentUser.current = user?.id;
  const readSequence = useRef(0);
  const pendingRequest = useRef<{ fingerprint: string; id: string } | null>(null);
  const endRef = useRef<HTMLDivElement>(null);

  const loadCredentials = useCallback(async () => {
    const id = user?.id;
    const next = await assistantAllPages<AssistantCredential>("/credentials");
    if (currentUser.current === id) {
      setCredentials(next);
      setCredentialID((previous) => previous && !next.some((item) => item.id === previous && item.enabled) ? "" : previous);
    }
  }, [user?.id]);

  useEffect(() => {
    let alive = true;
    setConversationID(""); setMessages([]); setResult(null); setAccount(null); setConversations([]); setError("");
    setModels([]); setCredentials([]); setModelID(""); setCredentialID(""); pendingRequest.current = null;
    if (!user) return;
    setLoading(true);
    Promise.all([
      assistantAllPages<AssistantModel>("/models"),
      assistantAllPages<AssistantCredential>("/credentials"),
      assistantRequest<AssistantAccountResponse>("/account"),
      assistantAllPages<AssistantConversation>("/conversations"),
    ]).then(([nextModels, nextCredentials, nextAccount, nextConversations]) => {
      if (!alive) return;
      setModels(nextModels); setCredentials(nextCredentials); setAccount(nextAccount); setConversations(nextConversations);
      setModelID(nextModels.find((model) => model.id === nextAccount.default_model_id)?.id || nextModels[0]?.id || ""); setCredentialID("");
    }).catch((cause) => { if (alive) setError(describeAssistantError(cause, t)); })
      .finally(() => { if (alive) setLoading(false); });
    return () => { alive = false; ++readSequence.current; };
  }, [user?.id, t]);

  useEffect(() => { endRef.current?.scrollIntoView({ block: "nearest", behavior: "smooth" }); }, [messages, busy]);

  const openConversation = async (id: string) => {
    const sequence = ++readSequence.current;
    setLoading(true); setError(""); setResult(null);
    try {
      const item = await assistantRequest<AssistantConversation>(`/conversations/${id}`);
      if (sequence !== readSequence.current) return;
      setConversationID(item.id); setMessages(item.messages || []); setModelID(item.model_id);
      setCredentialID(credentials.some((entry) => entry.id === item.byo_credential_id && entry.enabled) ? item.byo_credential_id || "" : "");
      pendingRequest.current = null;
    } catch (cause) { if (sequence === readSequence.current) setError(describeAssistantError(cause, t)); }
    finally { if (sequence === readSequence.current) setLoading(false); }
  };

  const send = async () => {
    const prompt = message.trim();
    if (!prompt || !modelID || busy) return;
    const ownerID = user?.id;
    const request = { message: prompt, model_id: modelID, credential_id: credentialID, ...(conversationID ? { conversation_id: conversationID } : {}) };
    const fingerprint = JSON.stringify(request);
    if (pendingRequest.current?.fingerprint !== fingerprint) pendingRequest.current = { fingerprint, id: crypto.randomUUID() };
    setBusy(true); setError(""); setResult(null);
    try {
      const next = await assistantRequest<ChatResult>("/chat", { method: "POST", body: JSON.stringify({ ...request, client_request_id: pendingRequest.current.id }) });
      if (currentUser.current !== ownerID) return;
      pendingRequest.current = null;
      setConversationID(next.conversation_id); setMessage(""); setResult(next);
      setMessages((previous) => [...previous, { role: "user", content: prompt }, { role: "assistant", content: next.answer }]);
      if (next.balance.user_id === ownerID) setAccount((previous) => previous ? { ...previous, balance: next.balance } : previous);
      if (next.error) setError(t("assistant.user.error.partial"));
      try {
        const [nextConversations, saved] = await Promise.all([
          assistantAllPages<AssistantConversation>("/conversations"),
          assistantRequest<AssistantConversation>(`/conversations/${next.conversation_id}`),
        ]);
        if (currentUser.current === ownerID) {
          setConversations(nextConversations);
          setMessages(saved.messages || []);
        }
      } catch { /* The answer is saved; sidebar refresh may be retried by reopening the page. */ }
    } catch (cause) { if (currentUser.current === ownerID) setError(describeAssistantError(cause, t)); }
    finally { setBusy(false); }
  };

  const removeConversation = async () => {
    if (!deleting) return;
    setBusy(true); setError("");
    try {
      await assistantRequest(`/conversations/${deleting.id}`, { method: "DELETE" });
      setConversations((previous) => previous.filter((item) => item.id !== deleting.id));
      if (conversationID === deleting.id) { setConversationID(""); setMessages([]); setResult(null); }
      setDeleting(null);
    } catch (cause) { setError(describeAssistantError(cause, t)); }
    finally { setBusy(false); }
  };

  if (authLoading) return <PageShell title={t("assistant.user.title")}><p role="status" className="text-sm text-text-muted">{t("common.loading")}</p></PageShell>;
  if (!user) return <PageShell title={t("assistant.user.title")} subtitle={t("assistant.user.subtitle")}><Card><p className="text-sm text-text-muted">{t("assistant.user.loginRequired")}</p><Link href="/login?redirect=%2Fassistant" className="inline-block mt-3 text-sm text-primary hover:underline">{t("navbar.signIn")}</Link></Card></PageShell>;
  const number = new Intl.NumberFormat(locale);
  const receipts = [...new Map([...conversationReceipts(messages), ...(result?.push_receipts || [])].map((receipt) => [pushReceiptKey(receipt), receipt])).values()];
  const tabs = [{ id: "chat", icon: MessageSquare }, { id: "credentials", icon: KeyRound }, { id: "usage", icon: Activity }] as const;
  return <PageShell title={t("assistant.user.title")} subtitle={t("assistant.user.subtitle")} icon={<Sparkles className="h-5 w-5 text-primary mt-0.5" />}>
    <Card className="flex flex-wrap items-center justify-between gap-3">
      <div className="flex flex-wrap gap-1" role="tablist" aria-label={t("assistant.user.title")}>{tabs.map(({ id, icon: Icon }) => <button type="button" key={id} role="tab" aria-selected={tab === id} aria-controls={`assistant-panel-${id}`} id={`assistant-tab-${id}`} disabled={busy} onClick={() => setTab(id)} className={`rounded-control px-3 py-2 text-xs flex items-center gap-1.5 ${tab === id ? "bg-primary/10 text-primary" : "text-text-muted hover:bg-surfaceHover"}`}><Icon className="w-3.5 h-3.5" />{t(`assistant.user.${id}`)}</button>)}</div>
      {account && <div className="text-right"><p className="text-xs text-text-body">{t("assistant.user.balance", { credits: number.format(account.balance.available_credits) })}</p><p className="mt-1 text-[10px] text-text-muted">{t("assistant.user.rewardHint", { registration: number.format(account.registration_credits), edit: number.format(account.accepted_edit_credits) })}</p></div>}
    </Card>
    {error && <p role="alert" className="text-sm text-danger">{error}</p>}
    {tab === "chat" && <div id="assistant-panel-chat" role="tabpanel" aria-labelledby="assistant-tab-chat" className="grid lg:grid-cols-[230px_minmax(0,1fr)] gap-4">
      <Card className="space-y-3 self-start">
        <button type="button" disabled={busy || loading} onClick={() => { ++readSequence.current; setConversationID(""); setMessages([]); setResult(null); setError(""); pendingRequest.current = null; }} className="w-full inline-flex items-center justify-center gap-1.5 rounded-control border border-line px-3 py-2 text-xs hover:bg-surfaceHover disabled:opacity-40"><Plus className="w-3.5 h-3.5" />{t("assistant.user.newChat")}</button>
        <div className="max-h-64 lg:max-h-[60vh] overflow-y-auto space-y-1">{conversations.map((item) => <div key={item.id} className={`group flex items-center rounded-control ${conversationID === item.id ? "bg-primary/10" : "hover:bg-surfaceHover"}`}>
          <button type="button" disabled={busy || loading} onClick={() => void openConversation(item.id)} className="flex-1 min-w-0 p-2 text-left text-xs"><span className="block truncate">{item.title || t("assistant.user.newChat")}</span><time className="block mt-1 text-[10px] text-text-faint">{new Date(item.updated_at).toLocaleDateString(locale)}</time></button>
          <button type="button" disabled={busy || loading} onClick={() => setDeleting(item)} aria-label={t("assistant.user.deleteChat")} className="p-2 text-text-faint hover:text-danger"><Trash2 className="w-3.5 h-3.5" /></button>
        </div>)}{!conversations.length && !loading && <p className="text-xs text-text-muted p-2">{t("assistant.user.noConversations")}</p>}</div>
      </Card>
      <Card className="min-w-0 space-y-4">
        <AssistantModelPicker models={models} credentials={credentials} modelID={modelID} credentialID={credentialID} onModel={setModelID} onCredential={setCredentialID} disabled={busy || loading} />
        <div role="log" aria-live="polite" aria-busy={busy || loading} className="min-h-48 max-h-[65vh] overflow-y-auto space-y-4 border-y border-line-subtle py-4">
          {loading && <p role="status" className="text-xs text-text-muted">{t("common.loading")}</p>}
          {!messages.length && !loading && <div className="py-8 text-center space-y-2"><Sparkles className="w-6 h-6 text-primary mx-auto" /><p className="text-sm text-text-body">{t("assistant.user.startHint")}</p></div>}
          {messages.filter((item) => (item.role === "user" || item.role === "assistant") && item.content).map((item, index) => <div key={index} className={`space-y-1.5 ${item.role === "user" ? "ml-4 sm:ml-12 rounded-card bg-surfaceSubtle p-3" : "mr-2"}`}>
            <p className="text-[10px] font-medium text-text-muted">{t(item.role === "user" ? "assistant.user.you" : "assistant.user.assistant")}</p>
            {item.role === "user" ? <p className="text-sm whitespace-pre-wrap break-words">{item.content}</p> : <MarkdownRenderer content={item.content} />}
          </div>)}
          {busy && <p role="status" className="flex items-center gap-2 text-xs text-text-muted"><Loader2 className="w-3.5 h-3.5 animate-spin" />{t("assistant.user.thinking")}</p>}
          <div ref={endRef} />
        </div>
        {(result || receipts.length > 0) && <div className="text-[11px] text-text-muted space-y-2">{result && <p>{t("assistant.user.consumed", { input: number.format(result.usage.input_tokens), output: number.format(result.usage.output_tokens), credits: number.format(result.credits_spent) })}{result.usage_estimated && <span className="ml-2 text-warn">{t("assistant.user.estimated")}</span>}</p>}
          {receipts.map((receipt) => <div key={pushReceiptKey(receipt)} className="rounded-control border border-line-subtle p-2"><p className={receipt.applied ? "text-success" : "text-warn"}>{t(receipt.applied ? "assistant.user.editApplied" : "assistant.user.editPending")}</p><Link href={receipt.pull_request ? `/pull-requests/${receipt.pull_request.id}` : receipt.workspace ? `/workspaces/${receipt.workspace.id}` : `/commits/${receipt.commits[0]?.id || ""}`} className="font-mono block mt-1 text-[10px] text-primary break-all hover:underline">{receipt.pull_request?.id || receipt.workspace?.id || receipt.commits[0]?.id}</Link><div className="flex flex-wrap gap-2 mt-1">{[...new Set(receipt.commits.flatMap((commit) => commit.changes.filter((item) => item.target === "entity").map((item) => item.id)))].map((id) => <Link key={id} href={`/catalog/${id}#revisions`} className="text-primary hover:underline">{t("assistant.user.recordHistory")}</Link>)}</div></div>)}
        </div>}
        <form onSubmit={(event) => { event.preventDefault(); void send(); }} className="space-y-2">
          <label htmlFor="assistant-message" className="sr-only">{t("assistant.user.message")}</label>
          <textarea id="assistant-message" rows={3} maxLength={16000} value={message} disabled={busy || loading} onChange={(event) => setMessage(event.target.value)} placeholder={t("assistant.user.placeholder")} className="w-full resize-y rounded-control border border-line bg-background p-3 text-sm focus:outline-none focus:ring-2 focus:ring-primary/30 disabled:opacity-50" />
          <div className="flex items-center justify-between gap-3"><p className="text-[11px] text-text-muted leading-relaxed">{t("assistant.user.editHint")}</p><button type="submit" disabled={busy || loading || !modelID || !message.trim()} className="shrink-0 inline-flex items-center gap-1.5 rounded-control bg-primary text-white px-4 py-2 text-xs font-semibold disabled:opacity-40"><Send className="w-3.5 h-3.5" />{t("assistant.user.send")}</button></div>
        </form>
      </Card>
    </div>}
    {tab === "credentials" && <div id="assistant-panel-credentials" role="tabpanel" aria-labelledby="assistant-tab-credentials"><AssistantCredentials items={credentials} onChanged={loadCredentials} /></div>}
    {tab === "usage" && <div id="assistant-panel-usage" role="tabpanel" aria-labelledby="assistant-tab-usage"><AssistantUsagePanel models={models} /></div>}
    <ConfirmDialog open={!!deleting} title={t("assistant.user.deleteChat")} message={t("assistant.user.deleteChatHint")} confirmLabel={t("assistant.user.delete")} busy={busy} onClose={() => { if (!busy) setDeleting(null); }} onConfirm={() => void removeConversation()} />
  </PageShell>;
}
