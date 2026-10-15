import type { User } from "./api";
import {
  ACCOUNT_CONSOLE_CODES, STORAGE_CONSOLE_CODES, can, canEnterCatalogConsole,
  COMMUNITY_BOARD_MANAGE, COMMUNITY_POST_MODERATE, COMMUNITY_REPORT_REVIEW,
  COMMUNITY_TOPIC_PIN, COMMUNITY_RATE_LIMITS_MANAGE,
} from "./permissions";

export const ADMIN_WORKSPACE_CHANNEL = "metafusion.admin.v1";
export const SERVICE_WORKSPACES = ["account", "community", "storage"] as const;
export type ServiceWorkspace = typeof SERVICE_WORKSPACES[number];
export type AdminWorkspace = "catalog" | ServiceWorkspace;
export const WORKSPACE_LABELS: Record<AdminWorkspace, string> = {
  catalog: "admin.console.consoleTitle", account: "admin.consoles.account",
  community: "admin.consoles.community", storage: "admin.consoles.storage",
};
export const WORKSPACE_BASES: Record<ServiceWorkspace, string> = {
  account: "/admin/account/", community: "/admin/community/", storage: "/admin/storage/",
};

export function permittedAdminWorkspaces(user: User | null | undefined): AdminWorkspace[] {
  const result: AdminWorkspace[] = [];
  if (canEnterCatalogConsole(user) || can(user, COMMUNITY_RATE_LIMITS_MANAGE)) result.push("catalog");
  if (ACCOUNT_CONSOLE_CODES.some((code) => can(user, code))) result.push("account");
  if ([COMMUNITY_BOARD_MANAGE, COMMUNITY_POST_MODERATE, COMMUNITY_REPORT_REVIEW, COMMUNITY_TOPIC_PIN].some((code) => can(user, code))) result.push("community");
  if (STORAGE_CONSOLE_CODES.some((code) => can(user, code))) result.push("storage");
  return result;
}

/** 只接受对应管理应用的页面；拒绝跨域、越界路径与应用内部 API/静态资源。 */
export function safeWorkspaceHref(workspace: ServiceWorkspace, input: unknown): string | null {
  if (typeof input !== "string" || !input.startsWith("/") || input.startsWith("//") || /[\\\u0000-\u0020]/.test(input)) return null;
  try {
    const url = new URL(input, "https://admin.invalid");
    const base = WORKSPACE_BASES[workspace];
    if (url.origin !== "https://admin.invalid") return null;
    if (url.pathname === base.slice(0, -1)) url.pathname = base;
    if (!url.pathname.startsWith(base)) return null;
    const decoded = decodeURIComponent(url.pathname);
    if (/%2f|%5c/i.test(url.pathname + decoded) || /[\\\u0000-\u001f]/.test(decoded) || decoded.split("/").some((part) => part === "." || part === "..")) return null;
    const segment = decodeURIComponent(url.pathname.slice(base.length)).split("/")[0];
    if (segment === "api" || segment === "_next") return null;
    return url.pathname + url.search + url.hash;
  } catch { return null; }
}

export function readAdminWorkspace(search: string): { workspace: AdminWorkspace | null; href?: string } {
  const params = new URLSearchParams(search);
  const workspace = params.get("workspace");
  if (workspace === "catalog") return { workspace };
  if (!SERVICE_WORKSPACES.includes(workspace as ServiceWorkspace)) return { workspace: null };
  const service = workspace as ServiceWorkspace;
  return { workspace: service, href: safeWorkspaceHref(service, params.get("path")) ?? WORKSPACE_BASES[service] };
}

export function adminWorkspaceUrl(workspace: AdminWorkspace, href = ""): string {
  if (workspace === "catalog") return `/admin${href.startsWith("#") ? href : ""}`;
  const path = safeWorkspaceHref(workspace, href) ?? WORKSPACE_BASES[workspace];
  return `/admin?${new URLSearchParams({ workspace, path })}`;
}

export type AdminFrameMessage = { type: "ready" | "navigate"; href: string; push: boolean } | { type: "login" };

/** 来源窗口与 origin 由接收组件核对；这里收敛消息格式与目标路径。 */
export function parseAdminFrameMessage(workspace: ServiceWorkspace, data: unknown): AdminFrameMessage | null {
  if (!data || typeof data !== "object") return null;
  const message = data as Record<string, unknown>;
  if (message.channel !== ADMIN_WORKSPACE_CHANNEL || message.workspace !== workspace) return null;
  if (message.type === "login") return { type: "login" };
  if (message.type !== "ready" && message.type !== "navigate") return null;
  const href = safeWorkspaceHref(workspace, message.href);
  return href ? { type: message.type, href, push: message.type === "navigate" && message.history === "push" } : null;
}
