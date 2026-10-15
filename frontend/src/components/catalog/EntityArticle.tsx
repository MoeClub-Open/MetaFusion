"use client";

import { useState } from "react";
import Link from "next/link";
import { useI18n } from "@/i18n/I18nProvider";
import { useAuth } from "@/lib/authContext";
import { canEditEntity } from "@/lib/permissions";
import { languageLabel } from "@/lib/languages";
import { buildTitleChain, findRowForLocale } from "@/lib/titles";
import { articleTranslations, pickArticleTranslation } from "@/lib/catalogArticle";
import { Select } from "@/components/ui/Select";
import type { Entity } from "./api";
import { CatalogArticle } from "./CatalogArticle";

export function EntityArticle({ entity, titleOrder }: { entity: Entity; titleOrder?: string[] }) {
  const { t, locale } = useI18n();
  const { user } = useAuth();
  const [selection, setSelection] = useState<{ entityID?: string; uiLocale: string; locale: string }>();
  const rows = articleTranslations(entity);
  const chosen = selection && selection.entityID === entity.id && selection.uiLocale === locale
    ? rows.find((row) => row.locale === selection.locale) : undefined;
  const article = chosen || pickArticleTranslation(entity, locale, titleOrder);
  const summaries = Object.entries(entity.translations || {}).map(([lang, row]) => ({ locale: lang, summary: row.summary }));
  const chain = buildTitleChain(article?.locale || locale, { order: article ? [article.locale] : titleOrder, originalLanguage: entity.original_language }, summaries.map((row) => row.locale));
  const summary = chain.map((lang) => findRowForLocale(summaries, lang)?.summary?.trim()).find(Boolean)
    || (typeof entity.attributes?.summary === "string" ? entity.attributes.summary : "")
    || (typeof entity.attributes?.description === "string" ? entity.attributes.description : "");
  return (
    <div className="min-w-0 space-y-4">
      <div className="flex flex-wrap items-center justify-between gap-3 text-xs">
        {article && <div className="flex items-center gap-2 min-w-0">
          <span className="text-text-muted">{t("catalog.article.language")}</span>
          {rows.length > 1 ? <Select value={article.locale} aria-label={t("catalog.article.language")} fullWidth={false}
            options={rows.map((row) => ({ value: row.locale, label: languageLabel(row.locale) }))}
            onChange={(next) => setSelection({ entityID: entity.id, uiLocale: locale, locale: next })} />
            : <span className="text-text-body">{languageLabel(article.locale)}</span>}
        </div>}
        {entity.id && canEditEntity(user ?? undefined, entity) && <Link className="text-primary hover:underline ml-auto"
          href={`/catalog/${entity.id}?edit=1&section=article${article ? `&article_locale=${encodeURIComponent(article.locale)}` : ""}`}>
          {t(article ? "catalog.article.edit" : "catalog.article.add")}
        </Link>}
      </div>
      {summary && <p className="text-sm text-text-strong leading-relaxed whitespace-pre-line">{summary}</p>}
      {article ? <CatalogArticle content={article.body} lang={article.locale} />
        : <p className="text-xs text-text-muted">{t("catalog.article.empty")}</p>}
    </div>
  );
}
