// 域：外部来源只读查询 /api/external-sources/*
import { fetchApi } from "./client";

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
