import { fetchApi } from "./client";
import type { User } from "./client";
import { canonicalDetailPath } from "../entityRoutes";
import { resolveLocalizedName } from "../localizedNames";
import type { Source } from "@/components/catalog/api";
import { catalogEvidenceError } from "../catalogEvidence";

export interface Tag {
  id: number;
  name: string;
}



export function pickLocalizedName(
  locale: string,
  names?: Record<string, string> | null,
  defaultSlug?: string
): string {
  return resolveLocalizedName(names, locale, defaultSlug || "");
}

export interface ConnectedEntityItem {
  entity_id: string;
  entity_name: string;
  entity_type: string;
  original_name?: string;
  cover_url?: string;
  country?: string;
  relationship_type: string;
  relationship_name: string;
  /** 关系行 id：同一类型同一对端存在多条边（多次署名）时用它区分身份。 */
  relation_id?: string;
  qualifier?: string;
  direction: 'forward' | 'reverse';
  label: string;
  begin_date?: string;
  end_date?: string;
  ended?: boolean;
  is_current?: boolean;
  date_span?: string;
  attributes: Record<string, any>;
}

export interface EntityRevision {
  id: string;
  target_type: string;
  target_id: string;
  editor_id?: string;
  edit_type: string;
  summary: string;
  edit_note: string;
  source_urls?: string[];
  before_state: Record<string, any>;
  after_state: Record<string, any>;
  diff: Record<string, { old: any; new: any }>;
  status: string;
  created_at: string;
  editor?: User;
}

export interface EntityRelationship {
  id?: string;
  source_type: string;
  source_id: string;
  target_type: string;
  target_id: string;
  relationship_type: string;
  qualifier?: string;
  begin_date?: string;
  end_date?: string;
  ended?: boolean;
  attributes: Record<string, any>;
  created_at?: string;
  updated_at?: string;
}

export interface RelationType {
  code: string;
  domain: string;
  names?: Record<string, string>;
  description?: string;
  is_symmetric: boolean;
  is_hierarchical: boolean;
  // 是否具有时间语义（任期/隶属/合约期）：为 true 时编辑器展示 begin/end/ended 输入。
  is_temporal?: boolean;
  attribute_schema?: { fields?: any[] } | any[];
  color: string;
  icon: string;
  sort_order: number;
  is_system: boolean;
  is_enabled: boolean;
  created_at?: string;
  display_name?: string;
  forward_label?: string;
  reverse_label?: string;
}









export function catalogEntityHref(id: string): string {
  return canonicalDetailPath(id) || `/catalog/${id}`;
}

export interface GraphNode {
  id: string;
  name: string;
  original_name?: string;
  type: string;
  category: string;
  role?: string;
  level: number;
  cover_image_url?: string;
  disambiguation?: string;
  country?: string;
  status?: string;
}

export interface GraphLink {
  id?: string;
  source: string;
  target: string;
  source_type?: string;
  target_type?: string;
  type: string;
  label: string;
  qualifier?: string;
  color?: string;
  attributes?: Record<string, any>;
  begin_date?: string;
  end_date?: string;
  ended?: boolean;
  is_hierarchical?: boolean;
}

// 合并走实体生命周期端点：POST /catalog/entities/:id/lifecycle（action=merge 语义由
// target_id 表达，服务端把 source 并入 target 并改写引用）。
export async function mergeEntities(payload: {
  source_id: string;
  target_id: string;
  merge_note: string;
  /** 用户填写的外部链接或出版物凭据。 */
  sources: Source[];
}): Promise<{ message: string; target_id: string }> {
  const note = (payload.merge_note || "").trim();
  const error = catalogEvidenceError(note, payload.sources);
  if (error) throw new Error(error);
  const source = await fetchApi<{ version: number }>(`/catalog/entities/${payload.source_id}`);
  await fetchApi(`/catalog/entities/${payload.source_id}/lifecycle`, {
    method: "POST",
    body: JSON.stringify({
      expected_version: source.version,
      target_id: payload.target_id,
      edit_note: note,
      sources: payload.sources,
    }),
  });
  return { message: "merged", target_id: payload.target_id };
}
