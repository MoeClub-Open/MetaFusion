import {
  can, canEnterCatalogConsole, CATALOG_DEFINITIONS_MANAGE,
  CATALOG_LIFECYCLE_MANAGE, CATALOG_SHELVES_MANAGE,
  COMMUNITY_RATE_LIMITS_MANAGE,
} from "./permissions";
import type { User } from "./api";

export type CatalogAdminTab = "overview" | "entities" | "definitions" | "reviews"
  | "merge" | "modules" | "extdb" | "shelves" | "exchange" | "ratelimits";

const sectionPermissions: Partial<Record<CatalogAdminTab, string>> = {
  definitions: CATALOG_DEFINITIONS_MANAGE,
  extdb: CATALOG_DEFINITIONS_MANAGE,
  shelves: CATALOG_SHELVES_MANAGE,
  reviews: CATALOG_LIFECYCLE_MANAGE,
  merge: CATALOG_LIFECYCLE_MANAGE,
};

// 管理台入口不授予所有工作面的权限；写入授权仍由各服务端点负责。
export function canUseCatalogAdminTab(user: User | null | undefined, tab: CatalogAdminTab): boolean {
  if (tab === "ratelimits") return can(user, CATALOG_DEFINITIONS_MANAGE) || can(user, COMMUNITY_RATE_LIMITS_MANAGE);
  if (!canEnterCatalogConsole(user)) return false;
  const permission = sectionPermissions[tab];
  return !permission || can(user, permission);
}
