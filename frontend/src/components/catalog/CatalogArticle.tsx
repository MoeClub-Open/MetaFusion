"use client";

import { useEffect, useMemo } from "react";
import MarkdownRenderer from "@/components/MarkdownRenderer";
import { useI18n } from "@/i18n/I18nProvider";
import { articleHeadings } from "@/lib/markdown";

export function CatalogArticle({ content, idPrefix = "article", lang }: { content: string; idPrefix?: string; lang?: string }) {
  const { t } = useI18n();
  const headings = useMemo(() => articleHeadings(content, idPrefix), [content, idPrefix]);
  useEffect(() => {
    let frame = 0;
    const scrollToFragment = () => {
      let hash: string;
      try { hash = decodeURIComponent(window.location.hash.slice(1)); } catch { return; }
      if (!hash.startsWith(`${idPrefix}-`)) return;
      cancelAnimationFrame(frame);
      frame = requestAnimationFrame(() => document.getElementById(hash)?.scrollIntoView({ block: "start" }));
    };
    scrollToFragment();
    window.addEventListener("hashchange", scrollToFragment);
    return () => { cancelAnimationFrame(frame); window.removeEventListener("hashchange", scrollToFragment); };
  }, [content, idPrefix]);
  return (
    <article lang={lang} className="min-w-0 space-y-5">
      {headings.length > 1 && (
        <details open className="rounded-lg border border-line bg-surfaceSubtle p-3 text-sm">
          <summary className="cursor-pointer font-semibold text-text-strong">{t("catalog.article.contents")}</summary>
          <nav aria-label={t("catalog.article.contents")} className="mt-2">
            <ol className="space-y-1">
              {headings.map((heading) => <li key={heading.id} style={{ paddingInlineStart: `${Math.min(heading.depth - 1, 3) * 0.75}rem` }}>
                <a className="text-primary hover:underline break-words" href={`#${encodeURIComponent(heading.id)}`}>{heading.text}</a>
              </li>)}
            </ol>
          </nav>
        </details>
      )}
      <MarkdownRenderer content={content} headingPrefix={idPrefix} footnoteLabel={t("catalog.article.references")} footnoteBackLabel={t("catalog.article.backReference")}
        className="[&_h1]:scroll-mt-28 [&_h2]:scroll-mt-28 [&_h3]:scroll-mt-28 [&_h4]:scroll-mt-28 [&_h5]:scroll-mt-28 [&_h6]:scroll-mt-28 [&_[id]]:scroll-mt-28" />
    </article>
  );
}
