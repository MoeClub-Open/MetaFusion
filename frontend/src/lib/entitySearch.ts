/** Local filtering must include the same identity labels people use in catalog search. */
export function matchesEntityQuery(entity: {
  title?: string;
  translations?: Record<string, { title?: string; summary?: string; aliases?: string[] }>;
  external_links?: { kind?: string; external_id?: string; url?: string }[];
  attributes?: Record<string, unknown>;
}, query: string): boolean {
  const needle = query.trim().toLocaleLowerCase();
  if (!needle) return true;
  const values = [entity.title || "", JSON.stringify(entity.attributes || {})];
  for (const translation of Object.values(entity.translations || {})) {
    values.push(translation.title || "", translation.summary || "", ...(translation.aliases || []));
  }
  for (const link of entity.external_links || []) {
    if (link.external_id) values.push(link.external_id, `${link.kind || ""}:${link.external_id}`);
    if (link.url) values.push(link.url);
  }
  return values.some((value) => value.toLocaleLowerCase().includes(needle));
}
