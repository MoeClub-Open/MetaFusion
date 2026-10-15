// 页面由网关按路径转发到各服务；账号登录与初始化页由账号服务唯一维护。
//
// 社区与存储都在同域网关路径下，因此这里没有"外部站点开关"：只有资源站是个真正
// 独立的外部站点（它自己就有 /subject/<id> 页面），才需要配置外链。

// 资源站是独立外部服务：未显式配置地址时视为尚未开放，不展示任何跳转入口，
// 避免出现指向不存在域名的死链（曾硬编码 resources.findverse.cc，DNS 无此记录）。
const CONFIGURED_STORAGE_URL = process.env.NEXT_PUBLIC_RESOURCE_STATION_URL || "";

export const STORAGE_SERVICE_URL = /^https?:\/\//i.test(CONFIGURED_STORAGE_URL)
  ? CONFIGURED_STORAGE_URL.replace(/\/+$/, "")
  : "";

/** 资源站是否已接入：为 false 时所有资源跳转入口都应隐藏。 */
export function hasResourceStation(): boolean {
  return STORAGE_SERVICE_URL !== "";
}

export const DOCS_SERVICE_URL =
  process.env.NEXT_PUBLIC_DOCS_URL || "/docs";

export function getAuthLoginUrl(returnTo?: string): string {
  const redirect = returnTo || (typeof window !== "undefined" ? window.location.href : "/home");
  return `/login?redirect=${encodeURIComponent(redirect)}`;
}

// 密码设置由主站 settings 页维护，供账号管理入口使用。
export function getAuthPasswordUrl(): string {
  return "/settings?tab=password";
}

// 账号管理统一进入管理中心；服务自带应用仍挂在 /admin/account/，由管理中心嵌入。
export function getAuthUsersAdminUrl(): string {
  return "/admin?workspace=account";
}

// 条目 → 论坛的地址。条目页的「在论坛打开」统一收在这里：板块可选，社区页从
// board_code 读初始板块（app/community/page.tsx），不传就是全部板块。
// 社区没有独立域名：站点同域由网关按 /community 前缀分流。
export function getForumEntityUrl(entityId: string, boardCode?: string): string {
  const query = new URLSearchParams({ entity_id: entityId });
  if (boardCode) query.set("board_code", boardCode);
  return `/community?${query.toString()}`;
}

// 合集（collection）就是目录实体：地址与其它实体同一条 /catalog/<id>。
export function getForumCollectionUrl(collectionId: string): string {
  return `/catalog/${encodeURIComponent(collectionId)}`;
}

export function getStorageEntityUrl(entityId?: string): string {
  if (STORAGE_SERVICE_URL) {
    return entityId
      ? `${STORAGE_SERVICE_URL}/subject/${encodeURIComponent(entityId)}`
      : STORAGE_SERVICE_URL;
  }
  const query = entityId ? `?subject_id=${encodeURIComponent(entityId)}` : "";
  return `/downloads${query}`;
}
