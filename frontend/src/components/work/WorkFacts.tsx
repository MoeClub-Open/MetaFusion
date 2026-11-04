"use client";

import React, { useMemo } from "react";
import { useI18n } from "@/i18n/I18nProvider";
import { DynamicDefinitions, resolveLocalizedName, getFieldName, getTermName, templatesForEntity } from "@/lib/definitions";
import { FieldValue } from "@/components/catalog/TemplateAttributeSections";

const NO_EXCLUDED_FIELDS: string[] = [];

type FactEntity = { kind?: string; attributes?: Record<string, any> | null };

function collectFactSections(
  entity: FactEntity | null,
  defs: DynamicDefinitions | null | undefined,
  excludeFields: string[],
) {
  const attrs = entity?.attributes || {};
  const visible = (code: string) => {
    if (excludeFields.includes(code)) return false;
    const v = attrs[code];
    if (v === undefined || v === null || v === "") return false;
    // 存档字段不进信息面板（页面上有专用区块呈现）。
    if (defs?.fields?.[code]?.hidden) return false;
    return true;
  };
  const templates = templatesForEntity(defs, entity?.kind || "", attrs);
  const seen = new Set<string>();
  const sections: { names: Record<string, string>; fields: string[] }[] = [];
  for (const tpl of templates) {
    for (const sec of tpl.sections || []) {
      const fields = (sec.fields || []).filter((f: string) => !seen.has(f) && visible(f));
      if (!fields.length) continue;
      fields.forEach((f: string) => seen.add(f));
      sections.push({ names: sec.names || {}, fields });
    }
  }
  const restFields = Object.keys(attrs).filter((k) => !seen.has(k) && visible(k));
  return { sections, restFields };
}

export function entityHasFacts(
  entity: FactEntity | null,
  defs: DynamicDefinitions | null | undefined,
): boolean {
  const { sections, restFields } = collectFactSections(entity, defs, NO_EXCLUDED_FIELDS);
  return sections.length + restFields.length > 0;
}

// WorkFacts：作品/实体的信息面板，按 kind 与已存字段匹配模板分区渲染。
// 作品专用布局与通用布局共用同一实现，避免字段集合与
// 展示次序各写一份而漂移。分区、字段、次序全部来自服务端 definitions，
// 代码不写死任何字段码；hidden 字段（如资料表原始条目）不进面板但仍可检索。
export function WorkFacts({
  entity,
  defs,
  locale,
  className = "",
  excludeFields = NO_EXCLUDED_FIELDS,
  stacked = false,
}: {
  entity: FactEntity | null;
  defs: DynamicDefinitions | null | undefined;
  locale: string;
  className?: string;
  excludeFields?: string[];
  /** 窄侧栏用标签在上、值在下。宽栏保持右对齐栅格。 */
  stacked?: boolean;
}) {
  const { t } = useI18n();

  const { sections, restFields } = useMemo(
    () => collectFactSections(entity, defs, excludeFields),
    [entity, defs, excludeFields],
  );

  if (!defs) return null;
  if (!sections.length && !restFields.length) return null;

  return (
    <div className={`space-y-3 ${className}`}>
      {sections.map((sec, i) => (
        <Section key={`s${i}`} title={resolveLocalizedName(sec.names, locale, "")} stacked={stacked}>
          {sec.fields.map((code) => (
            <Row key={code} label={getFieldName(defs, code, locale)} stacked={stacked}>
              <FieldValue code={code} value={(entity?.attributes || {})[code]} defs={defs} locale={locale} />
            </Row>
          ))}
        </Section>
      ))}
      {restFields.length > 0 && (
        <Section title={sections.length ? t("catalog.attributes") : ""} stacked={stacked}>
          {restFields.map((code) => (
            <Row key={code} label={getFieldName(defs, code, locale) || code} stacked={stacked}>
              <FieldValue code={code} value={(entity?.attributes || {})[code]} defs={defs} locale={locale} />
            </Row>
          ))}
        </Section>
      )}
    </div>
  );
}

function Section({ title, stacked = false, children }: { title: string; stacked?: boolean; children: React.ReactNode }) {
  return (
    <section>
      {title && (
        <h3 className="text-[11px] font-mono uppercase tracking-wider text-text-muted pb-1.5 mb-2 border-b border-line-subtle">
          {title}
        </h3>
      )}
      {/* 宽栏：标签右对齐。窄侧栏：标签在上，避免 220px 里两列互挤。 */}
      <dl className={stacked ? "space-y-2 text-xs" : "grid grid-cols-[minmax(72px,auto)_minmax(0,1fr)] gap-x-3 gap-y-1.5 text-xs items-baseline"}>
        {children}
      </dl>
    </section>
  );
}

function Row({ label, stacked = false, children }: { label: string; stacked?: boolean; children: React.ReactNode }) {
  if (stacked) {
    return (
      <div>
        <dt className="text-text-muted font-mono text-[11px] mb-0.5">{label}</dt>
        <dd className="text-text-strong leading-5 break-words min-w-0">{children}</dd>
      </div>
    );
  }
  return (
    <>
      <dt className="text-text-muted text-[11px] leading-5 text-right">{label}</dt>
      <dd className="text-text-strong leading-5 break-words min-w-0">{children}</dd>
    </>
  );
}

export default WorkFacts;

// entityBadges 按模板声明计算标题旁的徽章：主日期字段 + badge_fields。
// 字段码来自服务端模板，两个详情页共用，避免各自写死 edition_date/format。
export function entityBadges(
  entity: { kind?: string; attributes?: Record<string, any> | null } | null,
  defs: DynamicDefinitions | null | undefined,
  locale: string,
): { kind: "date" | "field"; text: string }[] {
  const attrs: Record<string, any> = entity?.attributes || {};
  const templates = templatesForEntity(defs, entity?.kind || "", attrs);
  const out: { kind: "date" | "field"; text: string }[] = [];
  const dateField = templates.map((tp) => tp.primary_date_field).find(Boolean) as string | undefined;
  if (dateField && attrs[dateField] && !defs?.fields?.[dateField]?.hidden) {
    out.push({ kind: "date", text: String(attrs[dateField]) });
  }
  const badgeFields: string[] = Array.from(new Set(templates.flatMap((tp) => tp.badge_fields || [])));
  for (const code of badgeFields) {
    if (!attrs[code] || defs?.fields?.[code]?.hidden) continue;
    const def: any = defs?.fields?.[code];
    const text =
      def?.type === "enum" && def?.vocabulary
        ? getTermName(defs, def.vocabulary, String(attrs[code]), locale)
        : String(attrs[code]);
    out.push({ kind: "field", text });
  }
  return out;
}
