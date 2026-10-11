"use client";

// 目录域管理台：只覆盖元数据目录自己的工作面（概览 / 条目 / 定义 / 外部来源 / 货架 / 审核 /
// 合并 / 子系统能力 / 实例交换），限流运维页按服务权限提供共享入口。账号、社区、存储的其他管理工作仍在独立应用
// （deploy/nginx.conf 的 /admin/account|community|storage/ 三条 location），这里只留入口：
// 左栏底部的「其他控制台」，按权限码与探活结果收敛——未部署的域不出现死链。

import React, { useEffect, useMemo, useRef, useState, Suspense } from "react";
import { LoadingFallback } from "@/components/common/LoadingFallback";
import Link from "next/link";
import { useAuth } from "@/lib/authContext";
import { useI18n } from "@/i18n/I18nProvider";
import { DefinitionsEditor } from "@/components/catalog/DefinitionsEditor";
import { Evidence } from "@/components/catalog/Fields";
import type { Source } from "@/components/catalog/api";
import { catalogEvidenceError } from "@/lib/catalogEvidence";
import { useDefinitions, getKindName, resolveKindOptions } from "@/lib/definitions";
import { PageContainer } from "@/components/ui/PageShell";
import { Select } from "@/components/ui/Select";
import { LocaleSwitcher } from "@/components/LocaleSwitcher";
import { ThemePicker } from "@/components/ThemePicker";
import { ThemeModeSwitcher } from "@/components/ThemeModeSwitcher";
import { TabPanel } from "@/components/ui/TabPanel";
import { ExternalDatabasesTab } from "./components/tabs/ExternalDatabasesTab";
import { RateLimitsTab } from "./components/tabs/RateLimitsTab";
import { ShelvesTab } from "./components/tabs/ShelvesTab";
import { ExchangeTab } from "./components/tabs/ExchangeTab";
import {
  AUTH_GROUPS_MANAGE,
  AUTH_AUDIT_READ,
  AUTH_INVITES_MANAGE,
  AUTH_OAUTH_MANAGE,
  AUTH_SETTINGS_MANAGE,
  AUTH_USERS_MANAGE,
  COMMUNITY_BOARD_MANAGE,
  COMMUNITY_POST_MODERATE,
  COMMUNITY_REPORT_REVIEW,
  COMMUNITY_RATE_LIMITS_MANAGE,
  COMMUNITY_TOPIC_PIN,
  STORAGE_ASSET_MODERATE,
  CATALOG_LIFECYCLE_MANAGE,
  can,
  canEnterAdmin,
  canEnterCatalogConsole,
} from "@/lib/permissions";
import { fetchApi, unpublishEntity } from "@/lib/api";
import { localizeCatalogError } from "@/lib/catalogErrors";
import { ConfirmDialog } from "@/components/oauth/ConfirmDialog";
import type { LucideIcon } from "lucide-react";
import {
  Shield,
  LayoutDashboard,
  Sliders,
  CheckSquare,
  GitMerge,
  Cpu,
  ArrowLeft,
  RefreshCw,
  Layers,
  Search,
  Plus,
  ArrowUpRight,
  Globe,
  HardDrive,
  MessageSquare,
  ShieldCheck,
  Gauge,
} from "lucide-react";

import { isCompatibleMergeTarget } from "@/lib/mergeTarget";
import { canUseCatalogAdminTab, type CatalogAdminTab as AdminTab } from "@/lib/catalogAdminNavigation";

const NAV_GROUPS: { labelKey: string; tabs: { id: AdminTab; labelKey: string; icon: LucideIcon }[] }[] = [
  { labelKey: "admin.nav.work", tabs: [
    { id: "overview", labelKey: "admin.tab.overview", icon: LayoutDashboard },
    { id: "reviews", labelKey: "admin.tab.reviews", icon: CheckSquare },
    { id: "entities", labelKey: "admin.nav.entities", icon: Layers },
    { id: "merge", labelKey: "admin.tab.merge", icon: GitMerge },
  ] },
  { labelKey: "admin.nav.catalogConfig", tabs: [
    { id: "definitions", labelKey: "admin.tab.definitions", icon: Sliders },
    { id: "shelves", labelKey: "admin.tab.shelves", icon: LayoutDashboard },
    { id: "extdb", labelKey: "admin.tab.extdb", icon: Globe },
  ] },
  { labelKey: "admin.nav.operations", tabs: [
    { id: "ratelimits", labelKey: "admin.tab.ratelimits", icon: Gauge },
    { id: "modules", labelKey: "admin.tab.modules", icon: Cpu },
    { id: "exchange", labelKey: "admin.tab.exchange", icon: ArrowUpRight },
  ] },
];
const PAGE_SIZE = 25;

type ConsoleId = "account" | "community" | "storage";
type ConsoleState = "unknown" | "online" | "offline";

// 其他控制台：三个已拆出去的管理台的入口定义。
//   * permissions 与目标应用自己的入口判定同集合：账号域六码任一（metafusion-auth/admin
//     的 SECTION_PERMISSIONS）、社区治理四码任一（community 的 GOVERNANCE_CODES，不含
//     普通发帖码 community.post.create）、存储只认治理码 storage.asset.moderate。
//     存储这里刻意不含 storage.asset.upload：它是普通成员"上传并登记自己的资产"的功能码
//     （种子 member 组默认就带），当准入用会让每个注册成员——含只做目录编目的编辑者——
//     都看到管理台入口，进去却只有一个 403 的空壳；治理码才代表"能管别人的资产"。
//   * href 带尾斜杠：nginx 对裸路径只回 301，直接用带尾斜杠的地址省一次跳转；
//   * target=_blank 见渲染处：点进去是另一个应用，不是本控制台的页签。
const OTHER_CONSOLES: {
  id: ConsoleId;
  href: string;
  labelKey: string;
  icon: LucideIcon;
  permissions: string[];
}[] = [
  {
    id: "account",
    href: "/admin/account/",
    labelKey: "admin.consoles.account",
    icon: ShieldCheck,
    permissions: [AUTH_USERS_MANAGE, AUTH_GROUPS_MANAGE, AUTH_INVITES_MANAGE, AUTH_SETTINGS_MANAGE, AUTH_OAUTH_MANAGE, AUTH_AUDIT_READ],
  },
  {
    id: "community",
    href: "/admin/community/",
    labelKey: "admin.consoles.community",
    icon: MessageSquare,
    permissions: [COMMUNITY_BOARD_MANAGE, COMMUNITY_POST_MODERATE, COMMUNITY_TOPIC_PIN, COMMUNITY_REPORT_REVIEW],
  },
  {
    id: "storage",
    href: "/admin/storage/",
    labelKey: "admin.consoles.storage",
    icon: HardDrive,
    permissions: [STORAGE_ASSET_MODERATE],
  },
];

// 聚合计数的取值口径：只接受有限数字。字段缺失（端点没有这个键）、类型不对、
// 整个响应拿不到，都返回 null，调用方据此保留占位符——"取不到"与"真的是 0"必须分开。
const countOrNull = (v: unknown): number | null =>
  typeof v === "number" && Number.isFinite(v) ? v : null;

function AdminInner() {
  const { user, loading: authLoading } = useAuth();
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
  }, [visibleTabsKey]);
  // 四个计数各为 null 表示"还没拿到或取不到"：卡片显示占位符，不把没取到的数当成 0 讲成事实。
  const [stats, setStats] = useState<{
    pending: number | null;
    published: number | null;
    tombstones: number | null;
    extDatabases: number | null;
  }>({ pending: null, published: null, tombstones: null, extDatabases: null });
  // 其他控制台的探活结果：unknown 表示还没探到——未知不等于在线，这时入口不渲染。
  const [consoles, setConsoles] = useState<Record<string, ConsoleState>>({});
  const [modules, setModules] = useState<any[]>([]);
  const [pendingItems, setPendingItems] = useState<any[]>([]);
  // 审核台要能看的不只是待审：已发布条目下架（published → draft）是审核工作的一部分，
  // 而它只能走 POST /entities/:id/unpublish（保存端点拒绝降级）。所以列表按状态分档取数。
  const [reviewStatus, setReviewStatus] = useState<"pending_review" | "published">("pending_review");
  const [unpublishTarget, setUnpublishTarget] = useState<any | null>(null);
  const [unpublishing, setUnpublishing] = useState(false);
  const [statusTarget, setStatusTarget] = useState<{ id: string; action: "published" | "draft"; expectedVersion: number; fromEntities: boolean } | null>(null);
  const [lifecycleNote, setLifecycleNote] = useState("");
  const [lifecycleSources, setLifecycleSources] = useState<Source[]>([{ kind: "url", citation: "", url: "" }]);
  const [reviewNotice, setReviewNotice] = useState("");
  const [reviewError, setReviewError] = useState(false);
  const [actingId, setActingId] = useState("");
  const statusActionBusy = useRef(false);
  // 取数失败必须与「没有待审条目」分开：管理员看到空列表会以为队列已清空（见 loadReviewList）。
  const [reviewListFailed, setReviewListFailed] = useState(false);
  const [reviewLoading, setReviewLoading] = useState(false);
  const [reviewPage, setReviewPage] = useState(1);
  const [reviewTotal, setReviewTotal] = useState<number | null>(null);
  const reviewRequest = useRef(0);

  // Entities management state
  const [entitiesList, setEntitiesList] = useState<any[]>([]);
  const [entitiesListFailed, setEntitiesListFailed] = useState(false);
  const [entitiesLoading, setEntitiesLoading] = useState(false);
  const [entitiesQ, setEntitiesQ] = useState("");
  const [entitiesKind, setEntitiesKind] = useState("all");
  const [entitiesStatus, setEntitiesStatus] = useState("all");
  const [entitiesPage, setEntitiesPage] = useState(1);
  const [entitiesTotal, setEntitiesTotal] = useState<number | null>(null);
  const entitiesRequest = useRef(0);

  // Merge form states
  const [mergeSource, setMergeSource] = useState("");
  const [mergeTarget, setMergeTarget] = useState("");
  const [mergeNote, setMergeNote] = useState("");
  const [mergeSources, setMergeSources] = useState<Source[]>([{ kind: "url", citation: "", url: "" }]);
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
    setStats({ pending: null, published: null, tombstones: null, extDatabases: null });
    setModules([]);
    const results = await Promise.allSettled([
      mayManageLifecycle ? fetchApi<{statuses: Record<string, number>}>("/catalog/entities/stats") : Promise.resolve(null),
      fetchApi<{items: unknown[]}>("/catalog/external-databases"),
      fetchApi<{modules: any[]}>("/capabilities"),
    ]);
    if (request !== overviewRequest.current) return;
    let failed = results.some((result) => result.status === "rejected");
    const counts = results[0];
    if (counts.status === "fulfilled" && counts.value) {
      const statuses = counts.value.statuses;
      const pending = countOrNull(statuses?.pending_review);
      const published = countOrNull(statuses?.published);
      const deleted = countOrNull(statuses?.deleted);
      const merged = countOrNull(statuses?.merged);
      if ([pending, published, deleted, merged].some((v) => v === null)) failed = true;
      setStats((prev) => ({...prev, pending, published, tombstones: deleted !== null && merged !== null ? deleted + merged : null}));
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

  const loadReviewList = () => {
    const request = ++reviewRequest.current;
    // 服务端返回真实 total；分页避免审核队列超过首屏后，后续条目在后台永远不可操作。
    setReviewLoading(true);
    setReviewListFailed(false);
    fetch(`/api/catalog/entities?status=${reviewStatus}&limit=${PAGE_SIZE}&page=${reviewPage}`, { credentials: "same-origin" })
      .then((r) => {
        if (!r.ok) throw new Error(String(r.status));
        return r.json();
      })
      .then((d) => {
        if (request !== reviewRequest.current) return;
        if (!Array.isArray(d.items) || countOrNull(d.total) === null) throw new Error("invalid_response");
        setPendingItems(d.items);
        setReviewTotal(countOrNull(d.total));
      })
      .catch(() => {
        if (request !== reviewRequest.current) return;
        setPendingItems([]);
        setReviewTotal(null);
        setReviewListFailed(true);
      })
      .finally(() => { if (request === reviewRequest.current) setReviewLoading(false); });
  };

  const loadEntities = (page = entitiesPage) => {
    const request = ++entitiesRequest.current;
    setEntitiesLoading(true);
    const params = new URLSearchParams();
    if (entitiesKind !== "all") params.set("kind", entitiesKind);
    if (entitiesStatus !== "all") params.set("status", entitiesStatus);
    if (entitiesQ.trim()) params.set("q", entitiesQ.trim());
    params.set("limit", String(PAGE_SIZE));
    params.set("page", String(page));

    setEntitiesListFailed(false);
    fetch(`/api/catalog/entities?${params.toString()}`, { credentials: "same-origin" })
      .then((r) => {
        if (!r.ok) throw new Error(String(r.status));
        return r.json();
      })
      .then((d) => {
        if (request !== entitiesRequest.current) return;
        if (!Array.isArray(d.items) || countOrNull(d.total) === null) throw new Error("invalid_response");
        setEntitiesList(d.items);
        setEntitiesTotal(countOrNull(d.total));
      })
      .catch(() => {
        if (request !== entitiesRequest.current) return;
        setEntitiesList([]);
        setEntitiesTotal(null);
        setEntitiesListFailed(true);
      })
      .finally(() => { if (request === entitiesRequest.current) setEntitiesLoading(false); });
  };

  // 两个取数闸门，别混成一个（原来只认 role==="admin"，后台分配了管理权限组但 role 仍是
  // user 的成员能进 /admin 却永远看不到概览与实体列表，空面板冒充"无数据"）：
  //   · mayEnterConsoles —— 控制台探活。只持账号域权限的人也会落到入口页，而那个页面
  //     要靠探活结果标"未部署"，所以这里是并集；
  //   · mayEnterCatalog —— 概览 / 实体 / 审核这些取数全是目录域端点，只该由目录域权限触发，
  //     否则账号域管理员一进 /admin 就打出一串注定 403 的请求。
  const mayEnterConsoles = canEnterAdmin(user);
  const mayEnterCatalog = canEnterCatalogConsole(user);

  // 各域管理台挂载时探活一次：2.5s 超时、no-store，只认 HTTP 200。
  // 三个应用的健康体并不一致（auth/storage 是 {"ok":true}，community 是 {"status":"ok"}），
  // 所以不能按字段判定，只看状态码；超时 / 404 / 网络失败一律当未部署，静默隐藏入口。
  // 主站本地开发下这三条路径没有代理，会稳定 404 —— 降级结果就是"看不到入口"，不是报错。
  useEffect(() => {
    if (!mayEnterConsoles) return;
    const controller = new AbortController();
    const timer = window.setTimeout(() => controller.abort(), 2500);
    let alive = true;
    void Promise.all(
      OTHER_CONSOLES.map(async (item) => {
        try {
          const res = await fetch(`${item.href}api/health`, {
            cache: "no-store",
            credentials: "same-origin",
            signal: controller.signal,
          });
          return [item.id, res.ok ? "online" : "offline"] as const;
        } catch {
          return [item.id, "offline"] as const;
        }
      }),
    )
      .then((entries) => {
        window.clearTimeout(timer);
        if (alive) setConsoles(Object.fromEntries(entries) as Record<ConsoleId, ConsoleState>);
      })
      .catch(() => {});
    return () => {
      alive = false;
      window.clearTimeout(timer);
      controller.abort();
    };
  }, [mayEnterConsoles]);

  useEffect(() => {
    if (mayEnterCatalog) {
      loadOverview();
    }
    return () => { overviewRequest.current += 1; };
  }, [mayEnterCatalog, user?.id, mayManageLifecycle]);

  useEffect(() => {
    if (currentTab === "entities" && mayEnterCatalog) {
      loadEntities();
    }
  }, [currentTab, entitiesKind, entitiesStatus, entitiesPage, mayEnterCatalog]);

  useEffect(() => {
    if (currentTab === "reviews" && mayEnterCatalog) {
      loadReviewList();
    }
  }, [currentTab, reviewStatus, reviewPage, mayEnterCatalog]);

  // PUT 是整份替换：先读全量再改状态，编辑凭据由管理员明确填写。
  const submitEntityStatus = async (id: string, status: "published" | "draft", note: string, expectedVersion: number, sources: Source[]) => {
    const full = await fetchApi<Record<string, any>>(`/catalog/entities/${id}`);
    if (full.version !== expectedVersion) throw new Error("version_conflict");
    const { updated_at: _updatedAt, ...doc } = full;
    await fetchApi(`/catalog/entities/${id}`, {
      method: "PUT",
      body: JSON.stringify({
        entity: { ...doc, id, status },
        expected_version: expectedVersion,
        edit_note: note,
        sources,
      }),
    });
  };

  // 通过（草稿/待审 → 已发布）只能走保存端点：它是唯一能把状态写成 published 的写路径；
  // 生命周期端点 POST …/lifecycle 只做删除与合并（lifecycle.go 恒把状态置 deleted/merged）。
  // 反向的 published → draft 被 store.go 的 use_lifecycle_endpoint 拦住，只能走
  // POST …/unpublish（见 handleUnpublish）。
  const handleStatusAction = async (id: string, action: "published" | "draft", expectedVersion: number, fromEntities = false) => {
    setLifecycleNote("");
    setLifecycleSources([{ kind: "url", citation: "", url: "" }]);
    setReviewNotice(""); setReviewError(false);
    setStatusTarget({ id, action, expectedVersion, fromEntities });
  };
  const confirmStatusAction = async () => {
    if (!statusTarget) return;
    const { id, action, expectedVersion, fromEntities } = statusTarget;
    const evidenceError = catalogEvidenceError(lifecycleNote, lifecycleSources, window.location.origin);
    if (evidenceError) {
      setReviewError(true); setReviewNotice(localizeCatalogError(evidenceError, t)); return;
    }
    if (!mayManageLifecycle || statusActionBusy.current) return;
    statusActionBusy.current = true;
    setActingId(id);
    setReviewNotice("");
    setReviewError(false);
    try {
      await submitEntityStatus(id, action, lifecycleNote.trim(), expectedVersion, lifecycleSources);
      setStatusTarget(null);
      if (fromEntities) loadEntities(); else loadReviewList();
      void loadOverview();
      setReviewNotice(t(action === "published" ? "admin.reviews.approved" : "admin.reviews.rejected"));
    } catch (e) {
      setReviewError(true);
      setReviewNotice(localizeCatalogError(String((e as Error).message || e), t));
    } finally {
      statusActionBusy.current = false;
      setActingId("");
    }
  };

  // 下架（published → draft）只能走 /unpublish：保存端点对 published 降级回
  // 400 use_lifecycle_endpoint，生命周期端点只写 deleted/merged。expected_version 用
  // 当前行的版本，服务端把它放进 WHERE 做乐观并发——别人先改过就是 409，不会覆盖掉。
  const handleUnpublish = async () => {
    if (!unpublishTarget || unpublishing) return;
    const evidenceError = catalogEvidenceError(lifecycleNote, lifecycleSources, window.location.origin);
    if (evidenceError) {
      setReviewError(true); setReviewNotice(localizeCatalogError(evidenceError, t)); return;
    }
    setUnpublishing(true);
    setReviewError(false);
    setReviewNotice("");
    try {
      await unpublishEntity({
        entity_id: unpublishTarget.id,
        expected_version: Number(unpublishTarget.version) || 1,
        edit_note: lifecycleNote.trim(),
        sources: lifecycleSources,
      });
      setReviewNotice(t("admin.reviews.unpublished"));
      setUnpublishTarget(null);
      loadReviewList();
      loadEntities();
      loadOverview();
    } catch (e) {
      // 下架有两种拒绝最值得单独讲清：状态已经变了（列表是旧的）与版本冲突（别人先改过）。
      // 其余错误码统一走码表，不把裸码渲染给用户。
      setReviewError(true);
      const raw = String((e as Error).message || e);
      const code = raw.trim().split(":")[0]?.trim();
      setReviewNotice(
        code === "invalid_status"
          ? t("admin.reviews.unpublishInvalidStatus")
          : code === "version_conflict"
            ? t("admin.reviews.unpublishVersionConflict")
            : localizeCatalogError(raw, t),
      );
    } finally {
      setUnpublishing(false);
    }
  };


  // 这里刻意没有"启停"动作：运行时模块开关已随子系统拆分退役，
  // 能力是否可用由部署决定（服务在不在、配置没配置），面板只呈现事实（enabled），
  // 不提供会必然失败的按钮——要开能力就去部署对应服务。

  const handleMergeSubmit = async (e: React.FormEvent) => {
    e.preventDefault();
    const sourceId = mergeSource.trim();
    const targetId = mergeTarget.trim();
    if (mergeBusy.current || !mayManageLifecycle) return;
    const evidenceError = catalogEvidenceError(mergeNote, mergeSources, window.location.origin);
    if (evidenceError) {
      setMergeError(true); setMergeMessage(localizeCatalogError(evidenceError, t)); return;
    }
    mergeBusy.current = true;
    setMerging(true);
    setMergeMessage("");
    setMergeError(false);
    try {
      // 前置校验与 lifecycle.go 的合并约束同一口径：源与目标要读得到、同 kind、
      // 同归属（作品/发行版/载体/父级/内容单元）、目标已发布、源不是已删除/已合并。
      // 少任何一条，服务端只会回 invalid_merge_target / invalid_status——先在本地讲清楚。
      const pair = await Promise.all([
        fetchApi<Record<string, any>>(`/catalog/entities/${encodeURIComponent(sourceId)}`),
        fetchApi<Record<string, any>>(`/catalog/entities/${encodeURIComponent(targetId)}`),
      ]);
      const [source, target] = pair;
      if (source.status === "deleted" || source.status === "merged") {
        setMergeError(true);
        setMergeMessage(t("admin.console.mergeSourceRetired"));
        return;
      }
      if (!isCompatibleMergeTarget(source, target)) {
        setMergeError(true);
        setMergeMessage(t("admin.console.mergeInvalidTarget"));
        return;
      }
      setMergePreview({source, target, note: mergeNote.trim(), sources: structuredClone(mergeSources)});
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
      setMergeSources([{ kind: "url", citation: "", url: "" }]);
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

  if (authLoading) {
    return (
      <div className="min-h-screen bg-background flex items-center justify-center text-text-faint font-mono text-xs">
        <RefreshCw className="w-5 h-5 animate-spin text-primary mr-2" />
        {t("admin.console.checkingPerms")}
      </div>
    );
  }

  // 这个人真的能进的域：按权限筛，探活结果与左栏入口同源（不重复请求）。
  // 必须在准入分支之前算：下面要用它决定"给控制台入口页，还是给权限不足页"。
  const permittedConsoles = OTHER_CONSOLES.filter((item) => item.permissions.some((code) => can(user, code)));

  if (!user || !canEnterAdmin(user)) {
    return (
      <div className="min-h-screen bg-background flex flex-col items-center justify-center p-6 text-center">
        <div className="w-12 h-12 rounded-full bg-rose-500/10 border border-rose-500/20 text-danger flex items-center justify-center mb-4">
          <Shield className="w-6 h-6" />
        </div>
        <h1 className="text-xl font-bold text-text-strong mb-2">
          {t("admin.console.permRequired")}
        </h1>
        <p className="text-sm text-text-muted max-w-md mb-6">
          {t("admin.console.permDesc")}
        </p>
        <Link
          href="/account"
          className="px-4 py-2 rounded-lg bg-primary hover:bg-primary/90 text-white text-xs font-medium"
        >
          {t("admin.console.goLogin")}
        </Link>
      </div>
    );
  }

  // 能进管理台、但没有目录域权限（例如只持 auth.users.manage）：这里绝不能把人放进目录外壳——
  // 本页页签全是目录工作面，对他一片 403，看着像管理台坏了。只给"你能进的控制台"入口页。
  // 未探活到在线的域不渲染成链接（与左栏同口径：不给死链），但保留一行说明它存在。
  if (!canEnterCatalogConsole(user) && !can(user, COMMUNITY_RATE_LIMITS_MANAGE)) {
    return (
      <div className="min-h-screen bg-background text-text-strong">
        <PageContainer width="narrow" className="py-12">
          <div>
            <h1 className="text-xl font-bold text-text-strong mb-2">
              {t("admin.consoles.pickTitle")}
            </h1>
            <p className="text-sm text-text-muted leading-relaxed mb-6">
              {t("admin.consoles.pickDesc")}
            </p>
            <div className="flex flex-col gap-3">
              {permittedConsoles.map((item) => {
                const Icon = item.icon;
                const online = consoles[item.id] === "online";
                const inner = (
                  <>
                    <Icon className="w-5 h-5 shrink-0 text-text-muted" />
                    <span className="flex-1 text-sm font-medium">{t(item.labelKey)}</span>
                    {online ? null : (
                      <span className="text-[10px] font-mono text-text-faint">
                        {t("admin.console.disabled")}
                      </span>
                    )}
                    <ArrowUpRight className="w-4 h-4 shrink-0 text-text-faint" />
                  </>
                );
                return online ? (
                  <a
                    key={item.id}
                    href={item.href}
                    target="_blank"
                    rel="noreferrer"
                    className="flex items-center gap-3 px-4 py-3.5 rounded-xl border border-line-subtle bg-surfaceSubtle hover:bg-surfaceHover transition-colors"
                  >
                    {inner}
                  </a>
                ) : (
                  <div
                    key={item.id}
                    className="flex items-center gap-3 px-4 py-3.5 rounded-xl border border-line-subtle bg-surfaceSubtle opacity-60"
                  >
                    {inner}
                  </div>
                );
              })}
            </div>
            <Link
              href="/account"
              className="inline-block mt-8 text-xs text-primary hover:underline"
            >
              {t("admin.console.goLogin")}
            </Link>
          </div>
        </PageContainer>
      </div>
    );
  }

  // 目录域工作面按任务组织，页签可通过 hash 直达；其余三个域仍是独立应用。

  // 左栏入口 = 有该域管理码 且 探活到在线；探活没回来时一律不渲染，免得闪出一个点进去 404 的链接。
  const consoleEntries = OTHER_CONSOLES.filter(
    (item) => consoles[item.id] === "online" && item.permissions.some((code) => can(user, code)),
  );

  return (
    <div className="min-h-screen flex flex-col bg-background text-text-strong">
      {/* Admin Topbar */}
      <header className="border-b border-line bg-surface/90 backdrop-blur sticky top-0 z-30">
        <PageContainer className="h-14 flex items-center justify-between gap-2">
          <div className="flex min-w-0 flex-1 items-center gap-2 sm:gap-3">
            <Link
              href="/home"
              aria-label={t("admin.console.backToSite")}
              title={t("admin.console.backToSite")}
              className="flex h-11 w-11 sm:w-auto shrink-0 items-center justify-center gap-1 text-xs text-text-muted hover:text-text-strong transition-colors duration-fast ease-soft"
            >
              <ArrowLeft className="w-4 h-4" />
              <span className="hidden sm:inline">{t("admin.console.backToSite")}</span>
            </Link>
            <span className="hidden text-text-faint sm:inline">/</span>
            <div className="min-w-0 font-semibold text-sm text-text-strong">
              <span className="block truncate">{t("admin.console.consoleTitle")}</span>
            </div>
          </div>

          <div className="flex shrink-0 items-center gap-2 text-xs font-mono text-text-muted">
            <LocaleSwitcher compact />
            <ThemeModeSwitcher />
            <ThemePicker />
            <span className="hidden sm:inline-flex max-w-40 truncate px-2 py-0.5 rounded bg-primary/20 text-primary border border-primary/30">
              {user.username}
            </span>
          </div>
        </PageContainer>
      </header>

      <PageContainer className="py-6 flex-1 flex flex-col lg:flex-row gap-6">
        {/* Left Sidebar */}
        <aside className="w-full lg:w-64 shrink-0 lg:sticky lg:top-20 lg:self-start lg:max-h-[calc(100vh-6rem)] lg:overflow-y-auto">
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
          {/* 粘附偏移只需包含管理台顶栏。 */}
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
                        <span className="rounded-full bg-amber-500/15 px-2 py-0.5 text-[11px] text-warn">{stats.pending}</span>
                      ) : null}
                    </button>
                  );
                })}
              </div>
            ))}
          </nav>

          {/* 其他控制台：独立应用，故 target=_blank；探不到在线的域整条不出现（无死链）。 */}
          {consoleEntries.length > 0 && (
            <div className="mt-3 pt-3 border-t border-line-subtle">
              <div className="px-3 mb-1.5 text-[10px] font-mono uppercase tracking-wide text-text-faint">
                {t("admin.consoles.title")}
              </div>
              <div className="grid grid-cols-1 sm:grid-cols-3 lg:flex lg:flex-col gap-1">
                {consoleEntries.map((item) => {
                  const Icon = item.icon;
                  return (
                    <a
                      key={item.id}
                      href={item.href}
                      target="_blank"
                      rel="noreferrer"
                      className="flex items-center gap-3 px-3.5 py-2.5 rounded-xl text-xs font-medium text-text-muted hover:text-text-strong hover:bg-surfaceHover transition-all whitespace-nowrap"
                    >
                      <Icon className="w-4 h-4 shrink-0" />
                      <span>{t(item.labelKey)}</span>
                      <ArrowUpRight className="w-3.5 h-3.5 shrink-0 text-text-faint" />
                    </a>
                  );
                })}
              </div>
            </div>
          )}
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
                <button type="button" onClick={() => { setEntitiesStatus("published"); setEntitiesPage(1); selectTab("entities"); }} className="p-4 rounded-xl border border-line-subtle bg-surfaceSubtle hover:border-primary/40 hover:bg-surfaceHover text-left transition-colors cursor-pointer">
                  <div className="text-xs text-text-muted font-mono mb-1">
                    {t("admin.console.publishedEntities")}
                  </div>
                  <div className="text-2xl font-bold text-success">{stats.published ?? "—"}</div>
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

              {reviewNotice && (
                <div role={reviewError ? "alert" : "status"} className={`p-3 rounded-xl border text-xs ${reviewError ? "border-danger/30 bg-danger/10 text-danger" : "border-success/30 bg-success/10 text-success"}`}>
                  {reviewNotice}
                </div>
              )}

              {/* Filter Row */}
              <div className="grid grid-cols-1 sm:grid-cols-12 gap-3 p-3.5 rounded-xl bg-surfaceSubtle border border-line-subtle">
                <form
                  onSubmit={(e) => {
                    e.preventDefault();
                    if (entitiesPage === 1) loadEntities(1);
                    else setEntitiesPage(1);
                  }}
                  className="sm:col-span-5 relative flex items-center"
                >
                  <Search className="absolute left-3 w-4 h-4 text-text-faint" />
                  <input
                    type="text"
                    value={entitiesQ}
                    onChange={(e) => setEntitiesQ(e.target.value)}
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

                <div className="sm:col-span-4">
                  <Select
                    value={entitiesStatus}
                    onChange={(v) => { setEntitiesStatus(v); setEntitiesPage(1); }}
                    options={[
                      { value: "all", label: t("catalog.allStates") },
                      { value: "published", label: t("catalog.status.published") },
                      { value: "pending_review", label: t("catalog.status.pending_review") },
                      { value: "draft", label: t("catalog.status.draft") },
                    ]}
                    aria-label={t("catalog.status")}
                  />
                </div>
              </div>
              <p className="text-xs text-text-faint">{t("admin.entities.activeOnlyHint")}</p>

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
                        <tr key={e.id} className="hover:bg-surfaceSubtle transition-colors duration-fast ease-soft">
                          <td className="py-2.5 px-3">
                            <Link href={`/catalog/${e.id}`} className="font-semibold text-text-strong hover:text-primary transition-colors duration-fast ease-soft line-clamp-1">
                              {e.title}
                            </Link>
                            <div className="text-[10px] text-text-faint font-mono">ID: {e.id}</div>
                          </td>
                          <td className="py-2.5 px-3">
                            <span className="px-1.5 py-0.5 rounded bg-surfaceSubtle text-[10px] font-mono">
                              {kindLabel(e.kind)}
                            </span>
                          </td>
                          <td className="py-2.5 px-3">
                            <span className={`px-1.5 py-0.5 rounded text-[10px] font-mono ${
                              e.status === "published" ? "bg-emerald-500/20 text-success" :
                              e.status === "pending_review" ? "bg-amber-500/20 text-warn" :
                              e.status === "deleted" ? "bg-rose-500/20 text-danger" :
                              e.status === "merged" ? "bg-purple-500/20 text-alt" :
                              "bg-surfaceSubtle text-text-muted"
                            }`}>
                              {tr(`catalog.status.${e.status}`, e.status)}
                            </span>
                          </td>
                          <td className="py-2.5 px-3 font-mono text-[11px] text-text-muted">
                            v{e.version || 1}
                          </td>
                          <td className="py-2.5 px-3 text-right">
                            <div className="flex items-center justify-end gap-1.5">
                              <Link
                                href={`/catalog/${e.id}`}
                                className="px-2 py-1 rounded bg-surfaceSubtle hover:bg-surfaceHover text-text-body hover:text-text-strong text-[11px] transition-colors duration-fast ease-soft"
                              >
                                {t("admin.entities.edit")}
                              </Link>
                              {/* 只有草稿/待审能通过：保存端点拒绝写 deleted/merged，也拒绝
                                  published → draft（store.go 的 use_lifecycle_endpoint）；
                                  生命周期端点只做删除与合并。已发布条目改成走下架端点（见下），
                                  已删除/已合并连合并入口都不给。 */}
                              {mayManageLifecycle && (e.status === "draft" || e.status === "pending_review") && (
                                <button
                                  type="button"
                                  disabled={!!actingId}
                                  onClick={() => void handleStatusAction(e.id, "published", e.version, true)}
                                  className="px-2 py-1 rounded bg-emerald-500/15 hover:bg-emerald-500/25 text-success text-[11px] transition-colors duration-fast ease-soft cursor-pointer"
                                >
                                  {t("admin.entities.approve")}
                                </button>
                              )}
                              {mayManageLifecycle && e.status === "published" && (
                                // 已发布条目现在有真正的降级入口（/unpublish），不再是禁用态：
                                // 调用前二次确认，确认框里说明这一步的影响面。
                                <button
                                  type="button"
                                  onClick={() => {
                                    setReviewNotice("");
                                    setReviewError(false);
                                    setLifecycleNote("");
                                    setLifecycleSources([{ kind: "url", citation: "", url: "" }]);
                                    setUnpublishTarget(e);
                                  }}
                                  className="px-2 py-1 rounded bg-amber-500/15 hover:bg-amber-500/25 text-warn text-[11px] transition-colors duration-fast ease-soft cursor-pointer"
                                >
                                  {t("admin.entities.unpublish")}
                                </button>
                              )}
                              {mayManageLifecycle && e.status !== "deleted" && e.status !== "merged" && (
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
                    <button type="button" disabled={entitiesPage * PAGE_SIZE >= entitiesTotal} onClick={() => setEntitiesPage((p) => p + 1)} className="rounded-lg border border-line px-3 py-2 hover:bg-surfaceHover disabled:opacity-40">{t("catalog.nextPage")}</button>
                  </div>
                </div>
              )}
            </div>
          )}

          {currentTab === "definitions" && <DefinitionsEditor />}

          {currentTab === "extdb" && <ExternalDatabasesTab />}

          {currentTab === "shelves" && <ShelvesTab />}

          {currentTab === "ratelimits" && <RateLimitsTab />}

          {currentTab === "reviews" && (
            <div className="space-y-4">
              <div className="p-4 rounded-xl bg-surfaceSubtle border border-line-subtle flex items-center justify-between">
                <div>
                  <h2 className="text-base font-semibold text-text-strong">
                    {t("admin.console.reviewWorkbench")}
                  </h2>
                  <p className="text-xs text-text-muted">
                    {t("admin.console.reviewWorkbenchDesc")}
                  </p>
                </div>
                <button
                  type="button"
                  onClick={() => {
                    loadReviewList();
                    loadOverview();
                  }}
                  aria-label={t("common.refresh")}
                  className="p-2 rounded-lg bg-surfaceSubtle hover:bg-surfaceHover text-xs text-text-body"
                >
                  <RefreshCw className="w-4 h-4" />
                </button>
              </div>

              {/* 两个档位共用同一端点、不同 status 筛选：待审的入口是"通过/驳回"，
                  已发布的入口是"退回草稿"（只有 /unpublish 能降级）。 */}
              <div className="flex items-center gap-2">
                {(["pending_review", "published"] as const).map((status) => (
                  <button
                    key={status}
                    type="button"
                    onClick={() => { setReviewStatus(status); setReviewPage(1); }}
                    className={`px-3 py-1.5 rounded-lg text-xs font-medium transition-colors duration-fast ease-soft cursor-pointer ${
                      reviewStatus === status
                        ? "bg-primary/20 text-primary"
                        : "bg-surfaceSubtle hover:bg-surfaceHover text-text-muted"
                    }`}
                  >
                    {status === "pending_review"
                      ? `${t("admin.reviews.pending")} (${stats.pending ?? "—"})`
                      : t("admin.reviews.published")}
                  </button>
                ))}
              </div>

              {reviewNotice && (
                <div role={reviewError ? "alert" : "status"} className={`p-3 rounded-xl border text-xs ${reviewError ? "border-danger/30 bg-danger/10 text-danger" : "border-success/30 bg-success/10 text-success"}`}>
                  {reviewNotice}
                </div>
              )}

              {reviewLoading ? (
                <div className="py-8 text-center text-xs text-text-faint flex items-center justify-center gap-2">
                  <RefreshCw className="w-4 h-4 animate-spin text-primary" />{t("catalog.loading")}
                </div>
              ) : reviewListFailed ? (
                // 与实体列表同因：取数失败会被读成"队列已清空"（loadReviewList 也刻意把失败与空列表分开）。
                <div role="alert" className="p-8 rounded-xl border border-amber-500/30 bg-amber-500/5 text-center text-xs space-y-2">
                  <p className="text-amber-700 dark:text-warn-soft">{t("catalog.listFailed")}</p>
                  <button type="button" onClick={() => loadReviewList()} className="font-mono text-primary hover:underline cursor-pointer">
                    {t("catalog.retry")}
                  </button>
                </div>
              ) : pendingItems.length === 0 ? (
                <div className="p-8 rounded-xl border border-dashed border-line text-center text-xs text-text-faint font-mono">
                  {reviewStatus === "pending_review" ? t("admin.console.noPending") : t("admin.reviews.noPublished")}
                </div>
              ) : (
                <div className="space-y-3">
                  {pendingItems.map((item) => (
                    <div
                      key={item.id}
                      className="p-4 rounded-xl border border-line-subtle bg-surfaceSubtle flex flex-col sm:flex-row sm:items-center justify-between gap-4"
                    >
                      <div className="min-w-0 space-y-1">
                        <div className="flex flex-wrap items-center gap-2">
                          <span className="px-2 py-0.5 rounded bg-primary/20 text-primary text-[10px] font-mono uppercase">
                            {item.kind}
                          </span>
                          <span className="font-semibold text-sm text-text-strong">{item.title}</span>
                        </div>
                        <div className="text-xs text-text-faint font-mono break-all">
                          ID: {item.id} · v{item.version}
                        </div>
                      </div>

                      <div className="flex items-center gap-2 shrink-0">
                        <Link
                          href={`/catalog/${item.id}`}
                          className="px-3 py-1.5 rounded-lg bg-surfaceSubtle hover:bg-surfaceHover text-xs text-text-body"
                        >
                          {t("admin.console.inspect")}
                        </Link>
                        {/* 已发布条目不能"通过发布"（已经是发布态），也不该走保存端点降级：
                            唯一的出口是 /unpublish，所以两个档位的操作集合不同。 */}
                        {item.status === "published" ? (
                          <button
                            type="button"
                            onClick={() => {
                              setReviewNotice("");
                              setReviewError(false);
                              setLifecycleNote("");
                              setLifecycleSources([{ kind: "url", citation: "", url: "" }]);
                              setUnpublishTarget(item);
                            }}
                            className="px-3 py-1.5 rounded-lg bg-amber-500/20 hover:bg-amber-500/30 text-warn text-xs font-semibold cursor-pointer"
                          >
                            {t("admin.entities.unpublish")}
                          </button>
                        ) : (
                          <>
                            <button
                              type="button"
                              disabled={!!actingId}
                              onClick={() => void handleStatusAction(item.id, "published", item.version)}
                              className="px-3 py-1.5 rounded-lg bg-emerald-500/20 hover:bg-emerald-500/30 text-success text-xs font-semibold"
                            >
                              {t("admin.reviews.approve")}
                            </button>
                            <button
                              type="button"
                              disabled={!!actingId}
                              onClick={() => void handleStatusAction(item.id, "draft", item.version)}
                              className="px-3 py-1.5 rounded-lg bg-rose-500/20 hover:bg-rose-500/30 text-danger text-xs font-semibold"
                            >
                              {t("admin.entities.reject")}
                            </button>
                          </>
                        )}
                      </div>
                    </div>
                  ))}
                </div>
              )}
              {!reviewLoading && !reviewListFailed && reviewTotal != null && reviewTotal > 0 && (
                <div className="flex flex-wrap items-center justify-between gap-3 text-xs text-text-muted">
                  <span>{t("admin.pagination", { page: reviewPage, pages: Math.ceil(reviewTotal / PAGE_SIZE), total: reviewTotal })}</span>
                  <div className="flex gap-2">
                    <button type="button" disabled={reviewPage === 1} onClick={() => setReviewPage((p) => p - 1)} className="rounded-lg border border-line px-3 py-2 hover:bg-surfaceHover disabled:opacity-40">{t("catalog.prevPage")}</button>
                    <button type="button" disabled={reviewPage * PAGE_SIZE >= reviewTotal} onClick={() => setReviewPage((p) => p + 1)} className="rounded-lg border border-line px-3 py-2 hover:bg-surfaceHover disabled:opacity-40">{t("catalog.nextPage")}</button>
                  </div>
                </div>
              )}
            </div>
          )}

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
                    placeholder={t("catalog.compareUuidPlaceholder")}
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
                    placeholder={t("catalog.compareUuidPlaceholder")}
                    className="w-full p-2.5 rounded-lg bg-surfaceSubtle border border-line text-xs font-mono text-text-strong placeholder:text-text-faint focus:border-primary outline-none"
                  />
                </div>

                <div className="catalog-v2">
                  <Evidence note={mergeNote} setNote={setMergeNote} sources={mergeSources} setSources={setMergeSources} />
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
      {/* 状态变更明确采集编辑说明与事实凭据。 */}
      <ConfirmDialog
        open={statusTarget != null}
        title={t(statusTarget?.action === "published" ? "admin.reviews.approve" : "admin.reviews.reject")}
        message={<div className="catalog-v2 space-y-3">
          {statusTarget ? <Link href={`/catalog/${statusTarget.id}`} target="_blank" rel="noopener noreferrer" className="break-all text-primary hover:underline">{statusTarget.id}</Link> : null}
          <Evidence note={lifecycleNote} setNote={setLifecycleNote} sources={lifecycleSources} setSources={setLifecycleSources} />
          {reviewError && reviewNotice ? <p role="alert">{reviewNotice}</p> : null}
        </div>}
        confirmLabel={t(statusTarget?.action === "published" ? "admin.reviews.approve" : "admin.reviews.reject")}
        busy={!!actingId}
        onClose={() => { if (!actingId) setStatusTarget(null); }}
        onConfirm={() => void confirmStatusAction()}
      />
      <ConfirmDialog
        open={unpublishTarget != null}
        title={t("admin.entities.unpublish")}
        message={<div className="space-y-2"><p>{t("admin.entities.unpublishConfirm", {
          title: unpublishTarget?.title ?? "",
          version: String(unpublishTarget?.version ?? 1),
        })}</p><div className="catalog-v2">
          <Evidence note={lifecycleNote} setNote={setLifecycleNote} sources={lifecycleSources} setSources={setLifecycleSources} />
        </div>{reviewError && reviewNotice ? <p role="alert">{reviewNotice}</p> : null}</div>}
        confirmLabel={t("admin.entities.unpublish")}
        busy={unpublishing}
        onClose={() => { if (!unpublishing) setUnpublishTarget(null); }}
        onConfirm={() => void handleUnpublish()}
      />
    </div>
  );
}

export default function AdminPage() {
  return (
    <Suspense fallback={<LoadingFallback className="min-h-screen bg-background text-text-faint font-mono text-xs grid place-items-center" />}>
      <AdminInner />
    </Suspense>
  );
}
