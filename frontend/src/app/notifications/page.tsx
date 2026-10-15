"use client";

import { useCallback, useEffect, useState } from "react";
import Link from "next/link";
import { Navbar } from "@/components/Navbar";
import { PageShell } from "@/components/ui/PageShell";
import { useAuth } from "@/lib/authContext";
import { useI18n } from "@/i18n/I18nProvider";
import {
  catalogEntityHref,
  emitNotificationsChanged,
  fetchNotifications,
  markAllNotificationsRead,
  markNotificationRead,
  NOTIFICATION_PAGE_SIZE_DEFAULT,
} from "@/lib/api";
import type { Notification, NotificationListResponse } from "@/lib/api";
import { isoTimestamp, localDateTime } from "@/lib/datetime";
import { getAuthLoginUrl } from "@/lib/services";
import {
  AlertCircle,
  Bell,
  Check,
  CheckCheck,
  CheckCircle2,
  ChevronLeft,
  ChevronRight,
  Inbox,
  Loader2,
  LogIn,
  MessageSquare,
  PackagePlus,
  RotateCw,
  UploadCloud,
  XCircle,
} from "lucide-react";

/**
 * 类型 → 图标与文案键。服务端将来新增类型时这里不认识，
 * 按 unknown 渲染（图标用铃铛、文案走 notifications.type.unknown），不崩也不显示裸 key。
 */
const TYPE_ICON: Record<string, typeof Bell> = {
  "comment.replied": MessageSquare,
  "entity.included": PackagePlus,
  "entity.review_approved": CheckCircle2,
  "entity.review_rejected": XCircle,
  "import.completed": UploadCloud,
};

const TYPE_LABEL_KEY: Record<string, string> = {
  "comment.replied": "notifications.type.comment_replied",
  "entity.included": "notifications.type.entity_included",
  "entity.review_approved": "notifications.type.review_approved",
  "entity.review_rejected": "notifications.type.review_rejected",
  "import.completed": "notifications.type.import_completed",
};

type Translate = (key: string, vars?: Record<string, string | number>) => string;

/** payload 是键值袋，缺键、换型都按"没有这个值"处理（不渲染空行）。 */
function payloadText(payload: Record<string, unknown>, key: string): string {
  const value = payload[key];
  return typeof value === "string" ? value.trim() : "";
}

function payloadNumberText(payload: Record<string, unknown>, key: string): string {
  const value = payload[key];
  return typeof value === "number" && Number.isFinite(value) ? String(value) : "";
}

/**
 * 跳转映射（契约里 payload 可能缺键，缺了就退回列表页/通用条目页，不拼一个必然 404 的地址）：
 *   comment.replied  → /community/<topic_id>，缺 topic_id 时 /community
 *   entity.*         → catalogEntityHref(entity_id)；详情地址只由 id 决定（kind 不参与 URL 命名）
 */
function notificationHref(n: Notification): string | null {
  const payload = n.payload;
  switch (n.type) {
    case "comment.replied": {
      const topicId = payloadText(payload, "topic_id");
      if (topicId) return `/community/${encodeURIComponent(topicId)}`;
      // 实体短评没有主题可跳（短评是 community.topics 里锚定条目的行，不是论坛主题）：
      // 落到被评论的条目页，服务端会给 entity_kind；连 kind 都没有时才退回评论流首页。
      const entityId = payloadText(payload, "entity_id");
      if (entityId) return catalogEntityHref(entityId);
      return "/community";
    }
    case "entity.included":
    case "entity.review_approved":
    case "entity.review_rejected": {
      if (n.subject_type === "commit") return `/submissions/${encodeURIComponent(n.subject_id)}`;
      const entityId = payloadText(payload, "entity_id") || (n.subject_type === "entity" ? n.subject_id : "");
      if (!entityId) return null;
      return catalogEntityHref(entityId);
    }
    case "import.completed":
      return "/contribute";
    default:
      return null;
  }
}

/** 每行下方的上下文（label/value 成对），缺键的行直接不出现。 */
function notificationRows(n: Notification, t: Translate): { label: string; value: string }[] {
  const payload = n.payload;
  const rows: { label: string; value: string }[] = [];
  const actor = n.actor_name.trim();
  if (actor) rows.push({ label: t("notifications.field.actor"), value: actor });

  switch (n.type) {
    case "comment.replied": {
      const topic = payloadText(payload, "topic_title") || payloadText(payload, "topic_id");
      if (topic) rows.push({ label: t("notifications.field.topic"), value: topic });
      const post = payloadNumberText(payload, "post_number");
      if (post) rows.push({ label: t("notifications.field.postNumber"), value: post });
      const entity = payloadText(payload, "entity_title");
      if (entity) rows.push({ label: t("notifications.field.entity"), value: entity });
      break;
    }
    case "entity.included": {
      const entity = payloadText(payload, "entity_title");
      if (entity) rows.push({ label: t("notifications.field.entity"), value: entity });
      const container = payloadText(payload, "container_title");
      if (container) rows.push({ label: t("notifications.field.container"), value: container });
      const role = payloadText(payload, "role");
      if (role) rows.push({ label: t("notifications.field.role"), value: role });
      break;
    }
    case "entity.review_approved":
    case "entity.review_rejected": {
      const entity = payloadText(payload, "entity_title");
      if (entity) rows.push({ label: t("notifications.field.entity"), value: entity });
      const version = payloadNumberText(payload, "version");
      if (version) rows.push({ label: t("notifications.field.version"), value: version });
      break;
    }
    case "import.completed": {
      const source = payloadText(payload, "source");
      if (source) rows.push({ label: t("notifications.field.source"), value: source });
      const status = payloadText(payload, "status");
      if (status) rows.push({ label: t("notifications.field.status"), value: status });
      // 仅渲染退役前已保存的历史通知；服务端不再生成此事件。
      // entities / relations 直接读取历史载荷，不重新计算或推导新建数。
      const entities = payloadNumberText(payload, "entities");
      if (entities) rows.push({ label: t("notifications.field.entities"), value: entities });
      const relations = payloadNumberText(payload, "relations");
      if (relations) rows.push({ label: t("notifications.field.relations"), value: relations });
      break;
    }
    default:
      break;
  }
  return rows;
}

export default function NotificationsPage() {
  const { user, loading: authLoading } = useAuth();
  const { t, locale } = useI18n();

  const [offset, setOffset] = useState(0);
  const [data, setData] = useState<NotificationListResponse | null>(null);
  const [loading, setLoading] = useState(true);
  // 失败与"没有通知"是两种状态：失败只说失败并给重试，绝不再显示空态文案
  // （同屏把失败讲成空是本项目修过多次的残留，见 docs-local/dev-log.md v0.2.5 段）。
  const [failed, setFailed] = useState(false);
  const [actionFailed, setActionFailed] = useState(false);
  const [markingAll, setMarkingAll] = useState(false);
  const [busyId, setBusyId] = useState<string | null>(null);
  // 本会话里已确认读过的 id：翻页重新取列表时若服务端仍回 read=false（缓存/竞态），
  // 不能把用户刚点过的行翻回未读。Set 只做查询，不迭代（target es5 无 downlevelIteration）。
  const [readIds, setReadIds] = useState<Set<string>>(new Set());

  const load = useCallback(async (nextOffset: number) => {
    setLoading(true);
    setFailed(false);
    // offset 先落定再发请求：失败时"重试"重试的是用户想去的这一页，而不是上一页。
    setOffset(nextOffset);
    try {
      const res = await fetchNotifications({ limit: NOTIFICATION_PAGE_SIZE_DEFAULT, offset: nextOffset });
      setData(res);
    } catch {
      // 失败必须如实显示：绝不 setData({ items: [] })，那会把"取不到"讲成"没有通知"。
      setData(null);
      setFailed(true);
    } finally {
      setLoading(false);
    }
  }, []);

  useEffect(() => {
    if (authLoading) return;
    if (!user) {
      setData(null);
      setFailed(false);
      setLoading(false);
      return;
    }
    load(0);
  }, [user, authLoading, load]);

  const items = data ? data.items : [];
  const total = data ? data.total : 0;
  const unread = data ? data.unread : 0;
  const pageCount = Math.max(1, Math.ceil(total / NOTIFICATION_PAGE_SIZE_DEFAULT));
  const page = Math.floor(offset / NOTIFICATION_PAGE_SIZE_DEFAULT) + 1;

  const handleMarkRead = async (n: Notification) => {
    if (busyId !== null || markingAll) return;
    setActionFailed(false);
    setBusyId(n.id);
    try {
      const res = await markNotificationRead(n.id);
      setReadIds((prev) => {
        const next = new Set(prev);
        next.add(n.id);
        return next;
      });
      setData((prev) =>
        prev
          ? {
              ...prev,
              unread: res.unread,
              items: prev.items.map((item) => (item.id === n.id ? { ...item, read: true } : item)),
            }
          : prev
      );
      // 顶栏角标据此立即刷新，不必为它开一条轮询。
      emitNotificationsChanged();
    } catch {
      setActionFailed(true);
    } finally {
      setBusyId(null);
    }
  };

  const handleMarkAll = async () => {
    if (markingAll || unread <= 0) return;
    setActionFailed(false);
    setMarkingAll(true);
    try {
      const res = await markAllNotificationsRead();
      setReadIds((prev) => {
        const next = new Set(prev);
        for (let i = 0; i < items.length; i += 1) next.add(items[i].id);
        return next;
      });
      setData((prev) =>
        prev
          ? { ...prev, unread: res.unread, items: prev.items.map((item) => ({ ...item, read: true })) }
          : prev
      );
      emitNotificationsChanged();
    } catch {
      setActionFailed(true);
    } finally {
      setMarkingAll(false);
    }
  };

  if (authLoading) {
    return (
      <div className="min-h-screen flex flex-col bg-background">
        <Navbar />
        <PageShell width="narrow" center>
          <Loader2 className="w-6 h-6 animate-spin text-primary" />
        </PageShell>
      </div>
    );
  }

  // 未登录：/notifications 不在 AuthGate 的受保护前缀里（它要能匿名打开），所以本页自己给
  // 登录入口，不整屏空白、也不把 401 显示成"暂无通知"。
  if (!user) {
    return (
      <div className="min-h-screen flex flex-col bg-background">
        <Navbar />
        <PageShell width="narrow" center>
          <div className="flex flex-col items-center gap-3 text-center">
            <Bell className="w-10 h-10 text-text-faint" strokeWidth={1.5} />
            <p className="text-sm font-medium text-text-strong">{t("notifications.loginRequired")}</p>
            <p className="text-xs text-text-muted">{t("notifications.loginRequiredHint")}</p>
            <a
              href={getAuthLoginUrl("/notifications")}
              className="inline-flex items-center gap-1.5 px-4 h-9 rounded-lg bg-primary/15 hover:bg-primary/25 border border-primary/30 text-xs font-semibold text-primary transition-colors duration-fast ease-soft"
            >
              <LogIn className="w-3.5 h-3.5" strokeWidth={1.8} />
              <span>{t("notifications.goLogin")}</span>
            </a>
          </div>
        </PageShell>
      </div>
    );
  }

  return (
    <div className="min-h-screen flex flex-col bg-background">
      <Navbar />
      <PageShell
        width="narrow"
        icon={<Bell className="w-5 h-5 text-primary" strokeWidth={1.8} />}
        title={t("notifications.title")}
        subtitle={unread > 0 ? t("notifications.unreadSummary", { count: unread }) : undefined}
        actions={
          <button
            type="button"
            onClick={handleMarkAll}
            disabled={markingAll || unread <= 0}
            className="inline-flex items-center gap-1.5 px-3 h-9 rounded-lg border border-line bg-emphasis/[0.04] hover:bg-emphasis/[0.08] text-xs font-medium text-text-strong transition-colors duration-fast ease-soft disabled:opacity-40 disabled:cursor-not-allowed"
          >
            <CheckCheck className="w-3.5 h-3.5" strokeWidth={1.8} />
            <span>{markingAll ? t("notifications.processing") : t("notifications.markAllRead")}</span>
          </button>
        }
        spacing="compact"
      >
        {actionFailed && (
          <div className="rounded-xl border border-amber-500/25 bg-amber-500/[0.06] p-3 text-amber-600 dark:text-warn-soft text-xs flex items-start gap-2">
            <AlertCircle className="w-4 h-4 shrink-0 mt-0.5" />
            <span className="min-w-0 break-words">{t("notifications.actionFailed")}</span>
          </div>
        )}

        {/* 失败态：只有失败文案 + 重试，没有空态文案。 */}
        {failed && (
          <div className="rounded-xl border border-red-500/25 bg-red-500/10 p-4 text-red-500 dark:text-danger-soft flex items-start gap-2.5">
            <AlertCircle className="w-4 h-4 shrink-0 mt-0.5" />
            <div className="min-w-0 space-y-1">
              <p className="text-sm font-medium break-words">{t("notifications.error")}</p>
              <p className="text-xs opacity-80 break-words">{t("notifications.errorHint")}</p>
              <button
                type="button"
                onClick={() => load(offset)}
                className="mt-1 inline-flex items-center gap-1.5 px-2.5 h-7 rounded-lg border border-red-500/30 font-mono text-[11px] hover:bg-red-500/10 transition-colors duration-fast ease-soft"
              >
                <RotateCw className="w-3 h-3" />
                <span>{t("notifications.retry")}</span>
              </button>
            </div>
          </div>
        )}

        {loading && !data && !failed && (
          <div className="rounded-xl border border-line bg-surfaceSubtle p-8 flex items-center justify-center gap-2 text-text-muted">
            <Loader2 className="w-4 h-4 animate-spin text-primary" />
            <span className="font-mono text-xs">{t("notifications.loading")}</span>
          </div>
        )}

        {/* 空态：仅在"服务端明确回了空列表"时出现（failed / 首次加载中都不进这里）。 */}
        {!failed && !loading && items.length === 0 && (
          <div className="rounded-xl border border-line bg-surfaceSubtle p-8 flex flex-col items-center gap-2 text-center">
            <Inbox className="w-8 h-8 text-text-faint" strokeWidth={1.5} />
            <p className="text-sm font-medium text-text-body">{t("notifications.empty")}</p>
            <p className="text-xs text-text-muted">{t("notifications.emptyHint")}</p>
          </div>
        )}

        {items.length > 0 && (
          <ul className={"space-y-2 transition-opacity duration-fast ease-soft " + (loading ? "opacity-60" : "")}>
            {items.map((n) => {
              const isRead = n.read || readIds.has(n.id);
              const Icon = TYPE_ICON[n.type] || Bell;
              const labelKey = TYPE_LABEL_KEY[n.type];
              const label = t(labelKey || "notifications.type.unknown");
              const rows = notificationRows(n, t);
              const excerpt = n.type === "comment.replied" ? payloadText(n.payload, "excerpt") : "";
              const href = notificationHref(n);
              const busy = busyId === n.id;
              return (
                <li
                  key={n.id}
                  className={
                    "rounded-xl border p-3.5 sm:p-4 transition-colors duration-fast ease-soft " +
                    (isRead ? "border-line bg-surface" : "border-primary/25 bg-primary/[0.04]")
                  }
                >
                  <div className="flex items-start gap-3">
                    <span
                      aria-hidden
                      className={"mt-1.5 w-2 h-2 rounded-full shrink-0 " + (isRead ? "bg-transparent" : "bg-primary")}
                    />
                    <div className="min-w-0 flex-1 space-y-1.5">
                      <div className="flex flex-wrap items-center gap-2">
                        <Icon
                          className={"w-4 h-4 shrink-0 " + (isRead ? "text-text-faint" : "text-primary")}
                          strokeWidth={1.8}
                        />
                        <span
                          className={
                            "text-sm break-words " + (isRead ? "font-medium text-text-body" : "font-semibold text-emphasis")
                          }
                        >
                          {label}
                        </span>
                        {n.count > 1 && (
                          <span className="rounded-full border border-line bg-surfaceSubtle px-2 py-0.5 font-mono text-[10px] text-text-muted">
                            {t("notifications.aggregated", { count: n.count })}
                          </span>
                        )}
                      </div>

                      {rows.length > 0 && (
                        <dl className="flex flex-wrap gap-x-4 gap-y-1 text-xs">
                          {rows.map((row, index) => (
                            <div key={row.label + "-" + index} className="flex items-baseline gap-1.5 min-w-0">
                              <dt className="text-text-faint shrink-0">{row.label}</dt>
                              <dd className="text-text-body break-words min-w-0">{row.value}</dd>
                            </div>
                          ))}
                        </dl>
                      )}

                      {excerpt && (
                        <p className="border-l-2 border-line pl-2 text-xs text-text-muted break-words">{excerpt}</p>
                      )}

                      <div className="flex flex-wrap items-center gap-2 pt-0.5">
                        <time
                          dateTime={n.created_at}
                          title={localDateTime(n.created_at, locale)}
                          className="font-mono text-[10px] text-text-faint"
                        >
                          {isoTimestamp(n.created_at)}
                        </time>
                        {href && (
                          <Link
                            href={href}
                            className="rounded-md px-2 h-6 inline-flex items-center font-mono text-[10px] text-primary hover:bg-primary/10 transition-colors duration-fast ease-soft"
                          >
                            {t("notifications.open")}
                          </Link>
                        )}
                        {!isRead && (
                          <button
                            type="button"
                            onClick={() => handleMarkRead(n)}
                            disabled={busyId !== null || markingAll}
                            className="rounded-md border border-line px-2 h-6 inline-flex items-center gap-1 font-mono text-[10px] text-text-muted hover:text-emphasis hover:bg-surfaceHover transition-colors duration-fast ease-soft disabled:opacity-40 disabled:cursor-not-allowed"
                          >
                            {busy ? <Loader2 className="w-3 h-3 animate-spin" /> : <Check className="w-3 h-3" />}
                            <span>{busy ? t("notifications.processing") : t("notifications.markRead")}</span>
                          </button>
                        )}
                      </div>
                    </div>
                  </div>
                </li>
              );
            })}
          </ul>
        )}

        {!failed && total > 0 && (
          <nav
            aria-label={t("notifications.pagination")}
            className="flex flex-col sm:flex-row items-center justify-between gap-2"
          >
            <span className="font-mono text-[11px] text-text-faint">
              {t("notifications.pageStatus", { page, pages: pageCount })}
            </span>
            <div className="flex items-center gap-2">
              <button
                type="button"
                onClick={() => load(Math.max(0, offset - NOTIFICATION_PAGE_SIZE_DEFAULT))}
                disabled={loading || offset <= 0}
                className="inline-flex items-center gap-1 px-3 h-8 rounded-lg border border-line bg-emphasis/[0.04] hover:bg-emphasis/[0.08] text-xs font-medium text-text-strong transition-colors duration-fast ease-soft disabled:opacity-40 disabled:cursor-not-allowed"
              >
                <ChevronLeft className="w-3.5 h-3.5" strokeWidth={1.8} />
                <span>{t("notifications.prev")}</span>
              </button>
              <button
                type="button"
                onClick={() => load(offset + NOTIFICATION_PAGE_SIZE_DEFAULT)}
                disabled={loading || offset + NOTIFICATION_PAGE_SIZE_DEFAULT >= total}
                className="inline-flex items-center gap-1 px-3 h-8 rounded-lg border border-line bg-emphasis/[0.04] hover:bg-emphasis/[0.08] text-xs font-medium text-text-strong transition-colors duration-fast ease-soft disabled:opacity-40 disabled:cursor-not-allowed"
              >
                <span>{t("notifications.next")}</span>
                <ChevronRight className="w-3.5 h-3.5" strokeWidth={1.8} />
              </button>
            </div>
          </nav>
        )}
      </PageShell>
    </div>
  );
}
