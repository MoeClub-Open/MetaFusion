"use client";

import { useCallback, useEffect, useRef, useState } from "react";
import Link from "next/link";
import { Activity, KeyRound, Loader2, MessageSquare, Plus, Send, Sparkles, Trash2, Search, PanelLeft, ArrowUpRight } from "lucide-react";
import { useI18n } from "@/i18n/I18nProvider";
import { useAuth } from "@/lib/authContext";
import { assistantRequest, type AssistantAccount, type AssistantConversation, type AssistantCredential, type AssistantMessage, type AssistantModel, type AssistantTokenUsage } from "@/lib/assistantApi";
import { assistantAllPages, conversationReceipts, pushReceiptKey, describeAssistantError, type AssistantAccountResponse, type AssistantPushReceipt } from "@/lib/assistantUser";
import { PageShell } from "@/components/ui/PageShell";
import { Card } from "@/components/ui/Card";
import { Modal } from "@/components/ui/Modal";
import { ConfirmDialog } from "@/components/oauth/ConfirmDialog";
import MarkdownRenderer from "@/components/MarkdownRenderer";
import { AssistantModelPicker } from "./AssistantModelPicker";
import { AssistantContextMeter } from "./AssistantContextMeter";
import { AssistantCredentials } from "./AssistantCredentials";
import { AssistantUsagePanel } from "./AssistantUsagePanel";

interface ChatResult {
  conversation_id: string; answer: string; usage: AssistantTokenUsage;
  credits_spent: number; balance: AssistantAccount; push_receipts?: AssistantPushReceipt[]; error?: string; usage_estimated?: boolean;
}

export function AssistantChat() {
 const {user}=useAuth();
 return <AssistantWorkspace key={user?.id||"anonymous"}/>;
}

function AssistantWorkspace() {
  const { t, locale } = useI18n();
  const { user, loading: authLoading } = useAuth();
  const [conversationSearch,setConversationSearch]=useState("");
  const [showConversations,setShowConversations]=useState(false);
  const [pendingPrompt,setPendingPrompt]=useState("");
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
    Promise.allSettled([
      assistantAllPages<AssistantModel>("/models"),
      assistantAllPages<AssistantCredential>("/credentials"),
      assistantRequest<AssistantAccountResponse>("/account"),
      assistantAllPages<AssistantConversation>("/conversations"),
    ]).then(([modelResult, credentialResult, accountResult, conversationResult]) => {
      if (!alive) return;
      const nextModels = modelResult.status === "fulfilled" ? modelResult.value : [];
      const nextAccount = accountResult.status === "fulfilled" ? accountResult.value : null;
      setModels(nextModels);
      if (credentialResult.status === "fulfilled") setCredentials(credentialResult.value);
      setAccount(nextAccount);
      if (conversationResult.status === "fulfilled") setConversations(conversationResult.value);
      setModelID(nextModels.find((model) => model.id === nextAccount?.default_model_id)?.id || nextModels[0]?.id || ""); setCredentialID("");
      const failed = [modelResult, credentialResult, accountResult, conversationResult].find(item => item.status === "rejected");
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
    setBusy(true); setPendingPrompt(prompt); setError(""); setResult(null);
    try {
      const next = await assistantRequest<ChatResult>("/chat", { method: "POST", body: JSON.stringify({ ...request, client_request_id: pendingRequest.current.id }) });
      if (currentUser.current !== ownerID) return;
      pendingRequest.current = null;
      setConversationID(next.conversation_id); setMessage(""); setResult(next);
      setMessages((previous) => [...previous, { role: "user", content: prompt }, { role: "assistant", content: next.answer }]);
      setPendingPrompt("");
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
    finally { setBusy(false);setPendingPrompt(""); }
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

  const conversationList = <div className="flex h-full min-h-0 flex-col gap-3">
          <button type="button" disabled={busy||loading} onClick={()=>{++readSequence.current;setConversationID("");setMessages([]);setResult(null);setError("");setShowConversations(false);pendingRequest.current=null;}} className="inline-flex items-center justify-center gap-2 rounded-control border border-line bg-surface px-3 py-2 text-sm font-medium disabled:opacity-40"><Plus className="size-4"/>{t("assistant.user.newChat")}</button>
          <label className="flex items-center gap-2 rounded-control border border-line bg-background px-2.5"><Search className="size-3.5 shrink-0 text-text-muted"/><input type="search" aria-label={t("assistantUI.searchChats")} placeholder={t("assistantUI.searchChats")} value={conversationSearch} onChange={event=>setConversationSearch(event.target.value)} className="h-8 min-w-0 w-full bg-transparent text-xs outline-none"/></label>
          <div className="min-h-0 flex-1 space-y-1 overflow-y-auto lg:max-h-[62vh]">
            {conversations.filter(item=>(item.title||"").toLocaleLowerCase().includes(conversationSearch.toLocaleLowerCase())).map(item=><div key={item.id} className={`group flex rounded-control ${conversationID===item.id?"bg-primary/10":"hover:bg-surfaceHover"}`}>
              <button type="button" disabled={busy||loading} onClick={()=>{void openConversation(item.id);setShowConversations(false);}} className="min-w-0 flex-1 p-2.5 text-left"><span className="line-clamp-2 text-sm">{item.title||t("assistant.user.newChat")}</span><time className="mt-1 block text-xs text-text-faint">{new Date(item.updated_at).toLocaleDateString(locale)}</time></button>
              <button type="button" disabled={busy||loading} onClick={()=>setDeleting(item)} aria-label={t("assistant.user.deleteChat")} className="self-start rounded-control p-2.5 text-text-faint hover:text-danger lg:opacity-0 lg:group-hover:opacity-100 lg:group-focus-within:opacity-100"><Trash2 className="size-3.5"/></button>
            </div>)}
            {!conversations.length&&!loading&&<p className="px-2 py-5 text-center text-xs text-text-muted">{t("assistant.user.noConversations")}</p>}
          </div>
  </div>;

  if (authLoading) return <PageShell title={t("assistant.user.title")}><p role="status" className="text-sm text-text-muted">{t("common.loading")}</p></PageShell>;
  if (!user) return <PageShell title={t("assistant.user.title")} subtitle={t("assistant.user.subtitle")}><Card><p className="text-sm text-text-muted">{t("assistant.user.loginRequired")}</p><Link href="/login?redirect=%2Fassistant" className="inline-block mt-3 text-sm text-primary hover:underline">{t("navbar.signIn")}</Link></Card></PageShell>;
  const number = new Intl.NumberFormat(locale);
  const receipts = [...new Map([...conversationReceipts(messages), ...(result?.push_receipts || [])].map((receipt) => [pushReceiptKey(receipt), receipt])).values()];
  const tabs = [{ id: "chat", icon: MessageSquare }, { id: "credentials", icon: KeyRound }, { id: "usage", icon: Activity }] as const;
  return <PageShell title={t("assistant.user.title")} subtitle={t("assistant.user.subtitle")} icon={<Sparkles className="size-5 text-primary" />} spacing="compact">
    <div className="flex flex-wrap items-center justify-between gap-3 border-b border-line-subtle pb-2">
      <div className="flex gap-1" role="tablist" aria-label={t("assistant.user.title")}>{tabs.map(({id,icon:Icon})=><button key={id} type="button" role="tab" aria-selected={tab===id} aria-controls={`assistant-panel-${id}`} id={`assistant-tab-${id}`} disabled={busy} onClick={()=>setTab(id)} className={`inline-flex items-center gap-2 rounded-control px-3 py-2 text-sm ${tab===id?"bg-primary/10 text-primary":"text-text-muted hover:bg-surfaceSubtle"}`}><Icon className="size-4"/>{t(`assistant.user.${id}`)}</button>)}</div>
      {account&&tab!=="chat"&&<span className="text-xs text-text-muted">{t("assistant.user.balance",{credits:number.format(account.balance.available_credits)})}</span>}
    </div>
    {error&&<p role="alert" className="rounded-control border border-danger/20 bg-danger/5 p-3 text-sm text-danger">{error}</p>}
    {tab==="chat"&&<section id="assistant-panel-chat" role="tabpanel" aria-labelledby="assistant-tab-chat" className="overflow-hidden rounded-panel border border-line bg-surface">
      <button type="button" onClick={()=>setShowConversations(value=>!value)} aria-expanded={showConversations} className="flex w-full items-center gap-2 border-b border-line-subtle px-4 py-3 text-sm text-text-muted lg:hidden"><PanelLeft className="size-4"/>{t("assistantUI.conversations")}</button>
      <div className="grid lg:grid-cols-[15rem_minmax(0,1fr)]">
        <aside className="hidden min-w-0 flex-col border-r border-line-subtle bg-surfaceSubtle p-3 lg:flex">{conversationList}</aside>
        <div className="flex min-w-0 flex-col lg:h-[calc(100dvh-16rem)] lg:min-h-[34rem]">
          <header className="flex flex-wrap items-center justify-between gap-2 border-b border-line-subtle px-4 py-3">
            <span className="truncate text-sm font-medium text-text-strong">{conversations.find(item=>item.id===conversationID)?.title||t("assistant.user.newChat")}</span>
            <span className="text-xs text-text-muted">{models.find(item=>item.id===modelID)?.name||t("assistant.user.noModels")}</span>
          </header>
          <div role="log" aria-live="polite" aria-busy={!!pendingPrompt||loading} className="min-h-[22rem] flex-1 space-y-6 overflow-y-auto px-4 py-6 sm:px-6">
            {loading&&<p role="status" className="flex items-center gap-2 text-sm text-text-muted"><Loader2 className="size-4 animate-spin"/>{t("common.loading")}</p>}
            {!messages.length&&!pendingPrompt&&!loading&&<div className="flex h-full flex-col justify-center px-4 py-8 text-center">
              <span className="mx-auto mb-4 grid size-10 place-items-center rounded-control border border-primary/20 bg-primary/10 text-primary"><Sparkles className="size-5"/></span>
              <h2 className="text-xl font-semibold text-text-strong">{t("assistantUI.welcome")}</h2><p className="mt-2 text-sm leading-relaxed text-text-muted">{t("assistant.user.startHint")}</p>
              <div className="mt-6 grid gap-2 sm:grid-cols-3">{["find","relations","edit"].map(key=><button type="button" key={key} onClick={()=>{setMessage(t(`assistantUI.prompt.${key}`));document.getElementById("assistant-message")?.focus();}} className="rounded-control border border-line px-3 py-3 text-left text-xs leading-relaxed text-text-body hover:border-primary/40 hover:bg-primary/5"><ArrowUpRight className="mb-2 size-4 text-primary"/>{t(`assistantUI.example.${key}`)}</button>)}</div>
              {!models.length&&<p className="mt-5 text-xs text-text-muted">{t("assistantUI.modelsUnavailable")}</p>}
            </div>}
            {messages.filter(item=>(item.role==="user"||item.role==="assistant")&&item.content).map((item,index)=><article key={index} className={item.role==="user"?"ml-auto max-w-[90%] rounded-control border border-line-subtle bg-surfaceSubtle px-4 py-3":"min-w-0 space-y-2"}><p className="mb-2 text-xs font-medium text-text-muted">{t(item.role==="user"?"assistant.user.you":"assistant.user.assistant")}</p>{item.role==="user"?<p className="whitespace-pre-wrap break-words text-sm">{item.content}</p>:<MarkdownRenderer content={item.content}/>}</article>)}
            {pendingPrompt&&<><div className="ml-auto max-w-[90%] rounded-control border border-line-subtle bg-surfaceSubtle px-4 py-3 text-sm whitespace-pre-wrap break-words">{pendingPrompt}</div><p role="status" className="flex items-center gap-2 text-sm text-text-muted"><Loader2 className="size-4 animate-spin"/>{t("assistant.user.thinking")}</p></>}
            {receipts.length>0&&<details className="rounded-control border border-line-subtle p-3 text-xs"><summary className="cursor-pointer text-primary">{t("assistantUI.changes")}</summary><div className="mt-3 space-y-2">{receipts.map(receipt=><div key={pushReceiptKey(receipt)} className="flex flex-wrap items-center justify-between gap-2"><span className={receipt.applied?"text-success":"text-warn"}>{t(receipt.applied?"assistant.user.editApplied":"assistant.user.editPending")}</span><Link className="text-primary hover:underline" href={receipt.pull_request?`/pull-requests/${receipt.pull_request.id}`:receipt.workspace?`/workspaces/${receipt.workspace.id}`:`/commits/${receipt.commits[0]?.id||""}`}>{t("assistantUI.viewChanges")}</Link></div>)}</div></details>}
            <div ref={endRef}/>
          </div>
          <footer className="shrink-0 border-t border-line-subtle bg-surface px-4 pb-3 pt-2 sm:px-6">
            <form onSubmit={event=>{event.preventDefault();void send();}} className="rounded-control border border-line bg-background p-2 focus-within:border-primary/50">
              <label htmlFor="assistant-message" className="sr-only">{t("assistant.user.message")}</label><textarea id="assistant-message" rows={2} maxLength={16000} value={message} disabled={busy||loading} onChange={event=>setMessage(event.target.value)} onKeyDown={event=>{if(event.key==="Enter"&&(event.ctrlKey||event.metaKey)){event.preventDefault();void send();}}} placeholder={t("assistant.user.placeholder")} className="max-h-48 min-h-16 w-full resize-y bg-transparent px-1 py-1 text-sm outline-none disabled:opacity-50"/>
              <div className="flex items-start gap-2 border-t border-line-subtle pt-2"><AssistantModelPicker compact models={models} credentials={credentials} modelID={modelID} credentialID={credentialID} onModel={setModelID} onCredential={setCredentialID} disabled={busy||loading}/><button type="submit" disabled={busy||loading||!modelID||!message.trim()} title="Ctrl / ⌘ + Enter" className="inline-flex h-8 shrink-0 items-center gap-1.5 rounded-control bg-primary px-3 text-xs font-medium text-white disabled:opacity-40"><Send className="size-3.5"/>{t("assistant.user.send")}</button></div>
            </form>
            <div className="mt-2 flex flex-wrap items-start justify-between gap-2"><AssistantContextMeter conversationID={conversationID} modelID={modelID} message={message} revision={messages.length} disabled={busy||loading}/>{account&&<span className="shrink-0 text-xs text-text-muted">{t("assistant.user.balance",{credits:number.format(account.balance.available_credits)})}</span>}</div>
            <div className="mt-2 flex flex-wrap items-center justify-between gap-2 text-xs text-text-faint"><details><summary className="cursor-pointer">{t("assistantUI.editBehavior")}</summary><p className="mt-1 max-w-lg leading-relaxed">{t("assistant.user.editHint")}</p></details>{result&&<span>{t("assistant.user.consumed",{input:number.format(result.usage.input_tokens),output:number.format(result.usage.output_tokens),credits:number.format(result.credits_spent)})}{result.usage_estimated&&` · ${t("assistant.user.estimated")}`}</span>}</div>
          </footer>
        </div>
      </div>
    </section>}
    {tab==="credentials"&&<div id="assistant-panel-credentials" role="tabpanel" aria-labelledby="assistant-tab-credentials"><AssistantCredentials items={credentials} onChanged={loadCredentials}/></div>}
    {tab==="usage"&&<div id="assistant-panel-usage" role="tabpanel" aria-labelledby="assistant-tab-usage"><AssistantUsagePanel models={models}/></div>}
    <Modal open={showConversations} onClose={()=>setShowConversations(false)} title={t("assistantUI.conversations")} placement="left" maxWidth="max-w-[min(20rem,calc(100vw-3rem))]" initialFocus="input[type=search]">{conversationList}</Modal>
    <ConfirmDialog open={!!deleting} title={t("assistant.user.deleteChat")} message={t("assistant.user.deleteChatHint")} confirmLabel={t("assistant.user.delete")} busy={busy} onClose={()=>{if(!busy)setDeleting(null);}} onConfirm={()=>void removeConversation()}/>
  </PageShell>;
}
