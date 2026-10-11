"use client";

import { useEffect, useState } from "react";
import { Check, Copy, Loader2, RefreshCw } from "lucide-react";
import { useI18n } from "@/i18n/I18nProvider";
import { copyText } from "@/lib/clipboard";
import { ApiError } from "@/lib/api";
import { ConfirmDialog } from "@/components/oauth/ConfirmDialog";

type Grant = {
  id: string;
  client_name: string;
  scopes: string[];
  expires_at: string;
  revoked_at: string | null;
};

// Reuse the MCP service's owner-scoped management API and the site's session cookie.
// Mutations are sent once; refresh only reads their resulting state.
async function request<T>(path: string, options: RequestInit = {}): Promise<T> {
  const response = await fetch(`/mcp/manage/api/${path}`, {
    ...options,
    credentials: "same-origin",
    headers: { "Content-Type": "application/json" },
  });
  const data = await response.json();
  if (!response.ok) throw new ApiError(data.error || "request_failed", response.status);
  return data as T;
}

export function McpGrantsPanel() {
  const { t, locale } = useI18n();
  const [endpoint, setEndpoint] = useState("");
  const [grants, setGrants] = useState<Grant[] | null>(null);
  const [selected, setSelected] = useState<Record<string, string[]>>({});
  const [loading, setLoading] = useState(true);
  const [reload, setReload] = useState(0);
  const [error, setError] = useState("");
  const [notice, setNotice] = useState("");
  const [copied, setCopied] = useState(false);
  const [busy, setBusy] = useState(false);
  const [pending, setPending] = useState<Grant | null>(null);

  useEffect(() => {
    const abort = new AbortController();
    setLoading(true);
    Promise.all([
      request<{ endpoint: string }>("me", { signal: abort.signal }),
      request<{ items: Grant[] }>("grants", { signal: abort.signal }),
    ]).then(([me, list]) => {
      if (abort.signal.aborted) return;
      if (typeof me.endpoint !== "string" || !Array.isArray(list.items)) throw new Error("invalid_response");
      setEndpoint(me.endpoint);
      setGrants(list.items);
      setSelected(Object.fromEntries(list.items.map((grant) => [grant.id, [...grant.scopes]])));
    }).catch((err: unknown) => {
      if (abort.signal.aborted) return;
      setGrants(null);
      setError(err instanceof ApiError && err.status === 401
        ? t("developer.error.unauthorized") : t("developer.mcp.failed"));
    }).finally(() => {
      if (!abort.signal.aborted) setLoading(false);
    });
    return () => abort.abort();
  }, [reload, t]);

  const mutate = async (grant: Grant, revoke: boolean) => {
    setBusy(true);
    setError("");
    setNotice("");
    try {
      const result = await request<{ upstream_token_revoked: boolean }>(`grants/${encodeURIComponent(grant.id)}`, {
        method: revoke ? "DELETE" : "PATCH",
        ...(revoke ? {} : { body: JSON.stringify({ scopes: selected[grant.id] }) }),
      });
      setNotice(t(result.upstream_token_revoked
        ? revoke ? "developer.mcp.revokedDone" : "developer.mcp.saved"
        : "developer.mcp.cleanupPending"));
      setPending(null);
      setReload((value) => value + 1);
    } catch (err: unknown) {
      setError(err instanceof ApiError && err.status === 401
        ? t("developer.error.unauthorized") : t("developer.mcp.writeFailed"));
      // Read back after an uncertain outcome, without replaying the mutation.
      setPending(null);
      setReload((value) => value + 1);
    } finally {
      setBusy(false);
    }
  };

  const label = (scope: string) => {
    if (scope === "mcp.read") return t("developer.mcp.read");
    const key = `developer.apiKeyScope.${scope}`;
    const value = t(key);
    return value === key ? scope : value;
  };
  const buttonClass = "px-3 py-1.5 rounded-lg border border-line bg-surfaceSubtle hover:bg-surfaceHover text-xs text-text-body disabled:opacity-50 cursor-pointer";

  return (
    <div className="space-y-3">
      <p className="text-xs text-text-muted leading-relaxed">{t("developer.mcp.subtitle")}</p>
      {endpoint && (
        <div className="flex flex-wrap items-center gap-2 rounded-lg border border-line bg-surfaceSubtle p-3">
          <span className="text-xs text-text-muted">{t("developer.mcp.endpoint")}</span>
          <code className="flex-1 break-all text-xs text-text-strong">{endpoint}</code>
          <button type="button" className={buttonClass} onClick={() => void copyText(endpoint).then((ok) => {
            setCopied(ok);
            if (!ok) setError(t("developer.copyFailed"));
          })}>
            {copied ? <Check className="h-3.5 w-3.5" /> : <Copy className="h-3.5 w-3.5" />}
            <span className="sr-only">{t("developer.apiKeyCopy")}</span>
          </button>
        </div>
      )}
      {error && <p role="alert" className="text-xs text-danger-soft">{error}</p>}
      {notice && <p role="status" className="text-xs text-text-body">{notice}</p>}
      <div className="flex items-center justify-between gap-2">
        <p className="text-xs text-text-faint">{t("developer.mcp.permissionsHint")}</p>
        <button type="button" disabled={busy || loading} className={buttonClass} aria-label={t("developer.reload")} onClick={() => setReload((value) => value + 1)}>
          <RefreshCw className="h-3.5 w-3.5" />
        </button>
      </div>
      {loading ? <Loader2 className="h-4 w-4 animate-spin text-primary" /> : grants?.length === 0 ? (
        <p className="text-xs text-text-faint">{t("developer.mcp.empty")}</p>
      ) : grants?.map((grant) => {
        const expired = new Date(grant.expires_at).getTime() <= Date.now();
        const active = !grant.revoked_at && !expired;
        const scopes = selected[grant.id] || [];
        const changed = grant.scopes.some((scope) => !scopes.includes(scope));
        return (
          <article key={grant.id} className="space-y-3 rounded-xl border border-line p-3">
            <div className="flex flex-wrap items-center justify-between gap-2">
              <h3 className="text-sm font-medium text-text-strong">{grant.client_name}</h3>
              <span className="text-xs text-text-muted">{t(grant.revoked_at ? "developer.apiKeyRevoked" : expired ? "developer.apiKeyExpired" : "developer.apiKeyActive")}</span>
            </div>
            <p className="text-xs text-text-faint">{t("developer.apiKeyExpiresAt", { time: new Date(grant.expires_at).toLocaleString(locale) })}</p>
            <fieldset disabled={!active || busy || loading} className="flex flex-wrap gap-x-4 gap-y-2">
              <legend className="sr-only">{t("developer.apiKeyScopes")}</legend>
              {Array.from(new Set(grant.scopes)).map((scope) => (
                <label key={scope} className="flex items-center gap-2 text-xs text-text-body">
                  <input type="checkbox" checked={scopes.includes(scope)} onChange={(event) => {
                    setSelected((values) => ({ ...values, [grant.id]: event.target.checked
                      ? [...scopes, scope] : scopes.filter((item) => item !== scope) }));
                  }} />
                  {label(scope)}
                </label>
              ))}
            </fieldset>
            {active && (
              <div className="flex gap-2">
                <button type="button" className={buttonClass} disabled={busy || loading || !changed || !scopes.length} onClick={() => void mutate(grant, false)}>{t("developer.mcp.save")}</button>
                <button type="button" className={buttonClass} disabled={busy || loading} onClick={() => setPending(grant)}>{t("developer.apiKeyRevoke")}</button>
              </div>
            )}
          </article>
        );
      })}
      <ConfirmDialog open={!!pending} busy={busy} title={t("developer.mcp.revokeTitle")}
        message={t("developer.mcp.revokeConfirm", { name: pending?.client_name || "" })}
        confirmLabel={t("developer.apiKeyRevoke")} onClose={() => setPending(null)}
        onConfirm={() => { if (pending) void mutate(pending, true); }} />
    </div>
  );
}
