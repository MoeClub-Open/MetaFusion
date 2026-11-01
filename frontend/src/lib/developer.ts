// 开发者中心（/api/developer/*）的前端薄封装。
//
// 契约来源（只读核对 metafusion-auth，本任务未改账号服务）：
//   internal/handler/developer.go —— 路由与响应形状（{app, client_secret} / {items} / overview）
//   internal/store/developer.go   —— DeveloperApp / DeveloperAppInput 字段与错误码
//
// 边界：client_secret 只在创建与轮换的响应里出现一次（库里只有 bcrypt 哈希），
// 界面拿到后必须立刻让用户保存；"再看一眼密钥"这种入口不存在，也不该伪造。
// 归属（谁能改）与核验状态由服务端判定，前端只展示、不提供 trusted / verified 开关。

import { ApiError, fetchApi, pickLocalizedName } from "@/lib/api";

/** 应用投影：与 store.DeveloperApp 字段一一对应（first_party / verified 由服务端算好）。 */
export interface DeveloperApp {
  client_id: string;
  name: string;
  description: string;
  homepage_url: string;
  redirect_uris: string[];
  scopes: string[];
  first_party: boolean;
  verified: boolean;
  disabled: boolean;
  has_secret: boolean;
  owner_user_id?: string;
  owner_username?: string;
  created_at: string;
}

/** 创建 / 轮换的响应：client_secret 是一次性明文，离开这一次响应就无处可取。 */
export interface DeveloperAppSecret {
  app: DeveloperApp;
  client_secret: string;
}

/** 受支持 scope 的四语说明（服务端与同意页同源，前端不另维护一份文案）。 */
export interface ScopeInfo {
  code: string;
  names: Record<string, string>;
  descriptions: Record<string, string>;
}

/** 接入配置：端点、能力与 scope 说明。
 *  platforms 是 overview 仍在回的清单（owner_user_id 为空的系统应用）：接口不为此改，
 *  字段留着是为了类型与响应形状不失真；开发者中心不渲染也不取用它，只留说明与管理员入口。 */
export interface AccessConfig {
  issuer: string;
  account_url: string;
  endpoints: Record<string, string>;
  grant_types: string[];
  response_types: string[];
  code_challenge_methods: string[];
  scopes: ScopeInfo[];
  platforms: DeveloperApp[];
}

/** 写入形状：字段缺省即"不改这一项"（服务端用指针区分没传与传了零值）。 */
export interface DeveloperAppDraft {
  client_id?: string;
  name?: string;
  description?: string;
  homepage_url?: string;
  redirect_uris?: string[];
  scopes?: string[];
}

/** client_id 形状：与 store.ValidClientID 同口径（小写字母开头，3–64 位）。 */
export const DEVELOPER_CLIENT_ID_RE = /^[a-z][a-z0-9_-]{2,63}$/;

// 翻译函数形状只有一份定义（i18n/I18nProvider）。
import type { TranslateFn } from "@/i18n/I18nProvider";
export type { TranslateFn };

/** 接入端点清单：顺序即界面展示顺序，键名与服务端 endpoints 对象一致。 */
export const ENDPOINT_KEYS = ["authorization", "token", "userinfo", "jwks", "discovery"] as const;

export function fetchAccessConfig(): Promise<AccessConfig> {
  return fetchApi<AccessConfig>("/developer/overview");
}

export async function fetchMyApps(): Promise<DeveloperApp[]> {
  const res = await fetchApi<{ items?: DeveloperApp[] }>("/developer/apps");
  return res.items ?? [];
}

export function createApp(draft: DeveloperAppDraft): Promise<DeveloperAppSecret> {
  return fetchApi<DeveloperAppSecret>("/developer/apps", { method: "POST", body: JSON.stringify(draft) });
}

/** 局部更新：只提交改动过的字段（服务端是补丁语义，多传就等于改了它）。 */
export function updateApp(id: string, patch: DeveloperAppDraft): Promise<DeveloperApp> {
  return fetchApi<{ app: DeveloperApp }>("/developer/apps/" + encodeURIComponent(id), {
    method: "PUT",
    body: JSON.stringify(patch),
  }).then((res) => res.app);
}

export function rotateAppSecret(id: string): Promise<DeveloperAppSecret> {
  return fetchApi<DeveloperAppSecret>("/developer/apps/" + encodeURIComponent(id) + "/rotate-secret", {
    method: "POST",
  });
}

export function deleteApp(id: string): Promise<{ ok: boolean }> {
  return fetchApi<{ ok: boolean }>("/developer/apps/" + encodeURIComponent(id), { method: "DELETE" });
}

/** 四语选择走与实体字段同一套回退链（lib/api 的 pickLocalizedName），不另写一份。 */
export function scopeText(item: ScopeInfo, locale: string, kind: "names" | "descriptions"): string {
  const values = kind === "names" ? item.names : item.descriptions;
  return pickLocalizedName(locale, values, kind === "names" ? item.code : "");
}

/**
 * 把账号服务的错误码翻成人话。
 * 码的形态有两种：单独的 code，以及 "code: detail"（例如 invalid_homepage_url: x、invalid_scope: email），
 * 后者把 detail 填进文案。先查开发者中心自己的码表，再复用管理台那份（同一批 store 错误码，
 * 两处解释不一致会让人以为是两个服务）。未知码回退到"操作失败（HTTP xxx）：原码"。
 */
export function describeDeveloperError(err: unknown, t: TranslateFn): string {
  if (err instanceof ApiError) {
    const raw = (err.message || "").trim();
    const separator = raw.indexOf(":");
    const code = (separator >= 0 ? raw.slice(0, separator) : raw).trim();
    const detail = separator >= 0 ? raw.slice(separator + 1).trim() : "";
    if (code) {
      for (const key of ["developer.error." + code, "admin.oauth.error." + code]) {
        const translated = t(key, { detail, status: err.status });
        if (translated !== key) return translated;
      }
    }
    if (err.status === 401) return t("developer.error.unauthorized");
    if (err.status === 403) return t("developer.error.forbidden");
    if (err.status === 404) return t("developer.error.client_not_found");
    if (err.status >= 502 && err.status <= 504) return t("developer.error.upstream", { status: err.status });
    return t("developer.error.generic", { status: err.status, message: raw || "HTTP " + err.status });
  }
  return t("developer.error.network", { message: err instanceof Error ? err.message : String(err) });
}

/** 自有应用的授权审计：谁（actor）对我的哪个客户端做了什么（同意/拒绝/轮换/删除…）。
 *  服务端按 oauth_clients.owner_user_id = 本人过滤，无归属行直接空列表。 */
export interface DeveloperAuditEntry {
  id: string;
  actor_user_id: string;
  actor_username: string;
  subject_user_id: string;
  client_id: string;
  client_name: string;
  action: string;
  scopes: string[];
  detail: string;
  created_at: string;
}

export async function fetchDeveloperAuditLogs(clientId?: string, limit?: number): Promise<DeveloperAuditEntry[]> {
  const params = new URLSearchParams();
  if (clientId) params.set("client_id", clientId);
  if (limit) params.set("limit", String(limit));
  const qs = params.toString();
  const res = await fetchApi<{ items?: DeveloperAuditEntry[] }>("/developer/audit-logs" + (qs ? "?" + qs : ""));
  return res.items ?? [];
}

/** 本人 API 调用日志：只记已登录请求，route 为模板路径（无实体 id 与查询串）。 */


/** 回调白名单是"一行一个"：换行/空格都当分隔，去空去重后与原值比较才谈得上"改没改"。 */
export function parseRedirects(raw: string): string[] {
  const out: string[] = [];
  for (const line of raw.split(/\r?\n/)) {
    const uri = line.trim();
    if (uri && !out.includes(uri)) out.push(uri);
  }
  return out;
}

/** 与 store.ValidateRedirectURIs 同口径的前置校验：通配符、相对地址、片段、内嵌凭据一律不收。 */
export function invalidRedirect(uri: string): boolean {
  if (uri.includes("*") || uri.includes("#")) return true;
  try {
    const u = new URL(uri);
    return (u.protocol !== "http:" && u.protocol !== "https:") || !u.hostname || u.username !== "" || u.password !== "";
  } catch {
    return true;
  }
}

/** 与 store.ValidateHomepageURL 同口径：空串合法（没填），非空必须是 http(s) 绝对地址。 */
export function invalidHomepage(uri: string): boolean {
  const value = uri.trim();
  if (!value) return false;
  return invalidRedirect(value);
}

/** 状态徽章：核验与停用由服务端字段决定，界面只做展示。 */
export function appStatus(app: DeveloperApp): "disabled" | "first_party" | "verified" | "unverified" {
  if (app.disabled) return "disabled";
  if (app.first_party) return "first_party";
  return app.verified ? "verified" : "unverified";
}
