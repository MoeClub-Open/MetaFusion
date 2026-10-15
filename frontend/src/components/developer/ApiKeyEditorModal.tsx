"use client";

import { useEffect, useId, useMemo, useState, type FormEvent } from "react";
import { KeyRound, Loader2 } from "lucide-react";
import { useAuth } from "@/lib/authContext";
import { useI18n } from "@/i18n/I18nProvider";
import { Modal } from "@/components/ui/Modal";
import { PermissionScopePicker } from "./PermissionScopePicker";
import { grantablePermissionCodes } from "@/lib/permissions";
import { authErrorText, httpStatusOf } from "@/lib/authErrors";
import { createPersonalAccessToken, updatePersonalAccessToken, type CreatedPersonalAccessToken, type PersonalAccessToken } from "@/lib/api";

export function ApiKeyEditorModal({ open, token, onClose, onCreated, onSaved }: {
  open: boolean; token: PersonalAccessToken | null; onClose: () => void;
  onCreated: (created: CreatedPersonalAccessToken) => void; onSaved: () => void;
}) {
  const { t } = useI18n();
  const { user } = useAuth();
  const formID = useId();
  const [name, setName] = useState("");
  const [scopes, setScopes] = useState<string[]>([]);
  const [expiry, setExpiry] = useState(0);
  const [error, setError] = useState("");
  const [busy, setBusy] = useState(false);
  const grantable = useMemo(() => grantablePermissionCodes(user), [user]);
  const options = useMemo(() => Array.from(new Set([...grantable, ...(token?.scopes || [])])), [grantable, token]);
  useEffect(() => {
    if (open) { setName(token?.name || ""); setScopes(token?.scopes || []); setExpiry(0); setError(""); }
  }, [open, token]);
  const submit = async (event: FormEvent) => {
    event.preventDefault();
    if (busy) return;
    if (!name.trim()) { setError(t("developer.apiKeyNameRequired")); return; }
    if (!scopes.length) { setError(t("developer.apiKeyScopesRequired")); return; }
    setBusy(true); setError("");
    try {
      if (token) {
        const changed = scopes.length !== token.scopes.length || scopes.some((scope) => !token.scopes.includes(scope));
        await updatePersonalAccessToken(token.id, { name: name.trim(), ...(changed ? { scopes } : {}) });
        onSaved();
      } else {
        onCreated(await createPersonalAccessToken({ name: name.trim(), scopes, ...(expiry ? { expires_in_days: expiry } : {}) }));
      }
    } catch (err) {
      setError(authErrorText(err instanceof Error ? err.message : String(err), t, httpStatusOf(err), token ? "developer.apiKeyWriteFailed" : "developer.apiKeyCreateFailed"));
    } finally { setBusy(false); }
  };
  const field = "w-full rounded-lg border border-line bg-surfaceSubtle px-3 py-2 text-sm text-text-body";
  return <Modal open={open} onClose={() => { if (!busy) onClose(); }} title={t(token ? "developer.apiKeyEditTitle" : "developer.apiKeyCreateTitle")} icon={<KeyRound className="h-4 w-4 text-primary" />} initialFocus='input[name="name"]' footer={<div className="flex justify-end gap-2">
    <button type="button" disabled={busy} onClick={onClose} className="rounded-lg border border-line px-3 py-2 text-xs text-text-body">{t("developer.permissions.cancel")}</button>
    <button type="submit" form={formID} disabled={busy || !scopes.length} className="inline-flex items-center gap-2 rounded-lg bg-primary px-3 py-2 text-xs text-white disabled:opacity-50">{busy && <Loader2 className="h-3 w-3 animate-spin" />}{t(token ? "developer.permissions.save" : "developer.apiKeyCreateBtn")}</button>
  </div>}>
    <form id={formID} onSubmit={submit} className="space-y-4">
      {error && <p role="alert" className="text-xs text-danger">{error}</p>}
      <label className="block space-y-1 text-xs text-text-muted"><span>{t("developer.apiKeyTokenName")}</span><input name="name" autoComplete="off" value={name} maxLength={64} disabled={busy} onChange={(event) => setName(event.target.value)} className={field} required /></label>
      {!token && <label className="block space-y-1 text-xs text-text-muted"><span>{t("developer.apiKeyExpiry")}</span><select value={expiry} disabled={busy} onChange={(event) => setExpiry(Number(event.target.value))} className={field}>
        {[0, 30, 90, 365].map((days) => <option key={days} value={days}>{days ? t("developer.apiKeyExpiryDays", { days }) : t("developer.apiKeyExpiryNever")}</option>)}
      </select></label>}
      <PermissionScopePicker options={options} grantable={grantable} value={scopes} onChange={setScopes} disabled={busy} />
      <p className="text-xs text-text-faint">{t("developer.apiKeyScopesHint")}</p>
      <p className="text-xs text-text-faint">{t(token ? "developer.apiKeyEditHint" : "developer.apiKeyLimitHint")}</p>
    </form>
  </Modal>;
}
