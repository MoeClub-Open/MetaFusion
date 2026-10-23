"use client";

import { searchInputLimitProps } from "@/lib/searchQuery";
import React, { useEffect, useMemo, useRef, useState } from "react";
import Link from "next/link";
import { useAuth } from "@/lib/authContext";
import { useI18n } from "@/i18n/I18nProvider";
import { PullRequestQueue } from "@/components/catalog/PullRequestReview";
import { AssistantAdmin } from "@/components/assistant/AssistantAdmin";
import { PublicSourcesAdmin } from "@/components/admin/PublicSourcesAdmin";
import { CatalogRequestLogsAdmin } from "@/components/admin/CatalogRequestLogsAdmin";
import { DefinitionsEditor } from "@/components/catalog/DefinitionsEditor";
import { ChangeNote } from "@/components/catalog/Fields";
import type { Entity, Source } from "@/components/catalog/api";
import { useDefinitions, getKindName, resolveKindOptions } from "@/lib/definitions";
import { PageContainer } from "@/components/ui/PageShell";
import { Select } from "@/components/ui/Select";
import { TabPanel } from "@/components/ui/TabPanel";
import { ExternalDatabasesTab } from "./components/tabs/ExternalDatabasesTab";
import { RateLimitsTab } from "./components/tabs/RateLimitsTab";
import { ShelvesTab } from "./components/tabs/ShelvesTab";
import { ExchangeTab } from "./components/tabs/ExchangeTab";
import { CATALOG_LIFECYCLE_MANAGE, can, canEnterCatalogConsole } from "@/lib/permissions";
import { fetchApi } from "@/lib/api";
import { localizeCatalogError } from "@/lib/catalogErrors";
import { ConfirmDialog } from "@/components/oauth/ConfirmDialog";
import type { LucideIcon } from "lucide-react";
import {
  LayoutDashboard,
  Sliders,
  CheckSquare,
  GitMerge,
  Cpu,
  RefreshCw,
  Layers,
  Search,
  Plus,
  ArrowUpRight,
  Globe,
  Gauge,
  Activity,
} from "lucide-react";

import { isCompatibleMergeTarget } from "@/lib/mergeTarget";
import { canUseCatalogAdminTab, type CatalogAdminTab as AdminTab } from "@/lib/catalogAdminNavigation";

const NAV_GROUPS: { labelKey: string; tabs: { id: AdminTab; labelKey: string; icon: LucideIcon }[] }[] = [
  { labelKey: "admin.nav.work", tabs: [
    { id: "overview", labelKey: "admin.tab.overview", icon: LayoutDashboard },
    { id: "reviews", labelKey: "collaboration.pullRequests", icon: CheckSquare },
    { id: "entities", labelKey: "admin.nav.entities", icon: Layers },
    { id: "merge", labelKey: "admin.tab.merge", icon: GitMerge },
  ] },
  { labelKey: "admin.nav.catalogConfig", tabs: [
    { id: "definitions", labelKey: "admin.tab.definitions", icon: Sliders },
    { id: "shelves", labelKey: "admin.tab.shelves", icon: LayoutDashboard },
    { id: "extdb", labelKey: "admin.tab.extdb", icon: Globe },
    { id: "publicSources", labelKey: "admin.publicSources.title", icon: Globe },
  ] },
  { labelKey: "admin.nav.operations", tabs: [
    { id: "assistant", labelKey: "assistant.admin.title", icon: Cpu },
    { id: "requestLogs", labelKey: "admin.requestLogs.title", icon: Activity },
    { id: "ratelimits", labelKey: "admin.tab.ratelimits", icon: Gauge },
    { id: "modules", labelKey: "admin.tab.modules", icon: Cpu },
    { id: "exchange", labelKey: "admin.tab.exchange", icon: ArrowUpRight },
  ] },
];
const PAGE_SIZE = 50;
type AdminEntity = Entity & { id: string };

// 聚合计数的取值口径：只接受有限数字。字段缺失（端点没有这个键）、类型不对、
// 整个响应拿不到，都返回 null，调用方据此保留占位符——"取不到"与"真的是 0"必须分开。
const countOrNull = (v: unknown): number | null =>
  typeof v === "number" && Number.isFinite(v) ? v : null;

export function CatalogWorkbench({ active }: { active: boolean }) {
  const { user } = useAuth();
  const { t, tr, locale } = useI18n();

  const { kinds } = useDefinitions();

  // 层级名与可选项：服务端 definitions.kinds 优先，服务端未给时退回内置骨架清单，
  // 字典只作名称兜底（缺键退原始码）。
  const kindLabel = (code: string) => getKindName(kinds, code, locale, tr(`catalog.kind.${code}`, code));
  const kindOptions = resolveKindOptions(kinds);

  const [activeTab, setActiveTab] = useState<AdminTab>("overview");
  const visibleGroups = useMemo(() => NAV_GROUPS.map((group) => ({
    ...group, tabs: group.tabs.filter((item) => canUseCatalogAdminTab(user, item.id)),
  })).filter((group) => group.tabs.length > 0), [user]);
  const visibleTabs = visibleGroups.flatMap((group) => group.tabs);
  const visibleTabsKey = visibleTabs.map((item) => item.id).join(",");
  const currentTab = visibleTabs.some((item) => item.id === activeTab) ? activeTab : (visibleTabs[0]?.id || "overview");
  const mayManageLifecycle = can(user, CATALOG_LIFECYCLE_MANAGE);
  const selectTab = (tab: AdminTab) => {
    if (!canUseCatalogAdminTab(user, tab)) return;
    setActiveTab(tab);
    if (window.location.hash !== `#${tab}`) window.history.pushState(null, "", `#${tab}`);
  };
  useEffect(() => {
    if (!active) return;
    const syncFromUrl = () => {
      const hash = window.location.hash.slice(1);
      setActiveTab(visibleTabs.some((item) => item.id === hash) ? hash as AdminTab : (visibleTabs[0]?.id || "overview"));
    };
    syncFromUrl();
    window.addEventListener("popstate", syncFromUrl);
    window.addEventListener("hashchange", syncFromUrl);
    return () => {
      window.removeEventListener("popstate", syncFromUrl);
      window.removeEventListener("hashchange", syncFromUrl);
    };
  }, [visibleTabsKey, active]);
  // 四个计数各为 null 表示"还没拿到或取不到"：卡片显示占位符，不把没取到的数当成 0 讲成事实。
  const [stats, setStats] = useState<{
    pending: number | null;
    active: number | null;
    tombstones: number | null;
    extDatabases: number | null;
  }>({ pending: null, active: null, tombstones: null, extDatabases: null });
  const [modules, setModules] = useState<any[]>([]);
  // Entities management state
  const [entitiesList, setEntitiesList] = useState<AdminEntity[]>([]);
  const [entitiesListFailed, setEntitiesListFailed] = useState(false);
  const [entitiesLoading, setEntitiesLoading] = useState(false);
  const [entitiesQ, setEntitiesQ] = useState("");
  const [entitiesSearch, setEntitiesSearch] = useState("");
  const [entitiesKind, setEntitiesKind] = useState("all");
  const [entitiesState, setEntitiesState] = useState("all");
  const [entitiesPage, setEntitiesPage] = useState(1);
  const [entitiesTotal, setEntitiesTotal] = useState<number | null>(null);
  const [entitiesHasMore, setEntitiesHasMore] = useState(false);
  const [expandedEntity, setExpandedEntity] = useState<string | null>(null);
  const entitiesRequest = useRef(0);

  // Merge form states
  const [mergeSource, setMergeSource] = useState("");
  const [mergeTarget, setMergeTarget] = useState("");
  const [mergeNote, setMergeNote] = useState("");
  const [mergeMessage, setMergeMessage] = useState("");
  // 合并结果的成功/失败由状态位决定配色：原先靠 "Error: " 前缀判定，等于把英文前缀写进流程。
  const [mergeError, setMergeError] = useState(false);
  const [merging, setMerging] = useState(false);
  const mergeBusy = useRef(false);
  const [mergePreview, setMergePreview] = useState<{source: Record<string, any>; target: Record<string, any>; note: string; sources: Source[]} | null>(null);
  const [overviewLoading, setOverviewLoading] = useState(false);
  const [overviewFailed, setOverviewFailed] = useState(false);
  const overviewRequest = useRef(0);

  const loadOverview = async () => {
    const request = ++overviewRequest.current;
    setOverviewLoading(true);
    setOverviewFailed(false);
    setStats({ pending: null, active: null, tombstones: null, extDatabases: null });
    setModules([]);
    const results = await Promise.allSettled([
      mayManageLifecycle ? fetchApi<{states: Record<string, number>; pending_pull_requests:number}>("/catalog/entities/stats") : Promise.resolve(null),
      fetchApi<{items: unknown[]}>("/catalog/external-databases"),
      fetchApi<{modules: any[]}>("/capabilities"),
    ]);
    if (request !== overviewRequest.current) return;
    let failed = results.some((result) => result.status === "rejected");
    const counts = results[0];
    if (counts.status === "fulfilled" && counts.value) {
      const statuses = counts.value.states;
      const pending = countOrNull(counts.value.pending_pull_requests);
      const active = countOrNull(statuses?.active);
      const deleted = countOrNull(statuses?.deleted);
      const merged = countOrNull(statuses?.merged);
      if ([pending, active, deleted, merged].some((v) => v === null)) failed = true;
      setStats((prev) => ({...prev, pending, active, tombstones: deleted !== null && merged !== null ? deleted + merged : null}));
    }
    const external = results[1];
    if (external.status === "fulfilled") {
      if (Array.isArray(external.value.items)) setStats((prev) => ({...prev, extDatabases: external.value.items.length}));
      else failed = true;
    }
    const capability = results[2];
    if (capability.status === "fulfilled") {
      if (Array.isArray(capability.value.modules)) setModules(capability.value.modules);
      else failed = true;
    }
    setOverviewFailed(failed);
    setOverviewLoading(false);
  };

  const loadEntities = (page = entitiesPage) => {
    const request = ++entitiesRequest.current;
    setEntitiesLoading(true);
    const params = new URLSearchParams();
    if (entitiesKind !== "all") params.set("kind", entitiesKind);
    if (entitiesSearch) params.set("q", entitiesSearch);
    if (entitiesState !== "all") params.set("state", entitiesState);
    params.set("limit", String(PAGE_SIZE));
    params.set("offset", String((page - 1) * PAGE_SIZE));

    setEntitiesListFailed(false);
    fetchApi<{ items: AdminEntity[]; total: number; has_more: boolean }>(`/admin/catalog-entities?${params.toString()}`)
      .then((d) => {
        if (request !== entitiesRequest.current) return;
        if (!Array.isArray(d.items) || countOrNull(d.total) === null || typeof d.has_more !== "boolean") throw new Error("invalid_response");
        setEntitiesList(d.items);
        setEntitiesTotal(countOrNull(d.total));
        setEntitiesHasMore(d.has_more);
        setExpandedEntity(null);
      })
      .catch(() => {
        if (request !== entitiesRequest.current) return;
        setEntitiesList([]);
        setEntitiesTotal(null);
        setEntitiesHasMore(false);
        setEntitiesListFailed(true);
      })
      .finally(() => { if (request === entitiesRequest.current) setEntitiesLoading(false); });
  };

  const mayEnterCatalog = canEnterCatalogConsole(user);

  useEffect(() => {
    if (mayEnterCatalog) {
      loadOverview();
    }
    return () => { overviewRequest.current += 1; };
  }, [mayEnterCatalog, user?.id, mayManageLifecycle]);

  useEffect(() => {
    if (currentTab === "entities" && mayManageLifecycle) {
      loadEntities();
    }
    return () => { entitiesRequest.current += 1; };
  }, [currentTab, entitiesKind, entitiesState, entitiesSearch, entitiesPage, mayManageLifecycle, user?.id]);

  // 这里刻意没有"启停"动作：运行时模块开关已随子系统拆分退役，
  // 能力是否可用由部署决定（服务在不在、配置没配置），面板只呈现事实（enabled），
  // 不提供会必然失败的按钮——要开能力就去部署对应服务。

  const handleMergeSubmit = async (e: React.FormEvent) => {
    e.preventDefault();
    const sourceId = mergeSource.trim();
    const targetId = mergeTarget.trim();
    if (mergeBusy.current || !mayManageLifecycle) return;
    mergeBusy.current = true;
    setMerging(true);
    setMergeMessage("");
    setMergeError(false);
    try {
      // 前置校验与 lifecycle.go 的合并约束同一口径：源与目标要读得到、同 kind、
      // 同归属（作品/发行版/载体/父级/内容单元）、目标已发布、源不是已删除/已合并。
      // 少任何一条，服务端只会回 invalid_merge_target / invalid_state——先在本地讲清楚。
      const pair = await Promise.all([
        fetchApi<Record<string, any>>(`/catalog/entities/${encodeURIComponent(sourceId)}`),
        fetchApi<Record<string, any>>(`/catalog/entities/${encodeURIComponent(targetId)}`),
      ]);
      const [source, target] = pair;
      if (source.state === "deleted" || source.state === "merged") {
        setMergeError(true);
        setMergeMessage(t("admin.console.mergeSourceRetired"));
        return;
      }
      if (!isCompatibleMergeTarget(source, target)) {
        setMergeError(true);
        setMergeMessage(t("admin.console.mergeInvalidTarget"));
        return;
      }
      setMergePreview({source, target, note: mergeNote.trim(), sources: []});
    } catch (err) {
      setMergeError(true);
      setMergeMessage(localizeCatalogError(String((err as Error).message || err), t));
    } finally {
      mergeBusy.current = false;
      setMerging(false);
    }
  };

  const confirmMerge = async () => {
    if (!mergePreview || mergeBusy.current || !mayManageLifecycle) return;
    const {source, target, note, sources} = mergePreview;
    mergeBusy.current = true;
    setMerging(true);
    try {
      await fetchApi(`/catalog/entities/${encodeURIComponent(source.id)}/lifecycle`, {
        method: "POST",
        body: JSON.stringify({ target_id: target.id, expected_version: source.version,
          edit_note: note, sources }),
      });
      setMergeError(false);
      setMergeMessage(t("admin.console.mergeSuccess"));
      setMergeSource(""); setMergeTarget(""); setMergeNote("");
      void loadOverview();
    } catch (err) {
      setMergeError(true);
      setMergeMessage(localizeCatalogError(String((err as Error).message || err), t));
    } finally {
      setMergePreview(null);
      mergeBusy.current = false;
      setMerging(false);
    }
  };

  return (
    <div className="flex flex-col bg-background text-text-strong">
      <PageContainer className="py-6 flex-1 flex flex-col lg:flex-row gap-6">
        {/* Left Sidebar */}
        <aside className="w-full lg:w-64 shrink-0 lg:sticky lg:top-32 lg:self-start lg:max-h-[calc(100vh-9rem)] lg:overflow-y-auto">
          <div className="lg:hidden rounded-xl border border-line-subtle bg-surfaceSubtle p-3">
            <label htmlFor="admin-section" className="block text-xs font-medium text-text-muted mb-2">
              {t("admin.nav.section")}
            </label>
            <Select
              id="admin-section"
              value={currentTab}
              onChange={(v) => selectTab(v as AdminTab)}
              options={visibleGroups.flatMap((group) =>
                group.tabs.map((item) => ({ value: item.id, label: t(item.labelKey) }))
              )}
              aria-label={t("admin.nav.section")}
            />
          </div>
          {/* 目录导航避开管理中心顶栏与工作区切换行。 */}
          <nav aria-label={t("admin.nav.section")} className="hidden lg:block rounded-xl border border-line-subtle bg-surfaceSubtle p-2">
            {visibleGroups.map((group) => (
              <div key={group.labelKey} className="py-1 first:pt-0 last:pb-0">
                <div className="px-3 py-2 text-[11px] font-semibold text-text-faint tracking-wide">
                  {t(group.labelKey)}
                </div>
                {group.tabs.map((item) => {
                  const Icon = item.icon;
                  const active = currentTab === item.id;
                  return (
                    <button
                      key={item.id}
                      type="button"
                      aria-current={active ? "page" : undefined}
                      onClick={() => selectTab(item.id)}
                      className={`w-full flex items-center gap-3 px-3 py-2.5 rounded-lg text-sm text-left transition-colors ${active ? "bg-primary/15 text-primary font-semibold" : "text-text-muted hover:text-text-strong hover:bg-surfaceHover"}`}
                    >
                      <Icon className="w-4 h-4 shrink-0" aria-hidden="true" />
                      <span className="flex-1">{t(item.labelKey)}</span>
                      {item.id === "reviews" && stats.pending != null && stats.pending > 0 ? (
                        <span className="rounded-chip bg-amber-500/15 px-2 py-0.5 text-[11px] text-warn">{stats.pending}</span>
                      ) : null}
                    </button>
                  );
                })}
              </div>
            ))}
          </nav>

        </aside>

        {/* Right Main Workbench */}
        {/* key 让每次切换页签都重放进入动画；外层统一包裹层保证各页签的首元素落在同一纵向位置 */}
        <TabPanel activeKey={currentTab} spacing="none" className="flex-1 min-w-0">
          <div className="space-y-4 [&>*:first-child]:mt-0">
          {currentTab === "overview" && (
            <div className="space-y-4">
              <div>
                <h2 className="text-base font-semibold text-text-strong">
                  {t("admin.console.sysOverview")}
                </h2>
              </div>

              {/* 卡片只放目录域拿得到的真实数据，且标签与端点口径一致：数据库版本与会话模式没有
                  任何端点暴露，部署态能力数（/api/capabilities）不是目录域指标，都不出现在这里；
                  取不到显示占位符，不写死、不拿 0 冒充。 */}
              <div className="grid grid-cols-2 sm:grid-cols-4 gap-4" aria-busy={overviewLoading}>
                {mayManageLifecycle && <>
                <button type="button" onClick={() => selectTab("reviews")} className="p-4 rounded-xl border border-line-subtle bg-surfaceSubtle hover:border-primary/40 hover:bg-surfaceHover text-left transition-colors cursor-pointer">
                  <div className="text-xs text-text-muted font-mono mb-1">
                    {t("admin.console.pendingReviews")}
                  </div>
                  <div className="text-2xl font-bold text-warn">{stats.pending ?? "—"}</div>
                </button>
                <button type="button" onClick={() => { setEntitiesPage(1); selectTab("entities"); }} className="p-4 rounded-xl border border-line-subtle bg-surfaceSubtle hover:border-primary/40 hover:bg-surfaceHover text-left transition-colors cursor-pointer">
                  <div className="text-xs text-text-muted font-mono mb-1">
                    {t("admin.console.activeEntities")}
                  </div>
                  <div className="text-2xl font-bold text-success">{stats.active ?? "—"}</div>
                </button>
                {/* 墓碑 = deleted + merged，只来自 GET /catalog/entities/stats：列表端点的过滤固定带
                    status NOT IN ('deleted','merged')（backend/internal/catalog/store.go listFilter），
                    与 status=deleted 相与恒为空，所以这张卡不走列表端点。取不到仍是占位符，
                    不拿 0 冒充——线上同表 deleted 有 288 条，显示 0 就是假数据。 */}
                <div className="p-4 rounded-xl border border-line-subtle bg-surfaceSubtle">
                  <div className="text-xs text-text-muted font-mono mb-1">
                    {t("admin.console.tombstones")}
                  </div>
                  <div className="text-2xl font-bold text-text-strong">{stats.tombstones ?? "—"}</div>
                </div>
                </>}
                <button type="button" disabled={!canUseCatalogAdminTab(user, "extdb")} onClick={() => selectTab("extdb")} className="p-4 rounded-xl border border-line-subtle bg-surfaceSubtle hover:border-primary/40 hover:bg-surfaceHover text-left transition-colors cursor-pointer">
                  <div className="text-xs text-text-muted font-mono mb-1">
                    {t("admin.console.extDatabases")}
                  </div>
                  <div className="text-2xl font-bold text-text-strong">{stats.extDatabases ?? "—"}</div>
                </button>
              </div>

              <div className="flex items-center justify-between gap-3">
                {overviewFailed ? <p role="alert" className="text-xs text-danger">{t("admin.console.overviewFailed")}</p> : <span />}
                <button type="button" disabled={overviewLoading} onClick={() => void loadOverview()} className="inline-flex items-center gap-2 rounded-lg border border-line px-3 py-2 text-xs disabled:opacity-50"><RefreshCw className={overviewLoading ? "h-4 w-4 animate-spin" : "h-4 w-4"} />{t("common.refresh")}</button>
              </div>
              <div className="rounded-xl border border-line-subtle bg-surfaceSubtle p-4">
                <h3 className="text-sm font-semibold text-text-strong mb-3">{t("admin.console.quickActions")}</h3>
                <div className="flex flex-wrap gap-2">
                  <Link href="/new" className="inline-flex items-center gap-2 rounded-lg bg-primary px-3 py-2 text-xs font-medium text-white hover:bg-primary/90">
                    <Plus className="h-4 w-4" aria-hidden="true" />{t("catalog.newEntity")}
                  </Link>
                  <button type="button" disabled={!canUseCatalogAdminTab(user, "definitions")} onClick={() => selectTab("definitions")} className="rounded-lg border border-line px-3 py-2 text-xs text-text-body hover:bg-surfaceHover">{t("admin.tab.definitions")}</button>
                  <button type="button" disabled={!canUseCatalogAdminTab(user, "shelves")} onClick={() => selectTab("shelves")} className="rounded-lg border border-line px-3 py-2 text-xs text-text-body hover:bg-surfaceHover">{t("admin.tab.shelves")}</button>
                </div>
              </div>

            </div>
          )}

          {currentTab === "entities" && (
            <div className="space-y-4">
              {/* Entities Header & Search */}
              <div className="flex flex-col sm:flex-row sm:items-center justify-between gap-4 p-4 rounded-xl bg-surfaceSubtle border border-line-subtle">
                <div>
                  <h2 className="text-base font-semibold text-text-strong">
                    {t("admin.entities.title")}
                  </h2>
                  <p className="text-xs text-text-muted mt-0.5">
                    {t("admin.entities.desc")}
                  </p>
                </div>

                <div className="flex items-center gap-2">
                  <button
                    type="button"
                    onClick={() => loadEntities()}
                    aria-label={t("common.refresh")}
                    className="p-2 rounded-lg bg-surfaceSubtle hover:bg-surfaceHover border border-line text-xs text-text-body transition-colors duration-fast ease-soft cursor-pointer"
                    title={t("common.refresh")}
                  >
                    <RefreshCw className={`w-4 h-4 ${entitiesLoading ? "animate-spin text-primary" : ""}`} />
                  </button>
                  <Link
                    href="/new"
                    className="inline-flex items-center gap-1.5 px-3 py-1.5 rounded-lg bg-primary hover:bg-primary/90 text-xs font-medium text-white transition-colors duration-fast ease-soft"
                  >
                    <Plus className="w-3.5 h-3.5" />
                    <span>{t("catalog.newEntity")}</span>
                  </Link>
                </div>
              </div>



              {/* Filter Row */}
              <div className="grid grid-cols-1 sm:grid-cols-12 gap-3 p-3.5 rounded-xl bg-surfaceSubtle border border-line-subtle">
                <form
                  onSubmit={(e) => {
                    e.preventDefault();
                    if (entitiesPage === 1 && entitiesSearch === entitiesQ.trim()) loadEntities(1);
                    else { setEntitiesSearch(entitiesQ.trim()); setEntitiesPage(1); }
                  }}
                  className="sm:col-span-6 relative flex items-center"
                >
                  <Search className="absolute left-3 w-4 h-4 text-text-faint" />
                  <input
                    type="text"
                    value={entitiesQ}
                    {...searchInputLimitProps(setEntitiesQ)}
                    placeholder={t("admin.entities.search")}
                    className="w-full pl-9 pr-14 py-1.5 rounded-lg bg-surfaceSubtle border border-line text-xs text-text-strong placeholder:text-text-faint focus:border-primary outline-none"
                  />
                  <button
                    type="submit"
                    className="absolute right-1 px-2.5 py-0.5 rounded bg-primary/20 hover:bg-primary/30 text-primary text-[11px] font-medium transition-colors duration-fast ease-soft"
                  >
                    {t("catalog.searchAction")}
                  </button>
                </form>

                <div className="sm:col-span-3">
                  <Select
                    value={entitiesKind}
                    onChange={(v) => { setEntitiesKind(v); setEntitiesPage(1); }}
                    options={[
                      { value: "all", label: t("catalog.kind.all") },
                      ...kindOptions.map((k) => ({ value: k, label: kindLabel(k) })),
                    ]}
                    aria-label={t("catalog.kindLabel")}
                  />
                </div>
                <div className="sm:col-span-3">
                  <Select value={entitiesState} onChange={(value) => { setEntitiesState(value); setEntitiesPage(1); }}
                    aria-label={t("admin.entities.colStatus")}
                    options={[
                      { value: "all", label: t("admin.entities.allStates") },
                      { value: "active", label: t("admin.entities.stateActive") },
                      { value: "deleted", label: t("catalog.state.deleted") },
                      { value: "merged", label: t("catalog.state.merged") },
                    ]} />
                </div>
              </div>
              <p className="text-xs text-text-faint">{t("admin.entities.allStatesHint")}</p>

              {/* Table */}
              {entitiesLoading ? (
                <div className="py-20 text-center text-xs text-text-faint font-mono flex items-center justify-center gap-2">
                  <RefreshCw className="w-4 h-4 animate-spin text-primary" />
                  <span>{t("catalog.loading")}</span>
                </div>
              ) : entitiesListFailed ? (
                // 管理员最容易把"取数失败"读成"没有待审条目"：这一格必须说清是失败。
                <div role="alert" className="p-8 rounded-xl border border-amber-500/30 bg-amber-500/5 text-center text-xs space-y-2">
                  <p className="text-amber-700 dark:text-warn-soft">{t("catalog.listFailed")}</p>
                  <button type="button" onClick={() => loadEntities()} className="font-mono text-primary hover:underline cursor-pointer">
                    {t("catalog.retry")}
                  </button>
                </div>
              ) : entitiesList.length === 0 ? (
                <div className="p-8 rounded-xl border border-dashed border-line text-center text-xs text-text-faint font-mono">
                  {t("catalog.emptyTitle")}
                </div>
              ) : (
                <div className="rounded-xl border border-line-subtle overflow-x-auto bg-surface/40">
                  <table className="w-full min-w-[780px] text-left text-xs border-collapse">
                    <thead>
                      <tr className="border-b border-line-subtle bg-surfaceSubtle text-text-muted font-mono">
                        <th className="py-2.5 px-3 font-medium">{t("admin.entities.colTitle")}</th>
                        <th className="py-2.5 px-3 font-medium">{t("admin.entities.colKind")}</th>
                        <th className="py-2.5 px-3 font-medium">{t("admin.entities.colStatus")}</th>
                        <th className="py-2.5 px-3 font-medium">{t("admin.entities.colVersion")}</th>
                        <th className="py-2.5 px-3 font-medium text-right">{t("admin.entities.colActions")}</th>
                      </tr>
                    </thead>
                    <tbody className="divide-y divide-line-subtle">
                      {entitiesList.map((e) => (
                        <React.Fragment key={e.id}>
                        <tr key={e.id} className="hover:bg-surfaceSubtle transition-colors duration-fast ease-soft">
                          <td className="py-2.5 px-3">
                            <button type="button" onClick={() => setExpandedEntity(expandedEntity === e.id ? null : e.id)} aria-expanded={expandedEntity === e.id}
                              className="font-semibold text-left text-text-strong hover:text-primary transition-colors duration-fast ease-soft line-clamp-1">{e.title}</button>
                            <div className="text-[10px] text-text-faint font-mono">ID: {e.id}</div>
                          </td>
                          <td className="py-2.5 px-3">
                            <span className="px-1.5 py-0.5 rounded bg-surfaceSubtle text-[10px] font-mono">
                              {kindLabel(e.kind)}
                            </span>
                          </td>
                          <td className="py-2.5 px-3">
                            <span className={`px-1.5 py-0.5 rounded text-[10px] font-mono ${
                              e.state === "active" ? "bg-emerald-500/20 text-success" :

                              e.state === "deleted" ? "bg-rose-500/20 text-danger" :
                              e.state === "merged" ? "bg-purple-500/20 text-alt" :
                              "bg-surfaceSubtle text-text-muted"
                            }`}>
                              {e.state === "active" ? t("admin.entities.stateActive") : tr(`catalog.state.${e.state}`, e.state)}
                            </span>
                          </td>
                          <td className="py-2.5 px-3 font-mono text-[11px] text-text-muted">
                            v{e.version || 1}
                          </td>
                          <td className="py-2.5 px-3 text-right">
                            <div className="flex items-center justify-end gap-1.5">
                              {e.state === "active" && <Link
                                href={`/catalog/${e.id}`}
                                className="px-2 py-1 rounded bg-surfaceSubtle hover:bg-surfaceHover text-text-body hover:text-text-strong text-[11px] transition-colors duration-fast ease-soft"
                              >
                                {t("admin.entities.open")}
                              </Link>}
                              <button type="button" onClick={() => setExpandedEntity(expandedEntity === e.id ? null : e.id)} aria-expanded={expandedEntity === e.id}
                                className="px-2 py-1 rounded bg-surfaceSubtle hover:bg-surfaceHover text-text-body hover:text-text-strong text-[11px]">{t("admin.entities.record")}</button>
                              {mayManageLifecycle && e.state !== "deleted" && e.state !== "merged" && (
                                <button
                                  type="button"
                                  onClick={() => {
                                    selectTab("merge");
                                    setMergeSource(e.id);
                                  }}
                                  className="px-2 py-1 rounded bg-indigo-500/15 hover:bg-indigo-500/25 text-alt text-[11px] transition-colors duration-fast ease-soft cursor-pointer"
                                >
                                  {t("admin.entities.merge")}
                                </button>
                              )}
                            </div>
                          </td>
                        </tr>
                        {expandedEntity === e.id && <tr><td colSpan={5} className="p-3 bg-surfaceSubtle">
                          <p className="mb-2 text-xs text-text-muted">{t("admin.entities.recordHint")}</p>
                          <pre className="max-h-96 overflow-auto whitespace-pre-wrap break-all rounded-lg border border-line bg-surface p-3 text-[11px] font-mono text-text-body">{JSON.stringify(e, null, 2)}</pre>
                        </td></tr>}
                        </React.Fragment>
                      ))}
                    </tbody>
                  </table>
                </div>
              )}
              {!entitiesLoading && !entitiesListFailed && entitiesTotal != null && entitiesTotal > 0 && (
                <div className="flex flex-wrap items-center justify-between gap-3 text-xs text-text-muted">
                  <span>{t("admin.pagination", { page: entitiesPage, pages: Math.ceil(entitiesTotal / PAGE_SIZE), total: entitiesTotal })}</span>
                  <div className="flex gap-2">
                    <button type="button" disabled={entitiesPage === 1} onClick={() => setEntitiesPage((p) => p - 1)} className="rounded-lg border border-line px-3 py-2 hover:bg-surfaceHover disabled:opacity-40">{t("catalog.prevPage")}</button>
                    <button type="button" disabled={!entitiesHasMore} onClick={() => setEntitiesPage((p) => p + 1)} className="rounded-lg border border-line px-3 py-2 hover:bg-surfaceHover disabled:opacity-40">{t("catalog.nextPage")}</button>
                  </div>
                </div>
              )}
            </div>
          )}

          {currentTab === "definitions" && <DefinitionsEditor />}

          {currentTab === "extdb" && <ExternalDatabasesTab />}

          {currentTab === "shelves" && <ShelvesTab />}

          {currentTab === "ratelimits" && <RateLimitsTab />}

          {currentTab === "reviews" && <PullRequestQueue />}

          {currentTab === "merge" && (
            <div className="space-y-4">
              <div className="p-4 rounded-xl bg-surfaceSubtle border border-line-subtle">
                <h2 className="text-base font-semibold text-text-strong mb-1">
                  {t("admin.console.mergeTitle")}
                </h2>
                <p className="text-xs text-text-muted leading-relaxed">
                  {t("admin.console.mergeDesc")}
                </p>
              </div>

              <form onSubmit={handleMergeSubmit} className="p-4 rounded-xl bg-surfaceSubtle border border-line-subtle space-y-4 max-w-xl">
                <div>
                  <label htmlFor="merge-source" className="block text-xs font-medium text-text-body mb-1">
                    {t("admin.console.sourceUuid")}
                  </label>
                  <input
                    type="text"
                    required
                    id="merge-source"
                    value={mergeSource}
                    onChange={(e) => setMergeSource(e.target.value)}
                    placeholder={t("admin.entityUuidPlaceholder")}
                    className="w-full p-2.5 rounded-lg bg-surfaceSubtle border border-line text-xs font-mono text-text-strong placeholder:text-text-faint focus:border-primary outline-none"
                  />
                </div>

                <div>
                  <label htmlFor="merge-target" className="block text-xs font-medium text-text-body mb-1">
                    {t("admin.console.targetUuid")}
                  </label>
                  <input
                    type="text"
                    required
                    id="merge-target"
                    value={mergeTarget}
                    onChange={(e) => setMergeTarget(e.target.value)}
                    placeholder={t("admin.entityUuidPlaceholder")}
                    className="w-full p-2.5 rounded-lg bg-surfaceSubtle border border-line text-xs font-mono text-text-strong placeholder:text-text-faint focus:border-primary outline-none"
                  />
                </div>

                <div className="catalog-v2">
                  <ChangeNote note={mergeNote} setNote={setMergeNote} />
                </div>

                {mergeMessage && (
                  <div role={mergeError ? "alert" : "status"} className={`p-3 rounded-lg text-xs font-mono ${
                    mergeError ? "bg-rose-500/20 text-danger" : "bg-emerald-500/20 text-success"
                  }`}>
                    {mergeMessage}
                  </div>
                )}

                <button
                  type="submit"
                  disabled={merging}
                  className="px-5 py-2.5 rounded-lg bg-primary hover:bg-primary/90 disabled:opacity-50 text-white text-xs font-semibold transition-all shadow-xs cursor-pointer"
                >
                  {merging ? t("admin.console.merging") : t("admin.console.previewMerge")}
                </button>
              </form>
            </div>
          )}

          {currentTab === "modules" && (
            <div className="space-y-4">
              <div className="p-4 rounded-xl bg-surfaceSubtle border border-line-subtle flex items-center justify-between">
                <div>
                  <h2 className="text-base font-semibold text-text-strong">
                    {t("admin.console.modulesTitle")}
                  </h2>
                  <p className="text-xs text-text-muted">
                    {t("admin.console.modulesDesc")}
                  </p>
                </div>
                <button
                  type="button"
                  onClick={loadOverview}
                  aria-label={t("common.refresh")}
                  className="p-2 rounded-lg bg-surfaceSubtle hover:bg-surfaceHover text-xs text-text-body"
                >
                  <RefreshCw className="w-4 h-4" aria-hidden="true" />
                </button>
              </div>

              {overviewFailed && <p role="alert" className="text-xs text-danger">{t("admin.console.overviewFailed")}</p>}
              <div className="grid grid-cols-1 sm:grid-cols-2 gap-4" aria-busy={overviewLoading}>
                {modules.map((mod) => (
                  <div
                    key={mod.id}
                    className="p-4 rounded-xl border border-line-subtle bg-surfaceSubtle flex items-start justify-between gap-4"
                  >
                    <div className="space-y-1">
                      <div className="flex items-center gap-2">
                        <span className="font-semibold text-text-strong text-sm font-mono">{mod.id}</span>
                      </div>

                    </div>

                    <span
                      className={`px-3 py-1.5 rounded-lg text-xs font-semibold ${
                        mod.enabled
                          ? "bg-emerald-500/15 text-success"
                          : "bg-rose-500/15 text-danger"
                      }`}
                    >
                      {mod.enabled ? t("admin.console.deployed") : t("admin.console.notDeployed")}
                    </span>
                  </div>
                ))}
              </div>
            </div>
          )}

          {currentTab === "exchange" && <ExchangeTab />}
          {currentTab === "assistant" && <AssistantAdmin />}
          {currentTab === "publicSources" && <PublicSourcesAdmin />}
          {currentTab === "requestLogs" && <CatalogRequestLogsAdmin />}
          </div>
        </TabPanel>
      </PageContainer>

      <ConfirmDialog open={mergePreview != null} title={t("admin.console.mergeTitle")}
        message={<div className="space-y-3">
          <p>{t("admin.console.mergeConfirm")}</p>
          {[ ["admin.console.sourceUuid", mergePreview?.source], ["admin.console.targetUuid", mergePreview?.target] ].map(([label, entity]) => {
            const row = entity as Record<string, any> | undefined;
            return <div key={String(label)} className="rounded-lg border border-line p-2 text-text-body">
              <div className="text-text-muted">{t(String(label))}</div>
              <div className="font-semibold break-words">{row?.title} · {row ? kindLabel(row.kind) : ""} · v{row?.version}</div>
              <div className="font-mono break-all">{row?.id}</div>
            </div>;
          })}
          <p className="break-words">{mergePreview?.note}</p>
        </div>}
        confirmLabel={t("admin.console.executeMerge")} busy={merging}
        onClose={() => { if (!merging) setMergePreview(null); }} onConfirm={() => void confirmMerge()} />

    </div>
  );
}
