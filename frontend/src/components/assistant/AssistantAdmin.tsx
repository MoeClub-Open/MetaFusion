"use client";

import { useCallback, useEffect, useId, useRef, useState, type ReactNode } from "react";
import { Eye, Pencil, Plus, RefreshCw, Trash2 } from "lucide-react";
import { useI18n } from "@/i18n/I18nProvider";
import { useAuth } from "@/lib/authContext";
import { ASSISTANT_MANAGE, can } from "@/lib/permissions";
import { Modal } from "@/components/ui/Modal";
import { ConfirmDialog } from "@/components/oauth/ConfirmDialog";
import {
  ASSISTANT_PROTOCOLS, assistantRequest, type AssistantPage, type AssistantProvider, type AssistantModel,
  type AssistantSettings, type AssistantCredential, type AssistantAccount,
  type AssistantUsage, type AssistantCreditEntry, type AssistantConversation,
  type AssistantBudgetUsage, type AssistantRequestRecord, type GeneratedContent,
} from "@/lib/assistantApi";

const TABS = ["providers", "models", "settings", "budgets", "credentials", "accounts", "ledger", "usage", "conversations", "requests", "generated"] as const;
type Tab = typeof TABS[number];
type Row = AssistantProvider | AssistantModel | AssistantCredential | AssistantAccount | AssistantUsage
  | AssistantCreditEntry | AssistantConversation | AssistantBudgetUsage | AssistantRequestRecord | GeneratedContent;
type Editor = { kind: "provider" | "model" | "credential" | "settings" | "adjust"; value?: Row | AssistantSettings };
type Draft = Record<string, string | boolean>;
type Column = { field: string; format?: "money" | "date" | "enabled" | "tokens" };
const PAGE_SIZE = 25;
const inputClass = "w-full rounded-lg border border-line bg-surface px-3 py-2 text-sm text-text-body outline-none focus:border-primary";
const buttonClass = "relative inline-flex items-center justify-center gap-1.5 rounded-lg border border-line px-3 py-2 text-xs text-text-body hover:bg-surfaceHover disabled:opacity-50";
const columns: Partial<Record<Tab, Column[]>> = {
  providers: [{ field: "name" }, { field: "protocol" }, { field: "base_url" }, { field: "key_hint" }, { field: "monthly_budget_microusd", format: "money" }, { field: "enabled", format: "enabled" }],
  models: [{ field: "name" }, { field: "provider_id" }, { field: "upstream_model" }, { field: "input_price_microusd_per_million", format: "money" }, { field: "output_price_microusd_per_million", format: "money" }, { field: "input_credits_per_million" }, { field: "output_credits_per_million" }, { field: "enabled", format: "enabled" }],
  budgets: [{ field: "scope" }, { field: "month", format: "date" }, { field: "spent_microusd", format: "money" }, { field: "reserved_microusd", format: "money" }],
  credentials: [{ field: "name" }, { field: "owner_id" }, { field: "protocol" }, { field: "base_url" }, { field: "key_hint" }, { field: "enabled", format: "enabled" }, { field: "updated_at", format: "date" }],
  accounts: [{ field: "user_id" }, { field: "credits" }, { field: "available_credits" }, { field: "reserved_credits" }, { field: "updated_at", format: "date" }],
  ledger: [{ field: "user_id" }, { field: "kind" }, { field: "credits" }, { field: "note" }, { field: "reference_id" }, { field: "created_at", format: "date" }],
  usage: [{ field: "user_id" }, { field: "model_id" }, { field: "purpose" }, { field: "usage", format: "tokens" }, { field: "credits" }, { field: "cost_microusd", format: "money" }, { field: "status" }, { field: "created_at", format: "date" }],
  conversations: [{ field: "title" }, { field: "owner_id" }, { field: "model_id" }, { field: "updated_at", format: "date" }],
  requests: [{ field: "id" }, { field: "owner_id" }, { field: "conversation_id" }, { field: "status" }, { field: "created_at", format: "date" }, { field: "updated_at", format: "date" }],
  generated: [{ field: "entity_id" }, { field: "locale" }, { field: "model_id" }, { field: "owner_id" }, { field: "context_hash" }, { field: "updated_at", format: "date" }],
};

function field(row: Row | AssistantSettings | undefined, key: string): unknown {
  return row ? (row as unknown as Record<string, unknown>)[key] : undefined;
}
function textField(row: Row | AssistantSettings | undefined, key: string): string {
  const value = field(row, key);
  return typeof value === "string" || typeof value === "number" ? String(value) : "";
}

/** Manage LLM configuration and records through the same authenticated HTTP contract. */
export function AssistantAdmin() {
  const { t, locale } = useI18n();
  const { user } = useAuth();
  const [tab, setTab] = useState<Tab>("providers");
  const [page, setPage] = useState<AssistantPage<Row> | null>(null);
  const [offset, setOffset] = useState(0);
  const [filterInput, setFilterInput] = useState("");
  const [filter, setFilter] = useState("");
  const [status, setStatus] = useState("");
  const [loading, setLoading] = useState(true);
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState("");
  const [notice, setNotice] = useState("");
  const [revision, setRevision] = useState(0);
  const [settings, setSettings] = useState<AssistantSettings | null>(null);
  const [providers, setProviders] = useState<AssistantProvider[]>([]);
  const [models, setModels] = useState<AssistantModel[]>([]);
  const [editor, setEditor] = useState<Editor | null>(null);
  const [pendingDelete, setPendingDelete] = useState<Row | null>(null);
  const [pendingCancel, setPendingCancel] = useState<Row | null>(null);
  const [detail, setDetail] = useState<Row | null>(null);
  const [detailLoading, setDetailLoading] = useState(false);
  const requestNumber = useRef(0);
  const mayManage = can(user, ASSISTANT_MANAGE);
  const hasFilter = ["credentials", "ledger", "usage", "conversations", "requests", "generated"].includes(tab);
  const reload = useCallback(() => setRevision((value) => value + 1), []);
  const fail = useCallback((cause: unknown) => setError(cause instanceof Error ? cause.message : t("assistant.admin.failed")), [t]);

  useEffect(() => {
    if (!mayManage) return;
    let alive = true;
    async function options<T>(resource: string): Promise<T[]> {
      const result: T[] = [];
      let next = 0;
      for (;;) {
        const response = await assistantRequest<AssistantPage<T>>(`/admin/${resource}?limit=100&offset=${next}`);
        result.push(...response.items);
        if (result.length >= response.total) return result;
        if (!response.items.length) throw new Error("incomplete_assistant_options");
        next += response.items.length;
      }
    }
    void Promise.all([options<AssistantProvider>("providers"), options<AssistantModel>("models")]).then(([allProviders, allModels]) => {
      if (alive) { setProviders(allProviders); setModels(allModels); }
    }).catch((cause) => { if (alive) fail(cause); });
    return () => { alive = false; };
  }, [revision, mayManage, fail]);

  useEffect(() => {
    if (!mayManage) return;
    const request = ++requestNumber.current;
    setLoading(true); setError(""); setPage(null);
    if (tab === "settings") setSettings(null);
    const params = new URLSearchParams({ limit: String(PAGE_SIZE), offset: String(offset) });
    if (hasFilter && filter) params.set(tab === "generated" ? "entity_id" : "user_id", filter);
    if (tab === "requests" && status) params.set("status", status);
    const promise = tab === "settings"
      ? assistantRequest<AssistantSettings>("/admin/settings").then((value) => { if (request === requestNumber.current) setSettings(value); })
      : assistantRequest<AssistantPage<Row>>(`/admin/${tab}?${params}`).then((value) => { if (request === requestNumber.current) setPage(value); });
    void promise.catch((cause) => { if (request === requestNumber.current) fail(cause); }).finally(() => { if (request === requestNumber.current) setLoading(false); });
    return () => { requestNumber.current += 1; };
  }, [tab, offset, filter, status, revision, mayManage, hasFilter, fail]);

  async function remove() {
    if (!pendingDelete || busy) return;
    const id = textField(pendingDelete, "id");
    setBusy(true); setError("");
    try {
      await assistantRequest<void>(`/admin/${tab}/${encodeURIComponent(id)}`, { method: "DELETE" });
      setPendingDelete(null); setNotice(t("assistant.admin.deleted"));
      if (offset > 0 && page?.items.length === 1) setOffset((value) => Math.max(0, value - PAGE_SIZE));
      else reload();
    } catch (cause) { fail(cause); } finally { setBusy(false); }
  }

  async function inspect(row: Row) {
    setDetail(row);
    if (tab !== "conversations" && tab !== "requests") return;
    setDetailLoading(true);
    try {
      const full = await assistantRequest<AssistantConversation | AssistantRequestRecord>(`/admin/${tab}/${encodeURIComponent(textField(row, "id"))}`);
      setDetail((current) => textField(current ?? undefined, "id") === full.id ? full : current);
    } catch (cause) { fail(cause); } finally { setDetailLoading(false); }
  }

  async function cancelRequest() {
    if (!pendingCancel || busy) return;
    setBusy(true); setError("");
    try {
      await assistantRequest(`/admin/requests/${encodeURIComponent(textField(pendingCancel, "id"))}/cancel`, { method: "POST", body: JSON.stringify({}) });
      setPendingCancel(null); setNotice(t("assistant.admin.requestCancelled")); reload();
    } catch (cause) { fail(cause); } finally { setBusy(false); }
  }

  function renderCell(row: Row, column: Column): ReactNode {
    const value = field(row, column.field);
    if (column.field === "protocol") return t(`assistant.protocol.${value}`);
    if (column.format === "enabled") return <span className={value ? "text-primary" : "text-text-muted"}>{t(value ? "assistant.admin.enabled" : "assistant.admin.disabled")}</span>;
    if (column.format === "money") return new Intl.NumberFormat(locale, { style: "currency", currency: "USD", maximumFractionDigits: 6 }).format(Number(value || 0) / 1_000_000);
    if (column.format === "date") return typeof value === "string" && Number.isFinite(Date.parse(value)) ? new Date(value).toLocaleString(locale) : "—";
    if (column.format === "tokens") {
      const usage = value as AssistantUsage["usage"];
      return usage ? <div className="whitespace-nowrap"><p>{t("assistant.admin.tokenPair", { input: usage.input_tokens, output: usage.output_tokens })}</p><p className="text-text-muted">{t("assistant.admin.cachedTokens", { count: usage.cached_input_tokens })}</p></div> : "—";
    }
    if (column.field === "provider_id") return providers.find((item) => item.id === value)?.name ?? textField(row, column.field);
    if (column.field === "model_id") return models.find((item) => item.id === value)?.name ?? textField(row, column.field);
    if (column.field === "status" && tab === "requests") return t(`assistant.admin.status.${textField(row, "status")}`);
    return typeof value === "number" ? value.toLocaleString(locale) : textField(row, column.field) || "—";
  }

  if (!mayManage) return <p role="alert" className="text-sm text-danger">{t("assistant.admin.forbidden")}</p>;
  return <section className="min-w-0 max-w-full space-y-5">
    <div><h2 className="text-lg font-semibold">{t("assistant.admin.title")}</h2><p className="mt-1 text-sm text-text-muted">{t("assistant.admin.description")}</p></div>
    <nav aria-label={t("assistant.admin.sections")} className="flex gap-1 overflow-x-auto border-b border-line pb-2 sm:flex-wrap sm:overflow-visible">
      {TABS.map((item) => <button key={item} type="button" aria-current={tab === item ? "page" : undefined} onClick={() => { setTab(item); setOffset(0); setFilter(""); setFilterInput(""); setStatus(""); setNotice(""); }} className={`${buttonClass} shrink-0 ${tab === item ? "border-primary bg-primary/10 text-primary" : "border-transparent"}`}>{t(`assistant.admin.tab.${item}`)}</button>)}
    </nav>
    <div className="flex flex-wrap items-end justify-between gap-3">
      {hasFilter ? <form onSubmit={(event) => { event.preventDefault(); setOffset(0); setFilter(filterInput.trim()); }} className="flex flex-wrap items-end gap-2">
        <label className="space-y-1 text-xs text-text-muted"><span>{t(tab === "generated" ? "assistant.admin.field.entity_id" : "assistant.admin.field.user_id")}</span><input value={filterInput} onChange={(event) => setFilterInput(event.target.value)} className={inputClass} /></label>
        {tab === "requests" ? <label className="space-y-1 text-xs text-text-muted"><span>{t("assistant.admin.field.status")}</span><select value={status} onChange={(event) => { setOffset(0); setStatus(event.target.value); }} className={inputClass}><option value="">{t("common.all")}</option>{["pending", "completed", "failed"].map((item) => <option key={item} value={item}>{t(`assistant.admin.status.${item}`)}</option>)}</select></label> : null}
        <button type="submit" className={buttonClass}>{t("common.search")}</button>
      </form> : <p className="max-w-xl text-xs text-text-muted">{t(`assistant.admin.hint.${tab}`)}</p>}
      <div className="flex gap-2">
        <button type="button" onClick={reload} disabled={loading} className={buttonClass}><RefreshCw className={`h-3.5 w-3.5 ${loading ? "animate-spin" : ""}`} />{t("common.refresh")}</button>
        {tab === "providers" || tab === "models" ? <button type="button" onClick={() => setEditor({ kind: tab === "providers" ? "provider" : "model" })} className={`${buttonClass} border-primary text-primary`}><Plus className="h-3.5 w-3.5" />{t("assistant.admin.add")}</button> : null}
        {tab === "accounts" ? <button type="button" onClick={() => setEditor({ kind: "adjust" })} className={buttonClass}>{t("assistant.admin.adjust")}</button> : null}
      </div>
    </div>
    {error ? <div role="alert" className="rounded-lg border border-danger/30 bg-danger/10 p-3 text-sm text-danger">{t("assistant.admin.failed")} <span className="break-all">{error}</span></div> : null}
    {notice ? <p role="status" className="text-sm text-primary">{notice}</p> : null}
    {loading ? <p role="status" className="py-10 text-center text-sm text-text-muted">{t("common.loading")}</p> : tab === "settings" && settings ? <div className="rounded-panel border border-line bg-surface p-5">
      <dl className="grid gap-4 text-sm sm:grid-cols-2 xl:grid-cols-3">
        {(["enabled", "default_model_id", "registration_credits", "accepted_edit_credits", "reward_effective_at", "monthly_budget_microusd", "max_tool_rounds"] as const).map((name) => <div key={name}><dt className="text-xs text-text-muted">{t(`assistant.admin.field.${name}`)}</dt><dd className="mt-1 break-all">{name === "enabled" ? t(settings.enabled ? "assistant.admin.enabled" : "assistant.admin.disabled") : name === "default_model_id" ? models.find((model) => model.id === settings.default_model_id)?.name || "—" : name === "monthly_budget_microusd" ? new Intl.NumberFormat(locale, { style: "currency", currency: "USD" }).format(settings[name] / 1_000_000) : name === "reward_effective_at" ? new Date(settings[name]).toLocaleString(locale) : String(settings[name])}</dd></div>)}
      </dl><button type="button" onClick={() => setEditor({ kind: "settings", value: settings })} className={`${buttonClass} mt-5`}><Pencil className="h-3.5 w-3.5" />{t("common.edit")}</button>
    </div> : page ? <>
      <div className="overflow-x-auto rounded-panel border border-line">
        <table className="w-full text-left text-xs"><thead className="bg-surfaceSubtle text-text-muted"><tr>{columns[tab]?.map((column) => <th key={column.field} scope="col" className="whitespace-nowrap px-3 py-3 font-medium">{t(`assistant.admin.field.${column.field}`)}</th>)}{tab !== "budgets" ? <th scope="col" className="px-3 py-3 font-medium">{t("assistant.admin.actions")}</th> : null}</tr></thead>
          <tbody>{page.items.length ? page.items.map((row, index) => <tr key={textField(row, "id") || textField(row, "user_id") || `${textField(row, "scope")}-${index}`} className="border-t border-line-subtle">
            {columns[tab]?.map((column) => <td key={column.field} className="max-w-64 break-words px-3 py-3 align-top">{renderCell(row, column)}</td>)}
            {tab !== "budgets" ? <td className="px-3 py-2 align-top"><div className="flex items-center gap-1">
              {tab === "providers" || tab === "models" || tab === "credentials" ? <button type="button" className={buttonClass} title={t("common.edit")} onClick={() => setEditor({ kind: tab === "providers" ? "provider" : tab === "models" ? "model" : "credential", value: row })}><Pencil className="h-3.5 w-3.5" /><span className="sr-only">{t("common.edit")}</span></button> : null}
              {tab === "accounts" ? <button type="button" className={buttonClass} onClick={() => setEditor({ kind: "adjust", value: row })}>{t("assistant.admin.adjust")}</button> : null}
              {tab === "requests" && textField(row, "status") === "pending" && Date.now() - Date.parse(textField(row, "created_at")) > 600_000 ? <button type="button" className={`${buttonClass} text-danger`} onClick={() => setPendingCancel(row)}>{t("assistant.admin.cancelRequest")}</button> : null}
              {["providers", "models", "credentials", "conversations", "generated"].includes(tab) ? <button type="button" className={`${buttonClass} text-danger`} title={t("common.delete")} onClick={() => setPendingDelete(row)}><Trash2 className="h-3.5 w-3.5" /><span className="sr-only">{t("common.delete")}</span></button> : null}
              <button type="button" className={buttonClass} title={t("assistant.admin.details")} onClick={() => void inspect(row)}><Eye className="h-3.5 w-3.5" /><span className="sr-only">{t("assistant.admin.details")}</span></button>
            </div></td> : null}
          </tr>) : <tr><td colSpan={(columns[tab]?.length || 1) + 1} className="p-10 text-center text-text-muted">{t("assistant.admin.empty")}</td></tr>}</tbody>
        </table>
      </div>
      <div className="flex flex-wrap items-center justify-between gap-3 text-xs text-text-muted"><span>{t("assistant.admin.pageCount", { start: page.total ? offset + 1 : 0, end: offset + page.items.length, total: page.total })}</span><div className="flex gap-2"><button type="button" disabled={!offset || loading} onClick={() => setOffset((value) => Math.max(0, value - PAGE_SIZE))} className={buttonClass}>{t("assistant.admin.previous")}</button><button type="button" disabled={offset + page.items.length >= page.total || loading} onClick={() => setOffset((value) => value + PAGE_SIZE)} className={buttonClass}>{t("assistant.admin.next")}</button></div></div>
    </> : null}
    {editor ? <AssistantEditor editor={editor} providers={providers} models={models} onClose={() => setEditor(null)} onSaved={() => { setEditor(null); setNotice(t("assistant.admin.saved")); reload(); }} /> : null}
    <ConfirmDialog open={!!pendingDelete} title={t("assistant.admin.deleteTitle")} message={t(`assistant.admin.delete.${tab}`)} confirmLabel={t("common.delete")} busy={busy} onClose={() => { if (!busy) setPendingDelete(null); }} onConfirm={() => void remove()} />
    <ConfirmDialog open={!!pendingCancel} title={t("assistant.admin.cancelRequest")} message={t("assistant.admin.cancelRequestHint")} confirmLabel={t("assistant.admin.cancelRequest")} busy={busy} onClose={() => { if (!busy) setPendingCancel(null); }} onConfirm={() => void cancelRequest()} />
    <Modal open={!!detail} onClose={() => setDetail(null)} title={t("assistant.admin.details")} maxWidth="max-w-3xl">
      {detail ? <RecordDetails row={detail} /> : null}
      {detailLoading ? <p role="status" className="text-xs text-text-muted">{t("common.loading")}</p> : null}
    </Modal>
  </section>;
}

function RecordDetails({ row }: { row: Row }) {
  const { t } = useI18n();
  const messages = field(row, "messages") as AssistantConversation["messages"] | undefined;
  return <div className="space-y-5"><dl className="space-y-3 text-xs">{Object.entries(row).filter(([key]) => !["content", "messages", "dependencies"].includes(key)).map(([key, value]) => <div key={key} className="grid gap-1 border-b border-line-subtle pb-2 sm:grid-cols-[12rem_1fr]"><dt className="text-text-muted">{t(`assistant.admin.field.${key}`)}</dt><dd className="break-all">{typeof value === "object" ? JSON.stringify(value) : typeof value === "boolean" ? t(value ? "assistant.admin.enabled" : "assistant.admin.disabled") : String(value ?? "—")}</dd></div>)}</dl>{messages?.length ? <ol className="space-y-3">{messages.map((message, index) => <li key={index} className="rounded-lg border border-line p-3 text-xs"><p className="mb-2 font-medium text-primary">{t(`assistant.admin.role.${message.role}`)}</p><p className="whitespace-pre-wrap break-words text-text-body">{message.content}</p>{message.tool_calls?.map((call) => <p key={call.id} className="mt-2 break-all font-mono text-text-muted">{call.function.name} {call.function.arguments}</p>)}</li>)}</ol> : null}</div>;
}

function AssistantEditor({ editor, providers, models, onClose, onSaved }: {
  editor: Editor; providers: AssistantProvider[]; models: AssistantModel[]; onClose: () => void; onSaved: () => void;
}) {
  const { t } = useI18n();
  const formId = useId();
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState("");
  const [adjustmentId] = useState(() => crypto.randomUUID());
  const value = editor.value;
  const [draft, setDraft] = useState<Draft>(() => {
    const initial: Draft = {};
    if (value) for (const [key, item] of Object.entries(value)) if (typeof item === "string" || typeof item === "boolean" || typeof item === "number") initial[key] = typeof item === "number" ? String(item) : item;
    initial.enabled = field(value, "enabled") !== false;
    initial.api_key = "";
    if (editor.kind === "provider" || editor.kind === "credential") initial.protocol = textField(value, "protocol") || "openai_compatible";
    if (editor.kind === "model") initial.max_tokens_parameter = textField(value, "max_tokens_parameter") || "max_tokens";
    for (const key of ["monthly_budget_microusd", "input_price_microusd_per_million", "cached_input_price_microusd_per_million", "output_price_microusd_per_million"]) initial[key] = String(Number(field(value, key) || 0) / 1_000_000);
    if (!value && editor.kind === "model") Object.assign(initial, { provider_id: providers[0]?.id || "", max_output_tokens: "4096", context_window: "32768", input_credits_per_million: "1000", output_credits_per_million: "1000" });
    return initial;
  });
  const update = (key: string, next: string | boolean) => setDraft((previous) => ({ ...previous, [key]: next }));
  const read = (key: string) => String(draft[key] ?? "").trim();
  const number = (key: string) => Number(read(key) || 0);
  const money = (key: string) => Math.round(number(key) * 1_000_000);
  const enabled = draft.enabled === true;
  const id = textField(value, "id");
  const isNew = !id;

  async function submit(event: React.FormEvent) {
    event.preventDefault();
    if (busy) return;
    setBusy(true); setError("");
    try {
      let path = "";
      let method = "PUT";
      let payload: Record<string, unknown> = {};
      if (editor.kind === "provider") {
        path = `/admin/providers${id ? `/${encodeURIComponent(id)}` : ""}`; method = id ? "PUT" : "POST";
        payload = { name: read("name"), base_url: read("base_url"), protocol: read("protocol"), enabled, monthly_budget_microusd: money("monthly_budget_microusd") };
        if (read("api_key")) payload.api_key = read("api_key");
        if (draft.clear_key === true) payload.api_key = "";
      } else if (editor.kind === "model") {
        path = `/admin/models${id ? `/${encodeURIComponent(id)}` : ""}`; method = id ? "PUT" : "POST";
        payload = { name: read("name"), provider_id: read("provider_id"), upstream_model: read("upstream_model"), enabled,
          input_price_microusd_per_million: money("input_price_microusd_per_million"), cached_input_price_microusd_per_million: money("cached_input_price_microusd_per_million"),
          output_price_microusd_per_million: money("output_price_microusd_per_million"), input_credits_per_million: number("input_credits_per_million"),
          output_credits_per_million: number("output_credits_per_million"), max_output_tokens: number("max_output_tokens"), context_window: number("context_window"), max_tokens_parameter: read("max_tokens_parameter") };
      } else if (editor.kind === "credential") {
        path = `/admin/credentials/${encodeURIComponent(id)}`;
        payload = { name: read("name"), base_url: read("base_url"), protocol: read("protocol"), upstream_model: read("upstream_model"), enabled };
      } else if (editor.kind === "settings") {
        path = "/admin/settings";
        payload = { enabled, default_model_id: read("default_model_id"), registration_credits: number("registration_credits"), accepted_edit_credits: number("accepted_edit_credits"),
          monthly_budget_microusd: money("monthly_budget_microusd"), max_tool_rounds: number("max_tool_rounds"), reward_effective_at: new Date(read("reward_effective_at")).toISOString() };
      } else {
        path = `/admin/accounts/${encodeURIComponent(read("user_id"))}/adjust`; method = "POST";
        payload = { id: adjustmentId, credits: number("credits"), note: read("note") };
      }
      if (Object.values(payload).some((item) => typeof item === "number" && !Number.isSafeInteger(item))) throw new Error(t("assistant.admin.invalidNumber"));
      await assistantRequest(path, { method, body: JSON.stringify(payload) });
      onSaved();
    } catch (cause) { setError(cause instanceof Error ? cause.message : t("assistant.admin.failed")); } finally { setBusy(false); }
  }

  function control(name: string, type: "text" | "url" | "password" | "number" | "datetime-local" = "text", required = true, moneyInput = false) {
    return <label key={name} className="block space-y-1.5 text-xs text-text-muted"><span>{t(`assistant.admin.field.${name}`)}{moneyInput && name === "monthly_budget_microusd" ? <span className="ml-1">{t("assistant.admin.usd")}</span> : null}</span><input name={name} type={type} value={String(draft[name] ?? "")} onChange={(event) => update(name, event.target.value)} required={required} min={type === "number" && editor.kind !== "adjust" ? 0 : undefined} step={type === "number" ? moneyInput ? "0.000001" : "1" : undefined} autoComplete={type === "password" ? "new-password" : "off"} className={inputClass} /></label>;
  }
  function select(name: string, options: { id: string; name: string }[], empty = false) {
    return <label key={name} className="block space-y-1.5 text-xs text-text-muted"><span>{t(`assistant.admin.field.${name}`)}</span><select name={name} value={read(name)} onChange={(event) => update(name, event.target.value)} required={!empty} className={inputClass}>{empty || !options.length ? <option value="">{t("assistant.admin.none")}</option> : null}{options.map((item) => <option key={item.id} value={item.id}>{item.name}</option>)}</select></label>;
  }
  return <Modal open onClose={() => { if (!busy) onClose(); }} title={t(`assistant.admin.editor.${editor.kind}`)} maxWidth="max-w-2xl" footer={<div className="flex justify-end gap-2"><button type="button" onClick={onClose} disabled={busy} className={buttonClass}>{t("common.cancel")}</button><button form={formId} type="submit" disabled={busy} className={`${buttonClass} border-primary text-primary`}>{t(busy ? "common.saving" : "common.save")}</button></div>}>
    <form id={formId} onSubmit={(event) => void submit(event)} className="space-y-4">
      {error ? <p role="alert" className="break-all text-xs text-danger">{t("assistant.admin.failed")} {error}</p> : null}
      <div className="grid gap-4 sm:grid-cols-2">
        {editor.kind === "provider" || editor.kind === "credential" ? <>{control("name")}{select("protocol",ASSISTANT_PROTOCOLS.map(id=>({id,name:t(`assistant.protocol.${id}`)})))}{control("base_url", "url")}{editor.kind === "provider" ? <>{control("api_key", "password", false)}{control("monthly_budget_microusd", "number", true, true)}</> : control("upstream_model", "text", false)}</> : null}
        {editor.kind === "model" ? <>{control("name")}{select("provider_id", providers)}{control("upstream_model")}{select("max_tokens_parameter", [{ id: "max_tokens", name: "max_tokens" }, { id: "max_completion_tokens", name: "max_completion_tokens" }])}{control("context_window", "number")}{control("max_output_tokens", "number")}{control("input_price_microusd_per_million", "number", true, true)}{control("cached_input_price_microusd_per_million", "number", true, true)}{control("output_price_microusd_per_million", "number", true, true)}{control("input_credits_per_million", "number")}{control("output_credits_per_million", "number")}</> : null}
        {editor.kind === "settings" ? <>{select("default_model_id", models, true)}{control("monthly_budget_microusd", "number", true, true)}{control("registration_credits", "number")}{control("accepted_edit_credits", "number")}{control("reward_effective_at")}{control("max_tool_rounds", "number")}</> : null}
        {editor.kind === "adjust" ? <>{control("user_id")}{control("credits", "number")}{control("note")}</> : null}
      </div>
      {editor.kind !== "adjust" ? <label className="flex items-center gap-2 text-sm"><input type="checkbox" checked={enabled} onChange={(event) => update("enabled", event.target.checked)} />{t("assistant.admin.enabled")}</label> : null}
      {editor.kind === "provider" ? <div className="space-y-2 text-xs text-text-muted"><p>{t("assistant.admin.keyHint")}</p>{!isNew && field(value, "has_key") ? <label className="flex items-center gap-2"><input type="checkbox" checked={draft.clear_key === true} onChange={(event) => update("clear_key", event.target.checked)} />{t("assistant.admin.clearKey")}</label> : null}<p>{t("assistant.admin.budgetHint")}</p></div> : null}
      {editor.kind === "model" ? <p className="text-xs text-text-muted">{t("assistant.admin.priceHint")}</p> : null}
      {editor.kind === "settings" ? <p className="text-xs text-text-muted">{t("assistant.admin.rewardHint")}</p> : null}
      {editor.kind === "adjust" ? <p className="text-xs text-text-muted">{t("assistant.admin.adjustHint")}</p> : null}
    </form>
  </Modal>;
}
