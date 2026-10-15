import type { Entity } from "@/components/catalog/api";
import { buildTitleChain, findRowForLocale } from "./titles";

export const MAX_ARTICLE_LENGTH = 100000;
export const ARTICLE_ANCHOR_TABS = { "article-": "overview" };

export function articleTranslations(entity: Pick<Entity, "translations">) {
  return Object.entries(entity.translations || {}).filter(([, row]) => row.body?.trim())
    .map(([locale, row]) => ({ locale, body: row.body! }));
}

export function pickArticleTranslation(entity: Pick<Entity, "translations" | "original_language">, locale: string, order?: string[]) {
  const rows = articleTranslations(entity);
  const chain = buildTitleChain(locale, { originalLanguage: entity.original_language, order }, rows.map((row) => row.locale));
  for (const candidate of chain) {
    const row = findRowForLocale(rows, candidate);
    if (row) return row;
  }
  return undefined;
}

export function articleBodyError(body: string): "article_too_long" | "invalid_article_body" | undefined {
  if (body.includes("\0")) return "invalid_article_body";
  if (Array.from(body).length > MAX_ARTICLE_LENGTH) return "article_too_long";
}
