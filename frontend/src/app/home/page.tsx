"use client";

import { useCallback, useEffect, useMemo, useState } from "react";
import Link from "next/link";
import { Navbar } from "@/components/Navbar";
import { useI18n } from "@/i18n/I18nProvider";
import { useDefinitions, getKindName, getTagNames, type KindMap } from "@/lib/definitions";
import { pickRecordTitle } from "@/lib/titles";
import { coverUrl, type CoverBearing } from "@/lib/cover";
import { PageContainer, PageShell } from "@/components/ui/PageShell";
import { useTitleDisplayOrder } from "@/hooks/useTitleDisplayOrder";
import { useAuth } from "@/lib/authContext";
import { EntityCard } from "@/components/common/EntityCard";
import { fetchApi } from "@/lib/api";
import { localizeCatalogError } from "@/lib/catalogErrors";
import { HomeCustomizeModal } from "@/components/home/HomeCustomizeModal";
import {
  EMPTY_PREFERENCES,
  iconFor,
  normalizePreferences,
  shelfTitle,
  type HomePreferences,
  type ShelfLike,
} from "@/lib/homeSections";
import { ArrowRight, Sparkles, Sliders } from "lucide-react";

/** 首页分区条目：图片结构复用 lib/cover 的 CoverBearing（首张即封面只在那一处定义）。 */
type EntityItem = CoverBearing & {
  id: string;
  kind: string;
  title: string;
  original_language?: string;
  translations?: Record<string, { title?: string; summary?: string; aliases?: string[] }>;
  attributes?: { tags?: string[] };
  version?: number;
};

// 分区定义（含 source：system 只能隐藏、custom 可删除）与图标集见 lib/homeSections.ts。
type FeedSection = { shelf: ShelfLike; items: EntityItem[]; total?: number };

export default function HomePage() {
  const { t, tr, locale } = useI18n();
  const { definitions, kinds } = useDefinitions();
  const titleOrder = useTitleDisplayOrder();
  const { user, loading: authLoading } = useAuth();

  const [sections, setSections] = useState<FeedSection[]>([]);
  const [loading, setLoading] = useState(true);
  const [failed, setFailed] = useState(false);
  const [customizing, setCustomizing] = useState(false);
  const [prefs, setPrefs] = useState<HomePreferences>(EMPTY_PREFERENCES);
  const [templates, setTemplates] = useState<ShelfLike[]>([]);
  const [prefsLoading, setPrefsLoading] = useState(false);
  const [prefsLoadFailed, setPrefsLoadFailed] = useState(false);
  const [saving, setSaving] = useState(false);
  const [saveError, setSaveError] = useState("");

  const loadFeed = useCallback(async () => {
    setLoading(true);
    setFailed(false);
    try {
      // 统一走 fetchApi：同域 Cookie、语言头与错误处理保持一致。
      // 用 components/catalog 的 api() 只会发 cookie，带身份的偏好不会被识别。
      const r = await fetchApi<{ items: FeedSection[] }>("/catalog/shelves/feed?per_shelf=14");
      setSections(Array.isArray(r.items) ? r.items : []);
    } catch {
      setSections([]);
      setFailed(true);
    } finally {
      setLoading(false);
    }
  }, []);

  useEffect(() => {
    if (authLoading) return;
    void loadFeed();
  }, [authLoading, loadFeed]);

  // 空分区不展示：推荐位不该出现"0 部作品"这类噪音。
  const visibleSections = useMemo(
    () => sections.filter((s) => (Array.isArray(s.items) ? s.items : []).length > 0),
    [sections],
  );

  const openCustomize = async () => {
    setSaveError("");
    setPrefsLoadFailed(false);
    setCustomizing(true);
    if (!user) return;
    setPrefsLoading(true);
    try {
      // 偏好给出用户自己的分区配置；/catalog/shelves 给出系统预设——它既是「添加分区」
      // 的候选，也是隐藏分区的定义来源（feed 已按偏好把隐藏项过滤掉了）。
      const [loaded, tpl] = await Promise.all([
        fetchApi<HomePreferences>("/catalog/me/home-preferences"),
        fetchApi<{ items: ShelfLike[] }>("/catalog/shelves"),
      ]);
      setPrefs(normalizePreferences(loaded));
      setTemplates(Array.isArray(tpl.items) ? tpl.items : []);
    } catch {
      setPrefsLoadFailed(true);
      setSaveError(t("catalog.loadFailed"));
    } finally {
      setPrefsLoading(false);
    }
  };

  const savePrefs = async (payload: HomePreferences) => {
    setSaving(true);
    setSaveError("");
    try {
      await fetchApi("/catalog/me/home-preferences", { method: "PUT", body: JSON.stringify(payload) });
      setCustomizing(false);
      await loadFeed();
    } catch (e) {
      // 后端给的是稳定错误码（invalid_slug / too_many_sections …），翻成四语文案再显示。
      setSaveError(localizeCatalogError((e as Error).message, t));
    } finally {
      setSaving(false);
    }
  };

  // 恢复默认 = 清空偏好、回落到系统预设；不改系统货架，也不影响别人。
  const resetPrefs = async () => {
    setSaving(true);
    setSaveError("");
    try {
      await fetchApi("/catalog/me/home-preferences", {
        method: "PUT",
        body: JSON.stringify(EMPTY_PREFERENCES),
      });
      setPrefs(EMPTY_PREFERENCES);
      setCustomizing(false);
      await loadFeed();
    } catch (e) {
      setSaveError(localizeCatalogError((e as Error).message, t));
    } finally {
      setSaving(false);
    }
  };

  const showSkeleton = authLoading || loading;

  // 根容器裁剪装饰光晕：-bottom-40/-right-40 的 600px 光晕溢出到视口外会撑出横向滚动条
  // （线上实测桌面 +154px / 移动 +161px）。用 overflow-clip 而不是 overflow-hidden——
  // clip 不建滚动容器，顶栏照旧相对视口吸附。
  return (
    <div className="min-h-screen flex flex-col bg-background text-text-strong relative overflow-clip selection:bg-primary selection:text-white">
      <div className="absolute inset-0 bg-radial-vignette opacity-70 pointer-events-none" aria-hidden />
      <div className="absolute -top-40 -left-40 w-[600px] h-[600px] bg-primary/10 rounded-full blur-[150px] pointer-events-none" aria-hidden />
      <div className="absolute -bottom-40 -right-40 w-[600px] h-[600px] bg-accent/10 rounded-full blur-[150px] pointer-events-none" aria-hidden />

      <Navbar />

      {/* 分区维持紧凑节奏，标题与列表之间仍保留清晰层级。 */}
      <PageShell width="page" spacing="none" contentClassName="space-y-5" className="relative z-10">
        <div className="flex flex-wrap items-center justify-between gap-3">
          <div>
            <h1 className="font-display text-xl font-bold tracking-tight text-text-strong">
              {t(user ? "home.recommended" : "navigation.home")}
            </h1>
          </div>
          {user && (
            <button
              type="button"
              onClick={() => void openCustomize()}
              className="mf-focus inline-flex items-center gap-1.5 px-3.5 h-9 max-sm:min-h-[44px] rounded-lg border border-line bg-emphasis/[0.03] hover:bg-emphasis/[0.07] text-xs font-medium text-text-body hover:text-emphasis transition-colors duration-fast ease-soft cursor-pointer"
            >
              <Sliders className="w-3.5 h-3.5" />
              <span>{t("home.customize")}</span>
            </button>
          )}
        </div>

        {!showSkeleton && visibleSections.length > 1 && (
          <nav aria-label={t("home.sections")} className="flex flex-wrap items-center gap-2">
            {visibleSections.map(({ shelf }) => {
              const Icon = iconFor(shelf);
              return (
                <a key={shelf.slug} href={`#shelf-${shelf.slug}`} className="mf-focus inline-flex items-center gap-1.5 min-h-9 max-sm:min-h-[44px] rounded-full border border-line bg-surface px-3 text-xs font-medium text-text-body hover:border-primary/40 hover:text-primary transition-colors duration-fast ease-soft">
                  <Icon className="hidden sm:block w-3.5 h-3.5" aria-hidden />
                  <span>{shelfTitle(shelf, locale)}</span>
                </a>
              );
            })}
          </nav>
        )}

        {failed && (
          <div role="alert" className="flex flex-wrap items-center justify-between gap-3 p-3.5 rounded-lg border border-amber-500/30 bg-amber-500/10 text-warn-soft text-xs">
            <span>{t("catalog.connectionError")}</span>
            <button type="button" onClick={() => void loadFeed()} className="mf-focus min-h-9 px-2 text-primary hover:underline">{t("catalog.retry")}</button>
          </div>
        )}

        {showSkeleton ? (
          <div className="space-y-5">
            {[1, 2].map((i) => (
              <div key={i} className="space-y-4">
                <div className="h-5 w-40 bg-emphasis/[0.04] rounded-md animate-pulse" />
                <div className="grid gap-3 grid-cols-2 sm:grid-cols-[repeat(auto-fill,minmax(160px,1fr))]">
                  {Array.from({ length: 6 }).map((_, j) => (
                    <div key={j} className="aspect-square rounded-card bg-emphasis/[0.02] border border-line-subtle animate-pulse" />
                  ))}
                </div>
              </div>
            ))}
          </div>
        ) : visibleSections.length === 0 && !failed ? (
          <div className="p-5 rounded-card border border-dashed border-line bg-emphasis/[0.01] text-center space-y-3">
            <Sparkles className="w-7 h-7 text-gray-600 mx-auto" />
            <p className="text-sm text-text-muted">{t("home.recommendEmpty")}</p>
            <Link
              href="/new"
              className="inline-flex items-center gap-1.5 px-4 py-2 rounded-lg bg-primary/10 hover:bg-primary/20 text-primary text-xs font-medium transition-colors duration-fast ease-soft"
            >
              <Sparkles className="w-3.5 h-3.5" />
              <span>{t("home.addFirst")}</span>
            </Link>
          </div>
        ) : (
          visibleSections.map(({ shelf, items, total }) => {
            const Icon = iconFor(shelf);
            const title = shelfTitle(shelf, locale);
            return (
              <section key={shelf.slug} id={`shelf-${shelf.slug}`} aria-labelledby={`shelf-title-${shelf.slug}`} className="scroll-mt-[calc(var(--mf-header-h)+1rem)] space-y-4">
                <div className="flex flex-wrap items-center justify-between gap-2 border-b border-line pb-3">
                  <div className="flex min-w-0 items-center gap-3">
                    <div className="w-9 h-9 shrink-0 rounded-xl border border-primary/20 bg-primary/10 flex items-center justify-center text-primary">
                      <Icon className="w-5 h-5" />
                    </div>
                    <div className="flex min-w-0 flex-wrap items-center gap-2">
                      <h2 id={`shelf-title-${shelf.slug}`} className="break-words font-bold text-text-strong text-base sm:text-lg tracking-tight">{title}</h2>
                      <span className="shrink-0 px-2 py-0.5 rounded-full bg-emphasis/[0.06] text-text-muted text-xs font-mono">
                        {t("home.itemCount", { count: (total ?? items.length).toString() })}
                      </span>
                    </div>
                  </div>
                  <Link
                    href={"/explore?" + shelfExploreParam(shelf)}
                    className="mf-focus shrink-0 inline-flex items-center gap-1 min-h-9 text-xs text-primary hover:underline"
                  >
                    <span>{t("home.viewAll")}</span>
                    <ArrowRight className="w-3.5 h-3.5" aria-hidden />
                  </Link>
                </div>

                <div className="grid gap-3 grid-cols-2 sm:grid-cols-[repeat(auto-fill,minmax(160px,1fr))]">
                  {items.map((item) => {
                    const displayTitle = pickRecordTitle(locale, item.translations, item.title, {
                      order: titleOrder,
                      originalLanguage: item.original_language,
                    });
                    // 角标显示**实体类型**（八骨架 kind），不是业务分类：
                    // 分类由货架（catalog.shelves）承担，业务类型在卡片正文里另行展示。
                    const badge = badgeFor(item.kind, kinds, locale, tr);
                    // 首页分区只看封面（首张图）：多图画廊在详情页。
                    const cover = coverUrl(item);
                    return (
                      <EntityCard
                        key={item.id}
                        id={item.id}
                        kind={item.kind}
                        badgeLabel={badge}
                        title={displayTitle}
                        baseTitle={item.title}
                        tags={getTagNames(definitions, item.attributes?.tags, locale)}
                        pictureUrl={cover}
                      />
                    );
                  })}
                </div>
              </section>
            );
          })
        )}
      </PageShell>

      <HomeCustomizeModal
        open={customizing}
        loading={prefsLoading}
        prefs={prefs}
        templates={templates}
        feedSections={sections}
        defs={definitions}
        saving={saving}
        loadFailed={prefsLoadFailed}
        error={saveError}
        onClose={() => setCustomizing(false)}
        onSave={(payload) => void savePrefs(payload)}
        onReset={() => void resetPrefs()}
      />

      <footer className="border-t border-line-subtle py-4 bg-surface/30 backdrop-blur-md">
        <PageContainer className="flex flex-col sm:flex-row items-center justify-between gap-4 text-xs font-mono text-text-muted">
          <div>
            <span>© 2026 MetaFusion</span>
          </div>
          {/* 这里只留顶栏没有的入口：/ 与 /explore、/community、/docs/catalog
              都已由顶栏 Logo 与主导航覆盖，不再重复。 */}
          <div className="flex items-center gap-4 flex-wrap">
            <a href="/developers" className="hover:text-emphasis transition-colors duration-fast ease-soft">
              {t("home.footerApi")}
            </a>
          </div>
        </PageContainer>
      </footer>
    </div>
  );
}

// 分区“查看全部”进入探索页，使用规则的全部开放标签。
function shelfExploreParam(shelf: ShelfLike): string {
  const params = new URLSearchParams({ kind: "work" });
  for (const tag of shelf.query?.tags || []) if (tag) params.append("tags", tag);
  return params.toString();
}

// 卡片左上角角标 = 实体类型（八骨架 kind），名称取服务端 definitions 的多语言 kinds，
// 缺失时回退前端字典的同名键。**不再**用业务类型当"分类"角标：
//   * 业务类型（album/song/动画…）是动态类型，本就该在卡片正文里以类型标签呈现；
//   * "分类"这件事只由货架（catalog.shelves，数据驱动、后台可配、名称多语言）承担。
// 这样页面不再出现"系统自己发明一套固定分类"的东西。
function badgeFor(
  kind: string,
  kinds: KindMap | null,
  loc: string,
  translate: (k: string, f: string) => string,
): string {
  return getKindName(kinds, kind, loc, translate("catalog.kind." + kind, kind));
}
