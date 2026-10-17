"use client";

import React, { useState, useEffect, useRef, useCallback } from "react";
import Link from "next/link";
import { usePathname, useRouter } from "next/navigation";
import { useAuth } from "@/lib/authContext";
import { LocaleSwitcher } from "./LocaleSwitcher";
import { ThemePicker } from "./ThemePicker";
import { ThemeModeSwitcher } from "./ThemeModeSwitcher";
import { useI18n } from "@/i18n/I18nProvider";
import { BrandMark } from "./Logo";
import { UserAvatar } from "./UserAvatar";
import { displayNameOf, fetchUnreadMessageCount, fetchUnreadCount, NOTIFICATIONS_CHANGED_EVENT } from "@/lib/api";
import { canEnterAdmin } from "@/lib/permissions";
import { getAuthLoginUrl, STORAGE_SERVICE_URL, hasResourceStation } from "@/lib/services";
import { PageContainer } from "@/components/ui/PageShell";
import { SearchSuggest } from "@/components/common/SearchSuggest";
import { Modal } from "@/components/ui/Modal";
import {
  Bell,
  Plus,
  LogOut,
  User as UserIcon,
  Shield,
  Settings,
  ChevronDown,
  Library,
  Compass,
  BookOpen,
  DownloadCloud,
  MessageSquare,
  Terminal,
  Mail,
  Menu,
  Search,
} from "lucide-react";

/**
 * 导航项的 active 判定：桌面顶栏与移动行共用这一份，避免两处漂移
 * （移动行曾用 pathname === href，桌面用 startsWith，于是 /community/<id>、
 * 实体详情这类子路径在移动端一个页签都不高亮）。
 * external 项不属于本应用路由（文档站前缀、外站资源站），不参与判定。
 */
// 顶栏角标的最短刷新间隔：可见性切换/其它触发都受它限制，杜绝高频轮询。
const NOTIFICATION_BADGE_MIN_INTERVAL_MS = 60000;

function isNavLinkActive(
  pathname: string,
  tab: { href: string; exact?: boolean; external?: boolean },
): boolean {
  if (tab.external) return false;
  return tab.exact ? pathname === tab.href : pathname.startsWith(tab.href);
}

export const Navbar: React.FC<{
  searchQuery?: string;
  onSearch?: (query: string) => void;
}> = ({ searchQuery = "", onSearch }) => {
  const { user, logout } = useAuth();
  const { t } = useI18n();
  const pathname = usePathname();
  const router = useRouter();
  const [query, setQuery] = useState(searchQuery);
  useEffect(() => {
    setQuery(searchQuery);
  }, [pathname, searchQuery]);

  const [isUserMenuOpen, setIsUserMenuOpen] = useState(false);
  const [mobilePanel, setMobilePanel] = useState<"navigation" | "search" | null>(null);
  const closeMobilePanel = useCallback(() => setMobilePanel(null), []);
  useEffect(() => {
    setMobilePanel(null);
    setIsUserMenuOpen(false);
  }, [pathname]);
  useEffect(() => {
    const desktop = window.matchMedia("(min-width: 768px)");
    const closeOnDesktop = () => { if (desktop.matches) setMobilePanel(null); };
    desktop.addEventListener("change", closeOnDesktop);
    return () => desktop.removeEventListener("change", closeOnDesktop);
  }, []);
  // 登录链接的回跳地址依赖 window.location.href，服务端渲染时只能取 "/home" 兜底：
  // 两边各算各的会让水合报 href 属性不匹配。首帧与服务端同值，挂载后再换真实地址。
  const [loginHref, setLoginHref] = useState(() => getAuthLoginUrl("/home"));
  useEffect(() => {
    setLoginHref(getAuthLoginUrl());
  }, [pathname]);
  // 未读私信角标：登录后拉一次 + 每 30s 一次。失败一律隐藏角标（null），**不渲染成 0**——
  // "取不到"与"没有未读"必须能区分；失败也不影响导航其余部分。
  const [unreadCount, setUnreadCount] = useState<number | null>(null);
  // 未读站内通知角标：与私信不同，这条**不轮询**——挂载后取一次、页面重新可见时取一次
  // （最短间隔见 NOTIFICATION_BADGE_MIN_INTERVAL_MS），通知页改完已读会广播事件让它立刻跟一次。
  // 失败一律静默且保留旧值（null = 不显示角标），"取不到"不画成 0，也不弄脏顶栏。
  const [unreadNotifications, setUnreadNotifications] = useState<number | null>(null);
  const userMenuRef = useRef<HTMLDivElement>(null);
  const headerRef = useRef<HTMLElement>(null);
  // 顶栏实际高度随断点变化：平板宽度保留一行导航，其余断点仅主行。
  // 这里把实测高度写进 --mf-header-h，吸顶元素（管理台内栏、社区工具条、首页筛选条）
  // 才不会被顶栏压住；globals.css 保留同口径静态值，供无 Navbar 的页面与首帧使用。
  useEffect(() => {
    const el = headerRef.current;
    if (!el) return;
    const publish = () =>
      document.documentElement.style.setProperty("--mf-header-h", `${el.offsetHeight}px`);
    publish();
    const observer = new ResizeObserver(publish);
    observer.observe(el);
    return () => {
      observer.disconnect();
      document.documentElement.style.removeProperty("--mf-header-h");
    };
  }, []);
  useEffect(() => {
    function handleClickOutside(e: MouseEvent) {
      if (userMenuRef.current && !userMenuRef.current.contains(e.target as Node)) {
        setIsUserMenuOpen(false);
      }
    }
    document.addEventListener("mousedown", handleClickOutside);
    return () => document.removeEventListener("mousedown", handleClickOutside);
  }, []);

  useEffect(() => {
    if (!user) {
      setUnreadCount(null);
      return;
    }
    let alive = true;
    const load = () => {
      fetchUnreadMessageCount()
        .then((n) => {
          if (alive) setUnreadCount(n);
        })
        .catch(() => {
          if (alive) setUnreadCount(null);
        });
    };
    load();
    const timer = window.setInterval(load, 30000);
    return () => {
      alive = false;
      window.clearInterval(timer);
    };
  }, [user]);

  // 打开用户菜单时顺手刷新一次：下拉里的角标不该停在最多 30 秒前的旧值。
  useEffect(() => {
    if (!user || !isUserMenuOpen) return;
    let alive = true;
    fetchUnreadMessageCount()
      .then((n) => {
        if (alive) setUnreadCount(n);
      })
      .catch(() => {
        if (alive) setUnreadCount(null);
      });
    return () => {
      alive = false;
    };
  }, [user, isUserMenuOpen]);

  useEffect(() => {
    if (!user) {
      setUnreadNotifications(null);
      return;
    }
    let alive = true;
    let lastFetchedAt = 0;
    const load = (force: boolean) => {
      // 成功才记时间戳：失败不占这个窗口，下一次可见/变更时还能立刻再试一次。
      if (!force && Date.now() - lastFetchedAt < NOTIFICATION_BADGE_MIN_INTERVAL_MS) return;
      fetchUnreadCount()
        .then((n) => {
          if (!alive) return;
          lastFetchedAt = Date.now();
          setUnreadNotifications(n);
        })
        .catch(() => {
          /* 静默：角标接口挂了不影响顶栏渲染，也不弹错 */
        });
    };
    load(true);
    const onVisibilityChange = () => {
      if (document.visibilityState === "visible") load(false);
    };
    // 通知页标记已读后广播：这是明确的用户动作，不受最短间隔限制。
    const onNotificationsChanged = () => load(true);
    document.addEventListener("visibilitychange", onVisibilityChange);
    window.addEventListener(NOTIFICATIONS_CHANGED_EVENT, onNotificationsChanged);
    return () => {
      alive = false;
      document.removeEventListener("visibilitychange", onVisibilityChange);
      window.removeEventListener(NOTIFICATIONS_CHANGED_EVENT, onNotificationsChanged);
    };
  }, [user]);

  const navLinks = [
    { href: "/home", label: t("navigation.home"), icon: Library, exact: true },
    { href: "/explore", label: t("navigation.explore"), icon: Compass },
    { href: "/community", label: t("navigation.community"), icon: MessageSquare },
    // 资源站未接入时不展示入口，避免死链。
    ...(hasResourceStation()
      ? [{ href: STORAGE_SERVICE_URL, label: t("navigation.resources"), icon: DownloadCloud, external: true }]
      : []),
    { href: "/docs/catalog", label: t("navigation.docs"), icon: BookOpen, external: true },
  ];

  return (
    <>
    <header ref={headerRef} className="sticky top-0 z-40 w-full border-b border-line-subtle bg-surface/85 backdrop-blur-xl supports-[backdrop-filter]:bg-surface/85">
      <PageContainer className="h-14 md:h-15 flex items-center justify-between gap-x-2 sm:gap-x-3">
        {/* Left Brand + Navigation */}
        <div className="flex items-center gap-1 md:gap-4 shrink-0">
          {/* 品牌名 span 在 <640px 被隐藏，只剩 26×26 的图形：title 只是兜底名，
              显式 aria-label 才是稳定可访问名（屏幕阅读器与自动化都以它为准）。 */}
          <Link href="/" aria-label={t("navbar.about")} title={t("navbar.about")} className="flex items-center gap-2.5 shrink-0 group">
            <BrandMark size={26} withGlow={false} idSuffix="nav" />
            <span className="hidden sm:flex flex-col leading-none">
              <span className="font-display text-[20px] leading-none tracking-[-0.03em] text-emphasis group-hover:text-primary transition-colors duration-fast ease-soft">
                MetaFusion
              </span>
              <span className="hidden sm:inline font-mono text-[7px] tracking-[0.16em] text-text-faint leading-none mt-[3px]">
                CATALOG
              </span>
            </span>
          </Link>
          <button type="button" aria-label={t("navigation.label")} aria-haspopup="dialog" aria-expanded={mobilePanel === "navigation"} onClick={() => { setIsUserMenuOpen(false); setMobilePanel("navigation"); }} className="md:hidden grid h-11 w-11 place-items-center rounded-control text-text-body hover:bg-surfaceHover">
            <Menu className="h-5 w-5" aria-hidden="true" />
          </button>

          <nav className="hidden xl:flex items-center gap-1">
            {/* 开发者中心与管理后台都不在顶栏——统一收进用户菜单，顶栏只剩内容导航。 */}
            {navLinks.map((tab) => {
              const Icon = tab.icon;
              const active = isNavLinkActive(pathname, tab);
              const className = `relative flex items-center gap-1 px-1.5 py-1.5 rounded-lg text-xs font-medium tracking-wide transition-all ${
                active
                  ? "text-primary bg-primary/10 border border-primary/25 font-semibold shadow-xs"
                  : "text-text-muted hover:text-emphasis hover:bg-surfaceHover"
              }`;

              if (tab.external) {
                return (
                  <a key={tab.href} href={tab.href} className={className}>
                    <Icon className="w-3.5 h-3.5" strokeWidth={1.8} />
                    <span>{tab.label}</span>
                  </a>
                );
              }
              return (
                <Link key={tab.href} href={tab.href} className={className}>
                  <Icon className="w-3.5 h-3.5" strokeWidth={1.8} />
                  <span>{tab.label}</span>
                </Link>
              );
            })}

          </nav>
        </div>

        <SearchSuggest
          className="hidden min-w-0 md:flex md:flex-1 md:min-w-[10rem] md:max-w-sm"
          value={query}
          onValueChange={setQuery}
          onSubmit={onSearch || ((q) => router.push(q ? "/explore?q=" + encodeURIComponent(q) : "/explore"))}
          placeholder={t("search.placeholder")}
          submitLabel={t("search.submit")}
        />

        {/* Right Controls */}
        <div className="flex items-center gap-1 lg:gap-2.5 shrink-0">
          <button type="button" aria-label={t("search.open")} aria-haspopup="dialog" aria-expanded={mobilePanel === "search"} onClick={() => { setIsUserMenuOpen(false); setMobilePanel("search"); }} className="md:hidden grid h-11 w-11 place-items-center rounded-control text-text-body hover:bg-surfaceHover">
            <Search className="h-5 w-5" aria-hidden="true" />
          </button>
          {/* 新建：指向统一新建页 /new（层级在编辑器内切换，?kind= 只做预选）。
              标签 span 带 hidden sm:inline，窄屏只剩加号图标，因此必须显式给可访问名。 */}
          {/* 站内通知：桌面右侧入口，手机收进导航弹窗；未读 >99 显示 99+。 */}
          {user && (
            <Link
              href="/notifications"
              aria-label={
                unreadNotifications !== null && unreadNotifications > 0
                  ? t("notifications.unreadBadge", { count: unreadNotifications })
                  : t("navigation.notifications")
              }
              title={t("navigation.notifications")}
              className={`relative hidden md:inline-flex items-center justify-center w-9 h-9 rounded-lg border transition-colors duration-fast ease-soft ${
                pathname.startsWith("/notifications")
                  ? "bg-primary/10 border-primary/25 text-primary"
                  : "bg-emphasis/[0.04] border-line text-text-strong hover:bg-emphasis/[0.08]"
              }`}
            >
              <Bell className="w-3.5 h-3.5" strokeWidth={1.8} />
              {unreadNotifications !== null && unreadNotifications > 0 && (
                <span
                  className="absolute -top-1 -right-1 min-w-[16px] h-4 px-1 rounded-full bg-primary text-white text-[9px] font-mono font-bold flex items-center justify-center"
                  title={t("notifications.unreadBadge", { count: unreadNotifications })}
                >
                  {unreadNotifications > 99 ? "99+" : unreadNotifications}
                </span>
              )}
            </Link>
          )}

          <Link
              href="/new"
            aria-label={t("catalog.create")}
            className="inline-flex items-center justify-center gap-1.5 px-2 sm:px-3 h-9 max-md:min-h-11 max-md:min-w-11 rounded-control bg-primary/15 hover:bg-primary/25 border border-primary/30 text-xs font-medium text-primary hover:text-emphasis transition-all shadow-2xs"
            >
              <Plus className="w-3.5 h-3.5" strokeWidth={2} />
              <span className="hidden lg:inline">{t("catalog.create")}</span>
            </Link>

          {/* 私信与通知独立计数；手机入口在导航弹窗及用户菜单。 */}
          {user && (
            <Link
              href="/messages"
              aria-label={
                unreadCount !== null && unreadCount > 0
                  ? t("messages.unreadLabel", { n: unreadCount })
                  : t("messages.title")
              }
              title={t("messages.title")}
              className={`relative hidden md:inline-flex items-center justify-center w-9 h-9 rounded-lg border transition-colors duration-fast ease-soft ${
                pathname.startsWith("/messages")
                  ? "bg-primary/10 border-primary/25 text-primary"
                  : "bg-emphasis/[0.04] border-line text-text-strong hover:bg-emphasis/[0.08]"
              }`}
            >
              <Mail className="w-3.5 h-3.5" strokeWidth={1.8} />
              {unreadCount !== null && unreadCount > 0 && (
                <span
                  className="absolute -top-1 -right-1 min-w-[16px] h-4 px-1 rounded-full bg-danger/15 border border-danger/30 text-danger-soft text-[10px] font-bold flex items-center justify-center"
                  title={t("messages.unreadLabel", { n: unreadCount })}
                >
                  {unreadCount > 99 ? "99+" : unreadCount}
                </span>
              )}
            </Link>
          )}

          {/* User Profile / Login */}
          {user ? (
            <div className="relative" ref={userMenuRef}>
              <button
                type="button"
                aria-label={t("navbar.userCenter")}
                aria-expanded={isUserMenuOpen}
                onClick={() => setIsUserMenuOpen(!isUserMenuOpen)}
                className="relative flex items-center justify-center gap-1 sm:gap-2 px-1.5 sm:pr-2.5 h-9 max-md:min-h-11 max-md:min-w-11 rounded-control bg-emphasis/[0.04] hover:bg-emphasis/[0.08] border border-line text-xs text-text-strong transition-colors duration-fast ease-soft cursor-pointer"
              >
                {/* 未读私信角标改挂在顶栏信封入口上（见上）：同一个计数在顶栏只出现一次，
                    头像按钮回归"纯菜单开关"，不再承担未读提示。 */}
                <UserAvatar user={user} size="sm" shape="rounded" />
                <span className="font-medium max-w-[60px] truncate hidden lg:inline text-xs">
                  {displayNameOf(user as unknown as { username: string; display_name?: string })}
                </span>
                <ChevronDown
                  className={`hidden sm:block w-3 h-3 text-text-muted transition-transform duration-200 ${
                    isUserMenuOpen ? "rotate-180" : ""
                  }`}
                  strokeWidth={1.5}
                />
              </button>

              {isUserMenuOpen && (
                <div
                  onClick={(e) => e.stopPropagation()}
                  className="absolute right-0 mt-2 w-56 rounded-xl border border-line bg-surface shadow-elevated py-1.5 z-50 animate-slide-up text-xs"
                >
                  <div className="px-3.5 py-2.5 border-b border-line-subtle flex items-center gap-2.5">
                    <UserAvatar user={user} size="md" shape="rounded" ring />
                    <div className="space-y-0.5 min-w-0 flex-1">
                      <div className="font-semibold text-emphasis truncate">
                        {displayNameOf(user as unknown as { username: string; display_name?: string })}
                      </div>
                      <div className="text-[10px] text-text-faint font-mono truncate">@{user.username}</div>
                    </div>
                  </div>

                  <div className="py-1">
                    <Link
                      href={`/users/${user.id}`}
                      onClick={() => setIsUserMenuOpen(false)}
                      className="w-full px-3 py-2 text-left text-text-body hover:text-emphasis hover:bg-surfaceHover flex items-center gap-2 transition-colors duration-fast ease-soft font-medium"
                    >
                      <UserIcon className="w-3.5 h-3.5 text-primary" strokeWidth={1.7} />
                      <span>{t("navbar.userCenter")}</span>
                    </Link>
                    <Link
                      href="/settings"
                      onClick={() => setIsUserMenuOpen(false)}
                      className="w-full px-3 py-2 text-left text-text-body hover:text-emphasis hover:bg-surfaceHover flex items-center gap-2 transition-colors duration-fast ease-soft font-medium"
                    >
                      <Settings className="w-3.5 h-3.5 text-primary" strokeWidth={1.7} />
                      <span>{t("navbar.userSettings")}</span>
                    </Link>

                    {/* 私信收件箱：用户菜单在所有断点都可见，窄屏与桌面共用这一条入口。 */}
                    <Link
                      href="/messages"
                      onClick={() => setIsUserMenuOpen(false)}
                      className="w-full px-3 py-2 text-left text-text-body hover:text-emphasis hover:bg-surfaceHover flex items-center gap-2 transition-colors duration-fast ease-soft font-medium"
                    >
                      <Mail className="w-3.5 h-3.5 text-primary" strokeWidth={1.7} />
                      <span>{t("messages.title")}</span>
                      {unreadCount !== null && unreadCount > 0 && (
                        <span
                          className="ml-auto shrink-0 min-w-[18px] h-[18px] px-1 rounded-full bg-danger/15 border border-danger/30 text-danger-soft text-[10px] font-bold flex items-center justify-center"
                          title={t("messages.unreadLabel", { n: unreadCount })}
                        >
                          {unreadCount > 99 ? "99+" : unreadCount}
                        </span>
                      )}
                    </Link>

                    {/* 开发者中心：顶栏不设入口，这里是唯一入口（全断点可见）。
                        登录即可自助登记应用，未登录不显示——本菜单只在 user 存在时渲染。 */}
                    <Link
                      href="/developer"
                      onClick={() => setIsUserMenuOpen(false)}
                      className="w-full px-3 py-2 text-left text-text-body hover:text-emphasis hover:bg-surfaceHover flex items-center gap-2 transition-colors duration-fast ease-soft font-medium"
                    >
                      <Terminal className="w-3.5 h-3.5 text-primary" strokeWidth={1.7} />
                      <span>{t("navigation.developer")}</span>
                    </Link>

                    {/* 顶栏不再设管理后台入口：这里是唯一入口，门与旧顶栏按钮同口径
                        （canEnterAdmin，持管理权限组同样可见，不只认 role）。 */}
                    {canEnterAdmin(user) && (
                      <Link
                        href="/admin"
                        onClick={() => setIsUserMenuOpen(false)}
                        className="w-full px-3 py-2 text-left text-danger hover:text-danger-soft hover:bg-rose-500/10 flex items-center gap-2 transition-colors duration-fast ease-soft font-medium"
                      >
                        <Shield className="w-3.5 h-3.5" strokeWidth={1.7} />
                        <span>{t("navbar.adminConsole")}</span>
                      </Link>
                    )}
                  </div>

                  <div className="border-t border-line-subtle pt-1">
                    <button
                      type="button"
                      onClick={() => {
                        setIsUserMenuOpen(false);
                        logout();
                      }}
                      className="w-full px-3 py-2 text-left text-danger hover:bg-rose-500/10 flex items-center gap-2 transition-colors duration-fast ease-soft cursor-pointer"
                    >
                      <LogOut className="w-3.5 h-3.5" strokeWidth={1.7} />
                      <span>{t("catalog.logout")}</span>
                    </button>
                  </div>
                </div>
              )}
            </div>
          ) : (
            <a
              href={loginHref}
              aria-label={t("navbar.signIn")}
              className="inline-flex items-center justify-center gap-1.5 px-3 h-9 max-md:min-h-11 max-md:min-w-11 rounded-lg bg-emphasis/[0.04] hover:bg-emphasis/[0.08] border border-line text-xs font-medium text-text-strong transition-colors duration-fast ease-soft"
            >
              <UserIcon className="w-3.5 h-3.5" />
              <span className="hidden lg:inline">{t("navbar.signIn")}</span>
            </a>
          )}

          {/* Controls: Theme & Locale */}
          <div className="hidden md:flex items-center border-l border-line pl-2 gap-1.5">
            <LocaleSwitcher compact />
            <ThemeModeSwitcher />
            <ThemePicker />
          </div>
        </div>
      </PageContainer>
      {/* 平板横向导航：手机使用弹窗，不占额外的顶栏行。 */}
      <PageContainer className="hidden md:block xl:hidden">
      <nav aria-label={t("navigation.label")} className="flex gap-1 overflow-x-auto pb-1">
        {navLinks.map((tab) => {
          const active = isNavLinkActive(pathname, tab);
          const className = `whitespace-nowrap rounded-lg px-3 py-1.5 text-xs ${active ? "bg-primary/10 text-primary" : "text-text-muted"}`;
          // external 项必须走 <a>：/docs/catalog 与外站资源站不是本应用的路由，
          // next/link 会让 App Router 去取一条不存在的 RSC 载荷（与桌面分支同一处理）。
          if (tab.external) {
            return (
              <a key={tab.href} href={tab.href} className={className}>
                {tab.label}
              </a>
            );
          }
          return (
            <Link key={tab.href} href={tab.href} aria-current={active ? "page" : undefined} className={className}>
              {tab.label}
            </Link>
          );
        })}
      </nav>
      </PageContainer>
    </header>
    <Modal open={mobilePanel === "search"} onClose={closeMobilePanel} title={t("search.submit")} icon={<Search className="h-4 w-4" />} initialFocus="input" maxWidth="max-w-lg">
      {/* 给联想结果预留空间，弹窗内的滚动不会遮住输入与关闭操作。 */}
      <div className="min-h-[min(22rem,55dvh)]">
        <SearchSuggest value={query} onValueChange={setQuery} onSubmit={(q) => { closeMobilePanel(); if (onSearch) onSearch(q); else router.push(q ? "/explore?q=" + encodeURIComponent(q) : "/explore"); }} onNavigate={closeMobilePanel} placeholder={t("search.placeholder")} submitLabel={t("search.submit")} />
      </div>
    </Modal>
    <Modal open={mobilePanel === "navigation"} onClose={closeMobilePanel} title={t("navigation.label")} icon={<Menu className="h-4 w-4" />} maxWidth="max-w-lg">
      <nav aria-label={t("navigation.label")} className="grid grid-cols-2 gap-2">
        {navLinks.map((tab) => {
          const Icon = tab.icon;
          const active = isNavLinkActive(pathname, tab);
          const className = `flex min-h-11 items-center gap-2 rounded-control px-3 py-2 text-sm ${active ? "bg-primary/10 text-primary" : "text-text-body hover:bg-surfaceHover"}`;
          const content = <><Icon className="h-4 w-4 shrink-0" aria-hidden="true" /><span>{tab.label}</span></>;
          return tab.external ? <a key={tab.href} href={tab.href} onClick={closeMobilePanel} className={className}>{content}</a> : <Link key={tab.href} href={tab.href} aria-current={active ? "page" : undefined} onClick={closeMobilePanel} className={className}>{content}</Link>;
        })}
        {user && <Link href="/notifications" onClick={closeMobilePanel} className="flex min-h-11 items-center gap-2 rounded-control px-3 py-2 text-sm text-text-body hover:bg-surfaceHover"><Bell className="h-4 w-4 shrink-0" aria-hidden="true" /><span>{t("navigation.notifications")}</span>{unreadNotifications !== null && unreadNotifications > 0 && <span className="text-primary">{unreadNotifications > 99 ? "99+" : unreadNotifications}</span>}</Link>}
        {user && <Link href="/messages" onClick={closeMobilePanel} className="flex min-h-11 items-center gap-2 rounded-control px-3 py-2 text-sm text-text-body hover:bg-surfaceHover"><Mail className="h-4 w-4 shrink-0" aria-hidden="true" /><span>{t("messages.title")}</span>{unreadCount !== null && unreadCount > 0 && <span className="text-primary">{unreadCount > 99 ? "99+" : unreadCount}</span>}</Link>}
      </nav>
      <div className="grid grid-cols-[1fr_auto_auto_auto] items-center gap-2 border-t border-line-subtle pt-3">
        <span className="order-1 text-xs text-text-muted">{t("settings.appearanceTitle")}</span>
        <div className="order-1"><LocaleSwitcher compact /></div>
        <ThemeModeSwitcher withinDialog />
        <ThemePicker withinDialog />
      </div>
    </Modal>
    </>
  );
};
