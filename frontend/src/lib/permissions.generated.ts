// 本文件由 frontend/scripts/generate-contracts.mjs 生成，勿手改。
// 权限码单一来源（各服务自己声明，账号服务持有全量码表）。
// 来源：backend/internal/catalog/permission.go、../metafusion-auth/internal/store/access.go、../metafusion-auth/internal/store/access.go、../metafusion-community/internal/auth/permission.go、../metafusion-storage/internal/auth/permission.go
// 校验：cd frontend && node scripts/generate-contracts.mjs --check

// ── catalog ──
// 目录权限：条目/关系/定义/生命周期/货架（判定在 backend/internal/catalog/permission.go 的 User.Can）。
export const CATALOG_DEFINITIONS_MANAGE = "catalog.definitions.manage" as const;
export const CATALOG_ENTITY_EDIT = "catalog.entity.edit" as const;
export const CATALOG_LIFECYCLE_MANAGE = "catalog.lifecycle.manage" as const;
export const CATALOG_RELATION_EDIT = "catalog.relation.edit" as const;
export const CATALOG_SHELVES_MANAGE = "catalog.shelves.manage" as const;

export const CATALOG_PERMISSION_CODES = [
  CATALOG_DEFINITIONS_MANAGE,
  CATALOG_ENTITY_EDIT,
  CATALOG_LIFECYCLE_MANAGE,
  CATALOG_RELATION_EDIT,
  CATALOG_SHELVES_MANAGE,
] as const;

// ── auth ──
// 账号权限：auth.oauth.manage 覆盖客户端管理、密钥轮换、令牌吊销与审计（metafusion-auth/internal/handler/oauth_admin.go）。
export const AUTH_AUDIT_READ = "auth.audit.read" as const;
export const AUTH_GROUPS_MANAGE = "auth.groups.manage" as const;
export const AUTH_INVITES_MANAGE = "auth.invites.manage" as const;
export const AUTH_OAUTH_MANAGE = "auth.oauth.manage" as const;
export const AUTH_SETTINGS_MANAGE = "auth.settings.manage" as const;
export const AUTH_USERS_MANAGE = "auth.users.manage" as const;

export const AUTH_PERMISSION_CODES = [
  AUTH_AUDIT_READ,
  AUTH_GROUPS_MANAGE,
  AUTH_INVITES_MANAGE,
  AUTH_OAUTH_MANAGE,
  AUTH_SETTINGS_MANAGE,
  AUTH_USERS_MANAGE,
] as const;

// ── mcp ──
// MCP 连接身份由账号服务声明，与个人 API Key 的业务权限分开。
export const MCP_CONNECT = "mcp.connect" as const;

export const MCP_PERMISSION_CODES = [
  MCP_CONNECT,
] as const;

// ── community ──
// 互动权限：发帖/审核/置顶/板块管理（metafusion-community/internal/auth/permission.go）。
export const COMMUNITY_BOARD_MANAGE = "community.board.manage" as const;
export const COMMUNITY_POST_CREATE = "community.post.create" as const;
export const COMMUNITY_POST_MODERATE = "community.post.moderate" as const;
export const COMMUNITY_RATE_LIMITS_MANAGE = "community.rate_limits.manage" as const;
export const COMMUNITY_REPORT_REVIEW = "community.report.review" as const;
export const COMMUNITY_TOPIC_PIN = "community.topic.pin" as const;

export const COMMUNITY_PERMISSION_CODES = [
  COMMUNITY_BOARD_MANAGE,
  COMMUNITY_POST_CREATE,
  COMMUNITY_POST_MODERATE,
  COMMUNITY_RATE_LIMITS_MANAGE,
  COMMUNITY_REPORT_REVIEW,
  COMMUNITY_TOPIC_PIN,
] as const;

// ── storage ──
// 存储权限：上传与内容审核（metafusion-storage/internal/auth/permission.go）。
export const STORAGE_ASSET_MODERATE = "storage.asset.moderate" as const;
export const STORAGE_ASSET_UPLOAD = "storage.asset.upload" as const;

export const STORAGE_PERMISSION_CODES = [
  STORAGE_ASSET_MODERATE,
  STORAGE_ASSET_UPLOAD,
] as const;

/** 各服务的码表（判定逻辑仍由各服务自己实现，这里只做前端入口显隐）。 */
export const PERMISSION_CODES_BY_SERVICE = {
  catalog: CATALOG_PERMISSION_CODES,
  auth: AUTH_PERMISSION_CODES,
  mcp: MCP_PERMISSION_CODES,
  community: COMMUNITY_PERMISSION_CODES,
  storage: STORAGE_PERMISSION_CODES,
} as const;

/** 全部权限码（去重、按字母序）。 */
export const ALL_PERMISSION_CODES = [
  "auth.audit.read",
  "auth.groups.manage",
  "auth.invites.manage",
  "auth.oauth.manage",
  "auth.settings.manage",
  "auth.users.manage",
  "catalog.definitions.manage",
  "catalog.entity.edit",
  "catalog.lifecycle.manage",
  "catalog.relation.edit",
  "catalog.shelves.manage",
  "community.board.manage",
  "community.post.create",
  "community.post.moderate",
  "community.rate_limits.manage",
  "community.report.review",
  "community.topic.pin",
  "mcp.connect",
  "storage.asset.moderate",
  "storage.asset.upload",
] as const;
