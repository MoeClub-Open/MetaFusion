"use client";

import { useEffect, useState, useMemo, useRef, useId, Suspense } from "react";
import { safeCount } from "@/lib/api/fields";
import Link from "next/link";
import { useSearchParams, useRouter } from "next/navigation";
import { Navbar } from "@/components/Navbar";
import { EntityCard } from "@/components/common/EntityCard";
import { Pagination } from "@/components/common/Pagination";
import { kindIcon } from "@/lib/kindIcons";
import { useI18n } from "@/i18n/I18nProvider";
import { useDefinitions, getKindName, getTagName, getTagNames } from "@/lib/definitions";
import { pickRecordTitle } from "@/lib/titles";
import { coverUrl, type CoverBearing } from "@/lib/cover";
import { PageShell, PageHeader } from "@/components/ui/PageShell";
import { Card } from "@/components/ui/Card";
import { useTitleDisplayOrder } from "@/hooks/useTitleDisplayOrder";
import {
  Search,
  LayoutGrid,
  List as ListIcon,
  Layers,
  ArrowRight,
  ChevronLeft,
  RefreshCw,
  Check,
  SlidersHorizontal,
  ChevronDown,
  X,
} from "lucide-react";
import { TabPanel } from "@/components/ui/TabPanel";
import { Select } from "@/components/ui/Select";
import { languageLabel, searchLanguages } from "@/lib/languages";

/** 列表项：图片结构复用 lib/cover 的 CoverBearing（首张即封面只在那一处定义）。 */
interface EntityItem extends CoverBearing {
  id: string;
  kind: string;
  title: string;
  original_language?: string;
  attributes?: { tags?: string[] };
  status: string;
  version: number;
  work_id?: string;
  release_id?: string;
  translations?: Record<string, { title: string; summary?: string; aliases?: string[] }>;
}

// 各 kind 的图标统一由 lib/kindIcons 提供（卡片、列表、搜索联想共用同一份）。

function getLocalizedTitle(
  item: EntityItem,
  locale: string,
  order: string[] = [],
): string {
  return pickRecordTitle(locale, item.translations, item.title, {
    order,
    originalLanguage: item.original_language,
  });
}

function ExploreInner() {
  const { t, tr, locale } = useI18n();
  const searchParams = useSearchParams();
  const router = useRouter();
  const [filtersExpanded, setFiltersExpanded] = useState(false);
  const filterPanelId = useId();

  const { definitions, kinds } = useDefinitions();
  const titleOrder = useTitleDisplayOrder();

  const currentKind = searchParams.get("kind") || "all";
  const currentStatus = searchParams.get("status") || "published";
  const currentQ = searchParams.get("q") || "";
  // 标签可多选；默认任一命中，all 要求同时包含全部选中标签。
  const currentTagsMode = searchParams.get("tags_mode") || "any";
  const currentTags = useMemo(
    () => searchParams.getAll("tags").flatMap((v) => v.split(",")).map((s) => s.trim()).filter(Boolean),
    [searchParams],
  );
  // 原语言：document->>'original_language' 精确匹配（ja/zh/en…大小写按入库原样比）。
  const currentOriginalLanguage = searchParams.get("original_language") || "";
  // 仅有封面：document->'pictures' 为非空数组。
  const currentHasPictures = searchParams.get("has_pictures") === "1";
  // 页码解析必须挡住 NaN：`?page=abc` 经 parseInt 得到 NaN，Math.max(1, NaN) 仍是 NaN，
  // 于是 offset=NaN 被原样发给服务端（现在会被按非法参数 400 拒绝，页面就以"加载失败"告终）。
  // 非数字或小于 1 一律当第 1 页；数字但超出结果范围的越界页另有可读提示（见 outOfRange）。
  const cursorParam = searchParams.get("cursor") || "";
  const rawPageParam = searchParams.get("page") || "";
  const parsedPage = /^\d+$/.test(rawPageParam) ? parseInt(rawPageParam, 10) : 1;
  const currentPage = parsedPage >= 1 ? parsedPage : 1;
  // 排序：写进 URL（可深链、可后退），取值与后端白名单一致；非法取值由后端 400 拒绝，
  // 这里只把 URL 原样传给服务端——前端不静默改写用户给的参数。
  const sortParam = searchParams.get("sort") || "";
  const orderParam = searchParams.get("order") || "";
  const sortKey = sortParam + ":" + (orderParam || (sortParam === "title" ? "asc" : "desc"));
  // 下拉的选中值：默认（不带参）= 最近更新，与后端空值口径一致。
  const sortValue = !currentQ && sortKey === "updated_at:desc" ? "" : sortParam ? sortKey : "";
  const searchLocale = sortParam === "title" ? locale : "";
  const previousSearchLocale = useRef(searchLocale);
  const limit = 24;
  const offset = (currentPage - 1) * limit;

  // 标签云本地搜索：只过滤面板展示，不发请求。
  const [tagQuery, setTagQuery] = useState("");
  const [items, setItems] = useState<EntityItem[]>([]);
  // 结果总数（后端 total）：翻页判定必须以它为准——items.length 只是当前窗口，结果数是页宽整数倍时
  // 会误判"还有下一页"，点进去是没有数据的空页。
  const [total, setTotal] = useState(0);
  const [hasMore, setHasMore] = useState(false);
  const [nextCursor, setNextCursor] = useState("");
  const totalPages = Math.max(1, Math.ceil(total / limit));
  const [loading, setLoading] = useState(true);
  // 加载失败态："" 无错误 / "rate_limited" 429 / "invalid_params" 400 / "failed" 其它失败。
  // 与"真的没有条目"必须是两种状态——把 429、5xx、断网都渲染成"未找到匹配的元数据实体"
  // 会让用户以为库里没有这个条目（并据此去建重复条目），也让排障无从知道是限流。
  // 400 更要单独说：那是"参数非法"，既不是没有结果、也不是服务故障，而且重试同样非法。
  const [loadError, setLoadError] = useState<"" | "rate_limited" | "invalid_params" | "search_expired" | "failed">("");
  const [reloadKey, setReloadKey] = useState(0);
  const [viewMode, setViewMode] = useState<"grid" | "list">("grid");
  const [topTags, setTopTags] = useState<{ name: string; count: number }[]>([]);
  // 默认展示数与搜索上限：面板不展开长云，冷门标签走搜索框（搜索池见下面的 limit=100）。
  const TAG_COLLAPSED_COUNT = 8;
  const TAG_SEARCH_CAP = 30;
  // 标签默认只露前几个：面板不展开长云，剩下的走搜索框。已选恒置顶（不因不在前 N 或搜不到而消失）。
  const visibleTags = useMemo(() => {
    const q = tagQuery.trim().toLowerCase();
    // 面板本地搜索同时匹配原始 tag code 与本地化展示名：只匹配原始码时，
    // 用户照着面板上的"轻小说"去搜反而搜不到（面板显示的是本地化名）。
    const matches = (name: string) => {
      if (!q) return true;
      return (
        name.toLowerCase().includes(q) ||
        getTagName(definitions, name, locale).toLowerCase().includes(q)
      );
    };
    if (q) {
      const base = topTags.filter((tag) => matches(tag.name)).slice(0, TAG_SEARCH_CAP);
      const selected = topTags.filter((tag) => currentTags.includes(tag.name) && !base.includes(tag));
      return [...selected, ...base];
    }
    const top = topTags.slice(0, TAG_COLLAPSED_COUNT);
    const selected = topTags.filter((tag) => currentTags.includes(tag.name) && !top.includes(tag));
    return [...selected, ...top];
  }, [topTags, tagQuery, currentTags, definitions, locale]);
  // 原语言下拉选项：常用在前、全表在后；URL 里带了表外码（别名/冷门码）时 pin 一项，免得选中态凭空消失。
  const langOptions = useMemo(() => {
    const base = searchLanguages("").map((e) => ({ value: e.code, label: languageLabel(e.code) }));
    if (currentOriginalLanguage && !base.some((o) => o.value === currentOriginalLanguage)) {
      return [
        { value: "", label: t("catalog.allLanguages") },
        { value: currentOriginalLanguage, label: languageLabel(currentOriginalLanguage) },
        ...base,
      ];
    }
    return [{ value: "", label: t("catalog.allLanguages") }, ...base];
  }, [currentOriginalLanguage, t]);
  const [tagsFailed, setTagsFailed] = useState(false);
  const [tagsReloadKey, setTagsReloadKey] = useState(0);
  // 越界页（如 ?page=99999）：服务端返回空 items，但 total 仍是筛选后的真实条数。
  // 这不是"没有结果"，页面要说清"没有更多"并把用户带回第一页——线上实测这一页
  // 显示"共 0 条"，与侧栏"全部实体 3070"自相矛盾，原因正是总数被写死成 0。
  const outOfRange = !loading && !loadError && items.length === 0 && total > 0 && currentPage > totalPages;

  // 标签云：来自真实聚合（各实体 attributes.tags 的频次），按使用量取前若干。
  // 取不到时说明"标签面板暂时不可用"，不再与"暂无标签"混成同一句。
  useEffect(() => {
    let alive = true;
    setTagsFailed(false);
    // 取 100 个当搜索池：面板默认只展示前 N（见 visibleTags），搜得到深处的冷门标签。
    fetch("/api/catalog/tags?limit=100", { credentials: "same-origin" })
      .then((res) => (res.ok ? res.json() : Promise.reject(new Error(String(res.status)))))
      .then((data) => { if (alive) setTopTags(Array.isArray(data.items) ? data.items : []); })
      .catch(() => { if (alive) { setTopTags([]); setTagsFailed(true); } });
    return () => { alive = false; };
  }, [tagsReloadKey]);

  useEffect(() => {
    let alive = true;
    if (previousSearchLocale.current !== searchLocale) {
      previousSearchLocale.current = searchLocale;
      if (currentQ && cursorParam) {
        const firstPage = new URLSearchParams(searchParams.toString());
        firstPage.delete("cursor");
        firstPage.set("page", "1");
        router.replace(`/explore?${firstPage}`);
        return;
      }
    }
    setLoading(true);
    setLoadError("");
    const params = new URLSearchParams();
    if (currentKind !== "all") params.set("kind", currentKind);
    if (currentStatus) params.set("status", currentStatus);
    if (currentQ) params.set("q", currentQ);
    currentTags.forEach((tag) => params.append("tags", tag));
    if (currentTagsMode !== "any") params.set("tags_mode", currentTagsMode);
    if (currentOriginalLanguage) params.set("original_language", currentOriginalLanguage);
    if (currentHasPictures) params.set("has_pictures", "1");
    if (sortParam) params.set("sort", sortParam);
    if (orderParam) params.set("order", orderParam);
    // title 排序由服务端按请求语种取题名（请求语种 → 原文语种 → en-US → 基础题名），
    // 与页面上显示的题名同源，而不是恒按基础题名排。
    if (sortParam === "title") params.set("locale", locale);
    params.set("limit", limit.toString());
    if (currentQ && cursorParam) params.set("cursor", cursorParam);
    else params.set("offset", offset.toString());

    fetch("/api/catalog/entities?" + params.toString(), { credentials: "same-origin" })
      .then(async (res) => {
        if (!res.ok) {
          // 400 带稳定机器码（invalid_query_param / query_too_long / invalid_limit /
          // invalid_offset / invalid_page / pagination_conflict…）。读出来只为分类：
          // 参数非法要单独提示，不能落成空态或"服务暂时不可用"。
          if (res.status === 400) {
            let code = "";
            try {
              code = (await res.json())?.error || "";
            } catch {
              // 非 JSON 错误体不阻断判定：仍然按 400 处理。
            }
            throw new Error(code === "search_cursor_expired" || code === "invalid_search_cursor" ? "search_expired" : "invalid_params:" + code);
          }
          throw new Error(res.status === 429 ? "rate_limited" : "load_failed:" + res.status);
        }
        return res.json();
      })
      .then((data) => {
        if (!alive) return;
        setItems(Array.isArray(data.items) ? data.items : []);
        setTotal(safeCount(data.total, 0));
        setHasMore(data.has_more === true);
        setNextCursor(typeof data.next_cursor === "string" ? data.next_cursor : "");
      })
      .catch((err: any) => {
        if (!alive) return;
        setItems([]);
        setTotal(0);
        setHasMore(false);
        setNextCursor("");
        const message = String(err?.message || "");
        setLoadError(
          message === "rate_limited"
            ? "rate_limited"
            : message === "search_expired"
              ? "search_expired"
            : message.startsWith("invalid_params")
              ? "invalid_params"
              : "failed",
        );
      })
      .finally(() => { if (alive) setLoading(false); });
    return () => { alive = false; };
  }, [currentKind, currentStatus, currentQ, currentTags, currentTagsMode, currentOriginalLanguage, currentHasPictures, cursorParam, offset, sortParam, orderParam, locale, searchLocale, reloadKey, router]);

  const updateFilters = (updates: Record<string, string>) => {
    const next = new URLSearchParams(searchParams.toString());
    Object.entries(updates).forEach(([k, v]) => {
      if (v) next.set(k, v);
      else next.delete(k);
    });
    if (!updates.page) next.delete("page");
    if (!updates.cursor) next.delete("cursor");
    router.push("/explore?" + next.toString());
  };

  // 标签多选：写回 URL 的 tags 参数（多个值），其余筛选保持不变。
  const toggleTag = (name: string) => {
    const next = new Set(currentTags);
    if (next.has(name)) next.delete(name);
    else next.add(name);
    const params = new URLSearchParams(searchParams.toString());
    params.delete("tags");
    next.forEach((tag) => params.append("tags", tag));
    params.delete("page");
    params.delete("cursor");
    router.push("/explore?" + params.toString());
  };

  // 实体类型（八骨架 kind）显示名：服务端 definitions 的 kinds 优先，前端字典兜底。
  // 左栏筛选与卡片角标都用它——"分类"不在这里，分类由货架承担。
  const kindLabel = (id: string) => getKindName(kinds, id, locale, tr("catalog.kind." + id, id));

  // 已选条件在结果上方始终可见；手机端收起面板后仍能逐项撤销。
  const appliedFilters: { id: string; label: string; remove: () => void }[] = [];
  if (currentQ) {
    appliedFilters.push({ id: "q", label: `${t("search.submit")}: ${currentQ}`, remove: () => updateFilters({ q: "" }) });
  }
  if (currentKind !== "all") {
    appliedFilters.push({ id: "kind", label: kindLabel(currentKind), remove: () => updateFilters({ kind: "" }) });
  }
  if (currentStatus !== "published") {
    appliedFilters.push({ id: "status", label: tr("catalog.status." + currentStatus, currentStatus), remove: () => updateFilters({ status: "" }) });
  }
  for (const tag of Array.from(new Set(currentTags))) {
    appliedFilters.push({ id: `tag:${tag}`, label: getTagName(definitions, tag, locale), remove: () => toggleTag(tag) });
  }
  if (currentOriginalLanguage) {
    appliedFilters.push({
      id: "original_language",
      label: `${t("catalog.originalLanguageFilter")}: ${languageLabel(currentOriginalLanguage)}`,
      remove: () => updateFilters({ original_language: "" }),
    });
  }
  if (currentHasPictures) {
    appliedFilters.push({ id: "has_pictures", label: t("catalog.hasPicturesFilter"), remove: () => updateFilters({ has_pictures: "" }) });
  }

  const clearFilters = () => {
    const next = new URLSearchParams(searchParams.toString());
    for (const key of ["q", "kind", "status", "tags", "tags_mode", "original_language", "has_pictures", "page", "cursor"]) next.delete(key);
    router.push(next.size ? "/explore?" + next.toString() : "/explore");
  };

  // 「实体种类」面板的可选项 = 服务端 definitions.kinds 里未停用的种类。
  // 前端不维护任何种类清单：后台增删 kind、改多语言名，这里自动跟着变；排序按当前语种的名称。
  const kindOptions = useMemo(
    () =>
      Object.keys(kinds || {})
        .filter((code) => kinds[code]?.enabled !== false)
        .sort((a, b) => getKindName(kinds, a, locale).localeCompare(getKindName(kinds, b, locale))),
    [kinds, locale],
  );

  // 面板条目 =「全部」+ 各启用种类；名称走 definitions 多语言名（helper 缺失时才回退前端字典）。
  const kindEntries = [
    { code: "", label: t("catalog.kind.all") },
    ...kindOptions.map((code) => ({ code, label: kindLabel(code) })),
  ];

  const [kindCounts, setKindCounts] = useState<Record<string, number>>({});

  // 各条目的数量：取列表接口响应里的 total（不是 items.length——列表被 limit 截断）。
  // 单个种类请求失败只让那一格留空，不影响其它条目与整页渲染。
  useEffect(() => {
    const codes = ["all", ...kindOptions];
    let cancelled = false;
    Promise.all(
      codes.map((code) =>
        fetch(
          "/api/catalog/entities?" +
            new URLSearchParams(code === "all" ? { limit: "1" } : { kind: code, limit: "1" }).toString(),
          { credentials: "same-origin" },
        )
          .then((res) => (res.ok ? res.json() : null))
          .then((data) => { const n = safeCount(data?.total, -1); return n >= 0 ? n : null; })
          .catch(() => null),
      ),
    ).then((totals) => {
      if (cancelled) return;
      const next: Record<string, number> = {};
      totals.forEach((n, i) => {
        if (typeof n === "number") next[codes[i]] = n;
      });
      setKindCounts(next);
    });
    return () => {
      cancelled = true;
    };
  }, [kindOptions]);

  // 列表容器 key：视图与筛选变化时重挂载、重放 .mf-tabpanel；
  // 顶栏尚未提交的搜索输入不参与，否则打字过程会一直闪。
  const listKey = [
    viewMode,
    currentKind,
    currentStatus,
    currentQ,
    currentTags.join(","),
    currentTagsMode,
    currentPage,
    sortKey,
  ].join("|");

  return (
    <div className="min-h-screen flex flex-col bg-background text-text-strong">
      <Navbar searchQuery={currentQ} onSearch={(q) => updateFilters({ q })} />

      <PageShell
        width="page"
        header={
        <PageHeader
          size="lg"
          bordered
          icon={<Layers className="w-7 h-7 text-primary" />}
          title={t("navigation.explore")}
        />
        }
      >
        {/* 双栏：左侧实体种类与标签筛选，右侧结果区 */}
        <div className="grid grid-cols-1 lg:grid-cols-[220px_minmax(0,1fr)] gap-5">
          <div className="min-w-0 space-y-3">
            <button type="button" aria-expanded={filtersExpanded} aria-controls={filterPanelId} onClick={() => setFiltersExpanded((value) => !value)} className="lg:hidden w-full flex items-center gap-2 p-3 rounded-control border border-line bg-surface text-sm text-text-body">
              <SlidersHorizontal className="w-4 h-4" />
              <span>{t("catalog.filters")}</span>
              <span className="text-xs text-text-muted">{t("catalog.activeFilters", { count: appliedFilters.length })}</span>
              <ChevronDown className={`ml-auto w-4 h-4 transition-transform ${filtersExpanded ? "rotate-180" : ""}`} />
            </button>
          <aside id={filterPanelId} className={`min-w-0 grid-cols-2 gap-3 lg:block lg:space-y-3 ${filtersExpanded ? "grid" : "hidden"}`}>
            {/* 实体种类：条目来自服务端 definitions.kinds 里启用中的种类 +「全部」，
                点击即写回 ?kind=<code>，与标签筛选叠加；窄屏收成一行横向滚动。 */}
            <Card padding="none" className="order-1 col-span-2 shadow-xs overflow-hidden">
              <div className="px-3.5 py-2.5 border-b border-line-subtle">
                <span className="text-[11px] font-mono uppercase tracking-wider text-text-faint">
                  {t("catalog.kindFilter")}
                </span>
              </div>
              <nav
                data-mf-kindnav=""
                className="p-2 flex gap-1.5 overflow-x-auto lg:flex-col lg:gap-0.5 lg:overflow-x-visible"
              >
                {kindEntries.map(({ code, label }) => {
                  const active = currentKind === (code || "all");
                  const count = kindCounts[code || "all"];
                  const KindIcon = kindIcon(code);
                  return (
                    <button
                      key={code || "all"}
                      type="button"
                      data-kind={code || "all"}
                      aria-current={active ? "true" : undefined}
                      onClick={() => updateFilters({ kind: code })}
                      className={
                        "shrink-0 lg:w-full flex items-center gap-2 px-2.5 py-1.5 rounded-lg text-xs whitespace-nowrap border transition-colors duration-fast ease-soft cursor-pointer " +
                        (active
                          ? "bg-primary/10 text-primary border-primary/25 font-semibold"
                          : "text-text-body border-transparent hover:bg-black/[0.04] dark:hover:bg-surfaceHover")
                      }
                    >
                      <KindIcon className="w-3.5 h-3.5 shrink-0" />
                      <span>{label}</span>
                      {typeof count === "number" && (
                        <span className="ml-auto pl-2 font-mono text-[10px] opacity-70">{count}</span>
                      )}
                    </button>
                  );
                })}
              </nav>
            </Card>

            {/* 状态：与种类/标签叠加；原来在顶部检索栏，现收归侧栏统一筛选。 */}
            <Card padding="none" className="order-2 shadow-xs overflow-hidden">
              <div className="px-3.5 py-2.5 border-b border-line-subtle">
                <span className="text-[11px] font-mono uppercase tracking-wider text-text-faint">
                  {t("catalog.status")}
                </span>
              </div>
              <div className="p-2.5">
                <Select
                  value={currentStatus}
                  aria-label={t("catalog.status")}
                  onChange={(v) => updateFilters({ status: v })}
                  options={[
                    { value: "published", label: tr("catalog.status.published", "published") },
                    { value: "pending_review", label: tr("catalog.status.pending_review", "pending_review") },
                    { value: "draft", label: tr("catalog.status.draft", "draft") },
                  ]}
                />
              </div>
            </Card>

            {/* 标签筛选：与种类筛选叠加生效；来源为真实标签聚合
                （/catalog/tags 聚合自各实体的 attributes.tags）。 */}
            <div className="order-4 col-span-2 rounded-card border border-line bg-surface shadow-xs overflow-hidden">
              <div className="px-3.5 py-2.5 border-b border-line-subtle flex items-center justify-between gap-2">
                <span className="text-[11px] font-mono uppercase tracking-wider text-text-faint">
                  {t("catalog.tagFilter")}
                </span>
                {currentTags.length > 0 && (
                  <button
                    type="button"
                    onClick={() => {
                      const p = new URLSearchParams(searchParams.toString());
                      p.delete("tags");
                      p.delete("page");
                      p.delete("cursor");
                      router.push("/explore?" + p.toString());
                    }}
                    className="text-[11px] text-primary hover:underline"
                  >
                    {t("catalog.clear")}
                  </button>
                )}
              </div>
              <div className="p-2.5">
                <fieldset className="mb-3">
                  <legend className="mb-1.5 text-xs font-medium text-text-body">{t("catalog.tagMatchMode")}</legend>
                  <div className="grid grid-cols-2 gap-1 rounded-lg border border-line bg-surfaceSubtle p-1">
                    {(["all", "any"] as const).map((mode) => (
                      <label key={mode} className={`focus-within:ring-2 focus-within:ring-primary flex cursor-pointer items-center justify-center gap-1 rounded-md px-1 py-2 text-xs transition-colors ${currentTagsMode === mode ? "bg-primary/15 text-primary font-semibold" : "text-text-muted hover:text-text-strong"}`}>
                        <input type="radio" name="tags-mode" value={mode} checked={currentTagsMode === mode} onChange={() => updateFilters({ tags_mode: mode === "any" ? "" : mode })} className="sr-only" />
                        <span>{t(mode === "all" ? "catalog.tagMatchAll" : "catalog.tagMatchAny")}</span>
                      </label>
                    ))}
                  </div>
                  <p className="mt-2 text-[11px] leading-relaxed text-text-muted">{t(currentTagsMode === "all" ? "catalog.tagMatchAllHint" : "catalog.tagMatchAnyHint")}</p>
                </fieldset>
                {/* 标签云本地搜索：只过滤面板展示，不发请求。 */}
                <div className="relative flex items-center mb-2">
                  <Search className="absolute left-2.5 w-3.5 h-3.5 text-text-muted pointer-events-none" />
                  <input
                    type="text"
                    value={tagQuery}
                    onChange={(e) => setTagQuery(e.target.value)}
                    placeholder={t("catalog.tagSearchPlaceholder")}
                    aria-label={t("catalog.tagFilter")}
                    className="w-full pl-8 pr-2 py-1.5 rounded-md bg-black/[0.02] dark:bg-white/[0.04] border border-line-subtle text-[11px] text-text-strong placeholder:text-text-muted focus:border-primary outline-none"
                  />
                </div>
                {tagsFailed ? (
                  <div className="px-1 py-2 flex items-center gap-2 text-xs text-amber-700 dark:text-warn-soft">
                    <span>{t("catalog.tagsFailed")}</span>
                    <button
                      type="button"
                      onClick={() => setTagsReloadKey((n) => n + 1)}
                      className="text-primary hover:underline cursor-pointer"
                    >
                      {t("catalog.retry")}
                    </button>
                  </div>
                ) : topTags.length === 0 ? (
                  <p className="px-1 py-2 text-xs text-text-faint">{t("catalog.noTags")}</p>
                ) : (
                  <div className="flex flex-wrap gap-1.5">
                    {visibleTags.map((tag) => {
                      const on = currentTags.includes(tag.name);
                      return (
                        <button
                          key={tag.name}
                          type="button"
                          aria-pressed={on}
                          onClick={() => toggleTag(tag.name)}
                          className={
                            "px-2 py-1 rounded-md text-[11px] border transition-colors duration-150 flex items-center gap-1.5 " +
                            (on
                              ? "bg-primary/15 text-primary border-primary/30"
                              : "text-text-body border-line-subtle hover:bg-black/[0.04] dark:hover:bg-surfaceHover")
                          }
                        >
                          {/* 名称与计数分离：粘连在一起（VOCALOID41）扫读时无法区分词与数。
                              显示名走 tags 词表多语言；点击/URL 值仍是原始 tag code（tag.name）。 */}
                          <span className={on ? "font-semibold" : "font-medium"}>{getTagName(definitions, tag.name, locale)}</span>
                          <span
                            className={
                              "pl-1.5 border-l border-line-subtle font-mono text-[10px] tabular-nums " +
                              (on ? "text-primary/70" : "text-text-faint")
                            }
                          >
                            {tag.count}
                          </span>
                        </button>
                      );
                    })}
                    {visibleTags.length === 0 && topTags.length > 0 && (
                      <p className="px-1 py-2 text-xs text-text-faint">{t("catalog.noTags")}</p>
                    )}
                  </div>
                )}
              </div>
            </div>

            {/* 原语言：document->>'original_language' 精确匹配，八层级通用。 */}
            <Card padding="none" className="order-3 shadow-xs overflow-hidden">
              <div className="px-3.5 py-2.5 border-b border-line-subtle">
                <span className="text-[11px] font-mono uppercase tracking-wider text-text-faint">
                  {t("catalog.originalLanguageFilter")}
                </span>
              </div>
              <div className="p-2.5">
                <Select
                  value={currentOriginalLanguage}
                  aria-label={t("catalog.originalLanguageFilter")}
                  onChange={(v) => updateFilters({ original_language: v })}
                  options={langOptions}
                />
              </div>
            </Card>

            {/* 封面：只留 pictures 非空数组的条目。 */}
            <Card padding="none" className="order-5 col-span-2 shadow-xs overflow-hidden">
              <button
                type="button"
                onClick={() => updateFilters({ has_pictures: currentHasPictures ? "" : "1" })}
                aria-pressed={currentHasPictures}
                className="w-full flex items-center gap-2 px-3.5 py-2.5 text-left cursor-pointer hover:bg-black/[0.02] dark:hover:bg-white/[0.03]"
              >
                <span
                  className={
                    "w-4 h-4 rounded border flex items-center justify-center shrink-0 transition-colors " +
                    (currentHasPictures ? "bg-primary border-primary text-white" : "border-line text-transparent")
                  }
                >
                  <Check className="w-3 h-3" />
                </span>
                <span className="text-xs text-text-body">{t("catalog.hasPicturesFilter")}</span>
              </button>
            </Card>
          </aside>
          </div>

          <div className="min-w-0 space-y-5">
            {/* 搜索统一在顶栏；本栏只保留排序与视图切换。 */}
            <div className="flex flex-wrap items-center justify-between gap-3 p-3 rounded-xl bg-surface border border-line shadow-xs">
              <p role="status" className="w-full sm:w-auto sm:mr-auto text-sm font-medium text-text-body">
                {loading ? t("catalog.loading") : loadError ? t("catalog.loadFailed") : t(currentQ ? "catalog.searchTotal" : "pagination.totalItems", { total })}
              </p>
              <div className="flex w-full sm:w-auto items-center gap-2">
                {/* 排序：取值写回 URL（?sort=&order=），可深链、后退键保持；后端按白名单校验。 */}
                <div className="min-w-0 flex-1 sm:w-48">
                  <Select
                    value={sortValue}
                    aria-label={t("catalog.sort")}
                    onChange={(v) => {
                      const [sort, order] = v.split(":");
                      updateFilters({ sort: sort || "", order: sort ? order || "" : "" });
                    }}
                    options={[
                      { value: "", label: t(currentQ ? "catalog.sortRelevance" : "catalog.sortUpdated") },
                      ...(currentQ ? [{ value: "updated_at:desc", label: t("catalog.sortUpdated") }] : []),
                      { value: "created_at:desc", label: t("catalog.sortCreated") },
                      { value: "title:asc", label: t("catalog.sortTitleAsc") },
                      { value: "title:desc", label: t("catalog.sortTitleDesc") },
                    ]}
                  />
                </div>

                <div className="flex shrink-0 items-center justify-end gap-1.5">
                  <button
                    type="button"
                    onClick={() => setViewMode("grid")}
                    aria-label={t("catalog.gridView")}
                    aria-pressed={viewMode === "grid"}
                    className={
                      "mf-focus p-2.5 min-h-10 max-sm:min-h-[44px] rounded-lg border text-xs transition-colors duration-fast ease-soft cursor-pointer " +
                      (viewMode === "grid"
                        ? "bg-primary/15 border-primary/40 text-primary font-bold"
                        : "bg-surface border-line text-text-muted hover:text-text-strong")
                    }
                    title={t("catalog.gridView")}
                  >
                    <LayoutGrid className="w-4 h-4" />
                  </button>
                  <button
                    type="button"
                    onClick={() => setViewMode("list")}
                    aria-label={t("catalog.listView")}
                    aria-pressed={viewMode === "list"}
                    className={
                      "mf-focus p-2.5 min-h-10 max-sm:min-h-[44px] rounded-lg border text-xs transition-colors duration-fast ease-soft cursor-pointer " +
                      (viewMode === "list"
                        ? "bg-primary/15 border-primary/40 text-primary font-bold"
                        : "bg-surface border-line text-text-muted hover:text-text-strong")
                    }
                    title={t("catalog.listView")}
                  >
                    <ListIcon className="w-4 h-4" />
                  </button>
                </div>
              </div>
            </div>

            {appliedFilters.length > 0 && (
              <div className="flex flex-wrap items-center gap-2">
                {currentTags.length > 1 && (
                  <span className="text-xs font-medium text-text-body">{t("catalog.tagMatchSummary", { mode: t(currentTagsMode === "all" ? "catalog.tagMatchAll" : "catalog.tagMatchAny") })}</span>
                )}
                <span className="sr-only">{t("catalog.activeFilters", { count: appliedFilters.length })}</span>
                {appliedFilters.map((filter) => (
                  <button
                    key={filter.id}
                    type="button"
                    onClick={filter.remove}
                    aria-label={t("catalog.removeFilter", { filter: filter.label })}
                    title={filter.label}
                    className="mf-focus inline-flex min-w-0 max-w-full items-center gap-1.5 min-h-9 max-sm:min-h-[44px] rounded-full border border-primary/25 bg-primary/10 px-3 text-xs text-primary hover:bg-primary/20 transition-colors duration-fast ease-soft"
                  >
                    <span className="truncate">{filter.label}</span>
                    <X className="w-3.5 h-3.5 shrink-0" aria-hidden />
                  </button>
                ))}
                <button type="button" onClick={clearFilters} className="mf-focus min-h-9 max-sm:min-h-[44px] px-2 text-xs text-text-muted hover:text-primary hover:underline">{t("catalog.clearFilters")}</button>
              </div>
            )}

            {loading ? (
              <div className="py-8 text-center text-text-faint font-mono text-xs flex flex-col items-center justify-center gap-3">
                <RefreshCw className="w-6 h-6 animate-spin text-primary" />
                <span>{t("catalog.loading")}</span>
              </div>
            ) : loadError ? (
              // 错误态与空态是两种状态：这里说的是"这一次没取到"，并给重试入口；
              // 429 单独给限流文案（服务端 120/分钟全站共享预算，命中是常态）。
              <div
                role="alert"
                className="p-5 rounded-card border border-amber-500/30 bg-amber-500/5 text-center shadow-2xs"
              >
                <p className="text-amber-700 dark:text-warn-soft text-sm mb-3">
                  {loadError === "rate_limited"
                    ? t("catalog.rateLimited")
                    : loadError === "invalid_params"
                      ? t("catalog.invalidQueryParams")
                      : loadError === "search_expired"
                        ? t("catalog.searchExpired")
                      : t("catalog.loadFailed")}
                </p>
                {loadError === "invalid_params" ? (
                  // 参数非法时给"清除筛选"而不是"重试"：同样的参数再发一次还是 400。
                  <button
                    type="button"
                    onClick={() => router.push("/explore")}
                    className="inline-flex items-center gap-1.5 text-xs font-mono text-primary hover:underline cursor-pointer"
                  >
                    {t("catalog.emptyAction")}
                  </button>
                ) : (
                  <button
                    type="button"
                    onClick={() => loadError === "search_expired" ? updateFilters({ page: "1" }) : setReloadKey((n) => n + 1)}
                    className="inline-flex items-center gap-1.5 text-xs font-mono text-primary hover:underline cursor-pointer"
                  >
                    <RefreshCw className="w-3.5 h-3.5" />
                    {t("catalog.retry")}
                  </button>
                )}
              </div>
            ) : outOfRange ? (
              // 越界页与空结果是两件事：这里给出真实总数与页数，并提供回第一页的出口。
              <div
                role="alert"
                className="p-5 rounded-card border border-dashed border-line text-center bg-surface/50 shadow-2xs"
              >
                <p className="text-gray-600 dark:text-gray-400 text-sm mb-3">
                  {t("catalog.pageOutOfRange", {
                    page: currentPage.toString(),
                    total: total.toString(),
                    pages: totalPages.toString(),
                  })}
                </p>
                <button
                  type="button"
                  onClick={() => updateFilters({ page: "1" })}
                  className="inline-flex items-center gap-1.5 text-xs font-mono text-primary hover:underline cursor-pointer"
                >
                  <ChevronLeft className="w-3.5 h-3.5" />
                  {t("catalog.goFirstPage")}
                </button>
              </div>
            ) : items.length === 0 ? (
              <div className="p-5 rounded-card border border-dashed border-line text-center bg-surface/50 shadow-2xs">
                <p className="text-gray-600 dark:text-gray-400 text-sm mb-3">{t("catalog.emptyTitle")}</p>
                <button
                  type="button"
                  onClick={clearFilters}
                  className="text-xs font-mono text-primary hover:underline cursor-pointer"
                >
                  {t("catalog.emptyAction")}
                </button>
              </div>
            ) : viewMode === "grid" ? (
              <TabPanel activeKey={listKey} spacing="none" className="grid grid-cols-2 sm:grid-cols-3 lg:grid-cols-4 xl:grid-cols-5 gap-3">
                {items.map((item) => {
                  const displayTitle = getLocalizedTitle(item, locale, titleOrder);
                  // 角标 = 实体类型（kind）；业务类型留在正文的类型标签里，不做成"分类"角标。
                  const badgeLabel = kindLabel(item.kind);
                  // 列表/网格只展示封面（首张图）：多图画廊在详情页。
                  const cover = coverUrl(item);

                  return (
                    <EntityCard
                      key={item.id}
                      id={item.id}
                      kind={item.kind}
                      badgeLabel={badgeLabel}
                      title={displayTitle}
                      baseTitle={item.title}
                      tags={getTagNames(definitions, item.attributes?.tags, locale)}
                      status={item.status}
                      statusLabel={tr("catalog.status." + item.status, item.status)}
                      pictureUrl={cover}
                    />
                  );
                })}
              </TabPanel>
            ) : (
              <TabPanel activeKey={listKey} spacing="none" className="rounded-xl border border-line bg-surface overflow-hidden divide-y dark:divide-white/[0.04] shadow-soft">
                {items.map((item) => {
                  const KindIcon = kindIcon(item.kind);
                  const displayTitle = getLocalizedTitle(item, locale, titleOrder);
                  // 角标 = 实体类型（kind）；业务类型留在正文的类型标签里，不做成"分类"角标。
                  const badgeLabel = kindLabel(item.kind);
                  // 列表视图同样是"只看封面"：取值走 lib/cover，多图只在详情页成廊展示。
                  const cover = coverUrl(item);

                  return (
                    <Link
                      key={item.id}
                      href={"/catalog/" + item.id}
                      className="mf-focus p-3.5 flex items-center justify-between gap-3 hover:bg-surfaceSubtle transition-colors duration-fast ease-soft group"
                    >
                      <div className="flex items-center gap-3 min-w-0">
                        <div className="w-11 h-11 rounded-lg bg-black/[0.03] dark:bg-black/40 border border-line shrink-0 overflow-hidden flex items-center justify-center">
                          {cover ? (
                            <img src={cover} alt={displayTitle} className="w-full h-full object-cover" />
                          ) : (
                            <KindIcon className="w-5 h-5 text-text-muted" />
                          )}
                        </div>
                        <div className="min-w-0">
                          <div className="flex flex-wrap items-center gap-x-2 gap-y-1 mb-0.5">
                            <span className="shrink-0 px-2 py-0.5 rounded bg-emphasis/[0.04] text-[10px] text-text-body font-medium">
                              {badgeLabel}
                            </span>
                            <h3 className="w-full sm:w-auto min-w-0 font-semibold text-text-strong group-hover:text-primary transition-colors duration-fast ease-soft text-sm line-clamp-2 break-words">
                              {displayTitle}
                            </h3>
                            {item.title !== displayTitle && (
                              <span className="text-[11px] text-text-faint font-mono hidden sm:inline truncate">
                                ({item.title})
                              </span>
                            )}
                            {item.status !== "published" && (
                              <span className="px-1.5 py-0.2 rounded bg-amber-500/20 text-amber-600 dark:text-warn text-[10px] font-mono font-medium">
                                {tr("catalog.status." + item.status, item.status)}
                              </span>
                            )}
                          </div>
                          <div className="flex items-center gap-1.5 text-xs text-text-muted font-mono">
                            {/* 列表行标签同样是本地化展示名；筛选值仍在 URL 的 tags 参数里。 */}
                            <span className="line-clamp-1">{getTagNames(definitions, item.attributes?.tags, locale).slice(0, 3).join(" · ")}</span>
                          </div>
                        </div>
                      </div>

                      <div className="flex items-center gap-4 shrink-0 text-xs font-mono text-text-muted">
                        <span className="hidden sm:inline">rev {item.version || 1}</span>
                        <ArrowRight className="w-4 h-4 text-text-faint group-hover:text-primary transition-colors duration-fast ease-soft" />
                      </div>
                    </Link>
                  );
                })}
              </TabPanel>
            )}

            <div className="flex flex-wrap items-center justify-between gap-3 border-t border-line pt-4 text-xs font-mono text-gray-600 dark:text-gray-400">
              <div>
                {/* 空结果不显示"第 1 - 0 项"这类自相矛盾的区间（offset 有值而 items 为空）。 */}
                <span>
                  {items.length > 0
                    ? t("catalog.showingPage", {
                        start: (offset + 1).toString(),
                        end: (offset + items.length).toString(),
                      })
                    : loadError
                      ? // 取数失败时不报"共 0 项"——那是在给一个没拿到的结论下定论。
                        t("catalog.loadFailed")
                      : // 空结果与越界页都报真实 total（后端在越界页也照常返回它），
                        // 不再写死 0：写死 0 会与侧栏"全部实体 N"直接矛盾。
                        t(currentQ ? "catalog.searchTotal" : "pagination.totalItems", { total })}
                </span>
              </div>
              {currentQ ? (
                <div className="flex items-center gap-3">
                  {currentPage > 1 && (
                    <button type="button" onClick={() => updateFilters({ page: "1" })} className="text-primary hover:underline">
                      {t("pagination.first")}
                    </button>
                  )}
                  <span>{t("pagination.pageN", { n: currentPage })}</span>
                  <button type="button" disabled={loading || !!loadError || !hasMore || !nextCursor}
                    onClick={() => updateFilters({ page: String(currentPage + 1), cursor: nextCursor })}
                    className="text-primary hover:underline disabled:opacity-40 disabled:cursor-not-allowed">
                    {t("pagination.next")}
                  </button>
                </div>
              ) : (
                <Pagination page={currentPage} totalPages={totalPages}
                  onChange={(n) => updateFilters({ page: n.toString() })} />
              )}
            </div>
          </div>
        </div>
      </PageShell>
    </div>
  );
}

function ExploreFallback() {
  const { t } = useI18n();
  return (
    <div className="min-h-screen bg-background text-text-faint font-mono text-xs grid place-items-center">{t("common.loading")}</div>
  );
}

export default function ExplorePage() {
  return (
    <Suspense fallback={<ExploreFallback />}>
      <ExploreInner />
    </Suspense>
  );
}
