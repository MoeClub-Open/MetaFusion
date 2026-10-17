import {
  can, canEnterCatalogConsole, CATALOG_CONSOLE_CODES, ASSISTANT_MANAGE, CATALOG_DEFINITIONS_MANAGE,
  CATALOG_LIFECYCLE_MANAGE, CATALOG_REVIEW_MANAGE, CATALOG_SHELVES_MANAGE,
  COMMUNITY_RATE_LIMITS_MANAGE,
} from "./permissions";
import type { User } from "./api";

export type CatalogAdminTab = "overview" | "entities" | "definitions" | "reviews"
  | "merge" | "modules" | "extdb" | "shelves" | "exchange" | "ratelimits" | "assistant" | "publicSources" | "requestLogs";

const sectionPermissions: Partial<Record<CatalogAdminTab, string>> = {
  definitions: CATALOG_DEFINITIONS_MANAGE,
  extdb: CATALOG_DEFINITIONS_MANAGE,
  publicSources: CATALOG_DEFINITIONS_MANAGE,
  shelves: CATALOG_SHELVES_MANAGE,
  reviews: CATALOG_REVIEW_MANAGE,
  entities: CATALOG_LIFECYCLE_MANAGE,
  requestLogs: CATALOG_LIFECYCLE_MANAGE,
  merge: CATALOG_LIFECYCLE_MANAGE,
  assistant: ASSISTANT_MANAGE,
};

// 管理台入口不授予所有工作面的权限；写入授权仍由各服务端点负责。
export function canUseCatalogAdminTab(user: User | null | undefined, tab: CatalogAdminTab): boolean {
  if (tab === "ratelimits") return can(user, CATALOG_DEFINITIONS_MANAGE) || can(user, COMMUNITY_RATE_LIMITS_MANAGE);
  if (!canEnterCatalogConsole(user)) return false;
  if (tab !== "assistant" && !CATALOG_CONSOLE_CODES.some((code) => code !== ASSISTANT_MANAGE && can(user, code))) return false;
  const permission = sectionPermissions[tab];
  return !permission || can(user, permission);
}
