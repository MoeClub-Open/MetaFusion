"use client";

import React, { useState, useEffect, useRef } from "react";
import Link from "next/link";
import { fetchDirectMessages, sendDirectMessage, DirectMessage } from "@/lib/api";
import { useAuth } from "@/lib/authContext";
import { useI18n } from "@/i18n/I18nProvider";
import { MessageCircle, Send, Loader2, AlertCircle, LogIn } from "lucide-react";

/**
 * 会话双方的最小资料。展示名来自账号服务的公开投影（/users/:id）：账号服务只保证
 * id / username / role，其余字段（头像、注册时间）在 auth.users 里并不存在，
 * 所以是"来源没有这一列"的可选项，不是"值恰好为空"。
 */
export interface MessageThreadPeer {
  id: string;
  username: string;
  avatar_url?: string;
  created_at?: string;
}

export interface MessageThreadProps {
  peerUser: MessageThreadPeer;
  /**
   * 初次加载成功后回调一次（说明这个会话确实被打开并读到了）。
   * 已读标记这类副作用留给调用方：弹窗与消息页各有各的错误处理口径，
   * 组件自己不发已读请求，也不假设对方是谁。
   */
  onRead?: () => void;
  /** 附加在根节点上：调用方决定这段消息流在父级 flex 里怎么撑开。 */
  className?: string;
}

/** 一页 20 条与后端缺省页宽一致；轮询只刷新第一页（最新的那一页）。 */
const PAGE_SIZE = 20;
const REFRESH_MS = 4000;

/** 会话在界面上按时间正序（上旧下新）展示；服务端返回的是倒序，合并时统一排序。 */
function mergeMessages(prev: DirectMessage[], incoming: DirectMessage[]): DirectMessage[] {
  const byId = new Map<string, DirectMessage>();
  for (const m of prev) byId.set(m.id, m);
  for (const m of incoming) if (m?.id) byId.set(m.id, m);
  return Array.from(byId.values()).sort((a, b) => {
    const ta = Date.parse(a.created_at || "") || 0;
    const tb = Date.parse(b.created_at || "") || 0;
    return ta - tb || a.id.localeCompare(b.id);
  });
}

/**
 * 一段私信会话的正文：消息流 + 加载更早 + 错误条 + 输入区。
 * 不包含头部（对方头像/昵称/关闭），那部分由弹窗与消息页各自渲染。
 */
export default function MessageThread({ peerUser, onRead, className = "" }: MessageThreadProps) {
  const { user } = useAuth();
  const { t, locale } = useI18n();
  const [messages, setMessages] = useState<DirectMessage[]>([]);
  const [total, setTotal] = useState(0);
  const [page, setPage] = useState(1);
  const [loading, setLoading] = useState(true);
  const [loadingMore, setLoadingMore] = useState(false);
  const [inputContent, setInputContent] = useState("");
  const [sending, setSending] = useState(false);
  const [err, setErr] = useState("");
  const messagesEndRef = useRef<HTMLDivElement>(null);
  const inputRef = useRef<HTMLTextAreaElement>(null);
  // onRead 每次渲染都可能是新的内联箭头，放进 effect 依赖会把轮询整体重建；用 ref 取值。
  const onReadRef = useRef(onRead);
  const readSignaledRef = useRef(false);

  useEffect(() => {
    onReadRef.current = onRead;
  });

  const isLoggedIn = !!user;
  // 不能给自己发：服务端以 400 invalid_recipient 拒绝，前端先把入口关掉再讲清楚原因。
  const isSelf = !!user && user.id === peerUser.id;
  const hasMore = messages.length < total;

  const scrollToBottom = (smooth = true) => {
    if (messagesEndRef.current) {
      messagesEndRef.current.scrollIntoView({
        behavior: smooth ? "smooth" : "auto",
        block: "end",
      });
    }
  };

  // 稳定错误码 → 当前语言的说明：不把 invalid_body 这类裸码贴给用户。
  const dmErrorText = (e: unknown, fallbackKey: string): string => {
    const status = typeof (e as { status?: unknown })?.status === "number" ? (e as { status: number }).status : undefined;
    if (status === 401) return t("users.profile.dmError.unauthorized");
    const code = String((e as { message?: unknown })?.message || "");
    if (code === "invalid_body") return t("users.profile.dmError.invalidBody");
    if (code === "invalid_recipient") return t("users.profile.cannotChatSelf");
    if (code === "not_found") return t("users.profile.dmError.notFound");
    return t(fallbackKey);
  };

  // 打开会话：拉第一页（最新 20 条）并起一个静默轮询。轮询失败不覆盖已加载内容——
  // 主动操作（发送 / 加载更早）的失败一定会显示出来，不静默。
  useEffect(() => {
    if (!peerUser?.id) return;
    let cancelled = false;
    setMessages([]);
    setTotal(0);
    setPage(1);
    setErr("");
    setLoading(true);
    readSignaledRef.current = false;

    fetchDirectMessages(peerUser.id, 1, PAGE_SIZE)
      .then((r) => {
        if (cancelled) return;
        setTotal(r.total);
        setMessages((prev) => mergeMessages(prev, r.items));
        setTimeout(() => scrollToBottom(false), 50);
        // 加载成功才算"真的读到了"：取不到内容时不标记已读，免得把没看到的消息吞掉。
        if (!readSignaledRef.current) {
          readSignaledRef.current = true;
          onReadRef.current?.();
        }
      })
      .catch((e: unknown) => {
        if (!cancelled) setErr(dmErrorText(e, "community.dmLoadFailed"));
      })
      .finally(() => {
        if (!cancelled) setLoading(false);
      });

    const timer = setInterval(() => {
      fetchDirectMessages(peerUser.id, 1, PAGE_SIZE)
        .then((r) => {
          if (cancelled) return;
          setTotal(r.total);
          setMessages((prev) => mergeMessages(prev, r.items));
        })
        .catch(() => {
          // 后台轮询失败保持静默：旧内容仍然可信，下一次轮询会补上。
        });
    }, REFRESH_MS);

    return () => {
      cancelled = true;
      clearInterval(timer);
    };
  }, [peerUser?.id]);

  useEffect(() => {
    const timer = setTimeout(() => {
      inputRef.current?.focus();
    }, 100);
    return () => clearTimeout(timer);
  }, []);

  const handleLoadMore = async () => {
    if (loadingMore || !hasMore) return;
    setLoadingMore(true);
    setErr("");
    try {
      // 往后翻是**更早**的消息：加载成功后并入列表顶部，不打断当前滚动位置。
      const next = page + 1;
      const r = await fetchDirectMessages(peerUser.id, next, PAGE_SIZE);
      setPage(next);
      setTotal(r.total);
      setMessages((prev) => mergeMessages(prev, r.items));
    } catch (e: unknown) {
      setErr(dmErrorText(e, "community.dmLoadFailed"));
    } finally {
      setLoadingMore(false);
    }
  };

  const handleSend = async (e?: React.FormEvent) => {
    if (e) e.preventDefault();
    const trimmed = inputContent.trim();
    if (!trimmed || sending || isSelf || !isLoggedIn) return;

    setSending(true);
    setErr("");
    try {
      const created = await sendDirectMessage(peerUser.id, trimmed);
      // 先落地服务端返回的那条（发送已经成功，不能因为后续回读失败就当没发出去）。
      setInputContent("");
      setMessages((prev) => mergeMessages(prev, [created]));
      setTimeout(() => scrollToBottom(true), 50);
      try {
        const r = await fetchDirectMessages(peerUser.id, 1, PAGE_SIZE);
        setTotal(r.total);
        setMessages((prev) => mergeMessages(prev, r.items));
      } catch {
        setErr(t("users.profile.dmError.refreshFailed"));
      }
    } catch (e: unknown) {
      // 发送失败保留输入内容，也不做本地乐观追加：服务端没落库就不显示成已发送。
      setErr(dmErrorText(e, "users.profile.dmError.generic"));
    } finally {
      setSending(false);
      inputRef.current?.focus();
    }
  };

  const handleKeyDown = (e: React.KeyboardEvent<HTMLTextAreaElement>) => {
    if (e.key === "Enter" && !e.shiftKey) {
      e.preventDefault();
      handleSend();
    }
  };

  return (
    <div className={`flex-1 min-h-0 flex flex-col ${className}`}>
      {/* Message Stream */}
      <div className="flex-1 overflow-y-auto p-4 space-y-3 bg-background/50">
        {loading ? (
          <div className="h-full flex items-center justify-center text-text-faint font-mono text-xs gap-2">
            <Loader2 className="w-4 h-4 animate-spin text-primary" />
            <span>{t("common.loading")}</span>
          </div>
        ) : messages.length === 0 && !err ? (
          // 有 err 时不再说"暂无私聊记录"：取不到与真的没有是两种状态，同时显示等于把失败讲成空。
          <div className="h-full flex flex-col items-center justify-center text-center p-6 text-text-faint space-y-2">
            <div className="w-12 h-12 rounded-full bg-emphasis/5 border border-line flex items-center justify-center text-text-muted">
              <MessageCircle className="w-6 h-6" />
            </div>
            <p className="text-xs">{t("users.profile.noMessages")}</p>
          </div>
        ) : (
          <>
            {hasMore && (
              <div className="flex justify-center pb-1">
                <button
                  type="button"
                  onClick={handleLoadMore}
                  disabled={loadingMore}
                  className="px-3 h-7 rounded-full bg-emphasis/5 hover:bg-emphasis/10 border border-line text-[11px] text-text-body font-mono inline-flex items-center gap-1.5 disabled:opacity-50"
                >
                  {loadingMore ? <Loader2 className="w-3 h-3 animate-spin" /> : null}
                  <span>
                    {loadingMore
                      ? t("common.loading")
                      : t("users.profile.loadMoreMessages") + " (" + (total - messages.length) + ")"}
                  </span>
                </button>
              </div>
            )}
            {messages.map((msg) => {
              const mine = msg.sender_id === user?.id;
              return (
                <div key={msg.id} className="flex flex-col" style={{ alignItems: mine ? "flex-end" : "flex-start" }}>
                  <div
                    className={
                      "max-w-[80%] rounded-card px-3.5 py-2.5 text-xs whitespace-pre-wrap leading-relaxed shadow-sm " +
                      (mine
                        ? "bg-primary text-white rounded-tr-xs"
                        : "bg-surface border border-line text-text-strong rounded-tl-xs")
                    }
                  >
                    {msg.body}
                  </div>
                  <div className="flex items-center gap-1 mt-1 px-1 text-[10px] font-mono text-text-faint">
                    <span>
                      {new Date(msg.created_at).toLocaleString(locale, {
                        month: "2-digit",
                        day: "2-digit",
                        hour: "2-digit",
                        minute: "2-digit",
                      })}
                    </span>
                  </div>
                </div>
              );
            })}
          </>
        )}
        <div ref={messagesEndRef} />
      </div>

      {/* Error message */}
      {err && (
        <div className="px-4 py-1.5 bg-rose-500/20 border-t border-rose-500/30 text-danger-soft text-xs font-mono flex items-center gap-1.5">
          <AlertCircle className="w-3.5 h-3.5 shrink-0" />
          <span>{err}</span>
        </div>
      )}

      {/* Input area */}
      {!isLoggedIn ? (
        <div className="p-3.5 bg-surface border-t border-line flex items-center justify-between gap-2.5 shrink-0 text-xs text-text-body">
          <span>{t("users.profile.dmError.unauthorized")}</span>
          <Link
            href="/login"
            className="px-2.5 h-7 rounded-md bg-primary text-white keep-white font-semibold inline-flex items-center gap-1 shrink-0"
          >
            <LogIn className="w-3.5 h-3.5" />
            <span>{t("nav.login")}</span>
          </Link>
        </div>
      ) : isSelf ? (
        <div className="p-3.5 bg-surface border-t border-line text-xs text-text-muted shrink-0">
          {t("users.profile.cannotChatSelf")}
        </div>
      ) : (
        <form
          onSubmit={handleSend}
          className="p-3.5 bg-surface border-t border-line flex items-end gap-2.5 shrink-0"
        >
          <textarea
            ref={inputRef}
            rows={2}
            value={inputContent}
            onChange={(e) => setInputContent(e.target.value)}
            onKeyDown={handleKeyDown}
            placeholder={t("users.profile.typeMessage")}
            className="flex-1 px-3.5 py-2.5 bg-background border border-line rounded-xl text-emphasis text-sm placeholder-gray-500 focus:outline-none focus:border-primary resize-none font-sans leading-relaxed"
          />
          <button
            type="submit"
            disabled={!inputContent.trim() || sending}
            className="h-11 px-4 rounded-xl bg-primary hover:opacity-90 disabled:opacity-40 text-white text-sm font-bold flex items-center justify-center gap-1.5 transition-all shrink-0 shadow-soft cursor-pointer"
          >
            {sending ? (
              <Loader2 className="w-4 h-4 animate-spin" />
            ) : (
              <Send className="w-4 h-4" />
            )}
            <span className="hidden sm:inline">
              {sending ? t("users.profile.sending") : t("users.profile.send")}
            </span>
          </button>
        </form>
      )}
    </div>
  );
}
