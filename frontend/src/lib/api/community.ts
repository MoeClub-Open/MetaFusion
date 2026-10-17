import { fetchApi } from "./client";
import { requireArray, safeCount } from "./fields";
import type { Tag } from "./catalog";
import type { User } from "./client";
import type { Entity } from "@/components/catalog/api";

// ── 收藏 ──
// target_type 直接就是实体 kind（固定八种骨架），不再使用旧词表别名。
export type FavoriteTargetType =
  | "agent"
  | "collection"
  | "work"
  | "content_unit"
  | "expression"
  | "release"
  | "medium"
  | "track";

export interface FavoriteItem {
  id: string;
  target_type: FavoriteTargetType;
  target_id: string;
  created_at: string;
  /** 被收藏实体本身；展示按 target_type 与实体的 translations/kind 渲染。 */
  entity?: Entity;
}

/** 切换收藏状态，返回切换后是否已收藏 */

export async function toggleFavorite(targetType: FavoriteTargetType, targetId: string): Promise<boolean> {
  const res = await fetchApi<{ favorited: boolean }>("/favorites/toggle", {
    method: "POST",
    body: JSON.stringify({ target_type: targetType, target_id: targetId }),
  });
  return res.favorited;
}

/** 批量查询当前用户对若干目标是否已收藏 */

export async function fetchFavoriteStatus(targetType: FavoriteTargetType, targetIds: string[]): Promise<Set<string>> {
  if (targetIds.length === 0) return new Set();
  const res = await fetchApi<{ favorited: string[] }>(
    `/favorites/status?target_type=${encodeURIComponent(targetType)}&target_ids=${encodeURIComponent(targetIds.join(","))}`
  );
  return new Set(res.favorited || []);
}

/** 拉取收藏列表（本人或公开用户），visible=false 表示对方未公开 */

export async function fetchFavorites(
  userIdOrMine: string,
  opts: { targetType?: FavoriteTargetType; page?: number; pageSize?: number } = {}
): Promise<{ items: FavoriteItem[]; total: number; visible: boolean }> {
  const endpoint = userIdOrMine === "mine" ? "/favorites/mine" : `/users/${userIdOrMine}/favorites`;
  const params = new URLSearchParams();
  if (opts.targetType) params.set("target_type", opts.targetType);
  if (opts.page) params.set("page", String(opts.page));
  if (opts.pageSize) params.set("page_size", String(opts.pageSize));
  const qs = params.toString();
  const result = await fetchApi<{ items?: unknown; total?: unknown; visible?: unknown }>(`${endpoint}${qs ? `?${qs}` : ""}`);
  if (typeof result?.visible !== "boolean") throw new Error("invalid_response: visible");
  const items = requireArray<FavoriteItem>(result.items, "items");
  if (typeof result.total !== "number" || !Number.isSafeInteger(result.total) || result.total < 0) {
    throw new Error("invalid_response: total");
  }
  return { items, total: result.total, visible: result.visible };
}

export interface ForumPost {
  id: string;
  topic_id: string;
  post_number: number;
  user_id: string;
  content: string;
  reply_to_post_number?: number | null;
  created_at: string;
  updated_at?: string;
  user?: User;
}

export interface DiscussionTopic { is_pinned?:boolean; pinned_at?:string; 
  id: string;
  user_id: string;
  board_code: string;
  /** 锚定的目录实体（若评论/主题挂在某个实体上）；标题与 kind 由后端补齐。 */
  entity_id?: string;
  entity_title?: string;
  entity_kind?: string;
  title: string;
  content: string;
  view_count: number;
  reply_count: number;
  created_at: string;
  updated_at: string;
  user?: User;
  posts?: ForumPost[];
  tags?: Tag[];
}

export interface CreateTopicPayload {
  board_code: string;
  title: string;
  content: string;
  entity_id?: string;
  tag_ids?: number[];
  tag_names?: string[];
}

export interface CreatePostPayload {
  content: string;
  reply_to_post_number?: number | null;
}

// ── 实体短评与关联合集（community 服务 /community/entities/:id/*）──
//
// 短评在服务端复用评论板块的 topics 行（board_code=comment、锚定 entity_id、无独立标题），
// 所以 GET 回来的就是评论行本身。以前这两个端点由详情页自己拼 URL 直调、POST 由详情页自己
// 拼请求体，写失败时还在前端自造一条"已发表"的评论——这里收成与其它互动端点同级的包装。
export interface EntityComment {
  id: string;
  author_id: string;
  author_name: string;
  body: string;
  created_at: string;
  /** 列表响应不带这两项，逐条读取（GET /community/posts/:id）时才回。 */
  entity_id?: string;
  entity_title?: string;
  entity_kind?: string;
}

/** 关联合集：服务端经目录关系取的 collection 邻居（最多 20 条，只要已发布），只有 id 与标题。 */
export interface EntityCollectionRef {
  id: string;
  title: string;
}

/** 某实体的短评列表：公开可读，服务端按 created_at DESC 取最新 100 条（无分页参数）。 */
export async function fetchEntityPosts(entityId: string): Promise<EntityComment[]> {
  const res = await fetchApi<{ items?: EntityComment[] }>(
    `/community/entities/${encodeURIComponent(entityId)}/posts`
  );
  // 详情页/作品页对空列表都有"暂无…"文案：漂移必须抛错走失败态，只有确认为数组才算"没有评论"。
  return requireArray<EntityComment>(res?.items, "items");
}

/** 某实体被哪些合集关联（服务端回查目录，实体对当前身份不可见时是 404 not_found）。 */
export async function fetchEntityCollections(entityId: string): Promise<EntityCollectionRef[]> {
  const res = await fetchApi<{ items?: EntityCollectionRef[] }>(
    `/community/entities/${encodeURIComponent(entityId)}/collections`
  );
  // 合集空列表在详情页有"暂无"文案：同上，漂移抛错而不是伪装成"没有合集"。
  return requireArray<EntityCollectionRef>(res?.items, "items");
}

/**
 * 发一条短评（请求体字段名是 body，与论坛回帖的 content 不同）。
 *
 * 返回 null 表示服务端回了 2xx 但没给 item **且没有抛错**——调用方此时应回读列表，
 * 绝不能在前端自造一条评论：伪造的 id/时间会让人以为已经落库，刷新后凭空消失。
 */
export async function createEntityComment(
  entityId: string,
  body: string
): Promise<EntityComment | null> {
  const res = await fetchApi<{ ok?: boolean; item?: EntityComment }>(
    `/community/entities/${encodeURIComponent(entityId)}/posts`,
    { method: "POST", body: JSON.stringify({ body }) }
  );
  return res?.item ?? null;
}

// 私信（community 服务 /api/messages/with/:id 的逐字契约）：
// items 里就是这四个字段 + id，**没有内容字段 content、没有 is_read**——
// 已读回执的 read_at 列在库里已预留但两个端点都不读不写，前端不许诺不存在的状态。
export interface DirectMessage {
  id: string;
  sender_id: string;
  recipient_id: string;
  body: string;
  created_at: string;
}

/**
 * 会话列表行（GET /api/messages/conversations）。
 *
 * 只有 peer_id，**没有对方用户名/头像**：账号资料归账号服务，互动服务不查它的库，
 * 展示名由调用方自己按 fetchUserProfile(peer_id) 解析；解析失败也不能因此让整行变成错误态。
 * last_message 是这段会话里最新的一条（服务端按 created_at DESC, id DESC 排序，与列表同序）。
 */
export interface ConversationItem {
  peer_id: string;
  last_message: DirectMessage;
  unread_count: number;
}

export interface ForumBoard {
  code: string;
  names?: Record<string, string>;
  descriptions?: Record<string, string>;
  /** i18n keys — 复用已存在的 board.* 翻译（board.all / board.announcement / ...） */
  nameKey: string;
  descKey: string;
  color: string;
  bgColor: string;
  borderColor: string;
  icon?: string;
  sort_order?: number;
  is_enabled?: boolean;
  show_in_feed?: boolean;
}

const BOARD_PALETTE: Record<string, { color: string; bgColor: string; borderColor: string }> = {
  emerald: { color: "text-success", bgColor: "bg-emerald-500/15", borderColor: "border-emerald-500/30" },
  amber: { color: "text-warn", bgColor: "bg-amber-500/15", borderColor: "border-amber-500/30" },
  sky: { color: "text-info", bgColor: "bg-sky-500/15", borderColor: "border-sky-500/30" },
  purple: { color: "text-alt", bgColor: "bg-purple-500/15", borderColor: "border-purple-500/30" },
  cyan: { color: "text-info", bgColor: "bg-cyan-500/15", borderColor: "border-cyan-500/30" },
  rose: { color: "text-danger", bgColor: "bg-rose-500/15", borderColor: "border-rose-500/30" },
  indigo: { color: "text-alt", bgColor: "bg-indigo-500/15", borderColor: "border-indigo-500/30" },
  teal: { color: "text-success", bgColor: "bg-teal-500/15", borderColor: "border-teal-500/30" },
};

interface ForumBoardResponse {
  code: string;
  names: Record<string, string>;
  descriptions: Record<string, string>;
  color: string;
  icon: string;
  sort_order: number;
  is_enabled: boolean;
  show_in_feed: boolean;
}

export function normalizeBoard(raw: ForumBoardResponse): ForumBoard {
  if (!raw || typeof raw.code !== "string" || !raw.code.trim()) {
    throw new Error("invalid_response: boards.code");
  }
  if (
    typeof raw.is_enabled !== "boolean" || typeof raw.show_in_feed !== "boolean" ||
    !Number.isSafeInteger(raw.sort_order)
  ) {
    throw new Error("invalid_response: boards.settings");
  }
  const palette = BOARD_PALETTE[raw.color] || BOARD_PALETTE.emerald;
  return {
    code: raw.code,
    nameKey: `board.${raw.code}`,
    descKey: `board.${raw.code}Desc`,
    names: raw.names,
    descriptions: raw.descriptions,
    color: palette.color,
    bgColor: palette.bgColor,
    borderColor: palette.borderColor,
    icon: raw.icon || "BookOpen",
    sort_order: raw.sort_order,
    is_enabled: raw.is_enabled,
    show_in_feed: raw.show_in_feed,
  };
}

/** 板块显示名：有 translator 时以 board.* 词条为权威，否则走 names 的语言回退链。 */

export function boardDisplayName(board: ForumBoard, locale?: string, t?: (k: string) => string): string {
  return localizedBoardText(board.nameKey, board.code, board.names, locale, t);
}

/** 板块多语言描述：有 translator 时以 board.*Desc 词条为权威，否则走 descriptions 的语言回退链。 */

export function boardDisplayDesc(board: ForumBoard, locale?: string, t?: (k: string) => string): string {
  return localizedBoardText(board.descKey, board.code, board.descriptions, locale, t, "");
}

function localizedBoardText(
  key: string,
  code: string,
  values: Record<string, string> | undefined,
  locale?: string,
  t?: (k: string) => string,
  fallback = code
): string {
  if (t) {
    const translated = (() => { try { const v = t(key); return v !== key ? v : ""; } catch { return ""; } })();
    if (translated) return translated;
  }
  const loc = locale || "zh-CN";
  if (values) {
    if (values[loc]) return values[loc];
    const prefix = loc.slice(0, 2);
    for (const [k, v] of Object.entries(values)) {
      if (k.startsWith(prefix) && v) return v;
    }
    if (values["zh-CN"]) return values["zh-CN"];
    if (values["en-US"]) return values["en-US"];
    for (const v of Object.values(values)) {
      if (v) return v;
    }
  }
  return fallback;
}

export const ALL_FORUM_BOARD: ForumBoard = {
  code: "all",
  nameKey: "board.all",
  descKey: "board.allDesc",
  color: "text-text-body",
  bgColor: "bg-gray-500/20",
  borderColor: "border-gray-500/40",
  icon: "Layers",
};

let boardsCache: ForumBoard[] | null = null;

let boardsCacheAt = 0;

const BOARDS_TTL_MS = 5 * 60 * 1000;

export async function fetchBoards(opts?: { force?: boolean }): Promise<ForumBoard[]> {
  const now = Date.now();
  if (!opts?.force && boardsCache && now - boardsCacheAt < BOARDS_TTL_MS) return boardsCache;
  const raw = await fetchApi<unknown>("/community/boards", { signal: AbortSignal.timeout(15000) });
  const normalized = requireArray<ForumBoardResponse>(raw, "boards").map(normalizeBoard);
  const result = [ALL_FORUM_BOARD, ...normalized.filter((b) => b.code !== "all")];
  boardsCache = result;
  boardsCacheAt = now;
  return result;
}

export function getBoardSync(code: string, boards: ForumBoard[]): ForumBoard | undefined {
  return boards.find((b) => b.code === code);
}

/**
 * 论坛分享：优先使用系统原生分享面板，失败或不支持时降级为剪贴板复制
 * 返回 'shared' | 'copied' | 'failed' 供调用方展示反馈
 */

export async function shareContent(opts: { title: string; text?: string; url: string }): Promise<'shared' | 'copied' | 'failed'> {
  const url = opts.url;
  // 1) 尝试 Web Share API (移动端最友好)
  try {
    if (typeof navigator !== 'undefined' && (navigator as any).share) {
      const canShare = !(navigator as any).canShare || (navigator as any).canShare({ title: opts.title, text: opts.text, url });
      if (canShare) {
        await (navigator as any).share({ title: opts.title, text: opts.text, url });
        return 'shared';
      }
    }
  } catch (e: any) {
    // 用户取消分享（AbortError）视为未失败，不再尝试剪贴板
    if (e && (e.name === 'AbortError' || String(e.message || '').includes('Abort'))) {
      return 'failed';
    }
  }

  // 2) 降级：剪贴板复制
  try {
    if (typeof navigator !== 'undefined' && navigator.clipboard?.writeText) {
      await navigator.clipboard.writeText(url);
      return 'copied';
    }
  } catch {}

  return 'failed';
}

export function buildShareUrl(topicId: string, highlightPostId?: string): string {
  if (typeof window === 'undefined') return `/community/${topicId}`;
  const base = window.location.origin;
  const hash = highlightPostId ? `#post-${highlightPostId}` : '';
  return `${base}/community/${topicId}${hash}`;
}

/**
 * 置顶/取消置顶（PUT /api/community/topics/:id/pin）。
 *
 * 请求体**只有 `pinned` 一个字段且必填**（服务端用指针判空，缺字段是 400 invalid_payload，
 * 没有独立的 unpin 端点）；用 `pinned:false` 取消。成功回的是 topic map（不含 tags/posts），
 * 所以调用方拿 is_pinned 更新本地状态即可，不必重取整页。
 *
 * 已知的 404 语义：主题若挂在 `comment` 板块（实体短评）也走这条 SQL，服务端刻意让它与
 * "主题不存在"同码——调用方无法区分，提示文案要同时覆盖两种可能。
 */
export async function setTopicPinned(topicId: string, pinned: boolean): Promise<DiscussionTopic> {
  return fetchApi<DiscussionTopic>(`/community/topics/${encodeURIComponent(topicId)}/pin`, {
    method: "PUT",
    body: JSON.stringify({ pinned }),
  });
}

export async function createTopic(payload: CreateTopicPayload): Promise<DiscussionTopic> {
  return fetchApi<DiscussionTopic>("/community/topics", {
    method: "POST",
    body: JSON.stringify(payload),
  });
}

export async function createPost(topicId: string, payload: CreatePostPayload): Promise<ForumPost> {
  return fetchApi<ForumPost>(`/community/topics/${topicId}/posts`, {
    method: "POST",
    body: JSON.stringify(payload),
  });
}

/**
 * 会话分页：**第一页是最新的**（服务端按 created_at DESC, id DESC），往后翻是更早的；
 * total 是整段会话的条数，不随窗口变化。调用方据此决定"加载更早的消息"。
 */
export async function fetchDirectMessages(
  userId: string,
  page = 1,
  pageSize = 20
): Promise<{ items: DirectMessage[]; total: number }> {
  const res = await fetchApi<{ items?: DirectMessage[]; total?: number }>(
    "/messages/with/" + encodeURIComponent(userId) + "?page=" + page + "&page_size=" + pageSize
  );
  const items = requireArray<DirectMessage>(res?.items, "items");
  return {
    items,
    // total 不再 ||0：缺失或异常值（NaN/负数/超大）时退回"本次已确认拿到的条数"这个下界，
    // 而不是谎报 0 条（私信弹窗靠它决定"还能加载更早的"，0 会直接掐掉翻页入口）。
    total: safeCount(res?.total, items.length),
  };
}

/** 发信：请求体字段名是 body（不是 content），响应把新消息包在 message 里。 */
export async function sendDirectMessage(userId: string, body: string): Promise<DirectMessage> {
  const res = await fetchApi<{ message: DirectMessage }>("/messages/with/" + encodeURIComponent(userId), {
    method: "POST",
    body: JSON.stringify({ body }),
  });
  return res.message;
}

/**
 * 会话列表（GET /api/messages/conversations）：按 last_message.created_at DESC, id DESC，
 * 第一页就是最新的会话；page/page_size 口径与逐会话消息一致（缺省 20、上限 100，越界静默收敛）。
 */
export async function fetchConversations(
  page = 1,
  pageSize = 20
): Promise<{ items: ConversationItem[]; total: number }> {
  const res = await fetchApi<{ items?: ConversationItem[]; total?: number }>(
    "/messages/conversations?page=" + page + "&page_size=" + pageSize
  );
  // 会话列表界面上有"还没有私信"空态文案：items 换型/缺失必须抛错走失败态，
  // 不能折成空数组把"取不到"讲成"没有会话"（与 fetchDirectMessages 同一口径）。
  const items = requireArray<ConversationItem>(res?.items, "items");
  return { items, total: safeCount(res?.total, items.length) };
}

/**
 * 全站未读数（GET /api/messages/unread），导航角标用。
 *
 * 字段非法时**抛错**而不是回 0：角标只有"有 N 条未读"和"不显示"两种状态，
 * 把取不到渲染成 0 会让用户以为没有新消息，比干脆不显示更糟。调用方接住错误即隐藏角标。
 */
export async function fetchUnreadMessageCount(): Promise<number> {
  const res = await fetchApi<{ unread_count?: unknown }>("/messages/unread");
  const raw = res?.unread_count;
  if (typeof raw !== "number" || !Number.isFinite(raw) || raw < 0 || raw > Number.MAX_SAFE_INTEGER) {
    throw new Error("invalid_response: unread_count");
  }
  return Math.floor(raw);
}

/**
 * 把来自 userId 的全部未读标记为已读（PUT /api/messages/with/:id/read），返回本次标记条数。
 *
 * marked 只作乐观更新用：字段缺失时退回 0（"没标记到"）而不抛错——已读回执是次要副作用，
 * 不该因为一个计数不合法就打断正在进行的阅读。
 */
export async function markConversationRead(userId: string): Promise<number> {
  const res = await fetchApi<{ marked?: number }>(
    "/messages/with/" + encodeURIComponent(userId) + "/read",
    { method: "PUT" }
  );
  return safeCount(res?.marked, 0);
}

/**
 * 收件设置（GET /api/messages/settings）：响应是扁平的一个布尔，不套信封。
 *
 * 字段缺失或换型必须抛错走失败态——把"取不到"画成"默认接收"就是在编造一个
 * 不存在的设置状态（与会话列表 items 必须 requireArray 同一口径）。
 */
export async function fetchMessageSettings(): Promise<boolean> {
  const res = await fetchApi<{ accept_from_strangers?: unknown }>("/messages/settings");
  const raw = res?.accept_from_strangers;
  if (typeof raw !== "boolean") {
    throw new Error("invalid_response: accept_from_strangers");
  }
  return raw;
}

/**
 * 改收件设置（PUT /api/messages/settings {"accept_from_strangers":bool}），
 * 以服务端回读为准；回读非法同样抛错，调用方回滚到旧值。
 */
export async function updateMessageSettings(accept: boolean): Promise<boolean> {
  const res = await fetchApi<{ accept_from_strangers?: unknown }>("/messages/settings", {
    method: "PUT",
    body: JSON.stringify({ accept_from_strangers: accept }),
  });
  const raw = res?.accept_from_strangers;
  if (typeof raw !== "boolean") {
    throw new Error("invalid_response: accept_from_strangers");
  }
  return raw;
}
