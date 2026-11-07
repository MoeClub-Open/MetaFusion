"use client";

import { useState } from "react";
import { Cpu, KeyRound, Loader2, Pencil, Plus, Power, Trash2 } from "lucide-react";
import { Modal } from "@/components/ui/Modal";
import { ConfirmDialog } from "@/components/oauth/ConfirmDialog";
import { Select } from "@/components/ui/Select";
import { useI18n } from "@/i18n/I18nProvider";
import {
  ASSISTANT_PROTOCOLS, ASSISTANT_REASONING_EFFORTS, assistantRequest,
  type AssistantBYOModel, type AssistantBYOProvider, type AssistantProtocol, type AssistantReasoningEffort,
} from "@/lib/assistantApi";
import { describeAssistantError } from "@/lib/assistantUser";

const inputClass = "w-full rounded-control border border-line bg-background px-3 py-2 text-sm text-text-strong focus:outline-none focus:ring-2 focus:ring-primary/30";
const buttonClass = "inline-flex items-center justify-center gap-1.5 rounded-control border border-line px-2.5 py-1.5 text-xs text-text-body hover:bg-surfaceHover disabled:opacity-50";

export function AssistantCredentials({ providers, models, onChanged }: {
  providers: AssistantBYOProvider[]; models: AssistantBYOModel[]; onChanged: () => Promise<void>;
}) {
  const { t, locale } = useI18n();
  const number = new Intl.NumberFormat(locale);
  const [editingProvider, setEditingProvider] = useState<AssistantBYOProvider | "new" | null>(null);
  const [deletingProvider, setDeletingProvider] = useState<AssistantBYOProvider | null>(null);
  const [providerForm, setProviderForm] = useState({ name: "", base_url: "", protocol: "openai_compatible" as AssistantProtocol, api_key: "", enabled: true });
  const [editingModel, setEditingModel] = useState<AssistantBYOModel | "new" | null>(null);
  const [deletingModel, setDeletingModel] = useState<AssistantBYOModel | null>(null);
  const [modelForm, setModelForm] = useState({
    provider_id: "", name: "", upstream_model: "", context_window: "65536", max_output_tokens: "4096",
    max_tokens_parameter: "max_tokens" as "max_tokens" | "max_completion_tokens",
    reasoning_effort: "" as AssistantReasoningEffort, enabled: true,
  });
  const [error, setError] = useState("");
  const [busy, setBusy] = useState(false);

  const openProvider = (item: AssistantBYOProvider | "new") => {
    setError("");
    setProviderForm(item === "new"
      ? { name: "", base_url: "", protocol: "openai_compatible", api_key: "", enabled: true }
      : { name: item.name, base_url: item.base_url, protocol: item.protocol, api_key: "", enabled: item.enabled });
    setEditingProvider(item);
  };

  const openModel = (item: AssistantBYOModel | "new") => {
    setError("");
    setModelForm(item === "new"
      ? { provider_id: providers[0]?.id || "", name: "", upstream_model: "", context_window: "65536", max_output_tokens: "4096", max_tokens_parameter: "max_tokens", reasoning_effort: "", enabled: true }
      : { provider_id: item.provider_id, name: item.name, upstream_model: item.upstream_model, context_window: String(item.context_window), max_output_tokens: String(item.max_output_tokens), max_tokens_parameter: item.max_tokens_parameter, reasoning_effort: item.reasoning_effort || "", enabled: item.enabled });
    setEditingModel(item);
  };

  const mutate = async (path: string, method: string, body?: unknown) => {
    setBusy(true); setError("");
    try {
      await assistantRequest(path, { method, ...(body ? { body: JSON.stringify(body) } : {}) });
      await onChanged();
      setEditingProvider(null); setDeletingProvider(null);
      setEditingModel(null); setDeletingModel(null);
      setProviderForm((prev) => ({ ...prev, api_key: "" }));
    } catch (cause) { setError(describeAssistantError(cause, t)); }
    finally { setBusy(false); }
  };

  return <div className="space-y-3">
    {error && <p role="alert" className="rounded-control border border-danger/20 bg-danger/5 px-3 py-2 text-xs text-danger">{error}</p>}

    <section className="rounded-panel border border-line bg-surface">
      <div className="flex flex-wrap items-center justify-between gap-2 border-b border-line-subtle px-4 py-3">
        <h2 className="flex items-center gap-2 text-sm font-semibold text-text-strong"><KeyRound className="size-4 text-text-muted" />{t("assistant.user.byoProviders")}</h2>
        <button type="button" disabled={busy} className={buttonClass} onClick={() => openProvider("new")}><Plus className="size-3.5" />{t("assistant.user.addBYOProvider")}</button>
      </div>
      {!providers.length ? <p className="px-4 py-6 text-center text-xs text-text-muted">{t("assistant.user.noBYOProviders")}</p> : <div className="divide-y divide-line-subtle">
        {providers.map((item) => <div key={item.id} className="flex flex-wrap items-center justify-between gap-3 px-4 py-3">
          <div className="min-w-0 space-y-0.5">
            <div className="flex items-center gap-2 text-sm font-medium text-text-strong">
              <span className="truncate">{item.name}</span>
              <span className={"rounded-control px-1.5 py-0.5 text-[10px] " + (item.enabled ? "bg-primary/10 text-primary" : "bg-surfaceSubtle text-text-muted")}>{t(item.enabled ? "assistant.user.enabled" : "assistant.user.disabled")}</span>
            </div>
            <p className="text-xs text-text-muted break-all">{t("assistant.protocol." + item.protocol)} · {item.base_url}{item.key_hint ? " · " + item.key_hint : ""}</p>
          </div>
          <div className="flex items-center gap-1.5">
            <button type="button" disabled={busy} className={buttonClass} onClick={() => openProvider(item)} aria-label={t("common.edit")}><Pencil className="size-3.5" /></button>
            <button type="button" disabled={busy} className={buttonClass} aria-label={t(item.enabled ? "assistant.user.disable" : "assistant.user.enable")}
              onClick={() => void mutate("/byo/providers/" + item.id, "PUT", { name: item.name, base_url: item.base_url, protocol: item.protocol, enabled: !item.enabled })}><Power className="size-3.5" /></button>
            <button type="button" disabled={busy} className={buttonClass + " text-danger"} onClick={() => { setError(""); setDeletingProvider(item); }} aria-label={t("assistant.user.delete")}><Trash2 className="size-3.5" /></button>
          </div>
        </div>)}
      </div>}
    </section>

    <section className="rounded-panel border border-line bg-surface">
      <div className="flex flex-wrap items-center justify-between gap-2 border-b border-line-subtle px-4 py-3">
        <h2 className="flex items-center gap-2 text-sm font-semibold text-text-strong"><Cpu className="size-4 text-text-muted" />{t("assistant.user.byoModels")}</h2>
        <button type="button" disabled={busy || !providers.length} className={buttonClass} onClick={() => openModel("new")}><Plus className="size-3.5" />{t("assistant.user.addBYOModel")}</button>
      </div>
      {!models.length ? <p className="px-4 py-6 text-center text-xs text-text-muted">{t("assistant.user.noBYOModels")}</p> : <div className="divide-y divide-line-subtle">
        {models.map((item) => {
          const providerName = providers.find((entry) => entry.id === item.provider_id)?.name || item.provider_id;
          return <div key={item.id} className="flex flex-wrap items-center justify-between gap-3 px-4 py-3">
            <div className="min-w-0 space-y-0.5">
              <div className="flex items-center gap-2 text-sm font-medium text-text-strong">
                <span className="truncate">{item.name}</span>
                <span className={"rounded-control px-1.5 py-0.5 text-[10px] " + (item.enabled ? "bg-primary/10 text-primary" : "bg-surfaceSubtle text-text-muted")}>{t(item.enabled ? "assistant.user.enabled" : "assistant.user.disabled")}</span>
              </div>
              <p className="text-xs text-text-muted break-all">{providerName} · <span className="font-mono">{item.upstream_model}</span> · {number.format(item.context_window)} / {number.format(item.max_output_tokens)}</p>
              <p className="text-[11px] text-text-faint">{t("assistant.user.reasoningEffort")}: {t("assistant.reasoning." + (item.reasoning_effort || "default"))}</p>
            </div>
            <div className="flex items-center gap-1.5">
              <button type="button" disabled={busy} className={buttonClass} onClick={() => openModel(item)} aria-label={t("common.edit")}><Pencil className="size-3.5" /></button>
              <button type="button" disabled={busy} className={buttonClass} aria-label={t(item.enabled ? "assistant.user.disable" : "assistant.user.enable")}
                onClick={() => void mutate("/byo/models/" + item.id, "PUT", {
                  provider_id: item.provider_id, name: item.name, upstream_model: item.upstream_model,
                  context_window: item.context_window, max_output_tokens: item.max_output_tokens,
                  max_tokens_parameter: item.max_tokens_parameter, reasoning_effort: item.reasoning_effort || "", enabled: !item.enabled,
                })}><Power className="size-3.5" /></button>
              <button type="button" disabled={busy} className={buttonClass + " text-danger"} onClick={() => { setError(""); setDeletingModel(item); }} aria-label={t("assistant.user.delete")}><Trash2 className="size-3.5" /></button>
            </div>
          </div>;
        })}
      </div>}
    </section>

    <Modal open={editingProvider !== null} onClose={() => { if (!busy) { setEditingProvider(null); setProviderForm((prev) => ({ ...prev, api_key: "" })); } }} title={t(editingProvider === "new" ? "assistant.user.addBYOProvider" : "assistant.user.editBYOProvider")}>
      <form className="space-y-3" onSubmit={(event) => {
        event.preventDefault();
        if (!editingProvider) return;
        void mutate(
          editingProvider === "new" ? "/byo/providers" : "/byo/providers/" + editingProvider.id,
          editingProvider === "new" ? "POST" : "PUT",
          { name: providerForm.name.trim(), base_url: providerForm.base_url.trim(), protocol: providerForm.protocol, enabled: providerForm.enabled, ...(providerForm.api_key.trim() ? { api_key: providerForm.api_key.trim() } : {}) },
        );
      }}>
        <label className="block space-y-1 text-xs"><span>{t("assistant.user.providerName")}</span><input required maxLength={120} value={providerForm.name} className={inputClass} onChange={(event) => setProviderForm({ ...providerForm, name: event.target.value })} /></label>
        <label className="block space-y-1 text-xs"><span>{t("assistant.user.protocol")}</span><Select aria-label={t("assistant.user.protocol")} value={providerForm.protocol} onChange={(value) => setProviderForm({ ...providerForm, protocol: value as AssistantProtocol })} options={ASSISTANT_PROTOCOLS.map((value) => ({ value, label: t("assistant.protocol." + value) }))} /></label>
        <label className="block space-y-1 text-xs"><span>{t("assistant.user.baseURL")}</span><input required type="url" placeholder={providerForm.protocol === "anthropic_messages" ? "https://api.anthropic.com/v1" : providerForm.protocol === "gemini_generate_content" ? "https://generativelanguage.googleapis.com/v1beta" : "https://api.openai.com/v1"} value={providerForm.base_url} className={inputClass} onChange={(event) => setProviderForm({ ...providerForm, base_url: event.target.value })} /></label>
        <label className="block space-y-1 text-xs"><span>{t("assistant.user.apiKey")}</span><input required={editingProvider === "new"} type="password" autoComplete="new-password" value={providerForm.api_key} className={inputClass} onChange={(event) => setProviderForm({ ...providerForm, api_key: event.target.value })} />
          <span className="block text-text-muted">{t(editingProvider === "new" ? "assistant.user.keyWriteOnly" : "assistant.user.keyKeepEmpty")}</span></label>
        <label className="flex items-center gap-2 text-xs"><input type="checkbox" checked={providerForm.enabled} onChange={(event) => setProviderForm({ ...providerForm, enabled: event.target.checked })} />{t("assistant.user.enabled")}</label>
        {error && <p role="alert" className="text-xs text-danger">{error}</p>}
        <button type="submit" disabled={busy} className="inline-flex items-center gap-2 rounded-control bg-primary px-4 py-2 text-xs font-semibold text-primary-foreground disabled:opacity-50">{busy && <Loader2 className="size-3.5 animate-spin" />}{t("common.save")}</button>
      </form>
    </Modal>

    <Modal open={editingModel !== null} onClose={() => { if (!busy) setEditingModel(null); }} title={t(editingModel === "new" ? "assistant.user.addBYOModel" : "assistant.user.editBYOModel")}>
      <form className="space-y-3" onSubmit={(event) => {
        event.preventDefault();
        if (!editingModel || !modelForm.provider_id) return;
        void mutate(
          editingModel === "new" ? "/byo/models" : "/byo/models/" + editingModel.id,
          editingModel === "new" ? "POST" : "PUT",
          {
            provider_id: modelForm.provider_id,
            name: modelForm.name.trim(),
            upstream_model: modelForm.upstream_model.trim(),
            context_window: Number(modelForm.context_window) || 65536,
            max_output_tokens: Number(modelForm.max_output_tokens) || 4096,
            max_tokens_parameter: modelForm.max_tokens_parameter,
            reasoning_effort: modelForm.reasoning_effort,
            enabled: modelForm.enabled,
          },
        );
      }}>
        <label className="block space-y-1 text-xs"><span>{t("assistant.user.byoProviders")}</span><Select aria-label={t("assistant.user.byoProviders")} value={modelForm.provider_id} onChange={(value) => setModelForm({ ...modelForm, provider_id: value })} options={providers.map((item) => ({ value: item.id, label: item.name }))} /></label>
        <label className="block space-y-1 text-xs"><span>{t("assistant.user.modelName")}</span><input required maxLength={120} value={modelForm.name} className={inputClass} onChange={(event) => setModelForm({ ...modelForm, name: event.target.value })} /></label>
        <label className="block space-y-1 text-xs"><span>{t("assistant.user.upstreamModel")}</span><input required maxLength={200} value={modelForm.upstream_model} className={inputClass} onChange={(event) => setModelForm({ ...modelForm, upstream_model: event.target.value })} /></label>
        <div className="grid gap-3 sm:grid-cols-2">
          <label className="block space-y-1 text-xs"><span>{t("assistant.user.contextWindow")}</span><input required type="number" min={1} max={10000000} value={modelForm.context_window} className={inputClass} onChange={(event) => setModelForm({ ...modelForm, context_window: event.target.value })} /></label>
          <label className="block space-y-1 text-xs"><span>{t("assistant.user.maxOutputTokens")}</span><input required type="number" min={1} max={10000000} value={modelForm.max_output_tokens} className={inputClass} onChange={(event) => setModelForm({ ...modelForm, max_output_tokens: event.target.value })} /></label>
        </div>
        <label className="block space-y-1 text-xs"><span>{t("assistant.user.maxTokensParameter")}</span><Select aria-label={t("assistant.user.maxTokensParameter")} value={modelForm.max_tokens_parameter} onChange={(value) => setModelForm({ ...modelForm, max_tokens_parameter: value as "max_tokens" | "max_completion_tokens" })} options={[{ value: "max_tokens", label: "max_tokens" }, { value: "max_completion_tokens", label: "max_completion_tokens" }]} /></label>
        <label className="block space-y-1 text-xs"><span>{t("assistant.user.reasoningEffort")}</span><Select aria-label={t("assistant.user.reasoningEffort")} value={modelForm.reasoning_effort} onChange={(value) => setModelForm({ ...modelForm, reasoning_effort: value as AssistantReasoningEffort })} options={ASSISTANT_REASONING_EFFORTS.map((value) => ({ value, label: t("assistant.reasoning." + (value || "default")) }))} /></label>
        <label className="flex items-center gap-2 text-xs"><input type="checkbox" checked={modelForm.enabled} onChange={(event) => setModelForm({ ...modelForm, enabled: event.target.checked })} />{t("assistant.user.enabled")}</label>
        {error && <p role="alert" className="text-xs text-danger">{error}</p>}
        <button type="submit" disabled={busy || !modelForm.provider_id} className="inline-flex items-center gap-2 rounded-control bg-primary px-4 py-2 text-xs font-semibold text-primary-foreground disabled:opacity-50">{busy && <Loader2 className="size-3.5 animate-spin" />}{t("common.save")}</button>
      </form>
    </Modal>

    <ConfirmDialog open={!!deletingProvider} title={t("assistant.user.deleteBYOProvider")} message={t("assistant.user.deleteBYOProviderHint", { name: deletingProvider?.name || "" })}
      confirmLabel={t("assistant.user.delete")} busy={busy} onClose={() => { if (!busy) setDeletingProvider(null); }} onConfirm={() => { if (deletingProvider) void mutate("/byo/providers/" + deletingProvider.id, "DELETE"); }} />
    <ConfirmDialog open={!!deletingModel} title={t("assistant.user.deleteBYOModel")} message={t("assistant.user.deleteBYOModelHint", { name: deletingModel?.name || "" })}
      confirmLabel={t("assistant.user.delete")} busy={busy} onClose={() => { if (!busy) setDeletingModel(null); }} onConfirm={() => { if (deletingModel) void mutate("/byo/models/" + deletingModel.id, "DELETE"); }} />
  </div>;
}
