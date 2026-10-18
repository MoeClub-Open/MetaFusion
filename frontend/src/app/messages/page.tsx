"use client";

import React, { Suspense, useCallback, useEffect, useState } from "react";
import Link from "next/link";
import { usePathname, useRouter, useSearchParams } from "next/navigation";
import { Navbar } from "@/components/Navbar";
import { PageShell } from "@/components/ui/PageShell";
import { UserAvatar } from "@/components/UserAvatar";
import { LoadingFallback } from "@/components/common/LoadingFallback";
import MessageThread, { MessageThreadPeer } from "@/components/community/MessageThread";
import { useAuth } from "@/lib/authContext";
import { useI18n } from "@/i18n/I18nProvider";
import { getAuthLoginUrl } from "@/lib/services";
import { fetchConversations, markConversationRead, ConversationItem } from "@/lib/api";
import { fetchUserProfile } from "@/lib/api/users";
import { AlertCircle, ArrowLeft, Loader2, LogIn, Mail, RotateCw } from "lucide-react";

// 与后端缺省页宽一致；翻页只往后追加。
const CONVERSATION_PAGE_SIZE = 20;
// 对方资料的解析并发上限：一次打开一页会话最多同时问账号服务这么多次。
const PEER_FETCH_CONCURRENCY = 5;

// 账号服务的公开投影只有 id / username / role（auth.users 里没有头像列），所以这里只存用户名：
// 头像缺席由 UserAvatar 的字母兜底渲染，不编造一个 avatar_url。
type PeerName = { status: "ok"; username: string } | { status: "failed" };

// 会话列表里**没有对方用户名**（账号资料归账号服务，互动服务不查它的库），
// 所以每个 peer_id 要单独解析一次。模块级缓存让翻页、切会话、来回进出都不重复请求；
// 失败也缓存成 failed（渲染短 id + 提示），刷新页面即重试——比每帧对同一个坏 id 重发请求好。
const peerNames = new Map<string, PeerName>();
const peerNamesInflight = new Map<string, Promise<void>>();

/** 解析失败时的显示名：短 id 是真实信息，比空白或"未知用户"更可核对。 */
function shortPeerId(id: string): string {
  return id.length > 8 ? id.slice(0, 8) : id;
}

function loadPeerName(id: string): Promise<void> {
  const existing = peerNamesInflight.get(id);
  if (existing) return existing;
  const task = (async () => {
    try {
      const profile = await fetchUserProfile(id);
      const username = typeof profile?.user?.username === "string" ? profile.user.username.trim() : "";
      if (!username) {
        // 200 但没有用户名：与取不到同样按未知处理，不把空串当成名字渲染。
        peerNames.set(id, { status: "failed" });
        return;
      }
      peerNames.set(id, { status: "ok", username });
    } catch {
      peerNames.set(id, { status: "failed" });
    } finally {
      peerNamesInflight.delete(id);
    }
  })();
  peerNamesInflight.set(id, task);
  return task;
}

/** 有界并发：一次最多开 PEER_FETCH_CONCURRENCY 条资料请求，不给账号服务制造突发。 */
async function loadPeerNames(ids: string[], limit = PEER_FETCH_CONCURRENCY): Promise<void> {
  let cursor = 0;
  const worker = async () => {
    while (cursor < ids.length) {
      const id = ids[cursor++];
      await loadPeerName(id);
    }
  };
  await Promise.all(Array.from({ length: Math.min(limit, ids.length) }, worker));
}

/** 会话时间：与社区列表同一套 time.* 词条，超过一个月退回本地日期。 */
function formatTimeAgo(dateStr: string, locale: string, t: (key: string, vars?: Record<string, string | number>) => string): string {
  const time = Date.parse(dateStr || "");
  if (!Number.isFinite(time)) return "";
  const mins = Math.floor((Date.now() - time) / 60000);
  if (mins < 1) return t("time.justNow");
  if (mins < 60) return t("time.minAgo", { n: mins });
  const hours = Math.floor(mins / 60);
  if (hours < 24) return t("time.hourAgo", { n: hours });
  const days = Math.floor(hours / 24);
  if (days < 30) return t("time.dayAgo", { n: days });
  return new Date(time).toLocaleDateString(locale);
}

function MessagesContent() {
  const { user, loading: authLoading } = useAuth();
  const { t, locale } = useI18n();
  const searchParams = useSearchParams();
  const router = useRouter();
  const pathname = usePathname();
  const selectedPeer = searchParams.get("peer") || "";

  const [items, setItems] = useState<ConversationItem[]>([]);
  const [total, setTotal] = useState(0);
  const [page, setPage] = useState(1);
  const [loading, setLoading] = useState(true);
  const [loadingMore, setLoadingMore] = useState(false);
  // 失败与"没有会话"是两种状态：失败只说失败并给重试，绝不再显示空态文案。
  const [listFailed, setListFailed] = useState(false);
  const [moreFailed, setMoreFailed] = useState(false);
  // 渲染用的那一份解析结果（模块级 Map 是请求缓存，契约仍是服务端那份；这里是它的投影）。
  const [names, setNames] = useState<Record<string, PeerName>>(() => Object.fromEntries(peerNames));

  const loadConversations = useCallback(async () => {
    setLoading(true);
    setListFailed(false);
    setMoreFailed(false);
    try {
      const r = await fetchConversations(1, CONVERSATION_PAGE_SIZE);
      setItems(r.items);
      setTotal(r.total);
      setPage(1);
    } catch {
      setItems([]);
      setTotal(0);
      setListFailed(true);
    } finally {
      setLoading(false);
    }
  }, []);

  useEffect(() => {
    if (authLoading) return;
    if (!user) {
      setItems([]);
      setTotal(0);
      setLoading(false);
      return;
    }
    loadConversations();
  }, [authLoading, user, loadConversations]);

  // 解析当前页（以及直接深链进来的那个）peer_id 的用户名；有界并发、结果进模块级缓存。
  useEffect(() => {
    const ids = Array.from(new Set([...items.map((c) => c.peer_id), selectedPeer])).filter(Boolean);
    const missing = ids.filter((id) => !peerNames.has(id) && !peerNamesInflight.has(id));
    if (missing.length === 0) {
      // 缓存里可能已经有别人先解出来的结果（同一会话第二次进入）：同步到渲染态。
      setNames((prev) => {
        const next = { ...prev };
        let changed = false;
        for (const id of ids) {
          const cached = peerNames.get(id);
          if (cached && next[id] !== cached) {
            next[id] = cached;
            changed = true;
          }
        }
        return changed ? next : prev;
      });
      return;
    }
    loadPeerNames(missing).then(() => setNames(Object.fromEntries(peerNames)));
  }, [items, selectedPeer]);

  const loadMore = async () => {
    if (loadingMore || items.length >= total) return;
    setLoadingMore(true);
    setMoreFailed(false);
    try {
      const next = page + 1;
      const r = await fetchConversations(next, CONVERSATION_PAGE_SIZE);
      setPage(next);
      setTotal(r.total);
      // 按 peer_id 去重后追加：新消息会把旧会话顶到新一页，取回的窗口可能与前几页重叠。
      setItems((prev) => {
        const seen = new Set(prev.map((c) => c.peer_id));
        return [...prev, ...r.items.filter((c) => c && c.peer_id && !seen.has(c.peer_id))];
      });
    } catch {
      setMoreFailed(true);
    } finally {
      setLoadingMore(false);
    }
  };

  const backToList = () => {
    router.replace(pathname, { scroll: false });
  };

  const markRead = useCallback(async (peerId: string) => {
    if (!peerId) return;
    try {
      await markConversationRead(peerId);
    } catch {
      // 已读失败静默：不打断阅读，列表下次刷新会带回服务端的真实未读数。
      return;
    }
    // 只有服务端确认标记后才清本地角标：失败时保持原样，免得角标先消失又跳回来。
    setItems((prev) => prev.map((c) => (c.peer_id === peerId ? { ...c, unread_count: 0 } : c)));
  }, []);

  const peerDisplayOf = (peerId: string): { name: string; unresolved: boolean } => {
    const entry = names[peerId];
    if (entry && entry.status === "ok") return { name: entry.username, unresolved: false };
    return { name: shortPeerId(peerId), unresolved: true };
  };

  const threadPeerOf = (peerId: string): MessageThreadPeer => {
    const entry = names[peerId];
    const username = entry && entry.status === "ok" ? entry.username : shortPeerId(peerId);
    return { id: peerId, username };
  };

  const hasMoreConversations = items.length > 0 && items.length < total;

  // w-full：面板是 flex 行容器，只给 h-full 的话整块会缩到内容宽度（截图里表现为右侧大片空白）。
  const centered = (children: React.ReactNode) => (
    <div className="h-full w-full min-h-[16rem] flex flex-col items-center justify-center text-center gap-3 p-8 text-text-faint">
      {children}
    </div>
  );

  const renderPanel = () => {
    if (authLoading || (user && loading)) {
      return centered(
        <span className="flex items-center gap-2 font-mono text-xs">
          <Loader2 className="w-4 h-4 animate-spin text-primary" />
          {t("common.loading")}
        </span>
      );
    }
    if (!user) {
      return centered(
        <>
          <div className="w-12 h-12 rounded-full bg-emphasis/5 border border-line flex items-center justify-center text-text-muted">
            <LogIn className="w-6 h-6" />
          </div>
          <p className="text-sm text-text-body">{t("messages.loginRequired")}</p>
          <p className="text-xs">{t("messages.loginRequiredHint")}</p>
          <a
            href={getAuthLoginUrl("/messages")}
            className="mt-1 inline-flex items-center gap-1.5 px-3.5 h-9 rounded-lg bg-primary text-white keep-white text-xs font-semibold hover:opacity-90 transition-opacity"
          >
            <LogIn className="w-3.5 h-3.5" />
            <span>{t("nav.login")}</span>
          </a>
        </>
      );
    }
    // 列表失败时只渲染失败态：不同屏出现"还没有私信"，那种"把取不到讲成没有"的写法已经踩过。
    if (listFailed) {
      return centered(
        <>
          <AlertCircle className="w-6 h-6 text-danger-soft" />
          <p className="text-sm text-danger-soft">{t("community.dmLoadFailed")}</p>
          <button
            type="button"
            onClick={loadConversations}
            className="mt-1 inline-flex items-center gap-2 px-3.5 h-9 rounded-lg border border-line bg-surface hover:bg-surfaceHover text-xs font-semibold text-text-strong transition-colors duration-fast ease-soft"
          >
            <RotateCw className="w-3.5 h-3.5" />
            {t("common.retry")}
          </button>
        </>
      );
    }
    // 成功且确实没有会话（尚未选中任何会话）时只渲染空态：与失败态严格分开。
    if (items.length === 0 && !selectedPeer) {
      return centered(
        <>
          <div className="w-12 h-12 rounded-full bg-emphasis/5 border border-line flex items-center justify-center text-text-muted">
            <Mail className="w-6 h-6" />
          </div>
          <p className="text-sm text-text-body">{t("messages.empty")}</p>
          <p className="text-xs">{t("messages.emptyHint")}</p>
        </>
      );
    }
    return (
      // flex-1 w-full：同样不能只给 h-full，否则两栏只占内容宽度、面板右侧留空。
      <div className="flex flex-1 w-full min-h-0">
        {/* 会话列表：窄屏选中会话后隐藏（返回按钮回到列表，不做左右硬挤的双栏）。 */}
        <aside
          className={`w-full md:w-[320px] md:shrink-0 min-h-0 border-r border-line flex-col ${selectedPeer ? "hidden md:flex" : "flex"}`}
        >
          <div className="shrink-0 px-4 py-3 border-b border-line bg-background/40 flex items-center gap-2">
            <Mail className="w-4 h-4 text-primary" />
            <span className="text-xs font-bold text-emphasis">{t("messages.conversations")}</span>
          </div>
          <div className="flex-1 min-h-0 overflow-y-auto">
            {items.length === 0 ? (
              <p className="p-6 text-xs text-text-faint text-center">{t("messages.empty")}</p>
            ) : (
              items.map((c) => {
                const peer = peerDisplayOf(c.peer_id);
                const active = c.peer_id === selectedPeer;
                return (
                  // 选中会话写进 URL：同一链接可复现，浏览器后退也能回到上一个会话。
                  <Link
                    key={c.peer_id}
                    href={`${pathname}?peer=${encodeURIComponent(c.peer_id)}`}
                    scroll={false}
                    className={`w-full px-3 py-3 border-b border-line-subtle flex items-start gap-3 transition-colors duration-fast ease-soft cursor-pointer ${
                      active ? "bg-emphasis/[0.06]" : "hover:bg-emphasis/[0.03]"
                    }`}
                  >
                    <UserAvatar user={{ id:c.peer_id, username: peer.name }} size="md" shape="circle" />
                    <div className="flex-1 min-w-0">
                      <div className="flex items-center gap-2">
                        <span
                          className={`text-sm font-semibold truncate ${peer.unresolved ? "text-text-muted font-mono" : "text-emphasis"}`}
                          title={peer.unresolved ? t("messages.unknownPeer") : peer.name}
                        >
                          {peer.name}
                        </span>
                        {peer.unresolved && (
                          <AlertCircle className="w-3 h-3 shrink-0 text-warn" aria-label={t("messages.unknownPeer")} />
                        )}
                        <span className="ml-auto shrink-0 text-[10px] font-mono text-text-faint">
                          {c.last_message?.created_at
                            ? formatTimeAgo(c.last_message.created_at, locale, t)
                            : ""}
                        </span>
                      </div>
                      <div className="mt-0.5 flex items-center gap-2">
                        <span className="flex-1 min-w-0 text-xs text-text-muted truncate">
                          {c.last_message?.body || ""}
                        </span>
                        {c.unread_count > 0 && (
                          <span
                            className="shrink-0 min-w-[18px] h-[18px] px-1 rounded-full bg-danger/15 border border-danger/30 text-danger-soft text-[10px] font-bold flex items-center justify-center"
                            title={t("messages.unreadLabel", { n: c.unread_count })}
                          >
                            {c.unread_count > 99 ? "99+" : c.unread_count}
                          </span>
                        )}
                      </div>
                    </div>
                  </Link>
                );
              })
            )}
            {hasMoreConversations && (
              <div className="p-3 space-y-2">
                <button
                  type="button"
                  onClick={loadMore}
                  disabled={loadingMore}
                  className="w-full h-9 rounded-lg border border-line bg-surface hover:bg-surfaceHover text-xs text-text-body font-mono inline-flex items-center justify-center gap-1.5 disabled:opacity-50"
                >
                  {loadingMore ? <Loader2 className="w-3.5 h-3.5 animate-spin" /> : null}
                  <span>{loadingMore ? t("common.loading") : t("messages.loadMoreConversations")}</span>
                </button>
                {moreFailed && (
                  // 更多会话失败只影响这一块，已经加载的会话保持可用（不清空、不整页转错误态）。
                  <p className="text-[11px] text-danger-soft text-center">{t("messages.loadMoreFailed")}</p>
                )}
              </div>
            )}
          </div>
        </aside>

        {/* 选中会话的正文：窄屏未选中时隐藏，只显示列表。 */}
        <section
          className={`flex-1 min-w-0 min-h-0 flex-col ${selectedPeer ? "flex" : "hidden md:flex"}`}
        >
          {selectedPeer ? (
            <>
              <div className="shrink-0 px-3 py-2.5 border-b border-line bg-background/40 flex items-center gap-2.5">
                <button
                  type="button"
                  onClick={backToList}
                  className="md:hidden p-1.5 rounded-md border border-line text-text-muted hover:text-emphasis"
                  title={t("messages.backToList")}
                  aria-label={t("messages.backToList")}
                >
                  <ArrowLeft className="w-4 h-4" />
                </button>
                <UserAvatar user={{ id:selectedPeer, username: peerDisplayOf(selectedPeer).name }} size="sm" shape="circle" />
                <Link
                  href={`/users/${encodeURIComponent(selectedPeer)}`}
                  className="text-sm font-semibold text-emphasis truncate hover:text-primary transition-colors duration-fast ease-soft"
                >
                  {peerDisplayOf(selectedPeer).name}
                </Link>
                {peerDisplayOf(selectedPeer).unresolved && (
                  <span className="text-[10px] text-warn truncate">{t("messages.unknownPeer")}</span>
                )}
              </div>
              <MessageThread
                key={selectedPeer}
                peerUser={threadPeerOf(selectedPeer)}
                onRead={() => markRead(selectedPeer)}
              />
            </>
          ) : (
            centered(<p className="text-sm">{t("messages.selectConversation")}</p>)
          )}
        </section>
      </div>
    );
  };

  return (
    <div className="min-h-screen relative flex flex-col">
      <Navbar />
      <PageShell
        title={t("messages.title")}
        icon={<Mail className="w-5 h-5 text-primary" />}
      >
        <div className="flex h-[calc(100dvh-var(--mf-header-h)-8rem)] min-h-[22rem] border border-line rounded-xl overflow-hidden bg-surface shadow-sm">
          {renderPanel()}
        </div>
      </PageShell>
    </div>
  );
}

export default function MessagesPage() {
  return (
    <Suspense
      fallback={
        <LoadingFallback className="min-h-screen flex items-center justify-center text-sm text-text-faint" />
      }
    >
      <MessagesContent />
    </Suspense>
  );
}
