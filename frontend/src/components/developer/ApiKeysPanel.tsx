"use client";

import { useEffect, useState } from "react";
import {
  KeyRound,
  Loader2,
  RefreshCw,
  ShieldOff,
  Pencil,
  Trash2,
} from "lucide-react";
import { useI18n } from "@/i18n/I18nProvider";
import { authErrorText, httpStatusOf } from "@/lib/authErrors";
import {
  fetchPersonalAccessTokens,
  revokePersonalAccessToken,
  deletePersonalAccessToken,
  type CreatedPersonalAccessToken,
  type PersonalAccessToken,
} from "@/lib/api";
import { ConfirmDialog } from "@/components/oauth/ConfirmDialog";
import { ApiKeyRevealModal } from "@/components/developer/ApiKeyRevealModal";
import { ApiKeyEditorModal } from "./ApiKeyEditorModal";

// Personal API keys: create, edit, revoke and remove. Secrets are revealed only once.
// Downstream introspection caches can take up to 60 seconds to observe changes.
export function ApiKeysPanel({ modalOpen, onModalClose }: { modalOpen: boolean; onModalClose: () => void }) {
  const { t, locale } = useI18n();

  const [tokens, setTokens] = useState<PersonalAccessToken[] | null>(null);
  const [loading, setLoading] = useState(true);
  const [error, setError] = useState("");
  const [notice, setNotice] = useState("");
  const [reloadKey, setReloadKey] = useState(0);

  // 明文只活在这一次会话的内存里：关闭弹窗即丢弃。
  const [reveal, setReveal] = useState<CreatedPersonalAccessToken | null>(null);

  const [confirming, setConfirming] = useState<PersonalAccessToken | null>(null);
  const [editing, setEditing] = useState<PersonalAccessToken | null>(null);
  const [action, setAction] = useState<"revoke" | "delete">("revoke");
  const [revokingId, setRevokingId] = useState("");

  useEffect(() => {
    let alive = true;
    setLoading(true);
    setError("");
    fetchPersonalAccessTokens()
      .then((r) => {
        if (!alive) return;
        // 第二层守卫（第一层在 lib/api/auth.ts 的 fetchPersonalAccessTokens：包装层现在会直接抛）：
        // HTTP 200 但 items 不是数组 = **取不到数据**，与下面 catch 同口径按失败处理。
        // 只有确认为数组才 setTokens——空数组才是"你还没有令牌"（developer.apiKeyEmpty）；
        // 把"取不到"落成 [] 就是本文档开头警告过的"把失败讲成你还没有令牌"。
        if (!Array.isArray(r.items)) {
          setTokens(null);
          setError(patErrorText(new Error("invalid_response: items"), "developer.apiKeyLoadFailed"));
          return;
        }
        setTokens(r.items);
      })
      .catch((e: unknown) => {
        if (alive) {
          // 保持 null：加载失败时不能落成"空列表"，否则界面上会同时出现"加载失败"和
          // "暂无令牌"两句话，把失败讲成"你还没有令牌"。
          setTokens(null);
          setError(patErrorText(e, "developer.apiKeyLoadFailed"));
        }
      })
      .finally(() => {
        if (alive) setLoading(false);
      });
    return () => {
      alive = false;
    };
  }, [reloadKey]);

  // 账号服务的错误码可能带明细后缀（invalid_scope: catalog.entity.edit、
  // scope_not_granted: <code>）：先按码取名话，再把明细原样附在后面——码本身不翻译，
  // 但用户必须看得见是哪一个码被拒了，否则只能对着"请求失败"猜。
  const patErrorText = (e: unknown, fallbackKey: string): string => {
    const raw = e instanceof Error ? e.message : String(e);
    const cut = raw.indexOf(": ");
    const code = cut >= 0 ? raw.slice(0, cut) : raw;
    const detail = cut >= 0 ? raw.slice(cut + 2).trim() : "";
    const text = authErrorText(code, t, httpStatusOf(e), fallbackKey);
    return detail ? `${text} (${detail})` : text;
  };

  const formatTime = (value?: string | null): string | null => {
    if (!value) return null;
    const d = new Date(value);
    return Number.isNaN(d.getTime()) ? null : d.toLocaleString(locale);
  };

  // 未翻译的键会把裸键名显示出来：动态拼接的键（权限码）缺译文时回退到码本身，
  // 至少用户知道自己在勾什么。
  const labelOf = (key: string, fallback: string): string => {
    const value = t(key);
    return value === key ? fallback : value;
  };



  const handleRevoke = async () => {
    if (!confirming) return;
    const target = confirming;
    setRevokingId(target.id);
    setError("");
    setNotice("");
    try {
      if (action === "delete") await deletePersonalAccessToken(target.id);
      else await revokePersonalAccessToken(target.id);
      setConfirming(null);
      setNotice(t(action === "delete" ? "developer.apiKeyDeleteDone" : "developer.apiKeyRevokeDone", { name: target.name }));
      setReloadKey((k) => k + 1);
    } catch (err: unknown) {
      setConfirming(null);
      setError(patErrorText(err, "developer.apiKeyWriteFailed"));
    } finally {
      setRevokingId("");
    }
  };

  // 状态只使用服务端计算值，避免浏览器时钟偏差。
  const statusOf = (token: PersonalAccessToken): "revoked" | "expired" | "active" => {
    if (token.active) return "active";
    return token.revoked_at ? "revoked" : "expired";
  };

  return (
    <div className="space-y-4">
      {error && <p role="alert" className="rounded-lg border border-danger/30 bg-danger/10 p-3 text-sm text-danger">{error}</p>}
      {notice && <p role="status" className="rounded-lg border border-success/30 bg-success/10 p-3 text-sm text-success">{notice}</p>}
      {/* 已颁发令牌 */}
      <div className="space-y-2">
        <div className="flex items-center gap-2">
          <span className="font-mono text-xs font-semibold text-text-body">{t("developer.apiKeyIssuedTitle")}</span>
          <button
            type="button"
            onClick={() => setReloadKey((k) => k + 1)}
            className="ml-auto px-2 h-7 rounded-md bg-black/[0.04] dark:bg-white/[0.06] border border-line text-[11px] inline-flex items-center gap-1 text-text-body"
          >
            <RefreshCw className="w-3 h-3" />
            <span>{t("developer.apiKeyRefresh")}</span>
          </button>
        </div>

        {loading ? (
          <div className="p-8 text-center text-text-faint text-xs font-mono flex items-center justify-center gap-2">
            <Loader2 className="w-4 h-4 animate-spin text-primary" />
            <span>{t("developer.apiKeyLoading")}</span>
          </div>
        ) : tokens === null ? null : tokens.length === 0 ? (
          <div className="p-6 rounded-xl bg-surfaceSubtle border border-line-subtle text-center space-y-2">
            <KeyRound className="w-5 h-5 text-text-muted mx-auto" strokeWidth={1.5} />
            <div className="text-xs text-text-body">{t("developer.apiKeyEmpty")}</div>
          </div>
        ) : (
          <ul className="space-y-2">
            {tokens.map((token) => {
              const status = statusOf(token);
              const lastUsed = formatTime(token.last_used_at);
              const createdAt = formatTime(token.created_at);
              const expiresAt = formatTime(token.expires_at);
              const statusClass =
                status === "active"
                  ? "bg-emerald-500/10 text-emerald-600 dark:text-success border-emerald-500/30"
                  : status === "expired"
                    ? "bg-amber-500/10 text-amber-600 dark:text-warn border-amber-500/30"
                    : "bg-rose-500/10 text-rose-600 dark:text-danger-soft border-rose-500/30";
              return (
                <li key={token.id} className="p-3 rounded-lg bg-background border border-line-subtle space-y-2">
                  <div className="flex items-start justify-between gap-3">
                    <div className="min-w-0 space-y-0.5">
                      <div className="flex items-center gap-2 flex-wrap">
                        <span className="text-sm font-semibold text-text-strong truncate">{token.name}</span>
                        <span className={"text-[10px] font-mono px-1.5 py-0.5 rounded-sm border " + statusClass}>
                          {status === "active"
                            ? t("developer.apiKeyActive")
                            : status === "expired"
                              ? t("developer.apiKeyExpired")
                              : t("developer.apiKeyRevoked")}
                        </span>
                      </div>
                      <div className="font-mono text-[11px] text-text-faint" data-mf-apikey-prefix={token.token_prefix}>
                        {t("developer.apiKeyPrefix", { prefix: token.token_prefix })}
                      </div>
                    </div>

                    <div className="flex shrink-0 flex-wrap items-center gap-2">
                      <button type="button" disabled={!!revokingId} onClick={() => setEditing(token)} className="inline-flex items-center gap-1 rounded-md border border-line px-2 py-1 text-xs text-text-body"><Pencil className="h-3.5 w-3.5" />{t("developer.apiKeyEdit")}</button>
                    {status !== "revoked" && (
                      <button
                        type="button"
                        disabled={!!revokingId}
                        onClick={() => {
                          setNotice("");
                          setError("");
                          setConfirming(token);
                          setAction("revoke");
                        }}
                        className="shrink-0 px-2.5 h-7 rounded-md bg-rose-500/10 hover:bg-rose-500/20 border border-rose-500/30 text-rose-600 dark:text-danger-soft text-xs font-medium inline-flex items-center gap-1.5"
                      >
                        <ShieldOff className="w-3.5 h-3.5" />
                        <span>{t("developer.apiKeyRevoke")}</span>
                      </button>
                    )}
                      <button type="button" disabled={!!revokingId} onClick={() => { setConfirming(token); setAction("delete"); }} className="inline-flex items-center gap-1 rounded-md border border-danger/30 px-2 py-1 text-xs text-danger-soft"><Trash2 className="h-3.5 w-3.5" />{t("developer.apiKeyDelete")}</button>
                    </div>
                  </div>

                  <div className="flex flex-wrap items-center gap-x-3 gap-y-1 text-[11px] font-mono text-text-faint">
                    {createdAt && (
                      <span>
                        {t("developer.apiKeyCreatedAt", { time: createdAt })}
                      </span>
                    )}
                    <span>
                      {lastUsed
                        ? t("developer.apiKeyLastUsedAt", { time: lastUsed })
                        : t("developer.apiKeyNeverUsed")}
                    </span>
                    <span>
                      {expiresAt
                        ? t("developer.apiKeyExpiresAt", { time: expiresAt })
                        : t("developer.apiKeyNoExpiry")}
                    </span>
                  </div>

                  {token.scopes.length > 0 && (
                    <div className="flex items-center gap-1.5 flex-wrap">
                      {token.scopes.map((code) => (
                        <span
                          key={code}
                          className="text-[10px] font-mono px-1.5 py-0.5 rounded-sm bg-sky-500/10 text-sky-600 dark:text-info border border-sky-500/20"
                        >
                          {labelOf(`developer.apiKeyScope.${code}`, code)}
                        </span>
                      ))}
                    </div>
                  )}
                </li>
              );
            })}
          </ul>
        )}
      </div>

      <ApiKeyEditorModal
        open={modalOpen || !!editing}
        token={editing}
        onClose={() => { setEditing(null); onModalClose(); }}
        onSaved={() => { setEditing(null); setNotice(t("developer.apiKeySaved")); setReloadKey((key) => key + 1); }}
        onCreated={(created) => {
          onModalClose();
          setReveal(created);
          setReloadKey((k) => k + 1);
        }}
      />

      <ApiKeyRevealModal created={reveal} onClose={() => setReveal(null)} />

      {/* 撤销二次确认：用 Modal 版 ConfirmDialog，不用原生 confirm（不可本地化/不可样式化） */}
      <ConfirmDialog
        open={!!confirming}
        title={t(action === "delete" ? "developer.apiKeyDeleteTitle" : "developer.apiKeyRevokeTitle")}
        message={t(action === "delete" ? "developer.apiKeyDeleteConfirm" : "developer.apiKeyRevokeConfirm", { name: confirming?.name || "" })}
        confirmLabel={t(action === "delete" ? "developer.apiKeyDelete" : "developer.apiKeyRevoke")}
        busy={!!revokingId}
        onClose={() => { if (!revokingId) setConfirming(null); }}
        onConfirm={handleRevoke}
      />
    </div>
  );
}

export default ApiKeysPanel;
