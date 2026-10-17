import { fetchApi } from "./client";
import type { User } from "./client";

/**
 * /auth/me 返回账号服务 store.User 的投影；界面不在各页面重复裁剪身份字段。
 *
 * 拿不准的字段一律不造值：邮箱缺席就保持缺席（页面走 settings.unboundEmail），
 * display_name 缺席交给 displayNameOf 回落 username；组与权限码有就整份带住。
 * 其余字段（avatar_url / bio / is_email_verified / invite_code…）原样透传——
 * 它们是否存在由账号服务决定，前端不在这里裁剪。
 */
export function normalizeSessionUser(raw: unknown): User {
  const u = (raw || {}) as Record<string, any>;
  const { email, display_name, groups, permissions, favorites_public, email_public, ...rest } = u;
  return {
    ...rest,
    id: String(u.id ?? ""),
    username: String(u.username ?? ""),
    ...(typeof email === "string" && email.trim() !== "" ? { email: email.trim() } : {}),
    ...(typeof display_name === "string" && display_name.trim() !== "" ? { display_name } : {}),
    ...(typeof favorites_public === "boolean" ? { favorites_public } : {}),
    ...(typeof email_public === "boolean" ? { email_public } : {}),
    // 账号服务按 omitempty 发这两个数组：给了就带住（空数组也是真实值），缺席才留空。
    ...(Array.isArray(groups) ? { groups: groups.filter((g: unknown) => typeof g === "string") } : {}),
    ...(Array.isArray(permissions) ? { permissions: permissions.filter((p: unknown) => typeof p === "string") } : {}),
  };
}

/** 邀请码：带次数上限与可选过期时间；used_count 由服务端在注册事务里累加。 */

export interface InviteCode {
  code: string;
  created_by: string;
  creator?: string;
  note: string;
  max_uses: number;
  used_count: number;
  revoked: boolean;
  expires_at?: string;
  created_at: string;
}

/** GET /auth/invite：我的邀请码台账 + 由我邀请进来的人 + 当前是否可签发。 */

export interface InviteLedger {
  items: InviteCode[];
  members: User[];
  can_create: boolean;
}

export function fetchInviteLedger(): Promise<InviteLedger> {
  return fetchApi<InviteLedger>("/auth/invite");
}

export function createInviteCode(payload: {
  note?: string;
  max_uses?: number;
  expires_in_days?: number;
}): Promise<InviteCode> {
  return fetchApi<InviteCode>("/auth/invite", {
    method: "POST",
    body: JSON.stringify(payload),
  });
}

// ── 第三方授权自助：GET/DELETE /auth/oauth-grants ──
//
// 归属是当前登录身份（路径里没有别人的 user id），因此普通成员也能看/收回自己的授权。
// 后端返回的形状是 store.AuthorizedApp 的逐字投影：名称取不到时用 client_id 兜底，
// last_authorized_at 来自同意审计（授权过但令牌已过期的应用也会在列表里），
// expires_at 只在还有生效令牌时才有值——两者都是可选项，缺席就整行不渲染。
export interface AuthorizedApp {
  client_id: string;
  name: string;
  scopes: string[];
  /** 当前还有未过期令牌。 */
  active: boolean;
  last_authorized_at?: string;
  expires_at?: string;
}

export function fetchOAuthGrants(): Promise<{ items: AuthorizedApp[] }> {
  return fetchApi<{ items: AuthorizedApp[] }>("/auth/oauth-grants");
}

/** 撤回：后端按 (user_id, client_id) 删除未过期令牌并作废未兑换授权码，幂等（本来没有也回 ok）。 */
export function revokeOAuthGrant(clientId: string): Promise<{ ok: boolean; revoked: number }> {
  return fetchApi<{ ok: boolean; revoked: number }>("/auth/oauth-grants/" + encodeURIComponent(clientId), {
    method: "DELETE",
  });
}

// ── 个人访问令牌（PAT）：POST/GET /auth/tokens、DELETE /auth/tokens/:id ──
//
// 明文形如 mfp_<32 字节 base62>，库里只存 sha256：创建响应是它唯一一次露面，
// 列表接口永远拿不回明文，界面也不该假装"稍后还能再看一眼"。
// scopes 就是权限码（如 catalog.entity.edit），令牌的有效权限 = 用户自身权限 ∩ scopes；
// 下游服务用 Authorization: Bearer mfp_...（没有 X-API-Key 这种写法）调账号服务内省端点换成身份；
// 无效/已吊销/已过期一律 401 invalid_token，不区分原因（免得被拿来探测令牌状态）；
// 内省结果按 token_hash 进程内缓存 60 秒 —— 因此**撤销最长有 60 秒窗口**（UI 与文档同口径）。
//
// 形状以账号服务实现为准；不接受旧载荷变体。明文读不到时抛错，
// 避免把不完整的创建响应展示为有效令牌。

export interface PersonalAccessToken {
  id: string;
  name: string;
  /** 前 12 字符，用于在列表里分辨令牌（明文只出现一次，之后只能靠前缀认人）。 */
  token_prefix: string;
  scopes: string[];
  expires_at?: string | null;
  last_used_at?: string | null;
  created_at?: string;
  /** 非空即已撤销；撤销是写时间戳，行不删。 */
  revoked_at?: string | null;
  /** 服务端计算的有效状态，避免客户端时钟偏差。 */
  active: boolean;
}

export interface CreatedPersonalAccessToken {
  /** mfp_ 明文：只在这一次响应里出现，调用方必须立刻展示给用户。 */
  token: string;
  item: PersonalAccessToken;
}

function strField(value: unknown): string | null {
  return typeof value === "string" && value.trim() !== "" ? value : null;
}

function normalizePersonalAccessToken(raw: unknown): PersonalAccessToken {
  const r = (raw || {}) as Record<string, unknown>;
  if (typeof r.active !== "boolean") throw new Error("invalid_response: active");
  return {
    id: strField(r.id) ?? "",
    name: strField(r.name) ?? "",
    token_prefix: strField(r.token_prefix) ?? "",
    scopes: Array.isArray(r.scopes) ? r.scopes.filter((s): s is string => typeof s === "string") : [],
    expires_at: strField(r.expires_at),
    last_used_at: strField(r.last_used_at),
    created_at: strField(r.created_at) ?? undefined,
    revoked_at: strField(r.revoked_at),
    active: r.active,
  };
}

function normalizeCreatedToken(raw: unknown): CreatedPersonalAccessToken {
  const r = (raw || {}) as Record<string, unknown>;
  const plain = strField(r.token) || "";
  if (!plain) throw new Error("pat_plaintext_missing");
  return { token: plain, item: normalizePersonalAccessToken(r.item) };
}

/** GET /auth/tokens：只列当前登录身份自己的令牌（含已撤销的，界面靠 revoked_at 打标）。 */
export async function fetchPersonalAccessTokens(): Promise<{ items: PersonalAccessToken[] }> {
  const raw = await fetchApi<unknown>("/auth/tokens");
  // 契约漂移必须能与"没有令牌"分开：这里原来把"既不是数组、也没有 items 数组"的响应也归一成
  // { items: [] }，界面上就成了"暂无令牌"——把取不到讲成了空列表，面板的失败分支永远进不去。
  // 现在按失败抛出（码留在 message 前缀里，面板的 patErrorText 会把码与明细一起显示出来）。
  const list = Array.isArray((raw as { items?: unknown })?.items)
    ? ((raw as { items: unknown[] }).items)
    : null;
  if (list === null) throw new Error("invalid_response: items");
  return { items: list.map(normalizePersonalAccessToken) };
}

/** POST /auth/tokens：需登录会话（PAT 本身不能创建 PAT）；expires_in_days 省略即永不过期。 */
export async function createPersonalAccessToken(payload: {
  name: string;
  scopes: string[];
  expires_in_days?: number;
}): Promise<CreatedPersonalAccessToken> {
  const raw = await fetchApi<unknown>("/auth/tokens", {
    method: "POST",
    body: JSON.stringify(payload),
  });
  return normalizeCreatedToken(raw);
}

/** PUT /api/auth/profile：自助改昵称与简介。空串=未设置；昵称 32 字、简介 500 字（后端同口径，超限 400）。 */
export interface OwnProfileUpdate {
  display_name: string;
  bio: string;
}

export async function updateOwnProfile(payload: OwnProfileUpdate): Promise<OwnProfileUpdate & { id: string; username: string }> {
  return fetchApi<OwnProfileUpdate & { id: string; username: string }>("/auth/profile", {
    method: "PUT",
    body: JSON.stringify(payload),
  });
}

/** 第一方会话自己的隐私选择；两字段均由账号服务明确返回，不用默认值代替读取失败。 */
export interface PrivacySettings {
  favorites_public: boolean;
  email_public: boolean;
}

function privacySettingsResponse(raw: unknown): PrivacySettings {
  const value = raw as Partial<PrivacySettings> | null;
  if (typeof value?.favorites_public !== "boolean" || typeof value?.email_public !== "boolean") {
    throw new Error("invalid_response: privacy_settings");
  }
  return { favorites_public: value.favorites_public, email_public: value.email_public };
}

export async function fetchPrivacySettings(): Promise<PrivacySettings> {
  return privacySettingsResponse(await fetchApi<unknown>("/auth/privacy"));
}

export async function updatePrivacySettings(payload: Partial<PrivacySettings>): Promise<PrivacySettings> {
  return privacySettingsResponse(await fetchApi<unknown>("/auth/privacy", {
    method: "PATCH",
    body: JSON.stringify(payload),
  }));
}

/** POST /auth/tokens/:id/revoke：撤销但保留列表记录，最长需 60 秒生效。 */
export function revokePersonalAccessToken(id: string): Promise<{ ok: boolean }> {
  return fetchApi<{ ok: boolean }>("/auth/tokens/" + encodeURIComponent(id) + "/revoke", { method: "POST" });
}

export function updatePersonalAccessToken(id: string, payload: { name?: string; scopes?: string[] }): Promise<{ ok: boolean }> {
  return fetchApi<{ ok: boolean }>("/auth/tokens/" + encodeURIComponent(id), { method: "PATCH", body: JSON.stringify(payload) });
}

export function deletePersonalAccessToken(id: string): Promise<{ ok: boolean }> {
  return fetchApi<{ ok: boolean }>("/auth/tokens/" + encodeURIComponent(id), { method: "DELETE" });
}

// ── 目录关系图谱拓扑与关系边 ──
// ── OOBE 开箱初始化设置 ──
// 站点名不在这里：它不是实例设置（账号服务里既没有消费方也已从接受表移除），
// 前端品牌文案由构建期决定；POST /api/setup 只认 username/email/password，多传字段一律 400。
export interface SetupStatusResponse {
  is_initialized: boolean;
  has_admin: boolean;
  total_users: number;
}

export async function fetchSetupStatus(): Promise<SetupStatusResponse> {
  try {
    const res = await fetch("/api/setup", { credentials: "same-origin" });
    if (res.ok) {
      const data = await res.json();
      return { is_initialized: !data.needed, has_admin: !data.needed, total_users: 1 };
    }
  } catch {}
  return { is_initialized: true, has_admin: true, total_users: 1 };
}

export interface PublicAuthSettings {
  registration_enabled: boolean;
  invite_required: boolean;
  require_email_verification: boolean;
  email_verification_enabled: boolean;
  rate_limit_enabled?: boolean;
  auth_rate_limit_enabled?: boolean;
}

export function fetchAuthSettings(): Promise<PublicAuthSettings> {
  return fetchApi<PublicAuthSettings>("/auth/settings");
}
