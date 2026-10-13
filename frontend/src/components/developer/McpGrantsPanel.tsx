"use client";

import { useEffect, useState } from "react";
import { Check, Copy, Loader2, RefreshCw } from "lucide-react";
import { useI18n } from "@/i18n/I18nProvider";
import { copyText } from "@/lib/clipboard";
import { ApiError } from "@/lib/api";
import { ConfirmDialog } from "@/components/oauth/ConfirmDialog";

type Grant = {
  id: string;
  client_id: string;
  client_name: string;
  scopes: string[];
  revoked_at: string | null;
};

const SCOPE_GROUPS: { id: string; scopes: string[] }[] = [
  { id: "catalogRead", scopes: ["mcp.read"] },
  { id: "catalogEdit", scopes: ["catalog.entity.edit", "catalog.relation.edit"] },
  { id: "community", scopes: ["community.post.create", "community.post.moderate", "community.topic.pin", "community.report.review"] },
  { id: "storage", scopes: ["storage.asset.upload", "storage.asset.moderate"] },
  { id: "audit", scopes: ["auth.audit.read"] },
];

const KNOWN_SCOPES: Set<string> = new Set(SCOPE_GROUPS.flatMap((group) => group.scopes));

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

function visibleScopes(scopes: string[]): string[] {
  return Array.from(new Set(scopes.filter((scope) => KNOWN_SCOPES.has(scope))));
}

export function McpGrantsPanel() {
  const { t } = useI18n();
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
  const [openGroups, setOpenGroups] = useState<Record<string, boolean>>({});

  useEffect(() => {
    const abort = new AbortController();
    setLoading(true);
    Promise.all([
      request<{ endpoint: string }>("me", { signal: abort.signal }),
      request<{ items: Grant[] }>("grants", { signal: abort.signal }),
    ]).then(([me, list]) => {
      if (abort.signal.aborted) return;
      if (typeof me.endpoint !== "string" || !Array.isArray(list.items)) throw new Error("invalid_response");
      const items = list.items.map((grant) => ({
        ...grant,
        scopes: visibleScopes(grant.scopes || []),
      }));
      setEndpoint(me.endpoint);
      setGrants(items);
      setSelected(Object.fromEntries(items.map((grant) => [grant.id, [...grant.scopes]])));
      const nextOpen: Record<string, boolean> = {};
      for (const grant of items) {
        for (const group of SCOPE_GROUPS) {
          if (group.scopes.some((scope) => grant.scopes.includes(scope))) {
            nextOpen[`${grant.id}:${group.id}`] = true;
          }
        }
      }
      setOpenGroups(nextOpen);
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
  const purpose = (scope: string) => {
    const key = `developer.mcp.scopePurpose.${scope}`;
    const value = t(key);
    return value === key ? "" : value;
  };
  const buttonClass = "px-3 py-1.5 rounded-lg border border-line bg-surfaceSubtle hover:bg-surfaceHover text-xs text-text-body disabled:opacity-50 cursor-pointer";
  const dangerClass = "px-3 py-1.5 rounded-lg border border-danger/40 bg-danger/10 hover:bg-danger/20 text-xs text-danger-soft disabled:opacity-50 cursor-pointer";
  return (
    <div className="space-y-3">
      <p className="text-xs text-text-muted leading-relaxed">{t("developer.mcp.subtitle")}</p>
      <p className="text-xs text-text-faint leading-relaxed">{t("developer.mcp.manageNote")}</p>
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
        <button type="button" disabled={busy || loading} className={buttonClass} aria-label={t("developer.reload")} onClick={() => {
          setError("");
          setReload((value) => value + 1);
        }}>
          <RefreshCw className="h-3.5 w-3.5" />
        </button>
      </div>
      {loading ? <Loader2 className="h-4 w-4 animate-spin text-primary" /> : grants?.length === 0 ? (
        <p className="text-xs text-text-faint">{t("developer.mcp.empty")}</p>
      ) : grants?.map((grant) => {
        const active = !grant.revoked_at;
        const scopes = selected[grant.id] || [];
        const changed = grant.scopes.some((scope) => !scopes.includes(scope)) || scopes.some((scope) => !grant.scopes.includes(scope));
        return (
          <article key={grant.id} className="space-y-3 rounded-xl border border-line p-3">
            <div className="flex flex-wrap items-center justify-between gap-2">
              <div>
                <h3 className="text-sm font-medium text-text-strong">{grant.client_name}</h3>
                <p className="text-xs text-text-faint">client_id: {grant.client_id}</p>
              </div>
              <span className={`text-xs rounded-full border px-2 py-0.5 ${
                grant.revoked_at ? "border-line text-text-faint" : "border-success/40 text-success"
              }`}>
                {t(grant.revoked_at ? "developer.apiKeyRevoked" : "developer.apiKeyActive")}
              </span>
            </div>
            {active && (
              <div className="space-y-2">
                {SCOPE_GROUPS.map((group) => {
                  const groupScopes = group.scopes.filter((scope) => grant.scopes.includes(scope));
                  if (!groupScopes.length) return null;
                  const key = `${grant.id}:${group.id}`;
                  const open = openGroups[key] ?? true;
                  return (
                    <details key={group.id} open={open} onToggle={(event) => {
                      setOpenGroups((values) => ({ ...values, [key]: (event.target as HTMLDetailsElement).open }));
                    }} className="rounded-lg border border-line bg-surfaceSubtle">
                      <summary className="flex flex-wrap items-center gap-2 cursor-pointer list-none px-3 py-2 text-xs text-text-strong">
                        <span className="font-medium">{t(`developer.mcp.group.${group.id}`)}</span>
                        <span className="text-text-muted font-normal">{t(`developer.mcp.group.${group.id}.desc`)}</span>
                        <span className="ml-auto flex gap-2">
                          <button type="button" className={buttonClass} disabled={busy || loading} onClick={(event) => {
                            event.preventDefault();
                            setSelected((values) => ({
                              ...values,
                              [grant.id]: Array.from(new Set([...scopes, ...groupScopes])),
                            }));
                          }}>{t("developer.mcp.selectGroup")}</button>
                          <button type="button" className={buttonClass} disabled={busy || loading} onClick={(event) => {
                            event.preventDefault();
                            setSelected((values) => ({
                              ...values,
                              [grant.id]: scopes.filter((scope) => !groupScopes.includes(scope)),
                            }));
                          }}>{t("developer.mcp.clearGroup")}</button>
                        </span>
                      </summary>
                      <fieldset disabled={busy || loading} className="space-y-2 px-3 pb-3">
                        <legend className="sr-only">{t(`developer.mcp.group.${group.id}`)}</legend>
                        {groupScopes.map((scope) => (
                          <label key={scope} className="grid grid-cols-[auto_1fr] gap-x-2 gap-y-0.5 text-xs text-text-body">
                            <input
                              type="checkbox"
                              className="mt-0.5"
                              checked={scopes.includes(scope)}
                              onChange={(event) => {
                                setSelected((values) => ({
                                  ...values,
                                  [grant.id]: event.target.checked
                                    ? [...scopes, scope]
                                    : scopes.filter((item) => item !== scope),
                                }));
                              }}
                            />
                            <span>
                              <span className="text-text-strong">{label(scope)}</span>
                              <span className="ml-2 font-mono text-[11px] text-text-faint">{scope}</span>
                            </span>
                            {purpose(scope) && <span className="col-start-2 text-text-muted">{purpose(scope)}</span>}
                          </label>
                        ))}
                      </fieldset>
                    </details>
                  );
                })}
              </div>
            )}
            {active && (
              <div className="flex flex-wrap gap-2">
                <button type="button" className={buttonClass} disabled={busy || loading || !changed || !scopes.length} onClick={() => void mutate(grant, false)}>{t("developer.mcp.save")}</button>
                <button type="button" className={buttonClass} disabled={busy || loading} onClick={() => setNotice(t("developer.mcp.reauthHelp"))}>{t("developer.mcp.reauthorize")}</button>
                <button type="button" className={dangerClass} disabled={busy || loading} onClick={() => setPending(grant)}>{t("developer.mcp.revokeAll")}</button>
              </div>
            )}
          </article>
        );
      })}
      <ConfirmDialog open={!!pending} busy={busy} title={t("developer.mcp.revokeTitle")}
        message={t("developer.mcp.revokeConfirm", { name: pending?.client_name || "" })}
        confirmLabel={t("developer.mcp.revokeAll")} onClose={() => setPending(null)}
        onConfirm={() => { if (pending) void mutate(pending, true); }} />
    </div>
  );
}
