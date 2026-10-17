"use client";

import { useCallback, useEffect, useMemo, useRef, useState } from "react";
import Link from "next/link";
import { ArrowLeft, RefreshCw, Shield } from "lucide-react";
import { useAuth } from "@/lib/authContext";
import { useI18n } from "@/i18n/I18nProvider";
import { useTheme } from "@/lib/themeContext";
import { getAuthLoginUrl } from "@/lib/services";
import { PageContainer } from "@/components/ui/PageShell";
import { LocaleSwitcher } from "@/components/LocaleSwitcher";
import { ThemeModeSwitcher } from "@/components/ThemeModeSwitcher";
import { ThemePicker } from "@/components/ThemePicker";
import {
  ADMIN_WORKSPACE_CHANNEL, SERVICE_WORKSPACES, WORKSPACE_BASES, WORKSPACE_LABELS,
  adminWorkspaceUrl, parseAdminFrameMessage, permittedAdminWorkspaces, readAdminWorkspace,
  type AdminWorkspace, type ServiceWorkspace,
} from "@/lib/adminWorkspace";
import { CatalogWorkbench } from "./CatalogWorkbench";

type Health = "checking" | "online" | "offline";

export default function AdminPage() {
  const { user, loading } = useAuth();
  const { t } = useI18n();
  const permitted = useMemo(() => permittedAdminWorkspaces(user), [user]);
  const [location, setLocation] = useState<ReturnType<typeof readAdminWorkspace> | null>(null);
  const [health, setHealth] = useState<Partial<Record<ServiceWorkspace, Health>>>({});
  const [probe, setProbe] = useState(0);
  const [visited, setVisited] = useState<AdminWorkspace[]>([]);
  const remembered = useRef<Partial<Record<AdminWorkspace, string>>>({});
  const active = location?.workspace && permitted.includes(location.workspace) ? location.workspace : permitted[0];
  const permittedKey = permitted.join(",");

  useEffect(() => {
    const sync = () => {
      const next = readAdminWorkspace(window.location.search);
      if (!next.workspace || next.workspace === "catalog") remembered.current.catalog = window.location.hash;
      setLocation(next);
    };
    sync();
    window.addEventListener("popstate", sync);
    window.addEventListener("hashchange", sync);
    return () => { window.removeEventListener("popstate", sync); window.removeEventListener("hashchange", sync); };
  }, []);

  useEffect(() => {
    if (!active || !location) return;
    setVisited((previous) => previous.includes(active) ? previous : [...previous, active]);
    if (location.workspace !== active) {
      window.history.replaceState(window.history.state, "", adminWorkspaceUrl(active, remembered.current[active]));
      setLocation({ workspace: active, href: active === "catalog" ? undefined : remembered.current[active] ?? WORKSPACE_BASES[active] });
    }
  }, [active, location]);

  useEffect(() => {
    const services = SERVICE_WORKSPACES.filter((id) => permitted.includes(id));
    if (!services.length) return;
    const controller = new AbortController();
    const timer = window.setTimeout(() => controller.abort(), 2500);
    let alive = true;
    setHealth((previous) => Object.fromEntries(services.map((id) => [id, previous[id] === "online" ? "online" : "checking"])));
    void Promise.all(services.map(async (id) => {
      try {
        const response = await fetch(`${WORKSPACE_BASES[id]}api/health`, { credentials: "same-origin", cache: "no-store", signal: controller.signal });
        return [id, response.status === 200 ? "online" : "offline"] as const;
      } catch { return [id, "offline"] as const; }
    })).then((entries) => { window.clearTimeout(timer); if (alive) setHealth(Object.fromEntries(entries)); });
    return () => { alive = false; window.clearTimeout(timer); controller.abort(); };
  }, [permittedKey, probe]);

  const selectWorkspace = (workspace: AdminWorkspace) => {
    if (workspace === active) return;
    if (active === "catalog") remembered.current.catalog = window.location.hash;
    const url = adminWorkspaceUrl(workspace, remembered.current[workspace]);
    window.history.pushState(window.history.state, "", url);
    setLocation(readAdminWorkspace(new URL(url, window.location.origin).search));
  };
  const rememberNavigation = useCallback((workspace: ServiceWorkspace, href: string, push: boolean) => {
    remembered.current[workspace] = href;
    const current = readAdminWorkspace(window.location.search);
    if (current.workspace !== workspace) return;
    // 嵌入应用使用 replace；只有用户导航由宿主创建一次历史项。
    const url = adminWorkspaceUrl(workspace, href);
    if (push && window.location.pathname + window.location.search !== url) window.history.pushState(window.history.state, "", url);
    else window.history.replaceState(window.history.state, "", url);
    setLocation({ workspace, href });
  }, []);

  if (loading || !location) return <div className="min-h-screen grid place-items-center text-sm text-text-muted">{t("admin.console.checkingPerms")}</div>;
  if (!user || !active) return <div className="min-h-screen flex flex-col items-center justify-center gap-4 px-6 text-center">
    <Shield className="h-8 w-8 text-danger" />
    <h1 className="text-xl font-semibold">{t("admin.console.permRequired")}</h1>
    <p className="max-w-md text-sm text-text-muted">{t("admin.console.permDesc")}</p>
    <Link href="/home" className="text-sm text-primary">{t("admin.console.backToSite")}</Link>
  </div>;

  return <div className="min-h-screen text-text-strong">
    <header className="sticky top-0 z-30 border-b border-line bg-surface/95 backdrop-blur">
      <PageContainer className="flex h-14 items-center justify-between gap-2">
        <div className="flex min-w-0 items-center gap-2 sm:gap-3">
          <Link href="/home" aria-label={t("admin.console.backToSite")} title={t("admin.console.backToSite")} className="flex h-11 w-11 shrink-0 items-center justify-center gap-1 text-xs text-text-muted hover:text-text-strong sm:w-auto">
            <ArrowLeft className="h-4 w-4" /><span className="hidden sm:inline">{t("admin.console.backToSite")}</span>
          </Link>
          <span className="hidden text-text-faint sm:inline">/</span>
          <h1 className="truncate text-sm font-semibold">{t("admin.workspace.title")}</h1>
        </div>
        <div className="flex shrink-0 items-center gap-2">
          <LocaleSwitcher compact /><ThemeModeSwitcher /><ThemePicker />
          <span className="hidden max-w-40 truncate rounded border border-primary/30 bg-primary/20 px-2 py-0.5 font-mono text-xs text-primary sm:inline-flex">{user.username}</span>
        </div>
      </PageContainer>
      <PageContainer>
        <nav aria-label={t("admin.workspace.switch")} className="flex h-12 gap-1 overflow-x-auto">
          {permitted.map((workspace) => <button key={workspace} type="button" aria-current={active === workspace ? "page" : undefined} onClick={() => selectWorkspace(workspace)} className={`shrink-0 whitespace-nowrap border-b-2 px-3 text-sm transition-colors ${active === workspace ? "border-primary font-semibold text-primary" : "border-transparent text-text-muted hover:text-text-strong"}`}>
            {t(WORKSPACE_LABELS[workspace])}
          </button>)}
        </nav>
      </PageContainer>
    </header>

    {permitted.filter((workspace) => visited.includes(workspace)).map((workspace) => <div key={workspace} id={`admin-workspace-${workspace}`} hidden={active !== workspace}>
      {workspace === "catalog" ? <CatalogWorkbench active={active === "catalog"} /> : health[workspace] === "online" ? <ServiceWorkspaceFrame
        workspace={workspace} active={active === workspace}
        href={(location.workspace === workspace ? location.href : undefined) ?? remembered.current[workspace] ?? WORKSPACE_BASES[workspace]}
        onNavigate={rememberNavigation}
      /> : <PageContainer className="py-12">
        <div role="status" className="rounded-xl border border-line bg-surfaceSubtle p-6 text-sm text-text-muted">
          <p>{t(health[workspace] === "offline" ? "admin.workspace.unavailable" : "admin.workspace.connecting")}</p>
          {health[workspace] === "offline" ? <button type="button" onClick={() => setProbe((value) => value + 1)} className="mt-4 inline-flex items-center gap-2 text-primary"><RefreshCw className="h-4 w-4" />{t("admin.workspace.retry")}</button> : null}
        </div>
      </PageContainer>}
    </div>)}
  </div>;
}

function ServiceWorkspaceFrame({ workspace, active, href, onNavigate }: {
  workspace: ServiceWorkspace; active: boolean; href: string;
  onNavigate: (workspace: ServiceWorkspace, href: string, push: boolean) => void;
}) {
  const { t, locale } = useI18n();
  const { resolvedMode, accent, tone } = useTheme();
  const frame = useRef<HTMLIFrameElement>(null);
  const initialHref = useRef(href);
  const currentHref = useRef(href);
  const activeRef = useRef(active);
  activeRef.current = active;
  const [ready, setReady] = useState(false);
  const [failed, setFailed] = useState(false);
  const [attempt, setAttempt] = useState(0);
  const preferences = useRef({ locale, mode: resolvedMode, accent, tone });
  preferences.current = { locale, mode: resolvedMode, accent, tone };
  const sendPreferences = useCallback(() => {
    frame.current?.contentWindow?.postMessage({ channel: ADMIN_WORKSPACE_CHANNEL, workspace, type: "preferences", ...preferences.current }, window.location.origin);
  }, [workspace]);

  useEffect(() => {
    const receive = (event: MessageEvent) => {
      if (event.origin !== window.location.origin || event.source !== frame.current?.contentWindow) return;
      const message = parseAdminFrameMessage(workspace, event.data);
      if (!message) return;
      if (message.type === "login") {
        if (activeRef.current) window.location.assign(getAuthLoginUrl(window.location.pathname + window.location.search + window.location.hash));
        return;
      }
      currentHref.current = message.href;
      setReady(true); setFailed(false);
      sendPreferences();
      onNavigate(workspace, message.href, message.push);
    };
    window.addEventListener("message", receive);
    return () => window.removeEventListener("message", receive);
  }, [workspace, onNavigate, sendPreferences]);

  useEffect(() => { if (ready) sendPreferences(); }, [ready, locale, resolvedMode, accent, tone, sendPreferences]);
  useEffect(() => {
    if (!active || !ready || currentHref.current === href) return;
    // 后退或直达另一页面时只刷新内容区，管理中心的顶栏与其他工作区保持挂载。
    currentHref.current = href;
    setReady(false); setFailed(false);
    frame.current?.contentWindow?.location.replace(href);
  }, [active, href, ready]);
  useEffect(() => {
    if (ready) return;
    const timer = window.setTimeout(() => setFailed(true), 12000);
    return () => window.clearTimeout(timer);
  }, [ready, attempt]);

  return <div className="relative h-[calc(100dvh-6.5rem)] min-h-[28rem]">
    {!ready ? <div role="status" className="absolute inset-0 z-10 flex flex-col items-center justify-center gap-4 bg-background px-6 text-center text-sm text-text-muted">
      <p>{t(failed ? "admin.workspace.noResponse" : "admin.workspace.connecting")}</p>
      {failed ? <div className="flex flex-wrap justify-center gap-4">
        <button type="button" className="text-primary" onClick={() => { initialHref.current = href; setFailed(false); setAttempt((value) => value + 1); }}>{t("admin.workspace.retry")}</button>
        <a href={href} target="_blank" rel="noopener noreferrer" className="text-primary">{t("admin.workspace.openStandalone")}</a>
      </div> : null}
    </div> : null}
    <iframe key={attempt} ref={frame} name={`metafusion-admin-${workspace}`} src={initialHref.current} title={t(WORKSPACE_LABELS[workspace])}
      onLoad={() => frame.current?.contentWindow?.postMessage({ channel: ADMIN_WORKSPACE_CHANNEL, workspace, type: "connect" }, window.location.origin)}
      className={`h-full w-full border-0 ${ready ? "" : "invisible"}`} onError={() => { setReady(false); setFailed(true); }} />
  </div>;
}
