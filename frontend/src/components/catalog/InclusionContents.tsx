"use client";

import Link from "next/link";
import { useI18n } from "@/i18n/I18nProvider";
import { useDefinitions } from "@/lib/definitions";
import { Entity, type Source, title } from "./api";
import { readerSourceLabel, readerVisibleSources } from "@/lib/readerNotes";

/** 读者面收录来源：过滤迁移/批处理词表说明，不靠 CSS 隐藏。 */
export function InclusionSources({
  sources,
  allowOps = false,
}: {
  sources: Source[] | undefined;
  allowOps?: boolean;
}) {
  const visible = readerVisibleSources(sources, { allowOps });
  return <>{visible.map((source, i) => {
    const label = allowOps ? (source.citation || source.url || "") : readerSourceLabel(source);
    if (!label && !source.url) return null;
    return source.url ? (
      <a key={i} className="block text-[11px] text-text-faint hover:text-primary" href={source.url} target="_blank" rel="noreferrer">{label || source.url}</a>
    ) : (
      <span key={i} className="block text-[11px] text-text-faint">{label}</span>
    );
  })}</>;
}
import { GroupAttributeInline, LocatorInline } from "./TemplateAttributeSections";

/** 载体与轨道详情统一展示收录表达、选段与本版定位。 */
export function InclusionContents({
  contents,
  expressions,
  loading = false,
}: {
  contents: Entity["contents"] | undefined;
  expressions: Record<string, Entity>;
  loading?: boolean;
}) {
  const { locale, t } = useI18n();
  const { definitions } = useDefinitions();
  if (!contents?.length) return null;
  return (
    <ul className="m-0 list-none space-y-1.5 pl-0 text-xs">
      {[...contents].sort((a, b) => a.position - b.position).map((content, index) => {
        const expression = expressions[content.expression_id];
        return (
          <li key={`${content.expression_id}-${index}`} className="space-y-0.5">
            {expression ? (
              <Link href={`/catalog/${expression.id || content.expression_id}`} className="text-primary hover:underline">
                {title(expression, locale)}
              </Link>
            ) : (
              <span className="text-text-muted">{t(loading ? "catalog.entityReference" : "catalog.referenceUnknown")}</span>
            )}
            <div className="flex flex-wrap gap-x-3 gap-y-0.5 text-[11px]">
              <LocatorInline defs={definitions} value={content.locator} locale={locale} />
              <GroupAttributeInline defs={definitions} code="inclusion_attributes" value={content.attributes} locale={locale} />
            </div>
            <InclusionSources sources={content.sources}/>
          </li>
        );
      })}
    </ul>
  );
}
