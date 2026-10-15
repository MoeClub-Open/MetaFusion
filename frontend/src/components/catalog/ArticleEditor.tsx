"use client";

import { useId, useState } from "react";
import dynamic from "next/dynamic";
import { useI18n } from "@/i18n/I18nProvider";
import { MAX_ARTICLE_LENGTH } from "@/lib/catalogArticle";
import { CatalogArticle } from "./CatalogArticle";

const ArticleRichEditor = dynamic(() => import("./ArticleRichEditor"), { ssr: false });

export function ArticleEditor({ value, onChange, locale }: { value: string; onChange: (body: string) => void; locale: string }) {
  const { t } = useI18n();
  const id = useId();
  const [mode, setMode] = useState<"visual" | "source" | "preview">("visual");
  const [unsupported, setUnsupported] = useState(false);
  const count = Array.from(value).length;
  return (
    <div className="space-y-3 min-w-0">
      <div className="flex flex-wrap justify-between items-center gap-2">
        <label id={`${id}-label`} htmlFor={mode === "source" ? `${id}-body` : undefined} className="font-semibold">{t("catalog.article.body")}</label>
        <div role="group" aria-label={t("catalog.article.mode")} className="flex gap-1">
          {(["visual", "source", "preview"] as const).map((item) => <button key={item} type="button" aria-pressed={mode === item}
            className={mode === item ? "!border-primary !text-primary !bg-primary/10" : ""} onClick={() => { setMode(item); if (item === "visual") setUnsupported(false); }}>{t(`catalog.article.${item}`)}</button>)}
        </div>
      </div>
      <p className="cv-hint" id={`${id}-hint`}>{t("catalog.article.hint")}</p>
      {unsupported && <p role="status" className="rounded-lg border border-warn/30 bg-warn/10 p-3 text-sm text-text-body">{t("catalog.article.unsupported")}</p>}
      {mode === "visual" ? <ArticleRichEditor value={value} onChange={onChange} locale={locale} labelId={`${id}-label`}
        onUnsupported={() => { setUnsupported(true); setMode("source"); }} /> : mode === "preview" ? <div className="rounded-lg border border-line bg-surface p-4 min-h-64">
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
