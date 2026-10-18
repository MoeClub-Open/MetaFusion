// 用户主页的三方数据源：账号资料（auth）、目录贡献（catalog）、互动统计与收藏（community）。
// 分属三个服务意味着三处会各自失败：网关分流未就绪、某个服务重启、某个用户在某一边没有记录，
// 都是常态。这里只负责"按各自的真实形状取数与归位"，降级由调用方按来源分别处理——
// 不把"某个上游没响应"折成 0 或空列表，那会把取不到讲成"这个人什么都没做"。
import { fetchApi } from "./client";
import { requireArray } from "./fields";

// ── 账号资料：GET /users/:id（匿名可读）──
//
// auth.users 有 display_name / bio 列（空串=未设置）及两项明确的隐私选择。
// avatar_url、created_at、invite_code 缺席时调用方按整块不渲染，
// 不要送进 new Date()（会渲染 Invalid Date），也不要当 0 用。
export interface PublicUser {
  id: string;
  username: string;
  /** 空串=未设置，调用方回退用户名。 */
  display_name?: string;
  avatar_url?: string;
  /** 空串=未设置，调用方不渲染简介区。 */
  bio?: string;
  /** 仅封禁账号带出（后端 omitempty）；封禁是账号状态，不代表资料不存在。 */
  banned?: boolean;
  favorites_public: boolean;
  email_public: boolean;
  /** 本人或用户主动公开邮箱时下发；不根据本地设置拼造邮箱。 */
  email?: string;
}

export interface PublicUserProfile {
  user: PublicUser;
  /** 账号服务只拥有 invited_count；其余计数分属目录与互动服务，缺席即"未知"。 */
  stats: { invited_count?: number };
}

export async function fetchUserProfile(id: string): Promise<PublicUserProfile> {
  const result = await fetchApi<PublicUserProfile>("/users/" + encodeURIComponent(id));
  if (typeof result?.user?.favorites_public !== "boolean" || typeof result?.user?.email_public !== "boolean") {
    throw new Error("invalid_response: profile_privacy");
  }
  return result;
}

// ── 目录贡献：GET /users/:id/contributions ──
//
export interface ContributionSummary {
  merged_commits: number;
  changed_entities: number;
  created_entities: number;
  merged_pull_requests: number;
  legacy_revisions: number;
}

export interface ContributionActor {
  id: string;
  name: string;
}

export interface ContributionEntity {
  id: string;
  kind: string;
  title: string;
}

export interface ContributionDay {
  date: string;
  count: number;
}

export interface ContributionItem {
  id: string;
  commit_id?: string;
  message: string;
  authored_at: string;
  merged_at: string;
  author: ContributionActor;
  authors: ContributionActor[];
  merged_by?: ContributionActor;
  pull_request_id?: string;
  changes_count: number;
  entities: ContributionEntity[];
  /** 未关联提交的历史修订，保持原始归属，不伪造批次。 */
  legacy: boolean;
}

export interface UserContributions {
  year: number;
  timezone: "UTC";
  summary: ContributionSummary;
  calendar: ContributionDay[];
  items: ContributionItem[];
  total: number;
  page: number;
  page_size: number;
}

export async function fetchUserContributions(
  id: string,
  opts: { year?: number; page?: number; pageSize?: number; from?: string; to?: string } = {}
): Promise<UserContributions> {
  const params = new URLSearchParams({
    year: String(opts.year ?? new Date().getUTCFullYear()),
    page: String(opts.page ?? 1),
    page_size: String(opts.pageSize ?? 20),
  });
  if (opts.from) params.set("from", opts.from);
  if (opts.to) params.set("to", opts.to);
  const res = await fetchApi<UserContributions>(
    "/users/" + encodeURIComponent(id) + "/contributions?" + params.toString()
  );
  if (!res || res.timezone !== "UTC" || !Number.isInteger(res.year) || res.year < 1970 || res.year > 9999) {
    throw new Error("invalid_response: contribution_year");
  }
  const summary = res.summary;
  if (!summary || ![summary.merged_commits, summary.changed_entities, summary.created_entities,
    summary.merged_pull_requests, summary.legacy_revisions].every(isContributionCount)) {
    throw new Error("invalid_response: contribution_summary");
  }
  if (!isContributionCount(res.total) || !isContributionCount(res.page) || res.page < 1 ||
      !isContributionCount(res.page_size) || res.page_size < 1) {
    throw new Error("invalid_response: contribution_pagination");
  }
  const calendar = requireArray<ContributionDay>(res.calendar, "calendar");
  if (calendar.some((day) => !day || !/^\d{4}-\d{2}-\d{2}$/.test(day.date) ||
      !day.date.startsWith(`${res.year}-`) || !Number.isFinite(Date.parse(`${day.date}T00:00:00Z`)) ||
      new Date(`${day.date}T00:00:00Z`).toISOString().slice(0, 10) !== day.date ||
      !isContributionCount(day.count))) {
    throw new Error("invalid_response: contribution_calendar");
  }
  const items = requireArray<ContributionItem>(res.items, "items");
  for (const item of items) {
    if (!item || typeof item.id !== "string" || !item.id || typeof item.message !== "string" ||
        !validContributionTime(item.authored_at) || !validContributionTime(item.merged_at) ||
        typeof item.legacy !== "boolean" || !validContributionActor(item.author) ||
        (item.merged_by !== undefined && !validContributionActor(item.merged_by)) ||
        !isContributionCount(item.changes_count) ||
        [item.commit_id, item.pull_request_id].some((value) => value !== undefined && (typeof value !== "string" || !value))) {
      throw new Error("invalid_response: contribution_item");
    }
    const authors = requireArray<ContributionActor>(item.authors, "authors");
    if (authors.length === 0 || authors.some((actor) => !validContributionActor(actor))) {
      throw new Error("invalid_response: contribution_authors");
    }
    const entities = requireArray<ContributionEntity>(item.entities, "entities");
    if (entities.some((entity) => !entity || typeof entity.id !== "string" || !entity.id ||
        typeof entity.kind !== "string" || typeof entity.title !== "string")) {
      throw new Error("invalid_response: contribution_entities");
    }
  }
  return res;
}

function isContributionCount(value: unknown): value is number {
  return typeof value === "number" && Number.isSafeInteger(value) && value >= 0;
}

function validContributionActor(value: ContributionActor): boolean {
  return Boolean(value && typeof value.id === "string" && value.id && typeof value.name === "string");
}

function validContributionTime(value: unknown): value is string {
  return typeof value === "string" && value.length > 0 && Number.isFinite(Date.parse(value));
}

// ── 互动统计：GET /users/:id/stats（匿名可读）──
//
// community 侧只给这三个数字（口径见其 README：短评不计入任何一项）；
// 账号与目录的计数不在这里，缺失时是"来源没有"，不是 0。
export interface CommunityUserStats {
  topics_created?: number;
  comments_created?: number;
  /** null 表示收藏未向请求者公开；缺席仍表示来源未知。 */
  favorites_count?: number | null;
}

export async function fetchUserCommunityStats(id: string): Promise<CommunityUserStats> {
  const res = await fetchApi<{ stats?: CommunityUserStats }>("/users/" + encodeURIComponent(id) + "/stats");
  return res.stats ?? {};
}
