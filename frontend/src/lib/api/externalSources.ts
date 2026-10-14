// 域：外部来源只读查询 /api/external-sources/*
import { fetchApi } from "./client";

export interface FetchMeta {
  url: string;
  fetched_at: string;
  content_hash: string;
}

export interface ExternalSourceQueryRequest {
  source?: string;
  op: "search" | "lookup" | "detail";
  q?: string;
  id?: string;
  url_or_id?: string;
  entity_type?: string;
  limit?: number;
}

export interface ExternalSourceSearchItem {
  id: string;
  title: string;
  entity_type?: string;
  url?: string;
  snippet?: string;
  raw?: unknown;
}

/** lookup/detail 仍返回既有 preview 映射形状（只读）。 */
export interface ExternalSourcePreview {
  source: string;
  entity_type?: string;
  external_id: string;
  external_url: string;
  media_type: string;
  work?: Record<string, unknown>;
  artist?: Record<string, unknown>;
  artists?: Record<string, unknown>[];
  has_release?: boolean;
  canonical_entries?: Record<string, unknown>[];
  release?: Record<string, unknown> | null;
  mediums?: Record<string, unknown>[];
  tags: string[];
  warnings?: string[];
}

export interface ExternalSourceQueryResponse {
  source: string;
  op: string;
  fetch?: FetchMeta;
  fetches?: FetchMeta[];
  raw?: unknown;
  preview?: ExternalSourcePreview;
  items?: ExternalSourceSearchItem[];
}

export interface ExternalSource {
  id: string;
  names: Record<string, string>;
  category: string;
  icon: string;
  description: string;
  url_pattern?: string;
}

export function fetchExternalSources() {
  return fetchApi<{ items: ExternalSource[] }>("/external-sources");
}

export function queryExternalSource(body: ExternalSourceQueryRequest) {
  return fetchApi<ExternalSourceQueryResponse>("/external-sources/query", {
    method: "POST",
    body: JSON.stringify(body),
  });
}
