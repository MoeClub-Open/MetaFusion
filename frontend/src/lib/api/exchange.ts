// 实例间交换（/api/exchange/*）：导出实体快照 + 提交外部编辑提案。
//
// 契约来源（只读核对，本任务未改后端）：
//   backend/internal/catalog/exchange.go       两个 handler 与状态码
//   backend/internal/catalog/http.go:252       registerExchange 的挂载点
//   backend/internal/catalog/types.go:50       Entity 快照字段
//   backend/internal/catalog/validation.go:176 evidence_required / invalid_source 的判定
//
// 两个端点的成功响应都**没有外层包装**：GET 回裸实体快照，POST 回保存后的实体本身，
// 所以这里不做解包，也不臆造 { data: … } 之类的外壳。
//
// 路由在 attachUser 与审计中间件之后注册；导出可见性与提案作者均由服务端身份判定。

import { ApiError, fetchApi } from "./client";
import { localizeCatalogError } from "@/lib/catalogErrors";

export interface ExchangeTranslation {
  title?: string;
  summary?: string;
  aliases?: string[];
}

export interface ExchangePicture {
  url?: string;
  caption?: Record<string, string>;
  /** 这张图自身的时间：只作元信息，**不决定顺序**（数组顺序即展示顺序，见 lib/cover.ts）。 */
  taken_at?: string;
  /** 用途码（definitions 的 picture_role 词表）；空 = 未声明用途。 */
  role?: string;
  /** 自托管封面在存储服务里的 assets UUID。 */
  asset_id?: string;
  source?: { kind?: string; citation?: string; url?: string };
}

export interface ExchangeInclusion {
  expression_id?: string;
  position?: number;
  locator?: Record<string, unknown>;
  attributes?: Record<string, unknown>;
}

export interface ExchangeSubject {
  work_id?: string;
  role?: string;
  position?: number;
  attributes?: Record<string, unknown>;
}

/**
 * 实体快照，字段与后端 types.go 的 Entity 逐字对应。
 *
 * kind=track 才有 contents、kind=release 才有 subjects，其它情况服务端回 null；
 * **快照不含 relations**（它不是带关系的详情 DTO）。
 * 字段全部可选：导出的结果要能原样回灌到 proposals 的 entity 字段，
 * 这里刻意保持宽松，实体的必填性由服务端校验（translation_required 等码）决定。
 */
export interface ExchangeEntity {
  id?: string;
  kind?: string;
  version?: number;
  title?: string;
  original_language?: string;
  translations?: Record<string, ExchangeTranslation>;
  attributes?: Record<string, unknown>;
  external_links?: { kind: string; url: string; external_id?: string; role?: string }[];
  pictures?: ExchangePicture[];
  status?: string;
  created_by?: string;
  redirect_id?: string;
  work_id?: string;
  content_unit_id?: string;
  release_id?: string;
  medium_id?: string;
  parent_id?: string;
  position?: number;
  number?: string;
  contents?: ExchangeInclusion[] | null;
  subjects?: ExchangeSubject[] | null;
  updated_at?: string;
}

/**
 * GET /exchange/entities/:id —— 导出实体快照。
 * 路由上没有闸门（匿名可达），可见性在 store 里判；因此任何错误都是 404 not_found
 * （连非法 uuid 也是 404），只有 marshal 失败才是 500 encode_failed。
 */
export function fetchExchangeEntity(id: string): Promise<ExchangeEntity> {
  return fetchApi<ExchangeEntity>(`/exchange/entities/${encodeURIComponent(id)}`);
}

/** 新编辑凭据只接受外部链接或出版物；历史 self 不可用于新的提案。 */
export const EXCHANGE_SOURCE_KINDS = ["url", "publication"] as const;
export type ExchangeSourceKind = (typeof EXCHANGE_SOURCE_KINDS)[number];

export interface ExchangeSource {
  kind: ExchangeSourceKind;
  citation: string;
  url?: string;
}

export interface ExchangeProposalInput {
  entity: ExchangeEntity;
  expectedVersion: number;
  editNote: string;
  sources: ExchangeSource[];
}

/**
 * POST /exchange/proposals —— 提交外部编辑提案。
 *
 * 顶层键必须与 Edit 结构体逐字一致：http.body() 用 DisallowUnknownFields，
 * 多一个顶层键（snapshot / payload / document 之类）就是 400 invalid_payload
 * —— 粘贴的实体对象放在 entity 里，不叫别的名字。
 * url 为空时不发该键，避免把空串当成"给了链接"。
 */
export function submitExchangeProposal(input: ExchangeProposalInput): Promise<ExchangeEntity> {
  const sources = input.sources.map((source) =>
    source.url && source.url.trim() !== ""
      ? { kind: source.kind, citation: source.citation, url: source.url.trim() }
      : { kind: source.kind, citation: source.citation }
  );
  return fetchApi<ExchangeEntity>("/exchange/proposals", {
    method: "POST",
    body: JSON.stringify({
      entity: input.entity,
      expected_version: input.expectedVersion,
      edit_note: input.editNote,
      sources,
    }),
  });
}

/**
 * 本地镜像后端 validURL（validation.go:39）：只认带主机的 http/https，且不接受 userinfo。
 * 目的仅是少发一次注定 400 invalid_source 的请求；最终判定仍以服务端为准。
 */
export function isValidSourceUrl(raw: string): boolean {
  try {
    const parsed = new URL(raw);
    const scheme = parsed.protocol;
    return (
      (scheme === "http:" || scheme === "https:") &&
      parsed.hostname !== "" &&
      parsed.username === "" &&
      parsed.password === ""
    );
  } catch {
    return false;
  }
}

// 翻译函数形状只有一份定义（i18n/I18nProvider）：这里只做类型再导出，避免各模块各写一遍。
import type { TranslateFn } from "@/i18n/I18nProvider";
export type { TranslateFn };

/**
 * 交换端点的错误码 → 文案键。
 *
 * 这里逐条翻译而不复用 general 的 catalog 映射：同一个码在交换场景下的处置不同
 * （例如 version_conflict 在这里是 expected_version 对不上，use_lifecycle_endpoint
 * 是"提案一律进待审"与已发布实体撞车），笼统文案会把唯一有用的信息掩盖掉。
 */
const EXCHANGE_ERROR_KEYS: Record<string, string> = {
  authentication_required: "admin.exchange.errUnauthorized",
  forbidden: "admin.exchange.errForbidden",
  not_found: "admin.exchange.errNotFound",
  invalid_payload: "admin.exchange.errInvalidPayload",
  evidence_required: "admin.exchange.errEvidenceRequired",
  invalid_source: "admin.exchange.errInvalidSource",
  version_conflict: "admin.exchange.errVersionConflict",
  constraint_violation: "admin.exchange.errConstraintViolation",
  invalid_id: "admin.exchange.errInvalidId",
  use_lifecycle_endpoint: "admin.exchange.errUseLifecycle",
  immutable_scope: "admin.exchange.errImmutableScope",
  undeclared_release_subject: "admin.exchange.errUndeclaredSubject",
  database_error: "admin.exchange.errDatabase",
  encode_failed: "admin.exchange.errEncode",
};

/**
 * 把交换端点的失败翻成如实的人话。
 *
 * 三层回退：交换专属码表 → 目录写入通用码表（unknown_field / four_locale_names_required /
 * translation_required 这类定义校验码已有四语文案，不重复造词）→ 未登记的码带状态码原样显示。
 * 绝不把失败说成成功，也不把裸码或空串吞掉。
 */
export function describeExchangeError(err: unknown, t: TranslateFn): string {
  if (err instanceof ApiError) {
    const message = String(err.message || "");
    // 目录服务的错误串可能带补充（unknown_field: duration），按冒号分段取第一个已登记的码。
    for (const segment of message.split(":")) {
      const key = EXCHANGE_ERROR_KEYS[segment.trim()];
      if (key) return t(key);
    }
    const localized = localizeCatalogError(message, t);
    if (localized !== message) return localized;
    return t("admin.exchange.errUnknown", {
      status: err.status,
      code: message !== "" ? message : `HTTP ${err.status}`,
    });
  }
  return t("admin.exchange.errNetwork", {
    message: err instanceof Error ? err.message : String(err),
  });
}
