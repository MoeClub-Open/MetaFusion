"use client";

import React, { useCallback, useEffect, useState } from "react";
import { Navbar } from "@/components/Navbar";
import { useAuth } from "@/lib/authContext";
import { useI18n } from "@/i18n/I18nProvider";
import {
  createInviteCode,
  fetchAuthSettings,
  fetchInviteLedger,
  InviteCode,
  InviteLedger,
  PublicAuthSettings,
} from "@/lib/api";
import { authErrorText, httpStatusOf } from "@/lib/authErrors";
import { copyText } from "@/lib/clipboard";
import {
  AlertCircle,
  Check,
  Copy,
  Link2,
  Loader2,
  Plus,
  ShieldAlert,
} from "lucide-react";
import { PageShell } from "@/components/ui/PageShell";

type CodeStatus = "active" | "revoked" | "expired" | "exhausted";

// 邀请码状态是服务端字段的组合结论（revoked/expires_at/used_count），不额外造枚举。
function codeStatus(item: InviteCode): CodeStatus {
  if (item.revoked) return "revoked";
  if (item.expires_at && new Date(item.expires_at).getTime() <= Date.now()) return "expired";
  if (item.used_count >= item.max_uses) return "exhausted";
  return "active";
}

const STATUS_CLASS: Record<CodeStatus, string> = {
  active: "bg-emerald-500/10 border-emerald-500/25 text-emerald-600 dark:text-success",
  revoked: "bg-red-500/10 border-red-500/25 text-red-500 dark:text-danger-soft",
  expired: "bg-black/[0.04] dark:bg-white/[0.06] border-line text-text-muted",
  exhausted: "bg-amber-500/10 border-amber-500/25 text-amber-600 dark:text-warn-soft",
};

function formatDay(value?: string): string {
  if (!value) return "";
  const d = new Date(value);
  if (Number.isNaN(d.getTime())) return "";
  return d.toLocaleDateString();
}

export default function InvitesPage() {
  const { t } = useI18n();
  const { user, loading: authLoading } = useAuth();

  const [ledger, setLedger] = useState<InviteLedger | null>(null);
  const [loading, setLoading] = useState(true);
  const [loadError, setLoadError] = useState<string | null>(null);
  const [settings, setSettings] = useState<PublicAuthSettings | null>(null);

  const [note, setNote] = useState("");
  const [maxUses, setMaxUses] = useState(1);
  const [expiresInDays, setExpiresInDays] = useState(0);
  const [creating, setCreating] = useState(false);
  const [createError, setCreateError] = useState<string | null>(null);
  const [createdCode, setCreatedCode] = useState<string | null>(null);
  const [copiedKey, setCopiedKey] = useState<string | null>(null);
  // 复制失败要如实说：明文 http 或用户拒权时剪贴板 API 不存在，徽标不出现会被读成"点了没反应"。
  const [copyFailed, setCopyFailed] = useState(false);

  const load = useCallback(async () => {
    setLoading(true);
    setLoadError(null);
    try {
      setLedger(await fetchInviteLedger());
    } catch (err: any) {
      setLedger(null);
      setLoadError(authErrorText(err?.message, t, httpStatusOf(err)));
    } finally {
      setLoading(false);
    }
  }, [t]);

  useEffect(() => {
    if (!user) {
      setLoading(false);
      return;
    }
    load();
  }, [user, load]);

  // 邀请页要如实说明"当前实例要不要邀请码、有没有开放注册"，与前端的注册入口同一份设置。
  useEffect(() => {
    fetchAuthSettings()
      .then(setSettings)
      .catch(() => setSettings(null));
  }, []);

  const handleCopy = async (key: string, text: string) => {
    const ok = await copyText(text);
    setCopyFailed(!ok);
    if (!ok) return;
    setCopiedKey(key);
    setTimeout(() => setCopiedKey(null), 2000);
  };

  const handleCreate = async (e: React.FormEvent) => {
    e.preventDefault();
    setCreateError(null);
    setCreatedCode(null);
    setCreating(true);
    try {
      const created = await createInviteCode({
        note: note.trim(),
        max_uses: maxUses,
        expires_in_days: expiresInDays,
      });
      setLedger((prev) => (prev ? { ...prev, items: [created, ...prev.items] } : prev));
      setNote("");
      setCreatedCode(created.code);
    } catch (err: any) {
      setCreateError(authErrorText(err?.message, t, httpStatusOf(err)));
    } finally {
      setCreating(false);
    }
  };

  if (authLoading || (!user && loading)) {
    return (
      <div className="min-h-screen flex flex-col">
        <Navbar />
        <PageShell width="narrow" center className="py-16">
          <Loader2 className="w-6 h-6 animate-spin text-primary" />
        </PageShell>
      </div>
    );
  }

  // 未登录不渲染本页：AuthGate 已把 /invites 的未登录访问 replace 到 /login?redirect=/invites，
  // 这里原来那份「请先登录」整屏永远渲染不到。
  const items = Array.isArray(ledger?.items) ? ledger?.items : [];
  const members = ledger?.members || [];
  const canCreate = ledger?.can_create === true;

  return (
    <div className="min-h-screen flex flex-col">
      <Navbar />
      <PageShell width="narrow" spacing="none" contentClassName="space-y-4 sm:space-y-5">
        <h1 className="font-display text-xl sm:text-2xl font-bold tracking-tight text-text-strong">
          {t("invite.title")}
        </h1>

        {loadError && (
          <div className="rounded-card border border-red-500/25 bg-red-500/10 p-3.5 text-red-500 dark:text-danger-soft font-mono text-xs sm:text-sm flex items-start gap-2">
            <AlertCircle className="w-4 h-4 shrink-0 mt-0.5" />
            <div className="min-w-0 space-y-1.5">
              <p className="break-words">{loadError}</p>
              <button
                type="button"
                onClick={load}
                className="underline underline-offset-2 hover:no-underline transition-opacity duration-fast"
              >
                {t("common.retry")}
              </button>
            </div>
          </div>
        )}

        {settings && !settings.registration_enabled && (
          <div className="rounded-card border border-amber-500/25 bg-amber-500/[0.06] p-3.5 text-amber-600 dark:text-warn-soft font-mono text-xs sm:text-sm flex items-start gap-2">
            <ShieldAlert className="w-4 h-4 shrink-0 mt-0.5" />
            <span className="min-w-0">{t("invite.registrationClosedHint")}</span>
          </div>
        )}

        {settings && settings.registration_enabled && !settings.invite_required && (
          <div className="rounded-card border border-line bg-surfaceSubtle p-3.5 text-text-muted font-mono text-xs sm:text-sm flex items-start gap-2">
            <AlertCircle className="w-4 h-4 shrink-0 mt-0.5" />
            <span className="min-w-0">{t("invite.notRequiredHint")}</span>
          </div>
        )}

        {!loading && ledger && !canCreate && (
          <div className="rounded-card border border-amber-500/25 bg-amber-500/[0.06] p-3.5 text-amber-600 dark:text-warn-soft font-mono text-xs sm:text-sm flex items-start gap-2">
            <ShieldAlert className="w-4 h-4 shrink-0 mt-0.5" />
            <span className="min-w-0">{t("invite.noPermission")}</span>
          </div>
        )}

        {canCreate && (
          <form onSubmit={handleCreate} className="rounded-card border border-line bg-surface shadow-soft overflow-hidden">
            <div className="px-4 py-3 border-b border-line-subtle flex items-center gap-2">
              <Plus className="w-4 h-4 text-primary" />
              <span className="font-display text-sm font-bold text-text-strong">{t("invite.createTitle")}</span>
            </div>
            <div className="p-4 space-y-3">
              {createError && (
                <div className="rounded-control border border-red-500/20 bg-red-500/10 p-3 text-red-500 dark:text-danger-soft font-mono text-xs flex items-start gap-2">
                  <AlertCircle className="w-4 h-4 shrink-0 mt-0.5" />
                  <span className="min-w-0 break-words">{createError}</span>
                </div>
              )}
              {createdCode && (
                <div className="rounded-control border border-emerald-500/25 bg-emerald-500/10 p-3 font-mono text-xs text-emerald-600 dark:text-success flex items-center gap-2">
                  <Check className="w-4 h-4 shrink-0" />
                  <span className="min-w-0">
                    {t("invite.created")} <span className="font-bold tracking-widest">{createdCode}</span>
                  </span>
                </div>
              )}

              <div className="space-y-1.5">
                <label className="font-mono text-xs text-text-muted">{t("invite.note")}</label>
                <input
                  type="text"
                  value={note}
                  maxLength={200}
                  onChange={(e) => setNote(e.target.value)}
                  placeholder={t("invite.notePlaceholder")}
                  className="w-full px-3.5 h-10 bg-black/[0.03] dark:bg-black/20 border border-line rounded-control text-text-strong text-sm placeholder:text-text-muted focus:outline-none focus:border-primary"
                />
              </div>

              <div className="grid grid-cols-2 gap-3">
                <div className="space-y-1.5">
                  <label className="font-mono text-xs text-text-muted">{t("invite.maxUses")}</label>
                  <input
                    type="number"
                    min={1}
                    max={1000}
                    value={maxUses}
                    onChange={(e) => setMaxUses(Math.max(1, Number(e.target.value) || 1))}
                    className="w-full px-3.5 h-10 bg-black/[0.03] dark:bg-black/20 border border-line rounded-control text-text-strong text-sm focus:outline-none focus:border-primary"
                  />
                  <p className="font-mono text-[11px] text-text-faint">{t("invite.maxUsesHint")}</p>
                </div>
                <div className="space-y-1.5">
                  <label className="font-mono text-xs text-text-muted">{t("invite.expiresInDays")}</label>
                  <input
                    type="number"
                    min={0}
                    max={3650}
                    value={expiresInDays}
                    onChange={(e) => setExpiresInDays(Math.max(0, Number(e.target.value) || 0))}
                    className="w-full px-3.5 h-10 bg-black/[0.03] dark:bg-black/20 border border-line rounded-control text-text-strong text-sm focus:outline-none focus:border-primary"
                  />
                  <p className="font-mono text-[11px] text-text-faint">
                    {expiresInDays > 0 ? t("invite.expiresAt", { date: formatDay(new Date(Date.now() + expiresInDays * 86400000).toISOString()) }) : t("invite.expiresNever")}
                  </p>
                </div>
              </div>

              <button
                type="submit"
                disabled={creating}
                className="w-full h-10 rounded-control bg-primary text-white keep-white font-semibold text-sm flex items-center justify-center gap-2 hover:opacity-90 transition-opacity duration-fast shadow-xs disabled:opacity-50 mf-focus"
              >
                {creating ? (
                  <Loader2 className="w-4 h-4 animate-spin" />
                ) : (
                  <>
                    <Plus className="w-4 h-4" />
                    <span>{t("invite.createSubmit")}</span>
                  </>
                )}
              </button>
            </div>
          </form>
        )}

        <div className="rounded-card border border-line bg-surface shadow-soft overflow-hidden">
          <div className="px-4 py-2.5 border-b border-line-subtle flex items-center justify-between bg-surfaceSubtle">
            <span className="font-medium text-text-strong text-xs">{t("invite.ledger")}</span>
            <span className="font-mono text-[10px] uppercase text-text-muted">
              {t("invite.ledgerCount", { count: items.length })}
            </span>
          </div>
          {copyFailed ? (
            <p className="px-4 py-2 border-b border-line-subtle text-[11px] font-mono text-amber-600 dark:text-warn">
              {t("common.copyFailed")}
            </p>
          ) : null}
          {loading ? (
            <div className="p-8 grid place-items-center">
              <Loader2 className="w-5 h-5 animate-spin text-primary" />
            </div>
          ) : items.length === 0 ? (
            <div className="p-8 text-center">
              <p className="text-xs text-text-strong font-medium">{t("invite.ledgerEmpty")}</p>
              <p className="font-mono text-[11px] text-text-muted mt-1">{t("invite.ledgerEmptyHint")}</p>
            </div>
          ) : (
            <div className="divide-y divide-line-subtle">
              {items.map((item) => {
                const status = codeStatus(item);
                const loginUrl = `${typeof window !== "undefined" ? window.location.origin : ""}/login?invite=${encodeURIComponent(item.code)}`;
                return (
                  <div key={item.code} className="px-4 py-3 space-y-2 hover:bg-black/[0.02] dark:hover:bg-white/[0.02] transition-colors duration-fast">
                    <div className="flex items-center justify-between gap-3">
                      <span className="font-mono text-sm font-bold tracking-widest text-text-strong break-all">
                        {item.code}
                      </span>
                      <span className={`shrink-0 px-2 py-0.5 rounded-chip border font-mono text-[10px] ${STATUS_CLASS[status]}`}>
                        {t(`invite.status.${status}`)}
                      </span>
                    </div>
                    {item.note && (
                      <p className="text-xs text-text-muted break-words">{item.note}</p>
                    )}
                    <div className="flex flex-wrap items-center gap-x-3 gap-y-1 font-mono text-[11px] text-text-muted">
                      <span>{t("invite.uses", { used: item.used_count, max: item.max_uses })}</span>
                      <span>
                        {item.expires_at
                          ? t("invite.expiresAt", { date: formatDay(item.expires_at) })
                          : t("invite.expiresNever")}
                      </span>
                    </div>
                    <div className="flex items-center gap-2">
                      <button
                        type="button"
                        onClick={() => handleCopy(`code:${item.code}`, item.code)}
                        className="h-7 px-3 rounded-control bg-black/[0.04] dark:bg-white/[0.06] border border-line text-text-body hover:text-primary inline-flex items-center gap-1.5 text-xs transition-colors duration-fast mf-focus"
                      >
                        {copiedKey === `code:${item.code}` ? <Check className="w-3.5 h-3.5" /> : <Copy className="w-3.5 h-3.5" />}
                        <span>{copiedKey === `code:${item.code}` ? t("common.copied") : t("common.copy")}</span>
                      </button>
                      <button
                        type="button"
                        onClick={() => handleCopy(`link:${item.code}`, loginUrl)}
                        className="h-7 px-3 rounded-control bg-black/[0.04] dark:bg-white/[0.06] border border-line text-text-body hover:text-primary inline-flex items-center gap-1.5 text-xs transition-colors duration-fast mf-focus"
                      >
                        {copiedKey === `link:${item.code}` ? <Check className="w-3.5 h-3.5" /> : <Link2 className="w-3.5 h-3.5" />}
                        <span>{copiedKey === `link:${item.code}` ? t("common.copied") : t("common.copyLink")}</span>
                      </button>
                    </div>
                  </div>
                );
              })}
            </div>
          )}
        </div>

        <div className="rounded-card border border-line bg-surface shadow-soft overflow-hidden">
          <div className="px-4 py-2.5 border-b border-line-subtle flex items-center justify-between bg-surfaceSubtle">
            <span className="font-medium text-text-strong text-xs">
              {t("invite.invitedMembers", { count: members.length })}
            </span>
            <span className="font-mono text-[10px] uppercase text-text-muted">{t("invite.invitedMembersShort")}</span>
          </div>
          {loading ? (
            <div className="p-8 grid place-items-center">
              <Loader2 className="w-5 h-5 animate-spin text-primary" />
            </div>
          ) : members.length === 0 ? (
            <div className="p-8 text-center">
              <p className="text-xs text-text-strong font-medium">{t("invite.noMembers")}</p>
              <p className="font-mono text-[11px] text-text-muted mt-1">{t("invite.noMembersHint")}</p>
            </div>
          ) : (
            <div className="divide-y divide-line-subtle">
              {members.map((member) => (
                <div
                  key={member.id}
                  className="px-4 py-2.5 flex items-center justify-between gap-3 hover:bg-black/[0.02] dark:hover:bg-white/[0.02] transition-colors duration-fast"
                >
                  <div className="min-w-0">
                    <span className="text-xs font-medium text-text-strong">{member.username}</span>
                    {member.email && (
                      <span className="block font-mono text-[10px] text-text-muted truncate">{member.email}</span>
                    )}
                  </div>
                  <span className="shrink-0 font-mono text-[10px] text-text-muted">
                    {formatDay(member.created_at) || "—"}
                  </span>
                </div>
              ))}
            </div>
          )}
        </div>
      </PageShell>
    </div>
  );
}
