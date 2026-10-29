import { fetchApi } from "./client";
import { canonicalDetailPath } from "../entityRoutes";
import { resolveLocalizedName } from "../localizedNames";
import type { Source } from "@/components/catalog/api";

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
