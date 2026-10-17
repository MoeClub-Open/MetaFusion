"use client";

import React, { useCallback, useEffect, useMemo, useState } from "react";
import { useParams } from "next/navigation";
import Link from "next/link";
import { api, Entity, mapLimit, title as entityTitle } from "@/components/catalog/api";
import { useI18n } from "@/i18n/I18nProvider";
import {
  useDefinitions,
  getKindName,
  getTermName,
  resolveLocalizedName,
  blocksForEntity,
} from "@/lib/definitions";
import { orderedTracksWithDepth } from "@/lib/trackTree";
import { PageShell } from "@/components/ui/PageShell";
import { EntityIdentityHeader } from "@/components/entity/EntityIdentityHeader";
import { QualityBadge } from "@/components/catalog/QualityBadge";
import { Card } from "@/components/ui/Card";
import { SectionTitle } from "@/components/ui/SectionTitle";
import { EntityStaffSection } from "@/components/entity/EntityStaffSection";
import { AggregateRelationList } from "@/components/catalog/AggregateRelationList";
import { classifyLoadFailure, DetailNotFound, DetailUnavailable, type LoadFailureKind } from "@/components/common/DetailLoadStates";
import { RecordList, GroupAttributeInline, LocatorInline } from "@/components/catalog/TemplateAttributeSections";
import { EntityLink } from "@/components/catalog/Fields";
import { formatDuration as formatDurationShared } from "@/lib/duration";
import { AdaptiveCardCover } from "@/components/common/AdaptiveCardCover";
import { CoverOriginNote } from "@/components/common/CoverOriginNote";
import { resolveCover } from "@/lib/cover";
import { EntityArticle } from "@/components/catalog/EntityArticle";
import { ARTICLE_ANCHOR_TABS } from "@/lib/catalogArticle";
import { useTitleDisplayOrder } from "@/hooks/useTitleDisplayOrder";
import { ErrorMessage } from "@/components/catalog/Fields";
import { InclusionSources } from "@/components/catalog/InclusionContents";
import { TabBar, useHashTab, type TabItem } from "@/components/catalog/DetailTabs";
import { ExternalAuthorityLinks } from "@/components/entity/ExternalAuthorityLinks";
import { EntityRevisions } from "@/components/catalog/EntityRevisions";
import { EntityCommentsPanel } from "@/components/entity/EntityCommentsPanel";

/** 页签没有数据时的统一占位。 */
function EmptyPanel() {
  const { t } = useI18n();
  return <Card padding="section"><p className="text-xs text-text-muted text-center">{t("common.noData")}</p></Card>;
}
import {
  ArrowLeft,
  ChevronDown,
  ChevronLeft,
  ChevronRight,
  ExternalLink,
  Layers,
  Users,
  BookOpen,
  Disc,
  Network,
  MessageSquare,
  History,
} from "lucide-react";


// 时长统一到 lib/duration.ts（M:SS / 超 1 小时 H:MM:SS），空值这里仍显示 "—"。
// 旧实现没有小时段：7200 秒会渲染成 "120:00"，与"附加信息"里的口径也对不上。
function formatDuration(totalSeconds?: number | null): string {
  return formatDurationShared(totalSeconds) || "—";
}

function attrText(v: unknown): string {
  if (typeof v === "string") return v.trim();
  if (typeof v === "number") return String(v);
  return "";
}

function attrList(v: unknown): Record<string, any>[] {
  if (!Array.isArray(v)) return [];
  return v.filter((x) => x && typeof x === "object") as Record<string, any>[];
}

function localizedText(v: unknown, locale: string): string {
  if (typeof v === "string") return v;
  if (v && typeof v === "object") {
    const rec = v as Record<string, string>;
    // 统一回退链：精确语种 → 短码 → zh-CN → zh-TW → ja → en-US（此前缺繁中/日文两档）。
    return resolveLocalizedName(rec, locale, "");
  }
  return "";
}


type Occurrence = {
  /** 引用形态：实体在批量响应的共享 entities 表里，按 id 取。 */
  release_id: string;
  medium_id: string;
  track_id: string;
  expression_id: string;
  position: number;
  locator?: Record<string, any> | null;
  /** 收录附加属性：键为 definitions 的 inclusion_attributes 子字段码。 */
  attributes?: Record<string, any> | null;
};

type MediumRow = { medium: Entity; tracks: Entity[] };
type ReleaseTOC = {
  release: Entity;
  media: MediumRow[];
  expressions: Record<string, Entity>;
  definition_etag: string;
};

function Collapsible({
  title,
  count,
  children,
}: {
  title: string;
  count?: number;
  children: React.ReactNode;
}) {
  const { t } = useI18n();
  const [open, setOpen] = useState(false);
  return (
    <Card padding="none" className="overflow-hidden">
      <button
        type="button"
        onClick={() => setOpen((v) => !v)}
        aria-expanded={open}
        className="w-full px-3.5 sm:px-4 py-2.5 flex items-center justify-between gap-2 hover:bg-surfaceSubtle transition-colors duration-fast ease-soft min-h-[44px]"
      >
        <span className="flex items-center gap-2 text-xs font-semibold text-text-strong">
          <span>{title}</span>
          {typeof count === "number" && (
            <span className="px-1.5 py-0.5 rounded-sm bg-primary/10 text-primary font-mono text-[10px] font-bold">
              {count}
            </span>
          )}
        </span>
        <span className="inline-flex items-center gap-1.5 font-mono text-[11px] text-text-faint">
          <span className="hidden sm:inline">{open ? t("common.collapse") : t("common.expand")}</span>
          <ChevronDown className={`w-3.5 h-3.5 transition-transform duration-base ease-soft ${open ? "rotate-180" : ""}`} strokeWidth={1.6} />
        </span>
      </button>
      {open && <div className="px-3.5 sm:px-4 py-3 border-t border-line-subtle">{children}</div>}
    </Card>
  );
}

export default function ReleaseDetailLayout() {
  const params = useParams();
  const releaseId = params.id as string;
  const { t, locale } = useI18n();
  const titleOrder = useTitleDisplayOrder();
  // 定义一律走 lib/definitions.ts 的缓存（与 /catalog 路由同源）——定义从来不住在 CatalogProvider 里
  // （Provider 只留模块状态与实例初始化状态），以前这里读 Provider 的 definition 恒为 undefined，字段名只能显示裸码。
  const { definitions: dynamicDefs, kinds } = useDefinitions();

  const [release, setRelease] = useState<Entity | null>(null);
  const [media, setMedia] = useState<{ medium: Entity; tracks: Entity[] }[]>([]);
  const [works, setWorks] = useState<Record<string, Entity>>({});
  const [expressions, setExpressions] = useState<Record<string, Entity>>({});
  const [occurrences, setOccurrences] = useState<Record<string, Occurrence[]>>({});
  // 同篇目其它表达（如同一集的加长版/另一录音）的收录，与自身收录分开展示，避免误读。
  const [expressionSiblings, setExpressionSiblings] = useState<Record<string, Occurrence[]>>({});
  const [expressionCredits, setExpressionCredits] = useState<Record<string, string>>({});
  // 收录引用的 release/medium/track 实体（批量响应的共享表）。
  const [occurrenceEntities, setOccurrenceEntities] = useState<Record<string, Entity>>({});
  const [loading, setLoading] = useState(true);
  // 失败态按四种可解释的原因归类：404/invalid_kind 是"没有这个条目"，429/5xx/断网是
  // "暂时取不到"。这里之前存的是原始 message，结果 invalid_kind、Failed to fetch 这类
  // 裸码与浏览器英文错误被当正文吐出来（同页对 not_found 有特判，其余全部漏过）。
  const [error, setError] = useState<LoadFailureKind | "">("");
  // 重试入口：重跑同一次取数（不改变路由与筛选）。
  const [reloadKey, setReloadKey] = useState(0);
  // 服务端解析与详情响应若不一致，禁止按发行版模板渲染。
  const [kindMismatch, setKindMismatch] = useState<string | null>(null);
  const [activeTab, setActiveTab] = useState<string>("all");
  const [showBonus, setShowBonus] = useState(false);
  const [releaseStaffCount, setReleaseStaffCount] = useState(0);
  // 署名计数未回告前视为"加载中"：计数初始 0 不能直接决定挂载，否则
  // EntityStaffSection 永远没有机会加载，含署名的发行首次进入也看不到人员。
  const [releaseStaffLoaded, setReleaseStaffLoaded] = useState(false);
  const handleReleaseStaffCount = useCallback((n: number) => {
    setReleaseStaffCount(n);
    setReleaseStaffLoaded(true);
  }, []);
  const [siblingReleases, setSiblingReleases] = useState<Entity[]>([]);
  const [editionGroup,setEditionGroup] = useState<Entity | null>(null);
  const [editionsError,setEditionsError] = useState("");
  // 批量数据的加载缺口按来源细分：实体查询与批量详情是两条路径，任一部分未恢复都要
  // 保留重试提示。旧实现只在逐条实体也失败时计数，批量失败但实体补回时计数为 0，
  // 提示不出现，用户看到的是"暂无反向收录数据"。署名与收录、兄弟收录同属批量详情响应。
  const [expressionLoadGaps, setExpressionLoadGaps] = useState<{ entities: number; details: number }>({ entities: 0, details: 0 });
  const [expressionReloadToken, setExpressionReloadToken] = useState(0);
  // 收录表默认只展示部分行，避免大目录下表格过长；"显示更多"就地展开。
  const [occPage, setOccPage] = useState(1);
  const OCC_PAGE_SIZE = 20;
  // 单个表达的收录可能很多：默认只显示前 OCC_PAGE_SIZE 条，可就地展开全部。
  const [expandedOcc, setExpandedOcc] = useState<Record<string, boolean>>({});

  useEffect(() => {
    if (!releaseId) return;
    let cancelled = false;
    setLoading(true);
    setError("");
    setRelease(null);
    setMedia([]);
    setWorks({});
    setExpressions({});
    (async () => {
      try {
        const rel = await api<Entity>(`/catalog/entities/${releaseId}/resolve`);
        if (rel.kind !== "release") {
          // 种类不符时不再继续请求按发行版模板组织的数据。
          if (!cancelled) setKindMismatch(rel.kind || "unknown");
          return;
        }
        if (!cancelled) setKindMismatch(null);
        const toc = await api<ReleaseTOC>(`/catalog/releases/${releaseId}/toc`);
        const currentRelease = toc.release;
        const rows = toc.media || [];
        const tocExpressions = toc.expressions || {};
        rows.sort((a, b) => (a.medium.position || 0) - (b.medium.position || 0));
        rows.forEach((r) => r.tracks.sort((a, b) => (a.position || 0) - (b.position || 0)));
        const workIds = Array.from(new Set((currentRelease.subjects || []).map((s) => s.work_id).filter(Boolean)));
        const workMap: Record<string, Entity> = {};
        await mapLimit(
          workIds, 8, async (id) => {
            try {
              workMap[id] = await api<Entity>(`/catalog/entities/${id}`);
            } catch {
              /* ignore */
            }
          }
        );
        if (cancelled) return;
        setRelease(currentRelease);
        setMedia(rows);
        setWorks(workMap);
        setExpressions(tocExpressions);
        setActiveTab("all");
      } catch (e: any) {
        if (!cancelled) setError(classifyLoadFailure(e));
      } finally {
        if (!cancelled) setLoading(false);
      }
    })();
    return () => {
      cancelled = true;
    };
  }, [releaseId, reloadKey]);

  // 版本组由显式关系声明；共同收录一个 Work 只说明相关收录。
  useEffect(() => {
    if (!release?.id) {
      setSiblingReleases([]);
      return;
    }
    setSiblingReleases([]); setEditionGroup(null); setEditionsError("");
    let cancelled = false;
    (async () => {
      try {
        const r = await api<{group:Entity | null;editions:Entity[]}>(`/catalog/releases/${release.id}/editions`);
        if (!cancelled) { setSiblingReleases(r.editions); setEditionGroup(r.group); }
      } catch (e) {
        if (!cancelled) setEditionsError((e as Error).message);
      }
    })();
    return () => {
      cancelled = true;
    };
  }, [release?.id,release?.version,dynamicDefs,reloadKey]);

  const expressionIds = useMemo(() => {
    const ids = new Set<string>();
    for (const row of media) {
      for (const tr of row.tracks) {
        for (const c of tr.contents || []) {
          if (c.expression_id) ids.add(c.expression_id);
        }
      }
    }
    return Array.from(ids);
  }, [media]);

  useEffect(() => {
    if (expressionIds.length === 0) return;
    let cancelled = false;
    (async () => {
      const exprMap: Record<string, Entity> = {};
      const occMap: Record<string, Occurrence[]> = {};
      const siblingMap: Record<string, Occurrence[]> = {};
      const creditMap: Record<string, string> = {};
      // 共享实体表：表达自身与收录引用到的 release/medium/track 都在这里，
      // 避免同一实体在每条收录里重复传输。
      const entityMap: Record<string, Entity> = {};
      // 已恢复的条目分两条路径记录：实体查询与批量详情互不包含（批量失败时实体可能
      // 仍能逐条补回），因此必须分别统计缺口。
      const gotEntities = new Set<string>();
      const gotDetails = new Set<string>();
      // 一条批量请求取回表达实体 + 自身收录 + 同篇目兄弟收录 + 首个署名，
      // 替代原先逐条 entities/:id、occurrences、relations、对端实体四类 N+1 请求。
      // 用 POST + JSON body：300 个 UUID 拼进 GET query 约 11KB，会超过常见 Nginx
      // 默认 8KB 请求行限制；分片仍保留以控制单请求体大小。
      const CHUNK = 300;
      for (let i = 0; i < expressionIds.length; i += CHUNK) {
        const slice = expressionIds.slice(i, i + CHUNK);
        try {
          const r = await api<{
            items: Record<string, { entity: Entity; occurrences: Occurrence[]; siblings?: Occurrence[]; credit_title?: string }>;
            entities?: Record<string, Entity>;
          }>("/catalog/expressions/details", "POST", { ids: slice });
          for (const [id, d] of Object.entries(r.items || {})) {
            gotDetails.add(id);
            if (d?.entity) {
              exprMap[id] = d.entity;
              gotEntities.add(id);
            }
            occMap[id] = d?.occurrences || [];
            siblingMap[id] = d?.siblings || [];
            if (d?.credit_title) creditMap[id] = d.credit_title;
          }
          for (const [id, e] of Object.entries(r.entities || {})) {
            if (e) {
              entityMap[id] = e;
              if (!exprMap[id]) exprMap[id] = e;
              gotEntities.add(id);
            }
          }
        } catch {
          // 批量失败时退化为逐条取实体，保证页面仍可用；收录/署名无法由此恢复，
          // 仍计入 details 缺口并提示重试，不再静默留空。
          await mapLimit(slice, 8, async (id) => {
            try {
              exprMap[id] = await api<Entity>(`/catalog/entities/${id}`);
              gotEntities.add(id);
            } catch {
              // 计入下方缺口统计。
            }
          });
        }
      }
      if (cancelled) return;
      setExpressions(exprMap);
      setOccurrences(occMap);
      setExpressionSiblings(siblingMap);
      setExpressionCredits(creditMap);
      setOccurrenceEntities(entityMap);
      setExpressionLoadGaps({
        entities: expressionIds.filter((id) => !gotEntities.has(id)).length,
        details: expressionIds.filter((id) => !gotDetails.has(id)).length,
      });
    })();
    return () => {
      cancelled = true;
    };
  }, [expressionIds.join(","), locale, expressionReloadToken]);

  const crossDurations = useMemo(() => {
    const map = new Map<string, Map<string, number>>();
    for (const row of media) {
      for (const tr of row.tracks) {
        const dur = Number(tr.attributes?.duration);
        if (!dur || dur <= 0) continue;
        for (const c of tr.contents || []) {
          if (!c.expression_id) continue;
          if (!map.has(c.expression_id)) map.set(c.expression_id, new Map());
          map.get(c.expression_id)!.set(row.medium.id!, dur);
        }
      }
    }
    return map;
  }, [media]);

  // 注意：全部 use* 必须在 early return 之前，保持每次渲染 Hook 顺序一致。
  // 载体树：A/B 面等子载体挂在所属盘之下；格式分组与曲目计数都按顶层载体算，
  // 子载体（面）不单独成组，避免同一张盘被拆成"主载体+未知格式"两份。
  const mediumTree = useMemo(() => {
    const byId = new Map<string, MediumRow>();
    for (const r of media) if (r.medium.id) byId.set(r.medium.id, r);
    const childrenOf = new Map<string, MediumRow[]>();
    const roots: MediumRow[] = [];
    for (const r of media) {
      const pid = r.medium.parent_id || "";
      if (pid && byId.has(pid)) {
        const list = childrenOf.get(pid) || [];
        list.push(r);
        childrenOf.set(pid, list);
      } else {
        roots.push(r);
      }
    }
    const byPos = (a: MediumRow, b: MediumRow) => (a.medium.position || 0) - (b.medium.position || 0);
    roots.sort(byPos);
    childrenOf.forEach((list) => list.sort(byPos));
    const totalTracks = new Map<string, number>();
    const totalOf = (r: MediumRow): number => {
      const id = r.medium.id!;
      const cached = totalTracks.get(id);
      if (cached !== undefined) return cached;
      totalTracks.set(id, 0); // 防御异常环状数据，先占位再回填
      const n = r.tracks.length + (childrenOf.get(id) || []).reduce((s, c) => s + totalOf(c), 0);
      totalTracks.set(id, n);
      return n;
    };
    roots.forEach(totalOf);
    return { roots, childrenOf, totalOf };
  }, [media]);

  const formatGroups = useMemo(() => {
    const groups = new Map<string, MediumRow[]>();
    for (const row of mediumTree.roots) {
      const fmt = attrText(row.medium.attributes?.format) || "unknown";
      if (!groups.has(fmt)) groups.set(fmt, []);
      groups.get(fmt)!.push(row);
    }
    return Array.from(groups.entries());
  }, [mediumTree]);

  // 页签（与作品详情页一致）：概览 · 载体 · 参与与署名 · 关联 · 评论 · 修订历史与证据溯源。
  const [aggregateCount, setAggregateCount] = useState<number | null>(null);
  const [commentCount, setCommentCount] = useState<number | null>(null);
  const [revisions, setRevisions] = useState<any[]>([]);
  const [revisionsState, setRevisionsState] = useState<"loading" | "ready" | "error">("loading");
  useEffect(() => {
    if (!releaseId) return;
    let alive = true;
    setRevisionsState("loading");
    api<{ items: any[] }>(`/catalog/entities/${encodeURIComponent(releaseId)}/revisions`)
      .then((res) => { if (alive) { setRevisions(Array.isArray(res?.items) ? res.items : []); setRevisionsState("ready"); } })
      .catch(() => { if (alive) setRevisionsState("error"); });
    return () => { alive = false; };
  }, [releaseId]);
  const sectionTabs: TabItem[] = [
    { id: "overview", label: t("entity.page.navOverview"), icon: <BookOpen className="w-3.5 h-3.5" strokeWidth={1.5} /> },
    { id: "mediums", label: t("release.detail.tabMediums"), badge: media.length, icon: <Disc className="w-3.5 h-3.5" strokeWidth={1.5} /> },
    { id: "credits", label: t("release.detail.tabCredits"), badge: releaseStaffLoaded ? releaseStaffCount : undefined, icon: <Users className="w-3.5 h-3.5" strokeWidth={1.5} /> },
    { id: "relations", label: t("entity.page.navRelations"), badge: aggregateCount === null ? undefined : aggregateCount + expressionIds.length, icon: <Network className="w-3.5 h-3.5" strokeWidth={1.5} /> },
    { id: "comments", label: t("entity.page.communityTitle"), badge: commentCount ?? undefined, icon: <MessageSquare className="w-3.5 h-3.5" strokeWidth={1.5} /> },
    { id: "revisions", label: t("release.detail.tabRevisions"), badge: revisionsState === "ready" ? revisions.length : undefined, icon: <History className="w-3.5 h-3.5" strokeWidth={1.5} /> },
  ];
  const { active: activeSection, select: selectSection } = useHashTab(sectionTabs, ARTICLE_ANCHOR_TABS);

  if (loading) {
    return (
      <div className="min-h-screen relative flex flex-col overflow-clip">
        <div className="relative z-10 min-h-screen grid place-items-center font-mono text-xs text-text-faint">{t("release.detail.loading")}</div>
      </div>
    );
  }

  if (!release) {
    return (
      <div className="min-h-screen relative flex flex-col overflow-clip">
        {!kindMismatch && (error === "not_found" || error === "invalid" || !error) ? (
          // 裸错误码（not_found / invalid_kind / invalid_id）不是给用户看的文案，
          // 统一走页面自己的"未找到"，并给出与站内 404 页相同的出口。
          <DetailNotFound title={t("common.notFoundRelease")} />
        ) : (
          <DetailUnavailable kind={error === "rate_limited" ? "rate_limited" : "unavailable"} onRetry={() => setReloadKey((n) => n + 1)} />
        )}
      </div>
    );
  }

  const attrs = release.attributes || {};
  const editionType = attrText(attrs.edition_type);
  const country = attrText(attrs.country);
  const language = attrText(attrs.language);
  const channel = attrText((attrs as Record<string, any>).distribution_channel);
  const packaging = attrText(attrs.packaging);
  const catalogNo = attrText(attrs.catalog_number);
  const barcode = attrText(attrs.barcode);
  const editionDate = attrText(attrs.edition_date);
  // 发行主体是实体引用：字符串形态存的是 id，必须解析成标题——直出 UUID 在页面上
  // 既是天书，又掩盖了"引用已失效"这件事（线上实测该 id 在 resolve 上已 404）。
  const publisher = attrs.publisher;
  const publisherId =
    typeof publisher === "string" ? publisher : attrText((publisher as any)?.id);
  const publisherName = typeof publisher === "string" ? "" : localizedText((publisher as any)?.name, locale);
  const attachments = attrList(attrs.attachments);
  const storeBonuses = attrList(attrs.store_bonuses);
  const events = attrList(attrs.events);

  const primaryWorkId = release.subjects?.find((s) => s.role === "primary")?.work_id || release.subjects?.[0]?.work_id;
  const primaryWork = (primaryWorkId && works[primaryWorkId]) || null;
  // 发行版自己没有图时借用主要收录作品的封面（反过来把作品图写进发行版 pictures 是错的：
  // 那是另一张商品的美术）。借用一律由 CoverOriginNote 写明来源。
  const cover = resolveCover(release, [{ origin: "subject_work", entity: primaryWork }]);
  // 载体与篇目的用词一律取自服务端 definitions 的骨架名称：
  // 旧的 mediaLabels 按遗留媒体类型（movie/anime/novel…）硬编码一套标签，
  // 与本项目"无 media_type 传统分类"的设计相冲，且对真实 type code（album/song/film…）
  // 基本全部落到默认分支——是只剩噪音的冗余。
  const entryKindLabel = getKindName(kinds, "track", locale, t("catalog.kind.track"));

  // 版本类别与发行批次是两个独立维度（可同时成立，如"限定版 + 初回发行"），
  // 词表名一律取自 definitions，不在代码/前端字典里另存一份枚举。
  const editionBatch = attrText(attrs.edition_batch);
  const vocabLabel = (vocab: string, code: string) =>
    code ? getTermName(dynamicDefs, vocab, code, locale) : "";
  const editionLabel = vocabLabel("edition_type", editionType);
  const editionBatchLabel = vocabLabel("edition_batch", editionBatch);
  const packagingLabel =
    packaging && dynamicDefs
      ? getTermName(dynamicDefs, "packaging", packaging, locale) !== packaging
        ? getTermName(dynamicDefs, "packaging", packaging, locale)
        : packaging
      : packaging;
  const channelLabel =
    channel && dynamicDefs
      ? getTermName(dynamicDefs, "distribution_channel", channel, locale) !== channel
        ? getTermName(dynamicDefs, "distribution_channel", channel, locale)
        : channel
      : channel;

  const visibleGroups = activeTab === "all" ? formatGroups : formatGroups.filter(([fmt]) => fmt === activeTab);
  const isBonusMedium = (medium: Entity) => {
    const role = attrText(medium.attributes?.role);
    return dynamicDefs?.vocabularies?.role?.terms?.[role]?.is_bonus === true;
  };
  const hasBonusMedia = media.some(({ medium }) => isBonusMedium(medium));
  const bonusGroups = showBonus ? visibleGroups : visibleGroups.map(([fmt, rows]) => [fmt, rows.filter((r) => !isBonusMedium(r.medium))] as [string, MediumRow[]]);
  const supplementGroups = visibleGroups.map(([fmt, rows]) => [fmt, rows.filter((r) => isBonusMedium(r.medium))] as [string, MediumRow[]]);
  const primaryGroups = (showBonus ? visibleGroups : bonusGroups).filter(([, rows]) => rows.length > 0);
  const bonusOnlyFormat =
    !showBonus &&
    activeTab !== "all" &&
    primaryGroups.length === 0 &&
    supplementGroups.some(([, rows]) => rows.length > 0);

  const releaseTitle = entityTitle(release, locale);

  // 单个载体（盘或面）的展示块：depth=0 为顶层盘，depth≥1 为嵌套的子载体（A/B 面）。
  // 子载体在其父块的轨道表之后递归渲染；特典过滤同样作用于子载体。
  const mediumBlock = (row: MediumRow, depth: number): React.ReactNode => {
    const medium = row.medium;
    const tracks = row.tracks;
    const ownFmt = attrText(medium.attributes?.format) || "unknown";
    const fmtLabel =
      dynamicDefs && ownFmt !== "unknown" ? getTermName(dynamicDefs, "format", ownFmt, locale) : "";
    const mediumTitle = entityTitle(medium, locale);
    const kids = (mediumTree.childrenOf.get(medium.id!) || []).filter(
      (r) => showBonus || !isBonusMedium(r.medium)
    );
    const ordered = orderedTracksWithDepth(tracks);
    const hasDuration = tracks.some((tr) => Number(tr.attributes?.duration) > 0 ||
      (tr.contents || []).some((c) => Number(expressions[c.expression_id]?.attributes?.duration) > 0));
    const body = (
      <>
        <div
          className={`px-3.5 sm:px-4 py-2.5 border-b border-line-subtle flex flex-col sm:flex-row sm:items-center justify-between gap-2 ${
            depth === 0 ? "bg-surfaceSubtle" : ""
          } ${depth > 0 ? "sm:pl-8" : ""}`}
        >
          <div className="flex items-center gap-2 min-w-0">
            <span className="w-6.5 h-6.5 grid place-items-center rounded-md bg-sky-500/10 border border-sky-500/20 shrink-0">
              <Layers className="w-3.5 h-3.5 text-sky-500" strokeWidth={1.5} />
            </span>
            <Link href={`/catalog/${medium.id}`} className="font-display text-sm font-bold tracking-tight text-text-strong truncate hover:text-primary">
              {depth === 0 && medium.position ? `${medium.position} · ` : ""}
              {mediumTitle}
            </Link>
            {fmtLabel && ownFmt !== "unknown" && (
              <span className="hidden sm:inline font-mono text-[11px] text-text-faint shrink-0">{fmtLabel}</span>
            )}
            {isBonusMedium(medium) && (
              <span className="px-1.5 py-0.5 rounded-sm bg-amber-500/10 text-amber-600 dark:text-warn border border-amber-500/20 font-mono text-[10px] shrink-0">
                {t("release.detail.bonusDisc")}
              </span>
            )}
          </div>
          <span className="font-mono text-[10px] text-text-muted">
            {t("release.detail.trackCount", { count: mediumTree.totalOf(row) })}
          </span>
        </div>
        {ordered.length > 0 ? (
          <div className="overflow-x-auto">
            <table className="w-full text-left text-xs min-w-[640px]">
              <thead className="bg-surfaceSubtle border-y border-line-subtle font-mono text-[10px] uppercase tracking-wider text-text-faint">
                <tr>
                  <th className="py-2 px-3.5 w-12 font-medium">{t("release.detail.tablePosition")}</th>
                  <th className="py-2 px-3.5 font-medium whitespace-nowrap">{entryKindLabel}</th>
                  {/* 中间"母版篇目"列负责吸收余量，末两列固定宽度且表头不折行：
                      表头折成三行时它们会被挤到 69px/49px（审计 2026-09-19 第 19 条：
                      同一组件在 Deltarune 页与颤栗页宽度差一倍）。表格外层已有
                      overflow-x-auto 与 min-w-[640px]，窄屏不会因此横向溢出页面。 */}
                  <th className="py-2 px-3.5 font-medium">{t("release.detail.tableMasterEntry")}</th>
                  <th className="py-2 px-3.5 font-medium whitespace-nowrap w-[9rem]">{t("release.detail.tableCredit")}</th>
                  {hasDuration && <th className="py-2 px-3.5 text-right font-medium whitespace-nowrap w-[5.5rem]">{t("release.detail.tableDuration")}</th>}
                </tr>
              </thead>
              <tbody className="divide-y divide-black/5 dark:divide-white/[0.06]">
                {ordered.map(({ track: tr, depth: trDepth }) => {
                  const contents = tr.contents || [];
                  const firstExpr = contents[0]?.expression_id;
                  const expr = firstExpr ? expressions[firstExpr] : undefined;
                  const displayTitle =
                    entityTitle(tr, locale) !== tr.title && entityTitle(tr, locale)
                      ? entityTitle(tr, locale)
                      : tr.title || (expr ? entityTitle(expr, locale) : "");
                  const overridden =
                    !!expr && !!displayTitle && displayTitle !== entityTitle(expr, locale);
                  const cross = firstExpr ? crossDurations.get(firstExpr) : undefined;
                  const trWorkId = (tr as Entity).work_id || expr?.work_id;
                  const trWork = (trWorkId && works[trWorkId]) || null;
                  const showWorkBadge = trWork && primaryWorkId && trWork.id !== primaryWorkId;
                  const dur = Number(tr.attributes?.duration);
                  return (
                    <tr key={tr.id} className="hover:bg-surfaceSubtle transition-colors duration-fast ease-soft">
                      <td className="py-2 px-3.5 font-mono text-text-faint tabular-nums whitespace-nowrap">{tr.number || tr.position}</td>
                      <td className="py-2 px-3.5 font-medium text-text-strong">
                        <div
                          className="flex flex-wrap items-center gap-1.5"
                          style={trDepth > 0 ? { paddingLeft: `${trDepth * 14}px` } : undefined}
                        >
                          {trDepth > 0 && <span className="text-text-muted font-mono text-[10px]">└</span>}
                          <Link href={`/catalog/${tr.id}`} className="hover:text-primary">{displayTitle || t("release.detail.untitledTrack")}</Link>
                          {overridden && (
                            <span className="text-amber-500 text-[10px]">[{t("release.detail.overridden")}]</span>
                          )}
                          {showWorkBadge && trWork && (
                            <Link
                              href={`/catalog/${trWork.id}`}
                              className="inline-flex items-center gap-1 px-1.5 py-0.5 rounded-sm bg-sky-500/10 text-sky-700 dark:text-info-soft border border-sky-500/20 text-[10px] hover:bg-sky-500/20 transition-colors duration-fast ease-soft font-mono"
                            >
                              <Layers className="w-2.5 h-2.5" />
                              <span className="truncate max-w-[22ch]">{entityTitle(trWork, locale)}</span>
                            </Link>
                          )}
                        </div>
                        {cross && cross.size > 1 && (
                          <div className="mt-0.5 font-mono text-[10px] font-normal text-text-faint">
                            {t("release.detail.crossMediumDuration")}:{" "}
                            {Array.from(cross.values()).map((s) => formatDuration(s)).join(" / ")}
                          </div>
                        )}
                      </td>
                      <td className="py-2 px-3.5 text-text-faint text-xs">
                        {contents.length > 0 ? (
                          <span className="inline-flex flex-col gap-1">
                            {contents.map((c, i) => {
                              const e = expressions[c.expression_id];
                              return (
                                <span key={`${tr.id}-${c.expression_id}-${i}`} className="flex flex-col gap-0.5">
                                  <Link
                                    href={`/catalog/${c.expression_id}`}
                                    className="text-text-body hover:text-primary hover:underline transition-colors duration-fast ease-soft"
                                  >
                                    {e ? entityTitle(e, locale) : c.expression_id.slice(0, 8)}
                                  </Link>
                                  <LocatorInline defs={dynamicDefs} value={c.locator} locale={locale} className="font-mono text-[10px]" />
                                  <GroupAttributeInline defs={dynamicDefs} code="inclusion_attributes" value={c.attributes} locale={locale} className="font-mono text-[10px]" />
                                  <InclusionSources sources={c.sources}/>
                                </span>
                              );
                            })}
                          </span>
                        ) : (
                          <span className="text-text-muted">—</span>
                        )}
                      </td>
                      <td className="py-2 px-3.5 text-text-faint">
                        {firstExpr && expressionCredits[firstExpr] ? (
                          <span className="text-xs text-text-body">
                            {expressionCredits[firstExpr]}
                            {attrText(tr.attributes?.isrc) && (
                              <span className="ml-1.5 font-mono text-[10px] text-text-muted">
                                {attrText(tr.attributes?.isrc)}
                              </span>
                            )}
                          </span>
                        ) : attrText(tr.attributes?.isrc) ? (
                          <span className="font-mono text-[11px]">{attrText(tr.attributes?.isrc)}</span>
                        ) : (
                          <span className="text-text-muted">—</span>
                        )}
                      </td>
                      {hasDuration && <td className="py-2 px-3.5 text-right font-mono text-text-faint tabular-nums whitespace-nowrap">
                        {formatDuration(dur > 0 ? dur : Number(expr?.attributes?.duration) || 0)}
                      </td>}
                    </tr>
                  );
                })}
              </tbody>
            </table>
          </div>
        ) : kids.length === 0 ? (
          <div className="px-3.5 py-4 font-mono text-[11px] text-text-faint">{t("release.detail.noTracks")}</div>
        ) : null}
        {kids.length > 0 && (
          <div className="border-t border-line-subtle">
            {kids.map((k) => mediumBlock(k, depth + 1))}
          </div>
        )}
      </>
    );
    // 顶层载体是卡片（圆角/描边/底色由 Card 给）；嵌套子载体（面/分册）只是分组，不带卡片外观。
    return depth === 0 ? (
      <Card key={medium.id} id={`medium-${medium.id}`} padding="none" className="overflow-hidden shadow-soft">
        {body}
      </Card>
    ) : (
      <section key={medium.id} id={`medium-${medium.id}`} className="bg-transparent">
        {body}
      </section>
    );
  };

  return (
    <div className="min-h-screen relative flex flex-col overflow-clip selection:bg-primary selection:text-white">
      <PageShell
        width="page"
        className="pb-[max(1.5rem,env(safe-area-inset-bottom))]"
        header={
        <div className="space-y-3">
        <div className="flex items-center gap-1.5 font-mono text-[11px] text-text-faint">
          {primaryWork && (
            <>
              <Link href={`/catalog/${primaryWork.id}`} className="hover:text-primary transition-colors duration-fast ease-soft inline-flex items-center gap-1">
                <ArrowLeft className="w-3 h-3" strokeWidth={1.6} />
                {entityTitle(primaryWork, locale)}
              </Link>
              <span className="text-text-muted dark:text-white/20">/</span>
            </>
          )}
          <span className="text-text-strong truncate">{releaseTitle}</span>
        </div>
          {/* 页面级 h1 归页头：与作品布局同一条左基线，不再落进卡片的左内边距；
              卡片边框因此不再包住标题，标题区直接在页面基线上。 */}
          <div className="flex flex-col sm:flex-row sm:items-start justify-between gap-3">
            <div className="space-y-1.5 min-w-0">
              <div className="flex flex-wrap items-center gap-1.5 font-mono text-[10px] tracking-wide">
                <span className="px-2 py-0.5 rounded-sm bg-primary text-white font-semibold">{t("release.detail.badge")}</span>
                {release?.review_status && (
                  <span className="px-2 py-0.5 rounded-sm bg-black/[0.04] dark:bg-white/[0.06] border border-line text-text-body uppercase font-semibold">
                    {t("catalog.review.reviewed")}
                  </span>
                )}
                <QualityBadge
                  entityId={releaseId}
                  onNavigate={() => {
                    const el = document.getElementById("release-directory");
                    if (el) el.scrollIntoView({ block: "start", behavior: "smooth" });
                  }}
                />
                {editionLabel && (
                  <span className="px-2 py-0.5 rounded-sm bg-violet-500/10 text-violet-600 dark:text-alt-soft border border-violet-500/25 font-semibold">
                    {editionLabel}
                  </span>
                )}
                {editionBatchLabel && (
                  <span className="px-2 py-0.5 rounded-sm bg-fuchsia-500/10 text-fuchsia-600 dark:text-alt-soft border border-fuchsia-500/25 font-semibold">
                    {editionBatchLabel}
                  </span>
                )}
                {country && (
                  <span className="px-2 py-0.5 rounded-sm bg-sky-500/10 text-sky-700 dark:text-info-soft border border-sky-500/20">{country}</span>
                )}
                {packagingLabel && (
                  <span className="text-text-faint">{t("release.detail.packagingLabel")}{packagingLabel}</span>
                )}
                {catalogNo && <span className="text-text-faint font-mono">{catalogNo}</span>}
                {barcode && <span className="text-text-faint">{t("release.detail.barcode", { code: barcode })}</span>}
              </div>
              <EntityIdentityHeader
                title={releaseTitle}
                translations={release.translations}
                originalLanguage={release.original_language}
              />
              <dl className="flex flex-wrap gap-x-4 gap-y-1 font-mono text-[11px] text-text-faint">
                {country && <div className="flex gap-1"><dt>{t("release.detail.countryLabel")}</dt><dd className="text-text-body">{country}</dd></div>}
                {language && <div className="flex gap-1"><dt>{t("release.detail.languageLabel")}</dt><dd className="text-text-body">{language}</dd></div>}
                {channelLabel && <div className="flex gap-1"><dt>{t("release.detail.channelLabel")}</dt><dd className="text-text-body">{channelLabel}</dd></div>}
                {editionDate && <div className="flex gap-1"><dt>{t("release.detail.dateLabel")}</dt><dd className="text-text-body">{editionDate}</dd></div>}
                {(publisherName || publisherId) && (
                <div className="flex gap-1">
                  <dt>{t("release.detail.publisherLabel")}</dt>
                  <dd className="text-text-body">
                    {publisherName || (
                      <EntityLink id={publisherId} fallback={t("release.detail.publisherUnknown")} />
                    )}
                  </dd>
                </div>
              )}
              </dl>
              {(release.subjects || []).length > 1 && (
                <div className="flex flex-wrap items-center gap-1.5 pt-1">
                  <Layers className="w-3 h-3 text-primary" strokeWidth={1.5} />
                  {(release.subjects || []).map((s) => {
                    const w = works[s.work_id];
                    if (!w) return null;
                    return (
                      <span
                        key={`${s.work_id}-${s.role}`}
                        className="inline-flex items-center gap-1.5 px-1.5 py-0.5 rounded-sm bg-black/[0.03] dark:bg-white/[0.04] border border-line text-[11px]"
                      >
                        <Link href={`/catalog/${s.work_id}`} className="text-text-body hover:text-primary">
                          {entityTitle(w, locale)}
                        </Link>
                        {/* 发行对象附加属性（definitions 声明，未声明则不显示）。 */}
                        <GroupAttributeInline defs={dynamicDefs} code="subject_attributes" value={s.attributes} locale={locale} />
                      </span>
                    );
                  })}
                </div>
              )}
            </div>
            <div className="flex flex-col items-start sm:items-end gap-2 shrink-0">
              {cover.url && (
                <div className="w-24 shrink-0">
                  <div className="w-full aspect-[3/4] rounded-md overflow-hidden border border-line">
                    <AdaptiveCardCover src={cover.url} alt={releaseTitle} fallbackIcon={<Layers className="w-6 h-6 text-text-muted" />} aspectClassName="w-full h-full" />
                  </div>
                  <CoverOriginNote
                    origin={cover.origin}
                    name={cover.from && cover.from.id !== release.id ? entityTitle(cover.from, locale) : ""}
                    className="mt-1"
                  />
                </div>
              )}
            </div>
          </div>
        </div>
        }
      >

        {/* 与作品详情页同构：左侧外部链接卡片常驻，右侧页签。没有数据的页签照常显示并写「暂无数据」。 */}
        <div className="grid gap-4 lg:grid-cols-[260px_minmax(0,1fr)] items-start">
        <aside className="min-w-0 space-y-4">
          <ExternalAuthorityLinks entity={release} category="release" variant="list" emptyText={t("entity.authority.empty")} />
        </aside>
        <div className="min-w-0 space-y-4">
        <TabBar ariaLabel={t("entity.page.sections")} active={activeSection} onSelect={selectSection} items={sectionTabs} />

        <div role="tabpanel" id="panel-overview" aria-labelledby="tab-overview" hidden={activeSection !== "overview"} className="space-y-4">

          <Card padding="section" className="space-y-4">
            <SectionTitle icon={<BookOpen className="w-4 h-4 text-primary" strokeWidth={1.5} />}>{t("entity.page.overviewTitle")}</SectionTitle>
            <EntityArticle entity={release} titleOrder={titleOrder} />
          </Card>

        {editionsError && <div className="space-y-2"><ErrorMessage error={editionsError}/><button type="button" onClick={() => setReloadKey((key) => key+1)}>{t("catalog.retry")}</button></div>}
        {blocksForEntity(dynamicDefs,"release",release.attributes).includes("editions") && siblingReleases.length > 1 && (
          <Card padding="none">
          <nav aria-label={t("release.detail.siblingVersions")} className="px-3.5 sm:px-4 py-3 space-y-2">
            <p className="font-mono text-[11px] text-text-faint">
              {t("release.detail.siblingVersions")} · {siblingReleases.length}
              {editionGroup && <> · <Link className="text-primary hover:underline" href={`/catalog/${editionGroup.id}`}>{entityTitle(editionGroup,locale)}</Link></>}
            </p>
            <div className="flex gap-1.5 overflow-x-auto pb-1 -mx-1 px-1">
              {siblingReleases.map((sib) => {
                // 兄弟版本同样按两个维度展示：限定版 + 初回发行 之类组合不能降级成一项。
                const sibEditionLabel = vocabLabel("edition_type", attrText(sib.attributes?.edition_type));
                const sibBatchLabel = vocabLabel("edition_batch", attrText(sib.attributes?.edition_batch));
                const sibCatalogNo = attrText(sib.attributes?.catalog_number);
                const active = sib.id === release.id;
                return (
                  <Link
                    key={sib.id}
                    href={`/catalog/${sib.id}`}
                    aria-current={active ? "page" : undefined}
                    className={`shrink-0 max-w-[220px] rounded-md border px-3 py-2 text-left transition-colors duration-fast ease-soft ${
                      active
                        ? "bg-primary text-white border-primary"
                        : "bg-surfaceSubtle border-line hover:border-primary/40"
                    }`}
                  >
                    <span className={`block text-xs font-semibold truncate ${active ? "" : "text-text-strong"}`}>
                      {entityTitle(sib, locale)}
                    </span>
                    <span className={`mt-0.5 block font-mono text-[10px] truncate ${active ? "text-text-strong" : "text-text-faint"}`}>
                      {[sibEditionLabel, sibBatchLabel, sibCatalogNo].filter(Boolean).join(" · ") || "—"}
                    </span>
                  </Link>
                );
              })}
            </div>
          </nav>
          </Card>
        )}

                <div className="space-y-3">
        {attachments.length > 0 && (
            <Collapsible title={t("release.detail.attachments")} count={attachments.length}>
              <RecordList
                items={attachments}
                field={dynamicDefs?.fields?.attachments}
                defs={dynamicDefs}
                locale={locale}
                fallbackLabel={(i) => t("release.detail.attachmentItem", { index: i + 1 })}
              />
            </Collapsible>
          )}
          {storeBonuses.length > 0 && (
            <Collapsible title={t("release.detail.storeBonuses")} count={storeBonuses.length}>
              <RecordList
                items={storeBonuses}
                field={dynamicDefs?.fields?.store_bonuses}
                defs={dynamicDefs}
                locale={locale}
                fallbackLabel={(i) => t("release.detail.attachmentItem", { index: i + 1 })}
              />
            </Collapsible>
          )}
          {events.length > 0 && (
            <Collapsible title={t("release.detail.releaseEvents")} count={events.length}>
              <RecordList
                items={events}
                field={dynamicDefs?.fields?.events}
                defs={dynamicDefs}
                locale={locale}
                fallbackLabel={(i) => t("release.detail.attachmentItem", { index: i + 1 })}
              />
            </Collapsible>
          )}
        </div>

        </div>

        <div id="release-directory" />
        <div role="tabpanel" id="panel-mediums" aria-labelledby="tab-mediums" hidden={activeSection !== "mediums"} className="space-y-4">

        {formatGroups.length > 1 && (
          <nav aria-label={t("release.detail.formatTabs")} className="flex gap-1.5 overflow-x-auto pb-1 -mx-4 px-4 sm:mx-0 sm:px-0 sm:flex-wrap">
            <button
              type="button"
              onClick={() => setActiveTab("all")}
              aria-pressed={activeTab === "all"}
              className={`shrink-0 h-9 max-sm:min-h-[44px] px-3 rounded-md border font-mono text-xs transition-colors duration-fast ease-soft ${
                activeTab === "all"
                  ? "bg-primary text-white border-primary"
                  : "bg-surface border-line text-text-body hover:border-primary/40 hover:text-primary"
              }`}
            >
              {t("release.detail.tabAllFormats")}
            </button>
            {formatGroups.map(([fmt, rows]) => {
              const label = dynamicDefs ? getTermName(dynamicDefs, "format", fmt, locale) : fmt;
              const trackCount = rows.reduce((n, r) => n + mediumTree.totalOf(r), 0);
              return (
                <button
                  key={fmt}
                  type="button"
                  onClick={() => setActiveTab(fmt)}
                  aria-pressed={activeTab === fmt}
                  className={`shrink-0 h-9 max-sm:min-h-[44px] px-3 rounded-md border font-mono text-xs transition-colors duration-fast ease-soft ${
                    activeTab === fmt
                      ? "bg-primary text-white border-primary"
                      : "bg-surface border-line text-text-body hover:border-primary/40 hover:text-primary"
                  }`}
                >
                  {label} · {trackCount}
                </button>
              );
            })}
          </nav>
        )}

        <div className="flex items-center justify-between gap-2">
          <p className="font-mono text-[11px] text-text-faint">{t("release.detail.mediumCount", { count: mediumTree.roots.length })}</p>
          {hasBonusMedia && <label className="inline-flex items-center gap-2 font-mono text-[11px] text-text-faint cursor-pointer select-none">
            <input
              type="checkbox"
              checked={showBonus}
              onChange={(e) => setShowBonus(e.target.checked)}
              className="w-4 h-4 rounded accent-primary cursor-pointer"
            />
            <span>{t("release.detail.showBonusDiscs")}</span>
          </label>}
        </div>

        {media.length === 0 ? (
          <Card padding="none">
            <div className="p-8 text-center font-mono text-xs text-text-faint">{t("release.detail.noMedium")}</div>
          </Card>
        ) : (
          <div className="space-y-4 sm:space-y-5">
            {bonusOnlyFormat && (
              <Card padding="card">
                <div className="flex flex-col sm:flex-row sm:items-center sm:justify-between gap-3">
                  <p className="text-sm text-text-body">{t("release.detail.bonusOnlyFormat")}</p>
                  <button
                    type="button"
                    onClick={() => setShowBonus(true)}
                    className="self-start sm:self-auto min-h-10 px-3 rounded-md border border-line bg-surface text-xs font-medium text-text-strong hover:border-primary/40 hover:text-primary transition-colors duration-fast ease-soft"
                  >
                    {t("release.detail.showBonusOnly")}
                  </button>
                </div>
              </Card>
            )}
            {primaryGroups.map(([fmt, rows]) => (
              <div key={fmt} className="space-y-4">
                {rows.map((row) => mediumBlock(row, 0))}
              </div>
            ))}
            {!showBonus && !bonusOnlyFormat && supplementGroups.some(([, rows]) => rows.length > 0) && (
              <Collapsible title={t("release.detail.bonusDiscs")} count={supplementGroups.reduce((n, [, rows]) => n + rows.length, 0)}>
                <div className="space-y-3">
                  {supplementGroups.map(([fmt, rows]) =>
                    rows.map(({ medium, tracks }) => {
                      const fmtLabel = dynamicDefs ? getTermName(dynamicDefs, "format", fmt, locale) : fmt;
                      return (
                        <Card key={medium.id} padding="card">
                          <div className="flex items-center gap-2 text-xs font-semibold text-text-strong">
                            <Layers className="w-3.5 h-3.5 text-amber-500" strokeWidth={1.5} />
                            <span className="truncate">{entityTitle(medium, locale)}</span>
                            {fmtLabel && fmt !== "unknown" && <span className="font-mono text-[10px] font-normal text-text-faint">{fmtLabel}</span>}
                            <span className="font-mono text-[10px] font-normal text-text-faint">{t("release.detail.trackCount", { count: tracks.length })}</span>
                          </div>
                          <div className="mt-2 space-y-1">
                            {tracks.slice().sort((a, b) => (a.position || 0) - (b.position || 0)).map((tr) => (
                              <div key={tr.id} className="flex items-center gap-2 text-xs">
                                <span className="font-mono text-text-faint w-8 shrink-0">{tr.number || tr.position}</span>
                                <span className="text-text-strong truncate">{entityTitle(tr, locale) || tr.title}</span>
                                <span className="ml-auto font-mono text-[11px] text-text-faint shrink-0">{formatDuration(Number(tr.attributes?.duration) || 0)}</span>
                              </div>
                            ))}
                          </div>
                          <Link href={`#medium-${medium.id}`} className="mt-2 inline-flex items-center gap-1 text-[11px] text-primary hover:underline">
                            {t("release.detail.viewBonusDisc")} <ExternalLink className="w-3 h-3" strokeWidth={1.5} />
                          </Link>
                        </Card>
                      );
                    })
                  )}
                </div>
              </Collapsible>
            )}
          </div>
        )}

                </div>

        <div role="tabpanel" id="panel-credits" aria-labelledby="tab-credits" hidden={activeSection !== "credits"} className="space-y-4">

        {(releaseStaffLoaded ? releaseStaffCount > 0 : true) && (
          <section className="space-y-3" aria-label={t("work.detail.staffAndCharacters")}>
            {releaseStaffCount > 0 && (
              <SectionTitle icon={<Users className="w-4 h-4 text-primary" strokeWidth={1.5} />}>
                {t("work.detail.staffAndCharacters")}
              </SectionTitle>
            )}
            <EntityStaffSection entityId={releaseId} onCount={handleReleaseStaffCount} />
          </section>
        )}

                {releaseStaffLoaded && releaseStaffCount === 0 && <EmptyPanel />}
        </div>

        <div role="tabpanel" id="panel-relations" aria-labelledby="tab-relations" hidden={activeSection !== "relations"} className="space-y-4">

        {expressionIds.length > 0 && (
          <Collapsible title={t("release.detail.sameRecordingTitle")} count={expressionIds.length}>
            {/* 批量加载不完整时给出可重试提示，不静默留空表：实体与收录/署名分别统计，
                任一部分未恢复都提示（旧实现只看实体失败，收录缺失时误报成功）。 */}
            {expressionLoadGaps.entities + expressionLoadGaps.details > 0 && (
              <Card padding="none" className="mx-3.5 sm:mx-4 mb-2 p-2.5 !border-amber-500/25 !bg-amber-500/10 text-[11px] text-amber-700 dark:text-warn-soft flex items-center gap-2">
                <span className="flex-1">
                  {t("release.detail.occurrencesLoadFailed", { count: expressionLoadGaps.entities + expressionLoadGaps.details })}
                  {expressionLoadGaps.entities > 0 && expressionLoadGaps.details > 0 && (
                    <span className="block mt-0.5 opacity-80">
                      {t("release.detail.occurrencesLoadGapDetail", { entities: expressionLoadGaps.entities, details: expressionLoadGaps.details })}
                    </span>
                  )}
                </span>
                <button
                  type="button"
                  onClick={() => setExpressionReloadToken((n) => n + 1)}
                  className="shrink-0 px-2 py-0.5 rounded border border-amber-500/40 hover:bg-amber-500/20"
                >
                  {t("catalog.retry")}
                </button>
              </Card>
            )}
            <div className="overflow-x-auto -mx-3.5 sm:-mx-4 px-3.5 sm:px-4">
              <table className="w-full text-left text-xs min-w-[720px]">
                <thead className="font-mono text-[10px] uppercase tracking-wider text-text-faint border-b border-line-subtle">
                  <tr>
                    <th className="py-2 pr-3 font-medium">{t("release.detail.sameRecordingExpr")}</th>
                    <th className="py-2 pr-3 font-medium">{t("release.detail.sameRecordingRelease")}</th>
                    <th className="py-2 pr-3 font-medium">{t("release.detail.sameRecordingMedium")}</th>
                    <th className="py-2 pr-3 font-medium">{t("release.detail.sameRecordingTrack")}</th>
                    <th className="py-2 text-right font-medium">{t("release.detail.tableDuration")}</th>
                  </tr>
                </thead>
                <tbody className="divide-y divide-black/5 dark:divide-white/[0.06]">
                  {expressionIds.slice((occPage - 1) * OCC_PAGE_SIZE, occPage * OCC_PAGE_SIZE).map((exprId) => {
                    const occ = occurrences[exprId] || [];
                    const sibs = expressionSiblings[exprId] || [];
                    const expr = expressions[exprId];
                    if (occ.length === 0 && sibs.length === 0) {
                      return (
                        <tr key={exprId}>
                          <td className="py-2 pr-3 text-text-strong">{expr ? entityTitle(expr, locale) : exprId.slice(0, 8)}</td>
                          <td colSpan={4} className="py-2 font-mono text-[11px] text-text-muted">{t("release.detail.sameRecordingEmpty")}</td>
                        </tr>
                      );
                    }
                    // 单个表达可能被大量发行收录：默认展示前 OCC_PAGE_SIZE 条，
                    // 可就地展开全部（不再只给一句"另有 N 条未展示"的文字而无入口）。
                    const expanded = !!expandedOcc[exprId];
                    const shown = expanded ? occ : occ.slice(0, OCC_PAGE_SIZE);
                    const hiddenOcc = occ.length - shown.length;
                    const rowSpan = shown.length + (sibs.length > 0 ? 1 : 0) + (hiddenOcc > 0 ? 1 : 0);
                    return (
                      <React.Fragment key={exprId}>
                        {shown.map((o, i) => (
                          <tr key={`${exprId}-${i}`}>
                            {i === 0 ? (
                              <td rowSpan={rowSpan} className="py-2 pr-3 text-text-strong align-top">
                                {expr ? entityTitle(expr, locale) : exprId.slice(0, 8)}
                              </td>
                            ) : null}
                            <td className="py-2 pr-3">
                              <Link href={`/catalog/${o.release_id}`} className="text-primary hover:underline">
                                {entityTitle(occurrenceEntities[o.release_id], locale)}
                              </Link>
                            </td>
                            <td className="py-2 pr-3 text-text-faint">{entityTitle(occurrenceEntities[o.medium_id], locale)}</td>
                            <td className="py-2 pr-3 font-mono text-text-faint">
                              #{occurrenceEntities[o.track_id]?.number || occurrenceEntities[o.track_id]?.position} {entityTitle(occurrenceEntities[o.track_id], locale)}
                            </td>
                            <td className="py-2 text-right font-mono text-text-faint tabular-nums">
                              {formatDuration(Number(occurrenceEntities[o.track_id]?.attributes?.duration) || 0)}
                              {/* 收录附加属性（definitions 声明，未声明则不显示）。 */}
                              <GroupAttributeInline defs={dynamicDefs} code="inclusion_attributes" value={o.attributes || undefined} locale={locale} className="ml-2 text-[10px] font-sans" />
                            </td>
                          </tr>
                        ))}
                        {/* 同篇目其它表达（加长版/另一录音）的收录单列一行，避免与自身收录混读。 */}
                        {sibs.length > 0 && (
                          <tr key={`${exprId}-sib`} className="bg-black/[0.015] bg-surfaceSubtle">
                            <td colSpan={4} className="py-1.5 pr-3 text-[11px] text-text-faint">
                              {t("release.detail.sameUnitSiblings", { count: sibs.length })}
                              {sibs.slice(0, 3).map((o, i) => (
                                <span key={`${exprId}-sib-${i}`} className="ml-2 inline-block">
                                  <Link href={`/catalog/${o.release_id}`} className="text-primary hover:underline">{entityTitle(occurrenceEntities[o.release_id], locale)}</Link>
                                </span>
                              ))}
                            </td>
                          </tr>
                        )}
                        {/* 单个表达的收录被截断时就地展开全部，不让用户以为只有这些。 */}
                        {(hiddenOcc > 0 || (expanded && occ.length > OCC_PAGE_SIZE)) && (
                          <tr key={`${exprId}-more`}>
                            <td colSpan={4} className="py-1.5 pr-3 text-[11px] text-text-muted">
                              {hiddenOcc > 0 ? t("release.detail.moreOccurrences", { count: hiddenOcc }) : null}
                              <button
                                type="button"
                                onClick={() => setExpandedOcc((prev) => ({ ...prev, [exprId]: !prev[exprId] }))}
                                className="ml-2 text-primary hover:underline"
                              >
                                {expanded ? t("release.detail.collapseOccurrences") : t("release.detail.showAllOccurrences")}
                              </button>
                            </td>
                          </tr>
                        )}
                      </React.Fragment>
                    );
                  })}
                </tbody>
              </table>
            </div>
            {/* 表达较多时分页，取代原先 slice(0, 60) 的静默截断。 */}
            {expressionIds.length > OCC_PAGE_SIZE && (() => {
              const occPages = Math.max(1, Math.ceil(expressionIds.length / OCC_PAGE_SIZE));
              return (
                <div className="px-3.5 sm:px-4 py-2.5 border-t border-line-subtle flex items-center justify-end gap-2">
                  <span className="font-mono text-[11px] text-text-faint">{t("common.pagination", { page: occPage, total: occPages })}</span>
                  <button type="button" disabled={occPage <= 1} onClick={() => setOccPage((p) => Math.max(1, p - 1))} aria-label={t("pagination.prev")} className="w-7 h-7 grid place-items-center rounded-full bg-black/[0.04] dark:bg-white/[0.06] border border-line disabled:opacity-40 hover:bg-black/[0.08] dark:hover:bg-white/[0.10]">
                    <ChevronLeft className="w-3.5 h-3.5" strokeWidth={1.6} />
                  </button>
                  <button type="button" disabled={occPage >= occPages} onClick={() => setOccPage((p) => Math.min(occPages, p + 1))} aria-label={t("pagination.next")} className="w-7 h-7 grid place-items-center rounded-full bg-black/[0.04] dark:bg-white/[0.06] border border-line disabled:opacity-40 hover:bg-black/[0.08] dark:hover:bg-white/[0.10]">
                    <ChevronRight className="w-3.5 h-3.5" strokeWidth={1.6} />
                  </button>
                </div>
              );
            })()}
          </Collapsible>
        )}

                <AggregateRelationList entityId={releaseId} onCount={setAggregateCount} />
        {aggregateCount === 0 && expressionIds.length === 0 && <EmptyPanel />}
        </div>

        <div role="tabpanel" id="panel-comments" aria-labelledby="tab-comments" hidden={activeSection !== "comments"} className="space-y-4">
          <EntityCommentsPanel entityId={releaseId} onCount={setCommentCount} />
        </div>

        <div role="tabpanel" id="panel-revisions" aria-labelledby="tab-revisions" hidden={activeSection !== "revisions"} className="space-y-4">
          {revisionsState === "error" ? (
            <p role="alert" className="text-xs text-text-muted">{t("catalog.listFailed")}</p>
          ) : revisionsState === "loading" ? (
            <p className="text-xs text-text-muted">{t("catalog.loading")}</p>
          ) : revisions.length === 0 ? (
            <EmptyPanel />
          ) : (
            <Card padding="section"><EntityRevisions revisions={revisions} currentEntity={release} /></Card>
          )}
        </div>
        </div>
        </div>

      </PageShell>
    </div>
  );
}
