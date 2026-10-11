import type { Source } from "@/components/catalog/api";

/** 编辑说明说明操作原因；来源必须由用户填写，不能从说明自动生成。 */
export function catalogEvidenceError(note: string, sources: Source[], instanceOrigin?: string): string | null {
  if (!note.trim() || sources.length === 0 || sources.some((source) => !source.citation.trim())) {
    return "evidence_required";
  }
  const instanceHost = instanceOrigin ? new URL(instanceOrigin).hostname.toLowerCase() : "";
  for (const source of sources) {
    if (source.kind !== "url" && source.kind !== "publication") return "source_not_eligible";
    const rawUrl = (source.url || "").trim();
    if (!rawUrl) {
      if (source.kind === "url") return "invalid_source";
      continue;
    }
    try {
      const url = new URL(rawUrl);
      if (url.protocol !== "https:" && url.protocol !== "http:") return "invalid_source";
      if (instanceHost && url.hostname.toLowerCase() === instanceHost) return "source_not_eligible";
    } catch {
      return "invalid_source";
    }
  }
  return null;
}
