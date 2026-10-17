"use client";

import React, { useEffect, useState, useRef, Suspense } from "react";
import { requireArray, safeCount } from "@/lib/api/fields";
import Link from "next/link";
import { usePathname, useRouter, useSearchParams } from "next/navigation";
import { Navbar } from "@/components/Navbar";
import { UserAvatar } from "@/components/UserAvatar";
import { fetchApi, DiscussionTopic, Tag, ForumBoard, fetchBoards, ALL_FORUM_BOARD, getBoardSync, boardDisplayName, boardDisplayDesc, catalogEntityHref } from "@/lib/api";
import PostComposer from "@/components/community/PostComposer";
import { useI18n } from "@/i18n/I18nProvider";
import { LoadingFallback } from "@/components/common/LoadingFallback";
import { useAuth } from "@/lib/authContext";
import {
 MessageSquare,
 Plus,
 Eye,
 User,
 X,
 Layers,
 Search,
 BookOpen,
 Flame,
 Sparkles,
 Menu,
 Hash,
 Archive,
  ChevronDown,
  Tag as TagIcon,
  Megaphone,
  Bug,
  MessageCircle,
  Cpu,
  Coffee,
  Bookmark,
  Film,
  Music2,
  RotateCw,
} from "lucide-react";
import { TabPanel } from "@/components/ui/TabPanel";
import ReportButton from "@/components/report/ReportButton";
import { PageShell } from "@/components/ui/PageShell";

// 列表页宽：与后端 /community/topics 的缺省 limit 一致，翻页后第一页窗口与改动前完全相同；
// 显式传窗口取代「缺省 = 第一页」的假设，第 31 条起才取得到（后端上限 100）。
const PAGE_SIZE = 30;

// 表头与主题行共用列宽，为英文参与者标题和相对时间保留空间。
const TOPIC_COLUMNS =
 "sm:grid-cols-[minmax(0,1fr)_7rem_3.5rem_8.5rem] md:grid-cols-[minmax(0,1fr)_7rem_3.5rem_3.5rem_8.5rem]";

function formatTimeAgo(dateStr: string, locale?: string, t?: (k: string, v?: Record<string,string|number>)=>string) {
 const diff = Date.now() - new Date(dateStr).getTime();
 const mins = Math.floor(diff / (1000 * 60));
 const tr = t || ((k:string)=>k);
 if (mins < 1) return tr("time.justNow");
 if (mins < 60) return tr("time.minAgo", { n: mins });
 const hours = Math.floor(mins / 60);
 if (hours < 24) return tr("time.hourAgo", { n: hours });
 const days = Math.floor(hours / 24);
 if (days < 30) return tr("time.dayAgo", { n: days });
 try { return new Date(dateStr).toLocaleDateString(locale || "zh-CN"); } catch { return new Date(dateStr).toLocaleDateString(); }
}

const BOARD_ICON_MAP: Record<string, React.ElementType> = {
 Layers, BookOpen, Cpu, Archive, Coffee, Hash, Tag: TagIcon, Sparkles, Flame, Bookmark, MessageSquare, Globe: Archive,
 Megaphone, Bug, MessageCircle, Film, Music2,
};
const BOARD_ICON: Record<string, React.ElementType> = {
  all: Layers,
  announcement: Megaphone,
  casual: Coffee,
  qa: Hash,
  reviews: BookOpen,
  bug_report: Bug,
  comment: MessageCircle,
};
function resolveBoardIcon(board: ForumBoard): React.ElementType {
 if (board.icon && BOARD_ICON_MAP[board.icon]) return BOARD_ICON_MAP[board.icon];
 return BOARD_ICON[board.code] || Hash;
}

function CommunityContent() {
 const { user } = useAuth();
 const { t, locale } = useI18n();
 const searchParams = useSearchParams();
 const router = useRouter();
 const pathname = usePathname();
 // 页码从 URL 派生：列表窗口由服务端 offset 决定，本地 state 恢复不出来，
 // 所以深链/后退/前进都必须以 URL 上的 page 为准（再同步回 state 触发重取）。
 const pageFromUrl = Math.max(1, parseInt(searchParams.get("page") || "1", 10) || 1);
 const initialTag = searchParams.get("tag");
 const initialTagId = searchParams.get("tag_id");
 // 从条目页跳来时带 entity_id / board_code，用于锁定到该条目的评论或指定板块。
 const entityFilter = searchParams.get("entity_id") || "";
 const initialBoard = searchParams.get("board_code") || "all";
 const [selectedBoard, setSelectedBoard] = useState<string>(initialBoard);
 const [topics, setTopics] = useState<DiscussionTopic[]>([]);
 // 页码（与 URL 双向同步）与后端的结果总数：计数与翻页判定都用 total，不是当前页条数。
 const [page, setPage] = useState(pageFromUrl);
 const [total, setTotal] = useState(0);
 const [loading, setLoading] = useState(true);
 // 失败与“没有主题”必须分开：失败要说清并可重试，不能伪装成空列表或永远停在加载中。
 const [loadError, setLoadError] = useState<string | null>(null);
 const [searchFilter, setSearchFilter] = useState("");
  const [sidebarOpen, setSidebarOpen] = useState(false);
  const [boardDropdownOpen, setBoardDropdownOpen] = useState(false);
  const [boardQuery, setBoardQuery] = useState("");
  const [tagDropdownOpen, setTagDropdownOpen] = useState(false);
  const [tagQuery, setTagQuery] = useState("");
  const boardDropdownRef = useRef<HTMLDivElement>(null);
  const boardSearchInputRef = useRef<HTMLInputElement>(null);
  const tagDropdownRef = useRef<HTMLDivElement>(null);
  const tagSearchInputRef = useRef<HTMLInputElement>(null);

 // tag filter for list
 const [availableTags, setAvailableTags] = useState<Tag[]>([]);
 const [filterTagId, setFilterTagId] = useState<number | null>(initialTagId ? Number(initialTagId) : null);
 const [filterTagName, setFilterTagName] = useState<string | null>(initialTag);

 // Composer drawer — only open/expanded kept, inner state lives in PostComposer
 const [isComposerOpen, setIsComposerOpen] = useState(false);
 const [composerExpanded, setComposerExpanded] = useState(false);

 const [boards, setBoards] = useState<ForumBoard[]>([ALL_FORUM_BOARD]);
 const [boardsLoading, setBoardsLoading] = useState(true);
 const [boardsFailed, setBoardsFailed] = useState(false);

 const loadBoards = async (force = false) => {
 setBoardsLoading(true);
 setBoardsFailed(false);
 try {
 setBoards(await fetchBoards({ force }));
 } catch {
 setBoards([ALL_FORUM_BOARD]);
 setBoardsFailed(true);
 } finally {
 setBoardsLoading(false);
 }
 };

 useEffect(() => {
 void loadBoards();
 }, []);

 const fetchTags = async (q?: string) => {
 try {
 const qs = q ? `?q=${encodeURIComponent(q)}` : "";
 const data = await fetchApi<Tag[]>(`/community/topic-tags${qs}`);
 setAvailableTags(data || []);
 } catch {
 // keep previous
 }
 };

 useEffect(() => {
 fetchTags();
 }, []);

  useEffect(() => {
    const handler = (e: MouseEvent) => {
      if (tagDropdownRef.current && !tagDropdownRef.current.contains(e.target as Node)) {
        setTagDropdownOpen(false);
      }
      if (boardDropdownRef.current && !boardDropdownRef.current.contains(e.target as Node)) {
        setBoardDropdownOpen(false);
      }
    };
    if (tagDropdownOpen || boardDropdownOpen) {
      document.addEventListener("mousedown", handler);
      if (tagDropdownOpen) {
        setTimeout(() => tagSearchInputRef.current?.focus(), 50);
      }
      if (boardDropdownOpen) {
        setTimeout(() => boardSearchInputRef.current?.focus(), 50);
      }
    }
    return () => document.removeEventListener("mousedown", handler);
  }, [tagDropdownOpen, boardDropdownOpen]);

  const filteredBoards = boards.filter((b) => {
    if (!boardQuery.trim()) return true;
    const q = boardQuery.trim().toLowerCase();
    const name = boardDisplayName(b, locale, t).toLowerCase();
    const desc = boardDisplayDesc(b, locale, t).toLowerCase();
    return (
      name.includes(q) ||
      desc.includes(q) ||
      b.code.toLowerCase().includes(q)
    );
  });

  const filteredTags = availableTags.filter((t) => {
    if (!tagQuery.trim()) return true;
    return t.name.toLowerCase().includes(tagQuery.trim().toLowerCase());
  });

 const currentSelectedTagObj = filterTagId
   ? availableTags.find((t) => t.id === filterTagId)
   : filterTagName
   ? availableTags.find((t) => t.name === filterTagName) || { id: -1, name: filterTagName }
   : null;

 const loadTopics = async () => {
 setLoading(true);
 setLoadError(null);
 // 请求挂住时 loading 必须有终态，否则列表永远停在“加载中”。
 const controller = new AbortController();
 const timer = setTimeout(() => controller.abort(), 15000);
 try {
 const params = new URLSearchParams();
 if (selectedBoard && selectedBoard !== "all") {
 params.append("board_code", selectedBoard);
 }
 if (entityFilter) {
 params.append("entity_id", entityFilter);
 }
 if (searchFilter.trim()) {
 params.append("q", searchFilter.trim());
 }
 if (filterTagId) {
 params.append("tag_id", String(filterTagId));
 } else if (filterTagName) {
 params.append("tag", filterTagName);
 }
 params.set("page", String(page));
 params.set("page_size", String(PAGE_SIZE));
 const res = await fetchApi<{ items: DiscussionTopic[]; total: number }>(
 `/community/topics?${params.toString()}`,
 { signal: controller.signal }
 );
 const list = requireArray<DiscussionTopic>(res?.items, "topics.items");
 setTopics(list);
 setTotal(safeCount(res.total, list.length));
 } catch {
 setTopics([]);
 setLoadError(t("community.loadFailed"));
 } finally {
 clearTimeout(timer);
 setLoading(false);
 }
 };

 // 翻页写 URL：同一个链接能复现同一窗口，后退键在页码之间往返（state 与 URL 一起走）。
 const goToPage = (next: number) => {
 const target = Math.max(1, next);
 if (target === page) return;
 const params = new URLSearchParams(searchParams.toString());
 if (target <= 1) params.delete("page");
 else params.set("page", String(target));
 const qs = params.toString();
 setPage(target);
 router.push(qs ? `${pathname}?${qs}` : pathname);
 };

 // 换分区/标签或重新搜索都要回第一页：URL 里留着上一组的页码，新筛选会取到空窗口。
 const resetToFirstPage = () => {
 if (page !== 1) setPage(1);
 if (!searchParams.get("page")) return;
 const params = new URLSearchParams(searchParams.toString());
 params.delete("page");
 const qs = params.toString();
 router.replace(qs ? `${pathname}?${qs}` : pathname);
 };

 // 搜索框内容只在 state 里（不进 URL）：页码本来就在第一页时 effect 不会重跑，得显式重取。
 const submitSearch = () => {
 if (page !== 1) { resetToFirstPage(); return; }
 loadTopics();
 };

 useEffect(() => {
 loadTopics();
 }, [selectedBoard, filterTagId, filterTagName, page]);

 // 后退/前进只改 URL 不改 state：页码必须从 URL 回灌，否则地址栏的页码与列表窗口会脱节。
 useEffect(() => {
 setPage(pageFromUrl);
 }, [pageFromUrl]);

 const getBoard = (code: string) => {
 return getBoardSync(code, boards);
 };

 const currentBoard = getBoard(selectedBoard);
 const currentBoardName = currentBoard ? boardDisplayName(currentBoard, locale, t) : selectedBoard;
 const canCreateTopic = !boardsLoading && !boardsFailed && boards.some((b) => b.code !== "all" && b.is_enabled !== false);
 const totalPages = Math.max(1, Math.ceil(total / PAGE_SIZE));
 // 越界页（手改 URL、或结果变少）：后端把 offset 静默收敛成空窗口，得自己给可读出口。
 const pageOutOfRange = !loading && !loadError && total > 0 && topics.length === 0 && page > totalPages;

 return (
 <div className="min-h-screen bg-background relative flex flex-col overflow-clip selection:bg-primary selection:text-white text-sm">
 <Navbar />

 {/* Forum layout: sidebar + topic stream */}
 <PageShell spacing="none" contentClassName="grid grid-cols-1 lg:grid-cols-[220px_minmax(0,1fr)] gap-5 items-start">
 {/* ===================== Left board sidebar ===================== */}
 {/* Desktop */}
 <aside className="hidden lg:flex min-w-0 flex-col rounded-xl border border-line bg-surface sticky top-[calc(var(--mf-header-h)+1rem)] max-h-[calc(100dvh-var(--mf-header-h)-2rem)] overflow-hidden">
 <div className="flex-1 overflow-y-auto">
 <div className="p-4 space-y-5">
 {/* Board list — single source: Latest-Top / search live in top bar */}
 <div className="space-y-1">
 <h3 className="px-2.5 text-xs font-mono font-bold tracking-widest text-text-faint uppercase flex items-center justify-between">
 <span>{t("community.boards")}</span>
 <span className="font-normal normal-case tracking-normal text-xs text-gray-600">{boardsLoading ? t("common.loading") : boardsFailed ? "—" : t("community.boardCount", {count: boards.length - 1})}</span>
 </h3>
 <div className="space-y-0.5">
 {boards.map((board) => {
 const Icon = resolveBoardIcon(board);
 const isActive = selectedBoard === board.code;
 const isCommentOnly = board.show_in_feed === false && board.code !== "all";
 let badge: string | null = null;
 // 角标是从当前页数据里数出来的：结果超过一页时它只是"这一页"的分布，会被读成总数，
 // 所以只在结果能在一页内取全时显示。
 if (total <= PAGE_SIZE) {
 if (selectedBoard === "all") {
 if (board.code === "all") badge = String(topics.length);
 else if (!isCommentOnly) {
 const c = topics.filter((t) => t.board_code === board.code).length;
 if (c > 0) badge = String(c);
 }
 } else if (isActive) {
 badge = String(topics.length);
 }
 }
 return (
 <button
 key={board.code}
 onClick={() => {
 setSelectedBoard(board.code);
 resetToFirstPage();
 }}
 className={`w-full group flex items-center gap-2.5 px-3.5 py-2.5 rounded-md border text-left transition-colors duration-fast ease-soft ${
 isActive
 ? "bg-surface border-line text-emphasis shadow-sm"
 : "border-transparent text-text-muted hover:text-emphasis hover:bg-surface/70 hover:border-line-subtle"
 }`}
 >
 <span className={`w-8 h-8 rounded-md flex items-center justify-center border shrink-0 ${board.bgColor} ${board.borderColor}`}>
 <Icon className={`w-4 h-4 ${board.color}`} />
 </span>
	 <span className="flex-1 min-w-0">
	 <span className={`block text-sm font-semibold leading-none truncate ${isActive ? "text-emphasis" : "text-text-body group-hover:text-emphasis"}`}>
	 {boardDisplayName(board, locale, t)}
	 </span>
	 <span className="block text-xs text-text-faint truncate leading-tight mt-0.5">{boardDisplayDesc(board, locale, t)}</span>
	 </span>
	 {badge && (
	 <span className={`shrink-0 px-2.5 py-1 rounded text-xs font-mono leading-none border ${isActive ? "bg-background border-line text-text-body" : "bg-surface border-line text-text-faint"}`}>
	 {badge}
	 </span>
	 )}
	 </button>
	 );
	 })}
	 </div>
	 </div>

	 </div>
	 </div>
	 </aside>

 {/* Mobile drawer */}
 {sidebarOpen && (
 <div className="lg:hidden fixed inset-0 z-40 flex">
 <div className="flex-1 bg-black/60 backdrop-blur-sm" onClick={() => setSidebarOpen(false)} />
 <div className="w-[300px] max-w-[85vw] bg-background border-r border-line flex flex-col overflow-hidden">
 <div className="h-12 flex items-center justify-between px-4 border-b border-line shrink-0">
 <span className="text-sm font-bold text-emphasis flex items-center gap-2">
 <Layers className="w-4 h-4 text-text-faint" />
 {t("community.boardNav")}
 </span>
 <button onClick={() => setSidebarOpen(false)} className="p-1.5 text-text-muted hover:text-emphasis rounded-md hover:bg-surface">
 <X className="w-4 h-4" />
 </button>
 </div>
 <div className="flex-1 overflow-y-auto p-4 space-y-4">
 {user ? (
 <button
 onClick={() => {
 setSidebarOpen(false);
 setIsComposerOpen(true);
 }}
 disabled={!canCreateTopic}
 className="w-full py-1.5 rounded-md bg-primary text-white text-sm font-bold flex items-center justify-center gap-2 disabled:opacity-50"
 >
 <Plus className="w-4 h-4" />
 <span>{t("community.publishNew")}</span>
 </button>
 ) : null}
	 <div className="space-y-1">
	 {boards.map((board) => {
	 const Icon = resolveBoardIcon(board);
	 const isActive = selectedBoard === board.code;
	 return (
	 <button
	 key={board.code}
	 onClick={() => {
	 setSelectedBoard(board.code);
	 setSidebarOpen(false);
	 resetToFirstPage();
	 }}
	 className={`w-full flex items-center gap-2 px-2.5 py-2 rounded-md border text-left ${isActive ? "bg-surface border-line text-emphasis" : "border-transparent text-text-muted"}`}
	 >
	 <span className={`w-8 h-8 rounded-md flex items-center justify-center border ${board.bgColor} ${board.borderColor}`}>
	 <Icon className={`w-4 h-4 ${board.color}`} />
	 </span>
	 <span className="flex-1 min-w-0">
	 <span className="block text-sm font-semibold truncate">{boardDisplayName(board, locale, t)}</span>
	 <span className="block text-xs text-text-faint truncate">{boardDisplayDesc(board, locale, t)}</span>
	 </span>
	 </button>
	 );
	 })}
	 </div>
	 </div>
	 </div>
	 </div>
	 )}

	{/* ===================== Main Topic List ===================== */}
	<div className="min-w-0 flex flex-col">
	  {/* Discourse-style Hero Search & Filter Header */}
	  <div className="sticky top-[var(--mf-header-h)] z-20 rounded-xl border border-line bg-surface/95 backdrop-blur">
	    <div className="p-4 space-y-3.5 w-full">
	      {/* Row 1: Discourse-style Prominent Centered Search Bar */}
	      <div className="flex items-center gap-2.5">
	        <button
	          onClick={() => setSidebarOpen(true)}
	          aria-label={t("community.boardNav")} className="lg:hidden p-2 rounded-md border border-line bg-surface text-text-muted hover:text-emphasis"
	        >
	          <Menu className="w-4 h-4" />
	        </button>

	        <div className="relative flex-1">
	          <Search className="w-4 h-4 text-text-muted absolute left-3.5 top-1/2 -translate-y-1/2 pointer-events-none" />
	          <input
	            type="text"
	            placeholder={t("community.boardSearchPlaceholder")}
	            value={searchFilter}
	            onChange={(e) => setSearchFilter(e.target.value)}
	            onKeyDown={(e) => e.key === "Enter" && submitSearch()}
	            className="w-full pl-10 pr-24 h-11 rounded-lg bg-surface border border-line text-emphasis text-sm placeholder-gray-500 focus:outline-none focus:border-gray-500 transition-colors duration-fast ease-soft shadow-inner"
	          />
	          <div className="absolute right-2 top-1/2 -translate-y-1/2 flex items-center gap-1.5">
	            {searchFilter && (
	              <button
	                onClick={() => {
	                  setSearchFilter("");
	                }}
	                className="p-1 text-text-faint hover:text-text-body transition-colors duration-fast ease-soft rounded"
	                title={t("community.clear")}
	              >
	                <X className="w-3.5 h-3.5" />
	              </button>
	            )}
	            <button
	              onClick={submitSearch}
	              className="px-3 py-1 rounded bg-emphasis/[0.08] hover:bg-emphasis/[0.15] border border-line text-xs font-mono text-text-strong transition-colors duration-fast ease-soft"
	            >
	              {t("common.search")}
	            </button>
	          </div>
	        </div>

	        {user && (
	          <button
	            onClick={() => setIsComposerOpen(true)}
	            disabled={!canCreateTopic}
	            className="px-4 h-11 rounded-lg bg-primary text-white text-sm font-bold inline-flex items-center gap-2 shadow-xs hover:opacity-90 transition-opacity shrink-0 disabled:opacity-50"
	          >
	            <Plus className="w-4 h-4 stroke-[2.5]" />
	            <span className="hidden sm:inline">{t("community.newTopic")}</span>
	          </button>
	        )}
	      </div>

	      {/* Row 2: Discourse Sub-Filter Controls (类别 > | 标签 > | 最新 | 热门) */}
	      <div className="flex flex-wrap items-center justify-between gap-2.5 pt-0.5">
	        <div className="flex items-center gap-2 flex-wrap">
	          {/* Discourse Category Dropdown (类别 >) */}
	          <div className="relative" ref={boardDropdownRef}>
	            <button
	              type="button"
	              onClick={() => {
	                setBoardDropdownOpen(!boardDropdownOpen);
	                setBoardQuery("");
	              }}
	              className={`h-9 px-3 rounded-md border text-xs font-medium inline-flex items-center gap-1.5 transition-colors duration-fast ease-soft cursor-pointer ${
	                selectedBoard !== "all"
	                  ? `${currentBoard?.bgColor || "bg-surface"} ${currentBoard?.borderColor || "border-line"} ${currentBoard?.color || "text-text-body"} font-semibold shadow-xs`
	                  : "bg-surface hover:bg-surfaceBorder border-line text-text-body"
	              }`}
	            >
	              <span className="text-text-muted font-normal">{t("community.searchCategoryFilter")}</span>
	              <span className="text-text-faint font-mono">&gt;</span>
	              <span className="max-w-[130px] truncate">
	                {selectedBoard === "all" ? t("community.allBoardsOption") : currentBoardName}
	              </span>
	              <ChevronDown className={`w-3 h-3 text-text-muted transition-transform duration-base ease-soft ${boardDropdownOpen ? "rotate-180" : ""}`} />
	            </button>

	            {boardDropdownOpen && (
	              <div className="absolute left-0 top-full mt-1.5 w-72 rounded-panel bg-surface border border-line shadow-xl z-50 p-2 space-y-2 animate-in duration-100">
	                <div className="relative">
	                  <Search className="w-3.5 h-3.5 text-text-faint absolute left-2.5 top-2.5" />
	                  <input
	                    ref={boardSearchInputRef}
	                    type="text"
	                    value={boardQuery}
	                    onChange={(e) => setBoardQuery(e.target.value)}
	                    placeholder={t("community.boardDropdownPlaceholder")}
	                    className="w-full pl-8 pr-2.5 py-1.5 rounded-md bg-background border border-line text-xs text-emphasis placeholder-gray-500 focus:outline-none focus:border-gray-500 font-mono"
	                  />
	                </div>

	                <div className="max-h-60 overflow-y-auto space-y-0.5 scrollbar-thin">
	                  <button
	                    type="button"
	                    onClick={() => {
	                      setSelectedBoard("all");
	                      setBoardDropdownOpen(false);
	                      resetToFirstPage();
	                    }}
	                    className={`w-full text-left px-2.5 py-2 rounded-md text-xs flex items-center justify-between transition-colors duration-fast ease-soft ${
	                      selectedBoard === "all"
	                        ? "bg-primary text-white font-semibold"
	                        : "text-text-body hover:text-white hover:bg-emphasis/[0.05]"
	                    }`}
	                  >
	                    <span className="flex items-center gap-2 truncate">
	                      <Hash className="w-3.5 h-3.5 shrink-0 opacity-70" />
	                      <span>{t("community.allBoardsOption")}</span>
	                    </span>
	                    {selectedBoard === "all" && <span className="text-[10px]">✓</span>}
	                  </button>

	                  {filteredBoards
	                    .filter((b) => b.code !== "all")
	                    .map((board) => {
	                      const Icon = resolveBoardIcon(board);
	                      const isSelected = selectedBoard === board.code;
	                      return (
	                        <button
	                          key={board.code}
	                          type="button"
	                          onClick={() => {
	                            setSelectedBoard(board.code);
	                            setBoardDropdownOpen(false);
	                            resetToFirstPage();
	                          }}
	                          className={`w-full text-left px-2.5 py-1.5 rounded-md text-xs flex items-center justify-between transition-colors duration-fast ease-soft ${
	                            isSelected
	                              ? `${board.bgColor} ${board.color} font-semibold border ${board.borderColor}`
	                              : "text-text-body hover:text-emphasis hover:bg-emphasis/[0.05]"
	                          }`}
	                        >
	                          <span className="flex items-center gap-2 min-w-0">
	                            <span className={`w-5 h-5 rounded flex items-center justify-center shrink-0 ${board.bgColor} ${board.borderColor} border`}>
	                              <Icon className={`w-3 h-3 ${board.color}`} />
	                            </span>
	                            <span className="truncate">{boardDisplayName(board, locale, t)}</span>
	                          </span>
	                          {isSelected && <span className="text-[10px]">✓</span>}
	                        </button>
	                      );
	                    })}
	                </div>
	              </div>
	            )}
	          </div>

	          {/* Discourse Tag Dropdown (标签 >) */}
	          <div className="relative" ref={tagDropdownRef}>
	            <button
	              type="button"
	              onClick={() => {
	                setTagDropdownOpen(!tagDropdownOpen);
	                setTagQuery("");
	              }}
	              className={`h-9 px-3 rounded-md border text-xs font-medium inline-flex items-center gap-1.5 transition-colors duration-fast ease-soft cursor-pointer ${
	                currentSelectedTagObj
	                  ? "bg-emerald-500/20 border-emerald-500/40 text-success-soft font-semibold shadow-xs"
	                  : "bg-surface hover:bg-surfaceBorder border-line text-text-body"
	              }`}
	            >
	              <span className="text-text-muted font-normal">{t("community.searchTagFilter")}</span>
	              <span className="text-text-faint font-mono">&gt;</span>
	              <span className="max-w-[120px] truncate">
	                {currentSelectedTagObj ? `#${currentSelectedTagObj.name}` : t("community.allTagsOption")}
	              </span>
	              <ChevronDown className={`w-3 h-3 text-text-muted transition-transform duration-base ease-soft ${tagDropdownOpen ? "rotate-180" : ""}`} />
	            </button>

	            {tagDropdownOpen && (
	              <div className="absolute left-0 top-full mt-1.5 w-64 rounded-panel bg-surface border border-line shadow-xl z-50 p-2 space-y-2 animate-in duration-100">
	                <div className="relative">
	                  <Search className="w-3.5 h-3.5 text-text-faint absolute left-2.5 top-2.5" />
	                  <input
	                    ref={tagSearchInputRef}
	                    type="text"
	                    value={tagQuery}
	                    onChange={(e) => {
	                      setTagQuery(e.target.value);
	                      fetchTags(e.target.value);
	                    }}
	                    placeholder={t("community.tagDropdownPlaceholder")}
	                    className="w-full pl-8 pr-2.5 py-1.5 rounded-md bg-background border border-line text-xs text-emphasis placeholder-gray-500 focus:outline-none focus:border-emerald-400 font-mono"
	                  />
	                </div>

	                <div className="max-h-56 overflow-y-auto space-y-0.5 scrollbar-thin">
	                  <button
	                    type="button"
	                    onClick={() => {
	                      setFilterTagId(null);
	                      setFilterTagName(null);
	                      setTagDropdownOpen(false);
	                      resetToFirstPage();
	                    }}
	                    className={`w-full text-left px-2.5 py-1.5 rounded-md text-xs font-mono flex items-center justify-between transition-colors duration-fast ease-soft ${
	                      !filterTagId && !filterTagName
	                        ? "bg-primary text-white font-semibold"
	                        : "text-text-muted hover:text-white hover:bg-emphasis/[0.05]"
	                    }`}
	                  >
	                    <span>{t("community.allTagsOption")}</span>
	                    {!filterTagId && !filterTagName && <span className="text-[10px]">✓</span>}
	                  </button>

	                  {filteredTags.length === 0 ? (
	                    <div className="py-4 text-center text-xs font-mono text-text-faint">
	                      {t("community.noTagMatch")}
	                    </div>
	                  ) : (
	                    filteredTags.map((tag) => {
	                      const isSelected = filterTagId === tag.id || filterTagName === tag.name;
	                      return (
	                        <button
	                          key={tag.id}
	                          type="button"
	                          onClick={() => {
	                            setFilterTagId(tag.id);
	                            setFilterTagName(null);
	                            setTagDropdownOpen(false);
	                            resetToFirstPage();
	                          }}
	                          className={`w-full text-left px-2.5 py-1.5 rounded-md text-xs font-mono flex items-center justify-between transition-colors duration-fast ease-soft ${
	                            isSelected
	                              ? "bg-emerald-500/20 text-success-soft font-semibold border border-emerald-500/30"
	                              : "text-text-body hover:text-emphasis hover:bg-emphasis/[0.05]"
	                          }`}
	                        >
	                          <span className="flex items-center gap-1.5 truncate">
	                            <TagIcon className="w-3 h-3 text-text-faint shrink-0" />
	                            <span className="truncate">#{tag.name}</span>
	                          </span>
	                          {isSelected && <span className="text-[10px] text-success">✓</span>}
	                        </button>
	                      );
	                    })
	                  )}
	                </div>
	              </div>
	            )}
	          </div>
	        </div>

	      </div>

	      {/* Active Tag Filter Chip */}
	      {currentSelectedTagObj && (
	        <div className="flex items-center gap-2 pt-0.5 text-xs font-mono animate-in duration-100">
	          <span className="text-text-faint">{t("community.selectedTag", { name: "" })}</span>
	          <span className="inline-flex items-center gap-1.5 px-2.5 py-0.5 rounded-md bg-emerald-500/15 border border-emerald-500/30 text-success-soft font-semibold">
	            <TagIcon className="w-3 h-3" />
	            <span>#{currentSelectedTagObj.name}</span>
	            <button
	              type="button"
	              onClick={() => {
	                setFilterTagId(null);
	                setFilterTagName(null);
	                resetToFirstPage();
	              }}
	              className="hover:text-emphasis p-0.5"
	              title={t("community.clearTag")}
	            >
	              <X className="w-3 h-3" />
	            </button>
	          </span>
	        </div>
	      )}
	    </div>
	  </div>

 <div className="py-6 space-y-5 flex-1">
 {boardsFailed && (
 <div role="alert" className="rounded-lg border border-line p-4 flex items-center justify-between gap-3">
 <p className="text-sm text-danger-soft">{t("community.boardsLoadFailed")}</p>
 <button onClick={() => void loadBoards(true)} className="text-sm text-text-body hover:text-emphasis">
 {t("common.retry")}
 </button>
 </div>
 )}
 {/* key 随页签/分区/筛选变化重放进入动画；搜索框内容不参与，避免输入时闪动 */}
 <TabPanel activeKey={selectedBoard + "-" + (filterTagId ?? filterTagName ?? "all")} spacing="none" className="border border-line rounded-xl overflow-hidden bg-surface shadow-sm">
 <div className={`hidden sm:grid ${TOPIC_COLUMNS} items-center py-2.5 bg-background/60 border-b border-line text-xs font-medium text-text-muted`}>
 <span className="min-w-0 px-4">{t("community.topic")}</span>
 <span className="min-w-0 px-1 text-center">{t("community.participants")}</span>
 <span className="min-w-0 px-1 text-center">{t("community.replies")}</span>
 <span className="min-w-0 px-1 text-center hidden md:inline">{t("community.views")}</span>
 <span className="min-w-0 pr-4 text-right">{t("community.activity")}</span>
 </div>

 {/* mobile header */}
 <div className="sm:hidden px-4 py-2 bg-background/60 border-b border-line text-sm font-mono text-text-faint flex items-center justify-between">
 <span>{t("community.topic")} · {currentBoardName}</span>
 <span>{t("community.topicItems", { count: total })}</span>
 </div>

 {loading ? (
 <div className="py-8 text-center text-text-faint font-mono text-sm">{t("common.loadingTopics")}</div>
 ) : loadError ? (
 <div className="py-8 text-center space-y-3">
 <p className="text-sm text-danger-soft">{loadError}</p>
 <button
 onClick={loadTopics}
 className="px-3.5 py-1.5 rounded-md bg-white hover:bg-gray-200 text-black text-sm font-bold inline-flex items-center gap-2 transition-colors duration-fast ease-soft"
 >
 <RotateCw className="w-4 h-4" />
 {t("common.retry")}
 </button>
 </div>
 ) : topics.length === 0 ? (
 <div className="py-20 text-center text-text-faint space-y-2">
 <p className="text-sm">{t("community.noTopics")}</p>
 <p className="text-sm text-gray-600">{t("community.noTopicsHint")}</p>
 {user && canCreateTopic && (
 <button onClick={() => setIsComposerOpen(true)} className="mt-3 px-3 py-1.5 rounded-md bg-white text-black text-sm font-bold inline-flex items-center gap-2">
 <Plus className="w-4 h-4" />
 {t("community.createFirstTopic")}
 </button>
 )}
 </div>
 ) : pageOutOfRange ? (
 <div className="py-8 text-center space-y-3">
 <p className="text-sm text-text-muted">{t("community.pageOutOfRange", { page, totalPages })}</p>
 <button
 onClick={() => goToPage(totalPages)}
 className="px-3.5 py-1.5 rounded-md bg-white hover:bg-gray-200 text-black text-sm font-bold inline-flex items-center gap-2 transition-colors duration-fast ease-soft"
 >
 {t("pagination.last")}
 </button>
 </div>
 ) : (
 <div className="divide-y divide-line-subtle">
 {topics.map((topic) => {
 const board = getBoard(topic.board_code);
 const Icon = board ? resolveBoardIcon(board) : Hash;
 const authorId = topic.user_id || topic.user?.id;
 return (
 <div key={topic.id} className={`group flex sm:grid ${TOPIC_COLUMNS} items-stretch hover:bg-emphasis/[0.02] transition-colors duration-fast ease-soft`}>
 {/* main col */}
 <div className="flex-1 min-w-0 py-3 px-4 space-y-1.5">
 <Link href={`/community/${topic.id}`} className="block text-sm font-semibold text-emphasis group-hover:text-success transition-colors duration-fast ease-soft leading-snug line-clamp-2 sm:line-clamp-1">
 {topic.is_pinned && <span className="mr-1 inline-flex items-center px-2.5 py-1 rounded bg-amber-500/15 border border-amber-500/30 text-warn-soft text-xs font-mono">📌 {t("community.pinned")}</span>}{topic.title}
 </Link>
 <div className="flex items-center gap-2 flex-wrap">
 <span className={`inline-flex items-center gap-2 px-2.5 py-1 rounded border text-xs font-mono ${board ? `${board.bgColor} ${board.borderColor} ${board.color}` : "border-line text-text-muted"}`}>
 <Icon className="w-4 h-4" />
 {board ? boardDisplayName(board, locale, t) : topic.board_code}
 </span>
 {topic.entity_id && topic.entity_title && (
 <Link
 href={catalogEntityHref(topic.entity_id)}
 className="inline-flex items-center gap-2 px-2.5 py-1 rounded bg-background border border-line text-text-body hover:text-emphasis text-xs font-mono hover:border-emerald-500/40 transition-colors duration-fast ease-soft max-w-[180px] truncate"
 onClick={(e) => e.stopPropagation()}
 >
 <BookOpen className="w-2.5 h-2.5 text-success shrink-0" />
 <span className="truncate">{topic.entity_title}</span>
 </Link>
 )}
 {topic.tags && topic.tags.length > 0 && topic.tags.map((tag) => (
 <button
 key={tag.id}
 onClick={() => setFilterTagId(tag.id)}
 className="inline-flex items-center gap-2 px-2.5 py-1 rounded-full bg-emerald-500/10 border border-emerald-500/25 text-success-soft hover:bg-emerald-500/20 text-xs font-mono transition-colors duration-fast ease-soft"
 title={t("community.filterByTag", {name: tag.name})}
 >
 <TagIcon className="w-2.5 h-2.5" />
 {tag.name}
 </button>
 ))}
 <span className="hidden sm:inline-flex items-center gap-2 text-xs text-text-faint font-mono">
 <User className="w-4 h-4" />
 {authorId ? (
 <Link
 href={`/users/${authorId}`}
 className="hover:text-emphasis hover:underline transition-colors duration-fast ease-soft"
 onClick={(e) => e.stopPropagation()}
 >
 {topic.user?.username || t("community.anonymous")}
 </Link>
 ) : (
 <span>{topic.user?.username || t("community.anonymous")}</span>
 )}
 <span className="text-gray-600">·</span>
 {formatTimeAgo(topic.created_at, locale, t)}
 </span>
 {/* 主题行内的举报入口：target_id 是主题 id（与标题链接同一对象）。 */}
 <ReportButton targetType="post" targetId={topic.id} compact />
 </div>
 {/* mobile meta */}
 <div className="sm:hidden flex items-center gap-3 text-sm font-mono text-text-faint">
 <span className="flex items-center gap-2">
 <MessageSquare className="w-4 h-4" />
 {topic.reply_count}
 </span>
 <span className="flex items-center gap-2">
 <Eye className="w-4 h-4" />
 {topic.view_count}
 </span>
 <span className="ml-auto">{formatTimeAgo(topic.updated_at || topic.created_at, locale, t)}</span>
 </div>
 </div>

 {/* avatars */}
 <div className="hidden sm:flex min-w-0 items-center justify-center">
 <div className="flex items-center -space-x-1.5">
 {authorId ? (
 <Link
 href={`/users/${authorId}`}
 title={`${t("community.authorPrefix")}${topic.user?.username || t("community.anonymous")}`}
 className="hover:opacity-90 transition-all z-10 block shrink-0"
 onClick={(e) => e.stopPropagation()}
 >
 <UserAvatar user={topic.user} size="sm" shape="circle" className="border-2 border-background ring-1 ring-surfaceBorder hover:ring-primary" />
 </Link>
 ) : (
 <div title={`${t("community.authorPrefix")}${topic.user?.username || t("community.anonymous")}`} className="block shrink-0">
 <UserAvatar user={topic.user} size="sm" shape="circle" className="border-2 border-background ring-1 ring-surfaceBorder" />
 </div>
 )}
 {topic.reply_count > 0 && (
 <div title={t("community.activeReplier")} className="w-7 h-7 rounded-full bg-emerald-500/20 border-2 border-background flex items-center justify-center text-[10px] font-bold text-success ring-1 ring-emerald-500/40 shrink-0">
 +{topic.reply_count}
 </div>
 )}
 </div>
 </div>

 <div className="hidden sm:flex min-w-0 items-center justify-center">
 <span className={`px-2.5 py-1 rounded text-sm font-bold font-mono ${topic.reply_count > 0 ? "bg-emphasis/[0.05] text-emphasis" : "text-text-faint"}`}>{topic.reply_count}</span>
 </div>
 <div className="hidden md:flex min-w-0 items-center justify-center text-text-muted font-mono text-sm">{topic.view_count}</div>
 <div className="hidden sm:flex min-w-0 items-center justify-end pr-4 text-text-muted font-mono text-sm whitespace-nowrap">{formatTimeAgo(topic.updated_at || topic.created_at, locale, t)}</div>
 </div>
 );
 })}
 </div>
 )}

 {/* 分页器只在多页时出现（与 works/[id] 的 totalPages > 1 守卫一致）。 */}
 {!loading && !loadError && totalPages > 1 && (
 <div className="flex items-center justify-between gap-2 px-4 py-3 border-t border-line bg-background/40 text-xs font-mono text-text-faint">
 <span>{t("community.topicItems", { count: total })}</span>
 <div className="flex items-center gap-2">
 <button
 type="button"
 disabled={page <= 1}
 onClick={() => goToPage(page - 1)}
 className="px-3 py-1.5 rounded-md border border-line bg-surface hover:bg-surfaceBorder disabled:opacity-40 disabled:pointer-events-none text-text-body transition-colors duration-fast ease-soft"
 >
 {t("pagination.prev")}
 </button>
 <span className="px-1 text-text-muted">{t("common.pagination", { page, total: totalPages })}</span>
 <button
 type="button"
 disabled={page >= totalPages}
 onClick={() => goToPage(page + 1)}
 className="px-3 py-1.5 rounded-md border border-line bg-surface hover:bg-surfaceBorder disabled:opacity-40 disabled:pointer-events-none text-text-body transition-colors duration-fast ease-soft"
 >
 {t("pagination.next")}
 </button>
 </div>
 </div>
 )}
 </TabPanel>
 </div>
 </div>
 </PageShell>

 {/* Composer Drawer — unified PostComposer */}
 {isComposerOpen && (
 <PostComposer
 mode="createTopic"
 boards={boards}
 availableTags={availableTags}
 expanded={composerExpanded}
 onExpandedChange={setComposerExpanded}
 locale={locale}
 t={t}
 onSuccess={() => {
 fetchTags();
 loadTopics();
 setIsComposerOpen(false);
 }}
 onClose={() => setIsComposerOpen(false)}
 />
 )}
 </div>
 );
}

export default function CommunityPage() {
  return (
    <Suspense fallback={<LoadingFallback className="min-h-screen bg-background flex items-center justify-center text-sm text-text-faint" />}>
      <CommunityContent />
    </Suspense>
  );
}
