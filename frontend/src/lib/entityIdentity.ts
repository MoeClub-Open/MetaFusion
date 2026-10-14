/** Compact, factual disambiguation for same-title records; no inferred edition or author. */
export function entityIdentitySuffix(entity: {
  id?: string;
  external_links?: { kind?: string; external_id?: string; url?: string }[];
}): string {
  const external = (entity.external_links || [])
    .filter((link) => typeof (link.external_id || link.url) === "string" && String(link.external_id || link.url).trim())
    .slice(0, 2)
    .map((link) => `${link.kind}: ${link.external_id || link.url}`);
  return [...external, ...(entity.id ? [entity.id.slice(0, 8)] : [])].join(" · ");
}
