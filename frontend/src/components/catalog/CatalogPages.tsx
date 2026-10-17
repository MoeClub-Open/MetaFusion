"use client";
import { useState } from "react";
import { useI18n } from "@/i18n/I18nProvider";
import { ConfirmDialog } from "@/components/oauth/ConfirmDialog";
import { Card } from "@/components/ui/Card";
import { getAuthPasswordUrl } from "@/lib/services";
import { api } from "./api";
import { useAuth } from "@/lib/authContext";

// 登录入口唯一：/login，由网关交给 metafusion-auth/user（含注册页签与 /setup 初始化页）。
// 本页未登录时由 components/AuthGate.tsx 跳 /login?redirect=/account，不要在这里再渲染
// 用户名/密码表单——两套登录界面并存时字段提示与文案还不一致，用户会以为是两个站。
// 本页也不展示"已授权应用"：账号服务已整条删除 GET /oauth/clients（登录即可枚举全量客户端），
// 自助入口是 /settings 的 OAuthGrantsPanel（GET /auth/oauth-grants 只回本人），不在这里重复一份。
// 改密码同理：唯一实现在 /settings?tab=password（错误码四语映射、minLength、改密后重新登录引导都在那边），
// 本页只留一个入口链接；账号治理（成员/角色/权限组/邀请/OAuth 客户端）归独立控制台 /admin/account/。
export function Account() {
  const { t } = useI18n();
  // 会话只有 useAuth 一份：退出登录后由它的 refreshProfile 读回 /auth/me（401 即清会话）。
  const { user, refreshProfile } = useAuth();
  // 待确认的「全部设备登出」：确认框自绘（见文件尾），这里只记开关。
  const [pendingLogoutAll, setPendingLogoutAll] = useState(false);

  const logoutAll = async () => {
    // 这一页没有 busy 展示位：先关框再发请求，用户看到框关闭即表示动作已提交。
    setPendingLogoutAll(false);
    await api("/auth/logout-all", "POST");
    await refreshProfile();
  };

  // 未登录不渲染任何内容：AuthGate 已把未登录访问重定向到 /login?redirect=/account。
  if (!user) return null;

  return (
    <div className="space-y-4">
      <h1 className="font-display text-lg font-bold tracking-tight text-text-strong">{t("catalog.account")}</h1>

      {/* 资料卡：用户名 / 角色 / UUID 与两个会话动作 */}
      <Card padding="section">
        <div className="flex flex-col sm:flex-row sm:items-center sm:justify-between gap-3">
          <div className="min-w-0">
            <div className="flex flex-wrap items-center gap-2.5">
              <span className="text-xl font-bold text-text-strong break-all">{user.username}</span>
            </div>
            <p className="mt-1 text-xs font-mono text-text-muted break-all">UUID: {user.id}</p>
          </div>

          <div className="flex flex-wrap gap-2 shrink-0">
            <button
              type="button"
              onClick={async () => {
                await api("/auth/logout", "POST");
                await refreshProfile();
              }}
              className="px-3.5 h-9 rounded-control border border-line bg-surfaceSubtle hover:bg-surfaceHover text-text-body hover:text-emphasis text-xs font-medium transition-colors duration-fast ease-soft cursor-pointer"
            >
              {t("catalog.logout")}
            </button>
            <button
              type="button"
              onClick={() => setPendingLogoutAll(true)}
              className="px-3.5 h-9 rounded-control border border-danger/30 bg-danger/10 hover:bg-danger/[0.16] text-danger text-xs font-semibold transition-colors duration-fast ease-soft cursor-pointer"
            >
              {t("account.logoutAllDevices")}
            </button>
          </div>
        </div>
      </Card>

      {/* 账户与安全设置：改密码、授权自助、资料与外观都在 /settings，本页不再自带表单 */}
      <Card padding="section">
        <h2 className="text-sm font-semibold text-text-strong mb-1.5">{t("account.settingsTitle")}</h2>
        <p className="text-xs text-text-muted leading-relaxed mb-3">{t("account.settingsDesc")}</p>
        <a
          className="inline-flex items-center justify-center h-9 px-3.5 rounded-control bg-primary hover:bg-primary/90 text-primary-foreground text-xs font-semibold transition-colors duration-fast ease-soft"
          href={getAuthPasswordUrl()}
        >
          {t("account.changePassword")}
        </a>
      </Card>

      {/* 「全部设备登出」是破坏性动作：确认框自绘（原生 confirm 不可本地化、不可样式化），
          与 /settings 安全页签里的同一入口同形。 */}
      <ConfirmDialog
        open={pendingLogoutAll}
        title={t("account.logoutAllDevices")}
        message={t("account.logoutAllConfirm")}
        confirmLabel={t("account.logoutAllDevices")}
        onClose={() => setPendingLogoutAll(false)}
        onConfirm={logoutAll}
      />
    </div>
  );
}
