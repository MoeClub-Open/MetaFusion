const getApiBase = () => {
  if (typeof window !== "undefined") {
    // 浏览器端：使用网关相对路径，自适应任何主机/域名/IP
    return "/api";
  }
  // 服务端 (SSR)：使用容器内网
  return process.env.INTERNAL_API_URL || "http://backend:8080/api";
};

export interface User {
  id: string;
  username: string;
  display_name?: string | null;
  /** 账号服务没绑邮箱时整字段缺席（不要造 `用户名@metafusion.local` 之类的假值）；
   *  展示端用 settings.unboundEmail 兜底。 */
  email?: string;
  /** 账号服务给的组与权限码：授权判定以 permissions 为准（见 lib/permissions.ts）。
   *  原始响应到 User 的映射只有一处：lib/api/auth.ts 的 normalizeSessionUser。 */
  groups?: string[];
  permissions?: string[];
  invite_code?: string;
  invites_remaining?: number;
  invited_by?: string;
  avatar_url?: string;
  bio?: string;
  favorites_public?: boolean;
  email_public?: boolean;
  is_email_verified?: boolean;
  created_at?: string;
  updated_at?: string;
  inviter?: User;
}

export function displayNameOf(u: Pick<User, "username" | "display_name">): string {
  const dn = (u as any).display_name;
  if (typeof dn === "string" && dn.trim() !== "") return dn.trim();
  return u.username;
}

// ── 自助注册与个人邀请码（账号服务 /api/auth/*）──

let refreshPromise: Promise<boolean> | null = null;

export function clearAuthTokens(): void {
  if (typeof window === "undefined") return;
  try {
    localStorage.removeItem("metafusion_token");
    localStorage.removeItem("metafusion_refresh_token");
  } catch {
    // 禁用浏览器存储时仍允许使用服务端 Cookie 会话。
  }
}

async function requestTokenRefresh(): Promise<boolean> {
  // 浏览器会话只使用 HttpOnly Cookie；刷新成功后账号服务会轮换该 Cookie。
  if (refreshPromise) {
    return refreshPromise;
  }

  refreshPromise = (async () => {
    try {
      const baseUrl = getApiBase();
      const res = await fetch(`${baseUrl}/auth/refresh`, {
        method: "POST",
        credentials: "same-origin",
        headers: { "Content-Type": "application/json" },
        body: JSON.stringify({}),
      });

      return res.ok;
    } catch {
      return false;
    } finally {
      refreshPromise = null;
    }
  })();

  return refreshPromise;
}

/**
 * API 错误：保留 HTTP 状态码。
 * 调用方据此区分 403/404/503（提示"权限不足/不存在/上游不可用"），而不是去解析错误文案。
 * 继承 Error 且 message 与旧实现一致，既有 `err.message` 用法不受影响。
 */

export class ApiError extends Error {
  readonly status: number;
  readonly details: unknown;
  constructor(message: string, status: number, details?: unknown) {
    super(message);
    this.name = "ApiError";
    this.status = status;
    this.details = details;
    // 目标为 ES5 时 Error 子类的原型链会断开，显式接回，保证 instanceof 判断仍可用。
    Object.setPrototypeOf(this, ApiError.prototype);
  }
}

// 供 fetchApi 带上 x-locale / Accept-Language：语言从 NEXT_LOCALE cookie 读，
// 拆分前它与 fetchApi 同在一个文件，随请求层一起搬到 core。
function readLocaleCookie(): string | null {
  if (typeof document === 'undefined') return null;
  const m = document.cookie.match(/(?:^|;\s*)NEXT_LOCALE=([^;]+)/);
  return m ? decodeURIComponent(m[1]!) : null;
}

export async function fetchApi<T>(endpoint: string, options: RequestInit = {}): Promise<T> {
  const locale = typeof window !== "undefined" ? readLocaleCookie() : null;
  const headers: Record<string, string> = {
    ...(!(options.body instanceof FormData) ? { "Content-Type": "application/json" } : {}),
    ...(options.headers as Record<string, string>),
  };
  if (options.body instanceof FormData) {
    delete headers["Content-Type"];
    delete headers["content-type"];
  }
  const hasExplicitAuthorization = Object.keys(headers).some((name) => name.toLowerCase() === "authorization");
  if (locale) {
    if (!headers["x-locale"] && !headers["X-Locale"]) headers["x-locale"] = locale;
    if (!headers["Accept-Language"]) headers["Accept-Language"] = locale;
  }
  const baseUrl = getApiBase();
  let res = await fetch(`${baseUrl}${endpoint}`, {
    ...options,
    headers,
    credentials: "same-origin",
  });

  // 只给普通 Cookie 会话续期；调用方显式传入的 PAT/Bearer 不与浏览器身份混用。
  const isAuthEndpoint =
    endpoint.startsWith("/auth/login") ||
    endpoint.startsWith("/auth/register") ||
    endpoint.startsWith("/auth/refresh") ||
    endpoint.startsWith("/auth/logout");

  if (res.status === 401 && !isAuthEndpoint && !hasExplicitAuthorization) {
    if (await requestTokenRefresh()) {
      res = await fetch(`${baseUrl}${endpoint}`, {
        ...options,
        headers,
        credentials: "same-origin",
      });
    }
  }

  if (!res.ok) {
    const errorData = await res.json().catch(() => ({ error: "Request failed" }));
    throw new ApiError(errorData.error || `HTTP ${res.status}`, res.status, errorData);
  }

  if (res.status === 204) return undefined as T;
  return res.json();
}
