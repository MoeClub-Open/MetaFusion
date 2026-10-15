/** Local filtering must include the same identity labels people use in catalog search. */
export function matchesEntityQuery(entity: {
  title?: string;
  translations?: Record<string, { title?: string; summary?: string; body?: string; aliases?: string[] }>;
  external_ids?: Record<string, string>;
  attributes?: Record<string, unknown>;
}, query: string): boolean {
  const needle = query.trim().toLocaleLowerCase();
  if (!needle) return true;
  const values = [entity.title || "", JSON.stringify(entity.attributes || {})];
  for (const translation of Object.values(entity.translations || {})) {
    values.push(translation.title || "", translation.summary || "", translation.body || "", ...(translation.aliases || []));
  }
  for (const [key, value] of Object.entries(entity.external_ids || {})) values.push(value, `${key}:${value}`);
  return values.some((value) => value.toLocaleLowerCase().includes(needle));
}
