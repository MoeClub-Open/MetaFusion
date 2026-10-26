"use client";

import React, { useCallback, useEffect, useState, useMemo, useRef } from "react";
import Link from "next/link";
import dynamic from "next/dynamic";
import { useSearchParams } from "next/navigation";
import { AdaptiveCover } from "@/components/common/AdaptiveCover";
import { COVER_PICTURE_INDEX, coverUrl, PICTURE_ROLE_VOCABULARY, resolveCover } from "@/lib/cover";
import { EntityCover } from "@/components/common/EntityCover";
import { PictureLightbox, type LightboxPicture } from "@/components/catalog/PictureLightbox";
import FavoriteButton from "@/components/FavoriteButton";
import { WorkFacts, entityBadges } from "@/components/work/WorkFacts";
import { EntityIdentityHeader } from "@/components/entity/EntityIdentityHeader";
import { EntityEditor } from "@/components/catalog/EntityEditor";
import { useCatalog } from "@/components/catalog/CatalogProvider";
import {
  api,
  Entity,
  fetchAllPages,
  mapLimit,
  title,
} from "@/components/catalog/api";
import { loadCatalogSection } from "@/lib/catalogSection";
import { fetchEntityRelationships, type RelationshipRecord } from "@/lib/catalogRelationships";
import { useAuth } from "@/lib/authContext";
import { useI18n } from "@/i18n/I18nProvider";
import { isDistinctOriginalTitle } from "@/lib/titles";
import { isNotFoundError, localizeCatalogError } from "@/lib/catalogErrors";
import { EntityLoadError } from "@/components/catalog/EntityLoadError";

const isInvalidIdError = (message?: string | null) => !!message && message.trim().split(":")[0].trim() === "invalid_id";
import { localizeCommunityError } from "@/lib/communityErrors";
import { copyText } from "@/lib/clipboard";
import { useTitleDisplayOrder } from "@/hooks/useTitleDisplayOrder";
import {
  GraphNode,
  GraphLink,
  FavoriteTargetType,
  fetchEntityPosts,
  fetchEntityCollections,
  createEntityComment,
} from "@/lib/api";
import type { EntityComment, EntityCollectionRef } from "@/lib/api";
import { EntityRevisions } from "./EntityRevisions";
import { QualityBadge } from "./QualityBadge";
import { EntityRelationDirectory } from "@/components/entity/EntityRelationDirectory";
import { PageShell, PageContainer } from "@/components/ui/PageShell";
import { TabPanel } from "@/components/ui/TabPanel";
import { Card, CardTitle } from "@/components/ui/Card";
import { SectionTitle } from "@/components/ui/SectionTitle";
import { StaffCharacterSection } from "@/components/entity/StaffCharacterSection";
import { buildStaffCredits, isCreditRelation, unrenderedCreditRelations } from "@/components/entity/staffCredits";
import { TabBar, useHashTab, TabItem } from "@/components/catalog/DetailTabs";
import { ExternalAuthorityLinks } from "@/components/entity/ExternalAuthorityLinks";
import { EntityResourceFiles } from "@/components/storage/EntityResourceFiles";
import { AggregateRelationList, useAggregateRelations } from "@/components/catalog/AggregateRelationList";
import { EntityActionToolbar } from "@/components/entity/EntityActionToolbar";
import { EntityArticle } from "./EntityArticle";
import { ARTICLE_ANCHOR_TABS } from "@/lib/catalogArticle";
import { EntityMergeModal } from "@/components/editor/EntityMergeModal";
import { WorkContentDirectory, hasWorkDirectoryContent, useWorkDirectoryData } from "@/components/work/WorkContentDirectory";
import { WorkReleasesSection } from "@/components/entity/WorkReleasesSection";
import { InclusionContents } from "./InclusionContents";
import { ExpressionCompositionPanel } from "./ExpressionCompositionPanel";
import { TrackContentEditor } from "./TrackContentEditor";
import { TrackDirectory } from "./TrackDirectory";
import { GroupAttributeInline, LocatorInline } from "./TemplateAttributeSections";
import { EntityLink } from "./Fields";
import { useInclusionExpressions } from "./useInclusionExpressions";
import { useDefinitions, getKindName, getRelationName, getTermName, getTagName, tagCode, resolveLocalizedName, templatesForEntity, blocksForEntity } from "@/lib/definitions";
import {
  getAuthLoginUrl,
  getForumEntityUrl,
  getForumCollectionUrl,
  getStorageEntityUrl,
  hasResourceStation,
} from "@/lib/services";
import {
  ArrowLeft,
  ArrowUpRight,
  Calendar,
  Check,
  Copy,
  Disc,
  FolderPlus,
  HardDrive,
  History,
  Layers,
  List,
  MessageSquare,
  Network,
  Send,
  Share2,
  Sparkles,
  Users,
  Tag as TagIcon,
  Sliders,
  BookOpen,
  ListTree,
} from "lucide-react";

const InteractiveRelationGraph = dynamic(
  () =>
    import("@/components/graph/InteractiveRelationGraph").then(
      (m) => m.InteractiveRelationGraph
    ),
  { ssr: false }
);

/** 关系端点的展示题名：本地化题名 → 原语言题名 → id，绝不返回空串（不出现空标题）。 */
const endTitleOf = (
  e: Entity | undefined,
  id: string,
  locale: string,
  order: string[],
): string => (e ? title(e, locale, order) || e.title || id : id);


export function EntityDetailView({ id }: { id: string }) {
  const { t, tr, locale } = useI18n();
  const titleOrder = useTitleDisplayOrder();
  // 会话与定义各只有一个来源：Provider 不再缓存这两份。同一份数据存两处的问题是
  // 两处会不同步——定义发布、会话刷新后，只有恰好挂在 Provider 下的路由才看得到变化。
  const { modules } = useCatalog();
  const { user } = useAuth();
  const { definitions: dynamicDefs, kinds, relationshipRules } = useDefinitions();
  const defs = dynamicDefs;
  // kind 展示名统一走服务端 definitions.kinds，字典只作兜底（缺键退原始码，不显示裸 key）。
  const kindLabel = useCallback(
    (kind: string) => getKindName(kinds, kind, locale, tr(`catalog.kind.${kind}`, kind)),
    [kinds, locale, tr],
  );
  // ?edit=1 直达编辑模式（works 页"编辑"跳转的目标）。useSearchParams 必须
  // 在任何早退 return 之前调用（hook 顺序），页面组件需提供 Suspense 边界。
  const searchParams = useSearchParams();

  const readKey = JSON.stringify([id, user?.id || ""]);
  const currentReadKeyRef = useRef(readKey);
  currentReadKeyRef.current = readKey;
  const loadSequenceRef = useRef(0);
  const [loadedKey, setLoadedKey] = useState("");
  const isCurrentRead = loadedKey === readKey;
  const [loadedEntity, setEntity] = useState<Entity | null>(null);
  const entity = isCurrentRead ? loadedEntity : null;
  const [motherWork, setMotherWork] = useState<Entity | null>(null);
  const [motherRelease, setMotherRelease] = useState<Entity | null>(null);
  const [motherMedium, setMotherMedium] = useState<Entity | null>(null);
  const [occurrences, setOccurrences] = useState<any[]>([]);
  // 作品发行数单独轻量拉取（不含 Medium 聚合），供页签角标在打开分节前显示。
  const [workReleaseCount, setWorkReleaseCount] = useState<number | undefined>(undefined);
  const [relations, setRelations] = useState<RelationshipRecord[]>([]);
  const [relatedEntities, setRelatedEntities] = useState<Record<string, Entity>>({});
  // 统一关系投影返回的主体 ID。
  const [relationSubjectId, setRelationSubjectId] = useState<string>("");
  const [loadedChildren, setChildren] = useState<Entity[]>([]);
  const children = isCurrentRead ? loadedChildren : [];
  const [revisions, setRevisions] = useState<any[]>([]);
  const [revisionTotal, setRevisionTotal] = useState(0);
  const [revisionOffset, setRevisionOffset] = useState(0);
  const [revisionsLoading, setRevisionsLoading] = useState(false);
  const [revisionsError, setRevisionsError] = useState(false);
  const revisionsBusyRef = useRef(false);
  const [loadedSectionFailures, setSectionFailures] = useState<Record<string, boolean>>({});
  const sectionFailures = isCurrentRead ? loadedSectionFailures : {};
  const [subjectWorks, setSubjectWorks] = useState<Entity[]>([]);
  const [communityPosts, setCommunityPosts] = useState<EntityComment[]>([]);
  // 失败与「没有评论/没有合集」必须分开：两处的空列表都各有一句文案。
  const [communityPostsFailed, setCommunityPostsFailed] = useState(false);
  const [communityCollectionsFailed, setCommunityCollectionsFailed] = useState(false);
  const [communityCollections, setCommunityCollections] = useState<EntityCollectionRef[]>([]);
  const [newCommentBody, setNewCommentBody] = useState("");
  const [submittingComment, setSubmittingComment] = useState(false);
  const [commentError, setCommentError] = useState("");

  const [readLoading, setLoading] = useState(true);
  const loading = !isCurrentRead || readLoading;
  const [readError, setError] = useState("");
  const error = isCurrentRead ? readError : "";
  const [readProjectionFailed, setProjectionFailed] = useState(false);
  const projectionFailed = isCurrentRead && readProjectionFailed;
  const [editing, setEditing] = useState(searchParams.get("edit") === "1");
  // 八种实体共用编辑与合并，公共变更记录集中在历史页签。
  const [isMergeOpen, setIsMergeOpen] = useState(false);
  // 作品内容目录：hook 必须无条件调用（早期 return 之前），非作品实体传空 id 时不发请求。
  const directoryData = useWorkDirectoryData(entity?.kind === "work" ? entity.id || id : "");
  // 聚合关系（内容结构的一部分）：在详情页层取数，标签计数和面板共用同一份数据。
  const aggregate = useAggregateRelations(id);
  const inclusionExpressions = useInclusionExpressions(
    [entity, ...children].flatMap((item) => (item?.contents || []).map((content) => content.expression_id)),
    user?.id,
  );
  const [copiedId, setCopiedId] = useState(false);
  const [copiedLink, setCopiedLink] = useState(false);
  // 复制失败的分支：null 表示没有失败。复制不是"点了没反应"的动作，失败必须可见。
  const [copyFailed, setCopyFailed] = useState<"id" | "link" | null>(null);
  const [relationViewMode, setRelationViewMode] = useState<"cards" | "graph">("cards");
  // 画廊灯箱打开在第几张（null=关闭）。索引即 pictures 的数组下标：顺序就是作者定的
  // 展示顺序，灯箱翻图也只按这个顺序走，不再二次排序。
  const [lightboxIndex, setLightboxIndex] = useState<number | null>(null);
  // Modal 的副作用依赖 onClose 的身份：给它稳定引用，否则每次翻图都会重挂副作用并抢焦点。
  const closeLightbox = useCallback(() => setLightboxIndex(null), []);

  const loadOlderRevisions = async () => {
    if (!entity?.id || revisionsBusyRef.current || revisionOffset >= revisionTotal) return;
    const key = readKey;
    const sequence = loadSequenceRef.current;
    const current = () => currentReadKeyRef.current === key && loadSequenceRef.current === sequence;
    revisionsBusyRef.current = true;
    setRevisionsLoading(true);
    setRevisionsError(false);
    try {
      const page = await api<{ items: any[]; total: number; offset: number }>(`/catalog/entities/${entity.id}/revisions?offset=${revisionOffset}`);
      if (!current()) return;
      setRevisions((items) => [...items, ...page.items.filter((item) => !items.some((previous) => previous.id === item.id))]);
      setRevisionTotal(page.total);
      setRevisionOffset(page.offset + page.items.length);
    } catch {
      if (current()) setRevisionsError(true);
    } finally {
      if (current()) {
        revisionsBusyRef.current = false;
        setRevisionsLoading(false);
      }
    }
  };

  const load = async () => {
    const key = readKey;
    const sequence = ++loadSequenceRef.current;
    const current = () => currentReadKeyRef.current === key && loadSequenceRef.current === sequence;
    setLoadedKey(key);
    setSectionFailures({});
    setLoading(true);
    setError("");
    setProjectionFailed(false);
    setEntity(null);
    setMotherWork(null);
    setMotherRelease(null);
    setMotherMedium(null);
    setSubjectWorks([]);
    setChildren([]);
    setOccurrences([]);
    setRelations([]);
    setRelatedEntities({});
    setRelationSubjectId("");
    setRevisions([]);
    setRevisionTotal(0);
    setRevisionOffset(0);
    setRevisionsLoading(false);
    setRevisionsError(false);
    revisionsBusyRef.current = false;
    setCommunityPosts([]);
    setCommunityCollections([]);
    setCommunityPostsFailed(false);
    setCommunityCollectionsFailed(false);
    try {
      const e = await api<Entity>(`/catalog/entities/${id}/resolve`);
      if (!current()) return;
      setEntity(e);

      // resolve 可能返回合并后的规范 id；回落路由参数只为类型收口（能渲染到这里的实体必有 id）。
      const communityId = e.id || id;
      // Fetch occurrences, relations, revisions, posts, collections in parallel
      // 互动服务的两条列表失败时只影响各自那一块：先记下失败，稍后连同数据一起落到 state。
      const failed: Record<string, boolean> = {};
      let postsFailed = false;
      let collectionsFailed = false;
      let metadataFailed = false;
      const [occResult, relResult, revResult, posts, collections] = await Promise.all([
        loadCatalogSection(api<{ items: any[] }>(`/catalog/entities/${e.id}/occurrences`), { items: [] }),
        loadCatalogSection(fetchEntityRelationships(e.id!), { items: [] as RelationshipRecord[], entities: {} as Record<string, Entity>, subject_id: e.id || "" }),
        loadCatalogSection(api<{ items: any[]; total: number; offset: number }>(`/catalog/entities/${e.id}/revisions`), { items: [], total: 0, offset: 0 }),
        // 互动服务没接入（或这两条端点不可用）时整块留空，不阻断条目详情渲染；
        // 端点与请求体由 lib/api/community.ts 的包装负责，本组件不再自己拼 URL。
        fetchEntityPosts(communityId).catch(() => { postsFailed = true; return [] as EntityComment[]; }),
        fetchEntityCollections(communityId).catch(() => { collectionsFailed = true; return [] as EntityCollectionRef[]; }),
      ]);
      if (!current()) return;

      const occRes = occResult.data, relRes = relResult.data, revRes = revResult.data;
      failed.occurrences = occResult.failed;
      failed.relations = relResult.failed;
      failed.revisions = revResult.failed;
      const occItems = Array.isArray(occRes.items) ? occRes.items : [];
      const relItems = Array.isArray(relRes.items) ? relRes.items : [];
      if (!Array.isArray(occRes.items) || !Array.isArray(relRes.items) || !Array.isArray(revRes.items)) metadataFailed = true;
      // 普通关系与字段引用共用已有 links 投影，按 has_more 读取完整页及端点摘要。
      // 映射覆盖每条关系的两端（含主体自身），subject_id 指出哪一端是主体；
      setRelatedEntities(relRes.entities || {});
      setRelationSubjectId(relRes.subject_id);
      const revItems = Array.isArray(revRes.items) ? revRes.items : [];
      setOccurrences(occItems);
      setRelations(relItems);
      setRevisions(revItems);
      setRevisionTotal(revRes.total);
      setRevisionOffset(revRes.offset + revItems.length);
      setCommunityPosts(posts);
      // 空数组 = 服务端确认没有；到这里仍是空且标记了失败 = 取不到——界面上不能混。
      setCommunityPostsFailed(postsFailed);
      setCommunityCollectionsFailed(collectionsFailed);
      setCommunityCollections(collections);

      // Resolve parent references
      const parentPromises: Promise<any>[] = [];
      if (e.work_id) {
        parentPromises.push(
          api<Entity>(`/catalog/entities/${e.work_id}`)
            .then((work) => { if (current()) setMotherWork(work); })
            .catch(() => { metadataFailed = true; if (current()) setMotherWork(null); })
        );
      }
      if (e.release_id) {
        parentPromises.push(
          api<Entity>(`/catalog/entities/${e.release_id}`)
            .then((release) => { if (current()) setMotherRelease(release); })
            .catch(() => { metadataFailed = true; if (current()) setMotherRelease(null); })
        );
      }
      if (e.medium_id) {
        parentPromises.push(
          api<Entity>(`/catalog/entities/${e.medium_id}`)
            .then((medium) => { if (current()) setMotherMedium(medium); })
            .catch(() => { metadataFailed = true; if (current()) setMotherMedium(null); })
        );
      }

      // If entity is a release, resolve its subject works
      if (e.kind === "release" && e.subjects && e.subjects.length > 0) {
        parentPromises.push(
          mapLimit(
            e.subjects, 8, (s) =>
              api<Entity>(`/catalog/entities/${s.work_id}`).catch(() => { metadataFailed = true; return null; })
          ).then((works) => { if (current()) setSubjectWorks(works.filter(Boolean) as Entity[]); })
        );
      }

      // If occurrences have release subjects, fetch the primary subject work for cover inheritance
      if (occItems.length > 0 && occItems[0]?.release?.subjects?.length > 0) {
        const primaryWorkId = occItems[0].release.subjects[0].work_id;
        parentPromises.push(
          api<Entity>(`/catalog/entities/${primaryWorkId}`)
            .then((w) => {
              if (current() && w) {
                setSubjectWorks((prev) => (prev.some((x) => x.id === w.id) ? prev : [...prev, w]));
              }
            })
            .catch(() => { metadataFailed = true; })
        );
      }

      // 归属与父子树是不同结构；章节、载体与轨道的直接子项都保留。
      const childQueries: string[] = [];
      if (e.kind === "work") childQueries.push(`work_id=${e.id}`);
      else if (e.kind === "content_unit") childQueries.push(`content_unit_id=${e.id}`, `kind=content_unit&parent_id=${e.id}`);
      else if (e.kind === "release") childQueries.push(`release_id=${e.id}`);
      else if (e.kind === "medium") childQueries.push(`medium_id=${e.id}`, `kind=medium&parent_id=${e.id}`);
      else if (e.kind === "track") childQueries.push(`kind=track&parent_id=${e.id}`);

      if (childQueries.length) {
        parentPromises.push(
          Promise.all(childQueries.map((query) => fetchAllPages<Entity>(`/catalog/entities?${query}`).catch(() => { metadataFailed = true; failed.children = true; return [] as Entity[]; })))
            .then((pages) => Array.from(new Map(pages.flat().map((child) => [child.id, child])).values()))
            .then(async (ch) => {
              if (!current()) return;
              if (e.kind === "release" || e.kind === "medium") {
                const tracks = await mapLimit(
                  ch.filter((x) => x.kind === "medium" && x.id),
                  8,
                  (m) => fetchAllPages<Entity>(`/catalog/entities?medium_id=${m.id}`).catch(() => { metadataFailed = true; failed.children = true; return [] as Entity[]; })
                );
                if (current()) setChildren([...ch, ...tracks.flat()]);
              } else {
                if (current()) setChildren(ch);
              }
            })
            .catch(() => { metadataFailed = true; failed.children = true; if (current()) setChildren([]); })
        );
      }

      await Promise.all(parentPromises);
      if (current()) {
        setProjectionFailed(metadataFailed);
        setSectionFailures(failed);
      }
    } catch (err) {
      if (current()) setError((err as Error).message);
    } finally {
      if (current()) setLoading(false);
    }
  };

  useEffect(() => {
    void load();
    return () => { loadSequenceRef.current += 1; };
  }, [id, user?.id]);

  // 封面派生链：本实体 → 母作品 → 发行对象作品 → 所属发行。比例一律留给 AdaptiveCover
  // （自然比例优先），这里不做按 kind 的硬编码；服务端也没有 cover_aspect 字段。
  const resolvedCover = useMemo(
    () =>
      resolveCover(entity, [
        { origin: "mother_work", entity: motherWork },
        { origin: "subject_work", entity: subjectWorks[0] },
        { origin: "release", entity: occurrences[0]?.release },
      ]),
    [entity, motherWork, subjectWorks, occurrences],
  );

  // 图片用途标签：码值来自 definitions 的 picture_role 词表，四语名由词表自己带，
  // 这里不硬编码任何语言的说法（词表缺该码时 getTermName 退成原始码，不空白）。
  const pictureRoleLabel = useCallback(
    (role?: string | null) =>
      String(role || "").trim()
        ? getTermName(defs, PICTURE_ROLE_VOCABULARY, String(role).trim(), locale)
        : "",
    [defs, locale],
  );

  // 灯箱与网格共用这一份读数：索引一致，翻图顺序就是 pictures 自己的顺序。
  const galleryPictures = useMemo<LightboxPicture[]>(
    () =>
      (entity?.pictures || []).map((p, i) => ({
        url: p.url,
        caption: resolveLocalizedName(p.caption, locale, entity?.title || ""),
        roleLabel: pictureRoleLabel(p.role),
        takenAt: String(p.taken_at || "").trim(),
        versionLabel: resolveLocalizedName(p.version_label, locale, ""),
        inUseFrom: String(p.usage_period?.begin || "").trim(),
        inUseUntil: String(p.usage_period?.end || "").trim(),
        isCover: i === COVER_PICTURE_INDEX,
        source: p.source,
      })),
    [entity, locale, pictureRoleLabel],
  );

  // Store bonuses
  const storeBonuses = useMemo(() => {
    if (!entity) return [];
    const direct = entity.attributes?.store_bonuses;
    if (Array.isArray(direct) && direct.length > 0) return direct;
    for (const occ of occurrences) {
      const b = occ.release?.attributes?.store_bonuses;
      if (Array.isArray(b) && b.length > 0) return b;
    }
    return [];
  }, [entity, occurrences]);


  // 头部徽章：主日期 + 模板 badge_fields 声明的字段（默认含平台/话数/载体格式）。
  // 取值逻辑与作品布局共用 entityBadges，字段码全部来自服务端模板声明。
  const headerBadges = useMemo(
    () =>
      entityBadges(entity, defs, locale).map((b) => ({
        icon:
          b.kind === "date" ? (
            <Calendar className="w-3 h-3 text-warn" strokeWidth={1.5} />
          ) : (
            <Disc className="w-3 h-3 text-primary" strokeWidth={1.5} />
          ),
        text: b.text,
      })),
    [entity, defs, locale],
  );

  // Graph nodes & links：节点摘要同样取自响应 entities 表，且每条关系的两端都建节点
  // （属性引用边的对端原先不在图上）；中心节点用 entities[subject_id] 的摘要，
  // 不依赖"页面上已知的实体"。缺表时才回退已知实体，仍缺则退回短 id。
  const { graphNodes, graphLinks } = useMemo(() => {
    if (!entity) return { graphNodes: [], graphLinks: [] };
    const centerId = entity.id || id;
    const nodeOf = (nid: string, level: number): GraphNode => {
      const e = relatedEntities[nid] || (nid === centerId ? entity : undefined);
      return {
        id: nid,
        name: e ? title(e, locale, titleOrder) || e.title || nid : nid.slice(0, 8),
        original_name: e ? e.title || "" : "",
        type: e?.kind || "related",
        category: e?.kind || "related",
        level,
        cover_image_url: coverUrl(e) || undefined,
      };
    };
    const nodes: GraphNode[] = [
      { ...nodeOf(centerId, 0), cover_image_url: resolvedCover.url || undefined },
    ];
    const links: GraphLink[] = [];
    const seenNodes = new Set<string>([centerId]);

    for (const r of relations) {
      for (const nid of [r.source_id, r.target_id]) {
        if (!nid || seenNodes.has(nid)) continue;
        seenNodes.add(nid);
        nodes.push(nodeOf(nid, 1));
      }
      links.push({
        source: r.source_id,
        target: r.target_id,
        type: r.type,
        label: getRelationName(defs, r.type, true, locale, relationshipRules),
      });
    }

    return { graphNodes: nodes, graphLinks: links };
  }, [entity, relations, relatedEntities, resolvedCover, locale, id, defs, titleOrder, relationshipRules]);

  const copyUuid = async () => {
    if (!entity?.id) return;
    // 走共享 helper：明文 http 或用户拒权时 navigator.clipboard 不存在，直接调它会同步抛
    // TypeError，按钮没有任何反馈（同一动作在 /invites、/developer 有 execCommand 降级）。
    const ok = await copyText(entity.id);
    setCopiedId(ok);
    setCopyFailed(ok ? null : "id");
    setTimeout(() => { setCopiedId(false); setCopyFailed(null); }, 2000);
  };

  const copyShareLink = async () => {
    if (typeof window === "undefined") return;
    const ok = await copyText(window.location.href);
    setCopiedLink(ok);
    setCopyFailed(ok ? null : "link");
    setTimeout(() => { setCopiedLink(false); setCopyFailed(null); }, 2000);
  };

  const handlePostComment = async (e: React.FormEvent) => {
    e.preventDefault();
    if (!newCommentBody.trim() || !entity?.id) return;
    if (!user) {
      window.location.href = getAuthLoginUrl(window.location.href);
      return;
    }
    setSubmittingComment(true);
    setCommentError("");
    try {
      const created = await createEntityComment(entity.id, newCommentBody.trim());
      if (created) {
        setCommunityPosts((prev) => [created, ...prev]);
      } else {
        // 2xx 但响应没带 item：回读服务端列表。不在这里自造一条评论——
        // 伪 id/伪时间会让人以为已经落库，刷新后凭空消失。
        setCommunityPosts(await fetchEntityPosts(entity.id));
      }
      setNewCommentBody("");
    } catch (err: any) {
      // 错误码 → 四语文案（communityErrors：module_error/not_found + 目录表共享码）。
      setCommentError(
        localizeCommunityError(String(err?.message || ""), t) || t("community.error.postFailed")
      );
    } finally {
      setSubmittingComment(false);
    }
  };

  // 关系分组与分节标签必须在提前 return 之前求值：loading/error/editing 分支
  // 若少调用一次 hook，就会触发 "Rendered more hooks than during the previous render"。
  // 关系两端的摘要一律取自响应 entities 表（覆盖两端 + 主体自身）：
  // 主体一侧不再依赖"页面上已知的实体"，对端也不再只认页面上猜出的那一端。
  const categorizedRelations = useMemo(
    () =>
      relations.map((r, index) => {
        const subjectId = relationSubjectId;
        const isOutgoing = subjectId === r.source_id;
        const isIncoming = subjectId === r.target_id;
        const isEndpoint = isOutgoing || isIncoming;
        // 跳转目标与对端摘要沿用旧口径（主体是终点时取 source，属性引用边同样取 source）。
        const otherId = isOutgoing ? r.target_id : r.source_id;
        // 展示用的两端：主体是端点时 = 主体 ↔ 对端；属性引用边（主体只是关系属性的取值，
        // 两端都不是主体）按边自身的方向展示 source → target，不硬把主体凑成一端。
        const fromId = isEndpoint ? subjectId : r.source_id;
        const toId = isEndpoint ? otherId : r.target_id;
        const subject = relatedEntities[subjectId];
        return {
          ...r,
          id: r.id || `${r.type}:${r.source_id}:${r.target_id}:${index}`,
          subjectId,
          subject,
          otherId,
          target: relatedEntities[otherId],
          isOutgoing,
          isIncoming,
          /** 两端摘要：均取自响应 entities 表，缺表时才回退已知实体。 */
          from: relatedEntities[fromId] || (fromId === entity?.id ? entity ?? undefined : undefined),
          to: relatedEntities[toId] || (toId === entity?.id ? entity ?? undefined : undefined),
          fromId,
          toId,
        };
      }),
    [relations, relatedEntities, entity, relationSubjectId]
  );

  // 演职区域用通用构造：标签计数与内容同源（只含署名主体，与展示行一致）。
  const staffCredits = useMemo(
    () =>
      entity?.id
        ? buildStaffCredits({
            entityId: entity.id,
            relations: relations.map((r, i) => ({
              id: r.id || `${r.type}:${r.source_id}:${r.target_id}:${i}`,
              type: r.type,
              source_id: r.source_id,
              target_id: r.target_id,
              attributes: r.attributes,
            })),
            relEntities: relatedEntities,
            defs,
            locale,
            tr,
          })
        : [],
    [entity, relations, relatedEntities, defs, locale, tr]
  );

  // 署名语义由 definitions 决定；卡片只能呈现「主体 ↔ Agent」的直接关系。
  // 角色自身指向作品、作为属性被引用的配音关系等不能因此从页面消失。
  const staffRelations = useMemo(() => {
    const cardIds = new Set(staffCredits.map((credit) => credit.id));
    return unrenderedCreditRelations(categorizedRelations, defs, cardIds);
  }, [categorizedRelations, staffCredits, defs]);

  const mediaRelations = useMemo(
    () =>
      categorizedRelations.filter(
        (r) => !isCreditRelation(defs, r.type)
      ),
    [categorizedRelations, defs]
  );

  // Direct peer agents can form an overview roster; relation codes and labels stay server-defined.
  const overviewRelations = useMemo(() => entity?.kind === "agent" ? mediaRelations.filter((row) =>
    (row.isIncoming || row.isOutgoing) && row.target?.kind === "agent" && defs?.relations?.[row.type]?.participant_slot === "peer") : [],
    [entity?.kind, mediaRelations, defs]);

  // 分类展示顺序：按实体 kind 与已有字段匹配的模板声明 relation_groups。
  const relationGroupOrder = useMemo(() => {
    const ordered: string[] = [];
    for (const tpl of templatesForEntity(defs, entity?.kind || "", entity?.attributes)) {
      for (const g of tpl?.relation_groups || []) {
        if (!ordered.includes(g)) ordered.push(g);
      }
    }
    return ordered;
  }, [defs, entity]);

  // 作品内容目录：目录形态取首个匹配模板的 directory 声明；可见性判据与作品页一致——
  // 加载中/失败时不断言"没有内容"，避免把"取不到"讲成"这个作品没有篇目"。
  const workDirectoryMode = useMemo(
    () => templatesForEntity(defs, entity?.kind || "", entity?.attributes).map((tpl) => tpl?.directory).find(Boolean),
    [defs, entity],
  );
  const directoryVisible =
    entity?.kind === "work" &&
    (directoryData.status !== "ready" ||
      hasWorkDirectoryContent(directoryData, entity.id || id, (code) => defs?.relations?.[code]?.aggregate === true));

  // 社区模块未启用时不展示该标签，避免出现永远为空的分节。
  // modules 来自 /api/capabilities：契约漂移（缺字段/不是数组）时这里不能抛错——
  // 本组件在渲染路径上，抛一次整页白屏；取不到就按"未启用"处理（分节自然消失）。
  const communityEnabled =
    Array.isArray(modules) && modules.some((m) => m.id === "community" && m.enabled);

  // 作品发行页签角标：只数 release 条数，不拉 Medium；与 WorkReleasesSection 列表同源接口。
  useEffect(() => {
    if (!entity || entity.kind !== "work") {
      setWorkReleaseCount(undefined);
      return;
    }
    const workId = entity.id || id;
    let alive = true;
    fetchAllPages<Entity>(`/catalog/entities?kind=release&work_id=${encodeURIComponent(workId)}`)
      .then((items) => {
        if (alive) setWorkReleaseCount(items.length);
      })
      .catch(() => {
        if (alive) setWorkReleaseCount(undefined);
      });
    return () => {
      alive = false;
    };
  }, [entity?.kind, entity?.id, id]);

  // 分节标签：与下方的条件渲染一一对应；标签集合随后数据到达再收窄。
  const templateBlocks = blocksForEntity(defs,entity?.kind || "",entity?.attributes);
  const tabBlocks: Record<string,string> = {staff:"credits",contents:entity?.kind === "expression" ? "composition" : "directory",releases:"occurrences",relations:"relations",resources:"resources"};
  const tabs: TabItem[] = [
    { id: "overview", label: t("entity.page.navOverview"), icon: <BookOpen className="w-3.5 h-3.5" strokeWidth={1.5} /> },
    { id: "staff", label: t("entity.page.navStaff"), badge: sectionFailures.relations ? undefined : staffCredits.length + staffRelations.length, visible: sectionFailures.relations || staffCredits.length + staffRelations.length > 0, icon: <Users className="w-3.5 h-3.5" strokeWidth={1.5} /> },
    // 作品的内容目录覆盖篇目/表达/聚合组成项，比"子实体列表"更准（子实体查询会连发行版一起带回），
    // 因此目录在场时以目录计数，且不再重复渲染子实体网格。
    { id: "contents", label: t("entity.page.navContents"), badge: entity?.kind === "expression" || sectionFailures.children ? undefined : (directoryVisible ? directoryData.items.length : children.length + (entity?.contents?.length || 0)) + aggregate.count, visible: entity?.kind === "expression" || entity?.kind === "track" || directoryVisible || sectionFailures.children || children.length > 0 || (entity?.contents?.length || 0) > 0 || aggregate.count > 0, icon: <ListTree className="w-3.5 h-3.5" strokeWidth={1.5} /> },
    { id: "releases", label: t("entity.page.navReleases"), badge: sectionFailures.occurrences ? undefined : (entity?.kind === "work" ? workReleaseCount : occurrences.length), visible: entity?.kind === "work" || sectionFailures.occurrences || occurrences.length > 0, icon: <Layers className="w-3.5 h-3.5" strokeWidth={1.5} /> },
    { id: "relations", label: t("entity.page.navRelations"), badge: sectionFailures.relations ? undefined : mediaRelations.length, visible: sectionFailures.relations || mediaRelations.length > 0, icon: <Network className="w-3.5 h-3.5" strokeWidth={1.5} /> },
    // entity 在数据到达前为 null，这里只能安全取值；分节本身的可见性由 kind 决定。
    { id: "resources", label: t("entity.detail.resourcesTitle"), visible: defs?.structure?.[String(entity?.kind || "")]?.resources === true, icon: <HardDrive className="w-3.5 h-3.5" strokeWidth={1.5} /> },
    // 评论与合集：以前渲染在页签面板外，切到哪个页签都会显示；现在是独立页签。
    { id: "community", label: t("entity.page.communityTitle"), badge: communityPosts.length, visible: communityEnabled, icon: <MessageSquare className="w-3.5 h-3.5" strokeWidth={1.5} /> },
    { id: "revisions", label: t("entity.detail.revisionsTitle"), badge: sectionFailures.revisions ? undefined : revisionTotal, icon: <History className="w-3.5 h-3.5" strokeWidth={1.5} /> },
  ].map((tab) => ({...tab,visible:tab.visible !== false && (!tabBlocks[tab.id] || templateBlocks.includes(tabBlocks[tab.id]))}))
    .sort((a,b) => {
      const rank = (id: string) => id === "overview" ? -1 : id === "revisions" ? templateBlocks.length+1 : id === "community" ? templateBlocks.length : templateBlocks.indexOf(tabBlocks[id]);
      return rank(a.id)-rank(b.id);
    });
  const { active, select } = useHashTab(tabs, ARTICLE_ANCHOR_TABS);

  // 讨论分节已不在标签栏（id="community" 现在是普通锚点）。客户端渲染下浏览器
  // 处理 hash 时元素还不存在，旧链接 #community 会停在页首；数据就绪后补一次滚动。
  useEffect(() => {
    if (loading || typeof window === "undefined") return;
    const h = decodeURIComponent(window.location.hash.replace(/^#/, ""));
    if (!h) return;
    const el = document.getElementById(h);
    if (el) el.scrollIntoView({ block: "start" });
  }, [loading, communityEnabled, children.length, occurrences.length, mediaRelations.length]);

  if (loading) {
    return (
      <div className="min-h-screen relative flex flex-col overflow-clip">
        <div className="relative z-10 min-h-[60vh] grid place-items-center font-mono text-xs text-text-muted">
          <div className="flex flex-col items-center gap-3">
            <div className="w-6 h-6 border-2 border-primary border-t-transparent rounded-full animate-spin" />
            <span>{t("entity.detail.loading")}</span>
          </div>
        </div>
      </div>
    );
  }

  if (error || !entity) {
    return (
      <div className="min-h-screen relative flex flex-col overflow-clip">
        <PageShell width="narrow" center>
          {/* 不存在 / ID 非法：不给重试；其余失败视为网络或服务故障，才给重试。 */}
          <EntityLoadError
            kind={!error || isNotFoundError(error) || isInvalidIdError(error) ? "not-found" : "network"}
            onRetry={() => void load()}
            message={error ? localizeCatalogError(error, t) : undefined}
          />
        </PageShell>
      </div>
    );
  }

  // Editing mode
  if (editing) {
    return (
      <div className="min-h-screen relative flex flex-col overflow-clip">
        <PageContainer className="sticky top-[var(--mf-header-h)] z-30 bg-surface/90 backdrop-blur-md border-b border-line py-2.5 flex items-center justify-between">
          <div className="flex items-center gap-2">
            <button
              type="button"
              onClick={() => setEditing(false)}
              className="inline-flex items-center gap-1.5 px-3 py-1.5 rounded-md border border-line text-xs font-medium text-text-body hover:bg-black/5 dark:hover:bg-white/5 transition-all cursor-pointer"
            >
              <ArrowLeft className="w-3.5 h-3.5" />
              <span>{t("entity.detail.backToDetail")}</span>
            </button>
            <span className="font-mono text-xs text-text-muted">/</span>
            <span className="font-medium text-xs text-text-strong truncate max-w-md">
              {title(entity, locale, titleOrder)}
            </span>
          </div>
          <span className="px-2 py-0.5 rounded-sm bg-primary/10 text-primary text-[10px] font-mono font-semibold uppercase">
            {t("entity.detail.editEntity")}
          </span>
        </PageContainer>
        <PageShell width="narrow">
          <EntityEditor
            initial={entity}
            initialSection={searchParams.get("section") || "basic"}
            initialLocale={searchParams.get("article_locale") || ""}
          />
        </PageShell>
      </div>
    );
  }

  const localizedTitle = title(entity, locale, titleOrder);

  const collectionRelations = categorizedRelations.filter(
    (r) => r.target && r.target.kind === "collection"
  );

  // Combine collections from relations and community collections
  const allDisplayCollections = [
    ...collectionRelations.map((r) => ({
      id: r.otherId,
      title: endTitleOf(r.target, r.otherId, locale, titleOrder),
      curator: r.target?.created_by ? t("entity.collection.communityCurator") : "MetaFusion",
    })),
    ...communityCollections.filter(
      (c) => !collectionRelations.some((r) => r.otherId === c.id)
    ),
  ];

  // Group directory elements
  const mediums = children.filter((c) => c.kind === "medium").sort((a, b) => a.position - b.position);
  const tracksByMedium: Record<string, Entity[]> = {};
  for (const m of mediums) {
    if (!m.id) continue;
    tracksByMedium[m.id] = children
      .filter((c) => c.kind === "track" && c.medium_id === m.id)
      .sort((a, b) => (a.position || 0) - (b.position || 0));
  }
  const listedMediumIds = new Set(mediums.map((medium) => medium.id));
  const directTracks = children.filter((c) => c.kind === "track" && !listedMediumIds.has(c.medium_id));
  const otherChildren = children.filter((c) => c.kind !== "medium" && c.kind !== "track")
    .sort((a, b) => a.position - b.position);

  return (
    <div className="min-h-screen relative flex flex-col overflow-clip selection:bg-primary selection:text-white">

      {/* Top Breadcrumb Navigation：作为页头交给外壳，页头到正文的间距由外壳统一给。 */}
      <PageShell
        width="page"
        className="pb-[max(3rem,env(safe-area-inset-bottom))]"
        header={
        <div className="flex flex-wrap items-center justify-between gap-2 font-mono text-xs text-text-faint border-b border-line-subtle pb-2.5">
          <div className="flex items-center gap-1.5 truncate">
            <Link href="/home" className="hover:text-primary transition-colors duration-fast ease-soft inline-flex items-center gap-1">
              <ArrowLeft className="w-3 h-3" strokeWidth={1.6} />
              <span>{t("nav.home")}</span>
            </Link>
            <span className="text-text-muted dark:text-white/20">/</span>
            <Link href="/explore" className="hover:text-primary transition-colors duration-fast ease-soft">
              {t("nav.catalog")}
            </Link>
            {motherWork && (
              <>
                <span className="text-text-muted dark:text-white/20">/</span>
                <Link
                  href={`/catalog/${motherWork.id}`}
                  className="hover:text-primary transition-colors duration-fast ease-soft truncate max-w-[200px]"
                  title={title(motherWork, locale, titleOrder)}
                >
                  {title(motherWork, locale, titleOrder)}
                </Link>
              </>
            )}
            {motherRelease && (
              <>
                <span className="text-text-muted dark:text-white/20">/</span>
                <Link
                  href={`/catalog/${motherRelease.id}`}
                  className="hover:text-primary transition-colors duration-fast ease-soft truncate max-w-[200px]"
                  title={title(motherRelease, locale, titleOrder)}
                >
                  {title(motherRelease, locale, titleOrder)}
                </Link>
              </>
            )}
            {motherMedium && (
              <>
                <span className="text-text-muted dark:text-white/20">/</span>
                <Link href={`/catalog/${motherMedium.id}`} className="hover:text-primary truncate max-w-[200px]">
                  {title(motherMedium, locale, titleOrder)}
                </Link>
              </>
            )}
            <span className="text-text-muted dark:text-white/20">/</span>
            <span className="text-text-strong font-semibold truncate max-w-[280px]">
              {localizedTitle}
            </span>
          </div>

          <div className="flex items-center gap-2 shrink-0">
            <button
              type="button"
              onClick={copyUuid}
              className="inline-flex items-center gap-1 px-2.5 py-1 rounded border border-line bg-surfaceSubtle hover:border-primary/50 text-text-faint hover:text-gray-900 dark:hover:text-white transition-all text-xs font-mono cursor-pointer"
              title={entity.id}
            >
              {copiedId ? <Check className="w-3 h-3 text-emerald-500" /> : <Copy className="w-3 h-3" />}
              <span>{copiedId ? t("entity.detail.idCopied") : t("entity.detail.copyId")}</span>
            </button>
            {copyFailed === "id" && (
              <span className="text-[11px] font-mono text-amber-600 dark:text-warn">{t("common.copyFailed")}</span>
            )}

          </div>
        </div>

        }
      >
        {projectionFailed && (
          <div role="alert" className="flex flex-wrap items-center gap-2 rounded-lg border border-amber-500/25 bg-amber-500/[0.07] p-3 text-xs text-amber-700 dark:text-warn-soft">
            <span>{t("catalog.loadFailed")}</span>
            <button type="button" onClick={() => void load()} className="text-primary hover:underline cursor-pointer">{t("catalog.retry")}</button>
          </div>
        )}
        {/* 页面级标题区：跨两栏放在封面列之上，左边界落在外壳内容基线上
            （与 /works、/releases、/mediums 等同一条左边线）；两栏结构保留在标题之下。 */}
        <header className="space-y-3">
          <div className="flex flex-wrap items-center gap-2">
            <span className="px-2.5 py-0.5 rounded-md bg-primary/10 text-primary border border-primary/20 text-xs font-mono font-bold tracking-wider">
              {kindLabel(entity.kind)}
            </span>

            {/* 头部徽章：主日期与载体格式的字段码由模板声明，不写死 edition_date/format */}
            {headerBadges.map((b, i) => (
              <span
                key={i}
                className="inline-flex items-center gap-1 px-2.5 py-0.5 rounded-md bg-black/[0.04] dark:bg-white/[0.06] border border-line text-text-body font-mono text-xs"
              >
                {b.icon}
                <span>{b.text}</span>
              </span>
            ))}

            {/* 质量标签：匿名可读；接口失败整颗隐藏。a08da54c 误删后恢复。 */}
            <QualityBadge
              entityId={entity.id || id}
              onNavigate={(tabId) => {
                const shown = tabs.some((tab) => tab.id === tabId && tab.visible !== false);
                select(shown ? tabId : "overview");
              }}
            />
          </div>

          {/* 标题和各语种题名 */}
          <EntityIdentityHeader
            title={localizedTitle}
            translations={entity.translations}
            originalLanguage={entity.original_language}
          />

          {/* Action Toolbar */}
          <div className="pt-3 border-t border-line-subtle flex flex-wrap items-center justify-between gap-3">
            <div className="flex flex-wrap items-center gap-2">
              {/* 公共历史统一放在“历史变更”页签。 */}
              <EntityActionToolbar
                onEdit={() => setEditing(true)}
                onMerge={() => setIsMergeOpen(true)}
                entityTypeLabel={kindLabel(entity.kind)}
              >
                <FavoriteButton targetType={entity.kind as FavoriteTargetType} targetId={entity.id || id} />
              </EntityActionToolbar>

              {hasResourceStation() && (
                <a
                  href={getStorageEntityUrl(entity.id || id)}
                  target="_blank"
                  rel="noopener noreferrer"
                  className="inline-flex items-center gap-1.5 px-3.5 py-2 rounded-lg border border-sky-500/25 bg-sky-500/10 text-xs font-medium text-sky-600 dark:text-info hover:bg-sky-500/20 hover:border-sky-500/40 transition-all shadow-2xs cursor-pointer"
                  title={t("entity.page.openResourceDownload")}
                >
                  <HardDrive className="w-3.5 h-3.5" />
                  <span>{t("entity.page.resourceStation")}</span>
                  <ArrowUpRight className="w-3 h-3 opacity-70" />
                </a>
              )}

              <button
                type="button"
                onClick={copyShareLink}
                className="inline-flex items-center gap-1.5 px-3 py-2 rounded-lg border border-line bg-surface text-xs font-medium text-text-faint hover:text-gray-900 dark:hover:text-white transition-all shadow-2xs cursor-pointer"
                title={copiedLink ? t("entity.page.linkCopied") : t("entity.page.share")}
              >
                {copiedLink ? <Check className="w-3.5 h-3.5 text-emerald-500" /> : <Share2 className="w-3.5 h-3.5" />}
                <span>{copiedLink ? t("entity.page.linkCopied") : t("entity.page.share")}</span>
              </button>
              {copyFailed === "link" && (
                <span className="text-[11px] font-mono text-amber-600 dark:text-warn">{t("common.copyFailed")}</span>
              )}
            </div>

          </div>
        </header>

        {/* Master 2-Column Wiki Layout (Inspired by 2cd76d44) */}
        <div className="grid grid-cols-[96px_minmax(0,1fr)] sm:grid-cols-[140px_minmax(0,1fr)] md:grid-cols-[220px_minmax(0,1fr)] xl:grid-cols-[240px_minmax(0,1fr)] gap-4 lg:gap-5 items-start">
          {/* ============================================================ */}
          {/* LEFT SIDEBAR: Cover + Facts + External Authority             */}
          {/* ============================================================ */}
          {/* 手机端把资料卡排到正文后，封面与外链仍位于正文前；桌面保留完整侧栏。 */}
          <aside className="contents md:block md:min-w-0 md:space-y-3">
            {/* 1. Cover Card */}
            <Card padding="none" className="overflow-hidden self-start order-1 shadow-xs">
              {/* 详情页是单张展示：比例在允许区间内跟着图片走（见 AdaptiveCover），
                  这里给上下限兜底——窄屏侧栏也不至于把长图压成一条、或让大图撑满整屏。 */}
              <AdaptiveCover
                src={resolvedCover.url}
                alt={localizedTitle}
                title={localizedTitle}
                originalTitle={entity.title}
                id={entity.id}
                tags={Array.isArray(entity.attributes?.tags) ? (entity.attributes.tags as string[]) : undefined}
                minHeight={96}
                maxHeight="min(45vh, 320px)"
                className="w-full h-auto"
              />
            </Card>

            {/* 2. External Authority & Official Links (官网与各权威数据源同级一体化呈现) */}
            <div className="order-2 min-w-0"><ExternalAuthorityLinks
              entity={entity}
              category={entity.kind}
              variant="list"
            /></div>

            {/* 3. Basic Facts & Information Card */}
            <Card padding="section" className="order-4 col-span-2 min-w-0 shadow-xs space-y-3">
              <CardTitle icon={<Sliders className="w-4 h-4 text-primary" strokeWidth={1.5} />}>
                {t("entity.page.basicInfo")}
              </CardTitle>

              <dl className="space-y-3 text-xs">
                <div>
                  <dt className="text-text-muted font-mono text-[11px] mb-0.5">
                    {t("entity.page.entityKind")}
                  </dt>
                  <dd className="font-medium text-text-strong flex items-center gap-1.5">
                    <span className="w-2 h-2 rounded-full bg-primary" />
                    <span>{kindLabel(entity.kind)}</span>
                  </dd>
                </div>

                {(defs?.structure?.[entity.kind]?.fields || []).map((field) => {
                  const reference = (entity as Record<string, unknown>)[field.code];
                  if (typeof reference !== "string" || !reference) return null;
                  const resolved = [motherWork, motherRelease, motherMedium].find((parent) => parent?.id === reference);
                  return (
                    <div key={field.code}>
                      <dt className="text-text-muted font-mono text-[11px] mb-0.5">
                        {resolveLocalizedName(field.names, locale, field.code)}
                      </dt>
                      <dd>
                        {resolved ? <Link href={`/catalog/${reference}`} className="text-primary hover:underline">{title(resolved, locale, titleOrder)}</Link> : <EntityLink id={reference} />}
                      </dd>
                    </div>
                  );
                })}

                {/* 官方链接只在「外部链接」卡片里出现（排第一并带「官方」标记），基本信息里不再重复。 */}
              </dl>

              {/* 其余属性按实体自身模板分区渲染，与作品布局共用同一实现。
                  字段、分区、次序、类型均来自服务端声明，新增媒体类型无需改本文件。 */}
              <WorkFacts entity={entity} defs={defs} locale={locale} />

              {/* 标签是独立的自由检索词；业务类型只决定字段方案，不再在界面上展示。 */}
              {Array.isArray(entity.attributes?.tags) && entity.attributes.tags.length > 0 && (
                <div className="pt-3 border-t border-line-subtle space-y-2">
                  {Array.isArray(entity.attributes?.tags) && entity.attributes.tags.length > 0 && <>
                  <div className="flex items-center gap-1 text-[11px] font-mono text-text-muted">
                    <TagIcon className="w-3 h-3" />
                    <span>{t("work.detail.tagsHeading")}</span>
                  </div>
                  <div className="flex flex-wrap gap-1.5">
                    {(Array.isArray(entity.attributes?.tags) ? entity.attributes.tags : []).map((tag: any, idx: number) => {
                      // 显示名走 tags 词表多语言（getTagName），链接参数仍用原始 tag code：
                      // 否则 /explore?tags= 会去筛一个库里不存在的本地化名。
                      const code = tagCode(tag);
                      if (!code) return null;
                      return (
                        <Link
                          key={idx}
                          href={`/explore?tags=${encodeURIComponent(code)}`}
                          className="px-2 py-0.5 rounded bg-black/[0.04] dark:bg-white/[0.06] hover:bg-primary/10 hover:text-primary text-text-body text-[11px] transition-colors duration-fast ease-soft"
                        >
                          {getTagName(defs, code, locale)}
                        </Link>
                      );
                    })}
                  </div>
                  </>}
                </div>
              )}
            </Card>

            {/* 4. Decoupled Resource Station Quick Jump（资源站未接入时不显示） */}
            {hasResourceStation() && (
              <Card padding="card" className="order-5 col-span-2 !border-sky-500/20 !bg-sky-500/[0.04] dark:!bg-sky-500/[0.08] space-y-2.5">
                <div className="flex items-center gap-2 text-sky-600 dark:text-info font-semibold text-xs font-mono">
                  <HardDrive className="w-4 h-4" />
                  <span>{t("entity.page.resourceStation")}</span>
                </div>
                <p className="text-[11px] text-gray-600 dark:text-gray-400 leading-relaxed">
                  {t("entity.page.resourceStationDesc")}
                </p>
                <a
                  href={getStorageEntityUrl(entity.id || id)}
                  target="_blank"
                  rel="noopener noreferrer"
                  className="inline-flex items-center gap-1 text-xs font-semibold text-sky-600 dark:text-info hover:underline cursor-pointer"
                >
                  <span>{t("entity.page.openResourceStation")}</span>
                  <ArrowUpRight className="w-3.5 h-3.5" />
                </a>
              </Card>
            )}
          </aside>

          {/* ============================================================ */}
          {/* RIGHT COLUMN: Main Content Flow (Wiki Standard)              */}
          {/* ============================================================ */}
          <div className="order-3 col-span-2 md:order-none md:col-span-1 min-w-0 space-y-4">
            {Object.values(sectionFailures).some(Boolean) && (
              <div role="alert" className="rounded-lg border border-line p-4 space-y-2 text-sm">
                <p>{t("entity.detail.partialFailure", { sections: [
                  sectionFailures.occurrences && t("entity.page.navReleases"),
                  sectionFailures.relations && t("entity.page.navRelations"),
                  sectionFailures.revisions && t("entity.detail.revisionsTitle"),
                  sectionFailures.children && t("entity.page.navContents"),
                ].filter(Boolean).join(" / ") })}</p>
                <button type="button" onClick={() => void load()} className="text-primary hover:underline">{t("catalog.retry")}</button>
              </div>
            )}
            {/* 分节标签栏：每节独立面板，不再整页滚动找内容（页头已提到两栏之上）。 */}
            <TabBar
              ariaLabel={t("entity.page.sections")}
              active={active}
              onSelect={select}
              items={tabs}
            />

            {/* ============================================================ */}
            {/* Section 1: Overview & Summary                                */}
            {/* ============================================================ */}
            {/* 页签面板：重挂载 + 重放进入动画由 TabPanel 负责；页签条在本卡片内，
                间距已由卡片内边距给出，故不再额外加顶距。 */}
            <TabPanel
              activeKey={active}
              role="tabpanel"
              id={`panel-${active}`}
              labelledBy={`tab-${active}`}
              spacing="none"
              className="space-y-4"
            >
            {active === "overview" && (
            <Card id="overview" padding="section" className="space-y-4 shadow-soft">
              <SectionTitle icon={<BookOpen className="w-4 h-4 text-primary" strokeWidth={1.5} />}>
                {t("entity.page.overviewTitle")}
              </SectionTitle>

              <EntityArticle entity={entity} titleOrder={titleOrder} />

              {/* 图片画廊：**数组顺序就是作者定的展示顺序**，pictures[0] 即封面（唯一口径见 lib/cover.ts）。
                  这里过去按 taken_at 重排过——那等于前端静默覆盖作者的排序，与服务端契约直接冲突，已删；
                  taken_at 只是这张图自身的时间元信息，摆在图下作说明。点图开灯箱看大图、图注与来源。 */}
              {(entity.pictures?.length || 0) > 0 && (
                <div id="pictures" className="pt-2 space-y-3 border-t border-line-subtle">
                  <div className="flex items-center gap-2 pt-3">
                    <h3 className="font-display text-sm font-bold text-text-strong uppercase tracking-wider font-mono">
                      {t("catalog.pictureGallery")}
                    </h3>
                    <span className="px-2 py-0.5 rounded-chip bg-primary/10 text-primary font-mono text-[11px] font-semibold">
                      {entity.pictures!.length}
                    </span>
                  </div>
                  <div className="grid grid-cols-2 sm:grid-cols-3 lg:grid-cols-4 gap-3">
                    {galleryPictures.map((pic, i) => (
                      <figure key={i} className="space-y-1.5">
                        <button
                          type="button"
                          onClick={() => setLightboxIndex(i)}
                          className="group block w-full text-left rounded-lg mf-focus"
                          title={pic.source?.citation || pic.caption}
                        >
                          {/* 画廊用同一套封面组件：自托管封面取不到（如 404 object_missing）时
                              退化成程序封面，而不是让浏览器画破图图标——作品页早就这么兜底，
                              两个页面对同一份数据给出两种观感是这里此前唯一的不一致。 */}
                          <Card tone="subtle" padding="none" className="relative aspect-[3/4] overflow-hidden">
                            <EntityCover
                              src={pic.url}
                              alt={pic.caption}
                              title={pic.caption}
                              id={entity.id}
                              className="w-full h-full"
                              imgClassName="w-full h-full object-cover group-hover:scale-[1.02] transition-transform duration-fast ease-soft"
                            />
                            {/* 首张=封面是顺序事实，其余图用 picture_role 词表的用途标签；
                                没有 role 就不显示（存量 800+ 张全是未声明，必须照常展示）。 */}
                            <span className="absolute left-1.5 top-1.5 flex flex-wrap items-center gap-1">
                              {pic.isCover && (
                                <span className="px-1.5 py-0.5 rounded-md bg-black/65 dark:bg-black/75 text-emphasis keep-white border border-emphasis/20 font-mono text-[9px] font-semibold leading-none">
                                  {t("catalog.pictureCoverBadge")}
                                </span>
                              )}
                              {pic.roleLabel && (
                                <span className="px-1.5 py-0.5 rounded-md bg-black/65 dark:bg-black/75 text-emphasis keep-white border border-emphasis/20 font-mono text-[9px] leading-none max-w-[7rem] truncate">
                                  {pic.roleLabel}
                                </span>
                              )}
                            </span>
                          </Card>
                        </button>
                        <figcaption className="font-mono text-[10px] text-text-faint">
                          {pic.versionLabel || (pic.inUseFrom || pic.inUseUntil
                            ? t("catalog.picturePeriod", { from: pic.inUseFrom || "?", until: pic.inUseUntil || "?" })
                            : pic.takenAt
                              ? t("catalog.pictureTakenAtValue", { date: pic.takenAt })
                              : t("catalog.imageTimeUnknown"))}
                        </figcaption>
                      </figure>
                    ))}
                  </div>
                  <PictureLightbox
                    pictures={galleryPictures}
                    index={lightboxIndex}
                    entityId={entity.id}
                    onIndexChange={setLightboxIndex}
                    onClose={closeLightbox}
                  />
                </div>
              )}
              {/* Mother Work Direct Card */}
              {motherWork && (
                <Card tone="subtle" padding="none" className="hover:border-primary/50 transition-all mt-2">
                  <Link
                    href={`/catalog/${motherWork.id}`}
                    className="group flex items-center gap-3.5 p-3 w-full"
                  >
                    <div className="w-12 h-12 rounded-lg overflow-hidden dark:bg-white/5 shrink-0 border border-line">
                      <AdaptiveCover
                        src={coverUrl(motherWork) || resolvedCover.url}
                        alt={title(motherWork, locale, titleOrder)}
                        title={title(motherWork, locale, titleOrder)}
                        id={motherWork.id}
                        aspect="2:3"
                        className="w-full h-full object-cover"
                      />
                    </div>
                    <div className="min-w-0 flex-1">
                      <div className="font-mono text-[10px] text-primary uppercase tracking-wider font-semibold">
                        {t("entity.detail.partOfWork")}
                      </div>
                      <div className="font-display text-sm font-bold text-text-strong group-hover:text-primary truncate">
                        {title(motherWork, locale, titleOrder)}
                      </div>
                      {isDistinctOriginalTitle(motherWork.title, title(motherWork, locale, titleOrder)) && (
                        <div className="font-mono text-xs text-text-muted truncate">
                          {motherWork.title}
                        </div>
                      )}
                    </div>
                    <ArrowUpRight className="w-4 h-4 text-text-muted group-hover:text-primary transition-colors duration-fast ease-soft shrink-0" />
                  </Link>
                </Card>
              )}

              {/* Store Bonuses Highlight */}
              {storeBonuses.length > 0 && (
                <Card padding="card" className="!border-amber-500/30 !bg-amber-500/[0.05] dark:!bg-amber-500/[0.08] space-y-2">
                  <div className="flex items-center gap-1.5 font-mono text-xs font-bold text-amber-600 dark:text-warn">
                    <Sparkles className="w-4 h-4" />
                    <span>{t("entity.detail.storeBonuses")}</span>
                  </div>
                  <div className="space-y-1.5 text-xs">
                    {storeBonuses.map((bonus: any, idx: number) => (
                      <div key={idx} className="flex flex-wrap items-baseline gap-2 text-text-strong">
                        <span className="font-semibold text-amber-700 dark:text-warn-soft font-mono">
                          {resolveLocalizedName(bonus.label, locale, "") || JSON.stringify(bonus.label || "")}
                        </span>
                        {bonus.condition && (
                          <span className="text-[11px] text-text-muted">
                            ({bonus.condition})
                          </span>
                        )}
                      </div>
                    ))}
                  </div>
                </Card>
              )}
            </Card>
            )}

            {active === "overview" && overviewRelations.length > 0 && (
              <Card padding="section" className="shadow-soft">
                <EntityRelationDirectory subjectId={entity.id || id} relations={overviewRelations} entities={relatedEntities} groupOrder={relationGroupOrder} summary />
              </Card>
            )}

            {/* ============================================================ */}
            {/* Section 2: Staff & Credits (演职人员与创作者)                 */}
            {/* ============================================================ */}
            {active === "staff" && (staffCredits.length > 0 || staffRelations.length > 0) && (
              <div id="staff" className="space-y-4">
                {staffCredits.length > 0 && <StaffCharacterSection credits={staffCredits} />}
                {staffRelations.length > 0 && (
                  <Card padding="section" className="space-y-4 shadow-soft">
                    <SectionTitle icon={<Users className="w-4 h-4 text-primary" strokeWidth={1.5} />}>
                      {t("entity.page.navStaff")}
                      <span className="ml-1.5 px-2 py-0.5 rounded-chip bg-primary/10 text-primary font-mono text-[11px] font-semibold">
                        {staffRelations.length}
                      </span>
                    </SectionTitle>
                    <EntityRelationDirectory subjectId={entity.id || id} relations={staffRelations} entities={relatedEntities} groupOrder={relationGroupOrder} />
                  </Card>
                )}
              </div>
            )}

            {/* Section 3: Contents & Tracklist (内容目录与曲目结构)          */}
            {/* ============================================================ */}
            {/* 作品内容目录（篇目树 / 表达 / 聚合组成项）：作品页此前独有，现由通用视图按 kind=work 渲染。 */}
            {active === "contents" && directoryVisible && (
              <Card id="work-directory" padding="section" className="space-y-4 shadow-soft">
                <WorkContentDirectory workId={entity.id || id} data={directoryData} directory={workDirectoryMode} />
              </Card>
            )}

            {active === "contents" && entity.kind === "expression" && <Card padding="section"><ExpressionCompositionPanel expression={entity}/></Card>}
            {active === "contents" && !directoryVisible && (entity.kind === "track" || children.length > 0 || entity.contents?.length > 0) && (
              <Card id="contents" padding="section" className="space-y-4 shadow-soft">
                <SectionTitle icon={<List className="w-4 h-4 text-primary" strokeWidth={1.5} />}>
                  {t("entity.page.contentsTitle")}
                  <span className="ml-1.5 px-2 py-0.5 rounded-chip bg-primary/10 text-primary font-mono text-[11px] font-semibold">
                    {children.length + (entity.contents?.length || 0)}
                  </span>
                </SectionTitle>

                {inclusionExpressions.failedCount > 0 && (
                  <div role="alert" className="rounded-lg border border-amber-500/30 bg-amber-500/5 p-3 text-xs space-y-2">
                    <p>{t("entity.page.inclusionsLoadFailed", { count: inclusionExpressions.failedCount })}</p>
                    <button type="button" className="text-primary hover:underline" onClick={inclusionExpressions.retry}>{t("catalog.retry")}</button>
                  </div>
                )}
                <InclusionContents contents={entity.contents} expressions={inclusionExpressions.expressions} loading={inclusionExpressions.loading} />
                {entity.kind === "track" && <TrackContentEditor track={entity} workspaceId={searchParams.get("workspace") || undefined}/>}
                {directTracks.length > 0 && <TrackDirectory tracks={directTracks} expressions={inclusionExpressions.expressions} loading={inclusionExpressions.loading} titleOrder={titleOrder} />}
                {mediums.length > 0 && (
                  <div className="space-y-4">
                    {mediums.map((m) => {
                      const mTracks = (m.id ? tracksByMedium[m.id] : []) || [];
                      return (
                        <Card key={m.id} padding="none" className="overflow-hidden">
                          <div className="px-4 py-3 bg-black/[0.03] dark:bg-white/[0.04] border-b border-line flex items-center justify-between">
                            <div className="flex items-center gap-2 font-mono text-xs font-bold text-text-strong">
                              <Disc className="w-4 h-4 text-primary" />
                              <Link href={`/catalog/${m.id}`} className="hover:text-primary">{title(m, locale, titleOrder)}</Link>
                              {m.attributes?.format && (
                                <span className="px-1.5 py-0.5 rounded bg-primary/10 text-primary text-[10px] uppercase">
                                  {getTermName(defs, defs?.fields?.format?.vocabulary || "", String(m.attributes.format), locale)}
                                </span>
                              )}
                            </div>
                            <span className="font-mono text-xs text-text-muted">
                              {mTracks.length} {t("entity.page.tracksUnit")}
                            </span>
                          </div>

                          <TrackDirectory tracks={mTracks} expressions={inclusionExpressions.expressions} loading={inclusionExpressions.loading} titleOrder={titleOrder} />
                        </Card>
                      );
                    })}
                  </div>
                )}
                {otherChildren.length > 0 && (
                  <div className="grid grid-cols-1 sm:grid-cols-2 gap-3">
                    {otherChildren.map((c) => (
                      <Card key={c.id} tone="subtle" padding="none" className="hover:border-primary/50 transition-all group">
                      <Link
                        href={`/catalog/${c.id}`}
                        className="p-3 flex items-center justify-between gap-3"
                      >
                        <div className="min-w-0 flex items-center gap-2.5">
                          <span className="px-1.5 py-0.5 rounded bg-primary/10 text-primary text-[10px] font-mono uppercase font-semibold shrink-0">
                            {kindLabel(c.kind)}
                          </span>
                          <span className="font-medium text-xs sm:text-sm text-text-strong group-hover:text-primary truncate">
                            {title(c, locale, titleOrder)}
                          </span>
                        </div>
                        <ArrowUpRight className="w-3.5 h-3.5 text-text-muted group-hover:text-primary transition-colors duration-fast ease-soft shrink-0" />
                      </Link>
                      </Card>
                    ))}
                  </div>
                )}
              </Card>
            )}

            {/* ============================================================ */}
            {/* Section 4: Releases & Occurrences (发行版本与收录情况)        */}
            {/* ============================================================ */}
            {/* 作品的发行目录：列与可筛选字段由发行版模板声明（columns / facet_fields），
                以及按实际 Medium 聚合的格式汇总——这一套此前只在作品页，合并后按 kind=work 渲染。 */}
            {active === "releases" && entity.kind === "work" && (
              <Card id="releases" padding="none" className="overflow-hidden shadow-soft">
                <WorkReleasesSection workId={entity.id || id} onCountChange={setWorkReleaseCount} />
              </Card>
            )}

            {active === "releases" && entity.kind !== "work" && occurrences.length > 0 && (
              <Card id="releases" padding="section" className="space-y-4 shadow-soft">
                <SectionTitle icon={<Disc className="w-4 h-4 text-primary" strokeWidth={1.5} />}>
                  {t("entity.page.releasesTitle")}
                  <span className="ml-1.5 px-2 py-0.5 rounded-chip bg-primary/10 text-primary font-mono text-[11px] font-semibold">
                    {occurrences.length}
                  </span>
                </SectionTitle>

                <div className="overflow-x-auto">
                  <table className="w-full text-left text-xs font-mono">
                    <thead>
                      <tr className="border-b border-line text-text-muted">
                        <th className="pb-2 font-medium">{t("entity.page.colEdition")}</th>
                        <th className="pb-2 font-medium">{t("entity.page.colFormat")}</th>
                        <th className="pb-2 font-medium">{t("entity.page.colInclusionLocation")}</th>
                        <th className="pb-2 font-medium">{t("entity.page.colCatalogNo")}</th>
                        <th className="pb-2 font-medium text-right">{t("entity.page.colDate")}</th>
                      </tr>
                    </thead>
                    <tbody className="divide-y divide-black/5 dark:divide-white/[0.06]">
                      {occurrences.map((occ: any, idx: number) => {
                        const rel = occ.release;
                        if (!rel) return null;
                        const editionType = rel.attributes?.edition_type ? String(rel.attributes.edition_type) : "";
                        const isBoxset =
                          editionType === "boxset" ||
                          occ.is_compilation ||
                          rel.attributes?.packaging?.toLowerCase()?.includes("box");
                        return (
                          <tr key={rel.id || idx} className="hover:bg-surfaceSubtle transition-colors duration-fast ease-soft">
                            <td className="py-2.5 pr-3">
                              <div className="flex flex-wrap items-center gap-2">
                                <Link
                                  href={`/catalog/${rel.id}`}
                                  className="font-semibold text-text-strong hover:text-primary inline-flex items-center gap-1.5"
                                >
                                  {title(rel, locale, titleOrder)}
                                  <ArrowUpRight className="w-3.5 h-3.5 text-text-muted" />
                                </Link>
                                {isBoxset && (
                                  <span className="px-1.5 py-0.2 rounded bg-amber-500/10 text-amber-600 border border-amber-500/20 text-[10px] font-semibold">
                                    {getTermName(defs, "edition_type", "boxset", locale)}
                                  </span>
                                )}
                              </div>
                            </td>
                            <td className="py-2.5 px-3 uppercase text-text-body">
                              {occ.medium?.attributes?.format
                                ? getTermName(defs, defs?.fields?.format?.vocabulary || "", String(occ.medium.attributes.format), locale)
                                : "—"}
                            </td>
                            <td className="py-2.5 px-3 text-text-body">
                              {occ.track?.id && (
                                <Link href={`/catalog/${occ.track.id}`} className="hover:text-primary">
                                  {[occ.track.number || occ.track.position, title(occ.track, locale, titleOrder)].filter((value) => value !== "" && value != null).join(" · ")}
                                </Link>
                              )}
                              <div className="mt-1 flex flex-col gap-0.5 text-[11px]">
                                <LocatorInline defs={defs} value={occ.locator} locale={locale} />
                                <GroupAttributeInline defs={defs} code="inclusion_attributes" value={occ.attributes} locale={locale} />
                              </div>
                            </td>
                            <td className="py-2.5 px-3 text-primary font-semibold">
                              {rel.attributes?.catalog_number || "—"}
                            </td>
                            <td className="py-2.5 pl-3 text-right text-text-faint">
                              {rel.attributes?.edition_date || rel.attributes?.release_date || "—"}
                            </td>
                          </tr>
                        );
                      })}
                    </tbody>
                  </table>
                </div>
              </Card>
            )}

            {/* ============================================================ */}
            {/* Section 5: Relations & Graph (关联作品与图谱)                 */}
            {/* ============================================================ */}
            {active === "relations" && mediaRelations.length > 0 && (
              <Card id="relations" padding="section" className="space-y-4 shadow-soft">
                <SectionTitle
                  icon={<Network className="w-4 h-4 text-primary" strokeWidth={1.5} />}
                  actions={
                    <div className="flex items-center gap-1 bg-black/[0.04] dark:bg-white/[0.06] p-0.5 rounded-lg text-xs font-mono">
                    <button
                      type="button"
                      onClick={() => setRelationViewMode("cards")}
                      className={`px-2.5 py-1 rounded-md transition-all ${
                        relationViewMode === "cards"
                          ? "bg-surface text-text-strong font-semibold shadow-xs"
                          : "text-text-faint hover:text-gray-900 dark:hover:text-white"
                      }`}
                    >
                      {t("relations.directory.view")}
                    </button>
                    <button
                      type="button"
                      onClick={() => setRelationViewMode("graph")}
                      className={`px-2.5 py-1 rounded-md transition-all ${
                        relationViewMode === "graph"
                          ? "bg-surface text-text-strong font-semibold shadow-xs"
                          : "text-text-faint hover:text-gray-900 dark:hover:text-white"
                      }`}
                    >
                      {t("entity.page.viewGraph")}
                    </button>
                    </div>
                  }
                >
                  {t("entity.page.relationsTitle")}
                  <span className="ml-1.5 px-2 py-0.5 rounded-chip bg-primary/10 text-primary font-mono text-[11px] font-semibold">
                    {mediaRelations.length}
                  </span>
                </SectionTitle>

                {relationViewMode === "graph" ? (
                    <InteractiveRelationGraph
                      height="clamp(360px, 65dvh, 640px)"
                      centerEntityId={entity.id || id}
                      centerEntityType={entity.kind}
                      orientation={
                        entity.kind === "release" || entity.kind === "medium" || entity.kind === "track"
                          ? "release"
                          : entity.kind === "agent"
                          ? "agent"
                          : "work"
                      }
                      nodes={graphNodes}
                      links={graphLinks}
                      onNodeClick={(n) => {
                        if (n.id && n.id !== entity.id) {
                          window.location.href = `/catalog/${n.id}`;
                        }
                      }}
                    />
                ) : (
                  <EntityRelationDirectory subjectId={entity.id || id} relations={mediaRelations} entities={relatedEntities} groupOrder={relationGroupOrder} />
                )}
              </Card>
            )}


            {/* ============================================================ */}
            {/* Section 7: Revisions (修订历史)                             */}
            {/* ============================================================ */}
            {active === "revisions" && !sectionFailures.revisions && (
            <Card id="revisions" padding="section" className="space-y-4 shadow-soft">
              <SectionTitle icon={<History className="w-4 h-4 text-primary" strokeWidth={1.5} />}>
                {t("entity.page.revisionsHistory")}
                <span className="ml-1.5 px-2 py-0.5 rounded-chip bg-primary/10 text-primary font-mono text-[11px] font-semibold">
                  {revisionTotal}
                </span>
              </SectionTitle>

              <EntityRevisions revisions={revisions} currentEntity={entity} />
              {revisionsError && <p role="alert" className="text-sm text-rose-500">{t("catalog.loadFailed")}</p>}
              {revisionOffset < revisionTotal && (
                <button type="button" onClick={loadOlderRevisions} disabled={revisionsLoading} className="inline-flex items-center justify-center rounded-md border border-line px-3 py-2 text-sm text-text-body hover:bg-surfaceSubtle disabled:cursor-default disabled:opacity-50" aria-busy={revisionsLoading}>
                  {t(revisionsError ? "common.retry" : "versioning.olderHistory")}
                </button>
              )}
            </Card>
            )}

            {/* ============================================================ */}
            {/* Section 8: Resource files (资源文件与上传)                    */}
            {/* ============================================================ */}
            {active === "resources" && defs?.structure?.[String(entity.kind || "")]?.resources === true && (
              <EntityResourceFiles entityId={entity.id || id} />
            )}
            {/* 聚合关系属于「内容结构」：放进对应面板；模板没有内容结构页签时退到概览。 */}
            {(aggregate.count > 0 || aggregate.state === "error") && (active === "contents" || (active === "overview" && !tabs.some((x) => x.id === "contents" && x.visible !== false))) && (
              <AggregateRelationList entityId={entity.id || id} data={aggregate} />
            )}
            {active === "community" && communityEnabled && (
            <Card id="community" padding="section" className="space-y-4 shadow-soft">
              <SectionTitle
                icon={<MessageSquare className="w-4 h-4 text-primary" strokeWidth={1.5} />}
                actions={
                  <a
                    href={getForumEntityUrl(entity.id || id)}
                    target="_blank"
                    rel="noopener noreferrer"
                    className="text-xs text-primary hover:underline inline-flex items-center gap-1 font-medium cursor-pointer"
                  >
                    <span>{t("entity.page.openInForum")}</span>
                    <ArrowUpRight className="w-3.5 h-3.5" />
                  </a>
                }
              >
                {/* 标题与计数写成一句「评论 (N)」：以前是标题后紧贴一个计数徽标，读屏和复制出来都是「评论0」。 */}
                {t("entity.page.communityTitleCount", { count: communityPosts.length })}
              </SectionTitle>

              {/* Quick Comment Composer */}
              <Card tone="subtle" padding="card" className="space-y-3">
              <form onSubmit={handlePostComment} className="space-y-3">
                <div className="flex items-center justify-between text-xs text-text-faint">
                  <span className="font-medium text-text-body font-mono">
                    {t("entity.page.quickReview")}
                  </span>
                  {user ? (
                    <span className="font-mono text-[11px] text-emerald-600 dark:text-success font-semibold">
                      @{user.username}
                    </span>
                  ) : (
                    <a href={getAuthLoginUrl()} className="text-primary hover:underline font-medium">
                      {t("entity.page.signInToComment")}
                    </a>
                  )}
                </div>
                <textarea
                  value={newCommentBody}
                  onChange={(e) => setNewCommentBody(e.target.value)}
                  placeholder={
                    user
                      ? (t("entity.page.commentPlaceholderAuthed"))
                      : (t("entity.page.commentPlaceholderGuest"))
                  }
                  disabled={!user || submittingComment}
                  rows={3}
                  className="w-full p-3 rounded-lg bg-surface border border-line text-xs sm:text-sm text-text-strong placeholder:text-text-muted focus:outline-none focus:ring-2 focus:ring-primary/40 disabled:opacity-60 resize-y"
                />
                {commentError && (
                  <div className="text-xs text-rose-500 font-mono">{commentError}</div>
                )}
                <div className="flex items-center justify-between pt-1">
                  <span className="text-[11px] text-text-muted">
                    {t("entity.page.syncNotice")}
                  </span>
                  {user ? (
                    <button
                      type="submit"
                      disabled={submittingComment || !newCommentBody.trim()}
                      className="inline-flex items-center gap-1.5 px-4 py-2 rounded-lg bg-primary text-white text-xs font-semibold hover:bg-primary/90 transition-all shadow-xs disabled:opacity-50 cursor-pointer"
                    >
                      <Send className="w-3.5 h-3.5" />
                      <span>{submittingComment ? t("entity.page.posting") : t("entity.page.postComment")}</span>
                    </button>
                  ) : (
                    <a
                      href={getAuthLoginUrl()}
                      className="inline-flex items-center gap-1.5 px-4 py-2 rounded-lg bg-primary/10 text-primary text-xs font-semibold hover:bg-primary/20 transition-all duration-base ease-soft"
                    >
                      <span>{t("entity.page.sendAfterLogin")}</span>
                    </a>
                  )}
                </div>
              </form>
              </Card>

              {/* Embedded Comments Stream */}
              <div className="space-y-3">
                <h3 className="font-display text-xs font-bold text-text-strong uppercase tracking-wider font-mono">
                  {t("entity.page.discussionStream")} ({communityPosts.length})
                </h3>
                {communityPostsFailed ? (
                  <Card padding="none" className="border-dashed">
                    <div className="p-8 text-center space-y-2">
                      <p className="text-xs text-text-faint">{t("community.commentsLoadFailed")}</p>
                    </div>
                  </Card>
                ) : communityPosts.length === 0 ? (
                  <Card padding="none" className="border-dashed">
                    <div className="p-8 text-center space-y-2">
                      <MessageSquare className="w-8 h-8 mx-auto text-text-muted opacity-50" />
                      <p className="text-xs text-text-faint">
                        {t("entity.page.noDiscussions")}
                      </p>
                    </div>
                  </Card>
                ) : (
                  <div className="space-y-3">
                    {communityPosts.map((post: any) => (
                      <Card key={post.id} tone="subtle" padding="card" className="space-y-2 hover:border-black/15 dark:hover:border-white/15 transition-colors duration-fast ease-soft">
                        <div className="flex items-center justify-between text-xs">
                          <div className="flex items-center gap-2">
                            <div className="w-6 h-6 rounded-full bg-primary/15 text-primary grid place-items-center font-mono text-[11px] font-bold">
                              {(post.author_name || "U")[0].toUpperCase()}
                            </div>
                            <span className="font-semibold text-text-strong font-mono text-xs">
                              {post.author_name || "User"}
                            </span>
                          </div>
                          <span className="text-[11px] text-text-muted font-mono">
                            {post.created_at ? new Date(post.created_at).toLocaleDateString() : ""}
                          </span>
                        </div>
                        <p className="text-xs sm:text-sm text-text-strong leading-relaxed whitespace-pre-line pl-8">
                          {post.body}
                        </p>
                      </Card>
                    ))}
                  </div>
                )}
              </div>

              {/* Embedded Collections (合集) */}
              <div className="pt-4 border-t border-line-subtle space-y-3">
                <div className="flex items-center justify-between">
                  <div className="flex items-center gap-2">
                    <FolderPlus className="w-4 h-4 text-indigo-500" />
                    <h3 className="font-display text-xs font-bold text-text-strong uppercase tracking-wider font-mono">
                      {t("entity.page.collectionsFeaturing")} ({allDisplayCollections.length})
                    </h3>
                  </div>
                  <a
                    href={getForumEntityUrl(entity.id || id)}
                    target="_blank"
                    rel="noopener noreferrer"
                    className="text-xs text-indigo-600 dark:text-alt hover:underline inline-flex items-center gap-1 font-medium cursor-pointer"
                  >
                    <span>{t("entity.page.exploreCollections")}</span>
                    <ArrowUpRight className="w-3.5 h-3.5" />
                  </a>
                </div>

                {communityCollectionsFailed && allDisplayCollections.length === 0 ? (
                  <Card padding="none" className="border-dashed">
                    <div className="p-6 text-center text-xs text-text-faint">
                      {t("catalog.listFailed")}
                    </div>
                  </Card>
                ) : allDisplayCollections.length === 0 ? (
                  <Card padding="none" className="border-dashed">
                    <div className="p-6 text-center text-xs text-text-faint">
                      {t("entity.page.noCollections")}
                    </div>
                  </Card>
                ) : (
                  <div className="grid grid-cols-1 sm:grid-cols-2 gap-3">
                    {allDisplayCollections.map((col: any) => (
                      <Card key={col.id} tone="subtle" padding="none" className="hover:border-indigo-500/50 transition-all group">
                      <a
                        href={getForumCollectionUrl(col.id)}
                        className="p-3.5 flex items-start gap-3 cursor-pointer"
                      >
                        <div className="w-10 h-10 rounded-lg bg-indigo-500/10 border border-indigo-500/20 text-indigo-500 grid place-items-center shrink-0">
                          <FolderPlus className="w-5 h-5" />
                        </div>
                        <div className="min-w-0 flex-1 space-y-1">
                          <div className="font-semibold text-xs sm:text-sm text-text-strong group-hover:text-indigo-600 dark:group-hover:text-alt truncate">
                            {col.title}
                          </div>
                          <div className="text-[11px] text-text-faint font-mono truncate">
                            {t("entity.page.curatorBy")}{col.curator || "Community"}
                          </div>
                        </div>
                        <ArrowUpRight className="w-4 h-4 text-text-muted group-hover:text-indigo-500 transition-colors duration-fast ease-soft shrink-0 mt-0.5" />
                      </a>
                      </Card>
                    ))}
                  </div>
                )}
              </div>
            </Card>
            )}
            </TabPanel>


          </div>
        </div>
      </PageShell>

      <EntityMergeModal
        isOpen={isMergeOpen}
        onClose={() => setIsMergeOpen(false)}
        targetType={entity.kind}
        sourceEntity={{ ...entity, id: entity.id || id, title: entity.title || "" }}
      />
    </div>
  );
}
