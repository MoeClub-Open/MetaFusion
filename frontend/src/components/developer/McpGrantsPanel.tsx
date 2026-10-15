"use client";

import { useEffect, useState } from "react";
import { Check, Copy, Loader2, Pencil, RefreshCw } from "lucide-react";
import { useI18n } from "@/i18n/I18nProvider";
import { copyText } from "@/lib/clipboard";
import { ApiError } from "@/lib/api";
import { ConfirmDialog } from "@/components/oauth/ConfirmDialog";
import { Modal } from "@/components/ui/Modal";
import { PermissionScopePicker } from "./PermissionScopePicker";

type Grant = { id: string; client_id: string; client_name: string; scopes: string[]; revoked_at: string | null; cleanup_pending?: boolean };

async function request<T>(path: string, options: RequestInit = {}): Promise<T> {
  const response = await fetch(`/mcp/manage/api/${path}`, { ...options, credentials: "same-origin", headers: { "Content-Type": "application/json" } });
  const data = await response.json();
  if (!response.ok) throw new ApiError(data.error || "request_failed", response.status);
  return data as T;
}

export function McpGrantsPanel() {
  const { t } = useI18n();
  const [endpoint, setEndpoint] = useState("");
  const [grants, setGrants] = useState<Grant[] | null>(null);
  const [loading, setLoading] = useState(true);
  const [reload, setReload] = useState(0);
  const [error, setError] = useState("");
  const [notice, setNotice] = useState("");
  const [copied, setCopied] = useState(false);
  const [busy, setBusy] = useState(false);
  const [pending, setPending] = useState<Grant | null>(null);
  const [editing, setEditing] = useState<Grant | null>(null);
  const [selected, setSelected] = useState<string[]>([]);
  const [editError, setEditError] = useState("");

  useEffect(() => {
    const abort = new AbortController();
    setLoading(true);
    Promise.all([request<{ endpoint: string }>("me", { signal: abort.signal }), request<{ items: Grant[] }>("grants", { signal: abort.signal })])
      .then(([me, list]) => {
        if (abort.signal.aborted) return;
        if (typeof me.endpoint !== "string" || !Array.isArray(list.items) || list.items.some((item) => !Array.isArray(item.scopes))) throw new Error("invalid_response");
        setEndpoint(me.endpoint); setGrants(list.items);
      }).catch((err: unknown) => {
        if (abort.signal.aborted) return;
        setGrants(null); setError(err instanceof ApiError && err.status === 401 ? t("developer.error.unauthorized") : t("developer.mcp.failed"));
      }).finally(() => { if (!abort.signal.aborted) setLoading(false); });
    return () => abort.abort();
  }, [reload, t]);

  const mutate = async (grant: Grant, revoke: boolean) => {
    if (busy) return;
    setBusy(true); setError(""); setEditError(""); setNotice("");
    try {
      const result = await request<{ upstream_token_revoked?: boolean; upstream_token_updated?: boolean }>(`grants/${encodeURIComponent(grant.id)}`, {
        method: revoke ? "DELETE" : "PATCH", ...(revoke ? {} : { body: JSON.stringify({ scopes: selected }) }),
      });
      setNotice(t(revoke ? result.upstream_token_revoked ? "developer.mcp.revokedDone" : "developer.mcp.cleanupPending" : "developer.mcp.saved"));
      setPending(null); setEditing(null); setReload((value) => value + 1);
    } catch (err: unknown) {
      const text = t(err instanceof ApiError && err.status === 401 ? "developer.error.unauthorized" : "developer.mcp.writeFailed");
      if (revoke) setError(text); else setEditError(text);
    } finally { setBusy(false); }
  };
  const button = "inline-flex items-center gap-1.5 rounded-lg border border-line bg-surfaceSubtle px-3 py-1.5 text-xs text-text-body hover:bg-surfaceHover disabled:opacity-50";
  const changed = editing && (selected.length !== editing.scopes.length || selected.some((scope) => !editing.scopes.includes(scope)));
  return <div className="space-y-4">
    <p className="text-xs leading-relaxed text-text-muted">{t("developer.mcp.subtitle")}</p>
    <p className="text-xs leading-relaxed text-text-faint">{t("developer.mcp.credentialHint")}</p>
    {endpoint && <div className="flex flex-wrap items-center gap-2 rounded-lg border border-line bg-surfaceSubtle p-3">
      <span className="text-xs text-text-muted">{t("developer.mcp.endpoint")}</span><code className="flex-1 break-all text-xs text-text-strong">{endpoint}</code>
      <button type="button" className={button} aria-label={t("developer.apiKeyCopy")} onClick={() => void copyText(endpoint).then((ok) => { setCopied(ok); if (!ok) setError(t("developer.copyFailed")); })}>{copied ? <Check className="h-3.5 w-3.5" /> : <Copy className="h-3.5 w-3.5" />}</button>
    </div>}
    {error && <p role="alert" className="text-xs text-danger-soft">{error}</p>}
    {notice && <p role="status" className="text-xs text-text-body">{notice}</p>}
    <div className="flex items-center justify-between gap-2"><p className="text-xs text-text-faint">{t("developer.mcp.manageNote")}</p><button type="button" disabled={busy || loading} className={button} aria-label={t("developer.reload")} onClick={() => { setError(""); setReload((value) => value + 1); }}><RefreshCw className="h-3.5 w-3.5" /></button></div>
    {loading ? <Loader2 className="h-4 w-4 animate-spin text-primary" /> : grants?.length === 0 ? <p className="text-xs text-text-faint">{t("developer.mcp.empty")}</p> : grants?.map((grant) => <article key={grant.id} className="space-y-3 rounded-xl border border-line p-4">
      <div className="flex flex-wrap items-start justify-between gap-3"><div className="min-w-0"><h3 className="break-words text-sm font-medium text-text-strong">{grant.client_name}</h3><code className="break-all text-[11px] text-text-faint">{grant.client_id}</code></div><span className={grant.revoked_at ? "text-xs text-text-faint" : "text-xs text-success"}>{t(grant.revoked_at ? "developer.apiKeyRevoked" : "developer.apiKeyActive")}</span></div>
      <p className="text-xs text-text-muted">{t("developer.mcp.permissionCount", { count: grant.scopes.length })}</p>
      {grant.cleanup_pending && <p className="text-xs text-warn">{t("developer.mcp.cleanupPending")}</p>}
      {(!grant.revoked_at || grant.cleanup_pending) && <div className="flex flex-wrap gap-2">
        {!grant.revoked_at && <><button type="button" className={button} disabled={busy} onClick={() => { setEditing(grant); setSelected([...grant.scopes]); setEditError(""); }}><Pencil className="h-3 w-3" />{t("developer.mcp.edit")}</button>
        <button type="button" className={button} disabled={busy} onClick={() => setNotice(t("developer.mcp.reauthHelp"))}>{t("developer.mcp.reauthorize")}</button></>}
        <button type="button" className={button.replace("text-text-body", "text-danger-soft")} disabled={busy} onClick={() => { if (grant.revoked_at) void mutate(grant, true); else setPending(grant); }}>{t(grant.revoked_at ? "developer.mcp.retryCleanup" : "developer.mcp.revokeAll")}</button>
      </div>}
    </article>)}
    <Modal open={!!editing} onClose={() => { if (!busy) setEditing(null); }} title={t("developer.mcp.editTitle", { name: editing?.client_name || "" })} initialFocus='input[type="search"]' footer={<div className="flex justify-end gap-2"><button type="button" disabled={busy} className={button} onClick={() => setEditing(null)}>{t("developer.permissions.cancel")}</button><button type="button" disabled={busy || !changed || !selected.length} className={button} onClick={() => { if (editing) void mutate(editing, false); }}>{t("developer.permissions.save")}</button></div>}>
      {editing && <div className="space-y-4">
        {editError && <p role="alert" className="text-xs text-danger-soft">{editError}</p>}
        <p className="text-xs text-text-muted">{t("developer.mcp.permissionsHint")}</p>
        <PermissionScopePicker key={editing.id} options={editing.scopes} value={selected} onChange={setSelected} disabled={busy} />
      </div>}
    </Modal>
    <ConfirmDialog open={!!pending} busy={busy} title={t("developer.mcp.revokeTitle")} message={t("developer.mcp.revokeConfirm", { name: pending?.client_name || "" })} confirmLabel={t(pending?.revoked_at ? "developer.mcp.retryCleanup" : "developer.mcp.revokeAll")} onClose={() => { if (!busy) setPending(null); }} onConfirm={() => { if (pending) void mutate(pending, true); }} />
  </div>;
}
