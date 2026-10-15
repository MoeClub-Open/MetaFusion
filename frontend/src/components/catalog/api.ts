import { pickRecordTitle } from "@/lib/titles";
import { resolveLocalizedName } from "@/lib/localizedNames";
import { fetchApi } from "@/lib/api";
import { ENTITY_KINDS } from "@/lib/kinds.generated";

/** 固定八种骨架：单一来源是目录库基线（见 frontend/scripts/generate-contracts.mjs）。 */
export const kinds: string[] = [...ENTITY_KINDS];
export type Names = Record<string, string>;
export type Field = {
  names: Names;
  type: string;
  enabled: boolean;
  required?: boolean;
  applicable_kinds?: string[];
  searchable?: boolean;
  comparable?: boolean;
  /** 存档/检索用途：可写可检索但不进详情面板与编辑器分组区。 */
  hidden?: boolean;
  /** 对比语义（闭集）："content" 为内容选择范围，"locating" 为本版定位。 */
  semantics?: string;
  unit?: Names;
  vocabulary?: string;
  kinds?: string[];
  fields?: Record<string, Field>;
  items?: Field;
  min?: number;
  max?: number;
  /** 仅 group 字段：组内任一其它子字段有值时该锚点子字段必填。 */
  anchor_key?: string;
  /** 仅 group 内的 number 子字段：声明本字段是同组该子字段的区间终点（起点不得大于终点）。 */
  range_start?: string;
};
export type Scheme = {
  names: Names;
  slot: string;
  kinds?: string[];
  medium_formats?: string[];
  fields: string[];
  required?: string[];
  require_range?: boolean;
  enabled: boolean;
};
export type Definitions = {
  schemes?: Record<string, Scheme>;
  /** 各层级的"所属与收录结构"：由服务端下发，编辑器据此渲染结构字段与资源区块。 */
  structure?: Record<
    string,
    {
      fields?: {
        code: string;
        names: Names;
        reverse_names: Names;
        target_kinds?: string[];
        scoped_by?: string;
        required?: boolean;
      }[];
      resources?: boolean;
      subjects?: boolean;
      subject_names?: Names;
      subject_reverse_names?: Names;
      contents?: boolean;
      content_names?: Names;
      content_reverse_names?: Names;
    }
  >;
  fields: Record<string, Field>;
  vocabularies: Record<
    string,
    { names: Names; terms: Record<string, { names: Names; enabled: boolean; is_bonus?: boolean }> }
  >;
  relations: Record<
    string,
    {
      names: Names;
      reverse_names: Names;
      source_kinds: string[];
      target_kinds: string[];
      fields: string[];
      symmetric: boolean;
      acyclic: boolean;
      scope?: string;
      cycle_group?: string;
      unique_position?: boolean;
      usage?: string;
      reference_scopes?: Record<string, string>;
      /** 聚合/组成关系：内容目录按它收录组成员（见 lib/definitions.ts RelationDef）。 */
      aggregate?: boolean;
      /** 署名槽位 person/character/peer，空=未声明（老文档）：展示端据此判定署名/角色。 */
      participant_slot?: string;
      /** 参与批量署名聚合：口径只看本声明，不看分组码（见 lib/definitions.ts）。 */
      counts_as_credit?: boolean;
      max_outgoing: number;
      max_incoming: number;
      group: string;
      group_names?: Names;
      enabled: boolean;
    }
  >;
  templates: Record<
    string,
    {
      names: Names;
      kinds?: string[];
      sections: { names: Names; fields: string[] }[];
      columns: string[];
      relation_groups: string[];
      directory: string;
      modules: string[];
      primary_date_field?: string;
      badge_fields?: string[];
      facet_fields?: string[];
      match?: import("@/lib/definitions").TemplateCondition[];
      priority?: number;
      blocks?: string[];
    }
  >;
};
export type ExternalLink = {
  kind: string;
  url: string;
  external_id?: string;
  role?: "official" | "primary" | "secondary" | "operator" | "";
  origin?: "migration" | "commit" | "";
  source_refs?: { source: string; pointers?: string[] }[];
};

export type Entity = {
  id?: string;
  kind: string;
  title: string;
  version: number;
  state: string;
  review_status?: "reviewed";
  created_by?: string;
  redirect_id?: string;
  original_language: string;
  translations: Record<
    string,
    { title: string; summary?: string; body?: string; aliases?: string[] }
  >;
  attributes: Record<string, any>;
  external_links: ExternalLink[];
  // 多图契约（见 backend/internal/catalog/types.go 的 PicturesJSON）：**数组顺序就是展示顺序**，
  // pictures[0] 即封面，服务端保存时不重排；taken_at 只是这张图自身的时间元信息、不参与排序。
  // 取封面一律走 lib/cover.ts 的 coverPicture / coverUrl，不在消费点重新索引 [0]。
  // role 是 picture_role 词表的用途码（空=未声明，存量图全部如此）；asset_id 是自托管封面
  // 在存储服务里的 assets UUID，此时 url 指向 /api/storage/assets/<uuid>/content。
  pictures: {
    url: string;
    caption: Names;
    taken_at?: string;
    version_label?: Names;
    usage_period?: { begin?: string; end?: string };
    role?: string;
    asset_id?: string;
    source: Source;
  }[];
  work_id?: string;
  content_unit_id?: string;
  release_id?: string;
  medium_id?: string;
  parent_id?: string;
  position: number;
  number: string;
  // 收录与发行对象的附加属性：键为 definitions 声明的子字段码
  // （inclusion_attributes / subject_attributes），未声明时为空。
  contents: {
    expression_id: string;
    position: number;
    locator: Record<string, any>;
    attributes?: Record<string, any>;
    sources?: Source[];
  }[];
  subjects: {
    work_id: string;
    role: string;
    position: number;
    attributes?: Record<string, any>;
  }[];
  updated_at?: string;
};
export type Source = { kind: string; citation: string; url?: string; key?: string; pointers?: string[]; scope?: string };
export type Relation = {
  id?: string;
  version: number;
  type: string;
  source_id: string;
  target_id: string;
  position: number;
  attributes: Record<string, any>;
};
// 能力清单是部署态声明，只有两态：id 是前端契约键，enabled 表示该子系统是否在场。
// 没有 healthy/version/dependencies——目录不探测上游，健康由网关/运维面各自读 /health。
export type Capability = {
  id: string;
  enabled: boolean;
};
export async function api<T = any>(
  path: string,
  method = "GET",
  data?: any,
  extraHeaders?: Record<string, string>,
): Promise<T> {
  // 统一走 lib/api.ts 的 fetchApi：它带同域 Cookie、按 locale 设头、
  // 并在 401 时静默续期后重试一次。此前这里自己 fetch，既不带令牌也没有续期——
  // 访问令牌 15 分钟一过，编辑器就再也写不进去（真人在编辑半小时后必然遇到）。
  return fetchApi<T>(path, {
    method,
    headers: extraHeaders,
    body:
      data === undefined
        ? undefined
        : data instanceof FormData
          ? data
          : JSON.stringify(data),
  });
}

// Browse uses offsets; search follows its PIT cursor even after a short page
// caused by current visibility rechecks. has_more is the completion contract.
export async function fetchAllPages<T = any>(query: string, pageSize = 100): Promise<T[]> {
	const items: T[] = [];
	const [path, rawQuery = ""] = query.split("?", 2);
	const params = new URLSearchParams(rawQuery);
	params.delete("page");
	params.set("limit", String(pageSize));
	for (let offset = 0; ; offset += pageSize) {
		if (!params.has("cursor")) params.set("offset", String(offset));
		const r = await api<{ items: T[]; has_more: boolean; next_cursor?: string }>(`${path}?${params}`);
		if (!Array.isArray(r?.items)) throw new Error("invalid_response: entities.items");
		if (typeof r.has_more !== "boolean") throw new Error("invalid_response: entities.has_more");
		items.push(...r.items);
		if (!r.has_more) return items;
		if (params.get("q")) {
			if (!r.next_cursor) throw new Error("invalid_response: entities.next_cursor");
			params.delete("offset"); params.set("cursor", r.next_cursor);
		}
	}
}

// mapLimit：带并发上限的顺序保底映射；详情页按实体逐个补取数据时防止请求风暴。
export async function mapLimit<T, R>(
  items: T[],
  limit: number,
  fn: (item: T, index: number) => Promise<R>,
): Promise<R[]> {
  const out: R[] = new Array(items.length);
  let next = 0;
  const workers = Array.from({ length: Math.max(1, Math.min(limit, items.length)) }, async () => {
    for (;;) {
      const i = next++;
      if (i >= items.length) return;
      out[i] = await fn(items[i], i);
    }
  });
  await Promise.all(workers);
  return out;
}
// 多语言回退链只在 lib/localizedNames.ts 实现一份（请求语种 → 同语系 → 简中 → 繁中 →
// 日文 → 英文 → 其余非空值）；这里只多一个"原文语种优先"的入参，供编辑/预览场景使用。
export function local(
  names: Names | undefined,
  locale: string,
  original = "",
  fallback = "",
) {
  const resolved = resolveLocalizedName(names, locale, "");
  if (resolved) return resolved;
  const raw = original ? names?.[original] : "";
  return typeof raw === "string" && raw.trim() ? raw.trim() : fallback;
}
export function title(e: Entity, locale: string, order: string[] = []) {
  return pickRecordTitle(locale, e.translations, e.title, {
    order,
    originalLanguage: e.original_language,
  });
}
export function emptyEntity(kind = "work"): Entity {
  return {
    kind,
    title: "",
    version: 0,
    state: "active", review_status: "reviewed",
    original_language: "",
    translations: {},
    attributes: {},
    external_links: [],
    pictures: [],
    position: 0,
    number: "",
    contents: [],
    subjects: [],
  };
}
