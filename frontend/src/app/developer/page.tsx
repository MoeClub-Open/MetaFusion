"use client";

// 开发者中心：把账号服务的"接入配置"和"我的应用"放在一个独立页面里（不再挤在管理台页签下）。
//
// 契约只读核对 metafusion-auth/internal/handler/developer.go：/api/developer/overview 给端点与
// scope 四语说明，/api/developer/apps* 给归属为当前账号的应用 CRUD。
// overview 仍回 platforms（owner_user_id 为空的系统应用），本页不渲染也不为它取数：
// 系统应用归管理后台的 OAuth 客户端页维护，这里只留一句说明与管理员入口。
// 归属与核验由服务端判定：这里只展示状态，不提供 trusted / verified 开关——
// 免同意是平台自己的身份，只能由管理员在管理台设置。

import { useCallback, useEffect, useState } from "react";
import {
  Activity,
  AppWindow,
  Check,
  Copy,
  ExternalLink,
  History,
  KeyRound,
  Loader2,
  Pencil,
  Plus,
  RefreshCw,
  RotateCcw,
  SlidersHorizontal,
  Trash2,
} from "lucide-react";
import { Navbar } from "@/components/Navbar";
import { PageContainer } from "@/components/ui/PageShell";
import { Card } from "@/components/ui/Card";
import { SectionTitle } from "@/components/ui/SectionTitle";
import { ConfirmDialog } from "@/components/oauth/ConfirmDialog";
import { SecretRevealModal, type SecretReveal } from "@/components/oauth/SecretRevealModal";
import { useI18n } from "@/i18n/I18nProvider";

import { DOCS_SERVICE_URL } from "@/lib/services";
import { copyText } from "@/lib/clipboard";
import {
  ENDPOINT_KEYS,
  appStatus,
  deleteApp,
  describeDeveloperError,
  fetchAccessConfig,
  fetchMyApps,
  rotateAppSecret,
  type AccessConfig,
  type DeveloperApp,
} from "@/lib/developer";
import { AppFormModal, type AppFormOutcome } from "./components/AppFormModal";
import { ApiKeysPanel } from "@/components/developer/ApiKeysPanel";
import { McpGrantsPanel } from "@/components/developer/McpGrantsPanel";
import { AuditLogsCard } from "./components/AuditLogsCard";
import { RequestLogsCard } from "./components/RequestLogsCard";

/** 待确认的破坏性动作：轮换与删除都先弹确认。 */
type Pending = { kind: "rotate" | "delete"; app: DeveloperApp };

/** 徽章色调：自有平台/已核验是"可信"语义，待核验是提醒，停用是失效。 */
const STATUS_CLASS: Record<string, string> = {
  first_party: "bg-indigo-500/10 border-indigo-500/30 text-alt",
  verified: "bg-emerald-500/10 border-emerald-500/30 text-success",
  unverified: "bg-amber-500/10 border-amber-500/30 text-warn",
  disabled: "bg-rose-500/10 border-rose-500/30 text-danger-soft",
};

export default function DeveloperPage() {
  const { t } = useI18n();
  const [config, setConfig] = useState<AccessConfig | null>(null);
  const [apps, setApps] = useState<DeveloperApp[]>([]);
  const [loading, setLoading] = useState(true);
  const [error, setError] = useState<string | null>(null);
  const [notice, setNotice] = useState<string | null>(null);
  const [creating, setCreating] = useState(false);
  const [editing, setEditing] = useState<DeveloperApp | null>(null);
  const [secret, setSecret] = useState<SecretReveal | null>(null);
  const [pending, setPending] = useState<Pending | null>(null);
  const [busy, setBusy] = useState(false);
  const [copied, setCopied] = useState<string | null>(null);

  // 左侧选项、右侧切换：四个自助区共用一页，窄屏时侧栏横排置顶。
  const TABS = [
    { key: "apps", label: t("developer.apps.title"), icon: AppWindow },
    { key: "keys", label: t("developer.credentials.title"), icon: KeyRound },
    { key: "audit", label: t("developer.audit.title"), icon: History },
    { key: "logs", label: t("developer.reqlogs.title"), icon: Activity },
  ] as const;
  const [tab, setTab] = useState<(typeof TABS)[number]["key"]>("apps");
  const [keyModalOpen, setKeyModalOpen] = useState(false);

  const load = useCallback(async () => {
    setLoading(true);
    try {
      const [nextConfig, nextApps] = await Promise.all([fetchAccessConfig(), fetchMyApps()]);
      setConfig(nextConfig);
      setApps(nextApps);
      setError(null);
    } catch (err) {
      setConfig(null);
      setApps([]);
      setError(describeDeveloperError(err, t));
    } finally {
      setLoading(false);
    }
  }, [t]);

  useEffect(() => {
    void load();
  }, [load]);

  useEffect(() => {
    if (window.location.hash === "#credentials") setTab("keys");
  }, []);

  const copy = async (key: string, value: string) => {
    setCopied((await copyText(value)) ? key : "failed");
  };

  const onSaved = (outcome: AppFormOutcome) => {
    setCreating(false);
    setEditing(null);
    if (outcome.secret) {
      setSecret({ clientId: outcome.app.client_id, secret: outcome.secret, mode: "created" });
    }
    setNotice(
      outcome.mode === "created"
        ? t("developer.apps.created", { client_id: outcome.app.client_id })
        : t("developer.apps.updated", { client_id: outcome.app.client_id }),
    );
    void load();
  };

  const runPending = async () => {
    if (!pending) return;
    const clientId = pending.app.client_id;
    setBusy(true);
    setError(null);
    try {
      if (pending.kind === "rotate") {
        const res = await rotateAppSecret(clientId);
        setSecret({ clientId: res.app.client_id, secret: res.client_secret, mode: "rotated" });
        setNotice(t("developer.apps.rotated", { client_id: clientId }));
      } else {
        await deleteApp(clientId);
        setNotice(t("developer.apps.deleted", { client_id: clientId }));
      }
      await load();
    } catch (err) {
      setError(describeDeveloperError(err, t));
    } finally {
      setPending(null);
      setBusy(false);
    }
  };

  const confirmText = (() => {
    if (!pending) return null;
    const vars = { client_id: pending.app.client_id };
    if (pending.kind === "rotate") {
      return {
        title: t("developer.apps.rotateTitle"),
        message: t("developer.apps.rotateConfirm", vars),
        label: t("developer.apps.rotate"),
      };
    }
    return {
      title: t("developer.apps.deleteTitle"),
      message: t("developer.apps.deleteConfirm", vars),
      label: t("developer.apps.confirmDelete"),
    };
  })();

  const actionClass =
    "px-2 py-1 rounded-md bg-surfaceSubtle hover:bg-surfaceHover border border-line text-text-body hover:text-text-strong text-[11px] transition-colors duration-fast ease-soft cursor-pointer inline-flex items-center gap-1";

  const badge = (app: DeveloperApp) => {
    const status = appStatus(app);
    const label =
      status === "first_party"
        ? t("developer.badge.firstParty")
        : status === "verified"
          ? t("developer.badge.verified")
          : status === "unverified"
            ? t("developer.badge.unverified")
            : t("developer.badge.disabled");
    return (
      <span className={`px-1.5 py-0.5 rounded-chip border text-[10px] font-mono ${STATUS_CLASS[status]}`}>
        {label}
      </span>
    );
  };

  return (
    <>
      <Navbar />
      <PageContainer className="py-6 space-y-4">
        <div className="flex flex-wrap items-start justify-between gap-3">
          <div>
            <h1 className="text-xl sm:text-2xl font-semibold text-text-strong">{t("developer.title")}</h1>
          </div>
          <div className="flex items-center gap-2">
            <button type="button" onClick={() => setTab("keys")} className="px-3 py-1.5 rounded-lg bg-surfaceSubtle hover:bg-surfaceHover border border-line text-text-body text-[11px] transition-colors cursor-pointer">
              {t("developer.mcp.manage")}
            </button>
            <a
              href={`${DOCS_SERVICE_URL}/api-overview`}
              target="_blank"
              rel="noreferrer"
              className="px-3 py-1.5 rounded-lg bg-surfaceSubtle hover:bg-surfaceHover border border-line text-text-body text-[11px] transition-colors duration-fast ease-soft inline-flex items-center gap-1.5"
            >
              <ExternalLink className="w-3.5 h-3.5" />
              <span>{t("developer.docsLink")}</span>
            </a>
            <button
              type="button"
              onClick={() => void load()}
              title={t("developer.reload")}
              className="p-2 rounded-lg bg-surfaceSubtle hover:bg-surfaceHover border border-line text-text-body transition-colors duration-fast ease-soft cursor-pointer"
            >
              <RefreshCw className={"w-3.5 h-3.5" + (loading ? " animate-spin text-primary" : "")} />
            </button>
          </div>
        </div>

        {error ? (
          <div className="p-3 rounded-xl bg-rose-500/10 border border-rose-500/30 text-danger-soft text-xs leading-relaxed">{error}</div>
        ) : null}
        {notice ? (
          <div className="p-3 rounded-xl bg-emerald-500/10 border border-emerald-500/30 text-success-soft text-xs leading-relaxed">{notice}</div>
        ) : null}

        {loading && !config ? (
          <div className="py-8 text-center text-xs text-text-faint font-mono flex items-center justify-center gap-2">
            <Loader2 className="w-4 h-4 animate-spin text-primary" />
            <span>{t("developer.loading")}</span>
          </div>
        ) : null}

        {config ? (
          <div className="grid grid-cols-1 md:grid-cols-[220px_minmax(0,1fr)] gap-4 items-start">
            <nav aria-label={t("developer.title")} className="flex md:flex-col gap-1 overflow-x-auto rounded-xl border border-line-subtle bg-surface p-1.5 md:sticky md:top-20">
              {TABS.map((item) => {
                const Icon = item.icon;
                const active = tab === item.key;
                return (
                  <button
                    key={item.key}
                    type="button"
                    onClick={() => setTab(item.key)}
                    aria-current={active ? "page" : undefined}
                    className={`flex items-center gap-2 px-3 py-2 rounded-lg text-left transition-colors duration-fast ease-soft cursor-pointer ${active ? "bg-primary/10 text-primary font-semibold" : "text-text-muted hover:text-emphasis hover:bg-surfaceHover"}`}
                  >
                    <Icon className="w-4 h-4 shrink-0" strokeWidth={1.8} />
                    <span className="whitespace-nowrap text-xs font-medium">{item.label}</span>
                  </button>
                );
              })}
            </nav>
            <div className="min-w-0">
              {tab === "apps" && (
                <div className="space-y-4">
              <Card padding="section" className="space-y-3">
                <SectionTitle
                  icon={<KeyRound className="w-4 h-4 text-primary" />}
                  actions={
                    <button
                      type="button"
                      onClick={() => setCreating(true)}
                      className="px-3 py-1.5 rounded-lg bg-primary hover:bg-primary/90 text-white text-[11px] font-semibold transition-colors duration-fast ease-soft cursor-pointer inline-flex items-center gap-1.5"
                    >
                      <Plus className="w-3.5 h-3.5" />
                      <span>{t("developer.apps.new")}</span>
                    </button>
                  }
                >
                  {t("developer.apps.title")}
                </SectionTitle>
                {apps.length === 0 ? (
                  <div className="p-8 rounded-xl border border-dashed border-line text-center text-xs text-text-faint font-mono">
                    {t("developer.apps.empty")}
                  </div>
                ) : (
                  <div className="rounded-xl border border-line-subtle overflow-x-auto" data-mf-developer-apps="">
                    <table className="w-full text-left text-xs border-collapse">
                      <thead>
                        <tr className="border-b border-line-subtle bg-surfaceSubtle text-text-muted font-mono text-[11px]">
                          <th className="py-2 px-3 font-medium">{t("developer.colName")}</th>
                          <th className="py-2 px-3 font-medium">{t("developer.colClientId")}</th>
                          <th className="py-2 px-3 font-medium">{t("developer.colRedirects")}</th>
                          <th className="py-2 px-3 font-medium">{t("developer.colScopes")}</th>
                          <th className="py-2 px-3 font-medium">{t("developer.colStatus")}</th>
                          <th className="py-2 px-3 font-medium text-right">{t("developer.colActions")}</th>
                        </tr>
                      </thead>
                      <tbody className="divide-y divide-line-subtle">
                        {apps.map((app) => (
                          <tr key={app.client_id} data-mf-developer-row={app.client_id} className="hover:bg-surfaceSubtle align-top">
                            <td className="py-2.5 px-3 text-text-body">
                              <div>{app.name || "—"}</div>
                              {app.description ? <div className="text-[10px] text-text-faint">{app.description}</div> : null}
                            </td>
                            <td className="py-2.5 px-3 font-mono text-[11px] text-text-strong break-all">{app.client_id}</td>
                            <td className="py-2.5 px-3">
                              {(app.redirect_uris || []).length === 0 ? (
                                <span className="font-mono text-[10px] text-danger">{t("developer.apps.noRedirects")}</span>
                              ) : (
                                <div className="space-y-0.5">
                                  {(app.redirect_uris || []).map((uri) => (
                                    <div key={uri} className="font-mono text-[10px] text-text-muted break-all">
                                      {uri}
                                    </div>
                                  ))}
                                </div>
                              )}
                            </td>
                            <td className="py-2.5 px-3">
                              <div className="flex flex-wrap gap-1">
                                {(app.scopes || []).map((code) => (
                                  <span
                                    key={code}
                                    className="px-1 py-0.5 rounded-chip bg-surfaceSubtle border border-line text-[10px] font-mono text-text-muted"
                                  >
                                    {code}
                                  </span>
                                ))}
                              </div>
                            </td>
                            <td className="py-2.5 px-3">{badge(app)}</td>
                            <td className="py-2.5 px-3">
                              <div className="flex flex-wrap gap-1 justify-end">
                                <button type="button" onClick={() => setEditing(app)} className={actionClass}>
                                  <Pencil className="w-3 h-3" />
                                  <span>{t("developer.apps.edit")}</span>
                                </button>
                                <button type="button" onClick={() => setPending({ kind: "rotate", app })} className={actionClass}>
                                  <RotateCcw className="w-3 h-3" />
                                  <span>{t("developer.apps.rotate")}</span>
                                </button>
                                <button type="button" onClick={() => setPending({ kind: "delete", app })} className={actionClass}>
                                  <Trash2 className="w-3 h-3" />
                                  <span>{t("developer.apps.delete")}</span>
                                </button>
                              </div>
                            </td>
                          </tr>
                        ))}
                      </tbody>
                    </table>
                  </div>
                )}
                <p className="text-[11px] text-text-faint leading-relaxed">{t("developer.apps.verifiedHint")}</p>
              </Card>
            <details className="rounded-xl border border-line-subtle bg-surface px-3.5 py-2.5">
              <summary className="text-xs text-text-muted cursor-pointer inline-flex items-center gap-1.5">
                <SlidersHorizontal className="w-3.5 h-3.5 text-primary" />
                {t("developer.access.title")}
              </summary>
              <div className="mt-2 rounded-lg border border-line-subtle overflow-hidden">
                <table className="w-full text-left text-xs border-collapse">
                  <tbody className="divide-y divide-line-subtle">
                    <tr>
                      <td className="py-2 px-3 text-text-muted whitespace-nowrap">issuer</td>
                      <td className="py-2 px-3 font-mono text-[11px] text-text-strong break-all">{config.issuer}</td>
                      <td className="py-2 px-3 text-right w-16">
                        <button type="button" onClick={() => void copy("issuer", config.issuer)} className={actionClass}>
                          {copied === "issuer" ? <Check className="w-3 h-3" /> : <Copy className="w-3 h-3" />}
                        </button>
                      </td>
                    </tr>
                    {ENDPOINT_KEYS.map((key) => {
                      const value = config.endpoints[key] || "";
                      return (
                        <tr key={key}>
                          <td className="py-2 px-3 text-text-muted whitespace-nowrap font-mono">{key}</td>
                          <td className="py-2 px-3 font-mono text-[11px] text-text-body break-all">{value}</td>
                          <td className="py-2 px-3 text-right w-16">
                            <button type="button" onClick={() => void copy(key, value)} className={actionClass}>
                              {copied === key ? <Check className="w-3 h-3" /> : <Copy className="w-3 h-3" />}
                            </button>
                          </td>
                        </tr>
                      );
                    })}
                  </tbody>
                </table>
              </div>
              {copied === "failed" ? <p className="mt-1 text-[11px] text-warn">{t("developer.copyFailed")}</p> : null}
            </details>
                </div>
              )}
              {tab === "keys" && (
              <div className="space-y-4">
              <Card padding="section" className="space-y-3">
                <SectionTitle
                  icon={<KeyRound className="w-4 h-4 text-primary" />}
                  actions={
                    <span className="inline-flex items-center gap-2">
                      <a
                        href={`${DOCS_SERVICE_URL}/api-auth`}
                        target="_blank"
                        rel="noreferrer"
                        className="text-[11px] text-primary hover:underline"
                      >
                        {t("developer.apiKeyViewDevDocs")}
                      </a>
                      <button
                        type="button"
                        onClick={() => setKeyModalOpen(true)}
                        className="px-3 py-1.5 rounded-lg bg-primary hover:bg-primary/90 text-white text-[11px] font-semibold transition-colors duration-fast ease-soft cursor-pointer inline-flex items-center gap-1.5"
                      >
                        <Plus className="w-3.5 h-3.5" />
                        <span>{t("developer.apiKeys.new")}</span>
                      </button>
                    </span>
                  }
                >
                  {t("developer.apiKeys.title")}
                </SectionTitle>
                <p className="text-xs text-text-muted leading-relaxed">{t("developer.apiKeys.subtitle")}</p>
                <ApiKeysPanel modalOpen={keyModalOpen} onModalClose={() => setKeyModalOpen(false)} />
              </Card>
              <Card padding="section" className="space-y-3">
                <SectionTitle icon={<KeyRound className="w-4 h-4 text-primary" />}>
                  {t("developer.mcp.manage")}
                </SectionTitle>
                <McpGrantsPanel />
              </Card>
              </div>
              )}
              {tab === "audit" && <AuditLogsCard apps={apps} />}
              {tab === "logs" && <RequestLogsCard />}
            </div>
          </div>
        ) : null}
      </PageContainer>

      <AppFormModal
        open={creating || !!editing}
        app={editing}
        scopes={config?.scopes ?? []}
        onClose={() => {
          setCreating(false);
          setEditing(null);
        }}
        onSaved={onSaved}
      />
      <SecretRevealModal reveal={secret} onClose={() => setSecret(null)} />
      {confirmText ? (
        <ConfirmDialog
          open
          title={confirmText.title}
          message={confirmText.message}
          confirmLabel={confirmText.label}
          busy={busy}
          onClose={() => setPending(null)}
          onConfirm={() => void runPending()}
        />
      ) : null}
    </>
  );
}
