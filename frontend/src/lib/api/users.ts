// 用户主页的三方数据源：账号资料（auth）、目录贡献（catalog）、互动统计与收藏（community）。
// 分属三个服务意味着三处会各自失败：网关分流未就绪、某个服务重启、某个用户在某一边没有记录，
// 都是常态。这里只负责"按各自的真实形状取数与归位"，降级由调用方按来源分别处理——
// 不把"某个上游没响应"折成 0 或空列表，那会把取不到讲成"这个人什么都没做"。
import { fetchApi } from "./client";
import { requireArray, safeCount } from "./fields";

// ── 账号资料：GET /users/:id（匿名可读）──
//
// auth.users 有 display_name / bio 列（空串=未设置）；avatar_url、created_at、
// favorites_public、invite_code 仍不存在。缺席字段调用方按整块不渲染，
// 不要送进 new Date()（会渲染 Invalid Date），也不要当 0 用。
export interface PublicUser {
  id: string;
  username: string;
  /** 空串=未设置，调用方回退用户名。 */
  display_name?: string;
  /** 空串=未设置，调用方不渲染简介区。 */
  bio?: string;
  /** 仅封禁账号带出（后端 omitempty）；封禁是账号状态，不代表资料不存在。 */
  banned?: boolean;
  /** 只有请求者就是本人时才下发。 */
  email?: string;
}

export interface PublicUserProfile {
  user: PublicUser;
  /** 账号服务只拥有 invited_count；其余计数分属目录与互动服务，缺席即"未知"。 */
  stats: { invited_count?: number };
}

export function fetchUserProfile(id: string): Promise<PublicUserProfile> {
  return fetchApi<PublicUserProfile>("/users/" + encodeURIComponent(id));
}

// ── 目录贡献：GET /users/:id/contributions ──
//
// 目录侧只服务这五个 tab（其余取值 400 invalid_tab）：篇目/载体/轨道只能作为修订出现，
// 主题/回复/审计不在目录服务里，别把它们的页签指到这里。
export type ContributionTab = "all" | "revisions" | "works" | "releases" | "artists";

export const CONTRIBUTION_TABS: readonly ContributionTab[] = ["all", "revisions", "works", "releases", "artists"];

export function isContributionTab(tab: string): tab is ContributionTab {
  return (CONTRIBUTION_TABS as readonly string[]).includes(tab);
}

/** 目录的五个计数：works/releases/artists 是"创建的实体数"，revisions 是修订行数，audit_actions 是生命周期动作次数。 */
export interface ContributionStats {
  works_created?: number;
  releases_created?: number;
  artists_created?: number;
  revisions_count?: number;
  audit_actions?: number;
}

export interface ContributionSource {
  kind?: string;
  citation?: string;
  url?: string;
}

export interface ContributionItem {
  id: string;
  tab?: string;
  kind?: string;
  title?: string;
  /** 实体 id（创建项与修订项都指向被编辑的实体）。 */
  target_id?: string;
  version?: number;
  /** create / update：后端字段名是 edit_type，冻结契约里写作 action，两种都认。 */
  action?: string;
  edit_note?: string;
  sources: ContributionSource[];
  state?: string;
  created_at?: string;
  updated_at?: string;
  /** 字段级差异，键为字段路径（attributes.tags 这类下钻一层），形状与 DiffViewer 的输入一致（old/new 可能为 null）。 */
  diff?: Record<string, { old: any; new: any }>;
  /** 后端只给摘要文案时的降级形态（契约里的 diff.summary）。 */
  diff_summary?: string;
}

export interface UserContributions {
  items: ContributionItem[];
  /** 与 tab 无关的全量总数，不是本页条数。 */
  total: number;
  /** 与 tab / 分页无关的五个计数；后端未给时保持 null（调用方显示"未知"）。 */
  stats: ContributionStats | null;
}

export async function fetchUserContributions(
  id: string,
  opts: { tab?: ContributionTab; page?: number; pageSize?: number } = {}
): Promise<UserContributions> {
  const params = new URLSearchParams({
    tab: opts.tab ?? "all",
    page: String(opts.page ?? 1),
    page_size: String(opts.pageSize ?? 20),
  });
  const res = await fetchApi<{ items?: unknown[]; total?: number; stats?: ContributionStats }>(
    "/users/" + encodeURIComponent(id) + "/contributions?" + params.toString()
  );
  // 本文件开头那条约束就在这里落地：上游没响应/换型时**不要**折成 0 或空列表
  // （用户主页对空列表有"暂无贡献"文案，折成空就是把取不到讲成"这个人什么都没做"）。
  const rows = requireArray<unknown>(res?.items, "items");
  return {
    items: rows.map(normalizeContributionItem),
    total: safeCount(res?.total, rows.length),
    stats: res?.stats && typeof res.stats === "object" ? res.stats : null,
  };
}

// normalizeContributionItem 把一条贡献项收敛成页面只认的形状：字段名与
// 服务端 ContributionItem 的 json tag 逐字对齐（不接受任何旧别名字段），
// 来源列表统一成 {kind,citation,url}、差异在"字段级 diff"与"摘要文案"之间二选一。
function normalizeContributionItem(raw: unknown): ContributionItem {
  const row = (raw ?? {}) as Record<string, any>;
  const item: ContributionItem = {
    id: String(row.id ?? ""),
    tab: row.tab,
    kind: row.kind,
    title: row.title,
    target_id: row.target_id,
    version: typeof row.version === "number" ? row.version : undefined,
    action: row.edit_type,
    edit_note: row.edit_note,
    sources: normalizeContributionSources(row),
    state: row.state,
    created_at: row.created_at,
    updated_at: row.updated_at,
  };

  const diff = row.diff;
  if (diff && typeof diff === "object" && !Array.isArray(diff)) {
    const entries = Object.entries(diff as Record<string, any>).filter(
      ([, v]) => v && typeof v === "object" && ("old" in v || "new" in v)
    );
    if (entries.length > 0) {
      item.diff = Object.fromEntries(entries) as ContributionItem["diff"];
    } else if (typeof (diff as Record<string, unknown>).summary === "string") {
      item.diff_summary = String((diff as Record<string, unknown>).summary);
    }
  }
  if (!item.diff_summary && typeof row.summary === "string" && row.summary.trim() !== "") {
    item.diff_summary = row.summary;
  }
  return item;
}

function normalizeContributionSources(row: Record<string, any>): ContributionSource[] {
  const out: ContributionSource[] = [];
  if (Array.isArray(row.sources)) {
    for (const s of row.sources) {
      if (typeof s === "string" && s.trim() !== "") out.push({ url: s });
      else if (s && typeof s === "object") out.push({ kind: s.kind, citation: s.citation, url: s.url });
    }
  }
  return out.filter((s) => Boolean(s.url) || Boolean(s.citation));
}

// ── 互动统计：GET /users/:id/stats（匿名可读）──
//
// community 侧只给这三个数字（口径见其 README：短评不计入任何一项）；
// 账号与目录的计数不在这里，缺失时是"来源没有"，不是 0。
export interface CommunityUserStats {
  topics_created?: number;
  comments_created?: number;
  favorites_count?: number;
}

export async function fetchUserCommunityStats(id: string): Promise<CommunityUserStats> {
  const res = await fetchApi<{ stats?: CommunityUserStats }>("/users/" + encodeURIComponent(id) + "/stats");
  return res.stats ?? {};
}
