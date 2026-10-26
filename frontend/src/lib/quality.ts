/** 质量审计：与 GET /catalog/entities/{id}/quality、POST /catalog/quality/batch 对齐。
 * 按需计算、不落库，可见性与实体详情相同（公开条目匿名可读）。 */

export type QualityStatus = "complete" | "partial" | "needs_verification";

export type QualityIssue = {
  code: string;
  severity: string;
  entity_id: string;
  field?: string;
  value?: string;
  message: string;
};

export type QualityReport = {
  entity_id: string;
  kind: string;
  status: QualityStatus;
  issues: QualityIssue[];
  checks: string[];
  definition_etag?: string;
};

export type QualityBatchResult = {
  reports: QualityReport[];
  errors: Record<string, string>;
};

/** 诊断码 → 详情页签 id（EntityDetailView 的 hash tab）。 */
export function qualityIssueTab(code: string): string {
  switch (code) {
    case "dangling_reference":
      return "relations";
    case "missing_required_field":
      return "overview";
    case "release_missing_media":
    case "medium_missing_tracks":
    case "track_empty_contents":
    case "track_position_gap":
      return "contents";
    default:
      return "overview";
  }
}

export async function fetchEntityQuality(entityId: string): Promise<QualityReport> {
  const res = await fetch(`/api/catalog/entities/${encodeURIComponent(entityId)}/quality`, {
    credentials: "same-origin",
  });
  if (!res.ok) {
    const body = await res.json().catch(() => ({}));
    throw new Error(typeof body?.error === "string" ? body.error : `quality_${res.status}`);
  }
  return res.json();
}

/** 批量审计当前页实体；失败时抛错，调用方应静默隐藏圆点。 */
export async function fetchQualityBatch(ids: string[]): Promise<QualityBatchResult> {
  const res = await fetch("/api/catalog/quality/batch", {
    method: "POST",
    credentials: "same-origin",
    headers: { "Content-Type": "application/json" },
    body: JSON.stringify({ ids }),
  });
  if (!res.ok) {
    const body = await res.json().catch(() => ({}));
    throw new Error(typeof body?.error === "string" ? body.error : `quality_batch_${res.status}`);
  }
  return res.json();
}
