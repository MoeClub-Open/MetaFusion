"use client";

import { useEffect, useState } from "react";
import { safeCount } from "@/lib/api/fields";
import { useParams, useSearchParams } from "next/navigation";
import Link from "next/link";
import { Navbar } from "@/components/Navbar";
import { UserAvatar } from "@/components/UserAvatar";
import {
  fetchFavorites,
  FavoriteItem,
  FavoriteTargetType,
  catalogEntityHref,
  toggleFavorite,
  fetchUserProfile,
  fetchUserContributions,
  fetchUserCommunityStats,
  isContributionTab,
  ContributionItem,
  ContributionStats,
  CommunityUserStats,
  PublicUserProfile,
} from "@/lib/api";
import { copyText } from "@/lib/clipboard";
import { useI18n } from "@/i18n/I18nProvider";
import { useAuth } from "@/lib/authContext";
import { getKindName, resolveKindOptions, useDefinitions } from "@/lib/definitions";
import { classifyLoadFailure, type LoadFailureKind } from "@/components/common/DetailLoadStates";
import { isoDate, isoTimestamp, localDateTime } from "@/lib/datetime";
import DirectMessageModal from "@/components/community/DirectMessageModal";
import ReportButton from "@/components/report/ReportButton";
import { DiffViewer } from "@/components/editor/DiffViewer";
import { TabPanel } from "@/components/ui/TabPanel";
import { PageShell } from "@/components/ui/PageShell";
import {
  FileText,
  Disc,
  Users,
  MessageSquare,
  History,
  Mail,
  MessageCircle,
  Heart,
  Lock,
  AlertCircle,
  Trash2,
  Settings,
  Copy,
  Check,
  ChevronDown,
  ChevronUp,
  ExternalLink,
  GitCommit,
  ShieldAlert,
  RefreshCw,
} from "lucide-react";

// 三个数据来源各自的状态：loading 只用于"还在取"，取不到一律按"未知/不可用"讲，
// 不折成 0 或空列表——那会把"来源没有响应"讲成"这个用户什么都没做"。
type SourceState = "loading" | "ok" | "error";

export default function UserDetailPage() {
  const params = useParams() as { id: string };
  const id = params.id;
  const searchParams = useSearchParams();
  const initialTab = searchParams.get("tab") || "all";

  const { user: currentUser } = useAuth();
  const viewerScope = `${id}:${currentUser?.id ?? "anonymous"}`;
  const { t, tr, locale } = useI18n();
  const { kinds } = useDefinitions();
  // 收藏筛选与条目角标的层级名：服务端 definitions.kinds 优先，服务端未给时才退回内置
  // 骨架清单，字典只作名称兜底（缺键退原始码）。
  const kindLabel = (code: string) => getKindName(kinds, code, locale, tr(`catalog.kind.${code}`, code));
  const kindOptions = resolveKindOptions(kinds);

  // 只保留真有数据源的页签：目录服务只服务 all/revisions/works/releases/artists
  // （其余取值 400 invalid_tab），收藏由互动服务承载。主题/回复/审计没有"按用户列清单"的端点，
  // 保留页签只会必然失败，因此不提供——统计数字仍在顶部如实展示（缺来源显示未知）。
  const tabs = [
    { id: "all", label: t("users.profile.tabs.all") },
    { id: "revisions", label: t("users.profile.tabs.revisions") },
    { id: "works", label: t("users.profile.tabs.works") },
    { id: "releases", label: t("users.profile.tabs.releases") },
    { id: "artists", label: t("users.profile.tabs.artists") },
    { id: "favorites", label: t("users.profile.tabs.favorites") },
  ] as const;

  const [loadedProfile, setProfile] = useState<PublicUserProfile | null>(null);
  const [profileScope, setProfileScope] = useState("");
  const [profileFailure, setProfileFailure] = useState<LoadFailureKind | "">("");
  const [contribStats, setContribStats] = useState<ContributionStats | null>(null);
  const [contribStatsError, setContribStatsError] = useState("");
  const [loadedCommunityStats, setCommunityStats] = useState<CommunityUserStats | null>(null);
  const [communityScope, setCommunityScope] = useState("");
  const [communityStatsError, setCommunityStatsError] = useState("");
  const [tab, setTab] = useState<string>(initialTab);
  const [favFilter, setFavFilter] = useState<FavoriteTargetType | "">("");
  const [items, setItems] = useState<any[]>([]);
  const [total, setTotal] = useState(0);
  const [page, setPage] = useState(1);
  const [loading, setLoading] = useState(false);
  // 失败原因按可解释的分类保存，而不是原始 message：线上实测这一页把裸码 not_found
  // 明文显示两次（横幅 + 列表），还把"服务正常返回 404"讲成"账号服务未响应"。
  const [listError, setListError] = useState<LoadFailureKind | "">("");
  const [reloadKey, setReloadKey] = useState(0);
  const [isChatOpen, setIsChatOpen] = useState(false);
  const [favVisible, setFavVisible] = useState<boolean | null>(null);
  const [favoritesScope, setFavoritesScope] = useState("");
  const [copiedId, setCopiedId] = useState(false);
  const [copyFailed, setCopyFailed] = useState(false);
  const [expandedDiffs, setExpandedDiffs] = useState<Record<string, boolean>>({});
  // 身份或主页切换的第一次渲染就屏蔽旧数据，不等 effect 清空（邮箱、收藏可能仅原请求者可见）。
  const profile = profileScope === viewerScope ? loadedProfile : null;
  const communityStats = communityScope === viewerScope ? loadedCommunityStats : null;
  const currentFavVisible = favoritesScope === viewerScope ? favVisible : null;
  const favoritesLoading = tab === "favorites" && favoritesScope !== viewerScope;

  // 账号资料（auth）：失败只影响顶部资料卡与邀请数，不影响目录贡献与互动数据。
  useEffect(() => {
    let alive = true;
    setProfile(null);
    setProfileFailure("");
    fetchUserProfile(id)
      .then((p) => {
        if (alive) {
          setProfile(p);
          setProfileScope(viewerScope);
        }
      })
      .catch((e: any) => {
        // 404 与"服务没响应"是两回事：前者说找不到这个人，后者才说暂时取不到。
        if (alive) setProfileFailure(classifyLoadFailure(e));
      });
    return () => {
      alive = false;
    };
  }, [id, viewerScope]);

  // 目录贡献计数：stats 与 tab、分页无关，page_size=1 只为拿那一份计数，避免和列表重复取 20 条。
  useEffect(() => {
    let alive = true;
    setContribStats(null);
    setContribStatsError("");
    fetchUserContributions(id, { tab: "all", page: 1, pageSize: 1 })
      .then((r) => {
        if (alive) setContribStats(r.stats ?? {});
      })
      .catch((e: any) => {
        if (alive) setContribStatsError(e?.message || "request_failed");
      });
    return () => {
      alive = false;
    };
  }, [id]);

  // 互动计数（community）：同样独立降级。
  useEffect(() => {
    let alive = true;
    setCommunityStats(null);
    setCommunityStatsError("");
    fetchUserCommunityStats(id)
      .then((s) => {
        if (alive) {
          setCommunityStats(s);
          setCommunityScope(viewerScope);
        }
      })
      .catch((e: any) => {
        if (alive) setCommunityStatsError(e?.message || "request_failed");
      });
    return () => {
      alive = false;
    };
  }, [id, viewerScope]);

  useEffect(() => {
    if (tab !== "favorites") return;
    let alive = true;
    setLoading(true);
    setListError("");
    setFavVisible(null);
    setFavoritesScope(viewerScope);
    fetchFavorites(id, { targetType: favFilter || undefined, page, pageSize: 20 })
      .then((r) => {
        if (!alive) return;
        setFavVisible(r.visible);
        setItems(Array.isArray(r.items) ? r.items : []);
        setTotal(safeCount(r.total, 0));
      })
      .catch((e: any) => {
        if (!alive) return;
        setItems([]);
        setTotal(0);
        setListError(classifyLoadFailure(e));
      })
      .finally(() => {
        if (alive) setLoading(false);
      });
    return () => {
      alive = false;
    };
  }, [id, tab, favFilter, page, reloadKey, viewerScope]);

  useEffect(() => {
    if (tab === "favorites" || !isContributionTab(tab)) return;
    let alive = true;
    setLoading(true);
    setListError("");
    fetchUserContributions(id, { tab, page, pageSize: 20 })
      .then((r) => {
        if (!alive) return;
        // 契约漂移防御：items 会在渲染路径上 .length/.map（收藏与贡献两个列表），
        // 非数组不能进 state；取不到按空列表降级，页面其余部分照常可用。
        setItems(Array.isArray(r.items) ? r.items : []);
        setTotal(r.total);
        if (r.stats) setContribStats(r.stats);
      })
      .catch((e: any) => {
        if (!alive) return;
        setItems([]);
        setTotal(0);
        setListError(classifyLoadFailure(e));
      })
      .finally(() => {
        if (alive) setLoading(false);
      });
    return () => {
      alive = false;
    };
  }, [id, tab, page, reloadKey]);

  const handleCopyId = async () => {
    // 走共享 helper 并如实报错：明文 http 下 navigator.clipboard 是 undefined，原来直接调它会
    // 同步抛 TypeError，按钮既不显示"已复制"也没有任何提示。
    const ok = await copyText(profile?.user.id || id);
    setCopiedId(ok);
    setCopyFailed(!ok);
    setTimeout(() => { setCopiedId(false); setCopyFailed(false); }, 2000);
  };

  const toggleDiff = (itemId: string) => {
    setExpandedDiffs((prev) => ({
      ...prev,
      [itemId]: !prev[itemId],
    }));
  };

  const handleRemoveFavorite = async (e: React.MouseEvent, it: FavoriteItem) => {
    e.preventDefault();
    e.stopPropagation();
    try {
      await toggleFavorite(it.target_type, it.target_id);
      setItems((prev) => prev.filter((item) => item.id !== it.id));
      setTotal((prev) => Math.max(0, prev - 1));
      setCommunityStats((prev) =>
        prev && typeof prev.favorites_count === "number"
          ? { ...prev, favorites_count: Math.max(0, prev.favorites_count - 1) }
          : prev
      );
    } catch (error: any) {
      // 取消失败不能静默：给出可读原因，收藏项保持原样（服务端没删就还在）。
      setListError(classifyLoadFailure(error));
    }
  };

  const u = profile?.user ?? null;
  // 自己是拿 URL 里的 id 判的，不依赖资料是否取到：账号服务不可用时，
  // 本人仍应能进设置页、能取消自己的收藏。
  const isMe = !!currentUser && currentUser.id === (u?.id || id);
  const favoritesPrivate = !isMe && (u?.favorites_public === false || communityStats?.favorites_count === null);

  const getRevisionActionLabel = (action?: string) => {
    switch (action) {
      case "create":
        return { label: t("editor.history.actionCreate"), color: "bg-emerald-500/10 text-emerald-600 dark:text-success border-emerald-500/20" };
      case "delete":
        return { label: t("editor.history.actionDelete"), color: "bg-rose-500/10 text-rose-600 dark:text-danger border-rose-500/20" };
      case "merge":
        return { label: t("editor.history.actionMerge"), color: "bg-purple-500/10 text-purple-600 dark:text-alt border-purple-500/20" };
      case "rollback":
        return { label: t("editor.history.actionRollback"), color: "bg-amber-500/10 text-amber-600 dark:text-warn border-amber-500/20" };
      case "cover_update":
        return { label: t("editor.history.actionCover"), color: "bg-pink-500/10 text-pink-600 dark:text-danger border-pink-500/20" };
      case "relation_update":
        return { label: t("editor.history.actionRelations"), color: "bg-indigo-500/10 text-indigo-600 dark:text-alt border-indigo-500/20" };
      case "external_links":
        return { label: t("editor.history.actionExternalIds"), color: "bg-sky-500/10 text-sky-600 dark:text-info border-sky-500/20" };
      case "release_mount":
        return { label: t("editor.history.actionReleaseMount"), color: "bg-teal-500/10 text-teal-600 dark:text-success border-teal-500/20" };
      case "update":
      default:
        return { label: t("editor.history.actionUpdate"), color: "bg-sky-500/10 text-sky-600 dark:text-info border-sky-500/20" };
    }
  };

  const accountState: SourceState = profileFailure ? "error" : profile ? "ok" : "loading";
  const catalogState: SourceState = contribStatsError ? "error" : contribStats ? "ok" : "loading";
  const communityState: SourceState = communityStatsError ? "error" : communityStats ? "ok" : "loading";

  // 计数按来源展示：来源不可用或该字段缺席时显示"未知"，绝不用 0 冒充满值。
  const statValue = (value: number | null | undefined, state: SourceState): string => {
    if (state === "loading") return "…";
    if (state === "error" || typeof value !== "number") return t("users.profile.stats.unknown");
    return String(value);
  };
  const statTiles = [
    { id: "revisions", label: t("users.profile.stats.revisions"), value: contribStats?.revisions_count, state: catalogState, icon: GitCommit },
    { id: "works", label: t("users.profile.stats.works"), value: contribStats?.works_created, state: catalogState, icon: FileText },
    { id: "releases", label: t("users.profile.stats.releases"), value: contribStats?.releases_created, state: catalogState, icon: Disc },
    { id: "artists", label: t("users.profile.stats.artists"), value: contribStats?.artists_created, state: catalogState, icon: Users },
    { id: "audits", label: t("users.profile.stats.audits"), value: contribStats?.audit_actions, state: catalogState, icon: History },
    { id: "favorites", label: t("users.profile.stats.favorites"), value: communityStats?.favorites_count, state: communityState, icon: Heart },
    { id: "topics", label: t("users.profile.stats.topics"), value: communityStats?.topics_created, state: communityState, icon: MessageSquare },
    { id: "comments", label: t("users.profile.stats.comments"), value: communityStats?.comments_created, state: communityState, icon: MessageSquare },
    { id: "invited", label: t("users.profile.stats.invited"), value: profile?.stats?.invited_count, state: accountState, icon: Users },
  ];

  const hasNext = page * 20 < total;

  return (
    <div className="min-h-screen text-text-strong flex flex-col">
      <Navbar />
      <PageShell width="narrow" spacing="none" contentClassName="space-y-4 sm:space-y-5">
        {/* User Card Header */}
        <div className="rounded-xl border border-line bg-surface p-4 sm:p-5 flex flex-col sm:flex-row gap-3.5 sm:items-center justify-between shadow-soft">
          <div className="flex gap-3.5 items-start min-w-0">
            {u ? (
              <UserAvatar user={u} size="xl" shape="rounded" ring className="shadow-md" />
            ) : (
              <div className="w-14 h-14 rounded-md border border-dashed border-line grid place-items-center text-text-muted shrink-0">
                <AlertCircle className="w-5 h-5" strokeWidth={1.6} />
              </div>
            )}
            <div className="min-w-0 flex-1 space-y-1.5">
              <div className="flex items-center gap-2 flex-wrap">
                <h1 className="text-lg font-bold text-text-strong">{u ? u.display_name || u.username : t("nav.userProfile")}</h1>
                {u?.banned && (
                  <span className="text-[11px] font-mono font-medium px-2 py-0.5 rounded-sm bg-rose-500/10 text-rose-600 dark:text-danger border border-rose-500/30 inline-flex items-center gap-1">
                    <ShieldAlert className="w-3 h-3" />
                    <span>{t("users.profile.banned")}</span>
                  </span>
                )}
              </div>
              {/* 简介：空串=未设置，整块不渲染。avatar_url / created_at 仍不在库里，保持缺席不渲染。 */}
              {u?.bio && (
                <p className="text-sm text-text-body leading-relaxed whitespace-pre-wrap break-words">{u.bio}</p>
              )}
              {profileFailure && (
                <p className="text-xs text-amber-600 dark:text-warn flex items-start gap-1.5">
                  <AlertCircle className="w-3.5 h-3.5 mt-0.5 shrink-0" />
                  <span>
                    {/* 服务明确回 404 时说"找不到这个人"，不再谎报"账号服务未响应"，
                        也不把裸错误码当正文。 */}
                    {profileFailure === "not_found" || profileFailure === "invalid"
                      ? t("users.profile.notFound")
                      : t("users.profile.accountUnavailable")}
                  </span>
                </p>
              )}
              {u?.email && (
                <div className="text-xs text-text-faint flex items-center gap-1">
                  <Mail className="w-3 h-3" />
                  <span>{u.email}</span>
                </div>
              )}
              <div className="text-[11px] font-mono text-text-muted flex items-center gap-2 flex-wrap">
                <span>
                  ID: <span className="text-text-body font-medium">{u?.id || id}</span>
                </span>
                <button
                  type="button"
                  onClick={handleCopyId}
                  title={t("users.profile.copyId")}
                  className="p-1 rounded hover:bg-black/5 dark:hover:bg-white/10 text-text-muted hover:text-gray-700 dark:hover:text-gray-200 transition-colors duration-fast ease-soft flex items-center gap-1"
                >
                  {copiedId ? <Check className="w-3 h-3 text-emerald-500" /> : <Copy className="w-3 h-3" />}
                  <span className="text-[10px]">{copiedId ? t("users.profile.copied") : t("users.profile.copyId")}</span>
                </button>
                {copyFailed && (
                  <span className="text-[10px] text-amber-600 dark:text-warn">{t("common.copyFailed")}</span>
                )}
              </div>
            </div>
          </div>

          <div className="shrink-0 flex items-center gap-2">
            {!u ? null : !isMe ? (
              <button
                type="button"
                onClick={() => {
                  if (!currentUser) {
                    window.location.href = `/login?redirect=/users/${u.id}`;
                    return;
                  }
                  setIsChatOpen(true);
                }}
                className="px-3.5 h-8 rounded-lg bg-primary hover:opacity-90 text-white keep-white text-xs font-semibold flex items-center gap-1.5 shadow-xs transition-opacity"
              >
                <MessageCircle className="w-3.5 h-3.5 stroke-[2]" />
                <span>{t("users.profile.sendMessage")}</span>
              </button>
            ) : (
              <Link
                href="/settings"
                className="px-3.5 h-8 rounded-lg bg-black/[0.04] dark:bg-white/[0.06] border border-line hover:border-primary/50 text-gray-700 dark:text-gray-200 hover:text-primary text-xs font-medium flex items-center gap-1.5 transition-colors duration-fast ease-soft shadow-2xs"
              >
                <Settings className="w-3.5 h-3.5" />
                <span>{t("users.profile.editSettings")}</span>
              </Link>
            )}
            {/* 举报他人入口：自己举报自己没有意义，isMe 时不渲染（与私信按钮同一判定）。 */}
            {u && !isMe && <ReportButton targetType="user" targetId={u.id} />}
          </div>
        </div>

        {/* Stats Grid：缺来源显示未知，未公开的收藏计数不伪装成零。 */}
        <div className="grid grid-cols-3 sm:grid-cols-5 lg:grid-cols-9 gap-2">
          {statTiles.map((it) => {
            const privateStat = it.id === "favorites" && favoritesPrivate;
            const unknown = it.state !== "ok" || typeof it.value !== "number";
            return (
              <div key={it.id} className="rounded-lg border border-line bg-surface p-2.5 text-center shadow-2xs">
                <div className="text-[10px] text-text-faint font-mono flex items-center justify-center gap-1">
                  <it.icon className="w-3 h-3" />
                  <span className="truncate">{it.label}</span>
                </div>
                <div
                  title={privateStat ? undefined : unknown ? t("users.profile.stats.unknown") : undefined}
                  className={unknown ? "text-xs font-semibold text-text-muted mt-1.5" : "text-base font-bold text-text-strong mt-0.5"}
                >
                  {privateStat ? t("users.profile.stats.private") : statValue(it.value, it.state)}
                </div>
              </div>
            );
          })}
        </div>

        {/* Tabs Bar */}
        <div className="flex gap-1 overflow-x-auto pb-1">
          {tabs.map((tabItem) => (
            <button
              key={tabItem.id}
              onClick={() => {
                setTab(tabItem.id);
                setPage(1);
              }}
              className={`px-3 h-7 rounded-md text-xs font-medium border shrink-0 transition-colors duration-fast ease-soft ${
                tab === tabItem.id
                  ? "bg-primary text-white keep-white border-primary shadow-xs"
                  : "bg-black/[0.03] dark:bg-white/[0.04] border-line text-text-body hover:text-gray-900 dark:hover:text-white"
              }`}
            >
              {tabItem.label}
            </button>
          ))}
        </div>

        {tab === "favorites" && currentFavVisible === true && !favoritesPrivate && (
          <div className="flex items-center gap-1.5 overflow-x-auto pb-0.5">
            {[
              { id: "", label: t("users.profile.favFilterAll") },
              ...(kindOptions as FavoriteTargetType[]).map((k) => ({ id: k, label: kindLabel(k) })),
            ].map((f) => (
              <button
                key={f.id}
                onClick={() => {
                  setFavFilter(f.id as FavoriteTargetType | "");
                  setPage(1);
                }}
                className={`px-2.5 h-6.5 rounded-full text-[11px] font-medium border transition-colors duration-fast ease-soft ${
                  favFilter === f.id
                    ? "bg-rose-500/10 text-rose-500 border-rose-500/30 font-semibold"
                    : "bg-surfaceSubtle border-line text-gray-600 dark:text-gray-400 hover:text-gray-900 dark:hover:text-white"
                }`}
              >
                {f.label}
              </button>
            ))}
          </div>
        )}

        {/* Content Box：key 随页签变化，切换时重放 .mf-tabpanel（与详情页/管理台同一约定） */}
        <TabPanel activeKey={tab} spacing="none" className="rounded-xl border border-line bg-surface overflow-hidden shadow-soft">
          {loading || favoritesLoading ? (
            <div className="p-8 text-center text-text-faint text-xs font-mono">{t("common.loading")}</div>
          ) : listError ? (
            <div className="p-8 text-center space-y-2">
              <AlertCircle className="w-5 h-5 text-amber-500 mx-auto" strokeWidth={1.6} />
              <div className="text-sm text-text-body font-medium">{t("users.profile.listFailed")}</div>
              <div className="text-xs text-text-faint">
                {listError === "not_found" || listError === "invalid"
                  ? t("users.profile.notFound")
                  : listError === "rate_limited"
                    ? t("catalog.rateLimited")
                    : t("users.profile.listUnavailable")}
              </div>
              <button
                type="button"
                onClick={() => setReloadKey((k) => k + 1)}
                className="mt-1 px-3 h-7 rounded-md bg-black/[0.04] dark:bg-white/[0.06] border border-line text-xs text-text-body inline-flex items-center gap-1.5 hover:text-primary"
              >
                <RefreshCw className="w-3 h-3" />
                <span>{t("common.retry")}</span>
              </button>
            </div>
          ) : tab === "favorites" && (currentFavVisible === false || favoritesPrivate) ? (
            <div className="p-5 text-center space-y-2">
              <Lock className="w-6 h-6 text-text-muted mx-auto" strokeWidth={1.5} />
              <div className="text-sm text-text-body font-medium">{t("users.profile.favoritesPrivate")}</div>
              <div className="text-xs text-text-faint font-mono">{t("users.profile.favoritesPrivateHint")}</div>
            </div>
          ) : tab === "favorites" && items.length === 0 ? (
            <div className="p-8 text-center text-text-faint text-xs font-mono">{t("users.profile.noFavorites")}</div>
          ) : tab === "favorites" ? (
            <ul className="divide-y divide-black/5 dark:divide-white/[0.06]">
              {items.map((it: FavoriteItem) => {
                const href = catalogEntityHref(it.target_id);
                const title = it.entity?.title || it.target_id;
                const typeLabel = kindLabel(it.target_type);
                return (
                  <li
                    key={it.id}
                    className="p-3 flex items-center justify-between gap-3 hover:bg-surfaceSubtle transition-colors duration-fast ease-soft group"
                  >
                    <Link href={href} className="flex items-center gap-2.5 min-w-0 flex-1">
                      <Heart className="w-3.5 h-3.5 shrink-0 text-rose-500" fill="currentColor" strokeWidth={0} />
                      <div className="min-w-0 flex-1">
                        <div className="text-xs text-text-strong font-medium truncate group-hover:text-primary transition-colors duration-fast ease-soft">
                          {title}
                        </div>
                        <div className="text-[10px] text-text-faint font-mono mt-0.5">
                          {typeLabel}
                          {it.created_at ? ` · ${isoDate(it.created_at)}` : ""}
                        </div>
                      </div>
                    </Link>
                    {isMe && (
                      <button
                        type="button"
                        onClick={(e) => handleRemoveFavorite(e, it)}
                        title={t("users.profile.unfavorite")}
                        className="opacity-0 group-hover:opacity-100 transition-opacity p-1.5 rounded hover:bg-rose-500/10 text-text-muted hover:text-rose-500"
                      >
                        <Trash2 className="w-3.5 h-3.5" />
                      </button>
                    )}
                  </li>
                );
              })}
            </ul>
          ) : items.length === 0 ? (
            <div className="p-8 text-center text-text-faint text-xs font-mono">{t("users.profile.noData")}</div>
          ) : (
            <ul className="divide-y divide-black/5 dark:divide-white/[0.06]">
              {items.map((it: ContributionItem, idx: number) => {
                const itemId = it.id || String(idx);
                const isDiffExpanded = !!expandedDiffs[itemId];
                const isRevision = it.action !== undefined;
                const actionBadge = isRevision ? getRevisionActionLabel(it.action) : null;

                // 实体链接：地址只看 id（创建项的 id 就是实体 id），kind 只用于展示标签。
                const kind = it.kind || "";
                const targetId = it.target_id || it.id;
                const entityHref = targetId ? catalogEntityHref(targetId) : "#";
                const entityDisplayName = it.title || "";

                return (
                  <li key={itemId} className="p-3.5 hover:bg-black/[0.01] dark:hover:bg-white/[0.01] transition-colors duration-fast ease-soft space-y-2">
                    <div className="flex items-start justify-between gap-3">
                      <div className="flex items-start gap-2.5 min-w-0 flex-1">
                        {it.action === "create" ? (
                          <FileText className="w-4 h-4 text-emerald-500 mt-0.5 shrink-0" />
                        ) : (
                          <GitCommit className="w-4 h-4 text-amber-500 mt-0.5 shrink-0" />
                        )}

                        <div className="min-w-0 flex-1 space-y-1">
                          <div className="flex items-center gap-2 flex-wrap text-xs">
                            {actionBadge && (
                              <span className={`px-1.5 py-0.2 rounded text-[10px] font-mono border font-medium ${actionBadge.color}`}>
                                {actionBadge.label}
                              </span>
                            )}
                            <Link href={entityHref} className="font-semibold text-text-strong hover:text-primary transition-colors duration-fast ease-soft truncate">
                              {entityDisplayName || t("users.profile.noData")}
                            </Link>
                            {kind && (
                              <span className="text-[10px] font-mono text-text-muted uppercase bg-black/[0.03] dark:bg-white/5 px-1 rounded">
                                {kindLabel(kind)}
                              </span>
                            )}
                          </div>

                          {it.edit_note && <p className="text-xs text-text-body font-sans">{it.edit_note}</p>}

                          {it.diff_summary && !it.diff && (
                            <p className="text-[11px] text-text-faint font-mono whitespace-pre-wrap">{it.diff_summary}</p>
                          )}

                          {it.sources.length > 0 && (
                            <div className="flex items-center gap-1.5 flex-wrap text-[10px] font-mono text-text-muted pt-0.5">
                              <span>{t("users.profile.sources")}:</span>
                              {it.sources.map((src, sidx) =>
                                src.url ? (
                                  <a
                                    key={sidx}
                                    href={src.url}
                                    target="_blank"
                                    rel="noopener noreferrer"
                                    title={src.citation || src.url}
                                    className="text-sky-600 dark:text-info hover:underline max-w-[220px] truncate inline-flex items-center gap-0.5"
                                  >
                                    <span>{src.citation || src.url}</span>
                                    <ExternalLink className="w-2.5 h-2.5 shrink-0" />
                                  </a>
                                ) : (
                                  <span key={sidx} className="max-w-[220px] truncate" title={src.citation}>
                                    {src.citation}
                                  </span>
                                )
                              )}
                            </div>
                          )}

                          <div className="text-[10px] text-text-faint font-mono flex items-center gap-2">
                            {/* 时间戳统一 ISO（见 lib/datetime.ts）；本地化写法留 title 供悬停查看。 */}
                            <span>
                              {it.created_at ? (
                                <time dateTime={isoTimestamp(it.created_at)} title={localDateTime(it.created_at, locale)}>
                                  {isoTimestamp(it.created_at)}
                                </time>
                              ) : (
                                ""
                              )}
                            </span>
                          </div>
                        </div>
                      </div>

                      {/* Diff Toggle Button */}
                      {it.diff && Object.keys(it.diff).length > 0 && (
                        <button
                          type="button"
                          onClick={() => toggleDiff(itemId)}
                          className="shrink-0 px-2.5 py-1 rounded-md bg-black/[0.04] dark:bg-white/[0.06] hover:bg-black/[0.08] dark:hover:bg-white/10 border border-line text-[11px] font-mono text-text-body flex items-center gap-1 transition-colors duration-fast ease-soft"
                        >
                          <span>{isDiffExpanded ? t("users.profile.hideDiff") : t("users.profile.viewDiff")}</span>
                          {isDiffExpanded ? <ChevronUp className="w-3 h-3" /> : <ChevronDown className="w-3 h-3" />}
                        </button>
                      )}
                    </div>

                    {/* Collapsible Field-by-Field Diff */}
                    {isDiffExpanded && it.diff && Object.keys(it.diff).length > 0 && (
                      <div className="pt-2 border-t border-line-subtle pl-6">
                        <DiffViewer diff={it.diff} editType={it.action} />
                      </div>
                    )}
                  </li>
                );
              })}
            </ul>
          )}
        </TabPanel>

        {/* Pagination */}
        {(tab !== "favorites" || (currentFavVisible === true && !favoritesPrivate && !favoritesLoading)) && (
        <div className="flex items-center justify-between text-xs text-text-faint">
          <span className="font-mono text-[11px]">{t("users.profile.pagination", { total, page })}</span>
          <div className="flex gap-1.5">
            <button
              disabled={page <= 1}
              onClick={() => setPage((p) => Math.max(1, p - 1))}
              className="px-2.5 h-6.5 rounded-md bg-black/[0.04] dark:bg-white/[0.06] border border-line disabled:opacity-40 hover:text-primary transition-colors duration-fast ease-soft text-xs"
            >
              {t("users.profile.prevPage")}
            </button>
            <button
              disabled={!hasNext}
              onClick={() => setPage((p) => p + 1)}
              className="px-2.5 h-6.5 rounded-md bg-primary text-white keep-white font-semibold disabled:opacity-40 hover:opacity-90 transition-opacity text-xs"
            >
              {t("users.profile.nextPage")}
            </button>
          </div>
        </div>
        )}
      </PageShell>

      {isChatOpen && u && (
        <DirectMessageModal
          peerUser={u}
          isOpen={isChatOpen}
          onClose={() => setIsChatOpen(false)}
        />
      )}
    </div>
  );
}
