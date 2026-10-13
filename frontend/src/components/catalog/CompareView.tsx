"use client";

import { searchInputLimitProps } from "@/lib/searchQuery";
import { useEffect, useState, useMemo, useRef } from "react";
import Link from "next/link";
import { useI18n } from "@/i18n/I18nProvider";
import { api, Entity, title } from "./api";
import { ErrorMessage } from "./Fields";
import { useAuth } from "@/lib/authContext";
import { useInclusionExpressions } from "./useInclusionExpressions";
import { FieldValue, GroupAttributeInline, LocatorInline } from "./TemplateAttributeSections";
import { useDefinitions, getFieldName, getKindName, getTermName, resolveKindOptions, resolveLocalizedName } from "@/lib/definitions";
import { computeAlignment, compareSemanticsOf } from "./compareAlignment";
import {
  COMPARE_MAX_SLOTS,
  COMPARE_MIN_SLOTS,
  compareHref,
  normalizeBasket,
  useCompareBasket,
} from "@/lib/compareBasket";
import { AdaptiveCardCover } from "@/components/common/AdaptiveCardCover";
// 对比页只看封面（首张图）：取值一律走 lib/cover，这里只把结果存进局部变量 coverUrl。
import { coverUrl as firstCoverUrl } from "@/lib/cover";
import { RevisionsCompare } from "./RevisionsCompare";
import { canonicalDetailPath } from "@/lib/entityRoutes";
import { orderedTracksWithDepth } from "@/lib/trackTree";
import {
  ArrowRightLeft,
  Search,
  Plus,
  X,
  Disc,
  Trash2,
  Check,
  AlertCircle,
  Sparkles,
  Columns,
  Layers,
} from "lucide-react";

export function Compare({ ids, revisions, mode }: { ids: string; revisions?: string; mode?: string }) {
  const { t, tr, locale } = useI18n();
  const { user } = useAuth();
  const viewerId = user?.id || "";
  // 定义只有这一份来源：CatalogProvider 只留模块状态与实例初始化状态，从不持有定义，
  // 以前这里取的是 Provider 的 definition，恒为 undefined，字段名与枚举值一律裸露。
  const { definitions: dynamicDefs, kinds } = useDefinitions();
  const searchInputRef = useRef<HTMLInputElement>(null);

  const urlSelection = useMemo(() => normalizeBasket((ids || "").split(",")), [ids]);
  // URL 显式清单优先；无清单时直接使用篮子，避免两份状态读后互相回写。
  const { basket: storedBasket, setBasket: writeBasket } = useCompareBasket();
  const selectedIds = ids ? urlSelection : storedBasket;
  const [loadedItems, setItems] = useState<any[]>([]);
  const [itemsKey, setItemsKey] = useState("");
  const queryKey = selectedIds.join(",");
  const requestKey = JSON.stringify([viewerId, queryKey]);
  // 身份或选择变化的同次渲染即隐藏旧资料，不等待 effect 清空私有名称。
  const items = itemsKey === requestKey ? loadedItems : [];
  const [loadedSingleEntity, setSingleEntity] = useState<Entity | null>(null);
  const [singleEntityKey, setSingleEntityKey] = useState("");
  const singleEntity = singleEntityKey === requestKey ? loadedSingleEntity : null;
  const [error, setError] = useState("");
  const [loading, setLoading] = useState(false);
  const [loadedRecentEntities, setRecentEntities] = useState<Entity[]>([]);
  const [recentViewerId, setRecentViewerId] = useState("");
  const recentEntities = recentViewerId === viewerId ? loadedRecentEntities : [];
  const [searchQuery, setSearchQuery] = useState("");
  // 选择器层级：all 不带 kind 参数（八层级混排），其余只取该层级。
  const [pickerKind, setPickerKind] = useState<string>("all");
  const [loadedSearchResults, setSearchResults] = useState<Entity[]>([]);
  const [searchViewerId, setSearchViewerId] = useState("");
  const searchResults = searchViewerId === viewerId ? loadedSearchResults : [];
  const [searching, setSearching] = useState(false);
  const [customIdInput, setCustomIdInput] = useState("");
  const [highlightDiff, setHighlightDiff] = useState(true);
  // 版本对比没有页签：?revisions=<entity>:<version>,<entity>:<version>（历史页复选进来）
  // 直接渲染版本对比，否则是纯实体对比。空着进版本模式是死路，所以不给入口。

  const maxSlots = COMPARE_MAX_SLOTS;
  const slotIndices = useMemo(() => Array.from({ length: maxSlots }, (_, i) => i), [maxSlots]);

  useEffect(() => {
    if (ids) writeBasket(urlSelection);
  }, [ids, urlSelection, writeBasket]);

  // 单个已选条目也回读标题与封面，不等到凑齐两个才加载，且不显示 UUID 作为题名。
  useEffect(() => {
    if (selectedIds.length !== 1) return;
    let active = true;
    api<Entity>(`/catalog/entities/${encodeURIComponent(selectedIds[0])}/resolve`)
      .then((entity) => {
        if (!active) return;
        setSingleEntity(entity);
        setSingleEntityKey(requestKey);
      })
      .catch(() => {
        if (!active) return;
        setSingleEntity(null);
        setSingleEntityKey(requestKey);
      });
    return () => { active = false; };
  }, [requestKey, selectedIds]);

  const updateSelected = (next: string[]) => {
    const cleaned = normalizeBasket(next);
    writeBasket(cleaned);
    if (typeof window !== "undefined") {
      window.history.replaceState(null, "", compareHref(cleaned));
    }
  };

  const addId = (id: string) => {
    const trimmed = id.trim();
    if (!trimmed) return;
    if (selectedIds.includes(trimmed)) return;
    if (selectedIds.length >= maxSlots) {
      setError(t("catalog.compareMaxError"));
      return;
    }
    setError("");
    updateSelected([...selectedIds, trimmed]);
  };

  const removeId = (id: string) => {
    updateSelected(selectedIds.filter((x) => x !== id));
  };

  const clearAll = () => {
    updateSelected([]);
    setItems([]);
    setError("");
  };

  const focusSearch = () => {
    if (searchInputRef.current) {
      searchInputRef.current.scrollIntoView({ behavior: "smooth", block: "center" });
      searchInputRef.current.focus();
    }
  };

  useEffect(() => {
    let active = true;
    setRecentEntities([]);
    setRecentViewerId(viewerId);
    const kindParam = pickerKind === "all" ? "" : `kind=${encodeURIComponent(pickerKind)}&`;
    api<{ items: Entity[] }>(`/catalog/entities?${kindParam}limit=12`)
      .then((r) => { if (active) setRecentEntities(Array.isArray(r.items) ? r.items : []); })
      .catch(() => {});
    return () => { active = false; };
  }, [pickerKind, viewerId]);

  useEffect(() => {
    let active = true;
    setSearchResults([]);
    setSearchViewerId(viewerId);
    setSearching(false);
    const q = searchQuery.trim();
    if (!q) return;
    const timer = setTimeout(() => {
      setSearching(true);
      const kindParam = pickerKind === "all" ? "" : `kind=${encodeURIComponent(pickerKind)}&`;
      api<{ items: Entity[] }>(`/catalog/entities?${kindParam}q=${encodeURIComponent(q)}&limit=8`)
        .then((r) => { if (active) setSearchResults(Array.isArray(r.items) ? r.items : []); })
        .catch((e) => { if (active) setError(e.message); })
        .finally(() => { if (active) setSearching(false); });
    }, 250);
    return () => { active = false; clearTimeout(timer); };
  }, [searchQuery, pickerKind, viewerId]);

  // 同一身份与 ids 只取一次；请求序号也阻止身份或选择往返后的旧响应覆盖新结果。
  const lastFetchedRef = useRef("");
  const fetchSequenceRef = useRef(0);
  useEffect(() => {
    if (selectedIds.length < COMPARE_MIN_SLOTS) {
      setItems([]);
      setError("");
      setLoading(false);
      lastFetchedRef.current = "";
      fetchSequenceRef.current += 1;
      return;
    }
    if (selectedIds.length > maxSlots) {
      setError(t("catalog.compareLimitError"));
      return;
    }
    const key = requestKey;
    if (lastFetchedRef.current === key) return;
    lastFetchedRef.current = key;
    const sequence = ++fetchSequenceRef.current;
    setLoading(true);
    setError("");
    api<{ items: any[] }>(`/catalog/compare?ids=${encodeURIComponent(queryKey)}`)
      .then((r) => {
        if (fetchSequenceRef.current !== sequence) return;
        setItems(Array.isArray(r.items) ? r.items : []);
        setItemsKey(key);
        setError("");
      })
      .catch((e) => {
        if (fetchSequenceRef.current !== sequence) return;
        setItems([]);
        setError(e.message);
      })
      .finally(() => { if (fetchSequenceRef.current === sequence) setLoading(false); });
  }, [selectedIds, t, maxSlots, requestKey, queryKey]);

  const comparableFields = useMemo(() => {
    const defs = dynamicDefs;
    const allKeys = Array.from(new Set(items.flatMap((x) => Object.keys(x.entity?.attributes || {}))));
    return allKeys.filter((k) => {
      const field = (defs as any)?.fields?.[k];
      if (!field) return true;
      if (typeof field.comparable === "boolean") return field.comparable;
      return true;
    });
  }, [items, dynamicDefs]);

  const sets = useMemo(() => {
    return items.map(
      (x) =>
        new Set<string>(
          (x.children || []).flatMap((m: any) =>
            m.tracks.flatMap((tr: any) =>
              (tr.contents || []).map((c: any) => c.expression_id)
            )
          )
        )
    );
  }, [items]);

  // —— 语义对齐：以表达（录音/正文）为行对齐各版本收录情况，再派生
  // "同曲不同录音"（按表达所属 Work 聚合）与"仅载体差异"（收录集合一致但介质构成不同）。 ——
  const expressionIds = useMemo(() => {
    const ids = new Set<string>();
    for (const x of items) {
      for (const m of x.children || []) {
        for (const tr of m.tracks || []) {
          for (const c of tr.contents || []) {
            if (c.expression_id) ids.add(c.expression_id);
          }
        }
      }
    }
    return Array.from(ids);
  }, [items]);

  const inclusionExpressions = useInclusionExpressions(expressionIds, viewerId);
  const exprEntities = inclusionExpressions.expressions;
  const expressionIdentityReady = !inclusionExpressions.loading && inclusionExpressions.failedCount === 0
    && expressionIds.every((id) => Boolean(exprEntities[id]?.content_unit_id || exprEntities[id]?.work_id));
  const expressionName = (id: string) => exprEntities[id]
    ? title(exprEntities[id], locale)
    : t(inclusionExpressions.loading ? "catalog.entityReference" : "catalog.referenceUnknown");

  const alignment = useMemo(
    () => computeAlignment(items, exprEntities, compareSemanticsOf(dynamicDefs)),
    [items, exprEntities, dynamicDefs],
  );

  const renderAttrValue = (key: string, value: unknown): string => {
    if (value == null || value === "") return "—";
    if (typeof value === "string" || typeof value === "number") {
      // 枚举值按 definitions 声明的词表本地化：不写死字段码清单，
      // 后台新增枚举字段（如发行批次）自动生效，缺词表命中则原样显示。
      const vocab = (dynamicDefs as any)?.fields?.[key]?.vocabulary;
      if (vocab) {
        const term = getTermName(dynamicDefs, vocab, String(value), locale);
        if (term !== String(value)) return term;
      }
      return String(value);
    }
    if (Array.isArray(value)) return t("release.detail.listCount", { count: value.length });
    if (typeof value === "object") {
      const rec = value as Record<string, string>;
      // 多语言属性值走统一回退链（含 zh-TW / ja），不再自造只认 locale/zh-CN/en-US 的三档实现。
      return resolveLocalizedName(rec, locale, "—");
    }
    return String(value);
  };

  const kindName = (kind: string) => (kind ? getKindName(kinds, kind, locale, tr(`catalog.kind.${kind}`, kind)) : "");

  // 槽位/卡片摘要：标题+封面通用，副标题按层级取（发行：品番/格式；载体：格式；其余：层级名）。
  const getEntitySummary = (id: string) => {
    const matched = items.find((x) => x.entity?.id === id) || (selectedIds[0] === id ? singleEntity : null) || recentEntities.find((x) => x.id === id);
    const obj = matched?.entity || matched;
    const summaryTitle = obj ? title(obj, locale) : t(loading || (selectedIds.length === 1 && singleEntityKey !== requestKey) ? "catalog.entityReference" : "catalog.referenceUnknown");
    const coverUrl = obj ? firstCoverUrl(obj) : "";
    const kind = obj?.kind || "";
    const catalogNo = obj?.attributes?.catalog_number || "";
    const rawFormat = obj?.attributes?.format || "";
    const format = rawFormat ? getTermName(dynamicDefs, dynamicDefs?.fields?.format?.vocabulary || "", String(rawFormat), locale) : "";
    const subtitle =
      kind === "release"
        ? [catalogNo, format].filter(Boolean).map(String).join(" · ") || kindName(kind)
        : kind === "medium"
          ? (format ? String(format) : kindName(kind))
          : kindName(kind);
    return { title: summaryTitle, coverUrl, kind, catalogNo, format, subtitle, raw: obj };
  };

  // 内容对齐只对有结构子树的槽位有意义（发行/载体）：纯属性对比时整段不渲染，
  // 不拿"没有可对齐内容"去吓唬只比两个作品的人。
  const hasStructure = useMemo(
    () => items.some((x) => Array.isArray(x.children) && x.children.length > 0),
    [items],
  );

  // 变更对比：带 ?revisions= 即在（历史页勾选两版进来），或页签切过去。
  // 只带 mode=revisions 而不带 revisions 不是死路了——选取器就在对比页里。
  const revisionMode = !!revisions || mode === "revisions";
  // 切换模式时保留另一种模式的参数：从实体对比切到变更对比再切回来，ids 不丢。
  const modeHref = (target: "entities" | "revisions") => {
    const qs = new URLSearchParams();
    if (target === "revisions") qs.set("mode", "revisions");
    if (selectedIds.length) qs.set("ids", selectedIds.join(","));
    else if (ids) qs.set("ids", ids);
    // revisions 一出现就进变更模式，所以只在切到变更对比时带上。
    if (target === "revisions" && revisions) qs.set("revisions", revisions);
    const q = qs.toString();
    return `/compare${q ? `?${q}` : ""}`;
  };

  return (
    <div className="space-y-6">
      <div className="flex flex-col sm:flex-row sm:items-center justify-between gap-4 pb-6 border-b border-border">
        <div className="flex items-center gap-2.5">
          <span className="p-2 rounded-xl bg-primary/10 text-primary">
            <ArrowRightLeft className="w-5 h-5" />
          </span>
          <h1 className="text-2xl sm:text-3xl font-bold tracking-tight text-foreground m-0">
            {t("catalog.compare")}
          </h1>
        </div>
        <div className="flex items-center gap-2 flex-wrap">
          {/* 两种对比共用一个 /compare：实体横向对比，与修订级变更 diff。 */}
          <div className="inline-flex rounded-lg border border-border bg-muted/20 p-1" role="tablist">
            <Link
              href={modeHref("entities")}
              role="tab"
              aria-selected={!revisionMode}
              className={`px-3 py-1.5 rounded-md text-xs font-medium transition-colors duration-fast ease-soft ${
                !revisionMode ? "bg-card text-foreground shadow-xs font-semibold" : "text-muted-foreground hover:text-foreground"
              }`}
            >
              {t("compare.modeEntities")}
            </Link>
            <Link
              href={modeHref("revisions")}
              role="tab"
              aria-selected={revisionMode}
              className={`px-3 py-1.5 rounded-md text-xs font-medium transition-colors duration-fast ease-soft ${
                revisionMode ? "bg-card text-foreground shadow-xs font-semibold" : "text-muted-foreground hover:text-foreground"
              }`}
            >
              {t("compare.modeRevisions")}
            </Link>
          </div>
          {!revisionMode && selectedIds.length > 0 && (
            <button
              type="button"
              onClick={clearAll}
              className="inline-flex items-center gap-1.5 px-3.5 py-2 text-xs font-medium text-muted-foreground hover:text-destructive hover:bg-destructive/10 rounded-lg border border-border hover:border-destructive/30 transition-all self-start sm:self-auto cursor-pointer"
            >
              <Trash2 className="w-3.5 h-3.5" />
              <span>{t("catalog.compareClear")}</span>
            </button>
          )}
        </div>
      </div>

      {revisionMode ? (
        <RevisionsCompare query={revisions || ""} />
      ) : (
      <>
      <section className="bg-card border border-border rounded-card p-4 shadow-sm">
        <div className="flex flex-wrap items-center justify-between gap-3 mb-4">
          <div className="flex items-center gap-3">
            <span className="font-semibold text-sm text-foreground">
              {t("catalog.compareSlots")} ({selectedIds.length} / {maxSlots})
            </span>
            <div className="flex items-center gap-1.5">
              {slotIndices.map((i) => (
                <span
                  key={i}
                  className={`w-2 h-2 rounded-full transition-all duration-base ease-soft ${
                    i < selectedIds.length
                      ? "bg-primary scale-110 shadow-xs"
                      : "bg-muted-foreground/20 border border-border"
                  }`}
                />
              ))}
            </div>
          </div>

          <div>
            {selectedIds.length < COMPARE_MIN_SLOTS && (
              <span className="inline-flex items-center gap-1.5 px-3 py-1 rounded-full text-xs font-medium bg-amber-500/10 text-amber-600 dark:text-warn border border-amber-500/20">
                <AlertCircle className="w-3.5 h-3.5" />
                {t("catalog.compareNeedMore", { count: COMPARE_MIN_SLOTS - selectedIds.length })}
              </span>
            )}
            {selectedIds.length >= COMPARE_MIN_SLOTS && selectedIds.length < maxSlots && (
              <span className="inline-flex items-center gap-1.5 px-3 py-1 rounded-full text-xs font-medium bg-emerald-500/10 text-emerald-600 dark:text-success border border-emerald-500/20">
                <Check className="w-3.5 h-3.5" />
                {t("catalog.compareReady")}
              </span>
            )}
            {selectedIds.length >= maxSlots && (
              <span className="inline-flex items-center gap-1.5 px-3 py-1 rounded-full text-xs font-medium bg-primary/10 text-primary border border-primary/20">
                <Sparkles className="w-3.5 h-3.5" />
                {t("catalog.compareMaxReached")}
              </span>
            )}
          </div>
        </div>

        <div className="grid grid-cols-2 sm:grid-cols-3 lg:grid-cols-6 gap-3">
          {slotIndices.map((index) => {
            const id = selectedIds[index];
            if (id) {
              const info = getEntitySummary(id);
              const href = canonicalDetailPath(id) ?? `/catalog/${id}`;
              return (
                <div
                  key={id}
                  className="relative group rounded-xl p-3 bg-surface hover:bg-surfaceHover/50 border border-border hover:border-primary/40 shadow-xs hover:shadow-md transition-all flex flex-col justify-between overflow-hidden"
                >
                  <div className="flex items-center justify-between gap-1 mb-2">
                    <span className="text-[11px] font-mono font-bold text-primary px-1.5 py-0.5 rounded bg-primary/10">
                      #{index + 1}
                    </span>
                    <button
                      type="button"
                      onClick={() => removeId(id)}
                      aria-label={t("catalog.compareRemove")}
                      className="p-1 text-muted-foreground hover:text-destructive hover:bg-destructive/10 rounded-md transition-colors duration-fast ease-soft cursor-pointer"
                      title={t("catalog.compareRemove")}
                    >
                      <X className="w-3.5 h-3.5" />
                    </button>
                  </div>

                  <div className="w-full aspect-[3/4] rounded-lg overflow-hidden mb-2 border border-border/60">
                    <AdaptiveCardCover
                      src={info.coverUrl}
                      alt={info.title}
                      fallbackIcon={<Disc className="w-7 h-7 text-muted-foreground/60" />}
                      aspectClassName="w-full h-full"
                    />
                  </div>

                  <div className="min-w-0">
                    <Link
                      href={href}
                      className="text-xs font-semibold text-foreground hover:text-primary line-clamp-2 leading-snug transition-colors duration-fast ease-soft"
                      title={info.title}
                    >
                      {info.title}
                    </Link>
                    <div className="flex items-center gap-1.5 mt-1.5 text-[11px] text-muted-foreground">
                      {info.kind === "release" && info.format ? (
                        <span className="px-1.5 py-0.2 rounded bg-muted/60 font-medium truncate">
                          {String(info.format)}
                        </span>
                      ) : null}
                      {info.kind === "release" && info.catalogNo ? (
                        <span className="truncate font-mono">
                          {String(info.catalogNo)}
                        </span>
                      ) : null}
                      <span className="truncate text-muted-foreground/80">{info.subtitle}</span>
                    </div>
                  </div>
                </div>
              );
            }

            return (
              <div
                key={`empty-${index}`}
                role="button"
                tabIndex={0}
                onKeyDown={(e) => {
                  if (e.key === "Enter" || e.key === " ") {
                    e.preventDefault();
                    e.currentTarget.click();
                  }
                }}
                onClick={focusSearch}
                className="border-2 border-dashed border-border/70 hover:border-primary/60 hover:bg-primary/5 rounded-xl p-3 flex flex-col items-center justify-center min-h-[96px] text-center transition-all cursor-pointer group"
              >
                <div className="w-9 h-9 rounded-full bg-muted/50 group-hover:bg-primary/10 text-muted-foreground group-hover:text-primary flex items-center justify-center mb-2 transition-all duration-base ease-soft group-hover:scale-110">
                  <Plus className="w-4 h-4" />
                </div>
                <span className="text-xs font-semibold text-foreground/80 group-hover:text-primary transition-colors duration-fast ease-soft">
                  {t("catalog.compareSlotIndex")} #{index + 1}
                </span>
                <span className="text-[10px] text-muted-foreground/60 mt-2 px-1.5 py-0.5 rounded bg-muted/30 group-hover:bg-primary/10 group-hover:text-primary transition-colors duration-fast ease-soft line-clamp-1">
                  {t("catalog.compareSlotClickToAdd")}
                </span>
              </div>
            );
          })}
        </div>
      </section>

      {selectedIds.length < maxSlots && (
        <section className="bg-card border border-border rounded-card p-4 shadow-sm">
          <h2 className="text-base sm:text-lg font-bold text-foreground mb-4 flex items-center gap-2">
            <Plus className="w-4 h-4 text-primary" />
            {t("catalog.compareSelectEntity")}
          </h2>

          {/* 选择器层级页签：默认混排，选定后搜索与快速加入只取该层级。 */}
          <div className="flex flex-wrap gap-1.5 mb-4" role="tablist" aria-label={t("catalog.kindFilter")}>
            {["all", ...resolveKindOptions(kinds)].map((k) => (
              <button
                key={k}
                type="button"
                role="tab"
                aria-selected={pickerKind === k}
                onClick={() => setPickerKind(k)}
                className={
                  "px-2.5 py-1 rounded-md text-[11px] font-mono border transition-colors duration-fast ease-soft cursor-pointer " +
                  (pickerKind === k
                    ? "bg-primary/15 text-primary border-primary/30 font-semibold"
                    : "text-text-body border-line-subtle hover:bg-black/[0.04] dark:hover:bg-surfaceHover")
                }
              >
                {k === "all" ? t("catalog.kind.all") : kindName(k)}
              </button>
            ))}
          </div>

          <div className="flex flex-col sm:flex-row items-stretch sm:items-center gap-3">
            <div className="relative flex-1">
              <Search className="w-4 h-4 text-muted-foreground absolute left-3.5 top-1/2 -translate-y-1/2 pointer-events-none" />
              <input
                ref={searchInputRef}
                type="text"
                placeholder={t("catalog.compareSearchPlaceholder")}
                value={searchQuery}
                {...searchInputLimitProps(setSearchQuery)}
                className="w-full pl-10 pr-10 py-2.5 bg-background border border-border rounded-xl text-sm text-foreground placeholder:text-muted-foreground focus:outline-hidden focus:ring-2 focus:ring-primary/30 focus:border-primary transition-all duration-base ease-soft"
              />
              {searchQuery && (
                <button
                  type="button"
                  onClick={() => setSearchQuery("")}
                  aria-label={t("catalog.clear")}
                  className="absolute right-3 top-1/2 -translate-y-1/2 text-muted-foreground hover:text-foreground p-1 cursor-pointer"
                >
                  <X className="w-3.5 h-3.5" />
                </button>
              )}
            </div>

            <div className="flex items-center gap-2">
              <input
                type="text"
                placeholder={t("catalog.compareUuidPlaceholder")}
                value={customIdInput}
                onChange={(e) => setCustomIdInput(e.target.value)}
                onKeyDown={(e) => {
                  if (e.key === "Enter" && customIdInput.trim()) {
                    addId(customIdInput.trim());
                    setCustomIdInput("");
                  }
                }}
                className="w-full sm:w-64 px-3.5 py-2.5 bg-background border border-border rounded-xl text-sm font-mono text-foreground placeholder:text-muted-foreground focus:outline-hidden focus:ring-2 focus:ring-primary/30 focus:border-primary transition-all duration-base ease-soft"
              />
              <button
                type="button"
                disabled={!customIdInput.trim()}
                onClick={() => {
                  addId(customIdInput.trim());
                  setCustomIdInput("");
                }}
                className="px-4 py-2.5 bg-primary text-primary-foreground hover:bg-primary/90 disabled:opacity-40 disabled:cursor-not-allowed rounded-xl text-sm font-medium transition-all inline-flex items-center gap-1.5 shrink-0 shadow-xs cursor-pointer"
              >
                <Plus className="w-4 h-4" />
                <span>{t("catalog.compareAdd")}</span>
              </button>
            </div>
          </div>

          {searching && (
            <div className="flex items-center gap-2 text-xs text-muted-foreground mt-3">
              <span className="w-3.5 h-3.5 border-2 border-primary border-t-transparent rounded-full animate-spin" />
              <span>{t("catalog.loading")}</span>
            </div>
          )}

          {searchResults.length > 0 && (
            <div className="mt-4 pt-4 border-t border-border">
              <div className="flex items-center justify-between mb-3">
                <span className="text-xs font-bold uppercase tracking-wider text-muted-foreground">
                  {t("catalog.compareSearchResults")} ({searchResults.length})
                </span>
              </div>
              <div className="grid grid-cols-1 sm:grid-cols-2 md:grid-cols-3 lg:grid-cols-4 gap-3">
                {searchResults.map((r) => {
                  const isSelected = selectedIds.includes(r.id!);
                  const releaseTitle = title(r, locale);
                  const coverUrl = firstCoverUrl(r);
                  const subtitle = getEntitySummary(r.id!).subtitle;
                  return (
                    <div
                      key={r.id}
                role="button"
                tabIndex={0}
                onKeyDown={(e) => {
                  if (e.key === "Enter" || e.key === " ") {
                    e.preventDefault();
                    e.currentTarget.click();
                  }
                }}
                      onClick={() => !isSelected && addId(r.id!)}
                      className={`group rounded-xl p-3 border transition-all flex items-center justify-between gap-3 ${
                        isSelected
                          ? "bg-surface/50 border-border opacity-70 cursor-default"
                          : "bg-surface hover:bg-surfaceHover/60 border-border hover:border-primary/50 shadow-xs hover:shadow-sm cursor-pointer"
                      }`}
                    >
                      <div className="flex items-center gap-2.5 min-w-0 flex-1">
                        <div className="w-10 h-10 rounded-lg overflow-hidden shrink-0 border border-border/60">
                          <AdaptiveCardCover
                            src={coverUrl}
                            alt={releaseTitle}
                            fallbackIcon={<Disc className="w-5 h-5 text-muted-foreground/60" />}
                            aspectClassName="w-full h-full"
                          />
                        </div>
                        <div className="min-w-0 flex-1">
                          <div className="text-xs font-semibold text-foreground group-hover:text-primary transition-colors duration-fast ease-soft truncate">
                            {releaseTitle}
                          </div>
                          <div className="text-[11px] text-muted-foreground truncate mt-0.5">
                            {subtitle}
                          </div>
                        </div>
                      </div>
                      <button
                        type="button"
                        disabled={isSelected}
                        onClick={(e) => {
                          e.stopPropagation();
                          addId(r.id!);
                        }}
                        className={`w-7 h-7 rounded-lg text-xs font-bold flex items-center justify-center shrink-0 transition-all cursor-pointer ${
                          isSelected
                            ? "bg-emerald-500/10 text-emerald-600 dark:text-success border border-emerald-500/20"
                            : "bg-primary/10 text-primary hover:bg-primary hover:text-emphasis"
                        }`}
                        aria-label={isSelected ? t("catalog.compareAdded") : t("catalog.compareAdd")}
                        title={isSelected ? t("catalog.compareAdded") : t("catalog.compareAdd")}
                      >
                        {isSelected ? <Check className="w-3.5 h-3.5" /> : <Plus className="w-3.5 h-3.5" />}
                      </button>
                    </div>
                  );
                })}
              </div>
            </div>
          )}

          {selectedIds.length < maxSlots && searchResults.length === 0 && recentEntities.length > 0 && (
            <div className="mt-5 pt-4 border-t border-border">
              <div className="text-xs font-bold text-foreground mb-3">
                {t("catalog.compareDemoHint")}
              </div>
              <div className="grid grid-cols-1 sm:grid-cols-2 md:grid-cols-3 lg:grid-cols-4 gap-3">
                {recentEntities.slice(0, 8).map((r) => {
                  const isSelected = selectedIds.includes(r.id!);
                  const releaseTitle = title(r, locale);
                  const coverUrl = firstCoverUrl(r);
                  const subtitle = getEntitySummary(r.id!).subtitle;
                  return (
                    <div
                      key={r.id}
                role="button"
                tabIndex={0}
                onKeyDown={(e) => {
                  if (e.key === "Enter" || e.key === " ") {
                    e.preventDefault();
                    e.currentTarget.click();
                  }
                }}
                      onClick={() => !isSelected && addId(r.id!)}
                      className={`group rounded-xl p-3 border transition-all duration-base ease-soft flex items-center justify-between gap-3 ${
                        isSelected
                          ? "bg-surface/50 border-border/70 opacity-60 cursor-default"
                          : "bg-surface hover:bg-surfaceHover/70 border-border hover:border-primary/50 shadow-2xs hover:shadow-md cursor-pointer"
                      }`}
                    >
                      <div className="flex items-center gap-2.5 min-w-0 flex-1">
                        <div className="w-10 h-10 rounded-lg overflow-hidden shrink-0 border border-border/60">
                          <AdaptiveCardCover
                            src={coverUrl}
                            alt={releaseTitle}
                            fallbackIcon={<Disc className="w-5 h-5 text-muted-foreground/60 group-hover:text-primary transition-colors duration-fast ease-soft" />}
                            aspectClassName="w-full h-full"
                          />
                        </div>
                        <div className="min-w-0 flex-1">
                          <div className="text-xs font-semibold text-foreground group-hover:text-primary transition-colors duration-fast ease-soft truncate">
                            {releaseTitle}
                          </div>
                          <div className="text-[11px] text-muted-foreground truncate mt-0.5">
                            {subtitle}
                          </div>
                        </div>
                      </div>
                      <button
                        type="button"
                        disabled={isSelected}
                        onClick={(e) => {
                          e.stopPropagation();
                          addId(r.id!);
                        }}
                        className={`w-7 h-7 rounded-lg text-xs font-bold flex items-center justify-center shrink-0 transition-all cursor-pointer ${
                          isSelected
                            ? "bg-emerald-500/10 text-emerald-600 dark:text-success border border-emerald-500/20"
                            : "bg-primary/10 text-primary group-hover:bg-primary group-hover:text-emphasis"
                        }`}
                        aria-label={isSelected ? t("catalog.compareAdded") : t("catalog.compareAdd")}
                        title={isSelected ? t("catalog.compareAdded") : t("catalog.compareAdd")}
                      >
                        {isSelected ? <Check className="w-3.5 h-3.5" /> : <Plus className="w-3.5 h-3.5" />}
                      </button>
                    </div>
                  );
                })}
              </div>
            </div>
          )}
        </section>
      )}

      <ErrorMessage error={error} />
      {loading && (
        <div className="flex items-center justify-center gap-3 py-8 text-sm text-muted-foreground">
          <span className="w-5 h-5 border-2 border-primary border-t-transparent rounded-full animate-spin" />
          <span>{t("catalog.loading")}</span>
        </div>
      )}

      {items.length >= COMPARE_MIN_SLOTS && !loading && hasStructure && (
        <section className="bg-card border border-border rounded-card p-4 shadow-sm mt-4 space-y-4">
          <div className="flex items-center gap-2">
            <Sparkles className="w-5 h-5 text-primary" />
            <h2 className="text-base sm:text-lg font-bold text-foreground m-0">
              {t("catalog.compareContentAlignment")}
            </h2>
          </div>
          {inclusionExpressions.loading && (
            <p role="status" className="m-0 text-xs text-muted-foreground">{t("catalog.compareExpressionsLoading")}</p>
          )}
          {inclusionExpressions.failedCount > 0 && (
            <div role="alert" className="flex flex-wrap items-center gap-2 rounded-lg border border-amber-500/25 bg-amber-500/[0.07] p-3 text-xs text-amber-700 dark:text-warn-soft">
              <span>{t("entity.page.inclusionsLoadFailed", { count: inclusionExpressions.failedCount })}</span>
              <button type="button" onClick={inclusionExpressions.retry} className="text-primary hover:underline cursor-pointer">{t("catalog.retry")}</button>
            </div>
          )}
          {/* 资料不足与待确认提示必须在"无可对齐内容"时也出现：全部发行都还没录入
              曲目/收录时，只显示"没有可对齐内容"会让人误以为已比对完成。 */}
          {alignment.incomplete.length > 0 && (
            <div className="rounded-lg border border-amber-500/25 bg-amber-500/[0.07] p-3 space-y-1.5">
              <h3 className="text-xs font-semibold uppercase tracking-wider text-amber-700 dark:text-warn-soft m-0">
                {t("catalog.compareIncompleteCatalog")}
              </h3>
              <ul className="space-y-1 m-0 p-0 list-none text-xs text-muted-foreground">
                {alignment.incomplete.map((i) => (
                  <li key={`incomplete-top-${i}`}>{title(items[i]?.entity, locale)}</li>
                ))}
              </ul>
            </div>
          )}
          {alignment.perExpr.size === 0 ? (
            <p className="text-sm text-muted-foreground m-0">{t("catalog.compareNoContent")}</p>
          ) : (
            <div className="space-y-4 text-sm">
              <div>
                <h3 className="text-xs font-semibold uppercase tracking-wider text-muted-foreground m-0 mb-2">
                  {t("catalog.compareSharedAll")} · {alignment.shared.length}
                </h3>
                {alignment.shared.length === 0 ? (
                  <p className="text-xs text-muted-foreground m-0">—</p>
                ) : (
                  <div className="flex flex-wrap gap-1.5">
                    {alignment.shared.map((id) => (
                      <span
                        key={id}
                        className="inline-flex items-center px-2 py-1 rounded-lg bg-emerald-500/10 border border-emerald-500/20 text-xs text-foreground"
                      >
                        {expressionName(id)}
                      </span>
                    ))}
                  </div>
                )}
              </div>
              {alignment.partial.length > 0 && (
                <div>
                  <h3 className="text-xs font-semibold uppercase tracking-wider text-muted-foreground m-0 mb-2">
                    {t("catalog.comparePartial")} · {alignment.partial.length}
                  </h3>
                  <ul className="space-y-1.5 m-0 p-0 list-none">
                    {alignment.partial.map(({ id, in: inSet }) => (
                      <li key={id} className="flex flex-wrap items-baseline gap-x-2 gap-y-0.5 text-xs">
                        <span className="font-medium text-foreground">
                          {expressionName(id)}
                        </span>
                        <span className="text-muted-foreground">
                          {t("catalog.compareIncludedIn")}{" "}
                          {inSet.map((i) => title(items[i]?.entity, locale)).join("、")}
                        </span>
                      </li>
                    ))}
                  </ul>
                </div>
              )}
              {expressionIdentityReady && alignment.workVariants.length > 0 && (
                <div>
                  <h3 className="text-xs font-semibold uppercase tracking-wider text-muted-foreground m-0 mb-2">
                    {t("catalog.compareWorkVariants")} · {alignment.workVariants.length}
                  </h3>
                  <ul className="space-y-1.5 m-0 p-0 list-none">
                    {alignment.workVariants.map(({ contentKey, ids }) => (
                      <li key={contentKey} className="flex flex-wrap items-baseline gap-x-3 gap-y-0.5 text-xs">
                        {ids.map((id) => (
                          <span key={id} className="inline-flex items-baseline gap-1">
                            <span className="font-medium text-foreground">
                              {expressionName(id)}
                            </span>
                            <span className="text-muted-foreground">
                              ({items
                                .filter((_, i) => alignment.perExpr.get(id)?.some((o) => o.releaseIndex === i))
                                .map((x) => title(x.entity, locale))
                                .join("、")})
                            </span>
                          </span>
                        ))}
                      </li>
                    ))}
                  </ul>
                </div>
              )}
              {expressionIdentityReady && alignment.carrierOnly.length > 0 && (
                <div>
                  <h3 className="text-xs font-semibold uppercase tracking-wider text-muted-foreground m-0 mb-2">
                    {t("catalog.compareCarrierOnly")}
                  </h3>
                  <ul className="space-y-1 m-0 p-0 list-none text-xs text-muted-foreground">
                    {alignment.carrierOnly.map(([i, j]) => (
                      <li key={`${i}-${j}`}>
                        {title(items[i]?.entity, locale)} × {title(items[j]?.entity, locale)}
                      </li>
                    ))}
                  </ul>
                </div>
              )}
              {/* 引用同一表达但收录范围/重复/顺序不同（如完整录音 vs 片段）：
                  这不是"仅载体差异"，必须单独指出。 */}
              {expressionIdentityReady && alignment.rangeDiffer.length > 0 && (
                <div>
                  <h3 className="text-xs font-semibold uppercase tracking-wider text-muted-foreground m-0 mb-2">
                    {t("catalog.compareRangeDiffer")}
                  </h3>
                  <ul className="space-y-1 m-0 p-0 list-none text-xs text-muted-foreground">
                    {alignment.rangeDiffer.map(([i, j]) => (
                      <li key={`${i}-${j}`}>
                        {title(items[i]?.entity, locale)} × {title(items[j]?.entity, locale)}
                      </li>
                    ))}
                  </ul>
                </div>
              )}
              {/* 内容与范围一致、仅本版定位（页码/时间码）不同：如同一译文换了排版位置。
                  这不是内容变化，也不是收录范围变化，单独一类避免误报。 */}
              {expressionIdentityReady && alignment.locatingDiffer.length > 0 && (
                <div>
                  <h3 className="text-xs font-semibold uppercase tracking-wider text-muted-foreground m-0 mb-2">
                    {t("catalog.compareLocatingDiffer")}
                  </h3>
                  <ul className="space-y-1 m-0 p-0 list-none text-xs text-muted-foreground">
                    {alignment.locatingDiffer.map(([i, j]) => (
                      <li key={`${i}-${j}`}>
                        {title(items[i]?.entity, locale)} × {title(items[j]?.entity, locale)}
                      </li>
                    ))}
                  </ul>
                </div>
              )}
              {/* 记录级附加属性差异无法归类为内容或定位：不能断言仅载体不同，提示人工核对。 */}
              {expressionIdentityReady && alignment.attributeDiffer.length > 0 && (
                <div>
                  <h3 className="text-xs font-semibold uppercase tracking-wider text-muted-foreground m-0 mb-2">
                    {t("catalog.compareAttributeDiffer")}
                  </h3>
                  <ul className="space-y-1 m-0 p-0 list-none text-xs text-muted-foreground">
                    {alignment.attributeDiffer.map(([i, j]) => (
                      <li key={`${i}-${j}`}>
                        {title(items[i]?.entity, locale)} × {title(items[j]?.entity, locale)}
                      </li>
                    ))}
                  </ul>
                </div>
              )}
              {/* 身份元数据解析不全时不下结论，明确提示人工确认。 */}
              {alignment.pendingConfirm && (
                <p className="m-0 text-xs text-amber-700 dark:text-warn-soft">{t("catalog.comparePendingConfirm")}</p>
              )}
            </div>
          )}
        </section>
      )}

      {items.length >= COMPARE_MIN_SLOTS && !loading && (
        <section className="bg-card border border-border rounded-card overflow-hidden shadow-sm mt-4">
          <div className="p-4 border-b border-border flex flex-wrap items-center justify-between gap-3 bg-muted/20">
            <div className="flex items-center gap-2">
              <Columns className="w-5 h-5 text-primary" />
              <h2 className="text-base sm:text-lg font-bold text-foreground m-0">
                {t("catalog.compareSpecifications")}
              </h2>
              <span className="text-xs px-2.5 py-0.5 rounded-full bg-primary/10 text-primary font-semibold">
                {items.length} {t("catalog.compareSlots")}
              </span>
            </div>

            <label className="flex items-center gap-2 text-xs font-medium text-muted-foreground cursor-pointer select-none">
              <input
                type="checkbox"
                checked={highlightDiff}
                onChange={(e) => setHighlightDiff(e.target.checked)}
                className="rounded border-border text-primary focus:ring-primary/20 cursor-pointer"
              />
              <span>{t("catalog.compareHighlightDiff")}</span>
            </label>
          </div>

          <div className="overflow-x-auto">
            <table className="w-full text-left border-collapse min-w-[720px]">
              <thead>
                <tr className="border-b border-border bg-muted/30">
                  <th className="p-4 w-44 min-w-[176px] font-semibold text-xs uppercase tracking-wider text-muted-foreground border-r border-border align-top">
                    {t("catalog.attributes")}
                  </th>
                  {items.map((x) => {
                    const summary = getEntitySummary(x.entity.id);
                    const releaseTitle = summary.title;
                    const coverUrl = summary.coverUrl;
                    const catNo = summary.kind === "release" ? summary.catalogNo : "";
                    const fmt = summary.kind === "release" || summary.kind === "medium" ? summary.format : "";
                    const href = canonicalDetailPath(x.entity.id) ?? `/catalog/${x.entity.id}`;
                    return (
                      <th
                        key={x.entity.id}
                        className="p-4 min-w-[240px] max-w-[320px] align-top border-r border-border last:border-r-0 font-normal"
                      >
                        <div className="flex flex-col gap-3">
                          <div className="w-full h-36 rounded-xl overflow-hidden border border-border/70 shadow-xs">
                            <AdaptiveCardCover
                              src={coverUrl}
                              alt={releaseTitle}
                              fallbackIcon={<Disc className="w-10 h-10 text-muted-foreground/50" />}
                              fallbackTitle={releaseTitle}
                              fallbackSubtitle={fmt ? String(fmt) : catNo ? String(catNo) : undefined}
                              aspectClassName="w-full h-full"
                            />
                          </div>

                          <div className="flex items-start justify-between gap-2">
                            <Link
                              href={href}
                              className="text-sm font-bold text-foreground hover:text-primary transition-colors duration-fast ease-soft line-clamp-2 leading-snug"
                              title={releaseTitle}
                            >
                              {releaseTitle}
                            </Link>
                            <button
                              type="button"
                              onClick={() => removeId(x.entity.id)}
                              aria-label={t("catalog.compareRemove")}
                              className="p-1 rounded-md text-muted-foreground hover:text-destructive hover:bg-destructive/10 border border-transparent hover:border-destructive/20 transition-all shrink-0 cursor-pointer"
                              title={t("catalog.compareRemove")}
                            >
                              <X className="w-4 h-4" />
                            </button>
                          </div>

                          <div className="flex flex-wrap items-center gap-1.5 text-xs">
                            {fmt ? (
                              <span className="px-2 py-0.5 rounded-md bg-primary/10 text-primary font-medium">
                                {String(fmt)}
                              </span>
                            ) : null}
                            {catNo ? (
                              <span className="px-2 py-0.5 rounded-md bg-muted text-muted-foreground font-mono">
                                {String(catNo)}
                              </span>
                            ) : null}
                          </div>
                        </div>
                      </th>
                    );
                  })}
                </tr>
              </thead>

              <tbody className="divide-y divide-border">
                <tr className="hover:bg-muted/20 transition-colors duration-fast ease-soft">
                  <th className="p-4 font-medium text-xs text-muted-foreground border-r border-border align-top bg-muted/10">
                    {t("catalog.compareEntityTitle")}
                  </th>
                  {items.map((x) => {
                    const href = canonicalDetailPath(x.entity?.id) ?? `/catalog/${x.entity?.id}`;
                    return (
                      <td key={x.entity.id} className="p-4 text-xs text-foreground border-r border-border last:border-r-0 align-top">
                        <Link href={href} className="hover:text-primary hover:underline">
                          {title(x.entity, locale)}
                        </Link>
                      </td>
                    );
                  })}
                </tr>
                {comparableFields.map((k) => {
                  // getFieldName 在定义缺席时回落字段码本身，与原先的两级兜底同义。
                  const fieldName = getFieldName(dynamicDefs, k, locale);
                  const rawValues = items.map((x) => JSON.stringify(x.entity.attributes?.[k] ?? null));
                  const isDiff = new Set(rawValues).size > 1;

                  return (
                    <tr
                      key={k}
                      className={`hover:bg-muted/20 transition-colors duration-fast ease-soft ${
                        highlightDiff && isDiff ? "bg-amber-500/[0.03] dark:bg-amber-500/[0.05]" : ""
                      }`}
                    >
                      <th className="p-4 font-medium text-xs text-muted-foreground border-r border-border align-top bg-muted/10">
                        <div className="flex items-center justify-between gap-1.5">
                          <span>{fieldName}</span>
                          {highlightDiff && isDiff && (
                            <span className="text-[10px] px-1.5 py-0.2 rounded bg-amber-500/15 text-amber-600 dark:text-warn font-normal">
                              {t("catalog.compareDiffTag")}
                            </span>
                          )}
                        </div>
                      </th>
                      {items.map((x) => (
                        <td
                          key={x.entity.id}
                          className={`p-4 text-xs text-foreground border-r border-border last:border-r-0 align-top ${
                            highlightDiff && isDiff ? "font-medium" : ""
                          }`}
                        >
                          {(dynamicDefs?.fields as any)?.[k] ? (
                            <FieldValue
                              code={k}
                              defs={dynamicDefs}
                              locale={locale}
                              field={(dynamicDefs?.fields as any)[k]}
                              value={x.entity.attributes?.[k]}
                            />
                          ) : (
                            <span>{renderAttrValue(k, x.entity.attributes?.[k])}</span>
                          )}
                        </td>
                      ))}
                    </tr>
                  );
                })}

                <tr className="bg-muted/20 border-t-2 border-border">
                  <th className="p-4 font-bold text-xs uppercase tracking-wider text-foreground border-r border-border align-top bg-muted/30">
                    <div className="flex items-center gap-1.5">
                      <Layers className="w-4 h-4 text-primary" />
                      <span>{t("catalog.compareMediaStructure")}</span>
                    </div>
                  </th>
                  {items.map((x, index) => (
                    <td
                      key={x.entity.id}
                      className="p-4 align-top border-r border-border last:border-r-0"
                    >
                      {(x.children || []).length === 0 ? (
                        <span className="text-xs text-muted-foreground">—</span>
                      ) : (
                      <div className="space-y-3">
                        {(x.children || []).map((m: any) => (
                          <div
                            key={m.medium.id}
                            className="bg-background dark:bg-muted/20 border border-border rounded-xl p-3.5 shadow-2xs"
                          >
                            <div className="flex items-center justify-between gap-2 mb-2 pb-2 border-b border-border">
                              <h3 className="text-xs font-bold text-foreground m-0 truncate">
                                <Link href={`/catalog/${m.medium.id}`} className="hover:text-primary">{title(m.medium, locale)}</Link>
                              </h3>
                              {m.medium.attributes?.format ? (
                                <span className="text-[10px] px-1.5 py-0.5 rounded bg-muted text-muted-foreground font-medium">
                                  {getTermName(dynamicDefs, dynamicDefs?.fields?.format?.vocabulary || "", String(m.medium.attributes.format), locale)}
                                </span>
                              ) : null}
                            </div>

                            <div className="space-y-1.5">
                              {orderedTracksWithDepth(m.tracks || []).map(({ track: tr, depth }) => (
                                <div
                                  key={tr.id}
                                  className="text-xs py-1 border-b border-dashed border-border/60 last:border-b-0"
                                  style={depth ? { marginLeft: `${depth * 12}px` } : undefined}
                                >
                                  <div className="flex items-center gap-1.5">
                                    <span className="font-mono text-muted-foreground text-[11px] shrink-0 font-semibold w-5">
                                      {tr.number || tr.position}
                                    </span>
                                    <Link href={`/catalog/${tr.id}`} className="font-medium text-foreground hover:text-primary truncate">
                                      {title(tr, locale)}
                                    </Link>
                                  </div>

                                  {[...(tr.contents || [])].sort((a, b) => a.position - b.position).map((c, i) => {
                                    const isVariant = sets.some(
                                      (s, j) => j !== index && !s.has(c.expression_id)
                                    );
                                    return (
                                      <div
                                        key={i}
                                        className="pl-6 pt-0.5 space-y-0.5 text-[11px]"
                                      >
                                        <div className="flex items-center gap-2">
                                          {exprEntities[c.expression_id] ? (
                                            <Link href={`/catalog/${c.expression_id}`} className="text-primary hover:underline">{title(exprEntities[c.expression_id], locale)}</Link>
                                          ) : <span className="text-muted-foreground">{expressionName(c.expression_id)}</span>}
                                          {isVariant && (
                                            <span className="text-[10px] px-1.5 py-0.2 rounded-full font-medium bg-emerald-500/10 text-emerald-600 dark:text-success border border-emerald-500/20 shadow-2xs">
                                              {t("catalog.variantContent")}
                                            </span>
                                          )}
                                        </div>
                                        <div className="flex flex-wrap gap-x-3 gap-y-0.5">
                                          <LocatorInline defs={dynamicDefs} value={c.locator} locale={locale} />
                                          <GroupAttributeInline defs={dynamicDefs} code="inclusion_attributes" value={c.attributes} locale={locale} />
                                        </div>
                                      </div>
                                    );
                                  })}
                                </div>
                              ))}
                            </div>
                          </div>
                        ))}
                      </div>
                      )}
                    </td>
                  ))}
                </tr>
              </tbody>
            </table>
          </div>
        </section>
      )}
      </>
      )}
    </div>
  );
}
