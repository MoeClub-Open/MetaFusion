"use client";

import { useId, useState } from "react";
import { useI18n } from "@/i18n/I18nProvider";
import { MAX_ARTICLE_LENGTH } from "@/lib/catalogArticle";
import { CatalogArticle } from "./CatalogArticle";

export function ArticleEditor({ value, onChange, locale }: { value: string; onChange: (body: string) => void; locale: string }) {
  const { t } = useI18n();
  const id = useId();
  const [preview, setPreview] = useState(false);
  const count = Array.from(value).length;
  return (
    <div className="space-y-3 min-w-0">
      <div className="flex flex-wrap justify-between items-center gap-2">
        <label htmlFor={`${id}-body`} className="font-semibold">{t("catalog.article.body")}</label>
        <div role="group" aria-label={t("catalog.article.mode")} className="flex gap-1">
          <button type="button" aria-pressed={!preview} onClick={() => setPreview(false)}>{t("catalog.article.write")}</button>
          <button type="button" aria-pressed={preview} onClick={() => setPreview(true)}>{t("catalog.article.preview")}</button>
        </div>
      </div>
      <p className="cv-hint" id={`${id}-hint`}>{t("catalog.article.hint")}</p>
      {preview ? <div className="rounded-lg border border-line bg-surface p-4 min-h-64">
        {value.trim() ? <CatalogArticle content={value} idPrefix={`preview-${id.replace(/[^a-zA-Z0-9_-]/g, "")}`} lang={locale} />
          : <p className="cv-hint">{t("catalog.article.empty")}</p>}
      </div> : <textarea id={`${id}-body`} value={value} onChange={(event) => onChange(event.target.value)} rows={22}
        aria-label={`${locale} · ${t("catalog.article.body")}`} aria-describedby={`${id}-hint ${id}-count`}
        aria-invalid={count > MAX_ARTICLE_LENGTH} spellCheck className="font-mono text-sm !min-h-80 resize-y" />}
      <p id={`${id}-count`} className={`text-xs ${count > MAX_ARTICLE_LENGTH ? "text-danger" : "text-text-muted"}`}>
        {t("catalog.article.length", { count, max: MAX_ARTICLE_LENGTH })}
      </p>
      <details className="rounded-lg border border-line p-3 text-sm">
        <summary className="cursor-pointer">{t("catalog.article.syntax")}</summary>
        <pre className="mt-2 whitespace-pre-wrap break-words text-xs font-mono">{t("catalog.article.example")}</pre>
      </details>
    </div>
  );
}
