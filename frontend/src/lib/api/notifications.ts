// 站内通知（网关 /api/notifications*，服务端实现见后端 notifications_http.go）。
//
// 失败一律抛出（fetchApi 保留 HTTP 状态码）：通知列表界面上有独立的"加载失败"分支，
// 把"取不到"讲成"没有通知"是本项目刚修过的一类缺陷（口径见 lib/api/fields.ts 头部注释
// 与 docs-local/dev-log.md 的 v0.2.4 段），所以这里对 items 不做任何空数组兜底。
import { fetchApi } from "./client";
import { requireArray, safeCount } from "./fields";

export type NotificationType =
  | "comment.replied"
  | "entity.included"
  | "entity.review_approved"
  | "entity.review_rejected"
  | "import.completed";

export type NotificationSubjectType = "topic" | "entity" | "commit" | "pull_request" | "import" | "";

/**
 * 一行通知。payload 是各类型自己的键值袋（键随类型变化，且可能缺键），
 * 取值方一律按 possibly-undefined 处理，不要假定某个键一定在。
 */
export interface Notification {
  id: string;
  type: NotificationType;
  /** 系统事件（无操作者）时为空串：不要当用户名渲染，也不要造一个假名字。 */
  actor_id: string;
  actor_name: string;
  subject_type: NotificationSubjectType;
  subject_id: string;
  payload: Record<string, unknown>;
  /** >=1：同一聚合键的多条事件被服务端合并成一行，count 是合并条数。 */
  count: number;
  read: boolean;
  /** RFC3339。 */
  created_at: string;
  updated_at: string;
}

export interface NotificationListResponse {
  items: Notification[];
  total: number;
  unread: number;
}

export interface MarkNotificationReadResult {
  ok: boolean;
  unread: number;
}

export interface MarkAllNotificationsReadResult {
  ok: boolean;
  updated: number;
  unread: number;
}

/** 契约给的缺省与上限：limit 缺省 20、上限 100（超出会被服务端拒绝或截断，这里先夹住）。 */
export const NOTIFICATION_PAGE_SIZE_DEFAULT = 20;
export const NOTIFICATION_PAGE_SIZE_MAX = 100;

/**
 * 已读状态变化事件名：通知页改完已读后广播一次，顶栏角标据此**立刻**刷新，
 * 这样角标就不需要靠高频轮询去追（顶栏的定时刷新最短间隔 >=60s）。
 */
export const NOTIFICATIONS_CHANGED_EVENT = "mf:notifications-changed";

export function emitNotificationsChanged(): void {
  if (typeof window === "undefined") return;
  window.dispatchEvent(new Event(NOTIFICATIONS_CHANGED_EVENT));
}

function clampInt(value: unknown, fallback: number, min: number, max: number): number {
  const n = typeof value === "number" && Number.isFinite(value) ? Math.floor(value) : fallback;
  if (n < min) return min;
  if (n > max) return max;
  return n;
}

/** count 缺失或换型时按 1 处理：宁可不显示聚合条数，也不把坏值当条数展示。 */
function normalizeNotification(raw: Record<string, any>): Notification {
  const payload = raw?.payload;
  return {
    id: String(raw?.id ?? ""),
    type: (typeof raw?.type === "string" ? raw.type : "") as NotificationType,
    actor_id: typeof raw?.actor_id === "string" ? raw.actor_id : "",
    actor_name: typeof raw?.actor_name === "string" ? raw.actor_name : "",
    subject_type: (typeof raw?.subject_type === "string" ? raw.subject_type : "") as NotificationSubjectType,
    subject_id: typeof raw?.subject_id === "string" ? raw.subject_id : "",
    // payload 只是行内的次要信息（没有"空"文案），非对象时退回空袋；取值方本来就按缺键处理。
    payload: payload && typeof payload === "object" && !Array.isArray(payload) ? (payload as Record<string, unknown>) : {},
    count: safeCount(raw?.count, 1),
    read: raw?.read === true,
    created_at: typeof raw?.created_at === "string" ? raw.created_at : "",
    updated_at: typeof raw?.updated_at === "string" ? raw.updated_at : "",
  };
}

/** GET /notifications?limit=&offset= —— 按时间倒序，未登录 401 authentication_required。 */
export async function fetchNotifications(
  params: { limit?: number; offset?: number } = {}
): Promise<NotificationListResponse> {
  const limit = clampInt(params.limit, NOTIFICATION_PAGE_SIZE_DEFAULT, 1, NOTIFICATION_PAGE_SIZE_MAX);
  const offset = clampInt(params.offset, 0, 0, Number.MAX_SAFE_INTEGER);
  const res = await fetchApi<Record<string, any>>(`/notifications?limit=${limit}&offset=${offset}`);
  // 非数组即抛 invalid_response: items：界面上有"暂无通知"文案，漂移不能伪装成空列表。
  const items = requireArray<Record<string, any>>(res?.items, "items").map(normalizeNotification);
  return {
    items,
    total: safeCount(res?.total, items.length),
    unread: safeCount(res?.unread, 0),
  };
}

/**
 * GET /notifications/unread-count —— 只回未读总数的轻量端点（供顶栏角标）。
 * 直接回数字方便调用方；请求失败会抛出，由调用方决定要不要静默。
 */
export async function fetchUnreadCount(): Promise<number> {
  const res = await fetchApi<Record<string, any>>("/notifications/unread-count");
  return safeCount(res?.unread, 0);
}

/**
 * POST /notifications/:id/read —— 不是自己的或不存在时抛 404 not_found（ApiError.status === 404）。
 * 返回服务端算好的未读总数，调用方不必再猜。
 */
export async function markNotificationRead(id: string): Promise<MarkNotificationReadResult> {
  const res = await fetchApi<Record<string, any>>(`/notifications/${encodeURIComponent(id)}/read`, { method: "POST" });
  return { ok: res?.ok === true, unread: safeCount(res?.unread, 0) };
}

/** POST /notifications/read-all —— 返回本次更新行数与未读总数（应为 0）。 */
export async function markAllNotificationsRead(): Promise<MarkAllNotificationsReadResult> {
  const res = await fetchApi<Record<string, any>>("/notifications/read-all", { method: "POST" });
  return {
    ok: res?.ok === true,
    updated: safeCount(res?.updated, 0),
    unread: safeCount(res?.unread, 0),
  };
}
