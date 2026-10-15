// 前端权限判定：码表由 backend/internal/catalog/permission.go 与各子系统仓库声明生成
// （见 frontend/scripts/generate-contracts.mjs），本文件不再手抄任何码。
//
// 权限码由账号服务装进组（auth.groups.permissions），各子系统声明并解释自己的码。
// 这里只做"能不能看到入口"的判断；真正的写入授权仍在服务端，前端判定只是别把
// 用户引到注定 403 的按钮上，也不能因为前端放行就以为服务端会放行。

import type { User } from "./api";
import {
  ALL_PERMISSION_CODES,
  AUTH_AUDIT_READ,
  AUTH_GROUPS_MANAGE,
  AUTH_INVITES_MANAGE,
  AUTH_OAUTH_MANAGE,
  AUTH_SETTINGS_MANAGE,
  AUTH_USERS_MANAGE,
  CATALOG_DEFINITIONS_MANAGE,
  CATALOG_ENTITY_EDIT,
  CATALOG_LIFECYCLE_MANAGE,
  CATALOG_SHELVES_MANAGE,
  COMMUNITY_BOARD_MANAGE,
  COMMUNITY_POST_MODERATE,
  COMMUNITY_REPORT_REVIEW,
  COMMUNITY_RATE_LIMITS_MANAGE,
  COMMUNITY_TOPIC_PIN,
  STORAGE_ASSET_MODERATE,
} from "./permissions.generated";

// 既有调用点从 "@/lib/permissions" 取这些常量，导出名保持不变；
// 用 export { ... } from 而不是 export const，以保留生成物里的字面量类型。
export {
  AUTH_AUDIT_READ,
  AUTH_GROUPS_MANAGE,
  AUTH_INVITES_MANAGE,
  AUTH_OAUTH_MANAGE,
  AUTH_SETTINGS_MANAGE,
  AUTH_USERS_MANAGE,
  CATALOG_DEFINITIONS_MANAGE,
  CATALOG_ENTITY_EDIT,
  CATALOG_LIFECYCLE_MANAGE,
  CATALOG_RELATION_EDIT,
  CATALOG_SHELVES_MANAGE,
  COMMUNITY_BOARD_MANAGE,
  COMMUNITY_POST_MODERATE,
  COMMUNITY_REPORT_REVIEW,
  COMMUNITY_RATE_LIMITS_MANAGE,
  COMMUNITY_TOPIC_PIN,
  STORAGE_ASSET_MODERATE,
  STORAGE_ASSET_UPLOAD,
} from "./permissions.generated";

type AnyUser = Pick<User, "id"> & Partial<Pick<User, "permissions" | "groups">>;

/**
 * can 判定"这个用户持有某个权限码吗"。
 *
 * 只按 permissions 判定（含通配符 *），与后端 User.Can 一致。
 */
export function can(user: AnyUser | null | undefined, code: string): boolean {
  if (!user) return false;
  const perms = user.permissions || [];
  return perms.includes("*") || perms.includes(code);
}

/**
 * 新个人访问令牌（PAT）可授权的权限码：本人持有且允许委托的那些码。
 *
 * 服务端签发时拒绝 * 与所有以 .manage 结尾的权限；前端不提供这些必然被拒的选项。
 * 本人持有 * 时展开为允许委托的具体码，其余权限按 can() 的持有码语义选择。
 * 本实例自定义的非管理码不在生成物里也照样列出。
 * 这里只用于新建选择器，不裁剪既有 PAT 的 scopes，也不改变站内 can() 判定。
 */
export function grantablePermissionCodes(user: AnyUser | null | undefined): string[] {
  if (!user) return [];
  const perms = user.permissions || [];
  const known = ALL_PERMISSION_CODES as readonly string[];
  const delegable = (code: string) => code !== "*" && code !== "mcp.connect" && !code.endsWith(".manage");
  if (perms.includes("*")) return known.filter(delegable);
  if (perms.length > 0) {
    const listed = known.filter((code) => delegable(code) && perms.includes(code));
    const custom = perms.filter((code) => delegable(code) && !known.includes(code));
    return [...listed, ...custom];
  }
  return [];
}

/**
 * 目录工作面的准入码：条目/定义/来源/货架/审核/合并/交换只接受目录域自己的权限。
 *
 * 账号、社区、存储三个域的管理台已经是独立应用（各自的 admin/ 应用 + 网关
 * /admin/{account,community,storage}/ 前缀），它们的码不构成进入目录工作面的凭据：
 * 共享限流页单独按服务权限开放，不通过这份码表授予其他目录工作面的访问权。
 */
export const CATALOG_CONSOLE_CODES = [
  CATALOG_DEFINITIONS_MANAGE,
  CATALOG_SHELVES_MANAGE,
  CATALOG_LIFECYCLE_MANAGE,
] as const;

/** 账号域控制台的准入码，与 metafusion-auth/admin 的 SECTION_PERMISSIONS 同集合。 */
export const ACCOUNT_CONSOLE_CODES = [
  AUTH_AUDIT_READ,
  AUTH_USERS_MANAGE,
  AUTH_GROUPS_MANAGE,
  AUTH_INVITES_MANAGE,
  AUTH_SETTINGS_MANAGE,
  AUTH_OAUTH_MANAGE,
] as const;

export const COMMUNITY_CONSOLE_CODES = [
  COMMUNITY_BOARD_MANAGE,
  COMMUNITY_POST_MODERATE,
  COMMUNITY_REPORT_REVIEW,
  COMMUNITY_TOPIC_PIN,
  COMMUNITY_RATE_LIMITS_MANAGE,
] as const;

export const STORAGE_CONSOLE_CODES = [STORAGE_ASSET_MODERATE] as const;

/** 能否进入目录控制台（`/admin` 的工作面）。 */
export function canEnterCatalogConsole(user: AnyUser | null | undefined): boolean {
  return CATALOG_CONSOLE_CODES.some((code) => can(user, code));
}

/**
 * 能否进入"某个"管理台：四个域的任一管理码。
 *
 * 只用于导航栏入口的显隐与"是否该给出控制台入口页"的判断——具体落到哪个控制台由
 * /admin 页面按域决定，所以这里刻意保持并集，不能用它当目录工作面的闸门。
 * 这里只控制管理中心入口；各域工作面仍各自按权限码隔离。
 */
export function canEnterAdmin(user: AnyUser | null | undefined): boolean {
  return [...CATALOG_CONSOLE_CODES, ...ACCOUNT_CONSOLE_CODES, ...COMMUNITY_CONSOLE_CODES, ...STORAGE_CONSOLE_CODES].some((code) => can(user, code));
}

/** 能否编辑这个实体：审核/生命周期码放行一切；编辑码放行（含协作维护已发布条目）；否则只能改自己的草稿。 */
export function canEditEntity(user: AnyUser | null | undefined, entity: { status?: string; created_by?: string } | null | undefined): boolean {
  if (!user || !entity) return false;
  if (entity.status === "deleted" || entity.status === "merged") return false;
  if (can(user, CATALOG_LIFECYCLE_MANAGE)) return true;
  if (can(user, CATALOG_ENTITY_EDIT)) return true;
  return entity.created_by === user.id && entity.status !== "published";
}

/** 能否把条目直接置为已发布（草稿/待审之外的状态选项）。 */
export function canPublishEntity(user: AnyUser | null | undefined, entity: { created_by?: string } | null | undefined): boolean {
  if (!user) return false;
  if (can(user, CATALOG_LIFECYCLE_MANAGE)) return true;
  if (!can(user, CATALOG_ENTITY_EDIT)) return false;
  return !entity?.created_by || entity.created_by === user.id;
}
