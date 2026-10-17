"use client";

import { useEffect, useState } from "react";
import { fetchOAuthGrants, revokeOAuthGrant, AuthorizedApp } from "@/lib/api";
import { useI18n } from "@/i18n/I18nProvider";
import { ShieldCheck, ShieldOff, Loader2, AlertCircle, Check, RefreshCw, KeyRound } from "lucide-react";

// 个人授权自助：列出"我授权过哪些第三方应用"，并能逐个撤回。
// 数据源是账号服务的 GET /api/auth/oauth-grants（只认当前登录身份，路径里没有别人的 user id）；
// 撤回走 DELETE /api/auth/oauth-grants/:client_id：删掉本人该客户端的未过期令牌并作废未兑换授权码，
// 幂等（本来就没有有效令牌时仍然回 ok），所以撤回失败必须如实报错而不是乐观地"先删了再回读"。
export function OAuthGrantsPanel() {
  const { t, locale } = useI18n();
  const [grants, setGrants] = useState<AuthorizedApp[] | null>(null);
  const [loading, setLoading] = useState(true);
  const [error, setError] = useState("");
  const [notice, setNotice] = useState("");
  const [confirming, setConfirming] = useState<AuthorizedApp | null>(null);
  const [revoking, setRevoking] = useState("");
  const [reloadKey, setReloadKey] = useState(0);

  // 401 单独讲：未登录/会话过期不是"加载失败"，也不该把用户晾在一个空列表上。
  const errorText = (e: unknown): string => {
    const status = typeof (e as { status?: unknown })?.status === "number" ? (e as { status: number }).status : undefined;
    if (status === 401) return t("settings.oauthLoginRequired");
    return t("settings.oauthLoadFailed");
  };

  useEffect(() => {
    let alive = true;
    setLoading(true);
    setError("");
    fetchOAuthGrants()
      .then((r) => {
        if (alive) setGrants(Array.isArray(r.items) ? r.items : []);
      })
      .catch((e: unknown) => {
        if (alive) {
          setGrants([]);
          setError(errorText(e));
        }
      })
      .finally(() => {
        if (alive) setLoading(false);
      });
    return () => {
      alive = false;
    };
  }, [reloadKey]);

  const formatTime = (value?: string): string | null => {
    if (!value) return null;
    const d = new Date(value);
    return Number.isNaN(d.getTime()) ? null : d.toLocaleString(locale);
  };

  const handleRevoke = async (app: AuthorizedApp) => {
    setRevoking(app.client_id);
    setError("");
    setNotice("");
    try {
      await revokeOAuthGrant(app.client_id);
      setConfirming(null);
      setNotice(t("settings.oauthRevoked", { name: app.name }));
      // 以服务端结果为准：撤回会同时改令牌与审计两侧，本地不猜状态，直接回读。
      setReloadKey((k) => k + 1);
    } catch (e: unknown) {
      const status = typeof (e as { status?: unknown })?.status === "number" ? (e as { status: number }).status : undefined;
      setError(status === 401 ? t("settings.oauthLoginRequired") : t("settings.oauthRevokeFailed"));
    } finally {
      setRevoking("");
    }
  };

  return (
    <div className="p-4 sm:p-5 space-y-4">
      <div className="space-y-1">
        <h3 className="text-sm font-semibold text-text-strong flex items-center gap-2">
          <KeyRound className="w-4 h-4 text-sky-500" />
          <span>{t("settings.oauthTitle")}</span>
        </h3>
        <p className="text-xs text-text-faint leading-relaxed">{t("settings.oauthDesc")}</p>
      </div>

      {error && (
        <div className="p-3 rounded-lg bg-red-500/10 border border-red-500/20 text-red-600 dark:text-danger-soft font-mono text-xs flex items-center gap-2">
          <AlertCircle className="w-4 h-4 shrink-0" strokeWidth={1.5} />
          <span className="flex-1">{error}</span>
          <button
            type="button"
            onClick={() => setReloadKey((k) => k + 1)}
            className="px-2 h-6 rounded-md bg-black/[0.04] dark:bg-white/[0.06] border border-line text-[11px] inline-flex items-center gap-1 shrink-0"
          >
            <RefreshCw className="w-3 h-3" />
            <span>{t("common.retry")}</span>
          </button>
        </div>
      )}

      {notice && (
        <div className="p-3 rounded-lg bg-emerald-500/10 border border-emerald-500/20 text-emerald-700 dark:text-success-soft font-mono text-xs flex items-center gap-2">
          <Check className="w-4 h-4 shrink-0" strokeWidth={1.5} />
          <span>{notice}</span>
        </div>
      )}

      {loading ? (
        <div className="p-8 text-center text-text-faint text-xs font-mono flex items-center justify-center gap-2">
          <Loader2 className="w-4 h-4 animate-spin text-primary" />
          <span>{t("common.loading")}</span>
        </div>
      ) : grants && grants.length === 0 ? (
        <div className="p-6 rounded-xl bg-surfaceSubtle border border-line-subtle text-center space-y-2">
          <ShieldCheck className="w-5 h-5 text-text-muted mx-auto" strokeWidth={1.5} />
          <div className="text-xs text-text-body">{t("settings.oauthEmpty")}</div>
        </div>
      ) : (
        <ul className="space-y-2">
          {(grants || []).map((app) => {
            const lastAt = formatTime(app.last_authorized_at);
            const expiresAt = formatTime(app.expires_at);
            return (
              <li key={app.client_id} className="p-3 rounded-lg bg-background border border-line-subtle space-y-2">
                <div className="flex items-start justify-between gap-3">
                  <div className="min-w-0 space-y-0.5">
                    <div className="flex items-center gap-2 flex-wrap">
                      <span className="text-sm font-semibold text-text-strong truncate">{app.name}</span>
                      <span
                        className={
                          "text-[10px] font-mono px-1.5 py-0.5 rounded-sm border " +
                          (app.active
                            ? "bg-emerald-500/10 text-emerald-600 dark:text-success border-emerald-500/30"
                            : "bg-black/[0.04] dark:bg-white/[0.06] text-text-faint border-line")
                        }
                      >
                        {app.active ? t("settings.oauthActive") : t("settings.oauthExpired")}
                      </span>
                    </div>
                    <div className="font-mono text-[10px] text-text-muted break-all">{app.client_id}</div>
                  </div>

                  <button
                    type="button"
                    onClick={() => {
                      setNotice("");
                      setError("");
                      setConfirming(app);
                    }}
                    className="shrink-0 px-2.5 h-7 rounded-md bg-rose-500/10 hover:bg-rose-500/20 border border-rose-500/30 text-rose-600 dark:text-danger-soft text-xs font-medium inline-flex items-center gap-1.5"
                  >
                    <ShieldOff className="w-3.5 h-3.5" />
                    <span>{t("settings.oauthRevoke")}</span>
                  </button>
                </div>

                <div className="flex flex-wrap items-center gap-x-3 gap-y-1 text-[11px] font-mono text-text-faint">
                  {lastAt && (
                    <span>
                      {t("settings.oauthLastAuthorized")}: <span className="text-text-body">{lastAt}</span>
                    </span>
                  )}
                  {expiresAt && (
                    <span>
                      {t("settings.oauthExpiresAt")}: <span className="text-text-body">{expiresAt}</span>
                    </span>
                  )}
                </div>

                {Array.isArray(app.scopes) && app.scopes.length > 0 && (
                  <div className="flex items-center gap-1.5 flex-wrap">
                    <span className="text-[10px] font-mono text-text-faint">{t("settings.oauthScopes")}:</span>
                    {app.scopes.map((scope) => (
                      <span
                        key={scope}
                        className="text-[10px] font-mono px-1.5 py-0.5 rounded-sm bg-sky-500/10 text-sky-600 dark:text-info border border-sky-500/20"
                      >
                        {scope}
                      </span>
                    ))}
                  </div>
                )}
              </li>
            );
          })}
        </ul>
      )}

      {/* 二次确认：撤回会让该应用手上的访问令牌立刻失效，不能一次点击就生效 */}
      {confirming && (
        <div className="fixed inset-0 z-50 flex items-center justify-center p-4 bg-black/70 backdrop-blur-sm">
          <div className="w-full max-w-md rounded-panel border border-line bg-surface shadow-2xl p-4 space-y-3">
            <div className="flex items-center gap-2">
              <div className="w-8 h-8 rounded-lg bg-rose-500/10 border border-rose-500/20 grid place-items-center shrink-0">
                <ShieldOff className="w-4 h-4 text-danger" />
              </div>
              <h4 className="text-sm font-semibold text-text-strong">{t("settings.oauthRevokeConfirmTitle")}</h4>
            </div>
            <p className="text-xs text-text-body leading-relaxed">
              {t("settings.oauthRevokeConfirm", { name: confirming.name })}
            </p>
            <div className="flex items-center justify-end gap-2 pt-1">
              <button
                type="button"
                onClick={() => setConfirming(null)}
                disabled={!!revoking}
                className="px-3 h-8 rounded-md border border-line text-xs text-text-body hover:bg-black/[0.04] dark:hover:bg-white/[0.06] disabled:opacity-50"
              >
                {t("common.cancel")}
              </button>
              <button
                type="button"
                onClick={() => handleRevoke(confirming)}
                disabled={!!revoking}
                className="px-3 h-8 rounded-md bg-rose-500 hover:bg-rose-400 text-emphasis keep-white text-xs font-semibold inline-flex items-center gap-1.5 disabled:opacity-50"
              >
                {revoking === confirming.client_id ? <Loader2 className="w-3.5 h-3.5 animate-spin" /> : <ShieldOff className="w-3.5 h-3.5" />}
                <span>{t("settings.oauthRevokeConfirmBtn")}</span>
              </button>
            </div>
          </div>
        </div>
      )}
    </div>
  );
}

export default OAuthGrantsPanel;
