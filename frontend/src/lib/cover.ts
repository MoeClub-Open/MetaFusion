/**
 * 封面比例推断。
 * 作品没有 media_type 字段，分类依赖标签 + 虚拟货架 channel，
 * 这里仅根据标签关键词猜测封面惯例比例：
 *   音乐类（专辑/单曲/EP/OST）→ 1:1
 *   影视海报类（电影/剧集/动画）→ 2:3
 *   书籍类（小说/漫画）→ 3:4
 */

export type CoverTagInput = string | { name?: string } | null | undefined;

/** 调用方显式指定的展示比例（如 "1:1"/"2:3"/"3:4"）：不是数据库字段，空/未知 = 走推断与自然比例 */
export function parseManualRatio(aspect?: string | null): number | null {
  if (!aspect) return null;
  const m = /^(\d+(?:\.\d+)?)\s*:\s*(\d+(?:\.\d+)?)$/.exec(aspect.trim());
  if (!m) return null;
  const w = parseFloat(m[1]);
  const h = parseFloat(m[2]);
  if (!isFinite(w) || !isFinite(h) || w <= 0 || h <= 0) return null;
  return w / h;
}

const SQUARE_EXACT = [
  "album", "single", "ep", "lp", "cd", "ost",
  "专辑", "单曲", "迷你专辑", "原声带", "原声", "配乐", "唱片", "音乐",
];

const POSTER_KEYWORDS = [
  "movie", "film", "series", "anime", "tv", "theatrical", "ova", "special",
  "电影", "影片", "剧场版", "映画", "动画", "動畫", "剧集", "電視劇", "电视剧", "特摄", "特攝", "海报", "海報",
];

const BOOK_KEYWORDS = [
  "novel", "book", "comic", "manga", "light novel",
  "小说", "小說", "轻小说", "輕小說", "漫画", "漫畫", "书籍", "書籍", "文库", "文庫", "画集", "畫集",
];

function normalizeTags(tags?: CoverTagInput[]): string[] {
  return (tags || [])
    .map((t) => (typeof t === "string" ? t : t?.name || ""))
    .map((n) => n.trim().toLowerCase())
    .filter(Boolean);
}

/**
 * 返回宽高比数值（width / height），可直接用于 CSS aspect-ratio。
 */
export function inferCoverRatio(tags?: CoverTagInput[]): number {
  const names = normalizeTags(tags);
  if (names.length === 0) return 3 / 4;

  const hasSquare = names.some(
    (n) => SQUARE_EXACT.includes(n) || (n.length > 4 && SQUARE_EXACT.some((k) => n.includes(k)))
  );
  if (hasSquare) return 1;

  const hitAny = (keywords: string[]) =>
    names.some((n) => keywords.some((k) => n === k || n.includes(k)));

  if (hitAny(POSTER_KEYWORDS)) return 2 / 3;
  if (hitAny(BOOK_KEYWORDS)) return 3 / 4;
  return 3 / 4;
}

/**
 * 封面容器允许的比例区间（宽/高）。
 * 下限 2:3 保住竖版海报/书封惯例；上限放宽到 2:1 容纳影视横剧照与横幅图。
 * 图片一律 object-fit: contain：区间内的图完整显示，超出区间的图只在边界留衬底，
 * 任何比例都不裁切主体（旧上限 1:1 配 cover 会把 2.39:1 的剧照裁掉约七成宽度）。
 */
export const MIN_COVER_ASPECT = 2 / 3;
export const MAX_COVER_ASPECT = 2;

/**
 * 网格封面加载中与无图时的占位比例；真实封面加载后按图片自身比例展示。
 */
export const GRID_COVER_ASPECT = 3 / 4;

export function clampCoverRatio(ratio: number): number {
  if (!isFinite(ratio) || ratio <= 0) return GRID_COVER_ASPECT;
  return Math.min(MAX_COVER_ASPECT, Math.max(MIN_COVER_ASPECT, ratio));
}

/**
 * 封面派生：实体自己没有 `pictures` 时，按"直接关联实体"顺序借用一张。
 *
 * 为什么必须把 `origin` 一路带到界面上：目录里曾出现过把整张借来的图当成本实体自己的
 * 封面来标注的情况（把所属发行的通用美术写成"该曲官方封面"、把官网首页横幅写成
 * "官方主视觉"）。展示侧借用是合理的兜底，**不写明借自哪里**才是问题，
 * 所以 `origin !== "self"` 时调用方必须渲染 `CoverOriginNote`。
 *
 * 这里只认结构，不 import Entity：列表与详情用的是同一个 DTO，
 * 关联实体只有 id 时传 `undefined` 即可，不会误判成"有封面"。
 */
export type CoverOrigin = "self" | "release" | "work" | "subject_work" | "mother_work" | "none";

/**
 * 一张图的展示侧最小结构（与 `components/catalog/api.ts` 里 `Entity.pictures` 的元素兼容）。
 *
 * 刻意不 import 那个类型：本模块同时被服务端模块（`lib/seo.ts`）与浏览器组件使用，
 * 而 api.ts 带着客户端取数依赖。字段全部可选，因为历史 JSON 里 `role` / `asset_id`
 * 可能整个键都不存在（后端 omitempty），读取端不能假设它们在。
 */
export interface CoverPicture {
  url?: string | null;
  /** 图注：按语种分组的多语言名，展示侧用 resolveLocalizedName 解析。 */
  caption?: Record<string, string> | null;
  /** 这张图自身的时间（拍摄/发布/改版）。**只作展示元信息，不参与排序**。 */
  taken_at?: string | null;
  /** 图像版本/活动期名称；与实体修订版本无关。 */
  version_label?: Record<string, string> | null;
  /** 已知使用区间；不会自动取代首图作为当前封面。 */
  usage_period?: { begin?: string | null; end?: string | null } | null;
  /** 图片用途码，取值是 definitions 的 `picture_role` 词表；空 = 未声明用途（存量图全部如此）。 */
  role?: string | null;
  /** 自托管封面在存储服务里的 assets UUID；此时 url 指向 `/api/storage/assets/<uuid>/content`。 */
  asset_id?: string | null;
  source?: { kind?: string | null; citation?: string | null; url?: string | null } | null;
}

export interface CoverBearing {
  id?: string;
  title?: string;
  pictures?: Array<CoverPicture | null> | null;
}

/**
 * 单实体图片张数上限：与后端 `validation.go` 的 `maxPictures` 同一口径。
 * 上限来自"每张都随 document 落库、并再复制进每次保存的 revisions 快照"，
 * 超了服务端直接拒 `too_many_pictures`；编辑器据此禁用添加按钮。
 */
export const MAX_ENTITY_PICTURES = 40;

/** 图片用途码的词表码（definitions.vocabularies 的键）：用途标签只从这里解析，不硬编码语言。 */
export const PICTURE_ROLE_VOCABULARY = "picture_role";

/**
 * 「数组顺序就是展示顺序、`pictures[0]` 即封面」这个契约在前端的**唯一落点**。
 *
 * 服务端保存时不重排，`taken_at` 只是这张图自身的时间元信息、不参与排序，所以换封面
 * 的动作是"把它挪到首位"。此前 13 处各自写死 `pictures?.[0]?.url`，等于把同一条契约
 * 复制了 13 遍——改判定（比如将来真要显式主图字段）就得赌 13 处都找齐。
 * 现在展示侧一律经由本模块取封面：判定只写成下面的 `COVER_PICTURE_INDEX` 一处，
 * 消费方不再自己索引 `[0]`，也不再各自抄那个字面量 0。
 */
export const COVER_PICTURE_INDEX = 0;

/**
 * 返回实体的封面图对象（= 首位那张）。数组下标 0 就是契约本身，不做"挑第一张有 url 的图"
 * 这种二次判定：那种兜底会把作者定的顺序悄悄改掉，正是这条契约要消灭的行为。
 * 首张的 url 为空时仍返回它——调用方要区分"没收录图片"（pictures 为空）与"首张没地址"。
 */
export const coverPicture = (entity?: CoverBearing | null): CoverPicture | null =>
  entity?.pictures?.[COVER_PICTURE_INDEX] ?? null;

/** 封面地址 = 首张图的 url（trim 后空串表示本实体没有收录图片）。 */
export const coverUrl = (entity?: CoverBearing | null): string =>
  String(coverPicture(entity)?.url || "").trim();

export interface ResolvedCover<T extends CoverBearing = CoverBearing> {
  url: string;
  origin: CoverOrigin;
  /** 提供这张图的实体（origin 为 self 时就是本实体；none 时为 undefined） */
  from?: T;
}

export type CoverChainHop<T extends CoverBearing = CoverBearing> = {
  origin: Exclude<CoverOrigin, "self" | "none">;
  entity: T | null | undefined;
};

/** 只看实体自己收录的封面（首张图）；空串表示没有。`resolveCover` 的借用链据此逐级判定。 */
export const ownCoverUrl = coverUrl;

export function resolveCover<T extends CoverBearing>(
  self: T | null | undefined,
  chain: CoverChainHop<T>[] = [],
): ResolvedCover<T> {
  const own = ownCoverUrl(self);
  if (own) return { url: own, origin: "self", from: self ?? undefined };
  for (const hop of chain) {
    const url = ownCoverUrl(hop.entity);
    if (url) return { url, origin: hop.origin, from: hop.entity ?? undefined };
  }
  return { url: "", origin: "none" };
}
