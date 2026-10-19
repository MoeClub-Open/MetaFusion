"use client";

import { useState } from "react";
import { KeyRound, Loader2, Pencil, Plus, Power, Trash2 } from "lucide-react";
import { Card } from "@/components/ui/Card";
import { Modal } from "@/components/ui/Modal";
import { ConfirmDialog } from "@/components/oauth/ConfirmDialog";
import { Select } from "@/components/ui/Select";
import { useI18n } from "@/i18n/I18nProvider";
import { ASSISTANT_PROTOCOLS, assistantRequest, type AssistantCredential, type AssistantProtocol } from "@/lib/assistantApi";
import { describeAssistantError } from "@/lib/assistantUser";

const inputClass = "w-full rounded-control border border-line bg-background px-3 py-2 text-sm text-text-strong focus:outline-none focus:ring-2 focus:ring-primary/30";
const buttonClass = "inline-flex items-center justify-center gap-1.5 rounded-control border border-line px-3 py-2 text-xs text-text-body hover:bg-surfaceHover disabled:opacity-50";

export function AssistantCredentials({ items, onChanged }: { items: AssistantCredential[]; onChanged: () => Promise<void> }) {
  const { t } = useI18n();
  const [editing, setEditing] = useState<AssistantCredential | "new" | null>(null);
  const [deleting, setDeleting] = useState<AssistantCredential | null>(null);
  const [form, setForm] = useState({ name: "", base_url: "", protocol: "openai_compatible" as AssistantProtocol, upstream_model: "", api_key: "", enabled: true });
  const [error, setError] = useState("");
  const [busy, setBusy] = useState(false);
  const open = (item: AssistantCredential | "new") => {
    setError("");
    setForm(item === "new" ? { name: "", base_url: "", protocol: "openai_compatible", upstream_model: "", api_key: "", enabled: true }
      : { name: item.name, base_url: item.base_url, protocol: item.protocol, upstream_model: item.upstream_model, api_key: "", enabled: item.enabled });
    setEditing(item);
  };
  const mutate = async (path: string, method: string, body?: unknown) => {
    setBusy(true); setError("");
    try {
      await assistantRequest(path, { method, ...(body ? { body: JSON.stringify(body) } : {}) });
      await onChanged();
      setEditing(null); setDeleting(null);
      setForm({ name: "", base_url: "", protocol: "openai_compatible", upstream_model: "", api_key: "", enabled: true });
    } catch (cause) { setError(describeAssistantError(cause, t)); }
    finally { setBusy(false); }
  };
  return <div className="space-y-4">
    <div className="flex flex-wrap items-start justify-between gap-3">
      <p className="text-xs text-text-muted leading-relaxed">{t("assistant.user.credentialsHint")}</p>
      <button type="button" className={buttonClass} onClick={() => open("new")}><Plus className="w-3.5 h-3.5" />{t("assistant.user.addCredential")}</button>
    </div>
    {error && <p role="alert" className="text-sm text-danger">{error}</p>}
    {!items.length && <Card><p className="text-xs text-text-muted">{t("assistant.user.noCredentials")}</p></Card>}
    {items.map((item) => <Card key={item.id}>
      <div className="flex flex-wrap items-center justify-between gap-3">
        <div className="min-w-0 space-y-1"><h2 className="text-sm font-semibold flex items-center gap-2"><KeyRound className="w-4 h-4 text-text-muted" />{item.name}<span className="text-[10px] text-text-muted font-normal">{t(item.enabled ? "assistant.user.enabled" : "assistant.user.disabled")}</span></h2>
          <p className="text-xs text-text-muted break-all">{item.base_url}</p>
          <p className="text-[11px] text-text-muted">{t(`assistant.protocol.${item.protocol}`)}</p>
          {item.upstream_model && <p className="text-[11px] text-text-muted">{t("assistant.user.upstreamModel")}: {item.upstream_model}</p>}
          <p className="text-[11px] text-text-faint font-mono">{item.key_hint}</p>
        </div>
        <div className="flex gap-1.5">
          <button type="button" disabled={busy} className={buttonClass} onClick={() => open(item)} aria-label={t("common.edit")}><Pencil className="w-3.5 h-3.5" /></button>
          <button type="button" disabled={busy} className={buttonClass} aria-label={t(item.enabled ? "assistant.user.disable" : "assistant.user.enable")}
            onClick={() => void mutate(`/credentials/${item.id}`, "PUT", { name: item.name, base_url: item.base_url, protocol: item.protocol, upstream_model: item.upstream_model, enabled: !item.enabled })}><Power className="w-3.5 h-3.5" /></button>
          <button type="button" disabled={busy} className={`${buttonClass} text-danger`} onClick={() => { setError(""); setDeleting(item); }} aria-label={t("assistant.user.delete")}><Trash2 className="w-3.5 h-3.5" /></button>
        </div>
      </div>
    </Card>)}
    <Modal open={editing !== null} onClose={() => { if (!busy) { setEditing(null); setForm((value) => ({ ...value, api_key: "" })); } }} title={t(editing === "new" ? "assistant.user.addCredential" : "assistant.user.editCredential")}>
      <form className="space-y-4" onSubmit={(event) => {
        event.preventDefault();
        if (!editing) return;
        void mutate(editing === "new" ? "/credentials" : `/credentials/${editing.id}`, editing === "new" ? "POST" : "PUT",
          { name: form.name.trim(), base_url: form.base_url.trim(), protocol: form.protocol, upstream_model: form.upstream_model.trim(), enabled: form.enabled, ...(form.api_key ? { api_key: form.api_key } : {}) });
      }}>
        <label className="block text-xs space-y-1"><span>{t("assistant.user.credentialName")}</span><input required maxLength={120} value={form.name} className={inputClass} onChange={(event) => setForm({ ...form, name: event.target.value })} /></label>
        <label className="block text-xs space-y-1"><span>{t("assistant.user.protocol")}</span><Select aria-label={t("assistant.user.protocol")} value={form.protocol} onChange={value=>setForm({...form,protocol:value as AssistantProtocol})} options={ASSISTANT_PROTOCOLS.map(value=>({value,label:t(`assistant.protocol.${value}`)}))}/></label>
        <label className="block text-xs space-y-1"><span>{t("assistant.user.baseURL")}</span><input required type="url" placeholder={form.protocol === "anthropic_messages" ? "https://api.anthropic.com/v1" : form.protocol === "gemini_generate_content" ? "https://generativelanguage.googleapis.com/v1beta" : "https://api.openai.com/v1"} value={form.base_url} className={inputClass} onChange={(event) => setForm({ ...form, base_url: event.target.value })} /></label>
        <label className="block text-xs space-y-1"><span>{t("assistant.user.upstreamModel")}</span><input maxLength={200} value={form.upstream_model} className={inputClass} onChange={(event) => setForm({ ...form, upstream_model: event.target.value })} /><span className="block text-text-muted">{t("assistant.user.upstreamModelHint")}</span></label>
        <label className="block text-xs space-y-1"><span>{t("assistant.user.apiKey")}</span><input required={editing === "new"} type="password" autoComplete="new-password" value={form.api_key} className={inputClass} onChange={(event) => setForm({ ...form, api_key: event.target.value })} />
          <span className="block text-text-muted leading-relaxed">{t(editing === "new" ? "assistant.user.keyWriteOnly" : "assistant.user.keyKeepEmpty")}</span></label>
        {error && <p role="alert" className="text-xs text-danger">{error}</p>}
        <button type="submit" disabled={busy} className="inline-flex items-center gap-2 rounded-control bg-primary px-4 py-2 text-xs font-semibold text-white disabled:opacity-50">{busy && <Loader2 className="w-3.5 h-3.5 animate-spin" />}{t("common.save")}</button>
      </form>
    </Modal>
    <ConfirmDialog open={!!deleting} title={t("assistant.user.deleteCredential")} message={t("assistant.user.deleteCredentialHint", { name: deleting?.name || "" })}
      confirmLabel={t("assistant.user.delete")} busy={busy} onClose={() => { if (!busy) setDeleting(null); }} onConfirm={() => { if (deleting) void mutate(`/credentials/${deleting.id}`, "DELETE"); }} />
  </div>;
}
