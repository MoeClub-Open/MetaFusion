// ── API 限流策略（单例） ──
export interface RateLimitRule {
  /** 每分钟额度；0 或缺失 = 未声明、继承下一级，不是禁止。 */
  per_minute?: number;
  /** true = 解除限制：该主体完全跳过计数，服务端也不下发 X-RateLimit-* 头。 */
  unlimited?: boolean;
}

export interface RateLimitPolicy {
  default_per_minute?: number;
  default_unlimited?: boolean;
  groups?: Record<string, RateLimitRule>;
  accounts?: Record<string, RateLimitRule>;
}

export interface RateLimitConfig {
  etag: string;
  policy: RateLimitPolicy;
  updated_at: string;
}

export interface MessageRateLimitPolicy {
  send_per_minute: number;
  stranger_per_hour: number;
  admin_unlimited: boolean;
}

export interface CommunityRateLimitConfig extends RateLimitConfig {
  messages: MessageRateLimitPolicy;
  updated_by?: string;
}

export async function fetchCommunityAdminRateLimits(): Promise<CommunityRateLimitConfig> {
  const res = await fetch("/api/community/admin/rate-limits", { credentials: "same-origin" });
  const body = await res.json().catch(() => ({}));
  if (!res.ok) throw new Error(body.error || `HTTP ${res.status}`);
  return body;
}

export async function saveCommunityAdminRateLimits(
  policy: RateLimitPolicy, messages: MessageRateLimitPolicy, expectedEtag: string, editNote: string,
): Promise<CommunityRateLimitConfig> {
  const res = await fetch("/api/community/admin/rate-limits", {
    method: "PUT",
    credentials: "same-origin",
    headers: { "Content-Type": "application/json" },
    body: JSON.stringify({ policy, messages, expected_etag: expectedEtag, edit_note: editNote }),
  });
  const body = await res.json().catch(() => ({}));
  if (!res.ok) throw new Error(body.error || `HTTP ${res.status}`);
  return body;
}

export function fetchAdminRateLimits(): Promise<RateLimitConfig> {
  return fetch("/api/admin/rate-limits", { credentials: "same-origin" })
    .then(async (res) => {
      const body = await res.json().catch(() => ({}));
      if (!res.ok) throw new Error(body.error || `HTTP ${res.status}`);
      return body;
    });
}

// PUT 是整体替换。限流调整使用管理审计说明，不生成元数据来源。
export function saveAdminRateLimits(policy: RateLimitPolicy, expectedEtag: string, editNote: string): Promise<RateLimitConfig> {
  return fetch("/api/admin/rate-limits", {
    method: "PUT",
    credentials: "same-origin",
    headers: { "Content-Type": "application/json" },
    body: JSON.stringify({
      policy,
      expected_etag: expectedEtag,
      edit_note: editNote,
    }),
  }).then(async (res) => {
    const body = await res.json().catch(() => ({}));
    if (!res.ok) throw new Error(body.error || `HTTP ${res.status}`);
    return body;
  });
}

// ── 外部权威数据库预设定义 ──
export interface ExternalDatabaseDefinition {
  code: string;
  /** 四语名称映射；zh-CN 为必填基准。 */
  names: Record<string, string>;
  /** 适用实体 kind："all" 或固定八实体 kind 之一。 */
  category: string;
  url_pattern: string;
  icon: string;
  icon_url: string;
  validation_regex: string;
  description: string;
  sort_order: number;
  is_enabled: boolean;
  is_system: boolean;
  created_at?: string;
  updated_at?: string;
}

export interface ExternalLinkDisplay {
  code: string;
  name: string;
  icon: string;
  icon_url: string;
  external_id: string;
  url: string;
}

export function fetchExternalDatabases(category?: string): Promise<{ items: ExternalDatabaseDefinition[] }> {
  const q = category ? `?category=${encodeURIComponent(category)}` : "";
  // 统一通过主系统 /api/catalog/external-databases 获取
  return fetch(`/api/catalog/external-databases${q}`, { credentials: "same-origin" })
    .then(async (res) => {
      if (!res.ok) return { items: [] };
      return res.json();
    })
    .catch(() => ({ items: [] }));
}

export function fetchAdminExternalDatabases(): Promise<{ items: ExternalDatabaseDefinition[] }> {
  return fetch("/api/admin/external-databases", { credentials: "same-origin" })
    .then(async (res) => {
      if (!res.ok) {
        const body = await res.json().catch(() => ({}));
        throw new Error(body.error || `HTTP ${res.status}`);
      }
      return res.json();
    });
}

export function createExternalDatabase(data: Partial<ExternalDatabaseDefinition>): Promise<{ message: string; data: ExternalDatabaseDefinition }> {
  return fetch("/api/admin/external-databases", {
    method: "POST",
    credentials: "same-origin",
    headers: { "Content-Type": "application/json" },
    body: JSON.stringify(data),
  }).then(async (res) => {
    const body = await res.json().catch(() => ({}));
    if (!res.ok) throw new Error(body.error || `HTTP ${res.status}`);
    return body;
  });
}

export function updateExternalDatabase(code: string, data: Partial<ExternalDatabaseDefinition>): Promise<{ message: string; data: ExternalDatabaseDefinition }> {
  return fetch(`/api/admin/external-databases/${encodeURIComponent(code)}`, {
    method: "PUT",
    credentials: "same-origin",
    headers: { "Content-Type": "application/json" },
    body: JSON.stringify(data),
  }).then(async (res) => {
    const body = await res.json().catch(() => ({}));
    if (!res.ok) throw new Error(body.error || `HTTP ${res.status}`);
    return body;
  });
}

export function deleteExternalDatabase(code: string): Promise<{ message: string }> {
  return fetch(`/api/admin/external-databases/${encodeURIComponent(code)}`, {
    method: "DELETE",
    credentials: "same-origin",
  }).then(async (res) => {
    const body = await res.json().catch(() => ({}));
    if (!res.ok) throw new Error(body.error || `HTTP ${res.status}`);
    return body;
  });
}
