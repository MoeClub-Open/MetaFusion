// 本人操作记录：读取面是账号服务的 GET /api/admin/audit-logs（跨服务审计表 audit.audit_log）。
//
// 同一个端点在服务端分两档（契约 docs/architecture/audit-log.md §5）：持 auth.audit.read 者按
// 全量过滤条件查（管理台排障），其余登录用户被强制收敛到 actor_user_id = 自己。不带
// actor_user_id 参数时管理员会读到全站记录。因此自助视图始终明确传当前用户 ID，服务端继续
// 校验调用者允许读取的范围；管理台的全量查询能力不受影响。
//
// 跨用户过滤（actor_user_id 指向他人、或 actor 前缀）服务端回 403 而不是静默改写成自己，
// 调用方必须当失败处理，不能当成"查到了别人的记录"。
import { fetchApi } from "./client";
import { requireArray, safeCount } from "./fields";

/** 与 audit.audit_log 的列一一对应；changes 已由写入侧脱敏（"[redacted]" / "a***@domain"）。 */
export interface AuditLogEntry {
  id: string;
  occurred_at: string;
  /** 产生这条记录的服，取值 catalog / auth / community / storage。 */
  service: string;
  /** 稳定机器码，形如 <域>.<过去式动作>（如 entity.saved、session.login）。只增不改。 */
  action: string;
  actor_user_id?: string;
  actor_username?: string;
  /** session / pat / oauth / anonymous：区分"用登录会话做的"与"用令牌做的"。 */
  credential_type?: string;
  actor_ip?: string;
  actor_user_agent?: string;
  target_type?: string;
  target_id?: string;
  /** 变更摘要，已脱敏并截断；空对象表示这次操作没有可摘要的字段变化。 */
  changes?: Record<string, unknown>;
  /** success / failure。 */
  result?: string;
  /** 失败原因码（与响应体的 error 同值，如 forbidden、invalid_payload）。 */
  error_code?: string;
  request_method?: string;
  route?: string;
  http_status?: number;
  /** 与请求头 X-Request-Id 同值，排障时用它对齐日志。 */
  request_id?: string;
}

export interface AuditLogPage {
  items: AuditLogEntry[];
  /** 与服务端同一套过滤条件下的总行数，不是本页条数。 */
  total: number;
  page: number;
  per_page: number;
}

/** 与账号服务的响应形状一致：单页 20 条足够读完一屏，翻页由调用方控制。 */
export const AUDIT_PAGE_SIZE = 20;

export async function fetchOwnAuditLogs(userId: string, page = 1, perPage = AUDIT_PAGE_SIZE): Promise<AuditLogPage> {
  const actorUserId = userId.trim();
  if (!actorUserId) throw new Error("actor_user_id_required");
  const params = new URLSearchParams({ actor_user_id: actorUserId, page: String(page), per_page: String(perPage) });
  const res = await fetchApi<{ items?: unknown[]; total?: number; page?: number; per_page?: number }>(
    "/admin/audit-logs?" + params.toString()
  );
  // 界面上有"暂无记录"文案，所以取不到 items 必须抛（requireArray），不能折成空数组——
  // 那会把"读不到"讲成"你没做过任何操作"（同 lib/api/fields.ts 的口径）。
  const rows = requireArray<unknown>(res?.items, "items");
  const currentPage = safeCount(res?.page, page);
  const currentSize = safeCount(res?.per_page, perPage);
  return {
    items: rows.map(normalizeAuditEntry),
    total: safeCount(res?.total, rows.length),
    // 服务端保证 page>=1 / per_page>=1；真回 0 时按请求值兜底，避免分页控件算出零页。
    page: currentPage < 1 ? page : currentPage,
    per_page: currentSize < 1 ? perPage : currentSize,
  };
}

// normalizeAuditEntry 收敛一条记录：只给高频字段稳定类型，changes 原样透传——
// 它的形状由写入侧与契约决定，界面按 JSON 展示，不做二次加工（更不"还原"脱敏值）。
function normalizeAuditEntry(raw: unknown): AuditLogEntry {
  const row = (raw ?? {}) as Record<string, any>;
  return {
    id: String(row.id ?? ""),
    occurred_at: typeof row.occurred_at === "string" ? row.occurred_at : "",
    service: str(row.service) ?? "",
    action: str(row.action) ?? "",
    actor_user_id: str(row.actor_user_id),
    actor_username: str(row.actor_username),
    credential_type: str(row.credential_type),
    actor_ip: str(row.actor_ip),
    actor_user_agent: str(row.actor_user_agent),
    target_type: str(row.target_type),
    target_id: str(row.target_id),
    changes:
      row.changes && typeof row.changes === "object" && !Array.isArray(row.changes)
        ? (row.changes as Record<string, unknown>)
        : undefined,
    result: str(row.result),
    error_code: str(row.error_code),
    request_method: str(row.request_method),
    route: str(row.route),
    http_status: typeof row.http_status === "number" ? row.http_status : undefined,
    request_id: str(row.request_id),
  };
}

function str(value: unknown): string | undefined {
  return typeof value === "string" && value !== "" ? value : undefined;
}
