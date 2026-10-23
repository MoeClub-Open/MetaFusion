"use client";

import React, { useState, useEffect } from "react";
import { Navbar } from "@/components/Navbar";
import { UserAvatar } from "@/components/UserAvatar";
import Link from "next/link";
import { AvatarEditor } from "@/components/settings/AvatarEditor";
import { Select } from "@/components/ui/Select";
import { useAuth } from "@/lib/authContext";
import { useI18n } from "@/i18n/I18nProvider";
import { UI_LOCALE_CODES, type UiLocale } from "@/lib/languages";
import { displayNameOf, updateOwnProfile } from "@/lib/api";
import { clearSessionHint } from "@/lib/sessionHint";
import { authErrorText, httpStatusOf } from "@/lib/authErrors";
import { TitleDisplayOrderSetting } from "@/components/settings/TitleDisplayOrderSetting";
import { OAuthGrantsPanel } from "@/components/settings/OAuthGrantsPanel";
import { DirectMessagePrivacyCard } from "@/components/settings/DirectMessagePrivacyCard";
import { ProfilePrivacyCards } from "@/components/settings/ProfilePrivacyCards";
import { MyActivityPanel } from "@/components/settings/MyActivityPanel";
import { ThemeControls } from "@/components/ThemeControls";
import { ThemeModeControls } from "@/components/ThemeModeControls";
import { useRouter, useSearchParams } from "next/navigation";
import {
  Shield,
  Globe,
  Lock,
  Check,
  AlertCircle,
  Settings,
  KeyRound,
  Eye,
  ShieldCheck,
  History,
  LogOut,
} from "lucide-react";
import { TabPanel } from "@/components/ui/TabPanel";
import { PageShell } from "@/components/ui/PageShell";
import { ConfirmDialog } from "@/components/oauth/ConfirmDialog";

// 页签白名单：?tab= 只认这几项，其余一律回资料页（避免深链把页面带到不存在的页签）。
type SettingsTab = "profile" | "activity" | "password" | "appearance" | "authorizations";

const SETTINGS_TABS: SettingsTab[] = ["profile", "activity", "password", "appearance", "authorizations"];

const SETTINGS_LOCALE_LABELS: Record<UiLocale, string> = {
  "zh-CN": "locale.simplifiedChinese",
  "zh-TW": "locale.traditionalChinese",
  "ja-JP": "locale.japanese",
  "en-US": "locale.englishUs",
};

export default function SettingsPage() {
  const { user, refreshProfile } = useAuth();
  const { t, locale, setLocale } = useI18n();
  const searchParams = useSearchParams();
  const router=useRouter();
  const tabParam = searchParams.get("tab");
  const initialTab: SettingsTab = SETTINGS_TABS.includes(tabParam as SettingsTab) ? (tabParam as SettingsTab) : "profile";

  const [activeTab, setActiveTab] = useState<SettingsTab>(initialTab);
  const [oldPassword, setOldPassword] = useState("");
  const [newPassword, setNewPassword] = useState("");
  const [confirmPassword, setConfirmPassword] = useState("");
  const [error, setError] = useState<string | null>(null);
  const [success, setSuccess] = useState<string | null>(null);
  const [submitting, setSubmitting] = useState(false);
  const [signingOutAll, setSigningOutAll] = useState(false);
  // 待确认的「全部设备登出」：确认框自绘（原生 confirm 不可本地化、不可样式化），与 /account 同形。
  const [pendingLogoutAll, setPendingLogoutAll] = useState(false);

  const [displayName, setDisplayName] = useState("");
  const [bio, setBio] = useState("");
  const [savingProfile, setSavingProfile] = useState(false);

  // 资料保存：PUT /api/auth/profile（空串=未设置）；成功后刷新会话用户，
  // 顶栏与用户主页即时跟进（/auth/me 读穿 DB，不等令牌周期）。
  const handleProfileSave = async () => {
    setError(null);
    setSuccess(null);
    setSavingProfile(true);
    try {
      await updateOwnProfile({ display_name: displayName.trim(), bio: bio.trim() });
      await refreshProfile();
      setSuccess(t("settings.profileSaved"));
    } catch (e: unknown) {
      setError(e instanceof Error ? e.message : t("settings.profileSaveFailed"));
    } finally {
      setSavingProfile(false);
    }
  };

  useEffect(() => {
    const tab = searchParams.get("tab");
    if (SETTINGS_TABS.includes(tab as SettingsTab)) {
      setActiveTab(tab as SettingsTab);
    }
  }, [searchParams]);

  useEffect(() => {
    if (user) {
      const u = user as unknown as Record<string, unknown>;
      setDisplayName((u["display_name"] as string) || "");
      setBio((u["bio"] as string) || "");
    }
  }, [user?.id, (user as any)?.avatar_url, (user as any)?.display_name, (user as any)?.bio]);

  const handlePasswordChange = async (e: React.FormEvent) => {
    e.preventDefault();
    setError(null);
    setSuccess(null);

    // 后端 Store.ChangePassword 要求 12–72 位，与请求体 old_password/new_password 对齐：
    // 上限也要在本地拦住，否则用户输入超长口令要等服务端 invalid_password_length 才知道。
    if (newPassword.length < 12 || newPassword.length > 72) {
      setError(t("auth.error.invalid_password_length"));
      return;
    }
    if (newPassword !== confirmPassword) {
      setError(t("settings.passwordMismatch"));
      return;
    }

    setSubmitting(true);
    try {
      // 保持原生 fetch，不走 fetchApi：fetchApi 在 401 时会静默续期并重试一次，而改密成功后
      // 服务端已删掉当前会话——那条重试路径只会多打一次 /auth/refresh 与重复的 PUT，还会把
      // "会话已失效，请重新登录"讲成密码错误。这里要的是如实报错、由用户自己重新登录。
      const res = await fetch("/api/auth/password", {
        method: "PUT",
        credentials: "same-origin",
        headers: { "Content-Type": "application/json" },
        body: JSON.stringify({
          old_password: oldPassword,
          new_password: newPassword,
        }),
      });
      const data = await res.json().catch(() => ({}));
      if (!res.ok) {
        // 带上状态码：账号服务的错误码要经 authErrorText 翻成当前语言的人话。
        const failed = new Error(data.error || "request_failed") as Error & { status?: number };
        failed.status = res.status;
        throw failed;
      }
      setOldPassword("");
      setNewPassword("");
      setConfirmPassword("");
      // 账号服务改密成功后会删掉该用户的全部 auth.sessions 与 auth.oauth_tokens（含发起这次
      // 请求的会话）：本地会话必须一起清掉，并把用户送回登录页说明原因，否则下一次请求会莫名 401。
      // 这里刻意用整页跳转，而不是 logout() + router.replace()：受保护页在 user 变成 null 时
      // AuthGate 也会 replace 一次 /login?redirect=/settings，两条客户端重定向会打架、把 ?notice
      // 顶掉（本地实测：URL 变成 /login?redirect=%2Fsettings，提示不出现）。
      clearSessionHint();
      window.location.assign(`/login?notice=password_changed&redirect=${encodeURIComponent("/settings?tab=password")}`);
    } catch (err: unknown) {
      // 服务端给的是稳定错误码（invalid_old_password / invalid_password_length 等）：
      // 必须走四语字典，不能把原始码当文案贴给用户（此处曾直出 invalid_old_password）。
      setError(
        authErrorText(
          err instanceof Error ? err.message : String(err),
          t,
          httpStatusOf(err),
          "settings.passwordFail"
        )
      );
    } finally {
      setSubmitting(false);
    }
  };

  // 点击只开确认框，确认后才走下面的动作（误触防护与 /account 的实现一致）。
  const handleLogoutAll = async () => {
    setPendingLogoutAll(false);
    setError(null);
    setSuccess(null);
    setSigningOutAll(true);
    try {
      // 原生 fetch，不走 fetchApi（与上面的改密同理）：账号服务这条端点会删掉该用户全部
      // auth.sessions，含发起这次请求的会话（metafusion-auth 的 Store.LogoutAll），
      // fetchApi 的 401 续期重试在这里只会多打一次注定失败的 /auth/refresh。
      const res = await fetch("/api/auth/logout-all", {
        method: "POST",
        credentials: "same-origin",
      });
      if (!res.ok) {
        const data = await res.json().catch(() => ({}));
        const failed = new Error(data.error || "request_failed") as Error & { status?: number };
        failed.status = res.status;
        throw failed;
      }
      // 当前 Cookie 会话已被撤销，清除提示后整页跳转并保留原因。
      clearSessionHint();
      window.location.assign(
        `/login?notice=sessions_revoked&redirect=${encodeURIComponent("/settings?tab=password")}`
      );
    } catch (err: unknown) {
      setError(authErrorText(err instanceof Error ? err.message : String(err), t, httpStatusOf(err)));
    } finally {
      setSigningOutAll(false);
    }
  };

  // 未登录时 AuthGate 先 return null 并 replace 到 /login?redirect=/settings，本页的「请先登录」
  // 界面从不渲染（同一条闸门写了两遍，其中一份是死代码）。这里只为把 user 收窄成非空。
  if (!user) return null;

  return (
    <div className="min-h-screen relative flex flex-col overflow-clip selection:bg-primary selection:text-white">
      <Navbar />
      <PageShell>
        <header className="mb-6"><h1 className="text-xl font-semibold text-text-strong">{t("settings.title")}</h1><p className="mt-1 text-sm text-text-muted">{t("settings.subtitle")}</p></header>
        <div className="grid items-start gap-6 md:grid-cols-[13rem_minmax(0,1fr)] lg:grid-cols-[15rem_minmax(0,1fr)]">
          <aside className="min-w-0 md:sticky md:top-20">
            <Link href={`/users/${user.id}`} className="mb-4 flex items-center gap-3 border-b border-line-subtle pb-4"><UserAvatar user={user} size="lg"/><span className="min-w-0"><strong className="block truncate text-sm">{displayNameOf(user)}</strong><span className="text-xs text-text-muted">{t("accountUI.publicProfile")}</span></span></Link>
            <nav aria-label={t("settings.title")} className="flex gap-1 overflow-x-auto md:flex-col">
              {([{id:"profile",label:"settings.tabProfile",icon:Settings},{id:"activity",label:"settings.tabActivity",icon:History},{id:"authorizations",label:"settings.tabAuthorizations",icon:ShieldCheck},{id:"appearance",label:"settings.appearanceTitle",icon:Globe},{id:"password",label:"settings.tabPassword",icon:Lock}] as const).map(({id,label,icon:Icon})=><button key={id} type="button" aria-current={activeTab===id?"page":undefined} onClick={()=>{setActiveTab(id);setError(null);setSuccess(null);router.replace(`/settings?tab=${id}`,{scroll:false});}} className={`flex shrink-0 items-center gap-2 rounded-control px-3 py-2.5 text-left text-sm ${activeTab===id?"bg-primary/10 font-medium text-primary":"text-text-muted hover:bg-surfaceSubtle"}`}><Icon className="size-4"/>{t(label)}</button>)}
            </nav>
          </aside>
          <div className="min-w-0">
        <TabPanel activeKey={activeTab} spacing="none" className="rounded-panel border border-line bg-surface overflow-hidden">
          {activeTab === "profile" && (
            <div className="p-4 sm:p-6 space-y-4">
              {error && (
                <div className="p-3 rounded-lg bg-red-500/10 border border-red-500/20 text-red-600 dark:text-danger-soft text-xs sm:text-sm flex items-center gap-2">
                  <AlertCircle className="w-4 h-4 shrink-0" strokeWidth={1.5} />
                  <span>{error}</span>
                </div>
              )}
              {success && (
                <div className="p-3 rounded-lg bg-emerald-500/10 border border-emerald-500/20 text-emerald-700 dark:text-success-soft text-xs sm:text-sm flex items-center gap-2">
                  <Check className="w-4 h-4 shrink-0" strokeWidth={1.5} />
                  <span>{success}</span>
                </div>
              )}

              <AvatarEditor key={user.id} />

              {/* 资料编辑：PUT /api/auth/profile（本人自助），空串=未设置 */}
              <form
                className="space-y-3.5"
                onSubmit={(e) => {
                  e.preventDefault();
                  void handleProfileSave();
                }}
              >
                <div className="space-y-1">
                  <label htmlFor="settings-display-name" className="text-xs sm:text-sm text-text-muted">{t("settings.displayName")}</label>
                  <input
                    id="settings-display-name"
                    value={displayName}
                    onChange={(e) => setDisplayName(e.target.value)}
                    maxLength={32}
                    placeholder={user.username}
                    className="w-full h-10 px-3.5 bg-background border border-line rounded-lg text-text-strong text-sm focus:outline-none focus:border-primary/50"
                  />
                  <p className="text-xs text-text-faint">{t("settings.displayNameHint")}</p>
                </div>
                <div className="space-y-1">
                  <label htmlFor="settings-bio" className="text-xs sm:text-sm text-text-muted">{t("settings.bioLabel")}</label>
                  <textarea
                    id="settings-bio"
                    value={bio}
                    onChange={(e) => setBio(e.target.value)}
                    maxLength={500}
                    rows={3}
                    className="w-full p-3.5 bg-background border border-line rounded-lg text-text-strong text-sm focus:outline-none focus:border-primary/50 resize-y"
                  />
                </div>
                <div className="flex items-center gap-2">
                  <button
                    type="submit"
                    disabled={savingProfile}
                    className="px-4 h-9 rounded-lg bg-primary text-white keep-white font-semibold text-xs inline-flex items-center gap-1.5 hover:opacity-90 transition-opacity disabled:opacity-50"
                  >
                    {savingProfile ? t("settings.profileSaving") : t("settings.profileSave")}
                  </button>
                </div>
              </form>

              <div className="grid gap-1.5 pt-2 border-t border-line-subtle">
                <div className="p-2.5 rounded-md bg-background border border-line-subtle flex items-center justify-between text-xs">
                  <span className="text-text-faint flex items-center gap-1.5">
                    <Shield className="w-3.5 h-3.5 text-text-muted" strokeWidth={1.5} />
					<span>{t("settings.accountGroups")}</span>
                  </span>
				  <span className="text-text-strong">{user.groups?.join(", ") || "—"}</span>
                </div>

                {!!(user as unknown as { invite_code?: string }).invite_code && (
                  <div className="p-2.5 rounded-md bg-background border border-line-subtle flex items-center justify-between text-xs">
                    <span className="text-text-faint flex items-center gap-1.5">
                      <KeyRound className="w-3.5 h-3.5 text-amber-500" strokeWidth={1.5} />
                      <span>{t("settings.inviteCodeLabel")}</span>
                    </span>
                    <span className="text-text-strong font-semibold tracking-widest">{(user as unknown as { invite_code: string }).invite_code}</span>
                  </div>
                )}


              </div>

              <div className="space-y-1.5 pt-2 border-t border-line-subtle">
                <div className="flex items-center gap-1.5 pb-1">
                  <Eye className="w-3.5 h-3.5 text-text-muted" strokeWidth={1.5} />
                  <span className="text-xs font-semibold text-text-body">{t("settings.privacyTitle")}</span>
                </div>

                <ProfilePrivacyCards key={`profile:${user.id}`} />
                <DirectMessagePrivacyCard key={`messages:${user.id}`} />
              </div>
            </div>
          )}

          {activeTab === "activity" && <MyActivityPanel />}

          {activeTab === "authorizations" && <OAuthGrantsPanel />}

          {activeTab === "appearance" && (
            <div className="space-y-5 p-4 sm:p-5">
              <section className="space-y-4">
                <div className="p-2.5 rounded-md bg-background border border-line-subtle flex items-center justify-between text-xs">
                  <span className="text-text-faint flex items-center gap-1.5">
                    <Globe className="w-3.5 h-3.5 text-sky-500" strokeWidth={1.5} />
                    <span>{t("settings.interfaceLanguage")}</span>
                  </span>
                  <Select
                    value={locale}
                    onChange={(val) => setLocale(val as UiLocale)}
                    fullWidth={false}
                    className="min-w-[10.5rem] h-8 px-2 text-xs"
                    options={UI_LOCALE_CODES.map((code) => ({ value: code, label: t(SETTINGS_LOCALE_LABELS[code as UiLocale]) }))}
                  />
                </div>

                <TitleDisplayOrderSetting />
              </section>
              <section className="space-y-2" aria-labelledby="appearance-mode-heading">
                <h2 id="appearance-mode-heading" className="text-sm font-semibold text-text-strong">{t("theme.displayMode")}</h2>
                <ThemeModeControls />
              </section>
              <section className="space-y-3 border-t border-line-subtle pt-4" aria-labelledby="appearance-palette-heading">
                <h2 id="appearance-palette-heading" className="text-sm font-semibold text-text-strong">{t("theme.palette")}</h2>
                <ThemeControls />
              </section>
            </div>
          )}

          {activeTab === "password" && (
            <form onSubmit={handlePasswordChange} className="p-4 sm:p-6 space-y-4">
              {error && (
                <div className="p-3 rounded-lg bg-red-500/10 border border-red-500/20 text-red-600 dark:text-danger-soft text-xs sm:text-sm flex items-center gap-2">
                  <AlertCircle className="w-4 h-4 shrink-0" strokeWidth={1.5} />
                  <span>{error}</span>
                </div>
              )}
              {success && (
                <div className="p-3 rounded-lg bg-emerald-500/10 border border-emerald-500/20 text-emerald-700 dark:text-success-soft text-xs sm:text-sm flex items-center gap-2">
                  <Check className="w-4 h-4 shrink-0" strokeWidth={1.5} />
                  <span>{success}</span>
                </div>
              )}

              <div className="space-y-1">
                <label className="text-xs sm:text-sm text-text-muted">{t("settings.oldPassword")}</label>
                <div className="relative">
                  <Lock className="absolute left-3 top-1/2 -translate-y-1/2 w-4 h-4 text-text-muted" strokeWidth={1.5} />
                  <input
                    type="password"
                    required
                    placeholder={t("settings.oldPasswordPlaceholder")}
                    value={oldPassword}
                    onChange={(e) => setOldPassword(e.target.value)}
                    className="w-full pl-9 pr-3.5 h-10 bg-background border border-line rounded-lg text-text-strong text-sm placeholder:text-text-muted focus:outline-none focus:border-primary/50"
                  />
                </div>
              </div>

              <div className="space-y-1">
                <label className="text-xs sm:text-sm text-text-muted">{t("settings.newPassword")}</label>
                <div className="relative">
                  <Lock className="absolute left-3 top-1/2 -translate-y-1/2 w-4 h-4 text-text-muted" strokeWidth={1.5} />
                  <input
                    type="password"
                    required
                    minLength={12}
                    maxLength={72}
                    placeholder={t("settings.newPasswordPlaceholder")}
                    value={newPassword}
                    onChange={(e) => setNewPassword(e.target.value)}
                    className="w-full pl-9 pr-3.5 h-10 bg-background border border-line rounded-lg text-text-strong text-sm placeholder:text-text-muted focus:outline-none focus:border-primary/50"
                  />
                </div>
                {/* 规则只此一处：与服务端 12–72 位同口径；/login 注册页用同一个键。 */}
                <p className="text-[11px] text-text-muted">{t("auth.registerPasswordHint")}</p>
              </div>

              <div className="space-y-1">
                <label className="text-xs sm:text-sm text-text-muted">{t("settings.confirmPassword")}</label>
                <div className="relative">
                  <Lock className="absolute left-3 top-1/2 -translate-y-1/2 w-4 h-4 text-text-muted" strokeWidth={1.5} />
                  <input
                    type="password"
                    required
                    minLength={12}
                    maxLength={72}
                    placeholder={t("settings.confirmPasswordPlaceholder")}
                    value={confirmPassword}
                    onChange={(e) => setConfirmPassword(e.target.value)}
                    className="w-full pl-9 pr-3.5 h-10 bg-background border border-line rounded-lg text-text-strong text-sm placeholder:text-text-muted focus:outline-none focus:border-primary/50"
                  />
                </div>
              </div>

              <button
                type="submit"
                disabled={submitting}
                className="w-full h-10 rounded-lg bg-primary text-white keep-white font-semibold text-sm flex items-center justify-center gap-2 hover:opacity-90 transition-opacity disabled:opacity-50 mt-1 shadow-xs"
              >
                {submitting ? <div className="w-4 h-4 rounded-full border-2 border-emphasis/30 border-t-white animate-spin" /> : t("settings.confirmChange")}
              </button>

              {/* 会话安全：「全部设备登出」原来只有 /account 一个入口（components/catalog/
                  CatalogPages.tsx），而该页在导航里已无入口，普通用户到不了。这里给同一语义的
                  入口（本页签就是安全与改密），服务端行为不变。 */}
              <div className="pt-3 mt-1 border-t border-line-subtle space-y-2">
                <div className="flex items-center gap-1.5">
                  <ShieldCheck className="w-3.5 h-3.5 text-text-muted" strokeWidth={1.5} />
                  <span className="text-xs font-semibold text-text-body">{t("settings.sessionsTitle")}</span>
                </div>
                <p className="text-[11px] text-text-faint leading-relaxed">{t("settings.sessionsDesc")}</p>
                {/* type=button：本按钮在改密表单内，但语义与表单无关，回车提交改密不受影响。 */}
                <button
                  type="button"
                  onClick={() => setPendingLogoutAll(true)}
                  disabled={signingOutAll}
                  className="w-full h-10 rounded-lg border border-red-500/30 bg-red-500/10 text-red-500 dark:text-danger-soft font-semibold text-sm flex items-center justify-center gap-2 hover:bg-red-500/[0.16] transition-colors disabled:opacity-50"
                >
                  {signingOutAll ? (
                    <div className="w-4 h-4 rounded-full border-2 border-red-400/30 border-t-red-400 animate-spin" />
                  ) : (
                    <>
                      <LogOut className="w-4 h-4" />
                      <span>{t("account.logoutAllDevices")}</span>
                    </>
                  )}
                </button>
              </div>
            </form>
          )}
        </TabPanel>
          </div>
        </div>
      </PageShell>

      <ConfirmDialog
        open={pendingLogoutAll}
        title={t("account.logoutAllDevices")}
        message={t("account.logoutAllConfirm")}
        confirmLabel={t("account.logoutAllDevices")}
        busy={signingOutAll}
        onClose={() => setPendingLogoutAll(false)}
        onConfirm={handleLogoutAll}
      />
    </div>
  );
}
